// Package tray puts an icon in the menu bar saying whether the agent is masking.
//
// It exists because of a decision made elsewhere. The installer never exports a
// base URL unconditionally, so a stopped agent leaves every tool working and
// unmasked rather than broken — availability over enforcement, on purpose. The
// cost of that trade is that silence is indistinguishable from protection: nothing
// on the workstation says which of the two is happening. This is the missing half.
//
// # It is a client, and it holds nothing
//
// No detector, no vault, no provider table, no key, no configuration. It reads
// /healthz and paints. That is what makes it safe to run beside the agent rather
// than inside it, and it is why it can say the one thing an icon inside the proxy
// could never say: that the proxy is not there.
//
// # The logic is separate from the toolkit
//
// Everything that decides what to show is in this file and takes no part of
// fyne.io/systray. What the toolkit gets is one value, a display, through a view
// with one method. A menu bar cannot be asserted on in CI, so the part that can be
// is kept where a test can reach it — the alternative is a feature whose behaviour
// has never run anywhere but a person's screen.
package tray

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/proxy"
)

// pollEvery is how often the agent is asked.
//
// Five seconds because the request is one loopback GET against a handler that
// touches no state, and because an icon that took a minute to notice the agent
// had stopped would be worse than no icon: somebody would trust it.
const pollEvery = 5 * time.Second

// askTimeout is short for the same reason it is short in `cloakfleet env`: on the
// loopback interface the answer takes a millisecond or it is not coming, and a
// menu bar that hung on a wedged socket would look like a wedged desktop.
const askTimeout = 2 * time.Second

// lineCount is how many informational lines the menu carries.
//
// Fixed, because a menu bar item is built once and its entries are then updated in
// place — the toolkit has no notion of a list that grows. render always returns
// this many, so the entries and the lines stay one to one.
const lineCount = 5

// maxProviderEntries bounds the "copy the command" submenu.
//
// A bound is needed because the provider list is open: ParseProviders adds a code
// it does not recognise rather than refusing it, so a deployment pointing several
// gateways at the agent can serve more than the eight built in. Twelve leaves room
// for that without building a menu nobody can read.
//
// Past it the extra providers are named as a count rather than dropped quietly. A
// menu that silently omitted two of them would have somebody conclude the agent
// does not serve them.
const maxProviderEntries = 12

// display is everything the menu bar shows at one moment.
//
// One value rather than four calls, so watch can tell whether anything changed by
// comparing two of them — and so a fifth thing to show cannot be added without the
// comparison being updated with it.
type display struct {
	icon      []byte
	tooltip   string
	lines     []string
	providers []string
}

// view is what the menu bar can be told. The real one wraps fyne.io/systray; a
// test uses its own and asserts on what it was told.
type view interface {
	show(d display)
}

// render turns a status into what the menu bar shows.
//
// The icon follows Masking and nothing else, so the picture and the exit code of
// `cloakfleet status` cannot disagree. Being up is not enough to earn the masking
// icon: an agent with no locale selected is healthy and recognises almost nothing,
// and an icon that called that protected would be the icon somebody trusted while
// their traffic went out in clear.
func render(s proxy.Status) display {
	verdict := "Not masking — traffic is leaving in clear"
	icon := unmaskedIcon
	if s.Masking() {
		verdict = "Masking"
		icon = maskingIcon
	}

	var lines []string

	switch {
	case !s.Answering:
		lines = []string{
			verdict,
			"The agent is not answering on " + s.Addr,
			"Your tools are reaching their provider directly",
			"Start it with: cloakfleet proxy",
			"",
		}
	case len(s.Locales) == 0:
		lines = []string{
			verdict,
			"The agent is answering on " + s.Addr,
			"No country pattern set is loaded",
			"Only identifiers and credentials are recognised",
			"Version " + or(s.Version, "unknown"),
		}
	default:
		lines = []string{
			verdict,
			"On " + s.Addr,
			"Locales: " + strings.Join(s.Locales, ", "),
			"Substitution: " + or(s.Substitution, "unknown"),
			"Version " + or(s.Version, "unknown"),
		}
	}

	return display{
		icon: icon,
		// The tooltip is the one thing read without clicking, so it carries the
		// verdict and where it applies, and nothing else.
		tooltip: fmt.Sprintf("cloakfleet — %s (%s)", strings.ToLower(verdict), s.Addr),
		lines:   lines,
		// Only what the agent says it is serving. A hard-coded list would go on
		// offering a provider a deployment had pointed elsewhere, and an agent that
		// is not answering serves nothing — the submenu empties with it rather than
		// handing out lines that lead nowhere.
		providers: s.Providers,
	}
}

// watch applies what ask reports, until the context is done.
//
// It applies only when something changed, because a menu bar told the same thing
// every five seconds is a menu bar redrawing itself for the life of the session —
// and on macOS every one of those calls crosses into the main thread.
//
// ask is passed in rather than called directly so the loop can be driven in a test
// without a listening agent.
func watch(ctx context.Context, v view, ask func() proxy.Status, every time.Duration) {
	var shown display

	apply := func() {
		next := render(ask())
		if same(shown, next) {
			return
		}
		shown = next
		v.show(next)
	}

	// Once before the first wait, or the menu bar would show whatever it was built
	// with for the first interval.
	apply()

	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			apply()
		}
	}
}

// same reports whether two displays would put the same thing on screen.
//
// The icons are compared by content, not by pointer. Comparing &a.icon[0] would
// have been cheaper and it panics on the first call: the display watch starts from
// is the zero value, and indexing a nil slice is not a comparison. Three hundred
// bytes every five seconds is not a cost worth being clever about.
func same(a, b display) bool {
	return slices.Equal(a.icon, b.icon) &&
		a.tooltip == b.tooltip &&
		slices.Equal(a.lines, b.lines) &&
		slices.Equal(a.providers, b.providers)
}

func or(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

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

// ask reports what the agent says about itself right now.
func ask(addr string) proxy.Status {
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	defer cancel()
	return proxy.Query(ctx, addr, askTimeout)
}
