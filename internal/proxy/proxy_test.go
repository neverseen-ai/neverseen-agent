package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/vault"
)

// The proxy's job is a round trip, and both halves have to be asserted on bytes:
// what actually left for the provider, and what actually came back to the
// caller. A test that only checked the masking would pass on a proxy that
// masked and never restored — which is a redactor, not this product.

// upstream is a fake provider that records every request body it received and
// answers with whatever a test tells it to.
type upstream struct {
	server *httptest.Server

	mu     sync.Mutex
	bodies []string
	paths  []string
}

func newUpstream(t *testing.T, handler http.HandlerFunc) *upstream {
	t.Helper()

	u := &upstream{}
	u.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		u.mu.Lock()
		u.bodies = append(u.bodies, string(body))
		u.paths = append(u.paths, r.URL.Path)
		u.mu.Unlock()

		// Put the body back: recording it must not consume it, or every handler
		// below reads an empty request and the test measures the recorder.
		r.Body = io.NopCloser(bytes.NewReader(body))
		handler(w, r)
	}))
	t.Cleanup(u.server.Close)
	return u
}

func (u *upstream) received() (bodies, paths []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.bodies...), append([]string(nil), u.paths...)
}

// echoJSON answers with the request body wrapped in a JSON field, so a test can
// see what the provider would have said about the text it was given.
func echoJSON(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"echo": string(body)})
}

// newAgent builds a proxy in front of one fake provider, addressed as
// "anthropic" because that is the code the end-to-end test uses too.
func newAgent(t *testing.T, up *upstream, locales []string) *httptest.Server {
	t.Helper()

	det := detector.New(detector.Config{Locales: locales})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}

	srv, err := New(Config{Providers: []Provider{
		{Code: "anthropic", BaseURL: up.server.URL},
		{Code: "openai", BaseURL: up.server.URL},
	}}, det, v)
	if err != nil {
		t.Fatal(err)
	}

	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)
	return agent
}

// reply is what a test needs from an exchange: the status, the headers, and the
// whole body as text. Returning this rather than a live *http.Response means no
// test has to remember to close anything.
type reply struct {
	status int
	header http.Header
	body   string
}

func post(t *testing.T, agent *httptest.Server, path, session, body string) reply {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, agent.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if session != "" {
		req.Header.Set("X-Session-Id", session)
	}
	return do(t, agent, req)
}

func do(t *testing.T, agent *httptest.Server, req *http.Request) reply {
	t.Helper()

	resp, err := agent.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read the response body: %v", err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: string(answer)}
}

func TestRoundTrip(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	const original = `{"messages":[{"content":"Écrire à claire@example.fr, NIR 184037511600176"}]}`

	got := post(t, agent, "/anthropic/v1/messages", "s1", original)

	// Half one: nothing the caller wrote reached the provider. Asserted on the
	// bytes that left, not on intent.
	bodies, paths := up.received()
	if len(bodies) != 1 {
		t.Fatalf("the provider saw %d requests, want 1", len(bodies))
	}
	for _, secret := range []string{"claire@example.fr", "184037511600176"} {
		if strings.Contains(bodies[0], secret) {
			t.Errorf("%q reached the provider unmasked: %s", secret, bodies[0])
		}
	}
	if !strings.Contains(bodies[0], "[EMAIL_") || !strings.Contains(bodies[0], "[NIR_") {
		t.Errorf("the provider did not receive tokens in their place: %s", bodies[0])
	}

	// The provider prefix is the agent's, not the provider's: it must be gone.
	if paths[0] != "/v1/messages" {
		t.Errorf("the provider was called at %q, want %q", paths[0], "/v1/messages")
	}

	// Half two: the caller gets its own data back.
	for _, value := range []string{"claire@example.fr", "184037511600176"} {
		if !strings.Contains(got.body, value) {
			t.Errorf("%q did not come back to the caller: %s", value, got.body)
		}
	}
	if strings.Contains(got.body, "[EMAIL_") {
		t.Errorf("a token survived into the answer: %s", got.body)
	}
}

// What the provider receives has to still be a JSON document, and masking the
// raw bytes as text does not guarantee that.
//
// This is the bug that made every real request fail. A string containing
// "…pourquoi.\n\n@RTK.md" is, in the bytes on the wire, a backslash followed by
// an "n" followed by "@RTK.md" — and the email pattern read "n@RTK.md" as an
// address, took the "n" out of the escape, and left a lone backslash in front of
// a bracket. The provider answered "invalid escaped character" and nothing worked
// at all.
func TestMaskedBodyIsStillValidJSON(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	// Built with the encoder, so the escapes on the wire are the real thing
	// rather than something a test author typed.
	body, err := json.Marshal(map[string]any{
		"system": "Ne pas supposer.\n\n@RTK.md\n\nContents of /Users/x/.claude/RTK.md:",
		"messages": []any{
			map[string]any{"content": "Écrire à claire@example.fr\nPuis \"relancer\" le 06 12 34 56 78."},
		},
		"max_tokens": 1024,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := post(t, agent, "/anthropic/v1/messages", "s1", string(body))

	bodies, _ := up.received()
	if len(bodies) != 1 {
		t.Fatalf("the provider saw %d requests, want 1", len(bodies))
	}

	var upstreamDoc map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &upstreamDoc); err != nil {
		t.Fatalf("the provider received something that is not JSON: %v\n%s", err, bodies[0])
	}

	// The escape was punctuation, not data: nothing near it may be masked, and
	// the text either side of it has to survive intact.
	system, _ := upstreamDoc["system"].(string)
	if !strings.Contains(system, "@RTK.md") {
		t.Errorf("a file reference was masked as an email address: %q", system)
	}

	// And the values that genuinely are values still are masked, so the fix did
	// not turn masking off.
	if strings.Contains(bodies[0], "claire@example.fr") || strings.Contains(bodies[0], "06 12 34 56 78") {
		t.Errorf("a real value reached the provider: %s", bodies[0])
	}

	// A number must not be rewritten on the way through.
	if !strings.Contains(bodies[0], "1024") {
		t.Errorf("max_tokens did not survive re-encoding: %s", bodies[0])
	}

	// The round trip has to end in valid JSON too, and an original carrying a
	// quote or a newline is exactly what breaks a raw splice on the way back.
	if !json.Valid([]byte(got.body)) {
		t.Errorf("the caller received something that is not JSON: %s", got.body)
	}
	if !strings.Contains(got.body, "claire@example.fr") {
		t.Errorf("the value did not come back: %s", got.body)
	}
}

func TestMapJSONStrings(t *testing.T) {
	t.Run("keys are left alone", func(t *testing.T) {
		// A key is the field name of an API, not somebody's data. Masking one
		// would break the request in a way no provider could interpret.
		out, ok := mapJSONStrings([]byte(`{"claire@example.fr":"claire@example.fr"}`),
			func(string) string { return "MASKED" })
		if !ok {
			t.Fatal("a valid document was rejected")
		}
		if !strings.Contains(string(out), `"claire@example.fr":"MASKED"`) {
			t.Errorf("got %s", out)
		}
	})

	t.Run("nested values are reached", func(t *testing.T) {
		out, ok := mapJSONStrings([]byte(`{"a":[{"b":"x"},["y"]],"c":{"d":"z"}}`),
			func(s string) string { return strings.ToUpper(s) })
		if !ok {
			t.Fatal("a valid document was rejected")
		}
		for _, want := range []string{`"X"`, `"Y"`, `"Z"`} {
			if !strings.Contains(string(out), want) {
				t.Errorf("%s is missing from %s", want, out)
			}
		}
	})

	t.Run("HTML is not escaped", func(t *testing.T) {
		// Go's encoder rewrites "<" as < by default, which is safe and
		// changes bytes the agent has no business changing.
		out, ok := mapJSONStrings([]byte(`{"a":"<b>&</b>"}`), func(s string) string { return s })
		if !ok {
			t.Fatal("a valid document was rejected")
		}
		if !strings.Contains(string(out), "<b>&</b>") {
			t.Errorf("HTML was escaped: %s", out)
		}
	})

	for _, raw := range []string{"not json", `{"a":1} {"b":2}`, ""} {
		t.Run("refused: "+raw, func(t *testing.T) {
			// Refused rather than half-processed, so the caller falls back to
			// treating the body as text instead of forwarding a mangled document.
			if _, ok := mapJSONStrings([]byte(raw), func(s string) string { return s }); ok {
				t.Errorf("mapJSONStrings accepted %q", raw)
			}
		})
	}
}

// One session's values must never be readable through another. The mapping is
// what turns a token back into a person, so sharing it across sessions would let
// one conversation read another's data.
func TestSessionsAreIsolated(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, nil)

	post(t, agent, "/anthropic/v1/messages", "alice", `{"c":"write to alice@example.fr"}`)

	// Bob's turn carries Alice's token. It must come back unexpanded: it means
	// nothing in his session.
	got := post(t, agent, "/anthropic/v1/messages", "bob", `{"c":"who is [EMAIL_1]?"}`)
	if strings.Contains(got.body, "alice@example.fr") {
		t.Errorf("one session's value was expanded in another: %s", got.body)
	}
}

// The same value, twice in one session, keeps one identity — even across two
// providers. That is the parity assertion worth making here: there is one
// pipeline, so the second provider must produce byte-identical masking rather
// than renumbering the same person.
func TestPipelineParityAcrossProviders(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	const body = `{"c":"claire@example.fr et 06 12 34 56 78"}`

	post(t, agent, "/anthropic/v1/messages", "same", body)
	post(t, agent, "/openai/v1/chat/completions", "same", body)

	bodies, _ := up.received()
	if len(bodies) != 2 {
		t.Fatalf("the provider saw %d requests, want 2", len(bodies))
	}
	if bodies[0] != bodies[1] {
		t.Errorf("the two providers received different masking of one body:\n  %s\n  %s", bodies[0], bodies[1])
	}
}

func TestUnknownProviderIsRefusedByName(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, nil)

	got := post(t, agent, "/vertex/v1/messages", "s", `{"c":"hello"}`)
	if got.status != http.StatusNotFound {
		t.Errorf("status %d, want 404", got.status)
	}
	// The message has to name what does exist: a proxy that silently picked a
	// provider would send one vendor's key to another vendor, so refusing is
	// right and saying which codes work is what makes it usable.
	for _, want := range []string{"vertex", "anthropic", "openai"} {
		if !strings.Contains(got.body, want) {
			t.Errorf("the refusal does not mention %q: %s", want, got.body)
		}
	}

	if bodies, _ := up.received(); len(bodies) != 0 {
		t.Errorf("the request reached a provider anyway: %v", bodies)
	}
}

// A gzipped request body is decompressed, masked, and forwarded uncompressed.
// Passing it through untouched would be a leak the agent could not even see.
func TestGzippedRequestBodyIsMasked(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, nil)

	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write([]byte(`{"c":"write to claire@example.fr"}`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		agent.URL+"/anthropic/v1/messages", bytes.NewReader(compressed.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	do(t, agent, req)

	bodies, _ := up.received()
	if len(bodies) != 1 {
		t.Fatalf("the provider saw %d requests, want 1", len(bodies))
	}
	if strings.Contains(bodies[0], "claire@example.fr") {
		t.Errorf("the value reached the provider unmasked: %s", bodies[0])
	}
	if !strings.Contains(bodies[0], "[EMAIL_") {
		t.Errorf("the gzipped body was not masked: %s", bodies[0])
	}
}

// An encoding the agent cannot read is refused rather than forwarded. Failing
// open here would be the one failure mode a data loss prevention tool must never
// have: a body it never inspected, sent on anyway.
func TestUnreadableEncodingFailsClosed(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, nil)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		agent.URL+"/anthropic/v1/messages", strings.NewReader("whatever this is"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Encoding", "br")

	if got := do(t, agent, req); got.status != http.StatusUnsupportedMediaType {
		t.Errorf("status %d, want 415", got.status)
	}
	if bodies, _ := up.received(); len(bodies) != 0 {
		t.Errorf("an uninspectable body was forwarded: %v", bodies)
	}
}

// A response the agent should not be reading at all is passed through untouched.
func TestNonTextualResponseIsUntouched(t *testing.T) {
	const payload = "\x89PNG\r\n\x1a\n not really a png"

	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		fmt.Fprint(w, payload)
	})
	agent := newAgent(t, up, nil)

	// A masked value first, so the session has a mapping to expand with.
	post(t, agent, "/anthropic/v1/messages", "s", `{"c":"write to claire@example.fr"}`)

	if got := post(t, agent, "/anthropic/v1/messages", "s", `{"c":"the image"}`); got.body != payload {
		t.Errorf("a binary response was rewritten:\n got %q\nwant %q", got.body, payload)
	}
}

func TestHealth(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, agent.URL+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := do(t, agent, req)
	if got.status != http.StatusOK {
		t.Errorf("status %d, want 200", got.status)
	}
	// The health line says what the agent is applying, which is what makes it
	// worth having: an operator can see the locale from outside the process.
	//
	// Decoded into the type the route is marshalled from, rather than matched as a
	// substring: the point of one type for both sides is that a field cannot be
	// renamed on one of them, and an assertion against hand-written JSON would be
	// a third spelling to keep in step.
	var health Health
	if err := json.Unmarshal([]byte(got.body), &health); err != nil {
		t.Fatalf("the health line does not parse: %v — %s", err, got.body)
	}
	if health.Status != "ok" || health.Substitution != "token" ||
		!slices.Equal(health.Locales, []string{"fr"}) {
		t.Errorf("the health line says %+v, want the locale and mode being applied", health)
	}
	if health.Version == "" || len(health.Providers) == 0 {
		t.Errorf("the health line leaves version or providers empty: %+v", health)
	}
}

func TestParseProviders(t *testing.T) {
	t.Run("an empty spec keeps the defaults", func(t *testing.T) {
		got, err := ParseProviders("")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(DefaultProviders) {
			t.Errorf("got %d providers, want the %d defaults", len(got), len(DefaultProviders))
		}
	})

	t.Run("an override replaces one and keeps the rest", func(t *testing.T) {
		// A replacement rather than an override would make a deployment that
		// redirects one provider look like it had lost the other seven.
		got, err := ParseProviders("anthropic=https://gateway.internal")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(DefaultProviders) {
			t.Fatalf("got %d providers, want %d", len(got), len(DefaultProviders))
		}
		for _, p := range got {
			if p.Code == "anthropic" && p.BaseURL != "https://gateway.internal" {
				t.Errorf("anthropic points at %q", p.BaseURL)
			}
			if p.Code == "openai" && p.BaseURL != "https://api.openai.com" {
				t.Errorf("openai was changed to %q", p.BaseURL)
			}
		}
	})

	t.Run("a new code is added", func(t *testing.T) {
		got, err := ParseProviders("vertex=https://aiplatform.googleapis.com")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(DefaultProviders)+1 {
			t.Errorf("got %d providers, want %d", len(got), len(DefaultProviders)+1)
		}
	})

	for _, spec := range []string{"anthropic", "=https://x", "anthropic=", "anthropic=ftp://x", "anthropic=://"} {
		t.Run("refused: "+spec, func(t *testing.T) {
			if _, err := ParseProviders(spec); err == nil {
				t.Errorf("ParseProviders(%q) accepted an invalid override", spec)
			}
		})
	}
}

func TestSplitProvider(t *testing.T) {
	tests := []struct{ path, code, rest string }{
		{"/anthropic/v1/messages", "anthropic", "/v1/messages"},
		{"/anthropic/", "anthropic", "/"},
		{"/anthropic", "anthropic", "/"},
		{"/openai/v1/chat/completions", "openai", "/v1/chat/completions"},
		{"/", "", "/"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			code, rest := splitProvider(tt.path)
			if code != tt.code || rest != tt.rest {
				t.Errorf("splitProvider(%q) = %q, %q; want %q, %q", tt.path, code, rest, tt.code, tt.rest)
			}
		})
	}
}

// The body forwarded to the provider keeps its fields in the order the tool sent
// them, at every depth.
//
// Decoding through a map[string]any lost that, because a Go map has no order: the
// agent re-encoded the document in Go's sorted marshal order. It is the same
// document to a parser and nothing depended on it — but the audit console prints
// the body received and the body sent to be read against each other, and two
// bodies whose fields are in different orders cannot be.
func TestTheMaskedBodyKeepsItsKeyOrder(t *testing.T) {
	// Deliberately not alphabetical, and not the order Go would choose: "model"
	// before "max_tokens", "system" before "messages", and inside the message
	// "role" after "content".
	const body = `{"model":"claude-opus-5","max_tokens":1024,` +
		`"system":[{"type":"text","text":"écris à pierre.paul@example.fr"}],` +
		`"messages":[{"content":"tél 06 12 34 56 78","role":"user"}]}`

	got, ok := mapJSONStrings([]byte(body), func(s string) string { return s })
	if !ok {
		t.Fatal("the body was not read as JSON")
	}
	if string(got) != body {
		t.Errorf("the order changed:\n got %s\nwant %s", got, body)
	}
}

// A duplicated key is kept rather than collapsed. Two members of the same name are
// pathological rather than useful, but silently keeping one is the agent deciding
// which — and whichever it dropped would have gone to the provider in the original.
func TestADuplicatedKeyIsNotCollapsed(t *testing.T) {
	const body = `{"prompt":"first","prompt":"second"}`

	got, ok := mapJSONStrings([]byte(body), strings.ToUpper)
	if !ok {
		t.Fatal("the body was not read as JSON")
	}
	if want := `{"prompt":"FIRST","prompt":"SECOND"}`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// The same holds on the way back: a streamed event is decoded, expanded and
// re-encoded, so its fields would be reordered by the same defect.
func TestAStreamedEventKeepsItsKeyOrder(t *testing.T) {
	const event = `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"bonjour"}}`

	decoded, err := decodeEvent(event)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, err := encodeJSONBody(decoded)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(got) != event {
		t.Errorf("the order changed:\n got %s\nwant %s", got, event)
	}
}

// HTML is not rewritten on its way through the ordered encoder either. Go's
// encoder turns "<" into its numeric escape by default, which is safe and changes
// bytes the agent has no business changing — a prompt full of markup would come
// back to the caller looking mangled.
func TestAngleBracketsSurviveTheRoundTrip(t *testing.T) {
	const body = `{"prompt":"<div class=\"x\">a & b</div>"}`

	got, ok := mapJSONStrings([]byte(body), func(s string) string { return s })
	if !ok {
		t.Fatal("the body was not read as JSON")
	}
	if string(got) != body {
		t.Errorf("got %s, want %s", got, body)
	}
}
