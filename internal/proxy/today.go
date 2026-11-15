package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
	contract "github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
)

// tally counts the values this agent has hidden since local midnight, for the menu
// bar.
//
// A value counts once per session — the mapping a request names (sessionOf), not the
// conversation — because the count is what the pass *minted*, never what it replaced.
// A conversation sends its whole history again on every turn, so the replacements
// (Pass.Counts) count an address quoted on turn one again on every turn after it:
// fifty turns, fifty. That figure is right for the heartbeat, where a value masked
// three times is three values that did not leave, and wrong for a person reading "how
// much did I expose today", where it reads as thousands over a day of thirty.
//
// TODO: the session is what the client names, so the count follows the client. One
// that names none — Claude Code — files every conversation under "default", and an
// address quoted in two of them counts once. One that named a fresh X-Request-Id per
// request would count the history again on every turn. A value is also counted again
// once its session's mapping is gone — three idle hours (vault.DefaultTTL), a change of
// substitution mode, or a restart — and twice when two requests are in flight at once
// and both carry it new: vault.Name gives it one name, but each pass counts its own
// first sighting. Upgrade: count distinct (conversationOf, fingerprint) pairs per day,
// the fingerprint keyed like vault.Name, at the cost of a set that grows through the
// day — and that must never leave the agent.
//
// TODO: held in memory, so a restart — an update is one — starts the day again from
// zero. Upgrade: store the day and the count in ~/.neverseen/ beside policy.json.
//
// Kept per category and summed by family, because the person chooses which families
// the menu bar's figure counts (uncounted, PUT /today): a day of database identifiers
// read as a day of exposure. The choice is applied here rather than by the menu bar,
// so the menu bar stays a reader of one number.
type tally struct {
	mu     sync.Mutex
	day    string
	minted map[pii.Category]int

	// uncounted are the families the figure leaves out, in registry order. A choice
	// about what is displayed, never about what is masked.
	uncounted []pii.Group

	// traffic is what crossed the agent today, for the settings page's overview.
	traffic dayTraffic
}

// dayTraffic is the day's traffic as the settings page shows it.
//
// The heartbeat's recorder counts the same events, per window, and is emptied by
// every report — when there is a backend at all. This is the person's day, emptied at
// midnight, and kept apart so neither can move the other. Counts and vocabulary words
// only, the rule every surface outside -a and -v keeps.
type dayTraffic struct {
	Requests int               `json:"requests"`
	Refused  int               `json:"refused"`
	Upstream contract.Upstream `json:"upstream"`
	Tools    dayTools          `json:"tools"`

	// Clients is requests per client family, from the closed vocabulary.
	Clients map[string]int `json:"clients,omitempty"`
}

type dayTools struct {
	Calls int `json:"calls"`

	// Restored is the calls whose arguments carried a value put back in clear.
	Restored int `json:"restored"`
}

// request counts a request forwarded today, by the client family that sent it.
func (t *tally) request(now time.Time, client string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roll(now)
	t.traffic.Requests++
	if client == "" {
		return
	}
	if !slices.Contains(contract.KnownClients, client) {
		client = contract.Other
	}
	if t.traffic.Clients == nil {
		t.traffic.Clients = make(map[string]int)
	}
	t.traffic.Clients[client]++
}

// refused counts a request the agent would not forward.
func (t *tally) refused(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roll(now)
	t.traffic.Refused++
}

// upstream counts how a provider answered, in the classes Recorder.Upstream uses.
// Zero is no answer at all.
func (t *tally) upstream(now time.Time, status int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roll(now)
	t.traffic.Upstream.Add(status)
}

// tool counts a tool call the model asked for.
func (t *tally) tool(now time.Time, restored bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roll(now)
	t.traffic.Tools.Calls++
	if restored {
		t.traffic.Tools.Restored++
	}
}

// add counts what a pass minted at now, per category.
func (t *tally) add(now time.Time, minted map[pii.Category]int) {
	if len(minted) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roll(now)
	if t.minted == nil {
		t.minted = make(map[pii.Category]int)
	}
	for cat, n := range minted {
		t.minted[cat] += n
	}
}

// reply is what GET /today answers about now's day, read under one lock so the
// figure and its breakdown describe the same moment.
//
// It rolls the day over too, rather than leaving that to add: an agent with no
// traffic since midnight would otherwise go on reporting yesterday's count all
// morning.
func (t *tally) reply(now time.Time) todayReply {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roll(now)

	r := todayReply{ByFamily: make(map[string]int), Uncounted: make([]string, 0, len(t.uncounted)), Traffic: t.traffic}
	r.Traffic.Clients = maps.Clone(t.traffic.Clients)
	for _, g := range t.uncounted {
		r.Uncounted = append(r.Uncounted, string(g))
	}
	for cat, n := range t.minted {
		family := pii.GroupOf(cat)
		r.ByFamily[string(family)] += n
		if !slices.Contains(t.uncounted, family) {
			r.Masked += n
		}
	}
	return r
}

// setUncounted replaces the families the figure leaves out.
func (t *tally) setUncounted(groups []pii.Group) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.uncounted = groups
}

// roll starts a new count when now falls on another day than the one being counted.
// The day is the workstation's, in its own time zone: "today" is the person's day,
// not UTC's.
//
// TODO: the day is the local zone's as the process read it at start — Go reads it once —
// so a laptop that changes zone goes on rolling at the old midnight until the agent
// restarts. Upgrade: reread the zone on each roll, which Go has no call for: the zone
// file on Unix, the registry on Windows.
func (t *tally) roll(now time.Time) {
	if day := now.Format(time.DateOnly); day != t.day {
		t.day = day
		t.minted = nil
		t.traffic = dayTraffic{}
	}
}

// todayReply is what GET /today answers.
type todayReply struct {
	// Masked is the menu bar's figure: the families counted, and no others.
	Masked int `json:"masked"`

	// ByFamily is the whole day per family code, the uncounted ones included, so the
	// settings page can show what a family would add before it is ticked.
	ByFamily map[string]int `json:"by_family"`

	// Uncounted are the family codes Masked leaves out.
	Uncounted []string `json:"uncounted"`

	Traffic dayTraffic `json:"traffic"`

	// Agent is how this agent runs: not the day's, but behind the same key for the
	// same reason — whether the console prints values in clear or traces sit on disk
	// tells any process on the machine where to look.
	Agent agentState `json:"agent"`
}

// agentState is how the agent runs and how it is exposed — the State fields the
// heartbeat carries (Console, Tracing, Exposed, Rerouted, Allowlisted), read from the
// same sources, plus when it started and when the extension last called it.
type agentState struct {
	StartedAt time.Time `json:"started_at"`

	Console     bool     `json:"console"`
	Tracing     bool     `json:"tracing"`
	Exposed     bool     `json:"exposed"`
	Rerouted    []string `json:"rerouted,omitempty"`
	Allowlisted int      `json:"allowlisted,omitempty"`

	// ExtensionSeenAt is the last /mask call, zero when none came since the start.
	ExtensionSeenAt *time.Time `json:"extension_seen_at,omitempty"`
}

// agentNow reads agentState from the server.
func (s *Server) agentNow() agentState {
	return agentState{
		StartedAt:       s.startedAt,
		Console:         s.audit.writes(),
		Tracing:         s.audit.traceDir() != "",
		Exposed:         s.exposed,
		Rerouted:        s.rerouted(),
		Allowlisted:     s.det.Allowlisted(),
		ExtensionSeenAt: s.extensionSeen.Load(),
	}
}

// todayRequest is what PUT /today carries: the whole choice, never a patch.
type todayRequest struct {
	Uncounted *[]string `json:"uncounted"`
}

// menuBarFile is where the choice survives a restart: beside the policy file, and
// stored only where that one is — a server told to store nothing stores neither.
// Its own file rather than a field of the policy, because PUT /policy replaces what
// the agent masks and every surface resends it whole; a display choice carried in it
// would have to travel with every click about masking, and would be lost by the one
// that forgot it.
func menuBarFile(policyFile string) string {
	if policyFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(policyFile), "menubar.json")
}

// storedMenuBar is the shape of menubar.json.
type storedMenuBar struct {
	Uncounted []string `json:"uncounted"`
}

// familiesFrom resolves family codes into registry order, refusing an unknown one.
//
// Refused rather than skipped, as an unknown locale is: a surface sending a name the
// agent does not know has drifted from it, and a quiet skip would draw a choice that
// was never applied.
func familiesFrom(codes []string) ([]pii.Group, error) {
	known := pii.Groups()
	for _, code := range codes {
		if !slices.Contains(known, pii.Group(code)) {
			return nil, fmt.Errorf("no family named %q", code)
		}
	}
	out := make([]pii.Group, 0, len(codes))
	for _, g := range known {
		if slices.Contains(codes, string(g)) {
			out = append(out, g)
		}
	}
	return out, nil
}

// loadMenuBar restores the stored choice at start-up. A file that cannot be read is
// a warning and every family is counted, the default: the figure is a display, and
// nothing about masking waits on it.
func (s *Server) loadMenuBar() {
	path := menuBarFile(s.policyFile)
	if path == "" {
		return
	}
	var stored storedMenuBar
	found, err := readStateFile(path, &stored)
	if err == nil && found {
		var groups []pii.Group
		if groups, err = familiesFrom(stored.Uncounted); err == nil {
			s.today.setUncounted(groups)
		}
	}
	if err != nil {
		s.log.Warn("the menu bar counts every family: its stored choice could not be read",
			"file", path, "error", err)
	}
}

// handleToday says how many values this agent has hidden today.
//
// Behind the control key, and not a field of /healthz, which carries what the agent
// is applying and nothing about who is using it: /healthz is unauthenticated, so a
// count there would tell any process on the machine — another account's included,
// and the network under -l — how much this person has been doing, and when.
//
// PUT replaces which families the figure counts — the settings page's choice — and
// answers what GET would.
func (s *Server) handleToday(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "neverseen: use GET to read today's count, PUT to choose what it counts", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorised(w, r) {
		return
	}
	if r.Method == http.MethodPut {
		if !s.chooseUncounted(w, r) {
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	reply := s.today.reply(time.Now())
	reply.Agent = s.agentNow()
	writeJSON(w, reply)
}

// todayRequestMaxBytes bounds PUT /today: a handful of family codes.
const todayRequestMaxBytes = 4096

// chooseUncounted applies PUT /today, and reports false once it has answered with a
// refusal.
func (s *Server) chooseUncounted(w http.ResponseWriter, r *http.Request) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, todayRequestMaxBytes+1))
	if err != nil || len(body) > todayRequestMaxBytes {
		http.Error(w, "neverseen: the request is too large or unreadable", http.StatusBadRequest)
		return false
	}
	var req todayRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Uncounted == nil {
		http.Error(w, `neverseen: send {"uncounted": [...]}, the whole choice — an empty list counts every family`,
			http.StatusBadRequest)
		return false
	}
	groups, err := familiesFrom(*req.Uncounted)
	if err != nil {
		http.Error(w, "neverseen: "+err.Error(), http.StatusUnprocessableEntity)
		return false
	}

	// Serialised with the write, so the file ends as the last choice applied rather
	// than whichever of two writes renamed last.
	s.menuBarMu.Lock()
	defer s.menuBarMu.Unlock()
	s.today.setUncounted(groups)
	if path := menuBarFile(s.policyFile); path != "" {
		stored := storedMenuBar{Uncounted: make([]string, 0, len(groups))}
		for _, g := range groups {
			stored.Uncounted = append(stored.Uncounted, string(g))
		}
		if err := writeStateFile(path, ".menubar-*.json", stored); err != nil {
			s.log.Warn("this choice will not survive a restart: the menu bar's could not be stored",
				"file", path, "error", err)
		}
	}
	return true
}

// todayMaxBytes bounds the answer to QueryToday. The menu bar reads one number from
// it, and the settings page the rest — the breakdown, the day's traffic, how the agent
// runs — so the bound covers the whole answer: cut short, it no longer parses, and the
// menu bar loses its figure without a word (TestTodayReplyFitsTheQueryBound).
const todayMaxBytes = 16 << 10

// QueryToday asks the agent how many values it has hidden today.
//
// It reports false when it was not told — no key, nothing listening, a refusal, or
// an agent built before the route — so a surface can leave the count out rather than
// draw a zero nobody measured.
func QueryToday(ctx context.Context, addr, key string, timeout time.Duration) (int, bool) {
	if addr == "" {
		addr = DefaultListen
	}
	if key == "" {
		return 0, false
	}

	code, body, err := askAgent(ctx, timeout, http.MethodGet, "http://"+addr+"/today", key, nil, todayMaxBytes)
	if err != nil || code != http.StatusOK {
		return 0, false
	}
	var reply todayReply
	if err := json.Unmarshal(body, &reply); err != nil {
		return 0, false
	}
	return reply.Masked, true
}
