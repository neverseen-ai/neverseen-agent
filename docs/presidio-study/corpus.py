#!/usr/bin/env python3
"""Run the neverseen corpus through a Presidio analyzer and score it the way
score_test.go does: (category, value) multisets, exact value match. Also a
lenient count (overlap), since NER boundaries differ from a regex span.

Usage: corpus.py <corpus-dir> <analyzer-url> [threshold] [language] [suite-glob]
"""
import glob
import json
import os
import statistics
import sys
import time
import urllib.request
from collections import Counter, defaultdict

import yaml

CORPUS = sys.argv[1]
URL = sys.argv[2].rstrip("/")
THRESHOLD = float(sys.argv[3]) if len(sys.argv) > 3 else 0.0
LANG = sys.argv[4] if len(sys.argv) > 4 else "en"
PATTERN = sys.argv[5] if len(sys.argv) > 5 else "*.yaml"

# Presidio entity -> neverseen category. Approximate where the two catalogues
# draw a different boundary (LOCATION vs ADDRESS, DATE_TIME vs DOB).
MAP = {
    "EMAIL_ADDRESS": "EMAIL",
    "CREDIT_CARD": "CREDIT_CARD",
    "IBAN_CODE": "IBAN",
    "IP_ADDRESS": "IP_ADDRESS",
    "PHONE_NUMBER": "PHONE",
    "US_SSN": "SSN",
    "UK_NHS": "NHS_NUMBER",
    "US_BANK_NUMBER": "ROUTING_NUMBER",
    "DATE_TIME": "DOB",
    "LOCATION": "ADDRESS",
}


def analyze(text, language="en"):
    body = json.dumps({"text": text, "language": language}).encode()
    req = urllib.request.Request(
        URL + "/analyze", data=body, headers={"Content-Type": "application/json"}
    )
    t0 = time.perf_counter()
    with urllib.request.urlopen(req, timeout=120) as r:
        out = json.loads(r.read())
    return out, time.perf_counter() - t0


def main():
    want_all = Counter()
    got_all = Counter()
    tp = Counter()
    fp = Counter()
    fn = Counter()
    lenient_tp = Counter()
    extra = defaultdict(Counter)  # unmapped presidio entities -> values
    persons = Counter()
    locations = Counter()
    lat = []
    cases = 0
    negatives = 0
    fp_on_negatives = Counter()

    for path in sorted(glob.glob(os.path.join(CORPUS, PATTERN))):
        suite = yaml.safe_load(open(path))
        for c in suite["cases"]:
            cases += 1
            text = c["text"]
            expect = c.get("expect") or []
            if not expect:
                negatives += 1
            want = Counter((e["category"], e["value"]) for e in expect)

            res, dt = analyze(text, LANG)
            lat.append(dt)

            got = Counter()
            for r in res:
                if r["score"] < THRESHOLD:
                    continue
                val = text[r["start"] : r["end"]]  # code-point offsets
                et = r["entity_type"]
                if et == "IP_ADDRESS" and ":" in val:
                    cat = "IPV6_ADDRESS"
                else:
                    cat = MAP.get(et)
                if et == "PERSON":
                    persons[val] += 1
                if et == "LOCATION":
                    locations[val] += 1
                if cat is None:
                    extra[et][val] += 1
                    continue
                got[(cat, val)] += 1

            for s, n in want.items():
                hit = min(n, got[s])
                tp[s[0]] += hit
                fn[s[0]] += n - hit
                want_all[s[0]] += n
                # lenient: any got span of the same category overlapping the value
                if hit < n:
                    for (gc, gv), m in got.items():
                        if gc == s[0] and (gv in s[1] or s[1] in gv):
                            lenient_tp[s[0]] += 1
                            break
            for s, n in got.items():
                ex = n - want[s]
                if ex > 0:
                    fp[s[0]] += ex
                    if not expect:
                        fp_on_negatives[s[0]] += ex
                got_all[s[0]] += n

    cats = sorted(set(want_all) | set(got_all))
    print(f"cases={cases} negatives={negatives} threshold={THRESHOLD}")
    print(f"latency per case (s): p50={statistics.median(lat):.4f} "
          f"p95={sorted(lat)[int(len(lat)*0.95)]:.4f} max={max(lat):.4f} "
          f"total={sum(lat):.2f}")
    print()
    print(f"{'category':<22}{'want':>6}{'tp':>6}{'fp':>6}{'fn':>6}{'tp+lenient':>12}")
    for cat in cats:
        w = want_all[cat]
        print(f"{cat:<22}{w:>6}{tp[cat]:>6}{fp[cat]:>6}{fn[cat]:>6}"
              f"{tp[cat] + lenient_tp[cat]:>12}")
    print()
    print("false positives on negative cases:", dict(fp_on_negatives))
    print()
    print("unmapped presidio entities (count of hits):",
          {k: sum(v.values()) for k, v in extra.items()})
    print()
    print("PERSON values found (top 40):")
    for v, n in persons.most_common(40):
        print(f"  {n:>3}  {v!r}")
    print()
    print("LOCATION values found (top 25):")
    for v, n in locations.most_common(25):
        print(f"  {n:>3}  {v!r}")


if __name__ == "__main__":
    main()
