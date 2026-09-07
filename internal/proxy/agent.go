package proxy

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/telemetry"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
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
	// unless `neverseen proxy -a` asked for it, which is the ordinary case.
	Audit io.Writer

	// ControlKeyFile overrides where the local control secret is kept. Empty means
	// the documented default, which is the ordinary case; a test sets it so a run
	// never touches the operator's own key. NoFile means none, and the route then
	// refuses everything, which is what a missing key already does.
	ControlKeyFile string

	// PolicyFile overrides where what a surface changed is stored, so it survives a
	// restart. Empty means the documented default, which is the ordinary case; a
	// test sets it so a run never reads or writes the operator's own state.
	//
	// NoFile means no file at all, and it exists because the empty string cannot
	// mean that here. Config.PolicyFile spells "store nothing" as "", and Options
	// spells "the documented default" the same way — the two are opposite readings
	// of one value, and without a third spelling a caller that wants neither the
	// operator's file nor a temporary one of its own has nothing to say. What that
	// cost was is on the record: eight call sites in this package's tests had to be
	// given a temp path, and the one test that reaches FromEnv through runProxy —
	// which passes no Options at all — had to move $HOME instead, after reading the
	// policy of whichever workstation ran it.
	PolicyFile string

	// TraceDir records both bodies of every exchange into this directory, one file
	// per exchange. Empty — the ordinary case — records nothing at all.
	//
	// A directory rather than a boolean, so the one thing this agent writes in clear
	// says in the option where it goes. Only `neverseen proxy -v` sets it.
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

// loadStoredPolicy reads the stored state, and reads nothing at all when there is
// no file to read: NoFile has to mean the agent never touches one, not that it
// touches the path spelled "".
func loadStoredPolicy(path string) (*policyRequest, error) {
	if path == "" {
		return nil, nil
	}
	return loadPolicyFile(path)
}

// NoFile means no file at all, given for one of the file options above: the agent
// neither reads nor writes it, and what would have been stored lasts as long as the
// process. A dash rather than an empty string, because empty already means "the
// documented default" here and one spelling cannot carry both.
const NoFile = "-"

// resolveFileOption reads one of those options: NoFile means none, empty means the
// documented default, anything else is a path.
func resolveFileOption(chosen, byDefault string) string {
	switch chosen {
	case NoFile:
		return ""
	case "":
		return byDefault
	default:
		return chosen
	}
}

// FromEnv assembles everything the agent needs to serve: the detector, the
// session vault, the server over them, and a reporter when one is configured.
//
// One function, so the command that calls it reads no environment of its own and
// cannot drift from what this configures.
func FromEnv(logger *slog.Logger, opts Options) (*Agent, error) {
	// A nil logger is what several callers pass, and assembling now says things —
	// which of the environment and the stored policy decided what is masked. A
	// discard handler here rather than a branch at each line.
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	policyFile := resolveFileOption(opts.PolicyFile, DefaultPolicyFile)
	det, err := DetectorFromEnv(logger, policyFile)
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
	//
	// NoFile is the same outcome said deliberately: no key, so the route refuses
	// everything.
	controlKey := ""
	if controlKeyFile := resolveFileOption(opts.ControlKeyFile, DefaultControlKeyFile); controlKeyFile != "" {
		stored, err := loadControlKey(controlKeyFile)
		if err != nil {
			logger.Warn("no control key, so nothing may change what this agent masks",
				"file", controlKeyFile, "error", err)
		}
		controlKey = stored
	}

	// Before anything is printed and before the server exists, so an operator who
	// cannot write where they asked is told at start-up rather than after the traffic
	// they wanted to look at has gone past.
	traces, err := newTracer(opts.TraceDir)
	if err != nil {
		return nil, err
	}

	// The command's choice wins over the environment: an audit run on the
	// configured port would fight the agent already listening there. Resolved
	// before the server is built, because the server reports whether the address
	// reaches beyond loopback.
	addr := opts.Listen
	if addr == "" {
		addr = strings.TrimSpace(os.Getenv(EnvListen))
	}
	if addr == "" {
		addr = DefaultListen
	}

	recorder := telemetry.NewRecorder(time.Now())
	srv, err := New(Config{
		Providers:  providers,
		Logger:     logger,
		Recorder:   recorder,
		Audit:      opts.Audit,
		Traces:     traces,
		ControlKey: controlKey,
		PolicyFile: policyFile,
		Listen:     addr,
	}, det, v)
	if err != nil {
		return nil, err
	}

	agent := &Agent{Server: srv, Addr: addr}
	if agent.Reporter, err = reporterFromEnv(logger, recorder, srv); err != nil {
		return nil, err
	}
	return agent, nil
}

// DetectorFromEnv assembles the detector the way every command must: from the
// environment, then from the stored policy, which wins.
//
// One function because there were two. `neverseen scan` built its detector from
// the environment alone while the agent applied the stored policy on top, so a
// category unticked in the menu bar was reported as masked by `scan` and forwarded
// in clear by the agent on the same workstation — the "two entrypoints drifted"
// failure, re-created between two commands of one binary. policyFile is "" for
// none, resolved by the caller through resolveFileOption.
func DetectorFromEnv(logger *slog.Logger, policyFile string) (*detector.Detector, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	det, err := detector.FromEnv()
	if err != nil {
		return nil, err
	}

	// The stored policy, and it wins over everything read above.
	//
	// The environment configures an agent nobody has said anything to yet. Once a
	// surface has written a policy — the menu bar, `neverseen mask`, the test page —
	// that policy is the state, or the click lasts exactly until the workstation
	// restarts, which is the whole reason this file exists. An operator who wants the
	// environment back deletes it, and the line below says at every start which of
	// the two the agent read.
	//
	// A file that cannot be read leaves the environment's configuration in place
	// rather than stopping the agent: masking configured by a profile is a working
	// agent, and starting from half a document is not.
	//
	// NoFile skips it entirely: nothing is read at start-up and nothing is stored
	// afterwards, so what a surface changes lasts as long as the process.
	if stored, err := loadStoredPolicy(policyFile); err != nil {
		logger.Warn("the stored policy could not be read, so this agent starts from its environment",
			"file", policyFile, "error", err)
	} else if stored != nil {
		if _, applied, err := applyPolicy(det, *stored); err != nil && !applied {
			// Refused before it touched anything — half a document, or a mode the
			// parser did not know — so the environment is still what this agent is,
			// the same outcome as a file that would not parse.
			logger.Warn("the stored policy was refused, so this agent starts from its environment",
				"file", policyFile, "error", err)
		} else if err != nil {
			// Named as partial rather than as ignored, because the applier is not a
			// transaction: a locale selection it accepted stays applied under a
			// category it refused. Whatever it read is what this agent is now, and
			// the next line an operator sees has to be true of that.
			logger.Warn("the stored policy was applied only in part; what it refused stays as configured",
				"file", policyFile, "error", err,
				"locales", det.Locales(), "masking", det.Masking().String())
		} else {
			logger.Info("what this agent masks comes from the stored policy, not the environment",
				"file", policyFile,
				"locales", det.Locales(),
				"substitution", det.Substitution().String(),
				"secret_level", det.SecretLevel().String(),
				"off", stored.Off,
				"masking", det.Masking().String())
		}
	}

	return det, nil
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
