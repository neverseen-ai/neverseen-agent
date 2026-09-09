package secure

import (
	"os"
	"path/filepath"
	"testing"
)

// The point of these is not that a mode is set — it is that IsRestricted and the
// thing it checks agree. On Unix that is a mode; on Windows it is a protected access
// list. A test written against either mechanism directly would pass on one platform
// and be unwritable on the other, which is how seven assertions in this tree came to
// be checking something NTFS does not have.

func TestAWrittenFileCarriesTheGuarantee(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "secret.txt")

	if err := WriteFile(path, []byte("hunter2\n")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if ok, why := IsRestricted(path); !ok {
		t.Errorf("the file is readable by more than its owner: %s", why)
	}
	// The directory too: a held file in a world-readable directory is a secret
	// anybody can watch appear.
	if ok, why := IsRestricted(filepath.Dir(path)); !ok {
		t.Errorf("the directory is readable by more than its owner: %s", why)
	}

	if got, err := os.ReadFile(path); err != nil || string(got) != "hunter2\n" {
		t.Errorf("the content did not survive being restricted: %q, %v", got, err)
	}
}

// TestAnExistingDirectoryIsTightened. os.MkdirAll leaves a directory that already
// exists alone, so a trace directory made before this rule existed — or by an
// installer, or by the person — would otherwise keep whatever it had.
func TestAnExistingDirectoryIsTightened(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loose")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := MkdirAll(path); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if ok, why := IsRestricted(path); !ok {
		t.Errorf("an existing directory was left loose: %s", why)
	}
}

// TestRestrictTightensSomethingAlreadyWritten covers the path the policy file and the
// traces take: created by something else — os.CreateTemp, os.WriteFile — then held.
func TestRestrictTightensSomethingAlreadyWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "written.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := Restrict(path); err != nil {
		t.Fatalf("Restrict: %v", err)
	}
	if ok, why := IsRestricted(path); !ok {
		t.Errorf("Restrict did not hold the file: %s", why)
	}
}

// TestIsRestrictedRejectsALooseFile, so the assertions built on it can fail. A checker
// that answered "yes" to everything would have made all seven call sites vacuous.
func TestIsRestrictedRejectsALooseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "open.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if ok, _ := IsRestricted(path); ok {
		t.Error("a 0644 file was reported as held to its owner")
	}
}

func TestIsRestrictedSaysWhyWhenThereIsNothingThere(t *testing.T) {
	ok, why := IsRestricted(filepath.Join(t.TempDir(), "absent"))
	if ok {
		t.Error("a path that does not exist was reported as held")
	}
	if why == "" {
		t.Error("no reason given, so a failing assertion would say nothing useful")
	}
}
