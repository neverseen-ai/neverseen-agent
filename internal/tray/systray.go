package tray

import (
	"context"

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
// menu says what is happening and offers no switch, because a stop button on a
// security control is a product decision and not a convenience, and because
// nothing here holds anything that could turn masking back on.
func Run(ctx context.Context, addr string) {
	if addr == "" {
		addr = proxy.DefaultListen
	}

	systray.Run(func() {
		bar := &menuBar{}
		bar.build(addr)

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
// thing the toolkit offers: there is no list to replace. It is why render returns
// a fixed number of lines.
type menuBar struct {
	entries []*systray.MenuItem
}

func (m *menuBar) build(addr string) {
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

	test := systray.AddMenuItem("Show what would be masked", "Open the agent's test page")
	quit := systray.AddMenuItem("Quit the icon", "Leave the agent running")
	go func() {
		for {
			select {
			case <-test.ClickedCh:
				openTestPage(addr)
			case <-quit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func (m *menuBar) icon(png []byte) {
	// Both arguments are the same image: the template is what macOS wants, and on
	// the platforms that have no notion of one it is the icon itself.
	systray.SetTemplateIcon(png, png)
}

func (m *menuBar) tooltip(text string) { systray.SetTooltip(text) }

func (m *menuBar) lines(lines []string) {
	for i, line := range lines {
		if i >= len(m.entries) {
			return
		}
		m.entries[i].SetTitle(line)
	}
}
