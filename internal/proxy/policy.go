package proxy

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
)

// The one route on this agent that changes what it does, and the only one that is
// authenticated.
//
// Everything else the agent serves is either a proxy hop carrying the caller's own
// credential, or /healthz and /test, which are read-only and carry nothing about
// anybody. Those are safe unauthenticated on the loopback interface because the
// worst they give a local process is a description of the configuration.
//
// This route is a different thing entirely: it switches masking off. Left open, any
// process on the workstation could disable the control — and so could a page in the
// browser, because a form post to 127.0.0.1 needs no permission from anybody. The
// consequence is silent: the traffic keeps flowing and stops being masked.
//
// What closes it is a shared secret read from a file only the user can read. A
// custom header is what a browser cannot set on a simple cross-origin request, so
// requiring one is what makes a page on the internet unable to reach this at all —
// not politeness about CORS, which the agent does not implement and must not.

// controlHeader carries the secret. Named rather than reusing the telemetry
// headers: that key authenticates this workstation to a backend, and reusing it
// would put a key with a remote meaning into a local exchange.
const controlHeader = "X-Neverseen-Control"

// authorised reports whether a request carries the control secret, and writes the
// refusal itself when it does not.
//
// One helper rather than the check written out per handler, because there are three
// routes behind it now and they are not equally forgiving of a drift. PUT /policy
// switches masking off, which is a future leak; POST /unmask reads the session
// mapping — token in, original out — which is an immediate one. A second copy of
// this check is the copy that comes to differ from the first, and the difference
// would be silent on exactly the route that matters most.
//
// Constant-time, because == on a secret returns at the first differing byte and this
// socket is reachable by every process on the workstation: a caller that can time a
// few thousand requests can walk the key out of it a byte at a time.
//
// An agent with no key refuses everything here. That is inherited from the route
// this was extracted from, and it is the safe direction: a key that could not be read
// or written must not degrade into accepting anything.
func (s *Server) authorised(w http.ResponseWriter, r *http.Request) bool {
	if s.controlKey == "" {
		http.Error(w, "neverseen: no control key on this agent, so this route refuses everything",
			http.StatusServiceUnavailable)
		return false
	}

	// Deliberately says nothing about what was wrong. A local process probing this
	// does not need to be told whether the header was missing or merely incorrect.
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(controlHeader)), []byte(s.controlKey)) != 1 {
		http.Error(w, "neverseen: not authorised", http.StatusForbidden)
		return false
	}
	return true
}

// policyRequest is what a surface sends to change what is masked.
//
// The whole set, not a toggle. Two surfaces can be looking at one agent — the menu
// bar and a browser tab on the test page — and a toggle is a read-modify-write
// whose halves interleave into a set neither of them asked for. Replacing the set
// makes the last writer's intention the state.
type policyRequest struct {
	// Off are the categories not to mask. Absent means none.
	Off []string `json:"off"`

	// Substitution is what a masked value looks like: "token" or "fake".
	Substitution string `json:"substitution"`

	// Locales are the country pattern sets to load. An empty list is a valid
	// state — the one an agent starts in when nothing is configured — so it cannot
	// double as "no change".
	Locales []string `json:"locales"`

	// SecretLevel is the weakest named secret to mask: "weak", "medium" or
	// "strong". Required, for the reason Substitution is: absent cannot mean
	// unchanged on a route that replaces the state, and a caller that sent
	// everything but this would silently move the agent back to masking every
	// ordinary word it finds.
	SecretLevel string `json:"secret_level"`
}

// handlePolicy replaces the set of categories the agent is not masking.
func (s *Server) handlePolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		// PUT rather than POST, because the request carries the whole state rather
		// than an increment, and saying so in the method is what tells a caller
		// that sending it twice is safe.
		w.Header().Set("Allow", http.MethodPut)
		http.Error(w, "neverseen: use PUT to replace the set of categories", http.StatusMethodNotAllowed)
		return
	}

	if !s.authorised(w, r) {
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 16*1024))
	if err != nil {
		http.Error(w, "neverseen: could not read the request", http.StatusBadRequest)
		return
	}

	var req policyRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "neverseen: the request is not a JSON object with an \"off\" list",
			http.StatusBadRequest)
		return
	}

	// One writer at a time. The detector's own state is atomic, but this handler is
	// a read-modify-write of the *file* — apply, then read the detector back, then
	// store — and two surfaces on one agent interleave the halves of it, which is
	// the hazard this route already names for the mapping.
	s.policyMu.Lock()
	defer s.policyMu.Unlock()

	changed, applied, err := applyPolicy(s.det, req)
	// Counted whatever the outcome, refusals included: State says what is switched
	// off, and this is what lets a fleet view see that somebody keeps trying.
	s.recorder.Policy(err == nil)
	if errors.Is(err, errPartialPolicy) {
		// A malformed request rather than a refused state: nothing was applied, and
		// there is nothing to persist or to log as a change.
		http.Error(w, "neverseen: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Recorded whatever the outcome below, and from the detector rather than from
	// this request. The route is not a transaction — a good locale selection with a
	// bad category leaves the locales applied and returns an error — so what has to
	// go to disk is what the agent *is* when this handler returns, or a restart
	// would undo half a change nobody could see had been half applied.
	//
	// Only when something was applied, though. A request refused before the first
	// change — a misspelled mode — is one the agent was never told, and writing over
	// it created the file for the first time and took the agent off its environment
	// configuration permanently, over a request that changed nothing. The file's
	// absence has to keep meaning "nobody has".
	if applied {
		s.persistPolicy()
	}

	if err != nil {
		http.Error(w, "neverseen: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}

	// The mapping is what the mode is read against, and it is read first: a value
	// the session has already seen keeps the shape it was first given, whatever the
	// mode now says. Left alone, a click on "fake" changed nothing anybody could see
	// — every value in the conversation had already been minted as a token, and on a
	// workstation where nothing sends a session header, that is every value the
	// agent has handled since it started.
	//
	// Best effort, and worth naming: a request already past vault.Load will save its
	// entries after this, and its own answer still expands. What the purge cannot do
	// is reach an exchange whose response has not come back yet — that one returns a
	// replacement nothing maps, one exchange wide.
	if changed {
		s.vault.Forget()
	}

	// Logged because it is the one request that changes what the agent does, and an
	// operator reading a log after the fact has to be able to see when the masking
	// changed and to what. Category names, locale codes and a mode — never a value.
	// The log rule holds here as everywhere.
	s.log.Warn("what this agent masks was changed",
		"off", req.Off,
		"locales", req.Locales,
		"substitution", s.det.Substitution().String(),
		"secret_level", s.det.SecretLevel().String(),
		"masking", s.det.Masking().String(),
		// Said out loud because it is the one part of this request that discards
		// state, and an unexpanded token in an answer is otherwise unexplainable.
		"sessions_cleared", changed)

	// The new state, from the agent rather than echoed back: a caller has to be
	// able to redraw from what is true rather than from what it asked for.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.healthNow())
}

// Policy is the whole of what a local surface can change about a running agent.
//
// All four together, because that is what the route takes: it replaces the state
// rather than patching it, since an empty locale list is a valid state and could not
// otherwise be told apart from "leave the locales alone". A caller therefore sends
// what the agent currently reports with its own change applied — which every surface
// here already has, from the poll it just did.
type Policy struct {
	// Off are the categories not to mask, by code.
	Off []string

	// Substitution is "token" or "fake". Required: the route refuses a request
	// without it rather than choosing one.
	Substitution string

	// Locales are the country pattern sets to load. Empty means none.
	Locales []string

	// SecretLevel is "weak", "medium" or "strong". Required, as Substitution is.
	SecretLevel string
}

// PolicyOf is the agent's current state as a Policy, for a caller about to change
// one part of it.
//
// Read from a Status the caller already has rather than fetched again, so the change
// it sends is against the state it was looking at.
func PolicyOf(s Status) Policy {
	return Policy{
		Off:          s.SwitchedOffCodes(),
		Substitution: s.Substitution,
		Locales:      s.Locales,
		SecretLevel:  s.SecretLevel,
	}
}

// SubstitutionModes are the modes this build offers, in the order a choice should be
// drawn: the reversible one first.
//
// Served from here rather than spelled out by each surface, for the reason the
// locales are: a menu offering "fake" to an agent that did not have it would fail on
// a name the menu itself suggested.
func SubstitutionModes() []string {
	return []string{detector.SubstitutionToken.String(), detector.SubstitutionFake.String()}
}

// SecretLevels are the levels this build offers, weakest first — the order the scale
// runs in, so a row somebody reads top to bottom goes from most masking to least.
//
// Served from here for the reason SubstitutionModes is: a menu offering a level the
// agent does not have would fail on a name the menu itself suggested.
func SecretLevels() []string { return detector.SecretLevels() }

func orEmpty(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// SetPolicy replaces what the agent applies, and returns what it says about itself
// afterwards.
//
// The one place a local surface writes this, as Query is the one place it is read.
// Two ways to change what an agent masks are two ways to be wrong about how
// somebody's traffic gets masked — and this one carries the header, the method and
// the shape of the body, all three of which the route checks.
//
// The Status comes from the agent's own reply rather than from what was asked for,
// so a caller redraws from what is true. A menu that drew its own request would show
// a credential as unticked the moment somebody clicked it, whatever the agent did
// with it.
func SetPolicy(ctx context.Context, addr, key string, want Policy, timeout time.Duration) (Status, error) {
	if addr == "" {
		addr = DefaultListen
	}
	status := Status{Addr: addr}

	if key == "" {
		return status, fmt.Errorf("no control key in %s, so this agent will not be told what to mask",
			DefaultControlKeyFile)
	}

	// Never nil in the body: an absent list and an empty one mean the same thing to
	// the route, but a JSON null reads as a mistake to anybody looking at the wire.
	body, err := json.Marshal(policyRequest{
		Off:          orEmpty(want.Off),
		Substitution: want.Substitution,
		Locales:      orEmpty(want.Locales),
		SecretLevel:  want.SecretLevel,
	})
	if err != nil {
		return status, err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://"+addr+"/policy", bytes.NewReader(body))
	if err != nil {
		return status, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(controlHeader, key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return status, err
	}
	defer func() { _ = resp.Body.Close() }()

	answer, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return status, err
	}
	if resp.StatusCode != http.StatusOK {
		// The agent's own words. It says which category it refused and why, and a
		// caller replacing that with "the request failed" throws away the only part
		// somebody can act on.
		return status, errors.New(strings.TrimSpace(string(answer)))
	}

	status.Answering = true
	if err := json.Unmarshal(answer, &status.Health); err != nil {
		return status, fmt.Errorf("the agent answered something this cannot read: %w", err)
	}
	return status, nil
}
