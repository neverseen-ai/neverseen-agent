//go:build !windows

// Not on Windows, because the thing under test refuses to run there: install.sh's
// platform() accepts Darwin and Linux and dies on anything else, so every case here
// would assert against that one refusal. Windows has install.ps1 instead, and the CI
// job for that platform parses it — the same coverage by the only means that exist
// there. Without this constraint the windows job failed on `go test ./...` from the
// day it was added, which is how it was found.

// Package install holds nothing but a test, and the thing it tests is not Go.
//
// install.sh had five failures found by driving it by hand, every one of them
// silent: a trap that died on an unbound variable and changed the exit status, a
// digest looked up with the archive's name used as a regular expression, an archive
// entry that could be a link, a login file searched by substring — which deleted
// lines the person had written themselves — and a second option quietly dropped.
// Nothing in the tree exercised any of it, and "nothing enters the tree unexercised"
// is the rule the rest of this repository is held to.
//
// A Go test rather than a shell harness, because it costs nothing to add: it is in
// `make test` and in CI already, `t.TempDir` gives every case a home and a prefix to
// ruin, and a failure prints like every other failure here. The script is driven as
// a subprocess — the real file, not a copy with the awkward parts removed.
//
// TODO: the known ceiling is everything behind the network and the platform. The
// download itself, the launchd and systemd registration, and the build from source
// are not reached here; `make e2e-claude` and a real install are what cover those. A
// case added here must not reach them either — every one of these runs offline, with
// HOME and NEVERSEEN_PREFIX pointing inside t.TempDir.
package install

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// script is the real install.sh, two directories up.
func script(t *testing.T) string {
	t.Helper()

	path, err := filepath.Abs(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("install.sh is not where this test expects it: %v", err)
	}
	return path
}

// run executes the script with a home and a prefix of its own, and never anything
// outside them: a test that installed into the person running it would be worse
// than no test.
func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	home := t.TempDir()
	return runIn(t, home, args...)
}

func runIn(t *testing.T, home string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "sh", append([]string{script(t)}, args...)...)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"NEVERSEEN_PREFIX="+filepath.Join(home, "prefix"),
	)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb

	// Run first, then read the builders: in a single return expression Go
	// evaluates left to right, so out.String() would be read before the command
	// had written a byte.
	code = exitCode(t, cmd.Run())
	return out.String(), errb.String(), code
}

// exitCode is the status a refusal exits with. A script that could not be started
// at all is a broken test rather than a refusal, so it fails loudly here.
func exitCode(t *testing.T, err error) int {
	t.Helper()

	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("running install.sh: %v", err)
	}
	return exit.ExitCode()
}

// call runs one of the script's functions with inputs of this test's choosing,
// through the library mode the script grew for exactly this.
func call(t *testing.T, home, snippet string) (output string, code int) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "sh", "-c",
		". "+script(t)+"\n"+snippet)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"NEVERSEEN_PREFIX="+filepath.Join(home, "prefix"),
		"NEVERSEEN_INSTALL_LIB=1",
	)
	var both strings.Builder
	cmd.Stdout = &both
	cmd.Stderr = &both

	code = exitCode(t, cmd.Run())
	return both.String(), code
}

// A release archive's name is full of dots, and as a regular expression each one
// matches any character. Two names differing only where a dot sits is not a
// contrivance — it is amd64 beside arm64 on a version with a dot in the same place —
// and the wrong line means installing a binary whose digest was never checked.
func TestTheDigestIsFoundByNameAndNotByPattern(t *testing.T) {
	home := t.TempDir()
	archive := filepath.Join(home, "neverseen_1.2.3_darwin_amd64.tar.gz")
	write(t, archive, "the real archive")

	// The decoy differs from the wanted name only at the dot, which as an ERE
	// matches the underscore. Listed first, so a pattern match takes it.
	checksums := filepath.Join(home, "checksums.txt")
	write(t, checksums, strings.Join([]string{
		hash("something else") + "  neverseen_1x2.3_darwin_amd64.tar.gz",
		hash("the real archive") + "  neverseen_1.2.3_darwin_amd64.tar.gz",
	}, "\n")+"\n")

	out, code := call(t, home,
		"verify_checksum "+archive+" "+checksums+" neverseen_1.2.3_darwin_amd64.tar.gz")

	if code != 0 {
		t.Fatalf("a matching archive was refused (exit %d):\n%s", code, out)
	}
}

// The coreutils format marks a binary read with a leading `*`, and a release's
// checksums.txt is written that way.
func TestTheDigestIsFoundWithTheBinaryMarker(t *testing.T) {
	home := t.TempDir()
	archive := filepath.Join(home, "a.tar.gz")
	write(t, archive, "contents")
	checksums := filepath.Join(home, "checksums.txt")
	write(t, checksums, hash("contents")+" *a.tar.gz\n")

	if out, code := call(t, home, "verify_checksum "+archive+" "+checksums+" a.tar.gz"); code != 0 {
		t.Fatalf("the binary-read marker was not understood (exit %d):\n%s", code, out)
	}
}

func TestAnArchiveNotListedIsRefused(t *testing.T) {
	home := t.TempDir()
	archive := filepath.Join(home, "a.tar.gz")
	write(t, archive, "contents")
	checksums := filepath.Join(home, "checksums.txt")
	write(t, checksums, hash("contents")+"  b.tar.gz\n")

	out, code := call(t, home, "verify_checksum "+archive+" "+checksums+" a.tar.gz")
	if code == 0 {
		t.Fatal("an archive absent from checksums.txt was accepted")
	}
	if !strings.Contains(out, "not listed") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
}

func TestAnAlteredArchiveIsRefused(t *testing.T) {
	home := t.TempDir()
	archive := filepath.Join(home, "a.tar.gz")
	write(t, archive, "what actually arrived")
	checksums := filepath.Join(home, "checksums.txt")
	write(t, checksums, hash("what was published")+"  a.tar.gz\n")

	out, code := call(t, home, "verify_checksum "+archive+" "+checksums+" a.tar.gz")
	if code == 0 {
		t.Fatal("an archive whose digest does not match was accepted")
	}
	// The likeliest cause names itself, because the alternative reading is alarming
	// and almost never the right one.
	if !strings.Contains(out, "caching proxy") {
		t.Errorf("the refusal does not name the likely cause:\n%s", out)
	}
}

// The entry the "no absolute, no .." check cannot see. A symlink pointing outside,
// followed by a plain file under the same name, has every entry looking relative
// while tar writes through the link.
func TestAnArchiveHoldingALinkIsRefused(t *testing.T) {
	home := t.TempDir()
	archive := filepath.Join(home, "linky.tar.gz")
	writeArchive(t, archive, []entry{
		{name: "neverseen", body: "a binary"},
		{name: "escape", link: "../../../../tmp"},
	})

	out, code := call(t, home, "refuse_unsafe_archive "+archive)
	if code == 0 {
		t.Fatal("an archive holding a link entry was accepted")
	}
	if !strings.Contains(out, "link entry") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
}

func TestAnArchiveSteppingOutsideIsRefused(t *testing.T) {
	home := t.TempDir()
	archive := filepath.Join(home, "climb.tar.gz")
	writeArchive(t, archive, []entry{{name: "../../etc/profile", body: "no"}})

	out, code := call(t, home, "refuse_unsafe_archive "+archive)
	if code == 0 {
		t.Fatal("an archive naming a path outside itself was accepted")
	}
	if !strings.Contains(out, "outside itself") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
}

func TestAnOrdinaryArchiveIsAccepted(t *testing.T) {
	home := t.TempDir()
	archive := filepath.Join(home, "plain.tar.gz")
	writeArchive(t, archive, []entry{
		{name: "neverseen", body: "a binary"},
		{name: "neverseen-tray", body: "an icon"},
	})

	if out, code := call(t, home, "refuse_unsafe_archive "+archive); code != 0 {
		t.Fatalf("a release-shaped archive was refused (exit %d):\n%s", code, out)
	}
}

// The failure that destroyed somebody's work: the login file was searched for the
// substring `neverseen env`, which matched lines they had written themselves.
func TestUninstallRemovesOnlyTheLineTheInstallerWrote(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".zshrc")

	// The exact line the installer adds, found by asking the script for it rather
	// than by copying it here — a copy would pass while the two had drifted apart.
	line, code := call(t, home, `printf '%s' "$SHELL_LINE"`)
	if code != 0 || strings.TrimSpace(line) == "" {
		t.Fatalf("could not read SHELL_LINE from the script: %q (exit %d)", line, code)
	}

	mine := []string{
		`alias ne="neverseen env --json"`,
		`# I disabled: eval "$(neverseen env)"`,
		`export EDITOR=vim`,
	}
	write(t, profile, strings.Join(append(mine, line), "\n")+"\n")

	runIn(t, home, "--uninstall")

	after := read(t, profile)
	for _, keep := range mine {
		if !strings.Contains(after, keep) {
			t.Errorf("--uninstall deleted a line the person wrote themselves:\n  %s\n\nprofile is now:\n%s",
				keep, after)
		}
	}
	if strings.Contains(after, line) {
		t.Errorf("--uninstall left its own line behind:\n%s", after)
	}
}

// Wired twice is wired once: the check is the same anchored match as the removal, so
// the two cannot come to disagree about which line is the installer's.
func TestWiringTheShellTwiceLeavesOneLine(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".zshrc")
	write(t, profile, "export EDITOR=vim\n")

	line, _ := call(t, home, `printf '%s' "$SHELL_LINE"`)

	for range 2 {
		if out, code := call(t, home, "wire_shell"); code != 0 {
			t.Fatalf("wire_shell failed (exit %d):\n%s", code, out)
		}
	}

	if n := strings.Count(read(t, profile), line); n != 1 {
		t.Errorf("the line is in the profile %d times, want 1:\n%s", n, read(t, profile))
	}
}

// `--shell --status` installed, wired the shell, and said nothing about the status
// it had been asked for. A verb silently dropped is worse than a refusal.
func TestASecondOptionIsRefusedRatherThanDropped(t *testing.T) {
	_, stderr, code := run(t, "--shell", "--status")

	if code == 0 {
		t.Fatal("two options were accepted, and one of them did nothing")
	}
	if !strings.Contains(stderr, "one option at a time") {
		t.Errorf("the refusal does not say what to do instead: %s", stderr)
	}
}

func TestAnUnknownOptionIsRefused(t *testing.T) {
	_, stderr, code := run(t, "--wat")
	if code == 0 {
		t.Fatal("an unknown option was accepted")
	}
	if !strings.Contains(stderr, "--wat") {
		t.Errorf("the refusal does not name the option: %s", stderr)
	}
}

// The trap read TEMP_DIR and STAGED, assigned forty lines below where it was armed.
// Under `set -u` a failure in between died on an unbound variable *inside the trap*,
// and a trap that fails changes the exit status of the script — which is the thing
// the `if`s in cleanup exist to avoid. The cheapest reachable proof is that a
// refusal exits with the status die intends and prints nothing about a variable.
func TestAFailureExitsCleanlyRatherThanInsideTheTrap(t *testing.T) {
	_, stderr, code := run(t, "--wat")

	if code != 1 {
		t.Errorf("exit %d, want 1: a trap that fails takes the status with it", code)
	}
	for _, leak := range []string{"unbound variable", "parameter not set", "TEMP_DIR", "STAGED"} {
		if strings.Contains(stderr, leak) {
			t.Errorf("the trap ran over an unset variable (%q):\n%s", leak, stderr)
		}
	}
}

// cleanup spells its two removals as `if`s, and that is not a style choice.
//
// Under `set -e` a false test as the *last* command of a function makes the
// function fail — so `[ -n "$TEMP_DIR" ] && rm -rf "$TEMP_DIR"`, on the ordinary
// run where no temporary directory was ever made, ends cleanup with a failure. The
// shell then leaves the EXIT trap on that status and every invocation exits
// non-zero, having done its work perfectly.
//
// Two other cases here already fail if the `if`s are turned back into `&&`, but
// they are named for a login file and for wiring a shell: an incidental failure
// records what the code does, where this records what it must. Somebody
// "simplifying" the `if`s would otherwise read two unrelated failures and look in
// the wrong place.
func TestASuccessfulRunExitsZeroThroughTheTrap(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".zshrc"), "export EDITOR=vim\n")

	// wire_shell touches nothing but the profile, and reaching the end of it runs
	// the EXIT trap over a TEMP_DIR and a STAGED that were never set — which is the
	// ordinary case, and the one the `&&` form gets wrong.
	out, code := call(t, home, "wire_shell")

	if code != 0 {
		t.Fatalf("a run that did its work exited %d:\n%s", code, out)
	}
}

// --- helpers ---------------------------------------------------------------

type entry struct {
	name string
	body string
	link string // non-empty makes it a symlink entry
}

func writeArchive(t *testing.T, path string, entries []entry) {
	t.Helper()

	f, err := os.Create(path) //nolint:gosec // a path this test just built under t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	zw := gzip.NewWriter(f)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body))}
		if e.link != "" {
			hdr.Typeflag, hdr.Linkname, hdr.Size = tar.TypeSymlink, e.link, 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.link == "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func hash(contents string) string {
	sum := sha256.Sum256([]byte(contents))
	return hex.EncodeToString(sum[:])
}

func write(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // a path this test just built under t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
