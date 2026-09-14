package proxy

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Pointing a tool at the agent means setting an environment variable, and that is
// where the project this replaces did real damage.
//
// Its installer exported ANTHROPIC_BASE_URL and friends into the shell profile.
// So every LLM tool launched from that shell went through the proxy — and the day
// somebody stopped the proxy without running the uninstaller, every one of them
// broke with a connection error, from a line in a file they had not touched.
//
// The fix is that the export is *conditional on the agent answering*. `neverseen
// env` prints the variables only when something is listening, so a profile line
// that evaluates it produces nothing when the agent is down, and the tools go
// straight to their provider exactly as they did before it was installed.
//
// That is a deliberate trade, and it is worth naming: availability over
// enforcement. A stopped agent means unmasked traffic rather than a broken
// workstation. It is the right way round for a tool developers depend on — and
// the supervision backend is what makes the trade safe, because a stopped agent
// shows up there as silent rather than as nothing at all.

// shellTools maps a provider to how a tool is pointed at this agent for it.
//
// One table rather than two keyed the same way, because the two facts drift apart
// otherwise: adding a provider here is one place to think about both.
//
// Variable is the environment variable that provider's SDK reads, and only the two
// that are de-facto standard are here. The other six have no agreed name, so
// printing a guess would be an instruction that does nothing — their paths are
// listed as comments instead, which an operator can act on.
//
// CLI is the command people actually run against that variable, where there is one
// everybody means. It is a narrower question than the variable: a guessed command
// name is worse than none, because it fails with "command not found" after somebody
// has already pasted it and believed it. Empty means "no single obvious one" — and
// also "a CLI exists but does not read this variable", which is the worse case of
// the two and the one codex turned out to be. Neither is "there is no CLI".
//
// Caveat is what somebody has to know before trusting the line, where the tool does
// not simply honour the variable. It is here rather than in whatever displays the
// line, so the fact lives beside the pair it qualifies — a caveat kept in the menu
// would be a caveat the shell command never mentions.
var shellTools = map[string]struct {
	Variable string
	CLI      string
	Caveat   string
}{
	"anthropic": {Variable: "ANTHROPIC_BASE_URL", CLI: "claude"},
	"openai": {
		Variable: "OPENAI_BASE_URL",
		// No CLI, and codex is the reason rather than an omission.
		//
		// It used to be here, and the pair was wrong: codex does not read
		// OPENAI_BASE_URL for its own model traffic at all. Its built-in openai
		// provider takes its base URL from the `openai_base_url` key in
		// ~/.codex/config.toml and from nothing else — read in
		// codex-rs/core/src/config/mod.rs, passed to built_in_model_providers,
		// and defaulted to api.openai.com when absent (openai/codex@53c542d,
		// verified 2026-09-12). The variable appears in that tree only in the
		// credential broker, which hands it to subprocesses codex spawns, and
		// in tests that preserve an ambient environment.
		//
		// So `OPENAI_BASE_URL=… codex` was a line somebody pastes, believes, and
		// gets no error from, while every request goes to OpenAI unmasked. The
		// variable itself stays: it is what the OpenAI SDKs read, which is what
		// the export in a profile is for.
		//
		// TODO: the ceiling is that nothing here checks this. A pair is verified
		// by hand against the tool's source, on the day it is added, and a
		// vendor can stop reading a variable in a release nobody here notices.
		// The upgrade path is `make e2e-openai` — one real request per named
		// tool, asserting the agent saw it.
		Caveat: "codex does not read this: put openai_base_url = \"<the URL above>\" " +
			"in ~/.codex/config.toml instead (user-level — the key is ignored in a project file)",
	},
}

// ToolCodes reports the providers a tool can be pointed at with a line worth
// printing, in a stable order.
//
// From the same table PointAt reads, because a second list of "which providers do
// we know how to hand over" is a second chance to hand over a variable name
// nobody's SDK reads.
func ToolCodes() []string {
	codes := make([]string, 0, len(shellTools))
	for code := range shellTools {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

// CaveatFor reports what somebody has to know before trusting the line PointAt
// returns for a provider, or empty when there is nothing to add.
func CaveatFor(code string) string { return shellTools[code].Caveat }

// PointAt returns the one line that points a single tool at this agent.
//
// An export for the two providers whose variable name is de-facto standard, and
// the bare URL for the rest — the same distinction ShellEnv makes when it prints
// the others as comments, from the same table, because a guessed variable name is
// an instruction that does nothing.
//
// Unconditional, unlike what ShellEnv writes, and that is the whole reason it is a
// separate function: this is for one shell now, typed or pasted by somebody who
// can see whether the agent is running. A line like this in a login file is the
// Agent Veil failure — every LLM tool on the machine breaking the day the proxy
// stops. Whatever offers this to a person has to say so at the point of offering.
func PointAt(code, addr string) string { return PointAtFor(code, addr, DefaultShell()) }

// PointAtFor is PointAt for a named shell.
//
// The pair exists for the reason ShellEnv and ShellEnvFor do, and the bug it
// closes is the one Shell was introduced to prevent: PointAt used to spell the
// POSIX `export` whatever the platform, while the menu bar that hands this line
// over is built and installed on Windows. In PowerShell `export NAME=value` is
// not an error — it is a command that does nothing — so the person pastes it,
// sees no complaint, and their traffic goes to the provider unmasked with no
// symptom anywhere. A guessed spelling and a guessed variable name fail the same
// way, which is why six providers are printed as comments rather than guessed at.
func PointAtFor(code, addr string, shell Shell) string {
	if addr == "" {
		addr = DefaultListen
	}
	url := "http://" + addr + "/" + code

	tool, standard := shellTools[code]
	switch {
	case !standard:
		return url
	case tool.CLI != "" && shell == ShellPowerShell:
		// TODO: on PowerShell the assignment outlives the command — `$env:` is the
		// process's environment and there is no one-run prefix to spell. The
		// upgrade is to run the CLI in a child process with the variable set for
		// that child alone; until then this is the readable equivalent, and it
		// leaves the variable set for the rest of the session.
		return fmt.Sprintf("$env:%s = %q; %s", tool.Variable, url, tool.CLI)
	case tool.CLI != "":
		// A prefixed assignment rather than an export, where the command is known:
		// it applies to that one run and leaves the shell as it was. It is also
		// what somebody asking for "the command to run" means — a line they can
		// paste and press return on, rather than a variable and then a guess at
		// what reads it.
		return tool.Variable + "=" + url + " " + tool.CLI
	default:
		// Through assignment, the one owner of how a variable is spelled, so this
		// cannot drift from what ShellEnvFor writes. It writes a whole line; this
		// returns one for a caller that does its own formatting.
		return strings.TrimSuffix(shell.assignment(tool.Variable, url), "\n")
	}
}

// ShellEnv writes the shell lines that point a tool at this agent.
//
// When force is false it first asks the agent whether it is running, and writes
// nothing if it is not. That is the whole point of the command: a profile line
// evaluating it has to be a no-op on a machine where the agent is stopped.
func ShellEnv(ctx context.Context, w io.Writer, addr string, force bool) error {
	return ShellEnvFor(ctx, w, addr, force, DefaultShell())
}

// Shell is a way of spelling an assignment. It is not a preference: a line written for
// the wrong one is a line that does nothing, silently, and the traffic goes out
// unmasked with no symptom in the terminal.
type Shell string

const (
	// ShellPosix is sh, bash and zsh: `export NAME=value`, evaluated with
	// `eval "$(neverseen env)"`.
	ShellPosix Shell = "posix"

	// ShellPowerShell is `$env:NAME = "value"`, evaluated with
	// `neverseen env | Invoke-Expression`.
	//
	// cmd is deliberately absent. It has no eval: the equivalent is a `for /f`
	// incantation nobody can read, and one that is subtly wrong points no tool at the
	// agent while looking as though it did. That is the same reason six providers are
	// printed as comments rather than as guessed variable names.
	ShellPowerShell Shell = "powershell"
)

// DefaultShell is what the platform's own shell is, since that is what will be
// evaluating this.
func DefaultShell() Shell {
	if runtime.GOOS == "windows" {
		return ShellPowerShell
	}
	return ShellPosix
}

// ParseShell reads the --shell argument, refusing what it cannot write.
func ParseShell(name string) (Shell, error) {
	switch Shell(strings.ToLower(strings.TrimSpace(name))) {
	case "":
		return DefaultShell(), nil
	case ShellPosix:
		return ShellPosix, nil
	case ShellPowerShell:
		return ShellPowerShell, nil
	default:
		// Named rather than fallen back on, and cmd is named explicitly because it is
		// what somebody on Windows will try first.
		return "", fmt.Errorf("unknown shell %q; use posix or powershell (cmd has no eval, so it cannot be supported)", name)
	}
}

// assignment writes one variable the way the named shell reads it.
//
// One function rather than a branch at each call site: the two spellings differ in
// three places on one line, and a table of them written twice is two chances to point
// somebody's traffic nowhere.
func (s Shell) assignment(name, value string) string {
	if s == ShellPowerShell {
		return fmt.Sprintf("$env:%s = %q\n", name, value)
	}
	return fmt.Sprintf("export %s=%s\n", name, value)
}

// ShellEnvFor is ShellEnv for a named shell.
//
// The comment marker is "#" in both, which is what keeps the property that matters
// intact on Windows: with the agent stopped this prints only comments, so evaluating
// it changes nothing and the tools reach their provider directly — working, unmasked
// — instead of failing on a line nobody wrote.
func ShellEnvFor(ctx context.Context, w io.Writer, addr string, force bool, shell Shell) error {
	if addr == "" {
		addr = DefaultListen
	}
	base := "http://" + addr

	if !force && !agentIsListening(ctx, addr) {
		fmt.Fprintf(w, "# neverseen is not answering on %s, so nothing is exported here and\n", addr)
		fmt.Fprintf(w, "# your tools will reach their provider directly, unmasked. Start it with\n")
		fmt.Fprintf(w, "# `neverseen proxy`, or pass --force to export anyway.\n")
		return nil
	}

	for _, code := range ToolCodes() {
		fmt.Fprint(w, shell.assignment(shellTools[code].Variable, base+"/"+code))

		// Right under the line it qualifies, and as a comment because this output is
		// evaluated by a shell. Said here as well as wherever else the line is handed
		// over: a tool that quietly ignores the variable sends the traffic out
		// unmasked, and that has no symptom a person would notice.
		if caveat := shellTools[code].Caveat; caveat != "" {
			fmt.Fprintf(w, "# %s\n", caveat)
		}
	}

	fmt.Fprintf(w, "# Other providers take the same shape, if their client reads a base URL:\n")
	for _, code := range providerCodes(DefaultProviders) {
		if _, standard := shellTools[code]; !standard {
			fmt.Fprintf(w, "#   %s/%s\n", base, code)
		}
	}
	return nil
}

// listenTimeout is how long a local caller waits for the agent to answer.
//
// Short because `neverseen env` runs on every new shell when it is wired into a
// profile, and a second of latency there is a second a developer waits for their
// prompt. On the loopback interface the answer takes a millisecond or it is not
// coming.
const listenTimeout = 300 * time.Millisecond

// agentIsListening asks the health endpoint, briefly.
//
// Through Query rather than a request of its own: two ways to ask "is the agent
// there" are two ways to answer it differently, and this one decides whether a
// shell exports anything at all.
func agentIsListening(ctx context.Context, addr string) bool {
	return Query(ctx, addr, listenTimeout).Answering
}

// ListenAddress reports where the agent is configured to listen, for the commands
// that need to reach it rather than be it.
func ListenAddress() string {
	if addr := strings.TrimSpace(envOr(EnvListen, "")); addr != "" {
		return addr
	}
	return DefaultListen
}
