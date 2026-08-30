package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/vault"
	"github.com/cloakfleet/cloakfleet/pkg/pii"
)

const testControlKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// newControlledAgent is an agent whose policy route is armed with a known key.
func newControlledAgent(t *testing.T, up *upstream, locales []string) (*httptest.Server, *detector.Detector) {
	t.Helper()

	det := detector.New(detector.Config{Locales: locales})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}

	srv, err := New(Config{
		Providers:  []Provider{{Code: "anthropic", BaseURL: up.server.URL}},
		ControlKey: testControlKey,
	}, det, v)
	if err != nil {
		t.Fatal(err)
	}

	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)
	return agent, det
}

// putPolicy sends a set of categories to switch off, with whatever key is given.
func putPolicy(t *testing.T, agent *httptest.Server, key, body string) reply {
	t.Helper()

	req, err := http.NewRequest(http.MethodPut, agent.URL+"/policy", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set(controlHeader, key)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: string(raw)}
}

func TestPolicySwitchesACategoryOff(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})

	got := putPolicy(t, agent, testControlKey, `{"off":["IP_ADDRESS"],"substitution":"token","locales":["fr"]}`)
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.body)
	}

	if off := det.Disabled(); len(off) != 1 || string(off[0]) != "IP_ADDRESS" {
		t.Fatalf("the agent switched off %v", off)
	}

	// The reply is the new state, from the agent, so a surface can redraw from what
	// is true rather than from what it asked for.
	var health Health
	if err := json.Unmarshal([]byte(got.body), &health); err != nil {
		t.Fatalf("the reply is not a Health: %v", err)
	}
	if health.Masking != detector.LevelPartial.String() {
		t.Errorf("the reply says masking=%q, want partial", health.Masking)
	}

	// And the value now travels in clear, which is the consequence the whole route
	// exists to produce. Asserted on what the provider received, not on the reply:
	// the response path expands the tokens again, so the caller sees its own values
	// either way — which is the round trip working, not the masking failing.
	post(t, agent, "/anthropic/v1/messages", "policy",
		`{"prompt":"depuis 192.168.1.44 et claire.dubois@example.fr"}`)

	bodies, _ := up.received()
	if len(bodies) != 1 {
		t.Fatalf("the provider received %d requests, want 1", len(bodies))
	}
	if !strings.Contains(bodies[0], "192.168.1.44") {
		t.Errorf("the address was still masked on the way out:\n%s", bodies[0])
	}
	if strings.Contains(bodies[0], "claire.dubois@example.fr") {
		t.Errorf("switching one category off stopped another being masked:\n%s", bodies[0])
	}
}

// Without the secret, nothing changes. This is the whole reason the route is
// authenticated: any local process — and any page in a browser, which can post to
// 127.0.0.1 — would otherwise be able to disable the control, silently.
func TestPolicyRefusesWithoutTheKey(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})

	for _, tt := range []struct {
		name string
		key  string
	}{
		{"no header at all", ""},
		{"a wrong key", strings.Repeat("f", 64)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := putPolicy(t, agent, tt.key, `{"off":["IP_ADDRESS"],"substitution":"token","locales":["fr"]}`)
			if got.status != http.StatusForbidden {
				t.Errorf("status %d, want 403: %s", got.status, got.body)
			}
			if off := det.Disabled(); off != nil {
				t.Errorf("the agent switched off %v anyway", off)
			}
		})
	}
}

// An agent with no key refuses everything rather than falling back to accepting
// anything, which is the failure the route's whole design is about.
func TestAnAgentWithNoKeyAcceptsNoChange(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	got := putPolicy(t, agent, testControlKey, `{"off":["IP_ADDRESS"],"substitution":"token","locales":["fr"]}`)
	if got.status != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503: %s", got.status, got.body)
	}
}

// The server refuses a credential, not just the menu. A surface that hid those
// rows would still be talking to an agent, and the rule has to live where nothing
// can route around it.
func TestPolicyRefusesACredentialFromTheWire(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})

	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{"an API key", `{"off":["SECRET_ANTHROPIC_KEY"],"substitution":"token","locales":["fr"]}`, "live key"},
		{"a connection string", `{"off":["SECRET_CONN_STR"],"substitution":"token","locales":["fr"]}`, "live key"},
		{"the deployment's own declaration", `{"off":["CUSTOM"],"substitution":"token","locales":["fr"]}`, "declared sensitive itself"},
		{"a category that does not exist", `{"off":["INVENTED"],"substitution":"token","locales":["fr"]}`, "no category named"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := putPolicy(t, agent, testControlKey, tt.body)
			if got.status != http.StatusUnprocessableEntity {
				t.Fatalf("status %d, want 422: %s", got.status, got.body)
			}
			if !strings.Contains(got.body, tt.want) {
				t.Errorf("the refusal does not say why: %s", got.body)
			}
			if off := det.Disabled(); off != nil {
				t.Errorf("a refused request left %v switched off", off)
			}
		})
	}
}

// A refused request must not leave half a set behind: the whole set is the unit.
func TestARefusedSetIsNotPartlyApplied(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})

	got := putPolicy(t, agent, testControlKey, `{"off":["IP_ADDRESS","SECRET_ANTHROPIC_KEY"],"substitution":"token","locales":["fr"]}`)
	if got.status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", got.status)
	}
	if off := det.Disabled(); off != nil {
		t.Errorf("the switchable half was applied anyway: %v", off)
	}
}

func TestPolicyRefusesOtherMethods(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _ := newControlledAgent(t, up, []string{"fr"})

	resp, err := http.Get(agent.URL + "/policy")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status %d, want 405", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != http.MethodPut {
		t.Errorf("Allow is %q, want PUT", got)
	}
}

// /healthz carries the whole catalogue as a surface needs to draw it, and says
// which entries may not be switched.
func TestHealthCarriesTheCatalogue(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _ := newControlledAgent(t, up, []string{"fr"})

	resp, err := http.Get(agent.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var health Health
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}

	if health.Masking != detector.LevelFull.String() {
		t.Errorf("masking=%q, want full", health.Masking)
	}

	seen := map[string]HealthCategory{}
	for _, g := range health.Groups {
		if g.Label == "" {
			t.Errorf("group %q has no label", g.Code)
		}
		for _, c := range g.Categories {
			if c.Label == "" {
				t.Errorf("category %q has no label", c.Code)
			}
			seen[c.Code] = c
		}
	}

	if got, ok := seen["SECRET_ANTHROPIC_KEY"]; !ok || !got.Locked {
		t.Errorf("the Anthropic key is %+v, want it present and locked", got)
	}
	if got, ok := seen["EMAIL"]; !ok || got.Locked || got.Off {
		t.Errorf("the email category is %+v, want it present, unlocked and on", got)
	}
	// Every category, so a surface reading this can never be missing a switch the
	// agent honours.
	if len(seen) < 30 {
		t.Errorf("/healthz listed %d categories, want the whole catalogue", len(seen))
	}
}

// The key file is created once, kept private, and read back rather than replaced.
func TestTheControlKeyIsCreatedOnceAndKeptPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "control.key")

	first, err := loadControlKey(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(first) != 64 {
		t.Fatalf("key is %d characters, want 64", len(first))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the key file is %o, want 600", perm)
	}
	if dir, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	} else if perm := dir.Mode().Perm(); perm != 0o700 {
		// A 0600 file in a world-readable directory is a secret anybody can watch
		// appear.
		t.Errorf("the directory is %o, want 700", perm)
	}

	again, err := loadControlKey(path)
	if err != nil {
		t.Fatalf("load again: %v", err)
	}
	if again != first {
		t.Error("the key was replaced on the second read, so the menu bar's copy would be stale")
	}

	if got := ReadControlKey(path); got != first {
		t.Errorf("ReadControlKey returned %q, want the key the agent wrote", got)
	}
}

// A truncated key file is replaced rather than used: a short secret is worse than
// none, because it looks like protection.
func TestATruncatedKeyFileIsReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.key")
	if err := os.WriteFile(path, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	key, err := loadControlKey(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(key) != 64 {
		t.Errorf("key is %d characters, want a fresh 64", len(key))
	}
	if got := ReadControlKey(path); got != key {
		t.Errorf("the replacement was not written back: %q", got)
	}
}

// A reader never creates a key, because one created by a reader is one the agent
// does not know.
func TestReadControlKeyCreatesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.key")

	if got := ReadControlKey(path); got != "" {
		t.Errorf("ReadControlKey invented %q", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("ReadControlKey created the key file")
	}
}

// SetPolicy is the one place a local surface writes the policy, as Query is the one
// place it is read. It carries the header, the method and the shape of the body —
// all three of which the route checks — so a test that skipped it would be testing
// a request no surface actually sends.
func TestSetPolicyGoesThroughTheRoute(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})
	addr := strings.TrimPrefix(agent.URL, "http://")

	status, err := SetPolicy(t.Context(), addr, testControlKey,
		Policy{Off: []string{"IP_ADDRESS"}, Substitution: "token", Locales: []string{"fr"}}, 2*time.Second)
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	if !status.Answering {
		t.Error("the reply was not read as an answer")
	}
	if got := status.Level(); got != detector.LevelPartial {
		t.Errorf("level is %v, want partial", got)
	}
	if off := status.SwitchedOff(); len(off) != 1 || off[0] != "IP address" {
		t.Errorf("the reply names %v as switched off", off)
	}
	if got := det.Disabled(); len(got) != 1 {
		t.Errorf("the agent switched off %v", got)
	}

	// And back on, with nil rather than an empty slice — the shape a menu sends when
	// somebody switches the last one back on.
	status, err = SetPolicy(t.Context(), addr, testControlKey,
		Policy{Substitution: "token", Locales: []string{"fr"}}, 2*time.Second)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := status.Level(); got != detector.LevelFull {
		t.Errorf("level is %v, want full", got)
	}
	if got := det.Disabled(); got != nil {
		t.Errorf("%v is still switched off", got)
	}
}

// The agent's own words reach the caller. A menu replacing them with "the request
// failed" would throw away the only part somebody can act on.
func TestSetPolicyCarriesTheAgentsRefusal(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _ := newControlledAgent(t, up, []string{"fr"})
	addr := strings.TrimPrefix(agent.URL, "http://")

	_, err := SetPolicy(t.Context(), addr, testControlKey,
		Policy{Off: []string{"SECRET_ANTHROPIC_KEY"}, Substitution: "token", Locales: []string{"fr"}},
		2*time.Second)
	if err == nil {
		t.Fatal("the credential was accepted")
	}
	if !strings.Contains(err.Error(), "live key") {
		t.Errorf("the error does not carry what the agent said: %v", err)
	}
}

// No key, no request. Said as an error rather than attempted, because the route
// would refuse it anyway and the caller has to be told where the key should be.
func TestSetPolicyWithoutAKeySaysWhere(t *testing.T) {
	_, err := SetPolicy(t.Context(), "127.0.0.1:1", "",
		Policy{Off: []string{"EMAIL"}, Substitution: "token"}, time.Second)
	if err == nil {
		t.Fatal("a policy change was attempted with no key")
	}
	if !strings.Contains(err.Error(), DefaultControlKeyFile) {
		t.Errorf("the error does not name the key file: %v", err)
	}
}

// An agent that is not there is an error rather than a silent success: a menu that
// redrew from a failed write would show a tick that lies.
func TestSetPolicyOnAnAbsentAgent(t *testing.T) {
	_, err := SetPolicy(t.Context(), "127.0.0.1:1", testControlKey,
		Policy{Substitution: "token"}, 500*time.Millisecond)
	if err == nil {
		t.Error("writing to nothing succeeded")
	}
}

// Level is read from what the agent said, not recomputed — and an agent that
// answered without the field is read from what it did carry.
func TestLevelIsReadFromTheAgent(t *testing.T) {
	tests := map[string]struct {
		status Status
		want   detector.Level
	}{
		"not answering": {Status{}, detector.LevelNone},
		"no locale": {
			Status{Answering: true, Health: Health{Masking: "full"}}, detector.LevelNone,
		},
		"full": {
			Status{Answering: true, Health: Health{Locales: []string{"fr"}, Masking: "full"}},
			detector.LevelFull,
		},
		"partial": {
			Status{Answering: true, Health: Health{Locales: []string{"fr"}, Masking: "partial"}},
			detector.LevelPartial,
		},
		// An older build has no policy route, so nothing can be switched off: full
		// is a fact about that build rather than an assumption.
		"a build with no masking field": {
			Status{Answering: true, Health: Health{Locales: []string{"fr"}}},
			detector.LevelFull,
		},
		// A body that parsed only partly is still read from what it carried.
		"no field but a category is off": {
			Status{Answering: true, Health: Health{
				Locales: []string{"fr"},
				Groups: []HealthGroup{{Code: "technical", Categories: []HealthCategory{
					{Code: "IP_ADDRESS", Label: "IP address", Off: true},
				}}},
			}},
			detector.LevelPartial,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.status.Level(); got != tt.want {
				t.Errorf("level is %v, want %v", got, tt.want)
			}
		})
	}
}

// The status text names what is in clear rather than counting it: a name is what
// tells somebody whether the category they care about is among them.
func TestStatusNamesWhatIsInClear(t *testing.T) {
	status := Status{Addr: "127.0.0.1:8787", Answering: true, Health: Health{
		Locales: []string{"fr"}, Masking: "partial", Version: "1.4.2", Substitution: "token",
		Groups: []HealthGroup{{Code: "technical", Label: "Technical identifiers",
			Categories: []HealthCategory{
				{Code: "IP_ADDRESS", Label: "IP address", Off: true},
				{Code: "MONGO_ID", Label: "Database identifier"},
			}}},
	}}

	var out strings.Builder
	status.Write(&out)

	got := out.String()
	if !strings.Contains(got, "1 category switched off") {
		t.Errorf("the summary does not count what is off:\n%s", got)
	}
	if !strings.Contains(got, "in clear       IP address") {
		t.Errorf("the report does not name what is in clear:\n%s", got)
	}
	if strings.Contains(got, "Database identifier") {
		t.Errorf("a category still being masked is listed as in clear:\n%s", got)
	}
}

// Only what the agent can actually find is offered as a switch.
//
// With one locale loaded the catalogue holds categories whose patterns are not in
// the detector at all, and an entry offering to stop masking a US social security
// number on a French deployment would say the agent is masking them — a switch whose
// only possible effect is to mislead whoever reads it.
func TestOnlyTheCategoriesInPlayAreOffered(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _ := newControlledAgent(t, up, []string{"fr"})

	resp, err := http.Get(agent.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var health Health
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}

	offered := map[string]bool{}
	for _, g := range health.Groups {
		for _, c := range g.Categories {
			offered[c.Code] = true
		}
	}

	// French identifiers and everything locale-independent, yes.
	for _, want := range []string{"NIR", "SIREN", "EMAIL", "IP_ADDRESS", "SECRET_ANTHROPIC_KEY"} {
		if !offered[want] {
			t.Errorf("%s is not offered, and this agent can find it", want)
		}
	}
	// The other countries' identifiers, no.
	for _, unwanted := range []string{"SSN", "NINO", "NHS_NUMBER", "EIN", "ROUTING_NUMBER"} {
		if offered[unwanted] {
			t.Errorf("%s is offered as a switch, and this agent cannot recognise it at all", unwanted)
		}
	}

	// And a family with nothing in play is not drawn at all: an empty heading reads
	// as a group the agent lost rather than one its locales never loaded.
	for _, g := range health.Groups {
		if len(g.Categories) == 0 {
			t.Errorf("family %q is drawn with no categories", g.Code)
		}
	}
}

// The route replaces the whole state, so a request missing part of it is refused
// rather than completed from what happens to be current.
//
// This is the footgun the refusal exists to close: a caller that sent only "off"
// would silently wipe the locale selection, turning the agent into one that masks
// almost nothing while reporting success.
func TestPolicyRefusesAPartialRequest(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})

	got := putPolicy(t, agent, testControlKey, `{"off":["IP_ADDRESS"]}`)
	if got.status != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", got.status, got.body)
	}
	if !strings.Contains(got.body, "whole state") {
		t.Errorf("the refusal does not say why: %s", got.body)
	}
	if l := det.Locales(); len(l) != 1 || l[0] != "fr" {
		t.Errorf("the locales were changed anyway: %v", l)
	}
	if off := det.Disabled(); off != nil {
		t.Errorf("the categories were changed anyway: %v", off)
	}
}

func TestPolicyChangesTheSubstitutionMode(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})

	got := putPolicy(t, agent, testControlKey, `{"substitution":"fake","locales":["fr"]}`)
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.body)
	}
	if mode := det.Substitution(); mode != detector.SubstitutionFake {
		t.Errorf("the mode is %v, want fake", mode)
	}

	// And a value leaving now reads as prose rather than as a token.
	post(t, agent, "/anthropic/v1/messages", "mode", `{"prompt":"tél 06 12 34 56 78"}`)
	bodies, _ := up.received()
	if strings.Contains(bodies[0], "[PHONE_") {
		t.Errorf("fake mode still sent a token:\n%s", bodies[0])
	}
	if strings.Contains(bodies[0], "06 12 34 56 78") {
		t.Errorf("the value went out in clear:\n%s", bodies[0])
	}
}

// A change of mode has to show on the traffic that follows it, including for the
// values the session has already seen.
//
// The regression this pins: the mapping is consulted before the mode is, so a value
// already minted keeps the shape it was first given. Nothing here sends a session
// header, so every exchange shares the one unnamed session — which made a click on
// "fake" change nothing anybody could observe until the agent was restarted.
func TestPolicyChangingTheModeClearsWhatWasAlreadyMinted(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _ := newControlledAgent(t, up, []string{"fr"})

	// One exchange in token mode, so the value is in the session's mapping.
	post(t, agent, "/anthropic/v1/messages", "mode", `{"prompt":"tél 06 12 34 56 78"}`)
	bodies, _ := up.received()
	if !strings.Contains(bodies[0], "[PHONE_") {
		t.Fatalf("the first exchange was not masked with a token:\n%s", bodies[0])
	}

	got := putPolicy(t, agent, testControlKey, `{"substitution":"fake","locales":["fr"]}`)
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.body)
	}

	// The same value again, on the same session.
	post(t, agent, "/anthropic/v1/messages", "mode", `{"prompt":"tél 06 12 34 56 78"}`)
	bodies, _ = up.received()
	last := bodies[len(bodies)-1]
	if strings.Contains(last, "[PHONE_") {
		t.Errorf("the mode changed and the value kept its token:\n%s", last)
	}
	if strings.Contains(last, "06 12 34 56 78") {
		t.Errorf("the value went out in clear:\n%s", last)
	}
}

// Resending the mode the agent is already in must not clear anything.
//
// Every surface sends the whole state on every click, because this route replaces
// rather than patches. Purging on each such request would discard the mapping when
// somebody switched off a category — and the answer to that exchange would come back
// carrying replacements nothing expands.
func TestPolicyResendingTheSameModeKeepsTheMapping(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _ := newControlledAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "keep", `{"prompt":"tél 06 12 34 56 78"}`)
	bodies, _ := up.received()
	first := bodies[0]

	// The mode is unchanged; only the switched-off set moves.
	got := putPolicy(t, agent, testControlKey,
		`{"off":["EMAIL"],"substitution":"token","locales":["fr"]}`)
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.body)
	}

	post(t, agent, "/anthropic/v1/messages", "keep", `{"prompt":"tél 06 12 34 56 78"}`)
	bodies, _ = up.received()
	if last := bodies[len(bodies)-1]; last != first {
		t.Errorf("the value was minted again although the mode did not change:\n%s\nwant\n%s",
			last, first)
	}
}

func TestPolicyRefusesAModeThatDoesNotExist(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})

	got := putPolicy(t, agent, testControlKey, `{"substitution":"invisible","locales":["fr"]}`)
	if got.status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422: %s", got.status, got.body)
	}
	if mode := det.Substitution(); mode != detector.SubstitutionToken {
		t.Errorf("the mode changed to %v anyway", mode)
	}
}

func TestPolicyChangesTheLocales(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})

	got := putPolicy(t, agent, testControlKey, `{"substitution":"token","locales":["gb","fr"]}`)
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.body)
	}
	// Registry order whatever order they arrived in, because load order settles
	// which country claims a value both could read.
	if l := det.Locales(); len(l) != 2 || l[0] != "fr" || l[1] != "gb" {
		t.Errorf("locales are %v, want fr then gb", l)
	}

	// The switches offered follow, which is the whole reason the catalogue is served
	// rather than read from pkg/pii by the caller.
	var health Health
	if err := json.Unmarshal([]byte(got.body), &health); err != nil {
		t.Fatal(err)
	}
	offered := map[string]bool{}
	for _, g := range health.Groups {
		for _, c := range g.Categories {
			offered[c.Code] = true
		}
	}
	if !offered["NINO"] {
		t.Error("loading gb did not add its identifiers to the switches offered")
	}
}

// No locale at all is a valid state and the one an agent starts in, so it has to be
// reachable — and it has to read as not masking.
func TestPolicyCanLoadNoLocaleAtAll(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})

	got := putPolicy(t, agent, testControlKey, `{"substitution":"token","locales":[]}`)
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.body)
	}
	if l := det.Locales(); len(l) != 0 {
		t.Errorf("locales are %v, want none", l)
	}

	var health Health
	if err := json.Unmarshal([]byte(got.body), &health); err != nil {
		t.Fatal(err)
	}
	if health.Masking != detector.LevelNone.String() {
		t.Errorf("masking is %q, want none", health.Masking)
	}
}

func TestPolicyRefusesALocaleThatDoesNotExist(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})

	got := putPolicy(t, agent, testControlKey, `{"substitution":"token","locales":["fr","uk"]}`)
	if got.status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422: %s", got.status, got.body)
	}
	if !strings.Contains(got.body, "no locale") {
		t.Errorf("the refusal does not name the problem: %s", got.body)
	}
	if l := det.Locales(); len(l) != 1 || l[0] != "fr" {
		t.Errorf("the refused selection was partly applied: %v", l)
	}
}

// /healthz names the locales the build has, not only the loaded ones: a surface
// offering a choice cannot draw one from a list of what is already on.
func TestHealthNamesTheAvailableLocales(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _ := newControlledAgent(t, up, []string{"fr"})

	resp, err := http.Get(agent.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var health Health
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}

	want := pii.LocaleCodes()
	if len(health.AvailableLocales) != len(want) {
		t.Fatalf("available locales are %v, want %v", health.AvailableLocales, want)
	}
	for i := range want {
		if health.AvailableLocales[i] != want[i] {
			t.Errorf("available locales are %v, want them in registry order %v",
				health.AvailableLocales, want)
		}
	}
}

// The modes a surface may offer come from the one parser that knows them.
func TestSubstitutionModesAreTheOnesTheBuildHas(t *testing.T) {
	modes := SubstitutionModes()
	if len(modes) != 2 || modes[0] != "token" || modes[1] != "fake" {
		t.Fatalf("modes are %v, want token then fake", modes)
	}
	for _, mode := range modes {
		if _, err := detector.ParseSubstitution(mode); err != nil {
			t.Errorf("mode %q is offered but the agent will not parse it: %v", mode, err)
		}
	}
}
