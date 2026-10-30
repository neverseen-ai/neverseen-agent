package proxy

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/internal/vault"
)

// What the agent lets through without reading it, and what comes back that it
// cannot put back, are both said in the log. Neither changes what the agent does:
// a picture travels as it did, and a placeholder nothing stands for still reaches
// the caller — but a plate read off a screenshot came back as "[PLATE_553]", went
// into a shell command as written, and nothing anywhere said so.

// newLoggedAgent is newAgent with its log kept.
func newLoggedAgent(t *testing.T, up *upstream) (*httptest.Server, *safeBuffer) {
	t.Helper()

	logged := &safeBuffer{}
	return newAgentWith(t, up, []string{"fr"}, vault.NewMemory(), slog.New(slog.NewTextHandler(logged, nil))), logged
}

// The body carries "[PLATE_553]" as text, so the agent hands that name to nothing,
// and the echo brings it back beside a name it did hand out.
func TestABufferedAnswerNamingNothingIsLogged(t *testing.T) {
	agent, logged := newLoggedAgent(t, newUpstream(t, echoJSON))

	got := post(t, agent, "/anthropic/v1/messages", "plates",
		`{"prompt":"write to pierre.paul@example.fr about [PLATE_553]"}`)
	if !strings.Contains(got.body, "pierre.paul@example.fr") || !strings.Contains(got.body, "[PLATE_553]") {
		t.Fatalf("the echo did not carry both names back: %s", got.body)
	}

	assertUnknownLogged(t, logged.String())
}

// Split across two events, as generated text is: reported once, and whole.
func TestAStreamedAnswerNamingNothingIsLogged(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, piece := range []string{"Write to [EMAIL_1] about [PLA", "TE_553] today"} {
			fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", anthropicDelta(piece))
		}
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	})
	agent, logged := newLoggedAgent(t, up)

	got := post(t, agent, "/anthropic/v1/messages", "plates", `{"prompt":"write to pierre.paul@example.fr"}`)
	if text := concatenatedText(t, got.body); text != "Write to pierre.paul@example.fr about [PLATE_553] today" {
		t.Fatalf("the caller read %q", text)
	}

	assertUnknownLogged(t, logged.String())
}

func assertUnknownLogged(t *testing.T, log string) {
	t.Helper()
	if n := strings.Count(log, "[PLATE_553]"); n != 1 {
		t.Errorf("[PLATE_553] logged %d times, want once:\n%s", n, log)
	}
	if strings.Contains(log, "[EMAIL_1]") {
		t.Errorf("a name the agent restored was reported as unknown:\n%s", log)
	}
	if strings.Contains(log, "pierre.paul@example.fr") {
		t.Errorf("the log carried a value in clear:\n%s", log)
	}
}

// Both shapes a picture arrives in: Anthropic's base64 block, and the data URI of
// the OpenAI-compatible families.
func TestARequestCarryingPicturesIsLogged(t *testing.T) {
	agent, logged := newLoggedAgent(t, newUpstream(t, echoJSON))

	post(t, agent, "/anthropic/v1/messages", "pictures", `{"messages":[{"role":"user","content":[`+
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}},`+
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}},`+
		`{"type":"text","text":"what plate is this?"}]}]}`)

	if !strings.Contains(logged.String(), "unread=2") {
		t.Errorf("the log does not count two pictures:\n%s", logged.String())
	}
}

func TestARequestWithoutPicturesSaysNothingAboutThem(t *testing.T) {
	agent, logged := newLoggedAgent(t, newUpstream(t, echoJSON))

	post(t, agent, "/anthropic/v1/messages", "text", `{"prompt":"write to pierre.paul@example.fr"}`)

	if strings.Contains(logged.String(), "unread=") {
		t.Errorf("a request with no picture was logged as carrying one:\n%s", logged.String())
	}
}

// The model writes a name down when it quotes one it was given as readily as when it
// invents one, and a conversation says it again every turn: said once per session,
// and again for another session.
func TestAnUnknownNameIsLoggedOncePerSession(t *testing.T) {
	agent, logged := newLoggedAgent(t, newUpstream(t, echoJSON))
	body := `{"prompt":"write to pierre.paul@example.fr about [PLATE_553]"}`

	post(t, agent, "/anthropic/v1/messages", "first", body)
	post(t, agent, "/anthropic/v1/messages", "first", body)
	if n := strings.Count(logged.String(), "[PLATE_553]"); n != 1 {
		t.Errorf("[PLATE_553] logged %d times over two turns of one session, want once:\n%s", n, logged.String())
	}

	post(t, agent, "/anthropic/v1/messages", "second", body)
	if n := strings.Count(logged.String(), "[PLATE_553]"); n != 2 {
		t.Errorf("[PLATE_553] logged %d times over two sessions, want twice:\n%s", n, logged.String())
	}
}

// The same pictures resent on the next turn are not news; another one is.
func TestPicturesAreLoggedWhenTheirCountChanges(t *testing.T) {
	agent, logged := newLoggedAgent(t, newUpstream(t, echoJSON))
	picture := `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}`
	body := func(n int) string {
		return `{"messages":[{"role":"user","content":[` + strings.Repeat(picture+",", n) + `{"type":"text","text":"x"}]}]}`
	}

	post(t, agent, "/anthropic/v1/messages", "pictures", body(1))
	post(t, agent, "/anthropic/v1/messages", "pictures", body(1))
	post(t, agent, "/anthropic/v1/messages", "pictures", body(2))

	if n := strings.Count(logged.String(), "unread="); n != 2 {
		t.Errorf("logged %d times over 1, 1 and 2 pictures, want twice:\n%s", n, logged.String())
	}
}

// A tool runs the name as written, so it is the case that matters. The arguments are
// accumulated and expanded whole: one report, however they were split.
func TestAToolCallNamingNothingIsLogged(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		token := mintedToken(t, string(body))

		w.Header().Set("Content-Type", "text/event-stream")
		arguments := `{"cmd":"echo ` + token + ` [PLATE_553]"}`
		cut := strings.Index(arguments, "PLATE") + 3
		for _, e := range []string{
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"Bash"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + jsonString(arguments[:cut]) + `}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + jsonString(arguments[cut:]) + `}}`,
			`{"type":"content_block_stop","index":0}`,
		} {
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", e)
		}
	})
	agent, logged := newLoggedAgent(t, up)

	got := post(t, agent, "/anthropic/v1/messages", "tool", `{"prompt":"write to pierre.paul@example.fr"}`)
	if !strings.Contains(got.body, `echo pierre.paul@example.fr [PLATE_553]`) {
		t.Fatalf("the caller did not receive the arguments whole: %s", got.body)
	}

	if n := strings.Count(logged.String(), "[PLATE_553]"); n != 1 {
		t.Errorf("[PLATE_553] logged %d times, want once:\n%s", n, logged.String())
	}
}
