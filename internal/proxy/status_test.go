package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/vault"
)

// The route and its reader are one type in one package, so the only way to check
// they agree is to serve a real agent and read it back.
func TestStatusReadsWhatTheAgentServes(t *testing.T) {
	det := detector.New(detector.Config{Locales: []string{"fr", "gb"}})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{}, det, v)
	if err != nil {
		t.Fatal(err)
	}

	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)

	got := Query(t.Context(), strings.TrimPrefix(agent.URL, "http://"), time.Second)
	if !got.Answering {
		t.Fatal("the agent's own health route did not answer")
	}
	if got.Status != "ok" {
		t.Errorf("status = %q, want ok", got.Status)
	}
	if len(got.Locales) == 0 || got.Substitution == "" || len(got.Providers) == 0 {
		t.Errorf("the health route left fields empty: %+v", got.Health)
	}
	if !got.Masking() {
		t.Errorf("an agent with %v loaded does not report itself masking", got.Locales)
	}
}

// Nothing listening is an answer, not a failure: every caller wants to report that
// state rather than handle an error, and a shell wired to `cloakfleet env` depends
// on it being cheap and quiet.
func TestQueryReportsAnAbsentAgent(t *testing.T) {
	got := Query(t.Context(), "127.0.0.1:1", 200*time.Millisecond)
	if got.Answering || got.Masking() {
		t.Errorf("a closed port reported %+v", got)
	}
	if got.Addr != "127.0.0.1:1" {
		t.Errorf("addr = %q, want the address that was asked", got.Addr)
	}

	var out strings.Builder
	got.Write(&out)
	// The consequence, not just the fact. A stopped agent means unmasked traffic
	// rather than a broken workstation, and somebody reading this has to know
	// which — it is the trade the installer makes on purpose.
	if !strings.Contains(out.String(), "unmasked") {
		t.Errorf("the report does not say what a stopped agent costs:\n%s", out.String())
	}
}

// Answering is not the question. An agent with no locale selected is up, healthy,
// and recognises almost nothing — the state a green light would call fine.
func TestAnAgentWithNoLocaleIsNotMasking(t *testing.T) {
	answering := Status{Addr: "127.0.0.1:8787", Answering: true,
		Health: Health{Status: "ok", Substitution: "token"}}
	if answering.Masking() {
		t.Error("an agent with no locale reports itself masking")
	}

	var out strings.Builder
	answering.Write(&out)
	if !strings.Contains(out.String(), "masking almost nothing") {
		t.Errorf("the report reads as healthy:\n%s", out.String())
	}
	// And it names the variable to set, from the constant the detector reads, so
	// the instruction cannot go stale the day it is renamed.
	if !strings.Contains(out.String(), "CLOAKFLEET_PII_LOCALE") {
		t.Errorf("the report does not say what to set:\n%s", out.String())
	}
}

// A body that will not parse still means something answered. Reporting "not
// answering" would send somebody to restart a service that is running.
func TestABadBodyStillCountsAsAnswering(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	got := Query(t.Context(), strings.TrimPrefix(srv.URL, "http://"), time.Second)
	if !got.Answering {
		t.Error("an unparseable answer was reported as no answer at all")
	}
	if got.Masking() {
		t.Error("an unparseable answer was reported as masking")
	}
}
