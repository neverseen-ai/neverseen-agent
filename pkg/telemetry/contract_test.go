package telemetry

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// The claim this package makes is that a heartbeat carries no content. A comment
// saying so is worth nothing: the field that breaks it will be added by somebody
// who read the comment and thought their case was different.
//
// So the type itself is checked. Every string that can reach the wire has to be
// on the list below, with a reason. A new one fails this test until somebody
// argues for it in a diff a reviewer can see — which is the only point at which
// "should the backend know this?" gets asked out loud.
var allowedStrings = map[string]string{
	"agent_id":                    "an identity the backend itself issued",
	"state.version":               "the agent build, which is the point of reporting state",
	"state.platform":              "the operating system and architecture",
	"state.substitution":          "which of two modes is running: token or fake",
	"state.locales[]":             "country codes from the agent's own registry",
	"state.providers[]":           "upstream codes from the agent's own list",
	"state.addresses[]":           "the machine's own IP addresses — personal data, and the one field here that is; see the field's comment",
	"state.masking":               "how much of the catalogue is applied: full, partial or none",
	"state.switched_off[]":        "category names from the agent's own catalogue, never a value — the same strings counters.masked already keys on",
	"state.secret_level":          "one of three fixed words — weak, medium, strong — chosen from a closed set the agent itself parses, never a value",
	"buckets[].counters.masked{}": "category names from the agent's own catalogue, never a value",
	"buckets[].counters.models{}": "the model id the provider reported, which is a product name",
}

func TestHeartbeatCarriesNoContent(t *testing.T) {
	// The type an agent actually posts, so a bucket's own fields are walked at the
	// path they reach the wire under. Walking Heartbeat instead would check a shape
	// nothing sends and miss anything a bucket gained.
	var found []string
	stringPaths(reflect.TypeOf(HeartbeatBatch{}), "", &found)
	sort.Strings(found)

	for _, path := range found {
		if _, ok := allowedStrings[path]; !ok {
			t.Errorf("the heartbeat can carry a string at %q, and it is not on the allow list.\n"+
				"Every string reaching the backend has to be argued for: if it can hold a prompt, a "+
				"detected value, a file name or a URL, it does not belong in this contract. If it "+
				"genuinely cannot, add it to allowedStrings with the reason.", path)
		}
	}

	// The other direction: an entry for a field that no longer exists is a rule
	// nobody is checking, and it makes the list look more thorough than it is.
	for path := range allowedStrings {
		if !slices.Contains(found, path) {
			t.Errorf("allowedStrings names %q, which the heartbeat no longer carries — remove it", path)
		}
	}
}

// stringPaths records every place a string can appear in a type, by its JSON
// path. Slices are marked "[]" and map keys "{}", so a reviewer reading a failure
// can tell a field from a key.
func stringPaths(t reflect.Type, prefix string, out *[]string) {
	// A timestamp marshals as a string and carries no content. Walking into it
	// would report its unexported fields, which are not part of the contract.
	if t == reflect.TypeOf(time.Time{}) {
		return
	}

	switch t.Kind() {
	case reflect.String:
		*out = append(*out, prefix)

	case reflect.Pointer:
		stringPaths(t.Elem(), prefix, out)

	case reflect.Slice, reflect.Array:
		stringPaths(t.Elem(), prefix+"[]", out)

	case reflect.Map:
		// Both halves: a map keyed by a category name and a map holding one are
		// different risks, and both reach the wire.
		stringPaths(t.Key(), prefix+"{}", out)
		stringPaths(t.Elem(), prefix+"{}.value", out)

	case reflect.Struct:
		for i := range t.NumField() {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			name := jsonName(field)
			if name == "-" {
				continue // never marshalled, so never on the wire
			}
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			stringPaths(field.Type, path, out)
		}
	}
}

func jsonName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag == "" {
		return strings.ToLower(field.Name)
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return strings.ToLower(field.Name)
	}
	return name
}

// The wire format, pinned to a file the backend repository reads too.
//
// It is the only thing both sides share, so a change to it that only one side
// knows about is the failure mode this guards: a field renamed here and read
// under its old name there fails no compiler and no unit test, and shows up as a
// dashboard column that is quietly always zero.
const goldenPath = "testdata/heartbeats.json"

var updateGolden = flag.Bool("update-golden", false, "rewrite "+goldenPath+" from this run")

func TestHeartbeatWireFormat(t *testing.T) {
	// Fixed values throughout: a golden file built from time.Now() pins nothing.
	at := time.Date(2026, 8, 25, 14, 30, 0, 0, time.UTC)

	batch := HeartbeatBatch{
		Schema:  SchemaVersion,
		AgentID: "agt_7f3c9a21",
		SentAt:  at,
		State: State{
			Version:      "1.4.2",
			Platform:     "darwin/arm64",
			StartedAt:    at.Add(-72 * time.Hour),
			Locales:      []string{"fr", "gb"},
			Substitution: "token",
			Providers:    []string{"anthropic", "openai"},
			// Present in the example on purpose. The field is omitempty, so an
			// example that left it out would be a field neither repository ever
			// tested on the wire — which is exactly the drift this golden exists to
			// catch. One v4 and one v6, both from documentation ranges (RFC 5737,
			// RFC 3849), so nothing here is a real machine anywhere.
			Addresses: []string{"192.0.2.47", "2001:db8::47"},

			// Both present for the same reason as the addresses above: they are
			// omitempty, so an example that left them out would be two fields
			// neither repository ever tested on the wire. And "partial" is the state
			// worth pinning — "full" would leave switched_off empty, so the plural
			// half of this pair would go untested exactly as it does when the whole
			// field is missing.
			Masking:     "partial",
			SwitchedOff: []string{"IP_ADDRESS", "MONGO_ID"},

			// "strong" rather than "weak" for the reason "partial" is pinned above:
			// weak is the level an agent starts at, so an example carrying it would
			// exercise nothing a zero value does not. Strong is also the state worth
			// showing a backend — an agent masking less than its catalogue allows
			// with nothing switched off to explain it.
			SecretLevel: "strong",
		},
		// Two buckets, not one. A batch of one would be a golden in which the
		// plural case — the whole reason the message is a batch — never appears,
		// and a backend that only ever read the first element would pass.
		Buckets: []Bucket{
			{
				Window: Window{Start: at.Add(-10 * time.Minute), End: at.Add(-5 * time.Minute)},
				Counters: Counters{
					Requests: 128,
					Masked: map[string]int{
						"EMAIL":             41,
						"NIR":               3,
						"SECRET_OPENAI_KEY": 1,
					},
					Models: map[string]TokenUsage{
						"claude-sonnet-4": {
							Input:      1204,
							Output:     22871,
							CacheWrite: 18430,
							CacheRead:  184302,
						},
						"gpt-4o": {Input: 9140, Output: 1205},
					},
					// Both omitempty, and in the example on purpose: a field neither
					// repository ever saw on the wire is exactly the drift this
					// golden exists to catch.
					Restarts: 2,
					Dropped:  2,
				},
			},
			{
				Window:   Window{Start: at.Add(-5 * time.Minute), End: at},
				Counters: Counters{Requests: 7},
			},
		},
	}

	encoded, err := json.MarshalIndent(batch, "", "  ")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	encoded = append(encoded, '\n')

	if *updateGolden {
		if err := os.WriteFile(filepath.Clean(goldenPath), encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s", goldenPath)
	}

	// Compared against the embedded copy rather than by re-reading the file, so
	// that what the backend imports and what this test checks cannot differ.
	if string(encoded) != ExampleHeartbeatsJSON {
		t.Errorf("the wire format changed.\n got:\n%s\nwant:\n%s\n\n"+
			"If the change is intended, run with -update-golden and say in the commit what a "+
			"backend on the old format will do with the new one.", encoded, ExampleHeartbeatsJSON)
	}

	// And it has to survive the round trip, or the backend reads something the
	// agent did not mean.
	var back HeartbeatBatch
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(batch, back) {
		t.Errorf("the batch does not round-trip through JSON:\n got %+v\nwant %+v", back, batch)
	}

	// And expanding it gives the shared fields to every bucket. A backend reading
	// the first element's window for all of them would otherwise pass every test
	// here and file a week of buckets under one five-minute period.
	windows := batch.Windows()
	if len(windows) != len(batch.Buckets) {
		t.Fatalf("Windows gave %d reports for %d buckets", len(windows), len(batch.Buckets))
	}
	for i, one := range windows {
		if one.AgentID != batch.AgentID || one.Schema != batch.Schema || !one.SentAt.Equal(batch.SentAt) {
			t.Errorf("report %d does not carry the batch's own identity: %+v", i, one)
		}
		if !one.Window.Start.Equal(batch.Buckets[i].Window.Start) {
			t.Errorf("report %d covers from %v, want %v", i,
				one.Window.Start, batch.Buckets[i].Window.Start)
		}
		if one.Counters.Requests != batch.Buckets[i].Counters.Requests {
			t.Errorf("report %d carries %d requests, want %d", i,
				one.Counters.Requests, batch.Buckets[i].Counters.Requests)
		}
	}
}
