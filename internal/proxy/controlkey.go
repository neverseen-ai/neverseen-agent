package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The secret that closes the one route which changes what this agent masks.
//
// Generated rather than configured, and deliberately not an environment variable: a
// secret in the environment is a secret in the process table and in whatever shell
// history set it, and this one has no reason to be typed by anybody. The file is the
// interface — the agent writes it, and every local surface reads the same path.
//
// A key that cannot be read or written is not fatal anywhere here. The agent's job
// is masking, and refusing to start because a menu will not be able to switch a
// category off would be the supervision mistake in another costume: the thing that
// adjusts the control must never be able to stop it. What happens instead is that
// PUT /policy refuses everything, which is what an agent that lost its key must do.

// DefaultControlKeyFile is where the local control secret is kept.
//
// Beside the identity a backend issued, under the same directory and the same
// permissions, because it is the same kind of thing: a credential this machine
// holds, which the installer deliberately does not delete.
const DefaultControlKeyFile = "~/.cloakfleet/control.key"

// loadControlKey reads the local control secret, creating one if there is none.
//
// Generated rather than configured, and not an environment variable: a secret in
// the environment is a secret in the process table and in whatever shell history
// set it, and this one has no reason to be typed by anybody. The file is the
// interface — the menu bar reads the same path.
//
// A key that cannot be read or written is not fatal. The agent's job is masking,
// and refusing to start because the menu bar will not be able to switch a category
// off would be the supervision mistake in another costume: the thing that adjusts
// the control must never be able to stop it.
func loadControlKey(path string) (string, error) {
	resolved, err := expandHome(path)
	if err != nil {
		return "", err
	}

	if raw, err := os.ReadFile(filepath.Clean(resolved)); err == nil {
		if key := trimKey(raw); key != "" {
			return key, nil
		}
		// An empty or truncated file is replaced rather than used: a short secret
		// is worse than none, because it looks like protection.
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", resolved, err)
	}

	key, err := newControlKey()
	if err != nil {
		return "", err
	}
	if err := writeControlKey(resolved, key); err != nil {
		return "", err
	}
	return key, nil
}

// newControlKey mints a secret. Hex of 32 random bytes, the same shape and length
// as the session encryption key an operator may set by hand, so there is one
// notion of "a key" in this agent rather than two.
func newControlKey() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate a control key: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// writeControlKey writes it where only its owner can read it.
//
// The directory first, at 0700, because a 0600 file in a world-readable directory
// is a secret anybody can watch appear. Written by rename for the reason the
// telemetry buffer is: a torn key file on the next start reads as no key, which
// silently turns the route off.
func writeControlKey(path, key string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(key+"\n"), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("move %s into place: %w", path, err)
	}
	return nil
}

// trimKey takes the newline off a key file and refuses anything that is not a
// full-length hex secret.
//
// Length-checked rather than trusted, because the failure it prevents is silent:
// a truncated file used as a key is a route that looks authenticated and is not.
func trimKey(raw []byte) string {
	key := strings.TrimSpace(string(raw))
	if len(key) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(key); err != nil {
		return ""
	}
	return key
}

// ReadControlKey returns the secret a local surface needs to change what the agent
// masks, or "" when there is none to read.
//
// Exported for the menu bar, and read from the same file the agent writes: two
// ways to learn this secret would be two things to get wrong about it. It never
// creates one — only the agent does that, because a key created by a reader would
// be a key the agent does not know.
func ReadControlKey(path string) string {
	if path == "" {
		path = DefaultControlKeyFile
	}
	resolved, err := expandHome(path)
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Clean(resolved))
	if err != nil {
		return ""
	}
	return trimKey(raw)
}
