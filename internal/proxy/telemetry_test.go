package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/telemetry"
	"github.com/cloakfleet/cloakfleet/internal/vault"
	contract "github.com/cloakfleet/cloakfleet/pkg/telemetry"
)

// The counters are only worth anything if the request path actually feeds them.
// A recorder that works perfectly and is never called looks exactly like one that
// is — the dashboard is simply always empty, which reads as "nobody uses the AI
// tools" rather than as a bug.

// newCountingAgent builds a proxy whose recorder the test holds.
func newCountingAgent(t *testing.T, up *upstream, locales []string) (*httptest.Server, *telemetry.Recorder) {
	t.Helper()

	det := detector.New(detector.Config{Locales: locales})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}

	recorder := telemetry.NewRecorder(time.Now())
	srv, err := New(Config{
		Providers: []Provider{{Code: "anthropic", BaseURL: up.server.URL}},
		Recorder:  recorder,
	}, det, v)
	if err != nil {
		t.Fatal(err)
	}

	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)
	return agent, recorder
}

func TestExchangeFeedsTheCounters(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"claude-sonnet-4","content":[{"text":"ok"}],
		                "usage":{"input_tokens":1204,"output_tokens":98,
		                         "cache_read_input_tokens":184302}}`)
	})
	agent, recorder := newCountingAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "s1",
		`{"c":"Écrire à claire@example.fr et à paul@example.fr, NIR 184037511600176"}`)

	counters, _ := recorder.Take(time.Now())

	if counters.Requests != 1 {
		t.Errorf("requests = %d, want 1", counters.Requests)
	}
	if counters.Masked["EMAIL"] != 2 || counters.Masked["NIR"] != 1 {
		t.Errorf("masked = %v, want EMAIL:2 NIR:1", counters.Masked)
	}

	// The cost, read out of the provider's own answer, and with the cache count
	// kept apart from the fresh input because the two are priced differently.
	want := contract.TokenUsage{Input: 1204, Output: 98, CacheRead: 184302}
	if got := counters.Models["claude-sonnet-4"]; got != want {
		t.Errorf("usage = %+v, want %+v", got, want)
	}
}

// A streamed answer reports its cost too, assembled from the events it arrived
// in: the model in the first, the output count in the last.
func TestStreamedExchangeFeedsTheCounters(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		for _, event := range []string{
			`{"type":"message_start","message":{"model":"claude-sonnet-4","usage":{"input_tokens":12,"cache_read_input_tokens":9000}}}`,
			`{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"message_delta","usage":{"output_tokens":98}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", event)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	agent, recorder := newCountingAgent(t, up, nil)

	// Read to the end, which is when a stream knows what it cost.
	post(t, agent, "/anthropic/v1/messages", "s1", `{"c":"write to claire@example.fr"}`)

	counters, _ := recorder.Take(time.Now())

	want := contract.TokenUsage{Input: 12, Output: 98, CacheRead: 9000}
	if got := counters.Models["claude-sonnet-4"]; got != want {
		t.Errorf("usage = %+v, want %+v — the counts arrive in different events and have to be "+
			"accumulated across the stream", got, want)
	}
	if counters.Masked["EMAIL"] != 1 {
		t.Errorf("masked = %v, want EMAIL:1", counters.Masked)
	}
}

// A request that never reaches a provider is not an exchange. Counting it would
// have a dashboard showing traffic that never happened.
func TestARefusedRequestIsNotCounted(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, recorder := newCountingAgent(t, up, nil)

	post(t, agent, "/vertex/v1/messages", "s1", `{"c":"write to claire@example.fr"}`)

	if counters, _ := recorder.Take(time.Now()); counters.Requests != 0 {
		t.Errorf("requests = %d, want 0 for a provider that does not exist", counters.Requests)
	}
}

// The test page masks text as a demonstration, and that is not traffic either.
func TestTheTestPageIsNotCounted(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, recorder := newCountingAgent(t, up, []string{"fr"})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, agent.URL+"/test",
		strings.NewReader("text=write+to+claire%40example.fr"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	do(t, agent, req)

	counters, _ := recorder.Take(time.Now())
	if counters.Requests != 0 || len(counters.Masked) != 0 {
		t.Errorf("the test page was counted as traffic: %+v", counters)
	}
}

// What the agent reports about itself has to be what it is applying, not what it
// was handed at startup — an agent with no locale selected masks almost nothing
// while looking perfectly healthy.
func TestStateReportsWhatIsRunning(t *testing.T) {
	up := newUpstream(t, echoJSON)

	det := detector.New(detector.Config{Locales: []string{"fr", "gb"}, Substitution: detector.SubstitutionFake})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{Providers: []Provider{{Code: "anthropic", BaseURL: up.server.URL}}}, det, v)
	if err != nil {
		t.Fatal(err)
	}

	state := srv.State()
	if len(state.Locales) != 2 || state.Locales[0] != "fr" {
		t.Errorf("locales = %v, want the two loaded", state.Locales)
	}
	if state.Substitution != "fake" {
		t.Errorf("substitution = %q, want fake", state.Substitution)
	}
	if len(state.Providers) != 1 || state.Providers[0] != "anthropic" {
		t.Errorf("providers = %v, want the one configured", state.Providers)
	}
	if state.Platform == "" || !strings.Contains(state.Platform, "/") {
		t.Errorf("platform = %q, want an os/arch pair", state.Platform)
	}
	if state.StartedAt.IsZero() {
		t.Error("started_at is zero, so a backend cannot show uptime or spot a restart loop")
	}
}

// An agent with no backend has no reporter at all, rather than one that quietly
// does nothing. It is the free half of the product, and it has to be a whole
// feature rather than a disabled one.
func TestNoBackendMeansNoReporter(t *testing.T) {
	t.Setenv("CLOAKFLEET_PII_LOCALE", "fr")
	t.Setenv("CLOAKFLEET_BACKEND_URL", "")

	agent, err := FromEnv(nil)
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if agent.Reporter != nil {
		t.Error("a reporter was built with no backend configured")
	}

	// And the agent is otherwise complete: it still serves, and still masks.
	server := httptest.NewServer(agent.Server.Handler())
	defer server.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"locales":"fr"`) {
		t.Errorf("an unsupervised agent is not fully configured: %s", body)
	}
}

func TestBackendConfiguredMeansAReporter(t *testing.T) {
	t.Setenv("CLOAKFLEET_PII_LOCALE", "fr")
	t.Setenv("CLOAKFLEET_BACKEND_URL", "https://supervision.example.invalid")
	t.Setenv("CLOAKFLEET_ENROLMENT_TOKEN", "enrol-me")
	t.Setenv("CLOAKFLEET_IDENTITY_FILE", t.TempDir()+"/agent.json")

	agent, err := FromEnv(nil)
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if agent.Reporter == nil {
		t.Error("no reporter was built although a backend is configured")
	}
}
