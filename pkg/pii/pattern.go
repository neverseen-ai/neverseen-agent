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

	// Locale is the country set this pattern came from, or "" for the ones that
	// are locale-independent. Stamped by LocalePatterns rather than written into
	// each pattern, so a new locale gets it without anybody remembering to.
	//
	// It exists because a shared category needs to know which country recognised
	// a value. With France, the UK and the US all enabled, a French telephone
	// number came out as "(555) 555-0100": three locales contribute a PHONE
	// generator, and whichever loaded last was winning. Knowing the value was
	// matched by the French pattern is what picks the French stand-in.
	Locale string

	// Group is the submatch carrying the value, when the expression has to match
	// more than it means. RE2 has no lookbehind, so a pattern that must reject a
	// preceding character has to consume it and point here instead. Zero means
	// the whole match.
	Group int

	// Verify is a rule about how this pattern's span was cut, where the category's
	// Verify is a rule about the value. The two generic-secret expressions share a
	// category and disagree here: a quoted value ends at its quote, so a bracket
	// inside it is the password's own, while a bare span ends where the text
	// resumes and an unclosed opener says it was cut out of an expression. Read on
	// the category alone, `password="Ab(12cd"` was refused as code and went out in
	// clear. Nil for almost every pattern.
	Verify func(string) bool

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
