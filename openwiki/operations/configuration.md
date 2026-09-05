# Configuration and the operator surfaces

## The environment, and who reads it

**Each setting has exactly one owner in the code, and no command in `cmd/` reads an
environment variable.** The detector's settings are read by `detector.FromEnv` from the
constants in `internal/detector/config.go:18-27`; the proxy's by
`internal/proxy/env.go:23-45`. Two readers of one setting are two chances to disagree about
it, which is how Agent Veil ended up applying a default on one path and not the other.

| Variable | Owner | Default | Notes |
| --- | --- | --- | --- |
| `CLOAKFLEET_PII_LOCALE` | `detector.EnvLocale` | unset = **none** | `fr`, `gb`, `us`, `none`, or a comma-separated mix |
| `CLOAKFLEET_PII_ALLOWLIST` | `detector.EnvAllowList` | empty | values never to mask, comma-separated; compared ignoring case and spacing |
| `CLOAKFLEET_PII_SUBSTITUTION` | `detector.EnvSubstitution` | `token` | `token` or `fake` |
| `CLOAKFLEET_LISTEN` | `proxy.EnvListen` | `127.0.0.1:8787` | loopback on purpose — see below |
| `CLOAKFLEET_PROVIDERS` | `proxy.EnvProviders` | none | `code=url` pairs, applied as **overrides** onto the default set |
| `CLOAKFLEET_ENCRYPTION_KEY` | `proxy.EnvEncryptionKey` | generated per process | 32 bytes, hex, for the session mapping |
| `CLOAKFLEET_BACKEND_URL` | `proxy.EnvBackendURL` | unset = **no supervision at all** | no reporter is built; a whole feature rather than a disabled one |
| `CLOAKFLEET_ENROLMENT_TOKEN` | `proxy.EnvEnrolmentToken` | unset | presented once, traded for a per-agent identity |
| `CLOAKFLEET_IDENTITY_FILE` | `proxy.EnvIdentityFile` | `~/.cloakfleet/agent.json` | where the issued identity is kept |

Every one of these is documented in **`.env.example`**, and
`TestDocumentedEnvironmentMatchesTheCode` (`cmd/cloakfleet/env_test.go:22`) fails in **both**
directions: a variable the code reads and the file does not mention fails the build, and so
does one the file documents and no code reads. `TestUsageNamesEverySetting` holds the usage
text to the same standard — `printUsage` builds it from the constants and the locale
registry, so nothing in it can go stale under a rename.

**`CLOAKFLEET_PII_LOCALE` unset means none, deliberately.** Scanning one country's data with
another country's patterns is worse than scanning none of it, and an operator who never set
the variable has not chosen that.

**`CLOAKFLEET_LISTEN` is not a default to override lightly** (`env.go:60-66`). The agent
trusts whoever reaches it — it forwards their credentials and scopes the session mapping by a
header they control — so it is built for one person on one workstation. Bound to a reachable
interface it becomes a way to read another user's session.

`~/.cloakfleet/` holds the operator's config, the identity a backend knows the machine by,
the policy a surface last applied, and any buckets not yet delivered. `install.sh
--uninstall` deliberately leaves it alone.

**The stored policy wins over every setting in the table above** that it covers — the
locales, the substitution mode, the secret level — see *What a surface changed survives
the restart* below. The environment configures an agent nobody has said anything to yet.

## Commands (`cmd/cloakfleet`)

```
cloakfleet proxy         run the agent: mask what goes out, restore what comes back
cloakfleet proxy -a      also print every value it replaces and restores, in clear
cloakfleet proxy -v      also write every exchange to ./traces: both bodies and the answer
cloakfleet scan [file]   report the sensitive values in a file, or in stdin
cloakfleet status        report whether the agent is masking, and what
cloakfleet mask          list what is masked, and switch a category or family off
cloakfleet env [--force] print the shell exports that point a tool at the agent
cloakfleet replay <dir>  rebuild the heartbeat batch from the traces in a
                         directory and print it; nothing is sent or queued
cloakfleet version       print the version
```

`run` (`main.go:105`) is `main`'s body with its inputs and output passed in, so every command
is testable without a subprocess.

### `proxy`

`serve` → `proxy.FromEnv(logger, opts)` → `serveAgent`. The graceful stop is not politeness:
a request cut off mid-flight has been masked and stored but never answered, so the caller
loses the turn and the mapping keeps values nothing will ask for again. `serveAgent` is
shared by `proxy` and `audit` so the two cannot come to differ about shutdown, supervision or
the last heartbeat.

#### `-l`, and what leaves the loopback default behind

`cloakfleet proxy -l 0.0.0.0:8787` serves every interface. The address was already
reachable through `CLOAKFLEET_LISTEN`; the flag is the same setting where a one-off run
can reach it, and `Options.Listen` wins over the variable — the command's choice does,
for every option, so an operator serving a container for one run does not have to unset
a profile (`TestTheListenFlagOverridesTheEnvironment`).

**The warning hangs off the address, not off the flag.** `proxy.BeyondLoopback` is asked
in `serveAgent`, where the agent starts listening, so both routes to a reachable address
go through one predicate and one message. Written on the flag, the variable — which
exposes exactly as much — would have gone on warning nowhere.

`:8787` counts as reachable, and that case is the reason the predicate is a function
rather than a comparison against `DefaultListen`: it reads as "no address given" and
binds every interface. A bare name counts too, because it resolves to whatever DNS says.

**It warns; it does not refuse.** Serving a container or a VM on this workstation is a
real thing to want, and an agent that refused is one somebody patches out — which ends
with the same bind and no warning at all. The message names the exposure rather than
scolding, because the person reading the log six months later is not the person who set
it:

```
level=WARN msg="this agent is reachable beyond this workstation" address=0.0.0.0:8787
  unauthenticated="/healthz and /test"
  why_it_matters="/test masks any text on request, and a session is named by a header
  the caller chooses, so a caller that guesses one is handed its replacements"
```

That is the whole cost, stated: `/healthz` and `/test` are unauthenticated **because**
of the loopback default. Reachable, `/test` is a masking oracle for anybody on the
network, and `sessionFrom` reads a header the caller controls — so naming somebody
else's session is enough to be handed its replacements. `PUT /policy` is unaffected: it
carries the control key whatever the interface.

The default stays quiet (`TestTheDefaultAddressDoesNotWarn`). A warning on every
ordinary start is one nobody reads by the time it matters.

### `status` — what is being *applied*, not that the process is up

`runStatus` (`main.go:141`) calls `proxy.Query` and exits non-zero unless
`Status.Level()` is `LevelFull`. **Answering is not enough, and neither is masking:** an agent with no locale selected is
up, healthy and recognises almost nothing, and a check that called that healthy would be the
check somebody trusted while their traffic went out in clear
(`TestAnAgentWithNoLocaleIsNotMasking`) — and an agent with a category switched off is
masking everything except the thing somebody switched off, which zero must not mean either.
That state prints the names of what is in clear, not a count: a name is what tells somebody
whether the category they care about is among them.

The non-zero exit uses `errQuiet` (`main.go:88`) — exit 1 with no extra message — because the
command's whole job is to report a state, and printing the same diagnosis again on stderr
prefixed as an error would make the ordinary case of a stopped agent read like a malfunction.

### `env` — prints nothing when the agent is stopped

`proxy.ShellEnv` (`internal/proxy/shellenv.go:128`) asks the agent whether it is running and
writes **nothing** if it is not (unless `--force`). That is the whole point: a login file
evaluating `eval "$(cloakfleet env)"` has to be a no-op on a machine where the agent is
stopped. See [Distribution](distribution.md#pointing-a-tool-at-the-agent) for the failure this
avoids and the `shellTools` table it reads.

### `mask` — see and change what is masked

```
cloakfleet mask                       list what is applied: mode, countries, categories
cloakfleet mask --off EMAIL,DOB       stop masking these
cloakfleet mask --on DOB              mask them again
cloakfleet mask --off personal        a whole family, by its name
cloakfleet mask --reset               mask everything again
cloakfleet mask --substitution fake   change what a masked value becomes
cloakfleet mask --locales fr,gb       load these country pattern sets
cloakfleet mask --locales none        load none of them
```

**Every change carries the whole state**, because the route replaces it rather than
patching it — a request naming only `--off` would wipe the locale selection. The
command reads the current state, applies what was named, and sends all three.

**`--locales` replaces the selection**, so it names every country wanted; `none` is
spelled out rather than expressed as an empty argument, because `--locales ""` is what
a shell produces from an unset variable by accident and must not silently mean "stop
looking for anything".

It exists because the menu bar is Cocoa: a Linux workstation had the route and no way
to reach it but `curl`. It reads and writes through `proxy.Query` and
`proxy.SetPolicy` — the one asker and the one writer — so it cannot come to disagree
with the menu bar about what a category is called or how it is switched, and it reads
no environment of its own.

**A name may be a category or a family**, because the point of grouping forty
categories is that people think in families. The two namespaces cannot collide: a
family is lower case, a category upper.

**Only what the agent can actually find is listed.** With one locale loaded the
catalogue holds categories whose patterns are not in the detector at all
(`Detector.Categories`), and a switch for a US social security number on a French
deployment would say the agent is masking them.

**A locked category named directly is an error; named through its family it is
skipped.** Somebody typing `--off SECRET_ANTHROPIC_KEY` has a belief about what the
agent will do and the only useful answer is that it will not — while `--off secrets`
is a reasonable thing to try, and refusing the whole request over it would leave
nothing switched.

**`--off` is a read-modify-write, and that is a deliberate exception.** The route
takes the whole set precisely to avoid one, because the hazard is two *surfaces*
changing the policy at the same moment. This is a person at a keyboard who has just
been shown the state and is about to be shown it again: the command prints the
catalogue from the agent's reply afterwards, which is the same mitigation the menu bar
uses when it redraws from the reply rather than from its own click.

The first column is a word rather than a tick, so `cloakfleet mask | grep "in clear"`
answers the question the command exists for. A symbol would need a legend and would
not survive a pipe.

#### Changing the mode clears the session mappings

`--substitution` is the one setting that discards state, and it has to be.

A session's mapping is consulted *before* the mode is: `Pass.mask` returns the
replacement a value was first given and only calls `Detector.render` — the one place
the mode is read — when the value is new. That is what lets a conversation straddling
the change round-trip, and on its own it is right.

What made it wrong in practice is the session. Nothing a workstation runs sends
`X-Session-Id` or `X-Request-Id`, so everything shares the one session named
`default` (`proxy.go`), whose 30-minute lifetime is refreshed by every use and
therefore never expires while somebody is working. Every value the agent had handled
since it started was already minted as a token, so switching to `fake` changed
nothing anybody could observe — the control appeared not to work at all.

So `handlePolicy` calls `vault.Forget` when the mode actually changes. The cost is
stated rather than hidden: replacements minted before the change stop being restored,
and an exchange whose answer has not come back yet returns one nothing expands. That
window is one exchange wide, against a control that otherwise looked broken.

The purge is guarded on the mode having *changed*, not on the request naming one.
Every surface resends the whole state on every click, because the route replaces
rather than patches — purging on each would discard the mapping when somebody merely
switched off a category, and the answer to the exchange in flight would come back
unexpandable. `TestPolicyChangingTheModeClearsWhatWasAlreadyMinted` and
`TestPolicyResendingTheSameModeKeepsTheMapping` are the two halves, and the second
fails on a purge that is not guarded.

#### What a surface changed survives the restart

`internal/proxy/policyfile.go`.

Everything the menu bar, `cloakfleet mask` and the test page can change went through
`PUT /policy` and lasted exactly as long as the process. A person unticked a category,
restarted the workstation, and the agent came back masking it again while the menu they
had set said otherwise on the next click — a control whose settings are forgotten is one
nobody can rely on having set.

So the route stores what it applied in **`~/.cloakfleet/policy.json`**, and `FromEnv`
reads it at start-up. The file holds the same four fields the route carries, in the same
shape, because it *is* that request: what the agent would have to be sent to arrive where
it is. One document rather than a field per setting, for the reason the route replaces the
whole state rather than patching it — a half-applied selection is a state nobody asked
for.

- **It is written from the detector, not from the request.** The route is not a
  transaction: a request whose locales were accepted and whose category was refused
  leaves the locales applied. What has to survive the restart is what the agent *is*, so
  `persistPolicy` runs on the refusal as well as on the success —
  `TestAPartlyRefusedChangeIsStoredAsItLanded`.
- **But only when something was applied.** Written from a `defer` above the applier, a
  request refused before the first change — a misspelled `substitution`, refused with 422
  — created the file for the first time, and from then on the agent read its own empty
  policy in preference to the environment: `CLOAKFLEET_PII_LOCALE` in the operator's
  profile did nothing, permanently, over a request that changed nothing. The file's
  absence has to keep meaning "nobody has", so `applyPolicy` reports `applied` and the two
  kinds of refusal are told apart. `TestARequestRefusedBeforeAnythingAppliedStoresNothing`
  is the counterpart of the case above and neither is meaningful alone.
- **One writer at a time, and a temporary name of its own.** Storing the file is a
  read-modify-write — apply, read the detector back, store — and the menu bar and
  `cloakfleet mask` interleave the halves of one, which is the hazard the route already
  names for the mapping. The handler takes `policyMu`, and the write goes through
  `os.CreateTemp` rather than a fixed `.tmp`: sharing one temporary path, two writers
  truncated and filled it under each other and what landed under the rename was one
  writer's document inside the other's call, or the two torn together — which parses to
  nothing and hands the agent back to the environment at the next start, silently.
  `TestConcurrentChangesLeaveAWholeFile`, under `-race`.
- **`off` is the intent, not the effect.** The whole switched-off set goes to disk,
  including categories no loaded locale can emit, because that is what the policy
  remembers — dropping the unreachable ones would silently switch a category back on the
  day its locale was loaded again. It is the same distinction `disabledInPlay` draws for
  the level.
- **It wins over the environment, and that is the point.** Once a surface has written a
  policy, that policy is the state; otherwise the click does not survive the restart and
  the file has no purpose. The cost is real: with the file present, changing
  `CLOAKFLEET_PII_LOCALE` in a profile does nothing. **Deleting the file hands the agent
  back to the environment**, and the agent says on every start which of the two it read.
  `TestAChangeSurvivesARestartAndBeatsTheEnvironment` asserts it through the two real
  halves — the route, then `FromEnv` — because a file written correctly and never read
  would pass a test and change nothing about the agent.
- **A file that cannot be read leaves the environment alone rather than stopping the
  agent.** Masking configured by a profile is a working agent; a state read from half a
  document is not. `TestAnUnreadableStoredPolicyLeavesTheEnvironmentAlone`.
- **One applier for the two callers.** `applyPolicy` is what the route and the stored file
  both go through, so an agent restarted into its saved state applies it exactly as the
  click did — two appliers is how a category comes to be switched off through a menu and
  back on through a restart.
- **Directory `0700`, file `0600`, written by rename** — the treatment the control key
  gets, for a weaker reason than a secret but a real one: it says which categories this
  workstation stopped masking, which describes what its user handles. Rename because a
  torn file reads as unparseable on the next start, and a crash mid-write would silently
  undo the change it was recording.
- **Nothing is written until something changes one.** The absence of the file has to keep
  meaning "nobody has said anything", which is what makes the environment's turn honest.

### `scan` — the offline check

Reads a file or stdin, assembles the detector exactly as the agent does —
`proxy.DetectorFromEnv`: the environment, then the stored policy, which wins — and prints
every value that would be masked
with its category, label, byte offsets and confidence, then a **per-category tally**. That
tally is the line that matters to an operator checking their own data is covered: a value they
expected to see named, missing from it, is a gap in the catalogue for their data shape.

## `/test` — a real tool, not a demo

While the agent runs, `http://127.0.0.1:8787/test` renders one text three ways: as written,
masked with tokens, and masked with stand-ins — **using the deployment's own detector**, its
locales and its allow list, rather than a demonstration built on defaults, which would answer
a different question than the one being asked.

It answers the two questions a log line cannot:

1. **Which substitution mode to run.** A model reasons better about prose than about
   brackets, but a stand-in in an answer is a value nobody can check.
2. **Whether the catalogue reads *your* data** — the one that actually costs deployments
   time. Paste a real record with the values changed and see in one screen what would leave
   the machine and what would not.

It also **proves the claim rather than describing it**: the token column is unmasked again and
compared to the input, so the page says whether the text round-trips exactly
(`TestTestPageReportsTheRoundTrip`).

Deliberately plain: a form POST re-renders server-side, no JavaScript, nothing to keep in
sync. The one thing it must get right is escaping, since it echoes text somebody typed —
hence `html/template` rather than concatenation (`TestTestPageEscapesWhatItEchoes`). Input is
capped at 32 KiB (`playgroundMaxBytes`): the catalogue is dozens of expressions run over the
whole input, and a page accepting a POST should not accept a megabyte of adversarial text.

**Nothing on it is sent anywhere, stored, or written to the session vault**
(`TestTestPageTouchesNothing`), and it is not counted as a request
(`TestTheTestPageIsNotCounted`). `/test` is a reserved route, so a provider cannot take it.

It is also how the two body-shape bugs and the locale stand-in bug were found.

## `-a` and `-v`: the one place a real value is printed or kept

Everywhere else the rule holds without exception: the log carries counts and category
names, the heartbeat carries no content at all. Those are read by somebody **other
than** the person whose data it is — a dashboard, a support ticket, a log shipper — so
a value reaching one of them has left the machine as surely as if it had gone to the
model.

These two flags are the opposite situation: **one operator, at their own keyboard, on
their own data**, asking "is my address actually being replaced" — which no count
answers.

```
cloakfleet proxy -a    print every value replaced on the way out and restored on the way back
cloakfleet proxy -v    write every exchange to ./traces (both bodies and the answer), one file each
```

**They are independent.** `-a` alone prints and keeps nothing; `-v` alone records and
prints nothing, which is what somebody wants when they mean to read the traffic
afterwards rather than watch it go past. `newAuditor` builds an auditor for either —
requiring a console writer to record a trace would have made the quiet half silently
do nothing.

### What replacing the `audit` command cost

There used to be a `cloakfleet audit` command on a port of its own (33333), and being
a *command* was the guarantee: printing a value in clear was a mode somebody entered,
and no environment variable could turn it on under the background service.

A flag can go in a service definition. The installer sends this agent's output to
`~/.cloakfleet/agent.log`, so `-a` in a launchd plist writes every prompt, in clear,
to a file, for as long as the service runs. **Neither flag belongs in one**, and the
banner says so on every start rather than leaving it in this page.

### The console carries the transformations, not the bodies

`-a` prints the two rules that bracket an exchange — the session and the size, so
something scrolling past can be accounted for — and one line per value:

```
── IN   from the tool · session=default · 110 B ──────────────────────────────
MASK pierre.paul@example.com TO [EMAIL_1]
MASK 06 12 34 56 78 TO [PHONE_1]
── OUT  to anthropic · session=default · 91 B ────────────────────────────────
     bodies in traces/20260828T081721-0001-default-anthropic.txt
```

Blue is a value in clear, red a replacement, on the MASK line and the UNMASK line
alike — one colour, one meaning, or the eye has to re-learn the palette per line.
Colour is decided by asking the writer whether it is a terminal (`isTerminal`), not by
reading `NO_COLOR`: that is the environment rule, and redirecting to a file is already
what somebody setting it would be doing.

**The bodies are not on screen**, because a coding tool resends tens of kilobytes of
system prompt every turn and they scroll the MASK lines away — and those lines are what
an operator is watching. The body-marking that used to make them readable went with
them rather than being left as a painter nothing calls.

**A tool call is the exception, and the only one.** When the model asks for an
execution, the console names the tool and prints the arguments it asked for it with,
restored — which is what the tool on this workstation will act on:

```
TOOL SendMail {"to":"pierre.paul@example.com"}
```

The rest of an answer is prose for a person to read; a tool call is an instruction
about to be carried out, so this is the line that says whether the masking held all
the way to the thing that acts. Green because it comes back from the provider, the
direction `UNMASK` already uses; the arguments blue, because after restoration that is
what they are.

One line per call, whatever the document's size, and printed once: the streaming path
reports at the moment the fragments become a whole document (`expandedArguments`), so
a value split across two events is shown restored rather than in halves. A buffered
answer is walked for the same calls (`reportToolCalls`), because shown for a streaming
client alone the silence would read as an answer that asked for no tools. Anthropic's
shape only — the OpenAI-compatible families put theirs in `tool_calls`, which this
agent does not yet read on either path.

**What it costs.** These arguments are the caller's own paths, commands and addresses,
in clear, one document at a time — so under the installer's service definition `-a`
files every tool call of every exchange in `~/.cloakfleet/agent.log` for as long as it
runs, and a `Write` call of several kilobytes will scroll the MASK lines away. There is
no ceiling on the width yet; the `TODO:` in `audit.go` says so.

A value is named **once as it is minted** and once per response as it is restored, not
once per occurrence: a system prompt resent every turn would bury the exchange being
watched (`Pass.Reveal`).

### The trace file

One file per exchange, holding the header, the transformations, and both bodies:

```
session:  default          ── IN   from the tool ──────────────
provider: anthropic        {
in:       110 bytes          "prompt": "écris à pierre.paul@example.com, matricule ZZ-4471"
out:      91 bytes         }
replaced: 2 value(s), 2 of them first seen in this session
                           ── OUT  to anthropic ───────────────
MASK pierre.paul@…           "prompt": "écris à [EMAIL_1], matricule ZZ-4471"
```

**Two numbers, because they answer different questions.** The first is what was
replaced in this body, repeats included — what left the machine transformed. The
second is how many of those the session was seeing for the first time, which is
exactly the set listed as `MASK` lines below it. They are equal only in a session's
first exchange: after that a value keeps the replacement it was first given, so the
mapping is reused and nothing is minted.

The header used to carry the second number alone, under the first one's name. A
trace then read `replaced: 0 value(s)` above a body holding `[SECRET_12]`,
`[EMAIL_8]` and `[EMAIL_9]` — three values replaced there, none minted there,
and a header reporting the control had done nothing.

**The finding lives here now.** `matricule ZZ-4471` appears identically in both halves,
so nothing recognised it and it went to the provider in clear — which no count reports.
`diff` says it better than the colour marking did, and a file must carry no escape
sequences anyway: they make it unsearchable for the value itself.

Mechanics, each one a decision:

- **A trace is the only thing this agent writes to disk that holds a value in clear.**
  Directory `0700`, file `0600` — the same treatment the control key gets, because both
  are things only their owner may read.
- **The directory is relative to where the command was run.** An operator reads the
  files where they are working; a path under the home directory would have them hunting
  for output they asked for thirty seconds ago. `traces/` is in `.gitignore`, for a
  stronger reason than `e2e-artefacts/`: these hold somebody's real data, so committing
  one is a leak rather than noise.
- **It is created at start-up**, so somebody who cannot write where they asked is told
  before the traffic they wanted to look at has gone past.
- **A session comes from a header the caller controls**, so it is sanitised before it
  reaches a filename — otherwise `X-Session-Id: ../../etc` decides where a file goes.
- **Timestamp first, then a sequence number**: `ls` is chronological, which is how
  somebody looks for the exchange they just made, and a coding tool sends several
  requests in the same second — a timestamp alone would have them overwrite each other,
  losing exactly the exchange being looked for.
- **A JSON body is indented with `json.Indent`**, which inserts whitespace between
  tokens and touches nothing inside a string, so every byte of every value is still the
  byte that was sent. A decode and re-encode would rewrite escapes, reorder keys and
  drop duplicates. The sizes in the header are measured on the body as it arrived.
- **Bodies are written whole.** A ceiling would be the trace choosing which part of the
  traffic is worth keeping, and the part it cut is exactly where an unrecognised value
  would be.
- **The answer is appended when the body closes, as it arrived.** Before expansion, so
  it still carries the replacements and no restored value; the size lands on a `back:`
  line where it became known, since it cannot be in the header written at the top. The
  outbound half is on disk before the request leaves, which is why the answer is
  appended rather than the file rewritten at the end.
- **What the exchange cost sits on the same lines**: `mask:` (the detector's scan
  alone), `upstream:` (to the provider's headers), `delivering:` (to the end of its
  body), `unmask:` (the restoration, summed) and, when the answer named a model,
  `model:` and `tokens:`. Four durations because they answer four questions a total
  would hide, and **tokens rather than a price** — a table per model belongs to the
  backend, and a stale price in a file read as evidence is worse than none.
- **A streamed answer is also written back together**, one entry per content block in
  the order they opened, above the verbatim events and never instead of them. The
  grain a stream arrives in is unreadable; reassembling it decodes the JSON strings,
  so the readable view is exactly the one that no longer holds the bytes that were
  sent. Both, in that order.
- **A trace that cannot be written says so and the agent carries on**, the rule the
  telemetry already follows: the thing that records the control must never be able to
  take it down.

One limit remains: a system prompt is one JSON string with its newlines escaped, so
after indenting it is still one enormous line. Turning those `\n` into real newlines
would read far better and would stop the file holding the bytes that were sent, which
is the one property a trace cannot trade away.

## See also

- [Distribution](distribution.md) — installing as a service, the menu bar, `cloakfleet env`.
- [Request path](../architecture/request-path.md) — what the console is watching.
