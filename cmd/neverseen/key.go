package main

import (
	"fmt"
	"io"

	"github.com/neverseen-ai/neverseen-agent/internal/proxy"
)

// `neverseen key` prints the control secret, for pasting into the browser
// extension's options page.
//
// It reads the file the agent wrote, through proxy.ReadControlKey — the one reader,
// as the menu bar and `neverseen mask` already use. It never creates one: a key
// minted by a reader is a key the agent does not know, and the route would refuse it
// while looking authenticated.
//
// The terminal is the only place this key travels. That is the rule the extension's
// key delivery is built around and the trap to refuse when the next rung is built: an
// unauthenticated GET /key "to keep it simple" is exactly the hole DNS rebinding
// exploits — a page on the internet that resolves a name to 127.0.0.1 can read
// anything this agent serves without a header.
func runKey(stdout io.Writer) error {
	key := proxy.ReadControlKey("")
	if key == "" {
		// Not an error about a missing file, because the ordinary cause is not one:
		// the agent writes this key the first time it starts, so an empty answer here
		// almost always means it has never run.
		return fmt.Errorf("no control key in %s — start the agent once with `neverseen proxy`, "+
			"which writes it", proxy.DefaultControlKeyFile)
	}

	// The key alone on a line, so `neverseen key | pbcopy` puts exactly the key on
	// the clipboard. Anything explanatory here would be pasted into the options page
	// along with it.
	fmt.Fprintln(stdout, key)
	return nil
}
