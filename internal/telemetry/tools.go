package telemetry

import (
	"encoding/json"
	"path"
	"strings"

	"github.com/cloakfleet/cloakfleet/pkg/telemetry"
)

// A tool call is the one part of an answer that is neither prose nor a value: it
// is an instruction the tool on this workstation is about to carry out. What the
// heartbeat says about it is what kind of instruction — which tool, which
// programs, what class of action — never the instruction itself. Every function
// in this file takes text and returns words from pkg/telemetry's closed
// vocabularies, and that reduction happens before the recorder's lock, so the
// text is gone by the time anything is counted.

// ToolCall is one tool call as the response path saw it, after restoration.
type ToolCall struct {
	// Name is the tool the model asked for, as the client's schema names it.
	Name string
	// Arguments is the call's arguments, one JSON document.
	Arguments string
	// Restored reports that a masked value was put back into the arguments.
	Restored bool
}

// classify reduces a tool call to the vocabularies: its tool name, the programs
// its shell command names, and the classes those commands fall into.
func classify(call ToolCall) (name string, programs, classes []string) {
	name = toolName(call.Name)
	if name != "Bash" {
		return name, nil, nil
	}
	command, ok := shellCommand(call.Arguments)
	if !ok {
		return name, nil, nil
	}
	programs, classes = readCommand(command)
	return name, programs, classes
}

// toolName reduces a tool's name to KnownTools. An MCP tool is "mcp" whatever its
// server, because the server half is a name the workstation's configuration chose.
func toolName(name string) string {
	if strings.HasPrefix(name, "mcp__") {
		return "mcp"
	}
	return inVocabulary(name, telemetry.KnownTools)
}

// shellCommand reads the command out of a Bash tool call's arguments.
func shellCommand(arguments string) (string, bool) {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil || args.Command == "" {
		return "", false
	}
	return args.Command, true
}

// wrappers are the programs that run the program named after them, and are
// stepped over to reach it. sudo is also a class of its own.
var wrappers = map[string]bool{
	"sudo": true, "doas": true, "env": true, "time": true, "nohup": true,
	"nice": true, "exec": true, "command": true, "xargs": true, "timeout": true,
}

// readCommand names the programs a shell command line runs and the classes they
// fall into.
//
// The line is split on the shell's own operators — newline, `;`, `&&`, `||`, `|`,
// `&`, `$(`, backtick — and the first word of each piece, after any `VAR=value`
// assignments and wrappers, is the program. A pipeline is kept together long
// enough to see a download feeding an interpreter.
//
// TODO: the split is not quote-aware, so an operator inside a quoted string
// splits there and the words after it are read as a command. What it costs is a
// word from a string counted as a program when it happens to be on the list;
// what it never costs is a word outside the list reaching the heartbeat. The
// upgrade is a small shell lexer, once a real corpus of tool calls says the
// miscount matters.
func readCommand(line string) (programs, classes []string) {
	seen := make(map[string]bool)
	class := func(c string) {
		if !seen[c] {
			seen[c] = true
			classes = append(classes, c)
		}
	}

	for _, statement := range splitStatements(line) {
		stages := strings.Split(statement, "|")
		var previous []string
		for _, stage := range stages {
			words := programWords(stage)
			if len(words) == 0 {
				previous = nil
				continue
			}
			program := words[0]
			programs = append(programs, inVocabulary(program, telemetry.KnownPrograms))
			for _, c := range classesOf(words) {
				class(c)
			}
			if len(previous) > 0 && downloaders[previous[0]] && interpreters[program] {
				class("pipe-to-shell")
			}
			previous = words
		}
	}
	return programs, classes
}

// splitStatements cuts a line at every operator that ends one command and starts
// another, leaving `|` in place for readCommand to walk as a pipeline.
func splitStatements(line string) []string {
	replacer := strings.NewReplacer(
		"||", "\n", "&&", "\n", ";", "\n", "$(", "\n", "`", "\n", " & ", "\n",
	)
	return strings.Split(replacer.Replace(line), "\n")
}

// programWords is one command's words, from the program on: assignments, wrappers
// and shell punctuation in front of it are stepped over, and the program is
// reduced to its base name so /usr/bin/python3 and python3 are one program.
//
// A wrapper that is a class of its own — sudo, doas — is kept, so classesOf still
// sees it in front of what it ran.
func programWords(stage string) []string {
	words := strings.Fields(stage)
	var kept []string
	for len(words) > 0 {
		word := strings.Trim(words[0], "(){}'\"")
		switch {
		case word == "":
			words = words[1:]
			continue
		case isAssignment(word):
			words = words[1:]
			continue
		case wrappers[word]:
			if word == "sudo" || word == "doas" {
				kept = append(kept, word)
			}
			words = words[1:]
			// A wrapper's own flags are stepped over with it: `sudo -u root cmd`,
			// `timeout 30 cmd`, `env -i cmd`. The two sudo flags that take a value
			// take it with them, or the user name reads as the program.
			for len(words) > 0 && (strings.HasPrefix(words[0], "-") || isAssignment(words[0]) || isNumber(words[0])) {
				if (word == "sudo" || word == "doas") && (words[0] == "-u" || words[0] == "-g") && len(words) > 1 {
					words = words[1:]
				}
				words = words[1:]
			}
			continue
		}
		break
	}
	if len(words) == 0 {
		if len(kept) == 0 {
			return nil
		}
		return kept
	}
	first := path.Base(strings.Trim(words[0], "(){}'\""))
	if len(kept) > 0 {
		// sudo, then the program it ran, so `sudo rm -rf` is both privilege and
		// destructive; the program counted is the one that acted.
		return append([]string{first}, append(words[1:], kept...)...)
	}
	return append([]string{first}, words[1:]...)
}

func isAssignment(word string) bool {
	i := strings.IndexByte(word, '=')
	if i <= 0 {
		return false
	}
	for _, c := range word[:i] {
		if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func isNumber(word string) bool {
	if word == "" {
		return false
	}
	for _, c := range word {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

var (
	downloaders  = map[string]bool{"curl": true, "wget": true}
	interpreters = map[string]bool{"sh": true, "bash": true, "zsh": true, "python": true, "python3": true, "node": true, "perl": true, "ruby": true}
	network      = map[string]bool{"curl": true, "wget": true, "ssh": true, "scp": true, "rsync": true, "nc": true, "telnet": true, "sftp": true, "ftp": true}
	secretStores = map[string]bool{"vault": true, "op": true, "pass": true, "gpg": true, "security": true}
)

// classesOf names the classes one command falls into, from its program and the
// words right after it. The rest of the line — the path, the payload, the message
// — is never read.
func classesOf(words []string) []string {
	program := words[0]
	rest := words[1:]
	var out []string

	if network[program] {
		out = append(out, "network")
	}
	if program == "sudo" || program == "doas" || program == "su" || hasWord(rest, "sudo") || hasWord(rest, "doas") {
		out = append(out, "privilege")
	}
	if secretStores[program] {
		out = append(out, "secrets")
	}

	sub := ""
	if len(rest) > 0 {
		sub = rest[0]
	}
	switch program {
	case "rm":
		if hasFlag(rest, 'r', 'R', 'f') || hasWord(rest, "--recursive") || hasWord(rest, "--force") {
			out = append(out, "destructive")
		}
	case "git":
		switch {
		case sub == "push" && (hasFlag(rest[1:], 'f') || hasWord(rest, "--force") || hasWord(rest, "--force-with-lease")),
			sub == "reset" && hasWord(rest, "--hard"),
			sub == "clean" && hasFlag(rest[1:], 'f'),
			sub == "branch" && hasFlag(rest[1:], 'D'):
			out = append(out, "destructive")
		}
	case "pip", "pip3", "uv", "brew", "apt", "apt-get", "yum", "dnf", "gem", "cargo", "go", "pnpm", "yarn", "npm":
		if sub == "install" || sub == "add" || (program == "npm" && sub == "i") || (program == "uv" && sub == "pip" && hasWord(rest, "install")) {
			out = append(out, "install")
		}
	case "composer":
		if sub == "require" {
			out = append(out, "install")
		}
	case "aws":
		if sub == "sts" || sub == "secretsmanager" || sub == "ssm" {
			out = append(out, "secrets")
		}
	case "gcloud":
		if sub == "auth" || sub == "secrets" {
			out = append(out, "secrets")
		}
	case "az":
		if sub == "keyvault" || sub == "login" {
			out = append(out, "secrets")
		}
	case "kubectl":
		if (sub == "get" || sub == "describe") && len(rest) > 1 && strings.HasPrefix(rest[1], "secret") {
			out = append(out, "secrets")
		}
	case "docker":
		if sub == "login" {
			out = append(out, "secrets")
		}
	}
	return out
}

// hasFlag reports whether a short-flag cluster in words carries any of the
// letters: `-rf`, `-fr`, `-r`.
func hasFlag(words []string, letters ...rune) bool {
	for _, w := range words {
		if !strings.HasPrefix(w, "-") || strings.HasPrefix(w, "--") {
			continue
		}
		for _, c := range w[1:] {
			for _, l := range letters {
				if c == l {
					return true
				}
			}
		}
	}
	return false
}

func hasWord(words []string, want string) bool {
	for _, w := range words {
		if w == want {
			return true
		}
	}
	return false
}

// ClientFamily reduces a User-Agent to KnownClients.
//
// Substrings of the product token each client actually sends, matched in an order
// that puts the more specific first: Claude Code's `claude-cli/…` before the
// Anthropic SDK it is built on. A User-Agent matching nothing is Other, which is
// the safe direction — a family guessed wrong would put traffic under a tool
// nobody runs.
// TODO: the tokens for cursor, copilot, continue, aider, codex, gemini-cli and
// opencode are best knowledge, not verified against a capture from each. One that
// is wrong costs a client counted as "other"; verify each against a real request
// and record the User-Agent seen beside it.
func ClientFamily(userAgent string) string {
	ua := strings.ToLower(userAgent)
	for _, m := range clientMarkers {
		if strings.Contains(ua, m.token) {
			return m.family
		}
	}
	return telemetry.Other
}

var clientMarkers = []struct{ token, family string }{
	{"claude-cli", "claude-code"},
	{"claude-code", "claude-code"},
	{"codex", "codex"},
	{"cursor", "cursor"},
	{"copilot", "copilot"},
	{"continue", "continue"},
	{"aider", "aider"},
	{"gemini", "gemini-cli"},
	{"opencode", "opencode"},
	{"anthropic", "anthropic-sdk"},
	{"openai", "openai-sdk"},
	{"curl/", "curl"},
}
