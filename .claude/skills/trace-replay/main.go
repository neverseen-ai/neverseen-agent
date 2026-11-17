// Command trace-replay sends the inbound body of each trace through the agent as it
// is built now, and compares what it would send to the provider with what the trace
// says was sent — newest trace first, until enough of them come out identical.
//
// It is a non-regression check run over real traffic rather than fixtures somebody
// wrote to pass. The comparison is modulo the numbering of replacements: a trace's
// tokens continue its session's numbering ([EMAIL_17]) where a replay starts from a
// fresh vault ([EMAIL_1]), so two bodies are the same when every replacement maps
// one-to-one onto a replacement of the same category and the text around them is
// byte-identical.
//
// Nothing is sent anywhere and nothing is written: http.DefaultTransport is replaced
// by one that records the request and answers it locally, the vault is in memory, and
// no names file, trace directory or control key is opened. The policy and the
// ~/.neverseen/.env file are read, as the agent beside it would read them.
//
// Run from the repository root:
//
//	go run ./.claude/skills/trace-replay [-dir traces] [-stop 20] [-show 5] [-trace file]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/neverseen-ai/neverseen-agent/internal/proxy"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
)

func main() {
	dir := flag.String("dir", "traces", "directory holding the traces")
	stop := flag.Int("stop", 20, "stop once this many traces have replayed identically")
	show := flag.Int("show", 5, "differing lines printed per trace")
	one := flag.String("trace", "", "replay this one trace and nothing else")
	flag.Parse()

	if err := proxy.LoadConfigFile(""); err != nil {
		fatal(err)
	}
	det, err := proxy.DetectorFromEnv(nil, proxy.DefaultPolicyFile)
	if err != nil {
		fatal(err)
	}
	providers, err := proxy.ProvidersFromEnv()
	if err != nil {
		fatal(err)
	}
	http.DefaultTransport = &upstream{}

	names := []string{*one}
	if *one == "" {
		if names, err = filepath.Glob(filepath.Join(*dir, "*.txt")); err != nil {
			fatal(err)
		}
		// The file name opens on the timestamp, so reverse lexical order is newest first.
		slices.Sort(names)
		slices.Reverse(names)
	}

	var same, differ, skipped, doubts int
	exercised := map[string]bool{}
	for _, name := range names {
		if *one == "" && same >= *stop {
			break
		}
		r := replayFile(name, providers, *show)
		fmt.Print(r.report)
		doubts += r.doubts
		switch r.verdict {
		case "SAME":
			same++
			maps.Copy(exercised, r.categories)
		case "SKIP":
			skipped++
		default:
			differ++
		}
	}
	fmt.Printf("\n%d replayed identically, %d differ, %d skipped, %d doubt(s), %d trace(s) available\n",
		same, differ, skipped, doubts, len(names))
	// An identical replay vouches only for the categories it carried: twenty traces
	// holding nothing but addresses say nothing about an IBAN.
	var tried, untried []string
	for _, c := range det.Categories() {
		if exercised[pii.Prefix(c)] {
			tried = append(tried, string(c))
		} else {
			untried = append(untried, string(c))
		}
	}
	fmt.Printf("categories the identical traces exercised: %s\n", strings.Join(tried, ", "))
	fmt.Printf("DOUBT    %d of %d categories never exercised: %s\n", len(untried), len(tried)+len(untried), strings.Join(untried, ", "))
	if same < *stop && *one == "" {
		fmt.Printf("ran out of traces before %d replayed identically\n", *stop)
	}
	if differ > 0 {
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "trace-replay:", err)
	os.Exit(2)
}

type result struct {
	verdict    string // SAME, DIFF, UNSTABLE, SKIP
	report     string
	doubts     int
	categories map[string]bool // token prefixes the replay minted
}

func replayFile(name string, providers []proxy.Provider, show int) result {
	base := filepath.Base(name)
	raw, err := os.ReadFile(name)
	if err != nil {
		return result{verdict: "SKIP", report: fmt.Sprintf("SKIP     %s: %v\n", base, err)}
	}
	tr, err := parse(string(raw))
	if err != nil {
		return result{verdict: "SKIP", report: fmt.Sprintf("SKIP     %s: %v\n", base, err)}
	}

	first, err := replay(tr, providers)
	if err != nil {
		return result{verdict: "DIFF", report: fmt.Sprintf("DIFF     %s: %v\n", base, err)}
	}
	// A fresh vault each time, so two replays of one body must agree byte for byte.
	// When they do not, the agent is not deterministic and every other comparison
	// below is noise.
	second, err := replay(tr, providers)
	if err != nil {
		return result{verdict: "UNSTABLE", report: fmt.Sprintf("UNSTABLE %s: the second replay failed: %v\n", base, err)}
	}
	if first != second {
		return result{verdict: "UNSTABLE", report: fmt.Sprintf("UNSTABLE %s: two replays of one body produced different bodies\n", base)}
	}

	c := compare(tr, indent(first), show)
	verdict := "SAME"
	if len(c.diffs) > 0 {
		verdict = "DIFF"
	}
	return result{
		verdict:    verdict,
		report:     fmt.Sprintf("%-8s %s\n%s%s", verdict, base, strings.Join(c.diffs, ""), strings.Join(c.doubts, "")),
		doubts:     len(c.doubts),
		categories: c.categories,
	}
}

// trace is what a replay needs out of one file. parseTrace in internal/proxy is the
// reference reader; it is unexported, so this repeats the part of it used here.
type trace struct {
	session, provider string
	in, out           string // as written: indented when the body was JSON
}

func parse(content string) (trace, error) {
	var t trace
	var in, out []string
	section := ""
	for _, line := range strings.Split(content, "\n") {
		if label, ok := strings.CutPrefix(line, "── "); ok {
			switch {
			case strings.HasPrefix(label, "IN "):
				section = "in"
			case strings.HasPrefix(label, "OUT "):
				section = "out"
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
			// The response header is written after the OUT body, at column 0.
			if isHeader(line) {
				section = ""
				break
			}
			out = append(out, line)
			continue
		}
		if !isHeader(line) {
			continue
		}
		key, value, _ := strings.Cut(line, ":")
		switch key {
		case "session":
			t.session = strings.TrimSpace(value)
		case "provider":
			t.provider = strings.TrimSpace(value)
		}
	}
	t.in = strings.TrimSpace(strings.Join(in, "\n"))
	t.out = strings.TrimSpace(strings.Join(out, "\n"))
	if t.provider == "" || t.in == "" || t.out == "" {
		return t, errors.New("not a trace with both bodies")
	}
	return t, nil
}

// isHeader is a `key: value` line at column 0. A JSON body's lines are indented or
// are a lone bracket, so they never match.
func isHeader(line string) bool {
	key, _, ok := strings.Cut(line, ":")
	return ok && key != "" && !strings.ContainsAny(key[:1], " {}[]\"") && !strings.Contains(key, " ")
}

// replay sends the trace's inbound body through a fresh agent and returns the body
// that reached the provider.
//
// Fresh down to the detector: older builds numbered tokens on a counter the detector
// held, so a detector shared between two replays continued its numbering and an old
// commit replayed against itself reported UNSTABLE.
func replay(t trace, providers []proxy.Provider) (string, error) {
	det, err := proxy.DetectorFromEnv(nil, proxy.DefaultPolicyFile)
	if err != nil {
		return "", err
	}
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		return "", err
	}
	srv, err := proxy.New(proxy.Config{Providers: providers}, det, v)
	if err != nil {
		return "", err
	}

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9787/"+t.provider+"/v1/messages", strings.NewReader(t.in))
	req.Header.Set("Content-Type", "application/json")
	if t.session != "default" {
		req.Header.Set("X-Session-Id", t.session)
	}
	sent := &captured{}
	req = req.WithContext(context.WithValue(req.Context(), captureKey{}, sent))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if !sent.ok {
		return "", fmt.Errorf("the agent forwarded nothing: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	return sent.body, nil
}

func indent(body string) string {
	var out bytes.Buffer
	if err := json.Indent(&out, []byte(body), "", "  "); err != nil {
		return strings.TrimSpace(body)
	}
	return strings.TrimSpace(out.String())
}

// comparison is what one trace's replay found: the lines that differ, the doubts —
// what is identical but should not be trusted as it stands — and the categories the
// replay minted, which is what an identical verdict vouches for.
type comparison struct {
	diffs, doubts []string
	categories    map[string]bool
}

// compare reads the recorded and the replayed outbound bodies line by line. Both are
// indented by the same function and a string never spans two lines, so a masking
// change stays on its line and a shape change shows as a different line count.
func compare(t trace, replayed string, show int) comparison {
	recLines := strings.Split(t.out, "\n")
	repLines := strings.Split(replayed, "\n")
	inLines := strings.Split(t.in, "\n")

	if len(recLines) != len(repLines) {
		i := firstDifference(recLines, repLines)
		return comparison{diffs: []string{fmt.Sprintf("  the outbound document changed shape: %d lines recorded, %d replayed; first difference at line %d\n"+
			"    recorded: %s\n    replayed: %s\n", len(recLines), len(repLines), i+1, at(recLines, i), at(repLines, i))}}
	}

	// Token strings the tool itself sent — a model's earlier answer echoing [MONGOID_3]
	// back in the history — are text, not replacements. A fresh vault numbers from 1,
	// so the replay can mint that very string for another value, which the agent's
	// own session, already further along, did not.
	literal := map[string]bool{}
	pii.ReplaceTokens(t.in, func(tok string) (string, bool) { literal[tok] = true; return "", false })

	m := renaming{fwd: map[string]string{}, back: map[string]string{}, literal: literal,
		collisions: map[string]string{}, categories: map[string]bool{}}
	var report []string
	differing := 0
	for i := range recLines {
		if m.equal(recLines[i], repLines[i]) {
			continue
		}
		differing++
		if differing > show {
			continue
		}
		var in string
		if len(inLines) == len(recLines) {
			in = inLines[i]
		}
		report = append(report, describe(i+1, in, recLines[i], repLines[i]))
	}
	if differing > show {
		report = append(report, fmt.Sprintf("  … and %d more differing line(s)\n", differing-show))
	}
	return comparison{diffs: report, doubts: m.doubts(t), categories: m.categories}
}

// doubts names what an identical verdict on this trace does not settle.
func (m *renaming) doubts(t trace) []string {
	var out []string
	for _, tok := range slices.Sorted(maps.Keys(m.collisions)) {
		// The tool sent this string as text and the agent also minted it for a value,
		// so restoring the answer cannot tell the two apart. Recorded means it
		// happened live; replay means a session starting afresh would do it.
		out = append(out, fmt.Sprintf("  DOUBT    %s was minted for a value (%s) while the tool had already sent it as text: restoring the answer cannot tell the two apart\n",
			tok, m.collisions[tok]))
	}
	// TODO: a heuristic. A fake-mode body that also carries a bracket goes unnoticed;
	// the upgrade is to ask the stored policy for the mode, or to find stand-ins with
	// the detector's own table as UnmaskSeen does.
	if t.in != t.out && !strings.Contains(t.out, "[") {
		out = append(out, "  DOUBT    the bodies differ but the outbound one holds no token: likely fake mode, which this comparison cannot read\n")
	}
	return out
}

// renaming is the one-to-one correspondence between the trace's replacements and the
// replay's, built as the lines are read. It is global to the document: a value named
// [EMAIL_17] in one field and [EMAIL_1] in the replay must be [EMAIL_1] everywhere.
type renaming struct {
	fwd, back map[string]string
	literal   map[string]bool

	// collisions are minted tokens whose string the tool also sent as text, by side:
	// "recorded" or "replay".
	collisions map[string]string
	categories map[string]bool
}

func (m *renaming) equal(rec, rep string) bool {
	if rec == rep && !strings.Contains(rec, "[") {
		return true
	}
	recLits, recToks := split(rec)
	repLits, repToks := split(rep)
	if len(recToks) != len(repToks) {
		return false
	}
	for i := range recLits {
		if recLits[i] != repLits[i] {
			return false
		}
	}
	for i, a := range recToks {
		b := repToks[i]
		// Identical on both sides and present in the inbound body: the tool's own
		// text, matched without entering the renaming. Excluding every string found
		// in the inbound body instead would refuse the replay's [MONGOID_3] standing
		// for a value the trace named [MONGOID_13].
		if a == b && m.literal[a] {
			continue
		}
		if category(a) != category(b) {
			return false
		}
		if x, ok := m.fwd[a]; ok && x != b {
			return false
		}
		if x, ok := m.back[b]; ok && x != a {
			return false
		}
		m.fwd[a], m.back[b] = b, a
		m.categories[category(b)] = true
		if m.literal[a] {
			m.collisions[a] = "recorded"
		}
		if m.literal[b] {
			m.collisions[b] = "replay"
		}
	}
	return true
}

// split cuts text into the literals around its tokens and the tokens themselves:
// lits has one more entry than toks.
func split(text string) (lits, toks []string) {
	pii.ReplaceTokens(text, func(tok string) (string, bool) { toks = append(toks, tok); return "", false })
	rest := text
	for _, tok := range toks {
		i := strings.Index(rest, tok)
		lits = append(lits, rest[:i])
		rest = rest[i+len(tok):]
	}
	return append(lits, rest), toks
}

// category is a token's name without its number: [EMAIL_17] gives EMAIL.
func category(tok string) string {
	body := strings.Trim(tok, "[]")
	if i := strings.LastIndex(body, "_"); i > 0 {
		return body[:i]
	}
	return body
}

// describe says what differs on one line: which values the trace masked and the
// replay no longer does, which the replay masks and the trace did not, or — when
// both masked the same values — that the text around them moved.
func describe(line int, in, rec, rep string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  line %d", line)
	if key := fieldName(rec); key != "" {
		fmt.Fprintf(&b, " (%s)", key)
	}
	b.WriteString("\n")

	// Only when both lines align with the inbound one: a line whose text moved
	// cannot say which values it carries, and guessing reports a leak that is not.
	was, okWas := values(in, rec)
	now, okNow := values(in, rep)
	if okWas && okNow {
		lost, gained := 0, 0
		for v, tok := range was {
			if _, ok := now[v]; !ok {
				lost++
				fmt.Fprintf(&b, "    no longer masked  %-14s %s\n", category(tok), clip(v, 100))
			}
		}
		for v, tok := range now {
			if _, ok := was[v]; !ok {
				gained++
				fmt.Fprintf(&b, "    newly masked      %-14s %s\n", category(tok), clip(v, 100))
			}
		}
		for v, tok := range was {
			if n, ok := now[v]; ok && category(n) != category(tok) {
				lost++
				fmt.Fprintf(&b, "    category changed  %s → %s  %s\n", category(tok), category(n), clip(v, 100))
			}
		}
		if lost+gained > 0 {
			return b.String()
		}
	}
	i := firstByte(rec, rep)
	fmt.Fprintf(&b, "    text around the replacements changed at byte %d\n    recorded: …%s…\n    replayed: …%s…\n",
		i, window(rec, i), window(rep, i))
	return b.String()
}

// values reads which original each token on a masked line stands for, by aligning it
// with the inbound line it came from — the literals between tokens locate the values.
// The same reading as recoverMapping in internal/proxy, by value rather than by token.
// It reports false when the line does not align: text outside the values moved.
func values(original, masked string) (map[string]string, bool) {
	out := map[string]string{}
	lits, toks := split(masked)
	if len(toks) == 0 {
		return out, original == masked
	}
	if !strings.HasPrefix(original, lits[0]) {
		return out, false
	}
	cursor := len(lits[0])
	for i, tok := range toks {
		next := lits[i+1]
		var end int
		switch {
		case i == len(toks)-1:
			if !strings.HasSuffix(original[cursor:], next) {
				return out, false
			}
			end = len(original) - len(next)
		case next == "":
			// Two tokens back to back: no literal to find the boundary by.
			return out, false
		default:
			j := strings.Index(original[cursor:], next)
			if j < 0 {
				return out, false
			}
			end = cursor + j
		}
		// A token standing for itself is one the tool sent, not a replacement.
		if end >= cursor && original[cursor:end] != tok {
			out[original[cursor:end]] = tok
		}
		cursor = end + len(next)
	}
	return out, true
}

func fieldName(line string) string {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "\"") {
		return ""
	}
	if end := strings.Index(t[1:], "\":"); end >= 0 {
		return t[1 : end+1]
	}
	return ""
}

func firstDifference(a, b []string) int {
	for i := range a {
		if i >= len(b) || a[i] != b[i] {
			return i
		}
	}
	return len(a)
}

func firstByte(a, b string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

func at(lines []string, i int) string {
	if i < len(lines) {
		return clip(strings.TrimSpace(lines[i]), 160)
	}
	return "(end of document)"
}

func window(s string, i int) string {
	from, to := max(0, i-60), min(len(s), i+60)
	return s[from:to]
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("… (%d bytes)", len(s))
}

// upstream stands in for every provider: it records the body the agent forwarded and
// answers with an empty message, so nothing leaves the machine.
type upstream struct{}

func (upstream) RoundTrip(r *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if c, ok := r.Context().Value(captureKey{}).(*captured); ok {
		c.body, c.ok = string(body), true
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"type":"message","content":[]}`)),
		Request:    r,
	}, nil
}

type captureKey struct{}

// captured is where upstream leaves the forwarded body for the replay that sent it.
type captured struct {
	body string
	ok   bool
}
