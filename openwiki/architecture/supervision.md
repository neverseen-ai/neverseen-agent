# Supervision (`pkg/telemetry` + `internal/telemetry`)

Supervision is **optional, and the agent is a complete product without it**. Set
`NEVERSEEN_BACKEND_URL` and an enrolment token and the agent reports every five minutes:
how many requests it proxied, how many values it masked in which categories, how many
tokens went to which model, and what configuration it is actually applying.

For the reader of the fleet view rather than of the code — what each figure answers
and what the heartbeat deliberately does not say — see
[`docs/telemetry-for-ai-governance.md`](../../docs/telemetry-for-ai-governance.md).

## Two hard rules

**`pkg/telemetry` is public and the backend imports it — never the reverse.** The paid
backend lives in a separate private repository (`../cloakfleet-cloud`). This repo must
compile, run and be useful with no backend in existence. Read the backend there when a
change touches the shared contract; never add it as a dependency.

**Nothing in `internal/telemetry` may reach the request path.** The reporter runs on its own
goroutine, returns no errors to anybody, and an agent with no backend has **no reporter at
all** rather than one that quietly does nothing (`reporterFromEnv`,
`internal/proxy/agent.go:288`, `TestNoBackendMeansNoReporter`). A supervision backend that
cannot be reached must never stop the masking, or the security control is taken down by the
tool that watches it.

## The contract (`pkg/telemetry/contract.go`)

`SchemaVersion` is 2. A `HeartbeatBatch` carries the agent id, `SentAt`, the live `State`,
and a slice of `Bucket` — each bucket a `Window` (start, end) plus `Counters`.
`Windows()` (`contract.go:102`) expands a batch into the single-window `Heartbeat` shape.

`Counters` holds `Requests`, `Masked map[string]int` (per category name), `Models
map[string]TokenUsage`, `Restarts` and `Dropped` — and, since the heartbeat started
answering the questions a technical director and a security officer ask about AI usage,
`Refused`, `Providers`, `Upstream`, `Clients`, `Policy`, `Degraded`, `Sessions` and
`Tools`. Each is described in its own section below. `TokenUsage` is **four numbers, not two** —
`Input`, `Output`, `CacheWrite`, `CacheRead` — because a coding agent re-sends its whole
context every turn and almost all of its input is a cache read, an order of magnitude
cheaper and far more numerous. Folding them together overstates the bill; leaving them out
understates it. The agent reports raw counts and the backend prices them, because prices
change and an agent that computed money would need redeploying to every workstation each
time one did.

### Nothing in a heartbeat is content

`TestHeartbeatCarriesNoContent` (`contract_test.go:46`) **walks the type** and fails on any
string field not on an explicit allow list, each entry carrying its reason. A new string
field fails the build until it is listed. Do not add one to make a dashboard nicer.

**`State.Addresses` and `State.Hostname` are the two fields in the contract that are
personal data**, and they are the exception that shows what the rule means (`contract.go`).
Everything else is a count, a category name or a build string; an IP address identifies a
machine and through it a person. They are there because the fleet view's whole purpose
collapses without them — "agt_4742be… is silent" sends somebody to a database, "the laptop
at 10.4.2.87 is silent" sends them to a desk. Neither is *content*: no prompt,
response or detected value can travel in them, which is the invariant the test defends.
Their `allowedStrings` entries say so, and the README says so to the operator, so a
customer's DPO reads it in the documentation rather than finding it in a database.

**The hostname is the more direct of the two, knowingly.** A workstation is very often named
after the person using it, so it can carry a name where an address only carries a machine —
and that is the reason to report it rather than a reason not to: it is the identifier an
operator already recognises, and the one that stays put when a laptop moves between networks
and its address does not. Sent as the operating system gives it (`hostname`,
`internal/proxy/addresses.go`), never resolved — a lookup would put this agent's reporting
on the network's DNS and let a slow resolver delay a heartbeat — and never hashed, which
would keep the personal data and lose the use. Empty when the machine cannot say, which
costs nothing: the backend still has the agent id and the address it observes the connection
from. Read at each heartbeat with the addresses, because a machine renamed while the agent
runs would otherwise report the old name until somebody restarted it.

**Local addresses, deliberately, not the public one.** On a corporate network the private
address distinguishes one workstation from another while the egress address is shared by the
whole site; the backend records the address it *observes* separately, which is also why this
field being absent or wrong costs nothing that matters. `localAddresses`
(`internal/proxy/addresses.go:33`) drops loopback and link-local, sorts within each family
and caps the count at 4 — a list that reshuffled between heartbeats would read as a machine
whose addresses kept changing, indistinguishable on the dashboard from a laptop moving
networks (`TestLocalAddressesAreStablyOrdered`).

### Closed vocabularies (`pkg/telemetry/vocabulary.go`)

Three of the new maps are keyed on words the agent chooses at run time — a client
family, a tool name, a program name — and that is exactly the kind of string the rule
above exists to keep off the wire. What makes them admissible is that **every key comes
from a list the contract carries** (`KnownClients`, `KnownTools`, `KnownPrograms`,
`CommandClasses`) and everything else is counted as `Other`. The word "python3" in a
heartbeat comes from the list, not from the prompt. The lists are in the contract rather
than in the agent because both sides need them: the agent reduces to them, the backend
renders and validates against them, and widening one is a contract change made in one
commit. The recorder re-checks every key against the list before counting it
(`inVocabulary`), so the invariant does not depend on each call site reducing correctly.

### Sessions

A session is the identity the request path already scopes its mapping by — the session
header — and, where a client sends none, the conversation id Claude Code writes into
`metadata.user_id`, read at that path and under the same host rule as the identifier
exemption (`conversationOf`, `internal/proxy/identifiers.go`): it is Anthropic's
identifier, so it is trusted on the way to Anthropic and nowhere else. Everything left is
the shared default session. The identity never leaves the recorder; the counts do.

Per window: `Active` (distinct sessions that sent a request), `Opened` (first seen),
`Closed` (idle for `SessionIdle`, thirty minutes — `TestSessionIdleMatchesTheVault` holds
it equal to `vault.DefaultTTL`, because a session here *is* the conversation the mapping
lives for). **The averages a dashboard wants are deliberately not computed by the agent.**
A mean over a five-minute window is wrong for conversations that last hours, and the
agent would be choosing the statistic. When a session closes, its totals — duration,
requests, input tokens (cache included), output tokens, tool calls, values masked — fall
into `Histogram`s with fixed power-of-two edges, and the backend reads a median or a p95
from those. Fixed edges rather than a map keyed by a range label, so the contract gains no
string. Sessions are swept on `Take` and `Snapshot`, the two moments the recorder is read
with a clock; the clock is injectable (`WithClock`, which the replay calls) for the same
reason `Config.Now` is.

### Tools

A tool call is the one part of an answer that is neither prose nor a value: an
instruction the tool on the workstation is about to carry out. `Tools` counts how many
(`Calls`), how many acted on a value the agent had masked on the way out (`Restored` —
personal data reaching a local action), and which tools (`Names`), programs (`Programs`)
and kinds of command (`Classes`) they named, each reduced to the vocabularies. The
reduction happens in `internal/telemetry/tools.go` before the recorder's lock is taken,
and the text is dropped: the first word of each shell command after the operators split
it, assignments and wrappers stepped over, then matched against `KnownPrograms`; the
classes — network, privilege, destructive, install, secrets, pipe-to-shell — decided from
the program and its first flags. The split is deliberately not quote-aware (a `TODO` names
the ceiling): what it can cost is a word from a quoted string counted as a program *when it
is on the list*; what it never costs is a word outside the list reaching the wire.

`Restored` is read off the expansion itself — the arguments changed — not off the mapping
being non-empty, because a session with a mapping still makes tool calls that touch none
of it. On the buffered path `reportToolCalls` therefore runs **before** the pass over the
whole document and expands each tool's input in place: `mapStrings` rewrites the document
it is given, so run after, it found every input already expanded and counted nothing. The
console and the recorder share one sink (`Server.toolSink`), always set: the callback used
to be nil without a console so that an agent not auditing paid nothing per tool call, and
every agent now pays the reduction — a JSON decode and a split, once per tool call.
Anthropic's shape only, the gap `jsonFragment` already records.

### The agent's own exposure, and the health of the control

`State` carries what the detector is applying — `Masking`, `SwitchedOff`, `SecretLevel`,
filled by `detectorState` at every heartbeat (`internal/proxy/telemetry.go`) because they
change while the process runs — and five facts about the agent itself that the rest of it
cannot say:
`Console` (`-a`, values printed in clear — under the installer's service, a file),
`Tracing` (`-v`, bodies on disk), `Exposed` (listening beyond loopback, where `/test` is a
masking oracle), `Rerouted` (provider codes whose route does not go to the vendor's own
host — a gateway the masked traffic reaches that the dashboard would otherwise not know
about), and `Allowlisted` (how many values are exempted — a count, never the values). An
agent applying its whole catalogue is still a risk if it is keeping the day's prompts in
`~/.neverseen/agent.log`, and a security officer has to see that from the row.

`Counters` gained the control's own health: `Refused` (the fail-closed 415 — a steady rate
is a tool that will end up pointed around the agent), `Upstream` (how the providers
answered, five integers by class rather than a map keyed by status, so no new string),
`Policy` (how often `PUT /policy` was applied or refused — `State` says *what* is off, this
says *when*, and how often somebody keeps trying; an unauthenticated attempt is not
counted, or anybody on the network could write to the counter), `Degraded` (tool-call
arguments that never formed a document and were expanded token by token — how often the
second-best path ran), `Providers` (requests per route, including the ones never answered)
and `Clients` (the User-Agent reduced to `KnownClients` — the inventory of AI tools in use
that nobody has another way to take).

A full batch of sixty of these buckets is several times the size it was;
`TestAFullBatchFitsTheBackendsBodyBound` holds it at half the backend's 1 MiB body bound,
the `/healthz` lesson applied to the other payload.

### Rebuilding the heartbeat from traces (`neverseen replay`)

A trace holds almost everything a heartbeat is made of, one exchange per file, and
`internal/proxy/replay.go` reads them back through the same recorder the agent uses:
the conversation from `metadata.user_id`, the categories by **masking the IN body again
with today's detector** (the trace holds a count; the replay answers what the current
catalogue makes of the traffic), the model and tokens from the header the tracer wrote,
the tool calls from the answer as it arrived. Windows are aligned on the interval
(`ReplayInterval`, the reporter's own) and dated by the file name; a window in which
nothing was requested and no session closed is dropped rather than filed empty. The mapping a tool call's `Restored` is decided against is **the
trace's own, recovered from its two bodies** (`recoverMapping`): token indices are a
counter on the detector, so a replay numbers the same address `[EMAIL_2]` where the trace
said `[EMAIL_1]`, and an answer carries the trace's number. Token mode only; a `TODO` names
fake mode.

**Read-only, and deliberately so.** The batch is printed, never queued in `buffer.json`
and never sent: a bucket rebuilt from traces and one the agent filed live share no window
boundary, so the backend's `(agent, window)` key would not recognise the one as a retry of
the other, and every count for the period would double. Two things a trace does not hold,
so a replay never reports them: the client's User-Agent and how the provider answered.
`Restarts` is zero — no process started. The parser is tested against the tracer itself
(`TestReplayRebuildsTheHeartbeatFromTraces` writes its fixture with `tracer.write` and
`appendResponse`), so a format change on either side fails there.

### The golden must exercise the new field

`pkg/telemetry/testdata/heartbeats.json` is the shared golden — exported as
`ExampleHeartbeatsJSON` through `//go:embed` (`pkg/telemetry/example.go`), which is what
`TestAFullBatchFitsTheBackendsBodyBound` and the backend read — regenerated with
`go test ./pkg/telemetry/ -update-golden` and asserted by `TestHeartbeatWireFormat`. Adding
a field to the contract means updating it **in the same commit**, and the example must
*exercise* the field: `Addresses` is `omitempty`, so an example that left it out would be a
field neither repository ever tested on the wire — exactly the drift the shared golden
exists to catch. Use documentation ranges (RFC 5737, RFC 3849) so nothing in the example is
a real machine.

### Enrolment and signing

`EnrolRequest` / `EnrolResponse` (`contract.go:495`): the operator's token is presented once
and traded for a per-agent id and key, the way Wazuh's enrolment works, so one workstation
can be revoked without touching the others. Requests carry `X-Neverseen-Agent` and
`X-Neverseen-Signature`; `Sign` / `VerifySignature` (`contract.go:547`) are the HMAC pair,
shared by both repositories so they cannot disagree.

The issued identity is stored by `internal/telemetry/identity.go` at
`~/.neverseen/agent.json` by default, in a private directory
(`TestIdentityDirectoryIsPrivate`). An incomplete identity file is refused rather than
half-used (`TestIncompleteIdentityIsRefused`); a missing one is not an error.

## The recorder (`internal/telemetry/recorder.go`)

`Recorder` accumulates the current window. The request path feeds it per session:
`Request(session, client, provider)`, `Masked(session, counts)`, `Usage(session, model,
usage)`, `Tool(session, call)`; the control's own health arrives through `Refused()`,
`Upstream(status)` — classed as ok, rate-limited (429), rejected (4xx), failed (5xx) or
unreachable (0) — `Policy(applied)` and `Degraded()`; the buffer reports `Drop(n)`. `Take(now)` closes the window and starts a new one; `Snapshot(now)`
reads it without closing, and returns a **change count** — which is what makes an idle
workstation stop rewriting the same bytes every thirty seconds.

**A process builds exactly one `Recorder`, so the constructor is where a restart is
counted.** `NewRecorder(now)` opens with `Counters.Restarts` already at 1: there is then no
separate call anybody can forget, and no way for the tally to disagree with how many
processes there actually were (`TestARecorderCountsItsProcessStart`). It cannot be derived
from `State.StartedAt`, which only ever holds the current process — eleven restarts between
two heartbeats read there as one. The first bucket an agent ever files carries 1, and that
one is the install.

The recorder is safe under concurrency (`TestRecorderIsSafeUnderConcurrency`).

## The buffer (`internal/telemetry/buffer.go`)

**A window the backend refused becomes a bucket in a queue on disk, and a bucket is still
closed every interval while the backend is down.** That is what keeps the record's
five-minute grain through an outage: merged into one window instead, a supervision service
down over a weekend came back to a single report saying "eleven thousand requests, some time
between Friday and Monday" — which cannot answer when a spike happened, when an agent
restarted, or whether the policy changed halfway through.

**On disk**, because the buffer is otherwise only as durable as the process. A workstation
rebooted, suspended or updated mid-outage lost every queued bucket *and* the dropped counter
that recorded the loss, which is the one outcome that counter exists to prevent. The file is
written by **rename, not in place** (`writeFile`, `buffer.go:297`): a torn file is
unreadable at the next start, which loses exactly what the persistence was added to keep
(`TestATornBufferFileIsRefusedNotGuessed`). And a queue or live file that cannot be read
or parsed is **counted** into `dropped` (`loadBuffer`,
`TestAnUnreadableQueueCountsItsLoss`), for the reason two paragraphs down: a gap nobody
counted looks like a quiet period.

**Two bounds, because the interval is configurable**: seven days of age (`maxWindowAge`) and
2016 buckets (`maxBufferedBuckets`). An agent set to report every ten seconds reaches the
age bound having queued sixty thousand buckets. **Age is measured from where a bucket
*ends*, not where it starts** — a laptop suspended for a week wakes with one bucket covering
the whole week, and measured from the start it would be discarded the moment it was closed.
Whichever bound bites, the oldest go and **the loss is counted** into the bucket being closed
right then: a gap nobody counted looks exactly like a quiet period, and that figure is what
an auditor needs (`TestBufferBoundsAreEnforcedAndCounted`).

**The dropped count is on disk with the buckets**, not only in memory — it would otherwise
be lost by exactly the crash the file exists to survive
(`TestTheDroppedCountSurvivesARestart`). It keeps the file alive on its own: an empty queue
that discarded the count would report the gap as a quiet period. And **one unusable bucket
costs one bucket** — `keepUsable` (`buffer.go:168`) steps over it and counts it, rather than
refusing the file and throwing away every good bucket beside it
(`TestAnUnusableBucketIsSteppedOverAndCounted`).

**Two files, and that is the reason.** The queue is rewritten only when a bucket is closed or
delivered; the bucket in progress lives beside it under `livePathFor(path)` (`buffer.go:160`)
and is the one rewritten often. In one file, a snapshot every thirty seconds re-serialised
the whole backlog — during a long outage on a busy workstation, a megabyte of JSON onto the
disk twice a minute for as long as the outage lasted.

**The interval that closes a bucket clears the `live` entry with it**, and that is not
housekeeping — it is the whole hazard of the mechanism. For as long as both exist the same
counters are on disk twice and only one is still true; left behind, the next process files
both and every number for that period doubles.
`TestClosingABucketClearsTheLiveEntry` reads the **files** rather than the loaded queue,
because loading deliberately turns a `live` entry into an ordinary bucket and is the one
view in which the distinction no longer exists. An empty queue leaves no files behind
(`TestAnEmptyQueueLeavesNoFilesBehind`).

## The reporting loop (`internal/telemetry/reporter.go`)

`DefaultInterval` is 5 minutes. **`Run` holds two cadences, and they must stay apart**
(`reporter.go:184`): a **ticker** closes a bucket every interval whatever the backend is
doing, and a **separate timer** sends, on the retry ladder. Fused — one timer that both
closed the window and sent it — the window boundaries moved with the backend's health, which
is the outage bug above (`TestRefusedBucketsKeepTheirOwnWindows`).

**The backlog goes in one batch per request, sixty buckets at a time**
(`maxBucketsPerRequest`), oldest first, and while any remains the next attempt is a second
away rather than an interval — a week of buckets delivered one attempt per five minutes
would take a week again (`TestRunDrainsABacklogWithoutWaitingAnInterval`). Batched because
every workstation in the fleet comes back the moment the backend does: a week is two
thousand buckets, and two thousand signed requests per workstation is a recovery that ends
in a second outage. The bound is the backend's body limit, not politeness. The ordinary case
is the same message with one bucket — a separate shape for the single case would be a code
path exercised only after an outage.

**Retried on a ladder** — 1s, 5s, 10s, 20s, 40s, doubling on, capped at the reporting
interval (`retryAfter`, `reporter.go:307`). Short at the bottom because most failures are a
redeployment or one dropped connection, and waiting a whole interval to find that out has
the fleet view calling a healthy agent silent for five minutes; growing because a backend
genuinely down must not be hit every second by every workstation; capped at the interval
because a ladder that grew past it would have a backend recovering after an hour waiting
another hour to hear from anybody. **One success resets it.** A failure to *enrol* climbs the
same ladder — it is the same backend being unreachable
(`TestRetryLadderGrowsAndIsCapped`, `TestAnUnreachableBackendIsSurvivable`).

**A hard kill cannot be caught, so what bounds it is having written recently.**
`snapshotInterval` (30s, `reporter.go:51`) writes the bucket *in progress*, and the next
process files it as a bucket of its own — its window ending at the snapshot, not at the kill,
so nothing is attributed to a period nobody measured. What is still lost is bounded by that
interval; driving it to zero means writing on every request, which the request path must not
pay for (`TestAKilledProcessLosesOnlyWhatWasNotSnapshotted`).

At shutdown the command **waits for the last report** (`serveAgent`,
`cmd/neverseen/main.go`): without it, the process exits as soon as the server has shut down
and the final heartbeat is cut off mid-flight — the dashboard's last few minutes before a
restart are simply missing. The wait is bounded at 20s, because a hung backend must not stop
the agent from stopping (`TestTheLastWindowIsFiledBeforeTheCommandReturns`,
`TestRunReportsAtStartAndOnShutdown`).

## What `State` reports, and why it is read from the running server

`Server.State()` (`internal/proxy/telemetry.go:26`) reads the **running** server rather than the
configuration it was built with, because the question a security officer is asking is not
"what was it told to do" but "what is it doing" — an agent running with no locale selected
masks almost nothing while looking perfectly healthy. `proxy.Version` is a package variable
stamped by the command at start-up (`agent.go:28`, `cmd/neverseen/main.go:243`), because a dashboard showing "dev" for
every workstation is a fleet nobody can audit.

## Where to start on a change here

- Adding or changing a contract field ⇒ read `../cloakfleet-cloud` first, then update
  `contract.go`, `allowedStrings`, and regenerate the golden in the same commit. A map
  keyed on something the agent observes needs a vocabulary in `vocabulary.go` and
  `Other` for the rest.
- Adding a tool class or a program ⇒ `vocabulary.go` and `internal/telemetry/tools.go`,
  with a case in `TestReadCommandNamesProgramsAndClasses`.
- Changing cadence or persistence ⇒ `reporter.go` + `buffer.go`, and expect
  `TestClosingABucketClearsTheLiveEntry` and the outage/restart tests to be the gate.
- Never introduce an error return from here into the request path.
