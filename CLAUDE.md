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

## Architecture — the invariants

Each line below is a rule whose absence has already leaked. The demonstration —
the request that failed, the value that went out in clear — is in the wiki page
named at the head of each block. Read that page before changing anything in the
area: the reasoning is what stops a tempting simplification being reintroduced.

### The request path — `openwiki/architecture/request-path.md`

- **One entrypoint assembles the pipeline**: `cmd/cloakfleet`. Two entrypoints
  drifted until the same request was masked in one and answered in clear in the
  other. `cmd/cloakfleet-tray` is the one exception: it assembles nothing — no
  detector, no vault, no key, no configuration. A third `main` has to clear the
  same bar (assembles no pipeline, holds no secret, pays a cost the agent would
  otherwise carry).
- **Fail closed**: a body the agent cannot read is a 415, never a pass-through.
- **A body is a JSON document, masked value by value** — never as raw bytes. A
  pattern reading the bytes sees the characters an escape is made of, and the
  request comes out invalid; the same in reverse, an original carrying a quote
  cannot be spliced into raw JSON. See `internal/proxy/jsonbody.go`.
- **A decoded body is a `jsonObject`, not a `map[string]any`** — an ordered slice, so
  the document forwarded keeps the caller's key order and a duplicated key is not
  collapsed. Anything that reads a decoded body has to switch on that type: three
  consumers went on *compiling* while matching nothing, which would have had the
  streaming rehydrator restore no value at all. A `type switch` falling into no
  branch is silent.
- **Both shapes of a masked value are expanded** — bracket token and stand-in —
  and expansion is **one forward walk**, not a replacement per entry.
- **A credential never gets a stand-in** (`Detector.render`). That is what makes
  the value-matching expansion safe; removing the token fallback removes the
  guarantee with it. `TestFakeMode` asserts both halves.
- **Streaming holds back a tail**, and it is `detector.TailLen`, not
  `pii.TokenTailLen` alone — a stand-in splits across two events exactly as a
  token does.
- **Overlap arbitration, in order**: credential, then confidence, then the longer
  span, then the leftmost. Each rule is there because its absence leaked.
- **The agent holds no API keys.** The caller's credential is forwarded untouched.
- **The environment is read in one place per setting** — `detector.FromEnv`, or
  `internal/proxy/env.go`. **Never read an environment variable in `cmd/`**; a
  command that has to differ passes `proxy.Options`.
- **Two layers, one-way**: `pkg/pii` is the catalogue, `internal/detector` the
  engine. The catalogue knows nothing about the engine.
- **Routes the agent answers itself are reserved** (`reservedRoutes`), so a
  provider cannot take one.

### The operator surfaces — `openwiki/operations/configuration.md`

- **`/test` is a real tool, not a demo.** It renders one text in both
  substitution modes, using the deployment's own detector.
- **`cloakfleet proxy -a` prints every value replaced and restored; `-v` writes
  both bodies of every exchange to `traces/`.** Two independent flags on the one
  command: `-a` alone keeps nothing, `-v` alone records and prints nothing, and
  `newAuditor` builds an auditor for either — requiring a console to record a
  trace would have made the quiet half silently do nothing.
- **These replaced a separate `audit` command, and the guarantee it carried is
  gone.** As a command, printing in clear was a *mode* somebody entered, on a
  port of its own. As a flag it can go in a service definition — and the
  installer sends this agent's output to `~/.cloakfleet/agent.log`, so `-a`
  there keeps every prompt in clear for as long as the service runs. The banner
  says so on every start, because documentation is not where somebody reads it.
- **The console carries no body**, and that is why nothing marks one any more:
  on screen the bodies scroll the MASK lines away, and a file must carry no
  escape sequences — they make it unsearchable for the value itself. The
  body-marking apparatus went with them rather than being left as a painter
  nothing calls.
- **A trace is the only thing this agent writes to disk holding a value in
  clear.** Directory 0700, file 0600, the same treatment the control key gets. A
  session comes from a header the caller controls, so it is sanitised before it
  reaches a filename. One file per exchange, timestamp-first so `ls` is
  chronological, with a sequence number because several requests share a second.
  Bodies are written whole — a ceiling would be the trace choosing which part of
  the traffic is worth keeping, and the part it cut is where an unrecognised
  value would be.
- **The finding still lives, one step further away**: the two bodies in the file,
  read against each other. A value present in both is one the catalogue never
  recognised, and `diff` says it better than any highlighting did.
- **Everywhere else: counts and category names, never content.** The heartbeat
  carries no content at all.
- **Three levels, not two, and each surface follows the right one.**
  `Status.Masking` is deliberately not `Answering` — an agent with no locale
  selected is healthy and recognises almost nothing. `Status.Level` is deliberately
  not `Masking`: an agent with a category switched off *is* masking, and reporting
  that alone is the green light over the values that are not being replaced. The
  exit code of `cloakfleet status` and the menu bar icon both follow **Level**, so
  zero means "everything this configuration loaded is being replaced".
- **`cloakfleet mask` and the menu bar are the two surfaces, and both go through
  `proxy.SetPolicy`** — the one writer, as `proxy.Query` is the one asker. The command
  exists because the menu bar is Cocoa and a Linux workstation had the route and no
  way to reach it. It accepts a family name as well as a category code, and lists only
  what the detector can actually emit (`Detector.Categories`): a switch for a category
  no loaded locale can find would say the agent is masking it.
- **Three things change while the agent runs: the switched-off categories, the
  substitution mode and the loaded locales.** All three live behind one atomic
  pointer in `detector.policy`, and the locales carry `patterns` and `fakes` with
  them in one `catalogue` value — swapped whole, because a scan reading new patterns
  against the old stand-in table would render a French address with an American
  postcode. One atomic load per scan, not a lock: the alternative is a read lock on
  the hottest loop in the agent to serve a click a day.
- **`PUT /policy` replaces the whole state, and refuses a partial request.** Not
  "absent means unchanged": an empty locale list is a *valid* state — the one an
  agent starts in — so absence cannot mean "leave them alone" without making "load
  none" unsayable. And a caller sending only `off` would silently wipe the locale
  selection, which is the request that turns an agent into one masking almost
  nothing while reporting success. It is **not a transaction**: each part refuses on
  its own and earlier parts stay applied, which is why every surface redraws from
  the reply rather than from its own request.
- **A locale selection is stored in registry order whatever order it arrived in.**
  Load order settles which country claims a value both could read, so a selection
  that reordered them would quietly change what nine bare digits become.
- **An unknown locale code is refused, not skipped.** `pii.LocalePatterns` skips one
  by design — it must not decide policy about a selection — so nothing below would
  notice, and an operator who mistyped "uk" would be told the change succeeded.
- **Changing the mode clears the session mappings, and it is the only setting that
  does.** The mapping is consulted *before* the mode is — a value already seen keeps
  the shape it was first given — so leaving it in place made a click on "fake" change
  nothing anybody could observe: nothing sends a session header, so one unnamed
  session carries every value the agent has handled since it started. The trade is
  paid knowingly: replacements minted before the change stop being restored, and an
  answer still in flight comes back carrying one nothing expands, one exchange wide.
  The purge is guarded on the mode having *actually* changed, because every surface
  resends the whole state on every click — purging on each would discard the mapping
  when somebody merely switched off a category. A credential is tokenized in either
  mode. `TestPolicyChangingTheModeClearsWhatWasAlreadyMinted` and
  `TestPolicyResendingTheSameModeKeepsTheMapping` are the two halves.
- **A category can be switched off, and `PUT /policy` is the only way.** It is the
  one route that changes what the agent does and the only authenticated one: a
  secret in `~/.cloakfleet/control.key` (0600), in a custom header, which is what a
  browser cannot set cross-origin. Left open, any local process — or a page
  somebody visits — could disable the control silently. The whole set is replaced,
  never toggled: two surfaces on one agent interleave the halves of a
  read-modify-write. **A credential is refused by the detector**, not by the menu,
  so nothing can route around it; `pii.Switchable` is that rule.

### Supervision — `openwiki/architecture/supervision.md`

- **`pkg/telemetry` is public and the backend imports it — never the reverse.**
  The agent must compile, run and be useful with no backend in existence.
- **Nothing in a heartbeat is content.** `TestHeartbeatCarriesNoContent` walks the
  type; a new string field fails until it is on the allow list **with a reason**.
  Do not add one to make a dashboard nicer.
- **`State.Masking` and `State.SwitchedOff` are read at every heartbeat**, not
  cached at start-up like the rest of `State`: they change while the process runs,
  and cached, a fleet view would show every agent applying its whole catalogue
  whatever anybody switched off.
- **`State.Addresses` is the one field that is personal data**, and its entry says
  why. Local addresses only, loopback and link-local dropped, stably ordered,
  capped.
- **Adding a field means updating `testdata/heartbeats.json` in the same commit**,
  and the example must *exercise* it.
- **Nothing in `internal/telemetry` may reach the request path.** No backend means
  no reporter at all, not a reporter that quietly does nothing.
- **Two cadences in `Run`, and they must stay apart**: a ticker closes a bucket
  every interval whatever the backend is doing; a separate timer sends, on the
  retry ladder. Fused, the window boundaries moved with the backend's health.
- **Buckets are queued on disk**, written by rename, in two files — the queue, and
  the bucket in progress under `livePathFor`. Closing a bucket **clears the live
  entry**: left behind, the next process files both and every number doubles.
- **Two bounds** (seven days of age, measured from where a bucket *ends*; 2016
  buckets), and **whichever bites, the loss is counted** — into the bucket being
  closed right then, and onto disk with them.
- **The backlog goes 60 buckets per request, oldest first, retried a second apart
  while any remains**, on a ladder capped at the interval and reset by one
  success.
- **`snapshotInterval` (30s) bounds what a hard kill loses**, its window ending at
  the snapshot rather than at the kill.
- **`NewRecorder` counts the restart**, because a process builds exactly one.
- **The OpenAI cached-token breakdown is deliberately not read** — it is a subset
  of the input, and reading both would bill the same tokens twice.

### Distribution — `openwiki/operations/distribution.md`

- **One type answers `/healthz`** (`proxy.Health`) and **one function asks it**
  (`proxy.Query`). Two ways to ask are two ways to answer differently.
- **The menu bar is a separate process on purpose.** An icon inside the proxy
  vanishes at the moment it becomes useful. Its launchd agent has no `KeepAlive`
  — the menu offers "Quit the icon" — while the agent's keeps it.
- **Everything in `internal/tray` that decides what to show is separate from the
  toolkit.** `render` and `watch` touch no part of fyne.io/systray and are tested.
- **`shellTools` is the one owner of how a tool is pointed at this agent** — the
  variable, the CLI and the caveat, together. Only verified pairs go in: a guessed
  command name fails after somebody has pasted it and believed it. **The caveat
  travels to every place the line is handed over.**
- **The icons are generated and committed**
  (`go run ./internal/tray/icons/generate.go`).
- **The installer never exports a base URL into a shell profile.** It adds
  `eval "$(cloakfleet env)"`, which prints nothing when the agent is stopped —
  availability over enforcement, on purpose. It touches no login file unless asked
  (`--shell`), and `--uninstall` leaves `~/.cloakfleet/` alone.

## Conventions

- Conventional Commits (`feat:`, `fix:`, `docs:`, `test:`, `refactor:`).
- **Everything written down is in English** — comments, commit messages, and every
  documentation file wherever it lives (`openwiki/`, `docs/`, a README, a note
  beside the code). A conversation may happen in another language; what lands in
  the tree does not. Two languages in one tree means a reader who can follow half
  of it, and a page nobody updates because it is not in the language the commit
  was thought in.
- **British spelling** in comments and prose — the linter is configured for it.
- Comments say *why*, and name the failure a rule prevents. A comment that
  restates the code is noise.
- Mark a deliberate simplification with a `TODO:` naming the known ceiling and
  the upgrade path.

### Adding a category, a locale, a provider, a contract field

The full procedure, and which test fails on each forgotten step:
`openwiki/workflows/extending-the-catalogue.md`. In short:

- **A PII category** — one entry in `categoryRegistry` carrying the prefix, score,
  checksum, credential flag, **group and label**; its pattern in the right set; a
  line in `pkg/pii/sample.go`; a corpus case (**including one that must come out
  untouched**); then `make score-update`. `validateCatalogue` panics at package
  initialisation on an unregistered category, a missing group or label, or a label
  another category already uses — two identical rows in the menu that switches them
  is a person unticking one with no idea which they got.
- **A locale** — one entry in `localeRegistry`, `patterns_<code>.go`, a corpus
  suite, a block in `.env.example`, `make score-update`. Three tests fail on the
  commit that forgets any of those. `Priority` is load order, and load order
  decides which country claims a value both could read.
- **The sample is a reference and must stay one.** Every category its set detects
  **and every notation each pattern accepts**, updated in the same commit as the
  catalogue. Its tables are deliberately not derived from the detector: a derived
  expectation agrees with whatever the detector does, including a form it silently
  stopped reading.
- **A setting** — one constant in the package that owns it, plus `.env.example`.
  `TestDocumentedEnvironmentMatchesTheCode` fails in both directions.
- **Recall alone cannot fail a pattern**: one widened to match everything scores
  100% recall. The `negatives` floor in `score-baseline.json` is the other half of
  the gate — see `openwiki/workflows/testing-and-accuracy.md`.

### Patterns

The catalogue, the locales and the substitution modes in full:
`openwiki/architecture/detection-engine.md`.

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

## OpenWiki

This repository has documentation located in the /openwiki directory.

Start here:
- [OpenWiki quickstart](openwiki/quickstart.md)

OpenWiki includes repository overview, architecture notes, workflows, domain concepts, operations, integrations, testing guidance, and source maps.

When working in this repository, read the OpenWiki quickstart first, then follow its links to the relevant architecture, workflow, domain, operation, and testing notes.
