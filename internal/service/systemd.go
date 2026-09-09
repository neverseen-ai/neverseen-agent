package service

import (
	"fmt"
	"strings"
)

// SystemdUnit is the unit name, which is also the file name.
const SystemdUnit = "neverseen.service"

// renderSystemd produces the systemd user unit, unchanged from what install.sh
// wrote before this package existed.
//
// A *user* unit, not a system one: the vault, ~/.neverseen and the session scoping
// all belong to one person, and a system unit would run this as root on behalf of
// whoever happened to be logged in.
//
// Only one definition comes back, and that is not an omission. There is no icon on
// Linux — the binary compiles, but whether a StatusNotifierItem is shown depends on
// the desktop, and GNOME needs an extension for it. An entry here would register a
// job that draws nothing on the most common desktop.
func renderSystemd(l Layout) []Definition {
	return []Definition{{
		Job:   JobAgent,
		Label: SystemdUnit,
		Path:  l.join(l.Home, ".config", "systemd", "user", SystemdUnit),
		// ExecStart is quoted, which is the one place this deviates from the heredoc
		// it replaces. systemd splits ExecStart on whitespace, so a --prefix or a home
		// directory containing a space produced a unit systemd refuses to start — and
		// the launchd side already went through quoteForShell for exactly this class.
		// EnvironmentFile takes the path as written to the end of the line and needs
		// no quoting; quoting it would make the quotes part of the filename.
		Content: fmt.Sprintf(`[Unit]
Description=Neverseen agent
After=network-online.target

[Service]
EnvironmentFile=%s
ExecStart="%s" proxy
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`, escapeSystemd(l.ConfigFile), escapeSystemd(l.Binary(JobAgent))),
	}}
}

// escapeSystemd renders a value safe to place on the right of a unit setting.
//
// systemd reads "%" as the start of a specifier, so a home directory or a --prefix
// carrying one produced a unit it refuses to parse — which reads to the person as an
// agent that simply never starts. The same class as the ampersand escapeXML was added
// for on the launchd side, and the same silent failure.
//
// Only "%", and only doubled. A path that carries none comes back unchanged, which is
// what keeps the units this package writes byte-for-byte the ones install.sh wrote for
// every path that already worked.
func escapeSystemd(s string) string { return strings.ReplaceAll(s, "%", "%%") }
