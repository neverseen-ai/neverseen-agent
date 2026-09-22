//go:build linux

package tray

import (
	"os"

	"github.com/godbus/dbus/v5"
)

// whyNoIcon asks the session bus whether anything would draw an icon, and reports
// why not when nothing would.
//
// The only file in this package that speaks dbus, exactly as systray.go is the only
// one that speaks to the toolkit. godbus is already in the build — fyne.io/systray
// reaches the session bus through it on this platform — so this adds a direct import
// of a module that was already being linked, not a dependency.
//
// A session bus that cannot be reached is the same answer as a bus with no host on
// it: in both cases nothing is going to draw anything, and the difference is not one
// the person can act on differently.
func whyNoIcon() string {
	conn, err := dbus.SessionBus()
	if err != nil {
		return hostAdvice(false, os.Getenv("XDG_CURRENT_DESKTOP"))
	}

	var owned bool
	// NameHasOwner rather than ListNames: the answer is one boolean about one name,
	// and listing every name on the bus to search it is the same question asked the
	// expensive way.
	if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, watcherName).Store(&owned); err != nil {
		owned = false
	}
	return hostAdvice(owned, os.Getenv("XDG_CURRENT_DESKTOP"))
}
