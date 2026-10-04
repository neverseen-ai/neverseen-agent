package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The verb comes first and the flags after it, and the prefix has to survive that
// order: Go's flag package stops at the first argument that is not a flag, so parsed
// the other way round `service install --prefix DIR` would leave DIR unread and
// register a service pointing at the default binaries. What is observable without a
// service manager is the refusal — install stats the agent under the prefix before it
// writes anything, and the error names the path it looked at.
func TestRunServiceReadsThePrefixAfterTheVerb(t *testing.T) {
	if runtime.GOOS == "linux" {
		// On Linux the systemctl check comes before the layout is looked at, and a
		// runner without it would fail one line earlier for an unrelated reason.
		if _, err := exec.LookPath("systemctl"); err != nil {
			t.Skip("systemctl is not here; the prefix is only read past that check")
		}
	}

	dir := t.TempDir()
	var out strings.Builder
	err := run([]string{"service", "install", "--prefix", dir}, strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("install with no binary under the prefix succeeded; it must refuse")
	}
	want := filepath.Join(dir, "bin", "neverseen")
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not name %s, so the prefix was not read:\n%v", want, err)
	}
}

// Uninstalling says which of the book's two files it deleted, and says so too when
// one would not go — after the service is gone, so the uninstall carries on.
func TestUninstallingSaysWhatItForgot(t *testing.T) {
	dir := t.TempDir()
	names := filepath.Join(dir, "names.json")
	for _, file := range []string{names, filepath.Join(dir, "names.key")} {
		if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var out strings.Builder
	forgetNames(&out, names)
	if strings.Count(out.String(), "Deleted ") != 2 {
		t.Errorf("the uninstall did not name both files it deleted:\n%s", out.String())
	}

	// A directory where the key was: it cannot be removed, and that is said.
	if err := os.MkdirAll(filepath.Join(dir, "names.key", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	forgetNames(&out, names)
	if !strings.Contains(out.String(), "Could not delete") {
		t.Errorf("a file that would not go was not reported:\n%s", out.String())
	}
}
