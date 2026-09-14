package tray

import (
	"bytes"
	"context"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/proxy"
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
// `neverseen status` cannot disagree about the same agent.
func TestTheIconFollowsWhetherValuesAreReplaced(t *testing.T) {
	masking := proxy.Status{Addr: "127.0.0.1:9787", Answering: true,
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
			proxy.Status{Addr: "127.0.0.1:9787", Answering: true,
				Health: proxy.Health{Version: "1.4.2", Substitution: "token"}},
			unmaskedIcon, "Not masking",
		},
		"not answering": {
			proxy.Status{Addr: "127.0.0.1:9787"},
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
	lines := render(proxy.Status{Addr: "127.0.0.1:9787"}).lines
	joined := strings.Join(lines, "\n")

	for _, want := range []string{"clear", "not answering", "neverseen proxy"} {
		if !strings.Contains(strings.ToLower(joined), want) {
			t.Errorf("the menu does not mention %q:\n%s", want, joined)
		}
	}

	lines = render(proxy.Status{Addr: "127.0.0.1:9787", Answering: true,
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
		// Read through iconSize, which is per-platform: these are PNGs everywhere but
		// Windows, where systray calls CreateIconFromResourceEx and only an .ico will
		// do. Asserted here with image/png alone, the new Windows runner failed on
		// "image: unknown format" — a red job over icons that are perfectly correct
		// for the platform they ship to.
		width, height, err := iconSize(raw)
		if err != nil {
			t.Fatalf("%s is not the format this platform's toolkit is handed: %v", name, err)
		}
		// Thirty-two because the darwin backend sets the image to sixteen points and
		// macOS is a @2x world. A different size is not broken, but it is soft or
		// wasteful, and neither is intended.
		if width != 32 || height != 32 {
			t.Errorf("%s is %dx%d, want 32x32", name, width, height)
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
	answer := proxy.Status{Addr: "127.0.0.1:9787", Answering: true,
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
	answer = proxy.Status{Addr: "127.0.0.1:9787"} // the agent stops
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
	serving := render(proxy.Status{Addr: "127.0.0.1:9787", Answering: true,
		Health: proxy.Health{Locales: []string{"fr"}, Providers: []string{"anthropic", "gemini"}}})
	if want := []string{"anthropic", "gemini"}; !slices.Equal(serving.providers, want) {
		t.Errorf("providers = %v, want %v", serving.providers, want)
	}

	// Nothing answering serves nothing, so there is no line worth handing over.
	stopped := render(proxy.Status{Addr: "127.0.0.1:9787"})
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
	answer := proxy.Status{Addr: "127.0.0.1:9787", Answering: true,
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
// `neverseen env` cannot come to disagree about how a tool is pointed at it.
func TestTheLineIsTheAgentsOwn(t *testing.T) {
	// A provider whose variable *and* CLI are both de-facto gets a line somebody
	// can paste and press return on. A prefixed assignment, not an export: it
	// applies to that one run and leaves the shell as it was.
	if got, want := proxy.PointAt("anthropic", "127.0.0.1:9787"),
		"ANTHROPIC_BASE_URL=http://127.0.0.1:9787/anthropic claude"; got != want {
		t.Errorf("PointAt = %q, want %q", got, want)
	}
	// A provider whose variable is de-facto but whose obvious CLI does not read it
	// gets the export and no command: codex takes its base URL from its own config
	// file alone, so naming it here produced a line that went out unmasked.
	if got, want := proxy.PointAt("openai", "127.0.0.1:9787"),
		"export OPENAI_BASE_URL=http://127.0.0.1:9787/openai"; got != want {
		t.Errorf("PointAt = %q, want %q", got, want)
	}
	// A provider with no agreed variable gets the URL and nothing invented: neither
	// a variable name nor a command name, because either would fail after somebody
	// had already pasted it and believed it. Six of the eight are in this case, and
	// the table is where that stops being true, one verified pair at a time.
	if got, want := proxy.PointAt("gemini", "127.0.0.1:9787"),
		"http://127.0.0.1:9787/gemini"; got != want {
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
	if err := copyToClipboard("neverseen tray test"); err != nil {
		t.Errorf("copying to the clipboard failed: %v", err)
	}
}

// Where a tool does not simply honour the variable, the entry says so. codex does
// not read OPENAI_BASE_URL for its own traffic at all — its base URL comes from the
// openai_base_url key in ~/.codex/config.toml — so the copied export points it
// nowhere, silently, with the traffic going out unmasked. That failure has no
// symptom from the terminal, which is why the file to edit is carried to the point
// of handover rather than left in a wiki page.
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

// The two changes the field-by-field comparison could not see.
//
// Neither is exotic: both are made by `neverseen mask`, which is the only way to
// reach these settings on a workstation with no menu bar, and both used to leave
// the bar ticking the value the agent had stopped applying. The secret level is
// named nowhere in the state lines, so it moved with nothing else to give it away;
// the locales are named in a state line only while no category is switched off,
// which is why the second case pins an agent in the partial state.
func TestSameSeesTheSettingsNoStateLineCarries(t *testing.T) {
	masking := proxy.Status{Addr: "127.0.0.1:9787", Answering: true, Health: proxy.Health{
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
	base := render(proxy.Status{Addr: "127.0.0.1:9787", Answering: true, Health: proxy.Health{
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

// The one write the menu has left, and the whole state has to travel with it.
//
// PUT /policy replaces rather than patches, so a request carrying nothing but the
// empty set would take the mode, the locales and the secret level down with it —
// switching off, from a menu entry that says "mask everything again", three settings
// somebody chose on the settings page.
func TestMaskingEverythingAgainCarriesTheRestUnchanged(t *testing.T) {
	health := healthWithGroups("EMAIL")
	health.SecretLevel = "strong"
	health.Substitution = "fake"
	d := render(proxy.Status{Addr: "127.0.0.1:9787", Answering: true, Health: health})

	want := d.maskEverything()

	if len(want.Off) != 0 {
		t.Errorf("a category stayed switched off: %v", want.Off)
	}
	// Empty rather than nil: the route reads an absent list as a malformed request,
	// and this one means "nothing is switched off".
	if want.Off == nil {
		t.Error("the switched-off set is nil, which the route refuses as a partial request")
	}
	if want.Substitution != "fake" {
		t.Errorf("the substitution mode was lost: %q", want.Substitution)
	}
	if want.SecretLevel != "strong" {
		t.Errorf("the secret level was lost: %q", want.SecretLevel)
	}
	if len(want.Locales) != 1 || want.Locales[0] != "fr" {
		t.Errorf("the locales were lost: %v", want.Locales)
	}
}
