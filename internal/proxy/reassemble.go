package proxy

import (
	"fmt"
	"strings"
)

// An event stream is unreadable at the grain it arrives in.
//
// A provider streams a few characters per event, so an answer of two paragraphs is
// several hundred blocks of framing around fragments the size of a syllable — and a
// tool call's arguments come apart mid-path: "R=/U", then "sers/alice", then
// "ly/Projets/s". Nobody can read what the model said from that, which is what
// somebody opening a trace is there to do.
//
// So the section below the figures is the same stream put back together: one entry
// per content block, in the order the blocks opened, carrying the concatenation of
// their deltas.
//
// # Why the raw events stay underneath it
//
// This is a derived view and the file must not be only that. A trace is evidence:
// the bodies hold the bytes that were sent, escapes and all, which is what makes
// them diffable against each other and searchable for a value. Reassembling decodes
// the JSON strings — an escaped newline becomes a newline — so what reads well is
// exactly what no longer holds what went over the wire. Both, in that order:
// readable first, because that is what the file is opened for; verbatim below,
// because that is what it is kept for.
//
// It is built from the answer as it arrived, before expansion, like everything else
// in that half. So a replacement still reads as [EMAIL_8] here, which is the point —
// against the OUT body above, it says which of the tokens sent up came back.

// reassembled renders an event stream as its content blocks, or reports that there
// was nothing to put back together.
//
// Nothing rather than an empty section: a buffered JSON answer is one document
// already, and a stream in a shape this does not model would get a heading over a
// blank space, which reads as an answer that said nothing.
func reassembled(raw string) (string, bool) {
	blocks := assembleBlocks(raw)
	if len(blocks) == 0 {
		return "", false
	}

	var b strings.Builder
	for i, block := range blocks {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[%d] %s\n%s\n", block.index, block.label, block.text.String())
	}
	return b.String(), true
}

// block is one content block being put back together.
type block struct {
	index int
	label string
	text  strings.Builder
}

// assembleBlocks walks the events and concatenates each block's deltas.
//
// Kept in arrival order rather than sorted by index, because that is the order the
// model produced them in and a reader is following a train of thought. A block whose
// start event never named it is labelled by the delta shape instead, so an unnamed
// block is still distinguishable from the one above it.
func assembleBlocks(raw string) []*block {
	var (
		blocks  []*block
		byIndex = map[int]*block{}
	)

	at := func(index int) *block {
		if b, ok := byIndex[index]; ok {
			return b
		}
		b := &block{index: index, label: "text"}
		byIndex[index] = b
		blocks = append(blocks, b)
		return b
	}

	for _, line := range strings.Split(raw, "\n") {
		payload, ok := eventPayload(line)
		if !ok {
			continue
		}
		event, err := decodeEvent(payload)
		if err != nil {
			// The "[DONE]" sentinel, or a shape with no structure to read.
			continue
		}

		index := 0
		if raw, ok := event.value("index"); ok {
			if n, ok := asInt(raw); ok {
				index = n
			}
		}

		// A start event names what the block is: generated text, a thought, or a
		// tool call — and for a tool call, which tool. That name is what makes the
		// difference between reading an answer and reading a wall of JSON.
		if start, ok := objectAt(event, "content_block"); ok {
			b := at(index)
			b.label, _ = stringAt(start, "type")
			if name, ok := stringAt(start, "name"); ok {
				b.label += " " + name
			}
			continue
		}

		if delta, ok := objectAt(event, "delta"); ok {
			// The three Anthropic shapes. partial_json is a tool call's arguments,
			// and it is the one that comes apart mid-path.
			for _, field := range []string{"text", "partial_json", "thinking"} {
				if text, ok := stringAt(delta, field); ok {
					at(index).text.WriteString(text)
					break
				}
			}
			continue
		}

		// OpenAI-compatible, where the whole answer is one block.
		if text, _, ok := deltaText(event); ok {
			at(index).text.WriteString(text)
		}
	}

	// A stream that opened blocks and streamed nothing into them is a start and a
	// stop with no answer between — the section would say less than its own heading.
	for _, b := range blocks {
		if b.text.Len() > 0 {
			return blocks
		}
	}
	return nil
}
