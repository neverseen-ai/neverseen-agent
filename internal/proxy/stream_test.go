package proxy

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// The failure these tests exist for cannot be reached by sending whole tokens:
// generated text arrives in pieces of a few characters, so a token the model
// echoed is regularly split across two events. A rehydrator that only ever sees
// one event at a time forwards "[EMA" and then "IL_1]" to the caller, and every
// test built on whole events passes while it does.

// rehydrate runs a stream through the rehydrator and returns what a caller would
// read.
func rehydrate(t *testing.T, stream string, known map[string]string) string {
	t.Helper()

	out, err := io.ReadAll(newStreamRehydrator(io.NopCloser(strings.NewReader(stream)), known, nil, nil))
	if err != nil {
		t.Fatalf("read the rehydrated stream: %v", err)
	}
	return string(out)
}

func TestStreamRehydratorReassemblesASplitToken(t *testing.T) {
	known := map[string]string{"[EMAIL_1]": "claire@example.fr"}

	tests := []struct {
		name   string
		pieces []string
	}{
		{
			name:   "whole in one delta",
			pieces: []string{"Write to [EMAIL_1] today"},
		},
		{
			// The realistic case, and the one a line-at-a-time expander cannot
			// do anything about.
			name:   "split down the middle",
			pieces: []string{"Write to [EMA", "IL_1] today"},
		},
		{
			name:   "split at the opening bracket",
			pieces: []string{"Write to ", "[EMAIL_1] today"},
		},
		{
			name:   "split before the closing bracket",
			pieces: []string{"Write to [EMAIL_1", "] today"},
		},
		{
			name:   "one character at a time",
			pieces: chars("Write to [EMAIL_1] today"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stream strings.Builder
			for _, piece := range tt.pieces {
				fmt.Fprintf(&stream, "event: content_block_delta\ndata: %s\n\n", anthropicDelta(piece))
			}
			stream.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")

			got := rehydrate(t, stream.String(), known)

			// The caller concatenates the deltas, so that concatenation is what
			// has to carry the original — not any single event.
			if text := concatenatedText(t, got); text != "Write to claire@example.fr today" {
				t.Errorf("the caller would read %q, want %q", text, "Write to claire@example.fr today")
			}
			if strings.Contains(got, "[EMAIL_") {
				t.Errorf("a token fragment survived:\n%s", got)
			}
		})
	}
}

// The OpenAI-compatible shape, which the seven providers that are not Anthropic
// speak. A rehydrator that only knew one shape would silently do nothing for
// most of the catalogue of providers.
func TestStreamRehydratorHandlesTheOpenAIShape(t *testing.T) {
	known := map[string]string{"[NIR_1]": "184037511600176"}

	stream := "data: " + openAIDelta("Le NIR est [NIR") + "\n\n" +
		"data: " + openAIDelta("_1] au dossier") + "\n\n" +
		"data: [DONE]\n\n"

	got := rehydrate(t, stream, known)
	if !strings.Contains(got, "184037511600176") {
		t.Errorf("the value was not restored:\n%s", got)
	}
	if strings.Contains(got, "[NIR") {
		t.Errorf("a token fragment survived:\n%s", got)
	}
	// The terminator is not JSON and must reach the client untouched, or the
	// client waits forever for an end that never comes.
	if !strings.Contains(got, "data: [DONE]") {
		t.Errorf("the [DONE] sentinel was lost:\n%s", got)
	}
}

// A tail held back from the last piece of text has to go out before "[DONE]".
//
// The OpenAI family has no stop event and its deltas carry no block index, so
// nothing closed the block before the end of the stream: the tail was flushed
// after the sentinel, where every SDK has already stopped reading, and the
// characters the caller wrote were delivered to nobody.
func TestAnOpenAITailIsReleasedBeforeTheDoneSentinel(t *testing.T) {
	known := map[string]string{"[NIR_1]": "184037511600176"}

	stream := "data: " + openAIDelta("dossier [NIR") + "\n\n" +
		"data: " + `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"

	got := rehydrate(t, stream, known)

	done := strings.Index(got, "data: [DONE]")
	if done < 0 {
		t.Fatalf("the [DONE] sentinel was lost:\n%s", got)
	}
	if text := concatenatedText(t, got[:done]); text != "dossier [NIR" {
		t.Errorf("before [DONE] the caller would read %q, want the held tail back verbatim:\n%s", text, got)
	}
	if strings.Contains(got[done:], "[NIR") {
		t.Errorf("the tail was delivered a second time, after [DONE]:\n%s", got)
	}
}

// A tail held back when the stream ends still has to reach the caller. It travels
// in a copy of the last delta event, because a bare fragment is not a valid event
// and a client would drop it.
func TestStreamRehydratorFlushesADanglingTail(t *testing.T) {
	stream := "data: " + anthropicDelta("the value is [EMA") + "\n\n"

	got := rehydrate(t, stream, map[string]string{"[EMAIL_1]": "claire@example.fr"})
	if text := concatenatedText(t, got); text != "the value is [EMA" {
		t.Errorf("the caller would read %q, want the unfinished text back verbatim", text)
	}
}

// Events that are not generated text — a start, a stop, a usage report — must
// pass through with their numbers intact. Re-encoding a JSON number through a
// float is how a token count reaches a caller as "1e+06".
func TestStreamRehydratorDoesNotRewriteNumbers(t *testing.T) {
	const line = `data: {"type":"message_delta","usage":{"input_tokens":1000000,"output_tokens":42}}`

	got := rehydrate(t, line+"\n\n", map[string]string{"[EMAIL_1]": "claire@example.fr"})
	for _, want := range []string{"1000000", "42"} {
		if !strings.Contains(got, want) {
			t.Errorf("the number %s did not survive:\n%s", want, got)
		}
	}
}

// A line that is not a data line carries the stream's structure. Reordering or
// rewriting one breaks the protocol rather than the text.
func TestStreamRehydratorLeavesStructureAlone(t *testing.T) {
	const stream = "event: message_start\n" +
		": a comment\n" +
		"\n" +
		"data: {\"type\":\"message_start\"}\n\n"

	got := rehydrate(t, stream, map[string]string{"[EMAIL_1]": "claire@example.fr"})
	for _, want := range []string{"event: message_start", ": a comment"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q was lost:\n%s", want, got)
		}
	}
}

// The whole path, through the proxy: a provider that streams a split token, and a
// caller that must read its own value.
func TestStreamingRoundTripThroughTheProxy(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		// Echo back whatever token the provider was given, split down the
		// middle — which is what a real provider's deltas do to it.
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
	agent := newAgent(t, up, nil)

	reply := post(t, agent, "/anthropic/v1/messages", "s1", `{"c":"write to claire@example.fr"}`)
	if ct := reply.header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content type is %q, want an event stream", ct)
	}
	got := reply.body

	if text := concatenatedText(t, got); !strings.Contains(text, "claire@example.fr") {
		t.Errorf("the caller read %q, without its own value", text)
	}
	if strings.Contains(got, "[EMAIL_") {
		t.Errorf("a token reached the caller:\n%s", got)
	}
	// And the provider never had the value in the first place.
	if bodies, _ := up.received(); strings.Contains(strings.Join(bodies, ""), "claire@example.fr") {
		t.Errorf("the value reached the provider: %v", bodies)
	}
}

// A gateway that declares a Content-Length on an event stream declares the length
// of the body it sent, not of the one the agent rewrites. Forwarded, the restored
// body was cut at the provider's length — or, shorter, left the client waiting for
// bytes that never came.
func TestAStreamDeclaringAContentLengthIsDeliveredWhole(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		body := "event: content_block_delta\ndata: " + anthropicDelta("mail [EMAIL_1] now") + "\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = io.WriteString(w, body)
	})
	agent := newAgent(t, up, []string{"fr"})

	// An original longer than its token, so the rewritten body outgrows the length.
	reply := post(t, agent, "/anthropic/v1/messages", "s1", `{"c":"mail claire-la-plus-longue@example.fr"}`)

	if text := concatenatedText(t, reply.body); text != "mail claire-la-plus-longue@example.fr now" {
		t.Errorf("the caller read %q, want the whole restored text", text)
	}
	if !strings.Contains(reply.body, "message_stop") {
		t.Errorf("the stream was cut before its end:\n%s", reply.body)
	}
}

func TestDeltaText(t *testing.T) {
	tests := []struct {
		name  string
		event string
		want  string
		found bool
	}{
		{name: "anthropic", event: anthropicDelta("hello"), want: "hello", found: true},
		{name: "openai", event: openAIDelta("hello"), want: "hello", found: true},
		{name: "no text at all", event: `{"type":"message_stop"}`, found: false},
		{name: "empty choices", event: `{"choices":[]}`, found: false},
		{name: "a choice without a delta", event: `{"choices":[{"finish_reason":"stop"}]}`, found: false},
		{name: "a delta without content", event: `{"choices":[{"delta":{"role":"assistant"}}]}`, found: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := decodeEvent(tt.event)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}

			got, set, found := deltaText(event)
			if found != tt.found {
				t.Fatalf("found = %v, want %v", found, tt.found)
			}
			if !found {
				return
			}
			if got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}

			// The setter has to write where the getter read, or a rewritten
			// event carries the old text.
			set("rewritten")
			if again, _, _ := deltaText(event); again != "rewritten" {
				t.Errorf("after set, text = %q, want %q", again, "rewritten")
			}
		})
	}
}

func anthropicDelta(text string) string {
	return fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text)
}

func openAIDelta(text string) string {
	return fmt.Sprintf(`{"choices":[{"index":0,"delta":{"content":%q}}]}`, text)
}

// concatenatedText joins the generated text of every delta in a stream, which is
// what a client actually shows its user. Individual events are an implementation
// detail — the rehydrator is allowed to move characters between them.
func concatenatedText(t *testing.T, stream string) string {
	t.Helper()

	var b strings.Builder
	for line := range strings.Lines(stream) {
		payload, ok := eventPayload(line)
		if !ok {
			continue
		}
		event, err := decodeEvent(payload)
		if err != nil {
			continue
		}
		if text, _, found := deltaText(event); found {
			b.WriteString(text)
		}
	}
	return b.String()
}

// chars splits a string into single-character pieces, to stream text as slowly as
// a provider ever would.
func chars(s string) []string {
	out := make([]string, 0, len(s))
	for _, r := range s {
		out = append(out, string(r))
	}
	return out
}

// An answer to a session that minted nothing reaches the caller exactly as the
// provider sent it, streamed or buffered.
//
// The rewrite is not byte-preserving — a lone surrogate comes out as U+FFFD, a
// pretty-printed body is compacted, a text ending on `[` is held back for a token
// that cannot arrive, and a tool call's fragments are released as one document — and
// with nothing to put back there is no reason to pay any of that. The counts and the
// tool calls are still read (TestAuditPrintsAToolCallWhenNothingWasMasked), which is
// why this is not the early return that once skipped both.
func TestAnAnswerToAnEmptySessionIsForwardedVerbatim(t *testing.T) {
	stream := "event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"Bash"}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"cmd\":\"ls ["}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a \ud83d ["}}` + "\n\n" +
		": keepalive\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n"
	if got := rehydrate(t, stream, nil); got != stream {
		t.Errorf("a stream for an empty session was rewritten:\n got: %q\nwant: %q", got, stream)
	}

	body := "{\n  \"content\": [{\"type\": \"text\", \"text\": \"a \\ud83d [\"}]\n}"
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
	agent := newAgent(t, up, []string{"fr"})
	if got := post(t, agent, "/anthropic/v1/messages", "empty", `{"prompt":"list the files"}`); got.body != body {
		t.Errorf("a buffered answer for an empty session was rewritten:\n got: %q\nwant: %q", got.body, body)
	}
}
