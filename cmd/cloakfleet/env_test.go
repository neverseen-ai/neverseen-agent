package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/proxy"
)

// The documented configuration and the code have to agree, in both directions.
//
// This test lives with the command because the command is the only place that
// sees every setting the binary has: the detector owns some and the proxy owns
// the rest, and a test inside either one can only ever check its own half.
//
// The direction that rots is the second one. The project this replaces
// documented five environment variables that nothing had ever read — an operator
// setting one of them would have watched it do nothing, with no way to tell.
func TestDocumentedEnvironmentMatchesTheCode(t *testing.T) {
	const path = "../../.env.example"

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc := string(raw)

	// Every variable some package reads, named from the constants themselves so
	// a rename cannot leave this list behind.
	read := []string{
		detector.EnvLocale,
		detector.EnvAllowList,
		detector.EnvSubstitution,
		proxy.EnvListen,
		proxy.EnvProviders,
		proxy.EnvEncryptionKey,
	}

	for _, name := range read {
		if !strings.Contains(doc, name) {
			t.Errorf("%s is read by the agent but is not documented in %s", name, path)
		}
	}

	for line := range strings.Lines(doc) {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		name, _, found := strings.Cut(line, "=")
		if !found || !strings.HasPrefix(name, "CLOAKFLEET_") {
			continue
		}
		if !slices.Contains(read, name) {
			t.Errorf("%s is documented in %s but no code reads it", name, path)
		}
	}
}

// The usage text is the other documented surface, and it is built from the same
// constants for the same reason. A wrong instruction in it is worse than none: it
// sends an operator to set a variable that does nothing.
func TestUsageNamesEverySetting(t *testing.T) {
	var out strings.Builder
	printUsage(&out)
	usage := out.String()

	for _, name := range []string{
		detector.EnvLocale, detector.EnvAllowList, detector.EnvSubstitution,
		proxy.EnvListen, proxy.EnvProviders, proxy.EnvEncryptionKey,
	} {
		if !strings.Contains(usage, name) {
			t.Errorf("the usage does not name %s", name)
		}
	}

	// And the shape of the thing an operator has to get right first: the
	// provider goes in the path.
	for _, want := range []string{"/anthropic", "/openai", proxy.DefaultListen} {
		if !strings.Contains(usage, want) {
			t.Errorf("the usage does not show %q", want)
		}
	}
}
