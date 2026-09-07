package proxy

import (
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

// The proxy's own environment, read here and nowhere else. The detector reads
// its own in its own package; a command reads none, so there is one owner per
// setting and no chance of two of them disagreeing.
const (
	// EnvListen is the address to listen on.
	EnvListen = "NEVERSEEN_LISTEN"
	// EnvProviders overrides upstream URLs, as "code=url" pairs separated by
	// commas.
	EnvProviders = "NEVERSEEN_PROVIDERS"
	// EnvEncryptionKey is the 32-byte key, hex-encoded, that the session mapping
	// is sealed with. Unset means one is generated for the life of the process.
	EnvEncryptionKey = "NEVERSEEN_ENCRYPTION_KEY"

	// EnvBackendURL points at a supervision backend. Unset means no supervision
	// at all — no reporter is built, nothing is sent, and the agent is otherwise
	// identical. That is the free half of the product, and it is a whole feature
	// rather than a disabled one.
	EnvBackendURL = "NEVERSEEN_BACKEND_URL"

	// EnvEnrolmentToken is presented once, to trade for an identity of this
	// agent's own.
	EnvEnrolmentToken = "NEVERSEEN_ENROLMENT_TOKEN"

	// EnvIdentityFile is where that issued identity is kept.
	EnvIdentityFile = "NEVERSEEN_IDENTITY_FILE"
)

// DefaultIdentityFile is where an agent keeps the identity a backend issued it.
const DefaultIdentityFile = "~/.neverseen/agent.json"

// DefaultListen binds the loopback interface only.
//
// Not a default to override lightly. The agent trusts whoever reaches it — it
// forwards their credentials and it scopes the mapping by a header they
// control — so it is built for one person on one workstation. Bound to a
// reachable interface it becomes a way to read another user's session.
const DefaultListen = "127.0.0.1:8787"

// BeyondLoopback reports whether an address puts this agent on an interface
// something other than this workstation can reach.
//
// It exists because the answer decides what a warning says, and the address arrives
// by two routes — NEVERSEEN_LISTEN and `proxy -l` — which must not come to disagree
// about what counts as reachable. One predicate, asked at the point the agent starts
// listening, covers both.
//
// What it guards is not theoretical. /healthz and /test are unauthenticated, and they
// are safe that way *because* of the loopback default: the worst they give a local
// process is a description of the configuration. Reachable, /test is a masking oracle
// for anybody on the network and the session mapping is scoped by a header the caller
// chooses, so naming somebody else's session is enough to be handed their
// replacements.
func BeyondLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Not host:port at all. ListenAndServe refuses it a moment later with a
		// better message than a warning about it would be.
		return false
	}

	switch strings.TrimSpace(host) {
	case "":
		// ":8787" binds every interface. This is the shape where saying nothing
		// would be worst: it reads as "no address given" and means "all of them".
		return true
	case "localhost":
		return false
	}

	ip, err := netip.ParseAddr(host)
	if err != nil {
		// A name rather than a literal. It resolves to whatever DNS says, which is
		// not something to assume is this machine.
		return true
	}
	return !ip.IsLoopback()
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

// expandHome resolves a leading "~/" so the documented default is one an operator
// can read and type.
func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve the home directory in %q: %w", path, err)
	}
	return filepath.Join(home, path[2:]), nil
}

// encryptionKeyFromEnv reads the sealing key, or reports nil so one is generated.
func encryptionKeyFromEnv() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(EnvEncryptionKey))
	if raw == "" {
		return nil, nil
	}

	key, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is not hex: %w", EnvEncryptionKey, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%s decodes to %d bytes, want 32 for AES-256", EnvEncryptionKey, len(key))
	}
	return key, nil
}
