package telemetry

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cloakfleet/cloakfleet/pkg/pii"
	"github.com/cloakfleet/cloakfleet/pkg/telemetry"
)

// backend is a fake supervision backend that records what reached it and
// verifies the signatures, so the tests assert on what an agent actually sends
// rather than on what it meant to.
type backend struct {
	server *httptest.Server

	mu         sync.Mutex
	heartbeats []telemetry.Heartbeat
	signatures []bool // whether each heartbeat verified against the issued key
	enrolments int
	key        []byte

	// fail, while set, makes every heartbeat fail.
	failing bool
}

func newBackend(t *testing.T) *backend {
	t.Helper()

	b := &backend{key: []byte("0123456789abcdef0123456789abcdef")}
	mux := http.NewServeMux()

	mux.HandleFunc(enrolPath, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var req telemetry.EnrolRequest
		if err := json.Unmarshal(body, &req); err != nil || req.Token == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		b.mu.Lock()
		b.enrolments++
		b.mu.Unlock()

		_ = json.NewEncoder(w).Encode(telemetry.EnrolResponse{
			Schema:  telemetry.SchemaVersion,
			AgentID: "agt_test",
			Key:     hex.EncodeToString(b.key),
		})
	})

	mux.HandleFunc(heartbeatPath, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		b.mu.Lock()
		failing := b.failing
		b.mu.Unlock()
		if failing {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		var hb telemetry.Heartbeat
		if err := json.Unmarshal(body, &hb); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		b.mu.Lock()
		b.heartbeats = append(b.heartbeats, hb)
		b.signatures = append(b.signatures,
			telemetry.VerifySignature(b.key, body, r.Header.Get(telemetry.HeaderSignature)))
		b.mu.Unlock()
	})

	b.server = httptest.NewServer(mux)
	t.Cleanup(b.server.Close)
	return b
}

func (b *backend) received() ([]telemetry.Heartbeat, []bool, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]telemetry.Heartbeat(nil), b.heartbeats...),
		append([]bool(nil), b.signatures...), b.enrolments
}

func (b *backend) setFailing(failing bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failing = failing
}

func newTestReporter(t *testing.T, b *backend, r *Recorder) (*Reporter, string) {
	t.Helper()

	identity := filepath.Join(t.TempDir(), "agent.json")
	rep, err := NewReporter(Config{
		BaseURL:        b.server.URL,
		EnrolmentToken: "enrol-me",
		IdentityFile:   identity,
		Recorder:       r,
		State: func() telemetry.State {
			return telemetry.State{Version: "1.0.0", Locales: []string{"fr"}, Substitution: "token"}
		},
		Interval: time.Hour, // the tests drive reportOnce directly
		Now:      func() time.Time { return epoch },
	})
	if err != nil {
		t.Fatal(err)
	}
	return rep, identity
}

func TestReporterEnrolsOnceThenReports(t *testing.T) {
	b := newBackend(t)
	r := NewRecorder(epoch)
	rep, identity := newTestReporter(t, b, r)

	r.Request()
	r.Masked(map[pii.Category]int{pii.CatEmail: 2})
	rep.reportOnce(t.Context())

	r.Request()
	rep.reportOnce(t.Context())

	got, signatures, enrolments := b.received()

	// Once, not twice. An agent that enrolled on every heartbeat would fill the
	// backend with one agent per five minutes, each counting against whatever the
	// operator is paying for.
	if enrolments != 1 {
		t.Errorf("enrolled %d times, want 1", enrolments)
	}
	if len(got) != 2 {
		t.Fatalf("the backend saw %d heartbeats, want 2", len(got))
	}

	// Every heartbeat is signed with the issued key, so one workstation cannot
	// file reports as another.
	for i, ok := range signatures {
		if !ok {
			t.Errorf("heartbeat %d did not verify against the issued key", i)
		}
	}

	if got[0].Counters.Masked["EMAIL"] != 2 {
		t.Errorf("the first heartbeat carries %v, want EMAIL:2", got[0].Counters.Masked)
	}
	if got[1].Counters.Masked["EMAIL"] != 0 {
		t.Errorf("the second heartbeat repeats the first window's counters: %v", got[1].Counters.Masked)
	}
	if got[0].AgentID != "agt_test" || got[0].Schema != telemetry.SchemaVersion {
		t.Errorf("heartbeat identity or schema is wrong: %+v", got[0])
	}

	// The identity was persisted, readable only by its owner: it holds a signing
	// key, and one that is world-readable on a shared machine is a key anybody
	// can file reports with.
	info, err := os.Stat(identity)
	if err != nil {
		t.Fatalf("the identity was not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the identity file is %o, want 600", perm)
	}
}

// The state is asked for at each heartbeat, so what the backend sees is what the
// agent is applying now — not what it was configured with when it started.
func TestReporterSendsLiveState(t *testing.T) {
	b := newBackend(t)
	r := NewRecorder(epoch)

	locales := []string{"fr"}
	rep, err := NewReporter(Config{
		BaseURL:        b.server.URL,
		EnrolmentToken: "enrol-me",
		IdentityFile:   filepath.Join(t.TempDir(), "agent.json"),
		Recorder:       r,
		State:          func() telemetry.State { return telemetry.State{Locales: locales} },
		Now:            func() time.Time { return epoch },
	})
	if err != nil {
		t.Fatal(err)
	}

	rep.reportOnce(t.Context())
	locales = []string{"fr", "gb", "us"}
	rep.reportOnce(t.Context())

	got, _, _ := b.received()
	if len(got) != 2 {
		t.Fatalf("the backend saw %d heartbeats, want 2", len(got))
	}
	if len(got[1].State.Locales) != 3 {
		t.Errorf("the second heartbeat reports %v, want the locales the agent is applying now",
			got[1].State.Locales)
	}
}

// A failed heartbeat keeps its window for the next attempt.
func TestFailedHeartbeatKeepsItsWindow(t *testing.T) {
	b := newBackend(t)
	r := NewRecorder(epoch)
	rep, _ := newTestReporter(t, b, r)

	r.Request()
	r.Masked(map[pii.Category]int{pii.CatEmail: 3})

	b.setFailing(true)
	rep.reportOnce(t.Context())

	if got, _, _ := b.received(); len(got) != 0 {
		t.Fatalf("the backend accepted a heartbeat it was meant to refuse: %v", got)
	}

	r.Request() // more happens while it is down
	b.setFailing(false)
	rep.reportOnce(t.Context())

	got, _, _ := b.received()
	if len(got) != 1 {
		t.Fatalf("the backend saw %d heartbeats, want 1", len(got))
	}
	if got[0].Counters.Requests != 2 || got[0].Counters.Masked["EMAIL"] != 3 {
		t.Errorf("the retried heartbeat carries %+v, want both windows merged", got[0].Counters)
	}
}

// Past the bound, a window the backend never accepted is abandoned and the loss
// is reported. Unbounded retention would have a laptop offline for a week
// accumulating a week of counters in memory, then filing one heartbeat claiming
// to cover it.
func TestAWindowTooOldIsDroppedAndCounted(t *testing.T) {
	b := newBackend(t)
	r := NewRecorder(epoch)

	now := epoch
	rep, err := NewReporter(Config{
		BaseURL:        b.server.URL,
		EnrolmentToken: "enrol-me",
		IdentityFile:   filepath.Join(t.TempDir(), "agent.json"),
		Recorder:       r,
		State:          func() telemetry.State { return telemetry.State{} },
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	r.Request()
	b.setFailing(true)

	now = epoch.Add(maxWindowAge + time.Minute)
	rep.reportOnce(t.Context())

	b.setFailing(false)
	rep.reportOnce(t.Context())

	got, _, _ := b.received()
	if len(got) != 1 {
		t.Fatalf("the backend saw %d heartbeats, want 1", len(got))
	}
	if got[0].Counters.Dropped != 1 {
		t.Errorf("dropped = %d, want 1: a lost window has to be visible, because a silent one "+
			"looks exactly like a quiet period", got[0].Counters.Dropped)
	}
	if got[0].Counters.Requests != 0 {
		t.Errorf("requests = %d, want 0: the abandoned window's counters are gone, not carried",
			got[0].Counters.Requests)
	}
}

// An unreachable backend must not stop anything. There is no caller who could
// act on the error, and the agent's job is masking.
func TestAnUnreachableBackendIsSurvivable(t *testing.T) {
	r := NewRecorder(epoch)
	rep, err := NewReporter(Config{
		BaseURL:        "http://127.0.0.1:1", // nothing listens there
		EnrolmentToken: "enrol-me",
		IdentityFile:   filepath.Join(t.TempDir(), "agent.json"),
		Recorder:       r,
		State:          func() telemetry.State { return telemetry.State{} },
		Client:         &http.Client{Timeout: 100 * time.Millisecond},
		Now:            func() time.Time { return epoch },
	})
	if err != nil {
		t.Fatal(err)
	}

	r.Request()
	rep.reportOnce(t.Context()) // must not panic and must not block

	// And the window is still there to send when the backend comes back.
	counters, _ := r.Take(epoch)
	if counters.Requests != 1 {
		t.Errorf("requests = %d, want 1 kept for a later attempt", counters.Requests)
	}
}

// Run reports as soon as it starts and once more on the way out.
//
// The first is what makes a freshly installed agent appear in the fleet view
// within seconds instead of at the first five-minute tick — and what makes an
// install that cannot reach the backend say so immediately instead of looking
// fine until somebody checks. The second is what stops a clean shutdown throwing
// away the window it was in the middle of.
func TestRunReportsAtStartAndOnShutdown(t *testing.T) {
	b := newBackend(t)
	r := NewRecorder(epoch)
	rep, _ := newTestReporter(t, b, r)

	r.Request()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		rep.Run(ctx)
	}()

	// Waited for, not raced. Cancelling straight away is how this test used to
	// read, and it passed for the wrong reason: Run had not been scheduled yet, so
	// its first report ran on an already-cancelled context and failed before it
	// sent anything. The shutdown report then arrived alone and the assertion of
	// "exactly one heartbeat" held — on scheduling luck, not behaviour.
	waitForHeartbeats(t, b, 1)

	r.Request()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return when its context was cancelled")
	}

	got, _, enrolments := b.received()
	if len(got) != 2 {
		t.Fatalf("the backend saw %d heartbeats, want one at start and one on shutdown", len(got))
	}
	if enrolments != 1 {
		t.Errorf("enrolled %d times, want 1: the report at start must not re-enrol", enrolments)
	}

	// One request in each window, never the same one twice. A window that carried
	// its predecessor's counters would double every number on the dashboard, and
	// the report at start is exactly the kind of extra send that would do it.
	if got[0].Counters.Requests != 1 || got[1].Counters.Requests != 1 {
		t.Errorf("the two heartbeats carry %d and %d requests, want 1 and 1",
			got[0].Counters.Requests, got[1].Counters.Requests)
	}
}

// waitForHeartbeats blocks until the backend has seen n of them.
func waitForHeartbeats(t *testing.T, b *backend, n int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, _, _ := b.received(); len(got) >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	got, _, _ := b.received()
	t.Fatalf("waited for %d heartbeats, the backend saw %d", n, len(got))
}

func TestReporterRequiresItsInputs(t *testing.T) {
	valid := Config{
		BaseURL:      "http://example.invalid",
		IdentityFile: "/tmp/agent.json",
		Recorder:     NewRecorder(epoch),
		State:        func() telemetry.State { return telemetry.State{} },
	}

	tests := map[string]func(*Config){
		"no backend URL":   func(c *Config) { c.BaseURL = "" },
		"no recorder":      func(c *Config) { c.Recorder = nil },
		"no state":         func(c *Config) { c.State = nil },
		"no identity file": func(c *Config) { c.IdentityFile = "" },
	}
	for name, break_ := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			break_(&cfg)
			if _, err := NewReporter(cfg); err == nil {
				t.Error("NewReporter accepted an incomplete configuration")
			}
		})
	}

	if _, err := NewReporter(valid); err != nil {
		t.Errorf("a complete configuration was refused: %v", err)
	}
}

// Without an identity and without a token there is nothing to do, and the window
// is kept rather than sent nowhere.
func TestNoTokenMeansNoReportAndNoLoss(t *testing.T) {
	b := newBackend(t)
	r := NewRecorder(epoch)

	rep, err := NewReporter(Config{
		BaseURL:      b.server.URL,
		IdentityFile: filepath.Join(t.TempDir(), "agent.json"),
		Recorder:     r,
		State:        func() telemetry.State { return telemetry.State{} },
		Now:          func() time.Time { return epoch },
	})
	if err != nil {
		t.Fatal(err)
	}

	r.Request()
	rep.reportOnce(t.Context())

	if _, _, enrolments := b.received(); enrolments != 0 {
		t.Errorf("enrolled %d times with no token", enrolments)
	}
	if counters, _ := r.Take(epoch); counters.Requests != 1 {
		t.Errorf("requests = %d, want 1 kept: the window was never taken", counters.Requests)
	}
}
