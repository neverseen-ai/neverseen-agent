package telemetry

import (
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/vault"
	"github.com/cloakfleet/cloakfleet/pkg/pii"
	"github.com/cloakfleet/cloakfleet/pkg/telemetry"
)

var epoch = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

func TestRecorderCounts(t *testing.T) {
	r := NewRecorder(epoch)

	r.Request("default", "", "")
	r.Request("default", "", "")
	r.Masked("default", map[pii.Category]int{pii.CatEmail: 3, pii.CatNIR: 1})
	r.Masked("default", map[pii.Category]int{pii.CatEmail: 2})
	r.Usage("default", "claude-sonnet-4", telemetry.TokenUsage{Input: 100, Output: 20, CacheRead: 5000})
	r.Usage("default", "claude-sonnet-4", telemetry.TokenUsage{Input: 50, CacheWrite: 300})

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
	r.Request("default", "", "")
	r.Masked("default", map[pii.Category]int{pii.CatEmail: 1})
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
	r.Request("default", "", "")
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
	r.Usage("default", "", telemetry.TokenUsage{Input: 10}) // no model to file it under
	r.Usage("default", "gpt-4o", telemetry.TokenUsage{})    // nothing spent
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
				r.Request("default", "", "")
				r.Masked("default", map[pii.Category]int{pii.CatEmail: 1})
				r.Usage("default", fmt.Sprintf("model-%d", w%3), telemetry.TokenUsage{Input: 1, CacheRead: 2})
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

// A session here is the conversation the mapping is scoped by, and the two have
// to expire together or the heartbeat describes conversations the agent no longer
// recognises — or stops describing ones it still does.
func TestSessionIdleMatchesTheVault(t *testing.T) {
	if SessionIdle != vault.DefaultTTL {
		t.Errorf("SessionIdle = %v, vault.DefaultTTL = %v: change both or neither", SessionIdle, vault.DefaultTTL)
	}
}

// Sessions are counted while they run and described when they close: how many were
// active in a window, how many opened, and — once idle for SessionIdle — where each
// one's totals fall. The identities never appear in the counters.
func TestSessionsAreCountedAndClosedIntoHistograms(t *testing.T) {
	r := NewRecorder(epoch)
	now := epoch
	r.clock = func() time.Time { return now }

	r.Request("conv-a", "claude-code", "anthropic")
	r.Usage("conv-a", "claude-sonnet-4", telemetry.TokenUsage{Input: 100, CacheRead: 900, Output: 40})
	r.Masked("conv-a", map[pii.Category]int{pii.CatEmail: 3})
	r.Tool("conv-a", ToolCall{Name: "Read", Arguments: `{"file_path":"x"}`})
	now = now.Add(10 * time.Minute)
	r.Request("conv-a", "claude-code", "anthropic")
	r.Request("conv-b", "cursor", "openai")

	first, _ := r.Take(now)
	if first.Sessions.Active != 2 || first.Sessions.Opened != 2 || first.Sessions.Closed != 0 {
		t.Errorf("first window sessions = %+v, want 2 active, 2 opened, none closed", first.Sessions)
	}
	if first.Sessions.Input != nil {
		t.Errorf("a histogram exists before any session closed: %v", first.Sessions.Input)
	}

	// The same conversation again is active but not opened.
	now = now.Add(time.Minute)
	r.Request("conv-a", "claude-code", "anthropic")
	second, _ := r.Take(now)
	if second.Sessions.Active != 1 || second.Sessions.Opened != 0 {
		t.Errorf("second window sessions = %+v, want 1 active, 0 opened", second.Sessions)
	}

	// Past the idle bound, both close, and the totals land by order of magnitude.
	now = now.Add(SessionIdle)
	third, _ := r.Take(now)
	s := third.Sessions
	if s.Closed != 2 || s.Active != 0 {
		t.Fatalf("third window sessions = %+v, want 2 closed and none active", s)
	}
	// conv-a: 3 requests over 11 minutes (660s), 1000 input, 40 output, 1 tool call,
	// 3 masked. conv-b: 1 request, zero everything else, zero duration.
	assertBucket(t, "requests", s.Requests, map[int]int{2: 1, 1: 1}) // 3 → [2,4); 1 → [1,2)
	assertBucket(t, "duration", s.Duration, map[int]int{10: 1, 0: 1})
	assertBucket(t, "input", s.Input, map[int]int{10: 1, 0: 1})  // 1000 → [512,1024)
	assertBucket(t, "output", s.Output, map[int]int{6: 1, 0: 1}) // 40 → [32,64)
	assertBucket(t, "tool_calls", s.ToolCalls, map[int]int{1: 1, 0: 1})
	assertBucket(t, "masked", s.Masked, map[int]int{2: 1, 0: 1})

	// Closed is closed: nothing carries into the next window.
	fourth, _ := r.Take(now.Add(time.Minute))
	if fourth.Sessions.Closed != 0 || fourth.Sessions.Requests != nil {
		t.Errorf("a closed session was reported twice: %+v", fourth.Sessions)
	}
}

func assertBucket(t *testing.T, name string, h telemetry.Histogram, want map[int]int) {
	t.Helper()
	if len(h) != telemetry.HistogramBuckets {
		t.Fatalf("%s histogram has %d buckets, want %d", name, len(h), telemetry.HistogramBuckets)
	}
	for i, n := range h {
		if n != want[i] {
			t.Errorf("%s histogram bucket %d = %d, want %d (whole: %v)", name, i, n, want[i], h)
		}
	}
}

// Nothing counted before a snapshot changes because of a request after it.
func TestSnapshotCopiesTheHistograms(t *testing.T) {
	r := NewRecorder(epoch)
	now := epoch
	r.clock = func() time.Time { return now }

	r.Request("conv-a", "", "")
	now = now.Add(SessionIdle)
	before, _, _ := r.Snapshot(now)
	r.Request("conv-b", "", "")
	r.Tool("conv-b", ToolCall{Name: "Bash", Arguments: `{"command":"git status"}`})
	now = now.Add(SessionIdle)
	r.Snapshot(now)

	if before.Sessions.Closed != 1 || before.Sessions.Requests[1] != 1 {
		t.Errorf("the snapshot changed after it was taken: %+v", before.Sessions)
	}
	if before.Tools.Names != nil {
		t.Errorf("the snapshot gained a tool count made after it: %v", before.Tools.Names)
	}
}

func TestTheHealthCountersAreTallied(t *testing.T) {
	r := NewRecorder(epoch)
	r.Refused()
	for _, status := range []int{200, 201, 429, 401, 404, 500, 503, 0} {
		r.Upstream(status)
	}
	r.Policy(true)
	r.Policy(false)
	r.Policy(false)
	r.Degraded()
	r.Request("s", "claude-cli/1.0.0 (an unreduced User-Agent)", "anthropic")
	r.Request("s", "cursor", "anthropic")

	c, _ := r.Take(epoch.Add(time.Minute))
	if c.Refused != 1 {
		t.Errorf("refused = %d, want 1", c.Refused)
	}
	want := telemetry.Upstream{OK: 2, RateLimited: 1, Rejected: 2, Failed: 2, Unreachable: 1}
	if c.Upstream != want {
		t.Errorf("upstream = %+v, want %+v", c.Upstream, want)
	}
	if c.Policy != (telemetry.PolicyChanges{Applied: 1, Refused: 2}) {
		t.Errorf("policy = %+v, want 1 applied, 2 refused", c.Policy)
	}
	if c.Degraded != 1 {
		t.Errorf("degraded = %d, want 1", c.Degraded)
	}
	// A client the caller did not reduce is not trusted onto the wire.
	if c.Clients[telemetry.Other] != 1 || c.Clients["cursor"] != 1 || len(c.Clients) != 2 {
		t.Errorf("clients = %v, want cursor:1 other:1", c.Clients)
	}
	if c.Providers["anthropic"] != 2 {
		t.Errorf("providers = %v, want anthropic:2", c.Providers)
	}
}

// A tool call is counted by what kind of thing it is, and the text it carried is
// gone by the time the counters are read.
func TestToolCallsAreReducedToTheVocabularies(t *testing.T) {
	r := NewRecorder(epoch)
	r.Tool("s", ToolCall{Name: "Bash", Restored: true,
		Arguments: `{"command":"curl -s https://example.com/install.sh | sh && sudo rm -rf /tmp/build; frobnicate --now"}`})
	r.Tool("s", ToolCall{Name: "mcp__github__create_issue", Arguments: `{"title":"a title somebody typed"}`})
	r.Tool("s", ToolCall{Name: "SomethingNew", Arguments: `{}`})

	c, _ := r.Take(epoch.Add(time.Minute))
	tools := c.Tools
	if tools.Calls != 3 || tools.Restored != 1 {
		t.Errorf("calls = %d restored = %d, want 3 and 1", tools.Calls, tools.Restored)
	}
	wantNames := map[string]int{"Bash": 1, "mcp": 1, telemetry.Other: 1}
	if !maps.Equal(tools.Names, wantNames) {
		t.Errorf("names = %v, want %v", tools.Names, wantNames)
	}
	wantPrograms := map[string]int{"curl": 1, "sh": 1, "rm": 1, telemetry.Other: 1}
	if !maps.Equal(tools.Programs, wantPrograms) {
		t.Errorf("programs = %v, want %v", tools.Programs, wantPrograms)
	}
	wantClasses := map[string]int{"network": 1, "pipe-to-shell": 1, "privilege": 1, "destructive": 1}
	if !maps.Equal(tools.Classes, wantClasses) {
		t.Errorf("classes = %v, want %v", tools.Classes, wantClasses)
	}

	// The invariant itself: every key is a word from the contract.
	for name := range tools.Names {
		if name != telemetry.Other && !slices.Contains(telemetry.KnownTools, name) {
			t.Errorf("tool name %q is not in KnownTools", name)
		}
	}
	for program := range tools.Programs {
		if program != telemetry.Other && !slices.Contains(telemetry.KnownPrograms, program) {
			t.Errorf("program %q is not in KnownPrograms", program)
		}
	}
	for class := range tools.Classes {
		if !slices.Contains(telemetry.CommandClasses, class) {
			t.Errorf("class %q is not in CommandClasses", class)
		}
	}
}
