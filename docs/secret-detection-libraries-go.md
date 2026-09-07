# Secret detection in Go — the landscape

Surveyed **30 August 2026**, betterleaks added **3 September 2026** (stars and push
dates read from the public GitHub API). This is a scouting note, not an architecture
decision: no code here is imported by neverseen, and none is planned. What has crossed
over is reasoning — see betterleaks below, which is where three of this catalogue's
patterns were fixed and two of its refusals were confirmed.

To refresh the figures:

```bash
for r in gitleaks/gitleaks betterleaks/betterleaks trufflesecurity/trufflehog Yelp/detect-secrets; do
  curl -s "https://api.github.com/repos/$r" | jq -r '"\(.full_name)\t\(.stargazers_count)\t\(.pushed_at[:10])\t\(.archived)"'
done
```

## The table

| Repository | ★ | Last push | Language | State |
|---|---:|---|---|---|
| [gitleaks/gitleaks](https://github.com/gitleaks/gitleaks) | 29,013 | 2026-08-26 | Go | active |
| [betterleaks/betterleaks](https://github.com/betterleaks/betterleaks) | 1,832 | 2026-09-02 | Go | active (read 3 September 2026) |
| [trufflesecurity/trufflehog](https://github.com/trufflesecurity/trufflehog) | 27,634 | 2026-08-28 | Go | active |
| [Yelp/detect-secrets](https://github.com/Yelp/detect-secrets) | 4,630 | 2026-04-02 | Python | active |
| [grab/secret-scanner](https://github.com/grab/secret-scanner) | 56 | 2024-01-02 | Go | abandoned |
| [octarinesec/secret-detector](https://github.com/octarinesec/secret-detector) | 4 | 2024-01-03 | Go | **archived** |
| [Zayan-Mohamed/secscan](https://github.com/Zayan-Mohamed/secscan) | 4 | 2026-07-17 | Go | personal project |
| [KevinWang15/sensitive-data-detector](https://github.com/KevinWang15/sensitive-data-detector) | 2 | 2025-11-19 | Go | personal project |
| [StacklokLabs/secret-scanning-api](https://github.com/StacklokLabs/secret-scanning-api) | 0 | 2026-06-08 | Go | experimental |

The duopoly is stark: there is nothing at all between 27,600 stars and 56, and
betterleaks — added a week after the survey — is the only entry to have appeared in
the gap since. No other credible Go library exists.

## What each one does

### gitleaks — the closest to what we need

Pure Go, **no network access**, rules in TOML (~170 patterns), Shannon entropy as a
secondary signal. The `detect` package is importable directly:
`detector.DetectString(s)` returns `report.Finding` values carrying offsets — the
exact shape `internal/detector` expects.

What is genuinely interesting is not the code but **the rule file**: they hit the
same problem `genericSecretRe` does — `password:` in source code is not a
credential — and answer it with per-rule `allowlists`, stopwords and context
regexes attached to each rule, where we have a `Verify` per category. Their
mechanism is the more general one; ours is the easier one to test.

### betterleaks — the source of inspiration, and the one that is read rather than imported

MIT, pure Go, a fork of gitleaks by the people who wrote it: "maintained by the folks
who made Gitleaks, including the original author". Development is funded by Aikido
Security. It is the entry on this page that has changed this catalogue the most, and it
was never a candidate for `go get`.

What makes it worth reading is that **its detection engine is documented in prose**,
one post per decision, and each post states the measurement behind the choice rather
than the choice alone. The four are linked from its own README:

- [Regex is (almost) all you need](https://lookingatcomputer.substack.com/p/regex-is-almost-all-you-need) — the wide generic regex, then entropy and allowlists to win precision back.
- [Rare, Not Random](https://lookingatcomputer.substack.com/p/rare-not-random) — Token Efficiency, `len(string)/len(tokens)` over a BPE vocabulary, offered as a replacement for Shannon entropy: "secrets aren't just random, they're statistically unusual compared to the natural distribution of human-written text".
- [Express YourCELf](https://lookingatcomputer.substack.com/p/express-yourcelf-filtering-and-validating) — prefilters, filters and validators as one expression language (CEL, then Expr), replacing the TOML allowlist fields that "became awkward as the logic became more specific".
- [Better generic secrets detection](https://www.aikido.dev/blog/better-generic-secrets-detection-non-secrets) — ~60 hand-written signatures for credentials that are *public by design*, removed from the generic findings.

**Three leaks in this catalogue were found by reading them**, and each is now a corpus
case: `:=`, `?=` and `::=` were not separators, so `apiKey := "…"` — Go, the language
this agent reads most — went out in clear; `CREDS` was not a keyword, and the
repeated-letter tolerance cannot reach it from `CREDENTIAL`; and `slugNamingItselfRe`
listed `api_key` and not `api-key`, so `your-api-key-here` was masked while
`your_api_key_here` was not.

**Two of its central ideas were measured against this corpus and rejected**, which is
worth as much as the three fixes. Token Efficiency at the published threshold (2.5, and
2.1 under twelve characters, `cl100k_base`) does beat entropy: on the values the
comment in `pkg/pii/strength.go` weighs it against, plus the two property paths
`GenericSecretCheck` exists for, it gets five of six where entropy gets two. It still
classes `hunter2-correct-horse` as natural language, along with
`troisieme-valeur-longue`, `MyPassword123!` and the corpus's own session cookie, and
across 55 corpus values it keeps 12 of 25 credentials where the current rules keep 23.
It rewards compressibility, so it penalises exactly the passphrases the default `weak`
level exists to mask, and it flags a commit SHA, an MD5 and a UUID alike: rare is not
secret. The public-credential
denylist is the same trade in the other direction — Stripe's `pk_live_` is public by
design, but the prefix is shared with Paystack, and a Supabase anon key is
byte-indistinguishable from a service-role one, so the confidence the post relies on
comes from a liveness request this agent must never make.

The asymmetry behind both refusals is the one this whole page turns on: a scanner pays a
false positive in triage, so it narrows; this agent pays a false negative in a value
that has already reached the provider, and a false positive is reversible, so it widens.

### trufflehog — the catalogue, not the engine

v3 rewritten in Go, over 800 detectors, one directory per provider under
`pkg/detectors/`. Its distinguishing feature is **live verification**: it calls the
provider's API to find out whether the key is still valid.

Unusable inline for us, and for a reason of substance rather than latency:
verifying a secret means sending it to the provider. An agent that masks
credentials cannot exfiltrate them to confirm that is what they are. What remains
useful is the catalogue of key prefixes (`sk-ant-`, `ghp_`, `xoxb-`…), the most
complete and best maintained list there is.

### detect-secrets (Yelp) — for the baseline, and it is Python

Plugin architecture, aimed at adopting a scanner on a legacy repository: the notion
of an audited baseline comes from here. Out of technical scope (Python), but it is
the tool named for false-positive management in the user's global CLAUDE.md.

### The small ones — to read, not to import

- **octarinesec/secret-detector** — plugin architecture (JSON/YAML/INI transformers,
  then detectors, results combined), built to scan a *stream* rather than a
  repository. Structurally the closest thing to our `Detector`. But the repository
  has been **archived** since January 2024: read it for the idea, never `go get` it.
- **grab/secret-scanner** — CI-oriented, died the same month.
- **secscan**, **sensitive-data-detector**, **secret-scanning-api** — personal or
  experimental projects, fewer than five stars each. Recorded for completeness.

## What their test suites yielded

Each project keeps its test cases beside its rules, and those cases are the
valuable part: they are measured against live traffic rather than reasoned from
a vendor's documentation page. Replayed against this catalogue they found ten
things, in two passes.

**gitleaks** (`cmd/generate/config/rules/*.go`, a `tps`/`fps` pair per rule) —
six shapes reaching no pattern at all: OpenAI service-account and admin keys,
GitHub's fine-grained token, AWS temporary and bearer keys, Stripe's prod
environment, uppercase-hex Twilio, the PGP armour header. And one pattern
corrupting ordinary text: Stripe had no left boundary, so `task_test_abcdef…`
was reported as the key `sk_test_abcdef…`, two characters into an identifier.

**trufflehog** (`pkg/detectors/`, one directory and one test file per provider) —
the worst finding of the two passes. Its private-key detector matches BEGIN
through END; ours matched the header line only, so `-----BEGIN RSA PRIVATE
KEY-----` was masked and every line of key material after it was forwarded in
clear. The masked span was decoration around the secret. It also named two JWT
misses: base64 padding (`=` sat outside the segment class, so a padded token
matched nothing at all rather than partially) and the `ewo` header a claim set
indented before encoding produces. Its `slackwebhook` detector named a fourth:
a webhook URL is a credential — whoever holds it can post as the app — and it
carries no `user:password@`, so the connection-string pattern never saw it.

**detect-secrets** (`detect_secrets/plugins/keyword.py` and its 11 KB of tests) —
a name list, and a lesson about a name not to take. Its `DENYLIST` carries
`auth_key`, `client_key`, `service_key`, `db_pass` and others this catalogue did
not watch. It also carries `pwd`, which this catalogue deliberately refuses:
detect-secrets reads files, where `pwd` is a password field, while this agent
reads prompts, where `PWD` is the working directory and appears in every pasted
environment dump. Taking the list wholesale would have masked a path on every
`env` somebody shares.

Its structural answer to the source-code false positive is worth recording even
though it cannot be borrowed: detect-secrets requires the value to be **quoted**,
and varies that requirement by file type — an unquoted right-hand side in Go or
Objective-C is an expression, not a literal. This agent sees a prompt rather than
a file and has no file type to read, which is why `GenericSecretCheck` reaches
the same conclusion from the shape of the value instead. Two routes to one
insight; only one of them is open here.

## What this changes for neverseen

None of these libraries replaces `pkg/pii`: they detect credentials, not localised
personal data, and **none of them does reversible substitution** — that is our job,
not theirs.

Three things are directly borrowable:

1. **betterleaks' blog posts**, which are the standing source of inspiration here: they
   argue each detection decision from a measurement, so they can be replayed against
   this corpus and either import a fix or record a refusal. Both have happened.
2. **gitleaks' per-rule allowlists**, should `GenericSecretCheck` ever need to grow
   beyond its current rules.
3. **trufflehog's per-provider catalogue**, the day key prefixes are added to the
   catalogue.

If a dependency were ever to enter the tree, it would be gitleaks or betterleaks — and
for their rules and their reasoning rather than for their engines.
