package pii

import (
	"reflect"
	"testing"
)

// The registry decides which patterns load and in which order, and load order is
// correctness rather than taste: France's checksummed identifiers have to reach
// a number before Vietnam's broader ones, whose old identity card matches any
// bare nine digits.

func TestLocaleRegistryIsComplete(t *testing.T) {
	if len(localeRegistry) == 0 {
		t.Fatal("the registry is empty: no country pattern set could ever load")
	}

	codes := map[string]bool{}
	priorities := map[int]string{}

	for _, l := range localeRegistry {
		if l.Code == "" {
			t.Error("a locale has no code: nothing could name it in a configuration")
		}
		if codes[l.Code] {
			t.Errorf("locale %q is registered twice", l.Code)
		}
		codes[l.Code] = true

		// A tie leaves the load order down to the slice order, which is
		// explicitly documented as not meaningful — so the set that claims a
		// number first would change with an unrelated edit.
		if other, taken := priorities[l.Priority]; taken {
			t.Errorf("locales %q and %q share priority %d: their load order would be undefined, "+
				"and load order decides which set claims a number first", other, l.Code, l.Priority)
		}
		priorities[l.Priority] = l.Code

		if l.Patterns == nil {
			t.Errorf("locale %q has no pattern set", l.Code)
			continue
		}
		if len(l.Patterns()) == 0 {
			t.Errorf("locale %q contributes no patterns: selecting it would do nothing", l.Code)
		}
	}
}

func TestLocalesAreSortedByPriority(t *testing.T) {
	locales := Locales()
	for i := 1; i < len(locales); i++ {
		if locales[i-1].Priority > locales[i].Priority {
			t.Fatalf("Locales() is not in load order: %q (%d) came before %q (%d)",
				locales[i-1].Code, locales[i-1].Priority, locales[i].Code, locales[i].Priority)
		}
	}

	// Not a style assertion: the order decides which country names a value two
	// of them could both claim. Nine bare digits are a French SIREN under Luhn
	// and a US routing number under the ABA weights.
	if got, want := LocaleCodes(), []string{"fr", "gb", "us"}; !reflect.DeepEqual(got, want) {
		t.Errorf("LocaleCodes() = %v, want %v", got, want)
	}
}

func TestLocalePatterns(t *testing.T) {
	t.Run("selecting nothing loads nothing", func(t *testing.T) {
		if got := LocalePatterns(nil); len(got) != 0 {
			t.Errorf("LocalePatterns(nil) returned %d patterns, want none", len(got))
		}
	})

	t.Run("an unknown code is skipped rather than guessed at", func(t *testing.T) {
		// The caller validates the selection; this must not decide policy about
		// an invalid one.
		if got := LocalePatterns([]string{"zz"}); len(got) != 0 {
			t.Errorf("LocalePatterns([zz]) returned %d patterns, want none", len(got))
		}
	})

	t.Run("several locales load in registry order, not argument order", func(t *testing.T) {
		// Asked for in the wrong order on purpose: the caller's spelling of the
		// list must not be able to reorder the catalogue.
		got := LocalePatterns([]string{"us", "gb", "fr"})

		want := FrancePatterns()
		want = append(want, UnitedKingdomPatterns()...)
		want = append(want, UnitedStatesPatterns()...)
		if len(got) != len(want) {
			t.Fatalf("got %d patterns, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i].Category != want[i].Category || got[i].Label != want[i].Label {
				t.Fatalf("pattern %d is %q (%s), want %q (%s) — the registry's order, not the caller's",
					i, got[i].Category, got[i].Label, want[i].Category, want[i].Label)
			}
		}
	})
}

func TestLocaleByCode(t *testing.T) {
	if l, ok := LocaleByCode("fr"); !ok || l.Code != "fr" {
		t.Errorf(`LocaleByCode("fr") = %+v, %v; want the French locale`, l, ok)
	}
	if _, ok := LocaleByCode("zz"); ok {
		t.Error(`LocaleByCode("zz") reported an unregistered locale as known`)
	}
}

// Every locale's patterns must be in the full catalogue. AllPatterns reads the
// registry precisely so that a locale cannot be added without appearing there —
// the hand-written version it replaces had shipped with a set missing, which
// exempted two thirds of the categories from every test that swept the
// catalogue.
func TestAllPatternsCoversEveryLocale(t *testing.T) {
	all := AllPatterns()

	labels := map[string]bool{}
	for _, p := range all {
		labels[p.Label] = true
	}

	for _, l := range Locales() {
		for _, p := range l.Patterns() {
			if !labels[p.Label] {
				t.Errorf("pattern %q of locale %q is missing from AllPatterns()", p.Label, l.Code)
			}
		}
	}

	// And the locale-independent sets, which are what a deployment selecting no
	// country still scans.
	for _, p := range append(InternationalPatterns(), SecretPatterns()...) {
		if !labels[p.Label] {
			t.Errorf("locale-independent pattern %q is missing from AllPatterns()", p.Label)
		}
	}
}
