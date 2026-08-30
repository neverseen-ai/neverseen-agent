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
// That is what the files are: this one turns a status into a display, plan.go says
// what each menu entry becomes and what a click on it means, desktop.go is the two
// things asked of the platform, and systray.go applies the results.
package tray

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/detector"
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

	// substitution is the live mode, and modes are every mode this build offers, so
	// the menu can draw a choice rather than a state.
	substitution string

	// secretLevel is the live level, and levels are every level this build offers.
	secretLevel  string
	secretLevels []string
	modes        []string

	// locales is one row per locale the build has, with whether it is loaded.
	locales []localeRow

	// switches is the catalogue as the menu draws it: every group in the order the
	// agent listed them, each with its categories. Built from what the agent
	// serves rather than from pkg/pii, so a menu cannot go on offering a switch a
	// rebuilt agent stopped honouring.
	switches []switchGroup
}

// localeRow is one country pattern set the build has, and whether it is loaded.
type localeRow struct {
	code string
	on   bool
}

// switchGroup is one family of categories in the menu.
type switchGroup struct {
	code  string
	label string

	// off is whether every switchable member is switched off, which is what the
	// group's own tick shows. The toolkit has no mixed state — Check and Uncheck
	// and nothing between — so a partly-off group says so in its title instead;
	// see title.
	off bool

	// locked is whether nothing in this group may be switched. A locked group is
	// one dim line rather than a submenu: twenty API keys nobody may switch off is
	// twenty rows of nothing to do.
	locked bool

	members []switchCategory
}

// switchCategory is one switch.
type switchCategory struct {
	code   string
	label  string
	off    bool
	locked bool
}

// title is what the group's menu entry says.
//
// The count of what is off lives here because fyne.io/systray offers Check and
// Uncheck and nothing between: a group with two of its nine categories off cannot
// show a third tick state, and a group drawn simply unticked would say "nothing
// here is masked" about seven categories that are.
func (g switchGroup) title() string {
	switch {
	case g.locked:
		return fmt.Sprintf("%s — %d, locked", g.label, len(g.members))
	case g.off:
		return fmt.Sprintf("%s — all %d off", g.label, len(g.members))
	}

	off := 0
	for _, m := range g.members {
		if m.off {
			off++
		}
	}
	if off > 0 {
		return fmt.Sprintf("%s — %d of %d off", g.label, off, len(g.members))
	}
	return g.label
}

// offCodes lists every category currently switched off, across every group.
//
// The whole set, because that is what the agent is sent: a toggle would be a
// read-modify-write, and two surfaces looking at one agent can interleave the two
// halves into a set neither asked for.
func (d display) offCodes() []string {
	var out []string
	for _, g := range d.switches {
		for _, m := range g.members {
			if m.off {
				out = append(out, m.code)
			}
		}
	}
	return out
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
			"Start it with: cloakfleet proxy",
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
		tooltip: fmt.Sprintf("cloakfleet — %s (%s)", strings.ToLower(verdict), s.Addr),
		lines:   lines,
		// Only what the agent says it is serving. A hard-coded list would go on
		// offering a provider a deployment had pointed elsewhere, and an agent that
		// is not answering serves nothing — the submenu empties with it rather than
		// handing out lines that lead nowhere.
		providers:    s.Providers,
		substitution: s.Substitution,
		modes:        proxy.SubstitutionModes(),
		secretLevel:  s.SecretLevel,
		secretLevels: proxy.SecretLevels(),
		locales:      localesOf(s),
		switches:     switchesOf(s),
	}
}

// localesOf is one row per locale the build has, ticked when it is loaded.
//
// Every locale the build has rather than only the loaded ones, because the menu is
// offering a choice: a list of what is already on has nothing to turn on. Drawn from
// what the agent published so the menu cannot suggest a locale the agent would
// refuse.
func localesOf(s proxy.Status) []localeRow {
	if !s.Answering {
		return nil
	}

	on := make(map[string]bool, len(s.Locales))
	for _, code := range s.Locales {
		on[code] = true
	}

	out := make([]localeRow, 0, len(s.AvailableLocales))
	for _, code := range s.AvailableLocales {
		out = append(out, localeRow{code: code, on: on[code]})
	}
	return out
}

// policyWith is the whole state to send, with one part replaced.
//
// Built from what the menu is currently drawing, which is what the agent last
// reported: the route replaces the state rather than patching it, so a click has to
// carry the other two parts unchanged.
func (d display) policyWith(off []string, substitution string, locales []string, level string) proxy.Policy {
	want := proxy.Policy{Off: d.offCodes(), Substitution: d.substitution, SecretLevel: d.secretLevel}
	for _, l := range d.locales {
		if l.on {
			want.Locales = append(want.Locales, l.code)
		}
	}

	if off != nil {
		want.Off = off
	}
	if substitution != "" {
		want.Substitution = substitution
	}
	if locales != nil {
		want.Locales = locales
	}
	if level != "" {
		want.SecretLevel = level
	}
	return want
}

// withLocaleToggled is the locale selection to send when one row is clicked.
func (d display) withLocaleToggled(code string) []string {
	var out []string
	for _, l := range d.locales {
		switch {
		case l.code == code && !l.on:
			out = append(out, l.code)
		case l.code == code:
			// Left out: this is the one being switched off.
		case l.on:
			out = append(out, l.code)
		}
	}
	// Never nil, so "no locale at all" is a selection the agent is actually sent
	// rather than a nil the caller might read as "no change".
	if out == nil {
		out = []string{}
	}
	return out
}

// switchesOf turns what the agent published into the rows the menu draws.
//
// A locked group keeps its members even though no row is drawn for them, because
// the count in its title is the honest thing to show: "Secrets and keys — 20,
// locked" says what is protected, where an empty line would look like a group the
// agent had stopped having.
func switchesOf(s proxy.Status) []switchGroup {
	if !s.Answering {
		// Nothing to offer about an agent that is not there, and offering it anyway
		// would be a menu whose clicks reach nothing.
		return nil
	}

	var out []switchGroup
	for _, g := range s.Groups {
		group := switchGroup{code: g.Code, label: g.Label, locked: true, off: true}

		for _, c := range g.Categories {
			group.members = append(group.members, switchCategory{
				code: c.Code, label: c.Label, off: c.Off, locked: c.Locked,
			})
			if !c.Locked {
				group.locked = false
				if !c.Off {
					group.off = false
				}
			}
		}

		// A group of nothing but locked categories is locked, and its "off" flag is
		// meaningless — cleared so a locked group never reads as switched off.
		if group.locked {
			group.off = false
		}
		out = append(out, group)
	}
	return out
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
// added without this being updated with it, and six were added past it: the
// substitution mode, the secret level, the two lists of choices, the locales and
// the switches. None of them was compared, and what that cost is a menu that lies.
// No line of the menu carries the secret level, so `cloakfleet mask
// --secret-level strong` changed nothing this looked at and the ticked row stayed
// on the old level until something else moved; the locales are named in a state
// line only while no category is switched off, so a locale change on an agent in
// the partial state was invisible in exactly the same way.
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
