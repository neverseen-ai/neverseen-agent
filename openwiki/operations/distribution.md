# Distribution: installer, services, release, menu bar

## `install.sh`

One POSIX shell script (CI checks it is portable shell). It installs the binaries into
`$PREFIX/bin` (`$NEVERSEEN_PREFIX`, default `~/.local`), writes `~/.neverseen/.env`,
and registers a background service.

```
./install.sh [--shell]     install, start, and optionally wire the shell
./install.sh --status      is it running, and what is it applying
./install.sh --restart     restart it (after editing the config)
./install.sh --logs        follow its log
./install.sh --uninstall   stop it, remove the services, undo the shell line
```

**Three things it deliberately does not do** (`install.sh:3-20`):

1. **It never exports a base URL into a shell profile.** Agent Veil did, and the day
   somebody stopped the proxy without running its uninstaller, every LLM tool on the machine
   broke with a connection error from a line in a file they had not touched. What goes into
   the profile instead is `eval "$(neverseen env)"`, which prints nothing while the agent is
   stopped — so the tools reach their provider directly, exactly as before it was installed.
   **Availability over enforcement, on purpose**; supervision is what makes it safe, because
   a stopped agent shows up in the dashboard as silent rather than as nothing at all.
2. **It touches no login file unless asked** (`--shell`). Editing a profile is the kind of
   change that has to be requested rather than assumed. `unwire_shell` matches the line
   verbatim, so `SHELL_LINE` has to stay one line and stay recognisable; the rewrite goes
   through a temporary file and a move, so an interrupted uninstall cannot leave a truncated
   login file behind.
3. **It does not enable supervision.** That is the paid half, it needs a backend URL and an
   enrolment token, and it belongs in the config file rather than in an installer's arguments
   where it would land in the shell history.

**The config file ships with the question rather than an answer**: `NEVERSEEN_PII_LOCALE=`
is written empty, because choosing a locale here would be worse than leaving it — scanning
one country's data with another country's patterns masks its invoice numbers and misses its
identifiers. The directory is `0700` and the file `0600`. An existing config is kept.

**`--uninstall` undoes exactly what the installer added and leaves `~/.neverseen/` alone.**
That directory holds the operator's config, the identity a backend knows the machine by, and
any buckets not yet delivered: deleting the identity silently would have the next install
enrol as a **second agent and count twice** against what they pay for, and deleting the
buffer would throw away the record of an outage still in progress.

### Services

- **macOS** — a launchd agent at `~/Library/LaunchAgents/ai.neverseen.agent.plist`, with
  `KeepAlive`, because nobody should stop the masking by accident.
- **macOS, the icon** — a second launchd agent, `ai.neverseen.tray.plist`, deliberately
  **without** `KeepAlive`: the menu offers "Quit the icon", and launchd would put it straight
  back while the person watched nothing happen. Closing a window has to work.
- **Linux** — a systemd user unit at `~/.config/systemd/user/neverseen.service`. No tray:
  the icon is Cocoa, and Linux has no menu bar to put it in.

## Release (`.goreleaser.yml`)

Two build ids. `neverseen` (`./cmd/neverseen`) builds for darwin and linux; `neverseen-tray`
(`./cmd/neverseen-tray`) for darwin only, since it needs cgo for AppKit. The macOS archives
carry both binaries, the Linux archives only the agent. CI runs `goreleaser check` and builds
a snapshot on every run, so a broken release configuration fails before a tag does
(`.github/workflows/ci.yml`).

**`version` is a `var`, not a `const`** in both `main` packages. Declared `const`, the
`-ldflags "-X main.version=…"` stamp is silently inert and every release reports the same
string — which is what Agent Veil shipped.

## The menu bar binary

`cmd/neverseen-tray` is the **one exception** to the one-entrypoint rule, and its package
doc is where the bar it had to clear is written down.

**It assembles nothing** — no detector, no vault, no provider table, no key, no configuration.
It reads the agent's `/healthz` and paints an icon. There is nothing in it that could come to
disagree with the agent about what masking means, because nothing in it has an opinion about
it.

**What earned it a binary of its own is what happened when it was a subcommand**, which was
tried and measured. The menu bar is Cocoa, cgo is a property of a whole binary rather than of
a subcommand, and `bin/neverseen` came out linking AppKit, no longer building under
`CGO_ENABLED=0`, and running a GUI toolkit's package initialiser in every proxy process that
would never draw anything. Worse, the two then shipped together — a broken Cocoa build would
mean no release of the masking agent at all. That is the rule the telemetry already follows at
runtime, *the thing that watches the control must never be able to stop it*, applied to the
build.

It is also a separate **process** for a reason that has nothing to do with cgo: an icon inside
the proxy vanishes at the exact moment it becomes useful. The state worth showing is that the
agent is **not there**, and only something that outlasts it can show that.

Anything else proposing a second `main` has to clear the same bar: assembles no pipeline,
holds no secret, and pays a cost the agent would otherwise carry.

It takes exactly one argument, `version`. There is nothing to configure — where the agent
listens is the agent's own setting, read from the environment like everywhere else, and a flag
here would be a second answer to it. `tray.Run` must be on the main goroutine: systray owns
the platform event loop, and on macOS that loop must be the main thread.

### What is testable is kept away from the toolkit

Everything in `internal/tray` that decides **what** to show is separate from the toolkit.
`render` (`tray.go:190`) and `watch` (`:394`) take no part of `fyne.io/systray` and are covered
by tests; the adapter that touches the toolkit (`systray.go`) builds the menu from what
`render` decided and holds no decision of its own — it has grown to a couple of dozen small
functions as the menu gained switches, modes, levels and locales, and the pool sizing for the
category rows is the one piece of arithmetic in it.
A menu bar cannot be asserted on in CI, so what can be is kept where a test reaches it — the
alternative is a feature whose behaviour has only ever run on somebody's screen.

- **The icon follows `Status.Level()` and nothing else**, so the picture and the exit code of
  `neverseen status` cannot disagree about the same agent
  (`TestTheIconFollowsWhetherValuesAreReplaced`,
  `TestAPartlyMaskingAgentGetsItsOwnIcon`). **There are three icons**, because a category
  can be switched off: such an agent is masking, so the masking picture would be the green
  light over the values that are not being replaced, and the unmasked one would be a lie
  about the twenty-odd categories that are. The third is the right-hand square **half
  filled** — the mark's own vocabulary again, and half rather than a smaller inner square
  because at sixteen points an inner shape is three pixels with a one-pixel gap.
- **The switch menu is built from what the agent published, never from `pkg/pii`.** The
  menu bar could import the catalogue directly, and must not: the agent is the one applying
  it, so a menu built from its own copy would go on offering a switch a rebuilt agent had
  stopped honouring. `/healthz` carries the groups, their categories, what is off and what
  is locked.
- **A group with some of its categories off says so in its title** — "Personal details — 1
  of 2 off". `fyne.io/systray` offers `Check()` and `Uncheck()` and nothing between, so a
  partly-off group cannot show a third tick state, and drawn simply unticked it would claim
  nothing in the family was being masked.
- **A locked family is one dim line with a count**, not a submenu: twenty API keys nobody
  may switch off is twenty rows of nothing to do, and a submenu that opened onto them would
  read as an invitation.
- **A click sends the whole set and redraws from the reply**, so a refused click corrects
  itself rather than leaving a tick that lies. `proxy.SetPolicy` is the one place a local
  surface writes this, as `proxy.Query` is the one place it is read.
- `display` is **one value rather than four calls**, so `watch` can tell whether anything
  changed by comparing two of them — and a fifth thing to show cannot be added without the
  comparison being updated with it. `watch` applies only on a change, because a menu bar told
  the same thing every five seconds redraws itself for the life of the session, and on macOS
  every one of those calls crosses into the main thread (`TestWatchAppliesOnlyWhatChanged`).
- `pollEvery` is 5s and `askTimeout` 2s: on the loopback interface the answer takes a
  millisecond or it is not coming, and an icon that took a minute to notice the agent had
  stopped would be worse than no icon — somebody would trust it.
- `lineCount` is fixed at 5 because a menu bar item is built once and its entries updated in
  place; the toolkit has no notion of a list that grows.
- The providers offered are **only what the agent says it is serving**
  (`TestTheProvidersOfferedAreTheOnesTheAgentServes`). A hard-coded list would go on offering a
  provider a deployment had pointed elsewhere, and an agent that is not answering serves
  nothing. `maxProviderEntries` is 12, and past it the extras are named as a **count rather
  than dropped quietly** — a menu that silently omitted two would have somebody conclude the
  agent does not serve them.

### The icons are generated and committed

`go run ./internal/tray/icons/generate.go`. Committed for the reason
`testdata/heartbeats.json` is: a generated asset a reviewer can look at beats a build step
nobody can, and the alternative is an SVG rasteriser in `go.mod` for three 32×32 pictures. They
are black plus alpha because macOS is handed them as template images and recolours them for a
light or a dark bar. The state is carried by the mark's own vocabulary — the right-hand square
outlined while that value is being replaced, filled when it is not — rather than by a badge
over it: a strike was tried and is eight pixels of diagonal at the size this is actually seen.

## Pointing a tool at the agent

**`shellTools` (`internal/proxy/shellenv.go:51`) is the one owner of how a tool is pointed at
this agent.** One entry per provider carrying three facts that would drift apart in three
tables:

| Field | What it is |
| --- | --- |
| `Variable` | the environment variable its SDK reads |
| `CLI` | the command everybody runs against that variable; empty means "no single obvious one", which is not the same as "there is no CLI" |
| `Caveat` | what somebody has to know before trusting the line, where the tool does not simply honour the variable |

`PointAt` (`:101`) builds the line the banner and the menu bar hand over, `CaveatFor` (`:87`)
the warning; `ShellEnv` writes its `export` from the same table (`:142`) rather than through
`PointAt`, because its line is for a login file and `PointAt`'s is for one shell. What all
three share is the table and the caveat — two spellings of the variable would be two chances
to be wrong about how somebody's traffic gets masked.

**Only two providers are in it**, and the reason is the same one that keeps the other six as
comments in `neverseen env`: a guessed variable name is an instruction that does nothing, and
a guessed command name is worse — it fails with "command not found" after somebody has already
pasted it and believed it. Adding one means a **verified** pair, not a plausible one.

Where the CLI is known the line is a **prefixed assignment** (`ANTHROPIC_BASE_URL=… claude`)
rather than an export: it applies to that run and leaves the shell as it was, which is what
"the command to run" means.

**A caveat is carried to every place the line is handed over.** Codex reads `OPENAI_BASE_URL`
but a `model_provider` in `~/.codex/config.toml` — or `--profile` — wins over it, so on an
already-configured machine the line does nothing, silently, and the traffic goes out unmasked.
That failure has **no symptom from the terminal**, which is why it lives in the table rather
than in whichever surface happened to be written last
(`TestTheCaveatTravelsWithTheLine`).

`PointAt` is unconditional, unlike `ShellEnv`, and that is the whole reason it is a separate
function: it is for one shell now, typed or pasted by somebody who can see whether the agent is
running. A line like that in a login file is the Agent Veil failure. Whatever offers it to a
person has to say so at the point of offering.

**`ShellEnv` (`:128`) asks first and writes nothing when the agent is down**
(`TestShellEnvPrintsNothingWhenTheAgentIsDown`), with a 300 ms timeout so a wedged socket
cannot hang a login (`TestShellEnvGivesUpQuicklyOnAHungAgent`). `--force` prints without
checking.

## See also

- [Configuration](configuration.md) — the environment and the commands.
- [Request path](../architecture/request-path.md#healthz-and-the-local-callers) — the
  `/healthz` type every local caller shares.
