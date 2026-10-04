package proxy

import (
	"encoding/hex"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
)

// DefaultNamesFile is where a running agent keeps each session's book — its
// numbering and the names it gave — so a restart neither hands a conversation's
// names to other values nor leaves the names it remembers unexpanded (vault.Book).
//
// Beside the policy, under the directory the installer leaves alone on uninstall.
// It holds session names, dates, counts per token prefix and replacements keyed by
// fingerprint — never a value — so it is written where the mapping, which holds the
// originals, never is. vault.Book says what a reader of it can still learn.
const DefaultNamesFile = "~/.neverseen/names.json"

// namesKeyFile is where the fingerprint key lives, beside the book and never in it.
//
// A file of its own so the book can be read, copied or attached to a report without
// the one thing that turns its fingerprints back into testable guesses.
const namesKeyFile = "names.key"

// namesFile is the book as it is written: the sessions, and the substitution mode
// their names were minted under.
//
// The mode is there because it can change between two processes without the route
// that clears the names — an environment edited with no policy file, or a policy
// file edited while the agent was stopped. A process restoring names of the other
// shape would hand every value seen before the shape the operator had just switched
// away from: the control appearing not to work, which is what Vault.Forget exists
// to prevent on the live path.
type namesFile struct {
	Mode     string     `json:"mode"`
	Sessions vault.Book `json:"sessions"`
}

// keepNames resumes the book a previous process wrote to path, and has the vault
// write every change back. path is "" for none, resolved by the caller.
//
// A file that cannot be read, or a write that fails, is reported and the agent goes
// on: what is lost is the book, which leaves the agent where every restart left it
// before this file existed — a session counting from 1 again, its old names
// unexpanded — and refusing to mask over bookkeeping would trade that for traffic
// going out in clear. A fingerprint key that cannot be read or created costs the
// names alone: the numbering is still kept, so nothing is ever restored as another
// value.
func keepNames(logger *slog.Logger, v *vault.Vault, det *detector.Detector, path string) {
	if path == "" {
		return
	}
	var stored namesFile
	if _, err := readStateFile(path, &stored); err != nil {
		logger.Warn("the session book could not be read, so every session counts from 1 again",
			"file", path, "error", err)
		stored = namesFile{}
	}
	if mode := det.Substitution().String(); stored.Mode != "" && stored.Mode != mode {
		// The names go and the numbering stays: a counter back at 1 would give the
		// names the conversation remembers to other values, whatever their shape.
		logger.Info("the stored names were minted in another substitution mode, so every value takes a new name",
			"file", path, "was", stored.Mode, "now", mode)
		for session, b := range stored.Sessions {
			b.Names = nil
			stored.Sessions[session] = b
		}
	}

	// The control key's shape and the control key's loader: 32 random bytes in hex,
	// 0600, created on first start.
	var mark []byte
	keyFile := filepath.Join(filepath.Dir(path), namesKeyFile)
	if key, err := loadControlKey(keyFile); err != nil {
		logger.Warn("the fingerprint key could not be read or created, so a restart forgets every name",
			"file", keyFile, "error", err)
	} else {
		mark, _ = hex.DecodeString(key) // loadControlKey hands back only full-length hex
	}

	v.Persist(stored.Sessions, mark, func(b vault.Book) {
		// The mode read as the book is written, so a change through PUT /policy —
		// whose Forget writes the book at once without its names — is what the file
		// says from then on.
		file := namesFile{Mode: det.Substitution().String(), Sessions: b}
		if err := writeStateFile(path, ".names-*.json", file); err != nil {
			logger.Warn("the session book could not be stored, so a restart would count from 1 again",
				"file", path, "error", err)
		}
	})
}

// ForgetNames deletes the session book at path and the fingerprint key beside it,
// and reports which of the two were there. A file already absent is not an error.
//
// It exists for uninstalling, the one exception to "~/.neverseen is left alone".
// That rule protects state worth more than the agent: the operator's configuration,
// the identity a backend bills by, the record of an outage still being delivered.
// The book is none of these — its only use is to name values for an agent that is
// about to stop existing, and losing it costs one round of unexpanded tokens after a
// reinstall. What keeping it costs is the reverse: a record of the values this agent
// named, testable with the key beside it, that nothing sweeps any more once the
// agent that swept it is gone.
func ForgetNames(path string) ([]string, error) {
	resolved, err := expandHome(path)
	if err != nil {
		return nil, err
	}
	var removed []string
	var errs []error
	for _, file := range []string{resolved, filepath.Join(filepath.Dir(resolved), namesKeyFile)} {
		switch err := os.Remove(file); {
		case err == nil:
			removed = append(removed, file)
		case !errors.Is(err, fs.ErrNotExist):
			errs = append(errs, err)
		}
	}
	return removed, errors.Join(errs...)
}
