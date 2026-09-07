package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/internal/proxy"
)

// What the banner prints is the whole point of -a for the first thirty seconds of a
// run: somebody reads it and pastes a line into another terminal. A line that is
// wrong there is traffic going out unmasked while they watch an empty console, so it
// is asserted rather than eyeballed.
func TestTheBannerPrintsTheCommandToRunElsewhere(t *testing.T) {
	t.Setenv("NEVERSEEN_PII_LOCALE", "fr")
	t.Setenv(proxy.EnvListen, "127.0.0.1:8799")

	agent, err := proxy.FromEnv(nil, proxy.Options{
		Audit:          io.Discard,
		ControlKeyFile: filepath.Join(t.TempDir(), "control.key"),
		// Never the operator's own stored policy: read, it would decide what this
		// test's agent masks, and written it would outlive the run.
		PolicyFile: proxy.NoFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	printRevealBanner(&out, agent, true)
	got := out.String()

	// Through PointAt, so a change to how a tool is pointed at this agent cannot
	// leave this banner handing over the old spelling.
	for _, code := range proxy.ToolCodes() {
		if line := proxy.PointAt(code, agent.Addr); !strings.Contains(got, line) {
			t.Errorf("the banner does not carry %q:\n%s", line, got)
		}
		// The caveat travels with the line everywhere the line is handed over: the
		// failure it warns about has no symptom from the terminal.
		if caveat := proxy.CaveatFor(code); caveat != "" && !strings.Contains(got, caveat) {
			t.Errorf("the banner does not carry the caveat for %s:\n%s", code, got)
		}
	}

	for _, want := range []string{"127.0.0.1:8799", "locales:      fr", "MASK", "UNMASK"} {
		if !strings.Contains(got, want) {
			t.Errorf("the banner does not mention %q:\n%s", want, got)
		}
	}
}

// An agent with no locale is healthy and recognises almost nothing, so an empty
// console means one of two opposite things. The banner has to say which.
func TestTheBannerWarnsWhenNothingWouldBeMasked(t *testing.T) {
	t.Setenv("NEVERSEEN_PII_LOCALE", "")

	agent, err := proxy.FromEnv(nil, proxy.Options{
		Audit:          io.Discard,
		ControlKeyFile: filepath.Join(t.TempDir(), "control.key"),
		// Never the operator's own stored policy: read, it would decide what this
		// test's agent masks, and written it would outlive the run.
		PolicyFile: proxy.NoFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	printRevealBanner(&out, agent, true)

	if got := out.String(); !strings.Contains(got, "Nothing will be masked") {
		t.Errorf("no warning for an agent with no locale:\n%s", got)
	}
}

// Without -v the banner says the bodies are not shown and where to get them.
//
// Said either way, because the bodies are what reveal a value the catalogue never
// recognised — it appears identically in both halves — and a console that simply
// stopped carrying them would leave an operator believing the MASK lines are the
// whole story.
func TestTheBannerSaysWhereTheBodiesAre(t *testing.T) {
	t.Setenv("NEVERSEEN_PII_LOCALE", "fr")
	key := filepath.Join(t.TempDir(), "control.key")
	const stored = proxy.NoFile

	quiet, err := proxy.FromEnv(nil, proxy.Options{Audit: io.Discard, ControlKeyFile: key, PolicyFile: stored})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printRevealBanner(&out, quiet, true)
	if got := out.String(); !strings.Contains(got, "-v") || !strings.Contains(got, defaultTraceDir) {
		t.Errorf("the banner does not say how to get the bodies:\n%s", got)
	}

	dir := filepath.Join(t.TempDir(), "traces")
	tracing, err := proxy.FromEnv(nil, proxy.Options{
		Audit: io.Discard, ControlKeyFile: key, PolicyFile: stored, TraceDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	printRevealBanner(&out, tracing, true)
	if got := out.String(); !strings.Contains(got, dir) {
		t.Errorf("the banner does not name where the bodies go:\n%s", got)
	}
}

// The usage names both flags and the directory -v writes to, from the constant the
// code actually uses. An instruction nobody can find is an instruction that does not
// exist, and one naming the wrong path is worse.
func TestUsageNamesTheRevealFlags(t *testing.T) {
	var out bytes.Buffer
	printUsage(&out)

	got := out.String()
	for _, want := range []string{"neverseen proxy [-a] [-v]", defaultTraceDir, "service definition"} {
		if !strings.Contains(got, want) {
			t.Errorf("the usage does not carry %q:\n%s", want, got)
		}
	}
	// The command it replaces must not linger in the instructions.
	if strings.Contains(got, "neverseen audit") {
		t.Errorf("the usage still names a command that no longer exists:\n%s", got)
	}
}

// With -v alone nothing is printed per exchange, so the banner must not promise
// lines that never arrive.
func TestTheBannerPromisesLinesOnlyWithA(t *testing.T) {
	t.Setenv("NEVERSEEN_PII_LOCALE", "fr")

	agent, err := proxy.FromEnv(nil, proxy.Options{
		ControlKeyFile: filepath.Join(t.TempDir(), "control.key"),
		// Never the operator's own stored policy: read, it would decide what this
		// test's agent masks, and written it would outlive the run.
		PolicyFile: proxy.NoFile,
		TraceDir:   filepath.Join(t.TempDir(), "traces"),
	})
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	printRevealBanner(&out, agent, false)
	got := out.String()

	if strings.Contains(got, "MASK pierre.paul@example.fr") {
		t.Errorf("the banner promises console lines that -v alone does not print:\n%s", got)
	}
	if !strings.Contains(got, "Both bodies of every exchange are written") {
		t.Errorf("the banner does not say where the bodies go:\n%s", got)
	}
}

// The hazard is said on every start rather than left in the documentation: under a
// service definition this output is a log file.
func TestTheBannerWarnsAgainstAServiceDefinition(t *testing.T) {
	t.Setenv("NEVERSEEN_PII_LOCALE", "fr")

	agent, err := proxy.FromEnv(nil, proxy.Options{
		Audit:          io.Discard,
		ControlKeyFile: filepath.Join(t.TempDir(), "control.key"),
		// Never the operator's own stored policy: read, it would decide what this
		// test's agent masks, and written it would outlive the run.
		PolicyFile: proxy.NoFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	printRevealBanner(&out, agent, true)

	if got := out.String(); !strings.Contains(got, "service definition") {
		t.Errorf("the banner does not warn about a service definition:\n%s", got)
	}
}

// The banner must not reassure somebody about a file it is at that moment filling.
// The sentence used to say "nothing is written to a file" unconditionally, which -v
// made false.
func TestTheBannerDoesNotDenyTheFileItIsWriting(t *testing.T) {
	t.Setenv("NEVERSEEN_PII_LOCALE", "fr")
	key := filepath.Join(t.TempDir(), "control.key")
	const stored = proxy.NoFile

	tracing, err := proxy.FromEnv(nil, proxy.Options{
		Audit: io.Discard, ControlKeyFile: key, PolicyFile: stored, TraceDir: filepath.Join(t.TempDir(), "traces"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printRevealBanner(&out, tracing, true)
	if got := out.String(); strings.Contains(got, "nothing is written to a file") {
		t.Errorf("the banner denies the file it is writing:\n%s", got)
	}

	// And without -v the claim is true, so it is still made: a stopped reveal that
	// left no trace is worth telling somebody.
	quiet, err := proxy.FromEnv(nil, proxy.Options{Audit: io.Discard, ControlKeyFile: key, PolicyFile: stored})
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	printRevealBanner(&out, quiet, true)
	if got := out.String(); !strings.Contains(got, "nothing is written to a file") {
		t.Errorf("the banner no longer says what a run without -v keeps:\n%s", got)
	}
}

// The flag has to win over the variable, because that is what makes it usable: an
// operator serving a container for one run should not have to unset a profile.
func TestTheListenFlagOverridesTheEnvironment(t *testing.T) {
	t.Setenv(proxy.EnvListen, "127.0.0.1:8799")

	agent, err := proxy.FromEnv(nil, proxy.Options{
		Listen:         "0.0.0.0:8801",
		ControlKeyFile: filepath.Join(t.TempDir(), "control.key"),
		// Never the operator's own stored policy: read, it would decide what this
		// test's agent masks, and written it would outlive the run.
		PolicyFile: proxy.NoFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	if agent.Addr != "0.0.0.0:8801" {
		t.Errorf("the agent listens on %q, want the address the flag named", agent.Addr)
	}
	if !proxy.BeyondLoopback(agent.Addr) {
		t.Error("the address the flag named is not reported as reachable, so nothing warns")
	}
}

// An agent left on the default must not warn. A warning on every ordinary start is
// one nobody reads by the time it matters.
func TestTheDefaultAddressDoesNotWarn(t *testing.T) {
	agent, err := proxy.FromEnv(nil, proxy.Options{
		ControlKeyFile: filepath.Join(t.TempDir(), "control.key"),
		// Never the operator's own stored policy: read, it would decide what this
		// test's agent masks, and written it would outlive the run.
		PolicyFile: proxy.NoFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	if agent.Addr != proxy.DefaultListen {
		t.Fatalf("the agent listens on %q, want the loopback default", agent.Addr)
	}
	if proxy.BeyondLoopback(agent.Addr) {
		t.Error("the loopback default is reported as reachable")
	}
}
