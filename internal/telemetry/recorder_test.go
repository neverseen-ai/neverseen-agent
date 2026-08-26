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

// A process builds exactly one Recorder, so the constructor is where a start is
// counted: there is no separate call anybody can forget, and no way for the tally
// to disagree with how many processes there actually were.
func TestARecorderCountsItsProcessStart(t *testing.T) {
	r := NewRecorder(epoch)

	first, _ := r.Take(epoch.Add(5 * time.Minute))
	if first.Restarts != 1 {
		t.Errorf("restarts = %d in the first bucket, want 1", first.Restarts)
	}

	// And only in the bucket the start happened in. Repeated on every bucket, a
	// perfectly healthy agent would read as one restarting every five minutes.
	second, _ := r.Take(epoch.Add(10 * time.Minute))
	if second.Restarts != 0 {
		t.Errorf("restarts = %d in the next bucket, want 0", second.Restarts)
	}
}

// An abandoned window is counted, and does not stretch the next one backwards
// over data that no longer exists.
func TestDropIsReportedAndDoesNotStretchTheWindow(t *testing.T) {
	r := NewRecorder(epoch)
	r.Request()
	r.Take(epoch.Add(5 * time.Minute)) // taken and lost
	r.Drop(1)

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
