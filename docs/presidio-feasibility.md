# Presidio as a detector, local or remote — a measured feasibility note

Studied **18 September 2026** against `ghcr.io/data-privacy-stack/presidio-analyzer:latest`
(image built 22 July 2026, arm64, Python 3.12, spaCy 3.8.13, `en_core_web_lg` 3.8.0), run
under Docker Desktop on an Apple M3 Pro. This is a scouting note, not an architecture
decision: nothing from Presidio is imported by neverseen and nothing is planned. The
scripts that produced every figure are in `docs/presidio-study/`, so the numbers can be
regenerated rather than trusted.

## The short version

- **Presidio cannot replace the engine.** It has no credential recogniser at all — 0 of
  the 131 categories in the secrets tier — none of the national identifiers this
  catalogue carries (NIR, SIREN, SIRET, NINO, EIN), no postcode, no plate, no checksum
  that *rejects*. On the corpus it fails every precision floor the suites hold at 100%.
- **What it would add is one thing: names of people**, and cities. Under the French model
  the spans are right on prose (`Mme Martin`, `Jean-Pierre Lefèvre`, `Aïcha Benali`) and
  wrong on everything that is not prose: in the secrets suite `Collé` is a person 22
  times, `Cloudflare`, `Twilio` and `Postman` are persons, and raw API tokens are persons.
  A coding tool's prompt is mostly not prose.
- **The cost is measured on real Claude Code bodies, not estimated**: 12.5 s and 20.8 s
  per request against 0.74 s and 1.17 s for the current `mask:` line of the same traces,
  with the analyzer's one worker. A per-value result cache would bring steady state
  under 0.1 s, because a turn adds 1–9 KiB to a 1 MB body — and a cold turn (new session,
  context compaction) stays at about 5 s per 270 KiB.
- **Locally** it means Docker Desktop on every workstation (a paid subscription above
  250 staff or $10M revenue), a 1.5 GB image (2.75 GB with French), 0.75–1.6 GiB resident,
  and a container that has to be up before the agent's fail-closed path lets any tool
  through. **Remotely** it means the prompt leaves the workstation in clear, which is the
  one thing this product says does not happen.
- **Recommendation: do not take Presidio as a runtime dependency**, in either shape. If
  names are the goal, the cheaper experiment is inside the engine (a title-anchored
  pattern and a first-name gazetteer, see below), with Presidio kept as an offline
  yardstick to measure that experiment against.

## What Presidio is, in September 2026

Presidio has left Microsoft: the repository is now `data-privacy-stack/presidio`, governed
by a community organisation, still MIT, images published on `ghcr.io`
([transition note](https://github.com/data-privacy-stack/presidio/blob/main/docs/project_transition.md),
[new site](https://presidio.dataprivacystack.org/project_transition/)). The old
`microsoft.github.io/presidio` pages redirect.

Only the **analyzer** is of interest. The anonymizer does what `detector.Pass` and the
vault already do — and does it without a session, so it could not keep one identity per
value across turns.

| Fact about the stock analyzer container | Measured |
| --- | --- |
| Image | 1.54 GB on disk as `docker images` reports it (555 MB by `docker image inspect`) |
| Resident memory, idle, one model | 747 MiB |
| Process model | `gunicorn -w $WORKERS`, **`WORKERS=1`** by default; each worker loads the model |
| Routes | `POST /analyze`, `GET /recognizers`, `GET /supportedentities`, `GET /health`. **No batch route** (`/batchanalyze` is 404; `BatchAnalyzerEngine` is Python-only) |
| Configuration | three YAML files named by `ANALYZER_CONF_FILE`, `RECOGNIZER_REGISTRY_CONF_FILE`, `NLP_CONF_FILE` |
| Languages | `en` only; `language: fr` answers "No matching recognizers were found" |
| Entities (en) | `PERSON`, `LOCATION`, `NRP`, `DATE_TIME`, `EMAIL_ADDRESS`, `PHONE_NUMBER`, `CREDIT_CARD`, `IBAN_CODE`, `IP_ADDRESS`, `URL`, `CRYPTO`, `MAC_ADDRESS`, `US_SSN`, `US_ITIN`, `US_PASSPORT`, `US_DRIVER_LICENSE`, `US_BANK_NUMBER`, `UK_NHS`, `MEDICAL_LICENSE` — 19 |
| Recognisers (en) | 17; `ORGANIZATION` is in `labels_to_ignore` by default, with the comment "Has many false positives" |
| Scores | fixed per recogniser, not a probability: email and IBAN 1.0, IP 0.6, phone 0.4, US bank 0.05; every NER hit 0.85. `default_score_threshold: 0` |
| Offsets | **code points, not bytes.** `Écrire à josé@example.fr` reports `start=9 end=24`; sliced as bytes that is `\xa0 jos\xc3\xa9@example` |
| Determinism | identical output on three identical calls |

**French needs a rebuilt image.** The model has to be installed at build time
(`fr_core_news_lg`, 3.8.0), the NLP configuration has to list it, and every generic
recogniser has to be re-declared with `fr` in its `supported_languages` — the defaults
carry `en`, `es`, `it`, `pl` context words and no `fr`. The bilingual image measured below
is 2.75 GB on disk and 1.6 GiB resident. The `fr` model exposes 10 entities: the
seven generic recognisers declared plus `PERSON`, `LOCATION`, `NRP`.

**One language per request.** The English model on French prose is unusable — it cut
`de Mme Martin est complet` and `Jean-Pierre Lefèvre habite 3` as persons — and the French
model finds nothing in `Please call Jean Martin tomorrow`. A body from a coding tool mixes
both in one request, so an integration would have to guess a language per string value or
run both models on every value.

## Measured: accuracy on this repository's corpus

The harness (`docs/presidio-study/corpus.py`) scores exactly as `score_test.go` does —
`(category, value)` multisets, exact value match — plus a lenient column that credits an
overlapping span of the same category, since a NER boundary is not a regex boundary.
Presidio's entities are mapped to this catalogue's categories as follows; the two marked
≈ are approximations in Presidio's favour.

`EMAIL_ADDRESS→EMAIL`, `CREDIT_CARD`, `IBAN_CODE→IBAN`, `IP_ADDRESS` (split on `:` for
IPv6), `PHONE_NUMBER→PHONE`, `US_SSN→SSN`, `UK_NHS→NHS_NUMBER`, `US_BANK_NUMBER→ROUTING_NUMBER`,
`DATE_TIME→DOB` ≈, `LOCATION→ADDRESS` ≈.

English model, no threshold, all eight suites — 534 cases, 225 of them negatives:

| Category | want | tp | fp | fn | tp incl. lenient | The engine's floor |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| EMAIL | 23 | 21 | 9 | 2 | 23 | 23 / 0 / 0 |
| CREDIT_CARD | 10 | 7 | 2 | 3 | 8 | 10 / 0 / 0 |
| IBAN | 12 | 10 | 0 | 2 | 10 | 12 / 0 / 0 |
| IP_ADDRESS | 9 | 9 | 8 | 0 | 9 | 9 / 0 / 0 |
| IPV6_ADDRESS | 3 | 3 | 3 | 0 | 3 | 3 / 0 / 0 |
| PHONE | 17 | 15 | 25 | 2 | 16 | 17 / 0 / 0 |
| NHS_NUMBER | 2 | 2 | 2 | 0 | 2 | 2 / 0 / 0 |
| ROUTING_NUMBER | 2 | 2 | 27 | 0 | 2 | 2 / 0 / 0 |
| DOB (≈ DATE_TIME) | 17 | 14 | 73 | 3 | 15 | 17 / 1 / 0 |
| ADDRESS (≈ LOCATION) | 10 | 0 | 40 | 10 | 6 | 10 / 0 / 0 |
| NIR, SIREN, SIRET, NINO, EIN, POSTAL_CODE, LICENSE_PLATE, MONGO_ID, GEO_POINT | 34 | 0 | 0 | 34 | 0 | all found |
| every `SECRET_*` category (311 cases) | — | 0 | 0 | all | 0 | all found |

Of the false positives, these landed on **negative cases** — sentences the corpus says
must come out untouched: ADDRESS 15, DOB 24, ROUTING_NUMBER 13, PHONE 12, IP_ADDRESS 8,
EMAIL 4, IPV6 2, CREDIT_CARD 1, SSN 1. The email ones are the RFC 2606 documentation
domains `DocumentationEmailCheck` refuses; the IP ones are the ranges `IPAddressCheck`
refuses; the routing numbers are any 8–17 digit run scored 0.05. Presidio has the
mechanism for this — per-recogniser context words and `allow_list` — but ships without the
rules, and the rules are this repository's catalogue.

**No threshold works.** At 0.85, the score every NER hit carries, PHONE, IP_ADDRESS,
IPV6_ADDRESS and ROUTING_NUMBER fall to zero recall because their recognisers score below
it, IBAN loses two and DOB four, and ADDRESS keeps its 40 false positives. The design
expects tuning per recogniser, which is the work the catalogue already is.

Beyond the mapped categories the English model produced 82 `URL`, 58 `US_DRIVER_LICENSE`
(any short alphanumeric run), 31 `PERSON`, 9 `US_PASSPORT`, 3 `NRP` hits. The 31 persons,
on a corpus that is mostly French prose and credentials, were `La` ×2, `Rappelle-moi au`,
`pour la`, `Twilio`, `Brevo`, `Clojars`, `Pulumi`, `HUNTER`, and the values
`oh3OUNbSakBymo7yplBf6CGV4aMM9zed` and `doo_v1_26d596…` — every one a token some
other pattern must catch first.

### The French model, on the French suites and on the secrets suite

`fr.yaml`, `hard-fr.yaml`, `precision-fr.yaml` (55 cases, 22 negatives) under
`fr_core_news_lg`, no threshold:

| Category | want | tp | fp | fn |
| --- | ---: | ---: | ---: | ---: |
| EMAIL | 10 | 9 | 4 | 1 |
| CREDIT_CARD, IBAN, IP_ADDRESS, PHONE | 12 | 12 | 1 | 0 |
| DOB (≈ DATE_TIME) | 7 | 3 | 1 | 4 |
| ADDRESS (≈ LOCATION) | 3 | 0 | 21 | 3 |
| NIR, SIREN, SIRET, POSTAL_CODE, LICENSE_PLATE | 8 | 0 | 0 | 8 |

`PERSON` found four values: `Mme Martin` and `Client Dupont` — right — and `Livraison`
and `Redirige` — a noun and a verb at the head of a sentence. `LOCATION` found the cities
(`Paris`, `Lyon`, `Grenoble`, `Brest`, `Quimper`) and street names, and also `FR14` (the
head of an IBAN), `SIREN 443061841`, `606 EUR` and `Guichet`. On the mixed probe sentence
it also read `avenue Victor Hugo` as the person Victor Hugo.

On `secrets.yaml` (311 cases, French labels around credentials) the French model found
**59 persons**: `Collé` 22 times, the vendors `Twilio`, `Aikido`, `Airtable`, `Apify`,
`Artifactory`, `Brevo`, `Buildkite`, `Cloudflare`, `Doppler`, `Postman`, `Pulumi`,
`LangSmith`, the words `CREDENTIAL` and `threshold`, and eleven raw credentials
(`github_pat_11ABCDEFG0…`, `dvc_server_jD0SSW0f`, `shippo_live_D466…`). Overlap
arbitration would hand a credential back to its own category — a credential always wins —
but a vendor name masked as a person in a prompt about that vendor's key is a worse
answer from the model, for nothing gained. That is the gate `looksLikeCode` exists for,
and it would have to apply to every NER hit.

## Measured: latency

The regex engine figures come from a throwaway test over `docs/testCorpus.txt` with
`fr,gb,us` loaded, same machine, same morning (it was deleted after the run). The
Presidio figures are client-side wall time against the container, one worker.

| Text | Regex engine | Presidio (en) | Presidio (fr) |
| --- | ---: | ---: | ---: |
| one corpus sentence (534 cases, p50 / p95) | — | 3.9 ms / 5.6 ms | 5.3 ms / 12.6 ms |
| `docs/testCorpus.txt`, 21 KiB, as one value | 53 ms | 280 ms | 260 ms |
| the same ×5, 107 KiB | 267 ms | 1.54 s | 1.36 s |
| 1093 lines one by one | 43 ms | — | — |
| 200 short values sequential / 4 in flight | — | 0.63 s / 0.40 s | 0.66 s / 0.38 s |

Four requests in flight gain a third, not a quarter: one gunicorn worker. More workers
means the model resident once per worker.

**On real bodies** — three traces recorded on 11 September 2026, Claude Code against
Anthropic, masked value by value as the agent does, English model. The `mask:` column is
the duration the trace itself recorded for the current engine on that exact body.

| Body | String values | Bytes in strings | `mask:` today | Presidio, sequential | Slowest single value |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1147 KiB | 3626 | 983 KiB | 1.17 s | **20.8 s** | 1.27 s (a 236 KiB tool result) |
| 517 KiB | 2482 | 401 KiB | 0.74 s | **12.5 s** | 0.75 s |
| 1 KiB | 17 | 1 KiB | 2.2 ms | 70 ms | 10 ms |

Those bodies are what the invariant in `CLAUDE.md` describes: a conversation replays its
whole history every turn. So the next question is what changes between turns. Twelve
consecutive traces of one session (`docs/presidio-study/turn_delta.py`):

| Turn | Strings | Bytes | New against the previous turn |
| --- | ---: | ---: | --- |
| 0028 | 3562 | 966 KiB | first |
| 0029–0033 (five turns) | 3576–3626 | 971–983 KiB | **5–7 strings, 1–5 KiB, 0–1%** |
| 0034 | 4 | 0 KiB | a new session |
| 0035 | 2466 | 268 KiB | 2463 strings, 268 KiB, **100%** |
| 0036–0039 (four turns) | 2477–2516 | 269–295 KiB | 6–9 strings, 1–9 KiB, 1–3% |

A cache of analyzer results keyed by the hash of a string value would therefore pay
Presidio's price once per *new* value: under 0.1 s per steady-state turn, about 5 s
on the turn that opens a session or follows a compaction (268 KiB ÷ 107 KiB × 1.5 s),
and a tool result that reads a large file costs its size every time. It is the only
shape in which the latency is survivable, and it is one more piece of per-session state
holding something derived from content.

## What an integration would have to be, rule by rule

Each line names the invariant in `CLAUDE.md` it answers to.

- **One implementation** (`internal/detector` package doc). Presidio is not a second
  scanner beside the engine; it is a second *source of candidates* inside
  `Detector.candidates`, feeding the same `resolveOverlaps`. That is what makes a
  credential win over a `PERSON` reading of the same bytes, and what keeps `Scan` the one
  answer to "what leaves this machine". Its `score` maps onto `Match.Confidence` (0.85 →
  85, on a scale that is already "not a probability"); the recogniser name becomes
  `Match.Label`; there is no `Verify` to hang anything on.
- **Offsets are bytes here and code points there.** Every span has to be converted with a
  rune walk before it touches `text[start:end]`; the corpus's accented cases
  (`hard-email-accented-local-part`) would catch a forgotten conversion.
- **A body is masked value by value**, so a body is a few thousand HTTP calls, and one
  worker serialises them. Joining values with a separator and splitting the offsets back
  is possible and bleeds NER context across values; a cache (above) is the actual answer.
- **The same value gets the same stand-in next turn.** Determinism per string holds, so
  the cached prefix survives. Two exposures break it: a *different* string containing the
  same person (`Martin` alone, `Mme Martin`, `Martin Dupont` are three spans, so three
  identities in the vault), and a model upgrade re-cutting spans, which re-mints every
  stand-in and breaks the prefix once for every open session.
- **Fail closed.** An analyzer that is down is a decision the code has to make: refuse
  (503, every tool on the workstation stops until Docker is up — including at login,
  where the agent is a LaunchAgent and Docker Desktop is not yet running) or degrade to
  regex only. Degrading silently is exactly what `Status.Level` exists to forbid, so it
  would have to show as a level and in the heartbeat's `State`.
- **New categories are a catalogue change with every gate attached.** `PERSON` and
  `LOCATION` need a `categoryRegistry` entry (prefix, score, `GroupPersonal`, label), a
  `Generator` for fake mode — a plausible name and city per locale, which the catalogue
  does not have — corpus cases, and `score-baseline.json`. Two gates assume a category has
  a *pattern*: `patternedCategories` in `score_test.go` derives what must be measured from
  the loaded patterns, and `Detector.Notations()` and `TestSampleExercisesEveryCategory`
  read notations off patterns. A category detected by something that is not a regex has
  no place in either yet.
- **Closed vocabularies are a contract.** The heartbeat's per-category counts are keyed
  on the catalogue, and `pkg/telemetry` is imported by `neverseen-cloud`; a new category is
  a coordinated change there. `TestHealthPayloadFitsTheQueryBound` grows by two entries.
- **What is code is not prose.** `looksLikeCode` already gates `DOB`, `PHONE`,
  `POSTAL_CODE`; a NER category is the noisiest one this engine would ever hold and the
  secrets-suite results above are what happens without the gate.
- **`/test`, `/mask` for the extension and `-a` follow for free**, since all three go
  through the one detector. The `-a` console would print names in clear, which is what
  it is for.

## Local against remote

**A local container** puts Docker on every workstation the installer reaches.

- Docker Desktop requires a paid subscription for any company above 250 employees or
  $10M annual revenue, per seat
  ([Docker licence terms](https://docs.docker.com/subscription/desktop-license/),
  [Docker pricing FAQ](https://www.docker.com/pricing/faq/)). Colima, Podman or OrbStack
  avoid the licence and move the installation burden into `install.sh`, on macOS and on
  Windows (WSL2) alike.
- 1.5–2.75 GB on disk and 0.75–1.6 GiB resident, per workstation, for a process that is
  idle most of the day.
- Start order: the agent binds at login; the container comes up whenever the Docker VM
  does. Under fail-closed that window is "no AI tool works"; under degrade it is "the
  masking is weaker and nobody said so".

**A remote container** — one analyzer for the company — removes the workstation cost and
breaks the product's sentence. Every string value of every prompt travels to it in clear:
it *is* the content, that is what an analyzer reads. Consequences, each one a new fact
about the agent:

- The `State` a heartbeat carries says how the agent is exposed (`Exposed`, `Rerouted`,
  `Allowlisted`); "delegates detection to a host" would be a new field, and
  `TestHeartbeatCarriesNoContent` would want the host name justified.
- Presidio has no authentication. A reverse proxy with mTLS or a bearer token puts a
  credential on the agent that is not the caller's — a second `control.key`.
- The right order, if it were done at all, is **regex first, then the residual**: the
  credentials and checksummed identifiers never reach the analyzer, which loses nothing
  since Presidio has no recogniser for any of them, and the raw tokens that the French
  model read as persons are gone before it looks. What remains is the prose — the names
  and addresses — which is exactly the data the analyzer exists to see and exactly the
  data the product promises stays on the machine.

## If names are the goal

Presidio's whole added value here is `PERSON`. Three ways to get some of it, in the
order of the ladder in the project's own rules:

1. **Inside the engine, no dependency.** A title-anchored pattern (`M.`, `Mme`, `Mlle`,
   `Dr`, `Maître`, `Pr`, then a capitalised run — RE2 can do this with `Group`), a
   first-name gazetteer as a `Verify` (INSEE publishes the list of first names given in
   France; a capitalised bigram whose first word is on it is a strong signal), gated by
   `looksLikeCode` and by `RejectedByPlacement`. Deterministic, byte-offset native, 0.5 ms.
   Recall will be lower than NER on free prose, and every miss is one Presidio would also
   have to be measured on — which is what the harness here is for. A `names.yaml` corpus
   suite with the negatives this note already found (`Collé`, `Livraison`, `Victor Hugo`
   in a street) would be the floor.
2. **In-process NER**, no Docker and no network: a small model (GLiNER-class) through ONNX
   Runtime. It breaks the single static binary (CGo, a 200–600 MB model file to
   distribute) and keeps the code-point, determinism and category gates above. Worth a
   spike only if 1 is measured and found short.
3. **Presidio as an offline yardstick**, which costs nothing: keep the harness, run it
   against `names.yaml` when it exists, and let the delta say whether 1 is good enough.

## Reproduce

```bash
# the stock analyzer
docker run -d --name presidio-study -p 5002:3000 ghcr.io/data-privacy-stack/presidio-analyzer:latest
curl -s "http://localhost:5002/supportedentities?language=en"
curl -s -X POST http://localhost:5002/analyze -H 'Content-Type: application/json' \
  -d '{"text":"Le dossier de Mme Martin est complet.","language":"en"}'

# the corpus, scored as score_test.go scores it; threshold, language and a suite glob are optional
python3 docs/presidio-study/corpus.py internal/detector/testdata/corpus http://localhost:5002 0
python3 docs/presidio-study/corpus.py internal/detector/testdata/corpus http://localhost:5003 0 fr '*fr*.yaml'

# latency on a large text, determinism, span stability, code-point offsets
python3 docs/presidio-study/probes.py http://localhost:5002 docs/testCorpus.txt

# what one recorded request would cost, counts and durations only (needs a traces/ directory)
python3 docs/presidio-study/trace_body.py http://localhost:5002 traces/<one>.txt
python3 docs/presidio-study/turn_delta.py traces/<turn-1>.txt traces/<turn-2>.txt …

# clean up
docker rm -f presidio-study presidio-fr
docker rmi ghcr.io/data-privacy-stack/presidio-analyzer:latest presidio-analyzer-fr:study
```

The bilingual image, built for the French figures above:

```dockerfile
FROM ghcr.io/data-privacy-stack/presidio-analyzer:latest
USER root
RUN pip install --no-cache-dir \
  https://github.com/explosion/spacy-models/releases/download/fr_core_news_lg-3.8.0/fr_core_news_lg-3.8.0-py3-none-any.whl
COPY conf-fr/ /app/conf-fr/
ENV NLP_CONF_FILE=/app/conf-fr/nlp.yaml \
    ANALYZER_CONF_FILE=/app/conf-fr/analyzer.yaml \
    RECOGNIZER_REGISTRY_CONF_FILE=/app/conf-fr/recognizers.yaml
USER 1001
```

`conf-fr/analyzer.yaml` lists `supported_languages: [en, fr]`; `conf-fr/nlp.yaml` is the
container's `presidio_analyzer/conf/default.yaml` with a second model
(`lang_code: fr`, `model_name: fr_core_news_lg`) and `MISC` added to `labels_to_ignore`;
`conf-fr/recognizers.yaml` declares `EmailRecognizer`, `IbanRecognizer`,
`CreditCardRecognizer`, `IpRecognizer`, `PhoneRecognizer`, `DateRecognizer` and
`UrlRecognizer`, each `type: predefined` with `supported_languages: [en, fr]`. The NER
recogniser is added per supported language by the registry itself.
