// Command neverseen is the agent: one binary, one pipeline.
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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/proxy"
	"github.com/neverseen-ai/neverseen-agent/internal/telemetry"
	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
)

// version is stamped at build time with -ldflags "-X main.version=…".
//
// A var, not a const: Agent Veil declared it const, which makes the linker flag
// silently inert, and every release it built reported the same string.
var version = "dev"

const usage = `neverseen — mask sensitive values before they reach a model.

Usage:
  neverseen proxy [-a] [-v] [-l addr]
                           run the agent: mask what goes out, restore what comes
                           back. -a prints every value it replaces and restores,
                           in clear; -v writes both bodies of every exchange to
                           ./%s. Neither belongs in a service definition.
                           -l binds somewhere other than the loopback default,
                           which makes /healthz and /test reachable
  neverseen scan [file]   report the sensitive values in a file, or in stdin
  neverseen status        report whether the agent is masking, and what
  neverseen mask          list what is masked, and switch a category or a family
                           off for this run
  neverseen key           print the control key, for the browser extension
  neverseen env [--force] [--shell posix|powershell]
                          print the lines that point a tool at the agent
  neverseen replay <dir>  rebuild the heartbeat batch from the traces in a
                           directory and print it; nothing is sent or queued
  neverseen service <cmd> register the agent to start at login, or take it back
                           off: install, uninstall, restart
  neverseen version       print the version

Point a client at the agent by naming the provider in the path:

  ANTHROPIC_BASE_URL=%s/anthropic
  OPENAI_BASE_URL=%s/openai

Or let your shell do it, safely — this prints nothing while the agent is stopped,
so your tools keep working instead of failing on a line you did not write:

  eval "$(neverseen env)"

While it runs, %s/test shows what would be masked — your own
text, both representations side by side, in this agent's configuration.

Configuration:
  %-28s which country pattern sets to load: %s,
                               none, or a comma-separated list. Unset means none.
  %-28s values never to mask, separated by commas.
  %-28s what a masked value looks like: token or fake.
  %-28s weakest named secret to mask: weak, medium or strong.
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
	// The operator's configuration, into this process's environment, before any
	// command reads a setting.
	//
	// Ahead of the dispatch, because there is more than one door: the agent goes
	// through proxy.FromEnv, `scan` through proxy.DetectorFromEnv, `status` and `env`
	// through proxy.ListenAddress. Loaded behind one of those, a command reached
	// through another would read a different configuration from the same binary —
	// the drift that already had `scan` reporting a category as masked while the
	// agent beside it forwarded it in clear.
	//
	// In main and not in run, and that distinction is not cosmetic: run is what the
	// unit tests call. Put there, every test on a developer's own machine read
	// whatever ~/.neverseen/.env happened to say and set it into the test binary —
	// so `scan`'s expected output became machine-dependent, and the values leaked
	// into every later test in the package. Green in CI, unreproducible locally,
	// which is the worst shape a test failure can take.
	//
	// This is not `cmd/` reading an environment variable, which is the rule it looks
	// like it bends: it names no setting and asks for no value.
	if err := proxy.LoadConfigFile(""); err != nil {
		fmt.Fprintln(os.Stderr, "neverseen:", err)
		os.Exit(1)
	}

	err := run(os.Args[1:], os.Stdin, os.Stdout)
	switch {
	case err == nil:
		return
	case errors.Is(err, errQuiet):
		os.Exit(1)
	default:
		fmt.Fprintln(os.Stderr, "neverseen:", err)
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
		return runProxy(args[1:], stdout)
	case "scan":
		return runScan(args[1:], stdin, stdout)
	case "status":
		return runStatus(stdout)
	case "mask":
		return runMask(args[1:], stdout)
	case "key":
		return runKey(stdout)
	case "env":
		return runEnv(args[1:], stdout)
	case "service":
		return runService(args[1:], stdout)
	case "replay":
		return runReplay(args[1:], stdout)
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
		defaultTraceDir,
		listen, listen, listen,
		detector.EnvLocale, strings.Join(pii.LocaleCodes(), ", "),
		detector.EnvAllowList,
		detector.EnvSubstitution,
		detector.EnvSecretLevel,
		proxy.EnvListen, proxy.DefaultListen,
		proxy.EnvProviders,
		proxy.EnvEncryptionKey,
		proxy.EnvBackendURL,
		proxy.EnvEnrolmentToken,
		proxy.EnvIdentityFile, proxy.DefaultIdentityFile)
}

// runProxy serves, optionally revealing what it replaced and recording it.
//
// One command rather than two, and that is a change from what came before: there
// used to be a separate `audit` command, on a port of its own, so that printing a
// value in clear was a *mode* somebody entered rather than a setting on the agent.
// The flags are simpler for the operator — the agent they are already pointed at is
// the one they watch — and the cost has to be stated where it can be read.
//
// # What the flags cost, and where they must not go
//
// -a prints values in clear to standard output. Under `neverseen proxy` in a
// terminal that is one operator looking at their own data, which is the situation the
// whole reveal was designed for. In a service definition it is something else: the
// installer redirects this agent's output to ~/.neverseen/agent.log, so -a in a
// plist writes everybody's prompts, in clear, to a file, for as long as the service
// runs. Neither flag belongs in one, and the banner below says so on every start.
//
// The pipeline is otherwise identical either way — the same detector, the same vault,
// the same substitution mode — because an audit of a different pipeline audits
// nothing.
func runProxy(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	fs.SetOutput(stdout)
	reveal := fs.Bool("a", false,
		"print every value replaced and restored, in clear")
	verbose := fs.Bool("v", false,
		"write both bodies of every exchange to a file under "+defaultTraceDir)
	// The address the environment already carries, as a flag, because a container or
	// a VM on this workstation cannot reach a loopback-bound agent and setting a
	// variable to say so is a poor fit for a one-off run. It overrides
	// NEVERSEEN_LISTEN — the command's choice wins, as it does for every option here
	// — and the agent warns on every start when the result is reachable, whichever of
	// the two set it.
	listen := fs.String("l", "",
		"address to listen on (default "+proxy.DefaultListen+"); 0.0.0.0:9787 serves every interface")
	// Passed by the Windows logon task and by nothing a person types. It hides the
	// console this process was handed, which a console binary started by Task
	// Scheduler otherwise shows at every login. It does nothing on macOS and Linux,
	// deliberately: a flag that existed on one platform would be a service definition
	// that could not be rendered on another.
	detach := fs.Bool("detach", false,
		"hide the console window this was given (Windows logon task; no effect elsewhere)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *detach {
		proxy.HideConsole()
	}

	opts := proxy.Options{Listen: *listen}
	if *reveal {
		opts.Audit = stdout
	}
	// A trace is the only thing this agent writes to disk that holds a value in
	// clear: the log carries counts, the heartbeat carries no content, and -a stops
	// with the terminal it printed to. A file outlives the run, so it takes its own
	// flag rather than coming along with -a.
	if *verbose {
		opts.TraceDir = defaultTraceDir
	}

	logger := slog.New(slog.NewTextHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	proxy.Version = version

	agent, err := proxy.FromEnv(logger, opts)
	if err != nil {
		return err
	}

	if *reveal || *verbose {
		printRevealBanner(stdout, agent, *reveal)
	}
	return serveAgent(logger, agent)
}

// defaultTraceDir is where -v writes, relative to wherever the command was run.
//
// Relative on purpose: an operator runs this in the directory they are working in and
// then reads the files there, and a path under the home directory would have them
// hunting for output they asked for thirty seconds ago. It is in .gitignore for the
// same reason e2e-artefacts is — the files hold real values in clear and are evidence
// of one run, not part of any tree.
const defaultTraceDir = "traces"

// printRevealBanner says what this terminal is about to show, and what must not be
// done with it.
//
// The state comes from the assembled agent rather than from the environment, for
// the reason `status` reports what is being applied rather than that the process
// is up: an agent with no locale selected is perfectly healthy and recognises
// almost nothing, and an audit console that stayed silent would read as "nothing
// sensitive in my data" instead of "nothing configured to look for it".
func printRevealBanner(w io.Writer, agent *proxy.Agent, reveal bool) {
	state := agent.Server.State()

	fmt.Fprintf(w, "\nneverseen — the agent in the foreground, on %s.\n\n", agent.Addr)

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

	// Only promised when -a was given. With -v alone nothing is printed per exchange,
	// and a banner announcing lines that never arrive would have somebody watching a
	// console for traffic that was going to a file all along.
	if reveal {
		fmt.Fprint(w, "\nEvery value this agent replaces on the way out and restores on the way back\n")
		fmt.Fprint(w, "is printed below, in clear:\n\n")
		fmt.Fprint(w, "  MASK pierre.paul@example.fr TO [EMAIL_1]\n")
		fmt.Fprint(w, "  UNMASK [EMAIL_1] TO pierre.paul@example.fr\n\n")
	}

	// Said either way. The bodies are what reveal a value the catalogue never
	// recognised — it appears identically in both halves — and a console that simply
	// stopped carrying them would leave an operator believing the MASK lines are the
	// whole story.
	if dir := agent.TraceDir(); dir != "" {
		fmt.Fprintf(w, "Both bodies of every exchange are written to %s/, one file per\n", dir)
		fmt.Fprint(w, "exchange. Read the two halves against each other: a value present in both\n")
		fmt.Fprint(w, "is one nothing recognised, which no count can tell you.\n\n")
	} else {
		fmt.Fprint(w, "The bodies themselves are not shown — on screen they scroll the lines above\n")
		fmt.Fprintf(w, "away. Add -v to write both halves of every exchange to %s/, which is\n", defaultTraceDir)
		fmt.Fprint(w, "how you see a value nothing recognised.\n\n")
	}

	// The hazard the flags carry, said on every start rather than left in the
	// documentation: under a service definition this output is a log file, and a
	// reveal that outlives the terminal is everybody's prompts on disk in clear.
	fmt.Fprint(w, "Neither -a nor -v belongs in a service definition: the installer sends this\n")
	fmt.Fprint(w, "agent's output to a log file, and a reveal there keeps every prompt in clear\n")
	fmt.Fprint(w, "for as long as the service runs.\n")

	// Said plainly, because it is the one place in this agent where a real value
	// is written out: the log carries counts, the heartbeat carries no content at
	// all, and these flags are the deliberate exception for one operator looking at
	// their own data.
	//
	// What it says about a file depends on -v, and that dependence is the point. The
	// sentence used to be "nothing is written to a file" unconditionally, which -v
	// made false — and a banner that reassures somebody about a file it is at that
	// moment filling is worse than no banner.
	if agent.TraceDir() != "" {
		fmt.Fprint(w, "Nothing of this reaches a backend: the heartbeat carries no content at all.\n")
		fmt.Fprint(w, "The files above are the exception, and they are yours to delete.\n")
	} else {
		fmt.Fprint(w, "That is this terminal only, for your own data — nothing is written to a file\n")
		fmt.Fprint(w, "and nothing of it is ever reported to a backend.\n")
	}
	fmt.Fprint(w, "Ctrl-C stops the agent.\n\n")
}

// serveAgent serves until it is signalled, then stops taking new requests and lets
// the ones in flight finish.
//
// The graceful stop is not politeness: a request cut off mid-flight has been masked
// and stored but never answered, so the caller loses the turn and the mapping keeps
// values nothing will ask for again.
//
// It is a function of its own rather than the body of runProxy because it was once
// shared with a second command — `audit`, which ran the same pipeline on a port of
// its own and is now `proxy -a`. One caller today, and it stays separate: shutdown,
// supervision and the last heartbeat are one thing to get right, and this is where
// a second entrypoint would have to come to reuse it rather than reimplement it.
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

		// Said on every start rather than written in a manual, for the reason the
		// reveal banner is: the person who set this is not the person reading the
		// log six months later, and the loopback default is what makes two
		// unauthenticated routes safe. It is a warning rather than a refusal —
		// serving a container or a VM on this workstation is a real thing to want,
		// and an agent that refused would be one somebody patches out.
		if proxy.BeyondLoopback(agent.Addr) {
			logger.Warn("this agent is reachable beyond this workstation",
				"address", agent.Addr,
				"unauthenticated", "/healthz and /test",
				"why_it_matters", "/test masks any text on request, and a session is named "+
					"by a header the caller chooses, so a caller that guesses one is handed "+
					"its replacements")
		}
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
	shell := fs.String("shell", "",
		"which shell to write for: posix or powershell; empty follows the platform")
	if err := fs.Parse(args); err != nil {
		return err
	}

	which, err := proxy.ParseShell(*shell)
	if err != nil {
		return err
	}
	return proxy.ShellEnvFor(context.Background(), stdout, proxy.ListenAddress(), *force, which)
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

	// The same assembly the agent runs, stored policy included, or this command
	// reports a value as masked that the agent beside it forwards in clear.
	d, err := proxy.DetectorFromEnv(nil, proxy.DefaultPolicyFile)
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

// runReplay rebuilds the heartbeat batch from a directory of traces and prints it.
//
// Read-only on purpose — see proxy.Replay for why a rebuilt bucket must never be
// queued beside the ones the agent filed live. The detector is the agent's own
// assembly, stored policy included, so what the replay counts as masked is what
// the agent beside it would mask today.
func runReplay(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(stdout)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("replay takes one argument: the directory holding the traces")
	}

	d, err := proxy.DetectorFromEnv(nil, proxy.DefaultPolicyFile)
	if err != nil {
		return err
	}
	batch, err := proxy.Replay(fs.Arg(0), d, time.Now())
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(batch)
}
