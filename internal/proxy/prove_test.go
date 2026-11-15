package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const testNonce = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// proveAt posts a nonce to /prove on a real server, with the Host the caller chose.
func proveAt(t *testing.T, agent *httptest.Server, host, nonce string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, agent.URL+"/prove",
		strings.NewReader(`{"nonce":"`+nonce+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if host != "" {
		req.Host = host
	}
	got := do(t, agent, req)
	return got.status, got.body
}

// The extension cannot ask the kernel who listens, so it asks the listener to show it
// holds the key before anything is sent — and the answer must show that without the
// key being sent either way.
func TestProveShowsTheKeyWithoutReceivingIt(t *testing.T) {
	srv := settingsAgent(t, testControlKey)
	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)
	port := strings.TrimPrefix(agent.URL, "http://127.0.0.1:")

	status, body := proveAt(t, agent, "", testNonce)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	if strings.Contains(body, testControlKey) {
		t.Fatal("the proof carried the key itself")
	}
	var reply proveReply
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if reply.Proof != proofOf(testControlKey, testNonce, port) {
		t.Errorf("proof %q, want the HMAC of the nonce and port %s", reply.Proof, port)
	}
}

// A listener holding the agent's usual port could relay the question to the real agent
// on another one. Bound to the port the request reached — the socket's, not the Host,
// which the relay writes — the answer it gets back is for that other port.
func TestProveIsBoundToThePortTheRequestReached(t *testing.T) {
	srv := settingsAgent(t, testControlKey)
	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)
	u, err := url.Parse(agent.URL)
	if err != nil {
		t.Fatal(err)
	}

	status, body := proveAt(t, agent, "127.0.0.1:9787", testNonce)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	var reply proveReply
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Proof == proofOf(testControlKey, testNonce, "9787") {
		t.Fatal("the proof follows the Host header, so a relay passes for the agent on the port it named")
	}
	if reply.Proof != proofOf(testControlKey, testNonce, u.Port()) {
		t.Errorf("the proof is not for the port the request reached, %s", u.Port())
	}
}

// The route signs without the key, so it signs nothing a caller shapes beyond a nonce.
func TestProveRefusesANonceOfAnotherShape(t *testing.T) {
	srv := settingsAgent(t, testControlKey)
	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)

	for _, nonce := range []string{"", "abcd", strings.Repeat("zz", 32), testNonce + "00"} {
		if status, body := proveAt(t, agent, "", nonce); status != http.StatusBadRequest {
			t.Errorf("nonce %q: status %d, want 400: %s", nonce, status, body)
		}
	}
}

// An agent with no key has nothing to prove, and says so rather than signing with "".
func TestProveRefusesWhenTheAgentHasNoKey(t *testing.T) {
	srv := settingsAgent(t, "")
	agent := httptest.NewServer(srv.Handler())
	t.Cleanup(agent.Close)

	if status, body := proveAt(t, agent, "", testNonce); status != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503: %s", status, body)
	}
}
