package proxy

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
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

	// onExpanded reports, once at Close, how long the expansion itself took —
	// the sum of the rewrites, not the wall clock of the stream, which is almost
	// entirely spent waiting for the provider. Set by the caller when the
	// exchange is being traced, nil otherwise. Once rather than per event: a
	// callback in that loop would be paid on every few characters of every
	// answer the agent forwards.
	onExpanded func(time.Duration)
	expanding  time.Duration

	out  bytes.Buffer
	done bool

	// pending is the held-back tail of the previous piece of text: characters
	// that could be the beginning of a token whose end has not arrived yet.
	pending string

	// block is which content block pending and arguments belong to, and -1 when
	// nothing is being held.
	//
	// A tail belongs to the block it was held back from: a replacement is inside
	// one value and cannot span two blocks. Carried across, it prefixed the next
	// block's text — or, when that block was a tool call, arrived after the stream
	// had ended in an event for a block closed long before, which is what it
	// actually did.
	block int

	// arguments is a tool call's, accumulated whole.
	//
	// They arrive as slices of a JSON document — "{\"command\":\"cat /U", then
	// "sers/alice" — so they are not text and cannot be treated as any. Expanding a
	// value into a slice splices it into the *source* of a document this only ever
	// sees a piece of, and an original carrying a quote ends the string it landed
	// in: the client's parse of the tool call then fails, at the client, silently.
	//
	// So the fragments are held until the block stops, when the concatenation is a
	// whole document — decoded, expanded value by value, re-encoded. The encoder
	// escapes, which is the rule the request path already follows for the same
	// reason. Nothing is lost by waiting: a client cannot use half a JSON document,
	// so it has to wait for the stop in any case.
	arguments    strings.Builder
	argumentsSet func(string)
	argumentsIn  jsonObject

	// template is the last delta event seen, kept so a pending tail still has an
	// event to travel in when the stream ends before the token completes.
	template []byte

	// name is the "event:" line whose data line has not arrived yet.
	//
	// An SSE event is a name line, a data line and a blank, and a client dispatches
	// on the name. So the three travel together or not at all. Forwarded the moment
	// it arrived, a name line whose data line is then held back reaches the caller
	// alone — and a client reading a named event with no data parses the empty
	// string: "JSON Parse error: Unexpected EOF", the whole exchange lost. Held
	// here, it is emitted with its own data line, and after whatever the block
	// before it was still holding.
	name string

	// argumentsName and templateName are the name lines those two held events
	// arrived under, so an event synthesised from either is dispatched as what it
	// is. Emitted with the copy rather than remembered globally: a bare data line
	// is attached by the client to whichever name it saw last, which is the stop
	// event that released the arguments in the first place.
	argumentsName string
	templateName  string

	// onTool reports a tool call the model asked for, once its arguments are a whole
	// document, and whether a masked value was put back into them. Always set by the
	// agent, because the heartbeat counts every tool call; nil only in tests that
	// build a rehydrator by hand.
	onTool func(name, arguments string, restored bool)

	// onDegraded reports that a tool call's fragments never formed a document and
	// were expanded token by token — the second-best path, counted so a fleet view
	// can see how often the control ran on it. Nil skips the report.
	onDegraded func()

	// toolName is what the block being read is a call to, taken from its start
	// event. Held beside the arguments rather than in a map keyed by block, because
	// exactly one block is being accumulated at a time — the same reason arguments
	// is one builder — and reset by every start event, or a second, unnamed tool
	// call would be reported under the first one's name.
	toolName string

	// observing records that a tool call's fragments are being collected for the
	// console alone, on the path that rewrites nothing — see observe.
	observing bool

	// held records that the event just read emitted nothing, so its blank
	// separator is dropped with it: an event that is held contributes none of its
	// three lines, not two of them. A conformant client ignores a blank line with
	// no fields before it, but a tool call arrives in hundreds of fragments and
	// betting the answer on that is how this file earned its last fix. Any other
	// field arriving before that blank ends the hold — see rewrite.
	//
	// TODO: SSE allows an `event:` line *after* its `data:` line, and this reads
	// the name as belonging to the data line that follows it. No provider this
	// agent proxies writes an event that way round.
	held bool
}

// The concrete type is returned rather than io.ReadCloser so a caller that traces
// can set onExpanded. A fifth positional callback would have made the constructor
// unreadable for something only one of its two callers wants.
func newStreamRehydrator(body io.ReadCloser, known map[string]string,
	onUsage func(string, telemetry.TokenUsage),
	seen func(replacement, original string)) *streamRehydrator {
	return &streamRehydrator{
		block:   -1,
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
			started := time.Now()
			rewritten := r.rewrite(line)
			r.expanding += time.Since(started)
			r.out.WriteString(rewritten)
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
	// Before the body below it is closed, because that is what files the trace:
	// reported after, the figure would arrive at a file already written.
	if r.onExpanded != nil {
		r.onExpanded(r.expanding)
	}
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
	if len(r.known) == 0 {
		return r.observe(line)
	}
	payload, ok := eventPayload(line)
	if !ok {
		// An event's name travels with its data line — see the field. Anything
		// else is a comment or a blank separator: nothing to expand, and nothing
		// that may be reordered.
		if strings.HasPrefix(line, "event:") {
			r.name = line
			return ""
		}
		if strings.TrimSpace(line) == "" {
			if r.held {
				r.held = false
				return ""
			}
			return line
		}
		// A comment, an `id:` or a `retry:` between a held data line and its blank.
		// Forwarded, and the hold ends with it: the blank that follows now closes
		// *this* line rather than the held event, and swallowing it would merge the
		// line into the next event's block — an `id:` attached to the wrong event
		// is a client resuming from the wrong point. Alone before a blank it
		// dispatches nothing and still sets what it sets, which is what it did in
		// the provider's stream.
		r.held = false
		return line
	}
	r.held = false

	event, err := decodeEvent(payload)
	if err != nil {
		// Not JSON — the "[DONE]" sentinel, or a shape we do not model. There is
		// no structure to work with, so expand whole tokens in the raw text and
		// hold nothing back.
		//
		// Whatever the block was holding goes out first. The OpenAI family has no
		// stop event and its deltas carry no block index, so a tail held back
		// from the last piece of text reached closeBlock only at end of stream —
		// after "[DONE]", which is where every SDK stops reading. The characters
		// the caller wrote were delivered to nobody.
		return r.closeBlock() + r.takeName() + strings.Replace(line, payload, detector.UnmaskSeen(payload, r.known, r.seen), 1)
	}

	r.noteUsage(event)

	// Whatever the block before was holding is emitted before this event, and
	// never after it: a block's own text has to reach the caller inside that
	// block, and a tool call's arguments before the stop that completes them.
	prefix := ""
	if index, ok := blockIndex(event); ok && index != r.block {
		prefix = r.closeBlock()
		r.block = index
	}
	// After the prefix and before the line it belongs to: whatever the block was
	// holding is a whole event of its own, carrying its own name.
	name := r.takeName()

	if eventType(event) == "content_block_stop" {
		return prefix + r.closeBlock() + name + line
	}

	// A start event says what the block is, and for a tool call which tool. Read
	// here rather than in the audit console, which never sees an event: this is the
	// only place that knows a name and the arguments belong together.
	if start, ok := objectAt(event, "content_block"); ok {
		r.toolName = ""
		if name, ok := toolCallName(start); ok {
			r.toolName = name
		}
	}

	// A tool call's arguments are held rather than rewritten — see the field.
	// Held *before* anything expands in place, because expanding in place is the
	// splice this exists to prevent.
	if fragment, set, ok := jsonFragment(event); ok {
		if r.argumentsIn == nil {
			// The first fragment's own event carries the whole document later, so
			// the shape emitted is the shape that arrived and the encoder does the
			// escaping.
			r.argumentsIn, r.argumentsSet, r.argumentsName = event, set, name
		}
		r.arguments.WriteString(fragment)
		r.held = true
		return prefix
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
			return prefix + name + line
		}
		return prefix + name + "data: " + string(encoded) + "\n\n"
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
		return prefix + name + line
	}

	r.template, r.templateName = encoded, name
	return prefix + name + "data: " + string(encoded) + "\n\n"
}

// observe reads one line of a stream for a session that minted nothing, and
// forwards it exactly as it arrived.
//
// An answer says three things and only one of them depends on the mapping: what to
// put back, what the exchange cost, and what tool the model asked to run. With
// nothing to put back the rewrite is the identity, and it is still not free of
// consequence: every event is decoded and re-encoded — a lone surrogate comes out as
// U+FFFD — a text ending on `[` is held back for a token that cannot arrive, and a
// tool call's arguments are released as one document under a synthesised event
// rather than as the fragments the provider sent. So the stream goes through
// untouched, and the two questions that do not depend on the mapping are answered
// by reading it: the counts are accumulated as they are on the rewriting path, and
// the console is told each tool call once its fragments are whole, at the stop that
// completes them.
func (r *streamRehydrator) observe(line string) string {
	payload, ok := eventPayload(line)
	if !ok {
		return line
	}
	event, err := decodeEvent(payload)
	if err != nil {
		return line
	}
	r.noteUsage(event)

	if r.onTool == nil {
		return line
	}
	if index, ok := blockIndex(event); ok && index != r.block {
		r.reportArguments()
		r.block = index
	}
	if eventType(event) == "content_block_stop" {
		r.reportArguments()
		return line
	}
	if start, ok := objectAt(event, "content_block"); ok {
		r.toolName = ""
		if name, ok := toolCallName(start); ok {
			r.toolName = name
		}
	}
	if fragment, _, ok := jsonFragment(event); ok {
		r.arguments.WriteString(fragment)
		r.observing = true
	}
	return line
}

// reportArguments hands the console the tool call whose fragments have all arrived,
// on the observing path, where nothing is expanded and the document is the
// concatenation as the provider sent it.
func (r *streamRehydrator) reportArguments() {
	if !r.observing {
		return
	}
	r.observing = false
	raw := r.arguments.String()
	r.arguments.Reset()
	// Nothing was restored: this is the path for a session that minted nothing.
	r.onTool(r.toolName, raw, false)
	r.toolName = ""
}

// noteUsage accumulates what the exchange cost from one decoded event. Accumulated
// rather than reported: the model and the counts arrive in different events.
//
// Every usage object is a snapshot, and a count it carries replaces the one held
// rather than adding to it. Anthropic's message_delta usage is cumulative — it
// repeats input_tokens and the cache counts from message_start beside the final
// output_tokens — so adding the two events billed the input twice. OpenAI sends
// one usage chunk, at the end, and a snapshot reads that correctly too. A count
// the event does not carry arrives as zero and leaves what an earlier event said.
func (r *streamRehydrator) noteUsage(event jsonObject) {
	if model, usage := usageFrom(event); model != "" || usage != (telemetry.TokenUsage{}) {
		if r.usageModel == "" {
			r.usageModel = model
		}
		if usage.Input != 0 {
			r.usageTotals.Input = usage.Input
		}
		if usage.Output != 0 {
			r.usageTotals.Output = usage.Output
		}
		if usage.CacheWrite != 0 {
			r.usageTotals.CacheWrite = usage.CacheWrite
		}
		if usage.CacheRead != 0 {
			r.usageTotals.CacheRead = usage.CacheRead
		}
	}
}

// takeName returns the withheld event-name line and clears it, so it is emitted
// with exactly one data line.
func (r *streamRehydrator) takeName() string {
	name := r.name
	r.name = ""
	return name
}

// closeBlock emits whatever the block being left behind was holding.
//
// Two things, never both: a text block's held-back tail, or a tool call's
// accumulated arguments. A tail that reaches here was never the start of a
// replacement — the block ended — so it is the caller's own text and is delivered
// as it stands.
func (r *streamRehydrator) closeBlock() string {
	out := ""
	// On the event being held, not on the fragments' length: Anthropic opens every
	// tool call's arguments with an empty fragment, and a tool that takes none sends
	// nothing after it. Released on length, that block kept its hold, and the next
	// tool call's restored arguments went out in *this* block's event — after its
	// stop, under its index, so the client attached one tool's arguments to another.
	if r.argumentsIn != nil {
		out += r.expandedArguments()
	}
	if r.pending != "" {
		out += r.tailEvent(r.pending)
		r.pending = ""
	}
	return out
}

// expandedArguments returns the tool call's arguments as one event, expanded as the
// document they are.
func (r *streamRehydrator) expandedArguments() string {
	raw := r.arguments.String()
	r.arguments.Reset()

	event, set, name := r.argumentsIn, r.argumentsSet, r.argumentsName
	r.argumentsIn, r.argumentsSet, r.argumentsName = nil, nil, ""
	if event == nil || set == nil {
		return ""
	}

	// restored is what the heartbeat counts: a tool about to act on a value the
	// model never saw. Read off the expansion itself rather than off the mapping,
	// because a session with a mapping still makes tool calls that touch none of it.
	restored := false
	expand := func(text string) string {
		out := detector.UnmaskSeen(text, r.known, r.seen)
		if out != text {
			restored = true
		}
		return out
	}

	out := ""
	if doc, err := decodeJSONBody([]byte(raw)); err == nil {
		if encoded, err := encodeJSONBody(mapStrings(doc, expand)); err == nil {
			out = string(encoded)
		}
	}
	if out == "" {
		// The fragments do not make a document: a stream cut short, or a shape this
		// does not model. Whole tokens in the raw text, which is what the agent did
		// before this held anything back — never worse than it was, and a client
		// that cannot parse the arguments could not have used them either way.
		out = expand(raw)
		if r.onDegraded != nil {
			r.onDegraded()
		}
	}

	// Reported here because here is where both halves exist at once: the tool's
	// name from its start event, and the arguments as one restored document. Before
	// the event is re-encoded, so the console shows what the client will act on
	// rather than the framing around it.
	if r.onTool != nil {
		r.onTool(r.toolName, out, restored)
	}
	r.toolName = ""

	set(out)
	encoded, err := encodeJSONBody(event)
	if err != nil {
		return ""
	}
	return name + "data: " + string(encoded) + "\n\n"
}

// tailEvent puts a held-back tail into a copy of the last delta event.
//
// A bare fragment is not a valid event and a client would drop it, which would lose
// characters the caller wrote.
func (r *streamRehydrator) tailEvent(tail string) string {
	if r.template == nil {
		return ""
	}
	event, err := decodeEvent(string(r.template))
	if err != nil {
		return ""
	}
	_, setText, found := deltaText(event)
	if !found {
		return ""
	}
	setText(tail)
	encoded, err := encodeJSONBody(event)
	if err != nil {
		return ""
	}
	return r.templateName + "data: " + string(encoded) + "\n\n"
}

// eventType reports the "type" an event declares, or "" for one that declares none.
func eventType(event jsonObject) string {
	name, _ := stringAt(event, "type")
	return name
}

// blockIndex reports which content block an event belongs to.
//
// Absent on the message-level events — a start, a stop, a usage report — and those
// must not be read as block zero, or the first of them would close the block that
// is still streaming.
func blockIndex(event jsonObject) (int, bool) {
	raw, ok := event.value("index")
	if !ok {
		return 0, false
	}
	return asInt(raw)
}

// jsonFragment finds a slice of a tool call's arguments, and returns a setter for it.
//
// Anthropic streams them as input_json_delta.partial_json. The OpenAI-compatible
// family streams its own as choices[].delta.tool_calls[].function.arguments, and
// that is deliberately not read here.
//
// TODO: OpenAI tool call arguments are still expanded in place, so a value split
// across two of them is not restored and one carrying a quote can break the
// document. It has no per-block stop to accumulate against — the end is a
// finish_reason on the message — and building that on an unverified reading of the
// format, in the component that decides what leaves in clear, is how a change passes
// its tests and fails the real stream. The upgrade is a recorded fixture from a real
// OpenAI tool call, then the same accumulation keyed on the tool call index.
func jsonFragment(event jsonObject) (fragment string, set func(string), found bool) {
	delta, ok := objectAt(event, "delta")
	if !ok {
		return "", nil, false
	}
	if fragment, ok := stringAt(delta, "partial_json"); ok {
		return fragment, func(v string) { delta.setValue("partial_json", v) }, true
	}
	return "", nil, false
}

// flush emits whatever the last block was still holding when the stream ended.
//
// A stream regularly ends without the stop that would have closed its block — cut
// short, or a shape with no stop event — so the same close runs here. The tail case
// is rare, the stream having to end on something that looks like the start of a
// token, but the alternative is losing characters the caller wrote; the arguments
// case is a tool call the caller would otherwise never receive at all.
func (r *streamRehydrator) flush() {
	if r.observing {
		// A stream cut short of its stop: the console is still told what had
		// arrived, as the rewriting path releases what it held.
		r.reportArguments()
	}
	r.out.WriteString(r.closeBlock())
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
func decodeEvent(payload string) (jsonObject, error) {
	doc, err := decodeJSONBody([]byte(payload))
	if err != nil {
		return nil, err
	}
	event, ok := doc.(jsonObject)
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
func deltaText(event jsonObject) (text string, set func(string), found bool) {
	// Anthropic: {"type":"content_block_delta","delta":{"text":"…"}}, and the same
	// shape under "thinking" for extended reasoning. Generated text either way, and
	// a replacement splits across two of one exactly as it does across two of the
	// other — left out, a thought carried the agent's own bookkeeping to the reader.
	if delta, ok := objectAt(event, "delta"); ok {
		for _, field := range []string{"text", "thinking"} {
			if text, ok := stringAt(delta, field); ok {
				return text, func(v string) { delta.setValue(field, v) }, true
			}
		}
	}

	// OpenAI-compatible: {"choices":[{"delta":{"content":"…"}}]}
	raw, ok := event.value("choices")
	if !ok {
		return "", nil, false
	}
	choices, ok := raw.([]any)
	if !ok || len(choices) == 0 {
		return "", nil, false
	}
	choice, ok := choices[0].(jsonObject)
	if !ok {
		return "", nil, false
	}
	delta, ok := objectAt(choice, "delta")
	if !ok {
		return "", nil, false
	}
	if content, ok := stringAt(delta, "content"); ok {
		return content, func(v string) { delta.setValue("content", v) }, true
	}
	return "", nil, false
}

// objectAt and stringAt read one typed field, so the navigation above says what it
// is looking for rather than repeating a type assertion at every step.
func objectAt(o jsonObject, key string) (jsonObject, bool) {
	raw, ok := o.value(key)
	if !ok {
		return nil, false
	}
	nested, ok := raw.(jsonObject)
	return nested, ok
}

func stringAt(o jsonObject, key string) (string, bool) {
	raw, ok := o.value(key)
	if !ok {
		return "", false
	}
	text, ok := raw.(string)
	return text, ok
}
