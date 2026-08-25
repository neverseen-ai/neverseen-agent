package proxy

import (
	"testing"

	"github.com/cloakfleet/cloakfleet/pkg/telemetry"
)

// Reading what a request cost, from the shapes the providers actually answer in.
//
// The rows are real response fragments rather than a normalised invention,
// because the whole difficulty here is that the vendors disagree — about the
// field names, about where the object sits, and about whether the cache counts
// are part of the input or beside it.

func TestUsageFrom(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantModel string
		wantUsage telemetry.TokenUsage
	}{
		{
			name: "anthropic, buffered",
			body: `{"id":"msg_1","model":"claude-sonnet-4","content":[{"type":"text","text":"hi"}],
			        "usage":{"input_tokens":1204,"output_tokens":98,
			                 "cache_creation_input_tokens":18430,"cache_read_input_tokens":184302}}`,
			wantModel: "claude-sonnet-4",
			// Anthropic reports the cache counts *beside* the input, so all four
			// add up to what was spent and each is priced on its own.
			wantUsage: telemetry.TokenUsage{Input: 1204, Output: 98, CacheWrite: 18430, CacheRead: 184302},
		},
		{
			name: "openai-compatible, buffered",
			body: `{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"message":{"content":"hi"}}],
			        "usage":{"prompt_tokens":9140,"completion_tokens":1205,"total_tokens":10345}}`,
			wantModel: "gpt-4o",
			wantUsage: telemetry.TokenUsage{Input: 9140, Output: 1205},
		},
		{
			// The double-count this design exists to avoid. OpenAI's
			// prompt_tokens is the *whole* input, with the cached portion broken
			// out underneath as a subset — so the breakdown is deliberately not
			// read. Adding it would bill the same tokens twice.
			name: "openai's cached tokens are a subset, not an addition",
			body: `{"model":"gpt-4o","usage":{"prompt_tokens":9140,"completion_tokens":100,
			        "prompt_tokens_details":{"cached_tokens":8000}}}`,
			wantModel: "gpt-4o",
			wantUsage: telemetry.TokenUsage{Input: 9140, Output: 100},
		},
		{
			// Anthropic's stream: the model arrives in the first event…
			name:      "anthropic stream, message_start",
			body:      `{"type":"message_start","message":{"model":"claude-sonnet-4","usage":{"input_tokens":12,"cache_read_input_tokens":900}}}`,
			wantModel: "claude-sonnet-4",
			wantUsage: telemetry.TokenUsage{Input: 12, CacheRead: 900},
		},
		{
			// …and the output count in the last, with no model beside it. Which
			// is why a caller accumulating over a stream has to keep whichever
			// half it has already seen.
			name:      "anthropic stream, message_delta",
			body:      `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":98}}`,
			wantModel: "",
			wantUsage: telemetry.TokenUsage{Output: 98},
		},
		{
			name:      "a model with no usage at all",
			body:      `{"model":"gpt-4o","choices":[]}`,
			wantModel: "gpt-4o",
		},
		{
			name: "an error response accounts for nothing",
			body: `{"type":"error","error":{"type":"invalid_request_error"}}`,
		},
		{
			// A count that arrived as a float, or as a string, is not a count.
			// Guessing would put a wrong number on a bill.
			name:      "unreadable counts are ignored rather than guessed",
			body:      `{"model":"gpt-4o","usage":{"prompt_tokens":"lots","completion_tokens":null}}`,
			wantModel: "gpt-4o",
		},
		{
			// A field nobody models shows up as a gap in the total, which is
			// visible. Folding it into input would show up as a wrong bill, which
			// is not.
			name:      "an unknown count is ignored",
			body:      `{"model":"gpt-4o","usage":{"prompt_tokens":10,"reasoning_tokens":500}}`,
			wantModel: "gpt-4o",
			wantUsage: telemetry.TokenUsage{Input: 10},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := decodeJSONBody([]byte(tt.body))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}

			model, usage := usageFrom(doc)
			if model != tt.wantModel {
				t.Errorf("model = %q, want %q", model, tt.wantModel)
			}
			if usage != tt.wantUsage {
				t.Errorf("usage = %+v, want %+v", usage, tt.wantUsage)
			}
		})
	}
}

// A very large count must survive. Ten million tokens through a float comes back
// as 1e+07 and truncates, which is why the decoder keeps numbers as written.
func TestUsageSurvivesLargeCounts(t *testing.T) {
	doc, err := decodeJSONBody([]byte(`{"model":"m","usage":{"input_tokens":12345678901}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, usage := usageFrom(doc); usage.Input != 12345678901 {
		t.Errorf("input = %d, want 12345678901", usage.Input)
	}
}
