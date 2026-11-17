package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/telemetry"
	pkgtelemetry "github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
)

// DefaultBackendURL is the backend the settings page's enrolment form starts from.
//
// Served to the page rather than written in it, as every closed set a surface draws
// is, and only ever a suggestion: nothing reports there until somebody presents a
// token there. An agent pointed elsewhere by EnvBackendURL offers that instead.
const DefaultBackendURL = "https://backend.neverseen.cloud"

// supervision is the reporter this agent runs, if it runs one, and the one way to
// gain one without a restart: the settings page's enrolment (POST /enrol).
//
// An agent with no backend still has no reporter at all — reporter stays nil until
// a token has actually been traded for an identity, so "the agent works standalone"
// goes on describing what was built. What this adds is that the trade can be made
// from the page, where a refusal is a sentence in front of the person holding the
// token rather than a line in a log after two edits of a file and a restart.
type supervision struct {
	// enrolling serialises POST /enrol, which holds it across a request to the
	// backend. Apart from mu, so /today — polled by the menu bar every few seconds —
	// never waits on a backend.
	enrolling sync.Mutex

	mu       sync.Mutex
	reporter *telemetry.Reporter
	backend  string // where reporter reports; "" with none

	// file is where a backend chosen on the page is kept, so the next start reports
	// there too. Empty stores nothing — see supervisionFile.
	file string

	// build makes the reporter for a backend. Nil on a server that was not assembled
	// by FromEnv, which then refuses the route.
	build func(backend string) (*telemetry.Reporter, error)

	// fresh starts the recorder's window, dropping what it counted so far.
	fresh func()

	// preview is the bucket in progress, as it stands at an instant and without
	// closing it. Nil where build is.
	preview func(now time.Time) pkgtelemetry.Bucket

	// start runs a reporter for as long as the agent serves. Nil before run and
	// after it, and a reporter built meanwhile is started by run or not at all.
	start func(*telemetry.Reporter)

	log *slog.Logger
}

// supervisionFile is where the page's choice of backend survives a restart: beside
// the policy file and stored only where that one is, the rule menubar.json follows —
// which is also what keeps a test that stores nothing from resuming somebody's
// supervision out of their home directory.
func supervisionFile(policyFile string) string {
	if policyFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(policyFile), "supervision.json")
}

// storedSupervision is the shape of supervision.json. The backend and nothing else:
// the identity it issued is in the identity file, and the token is never stored.
type storedSupervision struct {
	Backend string `json:"backend"`
}

// run runs the reporter — the one configured at start-up, or one the page enrols
// later — until ctx is cancelled and the last report has been attempted.
func (s *supervision) run(ctx context.Context) {
	var running sync.WaitGroup

	s.mu.Lock()
	s.start = func(r *telemetry.Reporter) { running.Go(func() { r.Run(ctx) }) }
	if s.reporter != nil {
		s.start(s.reporter)
	}
	s.mu.Unlock()

	<-ctx.Done()

	// Cleared under the lock every start is made under, so nothing is added to the
	// group once it is being waited on.
	s.mu.Lock()
	s.start = nil
	s.mu.Unlock()
	running.Wait()
}

// supervisionState is what the settings page is told about supervision. Behind the
// control key with the rest of agentState: where an agent reports is its user's
// business, not every local process's.
type supervisionState struct {
	// Backend is where this agent reports, empty while it reports nowhere.
	Backend string `json:"backend,omitempty"`

	// Offered is the address the enrolment form starts from.
	Offered string `json:"offered"`

	// AgentID is the identity a backend issued. Empty means not enrolled, which is
	// what the page shows its form on.
	AgentID string `json:"agent_id,omitempty"`

	// DeliveredAt is the last report the backend took.
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`

	// Problem is why the last attempt failed, empty once one succeeds.
	Problem string `json:"problem,omitempty"`

	// EnrolledAt is when the identity was written to this disk.
	EnrolledAt *time.Time `json:"enrolled_at,omitempty"`

	// NextAt is when the next delivery is attempted.
	NextAt *time.Time `json:"next_at,omitempty"`

	// Queued is how many reports wait to be delivered, and OldestQueued where the
	// first of them starts. Counts and dates: what waits is in buffer.json.
	Queued       int        `json:"queued,omitempty"`
	OldestQueued *time.Time `json:"oldest_queued,omitempty"`
}

// dated is a date for the page, or nothing for one that was never set: a zero time
// travels as year 1, which a page would draw.
func dated(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (s *supervision) state() supervisionState {
	s.mu.Lock()
	reporter, backend := s.reporter, s.backend
	s.mu.Unlock()

	state := supervisionState{Backend: backend, Offered: DefaultBackendURL}
	if reporter == nil {
		return state
	}
	state.Offered = backend
	status := reporter.Status()
	state.AgentID, state.Problem, state.Queued = status.AgentID, status.Problem, status.Queued
	state.DeliveredAt, state.EnrolledAt = dated(status.DeliveredAt), dated(status.EnrolledAt)
	state.NextAt, state.OldestQueued = dated(status.NextAt), dated(status.OldestQueued)
	return state
}

var (
	errNotEnrolled     = errors.New("this workstation reports nowhere: there is nothing to send")
	errAlreadyEnrolled = errors.New("this workstation is already enrolled")
	errNotAssembled    = errors.New("this agent was not started in a way that can be supervised")
)

// otherBackendError is an enrolment asked of a backend other than the one this agent
// was configured with. Refused rather than followed: the reporter in place sends to
// the configured one, and an identity issued elsewhere would be refused there on
// every heartbeat.
type otherBackendError struct{ configured string }

func (e otherBackendError) Error() string {
	return fmt.Sprintf("this agent reports to %s, set by %s; enrol there, or change that setting and restart",
		e.configured, EnvBackendURL)
}

// enrol trades token for an identity at backend and starts reporting there.
func (s *supervision) enrol(ctx context.Context, backend, token string) error {
	s.enrolling.Lock()
	defer s.enrolling.Unlock()

	if s.build == nil {
		return errNotAssembled
	}

	s.mu.Lock()
	reporter, configured := s.reporter, s.backend
	s.mu.Unlock()

	fresh := reporter == nil
	switch {
	case fresh:
		var err error
		if reporter, err = s.build(backend); err != nil {
			return err
		}
	case reporter.Status().AgentID != "":
		return errAlreadyEnrolled
	case configured != backend:
		return otherBackendError{configured}
	}

	id, err := reporter.Enrol(ctx, token)
	if err != nil {
		// A reporter built for this attempt is dropped with it: an agent whose
		// enrolment was refused is still one with no reporter at all.
		return err
	}
	if !fresh {
		return nil
	}

	// What the agent counted before anybody enrolled it is not reported. The window
	// has been open since the process started, and filed as it stands the first
	// bucket would hand a backend the days before the person agreed to one.
	s.fresh()

	if s.file != "" {
		if err := writeStateFile(s.file, ".supervision-*.json", storedSupervision{Backend: backend}); err != nil {
			s.log.Warn("enrolled, but where this agent reports could not be stored: "+
				"the next start will not report until it is set in "+EnvBackendURL,
				"file", s.file, "error", err)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reporter, s.backend = reporter, backend
	if s.start != nil {
		s.start(reporter)
	}
	s.log.Info("enrolled from the settings page, reporting every "+telemetry.DefaultInterval.String(),
		"backend", backend, "agent", id.AgentID)
	return nil
}

// enrolRequest is what POST /enrol carries.
type enrolRequest struct {
	Backend string `json:"backend"`
	Token   string `json:"token"`
}

// enrolMaxBytes bounds POST /enrol: an address and a token.
const enrolMaxBytes = 4096

// handleEnrol enrols this workstation with a supervision backend, from the settings
// page.
//
// Behind the control key, like everything that changes what the agent does: it
// decides where a description of this workstation is sent every five minutes. The
// token is read, presented once and dropped — never stored and never logged; what is
// kept is the identity it bought, in the identity file, as when the token came from
// the environment.
func (s *Server) handleEnrol(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "neverseen: use POST to enrol this workstation", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorised(w, r) {
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, enrolMaxBytes+1))
	if err != nil || len(body) > enrolMaxBytes {
		http.Error(w, "neverseen: the request is too large or unreadable", http.StatusBadRequest)
		return
	}
	var req enrolRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, `neverseen: send {"backend": "https://…", "token": "…"}`, http.StatusBadRequest)
		return
	}
	backend := strings.TrimRight(strings.TrimSpace(req.Backend), "/")
	token := strings.TrimSpace(req.Token)
	if token == "" {
		http.Error(w, "neverseen: an enrolment token is required", http.StatusBadRequest)
		return
	}
	if err := checkBackend(backend); err != nil {
		http.Error(w, "neverseen: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}

	// Not the request's own context: a tab closed while the backend is answering
	// would abandon a trade the backend has already made, and the token is
	// single-use — the workstation would hold a spent token and no identity. The
	// reporter's client bounds the wait.
	err = s.supervision.enrol(context.WithoutCancel(r.Context()), backend, token)

	var refused *telemetry.RefusedError
	var other otherBackendError
	switch {
	case err == nil:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, s.supervision.state())
	case errors.Is(err, errAlreadyEnrolled), errors.As(err, &other):
		http.Error(w, "neverseen: "+err.Error(), http.StatusConflict)
	case errors.Is(err, errNotAssembled):
		http.Error(w, "neverseen: "+err.Error(), http.StatusServiceUnavailable)
	case errors.As(err, &refused) && refused.Status == http.StatusUnauthorized:
		// The backend gives one answer for unknown, spent and expired, on purpose.
		http.Error(w, "neverseen: the backend did not accept that enrolment token: "+
			"unknown, already used or expired", http.StatusUnprocessableEntity)
	case errors.As(err, &refused) && refused.Status == http.StatusPaymentRequired:
		http.Error(w, "neverseen: the backend says this organisation is suspended; "+
			"the agent keeps masking, unsupervised", http.StatusUnprocessableEntity)
	default:
		http.Error(w, "neverseen: could not enrol with "+backend+": "+err.Error(), http.StatusBadGateway)
	}
}

// handleReport shows what the next report holds (GET) and has the waiting ones sent
// now (POST), for the settings page.
//
// Behind the control key. The GET is the claim "counts, never content" made checkable
// by the person it is made to: the bucket in progress and the state, in the shape a
// heartbeat carries them, read without closing the window. On an agent that reports
// nowhere it answers the same, because that is when somebody decides whether to
// enrol — and what it shows then is counted since the agent started, which an
// enrolment drops (fresh).
//
// TODO: the state shown leaves out whether rtk answers, which the reporter's own
// goroutine holds; the report sent carries it. The upgrade is the reporter serving
// the preview itself, under a lock that field does not have today.
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "neverseen: use GET to see the next report, POST to send the waiting ones", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorised(w, r) {
		return
	}
	sup := s.supervision
	w.Header().Set("Cache-Control", "no-store")

	if r.Method == http.MethodGet {
		if sup.preview == nil {
			http.Error(w, "neverseen: "+errNotAssembled.Error(), http.StatusServiceUnavailable)
			return
		}
		now := time.Now()
		writeJSON(w, pkgtelemetry.HeartbeatBatch{
			Schema:  pkgtelemetry.SchemaVersion,
			AgentID: sup.state().AgentID,
			SentAt:  now,
			State:   s.State(),
			Buckets: []pkgtelemetry.Bucket{sup.preview(now)},
		})
		return
	}

	sup.mu.Lock()
	reporter := sup.reporter
	sup.mu.Unlock()
	if reporter == nil || reporter.Status().AgentID == "" {
		http.Error(w, "neverseen: "+errNotEnrolled.Error(), http.StatusConflict)
		return
	}
	reporter.SendNow()
	writeJSON(w, sup.state())
}

// checkBackend refuses an address the token must not be sent to: one that is not an
// absolute http(s) URL, and clear HTTP to anything but this machine — the token is a
// credential, and every heartbeat after it names this workstation.
func checkBackend(backend string) error {
	u, err := url.Parse(backend)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("%q is not a backend address; it looks like %s", backend, DefaultBackendURL)
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fmt.Errorf("%s is clear HTTP to another machine; use https", backend)
		}
	}
	return nil
}
