# CLAUDE.md

Guidance for Claude Code working in this repository.

Neverseen is an agent installed on each workstation. It proxies the AI tools
people use to a model provider, masking personal data and credentials on the way
out and restoring them on the way back. Module path:
`github.com/neverseen-ai/neverseen-agent`. Licence: FSL-1.1-ALv2.

**How to describe the licence**: "source available today, open source on a
two-year delay". Never write that the current release *is* open source, and never
write the Apache 2.0 grant as a promise or a plan — it is *additional*,
*irrevocable*, and effective on the **second anniversary of each release**. The
full wording, and why each word is load-bearing: `README.md`, `LICENSE.md`.

The paid supervision backend is a **separate, private repository**
(`neverseen-cloud`, checked out as `../neverseen-cloud`). It imports this one;
this one must never import it, and must compile and run with no backend at all.
Read it there when a change touches `pkg/telemetry`; never add it as a dependency.

## Commands

```bash
make build            # bin/neverseen
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

Each line below is a rule whose absence has already leaked. **The demonstration —
the request that failed, the value that went out in clear — is in the wiki page
named at the head of each block. Read that page before changing anything in the
area**: the reasoning is what stops a tempting simplification being reintroduced,
and it is deliberately not repeated here.

### The request path — `openwiki/architecture/request-path.md`

- **One entrypoint assembles the pipeline**: `cmd/neverseen`. `cmd/neverseen-tray`
  is the one exception — it assembles nothing and holds no secret. A third `main`
  clears the same bar or it does not ship.
- **Fail closed**: a body the agent cannot read is a 415, never a pass-through.
- **A body is a JSON document, masked value by value**, never as raw bytes
  (`internal/proxy/jsonbody.go`).
- **A decoded body is a `jsonObject`, not a `map[string]any`** — ordered, and a
  duplicated key is not collapsed. Anything reading a decoded body must switch on
  that type: a `type switch` falling into no branch is silent.
- **The outbound body repeats the inbound one's prefix, and the agent must not be
  what breaks it.** Providers bill less and answer faster when a request's prefix
  matches the previous one's, and a conversation replays its whole history every
  turn — so any reordering, re-indentation or reshaping the agent introduces
  *anywhere* in the body forfeits the cached prefix from that point on, for every
  turn that follows. `jsonObject` being ordered is half of what holds this; masking
  a value in place is the other, and the session mapping is the third — the same
  value must get the same stand-in next turn or the prefix breaks where it appears.
  **Measure it on the reply's `usage`, never by comparing two traces**:
  `cache_read_input_tokens` should equal the previous request's `cache_read` plus
  its `cache_creation`, leaving `input_tokens` at the turn's delta. A trace is
  re-indented before it is written (`json.Indent`, `trace.go`) and the client itself
  collapses the block that carried `cache_control` last turn, so a byte comparison
  of two traces reports a break where the provider sees none.
- **Both shapes of a masked value are expanded** — bracket token and stand-in —
  in one forward walk, not a replacement per entry.
- **A credential never gets a stand-in** (`Detector.render`); that is what makes
  value-matching expansion safe. `TestFakeMode` asserts both halves.
- **Streaming holds back a tail** of `detector.TailLen`, not `pii.TokenTailLen`
  alone — a stand-in splits across two events exactly as a token does.
- **A tail belongs to the block it was held back from**, and `closeBlock` releases
  it there.
- **`deltaText` enumerates every shape a provider streams text in** — including
  `delta.thinking` and `delta.partial_json`, which carry tool calls.
- **A tool call's arguments are accumulated and expanded whole** (`jsonFragment`,
  `expandedArguments`), released before the stop that completes them and exactly
  once. Fragments that do not make a document fall back to whole-token expansion.
- **An SSE event is a name line and a data line, and holding one means holding
  both** — `rewrite` withholds the name (`takeName`), the synthesised events carry
  their original's name (`argumentsName`, `templateName`), and a held event's blank
  separator goes with it (`held`). A test stream with no names in it cannot exhibit
  the failure, which is why `namedEvents` exists beside `events`.
- **Overlap arbitration, in order**: credential, confidence, longer span, leftmost.
- **The agent holds no API keys.** The caller's credential is forwarded untouched.
- **What `-a` and `-v` reveal never changes what the agent does.** Every textual
  answer is read for its token counts and its tool calls; only a session that
  minted something has its answer rewritten (`streamRehydrator.observe`, and the
  `len(known) > 0` guard in `unmask`).
  `TestAnAnswerToAnEmptySessionIsForwardedVerbatim` and
  `TestAuditPrintsAToolCallWhenNothingWasMasked` are the two halves.
- **The identifiers a client uses to name itself to Anthropic reach Anthropic in
  clear** (`identifiers.go`). The anchor is the **path** `metadata.user_id` in the
  decoded document — never a field name over raw bytes — the **shape** is the
  second guard, and it applies **by value**, on **Anthropic alone**, where Anthropic
  is the host the route resolves to (`identifierHost`) and not the route's code.
  `Pass.Exempt` is per request because what goes in it is read out of the body being
  masked; `Detector.allowed` is the deployment-wide half of the same idea.
- **The environment is read in one place per setting** — `detector.FromEnv`, or
  `internal/proxy/env.go`. **Never read an environment variable in `cmd/`**; a
  command that has to differ passes `proxy.Options`.
- **Two layers, one-way**: `pkg/pii` is the catalogue, `internal/detector` the
  engine. The catalogue knows nothing about the engine.
- **Routes the agent answers itself are reserved** (`reservedRoutes`), so a
  provider cannot take one.

### The operator surfaces — `openwiki/operations/configuration.md`

- **`/test` is a real tool, not a demo.** It renders one text in both substitution
  modes, using the deployment's own detector.
- **`/settings` is where the configuration happens, and the menu bar only says what is
  happening.** The page holds no engine: it draws from `/healthz` and writes through
  `PUT /policy`, like every other surface. It exists because the toolkit was the wrong
  shape — no radio group, no mixed tick, no room for the sentence that says what a
  choice costs, so every one of those absences had been answered by writing the sentence
  into a menu entry's own title.
- **Each country is served with what it can find** (`Health.LocaleCategories`, from
  `pii.CategoriesInLocale`) — **labels for a reader, never codes to send back**, and
  **names rather than a count**: the lists overlap and a few categories need no country,
  so `fr`, `gb` and `us` name 8, 6 and 7 against 16 in play, and a count is a number a
  reader adds up and is wrong. `TestEachCountryIsServedWithWhatItFinds` fails if the sum
  ever equals what is in play.
- **Every closed set a surface draws is served, never spelled out by the surface** —
  `Groups`, `AvailableLocales`, `HealthGroup.Credentials`, and `Health.Substitutions` /
  `Health.SecretLevels`. A page with a list of its own goes on offering a name a
  rebuilt agent refuses — 422 on a name the page suggested — and stays silent about one
  it gained. `TestTheOfferedNamesAreOnesTheAgentTakes` asks it of the **payload**: asked
  of `SubstitutionModes()` it passed while the page drew literals of its own. The
  *sentence* describing a choice stays with the surface; it is prose, not a fact about
  what exists.
- **The settings page files a family under one of two headings, and the agent says
  which** (`HealthGroup.Credentials`, from `pii.IsCredentialGroup`). **Not `Locked()`**:
  a family can be locked without being credentials — `GroupDeclared` is personal data
  somebody authored on purpose — and a surface splitting on "can I switch it" would file
  it among the API keys. **The credentials heading carries no switch and cannot**:
  `pii.Switchable` refuses them at the detector, and a request naming one is refused
  whole. **They are listed all the same** — a name is not a control, and "is my vendor's
  key covered" has no other answer in this agent; the menu bar's one-line-with-a-count
  rule was right for a menu and is wrong for a page.
- **A locked category is shown by its notation, not its label**
  (`HealthCategory.Notations`, from `Detector.Notations()` — the loaded patterns, never
  the catalogue, because a postcode has one notation per country). The label says what a
  category *is*, a notation what it *looks like*. `CONN_STR` is the case that forced it:
  one entry whose pattern takes **any** URL scheme, so it names the shape
  (`scheme://user:password@…`) and never three example schemes — a list of examples
  reads as a closed list and is wrong in the direction that matters. `TestOnlyTheCredentialFamiliesAreCredentials` pins the split family by family,
  and fails on a family it has never been told about.
- **What decides is kept out of what draws, in the page too**
  (`settings_decisions.js`, inlined; `settings_decisions.test.mjs` under `node --test`,
  in `make test` and in CI). It is `internal/tray`'s split one storey up and it is here
  for the same reason: a browser cannot be asserted on in CI any more than a menu bar
  can. `plan.go` held these rules for the menu, and deleting it without this would have
  moved a hundred tested lines into JavaScript nothing runs.
- **`/settings` is the one response body carrying the control key**, so it is closed
  twice **before the key is read**: loopback only, hard, whatever `-l` bound; and the
  `Host` must name this machine (`namesThisMachine`), which is the only defence against
  DNS rebinding available to an agent that implements no CORS. Every refusal is asserted
  to carry **no key**, not merely to return 403 — a guard running after the template did
  would answer 403 with the secret in the body. No key on the agent refuses the page
  rather than serving one whose every control fails silently. The `Host` check reads the
  port as well as the name — `SplitHostPort` does not check that a port is one, and
  `localhost:9787@rebound.example.com` split to the name this guard wanted to see. The
  page is served under `default-src 'none' … connect-src 'self'`, whose clauses the test
  asserts **by name**: a policy that lost one would still look like a policy.
- **`-a` prints every value replaced and restored; `-v` writes every exchange to
  `traces/`.** Two independent flags, and `newAuditor` builds an auditor for either.
  Under the installer's service `-a` files every prompt and every tool call in
  `~/.neverseen/agent.log` — the banner says so on every start.
- **`-l` binds beyond loopback and the agent warns rather than refuses.** The guard
  is `proxy.BeyondLoopback`, asked where the agent starts listening and **not on the
  flag** — the environment variable exposes exactly as much. `:9787` counts as
  reachable. This is what `/healthz` and `/test` being unauthenticated costs.
  **`/settings` is deliberately not in that list** and refuses a caller off loopback
  whatever was bound: the trade that lets a page describe the configuration to the
  network does not transfer to one that hands over the ability to switch masking off.
- **A tool call is printed with its arguments in clear** (`auditor.tool`), from
  `expandedArguments` on the streaming path and `reportToolCalls` on the buffered
  one — both halves or neither. Anthropic's shape only. A `TODO:` names the missing
  ceiling.
- **The console carries no other body.** The body-marking apparatus went with them
  rather than being left as a painter nothing calls.
- **A trace is the only thing this agent writes to disk holding a value in clear.**
  Directory 0700, file 0600. The session is sanitised before it reaches a filename.
  One file per exchange, timestamp-first, with a sequence number. Bodies whole.
- **The answer is recorded as it arrived, before a single replacement was expanded**,
  and appended when the body closes — a caller that hangs up files what had arrived.
- **What the exchange cost is four durations, not one total, and tokens rather than
  a price**: masking (the detector alone), `upstream`, `delivering` (dated at the end
  of the body), `unmask` (summed per event on a stream). The line is omitted rather
  than zeroed when the answer named no model.
- **A stream is also written back together, above the events and never instead of
  them** (`reassemble.go`) — one entry per content block, labelled by the start
  event. Derived, so the verbatim half must stay below it. Omitted for a buffered
  answer.
- **Everywhere else: counts and category names, never content.**
- **Three levels, not two.** `Status.Masking` is deliberately not `Answering`;
  `Status.Level` is deliberately not `Masking`. The exit code of `neverseen status`
  follows **Level**.
- **Four icons, because the menu bar has a question the exit code does not.** The icon
  follows Level and then `Answering` over it (`internal/tray/tray.go`): a stopped agent
  reaches `render` as `LevelNone`, wearing the picture of one that is running and
  replacing nothing. Both leave the traffic in clear — which is why they were one
  picture — but one is fixed from the settings page and the other is not, and the icon
  is what somebody looks at before going there. `absentIcon` is `unmaskedIcon` without
  the divider, because the divider is the agent standing between a value and where it
  was going. **Not a strike through the mark**: that was tried and is eight pixels of
  diagonal at the size this is actually seen (`internal/tray/icons/generate.go`).
- **A level counts the effect, `Disabled` records the intent**, and the level counts
  only what the loaded patterns can emit (`disabledInPlay`).
  `TestTheLevelCountsOnlyWhatTheDetectorCanEmit`.
- **`neverseen mask`, the settings page and the menu bar all go through
  `proxy.SetPolicy`/`PUT /policy`** — the one writer, as `proxy.Query` is the one asker.
  Each lists only what the detector can emit (`Detector.Categories`).
- **The menu bar writes once — "Mask everything again" — and it carries the whole
  state.** The route replaces rather than patches, so a request carrying nothing but the
  empty set would switch off, from an entry that says "mask everything again", the mode,
  the locales and the secret level somebody had just chosen on the page. The three
  settings the menu stopped drawing are still compared by `same`, for that reason alone.
- **Four things change while the agent runs**: switched-off categories, substitution
  mode, secret level, loaded locales. All behind one atomic pointer in
  `detector.policy`, the locales carrying `patterns` and `fakes` in one `catalogue`
  value, swapped whole. One atomic load per scan, not a lock.
- **The secret level grades one pattern and only one** — `SECRET_GENERIC`, via
  `pii.SecretStrength` and `Detector.strongEnough`.
  `TestPolicySecretLevelGradesOnlyTheCatchAll`. **Classes, not entropy**; **a
  separator is not a character class**; **the default level is weak, and it has to
  be.**
- **Load order cannot decide a secret against a PII category**: `pickFromCluster`
  ranks on `IsSecret` first.
- **`PUT /policy` replaces the whole state and refuses a partial request — in
  `applyPolicy`**, so the stored file is refused the same way. It is **not a
  transaction**: every surface redraws from the reply, never from its own request.
- **A locale selection is stored in registry order** whatever order it arrived in.
- **An unknown locale code is refused, not skipped.**
- **Changing the mode clears the session mappings, and it is the only setting that
  does** — guarded on the mode having *actually* changed.
  `TestPolicyChangingTheModeClearsWhatWasAlreadyMinted` and
  `TestPolicyResendingTheSameModeKeepsTheMapping`.
- **`scan` and the agent assemble the detector through one function**
  (`proxy.DetectorFromEnv`: the environment, then the stored policy).
- **What a surface changed survives the restart, and the file is the state**
  (`~/.neverseen/policy.json`). One applier (`applyPolicy`) serves the route and the
  start-up read; `off` is stored as the **intent**; **it wins over the environment**;
  an unreadable file leaves the environment alone; nothing is written until something
  actually changes, refusals included — `applied` means the value *differed*. A
  surface resends `Health.Off`, never the switches it draws (`SwitchedOffCodes`).
  Serialised by `policyMu`, written through a temporary name.
- **A category can be switched off, and `PUT /policy` is the only way** — the one
  route that changes what the agent does and the only authenticated one
  (`~/.neverseen/control.key`, 0600, custom header). The whole set is replaced, never
  toggled. **A credential is refused by the detector**, not by the menu
  (`pii.Switchable`).

### The browser extension — `openwiki/architecture/browser-extension.md`

- **The extension holds no engine** — no catalogue, no policy, no mappings, no
  detector. It calls `POST /mask` and `POST /unmask`.
- **It lives in this repository because the API is a contract.**
  `extension/testdata/contract.json` is replayed by both sides. Regenerate with
  `make contract-update`, never casually.
- **Fail closed on the way out, open on the way back**, and every block raises a
  banner naming the command that fixes it.
- **`/mask` takes a list, and that is not batching** — one pass is what gives a
  value repeated across two fields one identity.
- **`/unmask` is stateless: the tail travels with the client**, held back with
  `detector.TailLen`. `final: true` is the protocol's explicit end.
- **One shared auth helper** (`Server.authorised`), constant-time. **`/unmask` is
  loopback only, hard**, whatever `-l` bound, checked before the key is read.
  **No CORS headers on the agent, ever.**
- **Neither route is captured by `-a` or `-v`**, structurally.
- **The key never leaves the service worker**, and never a bare HTTP route.
- **The page's world names no session and cannot be made trustworthy.** `PageAsk`
  carries no session; `bridge.acceptAsk` rebuilds every field by field, never
  spreading and never casting. `namesAWebSession` is the worker's second copy of the
  rule, and the name it must always refuse is `default`. A banner note carries a
  situation from a closed set, never a sentence.
- **A stream event is rewritten structurally, never as raw bytes**, and the calls are
  serialised — two in flight is corruption.
- **A transport that cannot be masked is refused**, not forwarded: `XMLHttpRequest.send`,
  `sendBeacon`, `WebSocket`.
- **`extension/e2e/run.test.mjs` is the only test with nothing stubbed**, and it
  asserts both halves — what the site received and what the page rendered.

### Supervision — `openwiki/architecture/supervision.md`

- **`pkg/telemetry` is public and the backend imports it — never the reverse.**
- **Nothing in a heartbeat is content.** `TestHeartbeatCarriesNoContent` walks the
  type; a new string field fails until it is on the allow list **with a reason**.
- **`State.Masking` and `State.SwitchedOff` are read at every heartbeat**, not cached
  at start-up like the rest of `State`.
- **`State.Addresses` and `State.Hostname` are the two fields that are personal
  data**, and their entries say why. Local addresses only, loopback and link-local
  dropped, stably ordered, capped. The hostname as the OS gives it — never resolved,
  never hashed.
- **Adding a field means updating `testdata/heartbeats.json` in the same commit**,
  and the example must *exercise* it.
- **Nothing in `internal/telemetry` may reach the request path.** No backend means no
  reporter at all, not a reporter that quietly does nothing.
- **Two cadences in `Run`, and they must stay apart**: a ticker closes a bucket every
  interval whatever the backend is doing; a separate timer sends, on the retry ladder.
- **Buckets are queued on disk**, written by rename, in two files — the queue and the
  bucket in progress (`livePathFor`). Closing a bucket **clears the live entry**.
- **Two bounds** (seven days of age, measured from where a bucket *ends*; 2016
  buckets), and **whichever bites, the loss is counted**.
- **A test whose buckets are fixed calendar dates pins the reporter's clock too**
  (`Config.Now`). `TestRunDrainsABacklogWithoutWaitingAnInterval` also asserts
  `dropped == 0`, because the count alone cannot tell a bucket pruned from a bucket
  never sent.
- **The backlog goes 60 buckets per request, oldest first, retried a second apart**,
  on a ladder capped at the interval and reset by one success.
- **`snapshotInterval` (30s) bounds what a hard kill loses.**
- **`NewRecorder` counts the restart**, because a process builds exactly one.
- **The OpenAI cached-token breakdown is deliberately not read.**
- **A map keyed on something the agent observes is keyed on a closed vocabulary, and
  the rest is `Other`** (`pkg/telemetry/vocabulary.go`). The recorder re-checks every
  key (`inVocabulary`). Widening a list is a contract change.
- **The agent computes no average** — power-of-two `Histogram`s, and the backend reads
  the median. The same rule as prices.
- **A session is the conversation the mapping is scoped by** (`conversationOf`).
  `SessionIdle` equals `vault.DefaultTTL` and a test holds them together. The identity
  never leaves the recorder.
- **`Tools.Restored` is read off the expansion, not off the mapping**, and on the
  buffered path `reportToolCalls` runs *before* the whole-document pass.
- **`State` says how the agent itself is exposed** — `Console`, `Tracing`, `Exposed`,
  `Rerouted`, `Allowlisted`.
- **`neverseen replay` is read-only**, and the mapping it decides `Restored` against
  is the trace's own (`recoverMapping`).

### Distribution — `openwiki/operations/distribution.md`

- **One type answers `/healthz`** (`proxy.Health`) and **one function asks it**
  (`proxy.Query`).
- **One owner of the service definition** (`internal/service`); `install.sh` is a
  caller. `neverseen service install|uninstall|restart` is the whole surface —
  **three verbs, because each writes or removes a definition**. `--status` and
  `--logs` stay in the script, because they observe and author nothing. **`Render`
  takes the platform as a field, not `runtime.GOOS`**, so every definition is
  asserted on every platform; only `Apply`, `Uninstall` and `Restart` are
  build-tagged. The existing definitions are reproduced **byte for byte**.
- **The agent is restarted and the icon is not, on every platform.**
  `TestOnlyTheAgentIsRestarted` holds both halves rather than leaving them to the
  golden files, which record what the code does where the test records what it must.
- **The menu bar is a separate process on purpose.** An icon inside the proxy
  vanishes at the moment it becomes useful.
- **A background job and a desktop job go to two registries on Linux**: the agent is
  a systemd user unit, the icon an XDG desktop entry under `~/.config/autostart`. A
  unit for the icon would need `graphical-session.target`, which not every desktop
  reaches, and would start without the session's environment — a job that never runs
  and says nothing. The entry redirects to `agent.log` because it has no
  `StandardErrorPath`.
- **The icon refuses to draw into an empty bus, and says which extension is
  missing** (`internal/tray/sni.go`). Linux has no notification area as a platform
  feature; whether a StatusNotifierItem is drawn is the desktop's decision, and stock
  GNOME needs an extension. It asks **only** whether anything owns
  `org.kde.StatusNotifierWatcher` — reading `IsStatusNotifierHostRegistered` beside it
  would refuse the desktops whose hosts never set it. **Every refusal says the agent
  is still masking**: an icon disappearing is what it looks like when masking stops.
  `TestEveryRefusalSaysTheAgentIsUnaffected`.
- **Everything in `internal/tray` that decides what to show is separate from the
  toolkit.** `render` and `watch` touch no part of fyne.io/systray and are tested,
  and `hostAdvice` is the same split for the session bus: `sni_linux.go` asks, `sni.go`
  decides, and only the second has a test.
- **`shellTools` is the one owner of how a tool is pointed at this agent** — the
  variable, the CLI and the caveat together. Only verified pairs go in, and **the
  caveat travels to every place the line is handed over.**
- **The icons are generated and committed**
  (`go run ./internal/tray/icons/generate.go`).
- **The installer never exports a base URL into a shell profile.** It adds
  `eval "$(neverseen env)"` — availability over enforcement, on purpose. It touches
  no login file unless asked (`--shell`), and `--uninstall` leaves `~/.neverseen/`
  alone.

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
`openwiki/workflows/extending-the-catalogue.md`. The rules that survive without it:

- **A PII category** needs its `categoryRegistry` entry (prefix, score, checksum,
  credential flag, **group and label**), its pattern, a line in `pkg/pii/sample.go`,
  a corpus case **including one that must come out untouched**, then
  `make score-update`. `validateCatalogue` panics at package initialisation on an
  unregistered category, a missing group or label, or a duplicate label.
- **A locale** needs a `localeRegistry` entry, `patterns_<code>.go`, a corpus suite,
  a block in `.env.example`, `make score-update`. `Priority` is load order, and load
  order decides which country claims a value both could read.
- **The sample is a reference and must stay one** — every category its set detects
  **and every notation each pattern accepts**, updated in the same commit. Its tables
  are deliberately not derived from the detector.
- **A setting** is one constant in the package that owns it, plus `.env.example`.
  `TestDocumentedEnvironmentMatchesTheCode` fails in both directions.
- **Recall alone cannot fail a pattern**: the `negatives` floor in
  `score-baseline.json` is the other half of the gate
  (`openwiki/workflows/testing-and-accuracy.md`).

### Patterns

**The catalogue, the locales, the substitution modes, and the reasoning behind every
individual pattern are in `openwiki/architecture/detection-engine.md`. Read it before
touching `pkg/pii` or `internal/detector` — every rule there was paid for by a leak
or a false positive, and none of it is reproduced here.**

What follows is only what you would otherwise get wrong on the first attempt, before
you had a reason to open that page:

- **RE2 has no lookbehind and no backreference.** A pattern that must reject a
  preceding character consumes it and points `Group` at the value. Separators that
  have to agree need one alternative per separator, not a character class.
- **Horizontal whitespace only** (`[ \t]`, never `\s`) in any span that could run
  long — with `\s` a span swallows the next line.
- **Go's `\b` is ASCII.** It finds a boundary inside an accented run. Use a leading
  character class and `Group` instead.
- **Beware `(?i)` over a long repetition**, and over a prefix: vendors issue their
  prefixes in one case only.
- **A prefix pattern is free only while Go can scan for its leading literal**, and a
  leading `\b`, a `(?i)`, or an alternation of prefixes each destroys that —
  measured at 62ms against 0.56ms over `docs/testCorpus.txt`. **Two patterns of one
  category is the shape to reach for**, and `Label` is what makes a report say which
  fired. Every alternation of prefixes in the second tier is split.
- **A trailing group that consumes a character makes the span eat the sentence.**
  Close on a permissive body one shorter, then a final class excluding
  `noSentenceTail`.
- **A checksum on its own is not evidence of a category — the length is the other
  half.** Luhn clears ~10% of arbitrary runs, NHS mod-11 ~9%, ABA ~4%, the NIR key
  ~0.9%. Wherever the real identifier fixes a length, the shape must fix it too
  (`ibanLengths`, and one length per card brand).
  `TestCreditCardShapeRejectsLengthsNoVisaHas` uses values that all pass Luhn, so it
  tests the shape rather than the checksum, and refuses to run on a case that does not.
- **`CategoryInfo.Verify` is where a checksum goes** — a value that fails it is
  dropped outright rather than scored down — **and it is for any rule the regex
  cannot express** (`DOBCheck`, `PostcodeCheck`, `GenericSecretCheck`). A `Verify`
  hangs off the *category*, so it guards every notation at once; `Pattern.Verify`
  is the per-pattern half, and the difference matters (`UnclosedBracketCheck`).
- **A `Verify` that reads the clock takes an injectable one.** `DOBCheck` calls
  `dobCheckAt(value, time.Now())` and the suite pins the day; against `time.Now` the
  boundary cases age out one by one and the suite goes green over a rule it has
  stopped exercising. For the same reason a corpus negative uses a year far out.
- **Narrowing a credential pattern is the change that leaks**, so what must still be
  caught is asserted beside what must not:
  `TestGenericSecretCheckRejectsSourceCode` and
  `TestGenericSecretCheckKeepsCredentials` are one pair, neither meaningful alone.
  `TestGenericSecretCheckOverMasksLowercaseMemberAccess` records what was knowingly
  given up, and `TestOurOwnSourceGrowsNoCredentials` is the measure that matters —
  what a code review through this agent would see.
- **A payload that carries the catalogue grows with the catalogue**, and `Query`
  reads `/healthz` under a bound. `TestHealthPayloadFitsTheQueryBound` fails at
  *half* that bound, so the batch that would break it is the one that still gets to
  choose the number.

## Lessons not to reimport

- **Nothing enters the tree unexercised.** A feature that has never run is not a
  feature.
- **No documentation ahead of the code** —
  `TestDocumentedEnvironmentMatchesTheCode` fails in both directions.
- **CI must trigger on the real default branch.**
- **`version` is a `var`, not a `const`** — declared `const`, the `-ldflags` stamp is
  silently inert and every release reports the same string.

## OpenWiki

Documentation lives in `openwiki/`. **Start with
[the quickstart](openwiki/quickstart.md), then follow its links to the page for the
area you are changing** — the pages named at the head of each block above are where
the reasoning lives, and this file is only the index of the rules.
