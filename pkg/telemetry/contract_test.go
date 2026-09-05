package telemetry

import (
	"encoding/json"
	"flag"
	"fmt"
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
	"agent_id":                            "an identity the backend itself issued",
	"state.version":                       "the agent build, which is the point of reporting state",
	"state.platform":                      "the operating system and architecture",
	"state.substitution":                  "which of two modes is running: token or fake",
	"state.locales[]":                     "country codes from the agent's own registry",
	"state.providers[]":                   "upstream codes from the agent's own list",
	"state.addresses[]":                   "the machine's own IP addresses — personal data, and the one field here that is; see the field's comment",
	"state.masking":                       "how much of the catalogue is applied: full, partial or none",
	"state.switched_off[]":                "category names from the agent's own catalogue, never a value — the same strings counters.masked already keys on",
	"state.secret_level":                  "one of three fixed words — weak, medium, strong — chosen from a closed set the agent itself parses, never a value",
	"buckets[].counters.masked{}":         "category names from the agent's own catalogue, never a value",
	"buckets[].counters.models{}":         "the model id the provider reported, which is a product name",
	"state.rerouted[]":                    "provider codes from the agent's own list — the same strings state.providers[] carries — naming which routes were pointed away from the vendor",
	"buckets[].counters.providers{}":      "provider codes from the agent's own list, the same strings state.providers[] carries",
	"buckets[].counters.clients{}":        "a client family from KnownClients, or \"other\" — a User-Agent is reduced to the closed list and never travels",
	"buckets[].counters.tools.names{}":    "a tool name from KnownTools, or \"other\" — never a name a client made up",
	"buckets[].counters.tools.programs{}": "a program name from KnownPrograms, or \"other\" — the word comes from the list, not from the command line",
	"buckets[].counters.tools.classes{}":  "a class from CommandClasses, a fact about the vocabulary and never about the text",
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

			// The exposure of the agent itself, all set, for the reason every
			// omitempty field above is: an example in which none of them is true
			// exercises none of them on the wire.
			Console:     true,
			Tracing:     true,
			Exposed:     true,
			Rerouted:    []string{"anthropic"},
			Allowlisted: 12,
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

					// Every field below is omitempty, and each is exercised here for
					// the reason the two above are.
					Refused:   1,
					Providers: map[string]int{"anthropic": 120, "openai": 8},
					Upstream:  Upstream{OK: 121, RateLimited: 3, Rejected: 2, Failed: 1, Unreachable: 1},
					Clients:   map[string]int{"claude-code": 120, "openai-sdk": 7, Other: 1},
					Policy:    PolicyChanges{Applied: 1, Refused: 1},
					Degraded:  1,
					Sessions: Sessions{
						Active:    4,
						Opened:    1,
						Closed:    2,
						Duration:  histogramOf(1800, 42),
						Requests:  histogramOf(60, 3),
						Input:     histogramOf(1_500_000, 9000),
						Output:    histogramOf(40_000, 800),
						ToolCalls: histogramOf(55, 0),
						Masked:    histogramOf(12, 0),
					},
					Tools: Tools{
						Calls:    55,
						Restored: 4,
						Names:    map[string]int{"Bash": 30, "Read": 20, "mcp": 3, Other: 2},
						Programs: map[string]int{"git": 12, "grep": 9, "python3": 4, Other: 5},
						Classes:  map[string]int{"network": 2, "privilege": 1},
					},
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

// The pair the whole contract rests on: the agent signs, the backend verifies, and
// signing lives here precisely so there is one implementation rather than two.
//
// Untested until now, which is the wrong state for a signature check — a
// VerifySignature that returned true for everything would pass every other test in
// this tree and make the header decorative, and one that returned false for
// everything would reject every heartbeat with each side convinced it was right.
// Both halves are asserted here, because either alone is meaningless.
func TestSignAndVerify(t *testing.T) {
	key := []byte("a key that is not a real one")
	body := []byte(`{"schema":1,"agent_id":"agent-1"}`)

	signature := Sign(key, body)
	if signature == "" {
		t.Fatal("Sign produced nothing")
	}
	if !VerifySignature(key, body, signature) {
		t.Error("a signature this package produced does not verify under the same key")
	}

	// Deterministic, because the agent signs and the backend verifies in two
	// processes: a signature that varied per call could never be checked.
	if again := Sign(key, body); again != signature {
		t.Errorf("signing the same body twice gave %q then %q", signature, again)
	}

	for name, tc := range map[string]struct {
		key       []byte
		body      []byte
		signature string
	}{
		// The one that matters: without it, one workstation could file reports as
		// another simply by naming its agent id.
		"another agent's key":         {[]byte("a different key"), body, signature},
		"a body that changed":         {key, append(body, ' '), signature},
		"a signature that is not hex": {key, body, "not hex"},
		// Truncated rather than wrong: hmac.Equal has to refuse a short digest
		// rather than compare the prefix it was given.
		"a truncated signature": {key, body, signature[:len(signature)-2]},
		"no signature at all":   {key, body, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if VerifySignature(tc.key, tc.body, tc.signature) {
				t.Error("verified, so the header is not binding anything")
			}
		})
	}
}

// histogramOf is a Histogram holding the given values.
func histogramOf(values ...int) Histogram {
	var h Histogram
	for _, v := range values {
		h.Add(v)
	}
	return h
}

func TestHistogramBucketsByPowerOfTwo(t *testing.T) {
	cases := map[int]int{
		0: 0, 1: 1, 2: 2, 3: 2, 4: 3, 7: 3, 8: 4, 1000: 10, 1 << 22: 23, 1 << 30: 23,
	}
	for v, want := range cases {
		var h Histogram
		h.Add(v)
		if len(h) != HistogramBuckets {
			t.Fatalf("Add(%d) made a histogram of %d buckets, want %d", v, len(h), HistogramBuckets)
		}
		if h[want] != 1 {
			t.Errorf("Add(%d) landed in bucket %d, want %d: %v", v, indexOfOne(h), want, h)
		}
	}
}

func indexOfOne(h Histogram) int {
	for i, n := range h {
		if n == 1 {
			return i
		}
	}
	return -1
}

// A backlog goes to the backend sixty buckets at a time, and the backend reads a
// body under a bound (cloakfleet-cloud's ingest.maxBody, 1 MiB). The counters
// gained histograms and four maps, so a bucket is several times the size it was;
// this holds a full batch at half the bound, so the field that would break it is
// the one that still gets to choose the number — the /healthz lesson,
// TestHealthPayloadFitsTheQueryBound, applied to the other payload.
func TestAFullBatchFitsTheBackendsBodyBound(t *testing.T) {
	var golden HeartbeatBatch
	if err := json.Unmarshal([]byte(ExampleHeartbeatsJSON), &golden); err != nil {
		t.Fatal(err)
	}
	full := golden.Buckets[0]
	// Every map at a plausible ceiling: a workstation talking to every vendor,
	// through every client, masking every category, running every program.
	for _, code := range []string{"anthropic", "openai", "gemini", "mistral", "groq", "together", "deepinfra", "xai"} {
		full.Counters.Providers[code] = 1
		full.Counters.Models["model-"+code+"-with-a-long-version-suffix-2026"] = TokenUsage{Input: 1, Output: 1, CacheWrite: 1, CacheRead: 1}
	}
	for _, c := range KnownClients {
		full.Counters.Clients[c] = 1
	}
	for _, name := range KnownTools {
		full.Counters.Tools.Names[name] = 1
	}
	for _, p := range KnownPrograms {
		full.Counters.Tools.Programs[p] = 1
	}
	for _, c := range CommandClasses {
		full.Counters.Tools.Classes[c] = 1
	}
	for i := range 200 {
		full.Counters.Masked[fmt.Sprintf("CATEGORY_%03d", i)] = 1
	}

	batch := golden
	batch.Buckets = make([]Bucket, 60)
	for i := range batch.Buckets {
		batch.Buckets[i] = full
	}
	encoded, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	const bound = 1024 * 1024
	if len(encoded) > bound/2 {
		t.Errorf("a full batch of 60 buckets encodes to %d bytes, over half the backend's %d-byte bound", len(encoded), bound)
	}
}
