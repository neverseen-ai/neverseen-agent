# The detection engine (`pkg/pii` + `internal/detector`)

**Two layers, and the direction is one-way.** `pkg/pii` is the **catalogue** — what counts
as sensitive, how it is recognised, what it is called. `internal/detector` is the **engine**
that runs the catalogue over text. The catalogue knows nothing about the engine.

## The pipeline

```
the selected locales' pattern sets, in registry order
  → the locale-independent identifiers
  → the credentials
→ every regex hit that is not allow-listed, clears its checksum,
  and reaches minConfidence          (Detector.candidates)
→ overlap resolution keeps one match per stretch of text   (resolveOverlaps)
→ matches returned in reading order                        (Detector.Scan)
```

`Detector.Scan` (`internal/detector/detector.go:131`) reports the **resolved** set, not
every regex hit: a raw list counts a postal code and the address containing it as two
findings, which would tell an auditor that two values leave the machine where one does.

## The catalogue: categories

A `Category` is the string that appears in a token (`[EMAIL_1]`), in the corpus, and in the
per-category counters the agent reports (`pkg/pii/category.go:24`). Three groups:

- **Locale-independent identifiers** (`category.go:32`) — `EMAIL`, `CREDIT_CARD`, `IBAN`,
  `IP_ADDRESS`, `MONGO_ID`, `DOB`. These stay on whatever locale a deployment selects,
  because disabling a country must never disable email detection. The IBAN belongs here
  rather than to a European locale: one expression and one checksum cover every issuing
  country, and a French deployment banking in Germany still needs the German account
  masked.
- **National identifiers** (`category.go:43`) — France: `NIR`, `SIREN`, `SIRET`; UK:
  `NINO`, `NHS_NUMBER`; US: `SSN`, `EIN`, `ROUTING_NUMBER`. Plus the shapes several locales
  contribute their own pattern for under one category: `PHONE`, `ADDRESS`, `POSTAL_CODE`,
  `LICENSE_PLATE` — a postcode is a postcode whether it is five digits and a commune, an
  alphanumeric outward code, or a state and a ZIP.
- **Credentials** (`category.go:79`) — 21 categories: keys for OpenAI, Anthropic, Google,
  AWS (access and secret), GitHub, GitLab, Slack, Stripe, SendGrid, Twilio, npm, PyPI,
  Docker, Hugging Face, Replicate, plus `SECRET_PEM_KEY`, `SECRET_JWT`,
  `SECRET_CONN_STR`, `SECRET_GENERIC`, `SECRET_HEX_KEY`.

`CatCustom` (`category.go:75`) carries what a deployment declares sensitive itself. It is
the one category with no regex in the catalogue — its patterns are built from
configuration.

### `CategoryInfo` — one entry is the whole registration

`categoryRegistry` (`category.go:136`) maps each category to four facts
(`CategoryInfo`, `:105`):

| Field | What it decides |
| --- | --- |
| `Prefix` | the name inside a token — `EMAIL` gives `[EMAIL_1]` |
| `Score` | confidence 1–100; orders candidates competing for the same span and gates against the reporting threshold. **Not a probability**, and no two categories' scores need to be comparable in any other sense |
| `Verify` | the rule the regex cannot express — a checksum, almost always. Nil when the shape stands on its own |
| `Secret` | marks a credential; credentials **outrank** the confidence scale in overlap resolution |

**`validateCatalogue` runs at package initialisation** (`category.go:200`) and panics if a
pattern emits a category with no registry entry. That is what makes "one entry in one
registry" the whole of adding a category — the four-places-to-forget problem cannot come
back.

### Groups and labels: how forty categories are put in front of a person

`CategoryInfo` also carries a `Group` and a `Label` (`pkg/pii/group.go`), and both are
catalogue facts rather than presentation: the catalogue's job is what counts as sensitive,
how it is recognised, **and what it is called**.

Seven groups, in display order rather than alphabetical — the first entries are the ones
somebody opened a menu for, and alphabetical puts *Banking*, which nobody switches off by
accident, above the personal details they came for:

| Group | n | Why the boundary is there |
| --- | --- | --- |
| Personal details | 10 | what people reach for, and the largest — so a group switch alone is not enough |
| Company identifiers | 3 | the strongest case for switching off: a registration number is public, and nine digits under a Luhn key is every internal fleet id too |
| Technical identifiers | 2 | the other strong case: debugging a network needs the address in the prompt |
| Banking | 3 | all three carry a checksum, so none is a false positive somebody switches off in irritation |
| Declared by this deployment | 1 | `CUSTOM`, the only category somebody authored on purpose |
| Connection strings | 1 | a group of one, and it earns it — `postgres://admin:pw@db` is what a person looks for |
| Secrets and keys | 20 | the API keys and tokens |

**`pii.Switchable` follows `Secret`, not the group**, and the two are deliberately not
merged. The flag decides what may be switched off; the group decides how a person finds it.
That is why connection strings sit apart and are still locked, and why `CUSTOM` is locked
too — switching off what a deployment declared itself would undo the one decision somebody
made explicitly.

`validateCatalogue` enforces all of it at package initialisation: a category with no group,
a group that is not registered, a missing label, or **a label another category already
uses**. That last one is the same class as the unique-prefix rule one level up — NIR and SSN
are both "social security number" until the catalogue says which country's, and two
identical rows in a menu is a person unticking one with no idea which they got.

### Three things change while the agent runs

The switched-off categories, the substitution mode and the loaded locales all live
behind one atomic pointer in `detector.policy` (`internal/detector/policy.go`).

The locales are the heavy one: changing them means a different pattern set **and** a
different stand-in table, so both travel together in one `catalogue` value that is
swapped whole. Separately mutated, a scan could read the new patterns against the old
`FakeSet` and render a French address with an American postcode. `Detector.SetLocales`
stores the selection in **registry order** whatever order it arrived in, because load
order settles which country claims a value both could read — and it **refuses an
unknown code** rather than skipping it, since `pii.LocalePatterns` skips by design and
nothing below would notice a typo.

The mode may change mid-session, and the vault is what makes that safe: it maps a
replacement to its original and expansion accepts both shapes, so stand-ins minted
before the change go on being restored while new values get tokens. A credential is
tokenized either way — `render` checks `IsSecret` before the mode.

The one detector that does *not* follow the live mode is the derived one the test page
uses: it pins its own with `subOverride`, because that page renders one text in both
modes at once and reading the live mode would draw the same column twice. It still
follows every locale change and every switched-off category, since it shares the
policy pointer.

### A switched-off category is skipped before anything else runs

`Detector.candidates` consults the disabled set **before the checksum and before the
score** (`internal/detector/policy.go`). A category switched off is not a weak match: it is
one this agent has been told not to look at, and running its checksum to throw the answer
away is work on the hottest loop in the agent.

The set is an immutable map behind an atomic pointer, not a mutex: every request reads it
once per candidate, and a write happens when somebody clicks a menu item. It is **shared by
pointer** with any detector `WithSubstitution` derives — the test page renders both modes
through two detectors, and a copy would have that page go on showing a category the agent
had stopped masking, while the page exists to say what the agent does to a text.

### A checksum lets a shape be loose; without one, the shape is all there is

A value that fails its `Verify` is **dropped outright, not scored down**. Fifteen digits
failing the NIR key are not a NIR, and no sensitivity setting should turn them into one.
This is where Agent Veil left an opening: at its most aggressive setting a failed checksum
still scored 30 and was reported.

Checksums live in `pkg/pii/checksum.go`: `LuhnCheck` (cards), `IBANCheck`, `NIRCheck`
(including the Corsica case, `TestNIRCheck_Corsica`), `SIRENCheck`, `SIRETCheck`,
`NHSNumberCheck`, `NINOCheck` (letter rules, not a checksum — six letters excluded from
the first position, seven from the second, seven whole prefixes unissued), `SSNCheck`,
`RoutingNumberCheck` (ABA weights).

`minConfidence` is 50 (`internal/detector/config.go:38`), set just at the level of the
weakest category worth reporting so every registered category is reportable and the
checksums do the discriminating. It is a constant, not a knob: a `TODO` records that a
deployment wanting only high-confidence matches would need it exposed, and that a knob
nothing sets is a knob nobody tested.

## The catalogue: patterns

`pii.Pattern` (`pkg/pii/pattern.go:12`) is one recognisable shape plus its category,
`Label`, `Locale`, `Group` and `Refine`. Pattern files: `patterns_fr.go`,
`patterns_gb.go`, `patterns_us.go`, `patterns_intl.go`, `patterns_secret.go`.

**Order within a set is correctness, not taste.** The first pattern to claim a literal
wins, so a specific shape must precede a broader one: `sk-ant-` before `sk-`, a
fourteen-digit SIRET before the nine-digit SIREN inside it, France's checksummed
identifiers before Vietnam's CMND, which matches any bare nine digits.

`AllPatterns` (`pattern.go:57`) reads the locale registry rather than concatenating sets by
hand. Agent Veil's hand-written version shipped with one set missing, which silently
exempted two thirds of the catalogue from every test that swept "each category".

### Writing a pattern — the rules and the leaks behind them

- **RE2 has no lookbehind or backreference.** A pattern that must reject a preceding
  character consumes it and points `Group` at the value. Separators that have to agree
  ("23/02-2004" is not a date) need one alternative per separator, not a character class.
- **Horizontal whitespace only** (`[ \t]`, never `\s`) in any span that could run long.
  With `\s` an address swallowed the first word of the next line, so the replacement ate
  ordinary text.
- **Go's `\b` is ASCII.** It finds a boundary inside an accented run, which once turned
  `andré.muller@example.fr` into a token bound to a fragment, with the start of the address
  forwarded in clear. Use a leading character class and `Group` instead.
- **Require a minimum length where a single character is an artefact.** The email
  local part is `{2,}`, not `+`: an escape sequence leaves one letter welded to what
  follows it, so raw text carrying `…pourquoi.\n\n@RTK.md` read `n@RTK.md` as an
  address, took the `n` out of the `\n` and left a lone backslash before the token —
  every request refused for an invalid escape. Masking a JSON body value by value is
  the proper fix, but a flat-text body and the audit console still meet the raw form,
  and the addresses a one-character local part claims are overwhelmingly artefacts —
  a path, a filename, a shell redirection — rather than mailboxes. The cost is
  recorded rather than hidden: the inherited corpus demanded `a@x.fr`, and that form
  is now a negative case (`prec-email-one-character-mailbox`) instead of a
  requirement.
- **Beware `(?i)` over a long repetition.** It let the IBAN expression walk through a
  sentence claiming lowercase words as groups; requiring groups of exactly four fixed the
  whole class.
- **`Refine` is for a span the regex had to over-match**, and only the IBAN needs it.
  Tolerating the conventional grouping by four makes the expression greedy enough to
  swallow the following word, and only the checksum knows where the account ends. A pattern
  with a `Refine` is scanned **one match at a time, resuming after the refined end**
  (`refinedSpans`, `detector.go:201`): scanning them all at once resumes after the greedy
  end, so a second IBAN immediately after the first was never seen — forwarded in clear.

## Locales

`localeRegistry` (`pkg/pii/locale.go:65`) holds `fr` (priority 10), `gb` (20), `us` (30).
Each entry carries a code, a `Priority`, a pattern set, a sample and a stand-in table.

**`Priority` is load order, and load order is a decision:** it settles which country claims
a value two of them could both read. Nine bare digits are a French SIREN under Luhn and a
US routing number under the ABA weights; the earlier locale in the registry names it.

`LocalePatterns` (`locale.go:113`) **stamps each pattern's `Locale` as it loads**, once, so
a new locale gets it without anybody remembering to. That stamp is load-bearing — see
stand-ins below.

`ParseLocales` (`internal/detector/config.go:75`) accepts a code, a comma-separated list, or
`none`. **Unset means none**, deliberately: a wrong locale is worse than none — scanning
French data with the Vietnamese set masks its invoice numbers and timestamps as identity
cards — and an operator who never set the variable has not chosen that
(`DefaultConfig`, `config.go:67`).

## Overlap arbitration

`resolveOverlaps` (`internal/detector/overlap.go:24`) keeps one match per stretch of text.
The order is **a credential always wins, then confidence, then the longer span, then the
leftmost**, and each rule is there because its absence leaked:

- Position deciding on its own is the trap: with "whichever starts later wins", the postal
  code inside `12 rue de la Paix, 75002 Paris` evicted the address containing it and the
  street went to the provider in clear.
- Credentials outrank the confidence scale rather than sitting on it: several ordinary
  categories score above a connection string, so `postgres://admin:pw@db` resolved to an
  **email** match — over the password, not the host. A credential reaching the vault under
  another category's token is one the response path expands back into a live secret
  (`TestCredentialWinsOverEmailInRealText`).

Matches are clustered first, so only transitively-overlapping candidates compete; two
`TODO`s in the file name the remaining ceilings — the Σk² cluster cost, and that a losing
match is dropped whole rather than clipped.

## Substitution modes

`Substitution` (`internal/detector/mask.go:15`) is `token` or `fake`, read from
`CLOAKFLEET_PII_SUBSTITUTION`. The zero value is `token`, so a `Config` built by hand keeps
the reversible behaviour without saying so.

`Detector.render` (`mask.go:132`) applies the mode to one index:

```go
if fake mode && !pii.IsSecret(cat) { if stand-in available for (cat, locale, index) → use it }
return pii.Token(cat, index)   // the fallback
```

The bracket-token fallback is the safe direction: a token is never a leak and never a
fabrication, it only reads less like prose. **Every credential takes it by design** — a
stand-in that looks like a working API key is a thing somebody will try to use.

That single line is also the guarantee the response path depends on: a non-token entry in
the mapping cannot be a credential, so the value-matching expansion cannot turn one back
into a live secret. Removing the fallback removes the guarantee with it, and `TestFakeMode`
asserts both halves — that a credential is mapped to a token, and that expanding still
returns the original.

**A stand-in is chosen by the locale that recognised the value**, not by the set of enabled
locales (`pii.FakeSet.Value`, `generators.go:83`). Merging the per-locale tables into one
map means a shared category — telephone, address, postcode — resolves to whichever locale
merged last: with `fr,gb,us` on, a French number came out as `(555) 555-0100`. The matched
pattern carries its `Locale`, and that is what picks the table.

Stand-ins are built to be implausible rather than short: they use reserved ranges and
**fail their own checksums** (`TestStandInsFailTheirOwnChecksum`,
`TestStandInsUseReservedRanges`), and a generator refuses to wrap past its capacity rather
than repeating itself (`TestStandInsRefuseToWrapPastCapacity`) — past that, `render` falls
back to a token.

**The known gap is named rather than hidden.** A short stand-in can be matched by
coincidence: a fake postcode is five digits, and a model that wrote those five digits about
something else has them replaced by the caller's real postcode. The `TODO` on
`detector.Unmask` (`mask.go:225`) carries it, with the upgrade path — a minimum length, or
marking the substitution invisibly.

## Tokens

`pii.Token(cat, index)` produces `[PREFIX_N]`; `tokenRe` / `tokenScanRe`
(`pkg/pii/token.go:20`) recognise the shape, `TokenAt` reads one at a position,
`ReplaceTokens` walks text expanding them, and `TokenTailLen` says how much of a trailing
fragment could still become a token. `maxTokenLen` is 48 (`token.go:113`).

Indices come from `Detector.nextIndex` (`mask.go:151`), per category, and are safe under
concurrency: `TestConcurrentPassesNeverShareAMask`.

## The sample is a reference, and must stay one

`pkg/pii/sample.go` holds one sample per locale plus the locale-independent and credential
sections. Each has to carry **every category its set detects and every notation each
pattern accepts** — all eleven French day-first date forms, both ISO separators, an
identifier compact and spaced, an address with and without its town. It is what an operator
reads to check their own data shape is covered, so a gap in it reads as a gap in the engine.

Any change to the catalogue — a new category, a newly accepted notation, a widened or
narrowed pattern — means updating the sample **in the same commit**. Five tests hold it for
every locale in the registry: `TestSampleExercisesEveryCategory` sweeps the live catalogue,
so a new category with no line fails; `TestSampleShowsEveryNotation` is an explicit table
of every accepted form with the category it must be read as; and
`TestSampleShowsEveryMonthName` / `EveryStreetType` / `EveryPostcodeNotation` /
`EveryCredential` cover the long alternations where a typo silently loses a branch.

**The tables are deliberately not derived from the detector.** A derived expectation agrees
with whatever the detector does, including a form it silently stopped reading.

## See also

- [Extending the catalogue](../workflows/extending-the-catalogue.md) — the exact steps for
  a new category or locale, and the tests that fail if you skip one.
- [Testing and accuracy](../workflows/testing-and-accuracy.md) — the corpus and the
  per-category score floor.
