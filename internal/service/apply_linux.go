package service

import (
	"fmt"
	"os"
	"os/exec"
)

// Install writes the systemd user unit, enables it and restarts it.
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

// Uninstall disables the unit and deletes it.
//
// Failures are tolerated up to the file removal: uninstalling has to work on a
// half-installed machine, which is the state somebody is in when they reach for it.
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

// Restart reloads the unit from disk and restarts it.
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
