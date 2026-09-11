package telemetry

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
)

// DefaultInterval is how often a supervised agent reports.
//
// Five minutes is short enough that a security officer sees an agent stop within
// one dashboard refresh, and long enough that a laptop asleep for an afternoon
// does not come back with hundreds of windows to send.
const DefaultInterval = 5 * time.Minute

// maxWindowAge bounds how long an undelivered bucket is kept.
//
// Past it the bucket is abandoned and counted as dropped. Seven days, because the
// thing being survived is an outage of the supervision service, and one that ends
// on the Monday after it started is an ordinary outcome for a hosted service — a
// bound that expired first would turn a supplier's bad weekend into a hole in the
// customer's audit trail.
//
// It can be that long because a bucket is small and the queue is bounded twice
// over: maxBufferedBuckets caps how many are held whatever the interval, and each
// one is a handful of integers and two maps bounded by the catalogue and by the
// models seen. A week of them is on the order of a megabyte, which is why the file
// is written in two parts — see the buffer's own comments.
//
// The bound still exists, because "carry it forever" is not a decision anybody
// made: past a week the counters describe a period nobody is still investigating,
// and abandoning them explicitly is what puts the loss on the record.
const maxWindowAge = 7 * 24 * time.Hour

// snapshotInterval is how often the bucket in progress is written to disk.
//
// A hard kill cannot be caught: SIGKILL, a power cut and a battery reaching zero
// all end the process with no chance to flush, so the only thing that bounds what
// they cost is having written the counters down recently. Half a minute of
// counters is a bound worth having; and nothing is written at all when nothing was
// counted, so an idle workstation does not rewrite the same bytes every thirty
// seconds for the life of the process.
const snapshotInterval = 30 * time.Second

// suspendAfter is how far the wall clock may move between two turns of the
// reporter's loop before the period is treated as one this agent did not measure.
//
// The loop touches something every snapshotInterval, and the longest a turn can
// hold it is the client's timeout — thirty seconds each, so a healthy agent never
// shows a gap of more than a minute. Four snapshots is therefore not scheduling
// jitter: the process was not running. A closed laptop is the ordinary cause, a
// paused virtual machine, a stopped process and a wall clock stepped forward the
// others, and the response is the same for all four because the fact is the same
// — nothing was watching, and no window may say otherwise.
//
// Deliberately below the reporting interval, so an ordinary five minutes of
// waiting can never be mistaken for one.
const suspendAfter = 2 * time.Minute

// Endpoint paths on the backend.
const (
	enrolPath      = "/v1/enrol"
	heartbeatsPath = "/v1/heartbeats"
)

// Reporter enrols once and then reports on an interval.
//
// It exists only when a backend is configured. An agent with no backend has no
// Reporter at all rather than one that quietly does nothing — there is then no
// code path to get wrong, and "the agent works standalone" is a statement about
// what was built rather than about what was disabled.
type Reporter struct {
	client   *http.Client
	baseURL  string
	token    string
	identity string // path to the identity file

	queue    *buffer // buckets not yet delivered, mirrored to disk
	recorder *Recorder

	// snapshotted is the recorder's change count at the last snapshot, so an
	// unchanged window is not written again.
	snapshotted uint64
	state       func() telemetry.State
	interval    time.Duration
	log         *slog.Logger

	// now is time.Now, replaceable so a test can drive windows without waiting.
	now func() time.Time

	// awake is the last moment the loop below was known to be running, which is
	// the moment a window is cut at when the machine turns out to have slept.
	awake time.Time
}

// Config is what a Reporter needs.
type Config struct {
	// BaseURL is the backend. Required.
	BaseURL string
	// EnrolmentToken is presented once, if this agent has no identity yet.
	EnrolmentToken string
	// IdentityFile is where the issued identity is kept.
	IdentityFile string
	// BufferFile is where buckets the backend has not taken are kept, so an outage
	// of the supervision service outlives the agent process. Required: a reporter
	// that buffered only in memory would be a second, less durable behaviour that
	// nothing exercises.
	BufferFile string

	Recorder *Recorder
	// State is asked for on every heartbeat, so what is reported is what the
	// agent is applying now rather than what it was configured with at startup.
	State func() telemetry.State

	// Interval defaults to DefaultInterval.
	Interval time.Duration
	Logger   *slog.Logger
	// Client defaults to one with a timeout. A reporter without a timeout can
	// hang on a hung backend and never report again.
	Client *http.Client
	// Now defaults to time.Now.
	Now func() time.Time
}

// NewReporter builds a reporter, refusing a configuration it could not act on:
// a reporter with no backend, no recorder or nowhere to keep its identity would
// run happily and report nothing.
func NewReporter(cfg Config) (*Reporter, error) {
	switch {
	case cfg.BaseURL == "":
		return nil, fmt.Errorf("a backend URL is required")
	case cfg.Recorder == nil:
		return nil, fmt.Errorf("a recorder is required")
	case cfg.State == nil:
		return nil, fmt.Errorf("a state function is required")
	case cfg.IdentityFile == "":
		return nil, fmt.Errorf("an identity file path is required")
	case cfg.BufferFile == "":
		return nil, fmt.Errorf("a buffer file path is required")
	}

	// Read here rather than lazily at the first tick, so a queue an earlier run
	// could not deliver is in hand before anything else happens — including the
	// bucket this process's own start is counted in.
	queue, err := loadBuffer(cfg.BufferFile)
	if err != nil {
		// Logged by the caller's logger, not fatal: an agent that refused to start
		// because it could not read a counters file would be telemetry taking down
		// the masking. The queue comes back empty and the next save overwrites it.
		if cfg.Logger != nil {
			cfg.Logger.Error("cannot read the buffered buckets, carrying on without them",
				"error", err)
		}
	}

	r := &Reporter{
		client:   cfg.Client,
		baseURL:  cfg.BaseURL,
		token:    cfg.EnrolmentToken,
		identity: cfg.IdentityFile,
		queue:    queue,
		recorder: cfg.Recorder,
		state:    cfg.State,
		interval: cfg.Interval,
		log:      cfg.Logger,
		now:      cfg.Now,
	}
	if r.client == nil {
		r.client = &http.Client{Timeout: 30 * time.Second}
	}
	if r.interval <= 0 {
		r.interval = DefaultInterval
	}
	if r.log == nil {
		r.log = slog.New(slog.DiscardHandler)
	}
	if r.now == nil {
		r.now = time.Now
	}
	return r, nil
}

// Run collects and reports until the context is cancelled.
//
// Two cadences, deliberately separate. A bucket is closed every interval whatever
// the backend is doing, so the record keeps its five-minute grain through an
// outage; sending is retried on its own ladder. Fused — one timer that both closed
// the window and sent it — the window boundaries moved with the backend's health,
// and an outage came back as one bucket covering it.
//
// Every failure is logged and the loop continues. Nothing in here returns an error
// to a caller, because there is no caller who could act on one: the agent's job is
// masking, and a supervision backend that cannot be reached must not stop it — or
// the security control would be taken down by the tool that monitors it.
func (r *Reporter) Run(ctx context.Context) {
	// Closes a bucket every interval, regardless of whether anything can be sent.
	collect := time.NewTicker(r.interval)
	defer collect.Stop()

	// Fires at once, so the first report happens before the first wait. It enrols,
	// so a freshly installed agent appears in the fleet view within seconds instead
	// of after five minutes — and an install that cannot reach the backend says so
	// at once rather than looking fine until somebody checks. The bucket it files is
	// empty, which is itself the useful fact that the path works.
	send := time.NewTimer(0)
	defer send.Stop()

	// One bucket closed before the first send, so there is something to file: it
	// carries this process's start, which is how a restart reaches the fleet view
	// in seconds rather than at the next interval.
	r.collect()

	// Dated here rather than at construction: a reporter built and started later
	// would otherwise read the delay between the two as a sleep.
	r.awake = r.now()

	// Writes the bucket in progress, so a hard kill costs at most this much.
	snapshot := time.NewTicker(snapshotInterval)
	defer snapshot.Stop()

	// Zero while the backend is answering, which is what makes the ladder a ladder:
	// each consecutive failure lengthens the next wait, and one success puts the
	// agent back on its configured cadence.
	var backoff time.Duration

	for {
		select {
		case <-ctx.Done():
			// The bucket in progress is closed and one delivery is attempted, so a
			// clean shutdown neither loses the period it was in the middle of nor
			// leaves a queue on disk it could have emptied.
			out := context.WithoutCancel(ctx)
			r.collect()
			r.drain(out)
			return

		case <-collect.C:
			// A wake has just closed the window at the moment the machine stopped
			// and opened a fresh one; closing that one too would file an empty
			// bucket over a zero-length window, which a backend computing a rate
			// divides by.
			if !r.wake() {
				r.collect()
			}

		case <-snapshot.C:
			r.wake()
			r.snapshot()

		case <-send.C:
			r.wake()
			delivered := r.drain(ctx)
			switch {
			case !delivered:
				backoff = r.retryAfter(backoff)
				r.log.Warn("the backend did not take the heartbeat, retrying", "in", backoff)
				send.Reset(backoff)
			case r.queue.pending() > 0:
				// Still a backlog, so the next attempt is not made to wait an
				// interval: a week of buckets delivered at one attempt per five
				// minutes would take a week again.
				backoff = 0
				send.Reset(time.Second)
			default:
				backoff = 0
				send.Reset(r.interval)
			}
		}
	}
}

// wake cuts the open window when the loop turns out not to have run for a while.
//
// A laptop that sleeps stops this process without ending it: no ticker fires, and
// the window opened before the lid closed is still open when it opens again. Left
// alone it is delivered as one window over the whole night — eight hours arriving
// as a single report, which the backend then draws as eight hours of coverage
// while its own silence alarm was going off about that very machine. The agent was
// alive, but it was measuring nothing, and those are not the same claim.
//
// So the window is closed at the last moment this loop was known to be running,
// and the next one opens at the wake. What lies between the two is in no window at
// all, which is exactly what it was: a period nobody can vouch for.
//
// The counters lose nothing. Nothing is proxied on a sleeping machine, so the
// bucket closed here holds every exchange there was.
//
// TODO: the sleep is noticed on the first timer to fire after the resume, up to
// snapshotInterval later, and a request proxied in that gap lands in the window
// closed here — dated before it happened. Bounded at thirty seconds; closing the
// gap means reading the clock on the request path.
//
// Reports whether it closed a window, so the caller does not close the fresh one
// on top of it.
func (r *Reporter) wake() bool {
	now, last := r.now(), r.awake
	r.awake = now
	slept := now.Sub(last)
	if slept <= suspendAfter {
		return false
	}
	r.log.Info("the machine was not running this agent, closing the window where it stopped",
		"for", slept.Round(time.Second), "measured_to", last)
	r.collectAt(last)
	r.recorder.Reopen(now)
	return true
}

// collect closes the current bucket and queues it.
//
// Queued rather than sent from here, because the two are independent: this runs on
// the interval whether or not the backend exists, and it is what keeps a bucket
// per five minutes through an outage.
func (r *Reporter) collect() { r.collectAt(r.now()) }

// collectAt closes the current bucket at a given instant, which is now for every
// caller but the one that has just found out the machine was asleep: that one
// closes the window at the last moment this agent was measuring, rather than at
// the moment it woke up.
func (r *Reporter) collectAt(at time.Time) {
	// Stale buckets go before the new one is taken, so the loss is folded into the
	// bucket being closed right now rather than into the one five minutes from now.
	// A loss nobody counted looks exactly like a quiet five minutes, which is the
	// whole reason the figure exists.
	r.queue.prune(at)
	if dropped := r.queue.takeDropped(); dropped > 0 {
		r.recorder.Drop(dropped)
		r.log.Error("abandoned buckets the backend never accepted", "count", dropped)
	}

	counters, window := r.recorder.Take(at)
	r.queue.add(bucket{Window: window, Counters: counters}, at)

	// The bucket in progress has just become a real one, so the live entry has to
	// go with it. Left behind, the same counters would be on disk twice and the
	// next process to read the file would deliver both — doubling every number for
	// that period on the dashboard.
	r.queue.setLive(nil)
	r.snapshotted = 0
	r.persist()
}

// snapshot writes the bucket in progress, so that a kill this process cannot
// handle costs at most one snapshotInterval of counters rather than a whole
// interval of them.
func (r *Reporter) snapshot() {
	counters, window, changes := r.recorder.Snapshot(r.now())
	if changes == r.snapshotted {
		// Nothing was counted since the last one, so the file already says this.
		return
	}

	r.queue.setLive(&bucket{Window: window, Counters: counters})
	if err := r.queue.saveLive(); err != nil {
		r.log.Warn("cannot write the bucket in progress", "error", err)
	}
	r.snapshotted = changes
}

// retryAfter is the wait before the next attempt, given the one before it:
// 1s, 5s, 10s, 20s, 40s and doubling on, never past the reporting interval.
//
// It starts short because most failures are not an outage — a backend being
// redeployed, a laptop's Wi-Fi coming back, one dropped connection — and waiting
// a whole interval to find that out has the fleet view calling a healthy agent
// silent for five minutes. It grows because a backend that is genuinely down must
// not be hit every second by every workstation in the fleet, which would turn an
// outage into an outage plus a load problem; each failed attempt is evidence the
// next one can afford to wait longer. And it is capped at the interval, so the
// worst case is the cadence the agent would have kept anyway — a ladder that grew
// past it would have a backend recovering after an hour waiting another hour to
// hear from anyone.
func (r *Reporter) retryAfter(previous time.Duration) time.Duration {
	var next time.Duration
	switch {
	case previous <= 0:
		next = time.Second
	// The first rung climbs by five rather than doubling: a retry one second after
	// a failure is worth making, a second one at two seconds almost never is.
	case previous < 5*time.Second:
		next = 5 * time.Second
	default:
		next = 2 * previous
	}
	return min(next, r.interval)
}

// drain delivers queued buckets in one batch, oldest first, and reports whether it
// got through.
//
// One request per attempt, not one per bucket. A week offline is two thousand
// buckets, and two thousand signed requests from every workstation in the fleet at
// the moment the service recovers is a recovery that ends in a second outage.
// Oldest first so a backend catching up sees the outage in the order it happened,
// and bounded per request so no single body can be too large to accept.
//
// The result is what the retry ladder reads. It never reaches the request path,
// which must not be able to notice any of this.
func (r *Reporter) drain(ctx context.Context) bool {
	id, err := r.ensureEnrolled(ctx)
	if err != nil {
		// Not enrolled means nothing can be sent, and the queue is kept rather than
		// thrown away. It counts as a failure, so an agent that cannot enrol climbs
		// the same ladder instead of retrying the enrolment every second for as long
		// as the backend is down.
		r.log.Warn("not enrolled with the backend, keeping the queued buckets", "error", err)
		return false
	}

	sending := r.queue.next()
	if len(sending) == 0 {
		// Nothing to file. Reaching enrolment was still worth it — that is what puts
		// a freshly installed agent in the fleet view — so this counts as a success
		// and the ladder resets.
		return true
	}

	batch := telemetry.HeartbeatBatch{
		Schema:  telemetry.SchemaVersion,
		AgentID: id.AgentID,
		SentAt:  r.now(),
		// Said once for the batch: it describes what the agent is applying now, and
		// stamping a week-old bucket with it would be no more true for being
		// repeated on every one of them.
		State:   r.state(),
		Buckets: make([]telemetry.Bucket, 0, len(sending)),
	}
	// Summed while the batch is built, for the line below. A count of buckets alone
	// cannot tell a heartbeat carrying an exchange from the empty one an idle agent
	// files every interval, and those are the two cases somebody comparing this log
	// against an empty dashboard has to separate.
	var requests, masked int
	for _, queued := range sending {
		batch.Buckets = append(batch.Buckets,
			telemetry.Bucket{Window: queued.Window, Counters: queued.Counters})
		requests += queued.Counters.Requests
		for _, count := range queued.Counters.Masked {
			masked += count
		}
	}

	if err := r.send(ctx, id, batch); err != nil {
		r.log.Warn("the batch was refused, its buckets stay queued",
			"buckets", len(sending), "queued", r.queue.pending(), "error", err)
		return false
	}

	// All or none: the backend accepts a batch whole, so a partial delivery is not
	// a state that exists. Forgotten only once it has them — dropping them before
	// the send would have an agent that crashed mid-request lose buckets that were
	// never delivered.
	r.queue.delivered(len(sending))
	r.persist()
	// Info rather than Debug: this is the only line that says the supervision path
	// actually works end to end, and at Debug it was invisible under the level every
	// command builds its logger with — so an agent reporting nothing and an agent
	// reporting emptily read identically, which is how a silent dashboard came to be
	// investigated from the backend's side first.
	r.log.Info("heartbeat delivered to the backend",
		"buckets", len(sending),
		"requests", requests,
		"masked", masked,
		"through", r.baseURL,
		"queued", r.queue.pending())
	return true
}

// persist mirrors the queue to disk. Failing to write it is logged and no more:
// the buckets are still in memory, and telemetry never stops the agent.
func (r *Reporter) persist() {
	if err := r.queue.save(); err != nil {
		r.log.Warn("cannot mirror the queued buckets to disk", "error", err)
	}
}

// ensureEnrolled returns this agent's identity, exchanging the enrolment token
// for one the first time.
func (r *Reporter) ensureEnrolled(ctx context.Context) (Identity, error) {
	if id, found, err := LoadIdentity(r.identity); err != nil {
		return Identity{}, err
	} else if found {
		return id, nil
	}

	if r.token == "" {
		return Identity{}, fmt.Errorf("no identity yet and no enrolment token configured")
	}

	body, err := json.Marshal(telemetry.EnrolRequest{
		Schema: telemetry.SchemaVersion,
		Token:  r.token,
		State:  r.state(),
	})
	if err != nil {
		return Identity{}, fmt.Errorf("encode the enrolment: %w", err)
	}

	resp, err := r.post(ctx, enrolPath, body, nil)
	if err != nil {
		return Identity{}, err
	}

	var issued telemetry.EnrolResponse
	if err := json.Unmarshal(resp, &issued); err != nil {
		return Identity{}, fmt.Errorf("parse the enrolment response: %w", err)
	}
	if issued.AgentID == "" || issued.Key == "" {
		return Identity{}, fmt.Errorf("the backend issued no agent id or key")
	}

	id := Identity{AgentID: issued.AgentID, Key: issued.Key}
	if err := SaveIdentity(r.identity, id); err != nil {
		// Refusing to carry on is deliberate. An agent that enrolled but could
		// not persist the result would enrol again on every heartbeat, and the
		// backend would fill with one agent per five minutes — each of them
		// counting against whatever the operator is paying for.
		return Identity{}, fmt.Errorf("persist the issued identity: %w", err)
	}

	r.log.Info("enrolled with the backend", "agent", id.AgentID)
	return id, nil
}

func (r *Reporter) send(ctx context.Context, id Identity, batch telemetry.HeartbeatBatch) error {
	body, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("encode the batch: %w", err)
	}

	key, err := hex.DecodeString(id.Key)
	if err != nil {
		return fmt.Errorf("the stored key is not hex: %w", err)
	}

	_, err = r.post(ctx, heartbeatsPath, body, map[string]string{
		telemetry.HeaderAgent: id.AgentID,
		// Signed by the contract's own function, which the backend verifies with.
		telemetry.HeaderSignature: telemetry.Sign(key, body),
	})
	return err
}

func (r *Reporter) post(ctx context.Context, path string, body []byte, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	// Bounded, because a misconfigured URL can point at anything at all and a
	// reporter must not be the thing that exhausts the machine's memory.
	answer, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read the backend's answer: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("the backend answered %d to %s", resp.StatusCode, path)
	}
	return answer, nil
}
