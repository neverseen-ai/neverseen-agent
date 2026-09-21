#!/usr/bin/env python3
"""Probes: latency on large texts, determinism, span stability of PERSON across
contexts, French names under the English model, and code-point vs byte offsets.

Usage: probes.py <analyzer-url> <large-text-file> [language]
"""
import json
import statistics
import sys
import time
import urllib.request
from concurrent.futures import ThreadPoolExecutor

URL = sys.argv[1].rstrip("/")
BIG = open(sys.argv[2], encoding="utf-8").read()
LANG = sys.argv[3] if len(sys.argv) > 3 else "en"


def analyze(text, language=None, threshold=None):
    language = language or LANG
    payload = {"text": text, "language": language}
    if threshold is not None:
        payload["score_threshold"] = threshold
    body = json.dumps(payload).encode()
    req = urllib.request.Request(
        URL + "/analyze", data=body, headers={"Content-Type": "application/json"}
    )
    t0 = time.perf_counter()
    with urllib.request.urlopen(req, timeout=600) as r:
        out = json.loads(r.read())
    return out, time.perf_counter() - t0


def show(label, text, res):
    print(f"--- {label}")
    for r in sorted(res, key=lambda r: r["start"]):
        print(f"  {r['entity_type']:<14} {r['score']:.2f}  {text[r['start']:r['end']]!r}")


# 1. Latency on large texts (whole document, as one string value).
sizes = [len(BIG.encode()), len((BIG * 5).encode())]
for text, size in ((BIG, sizes[0]), (BIG * 5, sizes[1])):
    times = []
    for _ in range(3):
        res, dt = analyze(text)
        times.append(dt)
    print(f"large text {size/1024:.0f} KiB: {len(res)} entities, "
          f"latency min={min(times):.2f}s median={statistics.median(times):.2f}s")

# 2. Many small values, sequential vs 4-way parallel (a body is masked value by value).
lines = [l for l in BIG.splitlines() if l.strip()]
chunk = lines[:200]
t0 = time.perf_counter()
for l in chunk:
    analyze(l)
seq = time.perf_counter() - t0
t0 = time.perf_counter()
with ThreadPoolExecutor(max_workers=4) as ex:
    list(ex.map(analyze, chunk))
par = time.perf_counter() - t0
print(f"{len(chunk)} short values ({sum(len(l) for l in chunk)/1024:.0f} KiB): "
      f"sequential={seq:.2f}s parallel(4)={par:.2f}s")

# 3. Determinism: the same text three times.
sample = "Le dossier de Mme Martin est complet. John Smith lives in Paris."
outs = [json.dumps(analyze(sample)[0], sort_keys=True) for _ in range(3)]
print("deterministic across 3 identical calls:", len(set(outs)) == 1)

# 4. Span stability for the session mapping: the same person, different contexts.
for label, text in [
    ("name with title", "Le dossier de Mme Martin est complet."),
    ("bare surname", "Martin a rappelé ce matin."),
    ("full name", "Martin Dupont a rappelé ce matin."),
    ("english full", "Please call Jean Martin tomorrow."),
    ("english bare", "Martin called this morning."),
]:
    res, _ = analyze(text)
    show(label, text, [r for r in res if r["entity_type"] == "PERSON"])

# 5. French text under the English model: names, address, French identifiers.
fr = ("Jean-Pierre Lefèvre habite 3 avenue Victor Hugo, 69003 Lyon. "
      "Sa collègue Aïcha Benali (SIRET 732 829 320 00074) est joignable au 06 12 34 56 78. "
      "Le patient Émile Zola, né le 02/04/1840, NIR 1 40 04 75 115 023 46.")
res, _ = analyze(fr)
show("french text, no threshold", fr, res)

# 6. Offsets are code points, not bytes.
t = "Écrire à josé@example.fr"
res, _ = analyze(t)
for r in res:
    if r["entity_type"] == "EMAIL_ADDRESS":
        print(f"offsets: start={r['start']} end={r['end']} "
              f"code-point slice={t[r['start']:r['end']]!r} "
              f"byte slice={t.encode()[r['start']:r['end']]!r}")
