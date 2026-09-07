package proxy

import (
	"testing"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	contract "github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
)

// The traces the replay reads are the ones the tracer writes, so the fixture is
// written by the tracer itself: a format change on either side fails here rather
// than in a replay somebody runs six months later over files the parser no longer
// understands.
func TestReplayRebuildsTheHeartbeatFromTraces(t *testing.T) {
	dir := t.TempDir()
	tr, err := newTracer(dir)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 28, 8, 24, 36, 0, time.UTC)
	tr.now = func() time.Time { return at }

	det := detector.New(detector.Config{Locales: []string{"fr"}})

	// Two exchanges in one window: a Claude Code conversation, then the same one
	// again, with a tool call whose arguments carry the restored address.
	body := `{"metadata":{"user_id":"{\"session_id\":\"18af0b2c-6b1e-4d8f-9a1b-2c3d4e5f6a7b\"}"},"c":"cherche claire@example.fr"}`
	masked := `{"metadata":{"user_id":"{\"session_id\":\"18af0b2c-6b1e-4d8f-9a1b-2c3d4e5f6a7b\"}"},"c":"cherche [EMAIL_1]"}`
	path, err := tr.write("default", "anthropic", body, masked, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.appendResponse(&traceRef{path: path, provider: "anthropic", model: "claude-sonnet-4",
		usage: contract.TokenUsage{Input: 12, Output: 9, CacheRead: 900}},
		`{"model":"claude-sonnet-4","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":12,"output_tokens":9}}`); err != nil {
		t.Fatal(err)
	}

	at = at.Add(10 * time.Second)
	path, err = tr.write("default", "anthropic", body, masked, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-sonnet-4\"}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"name\":\"Bash\",\"input\":{}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"command\\\":\\\"grep -rn [EMAIL_1] notes/\\\"}\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
	if err := tr.appendResponse(&traceRef{path: path, provider: "anthropic", model: "claude-sonnet-4",
		usage: contract.TokenUsage{Input: 3, Output: 20}}, stream); err != nil {
		t.Fatal(err)
	}

	// A third, an hour later, from another tool with a session header and no
	// answer filed: the provider never replied.
	at = at.Add(time.Hour)
	if _, err := tr.write("cursor-7", "openai", `{"c":"NIR 184037511600176"}`, `{"c":"[NIR_1]"}`, 1, nil); err != nil {
		t.Fatal(err)
	}

	batch, err := Replay(dir, det, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	// Three: the two exchanges at 08:24; the window in which the conversation went
	// idle and closed, half an hour after its last request; and the exchange an
	// hour later. A bucket with nothing but a closed session is a bucket, because
	// the histograms in it are what the session counts exist for.
	if len(batch.Buckets) != 3 {
		t.Fatalf("got %d buckets, want 3\n%+v", len(batch.Buckets), batch.Buckets)
	}
	first := batch.Buckets[0]
	if want := time.Date(2026, 8, 28, 8, 20, 0, 0, time.UTC); !first.Window.Start.Equal(want) || !first.Window.End.Equal(want.Add(5*time.Minute)) {
		t.Errorf("first window = %v..%v, want aligned on the interval from %v", first.Window.Start, first.Window.End, want)
	}
	c := first.Counters
	if c.Requests != 2 || c.Providers["anthropic"] != 2 || c.Restarts != 0 {
		t.Errorf("first bucket = requests %d providers %v restarts %d, want 2, anthropic:2, 0", c.Requests, c.Providers, c.Restarts)
	}
	if c.Masked["EMAIL"] != 2 {
		t.Errorf("masked = %v, want EMAIL:2 — the body is masked again with today's detector", c.Masked)
	}
	if got := c.Models["claude-sonnet-4"]; got != (contract.TokenUsage{Input: 15, Output: 29, CacheRead: 900}) {
		t.Errorf("usage = %+v, want the two header lines summed", got)
	}
	if c.Sessions.Active != 1 || c.Sessions.Opened != 1 {
		t.Errorf("sessions = %+v, want one conversation, read from metadata.user_id", c.Sessions)
	}
	if c.Tools.Calls != 1 || c.Tools.Restored != 1 || c.Tools.Names["Bash"] != 1 || c.Tools.Programs["grep"] != 1 {
		t.Errorf("tools = %+v, want one Bash call running grep with the address restored", c.Tools)
	}
	if len(c.Clients) != 0 || c.Upstream != (contract.Upstream{}) {
		t.Errorf("a replay reported what a trace does not hold: clients %v upstream %+v", c.Clients, c.Upstream)
	}

	closing := batch.Buckets[1]
	if want := time.Date(2026, 8, 28, 8, 50, 0, 0, time.UTC); !closing.Window.Start.Equal(want) {
		t.Errorf("the conversation closed in the window starting %v, want %v: thirty idle minutes after 08:24:46", closing.Window.Start, want)
	}
	if s := closing.Counters.Sessions; s.Closed != 1 || s.Requests == nil || s.Requests[2] != 1 || s.ToolCalls[1] != 1 {
		t.Errorf("sessions = %+v, want the conversation closed with 2 requests and 1 tool call", s)
	}

	third := batch.Buckets[2].Counters
	if third.Requests != 1 || third.Providers["openai"] != 1 || third.Masked["NIR"] != 1 {
		t.Errorf("third bucket = %+v, want the one openai exchange with its NIR", third)
	}
	if third.Sessions.Opened != 1 {
		t.Errorf("sessions = %+v, want the cursor session opened", third.Sessions)
	}
	if batch.State.Locales[0] != "fr" || batch.Schema != contract.SchemaVersion {
		t.Errorf("batch state = %+v, want the detector's own", batch.State)
	}
}

func TestReplayOverAnEmptyDirectoryIsAnEmptyBatch(t *testing.T) {
	batch, err := Replay(t.TempDir(), detector.New(detector.Config{}), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Buckets) != 0 {
		t.Errorf("got %d buckets from nothing", len(batch.Buckets))
	}
}

func TestParseTokensReadsTheTraceLine(t *testing.T) {
	got := parseTokens("in 1204, out 98, cache read 184302, cache write 7")
	if got != (contract.TokenUsage{Input: 1204, Output: 98, CacheRead: 184302, CacheWrite: 7}) {
		t.Errorf("parsed %+v", got)
	}
}

func TestRecoverMappingReadsTheTracesOwnTokens(t *testing.T) {
	in := `{"messages":[{"content":"écrire à claire@example.fr et à paul@example.fr, NIR 184037511600176"}],"n":1}`
	out := `{"messages":[{"content":"écrire à [EMAIL_1] et à [EMAIL_2], NIR [NIR_1]"}],"n":1}`
	got := recoverMapping(in, out)
	want := map[string]string{"[EMAIL_1]": "claire@example.fr", "[EMAIL_2]": "paul@example.fr", "[NIR_1]": "184037511600176"}
	if len(got) != len(want) {
		t.Fatalf("recovered %v, want %v", got, want)
	}
	for token, original := range want {
		if got[token] != original {
			t.Errorf("%s = %q, want %q", token, got[token], original)
		}
	}
	// A body that is not JSON is read as one string.
	if got := recoverMapping("to claire@example.fr", "to [EMAIL_1]"); got["[EMAIL_1]"] != "claire@example.fr" {
		t.Errorf("flat text: %v", got)
	}
	// Bodies that differ in shape yield nothing rather than a guess.
	if got := recoverMapping(`{"a":"x"}`, `{"b":"[EMAIL_1]"}`); len(got) != 0 {
		t.Errorf("mismatched shapes yielded %v", got)
	}
}
