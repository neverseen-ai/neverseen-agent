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

	"github.com/cloakfleet/cloakfleet/pkg/telemetry"
)

// DefaultInterval is how often a supervised agent reports.
//
// Five minutes is short enough that a security officer sees an agent stop within
// one dashboard refresh, and long enough that a laptop asleep for an afternoon
// does not come back with hundreds of windows to send.
const DefaultInterval = 5 * time.Minute

// maxWindowAge bounds how long a window can be carried across failed sends.
//
// Past it the window is abandoned and counted as dropped. The bound exists
// because the alternative is unbounded: a backend down for a week would have the
// agent accumulating a week of counters in memory and then filing one heartbeat
// claiming to cover it, which is both a growing process and a report nobody can
// read as a rate.
const maxWindowAge = 6 * time.Hour

// Endpoint paths on the backend.
const (
	enrolPath     = "/v1/enrol"
	heartbeatPath = "/v1/heartbeat"
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

	recorder *Recorder
	state    func() telemetry.State
	interval time.Duration
	log      *slog.Logger

	// now is time.Now, replaceable so a test can drive windows without waiting.
	now func() time.Time
}

// Config is what a Reporter needs.
type Config struct {
	// BaseURL is the backend. Required.
	BaseURL string
	// EnrolmentToken is presented once, if this agent has no identity yet.
	EnrolmentToken string
	// IdentityFile is where the issued identity is kept.
	IdentityFile string

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
	}

	r := &Reporter{
		client:   cfg.Client,
		baseURL:  cfg.BaseURL,
		token:    cfg.EnrolmentToken,
		identity: cfg.IdentityFile,
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

// Run reports until the context is cancelled.
//
// Every failure is logged and the loop continues. Nothing in here returns an
// error to a caller, because there is no caller who could act on one: the agent's
// job is masking, and a supervision backend that cannot be reached must not stop
// it — or the security control would be taken down by the tool that monitors it.
func (r *Reporter) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	// Once immediately, before the first tick. It enrols, so a freshly installed
	// agent appears in the fleet view within seconds instead of after five
	// minutes — and an install that cannot reach the backend says so at once
	// rather than looking fine until somebody checks. The window it files is
	// empty, which is itself the useful fact that the path works.
	r.reportOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			// One last report on the way out, so a clean shutdown does not throw
			// away the window it was in the middle of.
			r.reportOnce(context.WithoutCancel(ctx))
			return
		case <-ticker.C:
			r.reportOnce(ctx)
		}
	}
}

// reportOnce takes the window and sends it, putting it back if it could not go.
func (r *Reporter) reportOnce(ctx context.Context) {
	id, err := r.ensureEnrolled(ctx)
	if err != nil {
		// Not enrolled means nothing can be sent, and the window is kept for
		// the next attempt rather than thrown away.
		r.log.Warn("not enrolled with the backend, keeping the window", "error", err)
		return
	}

	counters, window := r.recorder.Take(r.now())
	hb := telemetry.Heartbeat{
		Schema:   telemetry.SchemaVersion,
		AgentID:  id.AgentID,
		SentAt:   r.now(),
		Window:   window,
		State:    r.state(),
		Counters: counters,
	}

	if err := r.send(ctx, id, hb); err != nil {
		if age := r.now().Sub(window.Start); age > maxWindowAge {
			r.recorder.Drop()
			r.log.Error("abandoning a window the backend never accepted",
				"age", age.Round(time.Minute), "error", err)
			return
		}
		r.recorder.Restore(counters, window)
		r.log.Warn("heartbeat failed, will retry with the next window", "error", err)
		return
	}

	r.log.Debug("heartbeat sent", "requests", counters.Requests, "categories", len(counters.Masked))
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

func (r *Reporter) send(ctx context.Context, id Identity, hb telemetry.Heartbeat) error {
	body, err := json.Marshal(hb)
	if err != nil {
		return fmt.Errorf("encode the heartbeat: %w", err)
	}

	key, err := hex.DecodeString(id.Key)
	if err != nil {
		return fmt.Errorf("the stored key is not hex: %w", err)
	}

	_, err = r.post(ctx, heartbeatPath, body, map[string]string{
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
