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
// Everything that decides what to show takes no part of fyne.io/systray, and
// systray.go is the only file that does. What the toolkit gets is one value, a
// display, through a view with one method. A menu bar cannot be asserted on in CI,
// so the part that can be is kept where a test can reach it — the alternative is a
// feature whose behaviour has never run anywhere but a person's screen.
//
// That is what the files are: this one turns a status into a display, desktop.go is
// the two things asked of the platform, and systray.go applies the results.
//
// # The menu says, the page configures
//
// It used to do both, and the second half never fitted. fyne.io/systray gives titles
// and ticks: no radio group, no mixed tick, no room for the sentence that says what a
// choice costs — so every one of those absences had been answered by writing the
// sentence into an entry's own title, and a menu bar of sixty-character rows is a menu
// nobody reads. What is configured is configured on the agent's own /settings page,
// which has the room this never had. The menu is back to the one thing an icon in the
// bar can do that nothing else can: say, without being clicked, whether the traffic is
// being masked.
package tray

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/proxy"
)

// pollEvery is how often the agent is asked.
//
// Five seconds because the request is one loopback GET against a handler that
// touches no state, and because an icon that took a minute to notice the agent
// had stopped would be worse than no icon: somebody would trust it.
const pollEvery = 5 * time.Second

// askTimeout is short for the same reason it is short in `neverseen env`: on the
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

	// The three settings the menu no longer draws, kept because it still writes
	// once: "Mask everything again" replaces the whole state, and a request that
	// left these out would switch them off by omission.
	//
	// Compared like everything else, so a change made on the settings page or by
	// `neverseen mask` still redraws this — see same.
	substitution string
	secretLevel  string
	locales      []string
}

// view is what the menu bar can be told. The real one wraps fyne.io/systray; a
// test uses its own and asserts on what it was told.
type view interface {
	show(d display)
}

// render turns a status into what the menu bar shows.
//
// The icon follows Masking and nothing else, so the picture and the exit code of
// `neverseen status` cannot disagree. Being up is not enough to earn the masking
// icon: an agent with no locale selected is healthy and recognises almost nothing,
// and an icon that called that protected would be the icon somebody trusted while
// their traffic went out in clear.
func render(s proxy.Status) display {
	verdict := "Not masking — traffic is leaving in clear"
	icon := unmaskedIcon

	switch s.Level() {
	case detector.LevelFull:
		verdict = "Masking"
		icon = maskingIcon
	case detector.LevelPartial:
		// The third state, and the reason it exists: this agent is masking, so a
		// two-state icon would show the masking picture while the categories
		// somebody switched off went out in clear.
		off := s.SwitchedOff()
		verdict = fmt.Sprintf("Masking, with %d categor%s in clear", len(off), plural(len(off), "y", "ies"))
		icon = partialIcon
	}

	var lines []string

	switch {
	case !s.Answering:
		lines = []string{
			verdict,
			"The agent is not answering on " + s.Addr,
			"Your tools are reaching their provider directly",
			"Start it with: neverseen proxy",
			"",
		}
	case len(s.Locales) == 0:
		lines = []string{
			verdict,
			"The agent is answering on " + s.Addr,
			"No country pattern set is loaded",
			"Only identifiers and credentials are recognised",
			"Version " + orUnknown(s.Version),
		}
	case s.Level() == detector.LevelPartial:
		// Named rather than counted, because the count is already in the verdict
		// line above and a name is what tells somebody whether the category they
		// care about is one of them.
		lines = []string{
			verdict,
			"On " + s.Addr,
			"In clear: " + strings.Join(s.SwitchedOff(), ", "),
			"Substitution: " + orUnknown(s.Substitution),
			"Version " + orUnknown(s.Version),
		}

	default:
		lines = []string{
			verdict,
			"On " + s.Addr,
			"Locales: " + strings.Join(s.Locales, ", "),
			"Substitution: " + orUnknown(s.Substitution),
			"Version " + orUnknown(s.Version),
		}
	}

	return display{
		icon: icon,
		// The tooltip is the one thing read without clicking, so it carries the
		// verdict and where it applies, and nothing else.
		tooltip: fmt.Sprintf("neverseen — %s (%s)", strings.ToLower(verdict), s.Addr),
		lines:   lines,
		// Only what the agent says it is serving. A hard-coded list would go on
		// offering a provider a deployment had pointed elsewhere, and an agent that
		// is not answering serves nothing — the submenu empties with it rather than
		// handing out lines that lead nowhere.
		providers:    s.Providers,
		substitution: s.Substitution,
		secretLevel:  s.SecretLevel,
		locales:      s.Locales,
	}
}

// maskEverything is the state to send when "Mask everything again" is clicked.
//
// The whole state with the switched-off set emptied, because PUT /policy replaces
// rather than patches: a request carrying only the empty set would take the mode,
// the locales and the secret level down with it.
//
// The other three parts come from what the agent last reported, which is what this
// menu is currently drawing. It is the one write left here, and the only one that
// needs no reading of the catalogue at all — "everything" is the empty set whatever
// the catalogue holds.
func (d display) maskEverything() proxy.Policy {
	return proxy.Policy{
		Off:          []string{},
		Substitution: d.substitution,
		Locales:      d.locales,
		SecretLevel:  d.secretLevel,
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
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
// The whole value, rather than the four fields somebody thought of. The
// field-by-field version carried a promise that a fifth thing to show could not be
// added without this being updated with it, and six were added past it. None of them
// was compared, and what that cost is a menu that lies: no line of the menu carries
// the secret level, so `neverseen mask --secret-level strong` changed nothing this
// looked at and the menu went on writing the old level back in its next request;
// the locales are named in a state line only while no category is switched off, so a
// locale change on an agent in the partial state was invisible in the same way.
//
// The three settings the menu stopped drawing are still compared for that second
// reason. They are not on screen, but they are in every request "Mask everything
// again" sends, and a stale copy of them is a click that quietly reverts what the
// settings page just changed.
//
// A promise in a comment is not what keeps a comparison current — comparing the
// value is. DeepEqual reads the icons by content as well, which is the property
// the pointer comparison could never have: the display watch starts from the zero
// value, and indexing a nil slice is not a comparison. Three hundred bytes and a
// handful of short slices every five seconds is not a cost worth being clever
// about.
func same(a, b display) bool { return reflect.DeepEqual(a, b) }

// orUnknown is what a state line says about a field the agent did not fill in.
//
// One fallback rather than a parameter, because every caller here means the same
// thing by an empty value: the agent answered and this field was not in it. A menu
// line reading "Version " with nothing after it looks like a bug in the menu.
func orUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

// ask reports what the agent says about itself right now.
func ask(addr string) proxy.Status {
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	defer cancel()
	return proxy.Query(ctx, addr, askTimeout)
}
