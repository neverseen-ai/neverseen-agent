package tray

import (
	"os/exec"
	"runtime"
	"strings"
)

// The two things this package asks the desktop to do for it, rather than doing
// itself.
//
// Both go through the platform's own command instead of a library. There is one per
// desktop, they all read standard input or take a URL, and a dependency for that
// would be a dependency to maintain for six lines.

// openTestPage asks the desktop to open the agent's own test page.
//
// Started and forgotten: whether a browser opened is not something this can do
// anything about, and a menu bar item that blocked on a browser launching would
// freeze the bar.
func openTestPage(addr string) {
	url := "http://" + addr + "/test"

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// copyToClipboard puts text where the next paste will find it.
//
// Through the platform's own tool rather than a library: there is one command per
// desktop, they all read standard input, and a dependency for that would be a
// dependency to maintain for three lines. Failure is reported so the caller can
// say so — a menu entry that looks like it worked and did not is worse than one
// that admits it.
func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("clip")
	default:
		// Wayland first, because it is now the common case and wl-copy is absent on
		// X11-only systems while xclip is absent on many Wayland ones.
		tool := "xclip"
		args := []string{"-selection", "clipboard"}
		if found, err := exec.LookPath("wl-copy"); err == nil {
			tool, args = found, nil
		}
		cmd = exec.Command(tool, args...)
	}

	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
