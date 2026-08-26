package proxy

import (
	"context"
	"fmt"
	"io"
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
// The fix is that the export is *conditional on the agent answering*. `cloakfleet
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
// has already pasted it and believed it. Empty means "no single obvious one", which
// is not the same as "there is no CLI".
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
		CLI:      "codex",
		// Codex reads the variable, but a model_provider in ~/.codex/config.toml and
		// --profile both win over it — so on a machine already configured for another
		// provider the line does nothing, silently, and the traffic goes out
		// unmasked. That failure has no symptom at all from the terminal, which is
		// exactly why it is worth saying where the line is handed over.
		Caveat: "codex ignores this if ~/.codex/config.toml sets model_provider, " +
			"or if you pass --profile",
	},
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
func PointAt(code, addr string) string {
	if addr == "" {
		addr = DefaultListen
	}
	url := "http://" + addr + "/" + code

	tool, standard := shellTools[code]
	switch {
	case !standard:
		return url
	case tool.CLI != "":
		// A prefixed assignment rather than an export, where the command is known:
		// it applies to that one run and leaves the shell as it was. It is also
		// what somebody asking for "the command to run" means — a line they can
		// paste and press return on, rather than a variable and then a guess at
		// what reads it.
		return tool.Variable + "=" + url + " " + tool.CLI
	default:
		return "export " + tool.Variable + "=" + url
	}
}

// ShellEnv writes the shell lines that point a tool at this agent.
//
// When force is false it first asks the agent whether it is running, and writes
// nothing if it is not. That is the whole point of the command: a profile line
// evaluating it has to be a no-op on a machine where the agent is stopped.
func ShellEnv(ctx context.Context, w io.Writer, addr string, force bool) error {
	if addr == "" {
		addr = DefaultListen
	}
	base := "http://" + addr

	if !force && !agentIsListening(ctx, addr) {
		fmt.Fprintf(w, "# cloakfleet is not answering on %s, so nothing is exported here and\n", addr)
		fmt.Fprintf(w, "# your tools will reach their provider directly, unmasked. Start it with\n")
		fmt.Fprintf(w, "# `cloakfleet proxy`, or pass --force to export anyway.\n")
		return nil
	}

	codes := make([]string, 0, len(shellTools))
	for code := range shellTools {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	for _, code := range codes {
		fmt.Fprintf(w, "export %s=%s/%s\n", shellTools[code].Variable, base, code)

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
// Short because `cloakfleet env` runs on every new shell when it is wired into a
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
