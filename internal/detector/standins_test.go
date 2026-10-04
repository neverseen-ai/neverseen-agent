package detector

import (
	"slices"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
)

// TestStandInsADetectorFindsAgain records which stand-ins the detector reports again
// when one comes back through it, and why each of those is acceptable.
//
// The comment over the US stand-ins once said this catalogue's own checks reject every
// one of them. It held for the checksummed categories and not for the rest, and
// nothing said so until a review scanned them. Not a leak — a stand-in of a stand-in
// is still not anybody's data — but a property claimed and not held is how a real one
// goes unnoticed. So the set is written down: a category that starts being found again,
// or stops, fails here and has to be explained.
//
// A stand-in that fails its own pattern is not always possible: a telephone number in
// a block reserved for fiction is still a telephone number. What keeps each of them
// nobody's is the reserved range, held by pii.TestStandInsUseReservedRanges and its
// neighbours, not the detector.
func TestStandInsADetectorFindsAgain(t *testing.T) {
	every := pii.LocaleCodes()

	// Category → the locales whose detector finds its stand-in again.
	want := map[pii.Category][]string{
		// A date is a date: 1900 is in the past, which is the one thing DOBCheck asks.
		pii.CatDOB: every,
		// Null Island is a coordinate like any other.
		pii.CatGeoPoint: every,
		// Any 24 hex digits are the shape of an ObjectId, and there is no check digit.
		pii.CatMongoID: every,
		// Blocks reserved for fiction — ARCEP's 06 39 98, Ofcom's 07700 900, the FCC's
		// 555-01 — are telephone numbers by every rule a pattern can read.
		pii.CatPhone: {"fr", "gb", "us"},
		// A street address whose postcode is unallocated is still an address.
		pii.CatAddress: {"fr", "gb", "us"},
		// ZZ99 is the NHS's pseudo-postcode and 00000 a ZIP nobody is given; both
		// keep the national layout, which is all the pattern knows.
		pii.CatPostalCode: {"gb", "us"},
		// Campus prefix 00 is never assigned, and the pattern reads the shape, not
		// the prefix.
		pii.CatEIN: {"us"},
	}

	got := map[pii.Category][]string{}
	for _, locale := range every {
		d := New(Config{Locales: []string{locale}})
		for _, cat := range pii.Categories() {
			// Two indices, because one generator can be caught at one and not the next.
			for _, index := range []int64{1, 42} {
				value, ok := pii.FakeValue(cat, locale, index)
				if !ok {
					continue
				}
				for _, m := range d.Scan(value) {
					if m.Category != cat {
						t.Errorf("the %s stand-in %q is found again as %s under %s: a stand-in "+
							"read as another category is restored as the wrong thing", cat, value, m.Category, locale)
						continue
					}
					if !slices.Contains(got[cat], locale) {
						got[cat] = append(got[cat], locale)
					}
				}
			}
		}
	}

	for cat, locales := range want {
		if !slices.Equal(got[cat], locales) {
			t.Errorf("%s stand-ins are found again under %v, recorded as %v — explain the change "+
				"here, beside the reason", cat, got[cat], locales)
		}
	}
	for cat, locales := range got {
		if _, recorded := want[cat]; !recorded {
			t.Errorf("%s stand-ins are found again under %v and nothing here says why that is "+
				"acceptable", cat, locales)
		}
	}
}
