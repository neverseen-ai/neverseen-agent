package service

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"unicode/utf16"
)

// Install writes both task definitions and registers them with Task Scheduler.
//
// /F on create, so a second run replaces rather than fails: an installer that could
// not be re-run after somebody edited their configuration is one nobody re-runs.
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
		if err := writeDefinition(d.Path, utf16LE(d.Content)); err != nil {
			return err
		}
		if err := run("schtasks", "/Create", "/TN", d.Label, "/XML", d.Path, "/F"); err != nil {
			return err
		}
	}
	// The agent is started now rather than at the next logon, so that installing and
	// then running `neverseen status` does not report an agent that is not there.
	// /End first, as Restart does: /Create /F replaces the definition but leaves the
	// running instance in place, and its policy is IgnoreNew — so on a second install
	// after editing the configuration the /Run was dropped and the old agent kept
	// running, where macOS unloads and loads. The stop is tolerated because on a first
	// install there is nothing to end.
	// The icon is not touched: it would appear over a session the person did not ask
	// it into.
	_ = run("schtasks", "/End", "/TN", WindowsAgentTask)
	return run("schtasks", "/Run", "/TN", WindowsAgentTask)
}

// Uninstall deletes both tasks and their definitions.
//
// Failures are tolerated: uninstalling has to work on a half-installed machine, which
// is the state somebody is in when they reach for it.
func Uninstall(l Layout) error {
	defs, err := Render(l)
	if err != nil {
		return err
	}
	for _, d := range defs {
		_ = run("schtasks", "/End", "/TN", d.Label)
		_ = run("schtasks", "/Delete", "/TN", d.Label, "/F")
		if err := os.Remove(d.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", d.Path, err)
		}
	}
	return nil
}

// Restart stops and starts whatever is installed, the icon included — as macOS does.
//
// It used to spare the icon, on the ground that an /End and /Run pair would put back
// an icon somebody had quit. That is what macOS's unload and load do too, on purpose:
// a restart is somebody asking for the configuration to take effect, and an icon still
// reporting the old one beside a restarted agent is the disagreement between two
// surfaces this package exists to prevent. One verb, one meaning, on both platforms
// that can restart an icon; Linux cannot (see apply_linux.go).
//
// "Quit the icon" still works: nothing restarts it but this verb, asked for by name.
// The definition file is what says a task was installed — Install writes it for every
// task it registers — so an icon that was never registered is not started here.
func Restart(l Layout) error {
	defs, err := Render(l)
	if err != nil {
		return err
	}
	for _, d := range defs {
		if _, err := os.Stat(d.Path); err != nil {
			continue
		}
		// The stop is tolerated, as launchctl's unload is on macOS: restarting a task
		// that is not running — after a crash, after "Quit the icon", before the first
		// logon — must start it rather than refuse and leave it stopped. /End first
		// because /Run on a running instance is ignored under IgnoreNew.
		_ = run("schtasks", "/End", "/TN", d.Label)
		if err := run("schtasks", "/Run", "/TN", d.Label); err != nil {
			return err
		}
	}
	return nil
}

// utf16LE encodes for schtasks, which reads an imported task as Unicode and rejects
// UTF-8 with a message about the file being invalid rather than about its encoding.
//
// The byte order mark is what it keys on. Rendering stays UTF-8 so a golden file is
// readable in a diff; the encoding is applied here, where the file is written.
func utf16LE(s string) []byte {
	var b bytes.Buffer
	b.Write([]byte{0xFF, 0xFE})
	for _, unit := range utf16.Encode([]rune(s)) {
		_ = binary.Write(&b, binary.LittleEndian, unit)
	}
	return b.Bytes()
}
