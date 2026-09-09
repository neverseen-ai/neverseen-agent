package service

import (
	"fmt"
	"os"
)

// Install writes both launchd agents and loads them.
//
// unload before load, ignoring the unload's failure, because that is the only way to
// make loading idempotent: launchctl refuses to load an agent already loaded, and an
// installer that failed on a second run would be one nobody re-runs after editing
// their configuration.
func Install(l Layout) error {
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
		_ = run("launchctl", "unload", d.Path)
		if err := run("launchctl", "load", "-w", d.Path); err != nil {
			return err
		}
	}
	return nil
}

// Uninstall unloads both agents and deletes their definitions.
//
// Every failure is tolerated and the walk continues: uninstalling has to work on a
// half-installed machine, which is exactly the state somebody is in when they reach
// for it.
func Uninstall(l Layout) error {
	defs, err := Render(l)
	if err != nil {
		return err
	}
	for _, d := range defs {
		_ = run("launchctl", "unload", d.Path)
		if err := os.Remove(d.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", d.Path, err)
		}
	}
	return nil
}

// Restart reloads whatever is installed, leaving alone what is not.
//
// It reloads the icon too. That is deliberate and is not the KeepAlive question: a
// restart is somebody asking for the new configuration to take effect, and an icon
// still reporting the old one beside a reloaded agent is the disagreement between two
// surfaces this whole package exists to prevent.
func Restart(l Layout) error {
	defs, err := Render(l)
	if err != nil {
		return err
	}
	for _, d := range defs {
		if _, err := os.Stat(d.Path); err != nil {
			continue
		}
		_ = run("launchctl", "unload", d.Path)
		if err := run("launchctl", "load", "-w", d.Path); err != nil {
			return err
		}
	}
	return nil
}
