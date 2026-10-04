package proxy

import (
	"encoding/json"

	"github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
)

// Reading how much a request cost, from whatever shape the provider answered in.
//
// It looks for two things — a "model" string and a "usage" object — at the top of
// the document and in the one object each streaming shape nests them in, rather
// than by knowing eight response schemas: the field names inside "usage" differ
// between vendors and are enumerated below, and a per-provider path table would be
// eight things to keep current instead of one.
//
// Not anywhere in the document. A walk took the first "model" string it met, so a
// tool call whose arguments carried a "model" key put a string the model chose on
// the wire as a key of the heartbeat's models map, and every nested "usage" object
// was summed into the bill.
//
// The counts are raw and never a cost. Converting to money needs a price table
// per model, and that table belongs to the backend: prices change, and an agent
// that computed them would have to be redeployed to every workstation each time
// one did.

// usageFrom walks a decoded document and reports the model it names and the
// tokens it accounts for.
//
// Both are optional and arrive separately in a stream: Anthropic sends the model
// in the first event and the output count in the last, so a caller accumulating
// across a stream has to keep whichever it has seen.
func usageFrom(doc any) (model string, usage telemetry.TokenUsage) {
	top, ok := doc.(jsonObject)
	if !ok {
		return "", usage
	}
	readModelAndUsage(top, &model, &usage)
	// Anthropic's message_start nests both under "message"; OpenAI's Responses
	// stream, which Codex speaks, under "response" in response.completed.
	for _, key := range []string{"message", "response"} {
		if inner, ok := objectAt(top, key); ok {
			readModelAndUsage(inner, &model, &usage)
		}
	}
	return model, usage
}

func readModelAndUsage(obj jsonObject, model *string, usage *telemetry.TokenUsage) {
	if *model == "" {
		if name, ok := stringAt(obj, "model"); ok {
			*model = name
		}
	}
	if fields, ok := objectAt(obj, "usage"); ok {
		readUsage(fields, usage)
	}
}

// readUsage adds the counts one "usage" object carries.
//
// The two vendor families disagree about more than spelling, and the difference
// is a double-count waiting to happen:
//
//   - Anthropic reports cache tokens *beside* input_tokens, so the four counts
//     add up to the total and each is priced on its own.
//   - OpenAI reports prompt_tokens as the whole input, with the cached portion
//     broken out underneath as a *subset*. So prompt_tokens is read and the
//     breakdown deliberately is not: adding it would bill the same tokens twice.
//
// An unknown key is ignored rather than guessed at. A provider adding a count
// then shows up as a gap in the total, which is visible; folding it into input
// would show up as a wrong bill, which is not.
func readUsage(fields jsonObject, usage *telemetry.TokenUsage) {
	for _, m := range fields {
		n, ok := asInt(m.value)
		if !ok {
			continue
		}
		switch m.key {
		case "input_tokens", "prompt_tokens":
			usage.Input += n
		case "output_tokens", "completion_tokens":
			usage.Output += n
		case "cache_creation_input_tokens":
			usage.CacheWrite += n
		case "cache_read_input_tokens":
			usage.CacheRead += n
		}
	}
}

// asInt reads a JSON number without going through a float. The decoder is set to
// UseNumber precisely so a count of ten million does not arrive as 1e+07 and get
// mangled on the way to an integer.
func asInt(raw any) (int, bool) {
	number, ok := raw.(json.Number)
	if !ok {
		return 0, false
	}
	n, err := number.Int64()
	if err != nil {
		return 0, false
	}
	return int(n), true
}
