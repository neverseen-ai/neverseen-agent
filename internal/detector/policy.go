package detector

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/cloakfleet/cloakfleet/pkg/pii"
)

// policy is the set of categories an operator has switched off, shared by every
// detector that came from one configuration.
//
// A pointer shared rather than a value copied, because WithSubstitution builds a
// second detector to render the test page in both modes: copied, that page would
// go on showing a category the agent had stopped masking, and the page exists
// precisely to say what the agent does to a text.
//
// The set is held as an immutable map behind an atomic pointer rather than behind
// a mutex. Every request in flight reads it once per candidate match, which is
// the hottest loop in the agent, and a write happens when somebody clicks a menu
// item. A read lock on the request path to serve a click a day is the wrong trade;
// swapping a whole map is one atomic store.
type policy struct {
	off atomic.Pointer[map[pii.Category]bool]

	// cat is the loaded catalogue: which locales, the patterns they bring, and the
	// stand-ins they resolve to. Behind a pointer for the same reason the disabled
	// set is, and swapped whole rather than mutated in place — the three have to
	// agree with each other, and a scan that read new patterns against the old
	// stand-in table would render a French address with an American postcode.
	cat atomic.Pointer[catalogue]

	// sub is the live substitution mode, held as an int32 because that is what
	// atomic offers. The test page's derived detectors override it; see
	// Detector.Substitution.
	sub atomic.Int32
}

// catalogue is everything a locale selection decides, assembled together.
//
// One value rather than three fields, so a locale change is one atomic store and
// nothing can observe half of it. Assembled in load order: the selected locales by
// their registry priority, then the locale-independent identifiers, then the
// credentials — the same order New has always used, because the first pattern to
// claim a literal wins.
type catalogue struct {
	locales  []string
	patterns []pii.Pattern
	fakes    pii.FakeSet
}

func newCatalogue(locales []string) *catalogue {
	sets := pii.LocalePatterns(locales)
	intl, secrets := pii.InternationalPatterns(), pii.SecretPatterns()

	patterns := make([]pii.Pattern, 0, len(sets)+len(intl)+len(secrets))
	patterns = append(patterns, sets...)
	patterns = append(patterns, intl...)
	patterns = append(patterns, secrets...)

	// Copied, so a caller that kept the slice it passed in cannot reorder the
	// locales this catalogue reports afterwards.
	kept := make([]string, len(locales))
	copy(kept, locales)

	return &catalogue{locales: kept, patterns: patterns, fakes: pii.NewFakeSet(locales)}
}

// catalogue returns the loaded one.
func (d *Detector) catalogue() *catalogue { return d.policy.cat.Load() }

// SetLocales replaces the country pattern sets this detector loads.
//
// The whole selection at once, as the disabled set is replaced whole: "add gb" is a
// read-modify-write, and the caller has just been shown the current selection.
//
// An unknown code is refused rather than skipped. pii.LocalePatterns skips one by
// design — it must not decide policy about a selection — so nothing below this would
// notice, and an operator who mistyped "uk" would be told the change succeeded while
// the agent went on not reading UK identifiers.
func (d *Detector) SetLocales(locales []string) error {
	for _, code := range locales {
		if _, ok := pii.LocaleByCode(code); !ok {
			return fmt.Errorf("no locale %q — this build has %s",
				code, strings.Join(pii.LocaleCodes(), ", "))
		}
	}

	// Stored in registry order whatever order they arrived in, because load order
	// settles which country claims a value both could read: nine bare digits are a
	// French SIREN under Luhn and a US routing number under the ABA weights. A
	// selection that reordered them would quietly change what those digits become.
	ordered := make([]string, 0, len(locales))
	wanted := make(map[string]bool, len(locales))
	for _, code := range locales {
		wanted[code] = true
	}
	for _, code := range pii.LocaleCodes() {
		if wanted[code] {
			ordered = append(ordered, code)
		}
	}

	d.policy.cat.Store(newCatalogue(ordered))
	return nil
}

// SetSubstitution replaces what a masked value looks like.
//
// Safe to change mid-session, and worth saying why: the vault maps a replacement to
// its original, and expansion accepts both shapes. So stand-ins minted before the
// change go on being restored while new values get tokens, and a conversation
// straddling the change round-trips either way.
func (d *Detector) SetSubstitution(mode Substitution) {
	d.policy.sub.Store(int32(mode))
}

// disabled returns the set, or nil when nothing is switched off. nil is the
// ordinary case and a nil map answers false to every lookup, so the request path
// needs no branch for it.
func (p *policy) disabled() map[pii.Category]bool {
	if p == nil {
		return nil
	}
	if set := p.off.Load(); set != nil {
		return *set
	}
	return nil
}

// Disabled reports which categories this detector has been told not to mask, in
// catalogue order.
//
// A slice rather than the map, because every caller — the status route, the menu
// bar, the supervision heartbeat — reports it, and a map handed out is a map a
// caller could write to.
func (d *Detector) Disabled() []pii.Category {
	set := d.policy.disabled()
	if len(set) == 0 {
		return nil
	}

	out := make([]pii.Category, 0, len(set))
	for cat := range set {
		out = append(out, cat)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Disable replaces the set of categories this detector will not mask.
//
// The whole set at once rather than one category toggled, and that is not a
// convenience: two surfaces — the menu bar and a browser tab on the test page —
// can be looking at this agent at the same time, and a toggle is a
// read-modify-write whose two halves can interleave into a set neither of them
// asked for. Replacing the set makes the last writer's intention the state,
// which is a rule somebody can reason about.
//
// A category that may not be switched off is refused rather than dropped: a
// caller asking for something the agent will not do has to be told, or a menu
// would draw a credential as unticked while the agent went on masking it — and
// the person reading that menu would believe the wrong thing about a live key.
func (d *Detector) Disable(cats []pii.Category) error {
	set := make(map[pii.Category]bool, len(cats))
	for _, cat := range cats {
		if _, known := pii.Info(cat); !known {
			return fmt.Errorf("no category named %q", cat)
		}
		if !pii.Switchable(cat) {
			return fmt.Errorf("category %q cannot be switched off: %s", cat, whyLocked(cat))
		}
		set[cat] = true
	}

	d.policy.off.Store(&set)
	return nil
}

// whyLocked says which rule refused, because "cannot be switched off" alone
// leaves a caller guessing between a bug and a policy.
func whyLocked(cat pii.Category) string {
	if pii.IsSecret(cat) {
		return "it is a credential, and a credential in clear is a live key handed to a provider"
	}
	return "it is what this deployment declared sensitive itself"
}

// masks reports whether this detector is masking a category at all.
func (d *Detector) masks(cat pii.Category) bool {
	return !d.policy.disabled()[cat]
}

// Masking reports how much of its catalogue the detector is applying.
//
// Three answers rather than two, and the middle one is the reason this exists: an
// agent with a category switched off is masking, and a caller that reported it as
// simply "masking" would be the green light over the traffic that is not. It is
// the same distinction the status route already draws between answering and
// masking, one level further in.
func (d *Detector) Masking() Level {
	switch {
	case len(d.catalogue().locales) == 0:
		// No country set loaded. The locale-independent identifiers and the
		// credentials are still scanned, but almost nothing else is, and calling
		// that "masking" is what the status route refuses to do.
		return LevelNone
	case len(d.policy.disabled()) > 0:
		return LevelPartial
	default:
		return LevelFull
	}
}

// Level is how much of the catalogue is being applied.
type Level int

const (
	// LevelNone is an agent that recognises almost nothing: no country pattern
	// set is loaded.
	LevelNone Level = iota
	// LevelPartial is an agent applying its catalogue with categories switched
	// off.
	LevelPartial
	// LevelFull is every category the configuration loaded.
	LevelFull
)

func (l Level) String() string {
	switch l {
	case LevelFull:
		return "full"
	case LevelPartial:
		return "partial"
	default:
		return "none"
	}
}

// Categories reports which categories this detector's patterns can actually emit,
// which is a narrower set than the catalogue.
//
// It is what any surface listing switches has to use. With only "fr" loaded the
// agent cannot recognise a US social security number at all, and an entry offering
// to stop masking one would say the agent is masking them — a switch whose only
// possible effect is to mislead the person reading it.
//
// Read from the loaded patterns rather than derived from the locale codes, because
// the pattern sets are the thing that decides: the locale-independent identifiers
// and the credentials load whatever the locales say, and a second answer to "what
// can this agent find" would be a second chance to be wrong about it.
func (d *Detector) Categories() []pii.Category {
	patterns := d.catalogue().patterns
	seen := make(map[pii.Category]bool, len(patterns))
	for _, p := range patterns {
		seen[p.Category] = true
	}

	out := make([]pii.Category, 0, len(seen))
	for cat := range seen {
		out = append(out, cat)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
