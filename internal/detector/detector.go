// Package detector finds the sensitive values in a piece of text.
//
// It is the one implementation. Whatever path a request arrives by, it is
// scanned here: a second scanner, however small, is a second answer to "what
// leaves this machine", and the two would diverge on the day one of them was
// fixed.
package detector

import (
	"sync"
	"sync/atomic"

	"github.com/cloakfleet/cloakfleet/pkg/pii"
)

// Match is one sensitive value found in a piece of text.
type Match struct {
	// Value is the text that was matched, exactly as it appeared.
	Value string

	Category pii.Category

	// Label describes the shape that fired, for a human reading a report. Two
	// patterns of one category carry different labels, which is what lets a
	// report say a Slack *app* token was found rather than just "a Slack token".
	Label string

	// Locale is the country set that recognised the value, or "" for the sets
	// that are locale-independent. It is what picks the right stand-in for a
	// category several countries contribute a shape to: a French telephone
	// number gets a French one.
	Locale string

	// Start and End are byte offsets into the scanned text.
	Start, End int

	// Confidence is the score that won this span, 1-100. It orders candidates
	// competing for the same stretch of text; it is not a probability.
	Confidence int
}

// Detector scans text against a catalogue, and masks what it finds.
//
// Safe for concurrent use. What varies with a request lives in the caller's text
// and in its Pass; the only shared mutable state is the index counters, which
// are shared on purpose — two requests in flight on one session must not mint
// the same index for different values.
type Detector struct {
	config Config

	// allow is config.AllowList in the comparable form, precomputed once.
	allow map[string]bool

	// policy is everything an operator can change while the agent runs: the loaded
	// catalogue, the substitution mode and the switched-off categories. Shared with
	// any detector WithSubstitution derives from this one — see policy.go.
	policy *policy

	// subOverride pins the substitution mode of a derived detector, ignoring the
	// live one. Only the test page sets it, and only because that page's whole job
	// is to render one text in both modes at once: reading the live mode there
	// would render the same column twice.
	subOverride *Substitution

	mu       sync.RWMutex
	counters map[string]*atomic.Int64 // token prefix -> highest index handed out
}

// New builds a Detector for a configuration.
//
// The catalogue is assembled in load order: the selected locales by their
// registry priority, then the locale-independent identifiers, then the
// credentials. The locale-independent sets are not optional — they are appended
// here rather than behind a flag, because "disabling a country must never
// disable email detection" is an invariant, and a flag is a way to get it wrong.
func New(cfg Config) *Detector {
	allow := make(map[string]bool, len(cfg.AllowList))
	for v := range cfg.AllowList {
		allow[normalizeListValue(v)] = true
	}

	d := &Detector{
		config:   cfg,
		allow:    allow,
		policy:   &policy{},
		counters: make(map[string]*atomic.Int64),
	}
	d.policy.cat.Store(newCatalogue(cfg.Locales))
	d.policy.sub.Store(int32(cfg.Substitution))
	d.policy.level.Store(int32(cfg.SecretLevel))
	return d
}

// Locales reports the country sets this detector loaded, for the status a
// supervised agent reports about itself.
func (d *Detector) Locales() []string { return d.catalogue().locales }

// Substitution reports how this detector renders a masked value, for the same
// status line.
func (d *Detector) Substitution() Substitution {
	if d.subOverride != nil {
		return *d.subOverride
	}
	return Substitution(d.policy.sub.Load())
}

// Sample returns text exercising every category this detector can find, in every
// notation its patterns accept.
//
// It lives here rather than beside whatever displays it, because the answer
// depends on the configuration and the detector is what holds it.
func (d *Detector) Sample() string { return pii.Sample(d.catalogue().locales) }

// WithSubstitution returns a detector holding the same catalogue and lists,
// rendering its replacements in the given mode.
//
// The mode belongs to the configuration, because a deployment has one answer to
// "what does a masked value look like here". This exists for the one caller that
// needs both at once: the side-by-side comparison, which cannot ask the question
// twice of the same detector.
//
// The result carries its own counters, and that is the point rather than a side
// effect. Sharing the live ones would number the same text [EMAIL_1] on one page
// load and [EMAIL_7] on the next, so a comparison nobody could read — and it
// would spend the real indices on a page that stores nothing.
func (d *Detector) WithSubstitution(mode Substitution) *Detector {
	// The same policy, not a copy of it, so the derived detector follows every
	// locale change and every switched-off category as they happen. The test page
	// renders both modes through two of these, and a copy would have that page show
	// a configuration the agent had stopped applying — while the page exists to say
	// what the agent does to a text.
	//
	// Its own counters, as before: the page mints indices nobody stores, and sharing
	// the agent's would advance the numbering of a real session on every reload.
	return &Detector{
		config:      d.config,
		allow:       d.allow,
		policy:      d.policy,
		subOverride: &mode,
		counters:    make(map[string]*atomic.Int64),
	}
}

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

	for _, p := range d.catalogue().patterns {
		for _, span := range patternSpans(p, text) {
			value := text[span[0]:span[1]]

			if !d.masks(p.Category) {
				// Before the checksum and before the score, because a category
				// switched off is not a weak match: it is a category this agent
				// has been told not to look at, and running its checksum to throw
				// the answer away is work on the hottest loop in the agent.
				continue
			}
			if d.allowed(value) {
				continue
			}
			score, ok := pii.Score(p.Category, value)
			if !ok || score < minConfidence {
				continue
			}
			if !d.strongEnough(p.Category, value) {
				continue
			}

			out = append(out, Match{
				Value:      value,
				Category:   p.Category,
				Label:      p.Label,
				Locale:     p.Locale,
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

// strongEnough applies the secret level, and applies it to one category.
//
// Only the named-secret catch-all is graded. Every other credential here is
// identified by a prefix somebody can verify — "gsk_", "sk-ant-", "ghp_" — or by a
// structure that leaves no room for judgement, and a level that could stop masking a
// real Anthropic key would be a setting whose only effect is to leak. The spectrum
// exists for the one pattern whose evidence is a *name* beside the value, where the
// value itself may be a generated key or an ordinary word.
//
// Read once per candidate, from the same atomic the rest of the policy lives behind.
func (d *Detector) strongEnough(cat pii.Category, value string) bool {
	if cat != pii.CatGenericSecret {
		return true
	}
	return pii.SecretStrength(value) >= d.SecretLevel()
}
