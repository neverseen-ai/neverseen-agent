package tray

import (
	"context"
	"fmt"
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

	m.showProviders(d.providers)
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
