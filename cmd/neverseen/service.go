package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/neverseen-ai/neverseen-agent/internal/service"
)

// runService registers this agent with whatever starts background jobs on this
// workstation, or takes it back off.
//
// # Why this is a subcommand and not a script
//
// The launchd plist, the systemd unit and the Windows scheduled task were heredocs in
// install.sh. Anything else that wanted to install this agent — a package, a disk
// image, a first-run pane in the menu bar icon — could only write a fourth copy of
// them, and the two that drifted would be the one that installed the agent and the one
// that restarted it. One owner of the service definition, the way shellTools owns how a
// tool is pointed at this agent and proxy.Query owns how its health is asked.
//
// # It is not a second entrypoint
//
// The bar CLAUDE.md sets for a new `main` is not being approached here: this is a
// subcommand of cmd/neverseen, it assembles no pipeline, holds no secret and reads no
// environment. What it needs that differs between installations arrives as --prefix.
//
// # Three verbs, not five
//
// install, uninstall and restart, because each of them *writes or removes a
// definition* — a restart is an unload and a load of the same one. `--status` and
// `--logs` stay in install.sh: they observe, they author nothing, and moving them here
// would grow this command without closing any drift.
func runService(args []string, stdout io.Writer) error {
	// The verb is taken before the flags are parsed, because Go's flag package
	// stops at the first argument that is not a flag: parsed the other way round,
	// `service install --prefix DIR` would leave the prefix unparsed and silently
	// register a service pointing at the wrong binaries.
	if len(args) == 0 {
		printServiceUsage(stdout)
		return fmt.Errorf("service takes one of install, uninstall, restart")
	}
	verb := args[0]

	fs := flag.NewFlagSet("service "+verb, flag.ContinueOnError)
	fs.SetOutput(stdout)
	prefix := fs.String("prefix", "",
		"where the binaries were installed; empty for ~/.local")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if extra := fs.Args(); len(extra) > 0 {
		printServiceUsage(stdout)
		return fmt.Errorf("service %s takes no arguments, got %q", verb, extra[0])
	}

	layout, err := service.DefaultLayout(*prefix)
	if err != nil {
		return err
	}

	switch verb {
	case "install":
		if err := service.Install(layout); err != nil {
			return err
		}
		// Named rather than implied. Somebody who has just installed a background
		// service should be able to see, without asking, what is now running under
		// their login and where to look when it is not.
		fmt.Fprintf(stdout, "Registered the agent to start at login.\n")
		fmt.Fprintf(stdout, "  configuration  %s\n", layout.ConfigFile)
		fmt.Fprintf(stdout, "  log            %s\n", layout.LogFile)
		return nil

	case "uninstall":
		if err := service.Uninstall(layout); err != nil {
			return err
		}
		// What is deliberately left behind is said here as well as in install.sh,
		// because this command can be reached without that script: the directory
		// holds the operator's configuration and the identity a supervision backend
		// knows this machine by, and deleting the identity silently would have the
		// next install enrol as a second agent and count twice.
		fmt.Fprintf(stdout, "Stopped it and removed the service.\n")
		fmt.Fprintf(stdout, "Left in place: %s\n", layout.ConfigFile)
		return nil

	case "restart":
		if err := service.Restart(layout); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Restarted.")
		return nil

	default:
		printServiceUsage(stdout)
		return fmt.Errorf("unknown service command %q", verb)
	}
}

func printServiceUsage(w io.Writer) {
	fmt.Fprint(w, `usage: neverseen service <command> [--prefix DIR]

The command comes first, then its flags.

  install     register the agent, and the menu bar icon if it is installed,
              to start at login, and start it now
  uninstall   stop it and remove the registration; leaves ~/.neverseen alone
  restart     reload the definition and restart, after editing the config

--prefix names where the binaries were installed. Empty means ~/.local, which
is where install.sh puts them.
`)
}
