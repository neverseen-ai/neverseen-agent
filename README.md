# Neverseen

**Your prompts reach the model. Your data does not.**

Neverseen is a local agent that sits between the AI tools you already use —
Claude Code, Cursor, Codex, a script — and the model provider. On the way out it
replaces personal data and credentials with tokens. On the way back it puts your
real values into the answer.

The model never sees the data. You never see a placeholder.

[![CI](https://github.com/neverseen-ai/neverseen-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/neverseen-ai/neverseen-agent/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Platforms](https://img.shields.io/badge/platforms-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](#install)
[![Licence](https://img.shields.io/badge/licence-FSL--1.1--ALv2-blue)](LICENSE.md)

```diff
  # what you typed
- The worker at 10.42.7.19 can't reach
- redis://cache:s3cr3t-pa55w0rd@10.42.7.30:6379/0, and ops-oncall@acme-corp.io
- is getting paged. STRIPE_SECRET_KEY=sk_live_51H8xQ2eZvKYlo2C9mVqTnBw3fLp

  # what actually left the machine
+ The worker at [IP_1] can't reach redis://[CONN_STR_1], and [EMAIL_1] is
+ getting paged. STRIPE_SECRET_KEY=[STRIPE_KEY_1]
```

*Not a mock-up: that is the real output of `neverseen scan` and `POST /mask` on
that exact input — four categories, one pass. Step 1 below reproduces it on your
own text. The answer comes back with your values put back in, so what you read in
your terminal is what you wrote.*

Nothing to rewrite in your prompts. Nothing to remember. One environment
variable and the tool you already run.

**And the obvious objection, answered first:** the agent holds no API keys.
Whatever credential your tool sent — a bearer token, an `x-api-key` header — is
forwarded untouched, because the tool making the request already has it. This is
one more thing between you and the provider, not one more place a key is stored.

---

## Try it

**1. Test the binary on your own examples — before installing anything**

Build it and point it at a text you control. Nothing is installed, no service is
started, nothing goes to a network:

```bash
git clone https://github.com/neverseen-ai/neverseen-agent && cd neverseen-agent
make build

# your own paste, with the values changed — a log line, a config, a ticket
echo "…" | NEVERSEEN_PII_LOCALE=fr,gb,us bin/neverseen scan
```

You get every value it would replace, with its category, its byte offsets and its
confidence:

```console
$ NEVERSEEN_PII_LOCALE=us bin/neverseen scan < incident.txt
locales: us

IP_ADDRESS               10.42.7.19
                           IPv4 address, bytes 14-24, confidence 75
SECRET_CONN_STR          cache:s3cr3t-pa55w0rd@10.42.7.30:6379/0
                           URL carrying credentials, bytes 45-84, confidence 92
EMAIL                    ops-oncall@acme-corp.io
                           Email address, bytes 90-113, confidence 95
SECRET_STRIPE_KEY        sk_live_51H8xQ2eZvKYlo2C9mVqTnBw3fLp
                           Stripe API key, bytes 150-186, confidence 97

4 value(s) would be masked:
  EMAIL                    1
  IP_ADDRESS               1
  SECRET_CONN_STR          1
  SECRET_STRIPE_KEY        1
```

**Read the silences, not the hits.** Whatever it says nothing about is what would
reach the provider in clear. That is the one thing worth checking before you let
it near your traffic — and it is why this step comes before the install rather
than after it.

**2. Install the binary as a background service**

Once the scan above has convinced you, this puts the same binary on the machine
as a service that starts with the session and stays up.

macOS and Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/neverseen-ai/neverseen-agent/main/install.sh | sh
```

Windows — downloaded rather than piped, because `install.ps1` takes named
parameters (`-Shell`, `-Status`, `-Uninstall`) and a piped script cannot receive
them:

```powershell
iwr -useb https://raw.githubusercontent.com/neverseen-ai/neverseen-agent/main/install.ps1 -OutFile install.ps1
.\install.ps1
```

The service is a launchd agent on macOS, a systemd user unit on Linux, a logon
task on Windows. It is supervised on all three — `KeepAlive`, `Restart=always`,
`RestartOnFailure` — because nobody is meant to be able to stop the masking by
killing a process. You also get an icon in the menu bar or the notification area,
and that one is deliberately *not* restarted: its menu offers "Quit the icon", and
a supervisor would put it straight back.

It does **not** touch your shell profile unless you ask for it with `--shell`.
Already cloned at step 1? `./install.sh` from that directory skips the download
and uses the binary you just built.

**3. Choose what to look for**

```bash
echo 'NEVERSEEN_PII_LOCALE=fr,gb,us' >> ~/.neverseen/.env
./install.sh --restart
```

**4. Point a tool at it**

```bash
ANTHROPIC_BASE_URL=http://127.0.0.1:9787/anthropic  claude
OPENAI_BASE_URL=http://127.0.0.1:9787/openai        codex
```

That's it. Ask your tool something that mentions a colleague, a customer record
or a key, and watch it work.

**5. Keep checking it, on your own data**

Open **<http://127.0.0.1:9787/test>** — paste a real record with the values
changed and see exactly what would leave the machine, in both substitution
modes, with this agent's own configuration. Nothing on that page is sent
anywhere, stored, or kept.

---

## What it catches

| Always, whatever the locale | With `fr` | With `gb` | With `us` |
|---|---|---|---|
| Email · payment cards · IBAN (every issuing country) · IP addresses · MongoDB ids · ISO dates | NIR · SIREN · SIRET · postcode + commune · address · number plate · phone · day-first dates | NHS number · National Insurance number · postcode · phone | SSN · EIN · ABA routing number · street address · ZIP · phone |

**Credentials, always:** API keys for 100+ vendors (Anthropic, OpenAI, AWS,
Stripe, GitHub, Slack…), private keys, JWTs, connection strings carrying a
password, and a graded catch-all for anything shaped like `SECRET=…`.

Checksums decide where a checksum exists — NHS, IBAN, NIR, SIREN/SIRET, cards,
ABA. A value failing its checksum is not a weak match; it is not a match.

---

## Prove it

Four ways, from cheapest to strongest. The first is step 1 above; the other
three work on traffic you actually sent.

```bash
# 1. Scan a text from the shell — nothing started, nothing sent
bin/neverseen scan < incident.txt

# 2. Watch a real exchange, value by value
neverseen proxy -a          # prints every value replaced and restored, coloured

# 3. Keep the evidence: both request bodies and the answer, per exchange
neverseen proxy -a -v       # writes traces/ — diff the two bodies to find what was missed

# 4. The whole product, end to end
make e2e-claude             # real CLI, real provider, a tap recording every byte upstream
```

`make e2e-claude` asserts both halves of the claim at once: that no value the
caller wrote reached the provider, and that the caller got it back anyway.

The finding you are looking for in `-v` is what appears in **both** bodies — a
value the catalogue never recognised. No counter reports that; a diff does.

> **What `-a` and `-v` cost, said plainly.** They are the only two places this
> agent puts a value in clear on a screen or on a disk — everywhere else it
> carries counts and category names, and the supervision heartbeat carries no
> content at all. A trace directory is `0700` and its files `0600`, the same
> treatment the control key gets. Neither flag belongs in a service definition:
> the installer sends this agent's output to `~/.neverseen/agent.log`, so `-a`
> there keeps every prompt in clear for as long as the service runs. The agent
> says so in its banner on every start, because a warning is only worth what it
> costs to ignore.

---

## Options

Everything lives in `~/.neverseen/.env`, and every variable the agent reads is
documented in [.env.example](.env.example) — a test fails the build in both
directions, so the file cannot go stale.

| Variable | What it does | Default |
|---|---|---|
| `NEVERSEEN_PII_LOCALE` | Which country's identifiers to look for: `fr`, `gb`, `us`, `none`, or a comma-separated mix | none |
| `NEVERSEEN_PII_SUBSTITUTION` | `token` → `[EMAIL_1]`, restored in the answer · `fake` → a stand-in that reads as prose | `token` |
| `NEVERSEEN_PII_ALLOWLIST` | Values never to mask — the escape hatch for your own office address or fleet ids | — |
| `NEVERSEEN_SECRET_LEVEL` | How far down the strength scale a *named* secret is masked: `weak`, `medium`, `strong`. Prefixed keys are masked at every level | `weak` |
| `NEVERSEEN_LISTEN` | Address to listen on | `127.0.0.1:9787` |
| `NEVERSEEN_PROVIDERS` | Point one provider at your own gateway, `code=url` — the other seven keep their defaults | — |
| `NEVERSEEN_BACKEND_URL` | Supervision backend, if you have one | — |

Three of them move **while the agent runs**, and what you change survives a
restart:

```bash
neverseen status                # what is it masking right now (exit code follows the answer)
neverseen mask --off EMAIL      # switch a category off
neverseen mask --substitution fake
```

…or click the icon in the menu bar, which shows the same state and hands you the
line that points a tool at the agent.

*A credential can never be switched off — the detector refuses, so no menu and no
API call can route around it.*

---

## The menu bar icon

A masking agent has one failure nobody notices: it stopped. Your tools keep
working — they reach the provider directly — and nothing on screen says the
traffic went out in clear. The icon exists for that one question, and it answers
it without being clicked: the mark's right-hand square is **outlined while values
are being replaced, filled when they are not**.

Three states, not two, because "masking" and "masking everything" are different
answers:

| Icon | Meaning |
|---|---|
| Outlined | Masking — every category the loaded locales can find is being replaced |
| Half | `Masking, with N categories in clear` — you switched something off, and it names which |
| Filled | Not masking — either no locale is loaded, or the agent is not answering |

On Linux the same three are **also** green, amber and red, because the panel draws
what it is handed rather than recolouring it the way macOS does. The colour is added
to the shape and never replaces it: it is the channel about one man in twelve reads
differently, and the square is what carries the meaning.

Click it and the menu says what is happening — the address, the locales, the
substitution mode, the version — and offers the few things a menu is the right
shape for:

- **Settings…**, which opens the agent's own page. That is where a category is
  switched off, the substitution mode is chosen and a locale is loaded: a menu bar
  gives you titles and ticks, with no radio group, no mixed tick and no room for the
  sentence that says what a choice costs, and every one of those absences used to be
  answered by writing the sentence into an entry's own title;
- **Mask everything again**, one click back to the whole catalogue;
- **copy the command that points one tool at the agent**, one entry per provider
  the agent actually serves — `ANTHROPIC_BASE_URL=… claude`, ready to paste. The
  first line of that submenu is a caution rather than a command: what belongs in a
  login file is `eval "$(neverseen env)"`, never an unconditional export;
- **open the test page**;
- **quit the icon**, which leaves the agent masking.

Everything you change on that page survives a restart — it goes through the same
`PUT /policy` as `neverseen mask`, and is stored in `~/.neverseen/policy.json`.
A credential can never be switched off from either: the detector refuses, so no
page and no menu can route around it.

It is a **separate process** (`neverseen-tray`) on purpose. An icon living inside
the proxy would vanish at the exact moment it became useful, since the state most
worth seeing is that the agent is *not* there. It holds nothing and decides
nothing, and its own service is deliberately not restarted — the menu offers
"Quit the icon", and a supervisor would put it straight back.

### On Linux

The same icon, started from `~/.config/autostart/neverseen-tray.desktop` at login
rather than by systemd — a desktop job goes where the desktop looks for one, and a
user unit would have to be wanted by `graphical-session.target`, which not every
desktop reaches.

Whether anything *draws* it is the desktop's decision, not the agent's. Linux has
no notification area as a platform feature; it has a convention, StatusNotifierItem,
published on the session bus and drawn by whatever happens to be listening. KDE
Plasma, XFCE, Cinnamon, MATE, Budgie, LXQt and Ubuntu's GNOME all listen. **Stock
GNOME does not** — the default on Fedora, Debian and RHEL — until you install the
*AppIndicator and KStatusNotifierItem Support* extension and log back in.

So the icon checks before it draws. Finding nothing that would host it, it says so
and exits rather than sitting in the process table publishing to an empty bus:

```
$ ~/.local/bin/neverseen-tray
neverseen-tray: nothing on this desktop draws tray icons, so there is no icon to
show. The agent is unaffected and still masking — `neverseen status` answers the
same question, and exits non-zero unless it is masking everything.
GNOME shows a StatusNotifierItem only through an extension: install "AppIndicator
and KStatusNotifierItem Support", then log out and back in.
```

Started at login there is no terminal to say that in, so the desktop entry redirects
to `~/.neverseen/agent.log` — a desktop entry has no `StandardErrorPath` the way a
launchd plist does. `./install.sh --status` points you at it.

The message matters more than it looks. An icon disappearing is exactly what it is
meant to look like when masking has stopped, so the one case where it goes for an
unrelated reason has to say the agent is fine. `neverseen status` is the answer that
needs no desktop at all, and it exits non-zero unless the agent is masking its whole
catalogue, so a script can use it.

---

## Web chats: the browser extension — coming later

**Not shipped yet.** It works in the repository and there is an end-to-end test
that drives a real Chrome against a real agent, but it is not released, not
packaged, and not in a store. Treat this section as what is being built, not as
something to install today. Everything above is what the product does now.

The problem it solves: a proxy needs something to stand in front of. A terminal
tool takes `ANTHROPIC_BASE_URL`; `claude.ai` takes nothing — the traffic goes from
the page to the site's own origin, over a connection the page opened, and there is
no variable to set.

The answer is deliberately thin. The extension replaces `window.fetch` before the
site's script runs, hands what you typed to the agent on this machine, and puts
your own values back into the answer as it streams in. It holds **no catalogue, no
policy and no mappings** — the same detector that masks your terminal's traffic
masks your browser's, with the same locales, the same substitution mode and the
same switches. A second engine in JavaScript would be a second answer to "what
does this agent mask".

Two behaviours are already settled, and they point in opposite directions on
purpose:

- **A send that could not be masked is not sent.** Agent stopped → the message
  stays in the box and a banner names the command that starts it. A value that
  reaches the model cannot be recalled.
- **A chunk that could not be restored is shown unexpanded.** Agent goes away
  mid-answer → you see `[EMAIL_1]` rather than losing an answer you already paid
  for. A visible replacement is unreadable, not unsafe.

Known limits, stated now rather than discovered at release: file uploads, images
and voice would reach the model unmasked, a reloaded conversation renders the
replacements rather than your values, and ChatGPT is a second site adapter that is
not written.

---

## Supervision (optional)

The agent is a complete product with no backend at all. Point it at one and it
reports every five minutes: requests proxied, values masked per category, tokens
per model, conversations, what the model asked the workstation to do, and what
configuration is actually being applied.

> **Never any content.** Not a prompt, not a response, not a detected value, not
> a file name, not a URL. That is structural rather than promised: the contract
> is public in [`pkg/telemetry`](pkg/telemetry/), and a test walks its own type
> and fails on any string field that is not on an explicit list, each entry
> carrying its reason.

Two things about the machine **are** reported, and are personal data: its local
IP addresses and its hostname. Both are there because a fleet view without them
does not work — `agt_4742be…` sends somebody to a database, `marie-macbook`
sends them to a desk. Details, and what each figure answers, in
[docs/telemetry-for-ai-governance.md](docs/telemetry-for-ai-governance.md).

Enrolment works the way Wazuh's does: an operator token is presented once and
traded for a per-agent key, so one workstation can be revoked without touching
the others.

---

## How the detection engine is held to account

Regular expressions are easy to write and easy to break silently, so the engine
is measured rather than trusted.

- **A corpus, not unit tests.** [`internal/detector/testdata/corpus/`](internal/detector/testdata/corpus/)
  holds cases written as prose — a sentence with an identifier in it, the way it
  arrives in a support thread — each declaring exactly which spans must be found.
- **Negative cases carry equal weight.** Version strings, order numbers, commit
  SHAs, documentation placeholders: they must come out **untouched**. A pattern
  widened until it matches everything scores perfect recall, and only those cases
  notice.
- **A per-category floor.** [`score-baseline.json`](internal/detector/testdata/score-baseline.json)
  commits true positives, false positives and misses for every category. `make
  score` fails if any of them slips, and a category added with no corpus case
  behind it fails the build.
- **The catalogue validates itself at startup.** A pattern emitting a category
  nobody registered panics at package initialisation rather than masking a value
  under a nameless token.

The corpus and the score floor are inherited from
[Agent Veil](https://github.com/vurakit/agentveil) (MIT, see [NOTICE](NOTICE))
and used as an executable specification: the engine is an independent
implementation, and the corpus is what holds it to the same behaviour.

---

## Known gaps

Written down rather than discovered later.

- **Only `fr`, `gb` and `us` exist.** Germany, Spain, Italy and the Netherlands
  are next.
- **No UK sort code** — its `12-34-56` shape is a date, so it needs a context
  word in front of it.
- **No US driver's licence** — fifty formats, no shared shape, no checksum.
- **The National Insurance number and the US employer id carry no checksum**, so
  an internal reference of the same shape can be masked. The allow list is the
  escape hatch.
- **A date is read by the locale that reads dates that way.** `05/06/2024` is
  genuinely both; with only one locale enabled, the other country's dates go out
  in clear. Enable the locale rather than widening the pattern.
- **Mixing locales costs precision** where two countries issue identifiers of the
  same length. Nine bare digits are a French SIREN under one checksum and a US
  routing number under another; the earlier locale in the registry names it.
- **The browser extension is not released.** It works in the tree and is tested
  end to end, but it is not packaged or published; web chats are unprotected
  until it ships. See the section above for what it will and will not cover.

---

## Build from source

Go 1.26 or later. No other dependencies.

```bash
make build            # bin/neverseen
make test             # the whole suite, with the race detector
make test-cover       # the same, with coverage; the CI gate is 80%
make lint             # golangci-lint
make score            # gate detection accuracy against the committed floor
make bench-accuracy   # the per-category accuracy report
```

Architecture, invariants and the reasoning behind each rule:
[**openwiki/quickstart.md**](openwiki/quickstart.md).

---

## Licence

**Source available today, open source on a two-year delay.** Every release ships
under the Functional Source License, which grants you a second licence in the
same breath: the right to use that release under **Apache 2.0, effective on its
second anniversary**. The grant is made *irrevocably*, on the day the release
ships — nobody has to decide anything later, and nobody can take it back. That
pattern has a name: delayed open source publication.

Until that date, the release is free for any use, including commercially and for
your company's internal use. The one thing the licence withholds is offering it
to others as a competing product or service. See [LICENSE.md](LICENSE.md).
