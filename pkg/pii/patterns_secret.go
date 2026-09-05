package pii

import (
	"regexp"
	"strings"
)

// Credentials, in three tiers: vendor-specific prefixes, then structural
// shapes, then the context-hinted generics. The order is the tiering — a
// generic "TOKEN=..." must never claim a span a vendor prefix can name.

// noSentenceTail is a character-class body: what a credential may contain but
// must not end on.
//
// Two of these patterns run their value to the next whitespace, which is right
// for the value and wrong for the sentence around it: "…@host:5432/db," and
// "SECRET=value," each take the separator with them. That matters more than a
// displaced character normally would — a credential is the one thing a reader
// cannot check against the original, so an eaten comma is an unexplained hole
// in the text they get back.
//
// "/" and "-" are deliberately absent: URLs and values genuinely end on them.
const noSentenceTail = `,.;:!?)\]}>`

// quoteChars are the delimiters a pasted value sits inside and therefore cannot
// contain: a value that ran through them swallowed the closing quote and, in
// JSON, the rest of the object with it. The backslash is here for the same
// reason: a value masked inside a JSON string ("PASSWORD=secret\"") runs to the
// closing \" and takes the escaping backslash with it, so the token replaces
// `secret\` and leaves `\"` as a bare `"` — malformed JSON the provider rejects.
const quoteChars = "\\s\"'`\\\\"

var (
	// --- vendor prefixes ---------------------------------------------------

	// OpenAI's modern keys, before the legacy shape below so the longer prefix
	// is the one reported.
	//
	// Three prefixes, not one. A service-account key ("sk-svcacct-") and an admin
	// key ("sk-admin-") reached no pattern at all: the legacy shape below admits
	// no dash, so it stopped after "sk-svcacct" — seven characters, under its own
	// floor — and both left in clear. An enumeration is right here because OpenAI
	// documents the set; a new prefix is a line, and until it is added the key is
	// invisible rather than partially masked.
	openAIModernRe = regexp.MustCompile(`sk-(?:proj|svcacct|admin)-[a-zA-Z0-9_-]{40,}`)
	// Anthropic, before the legacy OpenAI shape for the same reason.
	anthropicRe = regexp.MustCompile(`sk-ant-[a-zA-Z0-9_-]{20,}`)
	// Legacy OpenAI keys. The body admits no dash, which is what keeps it from
	// claiming "sk-ant-…" from the left, and the twenty-character floor is what
	// keeps it off a version string like "sk-1.2.3".
	//
	// TODO: it has no left boundary, so it is claimed from *inside* a longer run:
	// Cerebras issues "csk-<48>", and this matched it from offset 1. For a valid
	// second-tier token the tie at 98 settles it — equal scores hand the decision
	// to the longer span — but for anything shorter than a vendor's floor this
	// still wins, and masks a fragment while leaving the first character in clear.
	// A left boundary is the fix and it cannot be afforded here: consuming one
	// character to reject it destroys the literal-prefix scan, measured at 6us
	// against 654us over the corpus. The upgrade is a cheap required-literal
	// prefilter, the same one the second tier's own TODO asks for.
	openAILegacyRe = regexp.MustCompile(`sk-[a-zA-Z0-9]{20,}`)

	googleRe = regexp.MustCompile(`AIza[a-zA-Z0-9_-]{35}`)
	// Five prefixes, and "AKIA" alone was the wrong one to stop at. A temporary
	// key from AWS STS carries "ASIA", and that is the form a pasted terminal
	// session actually holds — an operator shows what `aws sts assume-role` just
	// printed far more often than a long-lived key. "ABIA" is a service bearer
	// token, "ACCA" a context-specific credential, "A3T" a key whose fourth
	// character varies. All five are the same twenty-character shape.
	awsAccessKeyRe = regexp.MustCompile(`(?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z0-9]{16}`)
	// Case-sensitive on purpose: "akia" in lowercase prose is not an AWS
	// identifier, and matching it would mask the word.

	// The AWS secret has no prefix of its own — forty base64 characters are not
	// distinguishable from any other blob — so the name in front of it is the
	// evidence. The span is the value, not the assignment: masking the name
	// would leave the reader unable to see which setting was redacted.
	awsSecretKeyRe = regexp.MustCompile(`(?i)(?:aws_secret_access_key|aws_secret|secret_access_key)['"]?\s*[=:]\s*['"]?([a-zA-Z0-9/+=]{40})`)

	githubRe = regexp.MustCompile(`gh[pousr]_[a-zA-Z0-9]{36,}`)
	// The fine-grained token, which is a different word and not a fifth letter in
	// the class above: "github_pat_" shares no prefix with "ghp_" and reached
	// nothing. It is the token GitHub now issues by default, so the gap covered
	// the common case rather than an exotic one. The body admits the underscore
	// that separates its two halves.
	githubFineGrainedRe = regexp.MustCompile(`github_pat_[a-zA-Z0-9_]{22,}`)
	gitlabRe            = regexp.MustCompile(`glpat-[a-zA-Z0-9_-]{20,}`)
	slackRe             = regexp.MustCompile(`xox[bpa]-[a-zA-Z0-9-]{10,}`)
	slackAppRe          = regexp.MustCompile(`xapp-[a-zA-Z0-9-]{10,}`)
	// A webhook URL is a credential — whoever holds it can post as the app — and
	// it carries no "user:password@", so the connection-string pattern never saw
	// it and nothing else did either. The host is the whole of the evidence.
	slackWebhookRe = regexp.MustCompile(`https://hooks\.slack\.com/(?:services|workflows)/[A-Za-z0-9/]{20,}`)
	// The leading \b is the whole point, and its absence corrupted ordinary text:
	// with none, this matched *inside* a word, so "task_test_abcdef…" was reported
	// as the Stripe key "sk_test_abcdef…" and an identifier came back with its
	// first two characters eaten. Any word ending in "sk", "rk" or "pk" does it —
	// task_, disk_, mask_. Go's \b is ASCII, which is all this prefix is.
	//
	// "prod" joins live and test because Stripe issues all three.
	stripeRe   = regexp.MustCompile(`\b[spr]k_(?:live|test|prod)_[a-zA-Z0-9]{20,}`)
	sendGridRe = regexp.MustCompile(`SG\.[a-zA-Z0-9_-]{22}\.[a-zA-Z0-9_-]{43}`)
	// Hex in either case: lowercase alone missed the uppercase form, and with a
	// locale loaded the IBAN pattern claimed it instead — "SK" opens a Slovak
	// IBAN — so the key was not merely missed, it was masked as somebody's bank
	// account. Overlap arbitration puts the credential first, which settles it
	// once both can match.
	twilioRe    = regexp.MustCompile(`SK[a-fA-F0-9]{32}`)
	npmRe       = regexp.MustCompile(`npm_[a-zA-Z0-9]{20,}`)
	pypiRe      = regexp.MustCompile(`pypi-[a-zA-Z0-9_-]{20,}`)
	dockerRe    = regexp.MustCompile(`dckr_pat_[a-zA-Z0-9_-]{20,}`)
	hfRe        = regexp.MustCompile(`hf_[a-zA-Z0-9]{20,}`)
	replicateRe = regexp.MustCompile(`r8_[a-zA-Z0-9]{20,}`)

	// Groq and xAI: the two of the eight providers this agent proxies whose key
	// carries a prefix somebody can verify. The prefix is the whole of the
	// evidence and it is enough — "gsk_" or "xai-" followed by thirty-odd
	// alphanumerics is not a shape ordinary text produces.
	//
	// The tail is {32,} rather than an exact count on purpose: neither vendor
	// documents a length, and a count taken from one observed key is a pattern
	// that stops matching the day they lengthen it — silently, which is the
	// failure mode a credential pattern must not have. Open-ended costs nothing
	// here because the prefix already did the discriminating.
	//
	// TODO: replace {32,} with the real length if either vendor ever documents
	// one. The three remaining providers — Mistral, Together, DeepInfra — have no
	// pattern here at all: see the note above SecretPatterns.
	groqRe = regexp.MustCompile(`\bgsk_[a-zA-Z0-9]{32,}`)
	xaiRe  = regexp.MustCompile(`\bxai-[a-zA-Z0-9]{32,}`)

	// --- structural shapes -------------------------------------------------

	// On "PRIVATE KEY", not on the delimiter shape: a certificate and a public
	// key are meant to be shared, and masking them breaks the paste for nothing.
	// Two patterns, and the first is the one that matters: the whole block,
	// header to footer, body included.
	//
	// On its own the header pattern below masked "-----BEGIN RSA PRIVATE KEY-----"
	// and forwarded every line of key material after it in clear — the delimiter
	// replaced, the key itself sent to the provider. Both gitleaks and trufflehog
	// match BEGIN through END for exactly this reason, and it is the one leak in
	// this catalogue where the masked span was decoration around the secret.
	//
	// Overlap arbitration settles the pair: same category, so the longer span
	// wins, which is the block wherever a block exists.
	//
	// TODO: the body is `[\s\S]*?`, as it is in gitleaks and trufflehog, so a text
	// that *mentions* a header and then pastes a whole key further down is matched
	// as one span from the mention to the footer, swallowing the prose between.
	// The direction is safe — over-masking, and reversible — but the reader loses
	// that prose. Narrowing the body to base64 does not fix it: prose is letters
	// and spaces, which base64 admits. A real fix bounds the gap by line shape.
	pemBlockRe = regexp.MustCompile(`-----BEGIN\s[A-Z\s]*PRIVATE\sKEY(?:\sBLOCK)?-----[\s\S]*?-----END\s[A-Z\s]*PRIVATE\sKEY(?:\sBLOCK)?-----`)

	// The header alone, which is how a private key is *mentioned* rather than
	// pasted: "the attachment starts with -----BEGIN OPENSSH PRIVATE KEY-----,
	// which is why the push was refused". Dropping it once the block pattern
	// exists would leave that sentence unmasked.
	//
	// The trailing BLOCK is what a PGP armour header carries, and requiring the
	// line to end on "KEY-----" missed it.
	pemRe = regexp.MustCompile(`-----BEGIN\s[A-Z\s]*PRIVATE\sKEY(?:\sBLOCK)?-----`)

	// Three base64url segments, the first two starting with the run a
	// base64-encoded "{" produces. Without that anchor any dotted blob of the
	// right lengths matched — a checksum, a build id.
	//
	// "eyJ" is what a compact "{"" gives. A claim set encoded with a newline or
	// indentation after the brace gives "ewo" instead, and its longer forms
	// "ewogIC" and "ewoid" — the same token, pretty-printed before encoding.
	//
	// The padding is the other half, and its absence was not a partial match but
	// no match at all: "=" is outside the segment class, so a padded first
	// segment ended early, the "." that had to follow was an "=", and the whole
	// expression failed. A padded JWT left in clear. RFC 7519 says unpadded, and
	// encoders emit padding anyway.
	jwtSeg = `[a-zA-Z0-9_-]{10,}={0,2}`
	jwtRe  = regexp.MustCompile(`(?:eyJ|ewogIC|ewoid|ewo)` + jwtSeg + `\.(?:eyJ|ewo)` + jwtSeg + `\.[a-zA-Z0-9_-]{10,}={0,2}`)

	// Any scheme://user:password@host.
	//
	// The scheme is generic on purpose. An enumerated list (mongodb, postgres,
	// …) sent every unlisted one — mariadb, mssql, rediss, plain https
	// basic-auth — to the email pattern instead, which gave the password a
	// reversible token that the response path then expanded back into a live
	// credential. Anything of this shape carries one, whatever the scheme is
	// called. The user part may be empty: "redis://:password@host".
	//
	// The password admits no "/". With it allowed, a plain URL carrying a port
	// and any later "@" read the port and path as a password:
	// "http://localhost:5173/@vite/client" came out destroyed as
	// "http://localhost:[CONN_STR_1]". A real password containing "/" arrives
	// percent-encoded.
	//
	// The host part stops before sentence punctuation — see noSentenceTail. It
	// is written as "a run, then one character that is not a tail" rather than
	// trimmed afterwards, because the span a pattern reports is what gets
	// replaced, and a trim would have to be repeated everywhere the span is read.
	//
	// The scheme is consumed but left out of the span, which is what Group is
	// for. So "postgres://admin:pw@db/app" is replaced as
	// "postgres://[CONN_STR_1]" rather than as a bare token: the scheme is not a
	// secret, and it is most of what makes the line answerable — a model asked to
	// fix a connection string needs to know it is Postgres and not Redis. It also
	// matches how every other named credential here reads, since the name in
	// front of a value is already outside the span.
	//
	// TODO: a password containing a quote or a backtick breaks the match
	// entirely, and the credential then falls through to the email pattern plus
	// clear text. Rare — quotes in pasted passwords are usually encoded — and
	// widening the class to quotes would swallow quoted prose. Revisit with a
	// two-pass match if the corpus ever carries one.
	connStrRe = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.\-]{1,29}://` +
		`([^` + quoteChars + `/@:]*:[^` + quoteChars + `@/]+@` +
		`[^` + quoteChars + `]*[^` + quoteChars + noSentenceTail + `])`)

	// --- context-hinted generics -------------------------------------------

	// NAME=value, where the name is the evidence and the value is the span.
	//
	// Two expressions, and what separates them is not length: it is whether the
	// value was quoted.
	//
	// A quoted value ends where its quote does, so punctuation inside it is the
	// value's own — `password="hunter2)"` is a password ending on a bracket, and
	// `"secret": "MyP@ssw0rd!"` is one ending on a bang. Both are taken whole.
	//
	// An unquoted value ends where the text around it resumes, so trailing
	// punctuation is that text — `[password=hunter2]` closes a bracket somebody
	// opened, and taking the `]` with the value emits `[password=[SECRET_1]` to the
	// model. That matters far more than a displaced character: this agent is read
	// by a model reviewing source code, and code with a delimiter removed is code
	// it analyses wrongly, silently, and reports on as if it were the caller's.
	//
	// This replaced a rule that decided on length — the value minus its tail at a
	// floor of eight, falling back to the raw run when trimming would drop under
	// it. That fallback existed so `PASSWORD=hunter2)` was not missed altogether,
	// and it worked, but the two halves of the catalogue then disagreed with each
	// other: `MyP@ssw0rd!` at eleven characters had its bang left in clear while
	// `hunter2)` at eight kept its bracket. One value's last character leaked and
	// the other's ate the syntax around it, decided by nothing but how long the
	// password was.
	//
	// The bare expression deliberately has no `['"]?` before its group. RE2 has no
	// lookbehind, so that absence is what keeps it off a quoted value: after the
	// separator the group must start on a non-quote, and `\s*` cannot step over the
	// opening quote to reach the value behind it.
	genericSecretNames = genericSecretName + `['"]?\s*` + genericSecretSeparator + `\s*`

	// The quotes admit padding, and the padding is not part of the value.
	//
	// quoteChars holds `\s`, so a single space behind the opening quote ended the
	// expression before it began: `API_KEY=" hunter2-correct-horse "` was
	// forwarded in clear while the same line without the spaces was masked. A
	// value written with room around it is the ordinary shape of a pasted
	// configuration line, and nothing about it says the credential is any less
	// one.
	//
	// Horizontal whitespace only, for the reason every other span here uses it:
	// with `\s` the opening quote could sit on one line and the value on the next,
	// and the span replaced would swallow the newline between them.
	genericSecretQuotedRe = regexp.MustCompile(genericSecretNames +
		`['"][ \t]*([^` + quoteChars + `]{6,})[ \t]*['"]`)

	genericSecretBareRe = regexp.MustCompile(genericSecretNames +
		`([^` + quoteChars + `]{5,}[^` + quoteChars + noSentenceTail + `])`)

	// The same name-is-the-evidence rule, written as an element rather than as an
	// assignment: `<apiKey>…</apiKey>`. A whole family of configuration files —
	// every Spring, .NET and Maven one there is — puts its credentials here, and
	// nothing in this catalogue read them.
	//
	// It is a pattern of its own rather than `>` added to the separator above,
	// because the value has to stop where the closing tag opens. Under the shared
	// bare expression the value class admits `<`, so the span ran on into
	// `</apiKey` and the mask ate the tag that closed the element.
	//
	// The closing `</` is the guard, and it is what keeps this off a comparison:
	// `if (secret > threshold)` has the name, the separator and a value of the
	// right size, and no closing tag anywhere after it.
	xmlSecretRe = regexp.MustCompile(genericSecretName +
		`>[ \t]*([^<>` + quoteChars + `]{6,})[ \t]*</`)

	// The same rule again, one step further out: the name is in an *attribute*
	// rather than in the element, which is how NuGet writes a feed's password.
	//
	// xmlSecretRe reads an element name, so `<add key="Password" value="…" />`
	// reached no pattern at all — the name it needs is inside `key=`, and the
	// value it must take is inside a different attribute. A NuGet.config with a
	// private feed in it is a file somebody pastes whole into a prompt to ask why
	// a restore fails.
	//
	// SECRET_GENERIC on purpose, as authHeaderRe is: that hands the value to
	// GenericSecretCheck, and what a `value="…"` attribute holds is as likely to
	// be a path or a version as a credential.
	// Two patterns and not one, because XML attribute order is not significant and
	// both orders are written: MSBuild emits `key` first, a hand-edited file often
	// puts `value` first, and one alternation cannot point Group at two places.
	// Case-insensitive past the element name for the same reason — `key="password"`
	// is as valid as `key="Password"` and the tooling emits both — while `<add`
	// itself stays literal, which is what keeps the prefix scan.
	nugetPasswordRe = regexp.MustCompile(`<add(?i)[ \t\r\n]+key="(?:cleartext)?password"` +
		`[ \t\r\n]+value="([^"]{8,})"`)
	nugetPasswordReversedRe = regexp.MustCompile(`<add(?i)[ \t\r\n]+value="([^"]{8,})"` +
		`[ \t\r\n]+key="(?:cleartext)?password"`)

	// `Authorization: Bearer …`, which is how a credential travels in a log, a
	// curl line, a header dump and every API page ever written — and nothing here
	// read it either. The whole of it went to the model in clear.
	//
	// The evidence is the scheme, not the name of a field, so this is the one
	// generic whose keyword is a value: after `Bearer` or `Basic` there is a
	// credential and nothing else. `token` is here because that is the scheme
	// GitHub documents; `Digest` is not, because its value is a comma-separated
	// parameter list and masking it whole would replace the realm and the nonce
	// along with the response.
	//
	// The category is SECRET_GENERIC deliberately, and it inherits
	// GenericSecretCheck with it. That guard is worth more here than the extra
	// precision a category of its own would buy: `Bearer $token->getValue()` has
	// the shape of a header and is code. "Bearer authentication" in a sentence is
	// prose, and AuthHeaderCheck refuses that on these two patterns alone — a
	// catalogue-wide rule for it refused real passwords carrying the letters.
	//
	// Two patterns, each opening on a literal, for the reason the vendor tier is
	// split: written `(?i)\bauthorization`, this ran over every request body at ten
	// times the cost of the two literals together — the `(?i)` and the `\b` are the
	// two habits that stop Go scanning for a leading literal. The header is spelled
	// `Authorization` by hand and `authorization` by HTTP/2, which lowercases every
	// name; nothing writes it any other way. Without the `\b`,
	// `Proxy-Authorization` is read too, which it should be.
	authHeaderRe          = regexp.MustCompile(`Authorization` + authHeaderTail)
	authHeaderLowercaseRe = regexp.MustCompile(`authorization` + authHeaderTail)

	clickhouseRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9])(4b1d[A-Za-z0-9]{38})(?:[^A-Za-z0-9]|$)`)

	// Sixty-four or more hex characters behind a key-shaped name. The floor is
	// what separates an encryption key from a commit SHA somebody assigned to a
	// field: forty hex under "KEY=" is a truncated SHA, and masking it breaks a
	// paste for nothing.
	// A session token, which needs a length floor the shared keyword list cannot
	// give it.
	//
	// "session" was in that list for one commit and came straight back out. The
	// evidence there is the name, and the value is then held apart from source code
	// by GenericSecretCheck — whose rule is that an identifier carrying a digit is a
	// credential, because `Sup3rS3cr3tValue123` is one. A *type annotation* defeats
	// that rule completely: `session: Http2Session` is an identifier with a digit,
	// and so are `Http2Stream`, `Base64String` and every other type name built on a
	// numbered standard. Measured over four megabytes of third-party TypeScript it
	// claimed nine of them, and over a hand-written sample of ordinary application
	// code it claimed one in ninety-five lines.
	//
	// What separates the two is length, not shape. A session token is a generated
	// blob — thirty-two hex characters in the cookie this exists for — and a type
	// name is a word or two. Twenty-four is above every type name in those two
	// corpora and below every real token, and it is a floor the shared expression
	// cannot carry because its keywords share one alternation and one group.
	//
	// GenericSecretCheck still applies, so a long expression behind `session:` is
	// still rejected on its punctuation.
	sessionSecretRe = regexp.MustCompile(`(?i)SESSION['"]?\s*[=:]\s*['"]?([A-Za-z0-9_./+-]{24,})['"]?`)

	hexSecretRe = regexp.MustCompile(`(?i)(?:KEY|SECRET|ENCRYPTION_KEY|SIGNING_KEY|HMAC_KEY)['"]?\s*[=:]\s*['"]?([0-9a-f]{64,})['"]?`)
)

// genericSecretKeywordNames are the names that make the value behind them evidence
// of a credential. They are written with no separator in them, because
// genericSecretFiller tolerates one between every pair of letters: APIKEY covers
// API_KEY, api-key and apikey at once, which is what the alternation used to spell
// three times.
var genericSecretKeywordNames = []string{
	"PASSWORD", "PASSWD", "SECRET", "TOKEN", "APIKEY", "ACCESSKEY",
	"ENCRYPTIONKEY", "PRIVATEKEY", "PRIVKEY", "AUTHTOKEN", "AUTHKEY",
	"CLIENTKEY", "SERVICEKEY", "ACCOUNTKEY", "DBKEY", "DATABASEKEY",
	"KEYPASS", "DBPASS", "DATABASEPASS", "SESSIONID", "SESSIONKEY",
	// "CREDENTIAL" and not "KEY". A bare key is what half the configuration
	// languages there are call the left-hand side of a pair — `key: value` in
	// YAML, `key=` in an INI section, `key` in every map literal — so it names a
	// credential no more often than it names nothing at all, and the value behind
	// it is whatever the document happened to hold. "credential" names one thing.
	//
	// What it costs is a path: GOOGLE_APPLICATION_CREDENTIALS=/etc/gcp/key.json is
	// masked, because a path is not identifier-shaped and GenericSecretCheck lets
	// it through. That is the same cost SECRET_FILE= already carries, it is
	// over-masking rather than a leak, and it is reversible.
	//
	// "CREDS" beside it, because `DB_CREDS=` is the short form the same people
	// write, and it carries none of the letters of "CREDENTIAL" past the fifth:
	// the repeated-letter tolerance cannot reach it. It went out in clear.
	"CREDENTIAL", "CREDS",
}

// genericSecretFiller is what may sit between two letters of a keyword without the
// name having stopped being that keyword: the preceding letter once more
// (`SUPER_SEECRET_VALUE`, a typo, and the shape somebody reaches for to slip a value
// past a scanner) or one identifier separator (`S_E_C_R_E_T`). Insertions only,
// never omissions: allowing a letter to be *missing* would put `TKN` and `SCRT` in
// the list, and those are initialisms.
//
// One optional class per gap rather than `+` on every letter, and the difference is
// not cosmetic. Go's regexp is an NFA simulation with no DFA behind it, so the cost
// of a scan tracks the number of states in the program: `S+E+C+R+E+T+` across
// twenty-three branches took the three expressions built from it to 38ms each over
// 88KB, against 23ms for the bare literals — the credential scan's own dominant
// cost, and more than twice everything else in this file put together. Written as a
// filler the same three cost 26ms and match the same names, because a gap that can
// hold at most one character is one state rather than a loop. What is given up is a
// letter repeated *twice* (`SEEECRET`), which is neither a typo nor a shape anybody
// writes.
//
// The tolerance is deliberately not a sub-sequence match — the letters in order with
// anything at all between them, which is the obvious reading of the shape this
// exists for. Measured against random base64: a sixty-character blob carries one of
// these keywords as a sub-sequence 7% of the time, an eighty-character one 20%, and
// at a hundred and eighty-two characters — the length of the WARP_READ_TOKEN
// GenericSecretCheck already records — 94%. So a free sub-sequence makes every long
// hash, integrity field and token in a lockfile a *name*, and it is then whatever
// follows it that gets masked. Bounded to repeats and separators the same measure is
// 0.00% at every length, because a generated blob has no separators in it and its
// letters do not queue up.
func genericSecretFiller(previous byte) string {
	return `[` + string(previous) + `_\-]?`
}

// genericSecretName is a whole name carrying one of those keywords, wherever the
// keyword falls in it, and the tail is what says the keyword is a word of that name
// rather than the first letters of a longer one.
//
// The tail used to be one optional `(?:[_\-]|[A-Z])…` for every keyword, and against
// a SCREAMING_SNAKE name that guard was a no-op: every letter of `SECRETARY_ID` is a
// capital, so the tail opened on the `A` of `ARY` and the name matched. Its lowercase
// twin `secretary_id` was refused, which is the asymmetry that says the rule was
// reading nothing — and `SECRETARIAT_EMAIL=bureau@example.fr` had the address itself
// claimed as a credential, which wins every overlap, so the value went out as
// [SECRET_1] instead of a stand-in address.
//
// So the tail is chosen by the case of the keyword's own last letter, which is the
// only thing that says where a word ended:
//
//   - a separator tail, always available: `STRIPE_SECRET_KEY`, `VERY_SECRET_TOO`.
//   - a capital tail, only behind a keyword whose last letter is lowercase, which is
//     what a camelCase boundary is: `accessTokenValue` yes, `SECRETARY_ID` no.
//
// A name that carries the keyword as a whole word is still a name — `PASSWORD_MIN_LENGTH`
// is masked, as it was — because that is the rule this exists to serve, and it now
// reads the same whichever case it is written in.
var genericSecretName = buildGenericSecretName()

const (
	// A new word behind a separator, whatever case the name is written in.
	genericSecretSeparatorTail = `(?:[_\-][A-Za-z0-9_\-]{0,32})?`

	// A new word behind a capital, which only counts as one when the letter before
	// it was lowercase. Bounded for the reason propertyPathRe is bounded, and
	// carrying no dot, so a member access cannot be read as one long name.
	genericSecretCamelTail = `[A-Z][A-Za-z0-9_\-]{0,32}`
)

func buildGenericSecretName() string {
	alternatives := make([]string, 0, len(genericSecretKeywordNames))
	for _, name := range genericSecretKeywordNames {
		var expanded strings.Builder

		// Every letter but the last, case-insensitive, with one filler between each
		// pair and one before the last letter.
		expanded.WriteString(`(?i:`)
		for i := 0; i < len(name)-1; i++ {
			if i > 0 {
				expanded.WriteString(genericSecretFiller(name[i-1]))
			}
			expanded.WriteByte(name[i])
		}
		expanded.WriteString(genericSecretFiller(name[len(name)-2]))
		expanded.WriteString(`)`)

		// The last letter, and its case decides which tail the name may carry. A
		// plural goes with it — `API_TOKENS=` and `api_tokens=` are both a name for
		// several of the thing the keyword names — and it is the plural that then
		// ends the word, so a capital behind `TOKENs` opens a camelCase tail exactly
		// as a lowercase last letter does.
		last := name[len(name)-1:]
		lower := strings.ToLower(last)
		expanded.WriteString(`(?:(?i:` + last + `)(?i:s)?` + genericSecretSeparatorTail +
			`|(?:` + lower + `|(?i:` + last + `)s)` + genericSecretCamelTail + `)`)

		alternatives = append(alternatives, expanded.String())
	}
	return `(?:` + strings.Join(alternatives, "|") + `)`
}

// genericSecretSeparator is what a configuration language puts between a name and
// its value. `=>` is a PHP and Perl hash — `'token' => 'ory_pat_…'` — and `->` is
// how a pasted note and a Ruby-ish config write the same pair; both were forwarded
// in clear for want of two characters.
//
// `:=` is Go's short declaration — the language this agent is written in and the
// one Claude Code reads through it most — and `?=` and `::=` are how a Makefile
// assigns. `apiKey := "8dyfuiRyq=vVc3RRr_edRk-fK__JItpZ"` went out in clear: the
// `:` was taken as the separator and `\s*` could not step over the `=`, so the
// value never began.
//
// The longer forms come first because Go's regexp is leftmost-first: with the class
// in front, `=>` matched on its `=` and the value then began on the `>`, which is a
// value the check below rejects and a credential nobody masked. `:=` fails the same
// way on its `:`.
//
// A member access is the shape this risks claiming, and GenericSecretCheck is what
// refuses it: `$secret->getValue()` ends on a call, `$token->id` is under the value
// floor, and `$password->hashedValue` is an identifier carrying no digit. A short
// declaration of an identifier — `token := utils.jwtFrom(req)` — ends on a call too.
const genericSecretSeparator = `(?::{1,3}=|\?=|=>|->|[=:])`

// authHeaderTail is what follows the header's name: a separator, the scheme and
// the credential. The scheme keeps its `(?i)` — it is not the leading literal, so
// it costs nothing there — because `bearer` and `BEARER` are both written.
const authHeaderTail = `['"]?[ \t]*[=:][ \t]*['"]?[ \t]*` +
	`(?i:bearer|basic|token)[ \t]+` +
	`([^` + quoteChars + `]{6,}[^` + quoteChars + noSentenceTail + `])`

// Three of the eight providers this agent proxies have no pattern of their own, and
// that is a decision rather than an omission.
//
// A Mistral key is thirty-two bare alphanumerics with no prefix. Written as a
// pattern it claims every MD5 digest, every abbreviated commit id and every
// thirty-two character session id there is — measured, not supposed. Together and
// DeepInfra document no format at all, and a pattern guessed from one key somebody
// posted is one that fails after an operator has trusted it.
//
// What covers them is the context: genericSecretRe reads MISTRAL_API_KEY=… and
// TOGETHER_API_KEY=… and masks the value, which is how an unprefixed key actually
// appears — in a .env, an export, a pasted configuration. A bare key in prose is not
// distinguishable from a hash by anything this engine can see, and claiming it would
// cost more than it saves.

// --- vendor prefixes, second tier ----------------------------------------
//
// Derived from the gitleaks/betterleaks rule catalogue (MIT, see NOTICE), and
// only its *value-only* rules: a prefix the vendor documents and a reader can
// verify. The rules that require a name beside the value -- "adafruit ... =
// <32 chars>" -- are deliberately absent, because genericSecretQuotedRe and
// genericSecretBareRe already read that shape, and a second reading of it
// would compete for the same span with no more evidence.
//
// Rewritten rather than copied, in three ways that are this file's own rules,
// each measured over docs/testCorpus.txt (22 KB):
//
//   - No leading \b. It defeats Go's literal-prefix scan, which is what makes
//     a prefix pattern free: 62ms for the tier as written, 28ms without it.
//   - No (?i) over a literal prefix. It defeats the same scan (28ms -> 17ms)
//     and it is wrong: Figma issues "figd_", never "FIGD_".
//   - No trailing boundary group. gitleaks closes most rules on something like
//     `(?:[^\w-]|$)`, which *consumes* a character, so the span would eat the
//     punctuation after the token -- the failure noSentenceTail exists for.
//
// Together those bring the whole tier to 557us, 1.4% on top of the scan as it
// stands. The rules with no literal prefix at all are left out: they are the
// remaining cost, and they are also identifiers rather than credentials -- a
// tenant id, a client id, an account name, an instance hostname.
//
// TODO: four real credentials are missing because their only literal is
// interior, which no prefix scan can use: Terraform Cloud
// ("<14>.atlasv1.<60>"), MaxMind ("<6>_<29>_mmk"), a Tableau PAT and the numeric
// Facebook access token ("<15-16 digits>|<27-40>" -- the EAA... page token has a
// prefix and is in the table). Each costs 0.6-1.7ms alone, twenty times the rest of
// this tier put together. The upgrade is a required-literal prefilter --
// strings.Contains before the regex -- which is worth building for a class,
// not for four.
//
// One category per vendor whatever the role of the credential, and several
// patterns per category where a vendor issues several shapes, as Slack's do.
var vendorPrefixes = []struct {
	Category Category
	Vendor   string
	Regex    *regexp.Regexp
}{
	{CatOnePasswordSecret, "1Password", regexp.MustCompile(`A3-[A-Z0-9]{6}-(?:(?:[A-Z0-9]{11})|(?:[A-Z0-9]{6}-[A-Z0-9]{5}))-[A-Z0-9]{5}-[A-Z0-9]{5}-[A-Z0-9]{5}`)},
	{CatOnePasswordSecret, "1Password", regexp.MustCompile(`ops_eyJ[a-zA-Z0-9+/]{250,}={0,3}`)},
	{CatAdobeSecret, "Adobe", regexp.MustCompile(`p8e-(?i)[a-z0-9]{32}`)},
	{CatAgeSecret, "age", regexp.MustCompile(`AGE-SECRET-KEY-1[QPZRY9X8GF2TVDW0S3JN54KHCE6MUA7L]{58}`)},
	{CatAikidoSecret, "Aikido", regexp.MustCompile(`AIK_CI_[A-Za-z0-9]{20,44}`)},
	{CatAikidoSecret, "Aikido", regexp.MustCompile(`AIK_SECRET_[A-Za-z0-9]{64}`)},
	{CatAirtableSecret, "Airtable", regexp.MustCompile(`pat[[:alnum:]]{14}\.[a-f0-9]{64}`)},
	{CatAlibabaSecret, "Alibaba Cloud", regexp.MustCompile(`LTAI[A-Za-z0-9]{17,21}`)},
	{CatAlibabaSecret, "Alibaba Cloud", regexp.MustCompile(`STS\.[A-Za-z0-9]{16,64}`)},
	{CatApifySecret, "Apify", regexp.MustCompile(`apify_api_[A-Za-z0-9]{34,38}`)},
	{CatArtifactorySecret, "Artifactory", regexp.MustCompile(`AKCp[A-Za-z0-9]{68,70}`)},
	{CatArtifactorySecret, "Artifactory", regexp.MustCompile(`cmVmd[A-Za-z0-9]{59}`)},
	// The dollar is part of the prefix Asaas issues, not a shell variable.
	{CatAsaasSecret, "Asaas", regexp.MustCompile(`\$aact_prod_[A-Za-z0-9_-]{20,100}`)},
	{CatAsaasSecret, "Asaas", regexp.MustCompile(`\$aact_hmlg_[A-Za-z0-9_-]{20,100}`)},
	// Four prefixes, four patterns, for the reason Square's two are split below.
	{CatAuthressSecret, "Authress", regexp.MustCompile(`sc_[a-z0-9]{5,30}\.[a-z0-9]{4,6}\.acc[_-][a-z0-9-]{10,32}\.[a-z0-9+/_=-]{30,120}`)},
	{CatAuthressSecret, "Authress", regexp.MustCompile(`ext_[a-z0-9]{5,30}\.[a-z0-9]{4,6}\.acc[_-][a-z0-9-]{10,32}\.[a-z0-9+/_=-]{30,120}`)},
	{CatAuthressSecret, "Authress", regexp.MustCompile(`scauth_[a-z0-9]{5,30}\.[a-z0-9]{4,6}\.acc[_-][a-z0-9-]{10,32}\.[a-z0-9+/_=-]{30,120}`)},
	{CatAuthressSecret, "Authress", regexp.MustCompile(`authress_[a-z0-9]{5,30}\.[a-z0-9]{4,6}\.acc[_-][a-z0-9-]{10,32}\.[a-z0-9+/_=-]{30,120}`)},
	{CatAzureAppConfigSecret, "Azure App Configuration", regexp.MustCompile(`Endpoint=(?P<azure_appconfig_endpoint>https://[a-z0-9-]+\.azconfig\.io);Id=(?P<azure_appconfig_id>[^;\s'"]{4,80});Secret=([A-Za-z0-9+/]{36,100}={0,2})`)},
	{CatAzureServiceBusSecret, "Azure Service Bus", regexp.MustCompile(`Endpoint=sb://[a-z0-9-]+\.servicebus\.windows\.net/;SharedAccessKeyName=[^;=\s\x27"]{1,128};SharedAccessKey=[A-Za-z0-9+/]{32,100}={0,2}`)},
	{CatBraveSearchSecret, "Brave Search", regexp.MustCompile(`BSA[A-Za-z0-9_-]{24,40}`)},
	// Seven prefixes, seven patterns, for the reason Square's two are split below:
	// as one alternation this cost 10.4us against ~5us for a single branch, and the
	// comment three lines above Square said every alternation in this tier was split
	// while this one was not.
	{CatBuildkiteSecret, "Buildkite", regexp.MustCompile(`bkaa_[A-Za-z0-9_-]{75}`)},
	{CatBuildkiteSecret, "Buildkite", regexp.MustCompile(`bkaj_[A-Za-z0-9_-]{333}`)},
	{CatBuildkiteSecret, "Buildkite", regexp.MustCompile(`bkar_[A-Za-z0-9_-]{73}`)},
	{CatBuildkiteSecret, "Buildkite", regexp.MustCompile(`bkct_[A-Za-z0-9_-]{73}`)},
	{CatBuildkiteSecret, "Buildkite", regexp.MustCompile(`bkpt_[A-Za-z0-9_-]{199}`)},
	{CatBuildkiteSecret, "Buildkite", regexp.MustCompile(`bkpat_[A-Za-z0-9_-]{54}`)},
	{CatBuildkiteSecret, "Buildkite", regexp.MustCompile(`bkps_[A-Za-z0-9_-]{64}`)},
	{CatBuildkiteSecret, "Buildkite", regexp.MustCompile(`bkua_(?:[a-z0-9]{40}|[a-z0-9]{53})`)},
	{CatCanvaSecret, "Canva", regexp.MustCompile(`cnvca[a-zA-Z0-9_-]{51}`)},
	{CatCerebrasSecret, "Cerebras", regexp.MustCompile(`csk-[a-z0-9]{48}`)},
	{CatCircleciSecret, "CircleCI", regexp.MustCompile(`CCIPAT_[a-zA-Z0-9]{22}_[a-z0-9]{40}`)},
	// ClickHouse is in the list below rather than here: its prefix needs a boundary
	// on both sides, and a boundary has to be consumed, which needs a Group this
	// table has no room for.
	{CatClojarsSecret, "Clojars", regexp.MustCompile(`CLOJARS_[a-z0-9]{60}`)},
	{CatCloudflareSecret, "Cloudflare", regexp.MustCompile(`v1\.0-[a-f0-9]{24}-[a-f0-9]{146}`)},
	{CatCloudsmithSecret, "Cloudsmith", regexp.MustCompile(`csa_[a-f0-9]{30}[A-Za-z0-9]{6}`)},
	{CatCockroachDBSecret, "CockroachDB Cloud", regexp.MustCompile(`CCDB1_[A-Za-z0-9]{22}_[A-Za-z0-9]{40}`)},
	{CatConfigcatSecret, "ConfigCat", regexp.MustCompile(`configcat-sdk-1/[A-Za-z0-9_-]{22}/[A-Za-z0-9_-]{22}`)},
	{CatDatabricksSecret, "Databricks", regexp.MustCompile(`dapi[a-f0-9]{32}(?:-\d)?`)},
	{CatDataStaxAstraSecret, "DataStax Astra", regexp.MustCompile(`AstraCS:[A-Za-z0-9]{20,}`)},
	{CatDenoSecret, "Deno Deploy", regexp.MustCompile(`ddp_[A-Za-z0-9]{36}`)},
	{CatDevcycleSecret, "DevCycle", regexp.MustCompile(`dvc_client_[A-Za-z0-9]{8,32}`)},
	{CatDevcycleSecret, "DevCycle", regexp.MustCompile(`dvc_mobile_[A-Za-z0-9]{8,32}`)},
	{CatDevcycleSecret, "DevCycle", regexp.MustCompile(`dvc_server_[A-Za-z0-9]{8,32}`)},
	{CatDevinSecret, "Devin", regexp.MustCompile(`apk_user_[A-Za-z0-9+/]{120,180}={0,2}`)},
	{CatDevinSecret, "Devin", regexp.MustCompile(`apk_[A-Za-z0-9+/]{80,100}={0,2}`)},
	{CatDevinSecret, "Devin", regexp.MustCompile(`cog_[a-z2-7]{52}`)},
	{CatDigitaloceanSecret, "DigitalOcean", regexp.MustCompile(`doo_v1_[a-f0-9]{64}`)},
	{CatDigitaloceanSecret, "DigitalOcean", regexp.MustCompile(`dop_v1_[a-f0-9]{64}`)},
	{CatDigitaloceanSecret, "DigitalOcean", regexp.MustCompile(`dor_v1_[a-f0-9]{64}`)},
	{CatDopplerSecret, "Doppler", regexp.MustCompile(`dp\.pt\.(?i)[a-z0-9]{43}`)},
	{CatDuffelSecret, "Duffel", regexp.MustCompile(`duffel_(?:test|live)_(?i)[a-z0-9_\-=]{43}`)},
	{CatDynatraceSecret, "Dynatrace", regexp.MustCompile(`dt0c01\.(?i)[a-z0-9]{24}\.[a-z0-9]{64}`)},
	{CatEasypostSecret, "EasyPost", regexp.MustCompile(`EZAK(?i)[a-z0-9]{54}`)},
	{CatEasypostSecret, "EasyPost", regexp.MustCompile(`EZTK(?i)[a-z0-9]{54}`)},
	{CatElasticSecret, "Elastic Cloud", regexp.MustCompile(`essu_[A-Za-z0-9_\-]{60,200}={0,2}`)},
	{CatExoscaleSecret, "Exoscale", regexp.MustCompile(`EXO[a-zA-Z0-9]{24,30}`)},
	{CatFacebookSecret, "Facebook", regexp.MustCompile(`EAA[MC](?i)[a-z0-9]{100,}`)},
	{CatFigmaSecret, "Figma", regexp.MustCompile(`figd_[A-Z0-9_-]{38,42}`)},
	{CatFlutterwaveSecret, "Flutterwave", regexp.MustCompile(`FLWSECK_TEST-(?i)[a-h0-9]{12}`)},
	{CatFlutterwaveSecret, "Flutterwave", regexp.MustCompile(`FLWSECK_TEST-(?i)[a-h0-9]{32}-X`)},
	// Two defects in the rule as written. The body admits a comma, so the span ran
	// into the one after the token -- the failure noSentenceTail exists for -- and
	// `\s` let a hundred-character value begin on the line below, which is why
	// every long span in this file uses horizontal whitespace only.
	{CatFlyIOSecret, "Fly.io", regexp.MustCompile(`FlyV1[ \t][A-Za-z0-9=_\-,/+]{99,}[A-Za-z0-9=_/+]`)},
	{CatFrameIOSecret, "Frame.io", regexp.MustCompile(`fio-u-(?i)[a-z0-9\-_=]{64}`)},
	{CatGCNotifySecret, "GC Notify", regexp.MustCompile(`ApiKey-v1 gcntfy-[a-zA-Z0-9_]+-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)},
	{CatGoogleGeminiSecret, "Google Gemini", regexp.MustCompile(`AQ\.Ab8RN6[A-Za-z0-9_-]{44}`)},
	{CatGrafanaSecret, "Grafana", regexp.MustCompile(`eyJrIjoi[A-Za-z0-9+/]{40,380}={0,2}`)},
	{CatGrafanaSecret, "Grafana", regexp.MustCompile(`glc_[A-Za-z0-9+/]{40,150}={0,2}`)},
	{CatGrafanaSecret, "Grafana", regexp.MustCompile(`glsa_[A-Za-z0-9]{32}_[A-Fa-f0-9]{8}`)},
	{CatHarnessSecret, "Harness", regexp.MustCompile(`pat\.[a-zA-Z0-9_-]{22}\.[0-9a-f]{24}\.[a-zA-Z0-9]{20}`)},
	{CatHarnessSecret, "Harness", regexp.MustCompile(`sat\.[a-zA-Z0-9_-]{22}\.[0-9a-f]{24}\.[a-zA-Z0-9]{20}`)},
	{CatHerokuSecret, "Heroku", regexp.MustCompile(`(?:HRKU-AA[0-9a-zA-Z_-]{58})`)},
	{CatInfracostSecret, "Infracost", regexp.MustCompile(`ico-[a-zA-Z0-9]{32}`)},
	{CatIntra42Secret, "42 Intra", regexp.MustCompile(`s-s4t2(?:ud|af)-(?i)[abcdef0123456789]{64}`)},
	{CatIonicSecret, "Ionic", regexp.MustCompile(`ion_[A-Za-z0-9]{42}`)},
	{CatLangSmithSecret, "LangSmith", regexp.MustCompile(`lsv2_pt_[0-9a-fA-F]{32}_[0-9a-fA-F]{10}`)},
	{CatLangSmithSecret, "LangSmith", regexp.MustCompile(`lsv2_sk_[0-9a-fA-F]{32}_[0-9a-fA-F]{10}`)},
	{CatLichessSecret, "Lichess", regexp.MustCompile(`lip_[A-Za-z0-9_]{16,60}`)},
	{CatLinearSecret, "Linear", regexp.MustCompile(`lin_api_(?i)[a-z0-9]{40}`)},
	{CatMailersendSecret, "MailerSend", regexp.MustCompile(`mlsn\.[A-Za-z0-9]{30,100}`)},
	{CatMercurySecret, "Mercury", regexp.MustCompile(`mercury_production_[a-z]{3,6}_[A-Za-z0-9]{40,50}_yrucrem`)},
	{CatMergifySecret, "Mergify", regexp.MustCompile(`mergify_application_key_[A-Za-z0-9_-]{40,200}`)},
	{CatMicrosoftTeamsSecret, "Microsoft Teams", regexp.MustCompile(`https://[a-z0-9]+\.webhook\.office\.com/webhookb2/[a-z0-9]{8}-([a-z0-9]{4}-){3}[a-z0-9]{12}@[a-z0-9]{8}-([a-z0-9]{4}-){3}[a-z0-9]{12}/IncomingWebhook/[a-z0-9]{32}/[a-z0-9]{8}-([a-z0-9]{4}-){3}[a-z0-9]{12}`)},
	{CatMinimaxSecret, "MiniMax", regexp.MustCompile(`sk-api-[A-Za-z0-9_-]{119}`)},
	{CatMongoDBAtlasSecret, "MongoDB Atlas", regexp.MustCompile(`mdb_sa_sk_[A-Za-z0-9_-]{40}`)},
	{CatNeonSecret, "Neon", regexp.MustCompile(`napi_[A-Za-z0-9]{64}`)},
	{CatNotionSecret, "Notion", regexp.MustCompile(`ntn_[0-9]{11}[A-Za-z0-9]{32}[A-Za-z0-9]{3}`)},
	{CatNVIDIASecret, "NVIDIA", regexp.MustCompile(`nvapi-[A-Z0-9_-]{60,70}`)},
	{CatOctopusDeploySecret, "Octopus Deploy", regexp.MustCompile(`API-[A-Z0-9]{26}`)},
	{CatOneSignalSecret, "OneSignal", regexp.MustCompile(`os_v2_(?:app|org)_[a-z2-7]{103}`)},
	{CatOnfidoSecret, "Onfido", regexp.MustCompile(`api_live_ca\.[A-Za-z0-9_-]{20,80}`)},
	{CatOnfidoSecret, "Onfido", regexp.MustCompile(`api_live\.[A-Za-z0-9_-]{20,80}`)},
	{CatOnfidoSecret, "Onfido", regexp.MustCompile(`api_live_us\.[A-Za-z0-9_-]{20,80}`)},
	{CatOpenRouterSecret, "OpenRouter", regexp.MustCompile(`sk-or-v1-[0-9a-f]{64}`)},
	{CatOpenShiftSecret, "OpenShift", regexp.MustCompile(`sha256~[\w-]{43}`)},
	{CatPaddleSecret, "Paddle", regexp.MustCompile(`pdl_live_apikey_[a-z0-9]{26}_[A-Za-z0-9]{22}_[A-Za-z0-9]{3}`)},
	{CatPerplexitySecret, "Perplexity", regexp.MustCompile(`pplx-[a-zA-Z0-9]{48}`)},
	{CatPersonaSecret, "Persona", regexp.MustCompile(`persona_production_[a-z0-9_-]{20,80}`)},
	{CatPineconeSecret, "Pinecone", regexp.MustCompile(`pcsk_[A-Za-z0-9]{5,6}_[A-Za-z0-9]{63}`)},
	{CatPinterestSecret, "Pinterest", regexp.MustCompile(`pina_[A-Za-z0-9_-]{20,200}`)},
	// The body admits a full stop, so the span took the one that ended the
	// sentence around it: "pscale_pw_...Dc6." came out one character long. The
	// (?i) went with it as noise -- \w is already both cases. Fly.io above and
	// Salesforce below are the same shape and the same fix: a permissive body,
	// then one final character that excludes what a sentence ends on.
	{CatPlanetscaleSecret, "PlanetScale", regexp.MustCompile(`pscale_tkn_[\w=.-]{31,63}[\w=-]`)},
	{CatPlanetscaleSecret, "PlanetScale", regexp.MustCompile(`pscale_oauth_[\w=.-]{31,63}[\w=-]`)},
	{CatPlanetscaleSecret, "PlanetScale", regexp.MustCompile(`pscale_pw_[\w=.-]{31,63}[\w=-]`)},
	{CatPolarSecret, "Polar", regexp.MustCompile(`polar_at_[A-Za-z0-9_-]{20,100}`)},
	{CatPolarSecret, "Polar", regexp.MustCompile(`polar_oat_[A-Za-z0-9_-]{20,100}`)},
	{CatPolarSecret, "Polar", regexp.MustCompile(`polar_pat_[A-Za-z0-9_-]{20,100}`)},
	{CatPosthogSecret, "PostHog", regexp.MustCompile(`phx_[a-zA-Z0-9_\-]{41,49}`)},
	{CatPosthogSecret, "PostHog", regexp.MustCompile(`phc_[a-zA-Z0-9_\-]{41,44}`)},
	{CatPostmanSecret, "Postman", regexp.MustCompile(`PMAK-(?i)[a-f0-9]{24}\-[a-f0-9]{34}`)},
	{CatPrefectSecret, "Prefect", regexp.MustCompile(`pnu_[a-zA-Z0-9]{36}`)},
	{CatProofSecret, "Proof", regexp.MustCompile(`prf_(?:cli_)?[A-Za-z0-9_-]{20,80}`)},
	{CatPulumiSecret, "Pulumi", regexp.MustCompile(`pul-[a-f0-9]{40}`)},
	{CatRampSecret, "Ramp", regexp.MustCompile(`ramp_sec_[A-Za-z0-9]{48}`)},
	{CatReadmeSecret, "ReadMe", regexp.MustCompile(`rdme_[a-z0-9]{70}`)},
	{CatRedirectPizzaSecret, "redirect.pizza", regexp.MustCompile(`rpa_[A-Za-z0-9]{30}`)},
	{CatRenderSecret, "Render", regexp.MustCompile(`rnd_[A-Za-z0-9]{28}`)},
	{CatRootlySecret, "Rootly", regexp.MustCompile(`rootly_[a-f0-9]{64}`)},
	{CatRubygemsSecret, "RubyGems", regexp.MustCompile(`rubygems_[a-f0-9]{48}`)},
	{CatRunpodSecret, "RunPod", regexp.MustCompile(`rpa_[A-Z0-9]{40}[A-Za-z0-9]{6}`)},
	{CatSalesforceSecret, "Salesforce", regexp.MustCompile(`00[A-Za-z0-9]{13}![A-Za-z0-9._-]{79,259}[A-Za-z0-9_-]`)},
	{CatSamsaraSecret, "Samsara", regexp.MustCompile(`samsara_api_[A-Za-z0-9]{26,32}`)},
	{CatScalingoSecret, "Scalingo", regexp.MustCompile(`tk-us-[\w-]{48}`)},
	{CatSegmentSecret, "Segment", regexp.MustCompile(`sgp_[A-Za-z0-9]{64}`)},
	{CatBrevoSecret, "Brevo", regexp.MustCompile(`xkeysib-[a-fA-F0-9]{64}-[a-zA-Z0-9]{16}`)},
	{CatSentrySecret, "Sentry", regexp.MustCompile(`sntrys_eyJpYXQiO[a-zA-Z0-9+/]{10,200}(?:LCJyZWdpb25fdXJs|InJlZ2lvbl91cmwi|cmVnaW9uX3VybCI6)[a-zA-Z0-9+/]{10,200}={0,2}_[a-zA-Z0-9+/]{43}`)},
	{CatSentrySecret, "Sentry", regexp.MustCompile(`sntryu_[a-f0-9]{64}`)},
	{CatSettlemintSecret, "SettleMint", regexp.MustCompile(`sm_aat_[a-zA-Z0-9]{16}`)},
	{CatSettlemintSecret, "SettleMint", regexp.MustCompile(`sm_pat_[a-zA-Z0-9]{16}`)},
	{CatSettlemintSecret, "SettleMint", regexp.MustCompile(`sm_sat_[a-zA-Z0-9]{16}`)},
	{CatShippoSecret, "Shippo", regexp.MustCompile(`shippo_(?:live|test)_[a-fA-F0-9]{40}`)},
	{CatShopifySecret, "Shopify", regexp.MustCompile(`shpat_[a-fA-F0-9]{32}`)},
	{CatShopifySecret, "Shopify", regexp.MustCompile(`shpca_[a-fA-F0-9]{32}`)},
	{CatShopifySecret, "Shopify", regexp.MustCompile(`shppa_[a-fA-F0-9]{32}`)},
	{CatShopifySecret, "Shopify", regexp.MustCompile(`shpss_[a-fA-F0-9]{32}`)},
	// Two shapes, two patterns. Both open on `sgp_`, and written as one alternation
	// Go finds no leading literal to scan for across the two branches.
	{CatSourcegraphSecret, "Sourcegraph", regexp.MustCompile(`sgp_(?:[a-fA-F0-9]{16}|local)_[a-fA-F0-9]{40}`)},
	{CatSourcegraphSecret, "Sourcegraph", regexp.MustCompile(`sgp_[a-fA-F0-9]{40}`)},
	// Two prefixes as two patterns rather than one alternation. RE2 scans for a
	// single leading literal, so it cannot use either branch of
	// `(?:EAAA|sq0atp-)`: measured over the corpus, the alternation costs 465us
	// and the two split patterns 6.8us and 6.6us. Every alternation of prefixes in
	// this tier is split for that reason.
	{CatSquareSecret, "Square", regexp.MustCompile(`EAAA[\w-]{22,60}`)},
	{CatSquareSecret, "Square", regexp.MustCompile(`sq0atp-[\w-]{22,60}`)},
	{CatSupabaseSecret, "Supabase", regexp.MustCompile(`sbp_[a-z0-9_-]{40}`)},
	{CatSupabaseSecret, "Supabase", regexp.MustCompile(`sb_secret_[A-Za-z0-9_-]{31}`)},
	{CatTailscaleSecret, "Tailscale", regexp.MustCompile(`tskey-api-[A-Za-z0-9_-]{20,36}`)},
	{CatTemporalSecret, "Temporal Cloud", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]*Y2NvdW50X2lk[A-Za-z0-9_-]*InRlbXBvcmFsLmlv[A-Za-z0-9_-]*(?:ICJrZXlfaWQiOi|a2V5X2lk|rZXlfaWQi)[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}`)},
	{CatThunderstoreSecret, "Thunderstore", regexp.MustCompile(`tss_[A-Za-z0-9_-]{20,80}`)},
	{CatTogetherAISecret, "Together AI", regexp.MustCompile(`tgp_v1_[A-Za-z0-9_-]{43}`)},
	{CatUnkeySecret, "Unkey", regexp.MustCompile(`unkey_[A-Za-z0-9]{20,32}`)},
	{CatUpcloudSecret, "UpCloud", regexp.MustCompile(`ucat_[0-9A-Za-z]{24,32}`)},
	{CatValTownSecret, "Val Town", regexp.MustCompile(`vtwn_[A-Za-z0-9_-]{20,80}`)},
	// The "hvs." and "hvb." shapes only. Vault also issues a legacy "s.<24 chars>"
	// token, deliberately absent: a bare "s." before twenty-four alphanumerics is
	// every second member access in a stack trace, and it defeated the prefix scan
	// as well — 443us against 1.4us for "hvs." alone.
	{CatVaultSecret, "HashiCorp Vault", regexp.MustCompile(`hvb\.[\w-]{138,300}`)},
	{CatVaultSecret, "HashiCorp Vault", regexp.MustCompile(`hvs\.[\w-]{90,120}`)},
	{CatVercelSecret, "Vercel", regexp.MustCompile(`vck_[A-Za-z0-9_-]{56}`)},
	{CatVercelSecret, "Vercel", regexp.MustCompile(`vca_[A-Za-z0-9_-]{56}`)},
	{CatVercelSecret, "Vercel", regexp.MustCompile(`vcr_[A-Za-z0-9_-]{56}`)},
	{CatVercelSecret, "Vercel", regexp.MustCompile(`vci_[A-Za-z0-9_-]{56}`)},
	{CatVercelSecret, "Vercel", regexp.MustCompile(`vcp_[A-Za-z0-9_-]{56}`)},
	{CatWakatimeSecret, "WakaTime", regexp.MustCompile(`waka_[a-z0-9]{36,64}`)},
	{CatWeightsAndBiasesSecret, "Weights & Biases", regexp.MustCompile(`wandb_v1_[A-Za-z0-9_]{77}`)},
	{CatWorkatoSecret, "Workato", regexp.MustCompile(`wrka(?:[a-z]{2})?-eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{64,}`)},
	{CatZuploSecret, "Zuplo", regexp.MustCompile(`zpka_[a-z0-9]{32}_[0-9a-f]{8}`)},
}

// SecretPatterns returns the credential set. It is locale-independent: a
// deployment that scans no country's identifiers still must not paste its keys
// into a model.
func SecretPatterns() []Pattern {
	out := []Pattern{
		// vendor prefixes, most specific first
		{Regex: openAIModernRe, Category: CatOpenAIKey, Label: "OpenAI API key"},
		{Regex: anthropicRe, Category: CatAnthropicKey, Label: "Anthropic API key"},
		{Regex: openAILegacyRe, Category: CatOpenAIKey, Label: "OpenAI API key (legacy)"},
		{Regex: googleRe, Category: CatGoogleKey, Label: "Google API key"},
		{Regex: awsAccessKeyRe, Category: CatAWSAccessKey, Label: "AWS access key id"},
		{Regex: awsSecretKeyRe, Group: 1, Category: CatAWSSecretKey, Label: "AWS secret access key"},
		{Regex: githubRe, Category: CatGitHubToken, Label: "GitHub token"},
		{Regex: githubFineGrainedRe, Category: CatGitHubToken, Label: "GitHub fine-grained token"},
		{Regex: gitlabRe, Category: CatGitLabToken, Label: "GitLab personal access token"},
		{Regex: slackRe, Category: CatSlackToken, Label: "Slack bot or user token"},
		{Regex: slackAppRe, Category: CatSlackToken, Label: "Slack app-level token"},
		{Regex: slackWebhookRe, Category: CatSlackToken, Label: "Slack webhook URL"},
		{Regex: stripeRe, Category: CatStripeKey, Label: "Stripe API key"},
		{Regex: sendGridRe, Category: CatSendGridKey, Label: "SendGrid API key"},
		{Regex: twilioRe, Category: CatTwilioKey, Label: "Twilio API key"},
		{Regex: npmRe, Category: CatNPMToken, Label: "npm access token"},
		{Regex: pypiRe, Category: CatPyPIToken, Label: "PyPI API token"},
		{Regex: dockerRe, Category: CatDockerToken, Label: "Docker personal access token"},

		// Second tier by origin, first tier by shape: `4b1d` is four hex characters,
		// so it occurs inside any long hash. `sha512-4b1d0123…` in a lockfile had
		// forty-two characters cut out of the middle of it and masked as a ClickHouse
		// key, leaving the rest of the hash in clear — a pasted package-lock.json came
		// back with fragments of its integrity fields replaced. A ClickHouse key is
		// exactly forty-two characters, so the length is the whole of the evidence and
		// both ends have to be bounded. RE2 has no lookaround, so each boundary is
		// consumed and Group points at the value; the leading literal scan is what it
		// costs, and it is the reason this one rule sits outside vendorPrefixes.
		{Regex: clickhouseRe, Group: 1, Category: CatClickhouseSecret, Label: "ClickHouse credential"},
		{Regex: hfRe, Category: CatHFToken, Label: "Hugging Face token"},
		{Regex: replicateRe, Category: CatReplicateToken, Label: "Replicate API token"},
		{Regex: groqRe, Category: CatGroqKey, Label: "Groq API key"},
		{Regex: xaiRe, Category: CatXAIKey, Label: "xAI API key"},

		// structural
		{Regex: pemBlockRe, Category: CatPEMKey, Label: "PEM private key, whole block"},
		{Regex: pemRe, Category: CatPEMKey, Label: "PEM private key header"},
		{Regex: jwtRe, Category: CatJWT, Label: "JSON Web Token"},
		{Regex: connStrRe, Group: 1, Category: CatConnStr, Label: "URL carrying credentials"},
	}

	// The second tier goes here — after the shapes reasoned about one by one,
	// and before the generics — for the reason the tiering exists: a
	// "TOKEN=..." hint must not claim a span a documented prefix can name.
	for _, v := range vendorPrefixes {
		// Two patterns of one category carry different labels, which is what
		// makes a report say which of a vendor's shapes actually fired — so the
		// label carries the prefix the pattern opens on, `bkaa_` against `bkua_`,
		// and not the vendor's name alone, under which all eight of Buildkite's
		// read the same.
		label := v.Vendor + " credential"
		if prefix, _ := v.Regex.LiteralPrefix(); prefix != "" {
			label += " (" + prefix + "…)"
		}
		out = append(out, Pattern{Regex: v.Regex, Category: v.Category, Label: label})
	}

	return append(out, []Pattern{
		// context-hinted generics, last
		{Regex: genericSecretQuotedRe, Group: 1, Category: CatGenericSecret, Label: "Named secret or password"},
		{Regex: genericSecretBareRe, Group: 1, Category: CatGenericSecret, Label: "Named secret or password", Verify: UnclosedBracketCheck},
		{Regex: xmlSecretRe, Group: 1, Category: CatGenericSecret, Label: "Named secret in an XML element"},
		{Regex: nugetPasswordRe, Group: 1, Category: CatGenericSecret, Label: "NuGet feed password"},
		{Regex: nugetPasswordReversedRe, Group: 1, Category: CatGenericSecret, Label: "NuGet feed password"},
		{Regex: authHeaderRe, Group: 1, Category: CatGenericSecret, Label: "Authorization header credential", Verify: AuthHeaderCheck},
		{Regex: authHeaderLowercaseRe, Group: 1, Category: CatGenericSecret, Label: "Authorization header credential", Verify: AuthHeaderCheck},
		{Regex: sessionSecretRe, Group: 1, Category: CatGenericSecret, Label: "Session token"},
		{Regex: hexSecretRe, Group: 1, Category: CatHexSecret, Label: "Hex-encoded key"},
	}...)
}
