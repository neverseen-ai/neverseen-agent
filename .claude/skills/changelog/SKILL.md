---
name: changelog
description: Keep CHANGELOG.md in step with the commits — add what landed since the last tag under Unreleased, backfill a section per tag when the file does not exist yet, and turn Unreleased into a dated version when a release is being prepared. Use when asked to update, write or check the changelog, or to prepare release notes. Triggers on "mets à jour le changelog", "génère le changelog", "prépare les notes de version", "update the changelog", "what changed since the last release".
---

# Keeping the changelog

`CHANGELOG.md`, at the root, is written for somebody who runs the agent and wants
to know whether to upgrade — not for somebody reading the history. The commits are
the source; the changelog is a selection of them, reworded. It follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/): newest first, one
`## [Unreleased]` section on top, then one `## [x.y.z] - YYYY-MM-DD` per tag.

Every entry ends with the short hash of each commit it was written from:

```markdown
## [Unreleased]

### Security

- Another account on the same machine can no longer read a restored answer (563650d)

### Fixed

- A restart on Windows restarts the menu bar icon too, as on macOS (876fcd0)
```

The hash is what makes an update repeatable: a commit whose hash is already in the
file has been dealt with, and an entry whose hash no longer exists was written from
a commit since amended or rebased away.

## Updating Unreleased

```bash
last=$(git describe --tags --abbrev=0)
git log --reverse --format='%h %s%n%b%n--' "$last"..HEAD
```

1. Read `CHANGELOG.md`. For each commit in the range, skip it if its hash is
   already there. **Compare by prefix, not by equality**: git lengthens `%h` as the
   repository grows, and a seven-character hash in the file is the same commit as
   the eight-character one in the log.
2. For each entry in `## [Unreleased]` none of whose hashes is in the range any
   more, ask which of two things happened — they call for opposite answers:

   ```bash
   git cat-file -e "$hash^{commit}"                      # fails: the commit is gone
   git tag --contains "$hash" --sort=creatordate | head -1   # prints a tag: it was released
   ```

   - **Released**: a tag was cut without this skill. Move the entry, untouched,
     under `## [x.y.z] - <the tag's date>` for the tag printed, creating the
     section if it is missing. Removing it would delete the notes of a release.
   - **Gone**: the commit was amended or rebased away. If a commit in the range
     has the same subject, it is the same work under a new hash — replace the hash
     and keep the wording. Only when nothing matches is the entry removed.

   Say in the report which entries moved, which were re-attached and which went.
3. Add the commits that remain, by the rules below.
4. **Leave every existing entry's wording alone.** Somebody may have rewritten it
   by hand, and the hash is the only part that is derived.

Report what was added, what was left out and why, in a few lines. Do not commit:
the changelog goes in with the next commit the user makes, or on its own as
`docs(changelog): …` if they ask.

**Asked only what changed** — "what changed since the last release" — answer from
the same commands and the same rules, and write nothing. A question is not a
request to edit a file.

## Which commits become entries

| Commit | Section |
|---|---|
| `feat` | **Added** — or **Changed** when it alters something that already existed |
| `fix` | **Fixed** — or **Security**, see below |
| `perf`, a `refactor` with an effect a user can observe | **Changed** |
| something taken away | **Removed** |
| `test`, `chore`, `ci`, `docs`, `style`, any other `refactor` | none |

Sections appear in this order, and only when they have an entry: Security, Added,
Changed, Removed, Fixed.

**The section follows the effect, not the prefix.** The table is where to start,
and the commit body is what decides: the prefix names the commit's main subject
and says nothing about the others.

- **Anything is Security when a value could go out in clear, come back to the
  wrong reader, or a credential could be reached** — a missed pattern, a route
  answering a caller it should refuse, a key in a response — whether the commit
  is a `fix` or a `feat` that closed the hole on its way. That is the reason this
  agent exists, so it is the section a reader looks at first. When in doubt read
  the commit body: it names the failure.
- **One commit can be several entries**, each carrying its hash. A commit here
  often lands a feature and closes a leak in one go (563650d: a new settings page,
  and other accounts shut out); folded into one entry, the half a reader upgrades
  for is the half that disappears.
- **A `!` after the type, or `BREAKING CHANGE` in the body, opens the entry with
  `**Breaking:**`** and says what somebody must do before upgrading.
- **Several commits on one subject are one entry**, carrying every hash. A feature
  and the three fixes that followed it before any release are the feature.
- **A commit fixing something introduced since the last tag is no entry at all**:
  no release ever had the fault. Its hash goes on the entry of what it repaired.

## Writing an entry

- One sentence, in English, British spelling, no trailing full stop.
- Say what changes for the person running the agent, not what changed in the code.
  No function, file or test names; a command, a flag, a route or a setting is fine,
  since those are what they type.
- Drop the Conventional Commits prefix and scope.
- **Never describe the licence as open source**, and never mention the supervision
  backend's internals or any other private repository. The wording that is safe is
  in `CLAUDE.md`.

## Creating the file

When `CHANGELOG.md` does not exist, backfill one section per tag, oldest range
first, then Unreleased:

```bash
git tag --sort=creatordate                       # the versions, in order
git for-each-ref --format='%(creatordate:short)' refs/tags/v0.2.0   # a section's date
git log --reverse --format='%h %s%n%b%n--' v0.1.2..v0.2.0
```

A section is dated by its tag, not by the commit the tag names: `git log --format=%ad`
gives the day the commit was written, and v0.1.0 was tagged the day after. The
first tag's range is everything up to it (`git log v0.1.0`). Open the file
with a title and two lines saying what it is and that it follows Keep a Changelog.

## Preparing a release

Only when asked, and given — or after proposing — a version. Before 1.0: a
**Breaking** entry or an **Added** one is a minor, anything else a patch.

1. **Refuse if `git status --short` prints anything**, and say what is pending. A
   tag names a commit; notes written over uncommitted work describe a release
   nobody published.
2. Update Unreleased as above.
3. Rename `## [Unreleased]` to `## [x.y.z] - <today>` and put an empty
   `## [Unreleased]` back above it.
4. **The changelog is committed before the tag** — `docs(changelog): x.y.z`, on
   the user's word. Step 3 has just made the tree dirty again: tagged now, the
   release would not contain the section that describes it. Say so in the report,
   as the one thing left to do before tagging.

Stop there. Tagging and pushing are the user's.

The notes on the GitHub release are not this file: goreleaser writes them from the
commit subjects (`changelog:` in `.goreleaser.yml`, which leaves out only `test`
and `chore`), so they list `docs`, `ci` and `refactor` commits this file does not.
Known and left as it is — pointing goreleaser at this file means changing the
release workflow.
