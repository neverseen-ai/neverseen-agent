# Telemetry for AI governance

What a supervised agent reports about how the AI tools on a workstation are used,
why each figure is there, and what it deliberately does not say. Written for the
people who read the fleet view — a technical director asking what the tools cost
and how they are used, a security officer asking whether the control holds — and
for whoever extends the heartbeat next.

The reference for the mechanism is `openwiki/architecture/supervision.md`; the
rules are in `CLAUDE.md` under *Supervision*. This page is the summary.

## One rule, and everything follows from it

**Nothing in a heartbeat is content.** Not a prompt, not an answer, not a value the
agent masked, not a file name, not a URL. `TestHeartbeatCarriesNoContent` walks the
wire type and fails on any string field that is not on an explicit allow list, each
entry carrying its reason. That test is what lets a customer's DPO read the contract
rather than audit a database.

Every figure below was designed against that rule. Where a question needed a word
the agent observes — which client, which tool, which program — the answer is a word
from a **closed vocabulary** the contract itself carries, and everything else is
`other`. The word `python3` in a heartbeat comes from `KnownPrograms`, not from the
prompt.

## What the heartbeat answers

The agent reports every five minutes, one bucket of counters per window, batched
when the backend was unreachable. `pkg/telemetry/contract.go` is the shape;
`pkg/telemetry/testdata/heartbeats.json` is one real batch with every field
exercised.

### How much is used, by whom, on what

| Question | Field | What it holds |
|---|---|---|
| How many exchanges | `requests` | Exchanges the agent forwarded |
| To which vendors | `providers{}` | Requests per route code, answered or not |
| From which tools | `clients{}` | User-Agent reduced to a family: `claude-code`, `cursor`, `openai-sdk`, … or `other` |
| What it cost | `models{}` | Input, output, cache-write and cache-read tokens per model id — raw counts, priced by the backend |
| How the vendors answered | `upstream` | Five integers: ok, rate-limited, rejected, failed, unreachable |

A `429` is separated from the other rejections because it is the one answer that
says the organisation is at its quota rather than that a request was wrong.

### Conversations, not just requests

| Field | What it holds |
|---|---|
| `sessions.active` | Distinct conversations that sent a request in the window |
| `sessions.opened` | Conversations seen for the first time |
| `sessions.closed` | Conversations idle for thirty minutes — the mapping's own lifetime |
| `sessions.duration`, `.requests`, `.input`, `.output`, `.tool_calls`, `.masked` | One histogram each, per closed conversation |

A conversation is the session header when the client sends one, else the id Claude
Code writes into `metadata.user_id` on the way to Anthropic, else the shared default
session. The identity never leaves the agent; only the counts do.

**The agent computes no average.** "Tokens per conversation" over a five-minute
window is meaningless for conversations that last hours, and an agent computing a
mean would be choosing the statistic. Instead a conversation's totals fall into a
histogram when it closes — twenty-four buckets by power of two, bucket *i* holding
values in [2^(i−1), 2^i) — and the backend reads a median or a p95 from those. Fixed
edges rather than range labels, so the contract gains no string.

### What the model asked the workstation to do

A tool call is the one part of an answer that is neither prose nor a value: an
instruction the tool on the workstation is about to carry out.

| Field | What it holds |
|---|---|
| `tools.calls` | Tool calls in the answers |
| `tools.restored` | Calls whose arguments carried a value the agent had masked on the way out — personal data or a credential reaching a local action |
| `tools.names{}` | Per tool, from `KnownTools` (`Bash`, `Read`, `Write`, `WebFetch`, …); an MCP tool is `mcp` whatever its server |
| `tools.programs{}` | Programs a shell command named, from `KnownPrograms`: `git`, `grep`, `python3`, `curl`, `kubectl`, … |
| `tools.classes{}` | Kinds of shell command, from `CommandClasses`: `network`, `privilege`, `destructive`, `install`, `secrets`, `pipe-to-shell` |

The shell command is split on its operators, assignments and wrappers (`sudo`,
`env`, `timeout`, `xargs`) are stepped over, and the first word of each piece is
matched against the list. A class is decided from the program and its first flags
— `rm -rf`, `git push --force`, `pip install`, `aws sts`, `curl … | sh` — never
from the rest of the line. The reduction happens before the recorder's lock is
taken, and the text is dropped.

The split is deliberately not quote-aware. What that can cost is a word inside a
quoted string counted as a program *when it is on the list*; what it never costs is
a word outside the list reaching the wire.

### The control's own health

| Field | What it says |
|---|---|
| `refused` | Bodies the agent would not forward (the fail-closed 415). A steady rate is a client the agent cannot read, which will end up pointed around it |
| `policy.applied`, `policy.refused` | Changes to what the agent masks, through the one authenticated route. `state` says *what* is off; this says *when*, and how often somebody keeps trying |
| `degraded` | Tool-call arguments that never formed a document and were expanded token by token — how often the second-best path ran |
| `restarts`, `dropped` | Process starts in the window, and buckets lost to a backend outage longer than the buffer holds |

### The agent as a risk

`state` describes what the agent is applying: version, locales, substitution mode,
what is switched off, the secret level. It now also says how the agent itself is
exposed — the one thing the rest of `state` could not:

| Field | Why a security officer wants it |
|---|---|
| `console` | `-a` is printing every value in clear. Under the installer's service that console is a file: the day's prompts are on disk |
| `tracing` | `-v` is writing both bodies of every exchange to disk |
| `exposed` | The agent listens beyond loopback, where `/test` is a masking oracle for the whole network |
| `rerouted[]` | Provider codes pointed at something other than the vendor's own host — a gateway the masked traffic reaches that the dashboard would not otherwise know about |
| `allowlisted` | How many values this deployment exempts from masking — a count, never the values |

An agent applying its whole catalogue is still a risk if it is filing prompts in
clear, and the fleet view has to show that from the row.

## What it deliberately does not say

- **No content, no identifiers.** No session id, no user, no file path, no command
  line, no URL, no value. `state.addresses` — the machine's own local IP addresses —
  is the one field that is personal data, argued for in the contract and named in
  the README.
- **No price.** Prices change; the table belongs to the backend.
- **No average.** Histograms, so the backend chooses the statistic.
- **No free string.** Client families, tool names, programs and classes are closed
  lists; a name a client made up, a program nobody has heard of, a hand-written
  User-Agent is `other`. Widening a list is a contract change made in one commit
  with the backend.
- **No version per client** yet. A `TODO` in `vocabulary.go` records it: the family
  alone answers "which tools", and the major version would need a reliable read from
  every family's User-Agent.

## Rebuilding the heartbeat from traces

`neverseen replay <dir>` reads a directory of `-v` traces back through the same
recorder the agent uses and prints the batch the agent would have filed: the
conversation from `metadata.user_id`, the categories by masking the IN body again
with today's detector, the model and tokens from the trace header, the tool calls
from the answer as it arrived. Windows are aligned on the interval and dated by the
file name.

It exists to check the counters against real traffic rather than against fixtures.
It is **read-only**: the batch is printed, never queued and never sent, because a
bucket rebuilt from traces shares no window boundary with one the agent filed live,
and the backend's `(agent, window)` key would count both. Two things a trace does
not hold, so a replay never reports them: the client's User-Agent and the
provider's status. `restarts` is zero.

## For the backend

Every new field is additive and `omitempty`, so a backend on the previous shape
ignores them and keeps working; the schema version stays at 2. `cloakfleet-cloud`
does not yet store the new fields — that is the follow-up in that repository. A full
batch of sixty buckets is held at half the backend's 1 MiB body bound by
`TestAFullBatchFitsTheBackendsBodyBound`.

## Where the code is

| Concern | File |
|---|---|
| The wire shape and its documentation | `pkg/telemetry/contract.go` |
| The closed vocabularies | `pkg/telemetry/vocabulary.go` |
| The allow list of strings, and the golden | `pkg/telemetry/contract_test.go`, `testdata/heartbeats.json` |
| Counting, sessions, histograms | `internal/telemetry/recorder.go` |
| Reducing a tool call to the vocabularies | `internal/telemetry/tools.go` |
| What the request path feeds, and `State` | `internal/proxy/proxy.go`, `stream.go`, `audit.go`, `policy.go`, `telemetry.go` |
| The conversation identity | `internal/proxy/identifiers.go` (`conversationOf`) |
| The replay | `internal/proxy/replay.go`, `cmd/neverseen/main.go` (`runReplay`) |
