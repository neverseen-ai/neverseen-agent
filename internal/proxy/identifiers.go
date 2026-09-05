package proxy

import (
	"regexp"
	"strings"
)

// A client names itself to the provider, and those names are not the caller's
// data to protect.
//
// Claude Code sends `metadata.user_id` as a JSON document of its own:
//
//	{"device_id":"5a1c…","account_uuid":"841a…","session_id":"18af…"}
//
// `session_id` is in genericSecretNames, so the value behind it is a named
// secret by shape and the agent replaced it with [SECRET_1] — in a field
// Anthropic defined for the client's own bookkeeping, holding an identifier
// Anthropic itself issued. Masking it protects nothing: the recipient is the
// party the value already belongs to. What it costs is real — that field is what
// rate limiting and abuse tracking key on, so a token there is a client
// reporting a different identity every time this agent restarts.
//
// The same value also arrives inside prompt text, because a session id travels
// in the arguments a tool was called with. So the values are *read* from that
// field and *applied* by value: exempting the field alone would send the
// identifier in clear in `metadata` and as [SECRET_1] three lines above it — one
// exchange, two identities — while reading them from anywhere in the body would
// exempt a `SESSION_ID=` line the caller pasted out of their own .env, which is
// their credential and not Anthropic's identifier.
//
// Anthropic only, and that is the point rather than a missing case. These are
// Anthropic's identifiers; the same `session_id` on the way to another provider
// is a value that vendor has no business seeing, and it stays masked.
//
// "Anthropic" is the host the request is going to, not the name of the route.
// Keyed on the code, the rule held for exactly as long as nobody used the
// override .env.example documents: `CLOAKFLEET_PROVIDERS=anthropic=https://gateway.internal`
// repoints the route at a gateway, a LiteLLM or a logging proxy, and every
// request through it would have sent the caller's session, device and account
// identifiers in clear — from `metadata` and from every tool argument holding
// the same value, since the exemption is applied by value — to a party that did
// not issue them, uncounted and unminted. Read from the resolved URL, the
// exemption follows the destination: a route named anything at all that resolves
// to Anthropic earns it, and the route named "anthropic" pointed elsewhere does not.
//
// A variable rather than a constant only so the suite can stand its own upstream
// in for Anthropic (see anthropicIs in the tests); nothing in the agent writes it.
var identifierHost = "api.anthropic.com"

// providerIdentifiers are the field names holding one.
//
// `device_id` and `account_uuid` are here although no pattern claims either
// today. The rule being asserted is "these three name the client to its own
// provider", not "these three currently leak": a credential pattern widened
// later must not silently start rewriting the field this exists to protect.
var providerIdentifiers = []string{"session_id", "device_id", "account_uuid"}

// identifierValue is what one of those fields is allowed to hold: a UUID, or a
// long hex blob.
//
// The shape is the guard, and it is deliberately narrow. An exemption is a value
// this agent promises never to mask, so a field name alone cannot be enough to
// earn one — `session_id: sk-ant-api03-…` in a prompt is a shape somebody could
// write, and a rule reading only the name would forward that key in clear. Every
// identifier these three fields actually carry is a generated opaque id, which no
// vendor-prefixed credential can be mistaken for.
//
// A format Anthropic changes later stops matching, and the value goes back to
// being masked. That is the safe direction: the failure is a token in a metadata
// field, not a credential on the wire.
//
// Lower-case hex, and read case-sensitively: these are generated identifiers and
// every client that writes them writes them this way, so accepting upper case
// widens the exemption for nothing.
const identifierValue = `(?:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[0-9a-f]{16,})`

// identifierRe finds one inside that document, written as a JSON member or as an
// assignment.
//
// A regex over the field's text rather than a second decode, because the field is
// a JSON document inside a string and a client is free to write it with whatever
// quoting it likes. Each quote is optional and may be escaped, so the same
// expression reads the member decoded (`"session_id":"18af…"`) or as it sat on
// the wire. The closing quote is not required at all: the shape above is what says
// where the value stops.
//
// Case-sensitive, and no `\b` in front. Both were here when this ran over the whole
// body, and both cost: `(?i)` widened identifierValue to accept upper-case hex a
// client that writes these lower-case never sends, and the pair of them defeat the
// scan Go does for a leading literal. What bounds the cost now is the span — the
// `user_id` document is a few hundred bytes, and nothing else is searched.
var identifierRe = regexp.MustCompile(
	`(?:` + strings.Join(providerIdentifiers, "|") + `)(?:\\?["'])?\s*[=:]\s*(?:\\?["'])?(` + identifierValue + `)`)

// exemptIdentifiers returns the values in the decoded body that must reach the
// upstream at host in clear, or nil when that host is not the one that issued them.
//
// Read at the path `metadata.user_id` and nowhere else, and that is what keeps the
// exemption from being a field name anybody can write. A regex for `"user_id"` over
// the raw bytes was the anchor before, and a body carries structured JSON beyond
// metadata — the `input` of every tool call in the history — so a tool once called
// with a `user_id` of its own exempted whatever hex sat inside it, body-wide: a
// 32-hex credential elsewhere in the same exchange went to Anthropic in clear,
// uncounted and unminted. The document is already decoded for masking, so the one
// member costs a lookup where the regex cost a scan of a hundred-kilobyte body.
//
// A body that is not a JSON object has no such path and earns nothing, which is
// the safe direction: the failure is a token in a metadata field, not a credential
// on the wire.
func exemptIdentifiers(host string, doc any) map[string]bool {
	if host != identifierHost {
		return nil
	}
	top, ok := doc.(jsonObject)
	if !ok {
		return nil
	}
	metadata, ok := objectAt(top, "metadata")
	if !ok {
		return nil
	}
	declared, ok := stringAt(metadata, "user_id")
	if !ok {
		return nil
	}

	var exempt map[string]bool
	for _, m := range identifierRe.FindAllStringSubmatch(declared, -1) {
		if exempt == nil {
			exempt = make(map[string]bool, len(providerIdentifiers))
		}
		exempt[m[1]] = true
	}
	return exempt
}

// sessionIDRe finds the conversation id alone in that document, for the
// heartbeat's session counts.
var sessionIDRe = regexp.MustCompile(
	`session_id(?:\\?["'])?\s*[=:]\s*(?:\\?["'])?(` + identifierValue + `)`)

// conversationOf names the conversation an exchange belongs to, for counting.
//
// The session header when the client sent one — that is the identity the request
// path already scopes the mapping by. When it did not, the conversation id Claude
// Code writes into `metadata.user_id`, read at the same path and under the same
// host rule as exemptIdentifiers: it is Anthropic's identifier, so it is trusted
// on the way to Anthropic and nowhere else. Anything left is the shared default
// session, which is what a header-less client is to the vault too.
//
// The identity never leaves the recorder — see telemetry.Sessions — so what is
// at stake here is a count being right, not a value being exposed. But the count
// is the whole point: without this, every Claude Code conversation on a
// workstation is one session, and "tokens per conversation" is tokens per day.
func conversationOf(session, host string, doc any) string {
	if session != "default" || host != identifierHost {
		return session
	}
	top, ok := doc.(jsonObject)
	if !ok {
		return session
	}
	metadata, ok := objectAt(top, "metadata")
	if !ok {
		return session
	}
	declared, ok := stringAt(metadata, "user_id")
	if !ok {
		return session
	}
	if m := sessionIDRe.FindStringSubmatch(declared); m != nil {
		return m[1]
	}
	return session
}
