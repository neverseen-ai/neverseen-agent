package pii

import "sort"

// Locale is one country's contribution to the catalogue: everything here that
// depends on which country's data a deployment scans. For now that is the
// pattern set; the stand-ins fake mode substitutes are national too, and join
// this registry when substitution lands.
//
// It is a registry rather than a set of flags because the alternative does not
// hold. With flags, adding a country meant a boolean on the detector's config, a
// case in the parser, a hand-written list inside that parser's error message, a
// branch at the right position in the load order, and an entry in a
// hand-maintained "all patterns" — with nothing connecting them. Agent Veil had
// already shipped that list with one set missing, which silently exempted two
// thirds of the catalogue from every test that swept each category.
//
// What the registry does not hold is locale-independent: email, IP, card, IBAN
// and every credential stay on whatever a deployment selects, because disabling
// a country must never disable them.
type Locale struct {
	// Code is what the configuration names it by.
	Code string

	// Priority orders the pattern sets when several locales are on at once.
	// Lower loads first, and first means its patterns are in front when two
	// candidates score the same.
	//
	// It matters wherever two countries issue identifiers of the same length:
	// nine bare digits are a French SIREN under Luhn and a US routing number
	// under the ABA weights, and a number satisfying both is genuinely
	// ambiguous. The earlier locale is the one that names it.
	//
	// It is a declared number rather than the order of a few ifs somewhere,
	// because a fourth locale with numeric identifiers would slot into that
	// order silently and wrongly.
	Priority int

	// Patterns is the ordered set this locale contributes.
	Patterns func() []Pattern
}

// localeRegistry is the whole set. Order in the slice is not meaningful;
// Priority is.
//
// TODO: Germany, Spain, Italy and the Netherlands are the next four, in that
// order of market size. Each is one entry here plus a pattern file, a corpus
// suite and a regenerated score floor — the registry is what keeps that from
// touching anything else.
var localeRegistry = []Locale{
	{Code: "fr", Priority: 10, Patterns: FrancePatterns},
	{Code: "gb", Priority: 20, Patterns: UnitedKingdomPatterns},
	{Code: "us", Priority: 30, Patterns: UnitedStatesPatterns},
}

// Locales returns every registered locale, lowest Priority first — the order
// their pattern sets must load in.
func Locales() []Locale {
	out := make([]Locale, len(localeRegistry))
	copy(out, localeRegistry)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

// LocaleByCode returns the locale a configuration names.
func LocaleByCode(code string) (Locale, bool) {
	for _, l := range localeRegistry {
		if l.Code == code {
			return l, true
		}
	}
	return Locale{}, false
}

// LocaleCodes lists the registered codes in load order — for the error messages
// and the documentation that would otherwise name them from a copy.
func LocaleCodes() []string {
	locales := Locales()
	codes := make([]string, 0, len(locales))
	for _, l := range locales {
		codes = append(codes, l.Code)
	}
	return codes
}

// LocalePatterns returns the pattern sets of the named locales, concatenated in
// load order. An unknown code is skipped: the caller validates the selection,
// and this must not decide policy about it.
func LocalePatterns(codes []string) []Pattern {
	wanted := make(map[string]bool, len(codes))
	for _, c := range codes {
		wanted[c] = true
	}

	var out []Pattern
	for _, l := range Locales() {
		if wanted[l.Code] {
			out = append(out, l.Patterns()...)
		}
	}
	return out
}
