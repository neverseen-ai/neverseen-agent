package pii

import "fmt"

// Fake mode replaces a value with a stand-in of the same shape instead of a
// bracket token, so the prompt reaches the model as prose rather than as a form
// with holes punched in it. A model reasons better about "write to
// contact7@example.org" than about "write to [EMAIL_1]".
//
// Two rules make it safe.
//
// **Indexed, never random.** A generator is a pure function of an index, and the
// index comes from the same per-category counter that numbers tokens. So the
// mapping is still one-to-one and still reversible, and the same value gets the
// same stand-in for as long as the session lives. Randomness would break both.
//
// **Unattributable by construction.** A stand-in must never be able to be
// somebody's real value. Where a shape carries a checksum, the stand-in is built
// to fail it — a card whose Luhn digit is deliberately wrong cannot be anyone's
// card. Where a shape has a range its issuer never allocates, the stand-in lives
// there: an unissued National Insurance prefix, a social security area of 000, a
// Federal Reserve district that does not exist, the numbers Ofcom and the FCC
// reserve for fiction, the domains and IP blocks the RFCs reserve for
// documentation. A "plausible" stand-in that could belong to a real person would
// turn masking into fabrication.
//
// Credentials get no generators at all. A stand-in that looks like a working API
// key is a thing somebody will try to use, and a category with no generator
// falls back to a bracket token — safe, never a leak, and obviously not data.

// Generator makes the stand-in for one category, and declares how many distinct
// values it can produce.
//
// Capacity is the span the index is permuted over. Past it the index would wrap
// and two different originals would share a stand-in, which is the one failure
// the indexed design exists to prevent — so FakeValue reports nothing instead,
// and the caller falls back to a bracket token.
type Generator struct {
	Make     func(index int64) string
	Capacity int64
}

// FakeSet is the generators available to a deployment: the locale-independent
// ones, and each selected locale's own kept separately.
//
// Separately, and that is the whole point. Merging them into one table means a
// shared category — a telephone number, an address, a postcode — resolves to
// whichever locale was merged last, so with France, the UK and the US all
// enabled a French number came out as "(555) 555-0100". Which stand-in is right
// depends on which country's pattern recognised the value, not on the order the
// tables were built in.
type FakeSet struct {
	shared   map[Category]Generator
	byLocale map[string]map[Category]Generator
}

// NewFakeSet resolves the generators for a set of locales.
func NewFakeSet(locales []string) FakeSet {
	set := FakeSet{
		shared:   fakeGenerators,
		byLocale: make(map[string]map[Category]Generator, len(locales)),
	}

	wanted := make(map[string]bool, len(locales))
	for _, code := range locales {
		wanted[code] = true
	}
	for _, l := range Locales() {
		if wanted[l.Code] && len(l.Fakes) > 0 {
			set.byLocale[l.Code] = l.Fakes
		}
	}
	return set
}

// Value returns the stand-in for the index-th value of a category, recognised by
// a given locale — "" for the locale-independent sets.
//
// The locale's own generator wins, then the shared one. False means the caller
// must fall back to a bracket token: either nothing generates this category, or
// the index has run past what its generator can produce without repeating
// itself.
func (s FakeSet) Value(cat Category, locale string, index int64) (string, bool) {
	gen, ok := s.byLocale[locale][cat]
	if !ok {
		gen, ok = s.shared[cat]
	}
	if !ok || index < 1 || index > gen.Capacity {
		return "", false
	}
	return gen.Make(index), true
}

// Has reports whether a category recognised by a locale has a stand-in at all,
// for the page that explains why some values keep a bracket token even in fake
// mode.
func (s FakeSet) Has(cat Category, locale string) bool {
	if _, ok := s.byLocale[locale][cat]; ok {
		return true
	}
	_, ok := s.shared[cat]
	return ok
}

// FakeValue is Value with every registered locale available. It is what the
// tests ask, so a locale's generators are exercised whatever a deployment
// selects; a running agent asks the FakeSet its own locales resolve to.
func FakeValue(cat Category, locale string, index int64) (string, bool) {
	return NewFakeSet(LocaleCodes()).Value(cat, locale, index)
}

// fakeGenerators are the stand-ins for shapes that mean the same everywhere.
var fakeGenerators = map[Category]Generator{
	// example.org is reserved by RFC 2606 and can never be registered, so no
	// address built on it can reach anybody.
	CatEmail: {Capacity: 999999, Make: func(i int64) string {
		return fmt.Sprintf("contact%d@example.org", i)
	}},

	// The three documentation blocks of RFC 5737, which are never routed.
	CatIPAddr: {Capacity: 3 * 256, Make: func(i int64) string {
		blocks := [...]string{"192.0.2", "198.51.100", "203.0.113"}
		i--
		return fmt.Sprintf("%s.%d", blocks[i/256], i%256)
	}},

	// A Visa-shaped number whose Luhn digit is deliberately wrong, so it cannot
	// be a card that was ever issued.
	//
	// Sixteen digits, because that is what a Visa has. Getting the length wrong
	// does not make the stand-in unsafe, it makes it stop reading as a card —
	// which is the whole reason for preferring one over a bracket token.
	CatCreditCard: {Capacity: 999999999, Make: func(i int64) string {
		body := fmt.Sprintf("400000%09d", i) // fifteen digits, before the check digit
		return body + invalidCheckDigit(luhnCheckDigit(body))
	}},

	// IBAN check digits run from 02 to 98, so "00" is a value the standard cannot
	// produce. Twenty-seven characters, the length of a French IBAN, for the same
	// reason the card is sixteen digits.
	CatIBAN: {Capacity: 99999999, Make: func(i int64) string {
		return fmt.Sprintf("FR00%023d", i)
	}},

	// A date in a fixed fictional decade. Any date belongs to somebody, so what
	// makes this safe is that it is not the one the caller wrote.
	CatDOB: {Capacity: 28 * 12, Make: func(i int64) string {
		i--
		return fmt.Sprintf("%02d/%02d/1900", i%28+1, i/28+1)
	}},

	// Twenty-four hex characters opening on a run of zeroes, which a real
	// ObjectId — whose first four bytes are a timestamp — never does.
	CatMongoID: {Capacity: 99999999, Make: func(i int64) string {
		return fmt.Sprintf("000000000000000%09x", i)
	}},
}

// luhnCheckDigit returns the digit that would make body pass the Luhn checksum.
func luhnCheckDigit(body string) int {
	sum, alt := 0, true // the check digit is not yet appended, so body's last is doubled
	for i := len(body) - 1; i >= 0; i-- {
		d := int(body[i] - '0')
		if alt {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return (10 - sum%10) % 10
}

// invalidCheckDigit renders any digit other than the correct one, so the value
// it completes is guaranteed to fail its checksum.
func invalidCheckDigit(correct int) string {
	return fmt.Sprint((correct + 1) % 10)
}
