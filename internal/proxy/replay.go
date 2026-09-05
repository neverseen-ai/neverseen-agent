package proxy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/telemetry"
	"github.com/cloakfleet/cloakfleet/pkg/pii"
	contract "github.com/cloakfleet/cloakfleet/pkg/telemetry"
)

// Rebuilding the heartbeat from trace files.
//
// A trace holds almost everything a heartbeat is made of, one exchange per file:
// the session and the body as the tool sent it (so the conversation id and, by
// masking it again, the categories), the provider, the model and its token
// counts, and the answer as it arrived (so the tool calls). Replayed through the
// same recorder the agent uses, dated by the file name, they come back out as the
// buckets the agent would have filed — which is how the counters are checked
// against real traffic rather than against fixtures somebody wrote to pass.
//
// Read-only, and deliberately so. The batch is printed, never queued in
// buffer.json and never sent: a bucket rebuilt from traces and a bucket the agent
// filed live share no window boundary, so the backend's (agent, window) key would
// not recognise the one as a retry of the other, and every count for the period
// would double.
//
// Two things a trace does not hold, so a replay never reports them: the client's
// User-Agent (Clients stays empty) and how the provider answered (Upstream stays
// empty). The window's Restarts is zero too — no process started.

// ReplayInterval is the window a replay closes a bucket on, the reporter's own.
const ReplayInterval = telemetry.DefaultInterval

// Replay rebuilds the heartbeat batch for every trace under dir, masking each
// exchange again with det to recover what the agent would have counted.
func Replay(dir string, det *detector.Detector, now time.Time) (contract.HeartbeatBatch, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil {
		return contract.HeartbeatBatch{}, err
	}
	// The file name opens on the timestamp, so lexical order is chronological.
	sort.Strings(names)

	batch := contract.HeartbeatBatch{
		Schema: contract.SchemaVersion,
		SentAt: now,
		State:  detectorState(det),
	}
	if len(names) == 0 {
		return batch, nil
	}

	at := time.Time{}
	recorder := telemetry.NewRecorder(at).WithClock(func() time.Time { return at })
	// The constructor counts a process start, and a replay is not one. The first
	// Take discards it with the window it opened.
	recorder.Take(at)

	windowEnd := time.Time{}
	close := func(end time.Time) {
		counters, window := recorder.Take(end)
		if counters.Requests > 0 || counters.Sessions.Closed > 0 {
			batch.Buckets = append(batch.Buckets, contract.Bucket{Window: window, Counters: counters})
		}
	}

	for _, name := range names {
		when, ok := traceTime(filepath.Base(name))
		if !ok {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return batch, fmt.Errorf("read %s: %w", name, err)
		}
		exchange, ok := parseTrace(string(raw))
		if !ok {
			continue
		}

		// Windows are aligned on the interval, as a reporter's are up to the drift
		// of its ticker, so a rebuilt bucket lands beside a live one on a dashboard.
		if windowEnd.IsZero() {
			start := when.Truncate(ReplayInterval)
			recorder.Take(start)
			windowEnd = start.Add(ReplayInterval)
		}
		for !when.Before(windowEnd) {
			at = windowEnd
			close(windowEnd)
			windowEnd = windowEnd.Add(ReplayInterval)
		}
		at = when
		replayExchange(recorder, det, exchange)
	}
	at = windowEnd
	close(windowEnd)
	return batch, nil
}

// replayExchange feeds one traced exchange to the recorder as the request path
// would have.
func replayExchange(recorder *telemetry.Recorder, det *detector.Detector, ex tracedExchange) {
	host := defaultHost(ex.provider)

	// Masked again, from the body as the tool sent it, with the detector of today.
	// That is the point rather than a limitation: the trace holds a count, and the
	// question a replay answers is what the current catalogue makes of the traffic.
	pass := det.NewPass(nil)
	mask := func(text string) string {
		out, _ := det.Mask(text, pass)
		return out
	}
	conversation := ex.session
	doc, decodeErr := decodeJSONBody([]byte(ex.in))
	if decodeErr == nil {
		pass.Exempt = exemptIdentifiers(host, doc)
		conversation = conversationOf(ex.session, host, doc)
		mapStrings(doc, mask)
	} else {
		mask(ex.in)
	}

	recorder.Request(conversation, "", ex.provider)
	recorder.Masked(conversation, pass.Counts())
	recorder.Usage(conversation, ex.model, ex.usage)

	if ex.answer == "" {
		return
	}
	// The mapping the answer was expanded against is the trace's own, read off its
	// two bodies — not what the pass above minted. Token indices are a counter on
	// the detector, so a replay numbers the same address [EMAIL_2] where the trace
	// said [EMAIL_1], and an answer's tool call carries the trace's number.
	known := recoverMapping(ex.in, ex.out)
	report := func(name, arguments string, restored bool) {
		recorder.Tool(conversation, telemetry.ToolCall{Name: name, Arguments: arguments, Restored: restored})
	}
	if strings.HasPrefix(strings.TrimLeft(ex.answer, "\n"), "event:") || strings.HasPrefix(strings.TrimLeft(ex.answer, "\n"), "data:") {
		stream := newStreamRehydrator(io.NopCloser(strings.NewReader(ex.answer)), known,
			func(string, contract.TokenUsage) {}, nil)
		stream.onTool = report
		stream.onDegraded = recorder.Degraded
		_, _ = io.Copy(io.Discard, stream)
		_ = stream.Close()
		return
	}
	if answer, err := decodeJSONBody([]byte(ex.answer)); err == nil {
		reportToolCalls(answer, known, nil, report)
	}
}

// tracedExchange is what a replay reads out of one trace file.
type tracedExchange struct {
	session, provider string
	// in is the body as the tool sent it, and out the body as it left.
	in, out string
	// model and usage are the answer's cost, from the header the tracer wrote.
	model string
	usage contract.TokenUsage
	// answer is the provider's answer as it arrived, or "" when none was filed.
	answer string
}

// traceTime reads the timestamp a trace file's name opens on — see
// tracer.filename, which writes it in UTC.
func traceTime(name string) (time.Time, bool) {
	stamp, _, ok := strings.Cut(name, "-")
	if !ok {
		return time.Time{}, false
	}
	when, err := time.Parse("20060102T150405", stamp)
	if err != nil {
		return time.Time{}, false
	}
	return when, true
}

// parseTrace reads a trace file back. It is the inverse of tracer.write and
// traceResponse, and tested against them rather than against a fixture, so a
// change to the format on either side fails here.
func parseTrace(content string) (tracedExchange, bool) {
	var ex tracedExchange
	lines := strings.Split(content, "\n")

	// The header: key/value lines at column 0, before the first rule and again
	// between the OUT body and the BACK rule. A JSON body's lines are indented or
	// open on a brace, so a bare `key:` at column 0 is never one of them.
	section := ""
	var in, out, answer []string
	for _, line := range lines {
		if strings.HasPrefix(line, "── ") {
			label := strings.TrimPrefix(line, "── ")
			switch {
			case strings.HasPrefix(label, "IN "):
				section = "in"
			case strings.HasPrefix(label, "OUT "):
				section = "out"
			case strings.HasPrefix(label, "BACK ") && strings.Contains(label, "reassembled"):
				section = "reassembled"
			case strings.HasPrefix(label, "BACK "):
				section = "back"
				answer = nil
			default:
				section = ""
			}
			continue
		}
		switch section {
		case "in":
			in = append(in, line)
			continue
		case "out":
			// The response header — back:, model:, tokens: — is written after the
			// OUT body, at column 0, so it is read below rather than kept as body.
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "{") && !strings.HasPrefix(line, "}") && strings.Contains(line, ":") && !strings.HasPrefix(line, "\"") {
				break
			}
			out = append(out, line)
			continue
		case "back":
			answer = append(answer, line)
			continue
		case "reassembled":
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "{") || strings.HasPrefix(line, "}") {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "session":
			ex.session = value
		case "provider":
			ex.provider = value
		case "model":
			ex.model = value
		case "tokens":
			ex.usage = parseTokens(value)
		}
	}
	if ex.session == "" || ex.provider == "" {
		return ex, false
	}
	ex.in = strings.TrimSpace(strings.Join(in, "\n"))
	ex.out = strings.TrimSpace(strings.Join(out, "\n"))
	ex.answer = strings.TrimSpace(strings.Join(answer, "\n"))
	return ex, true
}

// parseTokens reads the line traceResponse writes as
// "in 1204, out 98, cache read 184302, cache write 0".
func parseTokens(value string) contract.TokenUsage {
	var usage contract.TokenUsage
	for _, part := range strings.Split(value, ",") {
		fields := strings.Fields(part)
		if len(fields) < 2 {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(fields[len(fields)-1], "%d", &n); err != nil {
			continue
		}
		switch strings.Join(fields[:len(fields)-1], " ") {
		case "in":
			usage.Input = n
		case "out":
			usage.Output = n
		case "cache read":
			usage.CacheRead = n
		case "cache write":
			usage.CacheWrite = n
		}
	}
	return usage
}

// recoverMapping reads the trace's own mapping, replacement → original, off its
// two bodies: wherever OUT holds a token that IN does not, the text IN holds in
// its place is the original.
//
// Token mode only. A stand-in has no shape a reader can find in OUT without the
// table it came from, so a trace made in fake mode yields an empty mapping and the
// replay counts its tool calls without knowing which were restored.
// TODO: fake mode. The upgrade is the same structural walk with today's stand-in
// table, which finds a stand-in as UnmaskSeen does — once a replay over fake-mode
// traces is something somebody needs.
func recoverMapping(in, out string) map[string]string {
	known := make(map[string]string)
	inDoc, errIn := decodeJSONBody([]byte(in))
	outDoc, errOut := decodeJSONBody([]byte(out))
	if errIn == nil && errOut == nil {
		pairStrings(inDoc, outDoc, func(original, masked string) { pairTokens(original, masked, known) })
	} else {
		pairTokens(in, out, known)
	}
	return known
}

// pairStrings walks two documents of one shape side by side, calling f on each
// pair of strings that differ. Shapes that diverge are skipped, not guessed at.
func pairStrings(a, b any, f func(original, masked string)) {
	switch x := a.(type) {
	case string:
		if y, ok := b.(string); ok && x != y {
			f(x, y)
		}
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return
		}
		for i := range x {
			pairStrings(x[i], y[i], f)
		}
	case jsonObject:
		y, ok := b.(jsonObject)
		if !ok || len(x) != len(y) {
			return
		}
		for i := range x {
			if x[i].key == y[i].key {
				pairStrings(x[i].value, y[i].value, f)
			}
		}
	}
}

// pairTokens reads the originals out of one string pair. masked is
// s0 t1 s1 t2 … tk sk — literal text around tokens — and original is the same
// with each token replaced by its value, so the literals locate the values.
//
// The literal after a token is looked for at its first occurrence, which is wrong
// when the original itself contains it; the last token takes everything up to the
// trailing literal, which is exact. Good enough for what the mapping is used for
// here — deciding whether a tool call carried a restored value — and the failure
// is a wrong original in a map that never leaves the process.
func pairTokens(original, masked string, known map[string]string) {
	var tokens []string
	pii.ReplaceTokens(masked, func(token string) (string, bool) {
		tokens = append(tokens, token)
		return "", false
	})
	if len(tokens) == 0 {
		return
	}

	cursorM, cursorO := 0, 0
	for i, token := range tokens {
		at := strings.Index(masked[cursorM:], token)
		if at < 0 {
			return
		}
		literal := masked[cursorM : cursorM+at]
		if !strings.HasPrefix(original[cursorO:], literal) {
			return
		}
		cursorO += len(literal)
		cursorM += at + len(token)

		var value string
		if i == len(tokens)-1 {
			tail := masked[cursorM:]
			if !strings.HasSuffix(original[cursorO:], tail) {
				return
			}
			value = original[cursorO : len(original)-len(tail)]
		} else {
			next := strings.Index(masked[cursorM:], tokens[i+1])
			if next < 0 {
				return
			}
			between := masked[cursorM : cursorM+next]
			end := len(original[cursorO:])
			if between != "" {
				if end = strings.Index(original[cursorO:], between); end < 0 {
					return
				}
			}
			value = original[cursorO : cursorO+end]
			cursorO += end
		}
		if value != "" {
			known[token] = value
		}
	}
}
