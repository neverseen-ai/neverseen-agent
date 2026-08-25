package pii

import "regexp"

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
// TODO: no UK street address. Its identifying part is the postcode, which is
// matched, and its street types are the same words the US set already lists —
// so this is a matter of assembling gbStreetType from usStreetType with the
// postcode as the tail, not of new research.

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
	gbPostcodeRe = regexp.MustCompile(`\b[A-Z]{1,2}\d[A-Z\d]?[ ]?\d[A-Z]{2}\b`)

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

// UnitedKingdomPatterns returns the UK set, checksummed and context-bearing
// shapes first so a tie resolves towards the candidate that had evidence.
func UnitedKingdomPatterns() []Pattern {
	return []Pattern{
		{Regex: gbNHSRe, Category: CatNHSNumber, Label: "NHS number"},
		{Regex: gbPostcodeRe, Category: CatPostalCode, Label: "UK postcode"},
		{Regex: gbPhoneRe, Category: CatPhone, Label: "UK telephone number"},
		{Regex: gbNINORe, Category: CatNINO, Label: "National Insurance number"},
	}
}
