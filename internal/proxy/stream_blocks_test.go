package proxy

import (
	"strings"
	"testing"
)

// events frames SSE data lines, so a test reads as the stream it describes.
func events(payloads ...string) string {
	var b strings.Builder
	for _, p := range payloads {
		b.WriteString("data: " + p + "\n\n")
	}
	return b.String()
}

// A tool call's arguments are streamed as fragments of a JSON document, and a
// replacement lands across two of them as readily as it lands across two pieces of
// text. Unrestored, the value the tool receives is this agent's own bookkeeping —
// and the tool acts on it.
func TestAToolCallsArgumentsAreRestoredAcrossEvents(t *testing.T) {
	known := map[string]string{"[EMAIL_1]": "pierre.paul@example.fr"}
	stream := events(
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"Bash"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"to\":\"[EMA"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"IL_1]\"}"}}`,
		`{"type":"content_block_stop","index":0}`,
	)

	out := rehydrate(t, stream, known)
	if !strings.Contains(out, "pierre.paul@example.fr") {
		t.Fatalf("the value was not restored across two fragments:\n%s", out)
	}
	if strings.Contains(out, "[EMAIL_1]") {
		t.Errorf("the replacement reached the tool:\n%s", out)
	}
}

// The arguments are a JSON document, so a restored value carrying a quote has to be
// escaped for it. Spliced in raw, the document the client reassembles no longer
// parses — and it fails at the client, silently, where nothing here can see it.
func TestAToolCallsArgumentsStayValidJSON(t *testing.T) {
	known := map[string]string{"[NAME_1]": `Ann "Nan" O'Neill\Smith`}
	stream := events(
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"Write"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"who\":\"[NA"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"ME_1]\"}"}}`,
		`{"type":"content_block_stop","index":0}`,
	)

	// The client's job: concatenate every partial_json, then parse.
	var joined strings.Builder
	for _, line := range strings.Split(rehydrate(t, stream, known), "\n") {
		payload, ok := eventPayload(line)
		if !ok {
			continue
		}
		event, err := decodeEvent(payload)
		if err != nil {
			continue
		}
		if delta, ok := objectAt(event, "delta"); ok {
			if fragment, ok := stringAt(delta, "partial_json"); ok {
				joined.WriteString(fragment)
			}
		}
	}

	doc, err := decodeJSONBody([]byte(joined.String()))
	if err != nil {
		t.Fatalf("the reassembled arguments do not parse: %v\n%s", err, joined.String())
	}
	object, ok := doc.(jsonObject)
	if !ok {
		t.Fatalf("the arguments are not an object: %s", joined.String())
	}
	if got, _ := stringAt(object, "who"); got != known["[NAME_1]"] {
		t.Errorf("who = %q, want %q", got, known["[NAME_1]"])
	}
}

// A held-back tail belongs to the block it was held back from. Carried across a
// boundary it either prefixes the next block's text or, as it did, arrives after the
// stream has ended in an event for a block that was closed long before.
func TestATailDoesNotCrossABlockBoundary(t *testing.T) {
	known := map[string]string{"[EMAIL_1]": "pierre.paul@example.fr"}
	stream := events(
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"end of block [EMA"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"a new block"}}`,
	)

	out := rehydrate(t, stream, known)

	// The four characters are the caller's own text and must be delivered — in
	// block 0, before it stops, because they were never the start of a token.
	stop := strings.Index(out, "content_block_stop")
	if stop < 0 {
		t.Fatalf("the stop event is gone:\n%s", out)
	}
	if !strings.Contains(out[:stop], "[EMA") {
		t.Errorf("the held-back tail was not delivered before its block stopped:\n%s", out)
	}
	if strings.Contains(out[stop:], "[EMA") {
		t.Errorf("the tail crossed into another block, or arrived after the stream:\n%s", out[stop:])
	}
}

// Extended thinking is generated text like any other, and a replacement splits
// across two of its events exactly as it does across two of any other's.
func TestAValueIsRestoredAcrossThinkingEvents(t *testing.T) {
	known := map[string]string{"[EMAIL_1]": "pierre.paul@example.fr"}
	stream := events(
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"write to [EMA"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"IL_1] first"}}`,
	)

	if out := rehydrate(t, stream, known); !strings.Contains(out, "pierre.paul@example.fr") {
		t.Fatalf("the value was not restored across two thinking events:\n%s", out)
	}
}

// A stream cut off mid-arguments must still deliver the tool call. The fragments do
// not make a document, so they cannot be expanded as one — whole tokens in the raw
// text is what the agent did before it held anything back, and a client that cannot
// parse the arguments could not have used them either way. Losing them entirely is
// the one outcome that is worse than before.
func TestIncompleteArgumentsAreStillDelivered(t *testing.T) {
	known := map[string]string{"[EMAIL_1]": "pierre.paul@example.fr"}
	stream := events(
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"Bash"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"to\":\"[EMAIL_1]\", \"and\":"}}`,
	)

	out := rehydrate(t, stream, known)
	if !strings.Contains(out, "input_json_delta") {
		t.Fatalf("the unterminated tool call was dropped:\n%s", out)
	}
	if !strings.Contains(out, "pierre.paul@example.fr") {
		t.Errorf("a whole token in an unterminated document was not expanded:\n%s", out)
	}
}

// Arguments held back for one block must not be emitted into another. The stop that
// completes a tool call is what releases them, and it releases them before itself.
func TestArgumentsAreReleasedBeforeTheirStop(t *testing.T) {
	known := map[string]string{"[EMAIL_1]": "pierre.paul@example.fr"}
	stream := events(
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"Bash"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"to\":\"[EMA"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"IL_1]\"}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"after"}}`,
	)

	out := rehydrate(t, stream, known)
	args := strings.Index(out, "input_json_delta")
	stop := strings.Index(out, "content_block_stop")
	if args < 0 || stop < 0 {
		t.Fatalf("the tool call or its stop is missing:\n%s", out)
	}
	if args > stop {
		t.Errorf("the arguments were emitted after the stop that completes them:\n%s", out)
	}
	// And exactly once: emitted per fragment as well, the client would concatenate
	// the whole document twice and parse neither.
	if n := strings.Count(out, "input_json_delta"); n != 1 {
		t.Errorf("the arguments were emitted %d times, want once:\n%s", n, out)
	}
}

// namedEvents frames the stream the way Anthropic actually sends it: every data
// line preceded by the event name a client dispatches on.
//
// The helper above deliberately does not, which is how the failure below shipped —
// a stream with no names in it cannot exhibit a name that lost its data.
func namedEvents(payloads ...string) string {
	var b strings.Builder
	for _, p := range payloads {
		name, _, _ := strings.Cut(strings.TrimPrefix(p, `{"type":"`), `"`)
		b.WriteString("event: " + name + "\ndata: " + p + "\n\n")
	}
	return b.String()
}

// An SSE event is a name line and a data line, and they travel together or not at
// all.
//
// Holding a tool call's arguments back drops the data line, and the name line had
// already been forwarded — so the caller read a named content_block_delta with
// nothing under it, parsed the empty string and died on "JSON Parse error:
// Unexpected EOF". One malformed pair kills the whole answer, so the assertion is
// over every event in the stream rather than over the tool call's.
func TestEveryNamedEventCarriesItsData(t *testing.T) {
	known := map[string]string{"[EMAIL_1]": "pierre.paul@example.fr"}
	stream := namedEvents(
		`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"writing to [EMA"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"IL_1] now"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","name":"Bash"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"to\":\"[EMA"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"IL_1]\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_stop"}`,
	)

	out := rehydrate(t, stream, known)

	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "event:") {
			continue
		}
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "data:") {
			t.Errorf("%q reached the caller with no data line under it:\n%s", line, out)
		}
	}

	// And the other direction: a bare data line is attached by the client to
	// whichever name it saw last, so the arguments released at the stop would be
	// dispatched as the stop itself.
	for i, line := range lines {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		if i == 0 || !strings.HasPrefix(lines[i-1], "event:") {
			t.Errorf("%q reached the caller with no name above it:\n%s", line, out)
		}
	}
}

// Anthropic opens every tool call's arguments with an empty fragment, and a tool
// that takes none streams nothing else. Released on the fragments' *length*, that
// block never released anything: its start event stayed held, and the next tool
// call's restored arguments were emitted into it — in the first block's event,
// after that block's stop had already gone out, so the client attached one tool's
// arguments to another. A block releases what it holds at its stop, whatever it
// held.
func TestAnEmptyArgumentBlockReleasesItsHold(t *testing.T) {
	known := map[string]string{"[EMAIL_1]": "claire@example.fr"}
	stream := events(
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"ListFiles"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","name":"Send"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"to\":\"[EMA"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"IL_1]\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
	)

	out := rehydrate(t, stream, known)
	restored := strings.Index(out, "claire@example.fr")
	if restored < 0 {
		t.Fatalf("the restored arguments are missing:\n%s", out)
	}
	line := out[strings.LastIndex(out[:restored], "\n")+1:]
	if !strings.Contains(line, `"index":1`) {
		t.Errorf("the second tool's arguments travel in the first block's event:\n%s", out)
	}
	// And the empty block still says what it said: one fragment, empty, before its
	// stop — a client concatenating nothing parses nothing, exactly as it arrived.
	if n := strings.Count(out, "input_json_delta"); n != 2 {
		t.Errorf("%d argument events, want one per tool call:\n%s", n, out)
	}
}

// A field that is neither a name nor data — a comment a CDN injects as a keepalive,
// an `id:`, a `retry:` — can arrive between a held fragment's data line and its
// blank. Cleared only by a data line or a blank, the hold swallowed the blank that
// followed it and the line was merged into the next event's block: an `id:` then
// attached to the restored arguments, and a client resuming on Last-Event-ID resumed
// from the wrong point.
func TestAFieldBetweenAHeldFragmentAndItsBlankKeepsItsOwnBlank(t *testing.T) {
	known := map[string]string{"[EMAIL_1]": "pierre.paul@example.fr"}
	stream := "event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"Bash"}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"to\":\"[EMAIL_1]\"}"}}` + "\n" +
		"id: 42\n\n" +
		": keepalive\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n"

	out := rehydrate(t, stream, known)
	for _, field := range []string{"id: 42\n\n", ": keepalive\n\n"} {
		if !strings.Contains(out, field) {
			t.Errorf("%q lost the blank that closes it:\n%s", field, out)
		}
	}
	if !strings.Contains(out, "pierre.paul@example.fr") {
		t.Errorf("the arguments were not restored:\n%s", out)
	}
}
