// Package service registers the agent, and the menu bar icon beside it, with
// whatever starts background jobs on this workstation.
//
// # One owner of the service definition
//
// The launchd plist, the systemd unit and the Windows scheduled task were heredocs
// in install.sh. That meant anything else that wanted to install this agent — a
// package, a disk image, a first-run pane in the icon — could only write a fourth
// copy, and the two that drifted would be the one that installed the agent and the
// one that restarted it. It is the failure CLAUDE.md records for entrypoints,
// applied to the thing that starts them, and it is why install.sh is now a caller
// rather than an author.
//
// # What decides is kept away from what acts
//
// The same split internal/tray uses, for the same reason. Render is pure text and
// takes the platform as a *field* rather than reading runtime.GOOS, so every
// rendering is asserted on every runner: the plist is checked on Linux, the
// scheduled task on macOS. A definition only its own platform can test is a
// definition CI sees once a release. Apply and Remove, which shell out to launchctl,
// systemctl and schtasks, are the only build-tagged files here.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// The names the two jobs are known by to the platform.
//
// Reversed out of install.sh unchanged: they name launchd agents somebody may
// already have loaded, and a rename would leave the old pair running beside the new
// with nothing to say two agents are now masking the same traffic.
const (
	AgentLabel = "ai.neverseen.agent"
	TrayLabel  = "ai.neverseen.tray"

	// WindowsAgentTask and WindowsTrayTask are the same two jobs under Task
	// Scheduler, which has no dotted-label convention and shows the name to a person
	// in a list beside their printer's updater.
	WindowsAgentTask = "Neverseen"
	WindowsTrayTask  = "NeverseenTray"
)

// Job says which of the two a definition starts.
//
// They are not interchangeable and the difference is the whole reason this is a type
// rather than a boolean: the agent is restarted when it dies and the icon is not.
type Job int

const (
	// JobAgent is the proxy. It is restarted whatever happens to it, because nobody
	// is meant to be able to stop the masking by accident.
	JobAgent Job = iota

	// JobTray is the menu bar icon. It is deliberately *not* restarted: its own menu
	// offers "Quit the icon", and a supervisor that put it straight back would have
	// the person click it and watch nothing happen. It returns at the next login.
	JobTray
)

func (j Job) String() string {
	if j == JobTray {
		return "tray"
	}
	return "agent"
}

// Layout is where an installation put things.
//
// Every path a definition needs, gathered in one value so that rendering takes no
// second source. Home is carried rather than looked up so a test can render a
// layout that is not the machine running it — which is what lets the Windows task be
// asserted on a Linux runner.
type Layout struct {
	// Platform is "darwin", "linux" or "windows". A field, not runtime.GOOS: see the
	// package doc for why every rendering has to be reachable from every runner.
	Platform string

	Home       string // the user's home directory
	BinDir     string // where the two binaries were installed
	ConfigFile string // ~/.neverseen/.env
	LogFile    string // ~/.neverseen/agent.log
}

// DefaultLayout reports where install.sh puts things, for the machine this is
// running on.
//
// prefix is empty for the documented default. It is an argument rather than an
// environment variable read here: NEVERSEEN_PREFIX configures an installation and
// not the running agent, and reading it would owe .env.example a line documenting a
// setting that changes nothing about what the agent does —
// TestDocumentedEnvironmentMatchesTheCode fails in both directions.
func DefaultLayout(prefix string) (Layout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, fmt.Errorf("resolve the home directory: %w", err)
	}
	if prefix == "" {
		prefix = filepath.Join(home, ".local")
	}

	config := filepath.Join(home, ".neverseen")
	return Layout{
		Platform:   runtime.GOOS,
		Home:       home,
		BinDir:     filepath.Join(prefix, "bin"),
		ConfigFile: filepath.Join(config, ".env"),
		LogFile:    filepath.Join(config, "agent.log"),
	}, nil
}

// Binary reports the path of the executable a job runs.
func (l Layout) Binary(j Job) string {
	name := "neverseen"
	if j == JobTray {
		name = "neverseen-tray"
	}
	if l.Platform == "windows" {
		name += ".exe"
	}
	return l.join(l.BinDir, name)
}

// join builds a path for the *rendered* platform, not for the one doing the
// rendering.
//
// filepath.Join uses the separator of whatever is running, which quietly defeats the
// thing this package is built around. Rendered on a Mac, the Windows task came out as
// `C:\Users\alice\.local/bin/neverseen.exe` — half one separator, half the other —
// and rendered on the Windows runner the launchd plist would come out with
// backslashes throughout. Platform is a field precisely so every rendering can be
// asserted on every runner, and a path joined by the host makes that assertion mean
// something different on each.
func (l Layout) join(parts ...string) string {
	sep := "/"
	if l.Platform == "windows" {
		sep = `\`
	}
	return strings.Join(parts, sep)
}

// Definition is one job's registration, as a file and the name the platform knows it
// by.
type Definition struct {
	Job Job

	// Label is what the platform calls this job — a launchd label, a systemd unit
	// name, a scheduled task name. It is what Apply and Remove address.
	Label string

	// Path is where the file goes. Empty for a platform that does not keep the
	// definition as a file the user owns; every platform here does.
	Path string

	// Content is the file, in UTF-8. Windows wants its scheduled task XML in UTF-16,
	// and that conversion belongs to the writer rather than here: an encoding is how
	// a file is stored, not what it says, and a golden file nobody can read in a diff
	// is a golden file nobody checks.
	Content string
}

// Render produces the definitions for both jobs, in a stable order: the agent, then
// the icon.
//
// Both are always returned. Whether the icon's binary actually exists is the
// caller's question — install.sh has always skipped registering an icon it did not
// install — and answering it here would make the pure function stat the filesystem.
func Render(l Layout) ([]Definition, error) {
	switch l.Platform {
	case "darwin":
		return renderLaunchd(l), nil
	case "linux":
		return renderSystemd(l), nil
	case "windows":
		return renderSchtasks(l), nil
	default:
		return nil, fmt.Errorf("no service definition for %s; run `neverseen proxy` yourself", l.Platform)
	}
}

// sourceAndRun is the shell one-liner that gives the agent its configuration on the
// platforms that have a shell in the loop.
//
// The configuration lives in a file no Go code reads: launchd sources it here and
// systemd through EnvironmentFile. That is why Windows cannot be rendered the same
// way — Task Scheduler has neither, and cmd cannot read a .env without an
// incantation that would break on the first value carrying a space. See the TODO on
// renderSchtasks.
func sourceAndRun(configFile, binary string, args ...string) string {
	cmd := quoteForShell(binary)
	for _, a := range args {
		cmd += " " + a
	}
	return fmt.Sprintf(`set -a; . %s; set +a; exec %s`, quoteForShell(configFile), cmd)
}

// quoteForShell wraps a path for the shell that launchd and systemd hand it to.
//
// Double quotes rather than single: a home directory can carry an apostrophe — it is
// the surname of a good few million people — and a single-quoted string cannot hold
// one without ending itself.
func quoteForShell(path string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(path) + `"`
}

// escapeXML renders a value safe to place inside an XML element.
//
// A path is user-controlled, and install.sh interpolated it raw: a home directory
// carrying an ampersand produced a plist launchd refuses to parse, which reads to the
// person as an agent that simply never starts.
//
// Deliberately narrower than xml.EscapeText, which also escapes quotes, apostrophes
// and newlines. Every value here lands in element content, where only "&" and "<" are
// required — ">" by convention — so the wider escaper would rewrite paths that work
// today into &#34; and &#39; noise. The definitions this package produces have to be
// byte-for-byte what install.sh wrote for every path that already worked, or an agent
// somebody has loaded right now is replaced rather than adopted; the only bytes that
// may differ are the ones that were malformed.
func escapeXML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// writeDefinition puts one definition on disk, creating its directory.
//
// 0644 in a 0755 directory, which is what install.sh's `cat >` and `mkdir -p` gave
// under a normal umask. Not 0600: a service definition names paths and holds no
// secret, and launchd on some configurations declines to load an agent it considers
// too tightly held.
func writeDefinition(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// run executes a platform command and folds its output into the error.
//
// The output matters: `launchctl load` and `schtasks /create` both report what is
// wrong on standard error and exit non-zero with nothing else, so an error that
// dropped it would say "exit status 1" about a plist the platform refused for a
// reason it had written down.
func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err == nil {
		return nil
	}
	if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, trimmed)
	}
	return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
}

// installable drops the jobs whose binary is not there.
//
// The icon is skipped rather than refused, which is what install.sh did: a Linux
// archive carries no tray binary, and neither does an installation somebody trimmed.
// A missing *agent* is an error, because registering nothing and reporting success is
// how somebody ends up believing their traffic is masked.
func installable(l Layout, defs []Definition) ([]Definition, error) {
	kept := make([]Definition, 0, len(defs))
	for _, d := range defs {
		if _, err := os.Stat(l.Binary(d.Job)); err != nil {
			if d.Job == JobAgent {
				return nil, fmt.Errorf("no agent at %s; install the binaries first", l.Binary(d.Job))
			}
			continue
		}
		kept = append(kept, d)
	}
	return kept, nil
}
