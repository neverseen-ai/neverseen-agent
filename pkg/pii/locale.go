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

	// European puts the locale in the selection LocaleEurope names. Europe the
	// continent, not the Union: a French fleet crossing the Channel meets British
	// identifiers, and an "eu" that left them out would forward them in clear.
	//
	// A field on the locale rather than a list beside the alias, so a country
	// added to the registry joins "eu" in the same line that adds it.
	European bool

	// Patterns is the ordered set this locale contributes.
	Patterns func() []Pattern

	// Sample is text exercising every category this locale detects, in every
	// notation its patterns accept. It is the reference an operator reads to
	// check their own data shape is covered, so a gap in it reads as a gap in
	// the engine.
	Sample string

	// Fakes are the stand-ins fake mode substitutes for the categories whose
	// shape is national.
	//
	// Per locale, because a global table meant a British telephone number stood
	// in for a French one — which is exactly the machine artefact fake mode
	// exists to avoid. A category absent here falls back to the
	// locale-independent generators, and then to a bracket token.
	Fakes map[Category]Generator
}

// localeRegistry is the whole set. Order in the slice is not meaningful;
// Priority is.
//
// TODO: every locale after us carries only its licence plate so far; each
// country's national identifiers are the next entries in its file. Each is one entry here
// plus a pattern file, a corpus suite and a regenerated score floor — the
// registry is what keeps that from touching anything else.
var localeRegistry = []Locale{
	{
		Code: "fr", Priority: 10, European: true,
		Patterns: FrancePatterns, Sample: franceSample, Fakes: franceFakes,
	},
	{
		Code: "gb", Priority: 20, European: true,
		Patterns: UnitedKingdomPatterns, Sample: unitedKingdomSample, Fakes: unitedKingdomFakes,
	},
	{
		Code: "us", Priority: 30,
		Patterns: UnitedStatesPatterns, Sample: unitedStatesSample, Fakes: unitedStatesFakes,
	},
	{
		Code: "es", Priority: 40, European: true,
		Patterns: SpainPatterns, Sample: spainSample, Fakes: spainFakes,
	},
	{
		Code: "be", Priority: 50, European: true,
		Patterns: BelgiumPatterns, Sample: belgiumSample, Fakes: belgiumFakes,
	},
	// After France, whose plates Italy writes the same way; the Netherlands before
	// Portugal, for the same reason — the country with the letter rule decides.
	{
		Code: "it", Priority: 60, European: true,
		Patterns: ItalyPatterns, Sample: italySample, Fakes: italyFakes,
	},
	{
		Code: "nl", Priority: 70, European: true,
		Patterns: NetherlandsPatterns, Sample: netherlandsSample, Fakes: netherlandsFakes,
	},
	{
		Code: "pt", Priority: 80, European: true,
		Patterns: PortugalPatterns, Sample: portugalSample, Fakes: portugalFakes,
	},
	// The rest of Europe, largest market first (patterns_eu.go). Every one loads
	// after the countries above, so a shape they share is named by those.
	{
		Code: "de", Priority: 90, European: true,
		Patterns: GermanyPatterns, Sample: germanySample, Fakes: germanyFakes,
	},
	euroPlateLocale("pl", 100, samplePL),
	euroPlateLocale("at", 110, sampleAT),
	euroPlateLocale("ch", 120, sampleCH),
	euroPlateLocale("cz", 130, sampleCZ),
	euroPlateLocale("sk", 140, sampleSK),
	euroPlateLocale("hu", 150, sampleHU),
	euroPlateLocale("ro", 160, sampleRO),
	euroPlateLocale("ie", 170, sampleIE),
	euroPlateLocale("dk", 180, sampleDK),
	euroPlateLocale("se", 190, sampleSE),
	euroPlateLocale("no", 200, sampleNO),
	euroPlateLocale("fi", 210, sampleFI),
	euroPlateLocale("gr", 220, sampleGR),
	euroPlateLocale("bg", 230, sampleBG),
	euroPlateLocale("hr", 240, sampleHR),
	euroPlateLocale("si", 250, sampleSI),
	euroPlateLocale("lt", 260, sampleLT),
	euroPlateLocale("lv", 270, sampleLV),
	euroPlateLocale("ee", 280, sampleEE),
	euroPlateLocale("lu", 290, sampleLU),
	euroPlateLocale("mt", 300, sampleMT),
	euroPlateLocale("cy", 310, sampleCY),
	euroPlateLocale("is", 320, sampleIS),
	euroPlateLocale("li", 330, sampleLI),
	euroPlateLocale("al", 340, sampleAL),
	euroPlateLocale("mk", 350, sampleMK),
	euroPlateLocale("ba", 360, sampleBA),
	euroPlateLocale("me", 370, sampleME),
	euroPlateLocale("xk", 380, sampleXK),
	euroPlateLocale("ua", 390, sampleUA),
	euroPlateLocale("md", 400, sampleMD),
	euroPlateLocale("tr", 410, sampleTR),
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

// LocaleEurope is the selection that loads every European locale at once.
//
// It is a word a selection may use, not a Locale. As a registry entry it would
// stamp its own code on every pattern it carried, and a French telephone number
// would stop getting a French stand-in — the stamp is what picks the stand-in.
// So it is expanded where a selection is read, and nothing past that point ever
// sees it.
const LocaleEurope = "eu"

// EuropeanLocales lists the codes LocaleEurope stands for, in load order.
//
// Expanded when the selection is read, never stored: an environment naming "eu"
// picks up a country added in a later build on the next start, where a stored
// expansion would have frozen the list it was written with.
func EuropeanLocales() []string {
	var codes []string
	for _, l := range Locales() {
		if l.European {
			codes = append(codes, l.Code)
		}
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
		if !wanted[l.Code] {
			continue
		}
		// Stamped here, once, so a locale cannot contribute patterns that do not
		// know where they came from — which is what decides whose stand-in a
		// shared category gets.
		for _, p := range l.Patterns() {
			p.Locale = l.Code
			out = append(out, p)
		}
	}
	return out
}

// CategoriesInLocale names what one country's pattern set can find, in catalogue
// order and with each category named once however many patterns carry it.
//
// Derived from the patterns rather than declared beside the locale, for the reason
// IsCredentialGroup is derived: a second list is a list that comes to disagree with
// the first, and this one would disagree silently — a notation added to a locale
// without its category being added here would simply go unmentioned on the page.
//
// It is what this country can find, and that is **not** the same as what unticking
// it would cost. Countries share categories — a postcode, a telephone number and a
// postal address are national in shape and one category each in the catalogue — so a
// category two countries carry survives while either is loaded, and a few (a date of
// birth, an email address) are found with no country loaded at all. The lists
// therefore overlap, and their lengths do not add up to what a selection has in play:
// fr, gb and us name 8, 7 and 7 against 16 in play. That is why a surface shows the
// names from this and never a count — a count is a number a reader adds up and is
// wrong.
func CategoriesInLocale(code string) []Category {
	locale, ok := LocaleByCode(code)
	if !ok {
		return nil
	}

	seen := make(map[Category]bool)
	var out []Category
	for _, p := range locale.Patterns() {
		if seen[p.Category] {
			continue
		}
		seen[p.Category] = true
		out = append(out, p.Category)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
