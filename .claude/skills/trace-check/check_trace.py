#!/usr/bin/env python3
"""Check one neverseen trace: did the masking hold, and did anything get lost.

A trace holds the two halves of an exchange as bytes — the body that arrived, the
body that left, and the answer that came back — which is exactly what is needed to
answer three questions mechanically rather than by eye:

  * every value the exchange minted was replaced *everywhere* it appeared;
  * the outbound body is the inbound one with nothing but those replacements in it,
    so no field was truncated, dropped or reordered on the way through;
  * the answer arrived whole, and every replacement in it is one the exchange sent.

What it deliberately does not claim: the trace records the answer **before** a
single replacement was expanded, so the restored text the caller actually read is
not in the file. Whether unmasking ran is checked as far as the file allows — every
replacement echoed by the model is one the mapping can expand — and the report says
so rather than implying a round trip it cannot see.

Usage:
    check_trace.py [trace.txt | -]        one file, or the newest under traces/
    check_trace.py --dir traces           the newest trace in that directory
    check_trace.py --all traces           every trace in that directory

Exit status is 1 when anything failed, 0 otherwise.
"""

from __future__ import annotations

import argparse
import difflib
import json
import os
import re
import sys
from dataclasses import dataclass, field

# A bracket token, the shape pkg/pii/token.go mints. Kept in step with tokenRe
# there: a prefix in capitals, then an index.
TOKEN_RE = re.compile(r"\[[A-Z][A-Z0-9_]*_\d+\]")

# The separator internal/proxy/trace.go writes above each body.
RULE_RE = re.compile(r"^── (.+?) ─+\s*$")

# The prefixed fields appended once the answer arrived. They sit between the OUT
# body and the BACK rule, so they have to come off the end of that body.
TIMING_RE = re.compile(r"^(back|mask|upstream|delivering|unmask|model|tokens): ")

MASK_RE = re.compile(r"^MASK (.+) TO (.+)$")

FAIL, WARN, OK, INFO = "FAIL", "WARN", "OK", "INFO"

# How long an *inferred* replacement span may be before it is worth a second look.
# A masked value is an email, an address, a key — none of them runs to a paragraph,
# so a longer inference is more likely a tail this check absorbed than a value.
#
# TODO: only the inference is bounded. A replacement minted in an earlier exchange
# and sitting at the very end of a field has nothing after it to anchor against, so
# a tail lost there cannot be distinguished from a longer value. The upgrade path is
# the session's earlier traces, which hold the MASK line this one lacks.
absorbable = 128


@dataclass
class Finding:
    level: str
    title: str
    detail: str = ""


@dataclass
class Trace:
    path: str
    header: dict[str, str] = field(default_factory=dict)
    # original -> replacement, for the values this exchange minted. Values the
    # session had already seen carry no MASK line, which is why nothing below
    # treats an empty mapping as "nothing was replaced".
    minted: dict[str, str] = field(default_factory=dict)
    sent_in: str = ""
    sent_out: str = ""
    reassembled: str = ""
    back: str = ""
    provider: str = ""


def parse(path: str) -> Trace:
    with open(path, encoding="utf-8", errors="replace") as f:
        lines = f.read().split("\n")

    t = Trace(path=path)
    sections: dict[str, list[str]] = {}
    current: list[str] | None = None

    for line in lines:
        rule = RULE_RE.match(line)
        if rule:
            label = rule.group(1).strip()
            current = sections.setdefault(label, [])
            continue

        if current is None:
            # The header, and the MASK lines under it.
            if mask := MASK_RE.match(line):
                t.minted[mask.group(1)] = mask.group(2)
            elif ":" in line and not line.startswith(" "):
                key, _, value = line.partition(":")
                t.header[key.strip()] = value.strip()
            continue

        current.append(line)

    for label, body in sections.items():
        raw = "\n".join(body)
        text = raw.strip("\n")
        if label.startswith("IN "):
            t.sent_in = text
        elif label.startswith("OUT "):
            # What the exchange cost is appended once the answer arrived, so those
            # fields sit *after* the OUT rule and land at the end of this section
            # rather than in the header at the top. Read here or not at all.
            t.sent_out, appended = split_appended(text)
            t.header.update(appended)
        elif "reassembled" in label:
            t.reassembled = text
        elif label.startswith("BACK "):
            # Not stripped, because a stream *ends* on a blank line: the separator
            # after its last event is part of the body, and dropping it made the
            # section two bytes short of the size the header declares — a truncation
            # this check reported on every healthy trace. Only the single newline
            # traceResponse writes after the body comes off.
            t.back = raw[:-1] if raw.endswith("\n") else raw
            t.provider = label[len("BACK from "):].strip()

    t.provider = t.provider or t.header.get("provider", "")
    return t


def split_appended(text: str) -> tuple[str, dict[str, str]]:
    """Separate the OUT body from the cost fields appended below it."""
    lines = text.split("\n")
    fields: dict[str, str] = {}
    while lines and (not lines[-1].strip() or TIMING_RE.match(lines[-1])):
        line = lines.pop()
        if TIMING_RE.match(line):
            key, _, value = line.partition(":")
            fields[key.strip()] = value.strip()
    return "\n".join(lines), fields


# Memoised because the checks below ask the same three bodies the same questions
# several times each, and a body is regularly hundreds of kilobytes: recomputing a
# character-by-character scan six times over a coding tool's request is most of the
# runtime, over a directory of traces all of it.
_wire_cache: dict[int, str] = {}
_json_cache: dict[int, bool] = {}


def wire(body: str) -> str:
    """The body as it went over the wire.

    trace.go writes a JSON body through json.Indent, which only ever inserts
    whitespace *between* tokens — so removing whitespace outside strings restores
    the bytes exactly, duplicate keys, key order, escapes and all. A body that is
    not JSON was written as it arrived and must not be touched.
    """
    key = id(body)
    if key in _wire_cache:
        return _wire_cache[key]

    if not is_json(body):
        _wire_cache[key] = body
        return body

    out: list[str] = []
    in_string = escaped = False
    for ch in body:
        if in_string:
            out.append(ch)
            if escaped:
                escaped = False
            elif ch == "\\":
                escaped = True
            elif ch == '"':
                in_string = False
        elif ch == '"':
            in_string = True
            out.append(ch)
        elif ch not in " \t\n\r":
            out.append(ch)

    _wire_cache[key] = "".join(out)
    return _wire_cache[key]


def is_json(body: str) -> bool:
    key = id(body)
    if key in _json_cache:
        return _json_cache[key]
    try:
        json.loads(body)
        _json_cache[key] = True
    except (ValueError, RecursionError):
        _json_cache[key] = False
    return _json_cache[key]


def strings_of(body: str) -> list[tuple[str, str]] | None:
    """Every string value in a JSON document, in reading order, with its path.

    object_pairs_hook keeps the order and the duplicates, which is the shape
    jsonbody.go goes to some trouble to preserve — a walker that collapsed them
    would compare two documents the agent deliberately kept different.
    """
    try:
        doc = json.loads(body, object_pairs_hook=lambda pairs: ("__obj__", pairs))
    except (ValueError, RecursionError):
        return None

    found: list[tuple[str, str]] = []

    def walk(node, path: str) -> None:
        if isinstance(node, str):
            found.append((path, node))
        elif isinstance(node, tuple) and node and node[0] == "__obj__":
            for key, value in node[1]:
                walk(value, f"{path}.{key}" if path else key)
        elif isinstance(node, list):
            for i, item in enumerate(node):
                walk(item, f"{path}[{i}]")

    walk(doc, "")
    return found


def shape_of(body: str) -> str | None:
    """The document's skeleton: its key paths and container sizes, no values."""
    try:
        doc = json.loads(body, object_pairs_hook=lambda pairs: ("__obj__", pairs))
    except (ValueError, RecursionError):
        return None

    parts: list[str] = []

    def walk(node, path: str) -> None:
        if isinstance(node, tuple) and node and node[0] == "__obj__":
            parts.append(f"{path}{{{len(node[1])}}}")
            for key, value in node[1]:
                walk(value, f"{path}.{key}" if path else key)
        elif isinstance(node, list):
            parts.append(f"{path}[{len(node)}]")
            for i, item in enumerate(node):
                walk(item, f"{path}[{i}]")
        else:
            parts.append(f"{path}:{type(node).__name__}")

    walk(doc, "")
    return "\n".join(parts)


def restore(text: str, reverse: dict[str, str]) -> str:
    """Put the originals back, longest replacement first.

    Longest first for the reason detector.UnmaskSeen walks rather than replaces per
    entry: one stand-in can contain another, and expanding the shorter one first
    puts a value inside a value.
    """
    if not reverse:
        return text
    for replacement in sorted(reverse, key=len, reverse=True):
        text = text.replace(replacement, reverse[replacement])
    return text


# ── the checks ────────────────────────────────────────────────────────────────


def check_sizes(t: Trace) -> list[Finding]:
    out: list[Finding] = []
    for name, body in (("in", t.sent_in), ("out", t.sent_out), ("back", t.back)):
        declared = t.header.get(name)
        if declared is None or not body:
            continue
        want = int(declared.split()[0])
        got = len(wire(body).encode("utf-8"))
        if got != want:
            out.append(Finding(
                FAIL, f"the {name.upper()} body is not the size the header declares",
                f"header says {want} bytes, the section holds {got} — "
                f"{want - got:+d}. The body was truncated on disk, or the caller "
                f"hung up mid-write."))
    if not out:
        out.append(Finding(OK, "every body is exactly the size its header declares"))
    return out


def check_shape(t: Trace) -> list[Finding]:
    a, b = shape_of(wire(t.sent_in)), shape_of(wire(t.sent_out))
    if a is None or b is None:
        if is_json(t.sent_in) != is_json(t.sent_out):
            return [Finding(FAIL, "one request body is JSON and the other is not",
                            "masking must not change what a body is.")]
        return [Finding(INFO, "the request body is not JSON",
                        "it was masked as flat text, so no structural check applies.")]

    if a == b:
        return [Finding(OK, "the outbound document has the caller's shape",
                        "same key paths, same order, same array lengths.")]

    diff = "\n".join(list(difflib.unified_diff(
        a.split("\n"), b.split("\n"), "IN", "OUT", lineterm="", n=1))[:40])
    return [Finding(FAIL, "the outbound document is not shaped like the inbound one",
                    "a key, an order or an array length changed — the agent rewrites "
                    "values, never the shape around them.\n" + diff)]


def check_masking(t: Trace) -> list[Finding]:
    """The whole point: OUT must be IN with nothing but replacements in it."""
    out: list[Finding] = []
    reverse = {v: k for k, v in t.minted.items()}

    ins, outs = strings_of(wire(t.sent_in)), strings_of(wire(t.sent_out))
    if ins is None or outs is None:
        pairs = [("the body", t.sent_in, t.sent_out)]
    elif len(ins) != len(outs):
        return [Finding(FAIL, "the two request bodies hold a different number of strings",
                        f"IN has {len(ins)}, OUT has {len(outs)}. A string was added "
                        f"or dropped, which masking never does.")]
    elif shape_of(wire(t.sent_in)) != shape_of(wire(t.sent_out)):
        # Fields are paired by position, so two documents of different shapes pair
        # every field with the wrong one and each comparison then fails for a reason
        # that is not the one to fix. Reordering `model` and `messages` produced one
        # real finding and three invented truncations on top of it — and on a genuine
        # jsonbody bug those are exactly what would bury the cause.
        return [Finding(INFO, "the fields were not compared",
                        "the two documents are not shaped alike, so pairing them by "
                        "position would compare unrelated fields. Fix the shape "
                        "finding above and run this again.")]
    else:
        pairs = [(pa, sa, sb) for (pa, sa), (_, sb) in zip(ins, outs)]

    exact = explained = untouched = 0
    unbounded: list[str] = []
    for path, a, b in pairs:
        if a == b:
            untouched += 1
            continue

        # The file's own MASK lines put back every original this exchange minted,
        # and the result has to be the inbound text byte for byte. That is the
        # whole round trip checked on bytes — the replacements and everything
        # around them.
        #
        # Restoring *first* is what makes the fallback below sound: a known
        # replacement is substituted rather than guessed, so it cannot absorb a
        # truncated tail. Guessing all of them let `…et à [EMAIL_1]` swallow a
        # missing ", merci." as part of the value, and a cut field read as clean.
        restored = restore(b, reverse)
        if restored == a:
            exact += 1
            continue

        # What is left is replacements minted in an earlier exchange, for which the
        # file holds no original. Everything else can still be checked: the text
        # between them has to be the text that arrived, in order, covering the
        # inbound string end to end. A truncation anywhere else shows up here.
        ok, spans, unanchored = explains(a, restored)
        if ok:
            explained += 1
            # Only the *last* span can hide a truncation, and only when the field
            # ends on a replacement: an interior span is pinned by the literal text
            # on both sides, and a trailing literal is checked with endswith. Warning
            # on length alone reported every long credential the agent masks
            # correctly — a 248-byte Atlassian token inside a [CONN_STR_1], anchored
            # at both ends, which is the engine working.
            if unanchored and spans and spans[-1][1] - spans[-1][0] > absorbable:
                unbounded.append(path)
            continue

        out.append(Finding(
            FAIL, f"a value changed in a way no replacement explains: {path}",
            "the text around the replacements is not the text that arrived — "
            "something was truncated or rewritten.\n" + inline_diff(a, b)))

    if exact:
        out.append(Finding(OK, f"{exact} field(s) reverse exactly to what arrived",
                           "the MASK lines put the originals back and the result is "
                           "the inbound text byte for byte."))
    if explained:
        out.append(Finding(
            OK, f"{explained} field(s) carry replacements this exchange did not mint",
            "the session had already seen those values, so the file holds no original "
            "to put back. Everything around them is byte-identical to what arrived "
            "and each replacement covers a non-empty span, so nothing was truncated "
            "— but which value each stands for is not in this file. Read the "
            "session's earlier traces, or replay in a fresh session."))
    if unbounded:
        out.append(Finding(
            WARN, f"{len(unbounded)} field(s) end on a replacement standing for an "
            f"unusually long span",
            "the field ends on a value this exchange did not mint, so there is no "
            "text after it to pin it down and a truncated tail would look the same "
            "as a longer value. Over " + str(absorbable) + " bytes is worth reading "
            "by hand: " + ", ".join(unbounded[:6])))
    if untouched == len(pairs):
        out.append(Finding(INFO, "nothing in this body was replaced",
                           "either it held nothing the catalogue recognises, or the "
                           "categories that would have matched are switched off."))
    return out


def explains(original: str, masked: str) -> tuple[bool, list[tuple[int, int]], bool]:
    """Is `masked` exactly `original` with each replacement standing for one span?

    The literal text between the replacements carries the proof: it has to appear
    in the original, in order, starting on the first byte and ending on the last. A
    truncated tail leaves the closing literal short of the end; a dropped field
    leaves a literal that cannot be found at all.

    It also reports whether the last span is *unanchored* — the field ends on a
    replacement, so there is no trailing literal to pin it and a lost tail cannot be
    told from a longer value. That is the only span a truncation can hide in.

    The spans it returns are the values it inferred each replacement stands for.
    They are counted, never printed: this report gets pasted into places a trace
    does not go.
    """
    literals = TOKEN_RE.split(masked)
    if len(literals) == 1:
        return masked == original, [], False
    if not original.startswith(literals[0]) or not original.endswith(literals[-1]):
        return False, [], False

    pos, end = len(literals[0]), len(original) - len(literals[-1])
    if end < pos:
        return False, [], False

    spans: list[tuple[int, int]] = []
    for literal in literals[1:-1]:
        if not literal:
            # Two replacements with nothing between them: nothing says where one
            # value ended and the next began.
            return False, [], False
        found = original.find(literal, pos, end)
        if found < 0:
            return False, [], False
        spans.append((pos, found))
        pos = found + len(literal)

    spans.append((pos, end))
    # A replacement stands for something, so no span may be empty.
    return all(hi > lo for lo, hi in spans), spans, literals[-1] == ""


def inline_diff(a: str, b: str, width: int = 220) -> str:
    matcher = difflib.SequenceMatcher(None, a, b, autojunk=False)
    lines: list[str] = []
    for tag, i1, i2, j1, j2 in matcher.get_opcodes():
        if tag == "equal":
            continue
        lines.append(f"    IN  [{i1}:{i2}] {a[i1:i2][:width]!r}")
        lines.append(f"    OUT [{j1}:{j2}] {b[j1:j2][:width]!r}")
        if len(lines) >= 12:
            lines.append("    …")
            break
    return "\n".join(lines)


def check_residue(t: Trace) -> list[Finding]:
    """A minted value still present in the outbound body is a leak."""
    body = t.sent_out
    leaked = [original for original in t.minted if original in body]
    if leaked:
        return [Finding(
            FAIL, f"{len(leaked)} value(s) were replaced in one place and not another",
            "each of these has a MASK line, so the exchange decided it was "
            "sensitive — and an occurrence still left in clear:\n" +
            "\n".join(f"    {v[:80]!r} -> {t.minted[v]}" for v in leaked[:10]))]
    if t.minted:
        return [Finding(OK, f"none of the {len(t.minted)} minted value(s) survives in the outbound body")]
    return []


def check_count(t: Trace) -> list[Finding]:
    declared = t.header.get("replaced", "")
    match = re.match(r"(\d+) value\(s\), (\d+) of them first seen", declared)
    if not match:
        return []
    count, minted = int(match.group(1)), int(match.group(2))

    if minted != len(t.minted):
        return [Finding(FAIL, "the header and the MASK lines disagree",
                        f"the header claims {minted} value(s) first seen here, "
                        f"{len(t.minted)} MASK line(s) are listed.")]

    occurrences = sum(t.sent_out.count(r) for r in set(t.minted.values()))
    if t.minted and occurrences < minted:
        return [Finding(FAIL, "a minted replacement is not in the outbound body",
                        f"{minted} value(s) were minted, {occurrences} occurrence(s) "
                        f"of their replacements reached the provider.")]
    return [Finding(OK, f"{count} replacement(s) reported, {len(t.minted)} minted here")]


def check_answer(t: Trace) -> list[Finding]:
    if not t.back:
        return [Finding(WARN, "the trace holds no answer",
                        "the outbound half is written before the request leaves, so "
                        "this is an exchange the provider never answered — or one "
                        "still in flight.")]

    out: list[Finding] = []
    raw = t.back

    if "data:" in raw:
        out.extend(check_stream(raw))
    elif is_json(raw):
        out.append(Finding(OK, "the answer is one whole JSON document"))
    else:
        out.append(Finding(WARN, "the answer is neither a stream nor valid JSON",
                           "it may have been cut short. First 200 bytes:\n    "
                           + raw[:200].replace("\n", "\\n")))
    return out


def check_stream(raw: str) -> list[Finding]:
    """An SSE event is a name line and a data line, and it travels as both."""
    out: list[Finding] = []
    lines = raw.split("\n")

    orphans: list[int] = []
    bad_json: list[str] = []
    names: list[str] = []
    for i, line in enumerate(lines):
        if line.startswith("event:"):
            names.append(line[len("event:"):].strip())
            # The data line follows, possibly after nothing else.
            nxt = next((l for l in lines[i + 1:i + 3] if l.strip()), "")
            if not nxt.startswith("data:"):
                orphans.append(i + 1)
        elif line.startswith("data:"):
            payload = line[len("data:"):].strip()
            if payload and payload != "[DONE]" and not is_json(payload):
                bad_json.append(payload[:120])

    if orphans:
        out.append(Finding(
            FAIL, f"{len(orphans)} event name(s) arrived with no data line",
            "a client dispatches on `event: <name>` and parses the `data:` under it; "
            "a name with nothing beneath it is the empty string, and the caller "
            f"reports a JSON parse error. Lines: {orphans[:10]}"))
    if bad_json:
        out.append(Finding(FAIL, f"{len(bad_json)} data line(s) are not valid JSON",
                           "a value spliced into a document's source rather than "
                           "encoded into it ends the string it landed in.\n" +
                           "\n".join(f"    {p!r}" for p in bad_json[:5])))

    if "message_stop" in raw or "[DONE]" in raw:
        out.append(Finding(OK, f"the stream ends on its stop event ({len(names)} events)"))
    else:
        out.append(Finding(WARN, "the stream carries no stop event",
                           "the answer was cut short, or the caller hung up."))

    if not orphans and not bad_json:
        out.append(Finding(OK, "every event carries a name and a well-formed data line"))
    return out


def check_expansion(t: Trace) -> list[Finding]:
    """What the file can say about the way back."""
    if not t.back:
        return []

    out: list[Finding] = []
    sent = set(TOKEN_RE.findall(t.sent_out))
    echoed = set(TOKEN_RE.findall(t.back)) | set(TOKEN_RE.findall(t.reassembled))

    known = sorted(echoed & (sent | set(t.minted.values())))
    unknown = sorted(echoed - sent - set(t.minted.values()))

    if known:
        out.append(Finding(
            INFO, f"the answer echoed {len(known)} replacement(s) the exchange sent up",
            "these are what the caller's copy had to have expanded: "
            + ", ".join(known[:12]) +
            "\n    The trace records the answer *before* expansion by design, so the "
            "restored text is not in this file. Run the agent with -a to see each "
            "UNMASK line, or assert on the client's own output."))
    else:
        out.append(Finding(INFO, "the answer echoed no replacement",
                           "nothing had to be restored on the way back, so this file "
                           "cannot say whether expansion works."))

    if unknown:
        out.append(Finding(
            WARN, f"the answer holds {len(unknown)} token(s) this exchange never sent",
            "the mapping has nothing to expand them to, so the caller reads them "
            "literally. Either the model invented the shape, or they belong to a "
            "mapping this session has lost — changing the substitution mode clears "
            "it.\n    " + ", ".join(unknown[:12])))

    # A token cut in half is what TailLen and the held-back tail exist to prevent.
    # In the reassembled view the pieces are already back together, so a fragment
    # there is a fragment that was actually lost.
    if t.reassembled:
        fragments = re.findall(r"\[[A-Z][A-Z0-9_]*_?(?![A-Z0-9_]*\d+\])", t.reassembled)
        if fragments:
            out.append(Finding(
                WARN, f"{len(fragments)} bracket fragment(s) in the reassembled answer",
                "a token split across two events and put back together short is the "
                "failure a held-back tail exists to prevent. Check them by hand — an "
                "ordinary '[' in prose looks the same.\n    "
                + ", ".join(sorted(set(fragments))[:12])))
    return out


# ── the report ────────────────────────────────────────────────────────────────


def report(t: Trace) -> int:
    findings: list[Finding] = []
    for check in (check_sizes, check_shape, check_masking, check_residue,
                  check_count, check_answer, check_expansion):
        findings.extend(check(t))

    print(f"\n{os.path.basename(t.path)}")
    print(f"  session {t.header.get('session', '?')} · provider "
          f"{t.header.get('provider', '?')} · {t.header.get('replaced', '?')}")
    for key in ("mask", "upstream", "delivering", "unmask", "model", "tokens"):
        if key in t.header:
            print(f"  {key}: {t.header[key]}")
    print()

    order = {FAIL: 0, WARN: 1, OK: 2, INFO: 3}
    for f in sorted(findings, key=lambda f: order[f.level]):
        print(f"  [{f.level}] {f.title}")
        if f.detail:
            for line in f.detail.split("\n"):
                print(f"         {line}")
    fails = sum(1 for f in findings if f.level == FAIL)
    warns = sum(1 for f in findings if f.level == WARN)
    print(f"\n  {fails} failed, {warns} to look at")
    return fails


def dump_values(t: Trace, offset: int, limit: int) -> None:
    """Print the outbound body's string values, for a reader that is not a regex.

    This is the half a script must not attempt. Everything above checks that the
    exchange is *consistent with itself* — the values it decided to replace were
    replaced everywhere, and nothing else moved. It says nothing about a value the
    catalogue never recognised, and it cannot: a checker re-running the engine's own
    patterns agrees with the engine by construction, including where the engine is
    blind. Only a reader that is not those patterns can find what they missed.

    So the values go out as text for a model to read. Deduplicated, because a system
    prompt repeats itself and the same string reviewed forty times is forty times the
    cost for one answer. Never truncated, because the value somebody is looking for
    is exactly as likely to sit at the end of a forty-kilobyte field as at the start
    — use --offset and --limit to walk a large body in passes instead, and the
    footer says how much is left so a partial read cannot be mistaken for a whole
    one.
    """
    strings = strings_of(wire(t.sent_out))
    if strings is None:
        strings = [("the body", t.sent_out)]

    seen: set[str] = set()
    distinct: list[tuple[str, str]] = []
    for path, value in strings:
        if not value.strip() or value in seen:
            continue
        seen.add(value)
        distinct.append((path, value))

    window = distinct[offset:offset + limit] if limit else distinct[offset:]
    total = sum(len(v) for _, v in distinct)

    print(f"# {os.path.basename(t.path)} — the body that went to "
          f"{t.provider or 'the provider'}")
    print(f"# {len(distinct)} distinct string value(s), {total} chars; "
          f"showing {offset}..{offset + len(window)}")
    print("# Anything sensitive still in clear here is a value the catalogue never "
          "recognised.")
    print("# Replacements — [CATEGORY_n] — are values it did recognise: not findings.")
    for path, value in window:
        print(f"\n--- {path} ({len(value)} chars)")
        print(value)

    remaining = len(distinct) - (offset + len(window))
    if remaining > 0:
        print(f"\n# {remaining} value(s) not shown. Continue with "
              f"--values --offset {offset + len(window)}.")


def newest(directory: str) -> str:
    traces = [os.path.join(directory, n) for n in os.listdir(directory)
              if n.endswith(".txt")]
    if not traces:
        sys.exit(f"no trace in {directory}")
    return max(traces, key=os.path.getmtime)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("trace", nargs="?", help="a trace file; default is the newest")
    ap.add_argument("--dir", default="traces", help="where the traces are (default: traces)")
    ap.add_argument("--all", action="store_true", help="check every trace in --dir")
    ap.add_argument("--values", action="store_true",
                    help="print the outbound body's string values for a model to "
                         "read, instead of checking; this is how a value the "
                         "catalogue never recognised gets found")
    ap.add_argument("--offset", type=int, default=0, help="with --values, skip this many")
    ap.add_argument("--limit", type=int, default=0, help="with --values, show at most this many")
    args = ap.parse_args()

    if args.all:
        paths = sorted(os.path.join(args.dir, n) for n in os.listdir(args.dir)
                       if n.endswith(".txt"))
    elif args.trace:
        paths = [args.trace]
    else:
        paths = [newest(args.dir)]

    if args.values:
        for path in paths:
            dump_values(parse(path), args.offset, args.limit)
        return 0

    fails = 0
    for path in paths:
        fails += report(parse(path))
        # Keyed on id(), so they must not outlive the strings they were built from:
        # a freed body's id can be handed straight to the next trace's, and the
        # cache would answer for the wrong document.
        _wire_cache.clear()
        _json_cache.clear()
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main())
