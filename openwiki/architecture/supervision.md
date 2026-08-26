# Supervision (`pkg/telemetry` + `internal/telemetry`)

Supervision is **optional, and the agent is a complete product without it**. Set
`CLOAKFLEET_BACKEND_URL` and an enrolment token and the agent reports every five minutes:
how many requests it proxied, how many values it masked in which categories, how many
tokens went to which model, and what configuration it is actually applying.

## Two hard rules

**`pkg/telemetry` is public and the backend imports it — never the reverse.** The paid
backend lives in a separate private repository (`../cloakfleet-cloud`). This repo must
compile, run and be useful with no backend in existence. Read the backend there when a
change touches the shared contract; never add it as a dependency.

**Nothing in `internal/telemetry` may reach the request path.** The reporter runs on its own
goroutine, returns no errors to anybody, and an agent with no backend has **no reporter at
all** rather than one that quietly does nothing (`reporterFromEnv`,
`internal/proxy/env.go:155`, `TestNoBackendMeansNoReporter`). A supervision backend that
cannot be reached must never stop the masking, or the security control is taken down by the
tool that watches it.

## The contract (`pkg/telemetry/contract.go`)

`SchemaVersion` is 2. A `HeartbeatBatch` carries the agent id, `SentAt`, the live `State`,
and a slice of `Bucket` — each bucket a `Window` (start, end) plus `Counters`.
`Windows()` (`contract.go:101`) expands a batch into the single-window `Heartbeat` shape.

`Counters` holds `Requests`, `Masked map[string]int` (per category name), `Models
map[string]TokenUsage`, `Restarts` and `Dropped`. `TokenUsage` is **four numbers, not two** —
`Input`, `Output`, `CacheWrite`, `CacheRead` — because a coding agent re-sends its whole
context every turn and almost all of its input is a cache read, an order of magnitude
cheaper and far more numerous. Folding them together overstates the bill; leaving them out
understates it. The agent reports raw counts and the backend prices them, because prices
change and an agent that computed money would need redeploying to every workstation each
time one did.

### Nothing in a heartbeat is content

`TestHeartbeatCarriesNoContent` (`contract_test.go:36`) **walks the type** and fails on any
string field not on an explicit allow list, each entry carrying its reason. A new string
field fails the build until it is listed. Do not add one to make a dashboard nicer.

**`State.Addresses` is the one field in the contract that is personal data**, and it is the
exception that shows what the rule means (`contract.go:197`). Everything else is a count, a
category name or a build string; an IP address identifies a machine and through it a person.
It is there because the fleet view's whole purpose collapses without it — "agt_4742be… is
silent" sends somebody to a database, "the laptop at 10.4.2.87 is silent" sends them to a
desk. It is still not *content*: no prompt, response or detected value can travel in it,
which is the invariant the test defends. Its `allowedStrings` entry says so, and the README
says so to the operator, so a customer's DPO reads it in the documentation rather than
finding it in a database.

**Local addresses, deliberately, not the public one.** On a corporate network the private
address distinguishes one workstation from another while the egress address is shared by the
whole site; the backend records the address it *observes* separately, which is also why this
field being absent or wrong costs nothing that matters. `localAddresses`
(`internal/proxy/addresses.go:33`) drops loopback and link-local, sorts within each family
and caps the count at 4 — a list that reshuffled between heartbeats would read as a machine
whose addresses kept changing, indistinguishable on the dashboard from a laptop moving
networks (`TestLocalAddressesAreStablyOrdered`).

### The golden must exercise the new field

`pkg/telemetry/testdata/heartbeats.json` is the shared golden, regenerated with
`go test ./pkg/telemetry/ -update-golden` and asserted by `TestHeartbeatWireFormat`. Adding
a field to the contract means updating it **in the same commit**, and the example must
*exercise* the field: `Addresses` is `omitempty`, so an example that left it out would be a
field neither repository ever tested on the wire — exactly the drift the shared golden
exists to catch. Use documentation ranges (RFC 5737, RFC 3849) so nothing in the example is
a real machine.

### Enrolment and signing

`EnrolRequest` / `EnrolResponse` (`contract.go:267`): the operator's token is presented once
and traded for a per-agent id and key, the way Wazuh's enrolment works, so one workstation
can be revoked without touching the others. Requests carry `X-Cloakfleet-Agent` and
`X-Cloakfleet-Signature`; `Sign` / `VerifySignature` (`contract.go:319`) are the HMAC pair,
shared by both repositories so they cannot disagree.

The issued identity is stored by `internal/telemetry/identity.go` at
`~/.cloakfleet/agent.json` by default, in a private directory
(`TestIdentityDirectoryIsPrivate`). An incomplete identity file is refused rather than
half-used (`TestIncompleteIdentityIsRefused`); a missing one is not an error.

## The recorder (`internal/telemetry/recorder.go`)

`Recorder` accumulates the current window: `Request()`, `Masked(counts)`, `Usage(model,
usage)`, `Drop(n)`. `Take(now)` closes the window and starts a new one; `Snapshot(now)`
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
written by **rename, not in place** (`writeFile`, `buffer.go:287`): a torn file is
unreadable at the next start, which loses exactly what the persistence was added to keep
(`TestATornBufferFileIsRefusedNotGuessed`).

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
costs one bucket** — `keepUsable` (`buffer.go:158`) steps over it and counts it, rather than
refusing the file and throwing away every good bucket beside it
(`TestAnUnusableBucketIsSteppedOverAndCounted`).

**Two files, and that is the reason.** The queue is rewritten only when a bucket is closed or
delivered; the bucket in progress lives beside it under `livePathFor(path)` (`buffer.go:150`)
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
`cmd/cloakfleet/main.go`): without it, the process exits as soon as the server has shut down
and the final heartbeat is cut off mid-flight — the dashboard's last few minutes before a
restart are simply missing. The wait is bounded at 20s, because a hung backend must not stop
the agent from stopping (`TestTheLastWindowIsFiledBeforeTheCommandReturns`,
`TestRunReportsAtStartAndOnShutdown`).

## What `State` reports, and why it is read from the running server

`Server.State()` (`internal/proxy/env.go:187`) reads the **running** server rather than the
configuration it was built with, because the question a security officer is asking is not
"what was it told to do" but "what is it doing" — an agent running with no locale selected
masks almost nothing while looking perfectly healthy. `proxy.Version` is a package variable
stamped by the command at start-up (`env.go:207`), because a dashboard showing "dev" for
every workstation is a fleet nobody can audit.

## Where to start on a change here

- Adding or changing a contract field ⇒ read `../cloakfleet-cloud` first, then update
  `contract.go`, `allowedStrings`, and regenerate the golden in the same commit.
- Changing cadence or persistence ⇒ `reporter.go` + `buffer.go`, and expect
  `TestClosingABucketClearsTheLiveEntry` and the outage/restart tests to be the gate.
- Never introduce an error return from here into the request path.
