// Package telemetry is the agent's side of the supervision contract: it counts
// what happened and reports it, or does neither when no backend is configured.
//
// Two rules shape everything here.
//
// The request path must not be able to notice. An agent whose backend is down,
// slow, or wrong must keep masking and answering exactly as it did before —
// telemetry is a bystander, never a dependency. So counting is a mutex and some
// integers, reporting happens on its own goroutine, and no error from this
// package can reach a caller's request.
//
// And nothing counted here is content. The counters hold integers, category
// names from the agent's own catalogue, and model ids the provider reported.
// There is nowhere in this package to put a prompt even by accident, which is
// the property pkg/telemetry's contract test exists to keep.
package telemetry

import (
	"sync"
	"time"

	"github.com/cloakfleet/cloakfleet/pkg/pii"
	"github.com/cloakfleet/cloakfleet/pkg/telemetry"
)

// Recorder accumulates one window's counters.
//
// Always present, even with no backend configured. Counting costs a mutex and a
// few map writes per request, and having it unconditionally means the request
// path has one shape rather than two — no nil checks, no branch that only runs
// in the deployments nobody tested.
type Recorder struct {
	mu sync.Mutex

	start    time.Time
	requests int
	masked   map[string]int
	models   map[string]telemetry.TokenUsage
	dropped  int
}

// NewRecorder starts a window at now.
func NewRecorder(now time.Time) *Recorder {
	return &Recorder{
		start:  now,
		masked: make(map[string]int),
		models: make(map[string]telemetry.TokenUsage),
	}
}

// Request counts one proxied exchange.
func (r *Recorder) Request() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests++
}

// Masked counts values replaced, by category. Repeats included: a value masked
// three times in one body is three values that did not leave the machine.
func (r *Recorder) Masked(counts map[pii.Category]int) {
	if len(counts) == 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for cat, n := range counts {
		r.masked[string(cat)] += n
	}
}

// Usage counts tokens spent on a model.
//
// Raw counts, never a cost. The price table belongs to the backend: prices change,
// and an agent that computed money would have to be redeployed to every
// workstation each time one did.
func (r *Recorder) Usage(model string, usage telemetry.TokenUsage) {
	if model == "" || usage == (telemetry.TokenUsage{}) {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	seen := r.models[model]
	seen.Input += usage.Input
	seen.Output += usage.Output
	seen.CacheWrite += usage.CacheWrite
	seen.CacheRead += usage.CacheRead
	r.models[model] = seen
}

// Take returns the window's counters and starts a new window.
//
// The window is explicit in what it returns because a report that failed and was
// retried covers longer than the interval it was scheduled for, and a backend
// dividing by a fixed interval would then be wrong about the rate.
func (r *Recorder) Take(now time.Time) (telemetry.Counters, telemetry.Window) {
	r.mu.Lock()
	defer r.mu.Unlock()

	counters := telemetry.Counters{
		Requests: r.requests,
		Dropped:  r.dropped,
	}
	if len(r.masked) > 0 {
		counters.Masked = r.masked
	}
	if len(r.models) > 0 {
		counters.Models = r.models
	}
	window := telemetry.Window{Start: r.start, End: now}

	r.start = now
	r.requests, r.dropped = 0, 0
	r.masked = make(map[string]int)
	r.models = make(map[string]telemetry.TokenUsage)

	return counters, window
}

// Restore puts a window's counters back after a failed send, so the next attempt
// carries them.
//
// Without it a heartbeat that could not be delivered loses its window outright,
// and the record the backend keeps has a hole in it that looks exactly like a
// period in which nothing happened.
func (r *Recorder) Restore(counters telemetry.Counters, window telemetry.Window) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if window.Start.Before(r.start) {
		r.start = window.Start
	}
	r.requests += counters.Requests
	r.dropped += counters.Dropped
	for cat, n := range counters.Masked {
		r.masked[cat] += n
	}
	for model, usage := range counters.Models {
		seen := r.models[model]
		seen.Input += usage.Input
		seen.Output += usage.Output
		seen.CacheWrite += usage.CacheWrite
		seen.CacheRead += usage.CacheRead
		r.models[model] = seen
	}
}

// Drop abandons a window and records that it happened.
//
// Counted and reported rather than dropped quietly: a gap in the record is
// exactly what an auditor needs to see, and a silently lost window is
// indistinguishable from a quiet one.
//
// It deliberately does not extend the current window backwards the way Restore
// does. The data for that period is gone, and a window claiming to start before
// the data it holds would have a backend dividing by a period it never measured.
func (r *Recorder) Drop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropped++
}
