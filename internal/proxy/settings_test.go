package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
)

// This page is the only response body on this agent that carries the control key,
// so the tests are about who is refused it — not that it returns 200.
//
// Every refusal is asserted to carry no key as well as the right status. A guard
// that ran after the template did would still return 403 with the secret in the
// body, and a test reading the status alone would pass over it.

// settingsAgent is an agent whose settings page is armed with a known key.
func settingsAgent(t *testing.T, key string) *Server {
	t.Helper()

	det := detector.New(detector.Config{Locales: []string{"fr"}})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}

	srv, err := New(Config{
		Providers:  []Provider{{Code: "anthropic", BaseURL: "https://example.invalid"}},
		ControlKey: key,
	}, det, v)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// ask calls the page directly, so the source address and the Host are the test's to
// choose. Through an httptest server they are both loopback and there would be no
// way to exercise either guard.
func ask(t *testing.T, srv *Server, remoteAddr, host string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/settings", nil)
	req.RemoteAddr = remoteAddr
	req.Host = host

	rec := httptest.NewRecorder()
	srv.handleSettings(rec, req)
	return rec
}

func TestSettingsPageHandsTheControlKeyToALocalBrowser(t *testing.T) {
	srv := settingsAgent(t, testControlKey)

	got := ask(t, srv, "127.0.0.1:52341", "127.0.0.1:9787")
	if got.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", got.Code, got.Body.String())
	}
	if !strings.Contains(got.Body.String(), testControlKey) {
		t.Error("the page carries no control key, so every control on it would fail on submission")
	}
	// A page holding a secret has no business in a disk cache, and the agent it
	// describes moves under it besides.
	if cc := got.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", cc)
	}

	// The clause that bounds where the key can go, asserted by name rather than by
	// the header being present: a policy that lost it would still look like one.
	csp := got.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("the policy does not confine where the page may connect: %q", csp)
	}
	if !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("the policy does not default to refusing: %q", csp)
	}
}

// The DNS rebinding case, and the reason the Host is checked at all: the request
// below comes *from* loopback — a browser on this machine — carrying a domain
// somebody else controls. Refusing the source address alone would let that page
// read this body and switch masking off from the internet.
func TestSettingsPageRefusesAHostThatIsNotThisMachine(t *testing.T) {
	srv := settingsAgent(t, testControlKey)

	got := ask(t, srv, "127.0.0.1:52341", "rebound.example.com")
	if got.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", got.Code)
	}
	if strings.Contains(got.Body.String(), testControlKey) {
		t.Fatal("the refusal carried the control key")
	}
}

// The -l case. The warn-rather-than-refuse trade that lets /healthz and /test answer
// the network does not transfer to the one body that gives away the ability to
// switch masking off.
func TestSettingsPageRefusesACallerFromTheNetwork(t *testing.T) {
	srv := settingsAgent(t, testControlKey)

	got := ask(t, srv, "203.0.113.5:44120", "192.168.1.20:9787")
	if got.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", got.Code)
	}
	if strings.Contains(got.Body.String(), testControlKey) {
		t.Fatal("the refusal carried the control key")
	}
	if !strings.Contains(got.Body.String(), "loopback") {
		t.Errorf("the refusal does not say why: %s", got.Body.String())
	}
}

// An agent that could not read or write its key refuses, rather than serving a page
// whose every control fails on submission with nothing saying why.
func TestSettingsPageRefusesWhenTheAgentHasNoControlKey(t *testing.T) {
	srv := settingsAgent(t, "")

	got := ask(t, srv, "127.0.0.1:52341", "127.0.0.1:9787")
	if got.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", got.Code)
	}
}

func TestSettingsPageIsGETOnly(t *testing.T) {
	srv := settingsAgent(t, testControlKey)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/settings", nil)
	req.RemoteAddr = "127.0.0.1:52341"
	req.Host = "127.0.0.1:9787"

	rec := httptest.NewRecorder()
	srv.handleSettings(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d, want 405", rec.Code)
	}
	if strings.Contains(rec.Body.String(), testControlKey) {
		t.Fatal("the refusal carried the control key")
	}
}

func TestNamesThisMachine(t *testing.T) {
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"127.0.0.1:9787", true},
		{"localhost:9787", true},
		{"LOCALHOST:9787", true},
		// What a request to port 80 carries, which SplitHostPort refuses.
		{"localhost", true},
		{"127.0.0.1", true},
		{"[::1]:9787", true},
		// The whole 127/8 range is loopback, and a browser reaches the agent on any
		// of it.
		{"127.0.0.53:9787", true},

		{"rebound.example.com", false},
		// SplitHostPort does not check that a port is one, so the name it hands back
		// for this is "localhost" — which is the answer this guard wanted to see.
		{"localhost:9787@rebound.example.com", false},
		{"127.0.0.1:notaport", false},
		{"localhost.example.com:9787", false},
		{"192.168.1.20:9787", false},
		{"", false},
	} {
		if got := namesThisMachine(tc.host); got != tc.want {
			t.Errorf("namesThisMachine(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}
