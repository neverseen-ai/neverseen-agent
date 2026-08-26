# Cloakfleet — OpenWiki quickstart

Cloakfleet is a **workstation agent that masks sensitive values before they reach a
language model**. It sits between the AI tools people already use (Claude Code, Codex,
a script, an SDK) and the model provider: on the way out it replaces personal data and
credentials with a substitution, on the way back it restores the originals. The model
never sees the data, and the person using the tool never sees a placeholder.

Module path: `github.com/cloakfleet/cloakfleet` (see `go.mod`). Go 1.26; the only
dependencies are `fyne.io/systray` (menu bar icon) and `gopkg.in/yaml.v3` (test corpus).

**Licence:** FSL-1.1-ALv2 — *source available, not open source*. Say "source available".
Each release converts to Apache 2.0 two years after it ships (`LICENSE.md`).

The paid supervision backend is a **separate, private repository** (`cloakfleet-cloud`,
checked out alongside as `../cloakfleet-cloud`). It imports this repo; this repo must
never import it, and must compile and run with no backend at all.

## Read next

| Page | What it covers |
| --- | --- |
| [Request path](architecture/request-path.md) | `internal/proxy` — provider routing, masking a body, the session vault, response expansion, streaming |
| [Detection engine](architecture/detection-engine.md) | `pkg/pii` catalogue + `internal/detector` engine — categories, locales, checksums, overlap arbitration, token vs. stand-in |
| [Supervision](architecture/supervision.md) | `pkg/telemetry` contract + `internal/telemetry` recorder, on-disk buffer, reporting loop |
| [Configuration](operations/configuration.md) | The six environment variables, every CLI command, the test page, the audit console |
| [Distribution](operations/distribution.md) | `install.sh`, launchd services, GoReleaser, the menu bar binary, `cloakfleet env` |
| [Extending the catalogue](workflows/extending-the-catalogue.md) | Adding a PII category, a locale, a provider, a telemetry field |
| [Testing and accuracy](workflows/testing-and-accuracy.md) | The corpus, the per-category score floor, CI gates, the end-to-end test |

## Repository layout

```
cmd/cloakfleet/        the agent — the ONLY binary that masks anything
cmd/cloakfleet-tray/   menu bar icon; assembles no pipeline (see Distribution)
internal/proxy/        the request path, /healthz, /test, the audit console
internal/detector/     the engine that runs the catalogue over text
internal/vault/        per-session masked→original mapping, encrypted at rest
internal/telemetry/    recorder, on-disk buffer, reporting loop
internal/tray/         what the menu bar shows (toolkit-free, tested)
pkg/pii/               the catalogue: what is sensitive, how it is recognised
pkg/telemetry/         the public supervision contract the backend imports
install.sh             installer: binaries, launchd services, optional shell line
```

## Commands

```bash
make build            # bin/cloakfleet
make test             # go test -race ./...
make test-cover       # + coverage; the CI gate is 80%
make lint             # golangci-lint
make fmt              # gofmt -w . && go mod tidy
make score            # gate per-category accuracy against score-baseline.json
make score-update     # rewrite that floor from the current run — explain the delta
make bench-accuracy   # the per-category accuracy report
make e2e-claude       # real CLI, real provider, through the agent (spends quota)

go test ./internal/detector/ -run TestAccuracyCorpus -v   # one suite, verbose
```

Run it and point a tool at it by naming the provider in the first path segment:

```console
$ CLOAKFLEET_PII_LOCALE=fr,gb,us cloakfleet proxy
level=INFO msg=listening address=127.0.0.1:8787 providers=anthropic,deepinfra,gemini,...

$ ANTHROPIC_BASE_URL=http://127.0.0.1:8787/anthropic claude -p "…"
```

## The load-bearing rules

These are not style preferences. Each one is a failure that already happened, mostly in
**Agent Veil**, the MIT-licensed project this one replaces (see `NOTICE`).

- **One entrypoint assembles the pipeline.** `cmd/cloakfleet` is the only binary that
  masks. Agent Veil shipped two, each assembling its own pipeline from the same
  packages; they drifted, and the same request was masked in one and answered in clear
  in the other (`cmd/cloakfleet/main.go` package doc). `cmd/cloakfleet-tray` is the one
  exception and it assembles nothing — see [Distribution](operations/distribution.md).
- **The environment is read in one place per setting.** `detector.FromEnv` owns the
  detection settings, `internal/proxy/env.go` owns the proxy's. A command in `cmd/`
  reads **no** environment variable (`internal/detector/config.go:15-27`).
- **Fail closed.** A body the agent cannot read is a 415, never a pass-through
  (`internal/proxy/proxy.go:190-200`, `readBody` at `:273`).
- **The layer direction is one-way.** `pkg/pii` is the catalogue; `internal/detector` is
  the engine. The catalogue knows nothing about the engine.
- **`pkg/telemetry` is public and the backend imports it — never the reverse.**
- **Nothing in `internal/telemetry` may reach the request path.** A supervision backend
  that is down must never stop the masking.
- **The agent holds no API keys.** The caller's credential is forwarded untouched
  (`internal/proxy/provider.go:20-24`).
- **Counts, never content**, everywhere except `cloakfleet audit` — the one deliberate
  exception, one operator at their own keyboard on their own data.
- **British spelling** in comments and prose; the linter is configured for it.
- **Conventional Commits** (`feat:`, `fix:`, `docs:`, `test:`, `refactor:`).
- **Comments say *why*** and name the failure a rule prevents. A comment restating the
  code is noise.
- **Nothing enters the tree unexercised**, and **no documentation ahead of the code** —
  `TestDocumentedEnvironmentMatchesTheCode` fails in both directions
  (`cmd/cloakfleet/env_test.go:22`).

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
