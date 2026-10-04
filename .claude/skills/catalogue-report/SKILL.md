---
name: catalogue-report
description: Generate an HTML report of everything the agent tries to mask — split into secrets and personal data, each category with a coverage score (how few shapes its value can take, so how far one set of regular expressions can be trusted to catch them all: IPv4 100%, postal address 40%), plus a country matrix scoring each locale and the EU group on its own patterns. Use when asked what the agent masks, how reliable each category is, which countries are covered, or for a catalogue summary to share. Triggers on "rapport du catalogue", "ce que l'agent masque", "indice de confiance", "matrice par pays", "catalogue report", "what does the agent mask".
---

# The catalogue report

`docs/catalogue.html`, committed: one self-contained HTML page, computed from the
patterns compiled into the working tree — every locale, whatever this machine has
loaded:

- **Secrets** and **Personal data** (`pii.IsSecret`), each category with its score
  and the facts the score came from. Categories at 100% are folded away under one
  line; what is left open is what the report exists to point at.
- **By country**: one row per locale, plus an `eu` row loading every European locale
  together, one column per category a locale contributes. Each cell is the score on
  that locale's patterns alone — one French plate is one notation, every European
  plate together is forty-two.

## Run it

```bash
go run ./.claude/skills/catalogue-report                     # rewrites docs/catalogue.html
go run ./.claude/skills/catalogue-report -o /tmp/other.html  # anywhere else, e.g. to compare two builds
```

From the repository root. Offer `open docs/catalogue.html` rather than describing the
page.

**The page carries no date, on purpose**: two runs over one catalogue write the same
bytes, so `git diff docs/catalogue.html` shows exactly what a change to `pkg/pii` did
to the scores — and nothing when it did nothing. Regenerate it in the same commit as
any change to `pkg/pii`; a stale page is a page that says the wrong thing about
coverage. Nothing checks this for you. It reads no configuration, no trace and no value: only the
catalogue in `pkg/pii`. The page holds no personal data and can be shared.

Say which tree it describes: `git log -1 --oneline` and `git status --short`.

## The score, and what it is not

A heuristic over the **structure of the expressions** — not a precision, not a recall
(`make bench-accuracy` measures those, on the corpus). It starts at 100 and loses
points for each way the number of shapes grows past what one expression covers; the
page prints the formula from the constants in `main.go`:

| Penalty | When |
|---|---|
| −15 shape only | no run of 3+ fixed characters in the value itself — or of 2 before a bounded value of 16+ characters — and no `Verify` |
| −25 free text / −10 open length | an unanchored value of open length that spans words / stays in one |
| −5 per form | per unanchored *form* past the first, capped at −30 — see below |
| −5 per country | per locale past the first, capped at −20 |
| −10 noisy in code | `pii.NoisyInCode` |

What took several attempts to get right, each pinned by a test in `main_test.go`:

- **Only the value counts.** A pattern with a `Group` matches context around it; the
  `password=` before a generic secret anchors the match, not the value.
- **An anchor is a run of fixed positions, not a literal** — `gh[pousr]_` and
  `e(?:yJ|wo)` (how the parser factors `eyJ|ewo`) hold no literal of three.
- **A space never anchors, and a digit only where the pattern wrote it**: `r8_` is
  Replicate's, `[01]` and `19|20` are a month and a century. An alternation is a
  choice of numbers only when a branch is *nothing but* digits — the `3` in AWS's
  `A3T` once took `AKIA|ASIA` down with it.
- **Two fixed characters anchor a value of bounded length**: `SK` then exactly 32
  hex digits is Twilio's, `00`, 13 characters and a `!` is Salesforce's. Before a
  value of open length, two characters stay a separator.
- **A value found only by the name in front of it stays "shape only"**: the AWS
  secret key is forty base64 characters, and written without its name it is not
  caught. Its 85% is the report being right, not wrong.
- **A negated class is not free text** — it reaches the end of Unicode and so holds a
  space, but it says "up to the delimiter".

**A form, not a notation.** A notation whose every value in the sample
(`pii.Sample`) has the signature of a value another notation already reads — letters
as `a`, digits as `9`, runs counted: an AWS secret is `a40` — is one more *context*
for that form, another way of finding it, and costs nothing. Counting notations, the
AWS secret read beside its key id dropped the category from 85 to 80: the report
scored the fix of a leak as a loss. `TestAContextForAFormAlreadyReadCostsNothing` and
`TestOneMoreFormStillCosts` are the pair. The page shows both counts (`2 (2
unanchored, 1 form)`). Forms are read off the sample, which the repository already
requires to carry every notation; a notation the sample never exercises counts as a
form of its own.

Known ceilings (a `TODO:` in `main.go`): a short wrong pattern scores as well as a
short right one, and a `Verify` that only filters (`GenericSecretCheck`, `DOBCheck`)
earns what a confirming checksum earns — which is why the generic secret sits at 70
rather than lower. `TestTheScoreSeparatesAFixedShapeFromFreeText` is the calibration:
IPv4 and a prefixed vendor key at 100, a postal address at 60 or below, the generic
secret below every other credential. A change to the formula that breaks it is
measuring something else.

## When the program stops compiling

It imports `pkg/pii` only. A refactor there can break it: fix the program — it is the
tool — and run its tests. `.claude/` is skipped by `./...`, so neither `make test`
nor CI runs them:

```bash
go test ./.claude/skills/catalogue-report
```

## Reporting

Give the counts the command printed, the categories below 100% — with the facts from
the **Why** column, not a paraphrase — and `git diff --stat docs/catalogue.html`: a
regenerated page that differs means the scores moved, and that is worth saying.
