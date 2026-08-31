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
