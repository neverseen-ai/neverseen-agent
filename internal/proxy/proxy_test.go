package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
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
	return newAgentOver(t, up, locales, vault.NewMemory())
}

// newAgentOver is newAgent over a store the test keeps a handle on, for a test that
// has to expire the mapping between two turns without sleeping through a lifetime.
func newAgentOver(t *testing.T, up *upstream, locales []string, store vault.Store) *httptest.Server {
	t.Helper()
	return newAgentWith(t, up, locales, store, nil)
}

// newAgentWith is newAgentOver with a log the test keeps, nil for none.
func newAgentWith(t *testing.T, up *upstream, locales []string, store vault.Store, logger *slog.Logger) *httptest.Server {
	t.Helper()

	det := detector.New(detector.Config{Locales: locales})
	v, err := vault.New(store, nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}

	// The same upstream under two hosts, because the one thing the request path
	// decides on the destination — identifierHost — has to be able to tell the
	// two routes apart. `anthropicIs` points that decision at the first.
	srv, err := New(Config{Providers: []Provider{
		{Code: "anthropic", BaseURL: up.server.URL},
		{Code: "openai", BaseURL: strings.Replace(up.server.URL, "127.0.0.1", "localhost", 1)},
	}, Logger: logger}, det, v)
	if err != nil {
		t.Fatal(err)
	}

	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)
	return agent
}

// anthropicIs makes the test upstream count as Anthropic for the exemption
// identifiers.go grants, for the length of one test. The rule is keyed on the
// resolved host rather than on the route's code, and the test upstream is not
// api.anthropic.com — so a test asserting that Anthropic's own identifiers reach
// it in clear has to say which host Anthropic is.
func anthropicIs(t *testing.T, up *upstream) {
	t.Helper()
	was := identifierHost
	identifierHost = "127.0.0.1"
	t.Cleanup(func() { identifierHost = was })
	if !strings.Contains(up.server.URL, identifierHost) {
		t.Fatalf("the test upstream is at %s, not on %s", up.server.URL, identifierHost)
	}
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
	if strings.Contains(bodies[0], "benoit@example.fr") || strings.Contains(bodies[0], "06 12 34 56 78") {
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

// A token the model remembers is restored after a pause expired the mapping.
//
// What the model reasoned about comes back to it inside the thinking block's
// signature, encrypted and out of the agent's reach, so it still says the name it
// reasoned with. The history, replayed in clear, used to be masked afresh under a
// new index — and the caller read "Le boîtier de [PLATE_381]" back.
func TestATokenTheModelRemembersSurvivesAnExpiredMapping(t *testing.T) {
	var (
		mu      sync.Mutex
		turn    int
		said    string
		plateRe = regexp.MustCompile(`\[PLATE_\d+\]`)
	)
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		turn++
		if turn == 1 {
			said = plateRe.FindString(string(body))
		}
		// Every turn answers with the name the model met on the first.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "Le boîtier de " + said + " n'envoie pas de cap."})
	})

	store := vault.NewMemory()
	agent := newAgentOver(t, up, []string{"fr"}, store)

	const body = `{"messages":[{"role":"user","content":"Le véhicule AB-123-CD n'envoie pas de cap."}]}`
	post(t, agent, "/anthropic/v1/messages", "s1", body)
	mu.Lock()
	first := said
	mu.Unlock()
	if first == "" {
		t.Fatal("the first turn minted no plate token; the test proves nothing")
	}

	// The store's own Forget, not the vault's: it drops every mapping as a pause
	// past the lifetime does, and leaves the names the vault remembers where they are.
	store.Forget()
	got := post(t, agent, "/anthropic/v1/messages", "s1", body)

	bodies, _ := up.received()
	if len(bodies) != 2 || !strings.Contains(bodies[1], first) {
		t.Errorf("the second turn renamed the plate: want %s in %v", first, bodies)
	}
	if !strings.Contains(got.body, "AB-123-CD") || strings.Contains(got.body, "[PLATE_") {
		t.Errorf("the token the model remembered was not restored: %s", got.body)
	}
}

// A name is never handed to a value when the body already carries it as text.
//
// The model writes placeholders in the shape it sees: on 14 September, with the
// session's numbering at [MONGOID_2], it wrote a test fixture holding [MONGOID_3] to
// [MONGOID_6]. The tool kept them as text, sent them back in the history, and the next
// request minted [MONGOID_3] for a real identifier — one string standing for two
// things in what the model read, and the model's own placeholder restored as the real
// value in any answer that named it.
func TestAMintedNameIsNeverOneTheBodyAlreadyCarries(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "Le fixture garde [PLATE_1]."})
	})
	agent := newAgentOver(t, up, []string{"fr"}, vault.NewMemory())

	const body = `{"messages":[` +
		`{"role":"assistant","content":"const plate = '[PLATE_1]';"},` +
		`{"role":"user","content":"Le véhicule AB-123-CD n'envoie pas de cap."}]}`
	got := post(t, agent, "/anthropic/v1/messages", "s1", body)

	bodies, _ := up.received()
	if len(bodies) != 1 {
		t.Fatalf("want one request upstream, got %d", len(bodies))
	}
	if strings.Contains(bodies[0], "AB-123-CD") {
		t.Fatalf("the plate went out in clear; the test proves nothing: %s", bodies[0])
	}
	if n := strings.Count(bodies[0], "[PLATE_1]"); n != 1 {
		t.Errorf("[PLATE_1] stands for %d things in what the model read, want only the placeholder: %s", n, bodies[0])
	}
	if strings.Contains(got.body, "AB-123-CD") {
		t.Errorf("the model's placeholder was restored as the real plate: %s", got.body)
	}
}

// startFromEnv assembles the agent as the command does — FromEnv — in front of up,
// keeping its session book in names and nothing else of the operator's. Called twice
// on one names, it is a restart: a second assembly reading what the first one wrote.
func startFromEnv(t *testing.T, up *upstream, names string, logger *slog.Logger) *httptest.Server {
	t.Helper()
	t.Setenv(EnvProviders, "anthropic="+up.server.URL)
	t.Setenv("NEVERSEEN_PII_LOCALE", "fr")
	t.Setenv("NEVERSEEN_BACKEND_URL", "")
	agent, err := FromEnv(logger, Options{PolicyFile: NoFile, ControlKeyFile: NoFile, NamesFile: names})
	if err != nil {
		t.Fatalf("the agent did not start: %v", err)
	}
	server := httptest.NewServer(agent.Server.Handler())
	t.Cleanup(server.Close)
	return server
}

// rememberingModel answers every request naming the vehicle it met first.
func rememberingModel(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"text": "Le boîtier de [PLATE_1] n'envoie pas de cap."})
}

// A restart does not hand a name the conversation remembers to another value.
//
// The counters used to start again at 1 with every process. A conversation that
// ran through a restart remembered [PLATE_1] as one vehicle, the new process gave
// [PLATE_1] to the first plate it met, and the answer naming the first came back
// restored — as the second.
func TestARestartDoesNotHandARememberedNameToAnotherValue(t *testing.T) {
	up := newUpstream(t, rememberingModel)
	names := filepath.Join(t.TempDir(), "names.json")

	post(t, startFromEnv(t, up, names, nil), "/anthropic/v1/messages", "s1", `{"messages":[{"content":"Le véhicule AB-123-CD."}]}`)
	got := post(t, startFromEnv(t, up, names, nil), "/anthropic/v1/messages", "s1", `{"messages":[{"content":"Et le véhicule EF-456-GH ?"}]}`)

	bodies, _ := up.received()
	if len(bodies) != 2 || !strings.Contains(bodies[0], "[PLATE_1]") {
		t.Fatalf("the first process did not name the plate [PLATE_1]: %v", bodies)
	}
	if strings.Contains(bodies[1], "[PLATE_1]") {
		t.Errorf("the restarted agent reused [PLATE_1] for another plate: %s", bodies[1])
	}
	if strings.Contains(got.body, "EF-456-GH") {
		t.Errorf("the name the model remembered came back as the other plate: %s", got.body)
	}
}

// A restart hands the conversation back the names it remembers. The numbering alone
// stopped a remembered name being restored as another value; it still came back
// unexpanded, because the new process gave the replayed value a new name. With the
// book kept, the value the history replays takes its old name, and the answer
// naming it is restored. The other plate comes first in the second body, so a
// process numbering afresh would give it the next index and the remembered value
// the one after: only the book can put [PLATE_1] back on the right vehicle.
func TestARestartKeepsTheNamesAConversationRemembers(t *testing.T) {
	up := newUpstream(t, rememberingModel)
	dir := t.TempDir()
	names := filepath.Join(dir, "names.json")

	post(t, startFromEnv(t, up, names, nil), "/anthropic/v1/messages", "s1", `{"messages":[{"content":"Le véhicule AB-123-CD."}]}`)
	got := post(t, startFromEnv(t, up, names, nil), "/anthropic/v1/messages", "s1",
		`{"messages":[{"content":"Et le véhicule EF-456-GH ?"},{"content":"Le véhicule AB-123-CD."}]}`)

	if !strings.Contains(got.body, "AB-123-CD") {
		t.Errorf("the name the conversation remembers was not restored after the restart: %s", got.body)
	}
	if strings.Contains(got.body, "EF-456-GH") {
		t.Errorf("the name the conversation remembers came back as the other plate: %s", got.body)
	}

	// The key that makes the book's fingerprints testable is kept apart from it,
	// and only its owner can read either.
	for _, file := range []string{names, filepath.Join(dir, namesKeyFile)} {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatalf("%s was not written: %v", file, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v, want 0600", file, info.Mode().Perm())
		}
	}
	raw, _ := os.ReadFile(names)
	if strings.Contains(string(raw), "AB-123-CD") || strings.Contains(string(raw), "EF-456-GH") {
		t.Errorf("the book holds a value: %s", raw)
	}
}

// Names minted in one substitution mode are not handed back in another. The mode
// can change between two processes without PUT /policy — here the environment, with
// no policy file — and a value seen before would otherwise keep the old shape: the
// control appearing not to work. The numbering stays, so the new name is not one the
// conversation was told meant something else.
func TestARestartInAnotherModeGivesEveryValueANewName(t *testing.T) {
	up := newUpstream(t, echoJSON)
	names := filepath.Join(t.TempDir(), "names.json")
	const body = `{"messages":[{"content":"écrire à claire@example.fr"}]}`

	post(t, startFromEnv(t, up, names, nil), "/anthropic/v1/messages", "s1", body)
	t.Setenv("NEVERSEEN_PII_SUBSTITUTION", "fake")
	post(t, startFromEnv(t, up, names, nil), "/anthropic/v1/messages", "s1", body)

	bodies, _ := up.received()
	if len(bodies) != 2 || !strings.Contains(bodies[0], "[EMAIL_1]") {
		t.Fatalf("the first process did not name the address [EMAIL_1]: %v", bodies)
	}
	if strings.Contains(bodies[1], "[EMAIL_1]") || strings.Contains(bodies[1], "claire@example.fr") {
		t.Errorf("after the switch to fake the address went out as %s, want a stand-in", bodies[1])
	}
}

// A book that cannot be read leaves the agent counting from 1 — where every restart
// left it before the file existed — rather than refusing to mask; and the next
// request writes a file that reads.
func TestAnUnreadableNamesFileIsReplacedNotFatal(t *testing.T) {
	up := newUpstream(t, echoJSON)
	names := filepath.Join(t.TempDir(), "names.json")
	if err := os.WriteFile(names, []byte("{half a document"), 0o600); err != nil {
		t.Fatal(err)
	}

	post(t, startFromEnv(t, up, names, nil), "/anthropic/v1/messages", "s1", `{"c":"écrire à claire@example.fr"}`)

	var stored namesFile
	if _, err := readStateFile(names, &stored); err != nil || stored.Sessions["s1"].Highest["EMAIL"] != 1 || stored.Mode != "token" {
		t.Errorf("the book written after the request = %+v, %v; want s1 at EMAIL 1, in token mode", stored, err)
	}
}

// A book nobody can read or write — here a path under a regular file — costs a
// warning at start-up and one per write, and never the masking.
func TestANamesFileThatCannotBeWrittenIsAWarning(t *testing.T) {
	up := newUpstream(t, echoJSON)
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	var logged bytes.Buffer
	server := startFromEnv(t, up, filepath.Join(notADir, "names.json"), slog.New(slog.NewTextHandler(&logged, nil)))
	post(t, server, "/anthropic/v1/messages", "s1", `{"c":"écrire à claire@example.fr"}`)

	bodies, _ := up.received()
	if len(bodies) != 1 || strings.Contains(bodies[0], "claire@example.fr") {
		t.Errorf("the request was not masked: %v", bodies)
	}
	for _, want := range []string{"could not be read", "could not be stored"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("no warning saying the book %s:\n%s", want, logged.String())
		}
	}
}

// Uninstalling deletes the book and its key — the one exception to ~/.neverseen
// being left alone, because nothing sweeps the book once the agent is gone — and
// nothing else beside them. A book already absent is not an error.
func TestForgetNamesDeletesTheBookAndItsKeyAlone(t *testing.T) {
	dir := t.TempDir()
	names := filepath.Join(dir, "names.json")
	for _, file := range []string{names, filepath.Join(dir, namesKeyFile), filepath.Join(dir, "policy.json")} {
		if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := ForgetNames(names)
	if err != nil || len(removed) != 2 {
		t.Fatalf("ForgetNames = %v, %v; want the book and its key", removed, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "policy.json")); err != nil {
		t.Errorf("ForgetNames took the policy with it: %v", err)
	}
	if removed, err := ForgetNames(names); err != nil || len(removed) != 0 {
		t.Errorf("a second ForgetNames = %v, %v; want nothing removed and no error", removed, err)
	}

	// A file that will not go is reported, not skipped: a directory in the key's place.
	if err := os.Mkdir(filepath.Join(dir, namesKeyFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, namesKeyFile, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ForgetNames(names); err == nil {
		t.Error("a key that could not be deleted was not reported")
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
	if strings.Contains(bodies[0], "benoit@example.fr") {
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

// A page that rebinds its own domain to 127.0.0.1 reaches the loopback socket, but
// its browser still sends that domain as the Host — and that request must not reach
// the provider, or the page reads the restored answer.
func TestARequestForAnotherHostIsRefused(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		agent.URL+"/anthropic/v1/messages", strings.NewReader(`{"text":"benoit@example.fr"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Host = "rebound.example.com:9787"

	if got := do(t, agent, req); got.status != http.StatusMisdirectedRequest {
		t.Errorf("status %d, want 421", got.status)
	}
	if bodies, _ := up.received(); len(bodies) != 0 {
		t.Errorf("a request for another host was forwarded: %v", bodies)
	}
}

// Which Host values the agent answers for. Any IP literal, because a rebinding
// attack always arrives under a domain name and an agent bound beyond loopback is
// reached by its LAN address; any other name, only the one -l gave.
func TestTheAgentAnswersForItsOwnNamesOnly(t *testing.T) {
	for _, tt := range []struct {
		listen, host string
		want         bool
	}{
		{"127.0.0.1:9787", "127.0.0.1:9787", true},
		{"127.0.0.1:9787", "localhost:9787", true},
		{"127.0.0.1:9787", "LOCALHOST", true},
		{"127.0.0.1:9787", "[::1]:9787", true},
		{":9787", ":9787", true},     // what a local command sends when -l names no host
		{"127.0.0.1:9787", "", true}, // HTTP/1.0 with no Host, which no browser sends
		{":9787", "192.168.1.20:9787", true},
		{"0.0.0.0:9787", "[fe80::1]:9787", true},
		{"workstation.lan:9787", "workstation.lan:9787", true},
		{"workstation.lan:9787", "Workstation.LAN", true},

		{"127.0.0.1:9787", "rebound.example.com:9787", false},
		{"127.0.0.1:9787", "rebound.example.com", false},
		{"127.0.0.1:9787", "localhost:9787@rebound.example.com", false},
		{"127.0.0.1:9787", "127.0.0.1.rebound.example.com", false},
		{"workstation.lan:9787", "other.lan:9787", false},
		{":9787", "host.docker.internal:9787", false}, // the TODO on guardHost
	} {
		srv := &Server{listenName: listenName(tt.listen)}
		if got := srv.answersFor(tt.host); got != tt.want {
			t.Errorf("listening on %q, answersFor(%q) = %v, want %v", tt.listen, tt.host, got, tt.want)
		}
	}
}

// A body over the cap is refused, not forwarded and not buffered whole — measured
// after decompression, so a gzip bomb a few kilobytes long is refused too.
func TestOversizedBodyIsRefused(t *testing.T) {
	var bomb bytes.Buffer
	zw := gzip.NewWriter(&bomb)
	if _, err := zw.Write(make([]byte, requestMaxBytes+1)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name     string
		body     []byte
		encoding string
	}{
		{"plain", bytes.Repeat([]byte("a"), requestMaxBytes+1), ""},
		{"gzip", bomb.Bytes(), "gzip"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			up := newUpstream(t, echoJSON)
			agent := newAgent(t, up, nil)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
				agent.URL+"/anthropic/v1/messages", bytes.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if tt.encoding != "" {
				req.Header.Set("Content-Encoding", tt.encoding)
			}

			if got := do(t, agent, req); got.status != http.StatusRequestEntityTooLarge {
				t.Errorf("status %d, want 413", got.status)
			}
			if bodies, _ := up.received(); len(bodies) != 0 {
				t.Errorf("an oversized body was forwarded")
			}
		})
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

// A body with nothing to mask reaches the provider as the bytes that arrived:
// indentation, escapes and all. Re-encoded, it went out compacted with `é` and
// `\/` decoded — the same document to a parser, and still an agent visible where it
// had nothing to do.
func TestABodyWithNothingToMaskIsForwardedByteForByte(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	const body = "{\n  \"model\": \"claude-opus-5\",\n" +
		"  \"messages\": [\n    {\"role\": \"user\", \"content\": \"caf\\u00e9 \\/ th\\u00e9\\n\\tsuite\"}\n  ]\n}\n"

	post(t, agent, "/anthropic/v1/messages", "", body)

	bodies, _ := up.received()
	if len(bodies) != 1 {
		t.Fatalf("the provider saw %d requests, want 1", len(bodies))
	}
	if bodies[0] != body {
		t.Errorf("the body was rewritten with nothing to mask in it:\n got %q\nwant %q", bodies[0], body)
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

// An image pasted into a prompt is a base64 payload, and masking it destroys it.
//
// The catalogue is measured against prose. Over the few hundred kilobytes of
// random base64 a picture is, a vendor prefix fires by chance — two of them over
// 200KB, measured — and the handful of characters replaced by a token are in the
// middle of the picture. Anthropic then answers "an image in the conversation
// could not be processed and was removed", on every request carrying one, and
// nothing in the exchange says why.
//
// Both halves: the payload leaves byte for byte, and the text beside it is still
// masked, so the exemption is the payload rather than the whole message.
func TestAPastedImageReachesTheProviderIntact(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	// A payload holding values the catalogue does mask, which is the strongest
	// form of the assertion: not "nothing happened to be found in it" but "what
	// was found in it was left alone".
	payload := "iVBORw0KGgoAAAANSUhEUg" +
		"sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" +
		"claire@example.fr" +
		"AAAAAAAAAAAAAAAAAAAAAAAA"
	uri := "data:image/png;base64," + payload

	body, err := json.Marshal(map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": []any{
				// Anthropic's shape: the object says its data member is bytes.
				map[string]any{"type": "image", "source": map[string]any{
					"type": "base64", "media_type": "image/png", "data": payload,
				}},
				// And the OpenAI-compatible one, where the value says it itself.
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": uri}},
				map[string]any{"type": "text", "text": "Écrire à benoit@example.fr"},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	post(t, agent, "/anthropic/v1/messages", "s-image", string(body))

	bodies, _ := up.received()
	if len(bodies) != 1 {
		t.Fatalf("the provider saw %d requests, want 1", len(bodies))
	}
	if !strings.Contains(bodies[0], payload) {
		t.Errorf("the image was rewritten on the way out:\n%s", bodies[0])
	}
	if !strings.Contains(bodies[0], uri) {
		t.Errorf("the data URI was rewritten on the way out:\n%s", bodies[0])
	}
	if strings.Contains(bodies[0], "benoit@example.fr") {
		t.Errorf("the text beside the image was not masked:\n%s", bodies[0])
	}
}

// The name alone is no evidence. A `data` member outside an object declaring
// base64 is the caller's own text — a tool called with one carries a prompt in
// it — and exempting it by name would forward whatever it holds in clear.
func TestADataMemberThatDeclaresNothingIsStillMasked(t *testing.T) {
	out, ok := mapJSONStrings([]byte(`{"input":{"data":"x"},"s":{"type":"base64","data":"y"}}`),
		func(string) string { return "MASKED" })
	if !ok {
		t.Fatal("a valid document was rejected")
	}
	if !strings.Contains(string(out), `"input":{"data":"MASKED"}`) {
		t.Errorf("a plain data member was exempted: %s", out)
	}
	if !strings.Contains(string(out), `"data":"y"`) {
		t.Errorf("a declared payload was masked: %s", out)
	}
}

// openRoutes are the reserved routes that answer a caller without the control key,
// each with the reason it may. Every other reserved route must refuse one.
var openRoutes = map[string]string{
	"healthz":  "describes the configuration; carries no content and no counter",
	"test":     "masks a text the caller sent itself; reads and writes no session",
	"settings": "serves the page; the key is in it only with a ticket minted with the key",
	"prove":    "proves the agent holds the key without receiving it; signs a nonce of one shape",
}

// Authentication is something each handler has to remember to ask for, so this asks
// it of every route the agent answers itself, through the whole handler: without the
// key, none hands the key over, and each one not named in openRoutes refuses. A route
// added to reservedRoutes without being classified fails here, which makes refusal the
// default — the lesson of NetBird's daemon, whose API every local process could call
// for three years.
func TestEveryRouteRefusesACallerWithoutTheKey(t *testing.T) {
	for code := range openRoutes {
		if !slices.Contains(reservedRoutes, code) {
			t.Errorf("openRoutes names %q, which is not a reserved route", code)
		}
	}

	// A fresh key rather than testControlKey, which the /test page's own sample text
	// happens to contain as a hex secret to be masked.
	key, err := newControlKey()
	if err != nil {
		t.Fatal(err)
	}
	handler := settingsAgent(t, key).Handler()
	for _, code := range reservedRoutes {
		t.Run(code, func(t *testing.T) {
			_, open := openRoutes[code]
			refused := false
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut} {
				req := httptest.NewRequestWithContext(t.Context(), method, "/"+code, strings.NewReader("{}"))
				req.RemoteAddr = "127.0.0.1:52341"
				req.Host = "localhost:9787"
				req.Header.Set("Content-Type", "application/json")

				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				if strings.Contains(rec.Body.String(), key) {
					t.Errorf("%s /%s handed the control key to a caller without it", method, code)
				}
				if open {
					continue
				}
				if rec.Code < http.StatusMultipleChoices {
					t.Errorf("%s /%s answered %d to a caller without the key", method, code, rec.Code)
				}
				refused = refused || rec.Code == http.StatusForbidden
			}
			if !open && !refused {
				t.Errorf("/%s refused no method with 403, so it never asked for the key; "+
					"if it needs none, name it in openRoutes with the reason", code)
			}
		})
	}
}

// A pass asks, for each name it mints, whether the text already holds it as text. The
// tokens are collected once and asked of; a stand-in, which has no shape to collect, is
// searched for. Both must answer as a search of the text would.
func TestCarriedInAnswersAsASearchWould(t *testing.T) {
	carried := carriedIn([]byte(`{"text":"write to [EMAIL_3] or Jean Dupont, not [email_4]"}`))
	for name, want := range map[string]bool{
		"[EMAIL_3]":   true,
		"[EMAIL_4]":   false, // only in another case, which is no token
		"[EMAIL_33]":  false,
		"Jean Dupont": true,
		"Jean Durand": false,
	} {
		if got := carried(name); got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
}
