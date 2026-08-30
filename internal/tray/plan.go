package tray

import (
	"fmt"
	"sort"
)

// The menu's logic, kept out of the toolkit adapter: what each entry says, which
// slot holds it, and what a click on it means.
//
// That separation is the rule this package is built on. A menu bar cannot be
// asserted on in CI, so everything that *decides* lives where a test reaches it and
// systray.go only applies the results. Written the other way — the overflow
// arithmetic, the all-or-nothing group rule and the set computation inside the
// systray calls — this feature's behaviour would only ever have run on somebody's
// screen.
//
// It was the second half of tray.go until the file announced the split in its own
// middle, which is a file saying it should be two.

// withCategoryToggled is the set to send when one category is clicked.
func (d display) withCategoryToggled(code string) []string {
	off := d.offSet()
	if off[code] {
		delete(off, code)
	} else {
		off[code] = true
	}
	return sortedKeys(off)
}

// withGroupToggled is the set to send when a whole family is clicked.
//
// All or nothing: a click on a partly-off family turns the rest off too, rather
// than reviving what somebody switched off one at a time. Reviving them makes one
// click undo several deliberate ones, which is the surprising direction.
//
// Locked members are left alone, because the agent refuses them: including one
// would have the whole request rejected and the click do nothing at all.
func (d display) withGroupToggled(code string) []string {
	off := d.offSet()

	for _, g := range d.switches {
		if g.code != code {
			continue
		}

		allOff := true
		for _, m := range g.members {
			if !m.locked && !m.off {
				allOff = false
			}
		}
		for _, m := range g.members {
			switch {
			case m.locked:
			case allOff:
				delete(off, m.code)
			default:
				off[m.code] = true
			}
		}
	}
	return sortedKeys(off)
}

func (d display) offSet() map[string]bool {
	off := map[string]bool{}
	for _, code := range d.offCodes() {
		off[code] = true
	}
	return off
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// entryPlan is one menu entry as the adapter should set it.
//
// A plan rather than a sequence of calls, so the arithmetic that decides which slot
// holds what — and what the last slot says when there are more families than slots
// — is a value a test can read.
type entryPlan struct {
	code    string
	title   string
	visible bool
	enabled bool
	checked bool

	// members are the category entries under a family entry. A family with none
	// visible shows the "never switched off from here" line instead.
	members []entryPlan
}

// planSwitches lays the catalogue out over a fixed pool of entries.
//
// The pool is fixed because the toolkit builds a menu once and cannot add an entry
// later, which is the same reason the provider pool exists. Past the pool the last
// slot says how many families are missing rather than dropping them: a menu that
// silently omitted two would have somebody conclude the agent does not have them.
func planSwitches(d display, maxGroups, maxCats int) []entryPlan {
	plans := make([]entryPlan, maxGroups)

	shown := min(len(d.switches), maxGroups)
	overflow := len(d.switches) - maxGroups
	if overflow > 0 {
		shown = maxGroups - 1
	}

	for i := range plans {
		switch {
		case i < shown:
			plans[i] = planGroup(d.switches[i], maxCats)
		case i == shown && overflow > 0:
			plans[i] = entryPlan{
				title:   fmt.Sprintf("…and %d more — see cloakfleet status", overflow+1),
				visible: true,
			}
		}
	}
	return plans
}

// planGroup is one family and its switches.
func planGroup(g switchGroup, maxCats int) entryPlan {
	plan := entryPlan{
		code:    g.code,
		title:   g.title(),
		visible: true,
		enabled: !g.locked,
		// A locked family is ticked and cannot be unticked: everything in it is
		// being masked, and an unticked lock would say the opposite.
		checked: g.locked || !g.off,
	}

	if g.locked {
		// No rows under it. Twenty API keys nobody may switch off is twenty rows of
		// nothing to do, and a submenu that opened onto them would read as an
		// invitation.
		return plan
	}

	plan.members = make([]entryPlan, maxCats)
	for i := range plan.members {
		if i >= len(g.members) {
			continue
		}
		m := g.members[i]
		plan.members[i] = entryPlan{
			code:    m.code,
			title:   m.label,
			visible: true,
			enabled: !m.locked,
			checked: !m.off,
		}
	}
	return plan
}

// planModes is one row per substitution mode, ticked for the live one.
//
// A tick per mode rather than one item that cycles, because the toolkit has no radio
// group and a cycling item cannot say what it is about to become: "Substitution:
// token" is a state, and clicking it to get fake is a guess. Two ticked rows say
// both the state and the choice.
func planModes(d display) []entryPlan {
	out := make([]entryPlan, 0, len(d.modes))
	for _, mode := range d.modes {
		out = append(out, entryPlan{
			code:    mode,
			title:   modeTitle(mode),
			visible: true,
			// The live one is not clickable: clicking it would send the state it is
			// already in, and a menu row that does nothing is a row somebody clicks
			// twice wondering what broke.
			enabled: mode != d.substitution,
			checked: mode == d.substitution,
		})
	}
	return out
}

// modeTitle says what the mode does rather than only naming it. "token" and "fake"
// are the words the configuration uses and they have to stay, but neither says which
// one puts a value nobody can check in front of a caller.
func modeTitle(mode string) string {
	switch mode {
	case "fake":
		return "fake — reads as prose, and cannot be told from a real value"
	case "token":
		return "token — [EMAIL_1], obvious in an answer"
	default:
		return mode
	}
}

// planLocales is one row per locale the build has, ticked when it is loaded.
//
// Every one of them stays clickable, including the last one loaded: an agent with no
// locale at all is a valid state and the one it starts in, so a menu that refused to
// reach it would be hiding a state the agent can be in.
func planLocales(d display) []entryPlan {
	out := make([]entryPlan, 0, len(d.locales))
	for _, l := range d.locales {
		out = append(out, entryPlan{
			code:    l.code,
			title:   l.code,
			visible: true,
			enabled: true,
			checked: l.on,
		})
	}
	return out
}

// planLevels is one row per secret level, ticked for the live one.
//
// The same shape as planModes, and for the same reason: the toolkit has no radio
// group, and a cycling item cannot say what it is about to become.
//
// The titles say what each level costs rather than only naming it. "weak" and
// "strong" are the words the configuration uses and they have to stay, but neither
// says which one replaces the identifiers in the code somebody is asking about.
func planLevels(d display) []entryPlan {
	out := make([]entryPlan, 0, len(d.secretLevels))
	for _, level := range d.secretLevels {
		out = append(out, entryPlan{
			code:    level,
			title:   levelTitle(level),
			visible: true,
			enabled: level != d.secretLevel,
			checked: level == d.secretLevel,
		})
	}
	return out
}

func levelTitle(level string) string {
	switch level {
	case "weak":
		return "weak — every value found, words included; masks code too"
	case "medium":
		return "medium — values mixing two kinds of character"
	case "strong":
		return "strong — only what nobody typed; keeps code readable"
	default:
		return level
	}
}
