package proxy

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	return askFor(t, srv, "/settings", remoteAddr, host)
}

// askFor is ask for a target that carries a query, a ticket among them.
func askFor(t *testing.T, srv *Server, target, remoteAddr, host string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	req.RemoteAddr = remoteAddr
	req.Host = host

	rec := httptest.NewRecorder()
	srv.handleSettings(rec, req)
	return rec
}

// mintWith asks the agent for a ticket the way the menu bar does, presenting key.
func mintWith(t *testing.T, srv *Server, key string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/settings", nil)
	req.RemoteAddr = "127.0.0.1:52341"
	req.Host = "127.0.0.1:9787"
	req.Header.Set(controlHeader, key)

	rec := httptest.NewRecorder()
	srv.handleSettings(rec, req)
	return rec
}

// mint is mintWith the agent's key, and returns the ticket.
func mint(t *testing.T, srv *Server) string {
	t.Helper()

	rec := mintWith(t, srv, testControlKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("minting a ticket: status %d: %s", rec.Code, rec.Body.String())
	}
	var reply settingsTicket
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || reply.Ticket == "" {
		t.Fatalf("minting a ticket answered %q", rec.Body.String())
	}
	return reply.Ticket
}

// refusedWithATicket asks for the page with a live ticket from where a guard must refuse
// it, and holds the refusal to carry no key and to leave the ticket unspent. Without a
// ticket the page carries no key whether the guards run or not, so a refusal asked
// without one could not show a guard that ran too late.
func refusedWithATicket(t *testing.T, srv *Server, remoteAddr, host string) *httptest.ResponseRecorder {
	t.Helper()

	target := "/settings?" + ticketParam + "=" + mint(t, srv)
	got := askFor(t, srv, target, remoteAddr, host)
	if strings.Contains(got.Body.String(), testControlKey) {
		t.Fatal("the refusal carried the control key")
	}
	// Spent by a refused request, the owner's own link would open a page without it.
	if again := askFor(t, srv, target, "127.0.0.1:52341", "127.0.0.1:9787"); !strings.Contains(again.Body.String(), testControlKey) {
		t.Error("a refused request spent the ticket")
	}
	return got
}

func TestATicketOpensTheSettingsPageWithTheKeyOnce(t *testing.T) {
	srv := settingsAgent(t, testControlKey)
	target := "/settings?" + ticketParam + "=" + mint(t, srv)

	got := askFor(t, srv, target, "127.0.0.1:52341", "127.0.0.1:9787")
	if got.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", got.Code, got.Body.String())
	}
	if !strings.Contains(got.Body.String(), testControlKey) {
		t.Error("the page opened with a ticket carries no control key, so every control on it would fail")
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
	// Not covered by default-src: without it the page could be framed and its
	// switches clicked through a decoy.
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("the policy lets the page be framed: %q", csp)
	}

	// The second use is the copy read off a process list, and it gets the page
	// without the key.
	again := askFor(t, srv, target, "127.0.0.1:52341", "127.0.0.1:9787")
	if strings.Contains(again.Body.String(), testControlKey) {
		t.Fatal("a ticket opened the page with the key twice")
	}
}

func TestAnExpiredTicketCarriesNoKey(t *testing.T) {
	srv := settingsAgent(t, testControlKey)
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	srv.tickets.now = func() time.Time { return now }

	target := "/settings?" + ticketParam + "=" + mint(t, srv)
	now = now.Add(ticketTTL)

	got := askFor(t, srv, target, "127.0.0.1:52341", "127.0.0.1:9787")
	if strings.Contains(got.Body.String(), testControlKey) {
		t.Fatal("a ticket past its minute opened the page with the key")
	}
}

// A ticket is what proves the caller can read the key file, so minting one without
// the key would undo the whole arrangement.
func TestMintingATicketNeedsTheKey(t *testing.T) {
	srv := settingsAgent(t, testControlKey)

	for _, key := range []string{"", strings.Repeat("f", len(testControlKey))} {
		rec := mintWith(t, srv, key)
		if rec.Code != http.StatusForbidden {
			t.Errorf("key %q: status %d, want 403", key, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "ticket\"") {
			t.Errorf("key %q: the refusal carried a ticket: %s", key, rec.Body.String())
		}
	}
	if len(srv.tickets.issued) != 0 {
		t.Errorf("%d tickets were minted for a caller without the key", len(srv.tickets.issued))
	}
}

// Through the whole handler and a real socket, as the menu bar and `neverseen
// settings` reach it: the link SettingsURL returns opens the page with the key, once.
func TestSettingsURLIsALinkThatWorksOnce(t *testing.T) {
	srv := settingsAgent(t, testControlKey)
	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)
	addr := strings.TrimPrefix(agent.URL, "http://")

	link, err := SettingsURL(t.Context(), addr, testControlKey, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true, false} {
		resp, err := http.Get(link)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if got := strings.Contains(string(body), testControlKey); got != want {
			t.Errorf("open %d: carries the key = %v, want %v", i+1, got, want)
		}
	}

	if _, err := SettingsURL(t.Context(), addr, strings.Repeat("f", len(testControlKey)), 2*time.Second); err == nil {
		t.Error("a wrong key was given a link")
	}
}

// An agent bound beyond loopback (-l 0.0.0.0:<port>, for a container) still serves its
// page on loopback only, so the link must name loopback: naming the bound address, the
// ticket request was refused by namesThisMachine and the menu bar's entry for the page did nothing.
func TestSettingsURLNamesLoopbackWhateverWasBound(t *testing.T) {
	srv := settingsAgent(t, testControlKey)
	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(agent.URL, "http://"))

	for _, bound := range []string{"0.0.0.0:" + port, ":" + port, "[::]:" + port} {
		link, err := SettingsURL(t.Context(), bound, testControlKey, 2*time.Second)
		if err != nil {
			t.Fatalf("%s: %v", bound, err)
		}
		if want := "http://127.0.0.1:" + port + "/settings?"; !strings.HasPrefix(link, want) {
			t.Errorf("%s: link %q, want it under %q", bound, link, want)
		}
	}
}

// The menu bar opens a page without a ticket when it cannot get one, and named the bound
// address: http://:9787/settings, which is no URL, or 0.0.0.0, which namesThisMachine
// refuses. A browser here reaches an agent on every address on loopback.
func TestABrowserReachesAnAgentOnEveryAddressOnLoopback(t *testing.T) {
	for bound, want := range map[string]string{
		"":                "127.0.0.1:9787",
		":9787":           "127.0.0.1:9787",
		"0.0.0.0:9787":    "127.0.0.1:9787",
		"[::]:9787":       "127.0.0.1:9787",
		"localhost:9787":  "localhost:9787",
		"127.0.0.1:9787":  "127.0.0.1:9787",
		"192.0.2.10:9787": "192.0.2.10:9787", // the only address it listens on
	} {
		if got := BrowsableAddr(bound); got != want {
			t.Errorf("%q: %q, want %q", bound, got, want)
		}
	}
}

// An agent bound to one address that is not loopback has no page anywhere: it says so,
// where dialling 127.0.0.1, on which nothing listens, said "connection refused".
func TestSettingsURLSaysWhyAnAgentOffLoopbackHasNoPage(t *testing.T) {
	_, err := SettingsURL(t.Context(), "192.0.2.10:9787", testControlKey, time.Second)
	if err == nil || !strings.Contains(err.Error(), "loopback alone") {
		t.Fatalf("error %v, want one saying the page is served on loopback alone", err)
	}
}

// Another account on this machine. Its request is indistinguishable from the owner's
// browser at both guards — loopback source, a Host naming this machine — because TCP
// on the loopback interface carries no identity. Without a ticket, which only somebody
// who can read the key file can obtain, it must not be handed the key.
func TestSettingsPageWithoutATicketCarriesNoKey(t *testing.T) {
	srv := settingsAgent(t, testControlKey)

	got := ask(t, srv, "127.0.0.1:52341", "localhost:9787")
	if strings.Contains(got.Body.String(), testControlKey) {
		t.Fatal("a caller with no ticket was handed the control key")
	}
}

// The DNS rebinding case, and the reason the Host is checked at all: the request
// below comes *from* loopback — a browser on this machine — carrying a domain
// somebody else controls. Refusing the source address alone would let that page
// read this body and switch masking off from the internet.
func TestSettingsPageRefusesAHostThatIsNotThisMachine(t *testing.T) {
	srv := settingsAgent(t, testControlKey)

	got := refusedWithATicket(t, srv, "127.0.0.1:52341", "rebound.example.com")
	if got.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", got.Code)
	}
}

// The -l case. The warn-rather-than-refuse trade that lets /healthz and /test answer
// the network does not transfer to the one body that gives away the ability to
// switch masking off.
func TestSettingsPageRefusesACallerFromTheNetwork(t *testing.T) {
	srv := settingsAgent(t, testControlKey)

	got := refusedWithATicket(t, srv, "203.0.113.5:44120", "192.168.1.20:9787")
	if got.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", got.Code)
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

func TestSettingsPageRefusesOtherMethods(t *testing.T) {
	srv := settingsAgent(t, testControlKey)

	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequestWithContext(t.Context(), method, "/settings", nil)
		req.RemoteAddr = "127.0.0.1:52341"
		req.Host = "127.0.0.1:9787"
		req.Header.Set(controlHeader, testControlKey)

		rec := httptest.NewRecorder()
		srv.handleSettings(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d, want 405", method, rec.Code)
		}
		if strings.Contains(rec.Body.String(), testControlKey) {
			t.Errorf("%s: the refusal carried the control key", method)
		}
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
