package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/vault"
)

// The page exists to answer two questions an operator cannot answer from a log
// line: which substitution mode to run, and whether the catalogue reads their
// data. So the tests are about what it actually shows — not that it returns 200.

func get(t *testing.T, agent *httptest.Server, path string) reply {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, agent.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return do(t, agent, req)
}

func submit(t *testing.T, agent *httptest.Server, text string) reply {
	t.Helper()

	form := url.Values{"text": {text}}.Encode()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, agent.URL+"/test", strings.NewReader(form))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return do(t, agent, req)
}

func TestTestPageOpensOnTheSample(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr", "gb", "us"})

	got := get(t, agent, "/test")
	if got.status != http.StatusOK {
		t.Fatalf("status %d, want 200", got.status)
	}

	// The sample it opens on comes from the detector, so it demonstrates the
	// locales this deployment actually loaded rather than a fixed text.
	for _, want := range []string{
		"184037511600176", // French NIR, from the fr sample
		"943 476 5919",    // NHS number, from the gb sample
		"123-45-6789",     // US social security, from the us sample
	} {
		if !strings.Contains(got.body, want) {
			t.Errorf("the page does not show %q from the sample", want)
		}
	}

	// The configuration is on the page, because a comparison built on defaults
	// would answer a different question than the one being asked.
	if !strings.Contains(got.body, "fr, gb, us") {
		t.Errorf("the page does not say which locales are in use:\n%s", excerpt(got.body))
	}
}

// The point of three columns: one text, both representations, side by side.
func TestTestPageShowsBothModes(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	got := submit(t, agent, "Écrire à claire@example.fr au sujet du NIR 184037511600176.")

	// The token column, with the values replaced by brackets.
	if !strings.Contains(got.body, "[EMAIL_1]") || !strings.Contains(got.body, "[NIR_1]") {
		t.Errorf("the token column does not show tokens:\n%s", excerpt(got.body))
	}

	// The stand-in column, with values that read as prose instead.
	if !strings.Contains(got.body, "@example.org") {
		t.Errorf("the stand-in column does not show a generated address:\n%s", excerpt(got.body))
	}

	// And the original is still in the form, so the operator can edit it.
	if !strings.Contains(got.body, "claire@example.fr") {
		t.Errorf("the submitted text is not echoed back into the form")
	}
}

// The page proves the claim rather than describing it: it unmasks its own token
// column and says whether the text came back exactly.
func TestTestPageReportsTheRoundTrip(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	t.Run("ordinary text round-trips", func(t *testing.T) {
		got := submit(t, agent, "Écrire à claire@example.fr, tel 06 12 34 56 78.")
		if !strings.Contains(got.body, "Round trip verified") {
			t.Errorf("the page does not report a verified round trip:\n%s", excerpt(got.body))
		}
	})

	t.Run("text already containing a token is reported honestly", func(t *testing.T) {
		// Masking then unmasking cannot return this exactly: the text already
		// carries something the expander will rewrite. Saying so is better than
		// a claim that only holds for well-behaved input.
		got := submit(t, agent, "Le rapport mentionne [EMAIL_1] et claire@example.fr.")
		if !strings.Contains(got.body, "Round trip incomplete") {
			t.Errorf("the page claims a round trip it cannot deliver:\n%s", excerpt(got.body))
		}
	})
}

// The findings table is what tells a missing replacement from an undetected
// value, and it has to name why a category keeps a token in stand-in mode.
func TestTestPageListsFindings(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	got := submit(t, agent, "Clé sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789 et mail claire@example.fr")

	for _, want := range []string{
		"SECRET_ANTHROPIC_KEY", "Anthropic API key", // the category and the shape that fired
		"EMAIL", "Email address",
		"token even in stand-in mode", // every credential is one, by design
	} {
		if !strings.Contains(got.body, want) {
			t.Errorf("the findings table does not carry %q:\n%s", want, excerpt(got.body))
		}
	}
}

func TestTestPageSaysWhenNothingIsDetected(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, nil)

	got := submit(t, agent, "Rien de sensible ici, juste une phrase.")
	if !strings.Contains(got.body, "Nothing detected") {
		t.Errorf("the page does not say the text came out clean:\n%s", excerpt(got.body))
	}
	// And it points at the likeliest reason, which is a locale that is not loaded.
	if !strings.Contains(got.body, "locale this agent has not loaded") {
		t.Errorf("the page does not explain why nothing was found")
	}
}

// The page echoes text somebody typed, which makes escaping the one thing it
// cannot get wrong.
func TestTestPageEscapesWhatItEchoes(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, nil)

	got := submit(t, agent, `<script>alert("x")</script> and mail claire@example.fr`)

	if strings.Contains(got.body, "<script>alert") {
		t.Errorf("submitted markup was echoed unescaped:\n%s", excerpt(got.body))
	}
	// Escaped, not dropped: the operator has to see what they pasted.
	if !strings.Contains(got.body, "&lt;script&gt;") {
		t.Errorf("submitted markup was not shown at all:\n%s", excerpt(got.body))
	}
}

// Nothing on the page reaches a provider, and nothing it masks is stored — it is
// a scratchpad, not an exchange.
func TestTestPageTouchesNothing(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	submit(t, agent, "Écrire à claire@example.fr")

	if bodies, _ := up.received(); len(bodies) != 0 {
		t.Errorf("the page sent something to a provider: %v", bodies)
	}

	// The session a real exchange would use must still know nothing, so the page
	// cannot make a later answer expand a token it never sent.
	got := post(t, agent, "/anthropic/v1/messages", "default", `{"c":"who is [EMAIL_1]?"}`)
	if strings.Contains(got.body, "claire@example.fr") {
		t.Errorf("the page wrote into the session vault: %s", got.body)
	}
}

// Reloading has to render the same thing. Sharing the live counters would number
// one text [EMAIL_1] on the first load and [EMAIL_7] on the next, so the
// comparison would be unreadable — and it would spend real indices on a page that
// stores nothing.
func TestTestPageIsStableAcrossReloads(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	const text = "Écrire à claire@example.fr et à paul@example.fr"

	first := submit(t, agent, text)
	second := submit(t, agent, text)
	if first.body != second.body {
		t.Error("two identical submissions rendered differently, so the comparison cannot be read")
	}
}

func TestTestPageRefusesBadRequests(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, nil)

	t.Run("a text over the cap", func(t *testing.T) {
		// Capped because the catalogue is dozens of expressions over the whole
		// input, and a page that accepts a POST should not accept a megabyte of
		// adversarial text.
		form := url.Values{"text": {strings.Repeat("a", playgroundMaxBytes+1)}}.Encode()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
			agent.URL+"/test", strings.NewReader(form))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		if got := do(t, agent, req); got.status != http.StatusRequestEntityTooLarge {
			t.Errorf("status %d, want 413", got.status)
		}
	})

	t.Run("an unparseable form", func(t *testing.T) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
			agent.URL+"/test", strings.NewReader("%zz"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		if got := do(t, agent, req); got.status != http.StatusBadRequest {
			t.Errorf("status %d, want 400", got.status)
		}
	})

	t.Run("a method that is neither", func(t *testing.T) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, agent.URL+"/test", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := do(t, agent, req); got.status != http.StatusMethodNotAllowed {
			t.Errorf("status %d, want 405", got.status)
		}
	})

	t.Run("an empty submission falls back to the sample", func(t *testing.T) {
		if got := submit(t, agent, "   "); !strings.Contains(got.body, "@example.fr") {
			t.Errorf("an empty submission did not fall back to the sample:\n%s", excerpt(got.body))
		}
	})
}

// A provider may not take a path the agent answers itself. Otherwise "/test"
// reaches the page while "/test/v1/messages" reaches the provider, which is a
// routing table nobody could reason about.
func TestReservedRoutesCannotBeProviders(t *testing.T) {
	det := detector.New(detector.Config{})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}

	for _, code := range reservedRoutes {
		t.Run(code, func(t *testing.T) {
			_, err := New(Config{Providers: []Provider{
				{Code: code, BaseURL: "https://example.invalid"},
			}}, det, v)
			if err == nil {
				t.Fatalf("a provider named %q was accepted", code)
			}
			if !strings.Contains(err.Error(), code) {
				t.Errorf("the error does not name the offending code: %v", err)
			}
		})
	}
}

// excerpt keeps a failure message readable: the page is several kilobytes and
// dumping all of it hides the assertion that failed.
func excerpt(page string) string {
	const limit = 600
	if len(page) <= limit {
		return page
	}
	return page[:limit] + "\n… truncated"
}
