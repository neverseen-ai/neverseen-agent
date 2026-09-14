package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
)

// The two routes a caller that is not a proxy hop uses: text in, masked text out,
// and the reverse.
//
// They exist for the browser extension, which cannot be a proxy hop at all. A web
// chat's traffic goes to the site's own origin over a connection the page opened,
// so there is nothing to point at this agent; what the extension can do is wrap the
// page's fetch, hand the text here on the way out and hand the answer here on the
// way back.
//
// What is deliberately *not* here is an engine. The extension holds no catalogue, no
// policy and no mappings: one engine, one policy, one set of surfaces. A WASM build
// or a TypeScript rewrite would be a second answer to "what does this agent mask",
// which is the drift this project's one-entrypoint rule exists to prevent — and the
// corpus and its lessons do not transfer to JavaScript regexes, which have neither
// RE2's semantics nor its guarantees.
//
// Both are authenticated with the same control key PUT /policy uses. /unmask needs
// it most: it reads the session mapping, token in and original out, which is a
// present leak where switching masking off is a future one. /mask is authenticated
// too — it exposes what /test exposes, and one rule for the pair is one rule to get
// right.

// extensionMaxBytes caps a request body on these routes.
//
// A megabyte because a chat message carrying a pasted file is a real thing and 32 kB
// — what the /test form takes — would refuse it, while the catalogue is dozens of
// expressions run over whatever arrives and an unbounded read on an authenticated
// local route is still a way to spend this agent's memory by accident.
const extensionMaxBytes = 1 << 20

// maskRequest is what a caller sends to have text masked.
//
// A list rather than one string, and that is the whole reason this route is not a
// loop over a single-text one: a message being sent carries several fields, and
// masking them in one pass is what gives a value repeated across two of them one
// identity. Masked separately, the same address would leave as two different people.
type maskRequest struct {
	Texts []string `json:"texts"`
}

// maskReply carries the masked texts back, positionally.
type maskReply struct {
	Texts []string `json:"texts"`

	// Masked is how many values were replaced, repeats included — the same number
	// the log line carries, so a caller can show it without asking what was in it.
	Masked int `json:"masked"`
}

// handleMask masks text with the deployment's own detector, in the caller's session.
func (s *Server) handleMask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "neverseen: use POST to mask text", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorised(w, r) {
		return
	}

	var req maskRequest
	if !decodeExtensionBody(w, r, &req) {
		return
	}

	// The session is named by the header the request path already reads, and it has
	// to be: /mask and /unmask for one conversation must name the same session, or
	// the expansion finds nothing.
	session := sessionOf(r)
	pass := s.det.NewPass(s.vault.Load(session))

	out := make([]string, len(req.Texts))
	count := 0
	for i, text := range req.Texts {
		masked, n := s.det.Mask(text, pass)
		out[i] = masked
		count += n
	}

	if err := s.vault.Save(session, pass.Minted()); err != nil {
		// The same reason the request path fails closed on this: masking without
		// storing hands the caller replacements nothing can ever expand, so it would
		// send the model text it cannot read back.
		s.log.Error("storing the session mapping failed, refusing to answer",
			"session", session, "error", err)
		http.Error(w, "neverseen: the session mapping could not be stored", http.StatusInternalServerError)
		return
	}

	// Counted like any other masking this agent did. An extension exchange is values
	// that did not leave the workstation, and leaving it out would show a fleet view
	// a machine doing nothing while every browser chat on it was being masked.
	s.recorder.Request(session, "extension", "")
	if count > 0 {
		s.recorder.Masked(session, pass.Counts())

		// Counts, never content — the rule holds on this route as on every other.
		s.log.Info("text masked", "session", session, "values", count, "minted", len(pass.Minted()))
	}

	writeJSON(w, maskReply{Texts: out, Masked: count})
}

// unmaskRequest is one chunk of a streamed answer, plus what the caller is carrying.
//
// The tail travels with the client rather than being held here, which is what keeps
// this route stateless: two conversations in two tabs share one agent, and a held
// tail would be one more thing keyed by a session name the caller chooses.
type unmaskRequest struct {
	// Text is the chunk as it arrived.
	Text string `json:"text"`

	// Tail is what the previous call held back, prepended to Text before anything
	// is expanded. Empty on the first chunk.
	Tail string `json:"tail"`

	// Final ends the stream: the remaining tail is expanded and nothing is held
	// back. Without it a value masked at the very end of an answer is truncated on
	// screen, which is the streaming path's flush arrived at from the other side.
	Final bool `json:"final"`
}

// unmaskReply is what to show and what to carry into the next call.
type unmaskReply struct {
	Expanded string `json:"expanded"`

	// Tail is what must be prepended to the next chunk. Always empty when the
	// request was final.
	Tail string `json:"tail"`
}

// handleUnmask puts the originals back into one chunk of a streamed answer.
func (s *Server) handleUnmask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "neverseen: use POST to expand masked text", http.StatusMethodNotAllowed)
		return
	}

	// Loopback only, hard, whatever -l bound — and checked before the key is even
	// looked at, so a remote caller never gets to present one over clear HTTP.
	//
	// The warn-rather-than-refuse reasoning that lets /healthz and /test be reachable
	// does not transfer. Those describe a configuration; this one answers "what does
	// this replacement stand for", which is the whole mapping one question at a time.
	// There is no legitimate remote caller for it.
	//
	// TODO: a browser in a VM talking to the agent on its host is a real shape this
	// refuses. The upgrade path is TLS with a certificate the extension pins, or an
	// explicit opt-in that says out loud what it costs — not a relaxation of this.
	if !fromLoopback(r.RemoteAddr) {
		http.Error(w, "neverseen: this route answers on the loopback interface only",
			http.StatusForbidden)
		return
	}
	if !s.authorised(w, r) {
		return
	}

	var req unmaskRequest
	if !decodeExtensionBody(w, r, &req) {
		return
	}

	session := sessionOf(r)
	known := s.vault.Load(session)

	// Held back before anything is expanded, in that order, exactly as the
	// streaming rehydrator does it — and for the same reason. Expanded first, a
	// chunk ending on a stand-in that is the prefix of another is replaced there
	// and then, with the wrong original, and the tail handed back to the client is
	// sliced out of already-restored text: a fragment of a real original travels
	// back to the page, is prepended to the next chunk and expanded again. This
	// route exists so that a value at the very end of an answer is not truncated on
	// screen, and in the other order it was the mechanism doing the truncating.
	body := req.Tail + req.Text

	// One prepared mapping for the two questions this route asks of it.
	expander := detector.NewExpander(known)

	tail := ""
	if !req.Final {
		// The same held-back tail the streaming rehydrator computes, from the same
		// function: a stand-in splits across two chunks exactly as a token does, and a
		// caller answering that question for itself in JavaScript would be a second
		// answer to it.
		if n := expander.TailLen(body); n > 0 {
			tail, body = body[len(body)-n:], body[:len(body)-n]
		}
	}

	// nil rather than the audit console's callback, and that is structural rather
	// than an omission: -a and -v record the proxy's exchanges, and a trace of this
	// route would put originals on disk through a path the invariant never
	// considered. See TestExtensionRoutesAreNotAudited.
	writeJSON(w, unmaskReply{Expanded: expander.Unmask(body, nil), Tail: tail})
}

// fromLoopback reports whether a request came from this machine.
//
// Asked of the source address rather than of the bind address, which is a different
// question from BeyondLoopback: an agent bound to 0.0.0.0 still answers loopback
// callers, and those are the ones this route exists for.
//
// A RemoteAddr that will not parse is refused. It is not a shape net/http produces,
// and guessing "probably local" on the one route that reads the mapping is the wrong
// direction to guess in.
func fromLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// decodeExtensionBody reads and parses a bounded request body, answering the caller
// itself on failure.
func decodeExtensionBody(w http.ResponseWriter, r *http.Request, into any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, extensionMaxBytes+1))
	if err != nil {
		http.Error(w, "neverseen: could not read the request", http.StatusBadRequest)
		return false
	}
	if len(body) > extensionMaxBytes {
		http.Error(w, fmt.Sprintf("neverseen: the request is larger than %d bytes", extensionMaxBytes),
			http.StatusRequestEntityTooLarge)
		return false
	}
	if err := json.Unmarshal(body, into); err != nil {
		http.Error(w, "neverseen: the request is not the JSON object this route takes",
			http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
