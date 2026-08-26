package proxy

import (
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/telemetry"
	"github.com/cloakfleet/cloakfleet/internal/vault"
	contract "github.com/cloakfleet/cloakfleet/pkg/telemetry"
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

	// EnvBackendURL points at a supervision backend. Unset means no supervision
	// at all — no reporter is built, nothing is sent, and the agent is otherwise
	// identical. That is the free half of the product, and it is a whole feature
	// rather than a disabled one.
	EnvBackendURL = "CLOAKFLEET_BACKEND_URL"

	// EnvEnrolmentToken is presented once, to trade for an identity of this
	// agent's own.
	EnvEnrolmentToken = "CLOAKFLEET_ENROLMENT_TOKEN"

	// EnvIdentityFile is where that issued identity is kept.
	EnvIdentityFile = "CLOAKFLEET_IDENTITY_FILE"
)

// DefaultIdentityFile is where an agent keeps the identity a backend issued it.
const DefaultIdentityFile = "~/.cloakfleet/agent.json"

// DefaultAuditListen is where `cloakfleet audit` listens.
//
// A port of its own, and not a preference: an audit run is meant to sit beside a
// workstation's ordinary agent — the one the shell profile and the menu bar are
// already pointed at — rather than to replace it for the length of the run. On
// the same port the two would race for the socket, and whichever lost would
// leave the operator reading an empty console while their traffic went through
// the other one.
const DefaultAuditListen = "127.0.0.1:33333"

// DefaultListen binds the loopback interface only.
//
// Not a default to override lightly. The agent trusts whoever reaches it — it
// forwards their credentials and it scopes the mapping by a header they
// control — so it is built for one person on one workstation. Bound to a
// reachable interface it becomes a way to read another user's session.
const DefaultListen = "127.0.0.1:8787"

// Agent is everything the proxy command runs.
type Agent struct {
	Server *Server
	Addr   string

	// Reporter is nil when no backend is configured, which is the ordinary case
	// and not a degraded one.
	Reporter *telemetry.Reporter
}

// Options are what the command decides about an agent, as opposed to what the
// environment does.
//
// It exists so `cloakfleet audit` can differ from `cloakfleet proxy` in the two
// ways it has to — a port of its own and a console to reveal on — without a
// second assembly of the pipeline, which is the divergence this project's one
// entrypoint rule exists to prevent, and without a command reading an
// environment variable, which is the other rule.
type Options struct {
	// Listen overrides the configured address. Empty means the environment
	// decides, which is the ordinary case.
	Listen string

	// Audit is where every value replaced or restored is written, in clear. Nil
	// everywhere but the audit command.
	Audit io.Writer
}

// FromEnv assembles everything the agent needs to serve: the detector, the
// session vault, the server over them, and a reporter when one is configured.
//
// One function, so the command that calls it reads no environment of its own and
// cannot drift from what this configures.
func FromEnv(logger *slog.Logger, opts Options) (*Agent, error) {
	det, err := detector.FromEnv()
	if err != nil {
		return nil, err
	}

	key, err := encryptionKeyFromEnv()
	if err != nil {
		return nil, err
	}
	v, err := vault.New(vault.NewMemory(), key, vault.DefaultTTL)
	if err != nil {
		return nil, err
	}

	providers, err := ParseProviders(os.Getenv(EnvProviders))
	if err != nil {
		return nil, err
	}

	recorder := telemetry.NewRecorder(time.Now())
	srv, err := New(Config{
		Providers: providers,
		Logger:    logger,
		Recorder:  recorder,
		Audit:     opts.Audit,
	}, det, v)
	if err != nil {
		return nil, err
	}

	// The command's choice wins over the environment: an audit run on the
	// configured port would fight the agent already listening there.
	addr := opts.Listen
	if addr == "" {
		addr = strings.TrimSpace(os.Getenv(EnvListen))
	}
	if addr == "" {
		addr = DefaultListen
	}

	agent := &Agent{Server: srv, Addr: addr}
	if agent.Reporter, err = reporterFromEnv(logger, recorder, srv); err != nil {
		return nil, err
	}
	return agent, nil
}

// reporterFromEnv builds the reporter, or reports nil when no backend is
// configured.
//
// Nil rather than a reporter that does nothing: an agent with no backend then has
// no reporting code path at all, so "it works standalone" is a fact about what
// runs rather than about what was switched off.
func reporterFromEnv(logger *slog.Logger, recorder *telemetry.Recorder, srv *Server) (*telemetry.Reporter, error) {
	backend := strings.TrimSpace(os.Getenv(EnvBackendURL))
	if backend == "" {
		return nil, nil
	}

	identity, err := expandHome(envOr(EnvIdentityFile, DefaultIdentityFile))
	if err != nil {
		return nil, err
	}

	return telemetry.NewReporter(telemetry.Config{
		BaseURL:        strings.TrimRight(backend, "/"),
		EnrolmentToken: strings.TrimSpace(os.Getenv(EnvEnrolmentToken)),
		IdentityFile:   identity,
		// Beside the identity rather than behind a setting of its own: it is agent
		// state an operator never edits, it belongs in the directory the installer
		// already leaves alone on uninstall, and a variable nobody would set is one
		// more line of documentation to keep true.
		BufferFile: filepath.Join(filepath.Dir(identity), "buffer.json"),
		Recorder:   recorder,
		State:      srv.State,
		Logger:     logger,
	})
}

// State is what this agent reports about itself.
//
// Read from the running server rather than from the configuration it was built
// with, because the question a security officer is asking is not "what was it
// told to do" but "what is it doing" — and an agent running with no locale
// selected masks almost nothing while looking perfectly healthy.
func (s *Server) State() contract.State {
	return contract.State{
		Version:      Version,
		Platform:     runtime.GOOS + "/" + runtime.GOARCH,
		StartedAt:    s.startedAt,
		Locales:      s.det.Locales(),
		Substitution: s.det.Substitution().String(),
		Providers:    providerCodes(s.providers),

		// Read at each heartbeat rather than cached at start-up: a laptop moves
		// between networks and a cached address would name where the machine was
		// when it booted.
		Addresses: localAddresses(),
	}
}

// Version is the agent build, stamped by the command at start-up.
//
// A package variable because the version lives in main, where the linker flag
// puts it, and the reporter needs it here. Set once before serving.
var Version = "dev"

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

// Providers reports the upstreams this server serves, for the line the command
// prints on start-up.
func (s *Server) Providers() []string { return providerCodes(s.providers) }
