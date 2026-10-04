package proxy

import (
	"encoding/hex"
	"log/slog"
	"path/filepath"

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
func keepNames(logger *slog.Logger, v *vault.Vault, path string) {
	if path == "" {
		return
	}
	var stored vault.Book
	if _, err := readStateFile(path, &stored); err != nil {
		logger.Warn("the session book could not be read, so every session counts from 1 again",
			"file", path, "error", err)
		stored = nil
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

	v.Persist(stored, mark, func(b vault.Book) {
		if err := writeStateFile(path, ".names-*.json", b); err != nil {
			logger.Warn("the session book could not be stored, so a restart would count from 1 again",
				"file", path, "error", err)
		}
	})
}
