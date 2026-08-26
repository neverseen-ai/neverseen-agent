package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cloakfleet/cloakfleet/pkg/telemetry"
)

// The agent files its last window before the command returns.
//
// This is a guarantee about the process, not about the reporter, which is why it
// is tested here: the reporter's own test proves it reports when its context is
// cancelled, and that passed while this was broken. What was missing was anything
// waiting for it — runProxy returned as soon as the HTTP server had shut down, the
// process exited, and the final heartbeat was cut off mid-flight.
//
// It matters because SIGTERM is not an exceptional event for this agent. It is
// installed as a launchd or systemd service, so every restart, every upgrade and
// every logout sends one. Losing the window each time means the audit trail an
// org is certified on has a five-minute hole at exactly the moments a security
// officer would ask about.
func TestTheLastWindowIsFiledBeforeTheCommandReturns(t *testing.T) {
	var (
		mu         sync.Mutex
		heartbeats []telemetry.Heartbeat
	)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/enrol":
			// The key is hex because that is what the reporter decodes.
			_ = json.NewEncoder(w).Encode(telemetry.EnrolResponse{
				AgentID: "agt_shutdown_test",
				Key:     strings.Repeat("ab", 32),
			})
		case "/v1/heartbeats":
			body, _ := io.ReadAll(r.Body)
			var batch telemetry.HeartbeatBatch
			if err := json.Unmarshal(body, &batch); err == nil {
				mu.Lock()
				// Expanded through the contract's own function, as the backend does.
				heartbeats = append(heartbeats, batch.Windows()...)
				mu.Unlock()
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer backend.Close()

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"test-model","usage":{"input_tokens":7,"output_tokens":3}}`))
	}))
	defer provider.Close()

	addr := freePort(t)
	t.Setenv("CLOAKFLEET_LISTEN", addr)
	t.Setenv("CLOAKFLEET_PROVIDERS", "anthropic="+provider.URL)
	t.Setenv("CLOAKFLEET_PII_LOCALE", "fr")
	t.Setenv("CLOAKFLEET_BACKEND_URL", backend.URL)
	t.Setenv("CLOAKFLEET_ENROLMENT_TOKEN", "enrol-me")
	t.Setenv("CLOAKFLEET_IDENTITY_FILE", filepath.Join(t.TempDir(), "agent.json"))

	returned := make(chan error, 1)
	go func() { returned <- runProxy(io.Discard) }()

	// Waited for so the signal below cannot arrive before signal.NotifyContext has
	// registered — an unhandled SIGTERM would kill the test binary itself.
	waitUntilAnswering(t, "http://"+addr+"/healthz")

	// One request, after the report at startup, so the only heartbeat that can
	// carry it is the one filed on the way out.
	resp, err := http.Post("http://"+addr+"/anthropic/v1/messages", "application/json",
		strings.NewReader(`{"c":"write to claire@example.fr"}`))
	if err != nil {
		t.Fatalf("the proxy refused a request: %v", err)
	}
	_ = resp.Body.Close()

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}

	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("runProxy returned %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runProxy did not return after SIGTERM")
	}

	// Asserted with no waiting whatsoever, because that is the whole point: by the
	// time the command has returned the window is already at the backend. Polling
	// here would pass either way, since the reporter goroutine outlives runProxy
	// inside a test binary in a way it never does in a real process.
	mu.Lock()
	defer mu.Unlock()

	var carried bool
	for _, hb := range heartbeats {
		if hb.Counters.Requests == 1 && hb.Counters.Masked["EMAIL"] == 1 {
			carried = true
		}
	}
	if !carried {
		t.Errorf("no heartbeat carried the request made before shutdown; the backend got %d: %+v",
			len(heartbeats), heartbeats)
	}
}

// freePort returns an address nothing is listening on.
//
// A real port rather than :0 because the test has to poll the agent's own health
// endpoint, and a port the kernel chose inside ListenAndServe is one the test
// cannot name.
func freePort(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release the port: %v", err)
	}
	return addr
}

func waitUntilAnswering(t *testing.T, url string) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never answered", url)
}
