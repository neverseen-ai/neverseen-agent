//go:build !windows

package secure

import (
	"fmt"
	"os"
)

// restrict re-applies the mode, which is all this guarantee is on a Unix filesystem.
//
// Not a no-op, even though os.MkdirAll and os.WriteFile have already been given the
// mode: both are subject to the process umask, so a umask of 077 is fine and a umask
// of 022 leaves a 0644 file where 0600 was asked for. The explicit chmod is what makes
// the guarantee independent of the environment the agent happened to start in.
func restrict(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}

	want := FileMode
	if info.IsDir() {
		want = DirMode
	}
	if info.Mode().Perm() == want {
		return nil
	}
	if err := os.Chmod(path, want); err != nil {
		return fmt.Errorf("restrict %s: %w", path, err)
	}
	return nil
}

func isRestricted(path string) (bool, string) {
	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Sprintf("cannot inspect %s: %v", path, err)
	}

	want := FileMode
	if info.IsDir() {
		want = DirMode
	}
	if got := info.Mode().Perm(); got != want {
		return false, fmt.Sprintf("%s is %04o, want %04o", path, got, want)
	}
	return true, ""
}
