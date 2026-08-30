package pii

import "regexp"

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
	// The trailing BLOCK is what a PGP armour header carries, and requiring the
	// line to end on "KEY-----" missed it: "-----BEGIN PGP PRIVATE KEY BLOCK-----"
	// is a private key by any reading and reached nothing.
	pemRe = regexp.MustCompile(`-----BEGIN\s[A-Z\s]*PRIVATE\sKEY(?:\sBLOCK)?-----`)

	// Three base64url segments, the first two starting with the "eyJ" that a
	// base64-encoded "{"" always produces. Without that anchor any dotted blob
	// of the right lengths matched — a checksum, a build id.
	jwtRe = regexp.MustCompile(`eyJ[a-zA-Z0-9_-]{10,}\.eyJ[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}`)

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
	// Two branches, and their order is the point. The first is the run minus its
	// tail, written as seven-or-more plus one closing character so the minimum
	// stays eight; Go's regexp is leftmost-first, so it wins whenever it can.
	// The second is the raw run, and it exists for the one case the first cannot
	// reach: a value of exactly eight characters whose last is punctuation
	// ("PASSWORD=hunter2)"). Trimming there would drop it to seven, fall under
	// the floor, and match nothing at all — the password would leave in clear. A
	// narrowing that turns a caught credential into a silent miss is worse than
	// the eaten bracket it set out to fix.
	genericSecretRe = regexp.MustCompile(`(?i)(?:PASSWORD|PASSWD|SECRET|TOKEN|API_KEY|APIKEY|ACCESS_KEY|ENCRYPTION_KEY|PRIVATE_KEY|AUTH_TOKEN)['"]?\s*[=:]\s*['"]?` +
		`([^` + quoteChars + `]{7,}[^` + quoteChars + noSentenceTail + `]|[^` + quoteChars + `]{8,})['"]?`)

	// Sixty-four or more hex characters behind a key-shaped name. The floor is
	// what separates an encryption key from a commit SHA somebody assigned to a
	// field: forty hex under "KEY=" is a truncated SHA, and masking it breaks a
	// paste for nothing.
	hexSecretRe = regexp.MustCompile(`(?i)(?:KEY|SECRET|ENCRYPTION_KEY|SIGNING_KEY|HMAC_KEY)['"]?\s*[=:]\s*['"]?([0-9a-f]{64,})['"]?`)
)

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

// SecretPatterns returns the credential set. It is locale-independent: a
// deployment that scans no country's identifiers still must not paste its keys
// into a model.
func SecretPatterns() []Pattern {
	return []Pattern{
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
		{Regex: stripeRe, Category: CatStripeKey, Label: "Stripe API key"},
		{Regex: sendGridRe, Category: CatSendGridKey, Label: "SendGrid API key"},
		{Regex: twilioRe, Category: CatTwilioKey, Label: "Twilio API key"},
		{Regex: npmRe, Category: CatNPMToken, Label: "npm access token"},
		{Regex: pypiRe, Category: CatPyPIToken, Label: "PyPI API token"},
		{Regex: dockerRe, Category: CatDockerToken, Label: "Docker personal access token"},
		{Regex: hfRe, Category: CatHFToken, Label: "Hugging Face token"},
		{Regex: replicateRe, Category: CatReplicateToken, Label: "Replicate API token"},
		{Regex: groqRe, Category: CatGroqKey, Label: "Groq API key"},
		{Regex: xaiRe, Category: CatXAIKey, Label: "xAI API key"},

		// structural
		{Regex: pemRe, Category: CatPEMKey, Label: "PEM private key block"},
		{Regex: jwtRe, Category: CatJWT, Label: "JSON Web Token"},
		{Regex: connStrRe, Group: 1, Category: CatConnStr, Label: "URL carrying credentials"},

		// context-hinted generics, last
		{Regex: genericSecretRe, Group: 1, Category: CatGenericSecret, Label: "Named secret or password"},
		{Regex: hexSecretRe, Group: 1, Category: CatHexSecret, Label: "Hex-encoded key"},
	}
}
