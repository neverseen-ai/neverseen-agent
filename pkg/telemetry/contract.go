// Package telemetry is the contract between an agent and a supervision backend.
//
// It lives in the public repository, and the private backend imports it — never
// the other way round. That direction is the whole point: the agent has to
// compile, run and be useful with no backend in existence, so it cannot depend on
// one. It also means anybody can read exactly what a supervised agent sends,
// which is the only honest way to make the claim below.
//
// # Nothing here carries content
//
// A heartbeat is counters and configuration. Not a prompt, not a response, not a
// detected value, not an excerpt, not a file name, not a URL. What the backend
// learns is how many values were masked, in which categories, and how many tokens
// were spent on which model — never what any of them were.
//
// That is a structural guarantee rather than a promise: TestHeartbeatCarriesNoContent
// walks this type and fails on any string field that is not on an explicit list,
// each with a reason. Adding a field that could carry text means arguing for it in
// that list, in a diff a reader can see.
package telemetry

import "time"

// SchemaVersion is the version of this contract.
//
// Sent in every message so a backend serving several agent versions can tell
// them apart, and so an agent talking to an older backend is rejected cleanly
// rather than silently misread.
const SchemaVersion = 1

// Heartbeat is what a supervised agent reports, on a fixed interval.
type Heartbeat struct {
	Schema int `json:"schema"`

	// AgentID is the identity the backend issued at enrolment.
	AgentID string `json:"agent_id"`

	// SentAt is when the agent composed this message. The backend uses it to
	// spot a clock that is wrong and to reject a replayed message; it is not the
	// time the backend received it.
	SentAt time.Time `json:"sent_at"`

	// Window is the period the counters cover. Sent explicitly rather than
	// inferred from the interval, because a heartbeat that failed and was
	// retried covers a longer window than the one it was scheduled for.
	Window Window `json:"window"`

	// State is what the agent is applying right now — the answer to "are my
	// twelve agents all running the French policy?".
	State State `json:"state"`

	// Counters are what happened during the window.
	Counters Counters `json:"counters"`
}

// Window is the period a set of counters covers.
type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// State is the agent's configuration as it is actually running, not as a file
// says it should be.
//
// It exists because "the agent is up" is not the question a security officer has.
// The question is whether it is up *and applying the policy*, and an agent
// running with no locale selected is masking almost nothing while looking
// perfectly healthy.
type State struct {
	// Version is the agent build.
	Version string `json:"version"`

	// Platform is the operating system and architecture, as "darwin/arm64".
	Platform string `json:"platform"`

	// StartedAt is when this process began serving, so the backend can show
	// uptime and spot an agent restarting in a loop.
	StartedAt time.Time `json:"started_at"`

	// Locales are the country pattern sets loaded, by their code.
	Locales []string `json:"locales"`

	// Substitution is what a masked value looks like: "token" or "fake".
	Substitution string `json:"substitution"`

	// Providers are the upstream codes this agent will forward to.
	Providers []string `json:"providers"`
}

// Counters are the tallies for one window.
type Counters struct {
	// Requests is how many requests were proxied.
	Requests int `json:"requests"`

	// Masked counts replaced values per category, keyed by the category's own
	// name — "EMAIL", "NIR", "SECRET_OPENAI_KEY". A category name is a term from
	// the agent's catalogue, not anything observed in the data.
	Masked map[string]int `json:"masked,omitempty"`

	// Models counts tokens per model, keyed by the model id the provider itself
	// reported.
	//
	// Raw counts, never a cost. Converting to money needs a price table per
	// model, and that table belongs to the backend: prices change, and an agent
	// that computed them would have to be redeployed to every workstation each
	// time one did.
	Models map[string]TokenUsage `json:"models,omitempty"`

	// Dropped counts heartbeats this agent had to discard because the backend
	// was unreachable for longer than its buffer holds.
	//
	// Reported rather than hidden: a gap in the record is exactly what an
	// auditor needs to see, and a silently lost window looks identical to a
	// window in which nothing happened.
	Dropped int `json:"dropped,omitempty"`
}

// TokenUsage is what one model consumed.
//
// Four counts rather than two, because they are priced differently and a single
// "input" figure would be wrong by a lot. A coding agent re-sends the same long
// context on every turn, so almost all of its input is a cache read — cheaper
// than a fresh token by an order of magnitude, and more numerous. Folding those
// into Input would overstate the bill; leaving them out would understate it. The
// backend prices each from its own table.
type TokenUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`

	// CacheWrite is input the provider charged extra to store for later reuse.
	CacheWrite int `json:"cache_write,omitempty"`
	// CacheRead is input served from that store, at a discount.
	CacheRead int `json:"cache_read,omitempty"`
}

// EnrolRequest is what an agent sends once, to exchange an enrolment token the
// operator gave it for an identity of its own.
//
// The token is single-use and short-lived by the backend's choice; the agent
// simply presents it. This is the Wazuh arrangement, and the reason for it is
// that a shared secret copied onto every workstation cannot be revoked for one
// of them.
type EnrolRequest struct {
	Schema int `json:"schema"`

	// Token is the enrolment secret the operator configured. It is a credential
	// the operator supplied, not anything the agent observed.
	Token string `json:"token"`

	// State lets the backend record what enrolled, so an agent that never sends
	// a heartbeat is still visible as having tried.
	State State `json:"state"`
}

// EnrolResponse is the identity the backend issues.
type EnrolResponse struct {
	Schema int `json:"schema"`

	// AgentID names this agent from now on.
	AgentID string `json:"agent_id"`

	// Key signs every later heartbeat, hex-encoded. It is per agent, so
	// revoking one does not touch the others.
	Key string `json:"key"`
}

// Headers an agent sets on a heartbeat.
const (
	// HeaderAgent carries the agent id, so a backend can find the key to verify
	// with before parsing the body.
	HeaderAgent = "X-Cloakfleet-Agent"

	// HeaderSignature carries the hex HMAC-SHA256 of the request body under the
	// agent's key.
	//
	// Over the body rather than over a canonicalised set of headers, because the
	// body is the whole message and there is nothing else to bind. It makes a
	// heartbeat unforgeable without the key, which is what stops one workstation
	// filing reports as another.
	HeaderSignature = "X-Cloakfleet-Signature"
)
