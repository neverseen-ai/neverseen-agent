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

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"math/bits"
	"time"
)

// SchemaVersion is the version of this contract.
//
// Sent in every message so a backend serving several agent versions can tell
// them apart, and so an agent talking to an older backend is rejected cleanly
// rather than silently misread.
//
// 2 because an agent now posts a HeartbeatBatch to /v1/heartbeats where it used to
// post a Heartbeat to /v1/heartbeat, and Counters gained Restarts. The path change
// alone would have an older backend answer 404, which the agent reads as an outage
// and buffers against for seven days; the version is what turns that into the
// refusal the field exists to produce. Bumping it is not optional on a change like
// this — the field is only worth anything if it moves when the wire does.
const SchemaVersion = 2

// HeartbeatBatch is what a supervised agent posts: one or more buckets, with the
// fields they share said once.
//
// A batch rather than a message per bucket, because an agent that could not reach
// the backend keeps closing a bucket every interval and has a backlog to file when
// it comes back. A week of five-minute buckets is two thousand of them, and two
// thousand signed requests from every workstation in the fleet, at the moment the
// service recovers, is a recovery that ends in a second outage. Batched, the same
// week is a few dozen requests.
//
// The ordinary case — one bucket, on the interval — is the same message with one
// element. Deliberately: a separate shape for the single case would be a second
// code path exercised only after an outage, which is the branch nobody tests.
type HeartbeatBatch struct {
	Schema int `json:"schema"`

	// AgentID is the identity the backend issued at enrolment.
	AgentID string `json:"agent_id"`

	// SentAt is when the agent composed this message — not when any of the buckets
	// were measured. The backend uses it to spot a clock that is wrong and to
	// reject a replayed message.
	SentAt time.Time `json:"sent_at"`

	// State is what the agent is applying right now, so it is said once for the
	// whole batch: stamping a week-old bucket with today's configuration would be
	// no more true for being repeated on every one of them.
	State State `json:"state"`

	// Buckets are the windows being filed, oldest first.
	Buckets []Bucket `json:"buckets"`
}

// Bucket is one window's counters.
type Bucket struct {
	// Window is the period the counters cover. Sent explicitly rather than
	// inferred from the interval, because a bucket that waited out an outage is
	// filed long after the period it measured.
	//
	// It is also what makes filing a bucket idempotent. An agent forgets a batch
	// only once the backend has answered, so a process killed between the answer
	// and that write re-sends buckets that were in fact stored — and the backend
	// recognises them by (agent, window) and accepts the retry without counting it
	// twice. Any backend on this contract has to key on the window for that reason:
	// nothing else in a bucket identifies it.
	Window Window `json:"window"`

	// Counters are what happened during the window.
	Counters Counters `json:"counters"`
}

// Windows expands a batch into the per-window reports a backend records, so that
// both sides cannot disagree about how the shared fields are applied.
//
// Here rather than in the backend for the same reason Sign is here: one
// implementation, because two would be two chances to differ — and a batch whose
// buckets were stamped with the wrong sender or schema on one side only has no
// symptom beyond a dashboard that is subtly wrong.
func (b HeartbeatBatch) Windows() []Heartbeat {
	out := make([]Heartbeat, 0, len(b.Buckets))
	for _, one := range b.Buckets {
		out = append(out, Heartbeat{
			Schema:   b.Schema,
			AgentID:  b.AgentID,
			SentAt:   b.SentAt,
			Window:   one.Window,
			State:    b.State,
			Counters: one.Counters,
		})
	}
	return out
}

// Heartbeat is one window as a backend records it: a batch's shared fields plus
// one of its buckets, expanded by Windows.
//
// Not a message in its own right — an agent posts a HeartbeatBatch — but the shape
// a stored window has, and the argument every function that records one takes.
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

	// Addresses are the machine's own non-loopback IP addresses.
	//
	// Reported so a security officer can tell which machine an agent is, which is
	// the question behind every row of the fleet view: "agt_4742be… is silent" is
	// not actionable, "the laptop at 10.4.2.87 belonging to Marie is silent" is.
	//
	// This is the one field in this contract that is personal data. It is here
	// deliberately and it is the exception that proves the rule: everything else
	// is a count, a category name or a build string. An address is not content —
	// no prompt, no response and no detected value can travel in it — but it does
	// identify a machine and, through it, a person. It is declared in the
	// allow-list with that reasoning, and named in the README, so a customer's DPO
	// reads it in the documentation rather than discovering it in a database.
	//
	// Local addresses, not the public one: on a corporate network the private
	// address is what distinguishes one workstation from another, while the egress
	// address is shared by the whole site. The backend records the address it
	// observes the connection coming from separately — which also means this field
	// being wrong or absent costs nothing that matters.
	Addresses []string `json:"addresses,omitempty"`

	// Masking is how much of the catalogue the agent is applying: "full",
	// "partial" or "none".
	//
	// It exists because Locales alone stopped answering the question. An agent can
	// now be told to stop masking a category, so it can be loaded with three
	// locales and still be sending email addresses to a provider in clear — and a
	// fleet view reading only Locales would show that agent as configured and
	// green. This is the answering/masking distinction the whole State type exists
	// for, one level further in.
	Masking string `json:"masking,omitempty"`

	// SwitchedOff names the categories the agent is not masking.
	//
	// Category names, which the counters already carry, so this adds no new kind
	// of string to the contract — and it is the field that makes Masking
	// actionable: "partial" sends somebody looking, "partial, and EMAIL is off"
	// tells them whether it matters. A supervision backend that could see the
	// state but not what was in it would report a problem nobody could size.
	//
	// Deliberately not a count. Two categories off is not a fact anybody can act
	// on, and the names cost nothing that a value would.
	SwitchedOff []string `json:"switched_off,omitempty"`

	// SecretLevel is how far down the strength scale the catch-all secret pattern
	// is masking: "weak", "medium" or "strong".
	//
	// It belongs beside SwitchedOff and for the same reason: it changes while the
	// process runs, and it is the other way an agent can be masking less than its
	// catalogue allows without anything being switched off. A fleet view showing
	// "full" over a row of agents at "strong" would be telling somebody every
	// credential is covered while the ordinary ones are not.
	//
	// One of three fixed words, never a value.
	SecretLevel string `json:"secret_level,omitempty"`

	// The four fields below say how the agent itself is exposed, which is the one
	// thing about it the rest of State cannot: an agent applying its whole catalogue
	// is still a risk if it is writing every prompt in clear to a log file, or
	// answering a masking oracle to the whole network. A security officer reading a
	// fleet view has to be able to see those from the row, not from a shell on the
	// workstation.

	// Console reports that every value replaced and restored is being printed in
	// clear (`neverseen proxy -a`). Under the installer's service definition that
	// console is a file, so this is an agent keeping the day's prompts on disk.
	Console bool `json:"console,omitempty"`

	// Tracing reports that both bodies of every exchange are being written to disk
	// (`neverseen proxy -v`) — the only thing this agent ever writes in clear.
	Tracing bool `json:"tracing,omitempty"`

	// Exposed reports that the agent listens beyond the loopback interface, where
	// `/test` is a masking oracle for anybody on the network and a session is named
	// by a header the caller chooses.
	Exposed bool `json:"exposed,omitempty"`

	// Rerouted names the provider codes whose route was pointed somewhere other
	// than the vendor's own host (NEVERSEEN_PROVIDERS). A gateway, a LiteLLM or a
	// logging proxy is a party the masked traffic reaches that the dashboard would
	// otherwise not know about. Codes from the agent's own provider list, the same
	// strings Providers already carries.
	Rerouted []string `json:"rerouted,omitempty"`

	// Allowlisted counts the values this deployment declared it never masks
	// (NEVERSEEN_PII_ALLOWLIST). A count and never the values: two hundred
	// exemptions is an agent masking less than its catalogue says, and that is all
	// a fleet view needs to know.
	Allowlisted int `json:"allowlisted,omitempty"`
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

	// Restarts counts how many times the agent process started during the window.
	//
	// Reported per window rather than inferred from StartedAt, because StartedAt
	// only ever shows the *current* process: an agent that restarted eleven times
	// between two heartbeats looks, from that field alone, exactly like one that
	// restarted once. And an agent buffering through a backend outage files a
	// bucket every interval, so the restarts stay attributed to the five minutes
	// they happened in rather than to the moment the backend came back.
	//
	// The agent counts its own start, so the first window an agent ever files
	// carries 1 — that one is the install rather than a restart, and the backend
	// can tell because it is the same report that enrolled.
	Restarts int `json:"restarts,omitempty"`

	// Dropped counts windows this agent had to discard because the backend was
	// unreachable for longer than its buffer holds.
	//
	// Reported rather than hidden: a gap in the record is exactly what an
	// auditor needs to see, and a silently lost window looks identical to a
	// window in which nothing happened.
	Dropped int `json:"dropped,omitempty"`

	// Refused counts requests the agent would not forward because it could not
	// read the body — the fail-closed 415. One is a client sending a shape the
	// agent does not model; a steady rate is a tool that will end up pointed
	// around the agent.
	Refused int `json:"refused,omitempty"`

	// Providers counts requests per route code — "anthropic", "openai" — the same
	// strings State.Providers lists. Models says which model answered; this says
	// which vendor was asked, including the exchanges that never got an answer.
	Providers map[string]int `json:"providers,omitempty"`

	// Upstream counts how the providers answered, by class.
	Upstream Upstream `json:"upstream,omitzero"`

	// Clients counts requests per client family — "claude-code", "cursor",
	// "openai-python" — read from the User-Agent header and reduced to a closed
	// list (KnownClients); anything else is counted as "other". It is the
	// inventory a security officer has no other way to take: which AI tools are in
	// use on the workstations, and how much of the traffic each one is.
	Clients map[string]int `json:"clients,omitempty"`

	// Policy counts the changes made to what the agent masks, through PUT /policy.
	// State says what is switched off; this says when, and how often, somebody
	// switched something.
	Policy PolicyChanges `json:"policy,omitzero"`

	// Degraded counts answers where a tool call's arguments never formed a JSON
	// document — a stream cut short — and were expanded token by token instead of
	// as a whole. Not a leak: a value split across two events is then left
	// unrestored, so the tool acts on a replacement. A count of how often the
	// control's second-best path ran.
	Degraded int `json:"degraded,omitempty"`

	// Sessions counts the conversations behind the requests, and describes the
	// ones that ended during the window.
	Sessions Sessions `json:"sessions,omitzero"`

	// Tools counts what the model asked the workstation to run.
	Tools Tools `json:"tools,omitzero"`
}

// Upstream is how the providers answered, by status class. Five integers rather
// than a map keyed by status, so the contract gains no new string.
type Upstream struct {
	// OK is a 2xx.
	OK int `json:"ok,omitempty"`
	// RateLimited is a 429, on its own because it is the answer that says the
	// organisation is at its quota rather than that a request was wrong.
	RateLimited int `json:"rate_limited,omitempty"`
	// Rejected is any other 4xx: a bad key, a malformed request, a refused model.
	Rejected int `json:"rejected,omitempty"`
	// Failed is a 5xx.
	Failed int `json:"failed,omitempty"`
	// Unreachable is a request that got no answer at all.
	Unreachable int `json:"unreachable,omitempty"`
}

// PolicyChanges counts requests to PUT /policy.
type PolicyChanges struct {
	// Applied changed something about what the agent masks.
	Applied int `json:"applied,omitempty"`
	// Refused was rejected in whole or in part.
	Refused int `json:"refused,omitempty"`
}

// Sessions describes the conversations behind a window's requests.
//
// A session is the identity the request path already scopes its mapping by — the
// session header — and, where a client sends none, the conversation id Claude Code
// writes into `metadata.user_id`. Everything left names one shared session. The
// identity itself never travels: the agent keeps it in memory for as long as the
// mapping lives, and only the counts leave.
//
// The averages a dashboard wants — tokens per conversation, requests per
// conversation — are deliberately not computed here. A mean over a five-minute
// window is wrong for conversations that last hours, and the agent would be
// choosing the statistic. Instead a session's totals fall into histograms when it
// closes, and the backend reads a median or a p95 from those.
type Sessions struct {
	// Active is how many distinct sessions sent a request during the window.
	Active int `json:"active,omitempty"`
	// Opened is how many of those were seen for the first time.
	Opened int `json:"opened,omitempty"`
	// Closed is how many sessions expired during the window — no request for as
	// long as the mapping lives — and so how many the histograms below describe.
	Closed int `json:"closed,omitempty"`

	// Per closed session, each a Histogram: how long it lasted, in seconds, from
	// its first request to its last; how many requests it made; the input tokens
	// it consumed, cache included, and the output tokens; how many tool calls the
	// model made in it; and how many values were masked in it.
	Duration  Histogram `json:"duration,omitempty"`
	Requests  Histogram `json:"requests,omitempty"`
	Input     Histogram `json:"input,omitempty"`
	Output    Histogram `json:"output,omitempty"`
	ToolCalls Histogram `json:"tool_calls,omitempty"`
	Masked    Histogram `json:"masked,omitempty"`
}

// HistogramBuckets is the length of a Histogram.
//
// Twenty-four powers of two reach 8,388,608 — more input tokens than a
// conversation has, more seconds than a session's mapping survives — and the last
// bucket is open-ended, so nothing is ever lost, only capped.
const HistogramBuckets = 24

// Histogram counts values by order of magnitude: bucket 0 holds the zeros, and
// bucket i holds the values in [2^(i-1), 2^i), the last one everything above.
//
// Fixed edges rather than a map keyed by a range label, so the contract gains no
// string, and logarithmic because the quantities it describes span six orders of
// magnitude between a one-line question and an afternoon of code review. Nil until
// a session has closed.
type Histogram []int

// Add counts one value.
func (h *Histogram) Add(v int) {
	if *h == nil {
		*h = make(Histogram, HistogramBuckets)
	}
	i := 0
	if v > 0 {
		i = min(bits.Len(uint(v)), HistogramBuckets-1)
	}
	(*h)[i]++
}

// Tools counts what the model asked the workstation to run.
//
// A tool call is the one part of an answer that is neither prose nor a value: it
// is an instruction the tool on this workstation is about to carry out. What is
// counted is how many there were, how many acted on a value the agent had masked
// on the way out, and which tools and programs they named — each name reduced to a
// closed vocabulary the agent carries, so the heartbeat stays counts and catalogue
// terms. The arguments themselves never travel.
type Tools struct {
	// Calls is how many tool calls the answers carried.
	Calls int `json:"calls,omitempty"`

	// Restored is how many of those had a masked value put back into their
	// arguments: the number of times a personal or credential value the model
	// never saw reached an action on the workstation.
	Restored int `json:"restored,omitempty"`

	// Names counts calls per tool, from KnownTools; the rest are "other".
	Names map[string]int `json:"names,omitempty"`

	// Programs counts the programs a shell tool was asked to run, from
	// KnownPrograms; the rest are "other". Read from the first word of each command
	// in the arguments, never the arguments themselves.
	Programs map[string]int `json:"programs,omitempty"`

	// Classes counts the shell commands that fall into one of CommandClasses —
	// reaching the network, elevating privileges, installing software. What a
	// security officer wants to know about a command is not its name but what kind
	// of thing it does, and a class is a fact about the vocabulary, not the text.
	Classes map[string]int `json:"classes,omitempty"`
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
	HeaderAgent = "X-Neverseen-Agent"

	// HeaderSignature carries the hex HMAC-SHA256 of the request body under the
	// agent's key.
	//
	// Over the body rather than over a canonicalised set of headers, because the
	// body is the whole message and there is nothing else to bind. It makes a
	// heartbeat unforgeable without the key, which is what stops one workstation
	// filing reports as another.
	HeaderSignature = "X-Neverseen-Signature"
)

// Signing lives here, in the contract, rather than on either side of it.
//
// It has to: the agent signs and the backend verifies, and two implementations of
// one HMAC are two chances to disagree about what is covered. That failure has no
// good symptom — every heartbeat is rejected, with each side convinced it is
// doing the right thing.
//
// It is also why this is in the public package rather than in the agent's
// internals. Go's internal rule would put it out of the backend's reach, and the
// workaround anybody would reach for is a copy.

// Sign returns the hex HMAC-SHA256 of a heartbeat body under an agent's key.
func Sign(key, body []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature reports whether body was signed with key.
//
// Compared in constant time, so a caller cannot learn a signature one byte at a
// time from how long the answer takes.
func VerifySignature(key, body []byte, signature string) bool {
	want, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}
