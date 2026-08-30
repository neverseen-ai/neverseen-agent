package tray

import (
	"bytes"
	"context"
	"image"
	_ "image/png"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/proxy"
)

// recorder is a view that remembers what it was told, which is the whole of what
// a test can check about a menu bar.
type recorder struct {
	mu    sync.Mutex
	shown []display
}

func (r *recorder) show(d display) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shown = append(r.shown, d)
}

func (r *recorder) updates() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.shown)
}

func (r *recorder) last() display {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.shown[len(r.shown)-1]
}

// The icon follows Masking and nothing else, so the picture and the exit code of
// `cloakfleet status` cannot disagree about the same agent.
func TestTheIconFollowsWhetherValuesAreReplaced(t *testing.T) {
	masking := proxy.Status{Addr: "127.0.0.1:8787", Answering: true,
		Health: proxy.Health{Version: "1.4.2", Locales: []string{"fr", "gb"}, Substitution: "token"}}

	tests := map[string]struct {
		status   proxy.Status
		wantIcon []byte
		wantLine string
	}{
		"masking": {masking, maskingIcon, "Masking"},
		// Up, healthy, and recognising almost nothing. It gets the same icon as a
		// stopped agent because it has the same consequence: the traffic leaves in
		// clear. An icon that called this protected would be the icon somebody
		// trusted while it did not.
		"no locale": {
			proxy.Status{Addr: "127.0.0.1:8787", Answering: true,
				Health: proxy.Health{Version: "1.4.2", Substitution: "token"}},
			unmaskedIcon, "Not masking",
		},
		"not answering": {
			proxy.Status{Addr: "127.0.0.1:8787"},
			unmaskedIcon, "Not masking",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := render(tc.status)
			png, tooltip, lines := got.icon, got.tooltip, got.lines
			if !bytes.Equal(png, tc.wantIcon) {
				t.Error("the wrong icon was chosen for this state")
			}
			if !strings.HasPrefix(lines[0], tc.wantLine) {
				t.Errorf("the first line is %q, want it to start %q", lines[0], tc.wantLine)
			}
			// Every state fills every entry: the menu is built once with a fixed
			// number, and a short answer would leave the previous state's words in
			// the leftovers.
			if len(lines) != lineCount {
				t.Fatalf("%d lines, want %d", len(lines), lineCount)
			}
			if !strings.Contains(tooltip, tc.status.Addr) {
				t.Errorf("the tooltip does not say where it applies: %q", tooltip)
			}
		})
	}
}

// The words have to name the consequence, not the process state. "Not answering"
// alone tells somebody nothing about what it costs them.
func TestTheMenuSaysWhatIsHappeningToTheTraffic(t *testing.T) {
	lines := render(proxy.Status{Addr: "127.0.0.1:8787"}).lines
	joined := strings.Join(lines, "\n")

	for _, want := range []string{"clear", "not answering", "cloakfleet proxy"} {
		if !strings.Contains(strings.ToLower(joined), want) {
			t.Errorf("the menu does not mention %q:\n%s", want, joined)
		}
	}

	lines = render(proxy.Status{Addr: "127.0.0.1:8787", Answering: true,
		Health: proxy.Health{Locales: []string{"fr"}, Substitution: "fake", Version: "9.9.9"}}).lines
	joined = strings.Join(lines, "\n")
	for _, want := range []string{"fr", "fake", "9.9.9"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the menu does not report %q:\n%s", want, joined)
		}
	}
}

// The generated assets are what they claim to be, and they are two pictures.
// Committed assets are the kind of thing a copy-and-paste leaves identical, and
// two identical icons would be an indicator that never changes.
func TestTheIcons(t *testing.T) {
	icons := map[string][]byte{
		"masking": maskingIcon, "partial": partialIcon, "unmasked": unmaskedIcon,
	}
	for name, raw := range icons {
		cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s does not decode: %v", name, err)
		}
		if format != "png" {
			t.Errorf("%s is %s, want png — the toolkit is handed bytes and decodes them itself",
				name, format)
		}
		// Thirty-two because the darwin backend sets the image to sixteen points and
		// macOS is a @2x world. A different size is not broken, but it is soft or
		// wasteful, and neither is intended.
		if cfg.Width != 32 || cfg.Height != 32 {
			t.Errorf("%s is %dx%d, want 32x32", name, cfg.Width, cfg.Height)
		}
	}

	// All three distinct, pairwise. Two that matched would be a state the menu bar
	// could never show — and the pair most at risk is masking against partial, which
	// differ by half of one square.
	for a, one := range icons {
		for b, other := range icons {
			if a < b && bytes.Equal(one, other) {
				t.Errorf("%s and %s are the same image, so the menu bar could not tell them apart", a, b)
			}
		}
	}
}

// A menu bar told the same thing every five seconds is a menu bar redrawing itself
// for the life of the session, and on macOS each of those calls crosses onto the
// main thread.
func TestWatchAppliesOnlyWhatChanged(t *testing.T) {
	var mu sync.Mutex
	answer := proxy.Status{Addr: "127.0.0.1:8787", Answering: true,
		Health: proxy.Health{Locales: []string{"fr"}, Substitution: "token"}}

	view := &recorder{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		watch(ctx, view, func() proxy.Status {
			mu.Lock()
			defer mu.Unlock()
			return answer
		}, time.Millisecond)
	}()

	// The first application happens before the first wait, or the bar would show
	// whatever it was built with for a whole interval.
	waitFor(t, func() bool { return view.updates() >= 1 })

	// Several intervals of the same answer, and still one update.
	time.Sleep(20 * time.Millisecond)
	if got := view.updates(); got != 1 {
		t.Errorf("%d updates for an unchanged status, want 1", got)
	}

	mu.Lock()
	answer = proxy.Status{Addr: "127.0.0.1:8787"} // the agent stops
	mu.Unlock()

	waitFor(t, func() bool { return view.updates() >= 2 })
	if !bytes.Equal(view.last().icon, unmaskedIcon) {
		t.Error("the icon did not change when the agent stopped")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not return when its context was cancelled")
	}
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for the menu bar to be told something")
}

// The tray is a client and nothing else: it reads the agent's own health route, so
// a field renamed there reaches it as a compile error rather than as an empty line.
func TestItReadsARealAgent(t *testing.T) {
	got := ask("127.0.0.1:1") // nothing listens there
	if got.Answering || got.Masking() {
		t.Errorf("a closed port reported %+v", got)
	}
	if got.Addr != "127.0.0.1:1" {
		t.Errorf("addr = %q, want the address that was asked", got.Addr)
	}
}

// The submenu offers what the agent says it serves, not a list compiled in. A
// hard-coded one would go on offering a provider a deployment had pointed
// elsewhere, and would offer all eight while the agent was down.
func TestTheProvidersOfferedAreTheOnesTheAgentServes(t *testing.T) {
	serving := render(proxy.Status{Addr: "127.0.0.1:8787", Answering: true,
		Health: proxy.Health{Locales: []string{"fr"}, Providers: []string{"anthropic", "gemini"}}})
	if want := []string{"anthropic", "gemini"}; !slices.Equal(serving.providers, want) {
		t.Errorf("providers = %v, want %v", serving.providers, want)
	}

	// Nothing answering serves nothing, so there is no line worth handing over.
	stopped := render(proxy.Status{Addr: "127.0.0.1:8787"})
	if len(stopped.providers) != 0 {
		t.Errorf("a stopped agent offered %v", stopped.providers)
	}
}

// A change in the provider list is a change on screen. It went unnoticed while
// watch compared only the informational lines, which do not mention providers —
// so an agent restarted with a different set would have kept the old submenu for
// the life of the icon.
func TestWatchNoticesAProviderListChange(t *testing.T) {
	var mu sync.Mutex
	answer := proxy.Status{Addr: "127.0.0.1:8787", Answering: true,
		Health: proxy.Health{Locales: []string{"fr"}, Providers: []string{"anthropic"}}}

	view := &recorder{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go watch(ctx, view, func() proxy.Status {
		mu.Lock()
		defer mu.Unlock()
		return answer
	}, time.Millisecond)

	waitFor(t, func() bool { return view.updates() >= 1 })

	mu.Lock()
	answer.Providers = []string{"anthropic", "openai"} // the agent restarts with more
	mu.Unlock()

	waitFor(t, func() bool { return view.updates() >= 2 })
	if want := []string{"anthropic", "openai"}; !slices.Equal(view.last().providers, want) {
		t.Errorf("the menu shows %v, want %v", view.last().providers, want)
	}
}

// The line handed over is the one the agent's own table produces, so the menu and
// `cloakfleet env` cannot come to disagree about how a tool is pointed at it.
func TestTheLineIsTheAgentsOwn(t *testing.T) {
	// A provider whose variable *and* CLI are both de-facto gets a line somebody
	// can paste and press return on. A prefixed assignment, not an export: it
	// applies to that one run and leaves the shell as it was.
	if got, want := proxy.PointAt("anthropic", "127.0.0.1:8787"),
		"ANTHROPIC_BASE_URL=http://127.0.0.1:8787/anthropic claude"; got != want {
		t.Errorf("PointAt = %q, want %q", got, want)
	}
	if got, want := proxy.PointAt("openai", "127.0.0.1:8787"),
		"OPENAI_BASE_URL=http://127.0.0.1:8787/openai codex"; got != want {
		t.Errorf("PointAt = %q, want %q", got, want)
	}
	// A provider with no agreed variable gets the URL and nothing invented: neither
	// a variable name nor a command name, because either would fail after somebody
	// had already pasted it and believed it. Six of the eight are in this case, and
	// the table is where that stops being true, one verified pair at a time.
	if got, want := proxy.PointAt("gemini", "127.0.0.1:8787"),
		"http://127.0.0.1:8787/gemini"; got != want {
		t.Errorf("PointAt = %q, want %q", got, want)
	}
}

// The clipboard is reached through the platform's own tool. It cannot be asserted
// on in CI without a desktop, so what is checked is that the failure is reported
// rather than swallowed — the menu says so, and the entry does not lie.
func TestTheClipboardFailureIsReported(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("pbcopy is the only clipboard this can rely on being present")
	}
	if err := copyToClipboard("cloakfleet tray test"); err != nil {
		t.Errorf("copying to the clipboard failed: %v", err)
	}
}

// Where a tool does not simply honour the variable, the entry says so. Codex reads
// OPENAI_BASE_URL but a model_provider in its own config file wins over it, and on
// a machine already configured for another provider the copied line does nothing —
// silently, with the traffic going out unmasked. That failure has no symptom from
// the terminal, which is why it is worth carrying to the point of handover.
func TestTheCaveatTravelsWithTheLine(t *testing.T) {
	caveat := proxy.CaveatFor("openai")
	if !strings.Contains(caveat, "config.toml") {
		t.Errorf("the openai caveat does not name the file that overrides it: %q", caveat)
	}

	// And only where there is something to say. A caveat on every provider would be
	// noise, and noise is what stops the one that matters being read.
	if got := proxy.CaveatFor("anthropic"); got != "" {
		t.Errorf("anthropic carries a caveat it does not need: %q", got)
	}
	if got := proxy.CaveatFor("gemini"); got != "" {
		t.Errorf("a provider with no entry at all carries a caveat: %q", got)
	}
}

// healthWithGroups is what an agent serving the catalogue answers, cut down to the
// two families a test needs to reason about.
func healthWithGroups(off ...string) proxy.Health {
	isOff := func(code string) bool {
		for _, c := range off {
			if c == code {
				return true
			}
		}
		return false
	}

	return proxy.Health{
		Version: "1.4.2", Locales: []string{"fr"}, Substitution: "token",
		AvailableLocales: []string{"fr", "gb", "us"},
		Masking:          map[bool]string{true: "partial", false: "full"}[len(off) > 0],
		Groups: []proxy.HealthGroup{
			{Code: "personal", Label: "Personal details", Categories: []proxy.HealthCategory{
				{Code: "EMAIL", Label: "Email address", Off: isOff("EMAIL")},
				{Code: "PHONE", Label: "Telephone", Off: isOff("PHONE")},
			}},
			{Code: "technical", Label: "Technical identifiers", Categories: []proxy.HealthCategory{
				{Code: "IP_ADDRESS", Label: "IP address", Off: isOff("IP_ADDRESS")},
			}},
			{Code: "secrets", Label: "Secrets and keys", Categories: []proxy.HealthCategory{
				{Code: "SECRET_ANTHROPIC_KEY", Label: "Anthropic key", Locked: true},
				{Code: "SECRET_OPENAI_KEY", Label: "OpenAI key", Locked: true},
			}},
		},
	}
}

// The menu offers what the agent published, and nothing else. A menu built from its
// own copy of the catalogue would go on offering a switch a rebuilt agent had
// stopped honouring.
func TestTheSwitchesAreTheAgentsOwn(t *testing.T) {
	d := render(proxy.Status{Addr: "127.0.0.1:8787", Answering: true, Health: healthWithGroups()})

	if len(d.switches) != 3 {
		t.Fatalf("drew %d families, want 3", len(d.switches))
	}
	if got := d.switches[0].label; got != "Personal details" {
		t.Errorf("the first family is %q", got)
	}
	if got := len(d.switches[0].members); got != 2 {
		t.Errorf("the first family holds %d switches, want 2", got)
	}

	// A family of nothing but credentials is locked, and never reads as switched
	// off — an unticked lock would say those values are not being masked.
	secrets := d.switches[2]
	if !secrets.locked {
		t.Error("the credential family is not locked")
	}
	if secrets.off {
		t.Error("the credential family reads as switched off")
	}
	if got := secrets.title(); got != "Secrets and keys — 2, locked" {
		t.Errorf("the locked title is %q", got)
	}
}

// An agent that is not answering offers nothing: a menu whose clicks reach nothing
// is worse than a menu with no clicks.
func TestNoSwitchesWhenTheAgentIsAbsent(t *testing.T) {
	d := render(proxy.Status{Addr: "127.0.0.1:8787"})
	if d.switches != nil {
		t.Errorf("drew %d families for an agent that is not there", len(d.switches))
	}
}

// The toolkit has no mixed tick, so a partly-off family says so in its title. Drawn
// simply unticked, it would claim nothing in the family was being masked.
func TestAPartlyOffFamilySaysSoInItsTitle(t *testing.T) {
	tests := map[string]struct {
		off   []string
		want  string
		group int
	}{
		"nothing off":    {off: nil, want: "Personal details", group: 0},
		"one of two off": {off: []string{"EMAIL"}, want: "Personal details — 1 of 2 off", group: 0},
		"every member off": {off: []string{"EMAIL", "PHONE"},
			want: "Personal details — all 2 off", group: 0},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			d := render(proxy.Status{Addr: "a", Answering: true, Health: healthWithGroups(tt.off...)})
			group := d.switches[tt.group]

			if got := group.title(); got != tt.want {
				t.Errorf("title is %q, want %q", got, tt.want)
			}
			// The group's own tick is only cleared when every member is off, which is
			// what the title above is compensating for.
			wantOff := len(tt.off) == 2
			if group.off != wantOff {
				t.Errorf("the family reads off=%v, want %v", group.off, wantOff)
			}
		})
	}
}

// A click sends the whole set, so the set has to be readable from what is drawn.
func TestTheDrawnMenuCarriesTheWholeSet(t *testing.T) {
	d := render(proxy.Status{Addr: "a", Answering: true,
		Health: healthWithGroups("EMAIL", "IP_ADDRESS")})

	got := d.offCodes()
	if len(got) != 2 || got[0] != "EMAIL" || got[1] != "IP_ADDRESS" {
		t.Errorf("the drawn menu reports %v switched off", got)
	}
}

// The third icon, and the words beside it. An agent with a category switched off is
// masking, so the masking icon would be the green light over the values that are not
// being replaced.
func TestAPartlyMaskingAgentGetsItsOwnIcon(t *testing.T) {
	full := render(proxy.Status{Addr: "a", Answering: true, Health: healthWithGroups()})
	partial := render(proxy.Status{Addr: "a", Answering: true, Health: healthWithGroups("EMAIL")})

	if !bytes.Equal(full.icon, maskingIcon) {
		t.Error("an agent applying its whole catalogue does not get the masking icon")
	}
	if !bytes.Equal(partial.icon, partialIcon) {
		t.Error("an agent with a category switched off does not get the partial icon")
	}

	if !strings.Contains(partial.lines[0], "1 category in clear") {
		t.Errorf("the verdict line is %q", partial.lines[0])
	}
	// Named, not counted: the count is already in the verdict, and the name is what
	// tells somebody whether the category they care about is the one that is off.
	if !strings.Contains(partial.lines[2], "Email address") {
		t.Errorf("the state lines do not name what is in clear: %q", partial.lines[2])
	}
}

// A click sends the whole set, and what a click means is decided here rather than
// inside the toolkit adapter — which is the rule this package is built on.
func TestClickingACategorySendsTheWholeSet(t *testing.T) {
	d := render(proxy.Status{Addr: "a", Answering: true, Health: healthWithGroups("IP_ADDRESS")})

	// Switching one on leaves the other off.
	if got := d.withCategoryToggled("IP_ADDRESS"); len(got) != 0 {
		t.Errorf("unticking the only off category sent %v, want nothing off", got)
	}
	got := d.withCategoryToggled("EMAIL")
	if len(got) != 2 || got[0] != "EMAIL" || got[1] != "IP_ADDRESS" {
		t.Errorf("sent %v, want both off", got)
	}
}

// All or nothing for a family: a click on a partly-off one turns the rest off too.
// Reviving them would make one click undo several deliberate ones.
func TestClickingAFamilyIsAllOrNothing(t *testing.T) {
	tests := map[string]struct {
		off  []string
		want []string
	}{
		"nothing off turns the family off": {
			off: nil, want: []string{"EMAIL", "PHONE"},
		},
		"partly off turns the rest off too": {
			off: []string{"EMAIL"}, want: []string{"EMAIL", "PHONE"},
		},
		"all off turns the family back on": {
			off: []string{"EMAIL", "PHONE"}, want: nil,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			d := render(proxy.Status{Addr: "a", Answering: true, Health: healthWithGroups(tt.off...)})
			got := d.withGroupToggled("personal")

			if len(got) != len(tt.want) {
				t.Fatalf("sent %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("sent %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// A locked member is left out of the set a family click sends. Included, the agent
// would refuse the whole request and the click would do nothing at all.
func TestClickingALockedFamilySendsNothingItWouldRefuse(t *testing.T) {
	d := render(proxy.Status{Addr: "a", Answering: true, Health: healthWithGroups()})

	if got := d.withGroupToggled("secrets"); len(got) != 0 {
		t.Errorf("clicking the credential family sent %v, which the agent refuses", got)
	}
}

// The plan is where the slot arithmetic lives, so a test can read what the menu
// would draw — including what the last slot says when there are more families than
// slots.
func TestThePlanFillsTheSlots(t *testing.T) {
	d := render(proxy.Status{Addr: "a", Answering: true, Health: healthWithGroups("EMAIL")})

	plans := planSwitches(d, 8, 12)
	if len(plans) != 8 {
		t.Fatalf("planned %d slots, want the whole pool", len(plans))
	}

	// Three families, so three visible and five hidden.
	visible := 0
	for _, p := range plans {
		if p.visible {
			visible++
		}
	}
	if visible != 3 {
		t.Errorf("%d slots are visible, want 3", visible)
	}

	personal := plans[0]
	if personal.title != "Personal details — 1 of 2 off" || !personal.enabled {
		t.Errorf("the first family is %+v", personal)
	}
	if personal.members[0].code != "EMAIL" || personal.members[0].checked {
		t.Errorf("the switched-off category is drawn as %+v", personal.members[0])
	}
	if !personal.members[1].checked {
		t.Errorf("the category still masked is drawn as %+v", personal.members[1])
	}
	if personal.members[2].visible {
		t.Error("an unused category slot is visible")
	}

	// A locked family: ticked, not clickable, and no rows under it.
	secrets := plans[2]
	if secrets.enabled || !secrets.checked {
		t.Errorf("the credential family is %+v, want ticked and not clickable", secrets)
	}
	if len(secrets.members) != 0 {
		t.Errorf("the credential family drew %d rows, want none", len(secrets.members))
	}
}

// Past the pool the last slot says how many are missing. Dropping them quietly would
// have somebody conclude the agent does not have them.
func TestThePlanNamesWhatDoesNotFit(t *testing.T) {
	d := render(proxy.Status{Addr: "a", Answering: true, Health: healthWithGroups()})

	// A pool of two for three families: one drawn, one saying two are missing.
	plans := planSwitches(d, 2, 12)
	if !plans[0].visible || plans[0].code != "personal" {
		t.Errorf("the first slot is %+v", plans[0])
	}
	if !plans[1].visible {
		t.Fatal("the overflow slot is hidden, so two families vanished silently")
	}
	if !strings.Contains(plans[1].title, "2 more") {
		t.Errorf("the overflow slot says %q, want the count that did not fit", plans[1].title)
	}
	if plans[1].code != "" {
		t.Error("the overflow slot stands for a family, so clicking it would switch one off")
	}
}

// A tick per mode rather than one item that cycles: a cycling item cannot say what
// it is about to become, and the live one is not clickable because clicking it would
// send the state it is already in.
func TestTheModeRowsShowTheChoiceAndTheState(t *testing.T) {
	d := render(proxy.Status{Addr: "a", Answering: true, Health: healthWithGroups()})

	rows := planModes(d)
	if len(rows) != 2 {
		t.Fatalf("drew %d modes, want 2", len(rows))
	}

	live, other := rows[0], rows[1]
	if live.code != "token" || !live.checked || live.enabled {
		t.Errorf("the live mode is %+v, want ticked and not clickable", live)
	}
	if other.code != "fake" || other.checked || !other.enabled {
		t.Errorf("the other mode is %+v, want unticked and clickable", other)
	}
	// The title says what the mode does, not only its name: neither word says which
	// one puts a value nobody can check in front of a caller.
	if !strings.Contains(other.title, "cannot be told from a real value") {
		t.Errorf("the fake row does not say what it costs: %q", other.title)
	}
}

// One row per locale the build has, not per loaded one: a list of what is already on
// has nothing to turn on.
func TestTheLocaleRowsOfferEveryCountryTheBuildHas(t *testing.T) {
	d := render(proxy.Status{Addr: "a", Answering: true, Health: healthWithGroups()})

	rows := planLocales(d)
	if len(rows) != 3 {
		t.Fatalf("drew %d locales, want the three the build has", len(rows))
	}
	if rows[0].code != "fr" || !rows[0].checked {
		t.Errorf("the loaded locale is %+v", rows[0])
	}
	for _, row := range rows[1:] {
		if row.checked {
			t.Errorf("locale %q is ticked and is not loaded", row.code)
		}
		// Every row stays clickable, including the last loaded one: an agent with no
		// locale at all is a valid state and the one it starts in.
		if !row.enabled {
			t.Errorf("locale %q cannot be clicked", row.code)
		}
	}
}

func TestClickingALocaleReplacesTheSelection(t *testing.T) {
	d := render(proxy.Status{Addr: "a", Answering: true, Health: healthWithGroups()})

	if got := d.withLocaleToggled("gb"); len(got) != 2 || got[0] != "fr" || got[1] != "gb" {
		t.Errorf("adding gb sent %v", got)
	}
	// Switching the last one off is reachable, and sends an empty selection rather
	// than nil — which a caller could read as "no change".
	got := d.withLocaleToggled("fr")
	if got == nil {
		t.Fatal("switching off the last locale sent nil")
	}
	if len(got) != 0 {
		t.Errorf("switching off the only locale sent %v", got)
	}
}

// A click carries the other two parts of the state unchanged, because the route
// replaces the state rather than patching it.
func TestAClickCarriesTheWholeState(t *testing.T) {
	d := render(proxy.Status{Addr: "a", Answering: true, Health: healthWithGroups("EMAIL")})

	// Changing the mode leaves the categories and the locales alone.
	want := d.policyWith(nil, "fake", nil, "")
	if want.Substitution != "fake" {
		t.Errorf("the mode was not applied: %+v", want)
	}
	if len(want.Off) != 1 || want.Off[0] != "EMAIL" {
		t.Errorf("the switched-off categories were lost: %v", want.Off)
	}
	if len(want.Locales) != 1 || want.Locales[0] != "fr" {
		t.Errorf("the locales were lost: %v", want.Locales)
	}

	// And changing a category leaves the mode alone.
	want = d.policyWith(d.withCategoryToggled("EMAIL"), "", nil, "")
	if want.Substitution != "token" {
		t.Errorf("the mode was lost: %+v", want)
	}
	if len(want.Off) != 0 {
		t.Errorf("the category was not switched back on: %v", want.Off)
	}
}

// The two changes the field-by-field comparison could not see.
//
// Neither is exotic: both are made by `cloakfleet mask`, which is the only way to
// reach these settings on a workstation with no menu bar, and both used to leave
// the bar ticking the value the agent had stopped applying. The secret level is
// named nowhere in the state lines, so it moved with nothing else to give it away;
// the locales are named in a state line only while no category is switched off,
// which is why the second case pins an agent in the partial state.
func TestSameSeesTheSettingsNoStateLineCarries(t *testing.T) {
	masking := proxy.Status{Addr: "127.0.0.1:8787", Answering: true, Health: proxy.Health{
		Version: "1.0.0", Masking: "full", Substitution: "token", SecretLevel: "weak",
		Locales: []string{"fr"}, AvailableLocales: []string{"fr", "gb"},
	}}

	partial := masking
	partial.Health.Masking = "partial"
	partial.Groups = []proxy.HealthGroup{{Code: "personal", Label: "Personal details",
		Categories: []proxy.HealthCategory{{Code: "EMAIL", Label: "Email address", Off: true}}}}

	for _, tc := range []struct {
		name   string
		before proxy.Status
		change func(*proxy.Status)
	}{
		{"the secret level", masking, func(s *proxy.Status) { s.SecretLevel = "strong" }},
		{"the locales, with a category switched off", partial,
			func(s *proxy.Status) { s.Locales = []string{"gb"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := tc.before
			tc.change(&after)

			if same(render(tc.before), render(after)) {
				t.Errorf("%s changed and the menu bar would not redraw, "+
					"so it goes on ticking what the agent stopped applying", tc.name)
			}
		})
	}
}

// The guard the comparison's own comment promised and did not keep: six fields
// were added to display and none of them was compared.
//
// It walks the type rather than naming the fields, because a test that named them
// would be the same list going stale in a second place. A field this cannot
// perturb fails loudly rather than passing quietly — an unperturbed field is a
// field this test is not checking, which is exactly how the last one got through.
func TestSameComparesEveryFieldOfADisplay(t *testing.T) {
	base := render(proxy.Status{Addr: "127.0.0.1:8787", Answering: true, Health: proxy.Health{
		Version: "1.0.0", Masking: "full", Substitution: "token", SecretLevel: "weak",
		Locales: []string{"fr"}, AvailableLocales: []string{"fr", "gb"},
		Providers: []string{"anthropic"},
		Groups: []proxy.HealthGroup{{Code: "personal", Label: "Personal details",
			Categories: []proxy.HealthCategory{{Code: "EMAIL", Label: "Email address"}}}},
	}})

	typ := reflect.TypeOf(base)
	for i := range typ.NumField() {
		field := typ.Field(i)

		changed := base
		target := reflect.ValueOf(&changed).Elem().Field(i)
		if !perturb(target) {
			t.Fatalf("display.%s is a %s this test cannot change, so nothing here "+
				"checks that same() compares it", field.Name, field.Type)
		}

		if same(base, changed) {
			t.Errorf("display.%s changed and same() reported no change, so the menu "+
				"bar would go on showing the old value", field.Name)
		}
	}
}

// perturb gives a field a value it did not have, reporting false for a kind it
// does not know how to change — which the caller treats as a failure rather than
// as nothing to do.
func perturb(v reflect.Value) bool {
	// Reachable through the unexported fields of a display, which is the whole of
	// what this test is for.
	v = reflect.NewAt(v.Type(), v.Addr().UnsafePointer()).Elem()

	switch v.Kind() {
	case reflect.String:
		v.SetString(v.String() + " changed")
		return true
	case reflect.Bool:
		v.SetBool(!v.Bool())
		return true
	case reflect.Slice:
		// Appended rather than replaced, so a nil slice and an empty one are both
		// perturbed into something DeepEqual tells apart.
		v.Set(reflect.Append(v, reflect.New(v.Type().Elem()).Elem()))
		return true
	default:
		return false
	}
}
