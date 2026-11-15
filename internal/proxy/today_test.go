package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/telemetry"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
	contract "github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
)

// turn is one request of a conversation: the whole history so far, as a client
// sends it.
func turn(t *testing.T, contents ...string) string {
	t.Helper()

	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var messages []message
	for i, c := range contents {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		messages = append(messages, message{Role: role, Content: c})
	}
	body, err := json.Marshal(map[string]any{"messages": messages})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// maskedToday reads the count the way the menu bar does.
func maskedToday(t *testing.T, addr string) int {
	t.Helper()

	n, ok := QueryToday(t.Context(), strings.TrimPrefix(addr, "http://"), testControlKey, time.Second)
	if !ok {
		t.Fatal("the agent did not say how many values it hid today")
	}
	return n
}

// The count is what a person exposed, not how often it was replaced. A conversation
// sends its whole history every turn, so counting replacements counts the address
// quoted on turn one again on every turn after it — the figure the heartbeat wants,
// and thousands over a day of thirty on a menu bar.
//
// Once per session, which is what the client names — not per conversation (see the
// TODO on tally).
func TestTodayCountsAValueOncePerSession(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _ := newControlledAgent(t, up, []string{"fr"})

	const first, second = "jeanne.dupont@example.fr", "paul.durand@example.fr"

	post(t, agent, "/anthropic/v1/messages", "s1", turn(t, "écrire à "+first))
	if got := maskedToday(t, agent.URL); got != 1 {
		t.Fatalf("after the first turn the count is %d, want 1", got)
	}

	post(t, agent, "/anthropic/v1/messages", "s1",
		turn(t, "écrire à "+first, "c'est noté", "et en copie "+second))
	post(t, agent, "/anthropic/v1/messages", "s1",
		turn(t, "écrire à "+first, "c'est noté", "et en copie "+second, "voilà", "merci"))
	if got := maskedToday(t, agent.URL); got != 2 {
		t.Fatalf("three turns quoting two values counted %d, want 2: the history "+
			"sent again was counted again", got)
	}

	// Per session: another one quoting the same address is another exposure.
	post(t, agent, "/anthropic/v1/messages", "s2", turn(t, "relancer "+first))
	if got := maskedToday(t, agent.URL); got != 3 {
		t.Fatalf("a second session quoting the first address counted %d, want 3", got)
	}

	// The extension's route masks too, and its values count the same way.
	postAs(t, agent, "/mask", testControlKey, "web", `{"texts":["`+second+`"]}`)
	if got := maskedToday(t, agent.URL); got != 4 {
		t.Fatalf("a value masked through /mask left the count at %d, want 4", got)
	}

	// A client that names no session — Claude Code — files every conversation under
	// "default": two conversations quoting one new address count it once. Recorded
	// rather than wanted; the TODO on tally says what would count it twice.
	const third = "marc.petit@example.fr"
	post(t, agent, "/anthropic/v1/messages", "", turn(t, "écrire à "+third))
	post(t, agent, "/anthropic/v1/messages", "", turn(t, "une autre conversation pour "+third))
	if got := maskedToday(t, agent.URL); got != 5 {
		t.Fatalf("two conversations in the default session quoting one address counted %d, want 5", got)
	}
}

// The count says how much somebody has been doing and when, so it is behind the key
// that /healthz does not have.
func TestTodayIsBehindTheControlKey(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _ := newControlledAgent(t, up, []string{"fr"})
	addr := strings.TrimPrefix(agent.URL, "http://")

	for _, key := range []string{"", "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, agent.URL+"/today", nil)
		if err != nil {
			t.Fatal(err)
		}
		if key != "" {
			req.Header.Set(controlHeader, key)
		}
		if got := do(t, agent, req); got.status != http.StatusForbidden {
			t.Errorf("GET /today with key %q answered %d, want 403: %s", key, got.status, got.body)
		}
		if _, ok := QueryToday(t.Context(), addr, key, time.Second); ok {
			t.Errorf("QueryToday reported a count with key %q", key)
		}
	}

	if got := postAs(t, agent, "/today", testControlKey, "", ""); got.status != http.StatusMethodNotAllowed {
		t.Errorf("POST /today answered %d, want 405", got.status)
	}
}

// Nothing listening is "not told", never a zero nobody measured.
func TestQueryTodayReportsAnAbsentAgentAsNotTold(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if n, ok := QueryToday(ctx, "127.0.0.1:1", testControlKey, time.Second); ok {
		t.Errorf("an address with nothing on it reported %d values", n)
	}
}

// The day is the workstation's: the count starts again at local midnight, and a
// read after midnight with nothing masked since says zero rather than yesterday's.
func TestTodayStartsAgainAtMidnight(t *testing.T) {
	var count tally
	evening := time.Date(2026, 11, 8, 23, 59, 0, 0, time.Local)
	morning := time.Date(2026, 11, 9, 0, 1, 0, 0, time.Local)

	count.add(evening, map[pii.Category]int{pii.CatEmail: 5})
	if got := count.reply(evening).Masked; got != 5 {
		t.Fatalf("the evening count is %d, want 5", got)
	}
	if got := count.reply(morning).Masked; got != 0 {
		t.Errorf("after midnight the count is %d, want 0", got)
	}
	count.add(morning, map[pii.Category]int{pii.CatEmail: 2})
	if got := count.reply(morning).Masked; got != 2 {
		t.Errorf("the morning count is %d, want 2", got)
	}
}

// The figure counts the families chosen and the breakdown keeps them all, so the
// settings page can say what an unticked family would add.
func TestTodayLeavesOutTheFamiliesNotCounted(t *testing.T) {
	var count tally
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.Local)
	personal, technical := pii.GroupOf(pii.CatEmail), pii.GroupOf(pii.CatMongoID)
	if personal == technical {
		t.Fatal("the test needs two categories in two families")
	}

	count.add(now, map[pii.Category]int{pii.CatEmail: 2, pii.CatMongoID: 3})
	count.setUncounted([]pii.Group{technical})

	r := count.reply(now)
	if r.Masked != 2 {
		t.Errorf("the menu bar's figure is %d, want 2 — the uncounted family was added in", r.Masked)
	}
	if r.ByFamily[string(personal)] != 2 || r.ByFamily[string(technical)] != 3 {
		t.Errorf("by family = %v, want both families whole", r.ByFamily)
	}
	if !slices.Equal(r.Uncounted, []string{string(technical)}) {
		t.Errorf("uncounted = %v, want %s", r.Uncounted, technical)
	}
}

// todayAgent is an agent storing beside file, for the PUT /today tests.
func todayAgent(t *testing.T, file string) *Server {
	t.Helper()

	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{ControlKey: testControlKey, PolicyFile: file}, detector.New(detector.Config{}), v)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func askToday(t *testing.T, srv *Server, method, body, key string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, "/today", strings.NewReader(body))
	if key != "" {
		req.Header.Set(controlHeader, key)
	}
	rec := httptest.NewRecorder()
	srv.handleToday(rec, req)
	return rec
}

// The choice the settings page makes is the agent's to apply, and it survives the
// restart like every other choice a surface makes.
func TestPutTodayChoosesWhatTheMenuBarCounts(t *testing.T) {
	file := filepath.Join(t.TempDir(), "policy.json")
	srv := todayAgent(t, file)
	technical := string(pii.GroupOf(pii.CatMongoID))
	srv.today.add(time.Now(), map[pii.Category]int{pii.CatEmail: 2, pii.CatMongoID: 3})

	got := askToday(t, srv, http.MethodPut, `{"uncounted":["`+technical+`"]}`, testControlKey)

	if got.Code != http.StatusOK {
		t.Fatalf("PUT /today = %d: %s", got.Code, got.Body)
	}
	var r todayReply
	if err := json.Unmarshal(got.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Masked != 2 || !slices.Equal(r.Uncounted, []string{technical}) {
		t.Errorf("PUT /today answered %+v, want 2 counted and %s left out", r, technical)
	}
	if body := askToday(t, srv, http.MethodGet, "", testControlKey).Body.String(); !strings.Contains(body, `"masked":2`) {
		t.Errorf("GET /today after the choice = %s, want the narrowed figure", body)
	}

	info, err := os.Stat(menuBarFile(file))
	if err != nil {
		t.Fatalf("the choice was not stored: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 && os.PathSeparator == '/' {
		t.Errorf("menubar.json is %v, want 0600", mode)
	}
	if restarted := todayAgent(t, file); !slices.Equal(restarted.today.reply(time.Now()).Uncounted, []string{technical}) {
		t.Error("a restart forgot which families the menu bar leaves out")
	}
}

// Refused whole, and nothing changed: the choice the menu bar applies is the last one
// the agent accepted.
func TestPutTodayRefusesWhatItCannotApply(t *testing.T) {
	srv := todayAgent(t, "")
	technical := string(pii.GroupOf(pii.CatMongoID))
	askToday(t, srv, http.MethodPut, `{"uncounted":["`+technical+`"]}`, testControlKey)

	tests := []struct {
		name, body, key string
		want            int
	}{
		{"an unknown family", `{"uncounted":["nobody"]}`, testControlKey, http.StatusUnprocessableEntity},
		{"no choice at all", `{}`, testControlKey, http.StatusBadRequest},
		{"not JSON", `uncounted=technical`, testControlKey, http.StatusBadRequest},
		{"no key", `{"uncounted":[]}`, "", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := askToday(t, srv, http.MethodPut, tt.body, tt.key); got.Code != tt.want {
				t.Errorf("PUT /today = %d, want %d: %s", got.Code, tt.want, got.Body)
			}
			if got := srv.today.reply(time.Now()).Uncounted; !slices.Equal(got, []string{technical}) {
				t.Errorf("a refused request changed the choice to %v", got)
			}
		})
	}
}

// The menu bar reads this answer under todayMaxBytes; a breakdown naming every family
// must leave it room, or the menu bar loses its figure without a word.
func TestTodayReplyFitsTheQueryBound(t *testing.T) {
	var count tally
	minted := make(map[pii.Category]int)
	for _, g := range pii.Groups() {
		for _, cat := range pii.CategoriesInGroup(g) {
			minted[cat] = 1_000_000
		}
	}
	count.add(time.Now(), minted)
	for _, client := range append(slices.Clone(contract.KnownClients), "unknown") {
		count.request(time.Now(), client)
	}
	count.setUncounted(pii.Groups())

	reply := count.reply(time.Now())
	reply.Agent = agentState{StartedAt: time.Now(), Rerouted: []string{"anthropic", "openai", "gemini", "mistral"}, Allowlisted: 1_000_000}
	body, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > todayMaxBytes/2 {
		t.Errorf("a full /today answer is %d bytes, over half the %d the menu bar reads", len(body), todayMaxBytes)
	}
}

// The day's traffic is the person's day: counted in the recorder's classes, reduced to
// the closed vocabulary of clients, and emptied at midnight.
func TestTodayCountsTheDaysTraffic(t *testing.T) {
	var count tally
	evening := time.Date(2026, 11, 13, 23, 0, 0, 0, time.Local)
	morning := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local)

	count.request(evening, "claude-code")
	count.request(evening, "a-client-nobody-named")
	count.refused(evening)
	for _, status := range []int{200, 201, 429, 403, 502, 0} {
		count.upstream(evening, status)
	}
	count.tool(evening, true)
	count.tool(evening, false)

	got := count.reply(evening).Traffic
	want := dayTraffic{
		Requests: 2,
		Refused:  1,
		Upstream: contract.Upstream{OK: 2, RateLimited: 1, Rejected: 1, Failed: 1, Unreachable: 1},
		Tools:    dayTools{Calls: 2, Restored: 1},
		Clients:  map[string]int{"claude-code": 1, contract.Other: 1},
	}
	if got.Requests != want.Requests || got.Refused != want.Refused || got.Upstream != want.Upstream || got.Tools != want.Tools {
		t.Errorf("traffic = %+v, want %+v", got, want)
	}
	if len(got.Clients) != 2 || got.Clients["claude-code"] != 1 || got.Clients[contract.Other] != 1 {
		t.Errorf("clients = %v, want one known family and one reduced to %q", got.Clients, contract.Other)
	}
	if after := count.reply(morning).Traffic; after.Requests != 0 || after.Tools.Calls != 0 || len(after.Clients) != 0 {
		t.Errorf("after midnight the traffic is %+v, want an empty day", after)
	}
}

// How the agent runs is read from the same sources as the heartbeat's State, and the
// extension's last call is what the settings page says about it.
func TestTodayReportsHowTheAgentRuns(t *testing.T) {
	srv := todayAgent(t, "")
	read := func() agentState {
		t.Helper()
		var r todayReply
		if err := json.Unmarshal(askToday(t, srv, http.MethodGet, "", testControlKey).Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		return r.Agent
	}

	a := read()
	if a.StartedAt.IsZero() || a.Console || a.Tracing || a.Exposed || a.ExtensionSeenAt != nil {
		t.Errorf("a plain agent reports %+v", a)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mask", strings.NewReader(`{"texts":["write to claire@example.fr"]}`))
	req.RemoteAddr = "127.0.0.1:52341"
	req.Header.Set(controlHeader, testControlKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleMask(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /mask = %d: %s", rec.Code, rec.Body)
	}
	if seen := read().ExtensionSeenAt; seen == nil || time.Since(*seen) > time.Minute {
		t.Errorf("after a /mask call the extension was last seen at %v", seen)
	}
}

// The settings page's day and the heartbeat count the same events, each reported to
// both in one call (countRequest, countRefused, countMasked, countUpstream). A path that
// fed one and forgot the other is the disagreement this would catch: the fleet view and
// the person's own page telling two stories about one day.
func TestTheDayAndTheHeartbeatCountTheSameTraffic(t *testing.T) {
	// An answer asking for a tool, on its arguments the value the request carried: the
	// day's tool count is fed through toolSink, not through one of the count helpers.
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "send it") {
			echoJSON(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"content":[{"type":"tool_use","name":"SendMail","input":{"to":%q}}]}`,
			mintedToken(t, string(body)))
	})
	// A provider that never answers: the error handler's count, the one path that is
	// neither a request handler nor an answer.
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	det := detector.New(detector.Config{Locales: []string{"fr"}})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	recorder := telemetry.NewRecorder(time.Now())
	srv, err := New(Config{
		Providers:  []Provider{{Code: "anthropic", BaseURL: up.server.URL}, {Code: "openai", BaseURL: gone.URL}},
		Recorder:   recorder,
		ControlKey: testControlKey,
	}, det, v)
	if err != nil {
		t.Fatal(err)
	}
	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)

	// A masked exchange, one answered with a tool call, one nobody answers, the
	// extension's route, and a body the agent refuses.
	post(t, agent, "/anthropic/v1/messages", "s1", turn(t, "écrire à jeanne.dupont@example.fr"))
	post(t, agent, "/anthropic/v1/messages", "s2", turn(t, "send it to marc.petit@example.fr"))
	post(t, agent, "/openai/v1/chat/completions", "s3", turn(t, "bonjour"))
	postAs(t, agent, "/mask", testControlKey, "web", `{"texts":["paul.durand@example.fr"]}`)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		agent.URL+"/anthropic/v1/messages", strings.NewReader("whatever this is"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Encoding", "br")
	do(t, agent, req)

	heartbeat, _ := recorder.Take(time.Now())
	today := srv.today.reply(time.Now())
	day := today.Traffic
	if heartbeat.Requests != 4 || heartbeat.Refused != 1 || heartbeat.Upstream.Unreachable != 1 ||
		heartbeat.Tools.Calls != 1 || heartbeat.Tools.Restored != 1 {
		t.Fatalf("the heartbeat counted %d requests, %d refused, upstream %+v, tools %+v; want 4, "+
			"1, one unreachable, one call restored: the test drove less than it meant to",
			heartbeat.Requests, heartbeat.Refused, heartbeat.Upstream, heartbeat.Tools)
	}
	if day.Requests != heartbeat.Requests || day.Refused != heartbeat.Refused || day.Upstream != heartbeat.Upstream {
		t.Errorf("the day counted requests %d, refused %d, upstream %+v; the heartbeat %d, %d, %+v",
			day.Requests, day.Refused, day.Upstream,
			heartbeat.Requests, heartbeat.Refused, heartbeat.Upstream)
	}
	if day.Tools.Calls != heartbeat.Tools.Calls || day.Tools.Restored != heartbeat.Tools.Restored {
		t.Errorf("the day counted tools %+v; the heartbeat %d calls, %d restored",
			day.Tools, heartbeat.Tools.Calls, heartbeat.Tools.Restored)
	}
	// Each value appears once, so what was replaced and what was minted are the same
	// number here, though they are not in general (see tally).
	replaced, minted := 0, 0
	for _, n := range heartbeat.Masked {
		replaced += n
	}
	for _, n := range today.ByFamily {
		minted += n
	}
	if replaced != 3 || minted != replaced {
		t.Errorf("the heartbeat counted %d values replaced and the day %d minted, want 3 each", replaced, minted)
	}
}
