package proxy

import (
	"encoding/json"

	"github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
)

// Reading how much a request cost, from whatever shape the provider answered in.
//
// It is done by walking the decoded document for two things — a "model" string
// and a "usage" object — rather than by knowing eight response schemas. That is
// the right trade: the field names inside "usage" differ between vendors and are
// enumerated below, but *where* the object sits differs far more, and a
// per-provider path table would be eight things to keep current instead of one.
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
	walkUsage(doc, &model, &usage)
	return model, usage
}

func walkUsage(node any, model *string, usage *telemetry.TokenUsage) {
	switch t := node.(type) {
	case jsonObject:
		if *model == "" {
			if name, ok := stringAt(t, "model"); ok {
				*model = name
			}
		}
		if fields, ok := objectAt(t, "usage"); ok {
			readUsage(fields, usage)
		}
		for _, m := range t {
			walkUsage(m.value, model, usage)
		}

	case []any:
		for _, child := range t {
			walkUsage(child, model, usage)
		}
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
