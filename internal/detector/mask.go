package detector

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/cloakfleet/cloakfleet/pkg/pii"
)

// Substitution selects what a masked value looks like on the wire. Both are
// permanent options: a deployment picks the one that fits its tolerance for a
// placeholder turning up in a model's answer.
type Substitution int

const (
	// SubstitutionToken replaces a value with "[EMAIL_1]". Unmistakably not
	// data, impossible to confuse with a real value in a log, and invisible to
	// the catalogue on a second pass.
	SubstitutionToken Substitution = iota

	// SubstitutionFake replaces a value with a stand-in of the same shape, so
	// the prompt reaches the model as prose. Categories with no generator fall
	// back to a token — see pii.FakeSet.
	SubstitutionFake
)

func (s Substitution) String() string {
	if s == SubstitutionFake {
		return "fake"
	}
	return "token"
}

// ParseSubstitution reads the mode from its configured spelling. An empty spec
// means tokens, so an unset variable cannot quietly change how data leaves the
// machine.
func ParseSubstitution(spec string) (Substitution, error) {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "", "token":
		return SubstitutionToken, nil
	case "fake":
		return SubstitutionFake, nil
	default:
		return 0, fmt.Errorf("unknown substitution mode %q (want token or fake)", spec)
	}
}

// Pass carries the identity of values across one exchange.
//
// It exists because a request is not one piece of text. A body has several
// fields, a conversation has several turns, and the same person's address has to
// come out as the same thing in all of them — otherwise the model is told about
// three different people and the vault fills up with duplicates.
type Pass struct {
	d *Detector

	// known is what the session already had, masked → original.
	known map[string]string
	// byValue is its reverse, so a value seen again keeps the mask it had.
	byValue map[string]string
	// minted is what this pass added, masked → original: what the caller still
	// has to store.
	minted map[string]string
}

// NewPass starts a pass seeded with what a session already holds.
func (d *Detector) NewPass(known map[string]string) *Pass {
	p := &Pass{
		d:       d,
		known:   known,
		byValue: make(map[string]string, len(known)),
		minted:  make(map[string]string),
	}
	for masked, original := range known {
		p.byValue[original] = masked
	}
	return p
}

// Minted returns what this pass added to the mapping.
func (p *Pass) Minted() map[string]string { return p.minted }

// mask returns what a value is replaced by, minting it on first sight.
func (p *Pass) mask(cat pii.Category, original string) string {
	if masked, ok := p.byValue[original]; ok {
		return masked
	}

	index := p.d.nextIndex(cat)
	masked := p.d.render(cat, index)

	p.byValue[original] = masked
	p.minted[masked] = original
	return masked
}

// render is the substitution mode applied to one index.
//
// A category with no generator, or an index past what its generator can produce
// without repeating itself, falls back to a bracket token. That fallback is the
// safe direction: a token is never a leak and never a fabrication, it only reads
// less like prose. Every credential takes it by design — a stand-in that looks
// like a working API key is a thing somebody will try to use.
func (d *Detector) render(cat pii.Category, index int64) string {
	if d.config.Substitution == SubstitutionFake && !pii.IsSecret(cat) {
		if fake, ok := d.fakes.Value(cat, index); ok {
			return fake
		}
	}
	return pii.Token(cat, index)
}

// nextIndex hands out the next index for a category.
//
// Process-wide rather than per session, so two requests in flight on one session
// cannot mint the same index for different values. The cost is that indices are
// not dense per session — a session's first email may be [EMAIL_4] — which is
// bookkeeping nobody reads.
//
// TODO: per-process atomics are correct for one agent on one workstation. Two
// agents sharing a vault would need a shared counter, and without one the second
// write erases the first. That lands with the Redis-backed store.
func (d *Detector) nextIndex(cat pii.Category) int64 {
	prefix := pii.Prefix(cat)

	d.mu.RLock()
	counter, ok := d.counters[prefix]
	d.mu.RUnlock()
	if ok {
		return counter.Add(1)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if counter, ok = d.counters[prefix]; !ok {
		counter = &atomic.Int64{}
		d.counters[prefix] = counter
	}
	return counter.Add(1)
}

// Mask replaces every sensitive value in text and reports how many it replaced,
// repeats included.
//
// One forward walk over the resolved matches, which Scan already returns in
// reading order and disjoint. Splicing each replacement into the string in turn
// would re-copy the whole body per match — quadratic, on the same path a large
// unrecognised body takes.
func (d *Detector) Mask(text string, pass *Pass) (string, int) {
	matches := d.Scan(text)
	if len(matches) == 0 {
		return text, 0
	}

	var b strings.Builder
	b.Grow(len(text))

	cursor, replaced := 0, 0
	for _, m := range matches {
		if m.Start < cursor {
			continue // defensive: overlap resolution has already made these disjoint
		}
		b.WriteString(text[cursor:m.Start])
		b.WriteString(pass.mask(m.Category, m.Value))
		cursor = m.End
		replaced++
	}
	b.WriteString(text[cursor:])

	return b.String(), replaced
}

// MaskOnce is Mask for a caller with no session to carry: it masks text and
// returns the mapping it minted.
func (d *Detector) MaskOnce(text string) (string, map[string]string, int) {
	pass := d.NewPass(nil)
	masked, replaced := d.Mask(text, pass)
	return masked, pass.Minted(), replaced
}

// Unmask puts the originals back, for every token the mapping knows.
//
// Only bracket tokens are expanded, and that is a guarantee rather than an
// omission. A generated stand-in is left alone — fake mode is one-way at the
// proxy on purpose, because a stand-in reads as prose and expanding it back
// would undo the substitution the deployment asked for. Never loosen this to
// match a value rather than a token: the same filter is what stops a masked
// credential from being expanded into a live secret on its way to a caller.
func Unmask(text string, known map[string]string) string {
	if len(known) == 0 {
		return text
	}
	return pii.ReplaceTokens(text, func(token string) (string, bool) {
		original, ok := known[token]
		return original, ok
	})
}
