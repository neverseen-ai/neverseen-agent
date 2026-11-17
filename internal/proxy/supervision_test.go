package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	contract "github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
)

// supervisionBackend stands in for a supervision backend: it takes one token, refuses
// every other as the real one does, and keeps the heartbeats it is sent.
type supervisionBackend struct {
	*httptest.Server

	mu         sync.Mutex
	heartbeats []contract.Heartbeat
}

const goodToken = "nse_the-one-token-this-backend-takes"

func newSupervisionBackend(t *testing.T) *supervisionBackend {
	t.Helper()
	b := &supervisionBackend{}
	b.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/v1/enrol":
			var req contract.EnrolRequest
			if json.Unmarshal(body, &req) != nil || req.Token != goodToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(contract.EnrolResponse{
				Schema: contract.SchemaVersion, AgentID: "agt_page", Key: "0123456789abcdef",
			})
		case "/v1/heartbeats":
			var batch contract.HeartbeatBatch
			if json.Unmarshal(body, &batch) == nil {
				b.mu.Lock()
				b.heartbeats = append(b.heartbeats, batch.Windows()...)
				b.mu.Unlock()
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(b.Close)
	return b
}

func (b *supervisionBackend) received() []contract.Heartbeat {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]contract.Heartbeat(nil), b.heartbeats...)
}

// unsupervisedAgent assembles an agent the way the command does, with no backend
// configured and every file it keeps under home.
func unsupervisedAgent(t *testing.T, home string, logged io.Writer) *Agent {
	t.Helper()
	t.Setenv("NEVERSEEN_PII_LOCALE", "fr")
	t.Setenv("NEVERSEEN_BACKEND_URL", "")
	t.Setenv("NEVERSEEN_ENROLMENT_TOKEN", "")
	t.Setenv("NEVERSEEN_IDENTITY_FILE", filepath.Join(home, "agent.json"))

	agent, err := FromEnv(slog.New(slog.NewTextHandler(logged, nil)), Options{
		ControlKeyFile: filepath.Join(home, "control.key"),
		PolicyFile:     filepath.Join(home, "policy.json"),
		NamesFile:      NoFile,
	})
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	return agent
}

// enrolThrough sends POST /enrol as the settings page does.
func enrolThrough(t *testing.T, agent *Agent, backend, token string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(enrolRequest{Backend: backend, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/enrol", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:52341"
	req.Host = "localhost:9787"
	req.Header.Set(controlHeader, agent.Server.controlKey)

	rec := httptest.NewRecorder()
	agent.Server.Handler().ServeHTTP(rec, req)
	return rec
}

// The whole of what the settings page's enrolment is for: an agent that started with
// no backend is enrolled without a restart, reports at once, says so on the page, and
// reports again after a restart with nothing in its environment — while a token the
// backend refuses leaves it exactly as unsupervised as it was.
func TestTheSettingsPageEnrolsARunningAgent(t *testing.T) {
	backend := newSupervisionBackend(t)
	home := t.TempDir()
	var logged bytes.Buffer
	agent := unsupervisedAgent(t, home, &logged)
	if agent.Reporter != nil {
		t.Fatal("a reporter was built with no backend configured")
	}

	ctx, cancel := context.WithCancel(t.Context())
	supervised := make(chan struct{})
	go func() { defer close(supervised); agent.Supervise(ctx) }()

	// Counted before anybody enrolled: it must not be in the first report.
	agent.Server.recorder.Request("default", "", "")

	for _, refused := range []struct {
		name, backend, token string
		want                 int
	}{
		{"a token the backend does not take", backend.URL, "nse_spent", http.StatusUnprocessableEntity},
		{"no token", backend.URL, " ", http.StatusBadRequest},
		{"clear HTTP to another machine", "http://supervision.example.invalid", goodToken, http.StatusUnprocessableEntity},
		{"not an address", "backend", goodToken, http.StatusUnprocessableEntity},
	} {
		if got := enrolThrough(t, agent, refused.backend, refused.token); got.Code != refused.want {
			t.Errorf("%s: answered %d, want %d — %s", refused.name, got.Code, refused.want, got.Body)
		}
	}
	if state := agent.Server.supervision.state(); state.Backend != "" || state.AgentID != "" {
		t.Errorf("a refused enrolment left the agent supervised: %+v", state)
	}
	if state := agent.Server.supervision.state(); state.Offered != DefaultBackendURL {
		t.Errorf("the form is offered %q, want the default backend", state.Offered)
	}
	for _, file := range []string{"agent.json", "supervision.json"} {
		if _, err := os.Stat(filepath.Join(home, file)); !os.IsNotExist(err) {
			t.Errorf("%s exists after nothing but refusals: %v", file, err)
		}
	}

	// A trailing slash is what a pasted address carries.
	got := enrolThrough(t, agent, backend.URL+"/", "  "+goodToken+"\n")
	if got.Code != http.StatusOK {
		t.Fatalf("the enrolment answered %d: %s", got.Code, got.Body)
	}
	var state supervisionState
	if err := json.Unmarshal(got.Body.Bytes(), &state); err != nil {
		t.Fatalf("the answer does not parse: %v — %s", err, got.Body)
	}
	if state.AgentID != "agt_page" || state.Backend != backend.URL {
		t.Errorf("the answer says %+v, want agt_page at %s", state, backend.URL)
	}

	deadline := time.Now().Add(10 * time.Second)
	for len(backend.received()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no heartbeat reached the backend: the reporter was not started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if first := backend.received()[0]; first.Counters.Requests != 0 {
		t.Errorf("the first report carries %d request(s) counted before the enrolment", first.Counters.Requests)
	}

	// The page reads the state from /today, with everything else about the agent.
	deadline = time.Now().Add(5 * time.Second)
	for agent.Server.agentNow().Supervision.DeliveredAt == nil {
		if time.Now().After(deadline) {
			t.Fatal("/today never said a report was delivered")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if got := enrolThrough(t, agent, backend.URL, goodToken); got.Code != http.StatusConflict {
		t.Errorf("a second enrolment answered %d, want 409 — %s", got.Code, got.Body)
	}

	cancel()
	select {
	case <-supervised:
	case <-time.After(30 * time.Second):
		t.Fatal("Supervise did not return once its context was cancelled")
	}

	if strings.Contains(logged.String(), goodToken) || strings.Contains(logged.String(), "nse_spent") {
		t.Errorf("an enrolment token was logged:\n%s", logged.String())
	}
	for _, file := range []string{"agent.json", "supervision.json"} {
		raw, err := os.ReadFile(filepath.Join(home, file))
		if err != nil {
			t.Fatalf("%s was not kept: %v", file, err)
		}
		if strings.Contains(string(raw), goodToken) {
			t.Errorf("%s holds the enrolment token", file)
		}
	}

	// The next start has nothing in its environment and reports all the same.
	restarted := unsupervisedAgent(t, home, io.Discard)
	if restarted.Reporter == nil {
		t.Fatal("after a restart the agent no longer reports: the backend was not kept")
	}
	if state := restarted.Server.supervision.state(); state.Backend != backend.URL {
		t.Errorf("after a restart the agent reports to %q, want %s", state.Backend, backend.URL)
	}
}

// A backend set by the environment is where the reporter in place sends: an identity
// issued anywhere else would be refused there on every heartbeat, so the enrolment is.
func TestAnEnrolmentElsewhereThanTheConfiguredBackendIsRefused(t *testing.T) {
	backend := newSupervisionBackend(t)
	other := newSupervisionBackend(t)
	home := t.TempDir()
	t.Setenv("NEVERSEEN_PII_LOCALE", "fr")
	t.Setenv("NEVERSEEN_BACKEND_URL", backend.URL)
	t.Setenv("NEVERSEEN_ENROLMENT_TOKEN", "")
	t.Setenv("NEVERSEEN_IDENTITY_FILE", filepath.Join(home, "agent.json"))

	agent, err := FromEnv(nil, Options{
		ControlKeyFile: filepath.Join(home, "control.key"), PolicyFile: filepath.Join(home, "policy.json"), NamesFile: NoFile,
	})
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if state := agent.Server.supervision.state(); state.Offered != backend.URL {
		t.Errorf("the form is offered %q, want the configured backend", state.Offered)
	}

	if got := enrolThrough(t, agent, other.URL, goodToken); got.Code != http.StatusConflict {
		t.Fatalf("an enrolment elsewhere answered %d, want 409 — %s", got.Code, got.Body)
	}
	if _, err := os.Stat(filepath.Join(home, "agent.json")); !os.IsNotExist(err) {
		t.Errorf("an identity was issued by a backend this agent does not report to: %v", err)
	}

	if got := enrolThrough(t, agent, backend.URL, goodToken); got.Code != http.StatusOK {
		t.Fatalf("an enrolment at the configured backend answered %d — %s", got.Code, got.Body)
	}
	// The environment says where; nothing is stored that could come to disagree.
	if _, err := os.Stat(filepath.Join(home, "supervision.json")); !os.IsNotExist(err) {
		t.Errorf("supervision.json was written for a backend the environment sets: %v", err)
	}
}

// reportThrough calls /report as the settings page does.
func reportThrough(t *testing.T, agent *Agent, method string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, "/report", nil)
	req.RemoteAddr = "127.0.0.1:52341"
	req.Host = "localhost:9787"
	req.Header.Set(controlHeader, agent.Server.controlKey)

	rec := httptest.NewRecorder()
	agent.Server.Handler().ServeHTTP(rec, req)
	return rec
}

// "Counts, never content" is a claim the settings page lets its reader check: GET
// /report is the next report as it stands, in the shape a heartbeat carries, and
// looking at it must not close the window it shows — or every look would cut a
// five-minute bucket in two. POST sends what waits, and has nothing to send for an
// agent that reports nowhere.
func TestTheNextReportIsShownWithoutBeingTaken(t *testing.T) {
	backend := newSupervisionBackend(t)
	agent := unsupervisedAgent(t, t.TempDir(), io.Discard)

	ctx, cancel := context.WithCancel(t.Context())
	supervised := make(chan struct{})
	go func() { defer close(supervised); agent.Supervise(ctx) }()
	defer func() { cancel(); <-supervised }()

	shown := func() contract.HeartbeatBatch {
		t.Helper()
		got := reportThrough(t, agent, http.MethodGet)
		if got.Code != http.StatusOK {
			t.Fatalf("GET /report answered %d: %s", got.Code, got.Body)
		}
		var batch contract.HeartbeatBatch
		if err := json.Unmarshal(got.Body.Bytes(), &batch); err != nil {
			t.Fatalf("the next report does not parse as a heartbeat batch: %v — %s", err, got.Body)
		}
		if len(batch.Buckets) != 1 {
			t.Fatalf("the next report holds %d buckets, want the one in progress", len(batch.Buckets))
		}
		return batch
	}

	// Shown before anybody enrolled: that is when somebody decides whether to.
	agent.Server.recorder.Request("default", "", "")
	if got := shown(); got.Buckets[0].Counters.Requests != 1 || got.AgentID != "" {
		t.Errorf("unenrolled, the next report shows %d request(s) as %q", got.Buckets[0].Counters.Requests, got.AgentID)
	}
	if got := reportThrough(t, agent, http.MethodPost); got.Code != http.StatusConflict {
		t.Errorf("sending now with no backend answered %d, want 409 — %s", got.Code, got.Body)
	}

	if got := enrolThrough(t, agent, backend.URL, goodToken); got.Code != http.StatusOK {
		t.Fatalf("the enrolment answered %d: %s", got.Code, got.Body)
	}
	// An enrolment has Run report at once, and that report closes the window. Counted
	// before it left, the two requests below went with it and the look showed none —
	// on Linux every time, where the delivery wins the race, and never on macOS.
	deadline := time.Now().Add(5 * time.Second)
	for agent.Server.agentNow().Supervision.DeliveredAt == nil {
		if time.Now().After(deadline) {
			t.Fatal("the report an enrolment sends at once never left")
		}
		time.Sleep(20 * time.Millisecond)
	}
	agent.Server.recorder.Request("default", "", "")
	agent.Server.recorder.Request("default", "", "")
	for range 2 { // twice: the second look sees what the first left
		if got := shown(); got.Buckets[0].Counters.Requests != 2 || got.AgentID != "agt_page" {
			t.Errorf("the next report shows %d request(s) as %q, want 2 as agt_page",
				got.Buckets[0].Counters.Requests, got.AgentID)
		}
	}

	got := reportThrough(t, agent, http.MethodPost)
	if got.Code != http.StatusOK {
		t.Fatalf("POST /report answered %d: %s", got.Code, got.Body)
	}
	var state supervisionState
	if err := json.Unmarshal(got.Body.Bytes(), &state); err != nil || state.AgentID != "agt_page" {
		t.Errorf("sending now answered %s (%v), want the supervision state", got.Body, err)
	}
	if state.EnrolledAt == nil {
		t.Error("an enrolled agent does not say since when")
	}
}
