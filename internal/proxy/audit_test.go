package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
)

// Audit mode is the one surface in this agent that prints a real value, so what
// it prints is asserted on the bytes rather than trusted: an audit that showed a
// value it had not actually replaced, or stayed silent about one it had, is worse
// than no audit at all — somebody reads it and believes it.

// safeBuffer is a buffer two goroutines may write while the test reads. The
// auditor serialises its own lines; this is for the test's own read.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// newAuditingAgent is newAgent with a console attached, and nothing else changed:
// the point of the mode is that it audits the same pipeline.
func newAuditingAgent(t *testing.T, up *upstream, locales []string) (*httptest.Server, *safeBuffer) {
	t.Helper()

	console := &safeBuffer{}
	det := detector.New(detector.Config{Locales: locales})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}

	srv, err := New(Config{
		Providers: []Provider{{Code: "anthropic", BaseURL: up.server.URL}},
		Audit:     console,
	}, det, v)
	if err != nil {
		t.Fatal(err)
	}

	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)
	return agent, console
}

// newTracingAgent is the same pipeline with both flags: a console and a trace
// directory. The directory is the test's own, so a run never writes into a tree.
func newTracingAgent(t *testing.T, up *upstream, locales []string) (*httptest.Server, *safeBuffer, string) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "traces")
	traces, err := newTracer(dir)
	if err != nil {
		t.Fatal(err)
	}

	console := &safeBuffer{}
	det := detector.New(detector.Config{Locales: locales})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}

	srv, err := New(Config{
		Providers: []Provider{{Code: "anthropic", BaseURL: up.server.URL}},
		Audit:     console,
		Traces:    traces,
	}, det, v)
	if err != nil {
		t.Fatal(err)
	}

	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)
	return agent, console, dir
}

// traceFiles returns the traces written so far, oldest first, as text.
func traceFiles(t *testing.T, dir string) []string {
	t.Helper()

	names, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)

	out := make([]string, 0, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(raw))
	}
	return out
}

// auditLines returns the MASK and UNMASK pairs the console reported, in order.
//
// Parsed rather than compared to a fixed string, because the token index is
// process-wide: a session's first email may be [EMAIL_4], and a test pinned to
// [EMAIL_1] would fail on whichever other test happened to run first.
//
// Split on " TO " rather than on whitespace: a value is regularly several words —
// an address, a spaced identifier — and a fields count would read one as several.
// Escapes are stripped first, so the pairs are the values themselves whether the
// console was painting or not.
func auditLines(t *testing.T, console *safeBuffer, action string) [][2]string {
	t.Helper()

	var pairs [][2]string
	for _, line := range strings.Split(stripANSI(console.String()), "\n") {
		rest, ok := strings.CutPrefix(line, action+" ")
		if !ok {
			continue
		}
		from, to, found := strings.Cut(rest, " TO ")
		if !found {
			continue
		}
		pairs = append(pairs, [2]string{from, to})
	}
	return pairs
}

var ansiRe = regexp.MustCompile("\033\\[[0-9;]*m")

func stripANSI(text string) string { return ansiRe.ReplaceAllString(text, "") }

// The round trip, both halves, in the operator's own vocabulary.
func TestAuditReportsWhatWasMaskedAndRestored(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, console := newAuditingAgent(t, up, []string{"fr"})

	const email = "pierre.paul@example.fr"
	body := fmt.Sprintf(`{"prompt":%q}`, "write to "+email+", that is "+email)
	reply := post(t, agent, "/anthropic/v1/messages", "audit", body)
	if reply.status != http.StatusOK {
		t.Fatalf("status = %d, body %s", reply.status, reply.body)
	}

	masked := auditLines(t, console, "MASK")
	// One line, not two: the same value twice in one body is one transformation
	// to look at. Reported per occurrence, a system prompt resent every turn
	// would bury the exchange being audited.
	if len(masked) != 1 {
		t.Fatalf("MASK lines = %v, want exactly one", masked)
	}
	if masked[0][0] != email {
		t.Errorf("MASK reported %q, want the value in clear %q", masked[0][0], email)
	}
	token := masked[0][1]

	// The token it named is the token that actually left the machine. Without
	// this, the console could be reporting a mapping the request never used.
	bodies, _ := up.received()
	if len(bodies) != 1 || !strings.Contains(bodies[0], token) {
		t.Errorf("upstream received %q, want it to carry %q", bodies, token)
	}
	if strings.Contains(bodies[0], email) {
		t.Errorf("upstream received the value in clear: %q", bodies[0])
	}

	restored := auditLines(t, console, "UNMASK")
	if len(restored) != 1 || restored[0][0] != token || restored[0][1] != email {
		t.Errorf("UNMASK lines = %v, want one %s TO %s", restored, token, email)
	}
}

// The streaming path expands through its own held-back tail, so it reports
// through its own callback and has to be asserted separately: covered only by the
// buffered case, a stream could restore values and report none of them.
func TestAuditReportsAValueRestoredInAStream(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		// The provider echoes the token back in generated text, split down the
		// middle across two events, which is the case the held-back tail exists
		// for: the console must report the value the caller actually read, not
		// the halves that arrived.
		token := "[EMAIL_1]"
		if i := strings.Index(string(body), "[EMAIL_"); i >= 0 {
			if j := strings.Index(string(body)[i:], "]"); j >= 0 {
				token = string(body)[i : i+j+1]
			}
		}
		cut := len(token) / 2

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, piece := range []string{"Contact: " + token[:cut], token[cut:] + " — done"} {
			fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", anthropicDelta(piece))
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	agent, console := newAuditingAgent(t, up, []string{"fr"})

	const email = "pierre.paul@example.fr"
	reply := post(t, agent, "/anthropic/v1/messages", "audit-stream",
		fmt.Sprintf(`{"prompt":%q}`, "write to "+email))
	if reply.status != http.StatusOK {
		t.Fatalf("status = %d, body %s", reply.status, reply.body)
	}
	if !strings.Contains(reply.body, email) {
		t.Fatalf("the stream did not restore the value: %s", reply.body)
	}

	restored := auditLines(t, console, "UNMASK")
	if len(restored) != 1 || restored[0][1] != email {
		t.Errorf("UNMASK lines = %v, want one restoring %s", restored, email)
	}
}

// And the rule everywhere else still holds: with no console, no value is written
// anywhere. The log is where one would leak, because it is the surface a support
// ticket and a log shipper both read.
func TestWithoutAConsoleNoValueIsWritten(t *testing.T) {
	logged := &safeBuffer{}
	up := newUpstream(t, echoJSON)

	det := detector.New(detector.Config{Locales: []string{"fr"}})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{
		Providers: []Provider{{Code: "anthropic", BaseURL: up.server.URL}},
		Logger:    slog.New(slog.NewTextHandler(logged, nil)),
	}, det, v)
	if err != nil {
		t.Fatal(err)
	}
	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)

	const email = "pierre.paul@example.fr"
	post(t, agent, "/anthropic/v1/messages", "quiet", fmt.Sprintf(`{"prompt":%q}`, email))

	if out := logged.String(); strings.Contains(out, email) {
		t.Errorf("the log carried the value in clear: %s", out)
	}
	if strings.Contains(logged.String(), "MASK ") {
		t.Errorf("an audit line was written with no console configured: %s", logged.String())
	}
}

// Colour is for a screen. Redirected to a file or a pipe — which is what anybody
// keeping an audit trail does — escape sequences through the middle of a value
// would make it unsearchable for the value itself.
func TestTheConsoleIsPlainWhenItIsNotATerminal(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, console := newAuditingAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "plain",
		`{"prompt":"write to pierre.paul@example.fr"}`)

	if got := console.String(); strings.Contains(got, "\033[") {
		t.Errorf("the console wrote escape sequences to a buffer:\n%q", got)
	}
}

// And with a terminal it paints: a value in clear blue, a replacement red. One
// colour, one meaning, on both lines — the MASK line and the UNMASK line are the
// whole of what the console now says about content, so the two must not come to
// disagree about which half is which.
func TestTheConsolePaintsATerminal(t *testing.T) {
	a := &auditor{w: io.Discard, colour: true}

	masked := a.line(ansiYellow, "MASK",
		a.paint(ansiBlue, "pierre.paul@example.fr"), a.paint(ansiRed, "[EMAIL_1]"))
	if !strings.Contains(masked, ansiBlue+"pierre.paul@example.fr") {
		t.Errorf("the value in clear is not marked as one: %q", masked)
	}
	if !strings.Contains(masked, ansiRed+"[EMAIL_1]") {
		t.Errorf("the replacement is not marked as one: %q", masked)
	}

	// The way back, in the same two colours: the replacement red and the value blue,
	// whichever side of the exchange they are on.
	restored := a.line(ansiGreen, "UNMASK",
		a.paint(ansiRed, "[EMAIL_1]"), a.paint(ansiBlue, "pierre.paul@example.fr"))
	if !strings.Contains(restored, ansiRed+"[EMAIL_1]") ||
		!strings.Contains(restored, ansiBlue+"pierre.paul@example.fr") {
		t.Errorf("the two lines disagree about which half is a value: %q", restored)
	}
}

// Fake mode round-trips through the whole agent, buffered and streamed, and the
// console reports both halves of it. Streamed as well, because the tail held back
// between events has to cover a stand-in as well as a token — answering only for
// tokens left a buffered answer restored and a streamed one not.
func TestFakeModeRoundTripsThroughTheAgent(t *testing.T) {
	fakeAgent := func(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *safeBuffer) {
		t.Helper()

		console := &safeBuffer{}
		det := detector.New(detector.Config{
			Locales:      []string{"fr"},
			Substitution: detector.SubstitutionFake,
		})
		v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
		if err != nil {
			t.Fatal(err)
		}
		up := newUpstream(t, handler)
		srv, err := New(Config{
			Providers: []Provider{{Code: "anthropic", BaseURL: up.server.URL}},
			Audit:     console,
		}, det, v)
		if err != nil {
			t.Fatal(err)
		}
		agent := httptest.NewServer(srv.Handler())
		t.Cleanup(agent.Close)
		return agent, console
	}

	const address = "10 rue jean jaures, 29200 BREST"

	t.Run("buffered", func(t *testing.T) {
		agent, console := fakeAgent(t, echoJSON)

		reply := post(t, agent, "/anthropic/v1/messages", "fake-buffered",
			fmt.Sprintf(`{"prompt":"il habite %s"}`, address))

		if !strings.Contains(reply.body, address) {
			t.Errorf("the caller did not get its own value back:\n%s", reply.body)
		}
		if restored := auditLines(t, console, "UNMASK"); len(restored) != 1 || restored[0][1] != address {
			t.Errorf("UNMASK lines = %v, want one restoring the address", restored)
		}
	})

	t.Run("streamed, the stand-in split across two events", func(t *testing.T) {
		agent, console := fakeAgent(t, func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)

			// Whatever stand-in the provider was given, cut down the middle. Read
			// out of the body rather than assumed, so the generator can change
			// without this becoming a test of a literal.
			var sent struct {
				Prompt string `json:"prompt"`
			}
			_ = json.Unmarshal(body, &sent)
			standIn := strings.TrimPrefix(sent.Prompt, "il habite ")
			cut := len(standIn) / 2

			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			for _, piece := range []string{"Adresse : " + standIn[:cut], standIn[cut:] + " — noté"} {
				fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", anthropicDelta(piece))
				if flusher != nil {
					flusher.Flush()
				}
			}
		})

		reply := post(t, agent, "/anthropic/v1/messages", "fake-streamed",
			fmt.Sprintf(`{"prompt":"il habite %s"}`, address))

		if text := concatenatedText(t, reply.body); !strings.Contains(text, address) {
			t.Errorf("the caller read %q, without its own value", text)
		}
		if restored := auditLines(t, console, "UNMASK"); len(restored) != 1 || restored[0][1] != address {
			t.Errorf("UNMASK lines = %v, want one restoring the address", restored)
		}
	})
}

// The console carries the transformations and no body.
//
// Both halves used to be printed, and on screen they scroll the MASK lines away: a
// coding tool resends tens of kilobytes of system prompt every turn, and those lines
// are what an operator is watching.
func TestTheConsoleCarriesNoBody(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, console := newAuditingAgent(t, up, []string{"fr"})

	const email = "pierre.paul@example.fr"
	const missed = "matricule ZZ-4471"
	post(t, agent, "/anthropic/v1/messages", "bodies",
		fmt.Sprintf(`{"prompt":"write to %s about %s"}`, email, missed))

	got := console.String()

	// The value is named once as it is masked, and that is all: the body it came
	// from is not on screen.
	if !strings.Contains(got, "MASK "+email) {
		t.Errorf("the console does not report the value it replaced:\n%s", got)
	}
	if strings.Contains(got, missed) {
		t.Errorf("the console still carries a body:\n%s", got)
	}
	if strings.Contains(got, `{"prompt"`) {
		t.Errorf("the console still carries a body:\n%s", got)
	}
	// The rules stay, because the size and the session are how an exchange scrolling
	// past is accounted for at a glance.
	if !strings.Contains(got, "IN   from the tool") || !strings.Contains(got, "OUT  to anthropic") {
		t.Errorf("the console lost the rules that bracket an exchange:\n%s", got)
	}
}

// With -v both bodies go to a file, which is where the finding lives that no count
// carries: a value present in both halves is one the catalogue never recognised.
func TestATraceHoldsBothBodies(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, console, dir := newTracingAgent(t, up, []string{"fr"})

	const email = "pierre.paul@example.fr"
	const missed = "matricule ZZ-4471"
	post(t, agent, "/anthropic/v1/messages", "trace",
		fmt.Sprintf(`{"prompt":"write to %s about %s"}`, email, missed))

	files := traceFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("wrote %d traces, want 1", len(files))
	}
	trace := files[0]

	token := auditLines(t, console, "MASK")[0][1]

	// The received half carries the value in clear, the sent half the token and not
	// the value. Asserted on position, because a file that held them the other way
	// round would read as a leak.
	in := strings.Index(trace, "IN   from the tool")
	out := strings.Index(trace, "OUT  to anthropic")
	if in < 0 || out < 0 || out < in {
		t.Fatalf("the two halves are not both there, in order:\n%s", trace)
	}
	if !strings.Contains(trace[in:out], email) {
		t.Errorf("the received half does not carry what the tool sent:\n%s", trace)
	}
	if strings.Contains(trace[out:], email) {
		t.Errorf("the sent half carries the value in clear:\n%s", trace)
	}
	if !strings.Contains(trace[out:], token) {
		t.Errorf("the sent half does not carry %s:\n%s", token, trace)
	}
	// And the value nothing recognised is in both, which is the whole reason for
	// writing the bodies rather than the counts. Counted over the request's two
	// halves and not the whole file: the answer is appended below them, and this
	// fake provider echoes what it was sent, so a count over everything measures
	// the upstream rather than the finding.
	if n := strings.Count(trace[in:requestHalves(trace)], missed); n != 2 {
		t.Errorf("an unrecognised value appears %d times, want it in both halves:\n%s", n, trace)
	}

	// The console names the file, so the two are not something to correlate by hand.
	if !strings.Contains(console.String(), "bodies in ") {
		t.Errorf("the console does not say where the bodies went:\n%s", console.String())
	}
}

// The count in the header is what was replaced in this body, not what was minted in
// it, and the two differ for every exchange after the first.
//
// The trace that failed read "replaced: 0 value(s)" above a body carrying
// [SECRET_12], [EMAIL_8] and [EMAIL_9]. All three values were replaced there; none
// was minted there — the session had seen each of them in an earlier exchange, so
// the mapping was reused and Reveal, which fires at minting, fired for none. The
// header counted the transformations it could list and reported them as the whole.
func TestATraceCountsWhatWasReplacedNotWhatWasMinted(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	const email = "pierre.paul@example.fr"
	body := fmt.Sprintf(`{"prompt":"write to %s about the invoice"}`, email)

	// The same session and the same value twice: the second exchange replaces it
	// from the mapping the first one stored.
	post(t, agent, "/anthropic/v1/messages", "reuse", body)
	post(t, agent, "/anthropic/v1/messages", "reuse", body)

	files := traceFiles(t, dir)
	if len(files) != 2 {
		t.Fatalf("wrote %d traces, want 2", len(files))
	}

	const first = "replaced: 1 value(s), 1 of them first seen in this session"
	if !strings.Contains(files[0], first) {
		t.Errorf("the first trace does not say %q:\n%s", first, header(files[0]))
	}

	// The line the bug was: one value replaced, none of them new.
	const second = "replaced: 1 value(s), 0 of them first seen in this session"
	if !strings.Contains(files[1], second) {
		t.Errorf("the second trace does not say %q:\n%s", second, header(files[1]))
	}

	// And the body it sits above does carry the token, so the header is not
	// describing an exchange in which nothing happened.
	out := files[1][strings.Index(files[1], "OUT  to anthropic"):]
	if strings.Contains(out, email) || !strings.Contains(out, "[EMAIL_") {
		t.Errorf("the sent half is not the masked body this header describes:\n%s", out)
	}
}

// requestHalves is where the outbound part of a trace stops, so an assertion about
// the two request bodies is not answered by the answer appended after them.
func requestHalves(trace string) int {
	if i := strings.Index(trace, "\nback:"); i > 0 {
		return i
	}
	return len(trace)
}

// header is the trace's first lines, for a failure message that does not print a
// whole body.
func header(trace string) string {
	if i := strings.Index(trace, "\n\n"); i > 0 {
		return trace[:i]
	}
	return trace
}

// A body is written whole, however long. A ceiling would be the trace choosing which
// part of the traffic is worth keeping, and the part it cut is exactly where a value
// nothing recognised would be.
func TestATraceHoldsALongBodyWhole(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	const needle = "matricule ZZ-4471"
	filler := strings.Repeat("x", 64*1024)
	post(t, agent, "/anthropic/v1/messages", "long",
		fmt.Sprintf(`{"prompt":"%s %s write to pierre.paul@example.fr"}`, filler, needle))

	trace := traceFiles(t, dir)[0]
	if !strings.Contains(trace, filler) {
		t.Error("a 64 kB body did not reach the trace whole")
	}
	if n := strings.Count(trace[:requestHalves(trace)], needle); n != 2 {
		t.Errorf("the tail of the body appears %d times, want it in both halves", n)
	}
}

// A JSON body is indented, so the two halves line up field by field and a diff points
// at the field rather than at one enormous line. Every byte of every value survives
// it, which is what makes indenting safe to do to evidence.
func TestATraceIndentsAJSONBody(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "indent",
		`{"model":"claude","prompt":"write to pierre.paul@example.fr about ZZ-4471"}`)

	trace := traceFiles(t, dir)[0]
	if !strings.Contains(trace, "\n  \"model\": \"claude\"") {
		t.Errorf("the body was not indented:\n%s", trace)
	}
	if !strings.Contains(trace, "pierre.paul@example.fr") || strings.Count(trace, "ZZ-4471") < 2 {
		t.Errorf("indenting lost a value that was sent:\n%s", trace)
	}
}

// A body that is not JSON is written exactly as it arrived. A partially indented
// document would be the file inventing a shape for something it could not read.
func TestATraceLeavesANonJSONBodyAsItArrived(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	const body = "écris à pierre.paul@example.fr, {ceci n'est pas du JSON"
	post(t, agent, "/anthropic/v1/messages", "flat", body)

	if trace := traceFiles(t, dir)[0]; !strings.Contains(trace, body) {
		t.Errorf("a flat body was not written as it arrived:\n%s", trace)
	}
}

// The sizes in the header are the bytes that went over the wire, not the indented
// form under them. A figure counting the whitespace this file added would have an
// operator reconciling a request against bytes nothing ever sent.
func TestATraceReportsTheSizesSent(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	body := `{"model":"claude","prompt":"write to pierre.paul@example.fr"}`
	post(t, agent, "/anthropic/v1/messages", "size", body)

	trace := traceFiles(t, dir)[0]
	if want := fmt.Sprintf("in:       %d bytes", len(body)); !strings.Contains(trace, want) {
		t.Errorf("the header does not report %q:\n%s", want, trace)
	}
}

// One file per exchange, ordered, and never one overwriting another — two requests in
// the same second would otherwise lose exactly the exchange being looked for.
func TestEachExchangeGetsItsOwnTrace(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	for i := range 3 {
		post(t, agent, "/anthropic/v1/messages", "seq",
			fmt.Sprintf(`{"prompt":"exchange number %d"}`, i))
	}

	files := traceFiles(t, dir)
	if len(files) != 3 {
		t.Fatalf("wrote %d traces for 3 exchanges", len(files))
	}
	for i, trace := range files {
		if want := fmt.Sprintf("exchange number %d", i); !strings.Contains(trace, want) {
			t.Errorf("trace %d does not hold %q — the order or the naming is wrong", i, want)
		}
	}
}

// -v with no -a records and prints nothing. Requiring a console to write a trace
// would have made the quiet half of the two flags silently do nothing.
func TestTracingWithoutAConsole(t *testing.T) {
	up := newUpstream(t, echoJSON)
	dir := filepath.Join(t.TempDir(), "traces")
	traces, err := newTracer(dir)
	if err != nil {
		t.Fatal(err)
	}

	det := detector.New(detector.Config{Locales: []string{"fr"}})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{
		Providers: []Provider{{Code: "anthropic", BaseURL: up.server.URL}},
		Traces:    traces,
	}, det, v)
	if err != nil {
		t.Fatal(err)
	}
	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)

	post(t, agent, "/anthropic/v1/messages", "quiet",
		`{"prompt":"write to pierre.paul@example.fr"}`)

	trace := traceFiles(t, dir)[0]
	if !strings.Contains(trace, "pierre.paul@example.fr") {
		t.Errorf("nothing was recorded without a console:\n%s", trace)
	}
	// The MASK line is in the file, because the file is the only record of this run.
	if !strings.Contains(trace, "MASK pierre.paul@example.fr TO ") {
		t.Errorf("the trace does not record the transformation:\n%s", trace)
	}
}

// A trace holds somebody's data in clear, so it is readable by its owner and nobody
// else — the same treatment the control key gets.
func TestATraceIsPrivate(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "perm", `{"prompt":"pierre.paul@example.fr"}`)

	if info, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	} else if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("the trace directory is %o, want 700", perm)
	}

	names, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil || len(names) == 0 {
		t.Fatalf("no trace to check: %v", err)
	}
	if info, err := os.Stat(names[0]); err != nil {
		t.Fatal(err)
	} else if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the trace file is %o, want 600", perm)
	}
}

// A session comes from a header the caller controls, so it must not decide where a
// file goes.
func TestASessionCannotEscapeTheTraceDirectory(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "../../escaped",
		`{"prompt":"pierre.paul@example.fr"}`)

	names, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 {
		t.Fatalf("the trace did not land in the directory: %v", names)
	}
	if strings.Contains(filepath.Base(names[0]), "/") || strings.Contains(names[0], "..") {
		t.Errorf("the file name carries a path: %s", names[0])
	}
}

// The command prints where traces go, and it reads that from the assembled agent
// rather than from the flag that asked for it.
//
// The chain is Agent.TraceDir → auditor.traceDir → tracer.Dir, and every step of it
// is nil-safe because the ordinary agent has no auditor and no tracer at all. What
// it must report for that agent is "", not a path — the banner says "nothing is
// written to a file" on the strength of this answer, and a banner that reassures
// somebody about a file it is at that moment filling is worse than no banner.
func TestTheAgentReportsWhereItWrites(t *testing.T) {
	up := newUpstream(t, echoJSON)

	t.Run("no tracer at all", func(t *testing.T) {
		srv := newServer(t, up, nil, nil)
		agent := &Agent{Server: srv}

		if got := agent.TraceDir(); got != "" {
			t.Errorf("an agent writing nothing reports %q, want the empty string", got)
		}
	})

	t.Run("a tracer with a directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "traces")
		traces, err := newTracer(dir)
		if err != nil {
			t.Fatal(err)
		}
		agent := &Agent{Server: newServer(t, up, nil, traces)}

		// The resolved path, so an operator reads where the files actually are
		// rather than the argument they typed.
		if got := agent.TraceDir(); got != dir {
			t.Errorf("the agent reports %q, want %q", got, dir)
		}
	})

	// Nothing asked for means no tracer is built, which is what keeps the ordinary
	// agent from carrying trace state that is merely switched off.
	if traces, err := newTracer(""); err != nil || traces != nil {
		t.Errorf("an empty directory built a tracer (%v, %v)", traces, err)
	}
}

// newServer assembles a server the way the tests above need it, without an address.
func newServer(t *testing.T, up *upstream, console io.Writer, traces *tracer) *Server {
	t.Helper()

	det := detector.New(detector.Config{Locales: []string{"fr"}})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{
		Providers: []Provider{{Code: "anthropic", BaseURL: up.server.URL}},
		Audit:     console,
		Traces:    traces,
	}, det, v)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// The answer belongs in the same file as the request that caused it, and it belongs
// there as it arrived: still carrying the replacements, before a single one was
// expanded. That is the half worth keeping — read against the OUT body above it, it
// says which of the tokens sent up came back, and it carries no restored value, so
// recording it adds no exposure the file did not already have.
func TestATraceHoldsTheAnswerAsItArrived(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, console, dir := newTracingAgent(t, up, []string{"fr"})

	const email = "pierre.paul@example.fr"
	got := post(t, agent, "/anthropic/v1/messages", "answer",
		fmt.Sprintf(`{"prompt":%q}`, "write to "+email))
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, body %s", got.status, got.body)
	}
	// The caller reads its own value back, which is what makes the trace's copy a
	// deliberate choice rather than the only thing available.
	if !strings.Contains(got.body, email) {
		t.Fatalf("the caller did not get the value back: %s", got.body)
	}

	trace := traceFiles(t, dir)[0]
	token := auditLines(t, console, "MASK")[0][1]

	back := strings.Index(trace, "BACK from anthropic")
	if back < 0 {
		t.Fatalf("the answer is not in the trace:\n%s", header(trace))
	}
	if !strings.Contains(trace[back:], token) {
		t.Errorf("the answer half does not carry %s, so it was recorded after expansion:\n%s", token, trace[back:])
	}
	if strings.Contains(trace[back:], email) {
		t.Errorf("the answer half carries the value in clear:\n%s", trace[back:])
	}

	// The size is a header field written where it became known, and it counts the
	// body rather than the section around it.
	if !regexp.MustCompile(`\nback:       [1-9][0-9]* bytes\n`).MatchString(trace) {
		t.Errorf("the answer's size is not in the trace:\n%s", trace[back-300:back])
	}
}

// What the exchange cost, on the four lines that answer four different questions.
// A sum would answer none of them: on a stream, delivering is nearly all waiting
// for the provider while unmask is the agent's own work.
func TestATraceHoldsWhatTheExchangeCost(t *testing.T) {
	// The provider takes a measurable moment, so upstream cannot be read as zero by
	// a clock that never moved.
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		echoJSON(w, r)
	})
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "cost",
		`{"prompt":"write to pierre.paul@example.fr"}`)

	trace := traceFiles(t, dir)[0]
	for _, field := range []string{"mask:", "upstream:", "delivering:", "unmask:"} {
		if !strings.Contains(trace, "\n"+field) {
			t.Errorf("the trace does not carry %q:\n%s", field, trace[:600])
		}
	}

	// Asserted on the one figure whose floor the test controls. The others are
	// real durations that can legitimately round to microseconds on a fast
	// machine, and pinning them would be a test of the clock.
	upstream := regexp.MustCompile(`\nupstream:   ([0-9.]+)(ms|s)\n`).FindStringSubmatch(trace)
	if upstream == nil {
		t.Fatalf("no upstream duration in the trace:\n%s", trace[:600])
	}
	spent, err := time.ParseDuration(upstream[1] + upstream[2])
	if err != nil {
		t.Fatal(err)
	}
	if spent < 20*time.Millisecond {
		t.Errorf("upstream = %s, want at least the 20ms the provider slept", spent)
	}
}

// The tokens the provider charged, and never a price. Converting to money needs a
// table per model, and a stale price in a file read as evidence is worse than none.
func TestATraceHoldsTheTokensAndNoPrice(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"claude-opus-5","usage":{"input_tokens":120,`+
			`"output_tokens":34,"cache_read_input_tokens":900}}`)
	})
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "tokens",
		`{"prompt":"write to pierre.paul@example.fr"}`)

	trace := traceFiles(t, dir)[0]
	if !strings.Contains(trace, "\nmodel:      claude-opus-5\n") {
		t.Errorf("the model is not in the trace:\n%s", trace[:600])
	}
	if !strings.Contains(trace, "\ntokens:     in 120, out 34, cache read 900, cache write 0\n") {
		t.Errorf("the counts are not in the trace:\n%s", trace[:600])
	}
}

// An answer that names no model gets no token line at all. A row of noughts would
// read as an exchange that cost nothing, when what happened is that this agent could
// not read what the provider reported.
func TestATraceOmitsTheTokensWhenTheAnswerNamesNoModel(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "no-model",
		`{"prompt":"write to pierre.paul@example.fr"}`)

	trace := traceFiles(t, dir)[0]
	if strings.Contains(trace, "\ntokens:") || strings.Contains(trace, "\nmodel:") {
		t.Errorf("an unreadable answer was reported as costing nothing:\n%s", trace[:600])
	}
}

// A streamed answer reaches the trace whole, and still tokenised. The rehydrator
// reads through the recorder, so what the file holds is what the provider sent —
// events and all — not the text the caller ended up reading.
func TestATraceHoldsAStreamedAnswerUnexpanded(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		token := "[EMAIL_1]"
		if i := strings.Index(string(body), "[EMAIL_"); i >= 0 {
			if j := strings.Index(string(body)[i:], "]"); j >= 0 {
				token = string(body)[i : i+j+1]
			}
		}
		// Split down the middle, the case the held-back tail exists for: the trace
		// must show the halves that arrived, and the caller must read the value.
		cut := len(token) / 2

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, piece := range []string{"Contact: " + token[:cut], token[cut:] + " — done"} {
			fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", anthropicDelta(piece))
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	agent, console, dir := newTracingAgent(t, up, []string{"fr"})

	const email = "pierre.paul@example.fr"
	got := post(t, agent, "/anthropic/v1/messages", "answer-stream",
		fmt.Sprintf(`{"prompt":%q}`, "write to "+email))
	if !strings.Contains(got.body, email) {
		t.Fatalf("the stream did not restore the value: %s", got.body)
	}

	trace := traceFiles(t, dir)[0]
	// The verbatim half, which is the one that must hold the bytes that arrived.
	// Located on the plain heading rather than the reassembled one above it.
	back := strings.Index(trace, "── BACK from anthropic ")
	if back < 0 {
		t.Fatalf("the streamed answer is not in the trace:\n%s", header(trace))
	}
	answer := trace[back:]

	// Both events, and not the concatenation the caller read. Counted on the event
	// line rather than the name, which the payload carries a second time.
	if n := strings.Count(answer, "event: content_block_delta"); n != 2 {
		t.Errorf("the answer holds %d events, want both:\n%s", n, answer)
	}
	if strings.Contains(answer, email) {
		t.Errorf("the streamed answer was recorded after expansion:\n%s", answer)
	}
	token := auditLines(t, console, "MASK")[0][1]
	if strings.Contains(answer, token) {
		t.Errorf("the token arrived whole, so this no longer tests the split:\n%s", answer)
	}
}

// A stream is unreadable at the grain it arrives in, so the trace also carries it
// put back together: one entry per content block, in the order they opened. Derived
// and therefore above the verbatim events, never instead of them.
func TestATraceReassemblesAStreamAboveTheEvents(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []string{
			`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Reading "}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"the file."}}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","name":"Bash"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"cmd\":\"cat /Us"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"ers/x/pipe.yml\"}"}}`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", e)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "reassembled",
		`{"prompt":"write to pierre.paul@example.fr"}`)

	trace := traceFiles(t, dir)[0]
	view := strings.Index(trace, "── BACK from anthropic, reassembled")
	verbatim := strings.Index(trace, "── BACK from anthropic ")
	if view < 0 || verbatim < 0 {
		t.Fatalf("the trace is missing one of the two halves:\n%s", trace)
	}
	// Readable first, verbatim below: the file is opened for the one and kept for
	// the other.
	if view > verbatim {
		t.Errorf("the reassembled view is below the events, want it above:\n%s", trace)
	}
	assembled := trace[view:verbatim]

	// The blocks, in the order they opened, each labelled by its start event — a
	// tool call by the tool's name, which is what stops it reading as a wall of JSON.
	for _, want := range []string{
		"[0] text\nReading the file.\n",
		"[1] tool_use Bash\n{\"cmd\":\"cat /Users/x/pipe.yml\"}\n",
	} {
		if !strings.Contains(assembled, want) {
			t.Errorf("the reassembled view does not carry %q:\n%s", want, assembled)
		}
	}
}

// A buffered answer is one document already: reassembling it would put a heading
// over a blank space, which reads as an answer that said nothing.
func TestATraceReassemblesNothingThatIsNotAStream(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "not-a-stream",
		`{"prompt":"write to pierre.paul@example.fr"}`)

	if trace := traceFiles(t, dir)[0]; strings.Contains(trace, "reassembled") {
		t.Errorf("a buffered answer was given a reassembled section:\n%s", trace)
	}
}

// The answer is filed once. The buffered path closes the body itself and the reverse
// proxy closes whatever it is left holding: filed twice, a trace would read as if the
// provider had said everything two times over.
func TestATraceFilesTheAnswerOnce(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "once",
		`{"prompt":"write to pierre.paul@example.fr"}`)

	trace := traceFiles(t, dir)[0]
	if n := strings.Count(trace, "BACK from anthropic"); n != 1 {
		t.Errorf("the answer was filed %d times, want once:\n%s", n, trace)
	}
	if n := strings.Count(trace, "\nback:"); n != 1 {
		t.Errorf("the answer's size appears %d times, want once", n)
	}
}

// A tool call is the one part of an answer that is an instruction rather than
// prose, so the console names the tool and prints the arguments the model asked
// for it with — restored, which is what the tool on this workstation will act on.
//
// The arguments are split mid-value here, which is the case that makes the point:
// the console must print the whole document, once, not a fragment per event.
func TestAuditPrintsTheToolCallTheModelAskedFor(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		token := mintedToken(t, string(body))

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		arguments := `{"to":"` + token + `"}`
		cut := len(arguments) / 2
		events := []string{
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"SendMail"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + jsonString(arguments[:cut]) + `}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + jsonString(arguments[cut:]) + `}}`,
			`{"type":"content_block_stop","index":0}`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", e)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	agent, console := newAuditingAgent(t, up, []string{"fr"})

	const email = "pierre.paul@example.fr"
	post(t, agent, "/anthropic/v1/messages", "tool-stream",
		fmt.Sprintf(`{"prompt":%q}`, "write to "+email))

	want := `TOOL SendMail {"to":"` + email + `"}`
	printed := stripANSI(console.String())
	if n := strings.Count(printed, want); n != 1 {
		t.Errorf("the console printed the tool call %d times, want once:\n%s", n, printed)
	}
}

// A buffered answer has no blocks, and its tool calls sit in the content array.
// Without this half the console would show a streaming client's tool calls and
// nothing for a buffered one — a silence an operator would read as an answer that
// asked for no tools.
func TestAuditPrintsAToolCallFromABufferedAnswer(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		token := mintedToken(t, string(body))

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"content":[{"type":"text","text":"On it."},`+
			`{"type":"tool_use","name":"SendMail","input":{"to":%q}}]}`, token)
	})
	agent, console := newAuditingAgent(t, up, []string{"fr"})

	const email = "pierre.paul@example.fr"
	post(t, agent, "/anthropic/v1/messages", "tool-buffered",
		fmt.Sprintf(`{"prompt":%q}`, "write to "+email))

	want := `TOOL SendMail {"to":"` + email + `"}`
	if printed := stripANSI(console.String()); !strings.Contains(printed, want) {
		t.Errorf("the console does not carry %q:\n%s", want, printed)
	}
}

// An exchange whose session minted nothing still carries tool calls, and the
// console still shows them. Gated on the mapping instead, the line would be there
// for a prompt holding an address and silently absent for one that did not —
// which is the console lying about what the model asked for.
func TestAuditPrintsAToolCallWhenNothingWasMasked(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []string{
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"Bash"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"cmd\":\"ls\"}"}}`,
			`{"type":"content_block_stop","index":0}`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", e)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	agent, console := newAuditingAgent(t, up, []string{"fr"})

	got := post(t, agent, "/anthropic/v1/messages", "nothing-masked",
		`{"prompt":"list the files"}`)
	if !strings.Contains(got.body, `{\"cmd\":\"ls\"}`) {
		t.Fatalf("the tool call did not reach the caller: %s", got.body)
	}

	want := `TOOL Bash {"cmd":"ls"}`
	if printed := stripANSI(console.String()); !strings.Contains(printed, want) {
		t.Errorf("the console does not carry %q:\n%s", want, printed)
	}
}

// mintedToken returns the replacement the agent put into a body it forwarded, so a
// fake provider can answer with the token this exchange actually minted.
func mintedToken(t *testing.T, body string) string {
	t.Helper()

	i := strings.Index(body, "[EMAIL_")
	if i < 0 {
		t.Fatalf("the forwarded body holds no replacement: %s", body)
	}
	j := strings.Index(body[i:], "]")
	if j < 0 {
		t.Fatalf("the replacement is not closed: %s", body)
	}
	return body[i : i+j+1]
}

// jsonString renders text as a JSON string, so a fragment carrying a quote is
// escaped rather than spliced.
func jsonString(text string) string {
	encoded, err := json.Marshal(text)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// A tool the provider runs itself arrives as server_tool_use, and a connector's as
// mcp_tool_use — same name, same input document, same input_json_delta fragments.
// Matched on tool_use alone, a real web_search printed as "TOOL unnamed" while its
// name sat unread in the start event, which is the console failing at the one thing
// the line exists for: saying which tool is about to act.
func TestAuditNamesEveryFlavourOfToolCall(t *testing.T) {
	blocks := []string{
		`{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{}}`,
		`{"type":"mcp_tool_use","id":"mcptoolu_1","name":"jira_search","input":{}}`,
	}
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for i, block := range blocks {
			events := []string{
				fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":%s}`, i, block),
				fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"mcp\"}"}}`, i),
				fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, i),
			}
			for _, e := range events {
				fmt.Fprintf(w, "event: x\ndata: %s\n\n", e)
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	})
	agent, console := newAuditingAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "tool-flavours", `{"prompt":"search it"}`)

	printed := stripANSI(console.String())
	for _, want := range []string{
		`TOOL web_search {"query":"mcp"}`,
		`TOOL jira_search {"query":"mcp"}`,
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("the console does not carry %q:\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "TOOL unnamed") {
		t.Errorf("a named tool call was printed unnamed:\n%s", printed)
	}
}

// The same three flavours in a buffered answer, where the tool calls sit in the
// content array rather than in blocks. Fixed in one place and not the other, half
// the clients would still see the silence.
func TestAuditNamesEveryFlavourOfToolCallWhenBuffered(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[`+
			`{"type":"server_tool_use","name":"web_search","input":{"query":"mcp"}},`+
			`{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[]},`+
			`{"type":"mcp_tool_use","name":"jira_search","input":{"query":"mcp"}}]}`)
	})
	agent, console := newAuditingAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "tool-flavours-buffered", `{"prompt":"search it"}`)

	printed := stripANSI(console.String())
	for _, want := range []string{
		`TOOL web_search {"query":"mcp"}`,
		`TOOL jira_search {"query":"mcp"}`,
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("the console does not carry %q:\n%s", want, printed)
		}
	}
	// A result is not a call: its type ends on tool_result, and reported it would
	// put a line on the console for a tool nobody asked to run.
	if n := strings.Count(printed, "TOOL "); n != 2 {
		t.Errorf("the console printed %d tool lines, want 2:\n%s", n, printed)
	}
}
