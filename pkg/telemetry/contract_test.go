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
	"agent_id":           "an identity the backend itself issued",
	"state.version":      "the agent build, which is the point of reporting state",
	"state.platform":     "the operating system and architecture",
	"state.substitution": "which of two modes is running: token or fake",
	"state.locales[]":    "country codes from the agent's own registry",
	"state.providers[]":  "upstream codes from the agent's own list",
	"state.addresses[]":  "the machine's own IP addresses — personal data, and the one field here that is; see the field's comment",
	"counters.masked{}":  "category names from the agent's own catalogue, never a value",
	"counters.models{}":  "the model id the provider reported, which is a product name",
}

func TestHeartbeatCarriesNoContent(t *testing.T) {
	var found []string
	stringPaths(reflect.TypeOf(Heartbeat{}), "", &found)
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
const goldenPath = "testdata/heartbeat.json"

var updateGolden = flag.Bool("update-golden", false, "rewrite "+goldenPath+" from this run")

func TestHeartbeatWireFormat(t *testing.T) {
	// Fixed values throughout: a golden file built from time.Now() pins nothing.
	at := time.Date(2026, 8, 25, 14, 30, 0, 0, time.UTC)

	hb := Heartbeat{
		Schema:  SchemaVersion,
		AgentID: "agt_7f3c9a21",
		SentAt:  at,
		Window:  Window{Start: at.Add(-5 * time.Minute), End: at},
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
		},
		Counters: Counters{
			Requests: 128,
			Masked:   map[string]int{"EMAIL": 41, "NIR": 3, "SECRET_OPENAI_KEY": 1},
			Models: map[string]TokenUsage{
				// The shape a coding agent actually produces: a little fresh
				// input, a lot of cache read.
				"claude-sonnet-4": {Input: 1_204, Output: 22_871, CacheWrite: 18_430, CacheRead: 184_302},
				"gpt-4o":          {Input: 9_140, Output: 1_205},
			},
			Dropped: 2,
		},
	}

	encoded, err := json.MarshalIndent(hb, "", "  ")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	encoded = append(encoded, '\n')

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o750); err != nil {
			t.Fatalf("create the golden directory: %v", err)
		}
		if err := os.WriteFile(goldenPath, encoded, 0o600); err != nil {
			t.Fatalf("write the golden: %v", err)
		}
		t.Logf("rewrote %s", goldenPath)
	}

	// Compared against the embedded copy rather than by re-reading the file, so
	// that what the backend imports and what this test checks cannot differ.
	if string(encoded) != ExampleHeartbeatJSON {
		t.Errorf("the wire format changed.\n got:\n%s\nwant:\n%s\n\n"+
			"If the change is intended, run with -update-golden and say in the commit what a "+
			"backend on the old format will do with the new one.", encoded, ExampleHeartbeatJSON)
	}

	// And it has to survive the round trip, or the backend reads something the
	// agent did not mean.
	var back Heartbeat
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(hb, back) {
		t.Errorf("the heartbeat does not round-trip through JSON:\n got %+v\nwant %+v", back, hb)
	}
}

// An empty window must not carry empty maps: a backend counting keys would read
// "no categories" and "the field is absent" the same way, and omitempty is what
// keeps a quiet agent's heartbeat small.
func TestEmptyCountersOmitTheirMaps(t *testing.T) {
	encoded, err := json.Marshal(Heartbeat{Schema: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"masked", "models", "dropped"} {
		if strings.Contains(string(encoded), absent) {
			t.Errorf("an empty heartbeat carries %q: %s", absent, encoded)
		}
	}
	// The counts that are always meaningful stay, including at zero: a window
	// with no requests is a fact, not a missing field.
	if !strings.Contains(string(encoded), `"requests":0`) {
		t.Errorf("an empty heartbeat drops its request count: %s", encoded)
	}
}
