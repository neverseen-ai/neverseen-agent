package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
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
	if cc := got.header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q, want no-store: a cached page shows a configuration the agent no longer has", cc)
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
	// would answer a different question than the one being asked — and it is drawn
	// as the agent has it, ticked, so an untouched page still says what the agent
	// does.
	for _, code := range []string{"fr", "gb", "us"} {
		if !strings.Contains(got.body, `name="locale" value="`+code+`" checked`) {
			t.Errorf("locale %s is not drawn as loaded:\n%s", code, excerpt(got.body))
		}
	}
	if !strings.Contains(got.body, `name="secret_level" value="weak" checked`) {
		t.Errorf("the secret level is not drawn as the agent's:\n%s", excerpt(got.body))
	}
	if strings.Contains(got.body, "Simulated configuration") {
		t.Errorf("an untouched page must not say it is simulating:\n%s", excerpt(got.body))
	}

	// A row of switches carries its all/none toggle; a row of credentials, which
	// has no switches, must not offer one.
	if n := strings.Count(got.body, `data-set="1"`); n == 0 || n >= len(strings.Split(got.body, `class="row"`))-1 {
		t.Errorf("want a toggle on the switchable rows only, got %d:\n%s", n, excerpt(got.body))
	}
}

// The switches simulate; they never write. The page is unauthenticated and, under
// -l, reachable from the network, so a switch that wrote through would be the
// control PUT /policy exists to guard.
func TestTestPageSimulatesWithoutChangingTheAgent(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	const text = "Contact: alice" + "@" + "example.org, SSN 123-45-6789"

	// Email switched off, us loaded on top of fr, strong level.
	form := url.Values{
		"text":         {text},
		"locale":       {"fr", "us"},
		"secret_level": {"strong"},
		"shown":        {"EMAIL,PHONE"},
		"on":           {"PHONE"},
	}
	got := submitForm(t, agent, form)
	if got.status != http.StatusOK {
		t.Fatalf("status %d, want 200", got.status)
	}
	// The value itself is on the page whatever happens — in the "as written"
	// column and in the findings — so the token is what says which way it went.
	if strings.Contains(got.body, "[EMAIL_") {
		t.Errorf("EMAIL was unticked and still masked:\n%s", excerpt(got.body))
	}
	if !strings.Contains(got.body, "[SSN_") {
		t.Errorf("us was ticked and the SSN was not masked:\n%s", excerpt(got.body))
	}
	if !strings.Contains(got.body, "Simulated configuration") {
		t.Errorf("a page rendered off the agent's configuration must say so:\n%s", excerpt(got.body))
	}
	if !strings.Contains(got.body, `name="secret_level" value="strong" checked`) {
		t.Errorf("the page does not redraw the level it rendered with:\n%s", excerpt(got.body))
	}

	// The agent beside it has not moved: /healthz is the one answer to that.
	var h Health
	if err := json.Unmarshal([]byte(get(t, agent, "/healthz").body), &h); err != nil {
		t.Fatal(err)
	}
	if len(h.Locales) != 1 || h.Locales[0] != "fr" {
		t.Errorf("the agent's locales moved to %v", h.Locales)
	}
	if len(h.Off) != 0 {
		t.Errorf("the agent's off set moved to %v", h.Off)
	}
	if h.SecretLevel != "weak" {
		t.Errorf("the agent's level moved to %s", h.SecretLevel)
	}

	// A text posted on its own — the form as it was — still renders the agent's.
	plain := submit(t, agent, text)
	if strings.Contains(plain.body, "Simulated configuration") {
		t.Errorf("a submission carrying no configuration must render the agent's:\n%s", excerpt(plain.body))
	}
}

// Refused with the route's own reasons: the page cannot show a state the agent
// could never be in.
func TestTestPageRefusesAConfigurationTheAgentWould(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	for name, form := range map[string]url.Values{
		"unknown locale": {"secret_level": {"weak"}, "locale": {"uk"}},
		"unknown level":  {"secret_level": {"paranoid"}},
		"credential off": {"secret_level": {"weak"}, "shown": {"SECRET_ANTHROPIC_KEY"}},
	} {
		if got := submitForm(t, agent, form); got.status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, want 422", name, got.status)
		}
	}
}

func submitForm(t *testing.T, agent *httptest.Server, form url.Values) reply {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, agent.URL+"/test", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return do(t, agent, req)
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

	// The address is assembled rather than written, and so is the token expected
	// below: this repository is read through the agent it builds, and a literal of
	// either shape in a test is rewritten on the way in.
	address := "marie.durand" + "@" + "acme-corp.fr"
	got := submit(t, agent, `<script>alert("x")</script> and mail `+address)

	if strings.Contains(got.body, "<script>alert") {
		t.Errorf("submitted markup was echoed unescaped:\n%s", excerpt(got.body))
	}
	// Escaped, not dropped: the operator has to see what they pasted.
	if !strings.Contains(got.body, "&lt;script&gt;") {
		t.Errorf("submitted markup was not shown at all:\n%s", excerpt(got.body))
	}

	// The token column is the one piece of HTML built by hand, so the escaping is
	// asserted on it in particular: the pasted markup must arrive as entities there
	// too, and only what the detector wrote may be marked.
	token := "[" + "EMAIL_1]"
	if !strings.Contains(got.body, "<mark>"+token+"</mark>") {
		t.Errorf("the token is not highlighted in the token column:\n%s", excerpt(got.body))
	}
	if n := strings.Count(got.body, "<mark>"); n != 1 {
		t.Errorf("want exactly one highlighted token, got %d:\n%s", n, excerpt(got.body))
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

// TestTestPageResubmittedUnchangedIsNotSimulated is the other half of the banner.
//
// It was decided by `det != s.det`, a pointer comparison, and simulated() returns a
// fresh detector whenever the form carries secret_level — which the rendered form
// always does. So "Run it again" on an untouched page raised "this is not what the
// agent is doing" over a rendering that was exactly what the agent is doing, and a
// warning that is always on is one nobody reads on the visit where it is true.
func TestTestPageResubmittedUnchangedIsNotSimulated(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr", "us"})

	// The form as the page drew it: the agent's locales and level, every switch it
	// showed left ticked. This is the submission a person makes by pressing the
	// button without touching anything.
	page := get(t, agent, "/test")
	shown := hiddenShown(t, page.body)
	form := url.Values{
		"text":         {"nothing in particular"},
		"locale":       {"fr", "us"},
		"secret_level": {"weak"},
		"shown":        {shown},
		"on":           strings.Split(shown, ","),
	}

	got := submitForm(t, agent, form)
	if got.status != http.StatusOK {
		t.Fatalf("status %d, want 200", got.status)
	}
	if strings.Contains(got.body, "Simulated configuration") {
		t.Errorf("a submission reproducing the agent's own configuration must not raise the banner:\n%s",
			excerpt(got.body))
	}

	// And one switch off still does, or the fix above would have removed the banner
	// rather than aimed it.
	first, _, _ := strings.Cut(shown, ",")
	form["on"] = slices.DeleteFunc(strings.Split(shown, ","), func(c string) bool { return c == first })
	if off := submitForm(t, agent, form); !strings.Contains(off.body, "Simulated configuration") {
		t.Errorf("%s switched off and the page does not say it is simulating:\n%s", first, excerpt(off.body))
	}
}

// TestTestPageSampleFollowsTheSimulatedLocales is the failure that reads as the
// opposite of the truth.
//
// pii.Sample is locale-dependent, and the default text was read from the agent's
// detector before the simulation was applied. So clearing the box and ticking `us` on
// an agent configured for `fr` rendered the *French* sample under a US configuration:
// every US column comes back empty, which reads as "loading `us` masks nothing".
func TestTestPageSampleFollowsTheSimulatedLocales(t *testing.T) {
	up := newUpstream(t, echoJSON)
	agent := newAgent(t, up, []string{"fr"})

	got := submitForm(t, agent, url.Values{
		"text":         {"   "}, // cleared, so the page falls back to its sample
		"locale":       {"us"},
		"secret_level": {"weak"},
	})
	if got.status != http.StatusOK {
		t.Fatalf("status %d, want 200", got.status)
	}
	if !strings.Contains(got.body, "123-45-6789") {
		t.Errorf("the sample is not the one `us` would be judged on:\n%s", excerpt(got.body))
	}
	if strings.Contains(got.body, "184037511600176") {
		t.Errorf("the page shows the French sample under a US configuration:\n%s", excerpt(got.body))
	}
	if !strings.Contains(got.body, "[SSN_") {
		t.Errorf("the US sample rendered under `us` and nothing was masked:\n%s", excerpt(got.body))
	}
}

// hiddenShown reads the codes the page drew switches for, which is what the next
// submission needs in order to tell an unticked switch from one it never drew.
func hiddenShown(t *testing.T, body string) string {
	t.Helper()

	const open = `name="shown" value="`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatalf("the page carries no shown field:\n%s", excerpt(body))
	}
	rest := body[i+len(open):]
	end := strings.IndexByte(rest, '"')
	if end <= 0 {
		t.Fatalf("the shown field is empty:\n%s", excerpt(body))
	}
	return rest[:end]
}
