package service

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden definitions from this run")

// sample is one layout, used for every platform, so the golden files differ only
// where the platforms do.
//
// A home directory carrying an apostrophe, because a good few million surnames do
// and a single-quoted shell string cannot hold one without ending itself.
func sample(platform string) Layout {
	home := "/home/o'brien"
	if platform == "darwin" {
		home = "/Users/o'brien"
	}
	if platform == "windows" {
		home = `C:\Users\o'brien`
	}

	// Built through the layout's own join, not filepath.Join: the whole point of
	// Platform being a field is that a rendering does not depend on the machine doing
	// it, and a fixture assembled with the host's separator would put that back.
	l := Layout{Platform: platform, Home: home}
	l.BinDir = l.join(home, ".local", "bin")
	l.ConfigFile = l.join(home, ".neverseen", ".env")
	l.LogFile = l.join(home, ".neverseen", "agent.log")
	return l
}

// TestAPathIsJoinedForTheRenderedPlatform is the property the goldens cannot hold on
// their own: recorded on a Mac they look right, and the same code on the Windows
// runner would render every one of them differently. The Windows task carried
// `C:\Users\o'brien\.local/bin/neverseen.exe` — half one separator, half the other —
// until this was asserted.
func TestAPathIsJoinedForTheRenderedPlatform(t *testing.T) {
	if got := sample("windows").Binary(JobAgent); strings.Contains(got, "/") {
		t.Errorf("the Windows binary path carries a forward slash: %s", got)
	}
	for _, platform := range []string{"darwin", "linux"} {
		if got := sample(platform).Binary(JobAgent); strings.Contains(got, `\`) {
			t.Errorf("the %s binary path carries a backslash: %s", platform, got)
		}
	}
}

// TestRenderMatchesTheGoldenDefinitions asserts every platform's rendering on
// whatever runner this happens to be, which is the whole reason Platform is a field
// rather than runtime.GOOS. Rendered only where they run, the Windows task would be
// checked on no runner this project has and the plist on one job in the matrix.
func TestRenderMatchesTheGoldenDefinitions(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			defs, err := Render(sample(platform))
			if err != nil {
				t.Fatalf("Render(%s): %v", platform, err)
			}
			if len(defs) == 0 {
				t.Fatalf("Render(%s) produced nothing", platform)
			}

			for _, d := range defs {
				name := platform + "-" + d.Job.String() + goldenExt(platform, d.Job)
				path := filepath.Join("testdata", name)

				if *update {
					if err := os.WriteFile(path, []byte(d.Content), 0o644); err != nil {
						t.Fatalf("write %s: %v", path, err)
					}
					continue
				}

				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read %s: %v (run go test ./internal/service -update)", path, err)
				}
				if d.Content != string(want) {
					t.Errorf("%s differs from %s:\n--- got ---\n%s\n--- want ---\n%s",
						name, path, d.Content, want)
				}
			}
		})
	}
}

// goldenExt names the file by what the platform calls that kind of definition, which
// on Linux is two things: the agent is a systemd unit and the icon is a desktop entry.
// A golden called linux-tray.service would describe it as something it is not.
func goldenExt(platform string, job Job) string {
	switch platform {
	case "darwin":
		return ".plist"
	case "windows":
		return ".xml"
	default:
		if job == JobTray {
			return ".desktop"
		}
		return ".service"
	}
}

// TestOnlyTheAgentIsRestarted holds the one difference between the two jobs that
// matters, on every platform that registers an icon.
//
// The icon must not be restarted: its own menu offers "Quit the icon", and a
// supervisor that put it straight back would have the person click it and watch
// nothing happen. Asserted rather than left to the golden files, because a golden
// file records what the code does and this records what it must do — the two part
// company on the commit that regenerates a golden without reading it.
func TestOnlyTheAgentIsRestarted(t *testing.T) {
	// Spelled as it appears in each format rather than as a bare word, because the
	// three formats say the same thing three ways and a substring test on "Restart"
	// alone would match the unit's RestartSec and pass on a unit that restarts nothing.
	restartMarker := map[string]string{
		"darwin":  "<key>KeepAlive</key>",
		"linux":   "Restart=always",
		"windows": "<RestartOnFailure>",
	}

	for platform, marker := range restartMarker {
		defs, err := Render(sample(platform))
		if err != nil {
			t.Fatalf("Render(%s): %v", platform, err)
		}

		var sawTray bool
		for _, d := range defs {
			has := strings.Contains(d.Content, marker)

			switch d.Job {
			case JobAgent:
				if !has {
					t.Errorf("%s: the agent carries no %s, so nothing puts the masking back", platform, marker)
				}
			case JobTray:
				sawTray = true
				if has {
					t.Errorf("%s: the icon carries %s, so \"Quit the icon\" cannot work", platform, marker)
				}
			}
		}
		if !sawTray {
			t.Errorf("%s registers no icon", platform)
		}
	}
}

// TestLinuxRegistersTheIconSomewhereTheDesktopReads.
//
// The two jobs go to two registries on this platform and the icon must not drift into
// the agent's. A systemd unit for the icon would have to be wanted by
// graphical-session.target, which only some desktops reach, and would start without
// the session's environment: the failure is a job that never runs and says nothing,
// which is what registering the icon at all was meant to stop.
func TestLinuxRegistersTheIconSomewhereTheDesktopReads(t *testing.T) {
	defs, err := Render(sample("linux"))
	if err != nil {
		t.Fatalf("Render(linux): %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("Render(linux) = %d definitions, want the agent and the icon", len(defs))
	}

	for _, d := range defs {
		switch d.Job {
		case JobAgent:
			if !strings.HasSuffix(d.Path, "/.config/systemd/user/"+SystemdUnit) {
				t.Errorf("the agent is registered at %s, which systemd does not read", d.Path)
			}
		case JobTray:
			if !strings.HasSuffix(d.Path, "/.config/autostart/"+AutostartEntry) {
				t.Errorf("the icon is registered at %s, which no desktop reads at login", d.Path)
			}
			if strings.Contains(d.Content, "[Unit]") || strings.Contains(d.Content, "WantedBy=") {
				t.Errorf("the icon is rendered as a systemd unit:\n%s", d.Content)
			}
		}
	}
}

// TestTheIconWritesWhereSomebodyCanReadIt.
//
// A desktop entry has no StandardErrorPath, and what the icon has to say on Linux it
// says on the way out — that nothing on this desktop will draw it, and that the agent
// is masking all the same. Started bare, that message goes to a stream the session
// discards, and the person is left with the one symptom the icon exists to prevent
// being ambiguous: no icon.
func TestTheIconWritesWhereSomebodyCanReadIt(t *testing.T) {
	l := sample("linux")
	defs, err := Render(l)
	if err != nil {
		t.Fatalf("Render(linux): %v", err)
	}
	for _, d := range defs {
		if d.Job != JobTray {
			continue
		}
		if !strings.Contains(d.Content, l.LogFile) {
			t.Errorf("the icon's entry names no log file, so its one message goes nowhere:\n%s", d.Content)
		}
	}
}

// TestAHomeDirectoryWithAnApostropheSurvives is the case that a naive single-quoted
// shell string breaks. It is not hypothetical: the surname is common enough that
// somebody would have hit it, and the symptom is an agent that silently never starts.
func TestAHomeDirectoryWithAnApostropheSurvives(t *testing.T) {
	defs, err := Render(sample("darwin"))
	if err != nil {
		t.Fatalf("Render(darwin): %v", err)
	}
	for _, d := range defs {
		if strings.Contains(d.Content, `'`+"brien") && !strings.Contains(d.Content, `"/Users/o'brien`) {
			t.Errorf("%s quotes the path in a way that ends on the apostrophe:\n%s", d.Label, d.Content)
		}
	}
}

// TestAnUnknownPlatformIsRefused rather than silently rendering nothing. An
// installer that got an empty list would report success and register no service at
// all, which reads to the person as an agent that starts and then is not there.
func TestAnUnknownPlatformIsRefused(t *testing.T) {
	if _, err := Render(Layout{Platform: "plan9"}); err == nil {
		t.Fatal("Render accepted plan9; an unknown platform has to be refused")
	}
}

// TestTheWindowsTaskNamesItselfConsistently: the URI inside the XML and the label
// Apply addresses are two spellings of one name, and Task Scheduler refuses an import
// whose URI does not match the name it is filed under.
func TestTheWindowsTaskNamesItselfConsistently(t *testing.T) {
	defs, err := Render(sample("windows"))
	if err != nil {
		t.Fatalf("Render(windows): %v", err)
	}
	for _, d := range defs {
		if !strings.Contains(d.Content, `<URI>\`+d.Label+`</URI>`) {
			t.Errorf("%s: the XML does not carry its own label as a URI:\n%s", d.Label, d.Content)
		}
	}
}

// TestAPercentInAPathIsNotASystemdSpecifier is the Linux twin of the apostrophe case
// above, and it fails the same silent way — in both of the platform's two formats.
//
// systemd reads "%" as the start of a specifier, so a home directory or a --prefix
// carrying one yields a unit it refuses to parse: "Failed to resolve unit specifiers",
// which reads to the person as an agent that simply never starts. The literal has to
// arrive doubled.
func TestAPercentInAPathIsNotASystemdSpecifier(t *testing.T) {
	l := sample("linux")
	l.Home = "/home/50%off"
	l.BinDir = l.join(l.Home, ".local", "bin")
	l.ConfigFile = l.join(l.Home, ".neverseen", ".env")

	defs, err := Render(l)
	if err != nil {
		t.Fatalf("Render(linux): %v", err)
	}
	for _, d := range defs {
		for _, line := range strings.Split(d.Content, "\n") {
			// Exec= is the desktop entry's half of the same failure: "%" introduces a
			// field code there, so a literal one has to arrive doubled or the desktop
			// swallows it with the character after it.
			if !strings.HasPrefix(line, "EnvironmentFile=") && !strings.HasPrefix(line, "ExecStart=") &&
				!strings.HasPrefix(line, "Exec=") {
				continue
			}
			if !strings.Contains(line, "50%%off") {
				t.Errorf("%s: %q leaves the percent unescaped, so systemd reads a specifier and refuses the unit",
					d.Label, line)
			}
		}
	}
}
