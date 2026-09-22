package service

import (
	"fmt"
	"strings"
)

// AutostartEntry is the desktop entry's file name, which is also what Apply and
// Remove address it by.
const AutostartEntry = "neverseen-tray.desktop"

// renderAutostart produces the XDG desktop entry that starts the icon at login.
//
// # Why not a systemd user unit, when the agent is one
//
// The agent is a background job and the icon is a desktop one, and Linux registers
// those in two different places. A unit would be uniform with the agent and wrong in
// the way that matters: it would have to be wanted by graphical-session.target, which
// only the desktops with systemd session integration ever reach, and it would start
// in an environment that has DISPLAY, WAYLAND_DISPLAY and DBUS_SESSION_BUS_ADDRESS
// only if the session remembered to import them. The failure mode is a unit that
// never starts and says nothing — which is the exact failure this whole change exists
// to remove. ~/.config/autostart is understood by GNOME, KDE, XFCE, Cinnamon, MATE
// and LXQt without exception, and what it starts is a child of the graphical session
// with that session's environment already in it.
//
// # Why it redirects rather than being run bare
//
// A desktop entry has no StandardErrorPath, and what this process has to say on Linux
// it says on the way out: see internal/tray/sni.go. Sent nowhere, the one message
// explaining why there is no icon would be lost on every desktop that needs it, so
// the entry carries the redirection the launchd plist gets as a key.
//
// Nothing supervises it, and that is the same decision as the missing KeepAlive on
// macOS: the icon's own menu offers "Quit the icon", and something that put it
// straight back would have the person click it and watch nothing happen.
func renderAutostart(l Layout) Definition {
	// Two layers of quoting, and they happen to be the same one. The inner layer is
	// for the shell that reads `sh -c`; the outer is the Desktop Entry
	// specification's, which reserves the same four characters and escapes them the
	// same way — double quotes around the value, backslash before a quote, a dollar,
	// a backtick or a backslash. One function, because two that did the same thing
	// would drift and only one of them would have a test.
	command := fmt.Sprintf(`exec %s >> %s 2>&1`,
		quoteForShell(l.Binary(JobTray)), quoteForShell(l.LogFile))

	// Escaped at the value rather than over the whole file: "%" is a field code
	// introducer in Exec and an ordinary character in every other key, so a pass over
	// the entry would corrupt a Comment. Both paths land in Exec, which is what makes
	// escaping here sufficient and would stop being true the moment one reached
	// another key.
	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Neverseen
Comment=Whether this workstation is masking
Exec=/bin/sh -c %s
Terminal=false
X-GNOME-Autostart-enabled=true
# Nothing restarts this, unlike the agent's unit. The icon's own menu offers "Quit
# the icon", and a supervisor would put it straight back — the person would click it
# and watch nothing happen. The agent is supervised because nobody is meant to be
# able to stop the masking by accident; the icon is only a window onto it, and
# closing a window has to work. It returns at the next login.
`, escapeDesktop(quoteForShell(command)))

	return Definition{
		Job:     JobTray,
		Label:   AutostartEntry,
		Path:    l.join(l.Home, ".config", "autostart", AutostartEntry),
		Content: content,
	}
}

// escapeDesktop renders a value safe to place in a desktop entry's Exec.
//
// "%" starts a field code there — %f, %U, %i and friends — and a literal one has to
// be doubled or the desktop swallows it with the character after it. A home directory
// carrying a percent is not hypothetical, and the symptom is an icon that starts
// against a path with two characters missing from it, which is to say never starts.
//
// The same operation as escapeSystemd and deliberately not the same function: they
// double the percent for two unrelated reasons, and a shared helper would invite
// somebody fixing one format to change the other.
func escapeDesktop(s string) string { return strings.ReplaceAll(s, "%", "%%") }
