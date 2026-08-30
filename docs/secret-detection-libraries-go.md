# Secret detection in Go — the landscape

Surveyed **30 August 2026** (stars and push dates read from the public GitHub API).
This is a scouting note, not an architecture decision: nothing here is imported by
cloakfleet today.

To refresh the figures:

```bash
for r in gitleaks/gitleaks trufflesecurity/trufflehog Yelp/detect-secrets; do
  curl -s "https://api.github.com/repos/$r" | jq -r '"\(.full_name)\t\(.stargazers_count)\t\(.pushed_at[:10])\t\(.archived)"'
done
```

## The table

| Repository | ★ | Last push | Language | State |
|---|---:|---|---|---|
| [gitleaks/gitleaks](https://github.com/gitleaks/gitleaks) | 29,013 | 2026-08-26 | Go | active |
| [trufflesecurity/trufflehog](https://github.com/trufflesecurity/trufflehog) | 27,634 | 2026-08-28 | Go | active |
| [Yelp/detect-secrets](https://github.com/Yelp/detect-secrets) | 4,630 | 2026-04-02 | Python | active |
| [grab/secret-scanner](https://github.com/grab/secret-scanner) | 56 | 2024-01-02 | Go | abandoned |
| [octarinesec/secret-detector](https://github.com/octarinesec/secret-detector) | 4 | 2024-01-03 | Go | **archived** |
| [Zayan-Mohamed/secscan](https://github.com/Zayan-Mohamed/secscan) | 4 | 2026-07-17 | Go | personal project |
| [KevinWang15/sensitive-data-detector](https://github.com/KevinWang15/sensitive-data-detector) | 2 | 2025-11-19 | Go | personal project |
| [StacklokLabs/secret-scanning-api](https://github.com/StacklokLabs/secret-scanning-api) | 0 | 2026-06-08 | Go | experimental |

The duopoly is stark: there is nothing at all between 27,600 stars and 56. No third
credible Go library exists.

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

## What this changes for cloakfleet

None of these libraries replaces `pkg/pii`: they detect credentials, not localised
personal data, and **none of them does reversible substitution** — that is our job,
not theirs.

Two things are directly borrowable:

1. **gitleaks' per-rule allowlists**, should `GenericSecretCheck` ever need to grow
   beyond its current rules.
2. **trufflehog's per-provider catalogue**, the day key prefixes are added to the
   catalogue.

If a dependency were ever to enter the tree, it would be gitleaks — and for its
rule TOML rather than for its engine.
