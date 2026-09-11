// Command neverseen-tray shows in the menu bar whether the agent is masking.
//
// # Why this is a second binary, when the rule says one entrypoint
//
// That rule is in CLAUDE.md and it is a good one, but it is aimed at a specific
// failure: Agent Veil shipped two entrypoints that each *assembled the masking
// pipeline* from the same packages, and they drifted until the same request was
// masked by one and answered in clear by the other.
//
// This assembles nothing. It has no detector, no vault, no provider table, no key
// and no configuration; it reads the agent's /healthz and paints an icon. There is
// nothing here that could come to disagree with the agent about what masking means,
// because nothing here has an opinion about it.
//
// What made it worth a second binary is what happens when it is not one. The menu
// bar is Cocoa, so this needs cgo — and cgo is a property of a whole binary, not of
// a subcommand. Built into the agent, `neverseen` links AppKit, stops building
// with CGO_ENABLED=0, and runs a GUI toolkit's package initialiser in every proxy
// process that will never draw anything. Worse, the two then ship together: a
// broken Cocoa build means no release of the masking agent at all. That is the same
// rule the telemetry follows at runtime — the thing that watches the control must
// never be able to stop it — applied to the build.
//
// It is also a separate *process* for a reason that has nothing to do with cgo: an
// icon living inside the proxy vanishes at the exact moment it becomes useful. The
// state worth showing is that the agent is not there, and only something that
// outlasts it can show that.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/neverseen-ai/neverseen-agent/internal/proxy"
	"github.com/neverseen-ai/neverseen-agent/internal/tray"
)

// version is stamped at build time with -ldflags "-X main.version=…".
//
// A var, not a const: declared const, the linker flag is silently inert and every
// release reports the same string.
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			// The only argument it takes. There is nothing to configure: where the
			// agent listens is the agent's own setting, read from the environment
			// like everywhere else, and a flag here would be a second answer to it.
			if _, err := os.Stdout.WriteString(version + "\n"); err != nil {
				os.Exit(1)
			}
			return
		default:
			_, _ = os.Stderr.WriteString("usage: neverseen-tray [version]\n")
			os.Exit(1)
		}
	}

	// The operator's configuration, before the agent's address is asked for. The
	// agent reads ~/.neverseen/.env itself, and this has to as well: the Windows
	// logon task sources nothing, so a NEVERSEEN_LISTEN set only in that file had
	// the icon polling the default address for an agent that was never on it.
	// Not `cmd/` reading a setting, for the reason cmd/neverseen gives: it names no
	// variable and asks for no value.
	if err := proxy.LoadConfigFile(""); err != nil {
		_, _ = os.Stderr.WriteString("neverseen-tray: " + err.Error() + "\n")
		os.Exit(1)
	}

	// Cancelled on the way out, so the icon leaves the bar when the session ends
	// rather than sitting there dead until the user logs out.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// On this goroutine, and it has to be: systray owns the platform event loop,
	// and on macOS that loop must be the main thread — AppKit refuses to be driven
	// from anywhere else. It blocks until the context is done or the icon's own
	// Quit is chosen.
	tray.Run(ctx, proxy.ListenAddress())
}
