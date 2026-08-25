package pii

import (
	"fmt"
	"regexp"
)

// The United States set.
//
// Almost nothing here carries a real checksum — the routing number is the
// exception — so the work is done by ranges the issuer never allocates (the
// social security number) and by the punctuation the identifier is always
// written with (the employer id).
//
// TODO: no driver's licence. The format is set per state, fifty of them, with
// no shared shape and no checksum; it needs a per-state table, which is a
// research task rather than a pattern.

const (
	// Street types, with the abbreviations postal addresses actually use.
	// Longest first, because Go's alternation is leftmost-first: "Street" has
	// to be offered before "St".
	usStreetType = `Street|St|Avenue|Ave|Boulevard|Blvd|Road|Rd|Drive|Dr|Lane|Ln|` +
		`Court|Ct|Place|Pl|Terrace|Ter|Parkway|Pkwy|Circle|Cir|Highway|Hwy|Way`

	// The two-letter state abbreviations, all fifty plus DC and the territories
	// that use the same postal format. It is a list rather than [A-Z]{2} because
	// two arbitrary uppercase letters before five digits is a shape that reads
	// far too often in prose and in part numbers.
	usState = `A[KLRZ]|C[AOT]|D[CE]|FL|GA|HI|I[ADLN]|K[SY]|LA|M[ADEINOST]|` +
		`N[CDEHJMVY]|OH|OK|OR|PA|RI|S[CD]|T[NX]|UT|V[AT]|W[AIVY]|` +
		`AS|GU|MP|PR|VI`

	// Date fragments. The leading zero is optional on both the month and the day,
	// because a form field writes "3/14/1987" as readily as "03/14/1987", and the
	// year is anchored to 19xx/20xx so three loose numbers cannot read as a date.
	usMonth = `(?:0?[1-9]|1[0-2])`
	usDay   = `(?:0?[1-9]|[12]\d|3[01])`
	usYear  = `(?:19|20)\d{2}`
)

var (
	// Three digits, two, four. The dashes are how it is always written; a bare
	// nine-digit run is a routing number, an employer id or nothing at all.
	usSSNRe = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)

	// Employer identification number: two digits, a dash, seven digits. No
	// checksum, so the shape is the whole of the evidence — which is why it
	// scores below the identifiers that can be verified.
	usEINRe = regexp.MustCompile(`\b\d{2}-\d{7}\b`)

	// ABA routing number: nine bare digits, with a weighted checksum and an
	// assigned Federal Reserve district (see RoutingNumberCheck). Both are
	// needed — the checksum alone lets roughly one arbitrary nine-digit run in
	// ten through, and nine digits is what an order number looks like.
	usRoutingRe = regexp.MustCompile(`\b\d{9}\b`)

	// Area code, exchange, line. Both the area code and the exchange open on 2-9,
	// which is a real allocation rule and a useful filter: it keeps this off a
	// timestamp ("1700000000") and off anything opening on a zero or a one.
	//
	// The parenthesised form needs its own branch: \b cannot anchor before "(",
	// because neither side of it is a word character.
	usPhoneRe = regexp.MustCompile(`(?:\+1[ .-]?)?(?:\([2-9]\d{2}\)[ .-]?|\b[2-9]\d{2}[ .-]?)[2-9]\d{2}[ .-]?\d{4}\b`)

	// A ZIP only where something anchors it: a state abbreviation in front, or
	// the four-digit add-on behind. A bare five-digit run is not identifying and
	// matching it would mask every quantity and price in the payload — the same
	// rule the French postcode follows with its commune.
	usZIPRe = regexp.MustCompile(`\b(?:(?:` + usState + `)[ ]+\d{5}(?:-\d{4})?|\d{5}-\d{4})\b`)

	// Month-first dates, which is how the US writes them and nobody else does.
	//
	// It belongs to this locale for the same reason day-first belongs to France:
	// "03/14/1987" and "14/03/1987" are the same date read two ways, and only the
	// locale says which. With both sets enabled an unambiguous date is claimed by
	// whichever pattern can read it, and a genuinely ambiguous one — "05/06/2024"
	// — is claimed by both, resolves to one span, and is masked either way.
	//
	// One alternative per separator, because the separators have to agree and RE2
	// has no backreference: "03/14-1987" is not a date.
	usDateRe = regexp.MustCompile(`\b(?:` +
		usMonth + `/` + usDay + `/` + usYear + `|` +
		usMonth + `-` + usDay + `-` + usYear + `|` +
		usMonth + `\.` + usDay + `\.` + usYear +
		`)\b`)

	// A street address: a number, one to four capitalised words, and a street
	// type — optionally continuing through the city, state and ZIP.
	//
	// The capitalised words are what separate an address from a count of things
	// ("5 blocks down Main Street" has a lowercase word where a street name
	// belongs). The tail is optional but preferred, because taking the city and
	// state with it is what stops the address and the ZIP patterns from
	// disagreeing about where the value ends — the pair whose diverging spans
	// left a street in clear once already.
	usAddressRe = regexp.MustCompile(`\b\d{1,6}[ ]+(?:[A-Z][A-Za-z'.\-]*[ ]+){1,4}(?:` + usStreetType + `)\b\.?` +
		`(?:[ ]*,[ ]*[A-Z][A-Za-z]+(?:[ ][A-Z][A-Za-z]+){0,2}[ ]*,[ ]*(?:` + usState + `)[ ]+\d{5}(?:-\d{4})?)?`)
)

// unitedStatesFakes are the US stand-ins. Each lives in a range its issuer
// never allocates — a social security area of 000, a Federal Reserve district
// that does not exist, an unassigned ZIP, and the 555-01xx block the FCC
// reserves for fiction — so none of them can be anybody's real value, and this
// catalogue's own checks reject them rather than masking them twice.
var unitedStatesFakes = map[Category]Generator{
	// Area 000 is never issued.
	CatSSN: {Capacity: 999999, Make: func(i int64) string {
		return fmt.Sprintf("000-%02d-%04d", i/10000%100, i%10000)
	}},

	// 00 is not one of the campus prefixes the IRS assigns.
	CatEIN: {Capacity: 9999999, Make: func(i int64) string {
		return fmt.Sprintf("00-%07d", i)
	}},

	// District 99 does not exist, which RoutingNumberCheck rejects outright.
	CatRoutingNumber: {Capacity: 9999999, Make: func(i int64) string {
		return fmt.Sprintf("99%07d", i)
	}},

	// 555-0100 through 555-0199 are reserved for fiction.
	CatPhone: {Capacity: 100, Make: func(i int64) string {
		return fmt.Sprintf("(555) 555-01%02d", i-1)
	}},

	// 00000 is not an assigned ZIP.
	CatPostalCode: {Capacity: 9999, Make: func(i int64) string {
		return fmt.Sprintf("00000-%04d", i)
	}},

	CatAddress: {Capacity: 9999, Make: func(i int64) string {
		return fmt.Sprintf("%d Example Street, Anytown, IL 00000", i)
	}},
}

// UnitedStatesPatterns returns the US set. The address comes before the ZIP so
// that when both claim the same stretch the longer, more specific span is the
// one already in front.
func UnitedStatesPatterns() []Pattern {
	return []Pattern{
		{Regex: usSSNRe, Category: CatSSN, Label: "US Social Security number"},
		{Regex: usEINRe, Category: CatEIN, Label: "US employer identification number"},
		{Regex: usPhoneRe, Category: CatPhone, Label: "US telephone number"},
		{Regex: usDateRe, Category: CatDOB, Label: "Date (month first)"},
		{Regex: usAddressRe, Category: CatAddress, Label: "US street address"},
		{Regex: usZIPRe, Category: CatPostalCode, Label: "US ZIP code"},
		{Regex: usRoutingRe, Category: CatRoutingNumber, Label: "ABA routing number"},
	}
}
