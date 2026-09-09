package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfigFileReadsWhatTheShellRead(t *testing.T) {
	const file = `
# A comment, and the blank line above it.
NEVERSEEN_PII_LOCALE=fr
  NEVERSEEN_LISTEN = 127.0.0.1:9999
export NEVERSEEN_SUBSTITUTION=fake
NEVERSEEN_PII_ALLOWLIST="10 Downing Street,123456789"
QUOTED_SINGLE='keeps spaces'
TRAILING_COMMENT=us # United States only
HASH_IN_VALUE=p@ss#w0rd
EMPTY=
not a setting at all
=novalue
`

	want := map[string]string{
		"NEVERSEEN_PII_LOCALE":    "fr",
		"NEVERSEEN_LISTEN":        "127.0.0.1:9999",
		"NEVERSEEN_SUBSTITUTION":  "fake",
		"NEVERSEEN_PII_ALLOWLIST": "10 Downing Street,123456789",
		"QUOTED_SINGLE":           "keeps spaces",
		"TRAILING_COMMENT":        "us",
		"HASH_IN_VALUE":           "p@ss#w0rd",
		"EMPTY":                   "",
	}

	settings, err := parseConfigFile(strings.NewReader(file))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := map[string]string{}
	for _, s := range settings {
		got[s.name] = s.value
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}
	if len(got) != len(want) {
		t.Errorf("parsed %d settings, want %d: %v", len(got), len(want), got)
	}
}

// TestATrailingCommentIsNotPartOfTheValue is the case that is not cosmetic.
// `NEVERSEEN_PII_LOCALE=fr # France only` is a line somebody will write and the shell
// that used to source this file gave them "fr". Read whole, pii.LocalePatterns
// recognises none of it, and the agent comes up answering and masking almost nothing
// while the configuration looks right to the person reading it.
func TestATrailingCommentIsNotPartOfTheValue(t *testing.T) {
	settings, err := parseConfigFile(strings.NewReader("NEVERSEEN_PII_LOCALE=fr # France only\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(settings) != 1 || settings[0].value != "fr" {
		t.Fatalf("got %#v, want the locale alone", settings)
	}
}

// TestAHashInsideAValueSurvives is the other half of the rule above, and the reason a
// comment has to open on whitespace. A credential is exactly the kind of value that
// carries a hash, and cut in half it is a credential the agent then fails to match.
func TestAHashInsideAValueSurvives(t *testing.T) {
	settings, err := parseConfigFile(strings.NewReader("NEVERSEEN_ENROLMENT_TOKEN=abc#def\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(settings) != 1 || settings[0].value != "abc#def" {
		t.Fatalf("got %#v, want the token whole", settings)
	}
}

// TestAnExistingVariableWinsOverTheFile: the file is the standing configuration, an
// environment variable is a deliberate override for this run. The same order `-l`
// takes over NEVERSEEN_LISTEN.
func TestAnExistingVariableWinsOverTheFile(t *testing.T) {
	path := writeConfig(t, "NEVERSEEN_PII_LOCALE=fr\nNEVERSEEN_SUBSTITUTION=token\n")
	t.Setenv("NEVERSEEN_PII_LOCALE", "us")

	if err := LoadConfigFile(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := os.Getenv("NEVERSEEN_PII_LOCALE"); got != "us" {
		t.Errorf("the file overrode a variable that was already set: got %q, want us", got)
	}
	if got := os.Getenv("NEVERSEEN_SUBSTITUTION"); got != "token" {
		t.Errorf("a variable that was not set did not come from the file: got %q", got)
	}
}

// TestAnAbsentConfigFileIsNotAnError. Most of what runs this binary has no such file:
// a test, a container handed everything in variables, somebody who has just built it.
// Absence means "nobody has written one", as it does for the policy file — refusing to
// start over it would make the agent unrunnable in every one of those cases.
func TestAnAbsentConfigFileIsNotAnError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nothing-here", ".env")
	if err := LoadConfigFile(missing); err != nil {
		t.Fatalf("an absent file was reported as an error: %v", err)
	}
}

// TestTheShippedExampleParses holds the reader against the file this project actually
// ships. A parser that agreed with a fixture but not with .env.example would be one
// nobody noticed was wrong until an operator's locale silently failed to load.
func TestTheShippedExampleParses(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".env.example"))
	if err != nil {
		t.Skipf("no .env.example to read: %v", err)
	}

	settings, err := parseConfigFile(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse .env.example: %v", err)
	}
	if len(settings) == 0 {
		t.Fatal(".env.example parsed to nothing; every uncommented setting was missed")
	}

	// Every name it yields has to look like a name. A line misread as a setting shows
	// up here as a "name" carrying spaces or punctuation, which is how a broken parser
	// announces itself before it goes on to set something strange in the environment.
	for _, s := range settings {
		if strings.ContainsAny(s.name, " \t\"'#") {
			t.Errorf("parsed %q as a setting name, which means a line was misread", s.name)
		}
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestALeadingByteOrderMarkIsNotPartOfTheFirstName is the Windows half of the case
// above, and it fails the same way: silently. install.ps1 writes this file with
// Set-Content -Encoding UTF8, which on the PowerShell that ships with Windows emits a
// BOM, and Notepad does the same to a file somebody edits by hand. strings.TrimSpace
// does not remove U+FEFF, so the name arrives with it attached, os.Setenv sets a
// variable nothing reads, and the agent starts with no locale loaded.
//
// The setting is deliberately the first line here. In the shipped template the first
// line is a comment, which masks this by luck — the moment somebody moves a setting to
// the top, or writes their own file, the luck runs out.
func TestALeadingByteOrderMarkIsNotPartOfTheFirstName(t *testing.T) {
	const file = "\ufeffNEVERSEEN_PII_LOCALE=fr\nNEVERSEEN_LISTEN=127.0.0.1:9999\n"

	settings, err := parseConfigFile(strings.NewReader(file))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(settings) != 2 {
		t.Fatalf("parsed %d settings, want 2: %v", len(settings), settings)
	}
	if settings[0].name != "NEVERSEEN_PII_LOCALE" {
		t.Errorf("first name is %q, want %q — the byte order mark is still attached",
			settings[0].name, "NEVERSEEN_PII_LOCALE")
	}
	if settings[0].value != "fr" {
		t.Errorf("first value is %q, want %q", settings[0].value, "fr")
	}
}
