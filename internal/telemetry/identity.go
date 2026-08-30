package telemetry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Identity is what a backend issued to this agent at enrolment.
//
// Kept in a file rather than in the environment, because the enrolment token an
// operator configures is single-use by design: the whole arrangement — the one
// Wazuh uses — exists so that a shared secret copied onto every workstation does
// not become the thing that cannot be revoked for one of them. The agent trades
// the shared token once for a key of its own, and that key is what the file
// holds.
type Identity struct {
	AgentID string `json:"agent_id"`
	Key     string `json:"key"`
}

// LoadIdentity reads the identity at path. The second result is false when the
// file simply does not exist, which is the normal state of an agent that has not
// enrolled yet and not an error.
func LoadIdentity(path string) (Identity, bool, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return Identity{}, false, nil
	}
	if err != nil {
		return Identity{}, false, fmt.Errorf("read the identity file: %w", err)
	}

	var id Identity
	if err := json.Unmarshal(raw, &id); err != nil {
		return Identity{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if id.AgentID == "" || id.Key == "" {
		// A half-written file is worse than none: the agent would sign with an
		// empty key and every heartbeat would be rejected with nothing saying why.
		return Identity{}, false, fmt.Errorf("%s carries no agent id or key", path)
	}
	return id, true, nil
}

// SaveIdentity writes the identity, readable only by its owner.
//
// The directory is created with the same restriction. The file holds a signing
// key, and a key that is world-readable on a shared workstation is a key
// anybody can file reports with.
//
// Through writeFile, so it is written to a neighbour and renamed over the target
// — and of the three files this agent keeps, this is the one that most needed it
// and was the only one without it. A torn control key is regenerated on the next
// start; a torn buffer costs some counters. A torn identity cannot be repaired at
// all: LoadIdentity refuses it, the reporter falls back to enrolling, and the
// enrolment token it would present is single-use by design. The workstation is
// then off the fleet view until somebody re-provisions it by hand.
func SaveIdentity(path string, id Identity) error {
	if err := writeFile(path, id); err != nil {
		return fmt.Errorf("write the identity file: %w", err)
	}
	return nil
}
