# CLAUDE.md

Guidance for Claude Code working in this repository.

Neverseen is an agent installed on each workstation. It proxies the AI tools
people use to a model provider, masking personal data and credentials on the way
out and restoring them on the way back. Module path:
`github.com/neverseen-ai/neverseen-agent`. Licence: FSL-1.1-ALv2 (source available, not
open source — say "source available").

The paid supervision backend is a **separate, private repository**
(`cloakfleet-cloud`), checked out alongside this one — `../cloakfleet-cloud`. It
imports this one; this one must never import it, and must compile and run with no
backend at all. Read it there when a change touches the shared contract
(`pkg/telemetry`); never add it as a dependency.

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

Each line below is a rule whose absence has already leaked. The demonstration —
the request that failed, the value that went out in clear — is in the wiki page
named at the head of each block. Read that page before changing anything in the
area: the reasoning is what stops a tempting simplification being reintroduced.

### The request path — `openwiki/architecture/request-path.md`

- **One entrypoint assembles the pipeline**: `cmd/neverseen`. Two entrypoints
  drifted until the same request was masked in one and answered in clear in the
  other. `cmd/neverseen-tray` is the one exception: it assembles nothing — no
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
- **A tail belongs to the block it was held back from**, and `closeBlock` releases it
  there. Carried across, it prefixed the next block's text — or, when that block was
  a tool call, arrived after the stream had ended in an event for a block closed long
  before, which is what it actually did.
- **`deltaText` enumerates the shapes a provider streams text in, and the ones it
  missed were the ones carrying tool calls.** Only `delta.text` and
  `choices[].delta.content` were known, so `delta.thinking` and
  `delta.partial_json` fell into the branch that expands whole tokens in place and
  holds nothing back: a value split across two events was never restored, and the
  tool acted on `[EMAIL_1]`.
- **A tool call's arguments are a JSON document arriving in slices, so they are
  accumulated and expanded whole** (`jsonFragment`, `expandedArguments`). Expanding a
  value into a slice splices it into the *source* of a document only ever seen a
  piece of, and an original carrying a quote ends the string it landed in — the
  client's parse then fails, at the client, silently. Held until the block stops, the
  concatenation is a whole document and the encoder escapes, which is the rule the
  request path already follows. **Nothing is lost by waiting**: a client cannot use
  half a JSON document, so it waits for the stop in any case. Released **before** the
  stop that completes them, and exactly once. Fragments that do not make a document —
  a stream cut short — fall back to whole-token expansion, which is what the agent did
  before: never worse, and dropping them is the one outcome that would be.
- **An SSE event is a name line and a data line, and holding one back means holding
  both.** A client dispatches on `event: <name>`, so the name and its `data:` travel
  together or not at all. Rewritten a line at a time, the name went out the moment it
  arrived and every held-back fragment left it behind with nothing under it: the caller
  parsed the empty string and reported `JSON Parse error: Unexpected EOF`, killing the whole
  answer on the first tool call of every exchange. The reverse too — a synthesised event
  emitted as a bare `data:` line is attached by the client to whichever name came last,
  which was the stop that released it. So `rewrite` withholds the name (`takeName`) and
  emits it with its own data line, after whatever the previous block was holding; the two
  synthesised events carry the name their original arrived under (`argumentsName`,
  `templateName`); and a held event's blank separator goes with it (`held`), because an
  event that emits nothing emits none of its three lines. **A test stream with no names in
  it cannot exhibit a name that lost its data** — which is how the suite stayed green over
  an agent no client could talk to, and why `namedEvents` exists beside `events`.
- **Overlap arbitration, in order**: credential, then confidence, then the longer
  span, then the leftmost. Each rule is there because its absence leaked.
- **The agent holds no API keys.** The caller's credential is forwarded untouched.
- **What `-a` and `-v` reveal never changes what the agent does.** `unmask` returned
  early on a session that had minted nothing, so an exchange with no personal data in
  it — most of them — was never read for its token counts and never reached the
  heartbeat; gating that on a console being attached instead would have had two
  agents on identical traffic report different totals, and a trace's `unmask`
  duration appear and vanish with a flag. Every textual answer goes through, because
  an answer says three things and only one of them depends on the mapping: what to
  put back, what the exchange cost, and what tool the model asked to run. **Read for
  all three, rewritten only for the first**: the decode-and-encode round trip is not
  byte-preserving — a lone surrogate becomes U+FFFD, pretty-printing is compacted, a
  text ending on `[` is held back for a token that cannot arrive — so a session that
  minted nothing has its answer forwarded verbatim (`streamRehydrator.observe`, and
  the `len(known) > 0` guard in `unmask`) while the counts and the tool calls are
  still read out of it. `TestAnAnswerToAnEmptySessionIsForwardedVerbatim` and
  `TestAuditPrintsAToolCallWhenNothingWasMasked` are the two halves.
- **The identifiers a client uses to name itself to Anthropic reach Anthropic in
  clear** (`identifiers.go`). Claude Code writes `metadata.user_id` as a JSON
  document of its own — `device_id`, `account_uuid`, `session_id` — and
  `SESSION_ID` is in `genericSecretNames`, so the agent replaced the session id
  with `[SECRET_1]` in the field Anthropic defined for the client's own
  bookkeeping, holding an identifier Anthropic itself issued. Masking it protects
  nothing and costs the thing rate limiting and abuse tracking key on: a client
  reporting a new identity every time this agent restarts. **Read from that field,
  applied by value**: the same session id also travels in the arguments a tool was
  called with, so exempting the field alone sends one identifier in clear in
  `metadata` and as `[SECRET_1]` three lines above it, one exchange, two
  identities — while reading the three *names* anywhere in the body exempts a
  `SESSION_ID=` line somebody pasted out of their own `.env`, which is their
  credential and not Anthropic's identifier. **The anchor is the path
  `metadata.user_id` in the decoded document, not the field name**: a regex for
  `"user_id"` over the raw bytes also matched the `input` of a tool call in the
  history, and a tool once called with a `user_id` of its own exempted whatever hex
  sat inside it body-wide. **The shape is the
  second**: only a lower-case UUID or hex blob, so `session_id: sk-ant-…` inside
  that document is still a credential. **Anthropic only, and Anthropic is the host
  the route resolves to** (`identifierHost`), not the route's code: the same
  `session_id` on the way to another vendor is a value that vendor has no business
  seeing, and `NEVERSEEN_PROVIDERS=anthropic=https://gateway.internal` — the
  override `.env.example` documents — makes the route named "anthropic" another
  vendor. Keyed on the code, every request through that gateway carried the three
  identifiers in clear.
  `Pass.Exempt` is per request rather than on the detector, because what goes in
  it is read out of the body being masked; `Detector.allowed` is the
  deployment-wide half of the same idea.
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
- **`neverseen proxy -a` prints every value replaced and restored; `-v` writes
  every exchange to `traces/` — the two request bodies and the answer.** Two
  independent flags on the one command: `-a` alone keeps nothing, `-v` alone
  records and prints nothing, and `newAuditor` builds an auditor for either —
  requiring a console to record a trace would have made the quiet half silently
  do nothing.
- **`-l` binds somewhere other than loopback, and the agent warns rather than
  refuses.** The address already arrived by `NEVERSEEN_LISTEN`; the flag is the
  same setting where a one-off run can reach it, and the command's choice wins over
  the environment as it does for every option. The guard is `proxy.BeyondLoopback`
  asked at the point the agent starts listening, **not on the flag** — the variable
  exposes exactly as much and warned nowhere. `:8787` counts as reachable: it reads
  as "no address" and binds every interface. A warning rather than a refusal because
  serving a container or a VM on this workstation is a real thing to want, and an
  agent that refused is one somebody patches out. What it costs is the reason
  `/healthz` and `/test` are unauthenticated at all: reachable, `/test` is a masking
  oracle for anybody on the network, and a session is named by a header the caller
  chooses.
- **These replaced a separate `audit` command, and the guarantee it carried is
  gone.** As a command, printing in clear was a *mode* somebody entered, on a
  port of its own. As a flag it can go in a service definition — and the
  installer sends this agent's output to `~/.neverseen/agent.log`, so `-a`
  there keeps every prompt in clear for as long as the service runs. The banner
  says so on every start, because documentation is not where somebody reads it.
- **A tool call is the one thing on the console that is neither a value nor a
  replacement**, and it is printed with its arguments in clear (`auditor.tool`). The
  rest of an answer is prose for a person; a tool call is an instruction the tool on
  this workstation is about to carry out, so "the model asked to run `Bash` on this
  path" is the line that says whether the masking held all the way to the thing that
  acts. Both halves or nothing: the streaming path reports at
  `expandedArguments`, where the tool's name and the whole restored document exist at
  once, and `reportToolCalls` walks a buffered answer's `content[]` — shown for a
  streaming client alone, the silence would read as an answer that asked for no tools.
  Reported **even when the session minted nothing**, which is why `unmask` no longer
  returns early on an empty mapping while a console is attached: gated on the mapping,
  the line would appear for a prompt holding an address and vanish for one that did
  not. **Anthropic's shape only** — the OpenAI-compatible families are not read, the
  same gap `jsonFragment` already records, because a guessed shape prints a name no
  provider sent. What it costs is real and knowingly paid: the arguments carry the
  caller's own paths and commands, so under the installer's service `-a` files every
  tool call of every exchange in `~/.neverseen/agent.log`, and a `Write` of several
  kilobytes scrolls the MASK lines away — the reason bodies came off this console in
  the first place. A `TODO:` names the missing ceiling.
- **The console carries no other body**, and that is why nothing marks one any more:
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
- **The answer is recorded as it arrived, before a single replacement was expanded.**
  The recorder wraps the body before the rehydrator does, so the file holds what the
  provider sent, streamed events and all. That half carries no restored value, so it
  adds no class of exposure the file did not already have — and read against the OUT
  body above it, it says which of the tokens sent up came back. Recorded even when the
  expander will not touch the body: an outbound half on disk with no answer below it
  reads as an exchange that never came back. It is appended when the body closes rather
  than written with the rest, because the outbound half goes to disk *before* the
  request leaves — an exchange the provider never answers still leaves behind the body
  that was about to go. A caller that hangs up files what had arrived: partial and
  honest beats a trace that vanishes for the exchange somebody abandoned.
- **What the exchange cost is four durations, not one total, and tokens rather than
  a price.** Masking is this agent's own scan — timed around the detector alone, not
  the vault write and not the trace write, or the figure would say how much `-v`
  costs; `upstream` is the provider's latency, dated when its headers arrived;
  `delivering` is how long the answer took to arrive, dated at the *end of the body*
  and not at the close, because the buffered path reads, then expands, then closes;
  `unmask` is the restoration, summed per event on a stream rather than measured on
  the wall clock, which is almost all waiting. A sum of the four answers none of the
  four questions. **The counts are never a price** — that table belongs to the
  backend, the rule `usage.go` already carries — and the line is omitted rather than
  zeroed when the answer named no model, because a row of noughts reads as an
  exchange that cost nothing when what happened is that the agent could not read it.
- **A stream is also written back together, above the events and never instead of
  them** (`reassemble.go`). At the grain it arrives in it is unreadable: a few
  characters per event, and a tool call's arguments come apart mid-path — `"R=/U"`,
  `"sers/alice"`, `"ly/Projets/s"`. So the section above holds one entry per content
  block, in the order they opened, labelled by the start event: a tool call carries
  the tool's name, which is what stops it reading as a wall of JSON. It is **derived
  and the file must not be only that** — reassembling decodes the JSON strings, so
  what reads well is exactly what no longer holds the bytes that went over the wire.
  Readable first, because that is what the file is opened for; verbatim below,
  because that is what it is kept for. Omitted rather than empty for a buffered
  answer, which is one document already.
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
  exit code of `neverseen status` and the menu bar icon both follow **Level**, so
  zero means "everything this configuration loaded is being replaced".
- **A level counts the effect, `Disabled` records the intent, and the level counts
  only what the loaded patterns can emit** (`disabledInPlay`). Against the raw
  disabled set the two disagreed: with `fr` alone and a US category switched off,
  the level said "partial" while every surface listing what is off filtered that
  category out as unrecognisable — so `neverseen status` printed "masking, with 0
  categories in clear", the icon went amber over the same nought and the exit code
  was non-zero. The policy still remembers the switch, so loading `us` later finds
  the category still off. `TestTheLevelCountsOnlyWhatTheDetectorCanEmit` holds both
  halves: unreachable stays full, reachable still drops to partial.
- **`neverseen mask` and the menu bar are the two surfaces, and both go through
  `proxy.SetPolicy`** — the one writer, as `proxy.Query` is the one asker. The command
  exists because the menu bar is Cocoa and a Linux workstation had the route and no
  way to reach it. It accepts a family name as well as a category code, and lists only
  what the detector can actually emit (`Detector.Categories`): a switch for a category
  no loaded locale can find would say the agent is masking it.
- **Four things change while the agent runs: the switched-off categories, the
  substitution mode, the secret level and the loaded locales.** All three live behind one atomic
  pointer in `detector.policy`, and the locales carry `patterns` and `fakes` with
  them in one `catalogue` value — swapped whole, because a scan reading new patterns
  against the old stand-in table would render a French address with an American
  postcode. One atomic load per scan, not a lock: the alternative is a read lock on
  the hottest loop in the agent to serve a click a day.
- **The secret level grades one pattern, and only one.** `pii.SecretStrength`
  counts character classes — weak, medium, strong — and `Detector.strongEnough`
  drops a `SECRET_GENERIC` match below the level. Every other credential is
  identified by a prefix somebody can verify, and a level that could stop masking a
  real Anthropic key would be a setting whose only effect is to leak.
  `TestPolicySecretLevelGradesOnlyTheCatchAll` holds both halves at once.
- **A separator is not a character class.** Counting the hyphen,
  `troisieme-valeur-longue` scored a class above `troisiemevaleurlongue`, which put
  every passphrase of plain words into medium and left **weak unreachable** — a level
  in the menu that could never differ from the one below it. A separator says how a
  value is written, not how hard it is.
- **Classes, not entropy.** Shannon entropy at the threshold gitleaks uses (3.5) was
  measured against this corpus and did worse: it missed `hunter2-correct-horse` and
  `p@ssw0rd!` — real credentials, both short — while still claiming
  `security.authorize(plainUser`. Entropy rewards length and spread, which is what a
  long code expression has and a short password has not.
- **The default level is weak, and it has to be.** It is what the agent did before
  the level existed, and a setting nobody has touched must not quietly mask less
  than it used to.
- **Load order cannot decide a secret against a PII category, and no setting should
  pretend otherwise.** `pickFromCluster` ranks on `IsSecret` first, so the two never
  reach the tie-break that load order feeds. Measured as well as argued: reversing
  the two sets across the whole corpus and the sample — 199 texts — changed nothing.
- **`PUT /policy` replaces the whole state, and refuses a partial request — in
  `applyPolicy`, so the stored file is refused the same way.** Guarded at the route
  alone, `{"off":["EMAIL"]}` got a 400 when sent and was applied on restart: both
  parsers accept `""` and `SetLocales(nil)` unloads every locale, so a hand-edited
  file took the agent off its environment and left it masking almost nothing. Not
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
- **`scan` and the agent assemble the detector through one function**
  (`proxy.DetectorFromEnv`: the environment, then the stored policy). `scan` built
  its own from the environment alone, so a category unticked in the menu bar was
  reported as masked by `scan` and forwarded in clear by the agent beside it — the
  "two entrypoints drifted" failure, between two commands of one binary.
- **What a surface changed survives the restart, and the file is the state**
  (`~/.neverseen/policy.json`). Everything the menu bar could do lasted as long as
  the process: somebody unticked a category, restarted, and the agent came back
  masking it while the menu they set said otherwise on the next click. The file holds
  the four fields `PUT /policy` carries, in the same shape, because it *is* that
  request; one applier (`applyPolicy`) serves the route and the start-up read, or a
  category switched off through a menu comes back on through a restart. It is written
  from the **detector** and on the refusal as well as the success, because the route
  is not a transaction and what must survive is what the agent *is*; `off` is stored
  as the **intent**, unreachable categories included, for the reason `disabledInPlay`
  exists. **It wins over the environment** — the environment configures an agent
  nobody has said anything to yet — so with the file present `NEVERSEEN_PII_LOCALE`
  does nothing and deleting it hands the agent back; the start-up line says which of
  the two was read. A file that cannot be read leaves the environment's configuration
  alone rather than stopping the agent, and nothing is written until something changes
  one, because the file's absence has to keep meaning "nobody has" — and that
  includes a request the applier refused **before** it changed anything. Written from
  a `defer` above the applier, a misspelled mode created the file on a 422 and took
  the agent off its environment permanently, over a request that changed nothing, so
  `applyPolicy` reports `applied` and the two kinds of refusal are told apart —
  and `applied` is the locale list having *differed*, not `SetLocales` having
  returned nil: every surface resends the whole state, so a category refused over
  the loaded locales created the file the same way. **A surface resends
  `Health.Off`, not the switches it draws**: `Groups` lists only what the loaded
  locales can find, so a set rebuilt from it dropped the unreachable half and the
  next click on anything wrote that loss to disk — SSN off, `us` unloaded, one click
  on "fake", and loading `us` later found SSN on. `SwitchedOffCodes` reads `Off`. The
  handler is serialised (`policyMu`) and the file goes through a temporary name of
  its own: storing it is a read-modify-write, and two surfaces interleave the halves
  of one.
- **A category can be switched off, and `PUT /policy` is the only way.** It is the
  one route that changes what the agent does and the only authenticated one: a
  secret in `~/.neverseen/control.key` (0600), in a custom header, which is what a
  browser cannot set cross-origin. Left open, any local process — or a page
  somebody visits — could disable the control silently. The whole set is replaced,
  never toggled: two surfaces on one agent interleave the halves of a
  read-modify-write. **A credential is refused by the detector**, not by the menu,
  so nothing can route around it; `pii.Switchable` is that rule.

### The browser extension — `openwiki/architecture/browser-extension.md`

- **The extension holds no engine.** No catalogue, no policy, no mappings, no
  detector — it hands text to `POST /mask` and `POST /unmask` and puts back what
  comes out. A WASM build or a TypeScript rewrite is a second answer to "what does
  this agent mask", and JavaScript regexes have neither RE2's semantics nor the
  corpus that was measured against them.
- **It lives in this repository because the API is a contract.**
  `extension/testdata/contract.json` is recorded once and replayed by both sides —
  `internal/proxy/contract_test.go` against a real agent, `extension/test/contract.test.ts`
  against the client — so a field renamed on either side fails on the other, in the
  same commit. Regenerate with `make contract-update`, never casually.
- **Fail closed on the way out, open on the way back, and the asymmetry is
  deliberate.** A send that could not be masked is not sent: a value that reaches the
  model cannot be recalled. A chunk that could not be restored is shown unexpanded:
  failing closed there discards an answer already paid for and already arrived, to
  prevent nothing — a visible `[EMAIL_1]` is unreadable, not unsafe. Every block also
  raises a banner naming the command that fixes it; a rejected fetch alone reads as a
  broken site.
- **`/mask` takes a list, and that is not batching.** A send carries several text
  fields, and one pass is what gives a value repeated across two of them one identity.
- **`/unmask` is stateless: the tail travels with the client**, held back with
  `detector.TailLen` — not `pii.TokenTailLen`, because a stand-in splits across two
  events exactly as a token does. `final: true` is the protocol's explicit end;
  without it a value masked at the very end of an answer is never shown.
- **One shared auth helper** (`Server.authorised`), constant-time, for all three
  authenticated routes. **`/unmask` is loopback only, hard**, whatever `-l` bound, and
  checked before the key is read: it answers "what does this replacement stand for",
  which is the mapping one question at a time. `/mask` is not — it exposes what
  `/test` exposes. **No CORS headers on the agent, ever.**
- **Neither route is captured by `-a` or `-v`**, structurally: the handler passes
  `nil` where the proxy passes the audit callback. A trace of `/unmask` would put
  originals on disk through a path the invariant never considered.
- **The key never leaves the service worker**, and never a bare HTTP route. An
  unauthenticated `GET /key` "to keep it simple" is the hole DNS rebinding exploits.
- **The page's world names no session, and it cannot be made trustworthy.** The
  interceptor must run in `world: "MAIN"`, so any script the site loads can post what
  it posts and read the answer — there is nothing there to authenticate with. So
  `PageAsk` carries no session: the relay derives it from its own `location` and
  `bridge.acceptAsk` rebuilds every ask field by field, never spreading and never
  casting, or a `session` the page added rides along. `namesAWebSession` is the
  worker's second copy of the rule, and the name it must always refuse is `default` —
  the session every tool without a header shares, which carries a terminal's traffic.
  A banner note carries a situation from a closed set, never a sentence, so nothing
  can put its own words behind this extension's name.
- **A stream event is rewritten structurally, never as raw bytes**, and the calls are
  serialised — chunk *n*'s tail prefixes chunk *n+1*, so two in flight is corruption.
- **A transport that cannot be masked is refused**, not forwarded: `XMLHttpRequest.send`
  and `sendBeacon` are synchronous and a `WebSocket` is open before there is anything
  to inspect. A site that moved its chat onto one would otherwise keep working and
  mask nothing.
- **`extension/e2e/run.test.mjs` is the only test with nothing stubbed**, and it
  asserts both halves: what the site received (masked) and what the page rendered
  (restored). Either alone passes over a page that was never touched.

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
- **`State.Addresses` and `State.Hostname` are the two fields that are personal
  data**, and their entries say why. Local addresses only, loopback and link-local
  dropped, stably ordered, capped. The hostname is the more direct of the two — a
  workstation is often named after the person using it — and that is why it is
  reported: it is the identifier an operator recognises, and the one that survives
  a laptop moving between networks. As the OS gives it, never resolved, never
  hashed.
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
- **A test whose buckets are fixed calendar dates pins the reporter's clock too.**
  `Config.Now` exists for this and every reporter test uses it but one, which passed
  `time.Now`: seven days after `epoch` the age bound began discarding its fixtures,
  one every five minutes of wall time, and
  `TestRunDrainsABacklogWithoutWaitingAnInterval` went from green to 62 heartbeats
  to 61 without a line of code changing. It is the failure `DOBCheck`'s injectable
  clock is documented against, in the opposite direction — red rather than silently
  green — and the same fix. It now also asserts `dropped == 0`, because the count
  alone cannot tell a bucket pruned from a bucket never sent: both read as one
  heartbeat short.
- **The backlog goes 60 buckets per request, oldest first, retried a second apart
  while any remains**, on a ladder capped at the interval and reset by one
  success.
- **`snapshotInterval` (30s) bounds what a hard kill loses**, its window ending at
  the snapshot rather than at the kill.
- **`NewRecorder` counts the restart**, because a process builds exactly one.
- **The OpenAI cached-token breakdown is deliberately not read** — it is a subset
  of the input, and reading both would bill the same tokens twice.
- **A map keyed on something the agent observes is keyed on a closed vocabulary, and
  the rest is `Other`** (`pkg/telemetry/vocabulary.go`). Client families, tool names,
  programs and command classes are the words a heartbeat could smuggle text through;
  the word "python3" in a heartbeat comes from `KnownPrograms`, not from the prompt.
  The recorder re-checks every key (`inVocabulary`), so the rule does not depend on
  the call site. Widening a list is a contract change.
- **The agent computes no average.** A session's totals fall into power-of-two
  `Histogram`s when it closes and the backend reads the median — a mean over a
  five-minute window is wrong for a conversation that lasts hours, and the agent
  would be choosing the statistic. The same rule as prices.
- **A session is the conversation the mapping is scoped by**: the header, else the
  id Claude Code writes in `metadata.user_id` on the way to Anthropic
  (`conversationOf`), else the shared default. `SessionIdle` equals
  `vault.DefaultTTL` and a test holds them together. The identity never leaves the
  recorder.
- **`Tools.Restored` is read off the expansion, not off the mapping**, and on the
  buffered path `reportToolCalls` runs *before* the whole-document pass:
  `mapStrings` rewrites in place, so run after, it found every input already
  expanded and counted nothing.
- **`State` says how the agent itself is exposed** — `Console`, `Tracing`,
  `Exposed`, `Rerouted`, `Allowlisted`. An agent applying its whole catalogue is
  still a risk if `-a` is filing the day's prompts under the installer's service.
- **`neverseen replay` is read-only.** A bucket rebuilt from traces shares no
  window boundary with one filed live, so the backend's `(agent, window)` key would
  not recognise a retry and every count would double. The mapping it decides
  `Restored` against is the trace's own (`recoverMapping`), because token indices
  are a detector counter and a replay renumbers.

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
  `eval "$(neverseen env)"`, which prints nothing when the agent is stopped —
  availability over enforcement, on purpose. It touches no login file unless asked
  (`--shell`), and `--uninstall` leaves `~/.neverseen/` alone.

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

- **A checksum on its own is not evidence of a category — the length is the other
  half.** Measured across every checksummed category: Luhn clears ~10% of arbitrary
  runs, NHS mod-11 ~9%, ABA ~4%, the NIR key ~0.9%. That is all a checksum promises,
  so wherever the real identifier fixes a length, the shape must fix it too. Five of
  the six already did; the two that did not are recorded below, and both were found
  by asking this question rather than by a report. `ae5917ce58a7f1e2`, a short git object id, was masked as a bank account: it
  opens on "AE", a real country code, and one arbitrary string in ninety-seven clears
  mod-97. `ibanLengths` is what kills that class, because a hex blob can only open on
  letters a–f and almost none of those pairs names a country whose IBAN is that
  short. An **unknown** country code is still accepted: the registry gains members,
  and refusing one would silently stop masking a real account the day a country
  joined. The generic lesson is the one the file already carried as a `TODO` — where
  a category fixes a length, check it, or the checksum is doing all the work alone.
- **The card shape fixes the length per brand, and the Visa branch is why.** It
  ended on `\d{1,4}`, admitting thirteen, fourteen, fifteen and sixteen digits — and
  no Visa card has ever had fourteen or fifteen. Two lengths of pure false positive,
  each catching a tenth of the numbers that reached them, which is the IBAN failure in
  another costume. `TestCreditCardShapeRejectsLengthsNoVisaHas` uses values that all
  pass Luhn, so it tests the shape rather than the checksum — a case that failed Luhn
  would go green for the wrong reason, and the test refuses to run on one.
- **Each card branch now carries exactly the lengths its network issues**: Visa 13, 16
  and 19, Amex 15, Mastercard and Discover 16. The nineteen-digit Visa was the last
  gap and it was a **miss** — a real card forwarded in clear, the worse direction than
  a reference masked for nothing. Seventeen, eighteen and twenty sit between real
  lengths and are none of them. **Discover stays at sixteen deliberately**: ISO/IEC
  7812 permits nineteen and no source confirms the network issues one, and a guessed
  length either misses real cards or claims references — both silently.
- **A checksum lets a shape be loose; without one, the shape is all there is.**
  `CategoryInfo.Verify` is where a checksum goes, and a value that fails it is
  dropped outright rather than scored down.
- **`Verify` is not only for checksums — it is for any rule the regex cannot
  express.** `DOBCheck` is the case that makes the point: a date has no checksum,
  and its shape is satisfied by every deadline, renewal and invoice date in a
  prompt. What separates a birth date from those is where it sits relative to
  today, so the rule is a year in the past — "not in the future" alone still
  admits every date since January. It hangs off the *category*, so it guards
  day-first, month-first and ISO at once; a rule per pattern would have been
  three, and the third would have been forgotten. Its known cost is recorded as
  a `TODO`: an infant's date of birth is real personal data and this drops it.
- **A `Verify` that reads the clock takes an injectable one.** `DOBCheck` calls
  `dobCheckAt(value, time.Now())`, and the suite pins the day. Against `time.Now`
  the boundary cases age out one by one and the suite goes green over a rule it
  has stopped exercising. For the same reason a corpus negative uses a year far
  out (2099) rather than a near one.
- **A commune is not a month.** The French postcode pattern takes the commune with
  the code, because five bare digits are not identifiable — so any capitalised word
  after five digits is a commune, and `ls -l` puts one there on every line:
  `13469 Mar`, `11175 Mar`, `87617 Aug` were masked as postcodes. `13469 Mar` and
  `13290 Aix` are written identically, so only the word can decide, which is why
  `PostcodeCheck` is a `Verify` and not a narrower expression. **The three-letter
  abbreviations only, as whole words**: the full names would drop `PE15 8NF` (March,
  Cambridgeshire) and `42750 Mars` (a commune in the Loire), and a prefix match would
  drop `14320 May-sur-Orne` — a guessed list here forwards a real address in clear.
- **A token prefix is not a category code, and `CatDOB` is where they differ.** The
  code stays `DOB` — the corpus, the counters and `neverseen mask --off` all name it
  — while the prefix is `DATE`, because the token is read by a model and `[DATE_1]`
  says what the value was where `[DOB_1]` is an acronym it has to guess at.
- **`NAME=value` is evidence in configuration and noise in source code.**
  `genericSecretRe` treats the name as the proof, which holds for a `.env` line and
  collapses in a repository, where `password:` is a *field* name and the right side
  is an expression, a type or an identifier. Pointed at one, it claimed
  `newPassword`, `req.cookies.token`, `process.env.LLM_API_KEY` and
  `CreationOptional<string` — and the model received a review of code whose
  identifiers had been replaced by `[SECRET_n]`. `GenericSecretCheck` is the guard,
  and each of its rules is narrower than it looks, because the tree already held a
  case against every over-reach. The slash and the plus are not code punctuation —
  base64 is made of them.
- **What makes a bracket code is that it is *unclosed*, not that it is there.** The
  span was cut out of the surrounding text, so an opener with no closer inside it
  says the expression carries on past where the value stopped:
  `security.authorize(plainUser`, `generateSecret(`, `CreationOptional<string`.
  Presence alone was the rule, and it refused a password for holding a matched pair
  — `password="pa(ren)th1s"` and `password="[brackets]1"` went out in clear. A stray
  *closer* stays allowed, as it always was: `password="hunter2)"` is a real
  credential ending on one, and no expression begins that way. **The rule hangs off
  the bare patterns, not the category** (`Pattern.Verify`, `UnclosedBracketCheck`):
  a quoted value ends where its quote does, so `password="Ab(12cd"` is somebody's
  "one special character" password, and under the category it was refused as code
  and forwarded in clear — `GenericSecretCheck` is handed the value and not the
  quotes, so it cannot tell the two apart. `;` and `,` left the
  rule entirely — no case in `TestGenericSecretCheckRejectsSourceCode` carries
  either, while `password="a;b;c1234x"` and `API_TOKENS=abc12345,def67890` were
  refused for it. A `?` is only code as `?.`: `user?.token2` is optional chaining,
  `password="Wh4t?Really"` is punctuation somebody typed.
- **A variable reference is where a credential is read from, not one**
  (`interpolationRe`). Once the bracket rule moved onto the bare spans alone, the
  quoted `password: "${DB_PASSWORD}"` was a credential while the bare
  `POSTGRES_PASSWORD=${DB_PASSWORD}` stayed refused — one value, two answers,
  decided by the quotes — and a pasted `docker-compose.yml` came back with its
  references replaced by `[SECRET_n]`. A closed set of four syntaxes, matched
  whole: `${…}`, `{{…}}`, `$(…)`, `%(…)s`.
- **A name is written in words joined by case; a password is not — and the join is
  a capital after a lowercase letter, not any interior capital.** Read as any
  capital, `PASSWORD=HUNTER` was refused as a code identifier while
  `PASSWORD=Hunter` was masked, the same SCREAMING-case misread the name side had
  already fixed. The rule before that was
  "identifier-shaped and carrying no digit", and the digit cannot do that job:
  `PASSWORD=correcthorse`, `PASSWORD=changeme` and `password="correcthorse"` are
  real credentials of nothing but lowercase letters and all three were forwarded in
  clear — the whole of the gap this catalogue was measured against betterleaks on.
  What separates them from `newPassword` is the case: every dotless digitless code
  case asserted in the tree is camelCase (`publicKey`, `totpToken`,
  `newPasswordInString`), because that is how code joins words into a name. An
  **interior** capital, because `MyPassword123!` and `Sup3rS3cr3tValue123` open on
  one and must stay credentials. The floor came down with it, from seven characters
  to six: `PASSWORD=mcjrx4` is a bad password, not an absent one.
- **A word the language reserved is not a password**, and this is the cost of the
  rule above rather than a separate idea. Once a lowercase word counted as a
  credential, `secret_level: string` in this repository's own TypeScript claimed the
  *type* and `'X-Session-Id': 'default'` in its extension claimed the session name.
  `TestOurOwnSourceGrowsNoCredentials` is where both appeared, which is the measure
  that matters — it is what a code review through this agent would have seen.
  `reservedWords` is a closed set of tokens some language spells exactly that way,
  so it can be checked rather than argued about.
- **A dotted identifier chain is a property path, whatever digits it carries.** The
  digit rule above reads a digit as evidence of a credential, and a member access
  gets one for free from a service name: `c.S3.SecretAccessKey` was masked as
  `[SECRET_1]`, so the model reviewed Go code with a field access replaced by a
  token. `s3`, `ec2`, `oauth2`, `sha256`, `v1`, `utf8` — every cloud SDK is spelled
  this way, and each satisfies both halves of the digit rule at once. `readsACredential` refuses
  them, and it is narrower than it looks in the one way that matters: the dot has to
  be **interior**. `password="hunter2."` hands the
  check the value `hunter2.` — a quoted value ends where its quote does, so that dot
  is the credential's own — and reading a trailing dot as a member access stopped a
  real secret being masked, which is the only direction this rule must never move
  in. `identifierOnlyRe` admits `masked...` for the same reason.
- **A segment of a property path is the size and shape of a name, and without that
  bound the dot did all the work alone.** `WARP_READ_TOKEN=yrqUJ…Vhq8.37Zim…XlF` was
  forwarded in clear: a hundred and eighty-two characters of base64url carrying one
  interior dot, which bought it the promise the rule above writes for
  `c.S3.SecretAccessKey`. `propertyPathRe` is that bound — every dot-separated
  segment must be letter-led and at most forty characters — and the observed token
  fails **both** halves, because no field access is a hundred and sixty-one
  characters long and no language names a member `37Zim…`. The cap is loose on
  purpose: tightening it past what code really writes would stop masking nothing and
  would only start claiming member accesses again, which is the direction rule three
  exists to prevent, so `security.authenticatedUsers.tokenOfTheCurrentSession` is
  asserted beside the token. **A threshold on the mix of character classes cannot do
  this job** — measured on this tree's own values, `opts.Sha256Digest` is 17.6% digits
  and the observed token 17.0%, so any floor puts a real member access and a live
  credential on the same side; and by class *count* the corpus's own 32-hex session
  token has two where `c.S3.SecretAccessKey` has four. What used to be given up is a password
  whose every dot-separated run is short and letter-led: `secret=abcdef.ghijkl`.
- **What a dotted chain *says* is what settles it, because no shape can.**
  `query.current` is two lowercase segments of ordinary length naming nothing, and so
  is `abcdef.ghijkl`; measured against every case in
  `TestGenericSecretCheckRejectsSourceCode` there is no length, segment count or
  character mix that separates them. `readsACredential` reads the chain instead, and
  either mark is enough: **the last segment names the credential** —
  `secret = config.password` is code fetching a password rather than a password, and
  so are `req.cookies.token`, `c.S3.SecretAccessKey`, `aws.Config.Credentials`,
  `headers.authorization` — or **the chain carries an interior capital**, which is
  what keeps `opts.Sha256Digest`, `utf8.RuneCountInString` and both long chains
  refused. The first mark is the sentence `slugNamingItselfRe` already writes for
  `reset-password`: a passphrase names neither what it unlocks nor where it was read
  from. **The price is asserted, not buried**: six member accesses flipped to masked
  — `query.current`, `query.new`, `query.repeat`, `body.new`, `body.repeat`, `a.b2`
  — and `TestGenericSecretCheckOverMasksLowercaseMemberAccess` holds them, so a later
  rule that recovers them fails loudly instead of looking like a bug. The trade goes
  this way because the halves are unequal: a property access masked in a review is
  over-masking somebody can see and undo, and `secret=abcdef.ghijkl` in clear is a
  password delivered to a model.
- **A short word at the end of a chain needs a boundary in front of it, or an English
  word reads as a field holding a credential.** `credentialNameTailRe` carried bare
  `key` and `auth` with nothing before them, so `password: monkey.donkey` had its
  last segment matched on the "key" of "donkey", `readsACredential` called the chain
  code, and a real password went to the model in clear — the one direction that rule
  must never move in. The long keywords keep the bare suffix match, because no
  English word ends on `password` or `authorization` by accident; the two short ones
  need the start of the segment, a separator, or the lowercase letter that makes a
  camelCase join.
- **The leading `*` is stripped above every rule that reads the value as a word, not
  below them.** Placed after `reservedWords` and `slugNamingItselfRe`, neither ever
  saw the stripped value: `SECRET=*string` was masked as a credential while
  `SECRET=string` was correctly refused, and a YAML alias — `password:
  *default_secret` — was one for the same reason. What the star points at is what
  decides, so the rules that decide have to be handed it.
- **These twelve shapes were qualified by a person, one at a time.**
  `docs/secret-shapes-to-label.md` is the working file and the corpus block
  `sec-qualified-*` is where the answers landed. Each was a rule refusing a real
  credential on evidence that turned out to be about the *form* of the value rather
  than about code: punctuation, length, the absence of a digit. A rule of that kind
  is worth putting to somebody rather than reasoning about alone — the answers went
  against the code in twelve cases out of forty-four, and the other thirty-two
  confirmed it.
- **Quoting decides whether trailing punctuation belongs to a named secret, and
  length never did.** Two expressions, `genericSecretQuotedRe` and
  `genericSecretBareRe`. A quoted value ends where its quote does, so the
  punctuation inside is the value's own — `password="hunter2)"` ends on a bracket,
  `"secret": "MyP@ssw0rd!"` on a bang, and both are taken whole. An unquoted value
  ends where the text resumes, so the punctuation is that text: `[password=hunter2]`
  closes a bracket somebody opened. This replaced a rule that decided on length — the
  run minus its tail at a floor of eight, falling back to the raw run when trimming
  would drop under it — and the two halves then **disagreed with each other**:
  `MyP@ssw0rd!` at eleven characters had its bang left in clear while `hunter2)` at
  eight kept its bracket, one value leaking its last character and the other eating
  the syntax around it, decided by nothing but how long the password was. Eating it
  is the worse half, because this agent is read by a model reviewing source code and
  code with a delimiter removed is code it analyses wrongly, silently, and reports on
  as the caller's. **The bare expression deliberately has no `['"]?` before its
  group**: RE2 has no lookbehind, so that absence is what keeps it off a quoted value
  — after the separator the group must start on a non-quote, and `\s*` cannot step
  over the opening quote. Four corpus cases hold the rule and none is meaningful
  alone.
- **A keyword may fall anywhere in the name, and need not be spelled exactly.** The
  separator had to follow the keyword immediately, so a prefix was free and a suffix
  was fatal: `VERY_SECRET=` was masked while `VERY_SECRET_TOO=`,
  `SUPER_SECRET_VALUE=`, `accessTokenValue=` and `STRIPE_SECRET_KEY=` went out in
  clear — the last of them the ordinary way to name a Stripe or an AWS key, missed by
  nothing but where the word fell in the name. `genericSecretName` reads the rest of
  the name and it must **open on a new word**, or it runs on through `secretary_id`
  and `tokenised_at`, whose values a credential would then claim ahead of the
  category that owns them. `genericSecretFiller` tolerates the two ways a name
  carries a keyword without spelling it: a **repeated letter**
  (`SUPER_SEECRET_VALUE`, a typo and the shape somebody reaches for to dodge a
  scanner) and **one separator** between letters (`S_E_C_R_E_T`) — which is why
  `genericSecretKeywordNames` carries no separators, `APIKEY` covering `API_KEY`,
  `api-key` and `apikey` at once. **Insertions only, never omissions**: a missing
  letter would put `TKN` and `SCRT` in the list, and those are initialisms.
- **What opens a new word depends on the case the name is written in, and reading a
  capital as a boundary unconditionally made the guard a no-op.** Every letter of a
  SCREAMING_SNAKE name is a capital, so the tail opened on the `A` of `SECRETARY` and
  `SECRETARY_ID=`, `TOKENISED_AT=` and `SESSION_IDLE_TIMEOUT=` were masked while
  their lowercase twins were clean — one rule, two answers, decided by nothing but
  the case somebody typed. `SECRETARIAT_EMAIL=bureau@example.fr` is what it cost: a
  credential wins every overlap, so the address was replaced by `[SECRET_1]` rather
  than by a stand-in address. So a **separator** always opens a word, and a
  **capital** only where the keyword's own last letter is lowercase, which is what a
  camelCase join is — `accessTokenValue` yes, `SECRETARY_ID` no. A **plural goes with
  the keyword** and then ends the word itself: `API_TOKENS=` and `accessTokensValue=`
  are both names, and the first of them was a corpus case that the case rule alone
  would have dropped.
- **The tolerance is one optional class per gap, not `+` on every letter, and the
  difference is the credential scan's dominant cost.** Go's regexp is an NFA
  simulation with no DFA behind it, so a scan costs what the program has states:
  `S+E+C+R+E+T+` across twenty-three branches took the three expressions built from
  it to 38ms each over 88KB — 72% of the whole secret catalogue, against 0.56ms for
  the entire second vendor tier the same commit hand-optimised. As a filler class the
  same three cost 26ms and match the same names. What is given up is a letter
  repeated *twice*, which is neither a typo nor a shape anybody writes.
- **It is deliberately not a sub-sequence match**, which is the obvious reading of that
  shape. Measured against random base64: a sixty-character blob carries one of these
  keywords as a sub-sequence 7% of the time, an eighty-character one 20%, and at a
  hundred and eighty-two characters — the `WARP_READ_TOKEN` above — 94%. A free
  sub-sequence makes every long hash and integrity field in a lockfile a *name*, and it
  is then whatever follows it that gets masked. Bounded to repeats and separators the
  same measure is 0.00% at every length: a generated blob has no separators and its
  letters do not queue up.
- **A leading `*` or `&` is stripped, not refused.** `want.SecretLevel = *secretLevel`,
  a line in this agent's own mask command, was claimed the moment a keyword stopped
  having to end the name: a star is not an identifier character, so the digit rule
  never looked at the name behind it. Refusing outright would drop
  `PASSWORD=*Hunter2*`, a real password — stripping hands the rest to the other rules,
  so it is dropped only when what it points at is *also* code-shaped by them, and
  `*secret123` keeps its digit.
- **The scheme in an `Authorization` header is the evidence, and nothing read it.**
  `Authorization: Bearer <token>` is how a credential travels in a log line, a curl
  paste, a header dump and every API page ever written, and the whole of it went to
  the model in clear — the name is not a field name and no `NAME=value` shape
  reaches it. `authHeaderRe` anchors on the header and takes what follows `Bearer`,
  `Basic` or `token`; `Digest` is deliberately absent, because its value is a
  parameter list and masking it whole would replace the realm and the nonce with
  the response. It reports **`SECRET_GENERIC` on purpose**, which hands it
  `GenericSecretCheck`: that guard is worth more than the precision a category of
  its own would buy, because "Bearer authentication" in a sentence has the shape of
  a header and the digitless-identifier rule is exactly what refuses it.
- **A name is evidence in an element as well as in an assignment**
  (`xmlSecretRe`). `<apiKey>…</apiKey>` is where every Spring, .NET and Maven
  configuration keeps its credentials, and none of them was read. It is a pattern
  of its own rather than `>` added to the separator, because the value has to stop
  where the closing tag opens: under the shared bare expression the span ran on
  into `</apiKey` and the mask ate the tag. **The closing `</` is the guard** — it
  is what keeps this off `if (secret > threshold)`, which has the name, the
  separator and a value of the right size and no tag anywhere after it.
- **`=>` and `->` are separators too, and they come first in the alternation.**
  `'token' => 'ory_pat_…'` is a PHP or Perl hash and `api_key -> value` is a pasted
  note; both were forwarded for want of two characters. Go's regexp is
  leftmost-first, so with `[=:]` in front `=>` matched on its `=` and the value
  began on the `>`. What this risks claiming is a dereference, and
  `GenericSecretCheck` is what refuses it: `$secret->getValue()` ends on a call,
  `$token->id` is under the value floor, `$password->hashedValue` is camelCase without a digit.
- **A quoted value may be padded, and the padding is not the value.** `quoteChars`
  holds `\s`, so one space behind the opening quote ended the expression before it
  began: `API_KEY=" hunter2-correct-horse "` went out in clear while the same line
  without the spaces was masked. Horizontal whitespace only, for the reason every
  other span here uses it — with `\s` the quote could sit on one line and the value
  on the next.
- **`CREDENTIAL` is a keyword and `KEY` is not.** A bare key is what half the
  configuration languages there are call the left-hand side of a pair — `key: value`
  in YAML, `key=` in an INI section — so it names a credential no more often than it
  names nothing at all, and the value behind it is whatever the document happened to
  hold. What `CREDENTIAL` costs is a path: `GOOGLE_APPLICATION_CREDENTIALS=/etc/gcp/key.json`
  is masked, because a path is not identifier-shaped and the check lets it through.
  That is the cost `SECRET_FILE=` already carried, it is over-masking rather than a
  leak, and it is reversible.
- **Where shape runs out, the keyword decides.** `reset-password` behind `password:`
  has the *same shape* as `troisieme-valeur-longue`, the corpus's own credential —
  lowercase words joined by hyphens, both of them — so no rule about form could
  separate them. What does: **a passphrase does not name the thing it unlocks.** A
  lowercase slug carrying `password`, `secret`, `token` or `api_key` is a route name.
  Lowercase and hyphens only, which is what keeps `MyPassword123!` a credential.
- **Narrowing a credential pattern is the change that leaks, so what must still be
  caught is asserted beside what must not.**
  `TestGenericSecretCheckRejectsSourceCode` and
  `TestGenericSecretCheckKeepsCredentials` are one pair, and neither is meaningful
  alone.
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
- **A prefix pattern is free only while Go can scan for its leading literal, and
  three habits destroy that.** A leading `\b`, a `(?i)` in front of the prefix, and
  an alternation of prefixes. Importing the second tier of vendor credentials
  (`vendorPrefixes`, 109 vendors, 150 patterns) measured all three over
  `docs/testCorpus.txt`: 62ms as written, 28ms without the `\b`, 17ms without the
  `(?i)` as well, and 0.56ms once each alternation became one pattern per branch —
  `(?:EAAA|sq0atp-)` alone cost 465us where the two split patterns cost 6.8us and
  6.6us together. The `(?i)` is also simply wrong: Figma issues `figd_`, never
  `FIGD_`. **Two patterns of one category is the shape to reach for**, as Slack's
  already were, and `Label` is what makes a report say which fired.
- **A trailing group that consumes a character makes the span eat the sentence.**
  An imported rule closes on `(?:[^\w-]|$)` because RE2 has no free lookahead, and
  five arrived that way: `pscale_pw_…Dc6.` came out a character long and Fly.io's
  took the following comma. The fix is `genericSecretBareRe`'s own shape — a
  permissive body one shorter, then a final class excluding `noSentenceTail`. Fly.io
  carried the other classic in the same line, `\s` where a hundred-character span
  needs `[ \t]`.
- **Where one vendor prefix ends on another, the fix is an equal score, not a
  boundary.** Cerebras issues `csk-<48>`; `openAILegacyRe` is `sk-<20,>` with no left
  boundary, so it took the key from offset 1 and masked it as an OpenAI key with the
  leading `c` in clear. Arbitration is credential, then confidence, then the longer
  span — so **equal** scores hand the decision to the span and the longer prefix
  wins. `CatAnthropicKey` and `CatOpenAIKey` were already both 98 for this exact
  reason (`sk-ant-` contains `sk-`); the whole second tier is 98 so it holds for every
  containment in it (`ops_eyJ` over `eyJ`, `mercury_production_` over `ion_`). A left
  boundary would be direct and is unaffordable: it took `openAILegacyRe` from 6us to
  654us. A `TODO` records what still leaks — a run shorter than a vendor's floor.
- **Where a vendor prefix is short enough to occur inside a hash, the length is the
  whole of the evidence and both ends have to be bounded.** ClickHouse issues
  `4b1d<38>`, and four hex characters occur inside any long hash: `sha512-4b1d0123…`
  in a lockfile had forty-two characters cut out of the middle of it and masked, so a
  pasted `package-lock.json` came back with fragments of its integrity fields
  replaced by tokens. Nothing else claims that span, so no equal-score tie-break ever
  runs — the arbitration above cannot save this class. RE2 has no lookaround, so both
  boundaries are consumed and `Group` points at the value, which is why this one rule
  sits outside `vendorPrefixes`: that table has no `Group`. The leading-literal scan
  is what it costs, knowingly.
- **Every alternation of prefixes in the second tier is split, and two were not.**
  Buildkite spelled seven prefixes in one expression and Sourcegraph two, three lines
  above the comment that says the rule and measures it. A comment documenting
  something the table does not do is worse than no comment.
- **An XML attribute pair has two orders and both are written.** `nugetPasswordRe`
  read `key` first and matched `Password` case-sensitively, so a `NuGet.config` with
  a private feed in it — the file somebody pastes whole to ask why a restore fails —
  leaked its password whenever the writer had put `value` first or spelled the
  attribute in lower case. Two patterns rather than one alternation, because `Group`
  is a single index and the value sits in a different place in each.
- **Only the value-only half of an imported catalogue is worth taking.** Around 40%
  of gitleaks' rules name a credential by the field beside it, which
  `genericSecretQuotedRe` already reads; a second reading competes for the same span
  with no more evidence. And 28 of the rest were left out for being *identifiers* —
  a tenant id, a client id, a storage account name, an instance hostname — which are
  also, not coincidentally, the 28 with no literal prefix and 16.6ms of the cost.
- **A payload that carries the catalogue grows with the catalogue, and something
  downstream is reading it under a bound.** `Query` read `/healthz` under 8KiB; the
  second tier took the body to 10.7KiB, the read truncated, and the parse — quiet by
  design, "a body that will not parse still means something answered" — left every
  field empty. `neverseen status` printed nothing, the icon drew nothing and the exit
  code said something was wrong, over an agent answering perfectly.
  `TestHealthPayloadFitsTheQueryBound` fails at *half* the bound, so the batch that
  would break it is the one that still gets to choose the number.

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
