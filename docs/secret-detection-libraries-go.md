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
