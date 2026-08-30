package pii

import (
	"fmt"
	"regexp"
	"strings"
)

// The French set. Every numeric identifier here carries a checksum, which is
// what lets the shapes be loose enough to match how the values are actually
// written — spaced, dotted, or run together — without claiming every invoice
// number of the same length.
//
// TODO: no FNI (pre-2009) licence plate. Its "123-ABC-45" shape carries no
// checksum, and RE2 has no lookbehind, so a context-hinted variant would have
// to swallow the label in front of it into the span.

// Fragments of the street address, named because the assembled expression is
// unreadable as one line.
const (
	// Street types, with the abbreviations that actually turn up in address
	// fields. Longer spellings first: Go's alternation is leftmost-first, so
	// "route" must be offered before "rte", and "rue" before "r.".
	frStreetType = `avenue|av\.|allée|all\.|boulevard|bd\.?|chemin|ch\.|cours|crs|` +
		`impasse|imp\.|place|pl\.|quai|route|rte|rue|r\.|square|sq\.`

	// The street name. Lowercase is normal here ("rue de la Paix"), so there is
	// no capital anchor; the bound of five words is what stops a match from
	// running on into the sentence.
	//
	// Horizontal whitespace only, here and in the commune below. With \s the
	// span crossed a line break and swallowed the first word of the next line,
	// so the replacement ate a word of ordinary text and the provider received a
	// prompt with a hole in it. A wrapped address loses nothing: the postcode
	// and commune left on the next line are matched on their own.
	frStreetName = `[\p{L}'’\-]+(?:[ \t]+[\p{L}'’\-]+){0,4}`

	// Postcode and commune. The commune is capital-anchored — (?-i:…) because
	// the assembled expression is case-insensitive — and without that anchor the
	// match runs into the sentence: "75002 Paris avant vendredi" tokenized the
	// prose the model needed.
	//
	// Five digits only. Four would cover Belgium, Luxembourg and Switzerland,
	// outside this locale, and would let a year followed by a capitalised word
	// ("… 2024 Rapport") read as a commune.
	frCommune = `,?[ \t]*\d{5}[ \t]+(?-i:[A-ZÀ-ÖØ-Þ][\p{L}'’\-]+(?:[ \t-][A-ZÀ-ÖØ-Þ][\p{L}'’\-]+){0,2})`
)

// Date fragments: a day, a month as digits or as a French name, and a year
// anchored to 19xx/20xx. That anchor is what keeps the space-separated form from
// matching an arbitrary run of three numbers.
const (
	frDay       = `(?:0?[1-9]|[12]\d|3[01])`
	frMonthNum  = `(?:0?[1-9]|1[0-2])`
	frMonthName = `(?:janvier|f[eé]vrier|mars|avril|mai|juin|juillet|ao[uû]t|septembre|octobre|novembre|d[eé]cembre)`
	frYear      = `(?:19|20)\d{2}`
)

var (
	// Thirteen digits and a two-digit key, tolerating the spacing the number is
	// printed with. A Corsican birth department is written 2A or 2B.
	frNIRRe = regexp.MustCompile(`\b[12][ .]?\d{2}[ .]?(?:0[1-9]|1[0-2])[ .]?(?:\d{2}|2[AB])[ .]?\d{3}[ .]?\d{3}[ .]?\d{2}\b`)

	// SIV plate, since 2009: AB-123-CD. Uppercase only — a lowercase run of the
	// same shape is far more likely to be an internal identifier.
	frPlateRe = regexp.MustCompile(`\b[A-Z]{2}[ -]?\d{3}[ -]?[A-Z]{2}\b`)

	// 0X or +33/0033, separated by space, dot or dash, and tolerating the trunk
	// zero in parentheses that international notation carries: "+33 (0)1 …".
	frPhoneRe = regexp.MustCompile(`(?:\+33|0033)[ .-]?(?:\(0\)[ .-]?)?[1-9](?:[ .-]?\d{2}){4}\b|\b0[1-9](?:[ .-]?\d{2}){4}\b`)

	// The day-first orders, as digits or with the month spelled out. One
	// alternative per separator rather than a character class, because the
	// separators have to agree and RE2 has no backreference to say so:
	// "23/02-2004" is not a date. The spaced forms take [ ]+ so a span cannot
	// run across a line break and put a newline inside the replaced value.
	frDateRe = regexp.MustCompile(`(?i)\b(?:` +
		frDay + `/` + frMonthNum + `/` + frYear + `|` +
		frDay + `-` + frMonthNum + `-` + frYear + `|` +
		frDay + `\.` + frMonthNum + `\.` + frYear + `|` +
		frDay + `[ ]+` + frMonthNum + `[ ]+` + frYear + `|` +
		frDay + `(?:er)?[ ]+` + frMonthName + `[ ]+` + frYear +
		`)\b`)

	// SIRET: a SIREN plus a five-digit establishment number, Luhn-checked.
	frSIRETRe = regexp.MustCompile(`\b\d{3}[ .]?\d{3}[ .]?\d{3}[ .]?\d{5}\b`)

	// A street address, in the two shapes that occur in practice:
	//
	//	number + type + name (+ postcode + commune)  "12 rue de la Paix, 75002 Paris"
	//	type + name + postcode + commune             "Route de Lyon, 38000 Grenoble"
	//
	// The second shape has no house number, so the postcode and commune are
	// required rather than optional: they are what separates an address from the
	// prose use of the same words ("la route de la soie", "au cours de l'année").
	frAddressRe = regexp.MustCompile(`(?i)(?:` +
		`\b\d{1,4}(?:[ \t]?(?:bis|ter|quater))?[, \t]+(?:` + frStreetType + `)[ \t]+` + frStreetName + `(?:` + frCommune + `)?` +
		`|` +
		`\b(?:` + frStreetType + `)[ \t]+` + frStreetName + frCommune +
		`)`)

	// A postcode, only when a commune follows it. A bare five-digit run is not
	// identifiable: matching it alone would tokenize every quantity, price and
	// odometer reading in the payload.
	//
	// Departments stop at 98, so 99xxx is not a code. The follow words are
	// capital-anchored exactly as in the address pattern: when lowercase was
	// allowed here the two patterns' spans diverged on "75002 Paris la Défense",
	// overlap resolution dropped the postal match whole, and its tail went out
	// in clear.
	frPostalRe = regexp.MustCompile(`\b(?:0[1-9]|[1-8]\d|9[0-8])\d{3}[ \t]+[A-ZÀ-ÖØ-Þ][\p{L}'’-]+(?:[ \t][A-ZÀ-ÖØ-Þ][\p{L}'’-]+){0,3}`)

	// SIREN: nine digits, Luhn-checked. Last of the numeric patterns, so a SIRET
	// or a NIR claims its digits first when the spans tie.
	//
	// TODO: Luhn still lets roughly one bare nine-digit number in ten through,
	// so a nine-digit fleet or order id can be masked as a SIREN. Narrow it by
	// requiring the conventional 3-3-3 spacing, or list the known ids in the
	// deployment's allow list.
	frSIRENRe = regexp.MustCompile(`\b\d{3}[ .]?\d{3}[ .]?\d{3}\b`)
)

// franceFakes are the French stand-ins for fake mode. Every one of them is
// unattributable by construction: the checksummed identifiers are built to fail
// their own checksum, the telephone numbers come from the block ARCEP reserves
// for fiction, and the postcode uses a department number that does not exist —
// which also means the stand-in is not detected again on a second pass.
var franceFakes = map[Category]Generator{
	// The day-first notation this locale reads. Shared with the United Kingdom in
	// shape and deliberately not in code: the shared table renders ISO, because
	// that is what its own pattern reads, and a date that changed notation on the
	// way through is the machine artefact fake mode exists to avoid.
	CatDOB: {Capacity: 28 * 12, Make: func(i int64) string {
		month, day, _ := strings.Cut(isoMonthDay(i), "-")
		return day + "/" + month + "/1900"
	}},
	// 06 39 98 xx xx is reserved by ARCEP for use in fiction, so no number in it
	// can ring anybody.
	CatPhone: {Capacity: 10000, Make: func(i int64) string {
		i--
		return fmt.Sprintf("06 39 98 %02d %02d", i/100, i%100)
	}},

	// A key that is deliberately not the one the thirteen digits produce.
	CatNIR: {Capacity: 999999, Make: func(i int64) string {
		body := fmt.Sprintf("1900199%06d", i) // sex, year, month, department, order
		return body + fmt.Sprintf("%02d", (nirKey(body)%97)+1)
	}},

	CatSIREN: {Capacity: 99999999, Make: func(i int64) string {
		body := fmt.Sprintf("%08d", i)
		return body + invalidCheckDigit(luhnCheckDigit(body))
	}},

	CatSIRET: {Capacity: 9999999, Make: func(i int64) string {
		body := fmt.Sprintf("%013d", i)
		return body + invalidCheckDigit(luhnCheckDigit(body))
	}},

	// Departments stop at 98, so 99xxx is not a postcode — and this catalogue's
	// own pattern rejects it, which is the property worth having: a stand-in
	// that is detected again would be masked twice.
	CatPostalCode: {Capacity: 999, Make: func(i int64) string {
		return fmt.Sprintf("99000 Villeneuve-%d", i)
	}},

	CatAddress: {Capacity: 999, Make: func(i int64) string {
		return fmt.Sprintf("%d rue de l'Exemple, 99000 Villeneuve", i)
	}},

	// WW is the series French temporary plates use, so it is never a permanent
	// registration.
	CatLicPlate: {Capacity: 1000, Make: func(i int64) string {
		return fmt.Sprintf("WW-%03d-WW", i-1)
	}},
}

// nirKey returns the two check digits a thirteen-digit mainland body produces.
func nirKey(body string) int {
	m := 0
	for _, r := range body {
		m = (m*10 + int(r-'0')) % 97
	}
	return 97 - m
}

// FrancePatterns returns the French set, longest identifier first so the NIR's
// fifteen digits are never split by a shorter numeric shape when two candidates
// score the same.
func FrancePatterns() []Pattern {
	return []Pattern{
		{Regex: frNIRRe, Category: CatNIR, Label: "Numéro de sécurité sociale (NIR)"},
		{Regex: frPlateRe, Category: CatLicPlate, Label: "Plaque d'immatriculation (SIV)"},
		{Regex: frPhoneRe, Category: CatPhone, Label: "Numéro de téléphone français"},
		{Regex: frDateRe, Category: CatDOB, Label: "Date (jour d'abord)"},
		{Regex: frSIRETRe, Category: CatSIRET, Label: "SIRET"},
		{Regex: frAddressRe, Category: CatAddress, Label: "Adresse française"},
		{Regex: frPostalRe, Category: CatPostalCode, Label: "Code postal et commune"},
		{Regex: frSIRENRe, Category: CatSIREN, Label: "SIREN"},
	}
}
