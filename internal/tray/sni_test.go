package tray

import (
	"strings"
	"testing"
)

// A desktop with a host is left alone. This is the case the whole probe exists to
// get right: refusing here would take the icon away from KDE, XFCE, Cinnamon, MATE
// and every Ubuntu session, which all draw one today.
func TestADesktopWithAHostIsNotRefused(t *testing.T) {
	for _, desktop := range []string{"KDE", "XFCE", "ubuntu:GNOME", "GNOME", ""} {
		if advice := hostAdvice(true, desktop); advice != "" {
			t.Errorf("hostAdvice(true, %q) = %q, want the icon drawn", desktop, advice)
		}
	}
}

// GNOME is the one desktop with a specific answer, and the message has to carry it.
// "No owner for org.kde.StatusNotifierWatcher" is true and useless to somebody whose
// session is the default install of Fedora or Debian.
func TestGNOMEIsToldWhichExtension(t *testing.T) {
	// "ubuntu:GNOME" is in here for the reason namesGNOME exists: a plain comparison
	// against "GNOME" sends an Ubuntu user the generic message.
	for _, desktop := range []string{"GNOME", "gnome", "ubuntu:GNOME", "GNOME:GNOME-Classic"} {
		advice := hostAdvice(false, desktop)
		if !strings.Contains(advice, "AppIndicator") {
			t.Errorf("hostAdvice(false, %q) = %q, which does not name the extension", desktop, advice)
		}
	}
}

// Every refusal says the agent is unaffected, whatever desktop it is about.
//
// This is the sentence that matters more than the rest of the message. The icon
// disappearing is exactly what it is meant to look like when masking has stopped, so
// a message that does not say otherwise makes the icon lie on its way out.
func TestEveryRefusalSaysTheAgentIsUnaffected(t *testing.T) {
	for _, desktop := range []string{"GNOME", "KDE", "", "sway"} {
		advice := hostAdvice(false, desktop)
		if advice == "" {
			t.Fatalf("hostAdvice(false, %q) refused nothing", desktop)
		}
		if !strings.Contains(advice, "still masking") || !strings.Contains(advice, "neverseen status") {
			t.Errorf("hostAdvice(false, %q) = %q, which leaves the reader thinking masking stopped", desktop, advice)
		}
	}
}

// An unset XDG_CURRENT_DESKTOP is a gap in the session, not something to report as
// an empty value: "(XDG_CURRENT_DESKTOP=)" reads as a bug in this message.
func TestAnUnnamedDesktopIsNotReportedAsEmpty(t *testing.T) {
	if advice := hostAdvice(false, "  "); strings.Contains(advice, "XDG_CURRENT_DESKTOP") {
		t.Errorf("hostAdvice(false, blank) = %q, which reports a name it does not have", advice)
	}
}
