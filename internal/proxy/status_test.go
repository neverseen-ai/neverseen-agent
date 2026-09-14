package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
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
// state rather than handle an error, and a shell wired to `neverseen env` depends
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
	answering := Status{Addr: "127.0.0.1:9787", Answering: true,
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
	if !strings.Contains(out.String(), "NEVERSEEN_PII_LOCALE") {
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

// Write opens on Headline, so the sentence a person reads is the shared one rather
// than a second copy that drifts away from it.
//
// Asserted because the drift it prevents has already happened once: `neverseen
// status` called a category "switched off" where `neverseen mask` called the same
// category "in clear", and nothing failed. The command's half of the pair is held
// by TestMaskOpensOnTheSharedSentence.
func TestWriteOpensOnTheHeadline(t *testing.T) {
	for name, status := range map[string]Status{
		"stopped": {Addr: "127.0.0.1:9787"},
		"no locale": {Addr: "127.0.0.1:9787", Answering: true, Health: Health{
			Masking: "none"}},
		"partial": {Addr: "127.0.0.1:9787", Answering: true, Health: Health{
			Locales: []string{"fr"}, Masking: "partial",
			Groups: []HealthGroup{{Code: "technical", Label: "Technical identifiers",
				Categories: []HealthCategory{{Code: "IP_ADDRESS", Label: "IP address", Off: true}}}},
		}},
		"full": {Addr: "127.0.0.1:9787", Answering: true, Health: Health{
			Locales: []string{"fr"}, Masking: "full"}},
	} {
		t.Run(name, func(t *testing.T) {
			var out strings.Builder
			status.Write(&out)

			first, _, _ := strings.Cut(out.String(), "\n")
			if first != status.Headline() {
				t.Errorf("the report opens on %q, but the shared sentence is %q", first, status.Headline())
			}
		})
	}
}

// PolicyOf turns what a surface is looking at into what it has to send, and the
// switched-off set has to cross as codes.
//
// The two lists are for different readers: SwitchedOff names categories in words for
// a person, SwitchedOffCodes gives the codes a request carries. A caller that mixed
// them would send "Email address" to the agent, which fails with "no category named"
// — an error that reads as a bug in the agent rather than in the caller.
func TestPolicyOfCarriesCodesNotLabels(t *testing.T) {
	status := Status{Addr: "127.0.0.1:9787", Answering: true, Health: Health{
		Locales: []string{"fr", "gb"}, Substitution: "fake", SecretLevel: "strong",
		Masking: "partial",
		Groups: []HealthGroup{{Code: "technical", Label: "Technical identifiers",
			Categories: []HealthCategory{
				{Code: "IP_ADDRESS", Label: "IP address", Off: true},
				{Code: "MONGO_ID", Label: "Database identifier"},
			}}},
	}}

	want := PolicyOf(status)

	if len(want.Off) != 1 || want.Off[0] != "IP_ADDRESS" {
		t.Errorf("the set to send is %v, want the code IP_ADDRESS", want.Off)
	}
	if names := status.SwitchedOff(); len(names) != 1 || names[0] != "IP address" {
		t.Errorf("the names for a person are %v, want the label", names)
	}

	// The other three parts travel unchanged, because the route replaces the state
	// rather than patching it: a caller that dropped one would silently move the
	// agent somewhere nobody asked for.
	if want.Substitution != "fake" || want.SecretLevel != "strong" {
		t.Errorf("the mode and level did not survive: %+v", want)
	}
	if len(want.Locales) != 2 {
		t.Errorf("the locale selection did not survive: %v", want.Locales)
	}
}

// The modes and levels this build offers, served rather than spelled out by each
// surface. A page offering a name the agent does not have would fail on a name the
// page itself suggested.
//
// Asked of the payload rather than of the two functions, which is the half that was
// missing: they agreed with the parser all along while the settings page drew a list
// of literals of its own, so nothing here noticed that what a surface receives and
// what the agent takes had stopped being the same question.
func TestTheOfferedNamesAreOnesTheAgentTakes(t *testing.T) {
	health := (&Server{det: detector.New(detector.Config{})}).healthNow()

	if len(health.Substitutions) == 0 || len(health.SecretLevels) == 0 {
		t.Fatalf("the payload offers no choice to draw: %v / %v",
			health.Substitutions, health.SecretLevels)
	}

	for _, mode := range health.Substitutions {
		if _, err := detector.ParseSubstitution(mode); err != nil {
			t.Errorf("this build offers the mode %q and refuses it: %v", mode, err)
		}
	}
	for _, level := range health.SecretLevels {
		if _, err := detector.ParseSecretLevel(level); err != nil {
			t.Errorf("this build offers the level %q and refuses it: %v", level, err)
		}
	}

	// And the live value is one of them, or the page draws a choice with nothing
	// selected over a setting the agent is actually applying.
	if !slices.Contains(health.Substitutions, health.Substitution) {
		t.Errorf("the live mode %q is not among those offered: %v",
			health.Substitution, health.Substitutions)
	}
	if !slices.Contains(health.SecretLevels, health.SecretLevel) {
		t.Errorf("the live level %q is not among those offered: %v",
			health.SecretLevel, health.SecretLevels)
	}
}

// The health payload carries the whole catalogue by group, so it grows with the
// catalogue while the bound Query reads it under does not. At 8 KiB the two had
// already crossed: the second tier of vendor prefixes took the body past the
// cap, the read truncated, and Query's deliberately quiet parse left every field
// empty — an agent answering perfectly, reported by every surface as one that
// was not. The failure is silent by design, which is exactly why it needs a test
// rather than a comment.
func TestHealthPayloadFitsTheQueryBound(t *testing.T) {
	// Every locale at once: the payload lists a category per loaded locale, so
	// the largest selection is the one the bound has to hold.
	det := detector.New(detector.Config{Locales: pii.LocaleCodes()})
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

	resp, err := http.Get(agent.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if len(body) >= healthMaxBytes {
		t.Fatalf("the health payload is %d bytes and Query reads at most %d: it would be "+
			"truncated, the parse would fail and every surface would report an empty status "+
			"over a healthy agent", len(body), healthMaxBytes)
	}
	// Half the bound is the line worth failing on rather than the bound itself:
	// crossing it means the next batch of categories is the one that breaks the
	// field, and this is the commit that can still choose the number.
	if len(body) > healthMaxBytes/2 {
		t.Errorf("the health payload is %d bytes, over half of the %d Query reads — raise "+
			"healthMaxBytes in the same commit as the categories that did it", len(body), healthMaxBytes)
	}
	t.Logf("health payload %d bytes against a bound of %d", len(body), healthMaxBytes)
}

// The two headings the settings page draws, served rather than worked out there.
//
// A page splitting on a list of group codes of its own would be a second copy of
// the taxonomy, and the day a family was added it would file it under the wrong
// heading — quietly, since both headings draw the same switches.
func TestTheCatalogueSaysWhichFamiliesAreCredentials(t *testing.T) {
	det := detector.New(detector.Config{Locales: []string{"fr", "gb", "us"}})

	credentials := map[string]bool{}
	for _, g := range catalogueOf(det) {
		credentials[g.Code] = g.Credentials
	}

	for code, want := range map[string]bool{
		"personal":   false,
		"company":    false,
		"technical":  false,
		"banking":    false,
		"connection": true,
		"secrets":    true,
	} {
		got, drawn := credentials[code]
		if !drawn {
			t.Errorf("the catalogue does not carry the %q family at all", code)
			continue
		}
		if got != want {
			t.Errorf("family %q: credentials = %v, want %v", code, got, want)
		}
	}

	// The one that would be filed wrong by a surface splitting on "can I switch
	// it": every category in it is locked, and none of them is a credential.
	for _, g := range catalogueOf(det) {
		if g.Code != "declared" {
			continue
		}
		if !g.Locked() {
			t.Error("the declared family is no longer locked, which this case rests on")
		}
		if g.Credentials {
			t.Error("the declared family is filed under the credentials heading, " +
				"where what a deployment declared sensitive itself does not belong")
		}
	}
}

// Each country is served with what it can find, because a checkbox for a country
// that is not loaded raises a question nothing else on the payload answers.
//
// It also pins the arithmetic the page refuses to do. The lists overlap — several
// countries carry a postcode, a telephone number and a postal address — so their
// lengths do not add up to what a selection has in play, which is why a surface
// shows these names and never a count.
func TestEachCountryIsServedWithWhatItFinds(t *testing.T) {
	det := detector.New(detector.Config{Locales: []string{"fr", "gb", "us"}})
	srv := &Server{det: det}
	health := srv.healthNow()

	for _, code := range health.AvailableLocales {
		if len(health.LocaleCategories[code]) == 0 {
			t.Errorf("locale %q is offered with nothing said about what it finds", code)
		}
	}

	// Labels for a reader, not codes to send back. A surface that mixed the two
	// would send "Social security number (fr)" to a route that knows only SSN_FR.
	fr := health.LocaleCategories["fr"]
	if !slices.Contains(fr, pii.Label(pii.CatSIREN)) {
		t.Errorf("fr does not name SIREN among what it finds: %v", fr)
	}
	for _, label := range fr {
		if strings.ToUpper(label) == label {
			t.Errorf("fr names %q, which is a code rather than a label", label)
		}
	}

	// The overlap, asserted rather than described: a category two countries carry is
	// named by both, and survives while either is loaded.
	postcode := pii.Label(pii.CatPostalCode)
	for _, code := range []string{"fr", "gb", "us"} {
		if !slices.Contains(health.LocaleCategories[code], postcode) {
			t.Errorf("%q does not name %q, and this case rests on the overlap", code, postcode)
		}
	}

	// So the lengths must not add up to what is in play — the sum a count on the
	// page would invite a reader to make.
	sum := 0
	for _, code := range health.AvailableLocales {
		sum += len(health.LocaleCategories[code])
	}
	inPlay := 0
	for _, g := range health.Groups {
		if !g.Credentials {
			inPlay += len(g.Categories)
		}
	}
	if sum == inPlay {
		t.Errorf("the per-country lists add up to what is in play (%d), so this test "+
			"no longer demonstrates why the page shows names rather than a count", sum)
	}
}
