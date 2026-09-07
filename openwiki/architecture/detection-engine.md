# The detection engine (`pkg/pii` + `internal/detector`)

**Two layers, and the direction is one-way.** `pkg/pii` is the **catalogue** — what counts
as sensitive, how it is recognised, what it is called. `internal/detector` is the **engine**
that runs the catalogue over text. The catalogue knows nothing about the engine.

## The pipeline

```
the selected locales' pattern sets, in registry order
  → the locale-independent identifiers
  → the credentials
→ every regex hit that is not allow-listed, clears its Verify,
  reaches minConfidence and the secret level, is not refused by
  where it sits, and — in text that reads as code — is not a
  category source code satisfies      (Detector.candidates)
→ overlap resolution keeps one match per stretch of text   (resolveOverlaps)
→ matches returned in reading order                        (Detector.Scan)
```

`Detector.Scan` (`internal/detector/detector.go:150`) reports the **resolved** set, not
every regex hit: a raw list counts a postal code and the address containing it as two
findings, which would tell an auditor that two values leave the machine where one does.

## The catalogue: categories

A `Category` is the string that appears in a token (`[EMAIL_1]`), in the corpus, and in the
per-category counters the agent reports (`pkg/pii/category.go:24`). Three groups:

- **Locale-independent identifiers** (`category.go:32`) — `EMAIL`, `CREDIT_CARD`, `IBAN`,
  `IP_ADDRESS`, `IPV6_ADDRESS`, `MONGO_ID`, `DOB`. These stay on whatever locale a deployment selects,
  because disabling a country must never disable email detection. The IBAN belongs here
  rather than to a European locale: one expression and one checksum cover every issuing
  country, and a French deployment banking in Germany still needs the German account
  masked.
- **National identifiers** (`category.go:43`) — France: `NIR`, `SIREN`, `SIRET`; UK:
  `NINO`, `NHS_NUMBER`; US: `SSN`, `EIN`, `ROUTING_NUMBER`. Plus the shapes several locales
  contribute their own pattern for under one category: `PHONE`, `ADDRESS`, `POSTAL_CODE`,
  `LICENSE_PLATE` — a postcode is a postcode whether it is five digits and a commune, an
  alphanumeric outward code, or a state and a ZIP.
- **Credentials** — 23 first-tier categories: keys for OpenAI, Anthropic, Google, Groq,
  xAI, AWS (access and secret), GitHub, GitLab, Slack, Stripe, SendGrid, Twilio, npm,
  PyPI, Docker, Hugging Face, Replicate, plus `SECRET_PEM_KEY`, `SECRET_JWT`,
  `SECRET_CONN_STR`, `SECRET_GENERIC`, `SECRET_HEX_KEY`.

`CatCustom` (`category.go:76`) carries what a deployment declares sensitive itself. It is
the one category with no regex in the catalogue — its patterns are built from
configuration.

### `CategoryInfo` — one entry is the whole registration

`categoryRegistry` (`category.go:288`) maps each category to the facts that register it
(`CategoryInfo`, `:223`):

| Field | What it decides |
| --- | --- |
| `Prefix` | the name inside a token — `EMAIL` gives `[EMAIL_1]`. **Not the category code**, and `CatDOB` is where the two differ: the code stays `DOB` (the corpus, the counters and `neverseen mask --off` all name it) while the prefix is `DATE`, because the token is read by a model and `[DATE_1]` says what the value was where `[DOB_1]` is an acronym it has to guess at |
| `Score` | confidence 1–100; orders candidates competing for the same span and gates against the reporting threshold. **Not a probability**, and no two categories' scores need to be comparable in any other sense |
| `Verify` | the rule the regex cannot express — a checksum, almost always. Nil when the shape stands on its own |
| `Secret` | marks a credential; credentials **outrank** the confidence scale in overlap resolution |
| `NoisyInCode` | marks a category whose shape source code satisfies — `DOB`, `PHONE`, `POSTAL_CODE` — so a value found inside text that reads as code is not reported (see [where a value sits](#where-a-value-sits-and-what-the-text-is)). Per category rather than one sensitivity knob, and **no credential is ever marked**: a key in a `.env` a coding agent has just read is the most valuable thing this agent sees all day |

**`validateCatalogue` runs at package initialisation** (`category.go:526`) and panics if a
pattern emits a category with no registry entry. That is what makes "one entry in one
registry" the whole of adding a category — the four-places-to-forget problem cannot come
back.

### Groups and labels: how a hundred and fifty categories are put in front of a person

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
| Technical identifiers | 3 | the other strong case: debugging a network needs the address in the prompt |
| Banking | 3 | all three carry a checksum, so none is a false positive somebody switches off in irritation |
| Declared by this deployment | 1 | `CUSTOM`, the only category somebody authored on purpose |
| Connection strings | 1 | a group of one, and it earns it — `postgres://admin:pw@db` is what a person looks for |
| Secrets and keys | 131 | the API keys and tokens, both tiers |

**`pii.Switchable` follows `Secret`, not the group**, and the two are deliberately not
merged. The flag decides what may be switched off; the group decides how a person finds it.
That is why connection strings sit apart and are still locked, and why `CUSTOM` is locked
too — switching off what a deployment declared itself would undo the one decision somebody
made explicitly.

`validateCatalogue` enforces all of it at package initialisation: a prefix that does not
make a token by `tokenRe` (the `1PASSWORD_TOKEN` case), a score outside 1–100, a category
with no group, a group that is not registered, a missing label, or **a label another
category already uses**. That last one is the same class as `TestCategoryPrefixesAreUnique`
one level up — NIR and SSN
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
`RoutingNumberCheck` (ABA weights), `DOBCheck` (a year in the past), `PostcodeCheck` (the
commune is not a month), `IPAddressCheck` (`netip.ParseAddr`, then the ranges that name
nobody — guarding both IP categories) and `DocumentationEmailCheck` (the RFC 2606/6761
domains are never anybody's address).

Two more hang off a **pattern** rather than a category (`Pattern.Verify`,
`pkg/pii/pattern.go:40`), because the two generic-secret expressions share a category and
disagree about the value: `UnclosedBracketCheck` on the bare spans only, and
`AuthHeaderCheck` on the two header patterns — see the rules table below.

#### How much work each checksum does alone

Measured, by generating random runs of the length each shape admits and counting how
many clear the check:

| Check | Clears | Length fixed by the shape? |
|---|---|---|
| Luhn (SIREN, SIRET, card) | ~10% | SIREN 9, SIRET 14 — yes. Card: **no**, see below |
| NHS mod-11 | ~9% | 10 — yes |
| ABA weights + district | ~4% | 9 — yes |
| NIR mod-97 | ~0.9% | 15 — yes |
| IBAN mod-97 | ~1% | **no**, see below |

A checksum is one or two digits of evidence and nothing more. Where the real
identifier fixes a length, the shape has to fix it too, or the check is carrying the
whole category on its own. Two did not, and both were found by asking that question
of every row rather than by a report.

#### The card shape, and the two Visa lengths that do not exist

`creditCardRe`'s Visa branch ended on `\d{1,4}`, which admits **thirteen, fourteen,
fifteen and sixteen** digits. Visa issues thirteen and sixteen. Fourteen and fifteen
are not short cards or rare cards; they are not cards. Each was catching a tenth of
the numbers that reached it — an order reference, an account line, any long run
opening on a 4.

The other three branches were already exact: Mastercard and Discover sixteen, Amex
fifteen. Only the branch written with a range was wrong, which is the lesson worth
keeping — a range in a shape that has no range is where this class of bug lives.

`TestCreditCardShapeRejectsLengthsNoVisaHas` asserts on values that **all pass Luhn**,
so it exercises the shape rather than the checksum; a case that failed Luhn would go
green for the wrong reason.

**The branch now carries every length Visa issues**, which closes the other half of the
finding. Nineteen-digit numbers were missed — the repetition was fixed at two groups —
and a missed card is forwarded in clear, which is worse than a reference masked for
nothing. The optional trailing group of three is what covers it:

```
4\d{3}(?:[ \-]?\d{4}){2}[ \-]?(?:\d{4}(?:[ \-]?\d{3})?|\d)
                                   └ 16 ┘ └── 19 ──┘   └ 13 ┘
```

| Length | Verdict | |
|---|---|---|
| 13, 16, 19 | masked | the lengths Visa issues, run together or grouped |
| 14, 15 | ignored | no card has them |
| 17, 18, 20 | ignored | between real lengths, and none of them |

**Discover stays at sixteen deliberately.** ISO/IEC 7812 permits up to nineteen, and no
source confirms the network issues one. A guessed length here either misses real cards
or claims references, and both are silent — the same reason Mistral and DeepInfra have
no key pattern of their own (Together AI gained one, `tgp_v1_`, with the second tier).

#### The IBAN length table, and why a key alone was not enough

`MASK ae5917ce58a7f1e2 TO [IBAN_2]` — a short git object id, masked as a bank account.
It opens on `AE`, which is a real country code, and its mod-97 key verifies. One
arbitrary string in ninety-seven of the right shape does; that is what a two-digit
check is.

`ibanLengths` closes it. ISO 13616 fixes a length per country — Emirati accounts are
23 characters, this was 16 — and the country code is in the value, free to read. The
reason length works so well against this particular class is arithmetic: a hex blob
can only open on letters `a`–`f`, and of those pairs `AE` is 23, `AD` 24, `BA` 20,
`DE` 22, `EE` 20. **Only `BE`, at 16, still collides** — and a sixteen-character string
opening `BE` whose key verifies has every property a Belgian account has.

**An unknown country code is accepted, not refused.** The registry gains members, and
refusing an unknown one would silently stop masking real accounts the day a country
joined — a leak, weighed against false positives on the few two-letter prefixes that
are not countries and must still clear mod-97. The transcription is a `TODO`: a
country joining needs a line, and the symptom of forgetting is masking as before.

The file had carried the fix as a `TODO` since the check was written — *"add the table
if IBAN false positives show up in the corpus"* — and this is the one that showed up.

#### `DOBCheck`, the one that is not a checksum

A date has none, and nothing about its shape distinguishes a birth date from a deadline,
a renewal, an invoice date or a delivery slot. Left unverified, `DOB` claims all of them —
which is a lot of ordinary text replaced in a prompt somebody is asking a question about.

What separates the two is the one thing a regex cannot see: where the value sits relative
to today. The rule is **at least a year in the past**. "Not in the future" alone would
still admit every date since January, which is exactly where a renewal lands.

It hangs off the **category**, not off a pattern, which is the whole reason it is one
function. `Score` is the single place `Verify` is called, and `CatDOB` is shared by
day-first (`fr`), month-first (`us`) and ISO (locale-independent) — so one rule guards
three expressions. Written per pattern it would have been three, and the third would have
been forgotten the day a fourth locale landed.

The parsing is the awkward part, and it is deliberately shallow. Three fields split on any
of the separators the patterns accept; year-first means ISO; a spelled-out month fixes the
order whatever the locale; and a bare numeric triple is genuinely ambiguous — `05/06/2024`
is day-first in France and month-first in the US, and the locale that read it is not
carried as far as `Verify`. **Either reading being old enough is enough to go on masking.**
The two differ by months, and choosing wrong would drop a real birth date to spare an
ordinary one. A shape it cannot read at all is kept, for the same reason.

**The clock is injectable** (`dobCheckAt`), and that is not a testing nicety. Against
`time.Now` every boundary case ages past the threshold and starts passing for the wrong
reason — the suite would go green over a rule it had stopped exercising. `TestDOBCheck`
pins 2026-08-28 and checks both sides of the cut-off; the corpus negatives use 2099, which
is permanently in the future, rather than a near year that would rot.

**The known cost is infants**, recorded as a `TODO` on the function. Somebody born four
months ago has a real date of birth and it is personal data, and this drops it. The upgrade
is a rule that reads the words around the value rather than the value alone — a different
engine, not a wider expression.

One false positive this does *not* fix, and it is in the baseline: `precision-fr.yaml`
flags `1 12 2019` in "La version 1 12 2019 du document". A version string in the day-first
spaced notation, comfortably in the past. Only context separates it from a birth date.

#### `PostcodeCheck`, and the commune that was a month

The French pattern takes the **commune with the code**, because five bare digits are not
identifiable on their own — matching them alone tokenizes every quantity, price and
odometer reading in the payload. The consequence is that any capitalised word after a
five-digit run *is* a commune as far as the shape is concerned, and a long listing puts one
there on every line:

```
-rw-r--r--  1 alice  staff  13469 Mar  3 10:22 proxy.go

MASK 13469 Mar TO [POSTCODE_1]
MASK 11175 Mar TO [POSTCODE_2]
MASK 87617 Aug TO [POSTCODE_3]
```

The size passes the department test — 13, 11 and 87 are real departments — and the month
passes the commune test. `13469 Mar` and `13290 Aix` are written identically, so no rule
about form can separate them: the **word** is what has to decide, which is the second case
after `DOBCheck` of a `Verify` carrying something the regex cannot express.

The list is the **three-letter abbreviations only, matched as a whole word**. Both
restrictions are narrower than they look, and each has a real case against the wider
version: the full names would drop `PE15 8NF` (March, Cambridgeshire) and `42750 Mars` (a
commune in the Loire, and the French month), and a prefix match would drop
`14320 May-sur-Orne`. A guessed list here does not print a false positive — it forwards a
real address in clear, which is the worse direction.

It hangs off the **category**, so it guards the British and American patterns too;
neither puts a word after the code, so nothing there reaches the rule.
`TestPostcodeCheckRejectsALongListing` and `TestPostcodeCheckKeepsRealCodes` are one pair
and neither is meaningful alone, and `prec-postcode-long-listing` holds the observed line
in the corpus.

#### `GenericSecretCheck`, and the premise that fails in a repository

`genericSecretQuotedRe` and `genericSecretBareRe` read `NAME=value` and treat the
**name** as the evidence: a run of characters after `PASSWORD=` is a password because of
what precedes it. In a `.env` file, a YAML key or a shell export that is exactly right.

**Two expressions, and what separates them is quoting, not length.** A quoted value ends
where its quote does, so punctuation inside it is the value's own — `password="hunter2)"`
ends on a bracket, `"secret": "MyP@ssw0rd!"` on a bang, and both are taken whole. An
unquoted value ends where the surrounding text resumes, so trailing punctuation is that
text: `[password=hunter2]` closes a bracket somebody opened, and taking the `]` with the
value emits `[password=[SECRET_1]` — source code with a delimiter removed, which the model
then analyses wrongly and reports on as if it were the caller's.

The rule this replaced decided on length: the run minus its tail at a floor of eight,
falling back to the raw run when trimming would drop under it. The fallback stopped
`PASSWORD=hunter2)` being missed altogether, but the two halves disagreed with each other
— `MyP@ssw0rd!` at eleven characters had its bang left in clear while `hunter2)` at eight
kept its bracket. One value leaked its last character, the other ate the syntax around it,
and nothing but the password's length decided which.

**The keyword can fall anywhere in the name, and it need not be spelled exactly.** The
separator used to have to follow the keyword immediately, so a prefix was free and a
suffix was fatal: `VERY_SECRET=` was masked while `VERY_SECRET_TOO=`,
`SUPER_SECRET_VALUE=`, `accessTokenValue=` and `STRIPE_SECRET_KEY=` went out in clear —
the last of them the ordinary way to name a Stripe or an AWS key, missed by nothing but
where the word fell in the name. `genericSecretName` is what reads the rest of the name,
and it must **open on a new word** or it runs straight on through `secretary_id` and
`tokenised_at`, whose values a credential would then claim ahead of whatever category
really owns them, because a credential wins every overlap. It is bounded and carries no
dot, for the reason `propertyPathRe` is bounded.

**What opens a new word depends on the case the name is written in.** Reading any capital
as a boundary made that guard a no-op against a SCREAMING_SNAKE name, where every letter
is one: the tail opened on the `A` of `SECRETARY`, so `SECRETARY_ID=`, `TOKENISED_AT=` and
`SESSION_IDLE_TIMEOUT=` were masked while their lowercase twins were clean — one rule
giving two answers, decided by nothing but the case somebody typed.
`SECRETARIAT_EMAIL=bureau@example.fr` is what it cost: a credential wins every overlap, so
the address itself was replaced by `[SECRET_1]` instead of by a stand-in address. So:

- a **separator** always opens a word (`STRIPE_SECRET_KEY`, `VERY_SECRET_TOO`);
- a **capital** opens one only behind a keyword whose own last letter is lowercase, which
  is what a camelCase join is (`accessTokenValue` yes, `SECRETARY_ID` no);
- a **plural** goes with the keyword and then ends the word itself, so `API_TOKENS=` and
  `accessTokensValue=` are both names.

`genericSecretFiller` is what lets each keyword tolerate the two ways a name carries one
without spelling it: a **repeated letter** (`SUPER_SEECRET_VALUE`, a typo, and the shape
somebody reaches for to slip a value past a scanner) and **one identifier separator**
between letters (`S_E_C_R_E_T`). That is also why `genericSecretKeywordNames` is written
with no separators in it — `APIKEY` covers `API_KEY`, `api-key` and `apikey` at once,
which the alternation used to spell three times. **Insertions only, never omissions**:
letting a letter be missing would put `TKN` and `SCRT` in the list, and those are
initialisms.

**One optional class per gap, and not `+` on every letter.** Go's `regexp` is an NFA
simulation with no DFA behind it, so a scan costs what the program has states.
`S+E+C+R+E+T+` across twenty-three branches took the three expressions built from it to
38ms each over 88KB — 72% of the whole secret catalogue, against 0.56ms for the entire
second vendor tier. Written as a filler class the same three cost 26ms and match the same
names, because a gap that holds at most one character is one state rather than a loop.
What is given up is a letter repeated *twice* (`SEEECRET`), which is neither a typo nor a
shape anybody writes.

**It is deliberately not a sub-sequence match** — the letters in order with anything at
all between them, which is the obvious reading of the shape it exists for. Measured
against random base64: a sixty-character blob carries one of these keywords as a
sub-sequence 7% of the time, an eighty-character one 20%, and at a hundred and eighty-two
characters — the length of the `WARP_READ_TOKEN` recorded below — 94%. So a free
sub-sequence makes every long hash and integrity field in a lockfile a *name*, and it is
then whatever follows it that gets masked. Bounded to repeats and separators the same
measure is 0.00% at every length, because a generated blob has no separators in it and
its letters do not queue up. `sec-generic-blob-is-not-a-name` holds that in the corpus and
`TestGenericSecretNameReadsTheKeywordWhereverItFalls` holds both directions at once.

The bare expression deliberately carries **no `['"]?` before its group**. RE2 has no
lookbehind, so that absence is what keeps it off a quoted value: after the separator the
group must start on a non-quote, and `\s*` cannot step over the opening quote to reach the
value behind it. Four corpus cases hold the rule — `bound-generic-secret-bare-*` and
`bound-generic-secret-quoted-*` — and none of them is meaningful alone.

In source code it collapses. `password:` there is a *field* name, and what follows is an
expression, a type or an identifier. One real run against a repository produced, among
others:

```
MASK newPassword                    TO [SECRET_16]
MASK req.cookies.token              TO [SECRET_5]
MASK process.env.LLM_API_KEY        TO [SECRET_21]
MASK security.authorize(plainUser   TO [SECRET_27]
MASK CreationOptional<string        TO [SECRET_31]
```

None is a credential, and the model received a review of code whose identifiers had been
replaced by opaque tokens. The trailing `(` and the missing `>` are `noSentenceTail`
trimming the closing half of what the pattern had already eaten.

**Each rule is narrower than the obvious version**, because the tree already held a
case against each over-reach. Four of them were narrowed again after
`docs/secret-shapes-to-label.md` put forty-four shapes to a person one at a time: twelve
came back as credentials the engine was refusing, and the rules that refused them had all
been reading the *form* of the value rather than anything about code.

| Rule | Rejects | Why not wider |
|---|---|---|
| An **unclosed** bracket — on the **bare** spans only (`UnclosedBracketCheck`, a `Pattern.Verify`, not part of `GenericSecretCheck`) | `security.authorize({`, `generateSecret(`, `CreationOptional<string` | The balance is the rule, not the presence. And it hangs off the bare pattern because the check is handed the value without its quotes: a quoted value ends at its quote, so `password="Ab(12cd"` is somebody's one special character, and under the category it was refused as code. The span was cut out of the surrounding text, so an opener with no closer says the expression carries on past the end of the value; a matched pair inside a quoted value is punctuation somebody typed, and refusing it left `password="pa(ren)th1s"` and `password="[brackets]1"` in clear. A stray *closer* stays allowed — `password="hunter2)"` is a real credential ending on one, held by `bound-generic-secret-quoted-keeps-its-punctuation`. `;` and `,` left this rule entirely: no case in `TestGenericSecretCheckRejectsSourceCode` carries either, and `password="a;b;c1234x"` and `API_TOKENS=abc12345,def67890` were refused for holding one |
| Optional chaining, `?.` | `user?.token2`, `user.password?.replace(/./g` | The three code cases carrying a `?` all carry `?.`. A bare question mark before a letter is punctuation in a typed password, and refusing it left `password="Wh4t?Really"` in clear |
| Identifier-shaped **and** a capital **after a lowercase letter** (`hasCamelCaseJoin`), with no digit | `newPassword`, `totpToken`, `publicKey`, `newPasswordInString` | The rule was "no digit", and the digit cannot carry it: `PASSWORD=correcthorse`, `PASSWORD=changeme` and `password="correcthorse"` are real credentials of nothing but lowercase letters, and all three were forwarded in clear — the whole of the gap this catalogue was measured against betterleaks on. Every dotless digitless code case asserted in the tree is camelCase, because that is how code joins words into a name. A camelCase **join** and not any interior capital: read as any capital, `PASSWORD=HUNTER` was refused as an identifier while `PASSWORD=Hunter` was masked, and `MyPassword123!` and `Sup3rS3cr3tValue123` open on a capital and must stay credentials. The length floor came down with it, seven characters to six: `PASSWORD=mcjrx4` is a bad password, not an absent one |
| A word the language reserved | `string`, `default`, `null`, `boolean` | The cost of the rule above rather than a separate idea. Once a lowercase word counted as a credential, `secret_level: string` in this repository's own TypeScript claimed the type and `'X-Session-Id': 'default'` in its extension claimed the session name — both found by `TestOurOwnSourceGrowsNoCredentials`, which is the measure that matters because it is what a code review through this agent would have seen. `reservedWords` is a closed set of tokens some language spells exactly that way, so it can be checked rather than argued about. What it gives up is somebody whose password is literally `default` |
| A lowercase slug carrying the keyword | `reset-password`, `forgot-password`, `access-token`, `your-api-key-here` | Lowercase and hyphens only, so `MyPassword123!` — which carries the word too — stays a credential. The two-word keywords take either joiner: the rule listed `api_key` and not `api-key`, so `your-api-key-here` was masked while `your_api_key_here` was not |
| A **dotted chain** that *reads* a credential | `c.S3.SecretAccessKey`, `client.oauth2.Token`, `opts.Sha256Digest`, `secret = config.password` | Two bounds first, both still there: every segment letter-led and at most forty characters, because without them `WARP_READ_TOKEN=yrqUJ…Vhq8.37Zim…XlF` went out in clear — a hundred and eighty-two characters of base64url that bought this promise with one dot — and the dot has to be **interior** (`password="hunter2."` hands the check `hunter2.`, whose dot is the credential's own). But the chain also has to *say* something, because no shape can: `query.current` is two lowercase segments of ordinary length naming nothing, and so is `abcdef.ghijkl`. `readsACredential` takes either mark — **the last segment names the credential** (`config.password` is code fetching one, and so are `req.cookies.token`, `aws.Config.Credentials`, `headers.authorization`) or **the chain carries an interior capital** (which keeps `opts.Sha256Digest` and `utf8.RuneCountInString` refused). The first is the sentence the slug rule already writes for `reset-password`: a passphrase names neither what it unlocks nor where it was read from. The short tail keywords need a boundary in front of them (`credentialNameTailRe`): bare `key` matched the end of `monkey.donkey` and a real password behind `password:` was called code. **Six member accesses were given up to get `secret=abcdef.ghijkl` masked** — `query.current`, `query.new`, `query.repeat`, `body.new`, `body.repeat`, `a.b2` — and `TestGenericSecretCheckOverMasksLowercaseMemberAccess` holds them so a later recovery is noticed instead of read as a bug. The halves are unequal: a property access masked in a review is over-masking somebody can see and undo, and a password in clear is delivered |
| A value that is entirely a **variable reference** (`interpolationRe`) | `${DB_PASSWORD}`, `{{ secret }}`, `$(cat token)`, `%(pw)s` | Where a credential is read from, not one: a pasted `docker-compose.yml` came back with its references replaced by `[SECRET_n]`. A closed set of four syntaxes, matched whole |
| A value of **digits only** (`allDigits`) | `max_tokens=200000`, `SESSION_TTL=3600` | A number behind a keyword name is a tunable; `_TTL`, `_LENGTH`, `_COUNT` say so where the check cannot see the name |
| A leading `*` or `&`, **stripped rather than refused** | `*secretLevel`, `&cfg.Token`, `*opts.apiKey` | `want.SecretLevel = *secretLevel` — a line in this agent's own mask command — was claimed the moment a keyword no longer had to be the last segment of the name: a star is not an identifier character, so the digit rule never looked at the name behind it. Refusing it outright would drop `PASSWORD=*Hunter2*`, a real password; stripping hands the rest to the rules above, so it is only dropped when what it points at is *also* code-shaped by them, and `*secret123` keeps its digit |

The slash and the plus are deliberately **not** code punctuation: base64 is made of them,
and a secret is often base64.

**The slug rule is where shape runs out.** `MASK reset-password TO [SECRET_2]`, behind
`password:` in an object literal, is neither syntax nor an identifier — and it has the
*same shape* as `troisieme-valeur-longue`, which is a real credential in this corpus.
Lowercase words joined by hyphens describes both, so no rule about form was ever going
to separate them.

What does is the keyword: `reset-password` contains the very word that made the pattern
look at it, and **a passphrase does not name the thing it unlocks**. So a lowercase slug
carrying `password`, `secret`, `token` or `api_key` is a route name. It is restricted to
lowercase, hyphens and underscores, and that restriction is what keeps a weak-but-real
password out of it: `MyPassword123!` carries the word too, and its capitals, digits and
punctuation say it was typed as a secret rather than written as a route.

**The segment bounds in the dotted-chain rule are deliberately loose.** Forty characters is well
past the longest type and method names code really writes, and a cap tight enough to cut a
real member access would stop masking nothing — it would only start claiming code again,
which is what the rule exists to prevent. So
`security.authenticatedUsers.tokenOfTheCurrentSession` is asserted beside the token that
prompted the bound.

**A threshold on the mix of character classes cannot replace them**, and it was measured
on this tree's own values rather than argued. By digit density `opts.Sha256Digest` is
17.6% and the observed token 17.0%, so any floor puts a real member access and a live
credential on the same side of it. By class *count* the corpus's own session token,
`abcdef1234567890abcdef1234567890`, has two classes where `c.S3.SecretAccessKey` has
four — a floor at three would stop masking a real session cookie. It is the same finding
`SecretStrength` records for entropy, in the same direction: what separates a name from a
random run here is the *size and start of a segment*, not how varied its characters are.

**What still leaks is recorded rather than hidden**: a credential of nothing but letters
and dots — an unquoted `PASSWORD=correcthorse` — goes out in clear, and so does one whose
every dot-separated run is short and letter-led, `secret=abcdef.ghijkl`. The upgrade for
both is to read whether the value was quoted where it was found, which `Verify` cannot
see: it is handed the group, not its surroundings.

**Seven shapes the name-is-the-evidence rule reaches, beyond `NAME=value`.** Each was a
credential this catalogue forwarded whole, and each is one line of expression away.

| Shape | Written as | The guard that keeps it narrow |
|---|---|---|
| An `Authorization` header | `authHeaderRe` and `authHeaderLowercaseRe` — two patterns so each opens on a literal — taking what follows `Bearer`, `Basic` or `token` | The scheme is the evidence, not a field name — after `Bearer` there is a credential and nothing else. `Digest` is deliberately absent: its value is a parameter list, and masking it whole would replace the realm and the nonce along with the response. It reports `SECRET_GENERIC` **on purpose**, so it inherits `GenericSecretCheck` — "Bearer authentication" in a sentence has the shape of a header. The digitless-identifier rule used to refuse it; once that rule became a question about case, an all-lowercase word no longer reached it. `auth` went into the slug rule for a while and came back out: with no boundary in front of it, a four-letter run refused `author-of-words` and `my-authentic-horse` behind `PASSWORD=` — the `monkey.donkey` failure in the sibling rule. So `AuthHeaderCheck` carries the refusal on these two patterns alone |
| An XML **attribute** pair | `nugetPasswordRe` and its twin, `<add key="Password" value="…" />` | How NuGet writes a feed's password, and the file somebody pastes whole to ask why a restore fails. Two patterns because attribute order is not significant and `Group` is one index; case-insensitive past the element name. `SECRET_GENERIC` on purpose, as the header is, because a `value="…"` is as likely a path or a version |
| A session token | `sessionSecretRe`, `SESSION=<24+>` | `SESSION` is a name too common to be evidence alone — a type annotation `session: Http2Session` defeats the digit rule — so this one carries its own floor of twenty-four characters |
| An XML element | `xmlSecretRe`, `<apiKey>…</apiKey>` | A pattern of its own rather than `>` added to the separator, because the value has to stop where the closing tag opens: under the shared bare expression the span ran into `</apiKey` and the mask ate the tag. **The closing `</` is the guard** — `if (secret > threshold)` has the name, the separator and a value of the right size, and no tag after it |
| A hash arrow | `genericSecretSeparator`, `'token' => '…'` and `api_key -> …` | The two-character forms come **first** in the alternation: Go's regexp is leftmost-first, so with `[=:]` in front, `=>` matched on its `=` and the value began on the `>`. What this risks claiming is a dereference, and the check above refuses it — `$secret->getValue()` ends on an unclosed call, `$token->id` is under the value floor, `$password->hashedValue` is camelCase without a digit |
| A short declaration or a Makefile assignment | `genericSecretSeparator`, `apiKey := "…"`, `TOKEN ?= …`, `DB_PASSWORD ::= …` | With `[=:]` alone the colon was the separator and `\s*` could not step over the `=`, so `apiKey := "8dyfuiRyq=vVc3RRr_edRk-fK__JItpZ"` — Go, the language this agent reads most — went out in clear. Same leftmost-first reason as the arrows, same guard: `token := utils.jwtFrom(req)` ends on an unclosed call and `tokenName := 'ProdMyService'` is camelCase without a digit. `sec-generic-short-declaration` and `sec-generic-code-short-declaration` are the pair |
| A padded quoted value | `['"][ \t]*(…)[ \t]*['"]` | `quoteChars` holds `\s`, so one space behind the opening quote ended the expression before it began and `API_KEY=" hunter2-correct-horse "` went out in clear. Horizontal whitespace only: with `\s` the quote could sit on one line and the value on the next, and the span would swallow the newline |

**`CREDENTIAL` is a keyword and `KEY` is not.** A bare key is what half the configuration
languages there are call the left-hand side of a pair — `key: value` in YAML, `key=` in an
INI section, `key` in every map literal — so it names a credential no more often than it
names nothing at all, and the value behind it is whatever the document happened to hold.
What `CREDENTIAL` costs is a path: `GOOGLE_APPLICATION_CREDENTIALS=/etc/gcp/key.json` is
masked, because a path is not identifier-shaped and the check lets it through. That is the
cost `SECRET_FILE=` already carried, it is over-masking rather than a leak, and it is
reversible. `CREDS` sits beside it: `DB_CREDS=` is the short form the same people write, and the
repeated-letter tolerance cannot reach it from `CREDENTIAL`, so it went out in clear
(`sec-generic-creds`).

Narrowing a credential pattern is the change that leaks, so the two halves are asserted
together. `TestGenericSecretCheckRejectsSourceCode` carries the thirty values from that
run; `TestGenericSecretCheckKeepsCredentials` carries what must still be caught,
including both references in this tree. Neither is meaningful alone.

#### The secret level

`SECRET_GENERIC` is the one pattern whose evidence is a *name* beside the value, so the
value itself may be a generated key or an ordinary word. The level says how far down
that scale to mask, and `Detector.strongEnough` drops a match below it. The starting
value is `NEVERSEEN_SECRET_LEVEL` (`detector.EnvSecretLevel`, parsed by
`ParseSecretLevel`; `SecretLevels` lists the names a surface may offer), and the menu
bar, `neverseen mask --secret-level` and `PUT /policy` move it while the agent runs.

| Level | Masks | For |
|---|---|---|
| `weak` | everything the pattern finds, passphrases of plain words included | the default — what the agent did before the level existed |
| `medium` | values mixing two classes and up | ordinary use |
| `strong` | three classes or more, or long enough that nobody typed it | generated keys only. **Not** the answer to over-masking source code, and `strength.go` says it used to claim it was: measured over four megabytes of TypeScript, weak and strong claim the same values, because what a code review over-masks is personal-data categories no level touches — that is what `NoisyInCode` is for |

**It grades one pattern and only one.** Every other credential here is identified by a
prefix somebody can verify — `gsk_`, `sk-ant-`, `ghp_` — and a level that could stop
masking a real key would be a setting whose only effect is to leak.
`TestPolicySecretLevelGradesOnlyTheCatchAll` asserts both halves in one request.

**Classes, not entropy.** Shannon entropy at gitleaks' threshold (3.5) was measured
against this corpus and did worse than counting: it missed `hunter2-correct-horse`
(3.31) and `p@ssw0rd!` (2.95) — real credentials, both short — while still claiming
`security.authorize(plainUser` (4.01) and `process.env.LLM_API_KEY` (4.09). Entropy
rewards length and character spread, which is what a long code expression has and a
short password has not.

**A separator is not a class.** Counting the hyphen, `troisieme-valeur-longue` scored a
class above `troisiemevaleurlongue`, which put every passphrase of plain words into
medium and left **weak unreachable** — a level in the menu that could never differ from
the one below it. `-`, `_` and `.` say how a value is written, not how hard it is.

**The default is weak**, because it is what the agent did before the level existed. A
setting nobody has touched must not quietly mask less than it used to.

#### Load order cannot decide a secret against a PII category

`pickFromCluster` ranks a cluster on `(IsSecret ↓, Confidence ↓, length ↓, Start ↑)`.
For a secret against anything else the **first** key already differs, so the comparison
never reaches the tie-break that load order feeds. The guarantee is unconditional and
needs no setting — which is stronger than a setting would be, because it cannot be
configured wrong.

Measured as well as argued: reversing the two sets in `newCatalogue` and running the
whole corpus and the reference sample through both — 199 texts at the time; the corpus
alone is 515 cases today — produced **no difference at all**.

Load order still decides between *locales*, which is what `Locale.Priority` is for: nine
bare digits are a French SIREN and a US routing number, both non-secret, both scoring
the same, so the tie-break is reached and the earlier locale names the value.

`minConfidence` is 50 (`internal/detector/config.go:43`), set just at the level of the
weakest category worth reporting so every registered category is reportable and the
checksums do the discriminating. It is a constant, not a knob: a `TODO` records that a
deployment wanting only high-confidence matches would need it exposed, and that a knob
nothing sets is a knob nobody tested.

### Where a value sits, and what the text is

Two stages of `candidates` read outside the value, and both run last because everything
cheaper has already had its chance to reject.

**Placement** (`pkg/pii/placement.go`). `RejectedByPlacement` is handed the value and
`WindowSize` (32) bytes either side of it — enough for the name it was assigned to and the
punctuation around it, short enough that the rule cannot quietly become a scan of the
document. Three rules: `isNetworkPrefix` (`10.0.0.0/8` is a range, not a host),
`isObjectIdentifier` (a dotted run beside the word "oid") and `isPropertyValue`
(`z-index:2147483647` is not a phone number). **Credentials are never asked**: a secret is
recognised *by* its placement — a name and an assignment — so a rule reading the same
evidence the other way is the one that stops masking keys in configuration files.

**Code-awareness** (`internal/detector/code.go`). `looksLikeCode` is asked once per
scanned string: at least `codeMinLines` (3) lines, and either a fence or a shebang, or two
independent signals agreeing (`codeMinSignals`) — because each alone has an innocent
reading: prose in a narrow column has short lines, a numbered list has leading spaces, a
sentence about arithmetic has operators. In text that reads as code, a category marked
`NoisyInCode` is skipped. Only `DOB`, `PHONE` and `POSTAL_CODE` are marked; `EMAIL` is
not (an address in a fixture is still somebody's), `IP_ADDRESS` is not (a pasted
configuration *is* code, and an internal host in it is a topology), and no credential
ever is.

## The catalogue: patterns

`pii.Pattern` (`pkg/pii/pattern.go:12`) is one recognisable shape plus its category,
`Label`, `Locale`, `Group`, `Refine` and a per-pattern `Verify` (nil for almost every
pattern — see the checksum section for the two that carry one). Pattern files: `patterns_fr.go`,
`patterns_gb.go`, `patterns_us.go`, `patterns_intl.go`, `patterns_secret.go`.

**Order within a set is correctness, not taste.** The first pattern to claim a literal
wins, so a specific shape must precede a broader one: `sk-ant-` before `sk-`, a
fourteen-digit SIRET before the nine-digit SIREN inside it, France's checksummed
identifiers before Vietnam's CMND, which matches any bare nine digits.

### The two tiers of vendor prefixes

`patterns_secret.go` holds credentials in tiers, and the order *is* the tiering: a
generic `TOKEN=...` hint must never claim a span a documented prefix can name.

The first tier is the shapes reasoned about one at a time, each with its own `var` and
its own paragraph. The second is `vendorPrefixes`, a table of 108 vendors and 156
patterns (with ClickHouse's `4b1d<38>` sitting outside it, because that table has no
`Group` and a four-hex-character prefix has to be bounded at both ends) derived from the gitleaks/betterleaks catalogue (MIT, see `NOTICE`) and
rewritten to the rules below. It is a table rather than a hundred and fifty named `var`s because a
comment per row could only paraphrase the row; the reasoning that applies to all of them is
written once at the head of the table.

**Only the *value-only* rules were taken.** Roughly 40% of that catalogue identifies a
credential by the name beside it — `adafruit ... = <32 chars>` — and
`genericSecretQuotedRe` and `genericSecretBareRe` already read that shape. A second
reading of it would compete for the same span with no more evidence.

**What was deliberately left out is as considered as what came in.** Twenty-eight rules
have no literal prefix at all; they are the entire remaining cost (16.6 ms against
0.56 ms for the 150 that stayed) and they are also, almost all of them, *identifiers*
rather than credentials — an Azure tenant id, an eBay client id, a storage account
*name*, a Salesforce instance hostname, a Snowflake host. A bare 32-hex with no prefix
is the shape a commit SHA and a build id already have.

**Four real credentials are missing and it is recorded as a `TODO`**: Terraform Cloud
(`<14>.atlasv1.<60>`), MaxMind (`<6>_<29>_mmk`), a Tableau PAT and the numeric Facebook access
token (`<15-16 digits>|<27-40>` — the `EAA…` page token has a prefix and is in the
table). Each has a literal, but an *interior* one, which no prefix scan can use — so each
costs 0.6-1.7 ms alone, twenty times the rest of the tier put together. The upgrade is a
required-literal prefilter (`strings.Contains` before the regex), worth building for a
class and not for four.

**A prefilter is deliberately not built yet, and the measurement says why.** The obvious
reading of this tier is that 150 more patterns need one, because betterleaks affords 417
rules by running almost none of them. Measured per pattern over the corpus, three
patterns are 73% of this engine's scan — `xmlSecretRe` and the two
`genericSecret*Re` — and a keyword prefilter cannot touch those: their keyword set
(`genericSecretKeywordNames`, twenty-three names from `PASSWORD` and `TOKEN` to `DBPASS`
and `SESSIONKEY`) is present in nearly any configuration text.
The 150 that were added cost 0.56 ms, 1.4% on top of the scan as it stood.

`AllPatterns` (`pattern.go:66`) reads the locale registry rather than concatenating sets by
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
- **A leading `\b` and a `(?i)` over a literal prefix are both a cost, not just a
  style.** Go's regexp scans for a leading literal and skips ahead to it, which is what
  makes a prefix pattern nearly free — and either of those two destroys that scan.
  Measured over `docs/testCorpus.txt` (22 KB) while importing the second tier of vendor
  prefixes: 62 ms as the rules were written, 28 ms with the leading `\b` removed, 17 ms
  with `(?i)` removed too. The `(?i)` was wrong as well as slow, because a vendor prefix
  is case-significant: Figma issues `figd_`, never `FIGD_`.
- **An alternation of prefixes is two patterns, not one expression.** RE2 scans for a
  *single* leading literal, so it can use neither branch of `(?:EAAA|sq0atp-)`: that
  alternation cost 465 us over the corpus, and the same two shapes as separate patterns
  cost 6.8 us and 6.6 us. Two patterns of one category is the shape the catalogue already
  used for Slack, and `Label` is what tells a report which of them fired. HashiCorp Vault
  is the same lesson with a second edge: its legacy `s.<24 chars>` branch cost 443 us
  against 1.4 us for `hvs.` alone, and a bare `s.` before twenty-four alphanumerics is
  every second member access in a stack trace — narrowing was both the faster and the
  more correct change.
- **A trailing group that *consumes* a character makes the span eat the text after the
  value.** Imported rules commonly close on something like `(?:[^\w-]|$)`, and RE2 has no
  lookahead to express it for free. Five patterns arrived that way and each took the
  punctuation that ended the sentence around it: `pscale_pw_...Dc6.` came out a character
  long, and Fly.io's took the following comma. The fix is the one
  `genericSecretBareRe` already uses — a permissive body one character shorter, then a
  final character class that excludes what a sentence ends on (`noSentenceTail`).
- **Two vendor prefixes where one ends on the other are settled by the score being
  *equal*, not by a boundary.** Cerebras issues `csk-<48>` and `openAILegacyRe` is
  `sk-<20,>` with no left boundary, so it claimed the Cerebras key from offset 1 and
  masked it as an OpenAI key with the leading `c` in clear. Overlap arbitration goes
  credential, then confidence, then the longer span — so equal scores hand the decision
  to the span, and the longer prefix wins. `CatAnthropicKey` and `CatOpenAIKey` were
  already both 98 for exactly this reason (`sk-ant-` contains `sk-`), and the whole
  second tier is 98 so the rule holds for every containment in it: `ops_eyJ` over `eyJ`,
  `mercury_production_` over `ion_`. A left boundary would be the direct fix and cannot
  be afforded: consuming one character to reject it took `openAILegacyRe` from 6 us to
  654 us.
- **`Refine` is for a span the regex had to over-match**, and only the IBAN needs it.
  Tolerating the conventional grouping by four makes the expression greedy enough to
  swallow the following word, and only the checksum knows where the account ends. A pattern
  with a `Refine` is scanned **one match at a time, resuming after the refined end**
  (`refinedSpans`, `detector.go:308`): scanning them all at once resumes after the greedy
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

`ParseLocales` (`internal/detector/config.go:85`) accepts a code, a comma-separated list, or
`none`. **Unset means none**, deliberately: a wrong locale is worse than none — scanning
French data with the Vietnamese set masks its invoice numbers and timestamps as identity
cards — and an operator who never set the variable has not chosen that
(`DefaultConfig`, `config.go:77`).

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

`Substitution` (`internal/detector/policy.go:118`, `ParseSubstitution` at `:142`) is `token`
or `fake`, read from `NEVERSEEN_PII_SUBSTITUTION`. The zero value is `token`, so a `Config` built by hand keeps
the reversible behaviour without saying so.

`Detector.render` (`mask.go:102`) applies the mode to one index:

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
`detector.Unmask` (`mask.go:213`) carries it, with the upgrade path — a minimum length, or
marking the substitution invisibly.

## Tokens

`pii.Token(cat, index)` produces `[PREFIX_N]`; `tokenRe` / `tokenScanRe`
(`pkg/pii/token.go:20`) recognise the shape, `TokenAt` reads one at a position,
`ReplaceTokens` walks text expanding them, and `TokenTailLen` says how much of a trailing
fragment could still become a token. `maxTokenLen` is 48 (`token.go:113`).

Indices come from `Detector.nextIndex` (`mask.go:121`), per category, and are safe under
concurrency: `TestConcurrentPassesNeverShareAMask`.

## The sample is a reference, and must stay one

`pkg/pii/sample.go` holds one sample per locale plus the locale-independent and credential
sections. Each has to carry **every category its set detects and every notation each
pattern accepts** — all eleven French day-first date forms, both ISO separators, an
identifier compact and spaced, an address with and without its town. It is what an operator
reads to check their own data shape is covered, so a gap in it reads as a gap in the engine.

Any change to the catalogue — a new category, a newly accepted notation, a widened or
narrowed pattern — means updating the sample **in the same commit**. Six tests hold it for
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
