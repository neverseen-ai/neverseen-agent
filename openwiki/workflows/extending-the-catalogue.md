# Extending: a category, a locale, a provider, a contract field

Each of these has a short, fixed procedure, and in each case **a test fails on the commit
that forgets a step**. That is the design: the alternative is a four-places-to-forget
problem, which this repo has already had.

## Adding a PII category

1. **One entry in `categoryRegistry`** (`pkg/pii/category.go`) carrying the token prefix,
   the score, the checksum if there is one, whether it is a credential, **its group and its
   label**, and `NoisyInCode` where source code satisfies the shape (a date does), so a value
   found inside code is not reported — never on a credential, and its entry says why. `validateCatalogue` panics at package
   initialisation on a prefix that does not make a token, a score outside 1-100, a missing
   group or label, a group that is not registered, and a label another category already
   uses. It cannot check the checksum or the credential flag — nothing in the entry says
   whether one was meant — so those two are what the corpus case is for.
2. **Its pattern in the right set** — `patterns_fr.go`, `patterns_gb.go`, `patterns_us.go`,
   `patterns_intl.go` or `patterns_secret.go`. Mind the ordering rule: the first pattern to
   claim a literal wins, so a specific shape must precede a broader one.
3. **A line in `pkg/pii/sample.go`**, carrying every notation the pattern accepts.
4. **A corpus case** — including at least one that must come out **untouched**.
5. **`make score-update`**, and explain the delta in the PR body.

That is the whole of it. `validateCatalogue` panics at package initialisation if a pattern
emits a category with no entry, so the category cannot be masked under a nameless token.

**What fails if you skip a step:** `TestScoreCorpus` (a category with no corpus case scores
100% on an empty tally, so the test derives what it demands from the catalogue and fails),
`TestSampleExercisesEveryCategory`, `TestCategoryPrefixesAreUnique`, `TestLiveCatalogueIsSound`.

**If it needs a checksum**, put it in `pkg/pii/checksum.go` and point `CategoryInfo.Verify`
at it, with its own table test. A value that fails a `Verify` is dropped outright, not scored
down — see [Detection engine](../architecture/detection-engine.md#a-checksum-lets-a-shape-be-loose-without-one-the-shape-is-all-there-is).

**If it should have a stand-in** for `fake` mode, add a generator in `pkg/pii/generators.go`.
Stand-ins must be implausible: reserved ranges, and they must **fail their own checksum**
(`TestStandInsFailTheirOwnChecksum`, `TestStandInsUseReservedRanges`). **A credential never
gets one** — `Detector.render` takes the token fallback for every secret category by design,
and that is the guarantee the response path depends on
(`TestCredentialsHaveNoStandIn`, `TestFakeMode`).

## Adding a locale

1. **One entry in `localeRegistry`** (`pkg/pii/locale.go:65`) — a code, a `Priority`, a
   pattern set, a sample and a fakes table.
2. **`patterns_<code>.go`**.
3. **A corpus suite** declaring `locale: <code>`.
4. **A block in `.env.example`**.
5. **`make score-update`**.

**Three tests fail on the commit that forgets any of those**:
`TestEveryLocaleHasACorpusSuite`, `TestEveryLocaleIsDocumented`, `TestScoreCorpus`.

**`Priority` is load order, and load order is a decision.** It settles which country claims a
value two of them could both read: nine bare digits are a French SIREN under Luhn and a US
routing number under the ABA weights, and the earlier locale in the registry names it.

**Also update the sample.** `pkg/pii/sample.go` needs a section carrying every category the
new set detects **and every notation each pattern accepts** — every day-first date form, both
ISO separators, an identifier compact and spaced, an address with and without its town. It is
what an operator reads to check their own data shape is covered, so a gap in it reads as a gap
in the engine.

**Next locales, in order of market size: Germany, Spain, Italy, the Netherlands** (the `TODO`
on `localeRegistry` records it).

## Adding a provider

1. **An entry in `DefaultProviders`** (`internal/proxy/provider.go:34`) — a code and a base
   URL. The code must not collide with `reservedRoutes` (`healthz`, `test`, `policy`, `mask`,
   `unmask`); `New` refuses it if it does (`TestReservedRoutesCannotBeProviders`). The
   block in `.env.example` names every known code and counts them, so it changes in the
   same commit.
2. **Only add it to `shellTools`** (`internal/proxy/shellenv.go:51`) **if you can verify the
   pair.** A guessed variable name is an instruction that does nothing, and a guessed command
   name is worse — it fails with "command not found" after somebody has already pasted it and
   believed it. That is why only two providers are in the table and the other six stay as
   comments in `cloakfleet env`.
3. **If the tool does not simply honour the variable, the `Caveat` field is where that goes** —
   beside the pair it qualifies, not in whichever surface was written last. `cloakfleet env`,
   the audit console and the menu bar all hand over the caveat because they all read this
   table.

## Adding a group

One entry in `groupRegistry` (`pkg/pii/group.go`) with a label and an `Order`, and at least
one category pointing at it. `TestEveryCategoryIsInAGroupAndEveryGroupIsUsed` fails on a
group nothing points at — a heading a menu would draw empty, which is the
documentation-ahead-of-the-code failure in another form — and on a grouping that does not
account for the whole catalogue between its groups.

`Order` is display order, and it is a decision: the first entries are the ones somebody
opened the menu for. `TestGroupsAreInDisplayOrder` pins it, so a reordering is deliberate.

Mind the menu's ceilings if a group is large: `maxGroupEntries` (8) and
`maxCategoryEntries` (12) in `internal/tray/systray.go` bound the pools, because the toolkit
builds a menu once and cannot add an entry later. Past them the entries say how many are
missing rather than dropping them quietly.

## Adding a field to the supervision contract

1. **Read `../cloakfleet-cloud` first.** The backend imports `pkg/telemetry`; this repo must
   never import the backend, and must compile and run with no backend at all.
2. **Add the field to `pkg/telemetry/contract.go`.**
3. **If it is a string, add it to `allowedStrings` with its reason** — otherwise
   `TestHeartbeatCarriesNoContent` fails, which is the point. Do not add one to make a
   dashboard nicer.
4. **Regenerate the golden in the same commit**: `go test ./pkg/telemetry/ -update-golden`.
   The example must **exercise** the new field — an `omitempty` field left out of the example
   is a field neither repository ever tested on the wire, which is exactly the drift the
   shared golden exists to catch. Use documentation ranges (RFC 5737, RFC 3849) so nothing in
   the example is a real machine.

## Adding a configuration setting

1. **One constant, in the package that owns the setting** — `internal/detector/config.go` for
   detection, `internal/proxy/env.go` for the proxy. **Never read an environment variable in
   `cmd/`**; a command that needs to differ from another passes `proxy.Options` instead, which
   is how `-a` and `-v` reach the pipeline.
2. **Document it in `.env.example`.** `TestDocumentedEnvironmentMatchesTheCode` fails in both
   directions — a variable the code reads and the file does not mention, and one the file
   documents and no code reads.
3. **`printUsage` builds the usage text from the constants**, so nothing has to be written out
   by hand there; `TestUsageNamesEverySetting` holds it.

## Before you propose a second `main`

The bar is `cmd/cloakfleet-tray`'s: **assembles no pipeline, holds no secret, and pays a cost
the agent would otherwise carry** (a Cocoa dependency that would break `CGO_ENABLED=0` builds
of the masking agent and tie its release to a GUI toolkit's). See
[Distribution](../operations/distribution.md#the-menu-bar-binary).

## Lessons not to reimport

- **Nothing enters the tree unexercised.** Agent Veil carried two fully implemented, tested
  packages that nothing imported, while its README advertised both. A feature that has never
  run is not a feature.
- **No documentation ahead of the code.** It documented five environment variables nothing
  read.
- **CI must trigger on the real default branch.** Its workflow was keyed to `main` while the
  default branch was `master`, so it never ran.
- **`version` is a `var`, not a `const`** — declared `const`, the `-ldflags` stamp is silently
  inert and every release reports the same string.

## See also

- [Testing and accuracy](testing-and-accuracy.md) — the gates in detail.
- [Detection engine](../architecture/detection-engine.md) — the pattern-writing rules and the
  leak behind each one.
