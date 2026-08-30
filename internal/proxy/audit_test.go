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

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/vault"
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

	const email = "pierre.paul@example.com"
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

	const email = "pierre.paul@example.com"
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

	const email = "pierre.paul@example.com"
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
		`{"prompt":"write to pierre.paul@example.com"}`)

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
		a.paint(ansiBlue, "pierre.paul@example.com"), a.paint(ansiRed, "[EMAIL_1]"))
	if !strings.Contains(masked, ansiBlue+"pierre.paul@example.com") {
		t.Errorf("the value in clear is not marked as one: %q", masked)
	}
	if !strings.Contains(masked, ansiRed+"[EMAIL_1]") {
		t.Errorf("the replacement is not marked as one: %q", masked)
	}

	// The way back, in the same two colours: the replacement red and the value blue,
	// whichever side of the exchange they are on.
	restored := a.line(ansiGreen, "UNMASK",
		a.paint(ansiRed, "[EMAIL_1]"), a.paint(ansiBlue, "pierre.paul@example.com"))
	if !strings.Contains(restored, ansiRed+"[EMAIL_1]") ||
		!strings.Contains(restored, ansiBlue+"pierre.paul@example.com") {
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

	const email = "pierre.paul@example.com"
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

	const email = "pierre.paul@example.com"
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
	// writing the bodies rather than the counts.
	if n := strings.Count(trace, missed); n != 2 {
		t.Errorf("an unrecognised value appears %d times, want it in both halves:\n%s", n, trace)
	}

	// The console names the file, so the two are not something to correlate by hand.
	if !strings.Contains(console.String(), "bodies in ") {
		t.Errorf("the console does not say where the bodies went:\n%s", console.String())
	}
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
		fmt.Sprintf(`{"prompt":"%s %s write to pierre.paul@example.com"}`, filler, needle))

	trace := traceFiles(t, dir)[0]
	if !strings.Contains(trace, filler) {
		t.Error("a 64 kB body did not reach the trace whole")
	}
	if n := strings.Count(trace, needle); n != 2 {
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
		`{"model":"claude","prompt":"write to pierre.paul@example.com about ZZ-4471"}`)

	trace := traceFiles(t, dir)[0]
	if !strings.Contains(trace, "\n  \"model\": \"claude\"") {
		t.Errorf("the body was not indented:\n%s", trace)
	}
	if !strings.Contains(trace, "pierre.paul@example.com") || strings.Count(trace, "ZZ-4471") < 2 {
		t.Errorf("indenting lost a value that was sent:\n%s", trace)
	}
}

// A body that is not JSON is written exactly as it arrived. A partially indented
// document would be the file inventing a shape for something it could not read.
func TestATraceLeavesANonJSONBodyAsItArrived(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	const body = "écris à pierre.paul@example.com, {ceci n'est pas du JSON"
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

	body := `{"model":"claude","prompt":"write to pierre.paul@example.com"}`
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
		`{"prompt":"write to pierre.paul@example.com"}`)

	trace := traceFiles(t, dir)[0]
	if !strings.Contains(trace, "pierre.paul@example.com") {
		t.Errorf("nothing was recorded without a console:\n%s", trace)
	}
	// The MASK line is in the file, because the file is the only record of this run.
	if !strings.Contains(trace, "MASK pierre.paul@example.com TO ") {
		t.Errorf("the trace does not record the transformation:\n%s", trace)
	}
}

// A trace holds somebody's data in clear, so it is readable by its owner and nobody
// else — the same treatment the control key gets.
func TestATraceIsPrivate(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _, dir := newTracingAgent(t, up, []string{"fr"})

	post(t, agent, "/anthropic/v1/messages", "perm", `{"prompt":"pierre.paul@example.com"}`)

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
		`{"prompt":"pierre.paul@example.com"}`)

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
