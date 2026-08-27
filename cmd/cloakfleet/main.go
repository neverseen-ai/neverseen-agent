// Command cloakfleet is the agent: one binary, one pipeline.
//
// It is deliberately a single entrypoint. The project this one replaces shipped
// two, each assembling its own pipeline from the same packages, and they
// drifted: one applied a masking rule on the way back and the other did not, so
// the same request was masked in one and answered in clear in the other. A
// second entrypoint is a second answer to "what does this agent do", and the
// two will disagree eventually.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/proxy"
	"github.com/cloakfleet/cloakfleet/internal/telemetry"
	"github.com/cloakfleet/cloakfleet/pkg/pii"
)

// version is stamped at build time with -ldflags "-X main.version=…".
//
// A var, not a const: Agent Veil declared it const, which makes the linker flag
// silently inert, and every release it built reported the same string.
var version = "dev"

const usage = `cloakfleet — mask sensitive values before they reach a model.

Usage:
  cloakfleet proxy         run the agent: mask what goes out, restore what comes back
  cloakfleet audit         run it in the foreground on port %s, printing every
                           value it replaces and restores, in clear
  cloakfleet scan [file]   report the sensitive values in a file, or in stdin
  cloakfleet status        report whether the agent is masking, and what
  cloakfleet env [--force] print the shell exports that point a tool at the agent
  cloakfleet version       print the version

Point a client at the agent by naming the provider in the path:

  ANTHROPIC_BASE_URL=%s/anthropic
  OPENAI_BASE_URL=%s/openai

Or let your shell do it, safely — this prints nothing while the agent is stopped,
so your tools keep working instead of failing on a line you did not write:

  eval "$(cloakfleet env)"

While it runs, %s/test shows what would be masked — your own
text, both representations side by side, in this agent's configuration.

Configuration:
  %-28s which country pattern sets to load: %s,
                               none, or a comma-separated list. Unset means none.
  %-28s values never to mask, separated by commas.
  %-28s what a masked value looks like: token or fake.
  %-28s address to listen on (default %s).
  %-28s upstream overrides, as code=url pairs.
  %-28s 32-byte hex key for the session mapping.

Supervision is optional, and the agent is complete without it:
  %-28s a supervision backend. Unset means none.
  %-28s the enrolment token it gave you, presented once.
  %-28s where the issued identity is kept
                               (default %s).

Every variable is documented in .env.example.
`

// errQuiet exits non-zero without a message, for a command that has already said
// everything it has to say on its own output.
//
// `status` needs it: its whole job is to report a state, and one of those states
// is worth a non-zero exit so a script can act on it — but printing the same
// diagnosis again on stderr, prefixed as an error, would make the ordinary case
// of a stopped agent read like a malfunction of the command.
var errQuiet = errors.New("")

func main() {
	err := run(os.Args[1:], os.Stdin, os.Stdout)
	switch {
	case err == nil:
		return
	case errors.Is(err, errQuiet):
		os.Exit(1)
	default:
		fmt.Fprintln(os.Stderr, "cloakfleet:", err)
		os.Exit(1)
	}
}

// run is main's body with its inputs and output passed in, so the commands are
// testable without a subprocess.
func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return errors.New("no command given")
	}

	switch cmd := args[0]; cmd {
	case "proxy":
		return serve(stdout, proxy.Options{})
	case "audit":
		return runAudit(stdout)
	case "scan":
		return runScan(args[1:], stdin, stdout)
	case "status":
		return runStatus(stdout)
	case "env":
		return runEnv(args[1:], stdout)
	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "-h", "--help", "help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stdout)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// runStatus reports what the agent is applying, or that there is nothing there.
//
// It reads no environment of its own — proxy.ListenAddress owns that question, as
// the same rule requires everywhere else in this command.
//
// The exit code is zero only when the agent is applying its whole catalogue.
// Answering is not enough, and neither is masking: an agent with no locale selected
// is up and recognises almost nothing, and an agent with a category switched off is
// masking everything except the one thing somebody switched off. A check that called
// either of those healthy would be the check somebody trusted while their traffic
// went out in clear.
//
// So the code follows Level rather than Masking, and the middle state is the reason
// it had to: zero has to mean "everything this configuration loaded is being
// replaced", or a script cannot use it for anything.
func runStatus(stdout io.Writer) error {
	status := proxy.Query(context.Background(), proxy.ListenAddress(), 2*time.Second)
	status.Write(stdout)
	if status.Level() != detector.LevelFull {
		return errQuiet
	}
	return nil
}

// printUsage names the settings from the constants the code actually reads, and
// the locales from the registry. Anything written out by hand here goes stale the
// first time one of them is renamed, and a wrong instruction is worse than none.
func printUsage(w io.Writer) {
	listen := "http://" + proxy.DefaultListen
	fmt.Fprintf(w, usage,
		auditPort(),
		listen, listen, listen,
		detector.EnvLocale, strings.Join(pii.LocaleCodes(), ", "),
		detector.EnvAllowList,
		detector.EnvSubstitution,
		proxy.EnvListen, proxy.DefaultListen,
		proxy.EnvProviders,
		proxy.EnvEncryptionKey,
		proxy.EnvBackendURL,
		proxy.EnvEnrolmentToken,
		proxy.EnvIdentityFile, proxy.DefaultIdentityFile)
}

// runProxy serves until it is signalled, then stops taking new requests and lets
// the ones in flight finish.
//
// The graceful stop is not politeness: a request cut off mid-flight has been
// masked and stored but never answered, so the caller loses the turn and the
// mapping keeps values nothing will ask for again.
// runAudit runs the agent in the foreground with the console revealing what it
// replaced, and prints the command to point a tool at it from another terminal.
//
// It is the same pipeline as `proxy` — the same detector, the same vault, the same
// substitution mode — because an audit of a different pipeline audits nothing.
// The only two differences are the port and the console, and both are Options
// rather than a second assembly.
func runAudit(stdout io.Writer) error {
	// Built before anything is printed, so a configuration error is reported
	// instead of a screenful of instructions for an agent that never starts.
	logger := slog.New(slog.NewTextHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	proxy.Version = version

	agent, err := proxy.FromEnv(logger, proxy.Options{
		Listen: proxy.DefaultAuditListen,
		Audit:  stdout,
	})
	if err != nil {
		return err
	}

	printAuditInstructions(stdout, agent)
	return serveAgent(logger, agent)
}

// printAuditInstructions says what to run elsewhere, and what this terminal is
// about to show.
//
// The state comes from the assembled agent rather than from the environment, for
// the reason `status` reports what is being applied rather than that the process
// is up: an agent with no locale selected is perfectly healthy and recognises
// almost nothing, and an audit console that stayed silent would read as "nothing
// sensitive in my data" instead of "nothing configured to look for it".
func printAuditInstructions(w io.Writer, agent *proxy.Agent) {
	state := agent.Server.State()

	fmt.Fprintf(w, "\ncloakfleet audit — the agent in the foreground, on %s.\n\n", agent.Addr)

	locales := "none"
	if len(state.Locales) > 0 {
		locales = strings.Join(state.Locales, ",")
	}
	fmt.Fprintf(w, "  locales:      %s\n", locales)
	fmt.Fprintf(w, "  substitution: %s\n", state.Substitution)
	if len(state.Locales) == 0 {
		fmt.Fprintf(w, "\n  Nothing will be masked: no country pattern set is loaded. Set %s\n", detector.EnvLocale)
		fmt.Fprintf(w, "  to one of %s before trusting an empty console.\n", strings.Join(pii.LocaleCodes(), ", "))
	}

	fmt.Fprint(w, "\nIn another terminal, run your tool through it:\n\n")
	for _, code := range proxy.ToolCodes() {
		fmt.Fprintf(w, "  %s\n", proxy.PointAt(code, agent.Addr))

		// Carried here as everywhere the line is handed over: a tool that quietly
		// ignores the variable sends the traffic out unmasked, and this console
		// would stay empty while it happened — which is the one reading an audit
		// must never be able to get wrong.
		if caveat := proxy.CaveatFor(code); caveat != "" {
			fmt.Fprintf(w, "  # %s\n", caveat)
		}
	}

	fmt.Fprint(w, "\nEvery value this agent replaces on the way out and restores on the way back\n")
	fmt.Fprint(w, "is printed below, in clear:\n\n")
	fmt.Fprint(w, "  MASK pierre.paul@example.com TO [EMAIL_1]\n")
	fmt.Fprint(w, "  UNMASK [EMAIL_1] TO pierre.paul@example.com\n\n")

	// Said plainly, because it is the one place in this agent where a real value
	// is written out: the log carries counts, the heartbeat carries no content at
	// all, and this mode is the deliberate exception for one operator looking at
	// their own data. It goes to this terminal only, and stops with it.
	fmt.Fprint(w, "That is this mode only, on this terminal, for your own data — nothing is\n")
	fmt.Fprint(w, "written to a file and nothing of it is ever reported to a backend.\n")
	fmt.Fprint(w, "Ctrl-C stops the agent.\n\n")
}

// auditPort is the port DefaultAuditListen names, for the usage text.
//
// Derived rather than written out, so the two cannot disagree about where the
// command a person is told to run actually listens.
func auditPort() string {
	if _, port, err := net.SplitHostPort(proxy.DefaultAuditListen); err == nil {
		return port
	}
	return proxy.DefaultAuditListen
}

func serve(stdout io.Writer, opts proxy.Options) error {
	logger := slog.New(slog.NewTextHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Stamped before anything is assembled, because the agent reports it about
	// itself and a supervision dashboard showing "dev" for every workstation is
	// a fleet nobody can audit.
	proxy.Version = version

	agent, err := proxy.FromEnv(logger, opts)
	if err != nil {
		return err
	}

	return serveAgent(logger, agent)
}

// serveAgent is the serving half, shared by `proxy` and `audit` so the two cannot
// come to differ about shutdown, supervision or the last heartbeat.
func serveAgent(logger *slog.Logger, agent *proxy.Agent) error {
	server := &http.Server{
		Addr:              agent.Addr,
		Handler:           agent.Server.Handler(),
		ReadHeaderTimeout: 30 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Supervision runs on its own goroutine and can fail all it likes. A backend
	// that is down, slow or misconfigured must never stop the masking — or the
	// security control would be taken out by the tool that watches it.
	//
	// The channel is what makes its last report actually happen. Without waiting
	// on it, the process exits as soon as the server has shut down and the final
	// heartbeat is cut off mid-flight — the window is lost, and the dashboard's
	// last few minutes before a restart are simply missing.
	reported := make(chan struct{})
	if agent.Reporter != nil {
		logger.Info("supervision enabled, reporting every " + telemetry.DefaultInterval.String())
		go func() {
			defer close(reported)
			agent.Reporter.Run(ctx)
		}()
	} else {
		close(reported)
	}

	errs := make(chan error, 1)
	go func() {
		logger.Info("listening",
			"address", agent.Addr,
			"providers", strings.Join(agent.Server.Providers(), ","),
			"supervised", agent.Reporter != nil)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
		close(errs)
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		logger.Info("stopping, letting requests in flight finish")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := server.Shutdown(shutdownCtx)

		// Bounded, because a hung backend must not stop the agent from stopping.
		select {
		case <-reported:
		case <-time.After(20 * time.Second):
			logger.Warn("the last heartbeat did not finish; its window will be retried on restart")
		}
		return err
	}
}

// runEnv prints the shell exports, or deliberately nothing.
func runEnv(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("env", flag.ContinueOnError)
	fs.SetOutput(stdout)
	force := fs.Bool("force", false,
		"print the exports even when the agent is not answering")
	if err := fs.Parse(args); err != nil {
		return err
	}

	return proxy.ShellEnv(context.Background(), stdout, proxy.ListenAddress(), *force)
}

func runScan(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stdout)
	if err := fs.Parse(args); err != nil {
		return err
	}

	text, err := readInput(fs.Args(), stdin)
	if err != nil {
		return err
	}

	d, err := detector.FromEnv()
	if err != nil {
		return err
	}

	matches := d.Scan(text)
	report(stdout, d, matches)
	return nil
}

// readInput reads the named file, or stdin when no file is named.
func readInput(args []string, stdin io.Reader) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("scan takes at most one file, got %d", len(args))
	}
	if len(args) == 0 {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(raw), nil
	}

	raw, err := os.ReadFile(filepath.Clean(args[0]))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", args[0], err)
	}
	return string(raw), nil
}

// report prints what would be masked, then a count per category.
//
// The per-category count is the line that matters to an operator checking their
// own data is covered: a value they expected to see named, missing from the
// tally, is a gap in the catalogue for their data shape.
func report(w io.Writer, d *detector.Detector, matches []detector.Match) {
	locales := "none"
	if l := d.Locales(); len(l) > 0 {
		locales = strings.Join(l, ",")
	}
	fmt.Fprintf(w, "locales: %s\n\n", locales)

	if len(matches) == 0 {
		fmt.Fprintln(w, "no sensitive values found")
		return
	}

	counts := map[pii.Category]int{}
	for _, m := range matches {
		counts[m.Category]++
		fmt.Fprintf(w, "%-24s %s\n", m.Category, m.Value)
		fmt.Fprintf(w, "%-24s   %s, bytes %d-%d, confidence %d\n", "", m.Label, m.Start, m.End, m.Confidence)
	}

	cats := make([]string, 0, len(counts))
	for cat := range counts {
		cats = append(cats, string(cat))
	}
	sort.Strings(cats)

	fmt.Fprintf(w, "\n%d value(s) would be masked:\n", len(matches))
	for _, cat := range cats {
		fmt.Fprintf(w, "  %-24s %d\n", cat, counts[pii.Category(cat)])
	}
}
