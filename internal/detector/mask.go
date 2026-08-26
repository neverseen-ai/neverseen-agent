package detector

import (
	"fmt"
	"sort"
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

	// Reveal, when set, is called the first time a value is replaced, with the
	// value in clear. It is the audit console and nothing else — see
	// internal/proxy/audit.go for why that surface is allowed to see content
	// when no other one is.
	//
	// At minting rather than per occurrence: it fires in order of appearance,
	// and a value repeated forty times in a system prompt is one transformation
	// to look at rather than forty identical lines to scroll past.
	Reveal func(original, replacement string)

	// counts is how many values were replaced per category, repeats included.
	//
	// Repeats included on purpose: a value masked three times in one body is
	// three values that did not leave the machine, and that is the number a
	// security officer is looking at. It is counted here rather than derived
	// from minted, which only ever holds first sightings.
	counts map[pii.Category]int
}

// NewPass starts a pass seeded with what a session already holds.
func (d *Detector) NewPass(known map[string]string) *Pass {
	p := &Pass{
		d:       d,
		known:   known,
		byValue: make(map[string]string, len(known)),
		minted:  make(map[string]string),
		counts:  make(map[pii.Category]int),
	}
	for masked, original := range known {
		p.byValue[original] = masked
	}
	return p
}

// Minted returns what this pass added to the mapping.
func (p *Pass) Minted() map[string]string { return p.minted }

// Counts returns how many values this pass replaced, per category.
func (p *Pass) Counts() map[pii.Category]int { return p.counts }

// mask returns what a value is replaced by, minting it on first sight.
func (p *Pass) mask(cat pii.Category, locale, original string) string {
	if masked, ok := p.byValue[original]; ok {
		return masked
	}

	index := p.d.nextIndex(cat)
	masked := p.d.render(cat, locale, index)

	p.byValue[original] = masked
	p.minted[masked] = original
	if p.Reveal != nil {
		p.Reveal(original, masked)
	}
	return masked
}

// render is the substitution mode applied to one index, in the locale that
// recognised the value.
//
// A category with no generator, or an index past what its generator can produce
// without repeating itself, falls back to a bracket token. That fallback is the
// safe direction: a token is never a leak and never a fabrication, it only reads
// less like prose. Every credential takes it by design — a stand-in that looks
// like a working API key is a thing somebody will try to use.
func (d *Detector) render(cat pii.Category, locale string, index int64) string {
	if d.config.Substitution == SubstitutionFake && !pii.IsSecret(cat) {
		if fake, ok := d.fakes.Value(cat, locale, index); ok {
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
		b.WriteString(pass.mask(m.Category, m.Locale, m.Value))
		pass.counts[m.Category]++
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

// Unmask puts the originals back, for everything the mapping holds.
//
// A mapping holds two shapes, because a masked value takes two. A bracket token
// is expanded whatever the mode. A generated stand-in — fake mode's substitution,
// which reads as prose — is expanded too, matched by its own text, so an exchange
// in fake mode round-trips like any other.
//
// That is a deliberate reversal of what this used to do, and what still holds it
// safe is upstream rather than here: a credential never gets a stand-in.
// Detector.render falls back to a bracket token for every secret category by
// design, so a non-token key in the mapping cannot be a credential, and the
// value-matching path below can never expand one into a live secret. Removing
// that fallback would remove this guarantee with it.
//
// A token this process never minted is still left alone: inventing a value for it
// would put data in front of the caller that nothing supports.
//
// TODO: a short stand-in can be matched by coincidence — a fake postcode is five
// digits, and a model that wrote those five digits about something else has them
// replaced by the caller's real postcode. The stand-ins are picked to be
// implausible rather than short (see pii.FakeSet), which narrows it but does not
// close it; a minimum length, or marking the substitution invisibly, is the
// upgrade path if it is ever seen.
func Unmask(text string, known map[string]string) string {
	return UnmaskSeen(text, known, nil)
}

// UnmaskSeen is Unmask, reporting each replacement it expanded.
//
// One implementation rather than two, because the shapes it accepts are the
// guarantee: a second walk over the text written to report on it would be a
// second answer to "what may be expanded", and the wrong one restores something
// this agent never masked.
//
// seen receives the original value in clear, so nothing but the audit console may
// pass a non-nil one — see internal/proxy/audit.go.
func UnmaskSeen(text string, known map[string]string, seen func(masked, original string)) string {
	if len(known) == 0 {
		return text
	}

	standIns := standInsOf(known)
	if len(standIns) == 0 {
		// Token mode, which is every session that has minted nothing but tokens:
		// one regex scan that answers "is there anything here at all", and the
		// text is returned untouched when there is not.
		return pii.ReplaceTokens(text, func(token string) (string, bool) {
			original, ok := known[token]
			if ok && seen != nil {
				seen(token, original)
			}
			return original, ok
		})
	}

	// One forward walk once stand-ins are in play, and it has to be a walk rather
	// than a replacement per entry: one stand-in can contain another — a fake
	// postcode inside the fake address it belongs to — and replacing them in turn
	// expands the shorter one inside text that has already been expanded, which
	// puts a value inside a value.
	var starts [256]bool
	for _, standIn := range standIns {
		starts[standIn[0]] = true
	}

	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		// Longest first, so the entry that wins at a position is the widest one.
		if starts[text[i]] {
			matched := ""
			for _, standIn := range standIns {
				if strings.HasPrefix(text[i:], standIn) {
					matched = standIn
					break
				}
			}
			if matched != "" {
				i += len(matched)
				b.WriteString(report(known, matched, seen))
				continue
			}
		}

		if token := pii.TokenAt(text[i:]); token != "" {
			if _, ok := known[token]; ok {
				i += len(token)
				b.WriteString(report(known, token, seen))
				continue
			}
		}

		b.WriteByte(text[i])
		i++
	}
	return b.String()
}

// report returns what a masked value stands for, telling the console about it.
func report(known map[string]string, masked string, seen func(masked, original string)) string {
	original := known[masked]
	if seen != nil {
		seen(masked, original)
	}
	return original
}

// TailLen returns how much of the end of text has to be held back because it
// could be the beginning of something this mapping would expand.
//
// Streaming needs it, and it has to cover both shapes for the same reason the
// expander does: generated text arrives in pieces of a few characters, so a
// stand-in the model echoed is split across two of them exactly as a token is.
// Answering only for tokens is what left fake mode restoring nothing in a
// streamed answer while a buffered one round-tripped.
func TailLen(text string, known map[string]string) int {
	tail := pii.TokenTailLen(text)
	for _, standIn := range standInsOf(known) {
		limit := min(len(standIn)-1, len(text))
		for k := limit; k > tail; k-- {
			if strings.HasSuffix(text, standIn[:k]) {
				tail = k
				break
			}
		}
	}
	return tail
}

// standInsOf returns the mapping's keys that are not tokens, longest first.
//
// Longest first is what makes the walk above take the widest match at each
// position. Empty for a session that minted only tokens, which is what keeps
// token mode on the regex path it has always been on.
func standInsOf(known map[string]string) []string {
	var standIns []string
	for masked := range known {
		if masked != "" && !pii.IsToken(masked) {
			standIns = append(standIns, masked)
		}
	}
	sort.Slice(standIns, func(i, j int) bool { return len(standIns[i]) > len(standIns[j]) })
	return standIns
}
