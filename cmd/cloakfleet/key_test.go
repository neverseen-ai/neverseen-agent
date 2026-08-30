package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestKeyPrintsTheControlKey — and prints nothing else, because `cloakfleet key |
// pbcopy` is what it is for, and a heading on the clipboard is a key the options
// page refuses.
func TestKeyPrintsTheControlKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".cloakfleet"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cloakfleet", "control.key"),
		[]byte(sampleKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"key"}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("key: %v", err)
	}
	if got := strings.TrimRight(out.String(), "\n"); got != sampleKey {
		t.Fatalf("printed %q, want the key alone on a line", got)
	}
}

// TestKeySaysWhereToGetOneWhenThereIsNone.
//
// The ordinary cause of an empty answer is not a missing file but an agent that has
// never run — it writes this key on its first start — so the message names the
// command that creates one rather than the error the read returned.
func TestKeySaysWhereToGetOneWhenThereIsNone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var out bytes.Buffer
	err := run([]string{"key"}, strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("an agent with no key printed something")
	}
	if !strings.Contains(err.Error(), "cloakfleet proxy") {
		t.Fatalf("the message does not say how to get one: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("something was printed to stdout: %q", out.String())
	}
}

// TestKeyNeverCreatesOne. A key minted by a reader is a key the agent does not know:
// the route would refuse it while the options page reported it saved.
func TestKeyNeverCreatesOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_ = run([]string{"key"}, strings.NewReader(""), &bytes.Buffer{})

	if _, err := os.Stat(filepath.Join(home, ".cloakfleet", "control.key")); !os.IsNotExist(err) {
		t.Fatal("the command created a key file")
	}
}
