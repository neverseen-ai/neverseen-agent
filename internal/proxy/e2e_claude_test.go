package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/vault"
)

// The end-to-end test the unit suite cannot replace: a real agent, a real
// provider, and the proxy between them.
//
// It asserts the product's actual claim, which no unit test reaches — that the
// provider never sees a value the caller wrote, and that the caller gets it back
// anyway. Both halves need a real session to mean anything. The masking is on the
// request Claude Code composes, not one a test wrote; the restoration is in the
// answer it prints, after a full streaming exchange with deltas that fall wherever
// the provider happens to cut them.
//
// A tap sits between the proxy and Anthropic and keeps every request body that
// went upstream, so "the provider never saw it" is a statement about bytes rather
// than about intent.
//
// Skipped unless CLOAKFLEET_E2E_CLAUDE is set: it spends the operator's Claude
// quota and needs the CLI signed in. Run it with:
//
//	CLOAKFLEET_E2E_CLAUDE=1 go test ./internal/proxy/ -run TestE2EClaudeCode -v
func TestE2EClaudeCode(t *testing.T) {
	if os.Getenv("CLOAKFLEET_E2E_CLAUDE") == "" {
		t.Skip("CLOAKFLEET_E2E_CLAUDE is unset: this test runs the Claude CLI against the real " +
			"provider and spends the operator's quota. Set it to run.")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("the claude CLI is not on PATH: %v", err)
	}

	tap := newUpstreamTap(t, "https://api.anthropic.com")

	det := detector.New(detector.Config{Locales: []string{"fr", "gb", "us"}})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{Providers: []Provider{
		{Code: "anthropic", BaseURL: tap.URL()},
	}}, det, v)
	if err != nil {
		t.Fatal(err)
	}

	agent := httptest.NewServer(srv.Handler())
	defer agent.Close()

	// The task: turn the sample into an HTML page. It is asked to print the page
	// rather than write it, so the session needs no tool permission and leaves
	// nothing behind. Keeping the values verbatim is what makes the answer carry
	// evidence of the restoration.
	prompt := "Voici un texte. Produis une page HTML complète qui le met en forme, " +
		"une <section> par titre encadré de ===, en conservant toutes les valeurs " +
		"telles quelles. N'écris aucun fichier : affiche seulement le HTML.\n\n" +
		det.Sample()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "-p", prompt)
	cmd.Dir = t.TempDir() // nothing the session does can touch the repository
	cmd.Env = append(os.Environ(), "ANTHROPIC_BASE_URL="+agent.URL+"/anthropic")

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("the claude session failed: %v\nstderr:\n%s", err, stderr.String())
	}
	answer := stdout.String()

	// Written before the assertions, so a failure leaves the evidence behind
	// rather than only a message about it.
	page := saveE2EPage(t, answer)

	if !strings.Contains(strings.ToLower(answer), "<section") {
		t.Errorf("the answer carries no <section>, so the task was not done:\n%s", truncate(answer, 800))
	}

	sent := tap.bodies()
	if len(sent) == 0 {
		t.Fatal("nothing reached the tap: the session did not go through the proxy")
	}
	upstream := strings.Join(sent, "\n")

	// Half one, the one that matters: no value the caller wrote reached the
	// provider. Asserted on the bytes that left, over a spread of categories and
	// locales wide enough that a mode silently doing nothing would fail here.
	for _, secret := range []string{
		"claire.moreau@example.fr",    // email
		"FR1420041010050500013M02606", // IBAN
		"184037511600176",             // French NIR
		"4532015112830366",            // payment card
		"12 rue de la Paix, 75002 Paris",
		"9434765919",  // NHS number
		"AB123456C",   // National Insurance
		"123-45-6789", // US social security
		"021000021",   // ABA routing number
		"sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789", // a credential
	} {
		if strings.Contains(upstream, secret) {
			t.Errorf("%q reached the provider unmasked", secret)
		}
	}

	// Half two: the caller gets its own data back. The prompt asks for the values
	// to be kept, so an answer that did the task carries them — which it can only
	// do if the response was rehydrated on the way out, delta by delta.
	var restored []string
	for _, value := range []string{
		"claire.moreau@example.fr", "FR1420041010050500013M02606", "184037511600176",
		"9434765919", "123-45-6789", "021000021",
	} {
		if strings.Contains(answer, value) {
			restored = append(restored, value)
		}
	}
	if len(restored) == 0 {
		t.Errorf("no original value came back in the answer, so nothing was rehydrated:\n%s",
			truncate(answer, 800))
	}

	// A bracket token surviving into the answer is a rehydration gap: the caller
	// would be reading the agent's bookkeeping instead of its own data. Checked on
	// the shape rather than against a list, so a category nobody thought of still
	// counts — and so a token split across two deltas and reassembled wrongly
	// shows up here.
	if m := tokenLeak.FindString(answer); m != "" {
		t.Errorf("%s is still in the answer: it was not expanded on the way out", m)
	}

	t.Logf("%d requests reached the provider; %d/6 sampled values came back restored: %s",
		len(sent), len(restored), strings.Join(restored, ", "))
	t.Logf("the page the session produced is at %s", page)
}

// tokenLeak matches the bracket tokens the agent mints, so the test can look for
// one anywhere in the answer rather than checking a list of categories.
var tokenLeak = regexp.MustCompile(`\[[A-Z][A-Z0-9_]*_\d+\]`)

// saveE2EPage writes the answer where a reader can open it, and reports the path.
// Markdown fences are stripped: the CLI usually wraps the page in them, and a
// browser shows them as text at the top of the document.
func saveE2EPage(t *testing.T, answer string) string {
	t.Helper()

	// Cleaned, which is the honest answer to "this path came from the
	// environment": whoever runs the test decides where its artefact goes, and
	// the process already runs as them.
	path := filepath.Clean(os.Getenv("CLOAKFLEET_E2E_OUTPUT"))
	if path == "." {
		path = filepath.Join("..", "..", "e2e-artefacts", "claude-session.html")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create the artefact directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(unfence(answer)), 0o600); err != nil {
		t.Fatalf("write the page: %v", err)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// unfence returns the contents of the first fenced block in s, or s unchanged
// when there is none.
func unfence(s string) string {
	open := strings.Index(s, "```")
	if open < 0 {
		return s
	}
	rest := s[open+3:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:] // drop the language tag on the opening fence
	}
	if end := strings.Index(rest, "```"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest) + "\n"
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "\n… truncated"
}

// upstreamTap forwards to a real provider and keeps every request body it passed
// on, so a test can assert on what actually left the machine.
type upstreamTap struct {
	server *httptest.Server

	mu   sync.Mutex
	seen []string
}

func newUpstreamTap(t *testing.T, target string) *upstreamTap {
	t.Helper()

	dest, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}

	tap := &upstreamTap{}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(dest)
			pr.Out.Host = dest.Host // the provider routes on it

			if pr.Out.Body != nil {
				body, err := io.ReadAll(pr.Out.Body)
				_ = pr.Out.Body.Close()
				if err == nil {
					tap.record(string(body))
					pr.Out.Body = io.NopCloser(bytes.NewReader(body))
					pr.Out.ContentLength = int64(len(body))
				}
			}
		},
	}

	tap.server = httptest.NewServer(rp)
	t.Cleanup(tap.server.Close)
	return tap
}

func (u *upstreamTap) record(body string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.seen = append(u.seen, body)
}

func (u *upstreamTap) bodies() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.seen...)
}

func (u *upstreamTap) URL() string { return u.server.URL }
