package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/cloakfleet/cloakfleet/internal/proxy"
)

// What `audit` prints is the whole point of the command for the first thirty
// seconds of its life: somebody reads it and pastes a line into another terminal.
// A line that is wrong there is traffic going out unmasked while they watch an
// empty console, so it is asserted rather than eyeballed.
func TestAuditPrintsTheCommandToRunElsewhere(t *testing.T) {
	t.Setenv("CLOAKFLEET_PII_LOCALE", "fr")

	agent, err := proxy.FromEnv(nil, proxy.Options{
		Listen: proxy.DefaultAuditListen,
		Audit:  io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Addr != proxy.DefaultAuditListen {
		t.Fatalf("audit listens on %q, want %q", agent.Addr, proxy.DefaultAuditListen)
	}

	var out bytes.Buffer
	printAuditInstructions(&out, agent)
	got := out.String()

	// Through PointAt, so a change to how a tool is pointed at this agent cannot
	// leave this command handing over the old spelling.
	for _, code := range proxy.ToolCodes() {
		if line := proxy.PointAt(code, proxy.DefaultAuditListen); !strings.Contains(got, line) {
			t.Errorf("the instructions do not carry %q:\n%s", line, got)
		}
		// The caveat travels with the line everywhere the line is handed over:
		// the failure it warns about has no symptom from the terminal.
		if caveat := proxy.CaveatFor(code); caveat != "" && !strings.Contains(got, caveat) {
			t.Errorf("the instructions do not carry the caveat for %s:\n%s", code, got)
		}
	}

	for _, want := range []string{"33333", "locales:      fr", "MASK", "UNMASK"} {
		if !strings.Contains(got, want) {
			t.Errorf("the instructions do not mention %q:\n%s", want, got)
		}
	}
}

// An agent with no locale is healthy and recognises almost nothing, so an empty
// console means one of two opposite things. The command has to say which.
func TestAuditWarnsWhenNothingWouldBeMasked(t *testing.T) {
	t.Setenv("CLOAKFLEET_PII_LOCALE", "")

	agent, err := proxy.FromEnv(nil, proxy.Options{Listen: proxy.DefaultAuditListen, Audit: io.Discard})
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	printAuditInstructions(&out, agent)

	if got := out.String(); !strings.Contains(got, "Nothing will be masked") {
		t.Errorf("no warning for an agent with no locale:\n%s", got)
	}
}

// And the usage names the command, from the port the code actually binds.
func TestUsageNamesTheAuditCommand(t *testing.T) {
	var out bytes.Buffer
	printUsage(&out)

	got := out.String()
	if !strings.Contains(got, "cloakfleet audit") {
		t.Errorf("the usage does not name the audit command:\n%s", got)
	}
	if !strings.Contains(got, auditPort()) {
		t.Errorf("the usage does not name port %s:\n%s", auditPort(), got)
	}
}
