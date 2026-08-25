package pii

import (
	"fmt"
	"regexp"
)

// The United Kingdom set.
//
// Two of these shapes are strong on their own — the NHS number carries a
// checksum, and a postcode's alternation of letters and digits is unlike
// anything else in prose. The National Insurance number has no checksum at all,
// so its letter rules carry it (see NINOCheck).
//
// TODO: no sort code. Its "12-34-56" shape is indistinguishable from a date, so
// it needs a context hint, which is the same work the bank-account shapes need;
// they are worth doing together.
//
// Fragments of the street address, named because the assembled expression is
// unreadable as one line.
const (
	// Street types, longest spelling first because Go's alternation is
	// leftmost-first: "Street" has to be offered before "St".
	//
	// Deliberately not the longest possible list. "Green", "View", "Rise",
	// "Hill", "Row" and "Walk" are all street types here and all ordinary words,
	// and in the position this pattern reads them — a number, a capitalised word,
	// then the type — they would claim phrases like "3 New Green". A missed
	// address is a gap; a masked sentence is a broken prompt, and the deployment
	// allow list cannot help with a shape rather than a value.
	//
	// "Dr" is left out for the same reason, since it is also a title, while
	// "Drive" is kept. "St" stays: "High St" is too common to lose, and its other
	// reading is harmless — in "12 St Albans Road" the saint is consumed as part
	// of the street name and the type is still "Road".
	gbStreetType = `Street|St|Road|Rd|Avenue|Ave|Lane|Close|Drive|Place|Court|` +
		`Crescent|Gardens|Terrace|Square|Mews|Grove|Parade|Way`

	// The postcode, shared with the pattern that matches one on its own so the
	// two cannot disagree about what a postcode looks like.
	gbPostcodeBody = `[A-Z]{1,2}\d[A-Z\d]?[ ]?\d[A-Z]{2}`

	// A town, and the reason its words are three characters or more.
	//
	// At two, the greedy repetition ate the letters off the front of the postcode
	// that follows: "10 Downing Street, London SW1A 2AA" matched only as far as
	// "London SW", leaving "1A 2AA" outside the span. RE2 has no lookahead to say
	// "not a postcode", and no UK town is two letters long.
	gbTown = `[A-Z][A-Za-z'’\-]{2,}`
)

var (
	// A street address: a number, one to four capitalised words, and a street
	// type — optionally continuing through the town and the postcode.
	//
	// The tail is what makes this worth having at all. The postcode alone nearly
	// identifies a UK address and is matched on its own, so an address pattern
	// that stopped at the street would leave the town in clear beside a masked
	// postcode. Taking all three means the span the reader sees replaced is the
	// thing that identifies the household.
	//
	// The house number may carry a letter, because "221B" is an address.
	gbAddressRe = regexp.MustCompile(
		`\b\d{1,5}[A-Z]?[ ]+(?:[A-Z][A-Za-z'’.\-]*[ ]+){1,4}(?:` + gbStreetType + `)\b\.?` +
			`(?:[ ]*,[ ]*` + gbTown + `(?:[ ]` + gbTown + `){0,2})?` +
			`(?:[ ]+` + gbPostcodeBody + `)?`)
)

// TODO: no sort code. Its "12-34-56" shape is indistinguishable from a date, so
// it needs a context hint — the same work the bank-account shapes need, and worth
// doing together.

var (
	// Ten digits, written in a 3-3-4 group or run together, with a mod-11 check
	// digit at the end.
	gbNHSRe = regexp.MustCompile(`\b\d{3}[ -]?\d{3}[ -]?\d{4}\b`)

	// Two letters, six digits, and an optional suffix letter, written in pairs
	// or run together. Deliberately loose: NINOCheck rejects the prefixes that
	// are never issued and the letters that never appear, which is what stops
	// this from claiming any two letters followed by six digits.
	//
	// The suffix and its separator are one optional group, not two. Written as
	// "[ -]?[A-D]?" the separator could match on its own, so the eight-character
	// form took the space after it into the span — and a replacement that eats
	// the following space runs the value into the next word.
	//
	// TODO: two letters and six digits with allowed letters is still a shape an
	// internal reference can take, and there is no checksum to settle it. The
	// deployment allow list is the escape hatch until one of the context words
	// ("NI number", "National Insurance") can be required instead.
	gbNINORe = regexp.MustCompile(`\b[A-Z]{2}[ -]?\d{2}[ -]?\d{2}[ -]?\d{2}(?:[ -]?[A-D])?\b`)

	// A full postcode: outward code, then the inward code's digit and two
	// letters. The six valid layouts are all covered by making the third
	// character optional and letting it be a letter or a digit.
	//
	// Uppercase only, like the French plate: a lowercase run of this shape is
	// far more likely to be an identifier or a fragment of prose. A full
	// postcode narrows to about fifteen addresses, which is why it is treated as
	// identifying on its own where a bare five-digit code is not.
	gbPostcodeRe = regexp.MustCompile(`\b` + gbPostcodeBody + `\b`)

	// A trunk 0 or +44, then the number in any of the groupings the UK uses:
	// 2+8 for London, 4+6 for most cities, 5+6 for mobiles.
	//
	// Digits are required immediately after the leading pair, which is what
	// keeps this off a date ("01-02-2024") and off an amount written in groups
	// ("01 234 567") — both of which put a separator exactly there.
	//
	// The trunk-zero form is two branches, and splitting them is the point. A
	// single loose branch with every separator optional claimed any run of nine
	// to eleven digits opening on a zero: with the US set also enabled, the
	// routing number 021000021 came out labelled as a London telephone number,
	// because both patterns score 90 and the earlier locale broke the tie. So a
	// spaced number must actually carry its first separator, and a number run
	// together must be the full ten or eleven digits.
	//
	// TODO: a handful of 016977-area numbers are nine digits in total and are
	// missed by the compact branch. They need their own alternative, or the
	// deployment's allow list turned inside out.
	gbPhoneRe = regexp.MustCompile(
		`\+44[ .-]?(?:\(0\)[ .-]?)?[1-9]\d{1,3}[ .-]?\d{3,4}[ .-]?\d{3,4}\b` + // international
			`|\b0[1-9]\d{1,3}[ .-]\d{3,4}[ .-]?\d{3,4}\b` + // grouped, first separator required
			`|\b0[1-9]\d{8,9}\b`) // run together: ten or eleven digits in all
)

// unitedKingdomFakes are the UK stand-ins. Each one lives somewhere its issuer
// never allocates, so it cannot be anybody's real value: an unissued National
// Insurance prefix, the pseudo-postcode the ONS uses for "not known", the
// numbers Ofcom reserves for drama, and an NHS number built to fail its own
// check digit.
var unitedKingdomFakes = map[Category]Generator{
	// A check digit deliberately not the one the first nine produce.
	CatNHSNumber: {Capacity: 999999999, Make: func(i int64) string {
		body := fmt.Sprintf("%09d", i)
		return body + invalidNHSCheckDigit(body)
	}},

	// ZZ is one of the prefixes never issued, which this catalogue's own check
	// also rejects — so the stand-in is not detected again on a second pass.
	CatNINO: {Capacity: 999999, Make: func(i int64) string {
		return fmt.Sprintf("ZZ %02d %02d %02d A", i/10000%100, i/100%100, i%100)
	}},

	// ZZ99 is the Office for National Statistics pseudo-postcode for an unknown
	// address, so it is never a place.
	CatPostalCode: {Capacity: 10 * 26 * 26, Make: func(i int64) string {
		i--
		return fmt.Sprintf("ZZ99 %d%c%c", i/676, 'A'+byte(i/26%26), 'A'+byte(i%26))
	}},

	// 07700 900000-900999 is the Ofcom range reserved for drama, so no number in
	// it reaches a subscriber.
	CatPhone: {Capacity: 1000, Make: func(i int64) string {
		return fmt.Sprintf("07700 900%03d", i-1)
	}},

	// The pseudo-postcode again, so the stand-in address cannot be a place
	// either.
	CatAddress: {Capacity: 9999, Make: func(i int64) string {
		return fmt.Sprintf("%d Example Street, Anytown ZZ99 3CZ", i)
	}},
}

// invalidNHSCheckDigit renders a digit that is deliberately not the one body
// produces, so the number fails its own mod-11 checksum.
//
// A weighted sum whose remainder gives 10 has no valid check digit at all, so
// the number is already unusable and any digit will do.
func invalidNHSCheckDigit(body string) string {
	sum := 0
	for i := range 9 {
		sum += int(body[i]-'0') * (10 - i)
	}
	switch check := 11 - sum%11; check {
	case 10:
		return "0"
	case 11:
		return "1" // the valid digit would be 0
	default:
		return invalidCheckDigit(check)
	}
}

// UnitedKingdomPatterns returns the UK set, checksummed and context-bearing
// shapes first so a tie resolves towards the candidate that had evidence.
func UnitedKingdomPatterns() []Pattern {
	return []Pattern{
		{Regex: gbNHSRe, Category: CatNHSNumber, Label: "NHS number"},
		// Before the postcode, so that when both claim the same stretch the
		// longer, more specific span is the one already in front.
		{Regex: gbAddressRe, Category: CatAddress, Label: "UK street address"},
		{Regex: gbPostcodeRe, Category: CatPostalCode, Label: "UK postcode"},
		{Regex: gbPhoneRe, Category: CatPhone, Label: "UK telephone number"},
		{Regex: gbNINORe, Category: CatNINO, Label: "National Insurance number"},
	}
}
