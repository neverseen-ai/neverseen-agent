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

**Where the binary comes from, in order** (`install.sh:22-25`): this checkout when there is a
Go toolchain, an unpacked release archive when the script sits beside one, and otherwise the
latest release, downloaded from GitHub and **checksum-verified** — the third is what somebody
piping this script from the web gets, and it used to be an error message. There is no variable
that switches the verification off: this binary forwards the caller's credentials to a
provider, so an escape hatch on the only integrity check an installation has is the line that
ends up pasted into an internal wiki. Every entry in the archive is read before anything is
written, so one naming an absolute path or stepping out with `..` is refused rather than
unpacked into the home directory the script runs as. The tag is read off the redirect on
`/releases/latest` rather than through the REST API, whose sixty anonymous calls an hour are
spent by somebody else behind a company NAT — a rate limit is not a sentence anybody can act
on; `NEVERSEEN_VERSION` pins one instead, and is the way back to an older release.

**It ends by asking `neverseen status`** rather than treating a registered service as an agent
that is masking, and never fails the installation on the answer: the config it writes chooses
no locale on purpose, so a fresh install is running and recognising almost nothing, which is a
non-zero exit by design. An installer that read that as a failure would report the one thing
that is working as broken.

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

### One owner of the service definition

`internal/service` renders and registers them; `install.sh` calls it. Before that the
plist and the unit were heredocs in the script, which meant anything else that wanted
to install this agent could only write a fourth copy of them — and of the copies that
drifted, the pair that mattered would be the one that *installed* the agent and the one
that *restarted* it.

```
neverseen service install     register both jobs and start the agent now
neverseen service uninstall   stop and deregister; leaves ~/.neverseen alone
neverseen service restart     reload the definition and restart
```

**Three verbs, because each writes or removes a definition** — a restart is an unload
and a load of the same one. `--status` and `--logs` stay in `install.sh`: they observe
and author nothing, so moving them would grow the command without closing any drift.

**It is not a second entrypoint.** It is a subcommand of `cmd/neverseen`; it assembles
no pipeline, holds no secret and reads no environment. What differs between
installations arrives as `--prefix`, which is an argument rather than a variable read
here because `NEVERSEEN_PREFIX` configures an *installation* and not the running agent
— read here, it would owe `.env.example` a line documenting a setting that changes
nothing about what the agent does, and `TestDocumentedEnvironmentMatchesTheCode` fails
in both directions.

**`Render` takes the platform as a field, not `runtime.GOOS`.** That is what lets every
rendering be asserted on every runner: the plist is checked on Linux and the Windows
scheduled task on macOS. A definition only its own platform can test is a definition CI
sees once a release. Only `Apply`, `Uninstall` and `Restart` — which shell out to
`launchctl`, `systemctl` and `schtasks` — are build-tagged.

**The two definitions that already existed are reproduced byte for byte**, verified
against the script's own heredocs before they were deleted. Somebody has these loaded
right now: a plist differing in anything but whitespace is a second definition of the
same job rather than the same one moved. The only bytes that may differ are the ones
that were malformed — paths are XML-escaped now, which the heredocs never did, so a
home directory carrying an ampersand no longer produces a plist launchd silently
refuses.

**The agent is restarted and the icon is not**, on every platform that registers one:
`KeepAlive` on launchd, `RestartOnFailure` on Task Scheduler, and neither for the icon.
`TestOnlyTheAgentIsRestarted` holds both halves rather than leaving them to the golden
files — a golden file records what the code does, and this records what it must.

### Services

- **macOS** — a launchd agent at `~/Library/LaunchAgents/ai.neverseen.agent.plist`, with
  `KeepAlive`, because nobody should stop the masking by accident.
- **macOS, the icon** — a second launchd agent, `ai.neverseen.tray.plist`, deliberately
  **without** `KeepAlive`: the menu offers "Quit the icon", and launchd would put it straight
  back while the person watched nothing happen. Closing a window has to work.
- **Linux** — a systemd user unit at `~/.config/systemd/user/neverseen.service`. No tray,
  and the reason recorded here for years was wrong: the icon is cgo on **darwin alone**, and
  `GOOS=linux CGO_ENABLED=0 go build ./cmd/neverseen-tray` succeeds today —
  `fyne.io/systray` speaks StatusNotifierItem over dbus in pure Go. What actually stops it
  is the desktop. Plasma hosts a StatusNotifierItem natively; GNOME needs the AppIndicator
  extension. Shipping the icon would put one on most Linux machines that silently draws
  nothing, which reads as an agent that is not running.

## Release (`.goreleaser.yml`)

Two build ids. `neverseen` (`./cmd/neverseen`) builds for darwin and linux; `neverseen-tray`
(`./cmd/neverseen-tray`) for darwin only, since it needs cgo for AppKit. The macOS archives
carry both binaries, the Linux archives only the agent. CI runs `goreleaser check` and builds
a snapshot on every run, so a broken release configuration fails before a tag does
(`.github/workflows/ci.yml`).

**One workflow publishes** (`.github/workflows/release.yml`), on a `v*` tag and nothing else.
The snapshot in `ci.yml` answers whether the configuration still works; this is the only job
that uploads archives, so the two cannot disagree about what a release is. It runs on macOS
because `neverseen-tray` links Cocoa and needs cgo and frameworks no other runner has — the
agent itself is pure Go and cross-compiles from there. Without it the archives never existed,
and the branch of `install.sh` that unpacks one had nothing to fetch.

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
`render` and `watch` take no part of `fyne.io/systray` and are covered by tests; the adapter
that touches the toolkit (`systray.go`) builds the menu from what `render` decided and holds
no decision of its own — the provider pool sizing is the one piece of arithmetic left in it.
A menu bar cannot be asserted on in CI, so what can be is kept where a test reaches it — the
alternative is a feature whose behaviour has only ever run on somebody's screen.

**The menu says; it does not configure.** It used to do both, and `plan.go` existed to hold
the decisions that took: the all-or-nothing rule for a family, the arithmetic around locked
members, which slot held which group past the pool, what a row of each choice was titled.
All of it is gone, and so is half of `internal/tray`, because the toolkit was the wrong shape
for the job. `fyne.io/systray` gives titles and ticks — no radio group, no mixed tick, no
room for the sentence that says what a choice costs — so every one of those absences had
been answered by writing the sentence into an entry's own title ("weak — every value found,
words included; masks code too"), and a menu bar of sixty-character rows is a menu nobody
reads. What is configured is configured on
[`/settings`](configuration.md#settings--where-the-configuration-actually-happens), which has
the room this never had; the menu keeps the one thing an icon in the bar can do that nothing
else can, which is to say without being clicked whether the traffic is masked.

- **The icon follows `Status.Level()` and nothing else**, so the picture and the exit code of
  `neverseen status` cannot disagree about the same agent
  (`TestTheIconFollowsWhetherValuesAreReplaced`,
  `TestAPartlyMaskingAgentGetsItsOwnIcon`). **There are three icons**, because a category
  can be switched off: such an agent is masking, so the masking picture would be the green
  light over the values that are not being replaced, and the unmasked one would be a lie
  about the twenty-odd categories that are. The third is the right-hand square **half
  filled** — the mark's own vocabulary again, and half rather than a smaller inner square
  because at sixteen points an inner shape is three pixels with a one-pixel gap.
- **"Mask everything again" is the one thing the menu writes**, and it sends the whole state
  with the switched-off set emptied (`display.maskEverything`,
  `TestMaskingEverythingAgainCarriesTheRestUnchanged`). `PUT /policy` replaces rather than
  patches, so a request carrying nothing but the empty set would take the mode, the locales
  and the secret level down with it — switching off, from an entry that says "mask everything
  again", three settings somebody had just chosen on the page. It redraws from the reply, so
  a refused click corrects itself rather than leaving the menu claiming something it did not
  get. `proxy.SetPolicy` is the one place a local surface writes this, as `proxy.Query` is
  the one place it is read.
- **The three settings the menu stopped drawing are still compared.** They are not on
  screen, but they are in every request that entry sends, and a stale copy of them is a
  click that quietly reverts what the settings page has just changed.
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
