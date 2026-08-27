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
cloakfleet audit         run it in the foreground on 33333, printing every value
                         it replaces and restores, in clear
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

## `cloakfleet audit` — the one surface that prints a real value

Everywhere else the rule holds without exception: the log carries counts and category names,
the heartbeat carries no content at all. Those are read by somebody **other than** the person
whose data it is — a dashboard, a support ticket, a log shipper — so a value reaching one of
them has left the machine as surely as if it had gone to the model.

The audit console is the opposite situation: **one operator, at their own keyboard, on their
own data**, asking "is my address actually being replaced" — which no count answers, and
which reading a masked body in one window and guessing at the other is how the two shape bugs
got in.

**It is a mode rather than a setting.** There is no environment variable that turns it on
under `cloakfleet proxy`; only the command that assembled the agent passes a writer
(`proxy.Options.Audit`), and it passes its own terminal (`runAudit`, `main.go:180`).
`Options` exists precisely so `audit` can differ from `proxy` in the two ways it has to — a
port of its own and a console to reveal on — **without a second assembly of the pipeline**,
which is the one-entrypoint rule, and **without a command reading an environment variable**,
which is the other one. It is the same pipeline through `FromEnv`: an audit of a second
assembly audits nothing.

**Port 33333** (`proxy.DefaultAuditListen`), so it sits beside the agent the shell profile and
the menu bar are already pointed at, instead of racing it for the socket and leaving the
operator watching an empty console while their traffic goes through the other one.

`printAuditInstructions` (`main.go:200`) prints the locales and substitution mode read from
the **assembled agent**, and warns explicitly when nothing will be masked because no locale is
loaded — an empty console would otherwise read as "nothing sensitive in my data" instead of
"nothing configured to look for it" (`TestAuditWarnsWhenNothingWouldBeMasked`). It hands over
the command to run in another terminal for each tool, **with its caveat**
(`TestAuditPrintsTheCommandToRunElsewhere`, `TestTheCaveatTravelsWithTheLine`).

### What the console prints, and why both bodies

**It prints both bodies, marked, because the MASK lines cannot report what the catalogue never
saw.** What is unmarked in both bodies is the finding — `matricule ZZ-4471` present in the body
that left means nothing recognised it, and no count can carry that.

Colour: **blue is a value in clear, red is a replacement**, in the bodies and in the
MASK/UNMASK lines alike — one colour, one meaning, or the eye has to re-learn the palette per
line. Colour is decided by asking the writer whether it is a terminal (`isTerminal`,
`audit.go:63`), not by reading `NO_COLOR`: that is the environment rule, and redirecting the
console to a file is already what somebody setting it would be doing
(`TestTheConsoleIsPlainWhenItIsNotATerminal`, `TestTheConsolePaintsATerminal`).

Mechanics that are each a fixed bug:

- **The inbound marking looks for the value rather than using the scan's offsets**, because
  the body is masked field by field through a JSON decoder and a match's position belongs to a
  decoded string, not to the raw document. A value carrying an escape is therefore printed
  unmarked, and its MASK line still names it.
- **It is one forward walk taking the longest match at each position** (`mark`,
  `audit.go:245`), not a replacement per value: painting a shorter value inside one already
  painted leaves an inner reset that ends the outer colour early, so the rest of the longer
  value came out unmarked (`TestTheLongerValueIsMarkedFirst`).
- **The outbound half is marked from the pass, not from the shape of a token.** In `fake` mode
  a replacement is a stand-in that reads as prose, and a console looking for brackets marked
  nothing at all in the half where it matters most, on a running agent
  (`TestAuditInFakeModeMarksWhatLeft`). Tokens are marked **as well**, for the turn after: a
  conversation resends its history, so the body leaving on turn two carries tokens minted on
  turn one that the current pass never saw (`TestTheOutboundBodyMarksAStandInAsWellAsAToken`).
- **The MASK/UNMASK line takes its two halves already painted by the caller that knows which
  is which** — sniffing "does it look like a token" coloured a stand-in as a value in clear,
  the exact opposite of what it is.
- **The outbound half is written as one block under one lock** (`auditor.request`,
  `audit.go:108`), reveals collected rather than streamed: two tools talking to the agent at
  once would otherwise interleave their bodies, and a body read half from one exchange and half
  from another is the reading an audit must never allow.
- **A JSON body is indented, with `json.Indent`, and anything else is printed as it
  arrived.** `json.Indent` inserts whitespace between tokens and touches nothing
  inside a string, so every byte of every value is still the byte that was sent —
  which is what lets the marking go on finding a value by its own text. A decode and
  re-encode would rewrite escapes, reorder keys and drop duplicates, three ways for
  the console to disagree with the wire. The size on the rule is measured on the body
  as it arrived, never on the indented form. The two halves line up field by field
  because the pipeline preserves key order — see
  [Request path](../architecture/request-path.md#a-body-is-a-json-document-masked-value-by-value);
  that defect was invisible while both bodies were one line, and indenting is what
  surfaced it. One limit remains: a system prompt is one JSON string with its newlines
  escaped, so it is still one enormous line, and unescaping it would stop the console
  showing the bytes that were sent.
- **Bodies are printed whole**, and a ceiling was tried and taken back out: a coding tool
  resends tens of kilobytes every turn so it scrolls, but the clipped part is exactly where an
  unrecognised value would be, and a console that chose which half of the traffic to show
  would answer the question the command exists for with "some of it"
  (`TestAuditPrintsALongBodyWhole`).
- **A value is named once as it is minted and once per response as it is restored**, not once
  per occurrence: a system prompt resent every turn would bury the exchange being watched
  (`Pass.Reveal`, `internal/detector/mask.go:56`).

Without a console, no value is written anywhere (`TestWithoutAConsoleNoValueIsWritten`); the
auditor's methods are nil-safe so the request path calls them without a branch.

## See also

- [Distribution](distribution.md) — installing as a service, the menu bar, `cloakfleet env`.
- [Request path](../architecture/request-path.md) — what the console is watching.
