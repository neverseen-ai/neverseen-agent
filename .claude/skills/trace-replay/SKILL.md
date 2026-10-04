---
name: trace-replay
description: Non-regression check over real traffic — replay the inbound body of each trace in traces/, newest first, through the agent as it is built now, and compare the body it would send with the body the trace recorded; stop once 20 traces come out identical. Then doubt every finding — the tool, the code, the configuration — and run checks on the code (replay with the old commit, scan, bisect, gates) to decide whether it needs a fix. Use when asked whether a change regressed masking, to replay traces, or to check that results are idempotent. Triggers on "rejoue les traces", "non-régression", "replay the traces", "idempotent", "did this change break masking".
---

# Replaying traces as a non-regression check

A trace (`traces/*.txt`, written by `neverseen proxy -v`) holds the body the tool
sent and the body the agent forwarded. Replaying the first through today's agent
must give back the second. When it does not, either the program changed on purpose
since the trace was written — or it regressed. **The replay finds the differences;
deciding which kind each one is is your job**, and it is the half that matters.

## Run it

```bash
go run ./.claude/skills/trace-replay                    # newest first, stop after 20 identical
go run ./.claude/skills/trace-replay -stop 50 -show 10  # more traces, more lines per trace
go run ./.claude/skills/trace-replay -trace traces/20260914T202124-0320-default-anthropic.txt
```

Run it from the repository root, in the background: a replay costs about two
seconds per megabyte of body, so twenty large traces take a few minutes. Exit status
1 when anything differs, 2 when the agent could not be assembled.

**It replays the working tree, not a commit.** Say which one you replayed:
`git log -1 --oneline` and `git status --short`.

Nothing leaves the machine and nothing is written: `http.DefaultTransport` is
replaced by a stub that records the forwarded body, the vault is in memory, and no
names file, control key or trace directory is opened. It does read
`~/.neverseen/.env` and `~/.neverseen/policy.json`, as the agent beside it would.

The program imports `internal/proxy`, so a refactor there can stop it compiling.
Fix the program — it is the tool — and run its tests:

```bash
go test ./.claude/skills/trace-replay
```

The test injects each fault the comparator must catch, beside the cases that must
stay silent. `.claude/` is skipped by `./...`, so neither `make test` nor
`make lint` runs it.

## How the comparison works, and why it is not a byte comparison

- **Each trace is replayed twice, each time with a fresh vault.** The two must be
  byte-identical, or the verdict is `UNSTABLE`: the agent is not deterministic, and
  every other comparison on that trace is noise.
- **Replacements are compared up to their numbering.** A trace's tokens continue its
  session's numbering (`[EMAIL_17]`) and a replay starts at 1. Two bodies are the same
  when every replacement maps **one-to-one**, across the whole document, onto a
  replacement of the same category, and the text around them is byte-identical.
- **A token the tool itself sent is text, not a replacement.** A conversation's
  history carries `[MONGOID_3]` echoed back by the model, and a fresh vault can mint
  that very string for another value. Identical on both sides, it matches without
  entering the renaming.
- **Line by line over the indented bodies.** A string never spans two lines once
  indented, so a masking change stays on its line, and a shape change shows up as a
  different line count.

## Verdicts

| Line | Meaning |
|---|---|
| `SAME` | Identical up to numbering. Counts towards `-stop`. |
| `SKIP` | Not a trace with both bodies. |
| `UNSTABLE` | Two replays of one body differ. **Always a regression.** |
| `DIFF … the agent forwarded nothing: 415 …` | Today's agent refuses a body it used to forward. |
| `the outbound document changed shape` | A key, its order or an array length moved. **Always a regression** unless a commit says the body's structure changed on purpose: the agent rewrites values, never the shape around them. |
| `text around the replacements changed` | The bytes between replacements differ: escaping, whitespace, a value cut short. **A regression** — it breaks the provider's cached prefix for every turn after it (CLAUDE.md, "The outbound body repeats the inbound one's prefix"). |
| `no longer masked  CAT  value` | The trace replaced this value and today's agent sends it in clear. **A leak, unless a commit narrowed that pattern on purpose** — a false positive fixed. |
| `newly masked  CAT  value` | Today's agent replaces a value the trace sent in clear. Usually a pattern added or widened. Check that it is not a false positive. |
| `category changed  A → B  value` | Arbitration or a category's prefix changed. |
| a renaming that does not hold | Reported as a plain differing line: one value given two names, or two values one name — the session mapping is broken. |
| `DOUBT …` under a trace | Identical or not, something this comparison cannot vouch for. See step 5 below. |
| `DOUBT … categories never exercised` | In the summary: the categories the identical traces never carried. |

## Doubt, then check the code

**The replay is evidence, not a verdict.** A difference can be the program's fault,
the configuration's, or this tool's own; an identical replay can be identical to
something that was already wrong. So every finding goes through the same three
steps — **state the doubt, run the check that settles it, conclude with the
evidence** — and a finding whose check was not run is reported as *uncertain*, never
as *fine*.

**Do not change the product code on the strength of a replay.** Write the test that
reproduces the problem, show it failing, and propose the fix. Changing it is the
user's call.

### 1. Doubt the tool first

It has already been wrong twice: a fresh vault numbering `[MONGOID_3]` against the
same string sent as text produced 22 false differences, and a detector shared
between the two replays made an older commit — whose numbering lived on the
detector — report `UNSTABLE` against itself. Before reporting a `DIFF` or an
`UNSTABLE`:

- Replay that trace alone (`-trace`) and read the line it names.
- Inject the fault by hand on a copy of the trace (put a value back in clear in its
  OUT section) and check the replay reports it as it should.
- `go test ./.claude/skills/trace-replay`. A comparator bug is fixed in the
  comparator, with a case in `main_test.go`, and the run is done again.

### 2. Date every difference and list what changed since

```bash
git log --since=2026-09-14T20:21:24Z --format='%h %ad %s' --date=iso -- pkg/pii internal/detector internal/proxy
git status --short
ls -l ~/.neverseen/policy.json ~/.neverseen/.env
```

The file name opens on the trace's UTC timestamp. A trace records nothing about the
policy it ran under: switched-off categories, loaded locales, secret level and
substitution mode can all have changed since.

### 3. Separate the code from the configuration — replay with the old code

The decisive check: build the program as it was when the trace was written and
replay the same trace with it, under the same configuration.

```bash
repo=$PWD scratch=$(mktemp -d)
old=$(git rev-list -1 --before=2026-09-14T20:21:24Z HEAD)
git worktree add -q "$scratch/old" "$old"
mkdir -p "$scratch/old/.claude/skills" && cp -R .claude/skills/trace-replay "$scratch/old/.claude/skills/"
(cd "$scratch/old" && go run ./.claude/skills/trace-replay -trace "$repo/traces/<file>")
git worktree remove --force "$scratch/old"
```

`$repo` is captured first: inside the subshell `$PWD` is already the worktree.

- **The old code reproduces the trace, today's does not**: the code changed.
  `git bisect run` between `$old` and `HEAD`, with that single-trace replay as the
  test, names the commit.
- **The old code does not reproduce it either**: the configuration changed, or the
  trace was made by a binary built from a tree that was never committed. Not a
  regression of the code — say which setting explains it, or that none was found.
- **The old code does not compile with this program** (an internal API moved): say
  so, and fall back to step 4 alone.

### 4. Name the hypotheses and run the checks that tell them apart

For each difference, write down **what would make it intended and what would make it
a regression**, then run what discriminates:

| Finding | Doubt to settle | Checks |
|---|---|---|
| `no longer masked` | A false positive fixed on purpose, or a leak? | `go run ./cmd/neverseen scan <file holding the value in its context>`; `git log -S '<pattern fragment>' -- pkg/pii` then `git show <commit>` — does the message say this shape was given up?; is the value's shape in `internal/detector/testdata/corpus`?; `make score` |
| `newly masked` | A pattern added on purpose, or a new false positive? | `scan` as above; is the value really personal data or a credential, read in its context?; the commit that added the pattern and its corpus negatives |
| `category changed` | An arbitration rule changed, or load order now decides? | `scan` shows every candidate; CLAUDE.md, "Overlap arbitration" and "Load order cannot decide a secret" |
| `text around the replacements changed`, `changed shape`, `UNSTABLE` | Is any change of this kind intended at all? | `go test ./internal/proxy/`; `git log` on `internal/proxy/jsonbody.go`; a cached-prefix break costs every following turn, so the bar for "intended" is a commit that says so |
| a renaming that does not hold | Broken session mapping, or the tool? | step 1; then `go test ./internal/vault/ ./internal/proxy/` |

**A gate that stays green over a confirmed regression is a second finding.** If
`make score` or `go test ./...` passes while the replay shows a value no longer
masked, the corpus lacks that case — and that gap needs fixing as much as the
pattern does.

### 5. Settle every DOUBT line, not only the differences

`DOUBT` lines are emitted on identical traces too — an identical replay can be
identical to something wrong. Each one gets its check:

- **`… was minted for a value (recorded)`**: it *happened* live — the agent named a
  value with a string the tool had already sent as text, and the answer's restoring
  cannot tell them apart. Read how the restore chooses (`internal/proxy/stream.go`,
  `unmask`) and write a test reproducing it.
- **`… (replay)`**: a session starting afresh would do it. Find out whether one can,
  with that history already in the body: after `Forget`, after a mode change, under a
  new session id, without `names.json`. Read `internal/vault` (`Name`, `next`,
  `Forget`) before concluding either way.
- **`likely fake mode`**: this comparison cannot read stand-ins. Say the trace was
  not compared, rather than reporting it identical.
- **`categories never exercised`**: an identical run vouches only for the categories
  its traces carried. Name the ones that matter for the change being checked — the
  categories the commits since the trace touched — and say they were **not**
  covered; widen with `-stop` or point at their corpus suites.

To reproduce something inside a package, a probe test goes in that package under an
obviously temporary name (`zz_probe_test.go`), is run, and is deleted before you
report — unless the user wants it kept as the regression test.

### 6. Group, then report

One change shows up in every trace that carries such a value: report each cause
once, with the traces it explains. Then a table:

| Finding | Traces | Hypotheses | Checks run → result | Verdict | Proposed action |
|---|---|---|---|---|---|

with a verdict from **intended**, **regression — fix needed**, **tool error** or
**uncertain** (and then what would settle it). Then the counts — replayed,
identical, differing, doubts, whether `-stop` was reached — and a last section,
**always present even when everything is identical**: *what this run does not
vouch for*.

A regression becomes a test before it becomes a fix: a corpus case for a value
(`openwiki/workflows/extending-the-catalogue.md`), a test beside `jsonbody.go` for
a shape or a byte.

## What this does not cover

- **The answer.** The stub answers with an empty message; restoring the provider's
  answer is not replayed. Use the `trace-check` skill for that half.
- **The request's headers and path.** A trace records neither, so every replay goes
  to `/<provider>/v1/messages` with `X-Session-Id` set from the trace's session.
- **What the catalogue never recognised.** A value missed in the trace and missed
  today is identical on both sides. `trace-check --values` is the pass for that.

The output prints values in clear, as the trace holds them. Keep it in the terminal:
never paste it into a commit, an issue or a document.
