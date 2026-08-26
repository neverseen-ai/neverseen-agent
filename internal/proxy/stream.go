package proxy

import (
	"bufio"
	"bytes"
	"io"
	"strings"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/pkg/telemetry"
)

// Rehydrating a stream is not rehydrating a document one line at a time.
//
// Generated text arrives in pieces of a few characters, so a token the model
// echoed is regularly split across two of them: the caller would read "[EMA"
// from one event and "IL_1]" from the next, and an expander that only ever sees
// one event at a time can restore neither. Buffering the whole stream instead
// would fix it and defeat the point — the caller is a tool showing text as it
// arrives.
//
// So the tail of each piece is held back when it could be the start of a token,
// and prepended to the next one. What is emitted is always a whole event, just
// occasionally a few characters shorter than what arrived, with those characters
// moving to the event after it. A client concatenating deltas — which is what
// every one of them does — cannot tell the difference.

// streamRehydrator wraps an event-stream body and expands tokens as it passes.
type streamRehydrator struct {
	src    *bufio.Reader
	closer io.Closer
	known  map[string]string

	// onUsage is called once, when the stream ends, with whatever the events
	// accounted for.
	//
	// Once at the end rather than per event, because a stream reports its cost in
	// pieces: Anthropic names the model in the first event and the output count
	// in the last, so anything reported earlier would be a fraction of the truth
	// filed under an empty model name.
	onUsage     func(string, telemetry.TokenUsage)
	usageModel  string
	usageTotals telemetry.TokenUsage
	usageSent   bool

	// seen reports each replacement put back, for the audit console. Nil
	// otherwise, which is the ordinary case.
	seen func(replacement, original string)

	out  bytes.Buffer
	done bool

	// pending is the held-back tail of the previous piece of text: characters
	// that could be the beginning of a token whose end has not arrived yet.
	pending string

	// template is the last delta event seen, kept so a pending tail still has an
	// event to travel in when the stream ends before the token completes.
	template []byte
}

func newStreamRehydrator(body io.ReadCloser, known map[string]string,
	onUsage func(string, telemetry.TokenUsage),
	seen func(replacement, original string)) io.ReadCloser {
	return &streamRehydrator{
		src:     bufio.NewReader(body),
		closer:  body,
		known:   known,
		onUsage: onUsage,
		seen:    seen,
	}
}

func (r *streamRehydrator) Read(p []byte) (int, error) {
	for r.out.Len() == 0 {
		if r.done {
			return 0, io.EOF
		}

		line, err := r.src.ReadString('\n')
		if line != "" {
			r.out.WriteString(r.rewrite(line))
		}
		if err != nil {
			r.done = true
			r.flush()
			r.reportUsage()
			if err != io.EOF {
				// Whatever the buffer holds is still worth delivering: it is the
				// caller's own data, and dropping it to report a read error the
				// caller can do nothing about would lose text that arrived fine.
				if r.out.Len() == 0 {
					return 0, err
				}
			}
		}
	}
	return r.out.Read(p)
}

// Close reports the usage if the stream never reached its end.
//
// A caller that hangs up mid-answer still spent what the provider had already
// counted, and dropping it would make an abandoned request look free.
func (r *streamRehydrator) Close() error {
	r.reportUsage()
	return r.closer.Close()
}

// reportUsage hands the accumulated counts over, at most once.
func (r *streamRehydrator) reportUsage() {
	if r.usageSent || r.onUsage == nil {
		return
	}
	r.usageSent = true
	if r.usageModel != "" {
		r.onUsage(r.usageModel, r.usageTotals)
	}
}

// rewrite expands the tokens in one line of the stream.
func (r *streamRehydrator) rewrite(line string) string {
	payload, ok := eventPayload(line)
	if !ok {
		// Not a data line: an event name, a comment, a blank separator. Nothing
		// to expand, and nothing that may be reordered.
		return line
	}

	event, err := decodeEvent(payload)
	if err != nil {
		// Not JSON — the "[DONE]" sentinel, or a shape we do not model. There is
		// no structure to work with, so expand whole tokens in the raw text and
		// hold nothing back.
		return strings.Replace(line, payload, detector.UnmaskSeen(payload, r.known, r.seen), 1)
	}

	// The same decoded event answers what the exchange cost. Accumulated rather
	// than reported here: the model and the counts arrive in different events.
	if model, usage := usageFrom(event); model != "" || usage != (telemetry.TokenUsage{}) {
		if r.usageModel == "" {
			r.usageModel = model
		}
		r.usageTotals.Input += usage.Input
		r.usageTotals.Output += usage.Output
		r.usageTotals.CacheWrite += usage.CacheWrite
		r.usageTotals.CacheRead += usage.CacheRead
	}

	// Every string in the event, decoded, so an original carrying a quote or a
	// newline is escaped by the encoder rather than spliced into raw JSON.
	expand := func(text string) string { return detector.UnmaskSeen(text, r.known, r.seen) }
	mapStrings(event, expand)

	text, setText, found := deltaText(event)
	if !found {
		// A structured event that carries no generated text: a start, a stop, a
		// usage report. Already expanded above; nothing to hold back.
		encoded, err := encodeJSONBody(event)
		if err != nil {
			return line
		}
		return "data: " + string(encoded) + "\n\n"
	}

	combined := expand(r.pending + text)

	r.pending = ""
	if tail := detector.TailLen(combined, r.known); tail > 0 {
		r.pending, combined = combined[len(combined)-tail:], combined[:len(combined)-tail]
	}

	setText(combined)
	encoded, err := encodeJSONBody(event)
	if err != nil {
		// Unreachable for a value that just came out of a decoder, but the
		// original line is the safe answer rather than a panic.
		return line
	}

	r.template = encoded
	return "data: " + string(encoded) + "\n\n"
}

// flush emits whatever tail is still held back when the stream ends.
//
// It travels in a copy of the last delta event, because a bare fragment is not a
// valid event and a client would drop it. The case is rare — the stream has to
// end on something that looks like the start of a token — but the alternative is
// losing characters the caller wrote.
func (r *streamRehydrator) flush() {
	if r.pending == "" || r.template == nil {
		r.pending = ""
		return
	}

	event, err := decodeEvent(string(r.template))
	if err != nil {
		r.pending = ""
		return
	}
	_, setText, found := deltaText(event)
	if !found {
		r.pending = ""
		return
	}

	setText(r.pending)
	r.pending = ""

	if encoded, err := encodeJSONBody(event); err == nil {
		r.out.WriteString("data: " + string(encoded) + "\n\n")
	}
}

// eventPayload returns the JSON a "data:" line carries.
func eventPayload(line string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data:")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// decodeEvent parses an event payload into an object, keeping numbers exactly as
// written — see decodeJSONBody for why that matters.
func decodeEvent(payload string) (map[string]any, error) {
	doc, err := decodeJSONBody([]byte(payload))
	if err != nil {
		return nil, err
	}
	event, ok := doc.(map[string]any)
	if !ok {
		return nil, errNotAnEvent
	}
	return event, nil
}

var errNotAnEvent = errTrailing("the event payload is not a JSON object")

// deltaText finds where a provider streams generated text, and returns a setter
// for it.
//
// Two shapes cover every provider the agent knows: Anthropic's
// delta.text, and the OpenAI-compatible choices[].delta.content that the other
// seven speak. An event matching neither is expanded in place instead — correct
// for complete tokens, and unable to reassemble a split one, which is the honest
// limit of not knowing a format.
func deltaText(event map[string]any) (text string, set func(string), found bool) {
	// Anthropic: {"type":"content_block_delta","delta":{"text":"…"}}
	if delta, ok := event["delta"].(map[string]any); ok {
		if text, ok := delta["text"].(string); ok {
			return text, func(v string) { delta["text"] = v }, true
		}
	}

	// OpenAI-compatible: {"choices":[{"delta":{"content":"…"}}]}
	choices, ok := event["choices"].([]any)
	if !ok || len(choices) == 0 {
		return "", nil, false
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return "", nil, false
	}
	delta, ok := choice["delta"].(map[string]any)
	if !ok {
		return "", nil, false
	}
	if content, ok := delta["content"].(string); ok {
		return content, func(v string) { delta["content"] = v }, true
	}
	return "", nil, false
}
