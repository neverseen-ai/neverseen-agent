# CLAUDE.md

Guidance for Claude Code working in this repository.

Cloakfleet is an agent installed on each workstation. It proxies the AI tools
people use to a model provider, masking personal data and credentials on the way
out and restoring them on the way back. Module path:
`github.com/cloakfleet/cloakfleet`. Licence: FSL-1.1-ALv2 (source available, not
open source — say "source available").

The paid supervision backend is a **separate, private repository**
(`cloakfleet-cloud`). It imports this one; this one must never import it, and
must compile and run with no backend at all.

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

**Overlap arbitration, in order**: a credential always wins, then confidence,
then the longer span, then the leftmost. Each rule is there because its absence
leaked. Position deciding on its own let a postcode evict the address containing
it; the credential rank exists because several ordinary categories score above a
connection string, so `postgres://admin:pw@db` resolved to an *email* match over
the password — reversible, and expanded back into a live secret on the way out.

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
