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
	restarts int

	// changes counts what has been recorded into the open window, so a caller can
	// tell whether it is worth writing to disk again without comparing two sets of
	// maps. Reset with the counters, so zero means "nothing to snapshot".
	changes uint64
}

// NewRecorder starts a window at now.
//
// It opens with one restart already counted, because a process builds exactly one
// Recorder: making the constructor the place a start is recorded means there is no
// separate call anybody can forget to make, and no way for the count to disagree
// with the number of processes there actually were.
func NewRecorder(now time.Time) *Recorder {
	return &Recorder{
		start:    now,
		masked:   make(map[string]int),
		models:   make(map[string]telemetry.TokenUsage),
		restarts: 1,
	}
}

// Request counts one proxied exchange.
func (r *Recorder) Request() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests++
	r.changes++
}

// Masked counts values replaced, by category. Repeats included: a value masked
// three times in one body is three values that did not leave the machine.
func (r *Recorder) Masked(counts map[pii.Category]int) {
	if len(counts) == 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes++
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

	r.changes++
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
		Restarts: r.restarts,
	}
	if len(r.masked) > 0 {
		counters.Masked = r.masked
	}
	if len(r.models) > 0 {
		counters.Models = r.models
	}
	window := telemetry.Window{Start: r.start, End: now}

	r.start = now
	r.requests, r.dropped, r.restarts = 0, 0, 0
	r.changes = 0
	r.masked = make(map[string]int)
	r.models = make(map[string]telemetry.TokenUsage)

	return counters, window
}

// Snapshot is what the open window holds right now, without closing it.
//
// It exists so a bucket in progress can be written to disk between the intervals
// that close them: a hard kill — SIGKILL, a power cut, a battery reaching zero —
// cannot be caught and handled, so the only thing that bounds what it costs is
// having written the counters down recently. Nothing about it touches the request
// path; it is read on the reporter's own goroutine like everything else here.
//
// The maps are copied rather than handed over, because the window stays open and
// the recorder goes on writing to its own.
//
// The third result changes whenever something was counted, so a caller can skip
// rewriting a file that would come out identical — an idle workstation should not
// be writing the same bytes every half minute for the life of the process.
func (r *Recorder) Snapshot(now time.Time) (telemetry.Counters, telemetry.Window, uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	counters := telemetry.Counters{
		Requests: r.requests,
		Dropped:  r.dropped,
		Restarts: r.restarts,
	}
	if len(r.masked) > 0 {
		counters.Masked = make(map[string]int, len(r.masked))
		for cat, n := range r.masked {
			counters.Masked[cat] = n
		}
	}
	if len(r.models) > 0 {
		counters.Models = make(map[string]telemetry.TokenUsage, len(r.models))
		for model, usage := range r.models {
			counters.Models[model] = usage
		}
	}
	return counters, telemetry.Window{Start: r.start, End: now}, r.changes
}

// Drop records that n buckets were abandoned without being delivered.
//
// Counted and reported rather than dropped quietly: a gap in the record is
// exactly what an auditor needs to see, and a silently lost bucket is
// indistinguishable from a quiet five minutes.
//
// The count lands in the *current* window, deliberately, rather than extending it
// backwards over the period that was lost. That data is gone, and a window
// claiming to start before the data it holds would have a backend dividing by a
// period it never measured.
func (r *Recorder) Drop(n int) {
	if n <= 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropped += n
	r.changes++
}
