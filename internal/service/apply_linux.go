package service

import (
	"fmt"
	"os"
	"os/exec"
)

// Install writes both definitions, then enables and restarts the unit.
//
// Only the agent's unit is handed to systemctl. The icon's desktop entry is read by
// the session at the next login and by nothing before then: there is no reload to
// ask for, and no way to start it now that would give it the graphical session's
// environment. An installer that launched the icon itself would be starting it from
// the shell that ran the installer, which is the one environment it must not inherit.
func Install(l Layout) error {
	if err := requireSystemd(); err != nil {
		return err
	}
	defs, err := Render(l)
	if err != nil {
		return err
	}
	defs, err = installable(l, defs)
	if err != nil {
		return err
	}

	for _, d := range defs {
		if err := writeDefinition(d.Path, []byte(d.Content)); err != nil {
			return err
		}
	}
	if err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	if err := run("systemctl", "--user", "enable", SystemdUnit); err != nil {
		return err
	}
	// restart rather than enable --now: --now leaves an active unit as it is, so a
	// second install after editing the configuration reported success while the
	// agent kept running the old one — where macOS unloads and loads. restart also
	// starts a unit that is inactive, so a first install is served the same way.
	return run("systemctl", "--user", "restart", SystemdUnit)
}

// Uninstall disables the unit and deletes both definitions.
//
// Failures are tolerated up to the file removal: uninstalling has to work on a
// half-installed machine, which is the state somebody is in when they reach for it.
//
// The icon already running is left alone rather than killed. It polls an agent that
// is going away and will draw "not masking", which is true, and it goes at the next
// logout — where killing it means uninstall has to know a process name, on a platform
// where it is not the one that started it.
func Uninstall(l Layout) error {
	defs, err := Render(l)
	if err != nil {
		return err
	}
	_ = run("systemctl", "--user", "disable", "--now", SystemdUnit)
	for _, d := range defs {
		if err := os.Remove(d.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", d.Path, err)
		}
	}
	_ = run("systemctl", "--user", "daemon-reload")
	return nil
}

// Restart reloads the agent's unit from disk and restarts it.
//
// The icon is deliberately untouched, on this platform as on the other two: its own
// menu offers "Quit the icon", and something that put it straight back would have the
// person click it and watch nothing happen.
//
// daemon-reload first, because a restart alone runs the unit systemd already has in
// memory — so a definition this package had just rewritten would take effect at the
// next login rather than now, and the person would be told it had.
func Restart(l Layout) error {
	if err := requireSystemd(); err != nil {
		return err
	}
	if err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	return run("systemctl", "--user", "restart", SystemdUnit)
}

// requireSystemd refuses early rather than guessing at another init.
//
// A guessed init is the same failure class as a guessed CLI name in shellTools: it
// fails after somebody has believed it.
func requireSystemd() error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl not found; run `neverseen proxy` yourself")
	}
	return nil
}
