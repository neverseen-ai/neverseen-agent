package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
)

// The naming rules hold under the traffic that broke them, not only in the case
// each test isolates.
//
// Every failure the vault's naming has had showed up only in combination: a pause
// expiring the mapping while the model still remembered a token, two requests in
// flight meeting one value for the first time, a restart under a conversation that
// went on. So this drives the real Server, vault and numbering file with all of
// them at once: conversations in one session as Claude Code sends them, each
// replaying its whole history every turn, concurrently; a fake model that
// remembers every token it was shown and names old ones in its answers, as the
// thinking signature lets the real one; pauses that expire every mapping; restarts
// that rebuild the vault and the server from the numbering file.
//
// Ground truth comes from correlation. Each request carries its values one per
// line, in an order the client registers, so the upstream pairs the i-th token it
// sees with the i-th value and knows what every token meant when the model met it.
// Each answer is then judged token by token: restored to the value the model meant,
// left a token, or — the failure that matters most — restored to another value.

var simTokenRe = regexp.MustCompile(`\[(PLATE|EMAIL)_\d+\]`)

type simConfig struct {
	seed           uint64
	conversations  int
	rounds         int
	pause, restart float64 // probability of each, between two rounds
	shared         float64 // probability a new value comes from the pool every conversation draws on
}

type simReport struct {
	mentions, restored, wrong int
	// A token left unexpanded, split by whether the process that minted it is the
	// one answering.
	unrestoredSameProcess, unrestoredAfterRestart int
	// A value that reached the provider in clear.
	leaks int
	// A value given a second name, split the same way, and a name given to a
	// second value.
	secondNameSameProcess, secondNameAfterRestart, collisions int
	pauses, restarts                                          int
}

// simWorld is what the client and the fake model share: the ground truth.
type simWorld struct {
	mu      sync.Mutex
	sent    map[string][]string       // request key → its values, in order
	meant   map[string][]string       // request key → the values the answer names, in order
	born    map[string][]int          // request key → the process each named token was minted by
	memory  map[int]map[string]string // conversation → token → the value it meant when first seen
	names   map[string]map[string]int // value → its tokens → the process that minted each
	meaning map[string]string         // token → the value it was last seen meaning
	minted  map[string]int            // token → the process that minted it
	pickers map[int]*rand.Rand        // conversation → the model's choice of what to name
	process int
	report  *simReport
}

func simulate(t *testing.T, cfg simConfig) simReport {
	t.Helper()
	det := detector.New(detector.Config{Locales: []string{"fr"}})
	rng := rand.New(rand.NewPCG(cfg.seed, 1))
	report := &simReport{}

	line := func(v string) string {
		if strings.Contains(v, "@") {
			return "contact " + v
		}
		return "véhicule " + v
	}
	// Only values the detector masks exactly, alone on their line: a value it
	// missed would be a leak the harness put there, not one the agent let through.
	const letters = "ABCDEFGHJKLMNPRSTVXYZ"
	pair := func(r *rand.Rand) string {
		return string([]byte{letters[r.IntN(len(letters))], letters[r.IntN(len(letters))]})
	}
	value := func(r *rand.Rand, salt int) string {
		for {
			v := fmt.Sprintf("user%d.%d@example.fr", salt, r.IntN(1_000_000))
			if r.IntN(2) == 0 {
				v = fmt.Sprintf("%s-%03d-%s", pair(r), 1+r.IntN(998), pair(r))
			}
			if m := det.Scan(line(v)); len(m) == 1 && m[0].Value == v {
				return v
			}
		}
	}
	pool := make([]string, 30)
	for i := range pool {
		pool[i] = value(rng, -1-i)
	}

	w := &simWorld{
		sent: map[string][]string{}, meant: map[string][]string{}, born: map[string][]int{},
		memory: map[int]map[string]string{}, names: map[string]map[string]int{},
		meaning: map[string]string{}, minted: map[string]int{}, pickers: map[int]*rand.Rand{},
		report: report,
	}
	model := httptest.NewServer(http.HandlerFunc(w.answer))
	t.Cleanup(model.Close)

	numbering := filepath.Join(t.TempDir(), "numbering.json")
	logger := slog.New(slog.DiscardHandler)
	var handler atomic.Pointer[http.Handler]
	var store atomic.Pointer[vault.Memory]
	start := func() {
		m := vault.NewMemory()
		v, err := vault.New(m, nil, vault.DefaultTTL)
		if err != nil {
			t.Fatal(err)
		}
		keepNumbering(logger, v, numbering)
		srv, err := New(Config{Providers: []Provider{{Code: "anthropic", BaseURL: model.URL}}},
			detector.New(detector.Config{Locales: []string{"fr"}}), v)
		if err != nil {
			t.Fatal(err)
		}
		h := srv.Handler()
		handler.Store(&h)
		store.Store(m)
	}
	start()
	agent := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		(*handler.Load()).ServeHTTP(rw, r)
	}))
	t.Cleanup(agent.Close)

	history := make([][]string, cfg.conversations)
	clients := make([]*rand.Rand, cfg.conversations)
	for c := range clients {
		clients[c] = rand.New(rand.NewPCG(cfg.seed, uint64(c)+2))
	}

	for round := range cfg.rounds {
		var wg sync.WaitGroup
		for c := range cfg.conversations {
			wg.Go(func() {
				r := clients[c]
				for range 1 + r.IntN(2) {
					next := pool[r.IntN(len(pool))]
					if r.Float64() >= cfg.shared {
						next = value(r, c*10_000+round*10+len(history[c]))
					}
					history[c] = append(history[c], next)
				}
				w.turn(t, agent, fmt.Sprintf("%d/%d", c, round), history[c], line)
			})
		}
		wg.Wait()

		switch x := rng.Float64(); {
		case x < cfg.restart:
			start()
			w.mu.Lock()
			w.process++
			w.mu.Unlock()
			report.restarts++
		case x < cfg.restart+cfg.pause:
			// The store's Forget, not the vault's: every mapping expires, as a pause
			// past the lifetime does, and the names the vault remembers stay.
			store.Load().Forget()
			report.pauses++
		}
	}
	return *report
}

// turn sends one conversation's history and judges the answer.
func (w *simWorld) turn(t *testing.T, agent *httptest.Server, key string, values []string, line func(string) string) {
	lines := make([]string, len(values))
	for i, v := range values {
		lines[i] = line(v)
	}
	w.mu.Lock()
	w.sent[key] = slices.Clone(values)
	w.mu.Unlock()

	body, _ := json.Marshal(map[string]any{"messages": []map[string]string{{"role": "user", "content": strings.Join(lines, "\n")}}})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, agent.URL+"/anthropic/v1/messages", bytes.NewReader(body))
	if err != nil {
		t.Error(err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sim-Key", key)
	// Not do(): it fails with t.Fatal, which a goroutine must not call.
	resp, err := agent.Client().Do(req)
	if err != nil {
		t.Error(err)
		return
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	var answer map[string]string
	_ = json.Unmarshal(raw, &answer)
	said := strings.Split(strings.TrimPrefix(answer["text"], "R: "), " | ")

	w.mu.Lock()
	defer w.mu.Unlock()
	r := w.report
	for i, meant := range w.meant[key] {
		r.mentions++
		switch {
		case i >= len(said):
		case said[i] == meant:
			r.restored++
		case !simTokenRe.MatchString(said[i]):
			r.wrong++
			t.Errorf("%s: the model meant %s and the caller read %s", key, meant, said[i])
		case w.born[key][i] == w.process:
			r.unrestoredSameProcess++
		default:
			r.unrestoredAfterRestart++
		}
	}
}

// answer is the fake model: it learns what each token in the request means, then
// names up to three tokens it remembers from any turn of its conversation.
func (w *simWorld) answer(rw http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	key := r.Header.Get("X-Sim-Key")
	conversation, _ := strconv.Atoi(strings.SplitN(key, "/", 2)[0])
	var doc struct {
		Messages []struct{ Content string } `json:"messages"`
	}
	_ = json.Unmarshal(body, &doc)
	content := ""
	if len(doc.Messages) > 0 {
		content = doc.Messages[0].Content
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	rep := w.report
	values := w.sent[key]
	for _, v := range values {
		if strings.Contains(content, v) {
			rep.leaks++
		}
	}
	memory := w.memory[conversation]
	if memory == nil {
		memory = map[string]string{}
		w.memory[conversation] = memory
		w.pickers[conversation] = rand.New(rand.NewPCG(uint64(conversation), 99))
	}
	for i, tok := range simTokenRe.FindAllString(content, len(values)) {
		v := values[i]
		if _, known := memory[tok]; !known {
			memory[tok] = v
		}
		if was, seen := w.meaning[tok]; seen && was != v {
			rep.collisions++
		}
		w.meaning[tok] = v
		if _, seen := w.minted[tok]; !seen {
			w.minted[tok] = w.process
		}
		tokens := w.names[v]
		if tokens == nil {
			tokens = map[string]int{}
			w.names[v] = tokens
		}
		if _, seen := tokens[tok]; !seen {
			if len(tokens) > 0 {
				sameProcess := false
				for _, p := range tokens {
					sameProcess = sameProcess || p == w.process
				}
				if sameProcess {
					rep.secondNameSameProcess++
				} else {
					rep.secondNameAfterRestart++
				}
			}
			tokens[tok] = w.process
		}
	}

	remembered := make([]string, 0, len(memory))
	for tok := range memory {
		remembered = append(remembered, tok)
	}
	slices.Sort(remembered)
	w.pickers[conversation].Shuffle(len(remembered), func(i, j int) {
		remembered[i], remembered[j] = remembered[j], remembered[i]
	})
	named := remembered[:min(3, len(remembered))]
	for _, tok := range named {
		w.meant[key] = append(w.meant[key], memory[tok])
		w.born[key] = append(w.born[key], w.minted[tok])
	}

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(map[string]string{"text": "R: " + strings.Join(named, " | ")})
}

// Concurrent conversations in one session, through pauses and restarts, get every
// name back that the process answering them minted, never a wrong value, and never
// send a value in clear. Several seeds, because what fails here fails on an
// interleaving.
func TestSimulatedConversationsKeepTheirNames(t *testing.T) {
	for _, seed := range []uint64{2, 5, 7} {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			r := simulate(t, simConfig{
				seed: seed, conversations: 6, rounds: 25, pause: 0.15, restart: 0.08, shared: 0.5,
			})
			t.Logf("%+v", r)

			// Seeds chosen because each draws pauses and restarts both; a change to
			// the harness that drew neither would pass every assertion below.
			if r.mentions == 0 || r.pauses == 0 || r.restarts == 0 {
				t.Fatalf("the run exercised nothing: %+v", r)
			}
			if r.wrong != 0 || r.collisions != 0 {
				t.Errorf("%d answer(s) restored to another value, %d name(s) given to two values", r.wrong, r.collisions)
			}
			if r.leaks != 0 {
				t.Errorf("%d value(s) reached the provider in clear", r.leaks)
			}
			if r.secondNameSameProcess != 0 || r.unrestoredSameProcess != 0 {
				t.Errorf("one process gave %d value(s) a second name and left %d token(s) unexpanded",
					r.secondNameSameProcess, r.unrestoredSameProcess)
			}
		})
	}
}
