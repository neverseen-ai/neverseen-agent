# CLAUDE.md

Guidance for Claude Code working in this repository.

Cloakfleet is an agent installed on each workstation. It proxies the AI tools
people use to a model provider, masking personal data and credentials on the way
out and restoring them on the way back. Module path:
`github.com/cloakfleet/cloakfleet`. Licence: FSL-1.1-ALv2 (source available, not
open source — say "source available").

The paid supervision backend is a **separate, private repository**
(`cloakfleet-cloud`), checked out alongside this one — `../cloakfleet-cloud`. It
imports this one; this one must never import it, and must compile and run with no
backend at all. Read it there when a change touches the shared contract
(`pkg/telemetry`); never add it as a dependency.

## Commands

```bash
make build            # bin/cloakfleet
make test             # go test -race ./...
make test-cover       # + coverage; the CI gate is 80%
make lint             # golangci-lint
make fmt              # gofmt -w . && go mod tidy

make score            # gate per-category accuracy against score-baseline.json
make score-update     # rewrite that floor from the current run — explain the delta
make bench-accuracy   # the per-category accuracy report
make e2e-claude       # real CLI, real provider, through the agent (spends quota)

go test ./internal/detector/ -run TestAccuracyCorpus -v   # one suite, verbose
```

## Architecture

**One entrypoint.** `cmd/cloakfleet` is the only binary. Agent Veil, which this
project replaces, shipped two, each assembling its own pipeline from the same
packages — and they drifted, so the same request was masked in one and answered
in clear in the other. Do not add a second `main`.

**The environment is read in one place**: `detector.FromEnv`, from the constants
`detector.EnvLocale` and `detector.EnvAllowList`. Never read an environment
variable in `cmd/`.

**Two layers, and the direction is one-way.** `pkg/pii` is the catalogue — what
counts as sensitive, how it is recognised, what it is called. `internal/detector`
is the engine that runs the catalogue over text. The catalogue knows nothing
about the engine.

**The detection pipeline**: the selected locales' patterns in registry order,
then the locale-independent identifiers, then the credentials → every regex hit
that clears its checksum and the reporting threshold → overlap resolution keeps
one match per stretch of text → matches in reading order.

**The request pipeline**: resolve the provider from the first path segment →
mask the body → save what was minted to the session vault → forward → expand the
response. Fail closed: a body the agent cannot read is a 415, never a
pass-through.

**A body is a JSON document, and is masked value by value — never as raw bytes.**
This is not a preference. A JSON string carries escapes, and a pattern reading
the bytes sees the characters those escapes are made of: in a real Claude Code
request containing `…pourquoi.\n\n@RTK.md`, the email pattern read `n@RTK.md` as
an address, took the `n` out of the `\n`, and left a lone backslash before a
bracket. Every request failed with "invalid escaped character". The same applies
in reverse — an original carrying a quote or a newline cannot be spliced into raw
JSON. See `internal/proxy/jsonbody.go`.

**Only bracket tokens are ever expanded** (`pii.IsToken`). Never loosen that
filter to match a value rather than a token: it is what stops a masked credential
from being expanded into a live secret on its way to a caller, and it is why
`fake` mode is one-way at the proxy.

**Streaming holds back a tail.** Generated text arrives in pieces of a few
characters, so a token the model echoed is regularly split across two events. The
rehydrator keeps back anything that could be the start of a token and prepends it
to the next piece, so what it emits is always a whole event — occasionally a few
characters shorter, with those characters moving to the event after it.

**`/test` is a real tool, not a demo.** It renders one text in both substitution
modes side by side, using the deployment's own detector, and it is how the two
shape bugs and the locale bug below were found. Routes the agent answers itself
are reserved (`reservedRoutes`), so a provider cannot take one.

**A stand-in is chosen by the locale that recognised the value**, not by the set
of enabled locales. Merging the per-locale tables into one map means a shared
category — telephone, address, postcode — resolves to whichever locale merged
last: with `fr,gb,us` on, a French number came out as "(555) 555-0100". The
matched pattern carries its `Locale`, and that is what picks the table.

**The agent holds no API keys.** The caller's credential is forwarded untouched,
because the tool making the request already has it. Adding key storage would make
a workstation agent one more place a key lives.

**Overlap arbitration, in order**: a credential always wins, then confidence,
then the longer span, then the leftmost. Each rule is there because its absence
leaked. Position deciding on its own let a postcode evict the address containing
it; the credential rank exists because several ordinary categories score above a
connection string, so `postgres://admin:pw@db` resolved to an *email* match over
the password — reversible, and expanded back into a live secret on the way out.

### Telemetry

**`pkg/telemetry` is public and the backend imports it — never the reverse.** The
agent has to compile, run and be useful with no backend in existence.

**Nothing in a heartbeat is content**, and `TestHeartbeatCarriesNoContent` walks
the type to keep it that way. A new string field fails until it is on the allow
list with a reason. Do not add one to make a dashboard nicer.

**`State.Addresses` is the one field in the contract that is personal data**, and
it is the exception that shows what the rule means. Everything else is a count, a
category name or a build string; an IP address identifies a machine and through it
a person. It is there because the fleet view's whole purpose collapses without it
— "agt_4742be… is silent" sends somebody to a database, "the laptop at 10.4.2.87
is silent" sends them to a desk. It is still not *content*: no prompt, response or
detected value can travel in it, which is the invariant the test actually defends.
Its entry in `allowedStrings` says so, and the README says so to the operator, so
a customer's DPO reads it in the documentation rather than finding it in a
database.

Local addresses, deliberately, not the public one: on a corporate network the
private address distinguishes one workstation from another while the egress
address is shared by the whole site. The backend records the address it *observes*
the connection from separately, which is also why this field being absent or wrong
costs nothing that matters. `localAddresses` (`internal/proxy/addresses.go`) drops
loopback and link-local, sorts within each family and caps the count — a list that
reshuffled between heartbeats would read as a machine whose addresses kept
changing, which on the dashboard is indistinguishable from a laptop moving
networks.

**Adding a field to the contract means updating the golden in the same commit.**
`testdata/heartbeats.json` is regenerated with `go test ./pkg/telemetry/
-update-golden`, and it must *exercise* the new field: `Addresses` is `omitempty`,
so an example that left it out would be a field neither repository ever tested on
the wire — which is exactly the drift the shared golden exists to catch. Use
documentation ranges (RFC 5737, RFC 3849) so nothing in the example is a real
machine.

**Nothing in `internal/telemetry` may reach the request path.** The reporter runs
on its own goroutine, returns no errors to anybody, and an agent with no backend
has no reporter at all rather than one that quietly does nothing. A supervision
backend that cannot be reached must never stop the masking, or the security
control is taken down by the tool that watches it.

**A window the backend refused becomes a bucket in a queue on disk**
(`internal/telemetry/buffer.go`), and a bucket is still closed every interval
while the backend is down. That is what keeps the record's five-minute grain
through an outage: merged into one window instead, a supervision service down over
a weekend came back to a single report saying "eleven thousand requests, some time
between Friday and Monday" — which cannot answer when a spike happened, when an
agent restarted, or whether the policy changed halfway through.

**So `Run` holds two cadences, and they must stay apart.** A ticker closes a
bucket every interval whatever the backend is doing; a separate timer sends, on
the retry ladder. Fused — one timer that both closed the window and sent it — the
window boundaries moved with the backend's health, which is the bug above.

**On disk because the buffer is otherwise only as durable as the process.** A
workstation rebooted, suspended or updated mid-outage lost every queued bucket
*and* the dropped counter that recorded the loss, which is the one outcome that
counter exists to prevent. The file is written by rename, not in place: a torn
file is unreadable at the next start, which loses exactly what the persistence was
added to keep.

**Two bounds, because the interval is configurable**: seven days of age, and 2016
buckets. An agent set to report every ten seconds reaches the age bound having
queued sixty thousand buckets. Age is measured from where a bucket *ends*, not
where it starts — a laptop suspended for a week wakes with one bucket covering the
whole week, and measured from the start it would be discarded the moment it was
closed. Whichever bound bites, the oldest go and **the loss is counted** into the
bucket being closed right then: a gap nobody counted looks exactly like a quiet
period, and that figure is what an auditor needs.

**The backlog goes in one batch per request, sixty buckets at a time**
(`maxBucketsPerRequest`), oldest first, and while any remains the next attempt is a
second away rather than an interval — a week of buckets delivered one attempt per
five minutes would take a week again. Batched because every workstation in the
fleet comes back the moment the backend does: a week is two thousand buckets, and
two thousand signed requests per workstation is a recovery that ends in a second
outage. The bound is the backend's body limit, not politeness. The ordinary case is
the same message with one bucket — a separate shape for the single case would be a
code path exercised only after an outage.

**And it is retried on a ladder** — 1s, 5s, 10s, 20s, 40s, doubling on, capped at
the reporting interval (`retryAfter`). Short at the bottom because most failures
are a redeployment or one dropped connection, and waiting a whole interval to find
that out has the fleet view calling a healthy agent silent for five minutes;
growing because a backend that is genuinely down must not be hit every second by
every workstation; capped at the interval because a ladder that grew past it would
have a backend recovering after an hour waiting another hour to hear from anybody.
One success resets it. A failure to *enrol* climbs the same ladder — it is the same
backend being unreachable.

**A hard kill cannot be caught, so what bounds it is having written recently.**
`snapshotInterval` (30s) writes the bucket *in progress*, and the next process
files it as a bucket of its own — its window ending at the snapshot, not at the
kill, so nothing is attributed to a period nobody measured. What is still lost is
bounded by that interval; driving it to zero means writing on every request, which
the request path must not pay for.

**Two files, and that is the reason.** The queue is rewritten only when a bucket is
closed or delivered; the bucket in progress lives beside it under
`livePathFor(path)` and is the one rewritten often. In one file, a snapshot every
thirty seconds re-serialised the whole backlog — during a long outage on a busy
workstation, a megabyte of JSON onto the disk twice a minute for as long as the
outage lasted.

**The interval that closes a bucket clears the `live` entry with it**, and that is
not housekeeping — it is the whole hazard of the mechanism. For as long as both
exist the same counters are on disk twice and only one is still true; left behind,
the next process files both and every number for that period doubles.
`TestClosingABucketClearsTheLiveEntry` reads the files rather than the loaded
queue, because loading deliberately turns a `live` entry into an ordinary bucket
and is the one view in which the distinction no longer exists.

**The dropped count is on disk with the buckets**, not only in memory — it would
otherwise be lost by exactly the crash the file exists to survive, which is the one
outcome that figure exists to prevent. It keeps the file alive on its own: an empty
queue that discarded the count would report the gap as a quiet period. And one
unusable bucket costs one bucket — `keepUsable` steps over it and counts it, rather
than refusing the file and throwing away every good bucket beside it. Nothing is written while
nothing is counted — `Recorder.Snapshot`'s change count is what makes an idle
workstation stop rewriting the same bytes every thirty seconds.

**A process builds exactly one `Recorder`, so the constructor is where a restart
is counted.** `Counters.Restarts` is per bucket, and `NewRecorder` opens with one
already counted: there is then no separate call anybody can forget, and no way for
the tally to disagree with how many processes there actually were. It cannot be
derived from `State.StartedAt`, which only ever holds the current process — eleven
restarts between two heartbeats read there as one. The first bucket an agent ever
files carries 1, and that one is the install.

**Reading token usage is where the vendors disagree about more than spelling.**
Anthropic reports cache tokens *beside* the input; OpenAI reports the whole input
with the cached part broken out underneath as a *subset*. Reading both would bill
the same tokens twice, so the OpenAI breakdown is deliberately not read.

### Distribution

**The installer never exports a base URL into a shell profile.** It adds
`eval "$(cloakfleet env)"`, and that command prints nothing when the agent is not
answering — so stopping the agent leaves the tools working and unmasked instead
of broken. Agent Veil exported unconditionally and took every LLM tool on the
machine down with the proxy. Availability over enforcement, on purpose.

**It touches no login file unless asked** (`--shell`), and `--uninstall` undoes
exactly what it added. It leaves `~/.cloakfleet/` alone, because that holds the
operator's config, the identity a backend knows the machine by, and any buckets
not yet delivered — deleting the identity silently would have the next install
enrol as a second agent and count twice against what they pay for, and deleting
the buffer would throw away the record of an outage that is still in progress.

## Conventions

- Conventional Commits (`feat:`, `fix:`, `docs:`, `test:`, `refactor:`).
- **British spelling** in comments and prose — the linter is configured for it.
- Comments say *why*, and name the failure a rule prevents. A comment that
  restates the code is noise.

### Adding a PII category

One entry in `categoryRegistry` (`pkg/pii/category.go`) carrying the token
prefix, the score, the checksum if there is one, and whether it is a credential
— plus its pattern in the right set. That is the whole of it: `validateCatalogue`
panics at package initialisation if a pattern emits a category with no entry, so
the four-places-to-forget problem cannot come back.

Then a corpus case, then `make score-update`. `TestScoreCorpus` derives what it
demands from the catalogue itself, so **a category with no case fails the run** —
an empty tally scores 100% precision and 100% recall, and an unmeasured category
is indistinguishable from a perfect one.

Add cases that must come out **untouched**, not just cases that must be found.
Recall alone cannot fail a pattern: one widened to match everything scores 100%
recall and passes.

### Adding a locale

One entry in `localeRegistry` (`pkg/pii/locale.go`) — a code, a `Priority`, a
pattern set — plus `patterns_<code>.go`, a corpus suite declaring
`locale: <code>`, a block in `.env.example`, and `make score-update`. Three tests
fail on the commit that forgets any of those.

`Priority` is load order, and load order is a decision: it settles which country
names a value two of them could both claim. Nine bare digits are a French SIREN
under Luhn and a US routing number under the ABA weights.

Next locales, in order of market size: Germany, Spain, Italy, the Netherlands.

### The sample is a reference, and must stay one

`pkg/pii/sample.go` holds one sample per locale plus the locale-independent and
credential sections. Each has to carry every category its set detects **and every
notation each pattern accepts** — all eleven French day-first date forms, both ISO
separators, an identifier compact and spaced, an address with and without its
town. It is what an operator reads to check their own data shape is covered, so a
gap in it reads as a gap in the engine.

Any change to the catalogue — a new category, a newly accepted notation, a
widened or narrowed pattern — means updating the sample in the same commit. Three
tests hold it, for every locale in the registry: one sweeps the live catalogue so
a new category with no line fails; one is an explicit table of every accepted form
with the category it must be read as; one covers the twelve month names, which are
twelve alternatives in one expression where a typo silently loses a month.

The tables are deliberately not derived from the detector. A derived expectation
agrees with whatever the detector does, including a form it silently stopped
reading.

### Patterns

- **A checksum lets a shape be loose; without one, the shape is all there is.**
  `CategoryInfo.Verify` is where a checksum goes, and a value that fails it is
  dropped outright rather than scored down.
- **RE2 has no lookbehind or backreference.** A pattern that must reject a
  preceding character consumes it and points `Group` at the value. Separators
  that have to agree ("23/02-2004" is not a date) need one alternative per
  separator, not a character class.
- **Horizontal whitespace only** (`[ \t]`, not `\s`) in any span that could run
  long. With `\s` an address swallowed the first word of the next line, so the
  replacement ate ordinary text.
- **Go's `\b` is ASCII.** It finds a boundary inside an accented run, which once
  turned `andré.muller@example.fr` into a token bound to a fragment with the
  start of the address forwarded in clear. Use a leading character class and
  `Group` instead.
- **Beware `(?i)` over a long repetition.** It let the IBAN expression walk
  through a sentence claiming lowercase words as groups; requiring groups of
  exactly four fixed the whole class.

## Lessons not to reimport

- **Nothing enters the tree unexercised.** Agent Veil carried two fully
  implemented, tested packages that nothing imported, while its README
  advertised both. A feature that has never run is not a feature.
- **No documentation ahead of the code.** It documented five environment
  variables nothing read. `TestDocumentedEnvironmentMatchesTheCode` now fails on
  that in both directions.
- **CI must trigger on the real default branch.** Its workflow was keyed to
  `main` while the default branch was `master`, so it never ran.
- `version` is a `var`, not a `const` — declared `const`, the `-ldflags` stamp is
  silently inert and every release reports the same string.
