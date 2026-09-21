#!/usr/bin/env python3
"""Measure what Presidio would add to one real request: count the string values
of the inbound body, their bytes, and the time to analyse each one (value by
value, as the agent masks). Prints counts and durations only, never content."""
import json, re, sys, time, textwrap, urllib.request
URL = sys.argv[1].rstrip("/")
raw = open(sys.argv[2], encoding="utf-8", errors="replace").read()
m = re.search(r"IN   from the tool", raw); n = re.search(r"OUT  to ", raw)
body = textwrap.dedent(raw[m.end():n.start()]).strip()
body = body[body.index("{"):]
doc, _ = json.JSONDecoder().raw_decode(body)
vals = []
def walk(v):
    if isinstance(v, str): vals.append(v)
    elif isinstance(v, dict): [walk(x) for x in v.values()]
    elif isinstance(v, list): [walk(x) for x in v]
walk(doc)
sizes = sorted(len(v.encode()) for v in vals)
print(f"file={sys.argv[2].split('/')[-1]} body={len(body)/1024:.0f} KiB strings={len(vals)} "
      f"string-bytes={sum(sizes)/1024:.0f} KiB >1KiB={sum(s>1024 for s in sizes)} max={sizes[-1]/1024:.0f} KiB")
# The trace footer only: unindented, a known name, and a duration. Anchored this
# hard because a body is indented into the same file, so a loose pattern prints a
# line of somebody's prompt from a script whose whole point is to print no content.
FOOTER = re.compile(r"^(mask|upstream|delivering|unmask):[ \t]+[0-9.]+(?:ns|\u00b5s|ms|s)$")
for line in raw.splitlines():
    if FOOTER.match(line):
        print("   trace:", line)
def analyze(text):
    req = urllib.request.Request(URL + "/analyze", data=json.dumps({"text": text, "language": "en"}).encode(),
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=600) as r: return json.loads(r.read())
t0 = time.perf_counter(); ents = 0; per = []
for v in vals:
    if not v.strip(): continue
    t1 = time.perf_counter(); ents += len(analyze(v)); per.append(time.perf_counter() - t1)
tot = time.perf_counter() - t0
print(f"   presidio: {len(per)} calls, total={tot:.2f}s, slowest value={max(per):.2f}s, entities={ents}")
