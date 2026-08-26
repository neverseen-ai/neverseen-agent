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

// shellVars maps a provider to the environment variable its SDK reads.
//
// Only the two that are de-facto standard. The other six providers have no agreed
// variable name, so printing a guess would be an instruction that does nothing —
// their paths are listed as a comment instead, which an operator can act on.
var shellVars = map[string]string{
	"anthropic": "ANTHROPIC_BASE_URL",
	"openai":    "OPENAI_BASE_URL",
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

	codes := make([]string, 0, len(shellVars))
	for code := range shellVars {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	for _, code := range codes {
		fmt.Fprintf(w, "export %s=%s/%s\n", shellVars[code], base, code)
	}

	fmt.Fprintf(w, "# Other providers take the same shape, if their client reads a base URL:\n")
	for _, code := range providerCodes(DefaultProviders) {
		if _, standard := shellVars[code]; !standard {
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
