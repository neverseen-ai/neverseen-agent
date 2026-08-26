package tray

import (
	"bytes"
	"context"
	"image"
	_ "image/png"
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
	for name, raw := range map[string][]byte{"masking": maskingIcon, "unmasked": unmaskedIcon} {
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

	if bytes.Equal(maskingIcon, unmaskedIcon) {
		t.Error("the two icons are the same image, so the menu bar would never change")
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
