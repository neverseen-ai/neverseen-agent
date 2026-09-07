package detector

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
)

// The agent's own source, run through the agent's own catalogue.
//
// Two questions, and they are not the same one. The first is whether this project
// has committed something that reads as a credential — a real key pasted into a
// fixture is the failure every secret scanner exists to catch, and this repository
// happens to contain the scanner. The second is whether the catalogue *invents*
// credentials in ordinary source, which is what somebody pasting a file into a
// coding agent through this proxy experiences: identifiers replaced by
// [SECRET_n] and a model reviewing bookkeeping instead of code.
//
// `cmd/` and `extension/` because those are the two trees written to be read by
// people rather than by the corpus — the commands somebody runs and the client
// that will carry this traffic. The engine's own packages are excluded on purpose:
// pkg/pii is full of key shapes by construction, and asserting anything about them
// here would be asserting that the catalogue does not contain a catalogue.
//
// It is a gate rather than a measurement, so it names what it expects. A new
// finding fails, and the fix is either to remove the value or to add it here with
// a reason — which is the point: adding a line should feel like a decision.

// expectedSecrets are the values in those two trees that a credential pattern
// recognises, and should.
//
// Every one is fabricated and none has ever been live. They are here precisely
// because they carry the exact shape of the real thing: a fixture built from a
// value the detector cannot recognise would test the plumbing and not the
// catalogue, and would go green the day a pattern stopped working.
var expectedSecrets = map[string]string{
	"sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789": "a fabricated Anthropic key, so the " +
		"end-to-end fixtures exercise a credential the catalogue actually claims",
	"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": "a fabricated 64-byte " +
		"control key, the shape `neverseen key` writes and the extension reads",
	"claude:11111111-2222-3333-4444-555555555555": "a fabricated session name, behind the " +
		"`X-Session-Id` header the extension sends. SESSION_ID is in the generic keyword list on " +
		"purpose — identifiers.go depends on it — and the keyword now tolerates a separator between " +
		"its letters, so the header spelling reads exactly as the environment variable does",
}

func TestOurOwnSourceGrowsNoCredentials(t *testing.T) {
	// Every locale, because this is about what the agent would do to these files
	// on somebody's workstation and a deployment may have loaded any of them.
	d := New(Config{Locales: pii.LocaleCodes()})

	var unexpected []string
	for _, tree := range []string{"cmd", "extension"} {
		for _, file := range sourceFilesUnder(t, filepath.Join("..", "..", tree)) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			text := string(raw)

			for _, m := range d.Scan(text) {
				if !pii.IsSecret(m.Category) {
					// Personal-data categories are a different question, and one
					// this repository's fixtures answer deliberately: they carry
					// addresses and NHS numbers because that is what they test.
					continue
				}
				if _, known := expectedSecrets[m.Value]; known {
					continue
				}
				line := 1 + strings.Count(text[:m.Start], "\n")
				unexpected = append(unexpected, fmtFinding(file, line, m))
			}
		}
	}
	sort.Strings(unexpected)

	for _, f := range unexpected {
		t.Errorf("%s\n\tEither this is a real credential and must not be committed, or the "+
			"catalogue invented one in ordinary source — which is what a code review through "+
			"this agent would see. If it is a deliberate fixture, add it to expectedSecrets "+
			"with a reason.", f)
	}
}

// Every value the gate expects must still be found.
//
// The half that stops this test rotting into a pass. Without it, a pattern that
// broke would remove a finding and the assertion above would go quieter and
// greener — which is the direction that matters, since a credential pattern that
// stopped working is the failure the whole catalogue exists to prevent.
func TestTheExpectedCredentialsAreStillRecognised(t *testing.T) {
	d := New(Config{Locales: pii.LocaleCodes()})

	for value, why := range expectedSecrets {
		var found bool
		for _, m := range d.Scan("token = " + value) {
			if m.Value == value && pii.IsSecret(m.Category) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is no longer recognised as a credential, and it is in the tree as %s —\n"+
				"\tso either a pattern broke, or this entry is stale and should go", value, why)
		}
	}
}

// sourceFilesUnder lists the files worth scanning, or reports none when the tree
// is not checked out.
//
// A missing tree is skipped rather than failed: `extension/` is built
// independently of the agent, and a Go suite that could not run without it would
// be the coupling the whole repository layout avoids.
func sourceFilesUnder(t *testing.T, root string) []string {
	t.Helper()

	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Logf("%s is not present, so nothing was scanned there", root)
		return nil
	}

	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		// Built output and vendored dependencies are not written by anybody here.
		if strings.Contains(path, "node_modules") || strings.Contains(path, "/dist/") {
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".ts", ".js", ".mjs", ".json", ".html", ".sh", ".yml", ".yaml", ".md":
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

func fmtFinding(file string, line int, m Match) string {
	value := m.Value
	if len(value) > 48 {
		value = value[:48] + "…"
	}
	return file + ":" + itoa(line) + ": " + string(m.Category) + " " + value
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
