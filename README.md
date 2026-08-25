# Cloakfleet

An agent that runs on each workstation and masks sensitive values before they
reach a language model.

It sits between the AI tools people actually use — Claude Code, Cursor, a CLI, a
script — and the model provider. On the way out it replaces personal data and
credentials with tokens; on the way back it puts the real values into the answer.
The model never sees the data, and the person using it never sees a placeholder.

**Source available, not open source.** Free for any use, including inside a
company, under the Functional Source License; you may not offer it as a
competing product or service. Every release becomes Apache 2.0 two years after
it ships. See [LICENSE.md](LICENSE.md).

## Status

Under construction. What works today is the detection engine and a `scan`
command over it. The proxy, the vault and the fleet telemetry are the next
phases — the plan is tracked outside this repository.

```console
$ echo "Call 020 7946 0958, NHS number 9434765919" | CLOAKFLEET_PII_LOCALE=gb cloakfleet scan
locales: gb

PHONE                    020 7946 0958
                           UK telephone number, bytes 5-18, confidence 90
NHS_NUMBER               9434765919
                           NHS number, bytes 31-41, confidence 95

2 value(s) would be masked:
  NHS_NUMBER               1
  PHONE                    1
```

## Build

Go 1.26 or later, no other dependencies for the build.

```bash
make build            # bin/cloakfleet
make test             # the whole suite, with the race detector
make lint             # golangci-lint
make score            # gate detection accuracy against the committed floor
make bench-accuracy   # the per-category accuracy report
```

## Configuration

Two environment variables, both documented in
[.env.example](.env.example) — which a test keeps honest, in both directions: a
variable the code reads and the file does not mention fails the build, and so
does one the file documents and no code reads.

`CLOAKFLEET_PII_LOCALE` selects which country's identifiers to look for: `fr`,
`gb`, `us`, `none`, or a comma-separated mix. Unset means none, which is
deliberate — scanning one country's data with another country's patterns is
worse than scanning none of it, and an operator who never set the variable has
not chosen that.

Some things are found whatever the locale says, because they mean the same
everywhere: email addresses, payment cards, IBANs (every issuing country), IP
addresses, ISO dates, and every credential — API keys for a dozen providers,
AWS keys, private keys, JWTs, connection strings carrying a password.

## How the detection engine is held to account

Regular expressions are easy to write and easy to break silently, so the engine
is measured rather than trusted.

**A corpus, not unit tests.** [`internal/detector/testdata/corpus/`](internal/detector/testdata/corpus/)
holds cases written as prose — a sentence with an identifier in it, the way it
arrives in a support thread — each declaring exactly which spans must be found.
Suites also carry cases that must come out **untouched**: a version string, an
order number, a commit SHA, the placeholder numbers that fill documentation. A
pattern widened until it matches everything scores perfect recall, and only
those cases notice.

**A per-category floor.** [`score-baseline.json`](internal/detector/testdata/score-baseline.json)
commits the counts — true positives, false positives, misses — for every
category the catalogue can emit. `make score` fails if any of them slips. The
floor exists because an empty tally scores 100% precision and 100% recall by
construction, so a category nothing measures is indistinguishable from a perfect
one. A category added with no corpus case behind it fails the build.

**Checksums decide.** A shape can be loose where a checksum backs it: the NHS
number, the IBAN, the French NIR, SIREN and SIRET, payment cards, the ABA
routing number. A value failing its checksum is not a weak match, it is not a
match — no setting can turn fifteen digits with a wrong key into a social
security number.

**The catalogue validates itself at startup.** Adding a category means one entry
in one registry. A pattern emitting a category nobody registered fails at
package initialisation rather than masking a value under a nameless token.

The corpus and the score floor are inherited from
[Agent Veil](https://github.com/vurakit/agentveil) (MIT, see [NOTICE](NOTICE))
and used here as an executable specification: the engine is an independent
implementation, and the corpus is what holds it to the same behaviour.

## Known gaps

Written down rather than discovered later:

- **Only `fr`, `gb` and `us` exist.** Germany, Spain, Italy and the Netherlands
  are next. Adding one is an entry in the locale registry plus a pattern file, a
  corpus suite and a regenerated floor — three tests fail on the commit that
  forgets any of them.
- **No UK street address, and no UK sort code.** A sort code's `12-34-56` shape
  is a date, so it needs a context word first.
- **No US driver's licence.** Fifty formats, no shared shape, no checksum.
- **The National Insurance number and the employer id carry no checksum.** Their
  letter rules and punctuation are all the evidence there is, so an internal
  reference of the same shape can be masked. The allow list is the escape hatch.
- **A date written as three spaced numbers is ambiguous** and "1 12 2019" is
  masked as one. Recorded in the corpus as an accepted miss rather than hidden.
- **Mixing locales costs precision** where two countries issue identifiers of
  the same length. Nine bare digits are a French SIREN under one checksum and a
  US routing number under another; the earlier locale in the registry names it.
