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
		verify func(string) bool
	}{
		{CatCreditCard, LuhnCheck},
		{CatIBAN, IBANCheck},
		{CatNIR, NIRCheck},
		{CatSIREN, SIRENCheck},
		{CatSIRET, SIRETCheck},
		{CatNHSNumber, NHSNumberCheck},
		{CatNINO, NINOCheck},
		{CatSSN, SSNCheck},
		{CatRoutingNumber, RoutingNumberCheck},
	}

	for _, tt := range tests {
		t.Run(string(tt.cat), func(t *testing.T) {
			// Several indices, because a generator can be wrong for one and right
			// for the next — the check digit it has to avoid moves with the body.
			for _, index := range []int64{1, 2, 3, 42, 1000} {
				value, ok := FakeValue(tt.cat, index)
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
	}

	for _, tt := range tests {
		t.Run(string(tt.cat), func(t *testing.T) {
			// Asked of the French set, which is where the national stand-ins for
			// these come from.
			value, ok := NewFakeSet([]string{"fr"}).Value(tt.cat, 1)
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
			set  FakeSet
			cat  Category
			want string
		}{
			{gb, CatPostalCode, "ZZ99 "},  // the ONS pseudo-postcode for "not known"
			{gb, CatPhone, "07700 900"},   // the Ofcom drama range
			{us, CatEIN, "00-"},           // not an assigned IRS campus prefix
			{us, CatPhone, "555-01"},      // reserved for fiction
			{us, CatPostalCode, "00000-"}, // not an assigned ZIP
			{us, CatAddress, "IL 00000"},  // same, inside an address
		} {
			value, ok := tt.set.Value(tt.cat, 1)
			if !ok {
				t.Fatalf("no stand-in for %s", tt.cat)
			}
			if !strings.Contains(value, tt.want) {
				t.Errorf("%s stand-in %q does not use the reserved range %q", tt.cat, value, tt.want)
			}
		}
	})
}

// Indexed, never random: the same index always gives the same value, and two
// indices never give the same one. Both halves matter — the first is what makes
// a value keep one identity across a conversation, the second is what keeps two
// people from becoming one.
func TestStandInsAreIndexedAndDistinct(t *testing.T) {
	set := NewFakeSet(LocaleCodes())

	for cat, gen := range set {
		t.Run(string(cat), func(t *testing.T) {
			if gen.Capacity < 1 {
				t.Fatalf("capacity is %d: the generator can produce nothing", gen.Capacity)
			}

			// Bounded, because some capacities are in the millions.
			probe := min(gen.Capacity, 500)

			seen := make(map[string]int64, probe)
			for i := int64(1); i <= probe; i++ {
				value, ok := set.Value(cat, i)
				if !ok {
					t.Fatalf("index %d is within capacity %d but produced nothing", i, gen.Capacity)
				}
				if again, _ := set.Value(cat, i); again != value {
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

	gen, ok := set[CatIPAddr]
	if !ok {
		t.Fatal("no IP stand-in to test the bound with")
	}
	if _, ok := set.Value(CatIPAddr, gen.Capacity); !ok {
		t.Errorf("the last index within capacity %d produced nothing", gen.Capacity)
	}
	if value, ok := set.Value(CatIPAddr, gen.Capacity+1); ok {
		t.Errorf("index %d is past capacity %d but produced %q", gen.Capacity+1, gen.Capacity, value)
	}
	if _, ok := set.Value(CatIPAddr, 0); ok {
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
			if _, ok := set[cat]; ok {
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
	fr, _ := NewFakeSet([]string{"fr"}).Value(CatPhone, 1)
	gb, _ := NewFakeSet([]string{"gb"}).Value(CatPhone, 1)
	us, _ := NewFakeSet([]string{"us"}).Value(CatPhone, 1)

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
}
