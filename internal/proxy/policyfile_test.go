package proxy

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/vault"
)

// newStoringAgent is an agent whose policy route is armed and whose changes are
// stored, so a test can restart from the file it wrote.
func newStoringAgent(t *testing.T, locales []string, file string) *httptest.Server {
	t.Helper()

	det := detector.New(detector.Config{Locales: locales})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}

	srv, err := New(Config{ControlKey: testControlKey, PolicyFile: file}, det, v)
	if err != nil {
		t.Fatal(err)
	}

	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)
	return agent
}

// The point of the file: what somebody switched off in the menu is still switched
// off after the workstation restarts.
//
// Asserted through the two real halves — the route that a click goes through, then
// FromEnv, which is the only assembly there is — rather than by reading the JSON
// back, because a file written correctly and never read would pass a test and
// change nothing about the agent.
//
// The environment is deliberately set to something else. It is what an installed
// workstation looks like: a profile naming a locale, and a person who has since
// said otherwise. The file wins, or the click lasts until the next restart.
func TestAChangeSurvivesARestartAndBeatsTheEnvironment(t *testing.T) {
	file := filepath.Join(t.TempDir(), "policy.json")
	agent := newStoringAgent(t, []string{"fr"}, file)

	got := putPolicy(t, agent, testControlKey,
		`{"off":["EMAIL"],"substitution":"fake","locales":["gb"],"secret_level":"medium"}`)
	if got.status != 200 {
		t.Fatalf("the change was refused: %d — %s", got.status, got.body)
	}

	t.Setenv(detector.EnvLocale, "fr")
	t.Setenv(detector.EnvSubstitution, "token")
	t.Setenv(detector.EnvSecretLevel, "weak")

	restarted, err := FromEnv(nil, Options{
		ControlKeyFile: filepath.Join(t.TempDir(), "control.key"),
		PolicyFile:     file,
	})
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}

	health := restarted.Server.healthNow()
	if !slices.Equal(health.Locales, []string{"gb"}) {
		t.Errorf("the stored locales did not survive: %v", health.Locales)
	}
	if health.Substitution != "fake" {
		t.Errorf("the stored substitution mode did not survive: %s", health.Substitution)
	}
	if health.SecretLevel != "medium" {
		t.Errorf("the stored secret level did not survive: %s", health.SecretLevel)
	}
	if off := restarted.Server.livePolicy().Off; !slices.Equal(off, []string{"EMAIL"}) {
		t.Errorf("the switched-off category did not survive: %v", off)
	}
}

// The route is not a transaction: a request whose locales are good and whose
// category is refused leaves the locales applied. What goes to disk is therefore
// what the agent *is*, not what it was asked for — otherwise a restart would undo
// half a change with nothing to say it had happened.
func TestAPartlyRefusedChangeIsStoredAsItLanded(t *testing.T) {
	file := filepath.Join(t.TempDir(), "policy.json")
	agent := newStoringAgent(t, []string{"fr"}, file)

	got := putPolicy(t, agent, testControlKey,
		`{"off":["SECRET_GENERIC"],"substitution":"token","locales":["gb"],"secret_level":"weak"}`)
	if got.status != 422 {
		t.Fatalf("a credential was switched off: %d — %s", got.status, got.body)
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("nothing was stored for a change that half applied: %v", err)
	}
	var stored policyRequest
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stored.Locales, []string{"gb"}) {
		t.Errorf("the applied locales were not stored: %+v", stored)
	}
	if len(stored.Off) != 0 {
		t.Errorf("a refused category was stored as switched off: %+v", stored)
	}
}

// A request refused before the first change is one the agent was never told, and
// the file's absence has to keep meaning "nobody has".
//
// The store used to be deferred above the applier, so a misspelled mode — refused
// with 422, nothing applied — created the file for the first time. From then on the
// agent read its own empty policy in preference to the environment, so
// CLOAKFLEET_PII_LOCALE in the operator's profile did nothing, permanently, over a
// request that changed nothing. It is the counterpart of the case above, and neither
// is meaningful alone.
func TestARequestRefusedBeforeAnythingAppliedStoresNothing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "policy.json")
	agent := newStoringAgent(t, []string{"fr"}, file)

	// Non-empty, so it passes the "send the whole state" guard and reaches the
	// applier, and misspelled, so the applier refuses before it touches anything.
	got := putPolicy(t, agent, testControlKey,
		`{"off":[],"substitution":"tokens","locales":["gb"],"secret_level":"weak"}`)
	if got.status != 422 {
		t.Fatalf("a misspelled mode was accepted: %d — %s", got.status, got.body)
	}

	if _, err := os.Stat(file); !os.IsNotExist(err) {
		raw, _ := os.ReadFile(file)
		t.Errorf("a request that changed nothing created the policy file: %v — %s", err, raw)
	}
}

// Two surfaces write this file, and they interleave.
//
// The menu bar and `cloakfleet mask` poll, change one field and send the whole
// state, so a click on each within the same cycle is two of these at once. Written
// through one fixed ".tmp" path they truncated and filled the same file under each
// other, and what landed was one writer's document inside the other's rename — or
// the two torn together, which parses to nothing and hands the agent back to the
// environment at the next start, silently. Run under -race, which is how the
// handler's own read-modify-write is caught as well.
func TestConcurrentChangesLeaveAWholeFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "policy.json")
	agent := newStoringAgent(t, []string{"fr", "gb", "us"}, file)

	bodies := []string{
		`{"off":["EMAIL"],"substitution":"fake","locales":["fr","gb","us"],"secret_level":"strong"}`,
		`{"off":[],"substitution":"token","locales":["fr"],"secret_level":"weak"}`,
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(body string) {
			defer wg.Done()
			if got := putPolicy(t, agent, testControlKey, body); got.status != 200 {
				t.Errorf("the change was refused: %d — %s", got.status, got.body)
			}
		}(bodies[i%len(bodies)])
	}
	wg.Wait()

	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("nothing was stored: %v", err)
	}
	var stored policyRequest
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("the stored policy is not a whole document: %v — %s", err, raw)
	}
	// Whichever writer landed last, what is on disk has to be one of the two states
	// asked for and not a mixture of them.
	if stored.Substitution != "fake" && stored.Substitution != "token" {
		t.Errorf("the stored state is neither of the two that were sent: %+v", stored)
	}
}

// A file that cannot be read must not stop the agent, and must not start it from
// half a document. Masking configured by a profile is a working agent; a state read
// from a truncated file is not.
func TestAnUnreadableStoredPolicyLeavesTheEnvironmentAlone(t *testing.T) {
	file := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(file, []byte(`{"locales":["g`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv(detector.EnvLocale, "fr")
	agent, err := FromEnv(nil, Options{
		ControlKeyFile: filepath.Join(t.TempDir(), "control.key"),
		PolicyFile:     file,
	})
	if err != nil {
		t.Fatalf("a torn policy file stopped the agent: %v", err)
	}
	if locales := agent.Server.healthNow().Locales; !slices.Equal(locales, []string{"fr"}) {
		t.Errorf("the environment was not what the agent started from: %v", locales)
	}
}

// An agent that has never been told anything is the ordinary case, not a degraded
// one: no file, no warning, and the environment decides.
func TestNoStoredPolicyMeansTheEnvironmentDecides(t *testing.T) {
	dir := t.TempDir()

	t.Setenv(detector.EnvLocale, "us")
	agent, err := FromEnv(nil, Options{
		ControlKeyFile: filepath.Join(dir, "control.key"),
		PolicyFile:     filepath.Join(dir, "policy.json"),
	})
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if locales := agent.Server.healthNow().Locales; !slices.Equal(locales, []string{"us"}) {
		t.Errorf("the environment was not what the agent started from: %v", locales)
	}

	// And nothing is written before anybody changes anything: the file records a
	// decision somebody made, so its absence has to keep meaning "nobody has".
	if _, err := os.Stat(filepath.Join(dir, "policy.json")); !os.IsNotExist(err) {
		t.Errorf("a policy was stored although nothing changed one: %v", err)
	}
}

// A partial file is a partial request, and the applier — not the route — is what
// refuses one. Before this guard lived there, `{"off":["EMAIL"]}` on disk got a 400
// on PUT /policy and was applied on restart: both parsers accept "", SetLocales(nil)
// unloads every locale, and the agent came up masking almost nothing while its
// start-up line said the stored policy was authoritative.
func TestAPartialStoredPolicyLeavesTheEnvironmentAlone(t *testing.T) {
	file := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(file, []byte(`{"off":["EMAIL"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv(detector.EnvLocale, "fr")
	agent, err := FromEnv(nil, Options{
		ControlKeyFile: filepath.Join(t.TempDir(), "control.key"),
		PolicyFile:     file,
	})
	if err != nil {
		t.Fatalf("a partial policy file stopped the agent: %v", err)
	}
	if locales := agent.Server.healthNow().Locales; !slices.Equal(locales, []string{"fr"}) {
		t.Errorf("a partial file replaced the environment's locales: %v", locales)
	}
	if off := agent.Server.livePolicy().Off; len(off) != 0 {
		t.Errorf("half a file was applied: off=%v", off)
	}
}

// A request refused on its categories over a locale list identical to what was
// loaded is also one that changed nothing, and it must not create the file either.
//
// `applied` used to be set the moment SetLocales returned nil, and nil says the
// list was valid rather than that it differed — every surface resends the whole
// state on every click. So a typo in a category, or a credential somebody tried to
// switch off, got its 422 and created the file over the loaded locales, and from
// then on the environment was ignored over a change of nothing.
func TestARequestRefusedOnItsCategoriesOverTheSameLocalesStoresNothing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "policy.json")
	agent := newStoringAgent(t, []string{"fr"}, file)

	for _, body := range []string{
		`{"off":["SECRET_GENERIC"],"substitution":"token","locales":["fr"],"secret_level":"weak"}`,
		`{"off":["EMAILL"],"substitution":"token","locales":["fr"],"secret_level":"weak"}`,
	} {
		got := putPolicy(t, agent, testControlKey, body)
		if got.status != 422 {
			t.Fatalf("%s was accepted: %d — %s", body, got.status, got.body)
		}
	}

	if _, err := os.Stat(file); !os.IsNotExist(err) {
		raw, _ := os.ReadFile(file)
		t.Errorf("a request that changed nothing created the policy file: %v — %s", err, raw)
	}
}

// The intent survives a click that could not see it.
//
// A surface rebuilds the set it sends from /healthz, and the groups there list only
// what the loaded locales can find. With SSN off and `us` unloaded, one click on
// anything else — the mode, here — sent `off: []`, the detector switched SSN back
// on, and the file made it permanent: loading `us` later found SSN masked with the
// switch drawn on, over a setting nobody had touched. Health.Off is the whole set,
// and SwitchedOffCodes reads it, so what a surface resends is what the agent
// remembers.
func TestAnUnreachableCategoryStaysOffThroughAnUnrelatedClick(t *testing.T) {
	file := filepath.Join(t.TempDir(), "policy.json")
	agent := newStoringAgent(t, []string{"us"}, file)

	if got := putPolicy(t, agent, testControlKey,
		`{"off":["SSN"],"substitution":"token","locales":["us"],"secret_level":"weak"}`); got.status != 200 {
		t.Fatalf("switching SSN off: %d — %s", got.status, got.body)
	}
	if got := putPolicy(t, agent, testControlKey,
		`{"off":["SSN"],"substitution":"token","locales":["fr"],"secret_level":"weak"}`); got.status != 200 {
		t.Fatalf("unloading us: %d — %s", got.status, got.body)
	}

	// What a surface does: read the state, change one thing, send the whole state.
	status := Query(t.Context(), strings.TrimPrefix(agent.URL, "http://"), time.Second)
	if slices.Contains(status.SwitchedOff(), "Social security number (us)") {
		t.Errorf("an unreachable category is drawn as a switch: %v", status.SwitchedOff())
	}
	policy := PolicyOf(status)
	policy.Substitution = "fake"
	if _, err := SetPolicy(t.Context(), status.Addr, testControlKey, policy, time.Second); err != nil {
		t.Fatalf("the click was refused: %v", err)
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var stored policyRequest
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(stored.Off, "SSN") {
		t.Errorf("a click on the mode switched an unreachable category back on: %+v", stored)
	}
}
