package telemetry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/pkg/telemetry"

	"github.com/neverseen-ai/neverseen-agent/internal/secure"
)

func TestIdentityRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "agent.json")
	want := Identity{AgentID: "agt_1", Key: "00112233"}

	if err := SaveIdentity(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, found, err := LoadIdentity(path)
	if err != nil || !found {
		t.Fatalf("load: %v, found=%v", err, found)
	}
	if got != want {
		t.Errorf("loaded %+v, want %+v", got, want)
	}
}

// A missing file is the normal state of an agent that has not enrolled yet, not
// an error. Treating it as one would have the reporter give up on the very first
// tick of a fresh install.
func TestLoadMissingIdentityIsNotAnError(t *testing.T) {
	_, found, err := LoadIdentity(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Errorf("a missing identity file reported an error: %v", err)
	}
	if found {
		t.Error("a missing identity file was reported as found")
	}
}

// A half-written file is worse than none: the agent would sign with an empty key
// and every heartbeat would be rejected with nothing saying why.
func TestIncompleteIdentityIsRefused(t *testing.T) {
	for name, content := range map[string]string{
		"no key":      `{"agent_id":"agt_1"}`,
		"no agent id": `{"key":"00112233"}`,
		"empty":       `{}`,
		"not json":    `agt_1`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := LoadIdentity(path); err == nil {
				t.Errorf("an unusable identity file was accepted: %s", content)
			}
		})
	}
}

// The file holds a signing key, so its directory must not be readable by other
// users of a shared machine either.
func TestIdentityDirectoryIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "neverseen")
	if err := SaveIdentity(filepath.Join(dir, "agent.json"), Identity{AgentID: "a", Key: "b"}); err != nil {
		t.Fatal(err)
	}

	if ok, why := secure.IsRestricted(dir); !ok {
		t.Errorf("the identity directory is readable by more than its owner: %s", why)
	}
}

func TestVerifySignature(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	body := []byte(`{"schema":1}`)

	// Signed and verified through the contract, which is where both sides get it
	// from — two implementations of one HMAC are two chances to disagree.
	signature := telemetry.Sign(key, body)

	if !telemetry.VerifySignature(key, body, signature) {
		t.Error("a signature the contract produced did not verify")
	}
	if telemetry.VerifySignature(key, []byte(`{"schema":2}`), signature) {
		t.Error("a signature verified against a different body")
	}
	if telemetry.VerifySignature([]byte("ffffffffffffffffffffffffffffffff"), body, signature) {
		t.Error("a signature verified under a different key")
	}
	if telemetry.VerifySignature(key, body, "not hex") {
		t.Error("a signature that is not hex verified")
	}
}
