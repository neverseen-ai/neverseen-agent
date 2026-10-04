package proxy

import (
	"log/slog"

	"github.com/neverseen-ai/neverseen-agent/internal/vault"
)

// DefaultNumberingFile is where a running agent keeps each session's numbering, so
// a restart does not hand a conversation's names to other values (vault.Name).
//
// Beside the policy, under the directory the installer leaves alone on uninstall.
// It holds session names, dates and counts per token prefix — never a value — so
// it is written where the mapping, which holds the originals, never is.
const DefaultNumberingFile = "~/.neverseen/numbering.json"

// keepNumbering resumes the numbering a previous process wrote to path, and has
// the vault write every change back. path is "" for none, resolved by the caller.
//
// A file that cannot be read, or a write that fails, is reported and the agent goes
// on: what is lost is the numbering, which leaves the agent where every restart left
// it before this file existed — a session counting from 1 again — and refusing to
// mask over bookkeeping would trade that for traffic going out in clear.
func keepNumbering(logger *slog.Logger, v *vault.Vault, path string) {
	if path == "" {
		return
	}
	var stored vault.Numbering
	if _, err := readStateFile(path, &stored); err != nil {
		logger.Warn("the session numbering could not be read, so every session counts from 1 again",
			"file", path, "error", err)
		stored = nil
	}
	v.Persist(stored, func(n vault.Numbering) {
		if err := writeStateFile(path, ".numbering-*.json", n); err != nil {
			logger.Warn("the session numbering could not be stored, so a restart would count from 1 again",
				"file", path, "error", err)
		}
	})
}
