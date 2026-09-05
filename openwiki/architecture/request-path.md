# The request path (`internal/proxy`)

One pipeline, and every provider runs it. That is the whole design decision
(`internal/proxy/proxy.go:1-8`). Agent Veil had one per entrypoint, and they drifted
until the same request was masked in one and answered in clear in the other — a
divergence nothing could notice, because each path had its own tests and both passed.

```
request  →  resolve provider from first path segment
         →  read body (decompressing if needed) — unreadable ⇒ 415
         →  mask it value by value, seeded from the session vault
         →  save what was minted to the vault
         →  forward to the upstream
response →  expand the masked values back (buffered or streamed)
```

`Server.forward` (`proxy.go:306`) is that sequence end to end;
`Server.maskRequest` (`:352`) is the outbound half, `Server.unmask` (`:479`) the inbound.

## Provider routing

A provider is an upstream addressed by the **first path segment**: a caller pointed at
`http://127.0.0.1:8787/anthropic` reaches `api.anthropic.com`, one pointed at `/openai`
reaches `api.openai.com` (`provider.go:10-18`). Eight providers ship in
`DefaultProviders` (`provider.go:34`).

Routing on an explicit segment rather than sniffing the path is what makes eight
providers work on one port: six of them speak the same OpenAI-compatible paths, so
`/v1/chat/completions` names no upstream at all. An unknown code is refused **by name**
with the list of the ones that exist (`proxy.go:309-316`) — a proxy that silently picked
a provider would send one vendor's key to another vendor.

`CLOAKFLEET_PROVIDERS` applies `code=url` **overrides** onto the default set rather than
replacing it (`ParseProviders`, `provider.go:51`): a deployment pointing one provider at
its own gateway must not silently lose the other seven.

**Reserved routes.** `reservedRoutes` = `healthz`, `test`, `policy`, `mask`, `unmask`
(`proxy.go:201`) — every route the agent answers itself. A provider may
not take one of those codes — `/healthz` would reach the agent while `/healthz/v1/…`
reached the provider, a routing table nobody could reason about. `New` refuses it
(`proxy.go:156-160`, asserted by `TestReservedRoutesCannotBeProviders`).

**No API keys.** Whatever credential the caller sent — a bearer token, an `x-api-key`
header — is forwarded untouched, because the tool making the request already has it. A
workstation agent must not become one more place a key lives (`provider.go:20-24`).

## A body is a JSON document, masked value by value

**Never as raw bytes.** This is not a preference. A JSON string carries escapes, and a
pattern reading the bytes sees the characters those escapes are made of: in a real Claude
Code request containing `…pourquoi.\n\n@RTK.md`, the email pattern read `n@RTK.md` as an
address, took the `n` out of the `\n`, and left a lone backslash before a bracket — every
request failed with "invalid escaped character". The same applies in reverse: an original
carrying a quote or a newline cannot be spliced into raw JSON.

`mapJSONStrings` (`jsonbody.go:38`) decodes, maps every string value, and re-encodes;
`mapStrings` (`:54`) walks the tree. A body that is not JSON is masked as flat text
(`proxy.go:402-411`). Either way it is **one pass over the whole body**, so a value
repeated in two fields keeps one identity. A body carrying more than one JSON document is
an error, not a guess (`errTrailingJSON`, `jsonbody.go:259`).

**Key order is preserved, and that took an ordered representation.** An object is
decoded into `jsonObject` (`jsonbody.go:165`) — a slice of `jsonMember`, read a
token at a time by `decodeValue` (`:111`) — rather than into `map[string]any`, because a Go map
has no order and the body forwarded to the provider came out in Go's sorted marshal
order instead of the caller's. Semantically the same document, and nothing depended
on it; what made it worth fixing is that the audit console prints the body received
and the body sent to be read against each other, and two bodies whose fields are in
different orders cannot be. `MarshalJSON` writes the members back in that order,
each through an encoder with HTML escaping off, so a `<` a caller wrote is not
rewritten either (`TestTheMaskedBodyKeepsItsKeyOrder`,
`TestAngleBracketsSurviveTheRoundTrip`).

It holds in both directions: a streamed event goes through the same decode and
re-encode, so it would have been reordered on the way back to the caller
(`TestAStreamedEventKeepsItsKeyOrder`).

**A duplicated key is kept rather than collapsed.** Two members of the same name are
pathological rather than useful, but a map keeps one of them — which is the agent
deciding which, and whichever it dropped had gone to the provider in the original
(`TestADuplicatedKeyIsNotCollapsed`). Readers here — `deltaText`, `walkUsage` — take
the **last** member of a name, which is what any parser a provider might use would
do.

**Anything reading the decoded document has to know that type.** Three consumers
type-switched on `map[string]any`, and all three went on **compiling** while matching
nothing: a `type switch` that falls into no branch is silent. `decodeEvent` would have
rejected every event and the streaming rehydrator restored nothing; `walkUsage` would
have lost every token count. What caught it is that `TestDeltaText` and `TestUsageFrom`
build their input from raw JSON through the real decoder — a test constructing a map by
hand would have gone on passing over dead code.

**Fail closed.** `readBody` (`proxy.go:457`) decompresses a compressed body and returns an
error for an encoding it cannot read. Forwarding a body the agent could not inspect is the
one failure mode a data-loss-prevention tool must never have — hence the 415
(`TestUnreadableEncodingFailsClosed`, `TestGzippedRequestBodyIsMasked`).

## A pass keeps one identity per value

`detector.Pass` (`internal/detector/mask.go:17`) carries the identity of values across one
exchange. A request is not one piece of text: a body has several fields and a conversation
has several turns, and the same person's address must come out as the same thing in all of
them — otherwise the model is told about three different people and the vault fills with
duplicates.

`NewPass(known)` is seeded with what the session already holds, so a value seen on turn two
reuses the mask minted on turn one (`TestMaskReusesASessionsExistingTokens`).
`Pass.Minted()` is what still has to be stored; `Pass.Counts()` is per-category and
**includes repeats**, because a value masked three times is three values that did not leave
the machine.

## A client's own identifiers are not masked back to their issuer

`Pass.Exempt` holds values this pass must leave in clear whatever a pattern says, and
`internal/proxy/identifiers.go` is its only filler.

Claude Code writes `metadata.user_id` as a JSON document of its own:

```json
{"device_id":"5a1c…","account_uuid":"841a…","session_id":"18af…"}
```

`SESSION_ID` is one of `genericSecretNames`, so the value behind it is a named secret by
shape and the agent replaced it with `[SECRET_1]` — in a field Anthropic defined for the
client's own bookkeeping, holding an identifier Anthropic itself issued. Masking it
protects nothing: the recipient is the party the value already belongs to. What it costs is
real, because that field is what rate limiting and abuse tracking key on, so a token there
is a client reporting a different identity on every restart of this agent.

Four rules hold it narrow:

- **Anthropic only, and Anthropic is a host.** These are Anthropic's identifiers. The same
  `session_id` on the way to another vendor is a value that vendor has no business seeing,
  and it stays masked (`TestAnotherProviderStillMasksTheSameIdentifiers`). The decision is
  keyed on the host the route resolves to (`identifierHost`), not on the route's code:
  `CLOAKFLEET_PROVIDERS=anthropic=https://gateway.internal` makes the route named
  "anthropic" another vendor, and keyed on the name it sent the identifiers there in clear
  (`TestARouteNamedAnthropicPointedElsewhereEarnsNoExemption`).
- **Read from that field, applied by value.** The same session id also arrives inside the
  arguments a tool was called with, so exempting the field alone would send it in clear in
  `metadata` and as `[SECRET_1]` three lines above — one exchange, two identities. But the
  values are read out of `metadata.user_id` and nowhere else — the path in the decoded
  document, not a regex for the field name: read over the whole body, the rule exempted a
  `SESSION_ID=…` line pasted out of a `.env` or a log excerpt, which is the caller's own
  credential and is masked today (`TestAPastedSessionIdIsNotTheClientsIdentifier`); read
  by name, it exempted a `user_id` inside a tool call's `input`
  (`TestAUserIDInAToolCallEarnsNoExemption`).
- **The shape is the second guard.** Only a lower-case UUID or a long hex blob is
  eligible, because an exemption is a promise never to mask and a field name alone cannot
  earn one:
  `session_id: sk-ant-api03-…` is a shape somebody can write, and a rule reading the name
  would forward that key (`TestAnIdentifierFieldDoesNotExemptACredential`). A format
  Anthropic changes later stops matching and the value goes back to being masked — the safe
  direction.
- **Per pass, not on the detector.** What goes in it is read out of the body being masked.
  `Detector.allowed` is the deployment-wide half of the same idea and is precomputed once;
  this half cannot be.

An exempt value is written back exactly as it arrived and is neither counted nor minted: it
is not a replacement that failed to happen, it is a value that was never sensitive on this
route. Nothing lands in the mapping, so the response path has nothing to expand.

`device_id` and `account_uuid` are in the set although no pattern claims either today — the
rule being asserted is "these three name the client to its own provider", not "these three
currently leak". A credential pattern widened later must not silently start rewriting the
field this exists to protect.

## The session vault

`internal/vault` holds one mapping per session, **masked → original** — the direction the
response path reads it. The originals are encrypted at rest with AES-256-GCM, and the key
may be nil, in which case one is generated for the life of the process (`vault.go:64-70`).
There is deliberately no second, plaintext mode that only shows up in a deployment nobody
tested.

What that buys today is stated plainly in the code: the store is in memory, so it protects
the originals in a core dump or a swapped page, not against anything with access to the
running process. It earns its keep when the mapping moves to a shared store — which is why
`Store` is an interface with one implementation (`vault.go:36`), the second one being a
known requirement (Redis, for a mapping surviving a restart) rather than a speculative one.

- The nonce is fresh per value, so two identical originals under two tokens do not produce
  identical ciphertext — which would tell anyone reading the store that they are the same
  person (`vault.go:145-154`, `TestIdenticalOriginalsSealDifferently`).
- An entry that cannot be decrypted is **dropped, not reported** (`vault.go:102-104`): it means
  the process restarted under a new ephemeral key, so the token is simply unknown and the
  response path leaves it alone rather than failing a request over bookkeeping.
- `DefaultTTL` is 30 minutes (`vault.go:26`) — long enough for a conversation with a pause
  in it to keep one identity per value, short enough that a workstation left running does
  not accumulate the day's data. It is a privacy setting as much as a cache setting.

**Sessions scope the mapping.** `sessionOf` (`proxy.go:659`) reads the session a request
belongs to — `X-Session-Id`, else `X-Request-Id` — falling back to a single name. The
*conversation* the heartbeat counts is a separate identity, `conversationOf`
(`identifiers.go:155`): the same header, else the id Claude Code writes in
`metadata.user_id`, else the default; it names nothing the mapping is scoped by, and it
never leaves the recorder (see [supervision](supervision.md)). That is right for one person at one
workstation, which is what this agent is — and it is exactly why `DefaultListen` binds the
loopback interface only (`env.go:43-49`): the agent trusts whoever reaches it, forwards
their credentials, and scopes the mapping by a header they control.

## Expansion on the way back

Streaming and buffered responses go through the **same** expansion, so a value restored in
one is restored in the other (`proxy.go:475-478`). They differ only in how much text the
expander can see at a time.

Read for three things, rewritten for one. Every textual answer is decoded — for its token
counts (`usageFrom`) and for the tool calls the model asked for (`reportToolCalls` on the
buffered path, `stream.onTool` on the streamed one) — but the decode-and-encode round trip
is not byte-preserving, so the body is re-encoded only when the session has minted something
(`len(known) > 0`, `proxy.go:551-561`; `streamRehydrator.observe` for a stream). A stream
also reports a tool call whose arguments never formed a document through `onDegraded`
(`proxy.go:508`), which is the heartbeat's `degraded` count. The console and the trace file
that read these same two bodies are the operator's surface, described in
[configuration](../operations/configuration.md).

`isTextual` (`proxy.go:634`) gates it: an image or an audio stream is passed through
untouched (`TestNonTextualResponseIsUntouched`).

**Both shapes of a masked value are expanded.** A bracket token is expanded by
`pii.TokenAt`; a **stand-in** — fake mode's substitution, which reads as prose — is
expanded by matching its own text, so an exchange in `fake` mode round-trips like any
other. `fake` was one-way here until it was asked for both ways.

What keeps that safe is one step upstream, in `Detector.render`: **a credential never gets
a stand-in**, because every secret category takes the bracket-token fallback by design. So
a non-token entry in the mapping cannot be a credential, and the value-matching path cannot
expand one into a live secret on its way to a caller. `TestFakeMode` asserts both halves.
See [Detection engine](detection-engine.md#substitution-modes).

**Expansion is one forward walk, not a replacement per entry** (`UnmaskSeen`,
`internal/detector/mask.go:226`). One stand-in can contain another — a fake postcode inside
the fake address it belongs to — and replacing them in turn expands the shorter one inside
text already expanded, putting a value inside a value. A session that minted only tokens
keeps the single-regex fast path it always had (`standInsOf` is empty), so token mode is
byte-for-byte what it was.

## Streaming holds back a tail

Generated text arrives in pieces of a few characters, so a token the model echoed is
regularly split across two events. `streamRehydrator` (`stream.go:29`) keeps back anything
that could be the start of a masked value and prepends it to the next piece, so what it
emits is always a whole event — occasionally a few characters shorter, with those
characters moving to the event after it.

The held-back length comes from `detector.TailLen` (`mask.go:305`), **not**
`pii.TokenTailLen` alone: a stand-in the model echoed is split across two events exactly as
a token is, and answering only for tokens left `fake` mode restoring nothing in a streamed
answer while a buffered one round-tripped (`TestTailLenCoversBothShapes`).

It rewrites only the delta text of an event, found by `deltaText`, which handles the
Anthropic shapes — `delta.text` and `delta.thinking` — and the OpenAI
`choices[].delta.content` (`TestStreamRehydratorHandlesTheOpenAIShape`). Structure and
numbers are left alone (`TestStreamRehydratorLeavesStructureAlone`,
`TestStreamRehydratorDoesNotRewriteNumbers`), and a dangling tail at end of stream is
flushed (`flush`).

**A tail belongs to the block it was held back from.** `closeBlock` releases it when the
block stops or when an event for another block arrives. Carried across, it prefixed the
next block's text — or, when that block was a tool call, arrived after the stream had
ended in an event for a block closed long before
(`TestATailDoesNotCrossABlockBoundary`).

**A tool call's arguments are not text.** They arrive as slices of a JSON document
(`delta.partial_json`), so expanding a value into a slice splices it into the *source* of a
document only ever seen a piece of: an original carrying a quote ends the string it landed
in, and the client's parse of the tool call then fails, at the client, silently. So
`jsonFragment` holds the fragments and `expandedArguments` releases them at the block's
stop, when the concatenation is a whole document — decoded, expanded value by value,
re-encoded, the encoder doing the escaping. Nothing is lost by waiting: a client cannot use
half a JSON document. Released before the stop that completes them and exactly once
(`TestArgumentsAreReleasedBeforeTheirStop`); fragments that do not make a document fall
back to whole-token expansion rather than being dropped
(`TestIncompleteArgumentsAreStillDelivered`).

**An event is a name line and a data line, and holding one back means holding both.**
An SSE event is `event: <name>`, `data: <json>`, blank — and a client dispatches on the
name. Rewriting the stream a line at a time, the name line was forwarded the moment it
arrived, so every fragment held back above left its name behind with nothing under it: the
caller read a named `content_block_delta` carrying the empty string, parsed it, and
reported `JSON Parse error: Unexpected EOF` — the whole answer lost, on the first tool call
of every exchange. The reverse half was there too: `expandedArguments` and `tailEvent`
synthesise an event, and emitted as a bare data line it is attached by the client to
whichever name it saw last, which is the `content_block_stop` that released it.

So `rewrite` withholds the name line (`streamRehydrator.name`, taken by `takeName`) and
emits it with the one data line it belongs to, *after* whatever the previous block was still
holding; the two synthesised events carry the name their original arrived under
(`argumentsName`, `templateName`); and a held event's blank separator is dropped with it
(`held`), because an event that emits nothing emits none of its three lines. A conformant
client ignores a stray blank, but a tool call arrives in hundreds of fragments and that is
not a bet worth taking.

`TestEveryNamedEventCarriesItsData` asserts both directions over every event in the stream.
It needed a second framing helper — `namedEvents` — because `events` emits data lines only,
and **a stream with no names in it cannot exhibit a name that lost its data**: that is how
the whole suite stayed green over an agent no client could talk to.

The known gap is recorded as a `TODO` on `jsonFragment`: the OpenAI family streams tool
call arguments under `choices[].delta.tool_calls[].function.arguments`, which has no
per-block stop to accumulate against — its end is a `finish_reason` on the message — and
that reading is unverified against a real stream.

## `/healthz` and the local callers

`proxy.Health` (`status.go:27`) is **one type for both sides of the route**: the server
marshals it, and every local caller — `cloakfleet status`, `cloakfleet env`, the menu bar —
unmarshals it in the same package, so the two cannot come to disagree about a field name.

It carries what the agent is *applying* and nothing about who is using it: no counters, no
addresses, no backend URL. The route is unauthenticated on the loopback interface, and
anything richer would be a local oracle for what a person has been doing.

`proxy.Query` (`status.go:207`) is the only place that asks whether the agent is there. It
returns **no error**, because "nothing is listening" is an answer to the question and not a
failure to answer it. `Status.Masking()` is deliberately not `Answering`: an agent with no
locale selected is up, healthy, and recognises almost nothing — the state a green light
would call fine while the traffic went out in clear.

**And `Status.Level()` is deliberately not `Masking()`** — the same distinction one level
further in. An agent with a category switched off *is* masking: most of the catalogue, and
the credentials always. Reporting that as simply "masking" is the green light over the
values that are not being replaced, so there are three answers (`detector.LevelNone`,
`LevelPartial`, `LevelFull`), and the exit code of `cloakfleet status` and the menu bar
icon both follow this rather than `Masking()`. An agent that answers without the field is
read from what it did carry: a build with no policy route cannot have anything switched
off, so `LevelFull` is a fact about that build rather than an assumption.

**`PUT /policy` is the one route that changes what the agent does, and it is
authenticated** (`internal/proxy/policy.go`), through the one helper `Server.authorised`
that also guards the extension's `POST /mask` and `POST /unmask` — three routes, one
constant-time check, because a second copy is the one that drifts (see
[browser extension](browser-extension.md)). Everything else the agent serves is a
proxy hop carrying the caller's own credential, or a read-only description of the
configuration — safe unauthenticated on the loopback because the worst it gives a local
process is that description. This one switches masking off, and left open, any local
process could disable the control; so could a page in a browser, because a form post to
`127.0.0.1` needs nobody's permission. What closes it is a secret in
`~/.cloakfleet/control.key` (0600) sent in a custom header — which is precisely what a
browser cannot set on a simple cross-origin request, so no page on the internet can reach
it at all. That is the mechanism, not politeness about CORS, which the agent does not
implement and must not.

The request **replaces the whole set** rather than toggling one category: two surfaces can
be looking at one agent, and a toggle is a read-modify-write whose halves interleave into a
set neither of them asked for. The reply is the agent's own `Health`, so a caller redraws
from what is true rather than from what it asked for. And **the detector is what refuses a
credential** (`pii.Switchable`), not the route and not the menu — a surface that merely hid
those rows would still be talking to an agent that accepted the request from anything else
on the machine.

## Token usage and counters

`usageFrom` (`usage.go:28`) walks a response document for the model name and token counts,
feeding `telemetry.Recorder`. **Reading token usage is where the vendors disagree about
more than spelling:** Anthropic reports cache tokens *beside* the input, OpenAI reports the
whole input with the cached part broken out underneath as a *subset*. Reading both would
bill the same tokens twice, so the OpenAI breakdown is deliberately not read.

A refused request is not counted, and the test page is not counted
(`TestARefusedRequestIsNotCounted`, `TestTheTestPageIsNotCounted`). The log line per
exchange carries **counts and category names only** — the log is the one place a masked
value could come back into the clear by accident (`proxy.go:440-442`).

## Where to start on a change here

- Changing what gets masked ⇒ you are in [the detection engine](detection-engine.md), not
  here.
- Adding a provider ⇒ `DefaultProviders` in `provider.go`, plus the caveat question in
  [Distribution](../operations/distribution.md#pointing-a-tool-at-the-agent).
- Touching the response path ⇒ the parity tests are the gate:
  `TestPipelineParityAcrossProviders`, `TestStreamingRoundTripThroughTheProxy`,
  `TestFakeModeRoundTripsThroughTheAgent`.
- Anything that could forward an uninspected body ⇒ stop.
  `TestUnreadableEncodingFailsClosed` is the invariant.
