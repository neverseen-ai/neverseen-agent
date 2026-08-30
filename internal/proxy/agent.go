package proxy

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/telemetry"
	"github.com/cloakfleet/cloakfleet/internal/vault"
)

// Assembling the agent: what the command decides, what the environment decides,
// and the one function that puts the pipeline together from both.
//
// FromEnv is the only assembly there is, which is the project's one-entrypoint rule
// seen from the other side: a command that built its own detector and its own vault
// would be a second answer to "what does this agent do", and the two would drift.
// What a command may vary, it varies through Options.

// Version is the agent build, stamped by the command at start-up.
//
// A package variable because the version lives in main, where the linker flag
// puts it, and the reporter needs it here. Set once before serving.
var Version = "dev"

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
// It exists so `-a` and `-v` can change what the agent reveals without a second
// assembly of the pipeline — the divergence this project's one entrypoint rule exists
// to prevent — and without a command reading an environment variable, which is the
// other rule. The flags are parsed where flags belong, in the command, and arrive
// here as options.
type Options struct {
	// Listen overrides the configured address. Empty means the environment
	// decides, which is the ordinary case.
	Listen string

	// Audit is where every value replaced or restored is written, in clear. Nil
	// unless `cloakfleet proxy -a` asked for it, which is the ordinary case.
	Audit io.Writer

	// ControlKeyFile overrides where the local control secret is kept. Empty means
	// the documented default, which is the ordinary case; a test sets it so a run
	// never touches the operator's own key.
	ControlKeyFile string

	// TraceDir records both bodies of every exchange into this directory, one file
	// per exchange. Empty — the ordinary case — records nothing at all.
	//
	// A directory rather than a boolean, so the one thing this agent writes in clear
	// says in the option where it goes. Only `cloakfleet proxy -v` sets it.
	TraceDir string
}

// TraceDir reports where this agent writes both bodies of every exchange, or "" when
// it writes none.
//
// From the assembled agent rather than from the flag that asked for it, for the reason
// the audit instructions read the locales from the server: what the command prints has
// to be what the agent is doing, and a directory that could not be created is a
// difference somebody has to see.
func (a *Agent) TraceDir() string { return a.Server.audit.traceDir() }

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

	// A key that cannot be read or written leaves the route refusing everything
	// rather than stopping the agent. The agent's job is masking, and refusing to
	// start because a menu will not be able to switch a category off is the
	// supervision mistake in another costume — the thing that adjusts the control
	// must never be able to stop it.
	controlKeyFile := opts.ControlKeyFile
	if controlKeyFile == "" {
		controlKeyFile = DefaultControlKeyFile
	}
	controlKey, err := loadControlKey(controlKeyFile)
	if err != nil {
		logger.Warn("no control key, so nothing may change what this agent masks",
			"file", controlKeyFile, "error", err)
		controlKey = ""
	}

	// Before anything is printed and before the server exists, so an operator who
	// cannot write where they asked is told at start-up rather than after the traffic
	// they wanted to look at has gone past.
	traces, err := newTracer(opts.TraceDir)
	if err != nil {
		return nil, err
	}

	recorder := telemetry.NewRecorder(time.Now())
	srv, err := New(Config{
		Providers:  providers,
		Logger:     logger,
		Recorder:   recorder,
		Audit:      opts.Audit,
		Traces:     traces,
		ControlKey: controlKey,
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
