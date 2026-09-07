---
name: trace-check
description: Check a neverseen trace from traces/ — did the masking hold everywhere, is the outbound body the inbound one with nothing but replacements in it, did the answer arrive whole. Use when asked to verify a trace, check whether masking or unmasking worked, whether a value leaked, or whether a body or an answer was truncated. Triggers on "vérifie la trace", "check the last trace", "un secret a-t-il été oublié", "le texte a-t-il été tronqué", "traces/*.txt".
---

# Checking a trace

A trace (`traces/*.txt`, written by `neverseen proxy -v`) holds one exchange as
bytes: the body that arrived, the body that left, and the answer that came back.
That is enough to settle three questions mechanically. Do not read the file by eye
first — a coding tool's request is hundreds of kilobytes of one-line JSON, and
skimming it answers nothing while costing a great deal.

## Run the checker

```bash
python3 .claude/skills/trace-check/check_trace.py            # the newest trace
python3 .claude/skills/trace-check/check_trace.py traces/20260831T193047-0039-default-anthropic.txt
python3 .claude/skills/trace-check/check_trace.py --all traces
```

No dependencies, Python 3.9+. Exit status is 1 when anything failed. Report the
findings to the user — do not paste the whole trace back at them.

After changing the checker, run its fault injection:

```bash
python3 .claude/skills/trace-check/selftest.py
```

Ten traces, each carrying one real failure, plus three that must stay silent. It
exists because this checker was green on four faults before it did: an absorbed
tail, unread cost fields, a stripped SSE separator, and a cascade of invented
findings behind a shape change. **A case asserting silence is half the suite** — a
set of failures alone passes on a checker that reports everything.

## What each finding means

| Finding | What it says |
|---|---|
| `the … body is not the size the header declares` | The section on disk is shorter than the header's byte count. The body was truncated, or the caller hung up mid-write. On a stream this is the strongest truncation signal there is. |
| `the outbound document is not shaped like the inbound one` | A key, a key order or an array length changed. The agent rewrites values, never the shape around them — this is a bug in `jsonbody.go`. |
| `n field(s) reverse exactly to what arrived` | The best possible result. The trace's own `MASK` lines put every original back and the result is the inbound text byte for byte, so nothing outside a replacement moved. |
| `n field(s) carry replacements this exchange did not mint` | Also fine. The session had already seen those values, so the file holds no original to put back; everything *around* them is byte-identical. Which value each replacement stands for is not in this file. |
| `a value changed in a way no replacement explains` | **The truncation finding.** The text between the replacements is not the text that arrived. The diff points at the byte range. |
| `n value(s) were replaced in one place and not another` | **The leak finding.** A value has a `MASK` line — the exchange decided it was sensitive — and an occurrence still went out in clear. |
| `n event name(s) arrived with no data line` | A client dispatches on `event: <name>` and parses the `data:` under it; a name with nothing beneath is the empty string and kills the answer. See the `rewrite`/`takeName` rule in CLAUDE.md. |
| `n data line(s) are not valid JSON` | A value was spliced into a document's source rather than encoded into it — the `jsonFragment` failure. |
| `the stream carries no stop event` | The answer was cut short, or the caller hung up. |
| `the answer holds n token(s) this exchange never sent` | The mapping has nothing to expand them to, so the caller reads `[EMAIL_1]` literally. Either the model invented the shape, or the session lost its mapping — changing the substitution mode clears it. |
| `n field(s) end on a replacement standing for an unusually long span` | The field *ends* on a replacement this exchange did not mint, so no text follows it to pin it down and a lost tail looks the same as a longer value. Over 128 bytes, read it by hand. A replacement with text after it is anchored and never warns — a 248-byte credential masked correctly is not a finding. |

## Pass two: what the catalogue never recognised — you read this, not a script

Pass one is deterministic and it stops exactly where the engine's own knowledge
stops. **It contains no detection regex at all** — one expression for the token
shape, and nothing else — because a checker that re-ran the catalogue's patterns
would agree with the catalogue by construction, *including everywhere the catalogue
is blind*. A missed value is missed identically by both, and the report goes green.

Finding those is the model's job, and only the model's:

```bash
python3 .claude/skills/trace-check/check_trace.py --values traces/…txt
python3 .claude/skills/trace-check/check_trace.py --values --offset 40 --limit 40 traces/…txt
```

This prints the **outbound** body's distinct string values — what actually reached
the provider — for you to read. Then:

1. Read them as a person would. Name anything that is personal data or a
   credential and is still in clear: an address, a name beside an identifier, a
   token in a shape the catalogue has no pattern for, a value in a language or
   format no loaded locale covers.
2. `[CATEGORY_n]` is a value the catalogue *did* recognise. Not a finding.
3. Before reporting anything as a detector gap, check the policy — a switched-off
   category explains a value in clear completely:
   `curl -s localhost:8787/healthz | python3 -m json.tool`, and remember
   `~/.neverseen/policy.json` wins over the environment.
4. A real gap is a new corpus case first, then a pattern. The procedure is
   `openwiki/workflows/extending-the-catalogue.md` — and a corpus case that must
   come out **untouched** goes in beside it, or recall alone passes a pattern
   widened to match everything.

Mind the cost: a coding tool's request runs to hundreds of kilobytes, most of it
source code and system prompt. Walk it in windows with `--offset`/`--limit` and say
how far you got — the footer counts what is left, so a partial read is never
mistaken for a whole one.

## What the trace cannot tell you

**Unmasking is only half-checkable here, and saying otherwise would be wrong.**
The recorder wraps the answer *before* the rehydrator does, deliberately — the file
holds what the provider sent, so it carries no restored value. So the checker can
say that every replacement the answer echoed is one the mapping can expand, and it
cannot show the text the caller actually read.

To close that half, one of:

- `neverseen proxy -a` prints an `UNMASK` line per expansion, live;
- assert on the client's own output — `extension/e2e/run.test.mjs` is the model for
  this: it checks what the site received *and* what the page rendered, because
  either alone passes over a page that was never touched.

**A clean pass one is not "nothing leaked".** It says the values the exchange
decided to replace were replaced consistently, and that nothing else moved. A value
the catalogue never recognised sits in *both* bodies and pass one has no opinion on
it — that is what pass two above is for, and skipping it leaves the question
unanswered rather than answered "no".

One thing that is in clear on purpose: the identifiers a client uses to name itself
to Anthropic — `session_id`, `device_id`, `account_uuid` — are exempted for that
provider only. See `internal/proxy/identifiers.go`. Do not report them as a leak.

## When you change the trace format

`internal/proxy/trace.go` is the only writer, and the checker parses it: the rule
lines, the `MASK … TO …` lines, the header fields and the cost fields appended
after the OUT body. Change the layout there and this breaks — the section labels
(`IN `, `OUT `, `BACK from …`, `…, reassembled`) and the prefixed field names are
the contract.
