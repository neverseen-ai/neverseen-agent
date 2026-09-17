package pii

import (
	"strings"
	"testing"
)

// A stand-in has two jobs, and they pull against each other: it has to keep the
// shape of what it replaces, so the prompt reads as prose, and it must never be
// able to be somebody's real value. These tests are about the second one, which
// is the half that would fail silently — a generator producing plausible,
// possibly-real values looks exactly like one producing safe ones.

// Every checksummed category's stand-in must fail its own checksum. That is what
// makes it unattributable by construction rather than by luck.
func TestStandInsFailTheirOwnChecksum(t *testing.T) {
	tests := []struct {
		cat    Category
		locale string
		verify func(string) bool
	}{
		{CatCreditCard, "", LuhnCheck},
		{CatIBAN, "", IBANCheck},
		{CatNIR, "fr", NIRCheck},
		{CatSIREN, "fr", SIRENCheck},
		{CatSIRET, "fr", SIRETCheck},
		{CatNHSNumber, "gb", NHSNumberCheck},
		{CatNINO, "gb", NINOCheck},
		{CatSSN, "us", SSNCheck},
		{CatRoutingNumber, "us", RoutingNumberCheck},
	}

	for _, tt := range tests {
		t.Run(string(tt.cat), func(t *testing.T) {
			// Several indices, because a generator can be wrong for one and right
			// for the next — the check digit it has to avoid moves with the body.
			//
			// A sweep and not a handful, because the handful missed one: the IBAN
			// stand-in cleared mod-97 at every ninety-seventh index — 10, 107, 204
			// — and none of the five listed values landed on one, so the whole of
			// fake mode emitted a re-detectable account for eight years of indices
			// under a green test.
			indices := []int64{42, 1000}
			for i := int64(1); i <= 200; i++ {
				indices = append(indices, i)
			}

			for _, index := range indices {
				value, ok := FakeValue(tt.cat, tt.locale, index)
				if !ok {
					t.Fatalf("no stand-in for index %d", index)
				}
				if tt.verify(value) {
					t.Errorf("the stand-in %q for index %d passes its own checksum, so it could be "+
						"somebody's real value", value, index)
				}
			}
		})
	}
}

// The categories whose safety comes from a reserved range rather than a checksum.
// Each row names the range and why it can never reach anyone.
func TestStandInsUseReservedRanges(t *testing.T) {
	tests := []struct {
		cat    Category
		want   string // substring the stand-in must carry
		reason string
	}{
		{CatEmail, "@example.org", "RFC 2606 reserves example.org, so it can never be registered"},
		{CatIPAddr, "192.0.2.", "RFC 5737 reserves this block for documentation"},
		{CatPhone, "06 39 98 ", "ARCEP reserves this block for fiction"},
		{CatPostalCode, "99000 ", "French departments stop at 98"},
		{CatGeoPoint, "geo:0.", "open ocean off Null Island — geography has no unallocated range, so the only point that cannot be somebody's is one nobody lives at"},
	}

	for _, tt := range tests {
		t.Run(string(tt.cat), func(t *testing.T) {
			// Asked of the French set, which is where the national stand-ins for
			// these come from.
			value, ok := NewFakeSet([]string{"fr"}).Value(tt.cat, "fr", 1)
			if !ok {
				t.Fatalf("no stand-in for %s", tt.cat)
			}
			if !strings.Contains(value, tt.want) {
				t.Errorf("the stand-in %q does not use the reserved range %q — %s", value, tt.want, tt.reason)
			}
		})
	}

	t.Run("the UK and US ranges", func(t *testing.T) {
		gb := NewFakeSet([]string{"gb"})
		us := NewFakeSet([]string{"us"})

		for _, tt := range []struct {
			set    FakeSet
			locale string
			cat    Category
			want   string
		}{
			{gb, "gb", CatPostalCode, "ZZ99 "},  // the ONS pseudo-postcode for "not known"
			{gb, "gb", CatPhone, "07700 900"},   // the Ofcom drama range
			{us, "us", CatEIN, "00-"},           // not an assigned IRS campus prefix
			{us, "us", CatPhone, "555-01"},      // reserved for fiction
			{us, "us", CatPostalCode, "00000-"}, // not an assigned ZIP
			{us, "us", CatAddress, "IL 00000"},  // same, inside an address
		} {
			value, ok := tt.set.Value(tt.cat, tt.locale, 1)
			if !ok {
				t.Fatalf("no stand-in for %s", tt.cat)
			}
			if !strings.Contains(value, tt.want) {
				t.Errorf("%s stand-in %q does not use the reserved range %q", tt.cat, value, tt.want)
			}
		}
	})
}

// A stand-in has to be the right length, and this is the half that fails
// quietly: a wrong length is still unattributable, it just stops reading as the
// thing it replaced — which is the entire reason for preferring a stand-in over a
// bracket token. Two of these were wrong until a page rendered them side by side
// with the originals: an eighteen-digit payment card, and a twenty-three
// character IBAN.
//
// Written out rather than derived. Deriving the expected length from the
// generator would agree with whatever the generator does.
func TestStandInsHaveTheRightLength(t *testing.T) {
	tests := []struct {
		cat    Category
		locale string
		digits int
		what   string
	}{
		{CatCreditCard, "", 16, "a Visa"},
		{CatNIR, "fr", 15, "a French social security number"},
		{CatSIREN, "fr", 9, "a SIREN"},
		{CatSIRET, "fr", 14, "a SIRET"},
		{CatNHSNumber, "gb", 10, "an NHS number"},
		{CatSSN, "us", 9, "a US social security number"},
		{CatEIN, "us", 9, "an employer identification number"},
		{CatRoutingNumber, "us", 9, "an ABA routing number"},
	}

	for _, tt := range tests {
		t.Run(string(tt.cat), func(t *testing.T) {
			for _, index := range []int64{1, 7, 1234} {
				value, ok := FakeValue(tt.cat, tt.locale, index)
				if !ok {
					t.Fatalf("no stand-in for index %d", index)
				}
				if got := countDigits(value); got != tt.digits {
					t.Errorf("the stand-in %q carries %d digits, want %d — %s has %d",
						value, got, tt.digits, tt.what, tt.digits)
				}
			}
		})
	}

	// The IBAN is counted in characters rather than digits: its country code and
	// check digits are part of the length the standard fixes.
	t.Run(string(CatIBAN), func(t *testing.T) {
		value, ok := FakeValue(CatIBAN, "", 1)
		if !ok {
			t.Fatal("no IBAN stand-in")
		}
		if len(value) != 27 {
			t.Errorf("the stand-in %q is %d characters, want 27 — the length of a French IBAN",
				value, len(value))
		}
	})
}

func countDigits(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// everyGenerator enumerates each generator once per locale that owns it, plus
// the shared ones, so a sweep covers a locale's table rather than only whichever
// one a merged map happened to keep.
func everyGenerator() []struct {
	cat    Category
	locale string
	gen    Generator
} {
	var out []struct {
		cat    Category
		locale string
		gen    Generator
	}
	add := func(cat Category, locale string, gen Generator) {
		out = append(out, struct {
			cat    Category
			locale string
			gen    Generator
		}{cat, locale, gen})
	}

	for cat, gen := range fakeGenerators {
		add(cat, "", gen)
	}
	for _, l := range Locales() {
		for cat, gen := range l.Fakes {
			add(cat, l.Code, gen)
		}
	}
	return out
}

// Indexed, never random: the same index always gives the same value, and two
// indices never give the same one. Both halves matter — the first is what makes
// a value keep one identity across a conversation, the second is what keeps two
// people from becoming one.
func TestStandInsAreIndexedAndDistinct(t *testing.T) {
	set := NewFakeSet(LocaleCodes())

	for _, probe := range everyGenerator() {
		cat, locale, gen := probe.cat, probe.locale, probe.gen
		t.Run(string(cat)+"/"+locale, func(t *testing.T) {
			if gen.Capacity < 1 {
				t.Fatalf("capacity is %d: the generator can produce nothing", gen.Capacity)
			}

			// Bounded, because some capacities are in the millions.
			bound := min(gen.Capacity, 500)

			seen := make(map[string]int64, bound)
			for i := int64(1); i <= bound; i++ {
				value, ok := set.Value(cat, locale, i)
				if !ok {
					t.Fatalf("index %d is within capacity %d but produced nothing", i, gen.Capacity)
				}
				if again, _ := set.Value(cat, locale, i); again != value {
					t.Fatalf("index %d produced %q then %q: the generator is not a function of its index",
						i, value, again)
				}
				if other, clash := seen[value]; clash {
					t.Fatalf("indices %d and %d both produce %q: two originals would share a stand-in",
						other, i, value)
				}
				seen[value] = i
			}
		})
	}
}

// Past capacity the index would wrap and two originals would share a stand-in,
// which is the one failure the design exists to prevent. Reporting nothing is
// what sends the caller to a bracket token instead.
func TestStandInsRefuseToWrapPastCapacity(t *testing.T) {
	set := NewFakeSet(LocaleCodes())

	gen, ok := set.shared[CatIPAddr]
	if !ok {
		t.Fatal("no IP stand-in to test the bound with")
	}
	if _, ok := set.Value(CatIPAddr, "", gen.Capacity); !ok {
		t.Errorf("the last index within capacity %d produced nothing", gen.Capacity)
	}
	if value, ok := set.Value(CatIPAddr, "", gen.Capacity+1); ok {
		t.Errorf("index %d is past capacity %d but produced %q", gen.Capacity+1, gen.Capacity, value)
	}
	if _, ok := set.Value(CatIPAddr, "", 0); ok {
		t.Error("index 0 produced a value; indices start at 1")
	}
}

// No credential gets a stand-in. One that looks like a working key is a thing
// somebody will try to use, and the bracket-token fallback is both safe and
// obviously not data.
func TestCredentialsHaveNoStandIn(t *testing.T) {
	set := NewFakeSet(LocaleCodes())

	for _, cat := range Categories() {
		if IsSecret(cat) {
			if ok := set.Has(cat, ""); ok {
				t.Errorf("%s has a generator: a stand-in that looks like a working credential "+
					"is worse than a token", cat)
			}
		}
	}
}

// A locale's own generator wins over a locale-independent one of the same
// category. Without that, a British telephone number stood in for a French one —
// the machine artefact fake mode exists to avoid.
func TestLocaleStandInsOverrideTheSharedOnes(t *testing.T) {
	// All three enabled at once, which is the configuration that used to break:
	// three locales contribute a PHONE generator, and merging them into one table
	// meant whichever loaded last won for all of them.
	all := NewFakeSet(LocaleCodes())

	fr, _ := all.Value(CatPhone, "fr", 1)
	gb, _ := all.Value(CatPhone, "gb", 1)
	us, _ := all.Value(CatPhone, "us", 1)

	if fr == gb || gb == us || fr == us {
		t.Errorf("two locales produce the same telephone stand-in: fr=%q gb=%q us=%q", fr, gb, us)
	}
	if !strings.HasPrefix(fr, "06 ") {
		t.Errorf("the French stand-in %q does not read as a French number", fr)
	}
	if !strings.HasPrefix(gb, "07700") {
		t.Errorf("the UK stand-in %q does not read as a UK number", gb)
	}
	if !strings.HasPrefix(us, "(555)") {
		t.Errorf("the US stand-in %q does not read as a US number", us)
	}

	// The other two shared categories, which failed the same way. A page showed
	// a French address replaced by "1 Example Street, Anytown, IL 00000".
	for _, tt := range []struct {
		cat            Category
		locale, prefix string
	}{
		{CatAddress, "fr", "1 rue"},
		{CatAddress, "us", "1 Example Street"},
		{CatPostalCode, "fr", "99000"},
		{CatPostalCode, "gb", "ZZ99"},
		{CatPostalCode, "us", "00000-"},
	} {
		value, ok := all.Value(tt.cat, tt.locale, 1)
		if !ok {
			t.Errorf("no %s stand-in for locale %q", tt.cat, tt.locale)
			continue
		}
		if !strings.HasPrefix(value, tt.prefix) {
			t.Errorf("the %s stand-in for %q is %q, which does not read as that country's",
				tt.cat, tt.locale, value)
		}
	}
}

// A category with no national table falls back to the shared generator, whatever
// locale recognised it: a date is a date.
func TestSharedStandInsAreUsedWhenALocaleHasNone(t *testing.T) {
	all := NewFakeSet(LocaleCodes())

	for _, locale := range append([]string{""}, LocaleCodes()...) {
		value, ok := all.Value(CatDOB, locale, 1)
		if !ok {
			t.Errorf("no date stand-in for locale %q", locale)
			continue
		}
		if !strings.Contains(value, "1900") {
			t.Errorf("the date stand-in for %q is %q, not the shared one", locale, value)
		}
	}
}

// A date stand-in is written the way the locale that recognised it writes dates.
//
// The failure this holds is the one the per-locale tables were built for, which
// the telephone number had already demonstrated: with a single shared generator, a
// date matched by the US pattern came back day-first, so "03/14/1987" became
// "28/12/1900" — a month of 28, which is not a date anybody writes — and an ISO
// date came back with slashes. Fake mode exists to hand the model prose; a stand-in
// in the wrong notation is the machine artefact it was meant to avoid.
func TestDateStandInsFollowTheNotationOfTheirLocale(t *testing.T) {
	// The index is past twelve deliberately. Below it, day and month are both
	// small and every notation reads the same — which is exactly how a stand-in in
	// the wrong order goes unnoticed.
	const index = 336

	for _, tc := range []struct {
		locale string
		want   string
	}{
		// The locale-independent pattern is the ISO one, so its stand-in is ISO.
		{"", "1900-12-28"},
		{"fr", "28/12/1900"},
		{"gb", "28/12/1900"},
		{"us", "12/28/1900"},
	} {
		name := tc.locale
		if name == "" {
			name = "locale-independent"
		}
		t.Run(name, func(t *testing.T) {
			got, ok := FakeValue(CatDOB, tc.locale, index)
			if !ok {
				t.Fatalf("no stand-in for a date recognised by %q", tc.locale)
			}
			if got != tc.want {
				t.Errorf("a date recognised by %q stands in as %q, want %q — a notation "+
					"this locale does not write is a value the model reads as machine output",
					tc.locale, got, tc.want)
			}
		})
	}

	// Every rendering of one index is the same day, so two locales loaded at once
	// cannot mint two different stand-ins for one original.
	for _, locale := range append([]string{""}, LocaleCodes()...) {
		got, ok := FakeValue(CatDOB, locale, index)
		if !ok {
			t.Fatalf("no stand-in for %q", locale)
		}
		if !strings.Contains(got, "12") || !strings.Contains(got, "28") {
			t.Errorf("index %d renders as %q for %q, which is not the same day as the others",
				index, got, locale)
		}
	}
}
