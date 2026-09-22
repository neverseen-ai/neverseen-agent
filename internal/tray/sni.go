package tray

import "strings"

// Whether anything on this desktop will draw a tray icon at all.
//
// macOS and Windows have a menu bar and a notification area; Linux has neither as a
// platform feature. What it has is a convention — StatusNotifierItem, published on
// the session bus, and drawn by whatever happens to be listening. KDE, XFCE,
// Cinnamon, MATE, Budgie and LXQt all listen. GNOME Shell does not, unless the
// AppIndicator extension is installed, and GNOME is the default on Fedora, Debian
// and RHEL.
//
// The icon is registered on Linux all the same, because the alternative was what
// this repository shipped before: no icon anywhere on Linux, including on the
// desktops that would have shown one perfectly well. What makes registering it
// honest is this file — an icon that cannot be drawn says so and leaves, rather than
// sitting in the process table drawing nothing.

// watcherName is the bus name a StatusNotifierItem host takes.
//
// The org.kde prefix is not a KDE dependency: the specification came from KDE, was
// adopted by freedesktop.org, and the name was never renamed. Every implementation
// owns this one.
const watcherName = "org.kde.StatusNotifierWatcher"

// hostAdvice reports why no icon will appear, or "" when one will.
//
// Pure, and taking the answer rather than asking for it, for the reason the package
// doc gives about render and watch: a session bus is no more assertable in CI than a
// menu bar is. sni_linux.go is the half that talks to dbus and this is the half that
// decides, and only this one has a test.
//
// It asks one question — does anything own the watcher name — and deliberately does
// not read IsStatusNotifierHostRegistered beside it. Some hosts never set that
// property, so requiring it would refuse to draw on a desktop that works, which is a
// worse failure than the silence this replaces: the icon would be absent where it
// used to be absent *and* absent where it would have shown.
func hostAdvice(watcherPresent bool, desktop string) string {
	if watcherPresent {
		return ""
	}

	// Named first, because the person reading this in a log needs to know it is not
	// the agent that has stopped. The icon going is the one thing that must never be
	// read as the masking going.
	const preamble = "nothing on this desktop draws tray icons, so there is no icon to show. " +
		"The agent is unaffected and still masking — `neverseen status` answers the same question, " +
		"and exits non-zero unless it is masking everything."

	if namesGNOME(desktop) {
		return preamble + "\nGNOME shows a StatusNotifierItem only through an extension: install " +
			"\"AppIndicator and KStatusNotifierItem Support\", then log out and back in."
	}
	return preamble + "\nNo owner for " + watcherName + " on the session bus" + describeDesktop(desktop) + "."
}

// namesGNOME reads the desktop out of XDG_CURRENT_DESKTOP.
//
// Colon-separated and not a single name: Ubuntu sets "ubuntu:GNOME", and a plain
// comparison would send an Ubuntu user the generic message when theirs is the one
// desktop with a specific answer. Case-insensitively, because the value is set by
// the session and "gnome" is as common as "GNOME".
func namesGNOME(desktop string) bool {
	for part := range strings.SplitSeq(desktop, ":") {
		if strings.EqualFold(strings.TrimSpace(part), "GNOME") {
			return true
		}
	}
	return false
}

// describeDesktop names the desktop when the session said what it is.
//
// Omitted rather than reported as empty: "(desktop: )" in a log tells the reader
// nothing and looks like a bug in this message rather than a gap in their session.
func describeDesktop(desktop string) string {
	if strings.TrimSpace(desktop) == "" {
		return ""
	}
	return " (XDG_CURRENT_DESKTOP=" + desktop + ")"
}
