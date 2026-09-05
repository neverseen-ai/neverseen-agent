#!/usr/bin/env python3
"""Fault injection for check_trace.py: every fixture is one real failure.

A checker that is green on everything is worth nothing, and this one was green on
four faults before these fixtures existed — an absorbed tail, unread cost fields, a
stripped SSE separator, and a cascade of invented findings behind a shape change.
So each case below builds a trace carrying exactly one fault and asserts the
finding, and `clean` asserts silence, because a suite of failures alone passes on a
checker that reports everything.

    python3 .claude/skills/trace-check/selftest.py

Exit status is 1 on the first case that does not behave.
"""

from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).parent
COMPACT = {"ensure_ascii": False, "separators": (",", ":")}

BACK = ('event: message_start\ndata: {"type":"message_start"}\n\n'
        'event: content_block_delta\ndata: {"delta":{"text":"J\'ai écrit à [EMAIL_1]."}}\n\n'
        'event: message_stop\ndata: {"type":"message_stop"}\n\n')

DELTA = ('event: content_block_delta\n'
         'data: {"delta":{"text":"J\'ai écrit à [EMAIL_1]."}}\n')

# An answer echoing nothing, for the cases whose subject is the request body. BACK
# above carries [EMAIL_1], which those bodies never send — the answer would then
# hold a token the exchange did not send, a second finding the case is not about.
PLAIN_BACK = ('event: message_start\ndata: {"type":"message_start"}\n\n'
              'event: message_stop\ndata: {"type":"message_stop"}\n\n')


def load():
    """Import the checker beside this file."""
    spec = importlib.util.spec_from_file_location("check_trace", HERE / "check_trace.py")
    module = importlib.util.module_from_spec(spec)
    # Registered before exec, or @dataclass cannot find the module it belongs to.
    sys.modules["check_trace"] = module
    spec.loader.exec_module(module)
    return module


def rule(label: str, width: int = 78) -> str:
    head = f"── {label} "
    return head + "─" * max(0, width - len(head))


def trace(body_in: str, body_out: str, back: str, masks: list[tuple[str, str]],
          declared_back: int | None = None) -> str:
    """One trace file, laid out exactly as internal/proxy/trace.go writes it."""
    s = (f"session:  s1\nprovider: anthropic\n"
         f"in:       {len(body_in.encode())} bytes\n"
         f"out:      {len(body_out.encode())} bytes\n"
         f"replaced: 2 value(s), {len(masks)} of them first seen in this session\n")
    if masks:
        s += "\n" + "".join(f"MASK {a} TO {b}\n" for a, b in masks)
    s += f"\n{rule('IN   from the tool')}\n"
    s += json.dumps(json.loads(body_in), indent=2, ensure_ascii=False) + "\n"
    s += f"\n{rule('OUT  to anthropic')}\n"
    s += json.dumps(json.loads(body_out), indent=2, ensure_ascii=False) + "\n"
    s += (f"\nback:       {declared_back if declared_back is not None else len(back.encode())} bytes\n"
          f"mask:       1ms\nupstream:   2s\ndelivering: 3s\nunmask:     1ms\n")
    s += f"\n{rule('BACK from anthropic')}\n{back}\n"
    return s


def cases() -> list[tuple[str, str, str | None]]:
    """Each case is (name, file contents, the finding it must produce or None)."""
    plain = {"model": "m", "messages": [
        {"role": "user", "content": "Écrire à claire@example.fr et à claire@example.fr, merci."}]}
    masked = {"model": "m", "messages": [
        {"role": "user", "content": "Écrire à [EMAIL_1] et à [EMAIL_1], merci."}]}
    raw_in = json.dumps(plain, **COMPACT)
    raw_out = json.dumps(masked, **COMPACT)
    masks = [("claire@example.fr", "[EMAIL_1]")]

    # A long value with no MASK line, so its span has to be inferred.
    long_secret = "ATATT3xFfGF" + "a1B2c3D4e5" * 24
    def field(text: str) -> str:
        return json.dumps({"messages": [{"content": text}]}, **COMPACT)

    return [
        ("clean", trace(raw_in, raw_out, BACK, masks), None),

        # The tail after the last replacement is gone.
        ("truncated", trace(raw_in, raw_out.replace(", merci.", ""), BACK, masks),
         "a value changed in a way no replacement explains"),

        # Replaced once, left in clear the second time.
        ("leaked", trace(raw_in, raw_out.replace("à [EMAIL_1], merci",
                                                 "à claire@example.fr, merci"), BACK, masks),
         "were replaced in one place and not another"),

        # An event name with nothing under it — the bug that killed every answer.
        ("orphan", trace(raw_in, raw_out, BACK.replace(DELTA, "event: content_block_delta\n"), masks),
         "arrived with no data line"),

        # A token the exchange never sent: nothing can expand it.
        ("invented", trace(raw_in, raw_out, BACK.replace("[EMAIL_1]", "[PHONE_9]"), masks),
         "token(s) this exchange never sent"),

        # A stream cut short, its declared size now larger than what is on disk.
        ("cut", trace(raw_in, raw_out, BACK[:BACK.index("event: message_stop")], masks,
                      declared_back=len(BACK.encode())),
         "is not the size the header declares"),

        # A reordered document. One finding, and the per-field comparison must be
        # skipped: pairing by position across two shapes invents three more.
        ("reordered", trace(raw_in, json.dumps({"messages": masked["messages"], "model": "m"},
                                               **COMPACT), BACK, masks),
         "is not shaped like the inbound one"),

        # The field ends on an inferred replacement, so nothing anchors its tail.
        ("tail-unanchored", trace(field("Clé: " + long_secret), field("Clé: [SECRET_1]"), PLAIN_BACK, []),
         "end on a replacement standing for an unusually long span"),

        # The same long value with text after it: endswith pins it, so a truncation
        # would already have failed and there is nothing to warn about. This is the
        # 248-byte credential a length-only rule warned about on real traces.
        ("tail-anchored", trace(field("Clé: " + long_secret + " — fin."),
                                field("Clé: [SECRET_1] — fin."), PLAIN_BACK, []), None),

        # A short unanchored tail is a plausible value, not a lost one.
        ("tail-short", trace(field("Écrire à claire@example.fr"),
                             field("Écrire à [EMAIL_1]"), PLAIN_BACK, []), None),
    ]


def main() -> int:
    ct = load()
    failures = 0

    with tempfile.TemporaryDirectory() as tmp:
        for name, contents, want in cases():
            path = Path(tmp) / f"{name}.txt"
            path.write_text(contents, encoding="utf-8")

            t = ct.parse(str(path))
            findings = []
            for check in (ct.check_sizes, ct.check_shape, ct.check_masking,
                          ct.check_residue, ct.check_count, ct.check_answer,
                          ct.check_expansion):
                findings.extend(check(t))
            ct._wire_cache.clear()
            ct._json_cache.clear()

            raised = [f for f in findings if f.level in (ct.FAIL, ct.WARN)]
            titles = " | ".join(f.title for f in raised)

            if want is None:
                ok = not raised
                detail = "expected silence" if not ok else ""
            else:
                ok = any(want in f.title for f in raised)
                # And nothing else, bar one case: a stream cut short is honestly both
                # a body shorter than its header and a missing stop event. The cap is
                # what asserts against the cascade behind `reordered`, where pairing
                # fields across two shapes invented three findings.
                ok = ok and len(raised) <= (2 if name == "cut" else 1)
                detail = f"expected {want!r}" if not ok else ""

            print(f"  {'ok  ' if ok else 'FAIL'} {name:<17} {titles or '(silent)'}")
            if not ok:
                print(f"       {detail}")
                failures += 1

    print(f"\n  {len(cases()) - failures}/{len(cases())} cases behave")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
