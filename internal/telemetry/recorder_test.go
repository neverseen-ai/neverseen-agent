package telemetry

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cloakfleet/cloakfleet/pkg/pii"
	"github.com/cloakfleet/cloakfleet/pkg/telemetry"
)

var epoch = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

func TestRecorderCounts(t *testing.T) {
	r := NewRecorder(epoch)

	r.Request()
	r.Request()
	r.Masked(map[pii.Category]int{pii.CatEmail: 3, pii.CatNIR: 1})
	r.Masked(map[pii.Category]int{pii.CatEmail: 2})
	r.Usage("claude-sonnet-4", telemetry.TokenUsage{Input: 100, Output: 20, CacheRead: 5000})
	r.Usage("claude-sonnet-4", telemetry.TokenUsage{Input: 50, CacheWrite: 300})

	counters, window := r.Take(epoch.Add(5 * time.Minute))

	if counters.Requests != 2 {
		t.Errorf("requests = %d, want 2", counters.Requests)
	}
	// Repeats included: a value masked three times is three values that did not
	// leave the machine, which is the number being reported on.
	if counters.Masked["EMAIL"] != 5 || counters.Masked["NIR"] != 1 {
		t.Errorf("masked = %v, want EMAIL:5 NIR:1", counters.Masked)
	}

	want := telemetry.TokenUsage{Input: 150, Output: 20, CacheWrite: 300, CacheRead: 5000}
	if got := counters.Models["claude-sonnet-4"]; got != want {
		t.Errorf("usage = %+v, want %+v", got, want)
	}

	if !window.Start.Equal(epoch) || !window.End.Equal(epoch.Add(5*time.Minute)) {
		t.Errorf("window = %v..%v, want %v..%v", window.Start, window.End, epoch, epoch.Add(5*time.Minute))
	}
}

// Take starts a new window. Without the reset every heartbeat would report
// running totals, and a backend showing a rate would show it climbing forever.
func TestTakeStartsANewWindow(t *testing.T) {
	r := NewRecorder(epoch)
	r.Request()
	r.Masked(map[pii.Category]int{pii.CatEmail: 1})
	r.Take(epoch.Add(time.Minute))

	counters, window := r.Take(epoch.Add(2 * time.Minute))
	if counters.Requests != 0 || len(counters.Masked) != 0 {
		t.Errorf("the second window carries the first one's counters: %+v", counters)
	}
	if !window.Start.Equal(epoch.Add(time.Minute)) {
		t.Errorf("the second window starts at %v, want where the first ended", window.Start)
	}
}

// A heartbeat that could not be delivered must not lose its window: the record
// the backend keeps would have a hole in it that looks exactly like a period in
// which nothing happened.
func TestRestoreKeepsAFailedWindow(t *testing.T) {
	r := NewRecorder(epoch)
	r.Request()
	r.Masked(map[pii.Category]int{pii.CatEmail: 4})
	r.Usage("gpt-4o", telemetry.TokenUsage{Input: 10, Output: 2, CacheWrite: 3, CacheRead: 7})

	counters, window := r.Take(epoch.Add(5 * time.Minute))
	r.Restore(counters, window)

	// More happens while the backend is still down.
	r.Request()
	r.Usage("gpt-4o", telemetry.TokenUsage{Input: 5, CacheRead: 1})

	merged, mergedWindow := r.Take(epoch.Add(10 * time.Minute))

	if merged.Requests != 2 {
		t.Errorf("requests = %d, want 2 — the restored window plus the new one", merged.Requests)
	}
	if merged.Masked["EMAIL"] != 4 {
		t.Errorf("masked = %v, want EMAIL:4 carried over", merged.Masked)
	}
	// Every count, not just the two obvious ones. Cache tokens dominate a coding
	// agent's bill, so losing them on a retry would understate it by an order of
	// magnitude while looking plausible.
	want := telemetry.TokenUsage{Input: 15, Output: 2, CacheWrite: 3, CacheRead: 8}
	if got := merged.Models["gpt-4o"]; got != want {
		t.Errorf("usage = %+v, want %+v", got, want)
	}
	// And the window covers the whole period, so a backend computing a rate
	// divides by what was actually measured.
	if !mergedWindow.Start.Equal(epoch) {
		t.Errorf("the merged window starts at %v, want %v", mergedWindow.Start, epoch)
	}
}

// An abandoned window is counted, and does not stretch the next one backwards
// over data that no longer exists.
func TestDropIsReportedAndDoesNotStretchTheWindow(t *testing.T) {
	r := NewRecorder(epoch)
	r.Request()
	r.Take(epoch.Add(5 * time.Minute)) // taken and lost
	r.Drop()

	counters, window := r.Take(epoch.Add(10 * time.Minute))
	if counters.Dropped != 1 {
		t.Errorf("dropped = %d, want 1 — a gap in the record is what an auditor needs to see", counters.Dropped)
	}
	if !window.Start.Equal(epoch.Add(5 * time.Minute)) {
		t.Errorf("the window starts at %v, want where the lost one ended: a window claiming to cover "+
			"data that was thrown away would have the backend dividing by a period it never measured",
			window.Start)
	}
}

func TestUsageIgnoresNothing(t *testing.T) {
	r := NewRecorder(epoch)
	r.Usage("", telemetry.TokenUsage{Input: 10}) // no model to file it under
	r.Usage("gpt-4o", telemetry.TokenUsage{})    // nothing spent
	if counters, _ := r.Take(epoch); len(counters.Models) != 0 {
		t.Errorf("models = %v, want nothing recorded", counters.Models)
	}
}

// The recorder sits on the request path, so every method has to be safe to call
// from the handlers of concurrent requests.
func TestRecorderIsSafeUnderConcurrency(t *testing.T) {
	r := NewRecorder(epoch)

	const workers, each = 8, 200
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for range each {
				r.Request()
				r.Masked(map[pii.Category]int{pii.CatEmail: 1})
				r.Usage(fmt.Sprintf("model-%d", w%3), telemetry.TokenUsage{Input: 1, CacheRead: 2})
			}
		}(w)
	}

	// Taking a window while requests are still arriving is the real race: a
	// heartbeat fires on its own goroutine, not between requests.
	var taken int
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 20 {
			counters, _ := r.Take(epoch)
			taken += counters.Requests
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()
	<-done

	final, _ := r.Take(epoch)
	if got := taken + final.Requests; got != workers*each {
		t.Errorf("counted %d requests across every window, want %d — a count lost between a "+
			"request and a heartbeat is a count nobody can find again", got, workers*each)
	}
}
