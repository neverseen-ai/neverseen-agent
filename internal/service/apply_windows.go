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

// Restart stops and starts the agent.
//
// The icon is left running, unlike on macOS. Task Scheduler's /Run starts a second
// instance rather than replacing the first where the policy allows it, and the icon's
// policy is IgnoreNew — so an /End and /Run pair on an icon somebody had quit would
// put it back, which is the one thing "Quit the icon" must not do.
func Restart(l Layout) error {
	// The stop is tolerated, as launchctl's unload is on macOS: restarting an agent
	// that is not currently running — after a crash, or before the first logon — must
	// start it rather than refuse and leave it stopped.
	_ = run("schtasks", "/End", "/TN", WindowsAgentTask)
	return run("schtasks", "/Run", "/TN", WindowsAgentTask)
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
