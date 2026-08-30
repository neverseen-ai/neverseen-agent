# The browser extension

Cloakfleet masks what a *tool* sends by standing in front of it as a proxy. A web chat
gives it nothing to stand in front of: the traffic goes from the page to the site's own
origin, over a connection the page opened, and no environment variable points anywhere.

The extension is the answer, and it is deliberately thin. It intercepts the page's own
network calls, hands the text to the agent on this machine, and puts back what the agent
returns. It holds **no catalogue, no policy, no mappings and no detector** — one engine,
one policy, one set of surfaces.

Source: `extension/` (TypeScript, built with esbuild) and `internal/proxy/extension.go`
(the two routes it talks to).

---

## Why this shape

**Manifest v3 closed the clean path.** Blocking `webRequest` is gone and
`declarativeNetRequest` cannot modify a request or a response body. The one technique
left is a content script injected into `world: "MAIN"` at `document_start`, which
replaces `window.fetch` before the site's own script runs. Ordering is the whole
guarantee: a site that captured `fetch` into a local before this ran would keep calling
the original.

**The engine stays in the agent.** A WASM build would duplicate policy, mappings and
telemetry, and a `cmd` that assembles a detector fights the one-entrypoint invariant. A
TypeScript rewrite is two catalogues that drift — and JavaScript regexes do not have
RE2's semantics, so the corpus and every lesson in it would not transfer.

**Same repository.** The API between extension and agent is a contract, and in two
repositories a contract drifts. That is the class of failure this project documents
everywhere else, and `extension/testdata/contract.json` is what the decision buys: one
recorded file, replayed by both sides, in CI, on every commit.

**Fail closed.** A send that could not be masked is not sent. This is the extension's
mirror of the proxy's 415 on a body it cannot read, and it is the rule somebody will be
tempted to soften — because a blocked send looks like a broken site and a forwarded one
looks like nothing at all.

---

## The two routes

### `POST /mask`

`{"texts": [...]}` → `{"texts": [...], "masked": n}`, in the session named by
`X-Session-Id`.

**A list, not a string, and that is not a batching convenience.** A message being sent
carries several text fields, and one pass is what gives a value repeated across two of
them one identity. Masked field by field, the same address leaves as two different
people.

The count includes repeats, like the log line and the recorder: a value masked three
times is three values that did not leave the machine.

### `POST /unmask`

`{"text": "…", "tail": "…", "final": false}` → `{"expanded": "…", "tail": "…"}`, in the
same session.

**Stateless on the agent — the tail travels with the client.** Generated text arrives in
pieces of a few characters, so a replacement the model echoed is regularly split across
two of them. The agent returns whatever could be the start of something it would expand,
and the client prepends it to the next chunk. Holding it server-side would be one more
thing keyed by a session name the caller chooses.

The held-back length is `detector.TailLen`, the same function the streaming rehydrator
uses. **It is not `pii.TokenTailLen`**: a stand-in splits across two events exactly as a
token does, and answering only for tokens is what once left fake mode restoring nothing
in a streamed answer.

**The protocol has an explicit end.** `final: true` expands the remaining tail and holds
nothing back. Without it, a value masked as the very last characters of an answer is
never shown — the page reads a sentence with its last word missing.

**A token nothing expands is left visible.** That is what the agent's own response path
does for an answer still in flight when the mode changed and the mapping was purged.
Inventing a value would put unsupported data in front of somebody; hiding it would hide
that something went wrong.

### What guards them

- **The shared auth helper** (`Server.authorised`), extracted from `PUT /policy` and used
  by all three, comparing with `subtle.ConstantTimeCompare`. `==` on a secret returns at
  the first differing byte, and this socket is reachable by every process on the
  workstation.
- **`/unmask` is loopback only, hard**, whatever `-l` bound, checked *before* the key is
  looked at so no remote caller ever presents one over clear HTTP. The
  warn-rather-than-refuse reasoning behind `-l` does not transfer: `/healthz` and `/test`
  describe a configuration, this one answers "what does this replacement stand for".
  `TODO:` a browser in a VM talking to the host agent is a real shape this refuses; the
  upgrade path is TLS or an explicit opt-in, not a relaxation.
- **`/mask` is not loopback-only**: it exposes exactly what `/test` already exposes, and
  follows the same rule.
- **No CORS headers on the agent, ever.** The extension bypasses CORS through
  `host_permissions`; an `Access-Control-Allow-Origin` would hand the masking oracle to
  every web page.
- **Both are in `reservedRoutes`**, so a provider cannot take one.
- **Neither is captured by `-a` or `-v`.** A trace of `/unmask` would put originals on
  disk through a route that reasoning never covered. It is structural — the handler
  passes `nil` where the proxy passes the audit callback — and
  `TestExtensionRoutesAreNotAudited` runs a full round trip with both flags on and
  asserts an empty trace directory and a silent console.

---

## Inside the extension

Three worlds, because no two of them can reach each other:

| File | World | What it does |
| --- | --- | --- |
| `src/interceptor.ts` → `src/intercept.ts` | page (`MAIN`) | replaces `fetch`; guards XHR, `sendBeacon`, `WebSocket` |
| `src/relay.ts` | content (`ISOLATED`) | the trust boundary: names the session, writes the banner's words, relays the rest |
| `src/background.ts` → `src/agent.ts` | service worker | the only place that holds the key and talks to the agent |

The page's world has no `chrome.runtime`. The isolated world has no reach into the page's
globals. And the page's own Content-Security-Policy governs what a content script may
connect to — claude.ai's does not list `127.0.0.1` — while extension messaging is subject
to neither. So the ask crosses twice, and the fetch happens where the page has no reach
at all.

**The key never leaves the service worker.** A key readable from the page is a key any
script the site loads can read, and it opens `/unmask`.

### The trust boundary, and what it is not

The interceptor runs in `world: "MAIN"` because it has to — a copy of `fetch` in an
isolated world is not the one the site calls. That world is the page's own, and
everything in it is the page's: its globals, its listeners, and any message it posts.
**A script the site loads can post exactly what the interceptor posts, on the same
channel, and read the answer.**

So the relay cannot ask "is this really our interceptor". There is no answer to that
question, and any secret placed in the page's world to answer it is readable by the
page. What it can do is stop trusting the page about the things that decide *whose
data is read* and *what is said in this extension's name*:

- **The session is not the page's to state.** `PageAsk` has no session field. The
  relay derives it from its own `location` (`Site.sessionForPage`) and stamps it on.
  Left to the page, this was an oracle: a script could name the agent's anonymous
  `default` session — the one every tool that sends no session header shares, which on
  a workstation carries every value the agent has masked since it started — and read a
  terminal's traffic back one guessable token at a time.
- **`bridge.acceptAsk` rebuilds every ask field by field.** Not a spread, not an
  `as Ask` cast: either would let a `session` the page added ride along, which is
  precisely the field this takes away.
- **`protocol.namesAWebSession` is the service worker's copy of the rule**, checked
  again there. It is the last thing between a bug in the relay and the agent's own
  sessions, and `default` carries no namespace so it matches none.
- **A banner note carries a situation, never a sentence.** `Note` is a closed set and
  `guidance.noteMessage` writes the words, so a script cannot put arbitrary text
  behind this extension's name in its own branded banner.
- An address naming no conversation yields `null`, and the relay mints
  `claude:page-<uuid>` once per load. A fixed fallback would pool every new chat in
  the browser under one mapping; a guessable one would be `default` in a smaller
  costume.

**What this does not close, and is asserted as passing so nobody mistakes it for a
regression:** a hostile script on the site can still ask about the conversation the
tab is showing — whose values the page is already being handed, to render them. The
two halves are `e2e/run.test.mjs`: *a hostile script on the page cannot read another
session back*, and *what the fix does not close*.

`TODO:` a script can also `history.pushState` to another conversation's path and ask
under that id. It needs an id it must obtain from the site, and it cannot reach
`default` or any session outside the namespace. Closing it means a session the page
cannot influence at all, which costs the mapping its ability to survive a reload.

### The site adapter

`src/site/claude.ts` is the only part that goes stale: the agent's routes are a contract
in this repository, a site's request shapes are somebody else's product. A second site is
a second file of that shape and nothing else changed.

It names the fields that carry typed text rather than masking every string in the body —
the body also carries a model name, a conversation id and a timezone, and masking those
sends the site identifiers it cannot route on.

The session is `claude:<conversation uuid>`, so two conversations cannot expand each
other's replacements and one conversation keeps its own across turns. `/mask` and
`/unmask` for one conversation **must** name the same session or the expansion finds
nothing.

### Restoring a stream

`src/restore.ts` is the streaming rehydrator's problem from the browser side.

- **Complete events only.** A network chunk boundary and an event boundary are unrelated,
  and half an event is not something to parse or to hand the page.
- **Rewritten structurally, never as raw bytes.** An original can carry a quote or a
  newline; spliced into the payload as text it closes the JSON string early and the page
  fails to parse the event. The payload is decoded, the generated text replaced in the
  decoded object, and the encoder does the escaping — the same rule
  `internal/proxy/jsonbody.go` exists for.
- **Serialised.** Chunk *n*'s tail prefixes chunk *n+1*, so two calls in flight is a
  corruption rather than an optimisation. A `TransformStream` awaits each `transform`
  before the next, which is exactly that guarantee, and a test holds it by answering the
  first call slowest.
- **The event name above the payload survives**, because a client dispatches on it.

`TODO:` two known ceilings, both failing towards a visible bracket token rather than a
wrong value: an event whose payload is not JSON, and a replacement sitting in some string
of an event other than the generated text.

### Failing, in two directions

Outbound and inbound fail **opposite ways, deliberately**:

- **Outbound**: a masking failure blocks the send. A value that reaches the model cannot
  be recalled.
- **Inbound**: a restoration failure passes the text through unexpanded and says so.
  Failing closed here would throw away an answer that has already been paid for and
  already arrived, to prevent nothing — unexpanded text is the caller's own replacement
  showing as `[EMAIL_1]`, which is unreadable rather than unsafe.

Every block also raises a banner naming the command that fixes it
(`guidance.blockedMessage`). A rejected fetch alone reads to the site as a network
failure and to the person as a broken page.

### The three transports that are refused

`XMLHttpRequest.send` and `navigator.sendBeacon` are synchronous, and a `WebSocket` is
open before there is anything to look at. None can be masked, so a chat request over one
of them is **refused**. A site that moved its chat onto one would otherwise go on working
with this extension installed and mask nothing at all, which is the one failure worse
than a blocked send.

### The options page

`src/guidance.ts` decides what is said; `src/options.ts` puts strings into elements. The
separation is `internal/tray`'s, for the same reason: a decision buried in DOM calls is
one nothing can test.

Two questions are asked, not one: `/healthz` (unauthenticated) answers *is the agent
there*, and an empty `/unmask` answers *does it accept this key*. Collapsed into one call,
a wrong key reports as an agent that is down — sending somebody to restart a service that
is running. The probe uses `/unmask` rather than `/mask` because `/mask` counts a request
and a fleet view would show every open of this page as traffic.

The indicator follows **`Level`**, not `Masking`, for the reason the menu bar icon and the
`cloakfleet status` exit code do: an agent with a category switched off *is* masking, and
a green light over that is a green light over the values that are not being replaced.

The base URL is **discovered, not asked for** — `http://127.0.0.1:8787` by default; the
manual field exists only for an agent moved by `CLOAKFLEET_LISTEN`. Match patterns ignore
ports, so `http://127.0.0.1/*` covers whichever one it took.

---

## Key delivery

`cloakfleet key` prints the control key; it is pasted into the options page and lives in
`chrome.storage.local` — readable by this extension in this profile and by nothing else,
the profile's equivalent of the 0600 on the file it came from.

The key travels **agent → terminal → person → extension**, on one machine under one
account. The trap to refuse when the next rung is built: an unauthenticated `GET /key`
"to keep it simple" is exactly the hole DNS rebinding exploits — a page that resolves a
name to 127.0.0.1 can read anything this agent serves without a header. The key leaves the
terminal, which may read the file, never a bare HTTP route.

The next two rungs are described in `plans/chrome-extension.md` and are not built:
`cloakfleet extension connect` (the key in a URL *fragment*, which never crosses the
network), and native messaging (structural auth, for fleet deployment).

---

## Tests

| Suite | What it can prove |
| --- | --- |
| `internal/proxy/extension_test.go` | the routes: auth, loopback, the split token, the flush, no audit capture |
| `internal/proxy/contract_test.go` | the real agent answers the recorded responses |
| `extension/test/contract.test.ts` | the client sends the recorded requests and reads those answers |
| `extension/test/restore.test.ts` | the stream: splits, flush, JSON escaping, serialisation |
| `extension/test/intercept.test.ts` | outbound masking, fail closed, the three refused transports |
| `extension/test/guidance.test.ts` | the four guidance states, kept apart |
| `extension/test/bridge.test.ts` | the trust boundary: a session the page claims is discarded |
| `extension/e2e/run.test.mjs` | **nothing stubbed**: built extension, real Chrome, real agent |

The end-to-end test puts claude.ai on this machine with Chrome's
`--host-resolver-rules`, so the origin the browser sees — and therefore the content
scripts that apply — is the real one. The local site echoes the prompt back in
five-character events, which guarantees every replacement is split across two of them.

It asserts both halves, and only the pair is proof: **what the site received** (masked —
a page that was never touched renders identically) and **what the page rendered**
(restored).

Commands: `make extension`, `make extension-e2e`, `make contract-update`.

## Not covered, and said so

File uploads, images and voice: their contents reach the model unmasked. A reloaded
conversation shows replacements rather than originals — the request that fetches past
messages is not rewritten yet, and mapping persistence beyond an agent restart is a
separate decision with the same weight as `traces/`. Both are declared on the options
page rather than left to be discovered.
