package proxy

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/vault"
)

// The proxy's own environment, read here and nowhere else. The detector reads
// its own in its own package; a command reads none, so there is one owner per
// setting and no chance of two of them disagreeing.
const (
	// EnvListen is the address to listen on.
	EnvListen = "CLOAKFLEET_LISTEN"
	// EnvProviders overrides upstream URLs, as "code=url" pairs separated by
	// commas.
	EnvProviders = "CLOAKFLEET_PROVIDERS"
	// EnvEncryptionKey is the 32-byte key, hex-encoded, that the session mapping
	// is sealed with. Unset means one is generated for the life of the process.
	EnvEncryptionKey = "CLOAKFLEET_ENCRYPTION_KEY"
)

// DefaultListen binds the loopback interface only.
//
// Not a default to override lightly. The agent trusts whoever reaches it — it
// forwards their credentials and it scopes the mapping by a header they
// control — so it is built for one person on one workstation. Bound to a
// reachable interface it becomes a way to read another user's session.
const DefaultListen = "127.0.0.1:8787"

// FromEnv assembles everything the agent needs to serve: the detector, the
// session vault, and the server over them.
//
// One function, so the command that calls it reads no environment of its own and
// cannot drift from what this configures.
func FromEnv(logger *slog.Logger) (srv *Server, addr string, err error) {
	det, err := detector.FromEnv()
	if err != nil {
		return nil, "", err
	}

	key, err := encryptionKeyFromEnv()
	if err != nil {
		return nil, "", err
	}
	v, err := vault.New(vault.NewMemory(), key, vault.DefaultTTL)
	if err != nil {
		return nil, "", err
	}

	providers, err := ParseProviders(os.Getenv(EnvProviders))
	if err != nil {
		return nil, "", err
	}

	srv, err = New(Config{Providers: providers, Logger: logger}, det, v)
	if err != nil {
		return nil, "", err
	}

	addr = strings.TrimSpace(os.Getenv(EnvListen))
	if addr == "" {
		addr = DefaultListen
	}
	return srv, addr, nil
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

// Providers reports the upstreams this server serves, for the line the command
// prints on start-up.
func (s *Server) Providers() []string { return providerCodes(s.providers) }
