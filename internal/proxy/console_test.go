package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// The console is inlined into /settings as data, so a closing tag inside it would end
// its element early and hand the rest of the bundle to the HTML parser — markup the
// page never meant to have, in the one response that carries the control key. An
// HTML comment opener is the other way a script's content changes how it is parsed.
func TestTheConsoleCannotCloseItsOwnElement(t *testing.T) {
	script := strings.ToLower(consoleScript)
	if strings.Contains(script, "</script") {
		t.Error("console.js carries a closing script tag")
	}
	if strings.Contains(script, "<!--") {
		t.Error("console.js carries an HTML comment opener")
	}
	if strings.Contains(strings.ToLower(consoleStyle), "</style") {
		t.Error("console.css carries a closing style tag")
	}
}

// html/template escapes by context, and a script it did not recognise as one would
// have its JavaScript escaped as text: a page that renders, says nothing, and fails
// in the browser alone. The bundle has to arrive byte for byte.
func TestTheSettingsPageCarriesTheConsoleVerbatim(t *testing.T) {
	srv := settingsAgent(t, testControlKey)

	got := ask(t, srv, "127.0.0.1:52341", "127.0.0.1:9787")

	if got.Code != http.StatusOK {
		t.Fatalf("GET /settings = %d, want 200", got.Code)
	}
	body := got.Body.String()
	if consoleScript == "" || !strings.Contains(body, consoleScript) {
		t.Error("the console's script is not in the page as it was built")
	}
	if consoleStyle == "" || !strings.Contains(body, consoleStyle) {
		t.Error("the console's style is not in the page as it was built")
	}
	if !strings.Contains(body, `<div id="root"></div>`) {
		t.Error("the page has no element for the console to draw into")
	}
}

// postTestJSON asks /test the way the console's Try it screen does.
func postTestJSON(t *testing.T, agent *httptest.Server, body string) reply {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, agent.URL+"/test", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return do(t, agent, req)
}

func TestTestAnswersJSONInBothModes(t *testing.T) {
	agent := newAgent(t, newUpstream(t, echoJSON), []string{"fr"})

	got := postTestJSON(t, agent, `{"text":"Écrire à claire@example.fr, merci."}`)

	if got.status != http.StatusOK {
		t.Fatalf("POST /test = %d, want 200: %s", got.status, got.body)
	}
	var r testReply
	if err := json.Unmarshal([]byte(got.body), &r); err != nil {
		t.Fatalf("not the console's JSON: %v\n%s", err, got.body)
	}

	if !strings.Contains(r.Token.Text, "[EMAIL_1]") || r.Token.Count != 1 {
		t.Errorf("token column = %q (%d replaced), want [EMAIL_1] once", r.Token.Text, r.Token.Count)
	}
	if !slices.Contains(r.Token.Masks, testMask{Mask: "[EMAIL_1]"}) {
		t.Errorf("token masks = %v, want [EMAIL_1] to highlight", r.Token.Masks)
	}
	if strings.Contains(r.Fake.Text, "claire@example.fr") || r.Fake.Count != 1 || len(r.Fake.Masks) != 1 {
		t.Errorf("stand-in column = %q (%d replaced, masks %v), want the address replaced once", r.Fake.Text, r.Fake.Count, r.Fake.Masks)
	}
	// The page highlights by finding each mask in the text; a mask it cannot find is
	// a replacement drawn as if it were the writer's own words.
	for _, m := range r.Fake.Masks {
		if !strings.Contains(r.Fake.Text, m.Mask) {
			t.Errorf("stand-in mask %q is not in the text it was minted for", m.Mask)
		}
	}
	if !r.RoundTrips {
		t.Error("the token column does not round-trip")
	}
	if len(r.Findings) != 1 || r.Findings[0].Code != "EMAIL" || r.Findings[0].Value != "claire@example.fr" || r.Findings[0].Label == "" {
		t.Errorf("findings = %+v, want the one address, labelled", r.Findings)
	}
}

// A credential keeps a token in both modes, and the page draws it apart.
func TestTestJSONMarksACredentialInBothModes(t *testing.T) {
	agent := newAgent(t, newUpstream(t, echoJSON), []string{"fr"})
	key := "sk-ant-api03-" + strings.Repeat("Ab1", 15)

	got := postTestJSON(t, agent, `{"text":"export KEY=`+key+`"}`)

	var r testReply
	if err := json.Unmarshal([]byte(got.body), &r); err != nil {
		t.Fatalf("not the console's JSON: %v\n%s", err, got.body)
	}
	for name, col := range map[string]testColumn{"token": r.Token, "stand-in": r.Fake} {
		if len(col.Masks) != 1 || !col.Masks[0].Credential || !strings.HasPrefix(col.Masks[0].Mask, "[") {
			t.Errorf("%s masks = %+v, want one bracket token marked as a credential", name, col.Masks)
		}
	}
	if len(r.Findings) != 1 || !r.Findings[0].Credential || !r.Findings[0].TokenInFakeMode {
		t.Errorf("findings = %+v, want one credential that keeps its token", r.Findings)
	}
}

// The JSON mode reads the same text the form page does, under the same cap.
func TestTestJSONRefusesWhatTheFormWould(t *testing.T) {
	agent := newAgent(t, newUpstream(t, echoJSON), []string{"fr"})

	tests := []struct {
		name string
		body string
		want int
	}{
		{"not JSON", `text=hello`, http.StatusBadRequest},
		{"a text over the cap", `{"text":"` + strings.Repeat("a", playgroundMaxBytes+1) + `"}`, http.StatusRequestEntityTooLarge},
		{"a body over the cap", `{"text":"` + strings.Repeat(`\n`, testJSONMaxBytes) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := postTestJSON(t, agent, tt.body); got.status != tt.want {
				t.Errorf("POST /test = %d, want %d: %s", got.status, tt.want, got.body)
			}
		})
	}
}

// The settings page draws its connection lines from this, so it has to be the table
// `neverseen env` prints from, entry for entry, caveat included.
func TestHealthServesTheShellTools(t *testing.T) {
	srv := settingsAgent(t, testControlKey)

	tools := srv.healthNow().Tools

	codes := make([]string, 0, len(tools))
	for _, tool := range tools {
		codes = append(codes, tool.Provider)
		want := shellTools[tool.Provider]
		if tool.Variable != want.Variable || tool.CLI != want.CLI || tool.Caveat != want.Caveat {
			t.Errorf("tools[%s] = %+v, want %+v", tool.Provider, tool, want)
		}
	}
	if !slices.Equal(codes, ToolCodes()) {
		t.Errorf("tools = %v, want every shellTools entry in ToolCodes order %v", codes, ToolCodes())
	}
}
