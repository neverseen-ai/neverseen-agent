package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/proxy"
)

// `neverseen settings` prints a one-time link to the settings page.
//
// The page carries the control key only when it is opened with such a link, because a
// page typed by hand could be another account's on this machine. This is the way to it
// wherever there is no menu bar — a GNOME desktop without the extension, a server
// reached through an SSH tunnel.
//
// Printed rather than opened: a link handed to `open` or `xdg-open` sits for a moment
// on a command line other accounts can read, and nobody else reads this terminal.
func runSettings(stdout io.Writer) error {
	link, err := proxy.SettingsURL(context.Background(), proxy.ListenAddress(),
		proxy.ReadControlKey(""), 2*time.Second)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Open this within a minute; it works once:\n\n  %s\n", link)
	return nil
}
