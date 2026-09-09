package proxy

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// DefaultConfigFile is the operator's own configuration, written by install.sh.
const DefaultConfigFile = "~/.neverseen/.env"

// LoadConfigFile puts the operator's configuration into this process's environment,
// so that everything downstream reads it the way it always has.
//
// # Why this exists now
//
// Nothing in Go read this file. launchd sourced it in a shell inside the plist and
// systemd read it through EnvironmentFile, so the agent only ever saw an environment
// somebody else had populated. Windows has neither: Task Scheduler cannot source a
// file, and cmd cannot read a .env without an incantation that breaks on the first
// value carrying a space. So a Windows logon task would have started the agent on its
// built-in defaults — answering, and with no locale loaded, masking almost nothing.
//
// It also closes a gap that was already open on the other two: `neverseen proxy` run
// by hand read a different configuration from the same binary under the service. That
// is the "two entrypoints drifted" failure at one remove, and it is why this is not
// gated on the platform. One file, one meaning, everywhere.
//
// # An existing variable wins
//
// A variable already set is left alone. The file is the standing configuration and an
// environment variable is a deliberate override for this run — the same order `-l`
// takes over NEVERSEEN_LISTEN, and the same order every dotenv reader takes. Under a
// service it changes nothing, because launchd and systemd hand the agent an
// environment with none of these set; interactively it is the difference between
// `NEVERSEEN_PII_LOCALE=us neverseen scan` working and being silently overruled.
//
// # It is not an error for the file to be absent
//
// Most of what runs this has no such file: a test, a container handed its whole
// configuration in variables, somebody who has just built the binary. Absence means
// "nobody has written one", exactly as it does for the policy file.
func LoadConfigFile(path string) error {
	if path == "" {
		path = DefaultConfigFile
	}
	resolved, err := expandHome(path)
	if err != nil {
		return err
	}

	f, err := os.Open(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", resolved, err)
	}
	defer func() { _ = f.Close() }()

	settings, err := parseConfigFile(f)
	if err != nil {
		return fmt.Errorf("read %s: %w", resolved, err)
	}

	for _, s := range settings {
		if _, set := os.LookupEnv(s.name); set {
			continue
		}
		if err := os.Setenv(s.name, s.value); err != nil {
			return fmt.Errorf("apply %s from %s: %w", s.name, resolved, err)
		}
	}
	return nil
}

type setting struct{ name, value string }

// parseConfigFile reads the subset of shell this file has ever been.
//
// It is deliberately not a shell. What `set -a; . file` accepts is the whole language,
// and a Go reader that tried to match it would differ from it in some corner nobody
// would find until their locale silently failed to load. What is accepted here is what
// .env.example actually contains — comments, blank lines, NAME=value, and a value
// wrapped in quotes — and anything else is skipped rather than guessed at.
//
// No interpolation, and that is the one place this knowingly differs from the shell:
// `A=$B` in the file arrives as the literal characters. Expanding it would mean
// implementing parameter expansion, and a half-implementation of it is how a
// credential ends up as an empty string.
func parseConfigFile(r io.Reader) ([]setting, error) {
	var settings []setting

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		// A byte order mark is not whitespace, so TrimSpace leaves it on the front of
		// the first name. install.ps1 writes this file with Set-Content -Encoding UTF8,
		// which on the PowerShell that ships with Windows emits one, and so does
		// Notepad. Left there, the setting is applied under the name
		// "\ufeffNEVERSEEN_PII_LOCALE", nothing reads it, and the agent comes up
		// answering with no locale loaded — masking almost nothing, over a file whose
		// first line looks right to the person who wrote it.
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// `export NAME=value` is how a good few people write these, and the shell
		// takes it. Dropping the keyword costs nothing and refusing it would leave
		// the setting silently unread.
		line = strings.TrimPrefix(line, "export ")

		name, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		settings = append(settings, setting{name, parseValue(strings.TrimSpace(value))})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return settings, nil
}

// parseValue reads one right-hand side the way the shell that used to source this
// file read it.
//
// Two rules, and both are here because the shell has them. A value wrapped in quotes
// ends at its closing quote, and the quotes are syntax rather than content —
// NEVERSEEN_PII_ALLOWLIST="10 Downing Street" has to arrive without them or the
// allowlist entry never matches the address it was written for. An unquoted value ends
// at a comment, because `NEVERSEEN_PII_LOCALE=fr # France only` is a line somebody will
// write and `set -a; . file` would have given them "fr".
//
// Getting the second one wrong is not cosmetic: the locale would be the string
// "fr # France only", pii.LocalePatterns would recognise none of it, and the agent
// would come up answering and masking almost nothing while its configuration looked
// right to the person reading it.
func parseValue(raw string) string {
	if len(raw) > 0 && (raw[0] == '"' || raw[0] == '\'') {
		if end := strings.IndexByte(raw[1:], raw[0]); end >= 0 {
			return raw[1 : end+1]
		}
		// An opening quote with no closing one is not a quoted value; it is somebody's
		// apostrophe. Taken as the literal text, which is what they meant.
		return raw
	}

	// A comment opens on whitespace then "#". Without the whitespace requirement a
	// value that simply contains a hash — a URL fragment, a password — would be cut
	// in half.
	for i := 1; i < len(raw); i++ {
		if raw[i] == '#' && (raw[i-1] == ' ' || raw[i-1] == '\t') {
			return strings.TrimRight(raw[:i], " \t")
		}
	}
	return raw
}
