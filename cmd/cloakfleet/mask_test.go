package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloakfleet/cloakfleet/internal/proxy"
)

// fakeAgent answers /healthz with a catalogue and records what /policy was sent, so
// the command is tested against the shape a real agent serves rather than against a
// stub of its own invention.
type fakeAgent struct {
	server  *httptest.Server
	off     []string
	mode    string
	locales []string
	sent    []proxy.Policy
	refuse  string
}

func newFakeAgent(t *testing.T, off ...string) *fakeAgent {
	t.Helper()

	a := &fakeAgent{off: off, mode: "token", locales: []string{"fr"}}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, a.health())
	})
	mux.HandleFunc("/policy", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Off          []string `json:"off"`
			Substitution string   `json:"substitution"`
			Locales      []string `json:"locales"`
		}
		decodeJSON(t, r, &req)
		a.sent = append(a.sent, proxy.Policy{
			Off: req.Off, Substitution: req.Substitution, Locales: req.Locales,
		})

		// The real route refuses a request that does not carry the whole state, so
		// the fake one does too: a test passing against a laxer agent than the real
		// one is a test that proves nothing.
		if req.Substitution == "" {
			http.Error(w, "cloakfleet: this route replaces the whole state", http.StatusBadRequest)
			return
		}
		if a.refuse != "" {
			http.Error(w, "cloakfleet: "+a.refuse, http.StatusUnprocessableEntity)
			return
		}
		a.off, a.mode, a.locales = req.Off, req.Substitution, req.Locales
		writeJSON(t, w, a.health())
	})

	a.server = httptest.NewServer(mux)
	t.Cleanup(a.server.Close)
	t.Setenv(proxy.EnvListen, strings.TrimPrefix(a.server.URL, "http://"))
	return a
}

func (a *fakeAgent) health() proxy.Health {
	isOff := func(code string) bool {
		for _, c := range a.off {
			if c == code {
				return true
			}
		}
		return false
	}

	masking := "full"
	switch {
	case len(a.locales) == 0:
		masking = "none"
	case len(a.off) > 0:
		masking = "partial"
	}
	return proxy.Health{
		Status: "ok", Version: "1.4.2", Locales: a.locales,
		AvailableLocales: []string{"fr", "gb", "us"},
		Substitution:     a.mode, Masking: masking,
		Groups: []proxy.HealthGroup{
			{Code: "personal", Label: "Personal details", Categories: []proxy.HealthCategory{
				{Code: "EMAIL", Label: "Email address", Off: isOff("EMAIL")},
				{Code: "DOB", Label: "Date of birth", Off: isOff("DOB")},
			}},
			// Deliberately mixed, which no group in the real catalogue is today: one
			// switchable category beside a locked one. It is here to exercise the
			// branch that skips a locked member named through its family, which
			// becomes reachable the moment a group gains one.
			{Code: "technical", Label: "Technical identifiers", Categories: []proxy.HealthCategory{
				{Code: "IP_ADDRESS", Label: "IP address", Off: isOff("IP_ADDRESS")},
				{Code: "MONGO_ID", Label: "Database identifier", Locked: true},
			}},
			{Code: "secrets", Label: "Secrets and keys", Categories: []proxy.HealthCategory{
				{Code: "SECRET_ANTHROPIC_KEY", Label: "Anthropic key", Locked: true},
			}},
			{Code: "declared", Label: "Declared by this deployment", Categories: []proxy.HealthCategory{
				{Code: "CUSTOM", Label: "Declared value", Locked: true},
			}},
		},
	}
}

// With no flags the command lists the catalogue. The first column is a word rather
// than a symbol so `cloakfleet mask | grep "in clear"` answers the question the
// command exists for.
func TestMaskListsTheCatalogue(t *testing.T) {
	newFakeAgent(t, "DOB")

	var out strings.Builder
	if err := runMask(nil, &out); err != nil {
		t.Fatalf("mask: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"with 1 category in clear",
		"Personal details  — personal",
		"masked    EMAIL",
		"in clear  DOB",
		"Secrets and keys  (never switched off here)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the listing does not carry %q:\n%s", want, got)
		}
	}
}

// The whole set is sent, not the flag's argument: a request per category would be
// several read-modify-writes instead of one.
func TestMaskSendsTheWholeSet(t *testing.T) {
	agent := newFakeAgent(t, "DOB")

	var out strings.Builder
	if err := runMask([]string{"--off", "IP_ADDRESS"}, &out); err != nil {
		t.Fatalf("mask: %v", err)
	}

	if len(agent.sent) != 1 {
		t.Fatalf("sent %d requests, want 1", len(agent.sent))
	}
	if got := agent.sent[0].Off; len(got) != 2 || got[0] != "DOB" || got[1] != "IP_ADDRESS" {
		t.Errorf("sent %v, want what was already off plus the new one", got)
	}
	// Printed from the agent's reply, which is also the answer to the race this
	// read-modify-write cannot avoid.
	if !strings.Contains(out.String(), "in clear  IP_ADDRESS") {
		t.Errorf("the report does not show the change:\n%s", out.String())
	}
}

// A family by its name, because the point of grouping forty categories is that
// people think in families.
func TestMaskTakesAFamilyName(t *testing.T) {
	agent := newFakeAgent(t)

	var out strings.Builder
	if err := runMask([]string{"--off", "personal"}, &out); err != nil {
		t.Fatalf("mask: %v", err)
	}

	if got := agent.sent[0].Off; len(got) != 2 || got[0] != "DOB" || got[1] != "EMAIL" {
		t.Errorf("sent %v, want the family's two categories", got)
	}
}

// --on and --off in one call, applied to the current set, so one command is one
// request.
func TestMaskSwitchesBothWaysAtOnce(t *testing.T) {
	agent := newFakeAgent(t, "DOB", "EMAIL")

	var out strings.Builder
	if err := runMask([]string{"--off", "IP_ADDRESS", "--on", "EMAIL"}, &out); err != nil {
		t.Fatalf("mask: %v", err)
	}

	if got := agent.sent[0].Off; len(got) != 2 || got[0] != "DOB" || got[1] != "IP_ADDRESS" {
		t.Errorf("sent %v", got)
	}
}

func TestMaskResetSendsNothingOff(t *testing.T) {
	agent := newFakeAgent(t, "DOB", "EMAIL")

	var out strings.Builder
	if err := runMask([]string{"--reset"}, &out); err != nil {
		t.Fatalf("mask: %v", err)
	}
	if got := agent.sent[0].Off; len(got) != 0 {
		t.Errorf("sent %v, want an empty set", got)
	}
	if !strings.Contains(out.String(), "Every category its locales loaded is on") {
		t.Errorf("the report does not say everything is back on:\n%s", out.String())
	}
}

// A locked category named directly is an error: somebody typing it has a belief
// about what this agent will do, and the only useful answer is that it will not.
func TestMaskRefusesALockedCategoryByName(t *testing.T) {
	agent := newFakeAgent(t)

	for _, tt := range []struct {
		name string
		arg  string
		want string
	}{
		{"a credential", "SECRET_ANTHROPIC_KEY", "live key"},
		{"the deployment's own declaration", "CUSTOM", "declared sensitive itself"},
		{"a family of nothing but credentials", "secrets", "live key"},
		{"a name that does not exist", "INVENTED", "no category or family named"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			err := runMask([]string{"--off", tt.arg}, &out)
			if err == nil {
				t.Fatalf("%q was accepted", tt.arg)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not say why: want %q", err, tt.want)
			}
		})
	}

	// And nothing was sent: the command refuses before it writes.
	if len(agent.sent) != 0 {
		t.Errorf("%d requests were sent anyway", len(agent.sent))
	}
}

// Named through a family, a locked category is skipped rather than fatal: "--off
// personal" on a family holding one is a reasonable thing to try, and refusing the
// whole request over it would leave nothing switched.
func TestALockedCategoryInAMixedFamilyIsSkipped(t *testing.T) {
	agent := newFakeAgent(t)

	var out strings.Builder
	if err := runMask([]string{"--off", "technical"}, &out); err != nil {
		t.Fatalf("mask: %v", err)
	}

	// One switchable member sent, and the locked one left out rather than the whole
	// request refused — which would have left nothing switched at all.
	if got := agent.sent[0].Off; len(got) != 1 || got[0] != "IP_ADDRESS" {
		t.Errorf("sent %v, want only the switchable member", got)
	}
}

// The agent's own refusal reaches the caller: it says which category and why, and
// replacing that with "the request failed" throws away the only actionable part.
func TestMaskCarriesTheAgentsRefusal(t *testing.T) {
	agent := newFakeAgent(t)
	agent.refuse = `category "EMAIL" cannot be switched off: it is a credential`

	var out strings.Builder
	err := runMask([]string{"--off", "EMAIL"}, &out)
	if err == nil {
		t.Fatal("the refusal was swallowed")
	}
	if !strings.Contains(err.Error(), "cannot be switched off") {
		t.Errorf("error %q does not carry what the agent said", err)
	}
}

// An agent that is not answering: the same answer `status` gives, because it is the
// same situation — and a non-zero exit so a script notices.
func TestMaskWithNoAgent(t *testing.T) {
	t.Setenv(proxy.EnvListen, "127.0.0.1:1")

	var out strings.Builder
	err := runMask(nil, &out)
	if err == nil {
		t.Fatal("mask succeeded with no agent")
	}
	if !strings.Contains(out.String(), "is not answering") {
		t.Errorf("the report does not say the agent is absent:\n%s", out.String())
	}
}

func TestMaskRefusesContradictoryFlags(t *testing.T) {
	newFakeAgent(t)

	var out strings.Builder
	err := runMask([]string{"--reset", "--off", "EMAIL"}, &out)
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("error is %v, want a refusal to combine --reset with --off", err)
	}
}

// The usage text names the command, for the reason it names every setting: an
// instruction nobody can find is an instruction that does not exist.
func TestUsageNamesTheMaskCommand(t *testing.T) {
	var out strings.Builder
	printUsage(&out)

	if got := out.String(); !strings.Contains(got, "cloakfleet mask") {
		t.Errorf("the usage text does not mention the mask command:\n%s", got)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode: %v", err)
	}
}

func decodeJSON(t *testing.T, r *http.Request, v any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// Every change carries the whole state, because the route replaces it rather than
// patching it: a request naming only --off would wipe the locale selection.
func TestMaskCarriesTheWholeStateOnEveryChange(t *testing.T) {
	agent := newFakeAgent(t, "DOB")

	var out strings.Builder
	if err := runMask([]string{"--off", "EMAIL"}, &out); err != nil {
		t.Fatalf("mask: %v", err)
	}

	sent := agent.sent[0]
	if sent.Substitution != "token" {
		t.Errorf("the mode was not carried: %q", sent.Substitution)
	}
	if len(sent.Locales) != 1 || sent.Locales[0] != "fr" {
		t.Errorf("the locales were not carried: %v", sent.Locales)
	}
}

func TestMaskChangesTheSubstitutionMode(t *testing.T) {
	agent := newFakeAgent(t, "DOB")

	var out strings.Builder
	if err := runMask([]string{"--substitution", "fake"}, &out); err != nil {
		t.Fatalf("mask: %v", err)
	}

	sent := agent.sent[0]
	if sent.Substitution != "fake" {
		t.Errorf("sent %q", sent.Substitution)
	}
	// The other two parts unchanged.
	if len(sent.Off) != 1 || sent.Off[0] != "DOB" {
		t.Errorf("the switched-off categories changed: %v", sent.Off)
	}
	if !strings.Contains(out.String(), "substitution   fake") {
		t.Errorf("the report does not show the new mode:\n%s", out.String())
	}
}

func TestMaskChangesTheLocales(t *testing.T) {
	agent := newFakeAgent(t)

	var out strings.Builder
	if err := runMask([]string{"--locales", "fr,gb"}, &out); err != nil {
		t.Fatalf("mask: %v", err)
	}
	if got := agent.sent[0].Locales; len(got) != 2 || got[0] != "fr" || got[1] != "gb" {
		t.Errorf("sent %v", got)
	}
	if !strings.Contains(out.String(), "countries      fr, gb") {
		t.Errorf("the report does not show the countries:\n%s", out.String())
	}
}

// "none" is spelled out rather than expressed as an empty argument: `--locales ""` is
// what a shell produces from an unset variable by accident, and it must not silently
// mean "stop looking for anything".
func TestMaskTakesNoneAsALocaleSelection(t *testing.T) {
	agent := newFakeAgent(t)

	var out strings.Builder
	if err := runMask([]string{"--locales", "none"}, &out); err != nil {
		t.Fatalf("mask: %v", err)
	}
	if got := agent.sent[0].Locales; got == nil || len(got) != 0 {
		t.Errorf("sent %v, want an empty selection", got)
	}

	// An empty argument changes nothing at all rather than clearing the selection.
	agent.sent = nil
	if err := runMask([]string{"--locales", ""}, &out); err != nil {
		t.Fatalf("mask: %v", err)
	}
	if len(agent.sent) != 0 {
		t.Errorf("an empty --locales sent %v", agent.sent)
	}
}

// A country this agent does not have is refused by the command, so the person who
// typed it learns it from what they ran rather than from an HTTP status.
func TestMaskRefusesAnUnknownCountry(t *testing.T) {
	agent := newFakeAgent(t)

	var out strings.Builder
	err := runMask([]string{"--locales", "fr,uk"}, &out)
	if err == nil {
		t.Fatal("an unknown country was accepted")
	}
	if !strings.Contains(err.Error(), "no country pattern set") {
		t.Errorf("error %q does not name the problem", err)
	}
	if !strings.Contains(err.Error(), "fr, gb, us") {
		t.Errorf("error %q does not say which ones exist", err)
	}
	if len(agent.sent) != 0 {
		t.Errorf("a request was sent anyway: %v", agent.sent)
	}
}

// The listing reports the mode and the countries, since those are now things somebody
// can change and therefore things they have to be able to read.
func TestMaskListsTheModeAndTheCountries(t *testing.T) {
	newFakeAgent(t)

	var out strings.Builder
	if err := runMask(nil, &out); err != nil {
		t.Fatalf("mask: %v", err)
	}

	got := out.String()
	for _, want := range []string{"substitution   token", "countries      fr", "(of fr, gb, us)"} {
		if !strings.Contains(got, want) {
			t.Errorf("the listing does not carry %q:\n%s", want, got)
		}
	}
}

// With no country pattern set loaded the agent is up and recognises almost nothing,
// and a header saying "every category is on" over that would be true and useless.
func TestMaskSaysWhenNoCountryIsLoaded(t *testing.T) {
	agent := newFakeAgent(t)
	agent.locales = nil

	var out strings.Builder
	if err := runMask(nil, &out); err != nil {
		t.Fatalf("mask: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "masking almost nothing") {
		t.Errorf("the listing claims the agent is masking:\n%s", got)
	}
	if strings.Contains(got, "Every category its locales loaded is on") {
		t.Errorf("the listing says every category is on:\n%s", got)
	}
	if !strings.Contains(got, "countries      none") {
		t.Errorf("the listing does not report the empty selection:\n%s", got)
	}
}

// The catalogue listing opens on the same sentence `cloakfleet status` prints.
//
// The other half of the pair proxy's TestWriteOpensOnTheHeadline holds. The two
// commands report on one agent, and written apart they had already drifted: this
// one called a category "in clear" where the other called it "switched off", so a
// person running both was told two things about one state. What follows the
// sentence is each command's own — only this one is about to list the catalogue.
func TestMaskOpensOnTheSharedSentence(t *testing.T) {
	for name, status := range map[string]proxy.Status{
		"no locale": {Addr: "127.0.0.1:8787", Answering: true, Health: proxy.Health{
			Masking: "none"}},
		"partial": {Addr: "127.0.0.1:8787", Answering: true, Health: proxy.Health{
			Locales: []string{"fr"}, Masking: "partial",
			Groups: []proxy.HealthGroup{{Code: "technical", Label: "Technical identifiers",
				Categories: []proxy.HealthCategory{
					{Code: "IP_ADDRESS", Label: "IP address", Off: true}}}},
		}},
		"full": {Addr: "127.0.0.1:8787", Answering: true, Health: proxy.Health{
			Locales: []string{"fr"}, Masking: "full"}},
	} {
		t.Run(name, func(t *testing.T) {
			var out strings.Builder
			writeCatalogue(&out, status)

			first, _, _ := strings.Cut(out.String(), "\n")
			if first != status.Headline() {
				t.Errorf("the listing opens on %q, but the shared sentence is %q",
					first, status.Headline())
			}
		})
	}
}
