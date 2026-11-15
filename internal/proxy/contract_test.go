package proxy

import (
	"context"
	"encoding/json"
	"flag"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
)

// The contract between this agent and the browser extension, as one file both sides
// read.
//
// This is the test the same-repository decision exists to make possible. The
// extension and the agent speak a protocol; in two repositories that protocol drifts,
// which is the class of failure this project documents everywhere else. Here the
// exchanges are recorded once, in a file the extension's own suite reads to drive its
// client and this one reads to drive the real agent — so a field renamed on either
// side fails on the other, in the same commit.
//
// The two halves check different things and neither is enough alone:
//
//   - this one: given the recorded request, the real detector and the real vault
//     answer the recorded response, exactly;
//   - the extension's: given the recorded response, its client parses what it
//     expects, and given the same inputs it produces the recorded request, exactly.
//
// The file is regenerated with `make contract-update`, deliberately as awkward as
// `make score-update`: a fixture that rewrites itself on every run agrees with
// whatever the code does, including a field it silently stopped sending.

const contractPath = "../../extension/testdata/contract.json"

var updateContract = flag.Bool("update-contract", false,
	"rewrite the recorded exchanges from this run — explain the delta in the PR")

type contractFile struct {
	Comment   string             `json:"comment"`
	Detector  contractDetector   `json:"detector"`
	Session   string             `json:"session"`
	Exchanges []contractExchange `json:"exchanges"`
}

// contractDetector pins what the agent must be configured as for the recorded
// answers to be the right ones.
//
// Recorded rather than assumed, because every one of them changes what comes back: a
// different locale set recognises different values, and fake mode replaces them with
// prose rather than tokens.
type contractDetector struct {
	Locales      []string `json:"locales"`
	Substitution string   `json:"substitution"`
	SecretLevel  string   `json:"secret_level"`
}

type contractExchange struct {
	Name string `json:"name"`

	// Why is what this exchange is here to hold. Written by hand and never
	// regenerated: an exchange nobody can say the purpose of is one somebody will
	// delete the next time it fails.
	Why string `json:"why"`

	// Substitution, when set, switches the agent to that mode before this exchange
	// and leaves it there. The client is the same in either mode; the agent is not,
	// and fake mode is where a held-back tail spans several words.
	Substitution string `json:"substitution,omitempty"`

	Route    string          `json:"route"`
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response"`
}

// TestExtensionContract replays every recorded exchange against a real agent.
//
// One agent for the whole file, in order: the exchanges build on each other, because
// that is what a conversation does. The token an /unmask exchange expands is the one
// an earlier /mask exchange minted, and replaying them independently would test a
// protocol nothing speaks.
func TestExtensionContract(t *testing.T) {
	file := loadContract(t)

	det := detector.New(detector.Config{Locales: file.Detector.Locales})
	mode, err := detector.ParseSubstitution(file.Detector.Substitution)
	if err != nil {
		t.Fatal(err)
	}
	det.SetSubstitution(mode)
	level, err := detector.ParseSecretLevel(file.Detector.SecretLevel)
	if err != nil {
		t.Fatal(err)
	}
	det.SetSecretLevel(level)

	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{
		Providers:  []Provider{{Code: "anthropic", BaseURL: "https://example.invalid"}},
		ControlKey: testControlKey,
	}, det, v)
	if err != nil {
		t.Fatal(err)
	}
	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)

	live := make([]json.RawMessage, len(file.Exchanges))
	for i, exchange := range file.Exchanges {
		if exchange.Substitution != "" {
			// On the detector rather than through PUT /policy, which would also purge
			// the mappings: nothing after the switch names a token minted before it,
			// so the purge would change no answer, and the extension never sends one.
			mode, err := detector.ParseSubstitution(exchange.Substitution)
			if err != nil {
				t.Fatalf("%s: %v", exchange.Name, err)
			}
			det.SetSubstitution(mode)
		}
		var got reply
		if exchange.Route == "/prove" {
			got = proveOver(t, srv, DefaultListen, string(exchange.Request))
		} else {
			got = postAs(t, agent, exchange.Route, testControlKey, file.Session, string(exchange.Request))
		}
		if got.status != 200 {
			t.Fatalf("%s: status %d: %s", exchange.Name, got.status, got.body)
		}
		live[i] = json.RawMessage(got.body)

		if *updateContract {
			continue
		}
		if !sameJSON(t, exchange.Response, live[i]) {
			t.Errorf("%s\n  why:  %s\n  sent: %s\n  want: %s\n  got:  %s",
				exchange.Name, exchange.Why, exchange.Request, exchange.Response, got.body)
		}
	}

	if *updateContract {
		writeContract(t, file, live)
	}
}

// proveOver answers a /prove request as reached on addr. The proof is bound to the port
// the request reached, and the extension's half checks it against the port it dials —
// DEFAULT_BASE_URL's, which is this one's — where a test server's would be random.
func proveOver(t *testing.T, srv *Server, addr, body string) reply {
	t.Helper()

	here, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/prove", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, here))
	rec := httptest.NewRecorder()
	srv.handleProve(rec, req)
	return reply{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
}

// TestContractCoversWhatMatters holds the file itself to a standard.
//
// A contract file is only worth what it exercises, and it is the kind of fixture that
// quietly loses a case: somebody deletes a failing exchange, regenerates, and the
// suite goes green over a protocol nobody checks any more. So the shapes that must be
// in it are named here rather than left to whoever last edited the JSON.
func TestContractCoversWhatMatters(t *testing.T) {
	file := loadContract(t)

	seen := map[string]bool{}
	for _, e := range file.Exchanges {
		if e.Why == "" {
			t.Errorf("exchange %q says nothing about what it holds", e.Name)
		}
		seen[e.Route] = true
	}
	for _, route := range []string{"/mask", "/unmask", "/prove"} {
		if !seen[route] {
			t.Errorf("nothing in the contract exercises %s", route)
		}
	}

	// A split replacement and a final flush: the two cases the tail exists for, and
	// the two a client would pass without if it simply never carried one.
	var carriesTail, isFinal bool
	for _, e := range file.Exchanges {
		if e.Route != "/unmask" {
			continue
		}
		var req unmaskRequest
		if err := json.Unmarshal(e.Request, &req); err != nil {
			t.Fatalf("%s: %v", e.Name, err)
		}
		if req.Tail != "" {
			carriesTail = true
		}
		if req.Final {
			isFinal = true
		}
	}
	if !carriesTail {
		t.Error("no recorded exchange carries a tail, so the split-replacement case is untested")
	}
	if !isFinal {
		t.Error("no recorded exchange ends the stream, so the flush is untested")
	}

	// A stand-in held back across words — the reason TailLen is not TokenTailLen.
	// Recorded in token mode alone, a client that trimmed or split a tail on its
	// spaces would pass every exchange here.
	var multiWordTail bool
	for _, e := range file.Exchanges {
		var req unmaskRequest
		if e.Route == "/unmask" && json.Unmarshal(e.Request, &req) == nil &&
			strings.Contains(strings.TrimSpace(req.Tail), " ") {
			multiWordTail = true
		}
	}
	if !multiWordTail {
		t.Error("no recorded exchange carries a tail of several words, so fake mode's stand-ins are untested across the seam")
	}
}

func loadContract(t *testing.T) contractFile {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(contractPath))
	if err != nil {
		t.Fatal(err)
	}
	var file contractFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("%s: %v", contractPath, err)
	}
	return file
}

func writeContract(t *testing.T, file contractFile, live []json.RawMessage) {
	t.Helper()

	for i := range file.Exchanges {
		compact := &json.RawMessage{}
		if err := json.Unmarshal(live[i], compact); err != nil {
			t.Fatal(err)
		}
		file.Exchanges[i].Response = *compact
	}

	encoded, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contractPath, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("rewrote %s from this run", contractPath)
}

// sameJSON compares two documents by value rather than by bytes, so whitespace and
// key order in the file are not part of the contract. What is in it — a field
// present, a field absent, a value — is.
func sameJSON(t *testing.T, want, got json.RawMessage) bool {
	t.Helper()

	var a, b any
	if err := json.Unmarshal(want, &a); err != nil {
		t.Fatalf("the recorded response is not JSON: %v", err)
	}
	if err := json.Unmarshal(got, &b); err != nil {
		t.Fatalf("the agent answered something that is not JSON: %v", err)
	}
	return reflect.DeepEqual(a, b)
}
