package tray

import (
	"context"
	"fmt"
	"log"
	"sync"

	"fyne.io/systray"

	"github.com/cloakfleet/cloakfleet/internal/proxy"
)

// Run puts the icon in the menu bar and keeps it up to date until the context is
// done or the icon's own Quit is chosen.
//
// It blocks, and it must be called from the main goroutine: systray.Run owns the
// platform event loop, and on macOS that loop has to be the main thread — AppKit
// refuses to be driven from anywhere else.
//
// Quitting the icon does not stop the agent. It is deliberately an observer: the
// menu says what is happening, hands over the line that points a tool at it, and
// offers no switch — a stop button on a security control is a product decision and
// not a convenience, and nothing here holds anything that could turn masking back
// on.
func Run(ctx context.Context, addr string) {
	if addr == "" {
		addr = proxy.DefaultListen
	}

	systray.Run(func() {
		bar := &menuBar{addr: addr}
		bar.build()

		go watch(ctx, bar, func() proxy.Status { return ask(addr) }, pollEvery)
		go func() {
			// The icon goes when the process is asked to stop, so a logout does not
			// leave a dead item in the bar until the session ends.
			<-ctx.Done()
			systray.Quit()
		}()
	}, func() {})
}

// menuBar is the view backed by the real menu bar.
//
// Entries are created once and their titles rewritten, because that is the only
// thing the toolkit offers: there is no list to replace. It is why render returns a
// fixed number of lines, and why the provider submenu is a bounded pool of slots
// rather than a list built when the answer arrives.
type menuBar struct {
	addr string

	entries []*systray.MenuItem

	// mu guards what the provider slots currently stand for. watch writes them on
	// its own goroutine while the click handlers read them on theirs, and without
	// it a slot relabelled at the moment of a click copies another provider's line
	// — a bug that would look like the menu being wrong now and then.
	mu    sync.Mutex
	slots []providerSlot

	// levels is the pool of secret-strength rows, drawn like the modes are.
	levels []catSlot

	// groups is the pool of switch entries, created up front and revealed as the
	// agent reports its catalogue — the toolkit builds a menu once and there is no
	// adding an entry later, which is the same reason the provider pool exists.
	groups []groupSlot

	// modes and locales are the pools for the two choices, created up front like
	// every other entry: the toolkit builds a menu once.
	modes   []catSlot
	locales []catSlot

	// shown is the display the menu is currently drawing. A click computes the new
	// set from it, so what is sent is the whole set rather than one toggle: two
	// surfaces looking at one agent would otherwise interleave the halves of a
	// read-modify-write into a set neither asked for.
	shown display
}

// groupSlot is one group entry and its pool of category entries.
type groupSlot struct {
	item  *systray.MenuItem
	code  string
	cats  []catSlot
	empty *systray.MenuItem // shown in place of the categories when the group is locked
}

// catSlot is one category entry and what it currently stands for.
type catSlot struct {
	item *systray.MenuItem
	code string
}

// providerSlot is one entry in the provider submenu and the provider it currently
// stands for. The code is empty while the slot is hidden.
type providerSlot struct {
	item *systray.MenuItem
	code string
}

func (m *menuBar) build() {
	systray.SetTemplateIcon(unmaskedIcon, unmaskedIcon)
	systray.SetTooltip("cloakfleet")

	m.entries = make([]*systray.MenuItem, 0, lineCount)
	for range lineCount {
		entry := systray.AddMenuItem("", "")
		// Shown, not clickable. These are the state, and a menu whose state lines
		// could be pressed invites somebody to press one.
		entry.Disable()
		m.entries = append(m.entries, entry)
	}

	systray.AddSeparator()
	m.buildSwitches()
	m.buildSubstitution()
	m.buildSecretLevels()
	m.buildLocales()
	m.buildProviders()

	// The tooltip carries the address rather than restating the label: the page is
	// served by the agent this icon is watching, and which agent that is on a
	// machine with a non-default listen address is the one thing the label cannot
	// say.
	test := systray.AddMenuItem("Open the test page", "http://"+m.addr+"/test")
	quit := systray.AddMenuItem("Quit the icon", "Leave the agent running")
	go func() {
		for {
			select {
			case <-test.ClickedCh:
				openTestPage(m.addr)
			case <-quit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

// maxGroupEntries and maxCategoryEntries bound the switch pools.
//
// Eight groups because the catalogue has seven and a new family is one entry in a
// registry; twelve categories because the largest switchable group has ten. Both
// are the provider pool's reasoning: a menu is built once, so the ceiling has to be
// picked in advance, and past it the entries say how many are missing rather than
// dropping them quietly.
const (
	maxGroupEntries    = 8
	maxCategoryEntries = 12

	// maxModeEntries and maxLocaleEntries bound the other two pools. Two modes and
	// three locales today; the locale registry names Germany, Spain, Italy and the
	// Netherlands as the next four, so eight leaves room for all of them without a
	// menu nobody can read.
	maxModeEntries   = 4
	maxLocaleEntries = 8

	// maxLevelEntries bounds the secret-strength pool. Three levels today, and the
	// scale is a judgement rather than a registry — a fourth would mean a new
	// answer to "how much does this look like a credential", not a new country.
	maxLevelEntries = 4
)

// buildSwitches creates the group entries and their category entries, all hidden.
//
// Every entry that can be ticked is a checkbox from the start: the toolkit decides
// whether an item has a tick box when it is created, and an item that became one
// later would need a menu rebuilt, which is exactly what it cannot do.
func (m *menuBar) buildSwitches() {
	parent := systray.AddMenuItem("What gets masked", "One entry per family of values this agent recognises")

	caution := parent.AddSubMenuItem("Unticking sends those values to the provider in clear", "")
	caution.Disable()
	parent.AddSeparator()

	m.groups = make([]groupSlot, 0, maxGroupEntries)
	for range maxGroupEntries {
		item := parent.AddSubMenuItemCheckbox("", "", true)
		item.Hide()

		slot := groupSlot{item: item, cats: make([]catSlot, 0, maxCategoryEntries)}
		// Shown instead of the categories when nothing in the group may be
		// switched: twenty API keys nobody may touch is twenty rows of nothing to
		// do, and a submenu that opened onto them would read as an invitation.
		slot.empty = item.AddSubMenuItem("Never switched off from here", "")
		slot.empty.Disable()
		slot.empty.Hide()

		for range maxCategoryEntries {
			cat := item.AddSubMenuItemCheckbox("", "", true)
			cat.Hide()
			slot.cats = append(slot.cats, catSlot{item: cat})
		}

		m.groups = append(m.groups, slot)
		go m.watchClicks(item.ClickedCh, m.groupCode(len(m.groups)-1), toGroup)
		for c := range slot.cats {
			go m.watchClicks(slot.cats[c].item.ClickedCh, m.categoryCode(len(m.groups)-1, c), toCategory)
		}
	}

	systray.AddSeparator()
	restore := systray.AddMenuItem("Mask everything again", "Switch every category back on")
	go func() {
		for range restore.ClickedCh {
			m.mu.Lock()
			shown := m.shown
			m.mu.Unlock()
			m.apply(shown.policyWith([]string{}, "", nil, ""))
		}
	}()
	systray.AddSeparator()
}

// buildSubstitution creates the mode rows.
func (m *menuBar) buildSubstitution() {
	parent := systray.AddMenuItem("Substitution", "What a masked value is replaced by")

	m.modes = make([]catSlot, 0, maxModeEntries)
	for range maxModeEntries {
		item := parent.AddSubMenuItemCheckbox("", "", false)
		item.Hide()
		m.modes = append(m.modes, catSlot{item: item})
		go m.watchClicks(item.ClickedCh, m.modeCode(len(m.modes)-1), toMode)
	}
}

// buildSecretLevels creates the level rows.
func (m *menuBar) buildSecretLevels() {
	parent := systray.AddMenuItem("Secret strength", "How far down the scale a named secret is masked")

	caution := parent.AddSubMenuItem("A key with a known prefix is masked at every level", "")
	caution.Disable()
	parent.AddSeparator()

	m.levels = make([]catSlot, 0, maxLevelEntries)
	for range maxLevelEntries {
		item := parent.AddSubMenuItemCheckbox("", "", false)
		item.Hide()
		m.levels = append(m.levels, catSlot{item: item})
		go m.watchClicks(item.ClickedCh, m.levelCode(len(m.levels)-1), toLevel)
	}
}

// watchClicks turns every click on one entry into the whole policy to send.
//
// One loop rather than the five it was. They differed only in where the entry's code
// came from and which part of the policy the click replaced, and written out five
// times the shape had to be got right five times: take the lock, read the code *and*
// the display under it, release, ignore a hidden slot, send. The lock is the part
// that matters — a slot relabelled between reading its code and reading the display
// would send one entry's change against another entry's state.
//
// What a click means still lives in plan.go, where a test reaches it: the
// all-or-nothing rule for a family, the arithmetic around locked members and the
// locale toggle are decisions, and decisions do not belong in the half of this
// package that only ever runs on somebody's screen.
//
// A hidden slot is skipped rather than sent. The pools are built full and revealed
// as the agent reports its catalogue, so an entry with no code behind it is one the
// toolkit is drawing at nothing.
func (m *menuBar) watchClicks(clicks <-chan struct{}, code func() string,
	want func(d display, code string) proxy.Policy) {
	for range clicks {
		m.mu.Lock()
		this, shown := code(), m.shown
		m.mu.Unlock()
		if this == "" {
			continue
		}
		m.apply(want(shown, this))
	}
}

// The five things a click can change, as the policy each one sends. The route
// replaces the whole state rather than patching it, so every one of these carries
// the other parts through unchanged.
var (
	toGroup    = func(d display, code string) proxy.Policy { return d.policyWith(d.withGroupToggled(code), "", nil, "") }
	toCategory = func(d display, code string) proxy.Policy {
		return d.policyWith(d.withCategoryToggled(code), "", nil, "")
	}
	toMode   = func(d display, code string) proxy.Policy { return d.policyWith(nil, code, nil, "") }
	toLocale = func(d display, code string) proxy.Policy { return d.policyWith(nil, "", d.withLocaleToggled(code), "") }
	toLevel  = func(d display, code string) proxy.Policy { return d.policyWith(nil, "", nil, code) }
)

// buildLocales creates the locale rows.
func (m *menuBar) buildLocales() {
	parent := systray.AddMenuItem("Countries", "Which country's identifiers to look for")

	caution := parent.AddSubMenuItem("With none of them, only credentials and email are found", "")
	caution.Disable()
	parent.AddSeparator()

	m.locales = make([]catSlot, 0, maxLocaleEntries)
	for range maxLocaleEntries {
		item := parent.AddSubMenuItemCheckbox("", "", false)
		item.Hide()
		m.locales = append(m.locales, catSlot{item: item})
		go m.watchClicks(item.ClickedCh, m.localeCode(len(m.locales)-1), toLocale)
	}
}

// apply sends the new set to the agent and redraws from its answer.
//
// Redrawn from what the agent said rather than from what was asked for, because the
// agent is the one that refuses: a click on something it will not switch off would
// otherwise leave the menu showing it unticked while the value went on being
// masked, and somebody would believe the wrong thing about a live key.
func (m *menuBar) apply(want proxy.Policy) {
	status, err := proxy.SetPolicy(context.Background(), m.addr,
		proxy.ReadControlKey(""), want, askTimeout)
	if err != nil {
		// Nothing to do but leave the menu as it is: the next poll redraws it from
		// the agent, so a refused click corrects itself within pollEvery rather than
		// leaving a tick that lies.
		log.Printf("cloakfleet-tray: %v", err)
		return
	}
	m.show(render(status))
}

// buildProviders creates the submenu and its pool of slots, all hidden.
//
// Created up front and revealed as the agent reports what it serves, because the
// toolkit builds a menu once and there is no adding an entry later. That is the
// whole reason maxProviderEntries exists.
func (m *menuBar) buildProviders() {
	parent := systray.AddMenuItem("Copy the command for", "One line per provider this agent serves")

	// First in the submenu and never clickable, because of what these lines are.
	// They are unconditional exports, and an unconditional export in a login file is
	// the Agent Veil failure: every LLM tool on the machine breaking the day the
	// proxy stops, from a line nobody remembers adding. The form that is safe in a
	// profile is the one that prints nothing while the agent is down, and the place
	// to say so is where the line is handed over.
	caution := parent.AddSubMenuItem(`For one shell — a profile wants: eval "$(cloakfleet env)"`, "")
	caution.Disable()
	parent.AddSeparator()

	m.slots = make([]providerSlot, 0, maxProviderEntries)
	for range maxProviderEntries {
		item := parent.AddSubMenuItem("", "")
		item.Hide()
		m.slots = append(m.slots, providerSlot{item: item})

		go m.watchSlot(len(m.slots) - 1)
	}
}

// watchSlot copies the line for whatever its slot currently stands for.
//
// One goroutine per slot rather than one select over all of them: the toolkit gives
// each entry a channel of its own, and what a slot means changes under it whenever
// the agent reports a different set.
func (m *menuBar) watchSlot(index int) {
	for range m.slots[index].item.ClickedCh {
		m.mu.Lock()
		code := m.slots[index].code
		m.mu.Unlock()
		if code == "" {
			continue
		}

		if err := copyToClipboard(proxy.PointAt(code, m.addr)); err != nil {
			// Said in the menu rather than swallowed. An entry that looked like it
			// worked and did not is worse than one that admits it, and the menu is
			// the only place a person would look. The next poll puts the label back.
			m.slots[index].item.SetTitle(code + " — could not reach the clipboard")
			continue
		}
		m.slots[index].item.SetTitle(code + " — copied")
	}
}

// show puts a rendered display on screen.
func (m *menuBar) show(d display) {
	// Both arguments are the same image: the template is what macOS wants, and on
	// the platforms that have no notion of one it is the icon itself.
	systray.SetTemplateIcon(d.icon, d.icon)
	systray.SetTooltip(d.tooltip)

	for i, line := range d.lines {
		if i >= len(m.entries) {
			break
		}
		m.entries[i].SetTitle(line)
	}

	m.showSwitches(d)
	m.showRows(m.modes, planModes(d))
	m.showRows(m.levels, planLevels(d))
	m.showRows(m.locales, planLocales(d))
	m.showProviders(d.providers)
}

// showSwitches applies the plan planSwitches worked out.
//
// No arithmetic here on purpose: which slot holds what, and what the last one says
// when there are more families than slots, is decided in tray.go where a test can
// read it.
func (m *menuBar) showSwitches(d display) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Kept under the same lock as the slots, because a click reads it to work out
	// the new set while a poll is writing it.
	m.shown = d

	for i, plan := range planSwitches(d, len(m.groups), maxCategoryEntries) {
		slot := &m.groups[i]
		slot.code = plan.code

		if !plan.visible {
			m.hideCategories(i)
			slot.empty.Hide()
			slot.item.Hide()
			continue
		}

		slot.item.SetTitle(plan.title)
		setEnabled(slot.item, plan.enabled)
		setChecked(slot.item, plan.checked)

		for c := range slot.cats {
			if c >= len(plan.members) || !plan.members[c].visible {
				slot.cats[c].code = ""
				slot.cats[c].item.Hide()
				continue
			}
			member := plan.members[c]
			slot.cats[c].code = member.code
			slot.cats[c].item.SetTitle(member.title)
			setEnabled(slot.cats[c].item, member.enabled)
			setChecked(slot.cats[c].item, member.checked)
			slot.cats[c].item.Show()
		}

		// The stand-in line appears exactly when no category row does, which is the
		// locked family and the overflow entry.
		if len(plan.members) == 0 {
			slot.empty.Show()
		} else {
			slot.empty.Hide()
		}
		slot.item.Show()
	}
}

func (m *menuBar) hideCategories(index int) {
	for i := range m.groups[index].cats {
		m.groups[index].cats[i].code = ""
		m.groups[index].cats[i].item.Hide()
	}
}

// showRows applies a flat plan to a flat pool, which is the whole of what the two
// choices need. No arithmetic here either: planModes and planLocales decide.
func (m *menuBar) showRows(slots []catSlot, plans []entryPlan) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range slots {
		if i >= len(plans) || !plans[i].visible {
			slots[i].code = ""
			slots[i].item.Hide()
			continue
		}
		slots[i].code = plans[i].code
		slots[i].item.SetTitle(plans[i].title)
		setEnabled(slots[i].item, plans[i].enabled)
		setChecked(slots[i].item, plans[i].checked)
		slots[i].item.Show()
	}
}

func setEnabled(item *systray.MenuItem, enabled bool) {
	if enabled {
		item.Enable()
		return
	}
	item.Disable()
}

// setChecked drives the toolkit's tick to a state rather than toggling it, because
// a poll redraws the whole menu and a toggle would invert whatever was there.
func setChecked(item *systray.MenuItem, checked bool) {
	if checked {
		item.Check()
		return
	}
	item.Uncheck()
}

// showProviders labels one slot per provider and hides the rest.
func (m *menuBar) showProviders(codes []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	shown := min(len(codes), len(m.slots))
	// Past the pool, the last slot says how many are missing instead of holding one
	// more provider. Dropping them quietly would have somebody conclude the agent
	// does not serve them.
	overflow := len(codes) - len(m.slots)
	if overflow > 0 {
		shown = len(m.slots) - 1
	}

	for i := range m.slots {
		switch {
		case i < shown:
			code := codes[i]
			m.slots[i].code = code
			m.slots[i].item.SetTitle(code)
			// The line first and on its own, because the tooltip's job is to show
			// exactly what a click will copy. Anything the tool does not simply
			// honour follows on the next line.
			tip := proxy.PointAt(code, m.addr)
			if caveat := proxy.CaveatFor(code); caveat != "" {
				tip += "\n" + caveat
			}
			m.slots[i].item.SetTooltip(tip)
			m.slots[i].item.Show()
		case i == shown && overflow > 0:
			m.slots[i].code = ""
			m.slots[i].item.SetTitle(fmt.Sprintf("…and %d more — see cloakfleet env", overflow+1))
			m.slots[i].item.SetTooltip("")
			m.slots[i].item.Show()
		default:
			m.slots[i].code = ""
			m.slots[i].item.Hide()
		}
	}
}

// The code readers, one per pool. Each is called by watchClicks with m.mu held: the
// slots are rewritten by a poll on one goroutine while the clicks arrive on another.
//
// Every one of them reads through the receiver rather than closing over the slice.
// The pools are built at full capacity so append never moves them today, but a
// closure holding its own slice header would read a stale backing array the day one
// of those bounds was raised past its capacity — a click that then sends the code of
// whatever the slot used to be.
func (m *menuBar) modeCode(i int) func() string { return func() string { return m.modes[i].code } }

func (m *menuBar) levelCode(i int) func() string { return func() string { return m.levels[i].code } }

func (m *menuBar) localeCode(i int) func() string {
	return func() string { return m.locales[i].code }
}

func (m *menuBar) groupCode(i int) func() string { return func() string { return m.groups[i].code } }

func (m *menuBar) categoryCode(group, cat int) func() string {
	return func() string { return m.groups[group].cats[cat].code }
}
