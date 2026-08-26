package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
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

// Both bodies, in wire order: what the tool sent, then what left for the
// provider. It is the reading the MASK line cannot give on its own — a value the
// catalogue never recognised appears in both, and that absence is the finding.
func TestAuditShowsTheBodyReceivedAndTheBodySent(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, console := newAuditingAgent(t, up, []string{"fr"})

	const email = "pierre.paul@example.com"
	const missed = "matricule interne ZZ-4471"
	body := fmt.Sprintf(`{"prompt":"write to %s about %s"}`, email, missed)
	post(t, agent, "/anthropic/v1/messages", "bodies", body)

	got := console.String()
	token := auditLines(t, console, "MASK")[0][1]

	// The received half carries the value in clear, the sent half carries the
	// token and not the value. Asserted on position, because a console that
	// printed them the other way round would read as a leak.
	in := strings.Index(got, "IN   from the tool")
	out := strings.Index(got, "OUT  to anthropic")
	if in < 0 || out < 0 || out < in {
		t.Fatalf("the two halves are not both there, in order:\n%s", got)
	}
	received := got[in:out]

	// The sent half is the rule and the body under it, nothing after: the UNMASK
	// line of the same exchange carries the value in clear by design, and reading
	// it as part of the outbound body would make this test pass on a leak.
	//
	// Cut at that line rather than after two lines of output: a JSON body is
	// printed indented, so the body is as many lines as it has fields and a
	// two-line window would assert on an opening brace.
	sent := got[out:]
	if end := strings.Index(sent, "UNMASK"); end >= 0 {
		sent = sent[:end]
	}

	if !strings.Contains(received, email) || !strings.Contains(received, missed) {
		t.Errorf("the received half does not carry what the tool sent:\n%s", received)
	}
	if strings.Contains(sent, email) {
		t.Errorf("the sent half carries the value in clear:\n%s", sent)
	}
	if !strings.Contains(sent, token) {
		t.Errorf("the sent half does not carry %s:\n%s", token, sent)
	}
	// And the value the catalogue did not recognise is visible in both, which is
	// the whole reason for printing the bodies rather than the counts.
	if !strings.Contains(sent, missed) {
		t.Errorf("an unrecognised value is not shown leaving the machine:\n%s", sent)
	}
}

// A body is printed whole, however long. A ceiling would be the console choosing
// which part of the traffic is worth looking at, and the part it cut is exactly
// where a value nothing recognised would be.
func TestAuditPrintsALongBodyWhole(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, console := newAuditingAgent(t, up, []string{"fr"})

	const email = "pierre.paul@example.com"
	const needle = "matricule interne ZZ-4471"
	filler := strings.Repeat("x", 64*1024)
	post(t, agent, "/anthropic/v1/messages", "long",
		fmt.Sprintf(`{"prompt":"%s %s write to %s"}`, filler, needle, email))

	got := console.String()
	if !strings.Contains(got, filler) {
		t.Error("a 64 kB body did not reach the console whole")
	}
	// Twice: once as it arrived, once as it left. What is at the far end of a
	// long body is the case a ceiling would have hidden.
	if n := strings.Count(got, needle); n != 2 {
		t.Errorf("the tail of the body appears %d times, want it in both halves", n)
	}
	if masked := auditLines(t, console, "MASK"); len(masked) != 1 || masked[0][0] != email {
		t.Errorf("MASK lines = %v, want the value found past the filler", masked)
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

// And with a terminal it paints, in the same two colours the bodies mark: a value
// in clear blue, a replacement red. One colour, one meaning, whole console.
func TestTheConsolePaintsATerminal(t *testing.T) {
	a := &auditor{w: io.Discard, colour: true}

	line := a.line(ansiYellow, "MASK",
		a.paint(ansiBlue, "pierre.paul@example.com"), a.paint(ansiRed, "[EMAIL_1]"))
	if !strings.Contains(line, ansiBlue+"pierre.paul@example.com") {
		t.Errorf("the value in clear is not marked as one: %q", line)
	}
	if !strings.Contains(line, ansiRed+"[EMAIL_1]") {
		t.Errorf("the replacement is not marked as one: %q", line)
	}

	// The same colour a body marks it with, asserted together so a change to one
	// cannot quietly leave the console saying "value" two ways.
	if body := a.bodyIn("pierre.paul@example.com", [][2]string{
		{"pierre.paul@example.com", "[EMAIL_1]"},
	}); !strings.Contains(body, ansiBlue) {
		t.Errorf("the line and the body disagree about a value in clear: %q", body)
	}
	if body := a.bodyOut("[EMAIL_1]", nil); !strings.Contains(body, ansiRed) {
		t.Errorf("the line and the body disagree about a replacement: %q", body)
	}
}

// The two halves are read against each other, so each marks what is about to
// change hands: the values on their way out, and the replacements on their way
// back. What is unmarked in both is what the catalogue never saw.
func TestTheBodiesMarkWhatChangesHands(t *testing.T) {
	a := &auditor{w: io.Discard, colour: true}

	const email = "pierre.paul@example.com"
	const missed = "ZZ-4471"
	replaced := [][2]string{{email, "[EMAIL_1]"}}

	in := a.bodyIn(`{"prompt":"write to `+email+` about `+missed+`"}`, replaced)
	if !strings.Contains(in, ansiBlue+email) {
		t.Errorf("the value about to be replaced is not marked: %q", in)
	}
	if strings.Contains(in, ansiBlue+missed) {
		t.Errorf("a value nothing recognised was marked: %q", in)
	}

	out := a.bodyOut(`{"prompt":"write to [EMAIL_1] about `+missed+`"}`, replaced)
	if !strings.Contains(out, ansiRed+"[EMAIL_1]") {
		t.Errorf("the replacement about to be turned back is not marked: %q", out)
	}
	if strings.Contains(out, ansiRed+missed) {
		t.Errorf("a value nothing recognised was marked: %q", out)
	}

	// And neither marks anything without a terminal, or a log file kept from this
	// console could not be grepped for the value itself.
	plain := &auditor{w: io.Discard}
	if got := plain.bodyIn(email, replaced) + plain.bodyOut("[EMAIL_1]", replaced); strings.Contains(got, "\033[") {
		t.Errorf("the bodies were painted for a buffer: %q", got)
	}
}

// A value that happens to be a substring of a longer one in another field is
// marked inside its own occurrence, not inside the escape of the other: longest
// first is what keeps the sequence whole.
func TestTheLongerValueIsMarkedFirst(t *testing.T) {
	a := &auditor{w: io.Discard, colour: true}

	got := a.bodyIn(`{"a":"06 12 34 56 78","b":"06 12"}`, [][2]string{
		{"06 12", "[PHONE_2]"},
		{"06 12 34 56 78", "[PHONE_1]"},
	})
	if !strings.Contains(got, ansiBlue+"06 12 34 56 78"+ansiReset) {
		t.Errorf("the longer value was broken up: %q", got)
	}
}

// A stand-in is not a bracket token, and marking the outbound body by shape left
// fake mode with nothing marked in the half where it matters most: "1 rue de
// l'Exemple, 99000 Villeneuve" reads as prose. What was substituted comes from
// the pass that substituted it.
func TestTheOutboundBodyMarksAStandInAsWellAsAToken(t *testing.T) {
	a := &auditor{w: io.Discard, colour: true}

	const stand = "1 rue de l'Exemple, 99000 Villeneuve"
	replaced := [][2]string{{"10 rue jean jaures, 29200 BREST", stand}}

	got := a.bodyOut(`{"prompt":"habite `+stand+`, et voir [EMAIL_1]"}`, replaced)
	if !strings.Contains(got, ansiRed+stand+ansiReset) {
		t.Errorf("the stand-in is not marked: %q", got)
	}
	// And a token minted on an earlier turn, which this pass never saw, is still
	// marked: a conversation resends its history.
	if !strings.Contains(got, ansiRed+"[EMAIL_1]"+ansiReset) {
		t.Errorf("a token from an earlier turn is not marked: %q", got)
	}
	// Painted once, not once inside itself.
	if strings.Contains(got, ansiRed+ansiRed) {
		t.Errorf("a marking was nested: %q", got)
	}
}

// The same thing through the whole agent in fake mode, because the console
// showing a stand-in unmarked was reported on a running agent, not on a unit.
func TestAuditInFakeModeMarksWhatLeft(t *testing.T) {
	up := newUpstream(t, echoJSON)

	console := &safeBuffer{}
	det := detector.New(detector.Config{
		Locales:      []string{"fr"},
		Substitution: detector.SubstitutionFake,
	})
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
	// A terminal, so the marking runs: it is the marking that regressed, and a
	// plain console would pass whatever the palette did.
	srv.audit.colour = true

	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)

	post(t, agent, "/anthropic/v1/messages", "fake",
		`{"prompt":"habite 10 rue jean jaures, 29200 BREST"}`)

	masked := auditLines(t, console, "MASK")
	if len(masked) == 0 {
		t.Fatalf("nothing was masked:\n%s", console.String())
	}
	stand := masked[0][1]
	if strings.Contains(stand, "[") {
		t.Fatalf("fake mode produced a token, not a stand-in: %q", stand)
	}

	out := console.String()[strings.Index(console.String(), "OUT  to anthropic"):]
	if !strings.Contains(out, ansiRed+stand) {
		t.Errorf("the stand-in that left is not marked:\n%s", out)
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

// A JSON body is laid out over several lines, because a coding tool sends one
// line of tens of kilobytes and "which field was that value in" is a question the
// console exists to answer.
//
// What it asserts is the property that makes indenting safe rather than the
// layout itself: every byte of every value survives it, so the marking still
// finds a value by its own text and the console still shows what was sent.
func TestAuditIndentsAJSONBody(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, console := newAuditingAgent(t, up, []string{"fr"})

	const email = "pierre.paul@example.com"
	const missed = "ZZ-4471"
	post(t, agent, "/anthropic/v1/messages", "indent",
		fmt.Sprintf(`{"model":"claude","prompt":"write to %s about %s"}`, email, missed))

	got := console.String()

	// Laid out: the two keys are on lines of their own, which one line of JSON
	// cannot manage.
	if !strings.Contains(got, "\n  \"model\": \"claude\"") {
		t.Errorf("the body was not indented:\n%s", got)
	}
	// And nothing inside a string moved: the value in clear, the value nothing
	// recognised, and the token all still appear verbatim.
	if !strings.Contains(got, email) || strings.Count(got, missed) < 2 {
		t.Errorf("indenting lost a value that was sent:\n%s", got)
	}
	if token := auditLines(t, console, "MASK")[0][1]; !strings.Contains(got, token) {
		t.Errorf("the replacement is not in the indented body:\n%s", got)
	}
}

// A body that is not JSON is printed exactly as it arrived. A partially indented
// document would be the console inventing a shape for something it could not
// read.
func TestAuditLeavesANonJSONBodyAsItArrived(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, console := newAuditingAgent(t, up, []string{"fr"})

	const body = "écris à pierre.paul@example.com, {ceci n'est pas du JSON"
	post(t, agent, "/anthropic/v1/messages", "flat", body)

	if got := console.String(); !strings.Contains(got, body) {
		t.Errorf("a flat body was not printed as it arrived:\n%s", got)
	}
}

// The size on the rule is the size of the body that went over the wire, not of
// the indented form printed under it. A figure that counted the whitespace this
// console added would have an operator reconciling a request against bytes
// nothing ever sent.
func TestTheRuleReportsTheSizeSent(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, console := newAuditingAgent(t, up, []string{"fr"})

	body := `{"model":"claude","prompt":"write to pierre.paul@example.com"}`
	post(t, agent, "/anthropic/v1/messages", "size", body)

	want := fmt.Sprintf("%d B", len(body))
	if got := console.String(); !strings.Contains(got, want) {
		t.Errorf("the inbound rule does not report %s:\n%s", want, got)
	}
}
