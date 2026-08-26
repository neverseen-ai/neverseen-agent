package telemetry

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cloakfleet/cloakfleet/pkg/pii"
	"github.com/cloakfleet/cloakfleet/pkg/telemetry"
)

// backend is a fake supervision backend that records what reached it and
// verifies the signatures, so the tests assert on what an agent actually sends
// rather than on what it meant to.
type backend struct {
	server *httptest.Server

	mu         sync.Mutex
	heartbeats []telemetry.Heartbeat // every bucket, expanded as the real backend does
	batches    []int                 // how many buckets each accepted request carried
	signatures []bool                // whether each batch verified against the issued key
	enrolments int
	attempts   int // batches offered, refused ones included
	key        []byte

	// fail, while set, makes every heartbeat fail.
	failing bool
	// refuseFirst makes that many of the first attempts fail, so a test can have
	// the backend come back on its own between two of the agent's retries rather
	// than racing a flag against them.
	refuseFirst int
}

func newBackend(t *testing.T) *backend {
	t.Helper()

	b := &backend{key: []byte("0123456789abcdef0123456789abcdef")}
	mux := http.NewServeMux()

	mux.HandleFunc(enrolPath, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var req telemetry.EnrolRequest
		if err := json.Unmarshal(body, &req); err != nil || req.Token == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		b.mu.Lock()
		b.enrolments++
		b.mu.Unlock()

		_ = json.NewEncoder(w).Encode(telemetry.EnrolResponse{
			Schema:  telemetry.SchemaVersion,
			AgentID: "agt_test",
			Key:     hex.EncodeToString(b.key),
		})
	})

	mux.HandleFunc(heartbeatsPath, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		b.mu.Lock()
		b.attempts++
		failing := b.failing || b.attempts <= b.refuseFirst
		b.mu.Unlock()
		if failing {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		var batch telemetry.HeartbeatBatch
		if err := json.Unmarshal(body, &batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		b.mu.Lock()
		// Expanded through the contract's own function, the one the real backend
		// uses, so a test cannot agree with an expansion nothing ships.
		b.heartbeats = append(b.heartbeats, batch.Windows()...)
		b.batches = append(b.batches, len(batch.Buckets))
		b.signatures = append(b.signatures,
			telemetry.VerifySignature(b.key, body, r.Header.Get(telemetry.HeaderSignature)))
		b.mu.Unlock()
	})

	b.server = httptest.NewServer(mux)
	t.Cleanup(b.server.Close)
	return b
}

func (b *backend) received() ([]telemetry.Heartbeat, []bool, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]telemetry.Heartbeat(nil), b.heartbeats...),
		append([]bool(nil), b.signatures...), b.enrolments
}

// batchSizes reports how many buckets each accepted request carried.
func (b *backend) batchSizes() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]int(nil), b.batches...)
}

func (b *backend) attemptCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.attempts
}

func (b *backend) setFailing(failing bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failing = failing
}

func newTestReporter(t *testing.T, b *backend, r *Recorder) (*Reporter, string) {
	t.Helper()
	return reporterInHome(t, b, r, t.TempDir(), func() time.Time { return epoch })
}

// reporterInHome builds a reporter over a given state directory, so a test can
// build a second one over the same one — which is what a restart looks like from
// the backend's side.
func reporterInHome(t *testing.T, b *backend, r *Recorder, home string, now func() time.Time) (*Reporter, string) {
	t.Helper()

	identity := filepath.Join(home, "agent.json")
	rep, err := NewReporter(Config{
		BaseURL:        b.server.URL,
		EnrolmentToken: "enrol-me",
		IdentityFile:   identity,
		BufferFile:     filepath.Join(home, "buffer.json"),
		Recorder:       r,
		State: func() telemetry.State {
			return telemetry.State{Version: "1.0.0", Locales: []string{"fr"}, Substitution: "token"}
		},
		Interval: time.Hour, // the tests drive collect and drain directly
		Now:      now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rep, identity
}

// report is one tick of Run: close the current bucket and deliver what is queued.
// Run keeps the two on separate timers, which is the point of the design — the
// tests that care about that drive them apart.
func (r *Reporter) report(ctx context.Context) bool {
	r.collect()
	return r.drain(ctx)
}

func TestReporterEnrolsOnceThenReports(t *testing.T) {
	b := newBackend(t)
	r := NewRecorder(epoch)
	rep, identity := newTestReporter(t, b, r)

	r.Request()
	r.Masked(map[pii.Category]int{pii.CatEmail: 2})
	rep.report(t.Context())

	r.Request()
	rep.report(t.Context())

	got, signatures, enrolments := b.received()

	// Once, not twice. An agent that enrolled on every heartbeat would fill the
	// backend with one agent per five minutes, each counting against whatever the
	// operator is paying for.
	if enrolments != 1 {
		t.Errorf("enrolled %d times, want 1", enrolments)
	}
	if len(got) != 2 {
		t.Fatalf("the backend saw %d heartbeats, want 2", len(got))
	}

	// Every heartbeat is signed with the issued key, so one workstation cannot
	// file reports as another.
	for i, ok := range signatures {
		if !ok {
			t.Errorf("heartbeat %d did not verify against the issued key", i)
		}
	}

	if got[0].Counters.Masked["EMAIL"] != 2 {
		t.Errorf("the first heartbeat carries %v, want EMAIL:2", got[0].Counters.Masked)
	}
	if got[1].Counters.Masked["EMAIL"] != 0 {
		t.Errorf("the second heartbeat repeats the first window's counters: %v", got[1].Counters.Masked)
	}
	if got[0].AgentID != "agt_test" || got[0].Schema != telemetry.SchemaVersion {
		t.Errorf("heartbeat identity or schema is wrong: %+v", got[0])
	}

	// The identity was persisted, readable only by its owner: it holds a signing
	// key, and one that is world-readable on a shared machine is a key anybody
	// can file reports with.
	info, err := os.Stat(identity)
	if err != nil {
		t.Fatalf("the identity was not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the identity file is %o, want 600", perm)
	}
}

// The state is asked for at each heartbeat, so what the backend sees is what the
// agent is applying now — not what it was configured with when it started.
func TestReporterSendsLiveState(t *testing.T) {
	b := newBackend(t)
	r := NewRecorder(epoch)

	locales := []string{"fr"}
	rep, err := NewReporter(Config{
		BaseURL:        b.server.URL,
		EnrolmentToken: "enrol-me",
		IdentityFile:   filepath.Join(t.TempDir(), "agent.json"),
		BufferFile:     filepath.Join(t.TempDir(), "buffer.json"),
		Recorder:       r,
		State:          func() telemetry.State { return telemetry.State{Locales: locales} },
		Now:            func() time.Time { return epoch },
	})
	if err != nil {
		t.Fatal(err)
	}

	rep.report(t.Context())
	locales = []string{"fr", "gb", "us"}
	rep.report(t.Context())

	got, _, _ := b.received()
	if len(got) != 2 {
		t.Fatalf("the backend saw %d heartbeats, want 2", len(got))
	}
	if len(got[1].State.Locales) != 3 {
		t.Errorf("the second heartbeat reports %v, want the locales the agent is applying now",
			got[1].State.Locales)
	}
}

// A refused bucket is kept and filed later — and it is still its own bucket. This
// is the whole reason the buffer is a queue rather than one merged window: merged,
// a backend down over a weekend comes back to a single report saying "eleven
// thousand requests, some time between Friday and Monday", which cannot answer
// when a spike happened or when an agent restarted.
func TestRefusedBucketsKeepTheirOwnWindows(t *testing.T) {
	b := newBackend(t)
	r := NewRecorder(epoch)

	now := epoch
	rep, _ := reporterInHome(t, b, r, t.TempDir(), func() time.Time { return now })

	b.setFailing(true)
	r.Request()
	r.Masked(map[pii.Category]int{pii.CatEmail: 3})
	now = epoch.Add(5 * time.Minute)
	rep.report(t.Context())

	if got, _, _ := b.received(); len(got) != 0 {
		t.Fatalf("the backend accepted a heartbeat it was meant to refuse: %v", got)
	}

	r.Request() // the next five minutes, still with the backend down
	now = epoch.Add(10 * time.Minute)
	rep.report(t.Context())

	b.setFailing(false)
	now = epoch.Add(15 * time.Minute)
	rep.report(t.Context())

	got, signatures, _ := b.received()
	if len(got) != 3 {
		t.Fatalf("the backend saw %d buckets, want 3 — one per five-minute period", len(got))
	}
	for i, ok := range signatures {
		if !ok {
			t.Errorf("batch %d did not verify against the issued key", i)
		}
	}
	// Three buckets, but the last request carried the two that had been refused
	// plus the current one: that is the point of batching.
	if sizes := b.batchSizes(); len(sizes) != 1 || sizes[0] != 3 {
		t.Errorf("the accepted requests carried %v buckets, want one request of 3", sizes)
	}

	// Oldest first, each still covering the five minutes it measured.
	for i, want := range []struct {
		start, end time.Time
		requests   int
	}{
		{epoch, epoch.Add(5 * time.Minute), 1},
		{epoch.Add(5 * time.Minute), epoch.Add(10 * time.Minute), 1},
		{epoch.Add(10 * time.Minute), epoch.Add(15 * time.Minute), 0},
	} {
		if !got[i].Window.Start.Equal(want.start) || !got[i].Window.End.Equal(want.end) {
			t.Errorf("bucket %d covers %v–%v, want %v–%v", i,
				got[i].Window.Start, got[i].Window.End, want.start, want.end)
		}
		if got[i].Counters.Requests != want.requests {
			t.Errorf("bucket %d carries %d requests, want %d", i, got[i].Counters.Requests, want.requests)
		}
	}
	if got[0].Counters.Masked["EMAIL"] != 3 {
		t.Errorf("the first bucket carries %v, want EMAIL:3 in the window it happened in",
			got[0].Counters.Masked)
	}
	if got[0].Counters.Restarts != 1 || got[1].Counters.Restarts != 0 {
		t.Errorf("restarts = %d then %d, want 1 then 0: the start belongs to the bucket it "+
			"happened in", got[0].Counters.Restarts, got[1].Counters.Restarts)
	}
}

// Past the bound, buckets the backend never accepted are abandoned and the loss is
// reported. Unbounded retention would have a laptop offline for a month queueing a
// month of buckets on a disk it shares with the person's work.
func TestBucketsTooOldAreDroppedAndCounted(t *testing.T) {
	b := newBackend(t)
	r := NewRecorder(epoch)

	now := epoch
	rep, _ := reporterInHome(t, b, r, t.TempDir(), func() time.Time { return now })

	r.Request()
	b.setFailing(true)
	now = epoch.Add(5 * time.Minute)
	rep.report(t.Context()) // queued, refused

	// A week and a bit later the queued bucket is past the bound, and the collect
	// that notices is the one that drops it.
	now = epoch.Add(maxWindowAge + time.Hour)
	b.setFailing(false)
	rep.report(t.Context())

	got, _, _ := b.received()
	if len(got) != 1 {
		t.Fatalf("the backend saw %d heartbeats, want 1: the stale bucket is gone", len(got))
	}
	if got[0].Counters.Dropped != 1 {
		t.Errorf("dropped = %d, want 1: a lost bucket has to be visible, because a silent one "+
			"looks exactly like a quiet five minutes", got[0].Counters.Dropped)
	}
	if got[0].Counters.Requests != 0 {
		t.Errorf("requests = %d, want 0: the abandoned bucket's counters are gone, not carried",
			got[0].Counters.Requests)
	}
}

// An unreachable backend must not stop anything. There is no caller who could act
// on the error, and the agent's job is masking.
func TestAnUnreachableBackendIsSurvivable(t *testing.T) {
	home := t.TempDir()
	r := NewRecorder(epoch)
	rep, err := NewReporter(Config{
		BaseURL:        "http://127.0.0.1:1", // nothing listens there
		EnrolmentToken: "enrol-me",
		IdentityFile:   filepath.Join(home, "agent.json"),
		BufferFile:     filepath.Join(home, "buffer.json"),
		Recorder:       r,
		State:          func() telemetry.State { return telemetry.State{} },
		Client:         &http.Client{Timeout: 100 * time.Millisecond},
		Now:            func() time.Time { return epoch },
	})
	if err != nil {
		t.Fatal(err)
	}

	r.Request()
	rep.report(t.Context()) // must not panic and must not block

	// And the bucket is still queued, on disk, to send when the backend comes back.
	if rep.queue.pending() != 1 {
		t.Errorf("%d buckets queued, want 1 kept for a later attempt", rep.queue.pending())
	}
	queued, err := loadBuffer(filepath.Join(home, "buffer.json"))
	if err != nil {
		t.Fatal(err)
	}
	if queued.pending() != 1 || queued.buckets[0].Counters.Requests != 1 {
		t.Errorf("the buffer file holds %d buckets, want the one that could not go",
			queued.pending())
	}
}

// Run reports as soon as it starts and once more on the way out.
//
// The first is what makes a freshly installed agent appear in the fleet view
// within seconds instead of at the first five-minute tick — and what makes an
// install that cannot reach the backend say so immediately instead of looking
// fine until somebody checks. The second is what stops a clean shutdown throwing
// away the window it was in the middle of.
func TestRunReportsAtStartAndOnShutdown(t *testing.T) {
	b := newBackend(t)
	r := NewRecorder(epoch)
	rep, _ := newTestReporter(t, b, r)

	r.Request()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		rep.Run(ctx)
	}()

	// Waited for, not raced. Cancelling straight away is how this test used to
	// read, and it passed for the wrong reason: Run had not been scheduled yet, so
	// its first report ran on an already-cancelled context and failed before it
	// sent anything. The shutdown report then arrived alone and the assertion of
	// "exactly one heartbeat" held — on scheduling luck, not behaviour.
	waitForHeartbeats(t, b, 1)

	r.Request()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return when its context was cancelled")
	}

	got, _, enrolments := b.received()
	if len(got) != 2 {
		t.Fatalf("the backend saw %d heartbeats, want one at start and one on shutdown", len(got))
	}
	if enrolments != 1 {
		t.Errorf("enrolled %d times, want 1: the report at start must not re-enrol", enrolments)
	}

	// One request in each window, never the same one twice. A window that carried
	// its predecessor's counters would double every number on the dashboard, and
	// the report at start is exactly the kind of extra send that would do it.
	if got[0].Counters.Requests != 1 || got[1].Counters.Requests != 1 {
		t.Errorf("the two heartbeats carry %d and %d requests, want 1 and 1",
			got[0].Counters.Requests, got[1].Counters.Requests)
	}
}

// waitForHeartbeats blocks until the backend has seen n of them.
func waitForHeartbeats(t *testing.T, b *backend, n int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, _, _ := b.received(); len(got) >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	got, _, _ := b.received()
	t.Fatalf("waited for %d heartbeats, the backend saw %d", n, len(got))
}

func TestReporterRequiresItsInputs(t *testing.T) {
	valid := Config{
		BaseURL:      "http://example.invalid",
		IdentityFile: "/tmp/agent.json",
		BufferFile:   filepath.Join(t.TempDir(), "buffer.json"),
		Recorder:     NewRecorder(epoch),
		State:        func() telemetry.State { return telemetry.State{} },
	}

	tests := map[string]func(*Config){
		"no backend URL":   func(c *Config) { c.BaseURL = "" },
		"no recorder":      func(c *Config) { c.Recorder = nil },
		"no state":         func(c *Config) { c.State = nil },
		"no identity file": func(c *Config) { c.IdentityFile = "" },
		"no buffer file":   func(c *Config) { c.BufferFile = "" },
	}
	for name, break_ := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			break_(&cfg)
			if _, err := NewReporter(cfg); err == nil {
				t.Error("NewReporter accepted an incomplete configuration")
			}
		})
	}

	if _, err := NewReporter(valid); err != nil {
		t.Errorf("a complete configuration was refused: %v", err)
	}
}

// Without an identity and without a token there is nothing to do, and the window
// is kept rather than sent nowhere.
func TestNoTokenMeansNoReportAndNoLoss(t *testing.T) {
	b := newBackend(t)
	r := NewRecorder(epoch)

	rep, err := NewReporter(Config{
		BaseURL:      b.server.URL,
		IdentityFile: filepath.Join(t.TempDir(), "agent.json"),
		BufferFile:   filepath.Join(t.TempDir(), "buffer.json"),
		Recorder:     r,
		State:        func() telemetry.State { return telemetry.State{} },
		Now:          func() time.Time { return epoch },
	})
	if err != nil {
		t.Fatal(err)
	}

	r.Request()
	rep.report(t.Context())

	if _, _, enrolments := b.received(); enrolments != 0 {
		t.Errorf("enrolled %d times with no token", enrolments)
	}
	// Nothing was sent, and nothing was lost: the bucket is queued for an agent
	// that gets a token later.
	if rep.queue.pending() != 1 || rep.queue.buckets[0].Counters.Requests != 1 {
		t.Errorf("%d buckets queued, want the one that could not be filed", rep.queue.pending())
	}
}

// The retry ladder: short at first, growing while the backend stays down, never
// slower than the cadence the agent was configured for.
func TestRetryLadderGrowsAndIsCapped(t *testing.T) {
	rep, err := NewReporter(Config{
		BaseURL:      "http://example.invalid",
		IdentityFile: "/tmp/agent.json",
		BufferFile:   filepath.Join(t.TempDir(), "buffer.json"),
		Recorder:     NewRecorder(epoch),
		State:        func() telemetry.State { return telemetry.State{} },
		Interval:     time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []time.Duration{
		time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second,
		40 * time.Second,
		// Capped, and it stays there. A ladder that kept doubling would have a
		// backend recovering after an hour waiting another hour to hear from
		// anybody.
		time.Minute, time.Minute,
	}

	var got time.Duration
	for i, expected := range want {
		got = rep.retryAfter(got)
		if got != expected {
			t.Fatalf("retry %d waits %v, want %v", i+1, got, expected)
		}
	}

	// An interval shorter than the first rung is still the cap: the retry must
	// never be slower than the ordinary cadence, whatever it is set to.
	rep.interval = 500 * time.Millisecond
	if got := rep.retryAfter(0); got != 500*time.Millisecond {
		t.Errorf("with a 500ms interval the first retry waits %v, want 500ms", got)
	}
}

// A backend that refuses a heartbeat must be retried within seconds, not at the
// next interval — and the retry must carry the window that could not go. Together
// that is the buffering: nothing is filed while it is down, nothing is lost, and
// the recovery is not sat out.
func TestRunRetriesQuicklyAndSendsTheBufferedWindow(t *testing.T) {
	b := newBackend(t)
	b.refuseFirst = 1
	r := NewRecorder(epoch)
	rep, _ := newTestReporter(t, b, r) // interval: one hour

	r.Request()

	ctx, cancel := context.WithCancel(t.Context())
	// Waited for, not just cancelled: Run writes the buffer file on its way out, and
	// a test whose temporary directory is removed underneath it fails on cleanup
	// rather than on anything it was checking.
	stopped := runInBackground(t, rep, ctx)
	t.Cleanup(func() { cancel(); stopped() })

	// A second offer, with the interval set to an hour, can only be the ladder's
	// first rung — and this one is accepted.
	waitForAttempts(t, b, 2)
	waitForHeartbeats(t, b, 1)

	got, _, enrolments := b.received()
	if enrolments != 1 {
		t.Errorf("enrolled %d times, want 1: a retry must not enrol again", enrolments)
	}
	if got[0].Counters.Requests != 1 {
		t.Errorf("the retried heartbeat carries %d requests, want 1: the refused window "+
			"is what the retry sends", got[0].Counters.Requests)
	}
}

// readBufferFile decodes the buffer file as it is on disk.
func readBufferFile(t *testing.T, path string) bufferFile {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored bufferFile
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	return stored
}

// readLiveFile decodes the bucket in progress, reporting whether there is one.
func readLiveFile(t *testing.T, queuePath string) (bucket, bool) {
	t.Helper()

	raw, err := os.ReadFile(livePathFor(queuePath))
	if os.IsNotExist(err) {
		return bucket{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var stored liveFile
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	return stored.Live, true
}

// runInBackground starts Run and returns a function that waits for it to return.
func runInBackground(t *testing.T, rep *Reporter, ctx context.Context) func() {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		rep.Run(ctx)
	}()
	return func() {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return when its context was cancelled")
		}
	}
}

// waitForAttempts blocks until the backend has been offered n heartbeats,
// refused ones included.
func waitForAttempts(t *testing.T, b *backend, n int) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b.attemptCount() >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("waited for %d heartbeat attempts, the backend saw %d", n, b.attemptCount())
}

// The whole point of the buffer: an outage of the supervision service outlives the
// agent process, keeps its five-minute grain, and is filed in order when the
// backend comes back.
func TestAnOutageSurvivesARestartAndIsFiledOnRecovery(t *testing.T) {
	b := newBackend(t)
	home := t.TempDir()
	b.setFailing(true)

	now := epoch
	clock := func() time.Time { return now }

	// Three buckets of a first process, none of them delivered.
	first := NewRecorder(epoch)
	before, _ := reporterInHome(t, b, first, home, clock)
	for i := range 3 {
		first.Request()
		now = epoch.Add(time.Duration(i+1) * 5 * time.Minute)
		before.report(t.Context())
	}
	if got, _, _ := b.received(); len(got) != 0 {
		t.Fatalf("the backend accepted %d heartbeats it was meant to refuse", len(got))
	}

	// The process ends here — a reboot, an update, a laptop closed. A new recorder
	// and a new reporter over the same directory is what that looks like from the
	// backend's side.
	second := NewRecorder(now)
	after, _ := reporterInHome(t, b, second, home, clock)
	if after.queue.pending() != 3 {
		t.Fatalf("%d buckets survived the restart, want 3", after.queue.pending())
	}

	second.Request()
	now = now.Add(5 * time.Minute)
	b.setFailing(false)
	after.report(t.Context())

	got, _, enrolments := b.received()
	if len(got) != 4 {
		t.Fatalf("the backend saw %d buckets, want 4 — three buffered and one new", len(got))
	}
	if sizes := b.batchSizes(); len(sizes) != 1 || sizes[0] != 4 {
		t.Errorf("the accepted requests carried %v buckets, want one request of 4: a backlog "+
			"is filed in a batch, not one request per bucket", sizes)
	}
	if enrolments != 1 {
		t.Errorf("enrolled %d times, want 1: the identity survives a restart too", enrolments)
	}

	// In order, each covering its own five minutes.
	for i, hb := range got {
		want := epoch.Add(time.Duration(i) * 5 * time.Minute)
		if !hb.Window.Start.Equal(want) {
			t.Errorf("heartbeat %d covers from %v, want %v", i, hb.Window.Start, want)
		}
		if hb.Counters.Requests != 1 {
			t.Errorf("heartbeat %d carries %d requests, want 1", i, hb.Counters.Requests)
		}
	}

	// Two process starts, each counted in the bucket it happened in — which is the
	// thing StartedAt alone cannot say, because it only ever shows the current one.
	if got[0].Counters.Restarts != 1 {
		t.Errorf("the first bucket reports %d restarts, want 1", got[0].Counters.Restarts)
	}
	if got[3].Counters.Restarts != 1 {
		t.Errorf("the bucket after the restart reports %d restarts, want 1",
			got[3].Counters.Restarts)
	}
	if total := got[1].Counters.Restarts + got[2].Counters.Restarts; total != 0 {
		t.Errorf("the buckets in between report %d restarts, want 0", total)
	}

	// And the queue is gone from disk, or the next start would file it all again and
	// double every number on the dashboard.
	queued, err := loadBuffer(filepath.Join(home, "buffer.json"))
	if err != nil {
		t.Fatal(err)
	}
	if queued.pending() != 0 {
		t.Errorf("%d buckets are still buffered after delivery", queued.pending())
	}
}

// A backlog is not made to wait an interval between attempts, or a week of buckets
// would take a week to file. Run keeps sending until the queue is empty.
func TestRunDrainsABacklogWithoutWaitingAnInterval(t *testing.T) {
	b := newBackend(t)
	home := t.TempDir()
	r := NewRecorder(epoch)

	// More than one attempt's worth, so draining it has to take several passes.
	queue, err := loadBuffer(filepath.Join(home, "buffer.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range maxBucketsPerRequest + 3 {
		at := epoch.Add(time.Duration(i) * 5 * time.Minute)
		queue.add(bucketAt(at, 1), at)
	}
	if err := queue.save(); err != nil {
		t.Fatal(err)
	}

	rep, _ := reporterInHome(t, b, r, home, time.Now) // interval: one hour
	ctx, cancel := context.WithCancel(t.Context())
	// Waited for, not just cancelled: Run writes the buffer file on its way out, and
	// a test whose temporary directory is removed underneath it fails on cleanup
	// rather than on anything it was checking.
	stopped := runInBackground(t, rep, ctx)
	t.Cleanup(func() { cancel(); stopped() })

	// All of them, plus the bucket Run closes at start. With the interval set to an
	// hour, a second pass can only be the backlog rule.
	waitForHeartbeats(t, b, maxBucketsPerRequest+4)
}

// A kill this process cannot handle — SIGKILL, a power cut, a battery at zero —
// costs at most one snapshot interval instead of a whole bucket. The counters
// written between the intervals that close buckets are recovered by the next run
// and filed as their own bucket.
func TestAKilledProcessLosesOnlyWhatWasNotSnapshotted(t *testing.T) {
	b := newBackend(t)
	home := t.TempDir()

	now := epoch
	clock := func() time.Time { return now }

	killed := NewRecorder(epoch)
	before, _ := reporterInHome(t, b, killed, home, clock)
	before.collect() // the bucket carrying the process start, delivered below

	// Three minutes into the next bucket, with work done and no interval reached.
	killed.Request()
	killed.Masked(map[pii.Category]int{pii.CatEmail: 5})
	now = epoch.Add(3 * time.Minute)
	before.snapshot()

	// And here the process dies. Nothing closed the bucket it was filling.
	survivor := NewRecorder(now)
	after, _ := reporterInHome(t, b, survivor, home, clock)

	now = epoch.Add(8 * time.Minute)
	after.report(t.Context())

	got, _, _ := b.received()
	if len(got) != 3 {
		t.Fatalf("the backend saw %d buckets, want 3 — the first, the snapshotted one, "+
			"and this run's", len(got))
	}
	partial := got[1]
	if partial.Counters.Requests != 1 || partial.Counters.Masked["EMAIL"] != 5 {
		t.Errorf("the recovered bucket carries %+v, want the work done before the kill",
			partial.Counters)
	}
	// Its window ends at the snapshot, not at the moment the next process started:
	// the period after the snapshot was genuinely not measured, and a window
	// claiming it would have the backend dividing by time it never saw.
	if !partial.Window.End.Equal(epoch.Add(3 * time.Minute)) {
		t.Errorf("the recovered bucket ends at %v, want the last snapshot at %v",
			partial.Window.End, epoch.Add(3*time.Minute))
	}

	// Filed once. Delivered twice — once from the live entry and once from the
	// bucket that closed over it — every number for that period would be doubled.
	total := 0
	for _, hb := range got {
		total += hb.Counters.Requests
	}
	if total != 1 {
		t.Errorf("the backend counted %d requests in total, want 1: the snapshotted "+
			"counters must not be filed twice", total)
	}
}

// The interval that closes a bucket must clear the live entry with it. This is the
// double-delivery hazard the whole snapshot mechanism rests on: the counters are on
// disk in two places for as long as it takes, and only one of them is still true.
func TestClosingABucketClearsTheLiveEntry(t *testing.T) {
	b := newBackend(t)
	home := t.TempDir()
	path := filepath.Join(home, "buffer.json")

	now := epoch
	r := NewRecorder(epoch)
	rep, _ := reporterInHome(t, b, r, home, func() time.Time { return now })

	r.Request()
	now = epoch.Add(30 * time.Second)
	rep.snapshot()

	// Read as the files themselves, not through loadBuffer: loading deliberately
	// turns the live entry into an ordinary bucket, so this is the only view in
	// which the distinction this test is about still exists.
	live, found := readLiveFile(t, path)
	if !found || live.Counters.Requests != 1 {
		t.Fatalf("the snapshot did not reach its file: found=%v %+v", found, live)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the queue file was written by a snapshot: nothing has reached an " +
			"interval yet, and rewriting the whole backlog every thirty seconds is " +
			"what the second file exists to avoid")
	}

	now = epoch.Add(5 * time.Minute)
	rep.collect()

	if _, found := readLiveFile(t, path); found {
		t.Error("the live entry survived the interval that closed it — the same " +
			"counters are then on disk twice, and the next process files both")
	}
	stored := readBufferFile(t, path)
	if len(stored.Buckets) != 1 || stored.Buckets[0].Counters.Requests != 1 {
		t.Errorf("the file holds %d closed buckets, want 1 carrying the request once",
			len(stored.Buckets))
	}

	// And nothing is written again while nothing is counted: an idle workstation
	// must not rewrite the same bytes every thirty seconds for the life of the
	// process.
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	now = epoch.Add(6 * time.Minute)
	rep.snapshot()
	again, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !again.ModTime().Equal(stat.ModTime()) {
		t.Error("an idle snapshot rewrote the buffer file")
	}
	if _, found := readLiveFile(t, path); found {
		t.Error("an idle snapshot wrote a bucket in progress that holds nothing")
	}
}
