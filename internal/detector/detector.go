// Package detector finds the sensitive values in a piece of text.
//
// It is the one implementation. Whatever path a request arrives by, it is
// scanned here: a second scanner, however small, is a second answer to "what
// leaves this machine", and the two would diverge on the day one of them was
// fixed.
package detector

import "github.com/cloakfleet/cloakfleet/pkg/pii"

// Match is one sensitive value found in a piece of text.
type Match struct {
	// Value is the text that was matched, exactly as it appeared.
	Value string

	Category pii.Category

	// Label describes the shape that fired, for a human reading a report. Two
	// patterns of one category carry different labels, which is what lets a
	// report say a Slack *app* token was found rather than just "a Slack token".
	Label string

	// Start and End are byte offsets into the scanned text.
	Start, End int

	// Confidence is the score that won this span, 1-100. It orders candidates
	// competing for the same stretch of text; it is not a probability.
	Confidence int
}

// Detector scans text against a catalogue.
//
// It holds no per-request state and is safe for concurrent use: everything that
// varies with a request lives in the caller's text.
type Detector struct {
	patterns []pii.Pattern
	config   Config

	// allow is config.AllowList in the comparable form, precomputed once.
	allow map[string]bool
}

// New builds a Detector for a configuration.
//
// The catalogue is assembled in load order: the selected locales by their
// registry priority, then the locale-independent identifiers, then the
// credentials. The locale-independent sets are not optional — they are appended
// here rather than behind a flag, because "disabling a country must never
// disable email detection" is an invariant, and a flag is a way to get it wrong.
func New(cfg Config) *Detector {
	locales := pii.LocalePatterns(cfg.Locales)
	intl, secrets := pii.InternationalPatterns(), pii.SecretPatterns()

	patterns := make([]pii.Pattern, 0, len(locales)+len(intl)+len(secrets))
	patterns = append(patterns, locales...)
	patterns = append(patterns, intl...)
	patterns = append(patterns, secrets...)

	allow := make(map[string]bool, len(cfg.AllowList))
	for v := range cfg.AllowList {
		allow[normalizeListValue(v)] = true
	}

	return &Detector{patterns: patterns, config: cfg, allow: allow}
}

// Locales reports the country sets this detector loaded, for the status a
// supervised agent reports about itself.
func (d *Detector) Locales() []string { return d.config.Locales }

// Scan returns the sensitive values in text, in reading order, with overlaps
// resolved.
//
// It reports the resolved set rather than every regex hit. A raw list counts a
// postal code and the address containing it as two findings, which would tell an
// auditor that two values leave the machine where one does.
func (d *Detector) Scan(text string) []Match {
	return resolveOverlaps(d.candidates(text))
}

// candidates returns every regex hit that is reportable at all: not allow-listed,
// passing its category's checksum, and scoring at least minConfidence.
func (d *Detector) candidates(text string) []Match {
	var out []Match

	for _, p := range d.patterns {
		for _, span := range patternSpans(p, text) {
			value := text[span[0]:span[1]]

			if d.allowed(value) {
				continue
			}
			score, ok := pii.Score(p.Category, value)
			if !ok || score < minConfidence {
				continue
			}

			out = append(out, Match{
				Value:      value,
				Category:   p.Category,
				Label:      p.Label,
				Start:      span[0],
				End:        span[1],
				Confidence: score,
			})
		}
	}
	return out
}

// allowed reports whether a value is one this deployment declared it never wants
// masked.
func (d *Detector) allowed(value string) bool {
	if len(d.allow) == 0 {
		return false
	}
	return d.allow[normalizeListValue(value)]
}

// patternSpans returns the offsets a pattern claims: its whole match, or the
// submatch it points at when the expression had to consume more than it means.
func patternSpans(p pii.Pattern, text string) [][]int {
	if p.Refine != nil {
		return refinedSpans(p, text)
	}
	if p.Group == 0 {
		return p.Regex.FindAllStringIndex(text, -1)
	}

	lo, hi := 2*p.Group, 2*p.Group+1
	var out [][]int
	for _, m := range p.Regex.FindAllStringSubmatchIndex(text, -1) {
		if hi < len(m) && m[lo] >= 0 {
			out = append(out, []int{m[lo], m[hi]})
		}
	}
	return out
}

// refinedSpans scans a pattern that over-matches by design, one hit at a time,
// resuming after each refined end.
//
// Scanning them all at once and refining afterwards resumes after the *greedy*
// end instead: when the expression spanned two adjacent IBANs, the refinement
// kept the first and the second was never rescanned — forwarded in clear.
func refinedSpans(p pii.Pattern, text string) [][]int {
	var out [][]int

	for pos := 0; pos < len(text); {
		loc := p.Regex.FindStringIndex(text[pos:])
		if loc == nil {
			break
		}
		start, end := pos+loc[0], pos+loc[1]

		refined := p.Refine(text, start, end)
		out = append(out, []int{start, refined})

		if refined <= start {
			refined = start + 1 // unreachable, but never stall the scan
		}
		pos = refined
	}
	return out
}
