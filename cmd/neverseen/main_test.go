package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		stdin   string
		locale  string
		wantErr bool
		want    []string // substrings the output must carry
	}{
		{
			name:  "scanning stdin reports what would be masked",
			args:  []string{"scan"},
			stdin: "write to claire@example.fr about it",
			want:  []string{"EMAIL", "claire@example.fr", "1 value(s) would be masked"},
		},
		{
			name:   "the locale decides which country's identifiers are found",
			args:   []string{"scan"},
			stdin:  "NHS number 9434765919 is on the letter",
			locale: "gb",
			want:   []string{"NHS_NUMBER", "9434765919", "locales: gb"},
		},
		{
			// The same text with no locale set finds nothing, which is what
			// makes the reported locale line worth printing: an operator seeing
			// "none" knows why their data came back clean.
			name:  "clean text says so rather than printing nothing",
			args:  []string{"scan"},
			stdin: "NHS number 9434765919 is on the letter",
			want:  []string{"locales: none", "no sensitive values found"},
		},
		{
			name:  "the report names the pattern that fired",
			args:  []string{"scan"},
			stdin: "key sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789 leaked",
			want:  []string{"SECRET_ANTHROPIC_KEY", "Anthropic API key", "confidence 98"},
		},
		{
			name: "version prints the stamped version",
			args: []string{"version"},
			want: []string{version},
		},
		{
			name: "help prints the usage and the registered locales",
			args: []string{"help"},
			want: []string{"neverseen scan", "NEVERSEEN_PII_LOCALE", "fr, gb, us"},
		},
		{
			name:    "no command is an error, with the usage to recover from it",
			args:    nil,
			wantErr: true,
			want:    []string{"neverseen scan"},
		},
		{
			name:    "an unknown command is an error",
			args:    []string{"masquer"},
			wantErr: true,
		},
		{
			name:    "an invalid locale fails rather than scanning the wrong country",
			args:    []string{"scan"},
			stdin:   "anything",
			locale:  "zz",
			wantErr: true,
		},
		{
			name:    "more than one file is refused rather than silently ignored",
			args:    []string{"scan", "a.txt", "b.txt"},
			wantErr: true,
		},
		{
			name:    "a missing file is an error",
			args:    []string{"scan", "does-not-exist.txt"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NEVERSEEN_PII_LOCALE", tt.locale)
			t.Setenv("NEVERSEEN_PII_ALLOWLIST", "")
			// scan reads the stored policy as the agent does, and this workstation
			// may have one.
			t.Setenv("HOME", t.TempDir())

			var out bytes.Buffer
			err := run(tt.args, strings.NewReader(tt.stdin), &out)

			if tt.wantErr && err == nil {
				t.Fatalf("run(%v) succeeded, want an error", tt.args)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("run(%v): %v", tt.args, err)
			}
			for _, want := range tt.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output does not carry %q:\n%s", want, out.String())
				}
			}
		})
	}
}

// scan and the agent are one binary and must read the same state, or the one
// reports a value as masked that the other forwards in clear. Before scan read the
// stored policy, a category unticked in the menu bar was still reported by scan.
func TestRunScanFollowsTheStoredPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NEVERSEEN_PII_LOCALE", "fr")
	t.Setenv("NEVERSEEN_PII_ALLOWLIST", "")

	if err := os.MkdirAll(filepath.Join(home, ".neverseen"), 0o700); err != nil {
		t.Fatal(err)
	}
	stored := `{"off":["EMAIL"],"substitution":"token","locales":["gb"],"secret_level":"weak"}`
	if err := os.WriteFile(filepath.Join(home, ".neverseen", "policy.json"), []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"scan"}, strings.NewReader("write to claire@example.fr about it"), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "claire@example.fr") {
		t.Errorf("scan reported a category the stored policy switched off:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "locales: gb") {
		t.Errorf("scan read the environment's locale over the stored one:\n%s", out.String())
	}
}

func TestRunScanReadsAFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NEVERSEEN_PII_LOCALE", "fr")
	t.Setenv("NEVERSEEN_PII_ALLOWLIST", "")

	path := filepath.Join(t.TempDir(), "note.txt")
	const body = "Assuré 2 69 05 49 588 157 80, joignable au 06 12 34 56 78."
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var out bytes.Buffer
	if err := run([]string{"scan", path}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, want := range []string{"NIR", "2 69 05 49 588 157 80", "PHONE", "06 12 34 56 78"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output does not carry %q:\n%s", want, out.String())
		}
	}
}

// The allow list has to reach the command, not just the detector: an operator
// who declares a value and still sees it reported has no way to tell which of
// the two is ignoring them.
func TestRunScanHonoursTheAllowList(t *testing.T) {
	t.Setenv("NEVERSEEN_PII_LOCALE", "none")
	t.Setenv("NEVERSEEN_PII_ALLOWLIST", "claire@example.fr")

	var out bytes.Buffer
	if err := run([]string{"scan"}, strings.NewReader("write to claire@example.fr"), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "no sensitive values found") {
		t.Errorf("an allow-listed value was still reported:\n%s", out.String())
	}
}

// status answers about an agent that is not there, and exits non-zero without
// turning the ordinary case of a stopped agent into an error message.
func TestRunStatusWithNoAgent(t *testing.T) {
	t.Setenv("NEVERSEEN_LISTEN", "127.0.0.1:1") // nothing listens there

	var out strings.Builder
	err := run([]string{"status"}, nil, &out)
	if !errors.Is(err, errQuiet) {
		t.Errorf("err = %v, want errQuiet: the exit code is the signal, not a message", err)
	}
	if !strings.Contains(out.String(), "not answering on 127.0.0.1:1") {
		t.Errorf("the report does not name where it looked:\n%s", out.String())
	}
}
