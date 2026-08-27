package detector

import (
	"strings"
	"sync"
	"testing"

	"github.com/cloakfleet/cloakfleet/pkg/pii"
)

func TestASwitchedOffCategoryIsNotMasked(t *testing.T) {
	d := New(Config{Locales: []string{"fr"}})
	const text = "écris à claire.dubois@example.fr depuis 192.168.1.44"

	if got := len(d.Scan(text)); got != 2 {
		t.Fatalf("found %d values before anything was switched off, want 2", got)
	}

	if err := d.Disable([]pii.Category{pii.CatIPAddr}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	got := d.Scan(text)
	if len(got) != 1 {
		t.Fatalf("found %d values, want only the email: %v", len(got), got)
	}
	if got[0].Category != pii.CatEmail {
		t.Errorf("the surviving match is %q, want the email", got[0].Category)
	}

	// And the value really does travel in clear, which is the whole consequence.
	masked, _, _ := d.MaskOnce(text)
	if !strings.Contains(masked, "192.168.1.44") {
		t.Errorf("the address was still replaced:\n%s", masked)
	}
	if strings.Contains(masked, "claire.dubois@example.fr") {
		t.Errorf("switching one category off stopped another being masked:\n%s", masked)
	}
}

// Replacing the set, not toggling one entry: two surfaces can be looking at one
// agent, and a toggle is a read-modify-write whose halves interleave.
func TestDisableReplacesTheWholeSet(t *testing.T) {
	d := New(Config{Locales: []string{"fr"}})

	if err := d.Disable([]pii.Category{pii.CatIPAddr, pii.CatEmail}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := len(d.Disabled()); got != 2 {
		t.Fatalf("%d categories off, want 2", got)
	}

	if err := d.Disable([]pii.Category{pii.CatEmail}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := d.Disabled(); len(got) != 1 || got[0] != pii.CatEmail {
		t.Errorf("after replacing the set: %v, want only the email", got)
	}

	if err := d.Disable(nil); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := d.Disabled(); got != nil {
		t.Errorf("an empty set left %v behind", got)
	}
}

// A category the agent will not switch off is refused rather than dropped. Dropped,
// a menu would draw a credential as unticked while the agent went on masking it —
// and somebody would believe the wrong thing about a live key.
func TestDisableRefusesWhatItCannotSwitchOff(t *testing.T) {
	d := New(Config{Locales: []string{"fr"}})

	for _, tt := range []struct {
		name string
		cat  pii.Category
		want string
	}{
		{"a credential", pii.CatAnthropicKey, "live key"},
		{"a connection string, despite its own group", pii.CatConnStr, "live key"},
		{"the deployment's own declaration", pii.CatCustom, "declared sensitive itself"},
		{"a category that does not exist", pii.Category("INVENTED"), "no category named"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := d.Disable([]pii.Category{tt.cat})
			if err == nil {
				t.Fatalf("the agent accepted switching off %q", tt.cat)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not say why: want %q", err, tt.want)
			}
		})
	}

	// And nothing was applied: a refused request must not leave half a set behind.
	if got := d.Disabled(); got != nil {
		t.Errorf("a refused request left %v switched off", got)
	}
}

// The three answers, because the middle one is why this exists.
func TestMaskingLevel(t *testing.T) {
	if got := New(Config{}).Masking(); got != LevelNone {
		t.Errorf("with no locale: %v, want none", got)
	}

	d := New(Config{Locales: []string{"fr"}})
	if got := d.Masking(); got != LevelFull {
		t.Errorf("with a locale and nothing off: %v, want full", got)
	}

	if err := d.Disable([]pii.Category{pii.CatIPAddr}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := d.Masking(); got != LevelPartial {
		t.Errorf("with a category off: %v, want partial", got)
	}
}

// The test page renders both substitution modes through two detectors. A copied
// policy would have it show a category the agent had stopped masking.
func TestADerivedDetectorSharesThePolicy(t *testing.T) {
	d := New(Config{Locales: []string{"fr"}})
	fake := d.WithSubstitution(SubstitutionFake)

	if err := d.Disable([]pii.Category{pii.CatIPAddr}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if got := len(fake.Scan("depuis 192.168.1.44")); got != 0 {
		t.Errorf("the derived detector still found %d values", got)
	}

	// And in the other direction, since either may be the one a surface writes to.
	if err := fake.Disable(nil); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := len(d.Scan("depuis 192.168.1.44")); got != 1 {
		t.Errorf("switching back on through the derived detector left %d values", got)
	}
}

// The set is written while requests are reading it, which is the situation it was
// built for: a click arrives whenever it arrives.
func TestThePolicyIsSafeUnderConcurrency(t *testing.T) {
	d := New(Config{Locales: []string{"fr"}})

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for range 200 {
				if i%2 == 0 {
					_ = d.Disable([]pii.Category{pii.CatIPAddr})
				} else {
					_ = d.Disable(nil)
				}
			}
		}(i)

		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				d.Scan("écris à claire.dubois@example.fr depuis 192.168.1.44")
				_ = d.Disabled()
				_ = d.Masking()
			}
		}()
	}
	wg.Wait()
}

// The level's name is what crosses the wire to a supervision backend and into
// /healthz, so the three spellings are pinned rather than left to a formatter: a
// renamed string is a dashboard column that silently stops matching.
func TestLevelNames(t *testing.T) {
	for level, want := range map[Level]string{
		LevelNone:    "none",
		LevelPartial: "partial",
		LevelFull:    "full",
		Level(99):    "none",
	} {
		if got := level.String(); got != want {
			t.Errorf("level %d is %q, want %q", level, got, want)
		}
	}
}

// Locales change while the agent runs, and the whole catalogue changes with them:
// the patterns, and the stand-in table those patterns resolve against.
func TestSetLocalesReplacesTheCatalogue(t *testing.T) {
	d := New(Config{Locales: []string{"fr"}})

	// A UK national insurance number is unreadable with only fr loaded.
	const gbText = "Payroll shows AB 12 34 56 C against that employee."
	if got := len(d.Scan(gbText)); got != 0 {
		t.Fatalf("found %d values in UK text with fr loaded", got)
	}

	if err := d.SetLocales([]string{"fr", "gb"}); err != nil {
		t.Fatalf("set locales: %v", err)
	}
	if got := d.Scan(gbText); len(got) != 1 || got[0].Category != pii.CatNINO {
		t.Errorf("after loading gb: %v, want the NINO", got)
	}
	// And what the agent reports about itself follows.
	if got := d.Locales(); len(got) != 2 || got[0] != "fr" || got[1] != "gb" {
		t.Errorf("reports locales %v", got)
	}

	// Dropping one takes its patterns away again.
	if err := d.SetLocales([]string{"gb"}); err != nil {
		t.Fatalf("set locales: %v", err)
	}
	if got := len(d.Scan("NIR 2 69 05 49 588 157 80")); got != 0 {
		t.Errorf("a French identifier is still read with only gb loaded: %d matches", got)
	}
}

// Registry order, whatever order they arrived in. Load order settles which country
// claims a value both could read — nine bare digits are a French SIREN under Luhn
// and a US routing number under the ABA weights — so a selection that reordered them
// would quietly change what those digits become.
func TestSetLocalesKeepsRegistryOrder(t *testing.T) {
	d := New(Config{})

	if err := d.SetLocales([]string{"us", "fr", "gb"}); err != nil {
		t.Fatalf("set locales: %v", err)
	}
	got := d.Locales()
	want := []string{"fr", "gb", "us"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("locales are %v, want %v", got, want)
		}
	}
}

// An unknown code is refused, not skipped. pii.LocalePatterns skips one by design —
// it must not decide policy about a selection — so nothing below would notice, and
// an operator who mistyped "uk" would be told the change succeeded while the agent
// went on not reading UK identifiers.
func TestSetLocalesRefusesAnUnknownCode(t *testing.T) {
	d := New(Config{Locales: []string{"fr"}})

	err := d.SetLocales([]string{"fr", "uk"})
	if err == nil {
		t.Fatal("an unknown locale was accepted")
	}
	if !strings.Contains(err.Error(), "no locale") {
		t.Errorf("error %q does not say the code is unknown", err)
	}
	// Nothing applied: a refused selection must not leave half of one behind.
	if got := d.Locales(); len(got) != 1 || got[0] != "fr" {
		t.Errorf("the refused change left locales %v", got)
	}
}

// No locale at all is a valid state, and the one the agent starts in when nothing is
// configured. It must be reachable, and it must read as not masking.
func TestEveryLocaleCanBeSwitchedOff(t *testing.T) {
	d := New(Config{Locales: []string{"fr"}})

	if err := d.SetLocales(nil); err != nil {
		t.Fatalf("set locales: %v", err)
	}
	if got := d.Locales(); len(got) != 0 {
		t.Errorf("locales are %v, want none", got)
	}
	if got := d.Masking(); got != LevelNone {
		t.Errorf("masking is %v, want none", got)
	}
	// The locale-independent identifiers and the credentials still load, because
	// disabling a country must never disable email detection.
	if got := d.Scan("écris à claire.dubois@example.fr"); len(got) != 1 {
		t.Errorf("email detection was lost with the locales: %v", got)
	}
}

// The mode changes while the agent runs, and a conversation straddling the change
// round-trips either way: the vault maps a replacement to its original, and
// expansion accepts both shapes.
func TestSetSubstitutionChangesWhatAValueBecomes(t *testing.T) {
	d := New(Config{Locales: []string{"fr"}})

	pass := d.NewPass(nil)
	first, _ := d.Mask("tél 06 12 34 56 78", pass)
	if !strings.Contains(first, "[PHONE_1]") {
		t.Fatalf("token mode produced %q", first)
	}

	d.SetSubstitution(SubstitutionFake)
	if got := d.Substitution(); got != SubstitutionFake {
		t.Errorf("mode is %v after the change", got)
	}

	second, _ := d.Mask("et aussi 07 98 76 54 32", pass)
	if strings.Contains(second, "[PHONE_") {
		t.Errorf("fake mode still produced a token: %q", second)
	}

	// Both shapes are in one session's mapping now, and both come back.
	mixed := first + " " + second
	if got := Unmask(mixed, pass.Minted()); !strings.Contains(got, "06 12 34 56 78") ||
		!strings.Contains(got, "07 98 76 54 32") {
		t.Errorf("a session straddling the change did not round-trip: %q", got)
	}
}

// A credential never gets a stand-in, whatever the live mode is. The switch must not
// be a way around the rule that keeps a live key out of a caller's answer.
func TestSwitchingToFakeStillTokenizesACredential(t *testing.T) {
	d := New(Config{Locales: []string{"fr"}})
	d.SetSubstitution(SubstitutionFake)

	const key = "sk-ant-api03-" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA-AAAAAAAA"
	masked, known, _ := d.MaskOnce("clé " + key)

	if !strings.Contains(masked, "[ANTHROPIC_KEY_1]") {
		t.Errorf("the credential was not tokenized in fake mode: %q", masked)
	}
	if got := Unmask(masked, known); !strings.Contains(got, key) {
		t.Error("the credential did not come back")
	}
}

// The test page renders one text in both modes at once, so its derived detectors
// pin their own mode — reading the live one would render the same column twice. They
// must still follow a locale change, because the page's whole job is to say what the
// agent does to a text.
func TestADerivedDetectorPinsItsModeAndFollowsTheLocales(t *testing.T) {
	d := New(Config{Locales: []string{"fr"}})
	tokens := d.WithSubstitution(SubstitutionToken)
	fakes := d.WithSubstitution(SubstitutionFake)

	d.SetSubstitution(SubstitutionFake)
	if got := tokens.Substitution(); got != SubstitutionToken {
		t.Errorf("the token column followed the live mode: %v", got)
	}
	if got := fakes.Substitution(); got != SubstitutionFake {
		t.Errorf("the fake column is %v", got)
	}

	if err := d.SetLocales([]string{"fr", "gb"}); err != nil {
		t.Fatalf("set locales: %v", err)
	}
	for name, derived := range map[string]*Detector{"tokens": tokens, "fakes": fakes} {
		if got := len(derived.Scan("Payroll shows AB 12 34 56 C against that employee.")); got != 1 {
			t.Errorf("the %s column did not follow the locale change: %d matches", name, got)
		}
	}
}

// Locales and the mode are written while requests are reading them, which is the
// situation the atomic catalogue exists for.
func TestTheCatalogueIsSafeUnderConcurrency(t *testing.T) {
	d := New(Config{Locales: []string{"fr"}})

	var wg sync.WaitGroup
	for i := range 6 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for range 150 {
				switch i % 3 {
				case 0:
					_ = d.SetLocales([]string{"fr", "gb"})
				case 1:
					_ = d.SetLocales([]string{"us"})
				default:
					d.SetSubstitution(Substitution(i % 2))
				}
			}
		}(i)

		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 150 {
				d.MaskOnce("claire@example.fr, 06 12 34 56 78, AB 12 34 56 C")
				_ = d.Locales()
				_ = d.Substitution()
				_ = d.Categories()
				_ = d.Masking()
			}
		}()
	}
	wg.Wait()
}
