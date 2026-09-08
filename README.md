# Neverseen

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
$ NEVERSEEN_PII_LOCALE=fr,gb,us neverseen proxy
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

### Watching a real exchange: `neverseen proxy -a -v`

The test page answers what *would* happen to a text. Two flags on the agent answer
what happened to a request your tool actually sent: it runs the agent in the
foreground on its own port and prints, for every exchange, the body it received
from the tool, the body it sent to the provider, and every value it replaced on
the way out and restored on the way back — coloured, when it is writing to a
terminal.

```console
$ NEVERSEEN_PII_LOCALE=fr neverseen proxy -a -v

neverseen — the agent in the foreground, on 127.0.0.1:8787.

  locales:      fr
  substitution: token

In another terminal, run your tool through it:

  ANTHROPIC_BASE_URL=http://127.0.0.1:8787/anthropic claude
  OPENAI_BASE_URL=http://127.0.0.1:8787/openai codex
  # codex ignores this if ~/.codex/config.toml sets model_provider, or if you pass --profile
…
── IN   from the tool · session=default · 84 B ────────────────────────────────
{"prompt":"écris à pierre.paul@example.com, tél 06 12 34 56 78, matricule ZZ-4471"}
                        ^ blue: about to be replaced        ^ unmarked: not recognised
MASK pierre.paul@example.com TO [EMAIL_1]
MASK 06 12 34 56 78 TO [PHONE_1]
── OUT  to anthropic · session=default · 67 B ─────────────────────────────────
{"prompt":"écris à [EMAIL_1], tél [PHONE_1], matricule ZZ-4471"}
                    ^ red: will be turned back on the way in
UNMASK [EMAIL_1] TO pierre.paul@example.com
UNMASK [PHONE_1] TO 06 12 34 56 78
```

Blue is a value in clear and red is a replacement, in the bodies and in the
MASK/UNMASK lines alike, so the eye follows one colour from the body that arrived
through to the body that left. In `fake` mode the replacement is a stand-in
rather than a token — `1 rue de l'Exemple, 99000 Villeneuve` — and it is marked
just the same, because what was substituted comes from the exchange rather than
from the shape of the text. What
is unmarked in both is the finding: `matricule ZZ-4471` is in the body that left,
so the catalogue never recognised it and it went to the provider in clear. No
count reports that.

It is the same pipeline as `neverseen proxy` — the same catalogue, the same
substitution mode, the same vault — so what you watch is what the agent does.
Two things differ from a plain run, both on purpose. `-a` is the one place in this
agent where a real value is written to a screen, and `-v` the one place one is written
to disk: the log carries counts, the heartbeat carries no content at all. And neither
belongs in a service definition — the installer sends this agent's output to a log
file, so `-a` there would keep every prompt in clear for as long as the service runs.
The banner says so on every start.

A value is named once as it is replaced, and once as it is restored, rather than
once per occurrence: a system prompt resent every turn would otherwise bury the
exchange you are looking at. The bodies themselves are printed whole, however
long: a ceiling would be the console deciding which part of your traffic is worth
looking at, and the part it cut is exactly where a value nothing recognised would
be. The rule above each one says the size, so a body scrolling past is still
accounted for.

`fake` mode round-trips too: a stand-in is put back on the way in, by matching
its own text, so you see a MASK and an UNMASK line there as well. What keeps that
safe is that a credential never gets a stand-in — every secret category is
replaced by a bracket token by design — so the value-matching path can never
expand one into a live secret.

Colour is on only when the console is a terminal, so `neverseen proxy -a | tee
audit.log` gives plain text you can grep for a value rather than escape
sequences through the middle of it.

Or scan a file from the shell, without starting anything:

```console
$ echo "Call 020 7946 0958, NHS number 9434765919" | NEVERSEEN_PII_LOCALE=gb neverseen scan
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

Both shapes a masked value takes are expanded: a bracket token, and — in `fake`
mode — the stand-in that replaced it. What stops a masked credential from being
turned back into a live secret is one step earlier: a credential never gets a
stand-in, so it only ever travels as a bracket token.

## Install

```bash
./install.sh              # build, install, run as a service (launchd or systemd)
                          # …and on macOS, an icon in the menu bar
./install.sh --shell      # …and add the shell line, if you want it
./install.sh --status     # is it running, and what is it applying
./install.sh --restart    # after editing ~/.neverseen/.env
./install.sh --logs       # follow its log
./install.sh --uninstall  # stop it, remove the service, undo the shell line
```

On macOS the same answer is in the menu bar, from a second small binary
(`neverseen-tray`) that reads the agent's health and paints an icon: the mark with
its right-hand square outlined while values are being replaced, filled when they
are not. It is a separate process from the agent on purpose — an icon living inside
the proxy would vanish at the exact moment it became useful, since the state worth
seeing is that the agent is *not* there. It holds nothing, changes nothing, and
quitting it leaves the agent masking.

Its menu also hands over the line that points one tool at the agent, per provider —
`ANTHROPIC_BASE_URL=http://127.0.0.1:8787/anthropic claude`, ready to paste into a
terminal. A prefixed assignment rather than an export, so it applies to that run
and leaves the shell as it was; a login file wants `eval "$(neverseen env)"`
instead, and the menu says so where it hands the line over.

`neverseen status` answers the same question without the installer, and answers
it the useful way round: not "is the process up" but **what is it masking**. An
agent with no locale selected is up, healthy and recognises almost nothing, so a
green light there would be a green light over traffic going out in clear. It exits
non-zero unless the agent is actually masking, which makes it usable from a
script.

```
$ neverseen status
neverseen is masking on 127.0.0.1:8787.

  version        1.4.2
  locales        fr, gb
  substitution   token
  providers      anthropic, openai
```

The shell line is `eval "$(neverseen env)"`, and the reason it is written that
way is the failure it avoids. Agent Veil's installer exported
`ANTHROPIC_BASE_URL` into the profile unconditionally, so the day somebody
stopped the proxy without running the uninstaller, every LLM tool on the machine
broke with a connection error from a line they had not touched. `neverseen env`
asks the agent whether it is running and prints **nothing** when it is not, so a
stopped agent means unmasked traffic rather than a broken workstation.

That is a deliberate trade — availability over enforcement — and it is the right
way round for a tool developers depend on. Supervision is what makes it safe: a
stopped agent shows up in the dashboard as silent, rather than as nothing at all.

## Web chats: the browser extension

An agent is a proxy, and a proxy needs something to stand in front of. A tool takes
`ANTHROPIC_BASE_URL`; claude.ai takes nothing — the traffic goes from the page to the
site's own origin over a connection the page opened, and there is no variable to set.

`extension/` is the answer, and it is deliberately thin. It replaces `window.fetch`
before the site's script runs, hands what you typed to the agent on this machine, and
puts your own values back into the answer as it streams in. It holds **no catalogue, no
policy and no mappings** — the same detector that masks your terminal's traffic masks
your browser's, with the same locales, the same substitution mode and the same switches.

```bash
make extension            # build it into extension/dist
neverseen key            # print the control key, paste it into the options page
```

Then load `extension/dist` unpacked at `chrome://extensions`. The options page finds the
agent on its default port by itself; it only asks for the key.

Two things about it are worth knowing before you trust it:

**A send that could not be masked is not sent.** If the agent is stopped, the message
stays in the box and a banner says which command starts it. That is the same decision the
proxy makes on a body it cannot read, and it is the one somebody will be tempted to
soften — a blocked send looks like a broken site, and a forwarded one looks like nothing
at all.

**Restoration fails the other way, on purpose.** If the agent goes away mid-answer, the
text arrives showing `[EMAIL_1]` rather than not arriving. An answer you have already
paid for is worth more than tidiness, and a visible replacement is unreadable rather than
unsafe.

Not covered, and said here rather than left to be discovered: file uploads, images and
voice reach the model unmasked, and reloading a conversation shows the replacements
instead of your values. Both are declared on the extension's own options page too.

`extension/e2e/run.test.mjs` is what makes any of this a claim you can check: it loads
the built extension into a real Chrome, puts claude.ai on this machine, and asserts both
halves of one exchange — that the site received the token and that the page rendered the
address. Either half alone passes over a page that was never touched.

## Supervision

Optional, and the agent is a complete product without it. Set
`NEVERSEEN_BACKEND_URL` and an enrolment token and it reports every five
minutes: how many requests it proxied, how many values it masked in which
categories, how many tokens went to which model, how many conversations ran and
what the model asked the workstation to do, and what configuration it is
actually applying. What each figure answers, and what the heartbeat deliberately
does not say, is in [docs/telemetry-for-ai-governance.md](docs/telemetry-for-ai-governance.md).

**Never any content.** Not a prompt, not a response, not a detected value, not a
file name, not a URL. That is structural rather than promised: the contract lives
in [`pkg/telemetry`](pkg/telemetry/) — public, so anybody can read it — and a
test walks its own type and fails on any string field that is not on an explicit
list, each entry carrying its reason.

**Two things it does report about the machine: its own IP addresses, and its
name.** Stated plainly here because both are personal data, and because the rest
of this section would otherwise read as a stronger claim than it is. Neither is
content — nothing about the traffic travels in them — but both identify a
machine, and through a machine a person. They are there because a fleet view
without them does not work: "agt_4742be… has stopped reporting" sends somebody to
a database, "the laptop at 10.4.2.87 has stopped reporting" sends them to a
desk.

The hostname is the more direct of the two, and knowingly so: a workstation is
often named after the person using it, so `marie-macbook` names somebody where an
address only names a machine. That is also exactly why it is reported — it is the
identifier an operator already recognises, and one that survives a laptop moving
between networks when the address does not. It is sent as the operating system
gives it, never resolved and never hashed: hashing would keep the personal data
and lose the use.

The addresses reported are the machine's own local ones, not its public address:
on a corporate network the private address is what tells one workstation from
another. Loopback and link-local are dropped, and the list is capped. If you run
the backend, note that it also records the address it *sees* each connection come
from, which is a separate fact about the network rather than about the machine.

Token counts are four numbers per model, not two, because a coding agent
re-sends its whole context every turn and almost all of its input is a cache
read — an order of magnitude cheaper than a fresh token and far more numerous.
Folding those together would overstate the bill; leaving them out would
understate it. The agent reports raw counts and the backend prices them, because
prices change and an agent that computed money would need redeploying to every
workstation each time one did.

`neverseen replay <dir>` rebuilds that batch from a directory of `-v` traces and
prints it, to check the counters against real traffic rather than fixtures. It
sends nothing.

Enrolment works the way Wazuh's does: the operator's token is presented once and
traded for a per-agent key, so one workstation can be revoked without touching
the others.

## Build

Go 1.26 or later, no other dependencies for the build.

```bash
make build            # bin/neverseen
make test             # the whole suite, with the race detector
make test-cover       # the same, with coverage; the CI gate is 80%
make lint             # golangci-lint
make score            # gate detection accuracy against the committed floor
make bench-accuracy   # the per-category accuracy report
make e2e-claude       # the end-to-end test: real CLI, real provider (spends quota)
make extension-e2e    # the browser extension through a real Chrome against a real agent
```

`make e2e-claude` is the one that proves the product rather than its parts. It
runs the Claude CLI against Anthropic through the agent, with a tap in between
recording every byte that went upstream, and asserts both halves of the claim:
that no value the caller wrote reached the provider, and that the caller got it
back anyway. It needs the CLI signed in and it spends the operator's quota, which
is why it is opt-in.

## Configuration

Ten environment variables, all documented in
[.env.example](.env.example) — which a test keeps honest, in both directions: a
variable the code reads and the file does not mention fails the build, and so
does one the file documents and no code reads.

`NEVERSEEN_PII_LOCALE` selects which country's identifiers to look for: `fr`,
`gb`, `us`, `none`, or a comma-separated mix. Unset means none, which is
deliberate — scanning one country's data with another country's patterns is
worse than scanning none of it, and an operator who never set the variable has
not chosen that.

`NEVERSEEN_PII_SUBSTITUTION` picks what a masked value becomes, a token or a
stand-in; `NEVERSEEN_PII_ALLOWLIST` names the values never to mask; and
`NEVERSEEN_SECRET_LEVEL` says how far down the strength scale a named secret is
masked. All three are starting values: `neverseen mask`, the menu bar and
`PUT /policy` move them while the agent runs, and what they change survives a
restart. The rest — the address, the provider overrides, the session key and
the three supervision settings — are in the file.

Some things are found whatever the locale says, because they mean the same
everywhere: email addresses, payment cards, IBANs (every issuing country), IP
addresses, MongoDB ids, ISO dates, and every credential — API keys for a dozen providers,
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
- **The browser extension covers claude.ai and its message sends only.** File
  uploads, images and voice reach the model unmasked; a reloaded conversation
  renders the replacements rather than your values, because the request that
  fetches past messages is not rewritten. ChatGPT is a second site adapter and
  nothing else, and is not written.
- **Mixing locales costs precision** where two countries issue identifiers of
  the same length. Nine bare digits are a French SIREN under one checksum and a
  US routing number under another; the earlier locale in the registry names it.
