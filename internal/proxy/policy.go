package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/pkg/pii"
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
const controlHeader = "X-Cloakfleet-Control"

// DefaultControlKeyFile is where the local control secret is kept.
//
// Beside the identity a backend issued, under the same directory and the same
// permissions, because it is the same kind of thing: a credential this machine
// holds, which the installer deliberately does not delete.
const DefaultControlKeyFile = "~/.cloakfleet/control.key"

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
}

// handlePolicy replaces the set of categories the agent is not masking.
func (s *Server) handlePolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		// PUT rather than POST, because the request carries the whole state rather
		// than an increment, and saying so in the method is what tells a caller
		// that sending it twice is safe.
		w.Header().Set("Allow", http.MethodPut)
		http.Error(w, "cloakfleet: use PUT to replace the set of categories", http.StatusMethodNotAllowed)
		return
	}

	if s.controlKey == "" {
		// No key, no route. An agent that could not read or write its key must not
		// fall back to accepting anything: that is the failure this route's whole
		// design is about.
		http.Error(w, "cloakfleet: no control key on this agent, so nothing may change what it masks",
			http.StatusServiceUnavailable)
		return
	}
	if r.Header.Get(controlHeader) != s.controlKey {
		// Deliberately says nothing about what was wrong. A local process probing
		// this does not need to be told whether the header was missing or merely
		// incorrect.
		http.Error(w, "cloakfleet: not authorised to change what this agent masks", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 16*1024))
	if err != nil {
		http.Error(w, "cloakfleet: could not read the request", http.StatusBadRequest)
		return
	}

	var req policyRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "cloakfleet: the request is not a JSON object with an \"off\" list",
			http.StatusBadRequest)
		return
	}

	// The whole state, every time, and a request missing part of it is refused
	// rather than completed from what happens to be current.
	//
	// The alternative — "absent means unchanged" — cannot be written honestly here.
	// An empty locale list is a *valid* state, the one an agent starts in when
	// nothing is configured, so absence cannot mean "leave them alone" without
	// making "load none" unsayable. And a caller that sent only "off" would silently
	// wipe the locale selection, which is the request that turns an agent into one
	// masking almost nothing while reporting success.
	if req.Substitution == "" {
		http.Error(w, "cloakfleet: this route replaces the whole state, so \"substitution\" "+
			"is required — send what the agent currently reports on /healthz, with your change applied",
			http.StatusBadRequest)
		return
	}

	mode, err := detector.ParseSubstitution(req.Substitution)
	if err != nil {
		http.Error(w, "cloakfleet: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}

	cats := make([]pii.Category, 0, len(req.Off))
	for _, code := range req.Off {
		cats = append(cats, pii.Category(code))
	}

	// Locales first, because it decides which categories exist to be switched off
	// at all, and because it is the change that can fail on a typo. Applied in the
	// order a reader would expect the state to settle in.
	//
	// Each step refuses on its own and the earlier ones stay applied, which is worth
	// naming: this is not a transaction. A caller that sent a good locale selection
	// and a bad category gets the locales changed and an error, and its next poll
	// shows exactly that — which is why every surface here redraws from the reply
	// rather than from its own request.
	if err := s.det.SetLocales(req.Locales); err != nil {
		http.Error(w, "cloakfleet: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}

	// The detector is the one that refuses a credential, so it cannot be switched
	// off by anything — not by this route, not by a second surface written later.
	// The rule lives with the catalogue rather than with the transport.
	if err := s.det.Disable(cats); err != nil {
		http.Error(w, "cloakfleet: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}

	// Read before the change so the purge below can tell a real change from a
	// request that resent the mode it was already in — which every surface does on
	// every click, because this route replaces the whole state.
	changed := s.det.Substitution() != mode
	s.det.SetSubstitution(mode)

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
		"substitution", mode.String(),
		"masking", s.det.Masking().String(),
		// Said out loud because it is the one part of this request that discards
		// state, and an unexpanded token in an answer is otherwise unexplainable.
		"sessions_cleared", changed)

	// The new state, from the agent rather than echoed back: a caller has to be
	// able to redraw from what is true rather than from what it asked for.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.healthNow())
}

// loadControlKey reads the local control secret, creating one if there is none.
//
// Generated rather than configured, and not an environment variable: a secret in
// the environment is a secret in the process table and in whatever shell history
// set it, and this one has no reason to be typed by anybody. The file is the
// interface — the menu bar reads the same path.
//
// A key that cannot be read or written is not fatal. The agent's job is masking,
// and refusing to start because the menu bar will not be able to switch a category
// off would be the supervision mistake in another costume: the thing that adjusts
// the control must never be able to stop it.
func loadControlKey(path string) (string, error) {
	resolved, err := expandHome(path)
	if err != nil {
		return "", err
	}

	if raw, err := os.ReadFile(filepath.Clean(resolved)); err == nil {
		if key := trimKey(raw); key != "" {
			return key, nil
		}
		// An empty or truncated file is replaced rather than used: a short secret
		// is worse than none, because it looks like protection.
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", resolved, err)
	}

	key, err := newControlKey()
	if err != nil {
		return "", err
	}
	if err := writeControlKey(resolved, key); err != nil {
		return "", err
	}
	return key, nil
}

// newControlKey mints a secret. Hex of 32 random bytes, the same shape and length
// as the session encryption key an operator may set by hand, so there is one
// notion of "a key" in this agent rather than two.
func newControlKey() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate a control key: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// writeControlKey writes it where only its owner can read it.
//
// The directory first, at 0700, because a 0600 file in a world-readable directory
// is a secret anybody can watch appear. Written by rename for the reason the
// telemetry buffer is: a torn key file on the next start reads as no key, which
// silently turns the route off.
func writeControlKey(path, key string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(key+"\n"), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("move %s into place: %w", path, err)
	}
	return nil
}

// trimKey takes the newline off a key file and refuses anything that is not a
// full-length hex secret.
//
// Length-checked rather than trusted, because the failure it prevents is silent:
// a truncated file used as a key is a route that looks authenticated and is not.
func trimKey(raw []byte) string {
	key := strings.TrimSpace(string(raw))
	if len(key) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(key); err != nil {
		return ""
	}
	return key
}

// ReadControlKey returns the secret a local surface needs to change what the agent
// masks, or "" when there is none to read.
//
// Exported for the menu bar, and read from the same file the agent writes: two
// ways to learn this secret would be two things to get wrong about it. It never
// creates one — only the agent does that, because a key created by a reader would
// be a key the agent does not know.
func ReadControlKey(path string) string {
	if path == "" {
		path = DefaultControlKeyFile
	}
	resolved, err := expandHome(path)
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Clean(resolved))
	if err != nil {
		return ""
	}
	return trimKey(raw)
}

// Policy is the whole of what a local surface can change about a running agent.
//
// All three together, because that is what the route takes: it replaces the state
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
