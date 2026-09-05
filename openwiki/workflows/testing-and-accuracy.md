# Testing and accuracy

Regular expressions are easy to write and easy to break silently, so the engine is
**measured rather than trusted**. Everything below is a gate that fails a build, not a
report somebody is expected to read.

## The gates

```bash
make test          # go test -race ./...
make test-cover    # + coverage; the CI gate is 80%
make lint          # golangci-lint (British spelling is configured here)
make score         # per-category precision/recall against the committed floor
make bench-accuracy
make e2e-claude    # real CLI, real provider (opt-in, spends quota)
make extension     # typecheck, test and build the browser extension
make extension-e2e # the extension through a real Chrome against a real agent
make contract-update  # re-record extension/testdata/contract.json — explain the delta
```

CI (`.github/workflows/ci.yml`) runs, on the **real default branch** — Agent Veil's workflow
was keyed to `main` while the default branch was `master`, so it never ran once:

1. `gofmt` verification and a `go mod tidy` check,
2. `make build`,
3. the race suite with coverage, gated at **80%**,
4. `golangci-lint`,
5. `goreleaser check` plus a snapshot build,
6. a portability check on `install.sh`,
7. the extension's own typecheck, tests and build,
8. the **contract** job: the agent's side of the recorded exchanges, the extension's side,
   and `git diff --exit-code` on `extension/testdata/contract.json` — the one job that fails
   when neither side is broken on its own,
9. the extension end to end, in a real Chrome against a real agent.

## The corpus, not unit tests

`internal/detector/testdata/corpus/` holds cases written **as prose** — a sentence with an
identifier in it, the way it arrives in a support thread — each declaring exactly which spans
must be found. Suites: `fr.yaml`, `gb.yaml`, `us.yaml`, `intl.yaml`, `secrets.yaml`,
`hard-fr.yaml`, `precision-fr.yaml`, `boundaries.yaml`.

A suite declares its `locale`, its `min_precision` / `min_recall`, and a list of cases:

```yaml
locale: fr
min_precision: 100
min_recall: 100
cases:
  - id: fr-nir-inline
    text: "Le dossier de Mme Martin, NIR 2 69 05 49 588 157 80, est complet."
    expect:
      - {category: NIR, value: "2 69 05 49 588 157 80"}
```

Identifiers in the corpus carry **real checksums** — a fabricated NIR or SIRET would be
rejected by the very validation the suite exercises.

**Add cases that must come out untouched, not just cases that must be found.** Recall alone
cannot fail a pattern: one widened to match everything scores 100% recall and passes. The
negative cases — a version string, an order number, a commit SHA, the placeholder numbers
that fill documentation — carry `expect: []` and are what notice.

The corpus and the score floor are **inherited from Agent Veil** (MIT, see `NOTICE`) and used
here as an executable specification: the engine is an independent implementation, and the
corpus is what holds it to the same behaviour.

## The per-category score floor

`internal/detector/testdata/score-baseline.json` commits **counts, not percentages** — true
positives, false positives, misses — for every category the catalogue can emit, plus a
`negatives` count. `make score` (`TestScoreCorpus`, `score_test.go:68`) fails if any of them
slips. `make score-update` rewrites it; **explain the delta in the PR body**. An `fn` above
zero records a miss accepted today, so a baseline is a floor to ratchet, never a goal.

Two things it defends that a naive gate would not:

- **An empty tally scores 100% precision and 100% recall by construction**, so a category
  nothing measures is indistinguishable from a perfect one. `TestScoreCorpus` derives what it
  demands from the catalogue itself (`patternedCategories`), so **a category with no corpus
  case fails the run**.
- **The `negatives` floor is the other half of the gate.** A category's floor is carried by
  its `tp`, and a precision case contributes none: deleting every `expect: []` case leaves
  `tp` unchanged, `fp` at zero and the run green, while the catalogue's false-positive rate
  stops being measured entirely. So the count of negative cases is itself a floor.

Suites are counted together because a category belongs to a **locale, not to a file**: the
NIR is only reachable with `fr` on.

## Structural tests — the ones that fail on a forgotten step

| Test | What it refuses to let you forget |
| --- | --- |
| `validateCatalogue` (init, `pkg/pii/category.go:526`) | a pattern emitting a category with no registry entry — panics at package initialisation rather than masking a value under a nameless token |
| `TestCategoryPrefixesAreUnique` | two categories that would produce the same token prefix |
| `TestLocaleRegistryIsComplete`, `TestLocalesAreSortedByPriority`, `TestAllPatternsCoversEveryLocale` | a locale entry that is half-added |
| `TestEveryLocaleHasACorpusSuite`, `TestEveryLocaleIsDocumented` | a locale with no corpus suite, or no block in `.env.example` |
| `TestSampleExercisesEveryCategory` + the four notation tests | a catalogue change that leaves `pkg/pii/sample.go` behind |
| `TestDocumentedEnvironmentMatchesTheCode` | a variable documented and unread, **or** read and undocumented |
| `TestUsageNamesEverySetting` | usage text drifting from the code — by a hand-kept list, so a new setting goes into the test too |
| `TestHeartbeatCarriesNoContent` | a new string field on the supervision contract |
| `TestHeartbeatWireFormat` | the shared golden not regenerated with a contract change |
| `TestReservedRoutesCannotBeProviders` | a provider taking a route the agent answers itself (`/healthz`, `/test`, `/policy`, `/mask`, `/unmask`) |
| `TestPipelineParityAcrossProviders` | one provider's path behaving differently from another's |

## The end-to-end test

`make e2e-claude` (`TestE2EClaudeCode`, `internal/proxy/e2e_claude_test.go`) is the one that
proves the product rather than its parts. It runs the Claude CLI against Anthropic **through
the agent**, with a tap in between recording every byte that went upstream, and asserts both
halves of the claim: that no value the caller wrote reached the provider, and that the caller
got it back anyway.

It needs the CLI signed in and it spends the operator's quota, which is why it is opt-in
behind `CLOAKFLEET_E2E_CLAUDE=1`.

## Conventions in the tests themselves

- Test names are sentences about behaviour (`TestAnAgentWithNoLocaleIsNotMasking`,
  `TestClosingABucketClearsTheLiveEntry`), because the name is what a reader sees when it
  fails.
- Several tests deliberately assert on **files or wire bytes** rather than in-memory state —
  `TestClosingABucketClearsTheLiveEntry` reads the buffer files, because loading turns a
  `live` entry into an ordinary bucket and is the one view in which the distinction no longer
  exists.
- Expectation tables are **not derived from the implementation**. A derived expectation
  agrees with whatever the code does, including a form it silently stopped reading.

## See also

- [Extending the catalogue](extending-the-catalogue.md) — which of these gates fires for each
  kind of change.
- [Detection engine](../architecture/detection-engine.md) — what is being measured.
