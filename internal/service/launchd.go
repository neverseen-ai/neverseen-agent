package service

import (
	"fmt"
	"strings"
)

// renderLaunchd produces the two launchd agents.
//
// The label and the path are unchanged from what install.sh wrote before this
// package existed, deliberately: somebody has these loaded right now, and a
// definition under another label is a second job rather than the same one moved.
//
// The program is the binary itself, not a shell sourcing ~/.neverseen/.env first.
// That `.` failed outright when the file was absent — `neverseen service install` run
// without install.sh — and KeepAlive turned the failure into a respawn every ten
// seconds. The agent and the icon read that file themselves (proxy.LoadConfigFile),
// so the sourcing only ever added a way to fail.
func renderLaunchd(l Layout) []Definition {
	dir := l.join(l.Home, "Library", "LaunchAgents")

	return []Definition{
		{
			Job:   JobAgent,
			Label: AgentLabel,
			Path:  l.join(dir, AgentLabel+".plist"),
			Content: plist(l, AgentLabel,
				[]string{l.Binary(JobAgent), "proxy"},
				// KeepAlive, because nobody is meant to be able to stop the masking
				// by accident.
				"  <key>KeepAlive</key><true/>\n"),
		},
		{
			Job:   JobTray,
			Label: TrayLabel,
			Path:  l.join(dir, TrayLabel+".plist"),
			Content: plist(l, TrayLabel,
				[]string{l.Binary(JobTray)},
				`  <!-- No KeepAlive, unlike the agent's. The icon's own menu offers "Quit the
       icon", and launchd would put it straight back — the person would click it
       and watch nothing happen. The agent keeps KeepAlive because nobody is meant
       to be able to stop the masking by accident; the icon is only a window onto
       it, and closing a window has to work. It returns at the next login. -->
`),
		},
	}
}

// plist assembles one launchd agent.
//
// extra carries whatever distinguishes the two jobs and arrives already indented and
// newline-terminated, so the template holds no conditional: a template that branches
// on which job it is rendering is a template where the two can quietly diverge in
// something other than the line that is supposed to differ.
func plist(l Layout, label string, args []string, extra string) string {
	var program strings.Builder
	for _, a := range args {
		program.WriteString("<string>" + escapeXML(a) + "</string>")
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    %s
  </array>
  <key>RunAtLoad</key><true/>
%s  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, escapeXML(label), program.String(), extra, escapeXML(l.LogFile), escapeXML(l.LogFile))
}
