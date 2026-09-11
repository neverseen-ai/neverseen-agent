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
// names from the agent's own catalogue, model ids the provider reported, and
// words from the closed vocabularies the contract carries. There is nowhere in
// this package to put a prompt even by accident, which is the property
// pkg/telemetry's contract test exists to keep. The one place text arrives — a
// tool call's arguments, in Tool — is reduced to those vocabularies before the
// lock is even taken, and the text is dropped.
package telemetry

import (
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
	"github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
)

// SessionIdle is how long a session outlives its last request before it is
// counted as closed.
//
// The same thirty minutes as vault.DefaultTTL, and it has to be: a session here
// is the conversation the mapping is scoped by, and closing one while its mapping
// still lives — or keeping one after its mapping has gone — would have the
// heartbeat describing conversations the agent no longer recognises. Not imported
// from the vault, because this package must not reach into the request path;
// TestSessionIdleMatchesTheVault holds the two together.
const SessionIdle = 30 * time.Minute

// Recorder accumulates one window's counters.
//
// Always present, even with no backend configured. Counting costs a mutex and a
// few map writes per request, and having it unconditionally means the request
// path has one shape rather than two — no nil checks, no branch that only runs
// in the deployments nobody tested.
type Recorder struct {
	mu sync.Mutex

	start    time.Time
	counters telemetry.Counters

	// sessions are the conversations still alive, by the identity the request path
	// scopes them by. Never reported: the identity stays here and the counts leave.
	// Swept on every Take and Snapshot, so an entry lives SessionIdle past its last
	// request and the map is bounded by how many conversations half an hour holds.
	sessions map[string]*session

	// seen names the sessions that sent a request in the open window, for Active.
	seen map[string]struct{}

	// clock dates a session's requests. time.Now outside the tests, which pin it
	// for the reason every reporter test pins Config.Now: a session that ages out
	// under a real clock turns a test green or red with the wall time.
	clock func() time.Time

	// changes counts what has been recorded into the open window, so a caller can
	// tell whether it is worth writing to disk again without comparing two sets of
	// maps. Reset with the counters, so zero means "nothing to snapshot".
	changes uint64
}

// session is one conversation's running totals, folded into the histograms when
// it closes.
type session struct {
	first, last time.Time

	requests, input, output, toolCalls, masked int
}

// NewRecorder starts a window at now.
//
// It opens with one restart already counted, because a process builds exactly one
// Recorder: making the constructor the place a start is recorded means there is no
// separate call anybody can forget to make, and no way for the count to disagree
// with the number of processes there actually were.
func NewRecorder(now time.Time) *Recorder {
	r := &Recorder{
		start:    now,
		sessions: make(map[string]*session),
		clock:    time.Now,
	}
	r.reset(now)
	r.counters.Restarts = 1
	return r
}

// WithClock dates the recorder's sessions from clock rather than from the wall.
//
// For a replay: exchanges read back from trace files happened at the time the
// file says, and a session's idle bound has to be measured against that time or
// every conversation in the traces closes at once, at the first sweep. Not a
// setting of the running agent, which has no reason to date anything but now.
func (r *Recorder) WithClock(clock func() time.Time) *Recorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clock = clock
	return r
}

// reset opens a new window at now. Called under the lock.
func (r *Recorder) reset(now time.Time) {
	r.start = now
	r.counters = telemetry.Counters{}
	r.seen = make(map[string]struct{})
	r.changes = 0
}

// Request counts one proxied exchange: which conversation it belongs to, which
// client family sent it, and which provider it was for.
//
// client is a word from telemetry.KnownClients — see ClientFamily — and provider a
// route code; either may be empty. Anything outside the vocabulary is counted as
// Other here rather than trusted from the caller, so the invariant does not
// depend on every call site reducing correctly.
func (r *Recorder) Request(session, client, provider string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.counters.Requests++
	r.changes++
	if client != "" {
		count(&r.counters.Clients, inVocabulary(client, telemetry.KnownClients))
	}
	if provider != "" {
		count(&r.counters.Providers, provider)
	}

	s := r.session(session)
	s.requests++
}

// Refused counts a request the agent would not forward — the fail-closed 415.
func (r *Recorder) Refused() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters.Refused++
	r.changes++
}

// Upstream counts how a provider answered. Zero is no answer at all.
func (r *Recorder) Upstream(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes++
	switch {
	case status == 0:
		r.counters.Upstream.Unreachable++
	case status == 429:
		r.counters.Upstream.RateLimited++
	case status >= 500:
		r.counters.Upstream.Failed++
	case status >= 400:
		r.counters.Upstream.Rejected++
	default:
		r.counters.Upstream.OK++
	}
}

// Policy counts one request to change what the agent masks.
func (r *Recorder) Policy(applied bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes++
	if applied {
		r.counters.Policy.Applied++
	} else {
		r.counters.Policy.Refused++
	}
}

// Degraded counts an answer whose tool-call arguments never formed a document and
// were expanded token by token — see the contract field.
func (r *Recorder) Degraded() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters.Degraded++
	r.changes++
}

// Masked counts values replaced, by category. Repeats included: a value masked
// three times in one body is three values that did not leave the machine.
func (r *Recorder) Masked(session string, counts map[pii.Category]int) {
	if len(counts) == 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes++
	total := 0
	for cat, n := range counts {
		count(&r.counters.Masked, string(cat), n)
		total += n
	}
	r.session(session).masked += total
}

// Usage counts tokens spent on a model.
//
// Raw counts, never a cost. The price table belongs to the backend: prices change,
// and an agent that computed money would have to be redeployed to every
// workstation each time one did.
func (r *Recorder) Usage(session, model string, usage telemetry.TokenUsage) {
	if model == "" || usage == (telemetry.TokenUsage{}) {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.changes++
	if r.counters.Models == nil {
		r.counters.Models = make(map[string]telemetry.TokenUsage)
	}
	seen := r.counters.Models[model]
	seen.Input += usage.Input
	seen.Output += usage.Output
	seen.CacheWrite += usage.CacheWrite
	seen.CacheRead += usage.CacheRead
	r.counters.Models[model] = seen

	s := r.session(session)
	s.input += usage.Input + usage.CacheWrite + usage.CacheRead
	s.output += usage.Output
}

// Tool counts one tool call the model made.
//
// The arguments are read for the programs a shell command names and the classes
// they fall into, reduced to the contract's vocabularies before the lock is taken,
// and then dropped — see tools.go. Nothing of the text survives the call.
func (r *Recorder) Tool(session string, call ToolCall) {
	name, programs, classes := classify(call)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.changes++
	r.counters.Tools.Calls++
	if call.Restored {
		r.counters.Tools.Restored++
	}
	count(&r.counters.Tools.Names, name)
	for _, p := range programs {
		count(&r.counters.Tools.Programs, p)
	}
	for _, c := range classes {
		count(&r.counters.Tools.Classes, c)
	}
	r.session(session).toolCalls++
}

// session finds or opens the conversation's tally, and marks it active in this
// window. Called under the lock.
func (r *Recorder) session(id string) *session {
	now := r.clock()
	s, ok := r.sessions[id]
	if !ok {
		s = &session{first: now}
		r.sessions[id] = s
		r.counters.Sessions.Opened++
	}
	s.last = now
	if _, active := r.seen[id]; !active {
		r.seen[id] = struct{}{}
		r.counters.Sessions.Active++
	}
	return s
}

// sweep closes every session idle for SessionIdle at now, folding its totals into
// the open window's histograms. Called under the lock.
//
// From Take and Snapshot rather than from a timer of its own, because those are
// the two moments the recorder is read on the reporter's goroutine with a clock,
// and a session that closed between two of them lands in the window being filed
// either way.
func (r *Recorder) sweep(now time.Time) {
	for id, s := range r.sessions {
		if now.Sub(s.last) < SessionIdle {
			continue
		}
		delete(r.sessions, id)
		r.changes++
		sessions := &r.counters.Sessions
		sessions.Closed++
		sessions.Duration.Add(int(s.last.Sub(s.first) / time.Second))
		sessions.Requests.Add(s.requests)
		sessions.Input.Add(s.input)
		sessions.Output.Add(s.output)
		sessions.ToolCalls.Add(s.toolCalls)
		sessions.Masked.Add(s.masked)
	}
}

// Take returns the window's counters and starts a new window.
//
// The window is explicit in what it returns because a report that failed and was
// retried covers longer than the interval it was scheduled for, and a backend
// dividing by a fixed interval would then be wrong about the rate.
func (r *Recorder) Take(now time.Time) (telemetry.Counters, telemetry.Window) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.sweep(now)
	counters := r.counters
	window := telemetry.Window{Start: r.start, End: now}
	r.reset(now)
	return counters, window
}

// Reopen starts the next window at now, abandoning the period since the last one
// was taken.
//
// For one caller and one situation: the reporter has just closed a window at the
// last moment it was known to be running, having found the wall clock further
// ahead than its own loop. What lies between that moment and now is time this
// agent did not measure — a suspended laptop, a paused virtual machine, a stopped
// process. Carrying it into the next window would have one report claim a period
// nothing was watching, which is the one thing a masking record must not do.
func (r *Recorder) Reopen(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.start = now
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

	r.sweep(now)
	counters := r.counters
	counters.Masked = maps.Clone(counters.Masked)
	counters.Models = maps.Clone(counters.Models)
	counters.Providers = maps.Clone(counters.Providers)
	counters.Clients = maps.Clone(counters.Clients)
	counters.Tools.Names = maps.Clone(counters.Tools.Names)
	counters.Tools.Programs = maps.Clone(counters.Tools.Programs)
	counters.Tools.Classes = maps.Clone(counters.Tools.Classes)
	counters.Sessions.Duration = cloneHistogram(counters.Sessions.Duration)
	counters.Sessions.Requests = cloneHistogram(counters.Sessions.Requests)
	counters.Sessions.Input = cloneHistogram(counters.Sessions.Input)
	counters.Sessions.Output = cloneHistogram(counters.Sessions.Output)
	counters.Sessions.ToolCalls = cloneHistogram(counters.Sessions.ToolCalls)
	counters.Sessions.Masked = cloneHistogram(counters.Sessions.Masked)
	return counters, telemetry.Window{Start: r.start, End: now}, r.changes
}

func cloneHistogram(h telemetry.Histogram) telemetry.Histogram {
	return slices.Clone(h)
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
	r.counters.Dropped += n
	r.changes++
}

// count adds to a map that may not exist yet. Maps stay nil until the first
// entry so an empty one is omitted from the wire rather than sent as {}.
func count(m *map[string]int, key string, n ...int) {
	if *m == nil {
		*m = make(map[string]int)
	}
	if len(n) == 0 {
		(*m)[key]++
		return
	}
	(*m)[key] += n[0]
}

// inVocabulary returns word if the vocabulary lists it, and Other if not.
func inVocabulary(word string, vocabulary []string) string {
	if slices.Contains(vocabulary, word) {
		return word
	}
	return telemetry.Other
}
