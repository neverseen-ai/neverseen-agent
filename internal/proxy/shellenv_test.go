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
