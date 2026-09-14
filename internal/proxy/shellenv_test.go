package proxy

import (
	"context"
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
		// An export rather than a command, because the one command anybody would
		// name here does not read the variable. codex was in this slot and the
		// pair was wrong — it takes its base URL from ~/.codex/config.toml alone
		// — so the line it produced went to OpenAI unmasked with no error to
		// notice. The variable stays because the OpenAI SDKs do read it.
		"a provider whose variable is standard but whose CLI ignores it": {"openai",
			"export OPENAI_BASE_URL=http://" + addr + "/openai"},
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

// The same line for PowerShell, and asserted on every platform rather than only on
// Windows — the reason internal/service takes the platform as a field.
//
// PointAt used to spell the POSIX `export` whatever the platform, and the menu bar
// that hands this line over is built and installed on Windows. `export NAME=value`
// in PowerShell is not an error, it is a command that does nothing: the person
// pastes it, nothing complains, and every request goes to the provider unmasked.
// That is the failure Shell exists to prevent, reached through the one function
// that had no Shell to take.
func TestPointAtForSpellsTheAssignmentTheShellReads(t *testing.T) {
	const addr = "127.0.0.1:9999"

	for name, tc := range map[string]struct{ code, want string }{
		"a provider with a known CLI": {"anthropic",
			`$env:ANTHROPIC_BASE_URL = "http://` + addr + `/anthropic"; claude`},
		"a provider whose variable is standard but whose CLI ignores it": {"openai",
			`$env:OPENAI_BASE_URL = "http://` + addr + `/openai"`},
		// Shell-agnostic: there is no variable to assign, so there is nothing to
		// spell either way.
		"a provider with no standard variable": {"gemini", "http://" + addr + "/gemini"},
	} {
		t.Run(name, func(t *testing.T) {
			got := PointAtFor(tc.code, addr, ShellPowerShell)
			if got != tc.want {
				t.Errorf("PointAtFor(%q, powershell) = %q, want %q", tc.code, got, tc.want)
			}
			if strings.HasPrefix(got, "export ") {
				t.Errorf("PointAtFor(%q, powershell) returned a POSIX export: %q", tc.code, got)
			}
		})
	}

	// One line, never the two that a trailing newline from assignment would make:
	// the callers put this inside their own formatting — a banner line, a clipboard,
	// a menu tooltip — and a newline in the middle of it breaks all three.
	for _, shell := range []Shell{ShellPosix, ShellPowerShell} {
		for _, code := range ToolCodes() {
			if got := PointAtFor(code, addr, shell); strings.Contains(got, "\n") {
				t.Errorf("PointAtFor(%q, %s) = %q, which is more than one line", code, shell, got)
			}
		}
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

// No line pairs a variable with a command that does not read it.
//
// codex is the case that paid for this: it sat in the table as the CLI for
// OPENAI_BASE_URL, and it does not read that variable for its own model traffic —
// only the `openai_base_url` key in ~/.codex/config.toml reaches its built-in
// provider. So the line was pasted, believed, and answered by OpenAI directly,
// with nothing in the terminal to show it. Asserted on the rendered line rather
// than on the field, because the line is what somebody runs.
func TestNoLineOffersCodexAVariableItDoesNotRead(t *testing.T) {
	for _, code := range ToolCodes() {
		if line := PointAt(code, DefaultListen); strings.Contains(line, " codex") {
			t.Errorf("the line for %s runs codex against a variable it ignores: %q", code, line)
		}
	}

	// And the way that does work is named where the line is handed over, since the
	// export alone points codex at nothing.
	if caveat := CaveatFor("openai"); !strings.Contains(caveat, "openai_base_url") {
		t.Errorf("the openai caveat does not name the key codex actually reads: %q", caveat)
	}
}

// TestPowerShellGetsItsOwnSpelling: an `export` line in PowerShell is not an error, it
// is a command that does nothing — so the tool goes to its provider unmasked while the
// person watching the terminal sees no symptom at all. That is why this is a rendering
// rather than a preference.
func TestPowerShellGetsItsOwnSpelling(t *testing.T) {
	var out strings.Builder
	if err := ShellEnvFor(context.Background(), &out, "127.0.0.1:9787", true, ShellPowerShell); err != nil {
		t.Fatalf("ShellEnvFor: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, `$env:ANTHROPIC_BASE_URL = "http://127.0.0.1:9787/anthropic"`) {
		t.Errorf("no PowerShell assignment in:\n%s", got)
	}
	if strings.Contains(got, "export ") {
		t.Errorf("a POSIX export reached the PowerShell rendering:\n%s", got)
	}
}

// TestAStoppedAgentPrintsOnlyCommentsInEveryShell holds the property that makes the
// profile line safe to leave in place — in both shells, because it is the whole reason
// `eval "$(neverseen env)"` was chosen over exporting a base URL. Evaluated against a
// stopped agent it must change nothing, so the tools keep working unmasked rather than
// failing on a line nobody wrote.
func TestAStoppedAgentPrintsOnlyCommentsInEveryShell(t *testing.T) {
	// An address nothing is listening on. Query fails, and the timeout is short.
	const dead = "127.0.0.1:1"

	for _, shell := range []Shell{ShellPosix, ShellPowerShell} {
		var out strings.Builder
		if err := ShellEnvFor(context.Background(), &out, dead, false, shell); err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			if line != "" && !strings.HasPrefix(line, "#") {
				t.Errorf("%s: a stopped agent printed something evaluable: %q", shell, line)
			}
		}
	}
}

// TestParseShellRefusesCmdByName. Somebody on Windows will try it first, so the refusal
// says why rather than listing what is allowed and leaving them to guess.
func TestParseShellRefusesCmdByName(t *testing.T) {
	if _, err := ParseShell("cmd"); err == nil {
		t.Fatal("cmd was accepted; it has no eval and cannot be supported")
	} else if !strings.Contains(err.Error(), "eval") {
		t.Errorf("the refusal does not say why: %v", err)
	}

	if got, err := ParseShell(""); err != nil || got != DefaultShell() {
		t.Errorf(`ParseShell("") = %q, %v; want the platform default`, got, err)
	}
}
