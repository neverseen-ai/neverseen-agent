package proxy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
)

// proveNonceBytes is the one length of nonce /prove signs.
const proveNonceBytes = 32

// proveRequest is a client's challenge: a nonce it chose, sent before anything else.
type proveRequest struct {
	Nonce string `json:"nonce"`
}

// proveReply answers it.
type proveReply struct {
	Proof string `json:"proof"`
}

// handleProve shows a client that this agent holds the control key, without the client
// sending it: the proof is an HMAC, under the key, of the client's nonce and the port
// the request reached.
//
// The browser extension cannot ask the kernel who listens, as agentClient does, and TCP
// on loopback carries no identity: on a machine with several accounts, whoever bound
// the agent's port first received the key and every text the extension sent to be
// masked. So the extension asks this first, and sends nothing to a listener that cannot
// answer it.
//
// The port is this socket's own, never the Host header: a listener holding the usual
// port that relayed the question to the real agent on another one gets the proof for
// that other port, which the extension refuses. The Host is the caller's to write.
//
// Open — needing no key is its point — and harmless so: an HMAC under a 256-bit key
// gives nothing of the key away, and the nonce is held to one shape, so the route signs
// nothing a caller chose beyond thirty-two bytes.
func (s *Server) handleProve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "neverseen: use POST with a nonce", http.StatusMethodNotAllowed)
		return
	}
	if s.controlKey == "" {
		http.Error(w, "neverseen: no control key on this agent, so it has nothing to prove",
			http.StatusServiceUnavailable)
		return
	}

	var req proveRequest
	if !decodeExtensionBody(w, r, &req) {
		return
	}
	if raw, err := hex.DecodeString(req.Nonce); err != nil || len(raw) != proveNonceBytes {
		http.Error(w, "neverseen: the nonce is not 32 bytes of hex", http.StatusBadRequest)
		return
	}

	// Set by net/http on every request served over a connection.
	here, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		http.Error(w, "neverseen: cannot tell which address this request reached", http.StatusInternalServerError)
		return
	}
	_, port, err := net.SplitHostPort(here.String())
	if err != nil {
		http.Error(w, "neverseen: cannot tell which port this request reached", http.StatusInternalServerError)
		return
	}
	writeJSON(w, proveReply{Proof: proofOf(s.controlKey, req.Nonce, port)})
}

// proofOf is the one definition of the proof. extension/src/agent.ts computes the same
// thing, and the contract file holds the two to agreeing.
func proofOf(key, nonce, port string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte("neverseen-prove\x00" + nonce + "\x00" + port))
	return hex.EncodeToString(mac.Sum(nil))
}
