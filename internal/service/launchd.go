package service

import "fmt"

// renderLaunchd produces the two launchd agents, unchanged from what install.sh
// wrote before this package existed.
//
// Byte-for-byte unchanged, deliberately: somebody has these loaded right now, and a
// definition that differs in anything but whitespace is a second definition of the
// same job rather than the same one moved.
func renderLaunchd(l Layout) []Definition {
	dir := l.join(l.Home, "Library", "LaunchAgents")

	return []Definition{
		{
			Job:   JobAgent,
			Label: AgentLabel,
			Path:  l.join(dir, AgentLabel+".plist"),
			Content: plist(l, AgentLabel,
				sourceAndRun(l.ConfigFile, l.Binary(JobAgent), "proxy"),
				// KeepAlive, because nobody is meant to be able to stop the masking
				// by accident.
				"  <key>KeepAlive</key><true/>\n"),
		},
		{
			Job:   JobTray,
			Label: TrayLabel,
			Path:  l.join(dir, TrayLabel+".plist"),
			Content: plist(l, TrayLabel,
				sourceAndRun(l.ConfigFile, l.Binary(JobTray)),
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
func plist(l Layout, label, command, extra string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string><string>-c</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key><true/>
%s  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, escapeXML(label), escapeXML(command), extra, escapeXML(l.LogFile), escapeXML(l.LogFile))
}
