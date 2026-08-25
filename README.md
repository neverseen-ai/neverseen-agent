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

The agent is complete: it masks, it restores, it installs as a service, and it
reports to a supervision backend when there is one. The backend itself is a
separate, private repository.

Run the agent and point a client at it by naming the provider in the path:

```console
$ CLOAKFLEET_PII_LOCALE=fr,gb,us cloakfleet proxy
level=INFO msg=listening address=127.0.0.1:8787 providers=anthropic,deepinfra,gemini,...

$ ANTHROPIC_BASE_URL=http://127.0.0.1:8787/anthropic claude -p "…"
```

Everything that reaches the model is masked, and everything that comes back is
restored — including through a streaming response, where a token the model
echoed is regularly split across two events.

### The test page

While the agent runs, `http://127.0.0.1:8787/test` shows one text three ways: as
written, masked with tokens, and masked with stand-ins — in this agent's own
locales and allow list.

It answers the two questions a log line cannot. Which substitution mode to run:
a model reasons better about prose than about brackets, but a stand-in in an
answer is a value nobody can check. And, more usefully day to day, whether the
catalogue reads *your* data: paste a real record with the values changed, and see
what would leave the machine and what would not. The page also unmasks its own
token column and tells you whether the text round-trips exactly.

Nothing on it is sent anywhere, stored, or written to the session vault.

Or scan a file from the shell, without starting anything:

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

## How the round trip works

1. A client sends a request to `/<provider>/…`. Whatever credential it sent is
   forwarded untouched — the agent holds no API keys, because the tool making the
   request already has one.
2. The body is decoded as JSON and every **string value** is masked. Not the raw
   bytes: a JSON string carries escapes, and a pattern reading the bytes sees the
   characters those escapes are made of.
3. What was replaced is recorded in a session vault, encrypted, for thirty
   minutes. One value keeps one token for as long as the conversation lives, so
   the model is told about one person rather than three.
4. The response is expanded back on the way out — buffered or streamed, through
   the same expansion.

Only bracket tokens are ever expanded. That filter is what stops a masked
credential from being turned back into a live secret, and it is why `fake` mode
is deliberately one-way.

## Install

```bash
./install.sh              # build, install, run as a service (launchd or systemd)
./install.sh --shell      # …and add the shell line, if you want it
./install.sh --status     # is it running, and what is it applying
./install.sh --restart    # after editing ~/.cloakfleet/.env
./install.sh --uninstall  # stop it, remove the service, undo the shell line
```

The shell line is `eval "$(cloakfleet env)"`, and the reason it is written that
way is the failure it avoids. Agent Veil's installer exported
`ANTHROPIC_BASE_URL` into the profile unconditionally, so the day somebody
stopped the proxy without running the uninstaller, every LLM tool on the machine
broke with a connection error from a line they had not touched. `cloakfleet env`
asks the agent whether it is running and prints **nothing** when it is not, so a
stopped agent means unmasked traffic rather than a broken workstation.

That is a deliberate trade — availability over enforcement — and it is the right
way round for a tool developers depend on. Supervision is what makes it safe: a
stopped agent shows up in the dashboard as silent, rather than as nothing at all.

## Supervision

Optional, and the agent is a complete product without it. Set
`CLOAKFLEET_BACKEND_URL` and an enrolment token and it reports every five
minutes: how many requests it proxied, how many values it masked in which
categories, how many tokens went to which model, and what configuration it is
actually applying.

**Never any content.** Not a prompt, not a response, not a detected value, not a
file name, not a URL. That is structural rather than promised: the contract lives
in [`pkg/telemetry`](pkg/telemetry/) — public, so anybody can read it — and a
test walks its own type and fails on any string field that is not on an explicit
list, each entry carrying its reason.

Token counts are four numbers per model, not two, because a coding agent
re-sends its whole context every turn and almost all of its input is a cache
read — an order of magnitude cheaper than a fresh token and far more numerous.
Folding those together would overstate the bill; leaving them out would
understate it. The agent reports raw counts and the backend prices them, because
prices change and an agent that computed money would need redeploying to every
workstation each time one did.

Enrolment works the way Wazuh's does: the operator's token is presented once and
traded for a per-agent key, so one workstation can be revoked without touching
the others.

## Build

Go 1.26 or later, no other dependencies for the build.

```bash
make build            # bin/cloakfleet
make test             # the whole suite, with the race detector
make lint             # golangci-lint
make score            # gate detection accuracy against the committed floor
make bench-accuracy   # the per-category accuracy report
make e2e-claude       # the end-to-end test: real CLI, real provider (spends quota)
```

`make e2e-claude` is the one that proves the product rather than its parts. It
runs the Claude CLI against Anthropic through the agent, with a tap in between
recording every byte that went upstream, and asserts both halves of the claim:
that no value the caller wrote reached the provider, and that the caller got it
back anyway. It needs the CLI signed in and it spends the operator's quota, which
is why it is opt-in.

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
- **No UK sort code.** Its `12-34-56` shape is a date, so it needs a context word
  in front of it — the same work the bank-account shapes need.
- **No US driver's licence.** Fifty formats, no shared shape, no checksum.
- **The National Insurance number and the employer id carry no checksum.** Their
  letter rules and punctuation are all the evidence there is, so an internal
  reference of the same shape can be masked. The allow list is the escape hatch.
- **A date written as three spaced numbers is ambiguous** and "1 12 2019" is
  masked as one. Recorded in the corpus as an accepted miss rather than hidden.
- **A date is read by the locale that reads dates that way.** France reads
  day-first, the US month-first, and "05/06/2024" is genuinely both. With only
  one of the two enabled, the other country's dates go out in clear — the answer
  is to enable that locale, not to widen a pattern into ambiguity.
- **Mixing locales costs precision** where two countries issue identifiers of
  the same length. Nine bare digits are a French SIREN under one checksum and a
  US routing number under another; the earlier locale in the registry names it.
