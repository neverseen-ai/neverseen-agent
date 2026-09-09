// Package secure holds a file or a directory so that only the person who owns it can
// read it.
//
// # Why this is a package and not four literals
//
// It was four: 0700 and 0600 written out at the control key, the policy file, the
// trace directory and the telemetry buffer, each with a comment saying why. Those
// comments describe nothing on Windows — a Unix mode is silently ignored on NTFS, so
// every one of those guarantees was stated and not kept.
//
// How much that actually costs is smaller than it sounds and worth being exact about,
// because the exaggerated version led to the wrong priority. Inside a user's profile
// Windows already denies other standard accounts by inherited ACL, which is roughly
// what 0600 gives against a non-root user. What is genuinely unprotected is anything
// written *outside* the profile — the trace directory is configurable, and pointed at
// C:\temp it inherits an ACL that lets any local account read prompts in clear.
//
// So one seam, and on Windows it sets an explicit DACL naming the current user and
// nothing else, marked protected so it inherits nothing from wherever it was put.
package secure

import (
	"fmt"
	"os"
	"path/filepath"
)

// The modes this package applies on Unix, and the guarantee it reproduces elsewhere.
const (
	// DirMode is 0700: a 0600 file in a world-readable directory is a secret
	// anybody can watch appear.
	DirMode os.FileMode = 0o700

	// FileMode is 0600.
	FileMode os.FileMode = 0o600
)

// MkdirAll creates a directory, and every parent, holding the leaf to its owner.
//
// Only the leaf is restricted. The parents may be shared — ~/.local exists for other
// things — and clamping them would take a directory somebody else's tools use and make
// it unreadable to them.
func MkdirAll(path string) error {
	if err := os.MkdirAll(path, DirMode); err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	// Applied after the create as well, because MkdirAll leaves an existing directory
	// alone: a trace directory created before this rule existed, or by an installer,
	// would otherwise keep whatever it had.
	return restrict(path)
}

// WriteFile writes a file only its owner can read, creating its directory.
func WriteFile(path string, data []byte) error {
	if err := MkdirAll(filepath.Dir(path)); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, FileMode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return restrict(path)
}

// Restrict applies the guarantee to something that already exists.
//
// For the paths this package does not create — a temporary file from os.CreateTemp,
// a file opened for appending — where the mode is set at creation on Unix and needs
// an explicit call on Windows.
func Restrict(path string) error { return restrict(path) }

// IsRestricted reports whether a path actually carries the guarantee this package
// applies, and says why not when it does not.
//
// It exists for the tests, and it exists as one function rather than as an assertion
// repeated in each of them because the *check* is platform-specific too: seven tests
// across two packages compared a Unix mode, and on Windows all seven would have failed
// on a mode that filesystem does not have. A test that asserts the mechanism can only
// be written once per platform; a test that asserts the guarantee can be written once.
func IsRestricted(path string) (bool, string) { return isRestricted(path) }
