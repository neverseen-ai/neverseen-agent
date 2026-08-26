package tray

import (
	"bytes"
	"context"
	"image"
	_ "image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/proxy"
)

// recorder is a view that remembers what it was told, which is the whole of what
// a test can check about a menu bar.
type recorder struct {
	mu       sync.Mutex
	icons    [][]byte
	tooltips []string
	shown    [][]string
}

func (r *recorder) icon(png []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.icons = append(r.icons, png)
}

func (r *recorder) tooltip(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tooltips = append(r.tooltips, text)
}

func (r *recorder) lines(lines []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shown = append(r.shown, lines)
}

func (r *recorder) updates() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.shown)
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
			png, tooltip, lines := render(tc.status)
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
	_, _, lines := render(proxy.Status{Addr: "127.0.0.1:8787"})
	joined := strings.Join(lines, "\n")

	for _, want := range []string{"clear", "not answering", "cloakfleet proxy"} {
		if !strings.Contains(strings.ToLower(joined), want) {
			t.Errorf("the menu does not mention %q:\n%s", want, joined)
		}
	}

	_, _, lines = render(proxy.Status{Addr: "127.0.0.1:8787", Answering: true,
		Health: proxy.Health{Locales: []string{"fr"}, Substitution: "fake", Version: "9.9.9"}})
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
	if !bytes.Equal(view.icons[len(view.icons)-1], unmaskedIcon) {
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
