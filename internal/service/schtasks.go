package service

import "fmt"

// renderSchtasks produces the two Windows logon tasks.
//
// # Why a scheduled task and not a Windows service
//
// A service runs as SYSTEM or under a named account and needs an administrator to
// install. This agent is built around one person: their vault, their ~/.neverseen,
// their sessions. A logon task registered for the current user is the exact Windows
// spelling of a launchd agent or a systemd *user* unit, and it needs no
// administrator at all.
//
// # The two jobs differ in exactly one element
//
// RestartOnFailure on the agent and nothing on the icon, which is the same asymmetry
// KeepAlive draws on macOS and for the same reason.
//
// # Where the configuration comes from
//
// Nowhere in this file, and that is what forced a change elsewhere. launchd sources
// ~/.neverseen/.env in a shell and systemd reads it through EnvironmentFile; Task
// Scheduler has neither, and cmd cannot read a .env without an incantation that breaks
// on the first value carrying a space. So the agent reads that file itself, on every
// platform (proxy.LoadConfigFile) — which also closed a gap that was already open on
// the other two, where `neverseen proxy` run by hand and the same binary under the
// service read different configurations.
//
// TODO: nothing collects this agent's output on Windows. The plist and the unit both
// redirect it to ~/.neverseen/agent.log, and Task Scheduler cannot redirect. Wrapping
// the command in `cmd /c … >> log` would put back the console window --detach exists
// to remove, so the way out is a log destination the agent knows about itself. Until
// then `--logs` has nothing to follow on Windows, and a crash leaves no account of
// itself. Known ceiling; see plans/graphical-installation.md.
//
// TODO: no graceful shutdown either. The agent files a final heartbeat on SIGTERM,
// and Windows has no SIGTERM — `schtasks /End` terminates, so the bucket in progress
// is lost back to the last snapshot. snapshotInterval (30s) is what bounds that, and
// bounding it is what that interval exists for, but it is a real difference between
// platforms rather than an equivalent. Fixing it means listening for the console
// control events Windows does send.
func renderSchtasks(l Layout) []Definition {
	return []Definition{
		{
			Job:   JobAgent,
			Label: WindowsAgentTask,
			Path:  l.join(l.Home, ".neverseen", WindowsAgentTask+".xml"),
			Content: task(l, "Neverseen agent", l.Binary(JobAgent),
				// --detach hides the console this would otherwise show at every
				// logon. It is a flag the task passes and a person does not: run
				// interactively, `neverseen proxy` keeps its console, because the
				// alternative — building the CLI for the GUI subsystem — would have
				// `neverseen status` print nothing into the terminal it was run
				// from.
				"proxy --detach",
				`    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
`),
		},
		{
			Job:   JobTray,
			Label: WindowsTrayTask,
			Path:  l.join(l.Home, ".neverseen", WindowsTrayTask+".xml"),
			// No RestartOnFailure, for the reason the launchd tray agent has no
			// KeepAlive: "Quit the icon" has to work.
			Content: task(l, "Neverseen menu bar icon", l.Binary(JobTray), "", ""),
		},
	}
}

// task assembles one Task Scheduler definition.
//
// Rendered as UTF-8. schtasks wants the file it imports in UTF-16, and that
// conversion belongs to the writer: an encoding is how a file is stored rather than
// what it says, and a golden file no reviewer can read in a diff is a golden file
// nobody checks.
func task(l Layout, description, binary, arguments, extraSettings string) string {
	args := ""
	if arguments != "" {
		args = fmt.Sprintf("      <Arguments>%s</Arguments>\n", escapeXML(arguments))
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>%s</Description>
    <URI>\%s</URI>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
%s  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
%s    </Exec>
  </Actions>
</Task>
`, escapeXML(description), escapeXML(taskURI(binary, l)), extraSettings, escapeXML(binary), args)
}

// taskURI is the name the task is filed under, which Task Scheduler shows in its own
// list. It is derived from the job rather than passed, so the URI and the name Apply
// addresses cannot disagree.
func taskURI(binary string, l Layout) string {
	if binary == l.Binary(JobTray) {
		return WindowsTrayTask
	}
	return WindowsAgentTask
}
