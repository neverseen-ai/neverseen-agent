package pii

import "regexp"

// Pattern is one recognisable shape and the category it belongs to.
//
// Order within a set matters, and it is correctness rather than taste: the
// first pattern to claim a literal wins, so a specific shape must precede a
// broader one. "sk-ant-" before "sk-", a fourteen-digit SIRET before the
// nine-digit SIREN inside it, France's checksummed identifiers before Vietnam's
// CMND, which matches any bare nine digits.
type Pattern struct {
	Regex    *regexp.Regexp
	Category Category

	// Label describes the pattern for a human reading a log or a report. Two
	// patterns of the same category carry different labels — Slack has a bot
	// token and an app token — which is what makes a report say which shape
	// actually fired.
	Label string

	// Group is the submatch carrying the value, when the expression has to match
	// more than it means. RE2 has no lookbehind, so a pattern that must reject a
	// preceding character has to consume it and point here instead. Zero means
	// the whole match.
	Group int

	// Refine shortens a span the regex had to over-match, returning the real end
	// offset. Only the IBAN needs it: tolerating the conventional grouping by
	// four makes the expression greedy enough to swallow the following word, and
	// the checksum is the only thing that knows where the account actually ends.
	//
	// A pattern with a Refine is scanned one match at a time, resuming after the
	// refined end — scanning them all at once resumes after the greedy end
	// instead, so a second IBAN immediately after the first was never seen.
	Refine func(text string, start, end int) int
}

// AllPatterns returns every pattern in the catalogue: every locale in the
// registry, plus the locale-independent identifiers and the credentials.
//
// It reads the locale registry rather than concatenating the sets by hand. The
// hand-written version in Agent Veil had shipped with one set missing, which
// silently exempted two thirds of the catalogue from every test that swept
// "each category".
func AllPatterns() []Pattern { return allPatternsUnvalidated() }

// allPatternsUnvalidated is AllPatterns under the name that says it is also
// what validateCatalogue reads, before the catalogue is known to be sound.
func allPatternsUnvalidated() []Pattern {
	locales := LocalePatterns(LocaleCodes())
	intl, secrets := InternationalPatterns(), SecretPatterns()

	all := make([]Pattern, 0, len(locales)+len(intl)+len(secrets))
	all = append(all, locales...)
	all = append(all, intl...)
	all = append(all, secrets...)
	return all
}
