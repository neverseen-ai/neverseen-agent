# Neverseen — OpenWiki quickstart

Neverseen is a **workstation agent that masks sensitive values before they reach a
language model**. It sits between the AI tools people already use (Claude Code, Codex,
a script, an SDK) and the model provider: on the way out it replaces personal data and
credentials with a substitution, on the way back it restores the originals. The model
never sees the data, and the person using the tool never sees a placeholder.

Module path: `github.com/neverseen-ai/neverseen-agent` (see `go.mod`). Go 1.26; the only
dependencies are `fyne.io/systray` (menu bar icon) and `gopkg.in/yaml.v3` (test corpus).

**Licence:** FSL-1.1-ALv2 — *source available, not open source*. Say "source available".
Each release converts to Apache 2.0 two years after it ships (`LICENSE.md`).

The paid supervision backend is a **separate, private repository** (`neverseen-cloud`,
checked out alongside as `../neverseen-cloud`). It imports this repo; this repo must
never import it, and must compile and run with no backend at all.

## Read next

| Page | What it covers |
| --- | --- |
| [Request path](architecture/request-path.md) | `internal/proxy` — provider routing, masking a body, the session vault, response expansion, streaming |
| [Detection engine](architecture/detection-engine.md) | `pkg/pii` catalogue + `internal/detector` engine — categories, locales, checksums, overlap arbitration, token vs. stand-in |
| [Browser extension](architecture/browser-extension.md) | `extension/` + `POST /mask` and `POST /unmask` — masking a web chat by wrapping the page's own `fetch` |
| [Supervision](architecture/supervision.md) | `pkg/telemetry` contract + `internal/telemetry` recorder, on-disk buffer, reporting loop |
| [Configuration](operations/configuration.md) | The ten environment variables, every CLI command, the test page, the audit console |
| [Distribution](operations/distribution.md) | `install.sh`, launchd services, GoReleaser, the menu bar binary, `neverseen env` |
| [Extending the catalogue](workflows/extending-the-catalogue.md) | Adding a PII category, a locale, a provider, a telemetry field |
| [Testing and accuracy](workflows/testing-and-accuracy.md) | The corpus, the per-category score floor, CI gates, the end-to-end test |

## Repository layout

```
cmd/neverseen/        the agent — the ONLY binary that masks anything
cmd/neverseen-tray/   menu bar icon; assembles no pipeline (see Distribution)
internal/proxy/        the request path, the routes the agent answers itself
                       (/healthz, /test, /policy, /mask, /unmask), the console and the traces
internal/detector/     the engine that runs the catalogue over text
internal/vault/        per-session masked→original mapping, encrypted at rest
internal/telemetry/    recorder, on-disk buffer, reporting loop
internal/tray/         what the menu bar shows (toolkit-free, tested)
extension/             the browser extension; holds no engine (see Browser extension)
pkg/pii/               the catalogue: what is sensitive, how it is recognised
pkg/telemetry/         the public supervision contract the backend imports
install.sh             installer: binaries, launchd services, optional shell line
```

## Commands

```bash
make build            # bin/neverseen
make test             # go test -race ./...
make test-cover       # + coverage; the CI gate is 80%
make lint             # golangci-lint
make fmt              # gofmt -w . && go mod tidy
make score            # gate per-category accuracy against score-baseline.json
make score-update     # rewrite that floor from the current run — explain the delta
make bench-accuracy   # the per-category accuracy report
make e2e-claude       # real CLI, real provider, through the agent (spends quota)
make extension        # typecheck, test and build the browser extension
make extension-e2e    # drive it through a real Chrome against a real agent
make contract-update  # re-record the extension/agent exchanges — explain the delta

go test ./internal/detector/ -run TestAccuracyCorpus -v   # one suite, verbose
```

Run it and point a tool at it by naming the provider in the first path segment:

```console
$ NEVERSEEN_PII_LOCALE=fr,gb,us neverseen proxy
level=INFO msg=listening address=127.0.0.1:9787 providers=anthropic,deepinfra,gemini,...

$ ANTHROPIC_BASE_URL=http://127.0.0.1:9787/anthropic claude -p "…"
```

## The load-bearing rules

These are not style preferences. Each one is a failure that already happened, mostly in
**Agent Veil**, the MIT-licensed project this one replaces (see `NOTICE`).

- **One entrypoint assembles the pipeline.** `cmd/neverseen` is the only binary that
  masks. Agent Veil shipped two, each assembling its own pipeline from the same
  packages; they drifted, and the same request was masked in one and answered in clear
  in the other (`cmd/neverseen/main.go` package doc). `cmd/neverseen-tray` is the one
  exception and it assembles nothing — see [Distribution](operations/distribution.md).
- **The environment is read in one place per setting.** `detector.FromEnv` owns the
  detection settings, `internal/proxy/env.go` owns the proxy's. A command in `cmd/`
  reads **no** environment variable (`internal/detector/config.go:18-31`).
- **Fail closed.** A body the agent cannot read is a 415, never a pass-through
  (`internal/proxy/proxy.go:327`, `readBody` at `:457`).
- **The layer direction is one-way.** `pkg/pii` is the catalogue; `internal/detector` is
  the engine. The catalogue knows nothing about the engine.
- **`pkg/telemetry` is public and the backend imports it — never the reverse.**
- **Nothing in `internal/telemetry` may reach the request path.** A supervision backend
  that is down must never stop the masking.
- **The agent holds no API keys.** The caller's credential is forwarded untouched
  (`internal/proxy/provider.go:20-24`).
- **Counts, never content**, everywhere except `neverseen proxy -a` (a screen) and
  `-v` (a file under `traces/`) — the two deliberate exceptions, for one operator at
  their own keyboard on their own data. Neither belongs in a service definition.
- **British spelling** in comments and prose; the linter is configured for it.
- **Conventional Commits** (`feat:`, `fix:`, `docs:`, `test:`, `refactor:`).
- **Comments say *why*** and name the failure a rule prevents. A comment restating the
  code is noise.
- **Nothing enters the tree unexercised**, and **no documentation ahead of the code** —
  `TestDocumentedEnvironmentMatchesTheCode` fails in both directions
  (`cmd/neverseen/env_test.go:22`).

## Guidance for a change

1. Work out which layer owns the change: catalogue (`pkg/pii`), engine
   (`internal/detector`), request path (`internal/proxy`), supervision
   (`internal/telemetry` / `pkg/telemetry`), or distribution (`install.sh`,
   `.goreleaser.yml`).
2. Read the package doc comment first. In this repo the doc comments carry the *reason*
   the code is shaped the way it is, and most tempting simplifications are already
   recorded there as bugs that were shipped once.
3. Run `make test` (race detector) plus the gate for the area you touched — `make score`
   for detection, `go test ./pkg/telemetry/` for the contract.
4. A deliberate simplification gets a `TODO:` naming the ceiling and the upgrade path
   (existing examples: `detector.Unmask`, `minConfidence`, `vault.Memory`).
