package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The behaviour this exists for: with the agent stopped, evaluating it in a shell
// profile must be a no-op. The project this replaces exported the variables
// unconditionally, and stopping the proxy without running its uninstaller broke
// every LLM tool on the machine from a line nobody had touched.

func TestShellEnvPrintsNothingWhenTheAgentIsDown(t *testing.T) {
	var out strings.Builder
	// Nothing listens on port 1.
	if err := ShellEnv(t.Context(), &out, "127.0.0.1:1", false); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	// On a line start, not anywhere in the text: the explanation itself mentions
	// exporting, and a substring check reads that as an export.
	if exportsIn(got) {
		t.Errorf("an export was printed with the agent down, which is what breaks a shell:\n%s", got)
	}
	// Silent, but not mute: an operator running this by hand has to be told why
	// nothing came out, and what it means for their traffic.
	for _, want := range []string{"not answering", "unmasked", "--force"} {
		if !strings.Contains(got, want) {
			t.Errorf("the explanation does not mention %q:\n%s", want, got)
		}
	}
}

func TestShellEnvPrintsTheExportsWhenTheAgentAnswers(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, nil)

	addr := strings.TrimPrefix(agent.URL, "http://")

	var out strings.Builder
	if err := ShellEnv(t.Context(), &out, addr, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	// The two variables with a de-facto standard name, pointing at the path that
	// names their provider.
	for _, want := range []string{
		"export ANTHROPIC_BASE_URL=http://" + addr + "/anthropic",
		"export OPENAI_BASE_URL=http://" + addr + "/openai",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the exports do not carry %q:\n%s", want, got)
		}
	}

	// And the other six as comments, because they have no agreed variable name
	// and printing a guess would be an instruction that does nothing.
	for _, provider := range []string{"gemini", "mistral", "groq", "together", "deepinfra", "xai"} {
		if !strings.Contains(got, "/"+provider) {
			t.Errorf("the comment does not show the path for %q:\n%s", provider, got)
		}
	}
	if strings.Contains(got, "export GEMINI_BASE_URL") {
		t.Error("a variable name was invented for a provider that has none")
	}

	// The caveat travels with the line here too, and as a comment, because this
	// output is evaluated by a shell. A tool that quietly ignores the variable sends
	// the traffic out unmasked, which has no symptom a person would notice — so it
	// is said in both places the line is handed over, not just in the menu bar.
	if !strings.Contains(got, "# "+CaveatFor("openai")) {
		t.Errorf("the openai caveat is missing or is not a comment:\n%s", got)
	}
	// Every line of it, or `eval` on this output is a syntax error rather than an
	// export — which would break every new shell on the machine.
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if !strings.HasPrefix(line, "export ") && !strings.HasPrefix(line, "#") {
			t.Errorf("this line is neither an export nor a comment, so eval would fail on it: %q", line)
		}
	}
}

// --force is for the operator who knows the agent is about to start, and for
// anybody debugging why nothing was exported.
func TestShellEnvForcePrintsWithoutChecking(t *testing.T) {
	var out strings.Builder
	if err := ShellEnv(t.Context(), &out, "127.0.0.1:1", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "export ANTHROPIC_BASE_URL=http://127.0.0.1:1/anthropic") {
		t.Errorf("--force did not print the exports:\n%s", out.String())
	}
}

// A health check that hangs would delay every new shell on the machine, which is
// a second a developer waits for their prompt.
func TestShellEnvGivesUpQuicklyOnAHungAgent(t *testing.T) {
	// A listener that accepts and never answers.
	hung := httptest.NewServer(nil)
	hung.Config.Handler = nil
	hung.Close() // closed, so the connection is refused rather than hanging

	var out strings.Builder
	if err := ShellEnv(t.Context(), &out, strings.TrimPrefix(hung.URL, "http://"), false); err != nil {
		t.Fatal(err)
	}
	if exportsIn(out.String()) {
		t.Error("an unreachable agent was treated as running")
	}
}

func TestShellEnvDefaultsToTheConfiguredAddress(t *testing.T) {
	t.Setenv(EnvListen, "")
	if got := ListenAddress(); got != DefaultListen {
		t.Errorf("ListenAddress() = %q, want the default %q", got, DefaultListen)
	}

	t.Setenv(EnvListen, "127.0.0.1:9999")
	if got := ListenAddress(); got != "127.0.0.1:9999" {
		t.Errorf("ListenAddress() = %q, want the configured address", got)
	}
}

// exportsIn reports whether any line is an export statement — which is the thing
// a shell would act on, as opposed to a comment that happens to say the word.
func exportsIn(text string) bool {
	for line := range strings.Lines(text) {
		if strings.HasPrefix(strings.TrimSpace(line), "export ") {
			return true
		}
	}
	return false
}

// PointAt is the line somebody pastes into a shell, and it is the one place that
// decides what that line looks like per provider.
//
// Tested here rather than only through the menu bar that displays it: shellTools is
// the single owner of the variable, the command and the caveat, and a guessed pair
// fails after somebody has already pasted it and believed it. The three shapes are
// three deliberate answers — a command to run where there is one everybody means, an
// export where only the variable is standard, and the bare URL where neither is.
func TestPointAtIsTheLineForOneShell(t *testing.T) {
	const addr = "127.0.0.1:9999"

	for name, tc := range map[string]struct{ code, want string }{
		// A prefixed assignment rather than an export: it applies to that one run
		// and leaves the shell as it was, and it is what somebody asking for "the
		// command" means — a line to paste and press return on.
		"a provider with a known CLI": {"anthropic",
			"ANTHROPIC_BASE_URL=http://" + addr + "/anthropic claude"},
		"a provider whose CLI is the other one": {"openai",
			"OPENAI_BASE_URL=http://" + addr + "/openai codex"},
		// No agreed variable name, so a guess would be an instruction that does
		// nothing. The URL is what an operator can actually act on.
		"a provider with no standard variable": {"gemini", "http://" + addr + "/gemini"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := PointAt(tc.code, addr); got != tc.want {
				t.Errorf("PointAt(%q) = %q, want %q", tc.code, got, tc.want)
			}
		})
	}

	// The default address rather than a line pointing at nothing: every other local
	// caller here falls back the same way, and a line naming no host is one somebody
	// pastes and then has to debug.
	if got := PointAt("anthropic", ""); !strings.Contains(got, DefaultListen) {
		t.Errorf("PointAt with no address gave %q, which does not name %s", got, DefaultListen)
	}
}

// Every code the table offers a line for has a variable behind it.
//
// The failure this catches is the one the table exists to prevent: a code listed by
// ToolCodes with no variable would print "export =http://…", which is a line that
// silently does nothing in a profile while looking like it worked.
func TestEveryOfferedToolHasAVariable(t *testing.T) {
	for _, code := range ToolCodes() {
		if shellTools[code].Variable == "" {
			t.Errorf("%s is offered a line but has no environment variable behind it", code)
		}
		if line := PointAt(code, DefaultListen); !strings.Contains(line, shellTools[code].Variable) {
			t.Errorf("the line for %s does not carry %s: %q",
				code, shellTools[code].Variable, line)
		}
	}
}
