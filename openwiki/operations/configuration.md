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
and any buckets not yet delivered. `install.sh --uninstall` deliberately leaves it alone.

## Commands (`cmd/cloakfleet`)

```
cloakfleet proxy         run the agent: mask what goes out, restore what comes back
cloakfleet proxy -a      also print every value it replaces and restores, in clear
cloakfleet proxy -v      also write both bodies of every exchange to ./traces
cloakfleet scan [file]   report the sensitive values in a file, or in stdin
cloakfleet status        report whether the agent is masking, and what
cloakfleet mask          list what is masked, and switch a category or family off
cloakfleet env [--force] print the shell exports that point a tool at the agent
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

### `scan` — the offline check

Reads a file or stdin, runs `detector.FromEnv`, and prints every value that would be masked
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
cloakfleet proxy -v    write both bodies of every exchange to ./traces, one file per exchange
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
replaced: 2 value(s)
                           ── OUT  to anthropic ───────────────
MASK pierre.paul@…           "prompt": "écris à [EMAIL_1], matricule ZZ-4471"
```

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
