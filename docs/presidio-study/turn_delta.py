#!/usr/bin/env python3
"""How much of a turn's string values are new against the previous turn of the
same session: the upper bound of what a per-value result cache would save.
Counts and bytes only."""
import json, re, sys, textwrap
def strings(path):
    raw = open(path, encoding="utf-8", errors="replace").read()
    m = re.search(r"IN   from the tool", raw); n = re.search(r"OUT  to ", raw)
    body = textwrap.dedent(raw[m.end():n.start()]).strip(); body = body[body.index("{"):]
    doc, _ = json.JSONDecoder().raw_decode(body); out = []
    def walk(v):
        if isinstance(v, str): out.append(v)
        elif isinstance(v, dict): [walk(x) for x in v.values()]
        elif isinstance(v, list): [walk(x) for x in v]
    walk(doc); return out
prev = None
for p in sys.argv[1:]:
    cur = strings(p); total = sum(len(s.encode()) for s in cur)
    if prev is not None:
        seen = set(prev); new = [s for s in cur if s not in seen]
        nb = sum(len(s.encode()) for s in new)
        print(f"{p.split('/')[-1][:24]}: {len(cur)} strings {total/1024:.0f} KiB; new vs previous turn: {len(new)} strings {nb/1024:.0f} KiB ({100*nb/max(total,1):.0f}%)")
    else:
        print(f"{p.split('/')[-1][:24]}: {len(cur)} strings {total/1024:.0f} KiB (first)")
    prev = cur
