package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
)

// anthropicKeySample is a credential the catalogue recognises by prefix. Shaped like
// a real one and worth nothing: the point is that it comes back tokenized whatever
// the substitution mode says.
const anthropicKeySample = "sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"

// postAs sends a JSON body to one of the extension routes with a control key.
func postAs(t *testing.T, agent *httptest.Server, path, key, session, body string) reply {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, agent.URL+path,
		strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(controlHeader, key)
	}
	if session != "" {
		req.Header.Set("X-Session-Id", session)
	}
	return do(t, agent, req)
}

func decodeReply[T any](t *testing.T, got reply) T {
	t.Helper()

	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.body)
	}
	var out T
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("the agent answered something this cannot read: %v (%s)", err, got.body)
	}
	return out
}

// TestExtensionRoutesRefuseAWrongKey is the half of the shared check that the
// routes it guards cannot be reached without the secret.
//
// Both routes, and both failure shapes, because the check now has three callers and
// the one that matters most is the newest: /unmask reads the mapping, so a route
// left open there hands any local process the originals one replacement at a time.
func TestExtensionRoutesRefuseAWrongKey(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _ := newControlledAgent(t, up, []string{"fr"})

	const wrongKey = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

	for _, path := range []string{"/mask", "/unmask"} {
		for _, tc := range []struct {
			name string
			key  string
		}{
			{"no key at all", ""},
			{"a key of the right shape that is not this agent's", wrongKey},
		} {
			t.Run(path+" refuses "+tc.name, func(t *testing.T) {
				got := postAs(t, agent, path, tc.key, "", `{"texts":["claire@example.fr"]}`)
				if got.status != http.StatusForbidden {
					t.Fatalf("status %d, want 403: %s", got.status, got.body)
				}
				// The refusal must not describe the key, and must not leak it back.
				if strings.Contains(got.body, testControlKey) {
					t.Fatalf("the refusal carried the agent's own key: %s", got.body)
				}
			})
		}
	}
}

// TestExtensionRoutesRefuseAnAgentWithNoKey holds the inherited half: a key that
// could not be read leaves these routes refusing everything rather than open.
func TestExtensionRoutesRefuseAnAgentWithNoKey(t *testing.T) {
	up := newUpstream(t, echoJSON)

	det := detector.New(detector.Config{Locales: []string{"fr"}})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{Providers: []Provider{{Code: "anthropic", BaseURL: up.server.URL}}}, det, v)
	if err != nil {
		t.Fatal(err)
	}
	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)

	for _, path := range []string{"/mask", "/unmask", "/policy"} {
		got := postAs(t, agent, path, testControlKey, "", `{"texts":[]}`)
		// /policy takes PUT, so it refuses the method first; the other two reach the
		// key check. Either way nothing on an agent with no key may be reached.
		if got.status != http.StatusServiceUnavailable && got.status != http.StatusMethodNotAllowed {
			t.Fatalf("%s answered %d on an agent with no key: %s", path, got.status, got.body)
		}
	}
}

// TestMaskRendersCredentialsAndPIIDifferently drives the route through a real
// detector, in both substitution modes.
//
// The pair is the point. A credential must come back as a bracket token in fake mode
// too — that is what makes the value-matching expansion safe — and a stand-in that
// looked like a working key is a thing somebody would try to use.
func TestMaskRendersCredentialsAndPIIDifferently(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})

	const text = "write to claire@example.fr with sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"

	t.Run("token mode", func(t *testing.T) {
		det.SetSubstitution(detector.SubstitutionToken)
		out := decodeReply[maskReply](t, postAs(t, agent, "/mask", testControlKey, "token-session",
			`{"texts":`+jsonList(text)+`}`))

		if out.Masked != 2 {
			t.Fatalf("replaced %d values, want 2: %q", out.Masked, out.Texts[0])
		}
		if strings.Contains(out.Texts[0], "claire@example.fr") ||
			strings.Contains(out.Texts[0], anthropicKeySample) {
			t.Fatalf("a value went out in clear: %q", out.Texts[0])
		}
		if !strings.Contains(out.Texts[0], "[EMAIL_") {
			t.Fatalf("the email was not tokenized: %q", out.Texts[0])
		}
	})

	t.Run("fake mode still tokenizes the credential", func(t *testing.T) {
		det.SetSubstitution(detector.SubstitutionFake)
		out := decodeReply[maskReply](t, postAs(t, agent, "/mask", testControlKey, "fake-session",
			`{"texts":`+jsonList(text)+`}`))

		if strings.Contains(out.Texts[0], anthropicKeySample) {
			t.Fatalf("the credential went out in clear: %q", out.Texts[0])
		}
		if !strings.Contains(out.Texts[0], "[ANTHROPIC_KEY_") {
			t.Fatalf("the credential got a stand-in rather than a token: %q", out.Texts[0])
		}
		if strings.Contains(out.Texts[0], "[EMAIL_") {
			t.Fatalf("fake mode still tokenized the email: %q", out.Texts[0])
		}
	})
}

// TestMaskGivesOneValueOneIdentityAcrossFields is why the route takes a list.
//
// A message being sent carries several text fields. Masked one call at a time they
// would still agree, because the session mapping is consulted first — but masked in
// one pass they agree without a round trip per field, and this asserts the property
// the caller depends on rather than the optimisation.
func TestMaskGivesOneValueOneIdentityAcrossFields(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})
	det.SetSubstitution(detector.SubstitutionToken)

	out := decodeReply[maskReply](t, postAs(t, agent, "/mask", testControlKey, "one-identity",
		`{"texts":["mail claire@example.fr","and claire@example.fr again"]}`))

	first := tokenIn(t, out.Texts[0])
	second := tokenIn(t, out.Texts[1])
	if first != second {
		t.Fatalf("one address became two people: %q and %q", first, second)
	}
	if out.Masked != 2 {
		t.Fatalf("counted %d replacements, want 2 — repeats are values that did not leave", out.Masked)
	}
}

// TestUnmaskRestoresATokenSplitAcrossTwoCalls is the protocol's whole reason to
// carry a tail.
//
// Generated text arrives in pieces of a few characters, so a token the model echoed
// is regularly cut in half. The client holds what the agent hands back and prepends
// it to the next chunk, which is the streaming rehydrator's held-back tail with the
// client doing the holding.
func TestUnmaskRestoresATokenSplitAcrossTwoCalls(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})
	det.SetSubstitution(detector.SubstitutionToken)

	masked := decodeReply[maskReply](t, postAs(t, agent, "/mask", testControlKey, "split",
		`{"texts":["claire@example.fr"]}`))
	token := masked.Texts[0]

	// Cut through the middle of the token, as an event boundary would.
	cut := len(token) / 2
	first := "reply to " + token[:cut]
	second := token[cut:] + " today"

	one := decodeReply[unmaskReply](t, postAs(t, agent, "/unmask", testControlKey, "split",
		body(t, unmaskRequest{Text: first})))
	if strings.Contains(one.Expanded, token[:cut]) {
		t.Fatalf("the half token was emitted rather than held back: %q", one.Expanded)
	}
	if one.Tail == "" {
		t.Fatal("nothing was held back, so the next chunk cannot complete the token")
	}

	two := decodeReply[unmaskReply](t, postAs(t, agent, "/unmask", testControlKey, "split",
		body(t, unmaskRequest{Text: second, Tail: one.Tail, Final: true})))

	whole := one.Expanded + two.Expanded
	if whole != "reply to claire@example.fr today" {
		t.Fatalf("the stream read %q", whole)
	}
	if two.Tail != "" {
		t.Fatalf("a final call held %q back, which nothing will ever ask for", two.Tail)
	}
}

// TestUnmaskFlushesAValueEndingTheStream is the case the explicit end exists for.
//
// A value masked as the very last characters of an answer leaves a tail that looks
// like the start of a token, and without a final call it is simply never shown: the
// caller reads a sentence with its last word missing.
func TestUnmaskFlushesAValueEndingTheStream(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})
	det.SetSubstitution(detector.SubstitutionToken)

	masked := decodeReply[maskReply](t, postAs(t, agent, "/mask", testControlKey, "ending",
		`{"texts":["claire@example.fr"]}`))
	token := masked.Texts[0]

	// The stream ends mid-token, which is what a provider's last delta looks like
	// when the model finished on a value.
	cut := len(token) - 3
	one := decodeReply[unmaskReply](t, postAs(t, agent, "/unmask", testControlKey, "ending",
		body(t, unmaskRequest{Text: "her address is " + token[:cut]})))

	flush := decodeReply[unmaskReply](t, postAs(t, agent, "/unmask", testControlKey, "ending",
		body(t, unmaskRequest{Text: token[cut:], Tail: one.Tail, Final: true})))

	if got := one.Expanded + flush.Expanded; got != "her address is claire@example.fr" {
		t.Fatalf("the stream read %q — the value at the end was lost", got)
	}
}

// TestUnmaskLeavesATokenNothingExpands alone, which is what the agent's own response
// path does for an answer still in flight when the mode changed and the mapping was
// purged. Inventing a value for it would put data in front of somebody that nothing
// supports; hiding it would hide that something went wrong.
func TestUnmaskLeavesATokenNothingExpands(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _ := newControlledAgent(t, up, []string{"fr"})

	got := decodeReply[unmaskReply](t, postAs(t, agent, "/unmask", testControlKey, "orphan",
		body(t, unmaskRequest{Text: "as [EMAIL_99] said", Final: true})))

	if got.Expanded != "as [EMAIL_99] said" {
		t.Fatalf("an unknown token was rewritten to %q", got.Expanded)
	}
}

// TestUnmaskRefusesANonLoopbackCaller is the rule that does not transfer from -l.
//
// /healthz and /test are reachable beyond loopback under a warning because they
// describe a configuration. This one answers "what does this replacement stand for",
// which is the mapping one question at a time, and there is no legitimate remote
// caller for it — the key would travel in clear HTTP to reach one.
func TestUnmaskRefusesANonLoopbackCaller(t *testing.T) {
	up := newUpstream(t, echoJSON)

	det := detector.New(detector.Config{Locales: []string{"fr"}})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{
		Providers:  []Provider{{Code: "anthropic", BaseURL: up.server.URL}},
		ControlKey: testControlKey,
	}, det, v)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		remote string
		want   int
	}{
		{"a caller on the local network", "192.168.1.40:51000", http.StatusForbidden},
		{"a caller from anywhere", "203.0.113.5:443", http.StatusForbidden},
		{"an address this cannot parse", "not-an-address", http.StatusForbidden},
		{"loopback", "127.0.0.1:51000", http.StatusOK},
		{"loopback over IPv6", "[::1]:51000", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/unmask",
				strings.NewReader(body(t, unmaskRequest{Text: "hello", Final: true})))
			req.RemoteAddr = tc.remote
			req.Header.Set(controlHeader, testControlKey)

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// TestExtensionRoutesAreReserved: a provider that took one of these codes would be
// unreachable behind a route the agent answers itself, which is a routing table
// nobody could reason about.
func TestExtensionRoutesAreReserved(t *testing.T) {
	det := detector.New(detector.Config{})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}

	for _, code := range []string{"mask", "unmask"} {
		if _, err := New(Config{
			Providers: []Provider{{Code: code, BaseURL: "https://example.invalid"}},
		}, det, v); err == nil {
			t.Fatalf("a provider named %q was accepted", code)
		}
	}
}

// TestExtensionRoutesAreNotAudited is the invariant step 4 of the plan asks for.
//
// -a prints every value replaced and restored; -v writes both bodies of every
// exchange to disk. Both were designed around the proxy's exchanges, and a trace of
// /unmask would put originals on disk through a path that reasoning never covered.
// So the extension routes go through neither, and this holds it: both flags on, a
// full mask-and-unmask round trip, and nothing written and nothing printed.
func TestExtensionRoutesAreNotAudited(t *testing.T) {
	up := newUpstream(t, echoJSON)

	dir := filepath.Join(t.TempDir(), "traces")
	traces, err := newTracer(dir)
	if err != nil {
		t.Fatal(err)
	}

	console := &strings.Builder{}
	det := detector.New(detector.Config{Locales: []string{"fr"}})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{
		Providers:  []Provider{{Code: "anthropic", BaseURL: up.server.URL}},
		Audit:      console,
		Traces:     traces,
		ControlKey: testControlKey,
	}, det, v)
	if err != nil {
		t.Fatal(err)
	}
	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)

	masked := decodeReply[maskReply](t, postAs(t, agent, "/mask", testControlKey, "audited",
		`{"texts":["write to claire@example.fr"]}`))
	if masked.Masked != 1 {
		t.Fatalf("the round trip masked nothing, so this proves nothing: %q", masked.Texts[0])
	}
	back := decodeReply[unmaskReply](t, postAs(t, agent, "/unmask", testControlKey, "audited",
		body(t, unmaskRequest{Text: masked.Texts[0], Final: true})))
	if back.Expanded != "write to claire@example.fr" {
		t.Fatalf("the round trip did not restore the value: %q", back.Expanded)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the extension routes wrote %d trace file(s); a value in clear reached disk", len(entries))
	}
	if console.Len() != 0 {
		t.Fatalf("the extension routes printed to the audit console: %q", console.String())
	}
}

// TestExtensionRoutesRefuseTheWrongMethod, because a GET carrying text in a query
// string is a value in a URL, and URLs end up in logs and history.
func TestExtensionRoutesRefuseTheWrongMethod(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _ := newControlledAgent(t, up, []string{"fr"})

	for _, path := range []string{"/mask", "/unmask"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, agent.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(controlHeader, testControlKey)

		got := do(t, agent, req)
		if got.status != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s answered %d, want 405", path, got.status)
		}
		if got.header.Get("Allow") != http.MethodPost {
			t.Fatalf("GET %s did not say what to use instead: %q", path, got.header.Get("Allow"))
		}
	}
}

// TestExtensionRoutesRefuseABodyTheyCannotRead — fail closed, as the request path
// does on a body it cannot inspect.
func TestExtensionRoutesRefuseABodyTheyCannotRead(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, _ := newControlledAgent(t, up, []string{"fr"})

	for _, path := range []string{"/mask", "/unmask"} {
		got := postAs(t, agent, path, testControlKey, "", "not json at all")
		if got.status != http.StatusBadRequest {
			t.Fatalf("POST %s answered %d on a body it cannot read, want 400", path, got.status)
		}
	}

	// And a body past the ceiling, which is a bound on an authenticated local route
	// rather than a defence against anybody.
	huge := `{"text":"` + strings.Repeat("a", extensionMaxBytes) + `"}`
	if got := postAs(t, agent, "/unmask", testControlKey, "", huge); got.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized body answered %d, want 413", got.status)
	}
}

// jsonList encodes one string as a JSON array, so a test can put text carrying
// quotes into a request without escaping it by hand.
func jsonList(texts ...string) string {
	encoded, err := json.Marshal(texts)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func body(t *testing.T, v any) string {
	t.Helper()

	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// tokenIn returns the bracket token in a masked text, failing when there is none.
func tokenIn(t *testing.T, text string) string {
	t.Helper()

	start := strings.Index(text, "[")
	end := strings.Index(text, "]")
	if start < 0 || end < start {
		t.Fatalf("no token in %q", text)
	}
	return text[start : end+1]
}

// Fake mode, where the tail's two halves can disagree — and the shape no token
// test can reach, because no complete bracket token is the prefix of another.
//
// Fake IP addresses are minted 192.0.2.1, 192.0.2.2 … 192.0.2.11, so a session
// that masks eleven addresses holds a stand-in that is a proper prefix of another.
// Expanded before the holdback was decided, a chunk ending on the shorter one was
// replaced there and then with the wrong original, and the tail handed back was
// sliced out of restored text — so a fragment of a real original travelled to the
// page and was expanded a second time on the way in.
func TestUnmaskHoldsBackAStandInThatIsThePrefixOfAnother(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent, det := newControlledAgent(t, up, []string{"fr"})
	det.SetSubstitution(detector.SubstitutionFake)

	// One call, so the eleven share a mapping: /mask taking a list is what gives
	// them one identity, and it is what makes the prefix pair exist at all.
	var originals []string
	for i := 1; i <= 11; i++ {
		originals = append(originals, "10.1.1."+strconv.Itoa(i))
	}
	masked := decodeReply[maskReply](t, postAs(t, agent, "/mask", testControlKey, "fake",
		body(t, maskRequest{Texts: originals})))

	// Read off the reply rather than assumed: what matters is that some stand-in is
	// a proper prefix of another, not which one the catalogue happens to mint.
	short, long := "", ""
	for _, a := range masked.Texts {
		for _, b := range masked.Texts {
			if a != b && strings.HasPrefix(b, a) && len(b) > len(long) {
				short, long = a, b
			}
		}
	}
	if short == "" {
		t.Skipf("no stand-in in %v is the prefix of another, so this shape is unreachable", masked.Texts)
	}
	original := originals[slices.Index(masked.Texts, long)]

	// Split exactly where it hurts: the first chunk ends on the whole of the
	// shorter stand-in, and the next carries what makes it the longer one.
	one := decodeReply[unmaskReply](t, postAs(t, agent, "/unmask", testControlKey, "fake",
		body(t, unmaskRequest{Text: "host " + short})))
	two := decodeReply[unmaskReply](t, postAs(t, agent, "/unmask", testControlKey, "fake",
		body(t, unmaskRequest{Text: long[len(short):] + " is down", Tail: one.Tail, Final: true})))

	if whole := one.Expanded + two.Expanded; whole != "host "+original+" is down" {
		t.Errorf("the page rendered %q, want %q", whole, "host "+original+" is down")
	}
}
