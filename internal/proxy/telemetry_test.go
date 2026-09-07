package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/telemetry"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
	contract "github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
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

// An exchange that masked nothing still cost tokens, and the heartbeat is what
// those counts are for.
//
// unmask returned early on an empty session mapping, so the answer was never read
// and the cost was never reported: a prompt carrying no personal data at all — most
// of them — was invisible to the fleet view. Gating that on `-a` instead would have
// been worse, because two agents on identical traffic would then report different
// totals depending on whether a console was attached. Both halves are asserted here
// at once, buffered and streamed, because the early return was above both.
func TestAnExchangeThatMaskedNothingStillReportsItsCost(t *testing.T) {
	for _, mode := range []struct {
		name  string
		serve http.HandlerFunc
	}{
		{"buffered", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"model":"claude-sonnet-4","content":[{"text":"ok"}],`+
				`"usage":{"input_tokens":1204,"output_tokens":98}}`)
		}},
		{"streamed", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			for _, event := range []string{
				`{"type":"message_start","message":{"model":"claude-sonnet-4","usage":{"input_tokens":1204}}}`,
				`{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}`,
				`{"type":"message_delta","usage":{"output_tokens":98}}`,
			} {
				fmt.Fprintf(w, "data: %s\n\n", event)
				if flusher != nil {
					flusher.Flush()
				}
			}
		}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			up := newUpstream(t, mode.serve)
			agent, recorder := newCountingAgent(t, up, []string{"fr"})

			// Nothing in it any pattern recognises, so the session mints nothing and
			// the mapping stays empty.
			post(t, agent, "/anthropic/v1/messages", "s1", `{"c":"how do I sort a slice"}`)

			counters, _ := recorder.Take(time.Now())
			if len(counters.Masked) != 0 {
				t.Fatalf("masked = %v, want nothing — the case only means something with an empty mapping",
					counters.Masked)
			}

			want := contract.TokenUsage{Input: 1204, Output: 98}
			if got := counters.Models["claude-sonnet-4"]; got != want {
				t.Errorf("usage = %+v, want %+v", got, want)
			}
		})
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
	t.Setenv("NEVERSEEN_PII_LOCALE", "fr")
	t.Setenv("NEVERSEEN_BACKEND_URL", "")

	agent, err := FromEnv(nil, Options{PolicyFile: NoFile})
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
	var health Health
	if err := json.Unmarshal(body, &health); err != nil {
		t.Fatalf("the health line does not parse: %v — %s", err, body)
	}
	if !slices.Equal(health.Locales, []string{"fr"}) {
		t.Errorf("an unsupervised agent is not fully configured: %+v", health)
	}
}

func TestBackendConfiguredMeansAReporter(t *testing.T) {
	t.Setenv("NEVERSEEN_PII_LOCALE", "fr")
	t.Setenv("NEVERSEEN_BACKEND_URL", "https://supervision.example.invalid")
	t.Setenv("NEVERSEEN_ENROLMENT_TOKEN", "enrol-me")
	t.Setenv("NEVERSEEN_IDENTITY_FILE", t.TempDir()+"/agent.json")

	agent, err := FromEnv(nil, Options{PolicyFile: NoFile})
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if agent.Reporter == nil {
		t.Error("no reporter was built although a backend is configured")
	}
}

// A supervision backend has to be able to see that an agent is masking less than it
// was configured to. Without these two fields a fleet view reading only Locales
// would show an agent as configured and green while it sent email addresses to a
// provider in clear.
func TestTheReportedStateCarriesWhatIsSwitchedOff(t *testing.T) {
	up := newUpstream(t, echoJSON)
	det := detector.New(detector.Config{Locales: []string{"fr"}})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{Providers: []Provider{{Code: "anthropic", BaseURL: up.server.URL}}}, det, v)
	if err != nil {
		t.Fatal(err)
	}

	if got := srv.State(); got.Masking != "full" || got.SwitchedOff != nil {
		t.Errorf("a whole catalogue reports masking=%q switched_off=%v", got.Masking, got.SwitchedOff)
	}

	if err := det.Disable([]pii.Category{pii.CatIPAddr, pii.CatDOB}); err != nil {
		t.Fatal(err)
	}

	// Read at the moment of the report rather than cached at start-up, which is the
	// only field in State that changes while the process runs.
	state := srv.State()
	if state.Masking != "partial" {
		t.Errorf("masking=%q, want partial", state.Masking)
	}
	if len(state.SwitchedOff) != 2 || state.SwitchedOff[0] != "DOB" || state.SwitchedOff[1] != "IP_ADDRESS" {
		t.Errorf("switched_off=%v, want the two categories in catalogue order", state.SwitchedOff)
	}
}

// An exchange says more than how many and how much: which client sent it, which
// vendor it went to, how the vendor answered, and which conversation it belongs
// to. Each is a count or a word from a closed list, and each has to be fed by the
// request path or the dashboard column is quietly always empty.
func TestAnExchangeCountsItsClientProviderAnswerAndSession(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, recorder := newCountingAgent(t, up, nil)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		agent.URL+"/anthropic/v1/messages", strings.NewReader(`{"c":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-cli/1.0.83 (external, cli)")
	req.Header.Set("X-Session-Id", "conv-1")
	do(t, agent, req)
	post(t, agent, "/anthropic/v1/messages", "conv-1", `{"c":"again"}`)
	post(t, agent, "/anthropic/v1/messages", "conv-2", `{"c":"another"}`)

	c, _ := recorder.Take(time.Now())
	if c.Clients["claude-code"] != 1 || c.Clients[contract.Other] != 2 {
		t.Errorf("clients = %v, want claude-code:1 other:2 — Go's default User-Agent is nobody", c.Clients)
	}
	if c.Providers["anthropic"] != 3 {
		t.Errorf("providers = %v, want anthropic:3", c.Providers)
	}
	if c.Upstream.OK != 3 {
		t.Errorf("upstream = %+v, want 3 ok", c.Upstream)
	}
	if c.Sessions.Active != 2 || c.Sessions.Opened != 2 {
		t.Errorf("sessions = %+v, want 2 active and 2 opened", c.Sessions)
	}
}

// A header-less Claude Code client names its conversation in metadata.user_id, and
// that is what the session counts key on — on the way to Anthropic, whose
// identifier it is. Two conversations without headers are otherwise one session,
// and "tokens per conversation" is then tokens per day.
func TestClaudeCodeConversationsAreCountedApart(t *testing.T) {
	up := newUpstream(t, echoJSON)
	anthropicIs(t, up)
	agent, recorder := newCountingAgent(t, up, nil)

	for _, id := range []string{"18af0b2c-6b1e-4d8f-9a1b-2c3d4e5f6a7b", "28af0b2c-6b1e-4d8f-9a1b-2c3d4e5f6a7b", "18af0b2c-6b1e-4d8f-9a1b-2c3d4e5f6a7b"} {
		post(t, agent, "/anthropic/v1/messages", "",
			`{"metadata":{"user_id":"{\"device_id\":\"5a1c\",\"session_id\":\"`+id+`\"}"},"c":"hi"}`)
	}
	// And one with no metadata at all, which is the shared default session.
	post(t, agent, "/anthropic/v1/messages", "", `{"c":"hi"}`)

	c, _ := recorder.Take(time.Now())
	if c.Sessions.Active != 3 {
		t.Errorf("sessions active = %d, want 3: two conversations and the default", c.Sessions.Active)
	}
}

// The same document on the way to another vendor earns nothing: it is Anthropic's
// identifier, and elsewhere it is a value in a prompt.
func TestAnotherVendorsRequestsShareTheDefaultSession(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, recorder := newCountingAgent(t, up, nil) // no anthropicIs: the upstream is not Anthropic

	for _, id := range []string{"18af0b2c-6b1e-4d8f-9a1b-2c3d4e5f6a7b", "28af0b2c-6b1e-4d8f-9a1b-2c3d4e5f6a7b"} {
		post(t, agent, "/anthropic/v1/messages", "",
			`{"metadata":{"user_id":"{\"session_id\":\"`+id+`\"}"},"c":"hi"}`)
	}
	if c, _ := recorder.Take(time.Now()); c.Sessions.Active != 1 {
		t.Errorf("sessions active = %d, want 1", c.Sessions.Active)
	}
}

// The fail-closed refusal is counted as what it is — not a request, a refusal.
func TestAFailClosedRefusalIsCounted(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, recorder := newCountingAgent(t, up, nil)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		agent.URL+"/anthropic/v1/messages", strings.NewReader("whatever this is"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Encoding", "br")
	if got := do(t, agent, req); got.status != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d, want 415", got.status)
	}

	c, _ := recorder.Take(time.Now())
	if c.Refused != 1 || c.Requests != 0 {
		t.Errorf("refused = %d requests = %d, want 1 and 0", c.Refused, c.Requests)
	}
}

func TestHowTheProviderAnsweredIsCounted(t *testing.T) {
	status := http.StatusOK
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, `{"error":"x"}`)
	})
	agent, recorder := newCountingAgent(t, up, nil)

	for _, s := range []int{200, 429, 401, 503} {
		status = s
		post(t, agent, "/anthropic/v1/messages", "s", `{"c":"hi"}`)
	}
	// And one to a provider nothing listens on.
	det := detector.New(detector.Config{})
	v, _ := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	dark, err := New(Config{Providers: []Provider{{Code: "anthropic", BaseURL: "http://127.0.0.1:9"}}, Recorder: recorder}, det, v)
	if err != nil {
		t.Fatal(err)
	}
	darkAgent := httptest.NewServer(dark.Handler())
	t.Cleanup(darkAgent.Close)
	if got := post(t, darkAgent, "/anthropic/v1/messages", "s", `{"c":"hi"}`); got.status != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", got.status)
	}

	c, _ := recorder.Take(time.Now())
	want := contract.Upstream{OK: 1, RateLimited: 1, Rejected: 1, Failed: 1, Unreachable: 1}
	if c.Upstream != want {
		t.Errorf("upstream = %+v, want %+v", c.Upstream, want)
	}
}

// A tool call is counted with what it is a call to, what it runs, and whether a
// value the agent masked on the way out was put back into it — the number of times
// personal data reached an action on the workstation. Streamed and buffered both,
// because the two paths find their tool calls in different machinery.
func TestAToolCallIsCountedWithItsRestoredValue(t *testing.T) {
	streamed := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"message_start","message":{"model":"claude-sonnet-4","usage":{"input_tokens":12}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"Bash","input":{}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"grep -rn [EMA"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"IL_1] notes/ | sort\"}"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","name":"Read","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"notes/a.md\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","usage":{"output_tokens":9}}`,
		} {
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", event)
			w.(http.Flusher).Flush()
		}
	}
	buffered := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"claude-sonnet-4","content":[
			{"type":"tool_use","name":"Bash","input":{"command":"grep -rn [EMAIL_1] notes/ | sort"}},
			{"type":"tool_use","name":"Read","input":{"file_path":"notes/a.md"}}],
			"usage":{"input_tokens":12,"output_tokens":9}}`)
	}
	for name, handler := range map[string]http.HandlerFunc{"streamed": streamed, "buffered": buffered} {
		t.Run(name, func(t *testing.T) {
			up := newUpstream(t, handler)
			agent, recorder := newCountingAgent(t, up, []string{"fr"})

			post(t, agent, "/anthropic/v1/messages", "s1", `{"c":"cherche claire@example.fr dans mes notes"}`)

			c, _ := recorder.Take(time.Now())
			tools := c.Tools
			if tools.Calls != 2 || tools.Restored != 1 {
				t.Errorf("calls = %d restored = %d, want 2 and 1", tools.Calls, tools.Restored)
			}
			if tools.Names["Bash"] != 1 || tools.Names["Read"] != 1 {
				t.Errorf("names = %v, want Bash:1 Read:1", tools.Names)
			}
			if tools.Programs["grep"] != 1 || tools.Programs["sort"] != 1 || len(tools.Programs) != 2 {
				t.Errorf("programs = %v, want grep:1 sort:1", tools.Programs)
			}
			if c.Degraded != 0 {
				t.Errorf("degraded = %d for arguments that formed a whole document", c.Degraded)
			}
		})
	}
}

// A stream cut off before a tool call's arguments are whole is the second-best
// path, and it is counted rather than passed off as the first.
func TestATruncatedToolCallIsCountedAsDegraded(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"Bash","input":{}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"grep [EMAIL_1]"}}`,
			`{"type":"content_block_stop","index":0}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", event)
			w.(http.Flusher).Flush()
		}
	})
	agent, recorder := newCountingAgent(t, up, []string{"fr"})
	post(t, agent, "/anthropic/v1/messages", "s1", `{"c":"cherche claire@example.fr"}`)

	c, _ := recorder.Take(time.Now())
	if c.Degraded != 1 || c.Tools.Calls != 1 {
		t.Errorf("degraded = %d calls = %d, want 1 and 1", c.Degraded, c.Tools.Calls)
	}
}

func TestAPolicyChangeIsCounted(t *testing.T) {
	up := newUpstream(t, echoJSON)
	det := detector.New(detector.Config{Locales: []string{"fr"}})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	recorder := telemetry.NewRecorder(time.Now())
	srv, err := New(Config{
		Providers:  []Provider{{Code: "anthropic", BaseURL: up.server.URL}},
		ControlKey: testControlKey,
		Recorder:   recorder,
	}, det, v)
	if err != nil {
		t.Fatal(err)
	}
	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)

	putPolicy(t, agent, testControlKey, `{"off":["IP_ADDRESS"],"substitution":"token","secret_level":"weak","locales":["fr"]}`)
	putPolicy(t, agent, testControlKey, `{"off":["SECRET_OPENAI_KEY"],"substitution":"token","secret_level":"weak","locales":["fr"]}`)
	putPolicy(t, agent, "not-the-key", `{"off":[],"substitution":"token","secret_level":"weak","locales":["fr"]}`)

	c, _ := recorder.Take(time.Now())
	// The unauthenticated attempt is not a policy request at all: it never reached
	// the applier, and counting it would let anybody on the network write to this
	// counter.
	if c.Policy != (contract.PolicyChanges{Applied: 1, Refused: 1}) {
		t.Errorf("policy = %+v, want 1 applied (IP_ADDRESS off), 1 refused (a credential)", c.Policy)
	}
}

// What the agent says about its own exposure is what a fleet view has no other
// way to learn: the console printing values in clear, the traces on disk, an
// address beyond loopback, a route pointed away from the vendor.
func TestStateReportsTheAgentsOwnExposure(t *testing.T) {
	up := newUpstream(t, echoJSON)
	det := detector.New(detector.Config{AllowList: map[string]bool{"123456789": true, "10 downing street": true}})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	traces, err := newTracer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	quiet, err := New(Config{Providers: []Provider{{Code: "openai", BaseURL: "https://api.openai.com"}}}, det, v)
	if err != nil {
		t.Fatal(err)
	}
	state := quiet.State()
	if state.Console || state.Tracing || state.Exposed || state.Rerouted != nil {
		t.Errorf("a quiet agent on loopback with the vendor's own route reports exposure: %+v", state)
	}
	if state.Allowlisted != 2 {
		t.Errorf("allowlisted = %d, want 2", state.Allowlisted)
	}

	loud, err := New(Config{
		Providers: []Provider{
			{Code: "anthropic", BaseURL: up.server.URL}, // a default code, pointed at a gateway
			{Code: "openai", BaseURL: "https://api.openai.com"},
			{Code: "internal", BaseURL: "https://llm.internal"}, // a code the agent does not know
		},
		Audit:  io.Discard,
		Traces: traces,
		Listen: ":8787",
	}, det, v)
	if err != nil {
		t.Fatal(err)
	}
	state = loud.State()
	if !state.Console || !state.Tracing || !state.Exposed {
		t.Errorf("console=%v tracing=%v exposed=%v, want all three", state.Console, state.Tracing, state.Exposed)
	}
	if want := []string{"anthropic", "internal"}; !slices.Equal(state.Rerouted, want) {
		t.Errorf("rerouted = %v, want %v", state.Rerouted, want)
	}
}
