// Package pii holds the catalogue: what counts as a sensitive value, how each
// kind is recognised, and what it is called once replaced.
//
// The catalogue is data. A category is one entry in the registry below, and that
// entry carries everything the engine needs to know about it that is not its
// regex: the token prefix, the confidence a match carries, the checksum the
// shape cannot express, and whether it is a credential. Agent Veil, which this
// project replaces, spread those across a constant, a prefix map, a switch arm
// and a predicate — four places to edit, each with a silent default when
// forgotten: a missing prefix rendered tokens as "[_1]", a missing switch arm
// scored the category 60. Here a category with no entry fails validateCatalogue
// at package initialisation, which is a startup failure rather than a value
// masked under a nameless token in production.
package pii

import (
	"fmt"
	"sort"
)

// Category names one kind of sensitive value. It is the string that appears in
// a token ("[EMAIL_1]"), in the corpus, and in the per-category counters the
// agent reports.
type Category string

// Locale-independent identifiers: these stay on whatever locale a deployment
// selects, because disabling a country must never disable email detection.
//
// The IBAN belongs here rather than to any European locale: one expression and
// one checksum cover every country that issues them, and a French deployment
// banking in Germany still needs the German account masked.
const (
	CatEmail      Category = "EMAIL"
	CatCreditCard Category = "CREDIT_CARD"
	CatIBAN       Category = "IBAN"
	CatIPAddr     Category = "IP_ADDRESS"
	CatIPv6       Category = "IPV6_ADDRESS"
	CatMongoID    Category = "MONGO_ID"
	CatDOB        Category = "DOB"
)

// Identifiers whose shape is national. Which of these load is decided by the
// locale registry (see locale.go).
const (
	// France
	CatNIR   Category = "NIR"
	CatSIREN Category = "SIREN"
	CatSIRET Category = "SIRET"

	// United Kingdom
	CatNINO      Category = "NINO"
	CatNHSNumber Category = "NHS_NUMBER"

	// United States. The social security number is here rather than in the
	// locale-independent set: it is a national identifier, and a deployment
	// scanning European data has no reason to carry a US range rule — the same
	// argument that keeps a day-first date in the locale that reads dates that
	// way.
	CatSSN           Category = "SSN"
	CatEIN           Category = "EIN"
	CatRoutingNumber Category = "ROUTING_NUMBER"

	// Shapes several locales contribute their own pattern for, under one
	// category. A postcode is a postcode whether it is five digits and a
	// commune, an alphanumeric outward code, or a state and a ZIP.
	CatPhone      Category = "PHONE"
	CatAddress    Category = "ADDRESS"
	CatPostalCode Category = "POSTAL_CODE"
	CatLicPlate   Category = "LICENSE_PLATE"
)

// CatCustom carries the values a deployment declares sensitive itself — a
// project code name, an internal customer id, anything no pattern can be
// expected to know about. It is the one category with no regex in the
// catalogue: its patterns are built from configuration.
const CatCustom Category = "CUSTOM"

// Credentials. Every one of these is a category like any other — tokenized,
// numbered, and restored on the way out.
const (
	CatOpenAIKey      Category = "SECRET_OPENAI_KEY"
	CatAnthropicKey   Category = "SECRET_ANTHROPIC_KEY"
	CatGoogleKey      Category = "SECRET_GOOGLE_KEY"
	CatAWSAccessKey   Category = "SECRET_AWS_ACCESS_KEY"
	CatAWSSecretKey   Category = "SECRET_AWS_SECRET_KEY"
	CatGitHubToken    Category = "SECRET_GITHUB_TOKEN"
	CatGitLabToken    Category = "SECRET_GITLAB_TOKEN"
	CatSlackToken     Category = "SECRET_SLACK_TOKEN"
	CatStripeKey      Category = "SECRET_STRIPE_KEY"
	CatSendGridKey    Category = "SECRET_SENDGRID_KEY"
	CatTwilioKey      Category = "SECRET_TWILIO_KEY"
	CatNPMToken       Category = "SECRET_NPM_TOKEN"
	CatPyPIToken      Category = "SECRET_PYPI_TOKEN"
	CatDockerToken    Category = "SECRET_DOCKER_TOKEN"
	CatHFToken        Category = "SECRET_HF_TOKEN"
	CatReplicateToken Category = "SECRET_REPLICATE_TOKEN"
	CatGroqKey        Category = "SECRET_GROQ_KEY"
	CatXAIKey         Category = "SECRET_XAI_KEY"
	CatPEMKey         Category = "SECRET_PEM_KEY"
	CatJWT            Category = "SECRET_JWT"
	CatConnStr        Category = "SECRET_CONN_STR"
	CatGenericSecret  Category = "SECRET_GENERIC"
	CatHexSecret      Category = "SECRET_HEX_KEY"

	// The second tier of vendor prefixes. One category per vendor, whatever the
	// role of the credential — a vendor issues personal tokens, service keys and
	// refresh tokens against one account, and a person revoking one revokes them
	// together. Several patterns share a category where a vendor issues several
	// shapes, as Slack's already do.
	CatOnePasswordSecret      Category = "SECRET_1PASSWORD"
	CatAdobeSecret            Category = "SECRET_ADOBE"
	CatAgeSecret              Category = "SECRET_AGE"
	CatAikidoSecret           Category = "SECRET_AIKIDO"
	CatAirtableSecret         Category = "SECRET_AIRTABLE"
	CatAlibabaSecret          Category = "SECRET_ALIBABA"
	CatApifySecret            Category = "SECRET_APIFY"
	CatArtifactorySecret      Category = "SECRET_ARTIFACTORY"
	CatAsaasSecret            Category = "SECRET_ASAAS"
	CatAuthressSecret         Category = "SECRET_AUTHRESS"
	CatAzureAppConfigSecret   Category = "SECRET_AZURE_APP_CONFIGURATION"
	CatAzureServiceBusSecret  Category = "SECRET_AZURE_SERVICEBUS"
	CatBraveSearchSecret      Category = "SECRET_BRAVE_SEARCH"
	CatBuildkiteSecret        Category = "SECRET_BUILDKITE"
	CatCanvaSecret            Category = "SECRET_CANVA"
	CatCerebrasSecret         Category = "SECRET_CEREBRAS"
	CatCircleciSecret         Category = "SECRET_CIRCLECI"
	CatClickhouseSecret       Category = "SECRET_CLICKHOUSE"
	CatClojarsSecret          Category = "SECRET_CLOJARS"
	CatCloudflareSecret       Category = "SECRET_CLOUDFLARE"
	CatCloudsmithSecret       Category = "SECRET_CLOUDSMITH"
	CatCockroachDBSecret      Category = "SECRET_COCKROACHLABS"
	CatConfigcatSecret        Category = "SECRET_CONFIGCAT"
	CatDatabricksSecret       Category = "SECRET_DATABRICKS"
	CatDataStaxAstraSecret    Category = "SECRET_DATASTAX_ASTRA"
	CatDenoSecret             Category = "SECRET_DENO"
	CatDevcycleSecret         Category = "SECRET_DEVCYCLE"
	CatDevinSecret            Category = "SECRET_DEVIN"
	CatDigitaloceanSecret     Category = "SECRET_DIGITALOCEAN"
	CatDopplerSecret          Category = "SECRET_DOPPLER"
	CatDuffelSecret           Category = "SECRET_DUFFEL"
	CatDynatraceSecret        Category = "SECRET_DYNATRACE"
	CatEasypostSecret         Category = "SECRET_EASYPOST"
	CatElasticSecret          Category = "SECRET_ELASTIC"
	CatExoscaleSecret         Category = "SECRET_EXOSCALE"
	CatFacebookSecret         Category = "SECRET_FACEBOOK"
	CatFigmaSecret            Category = "SECRET_FIGMA"
	CatFlutterwaveSecret      Category = "SECRET_FLUTTERWAVE"
	CatFlyIOSecret            Category = "SECRET_FLYIO"
	CatFrameIOSecret          Category = "SECRET_FRAMEIO"
	CatGCNotifySecret         Category = "SECRET_GC_NOTIFY"
	CatGoogleGeminiSecret     Category = "SECRET_GOOGLE_GEMINI"
	CatGrafanaSecret          Category = "SECRET_GRAFANA"
	CatHarnessSecret          Category = "SECRET_HARNESS"
	CatHerokuSecret           Category = "SECRET_HEROKU"
	CatInfracostSecret        Category = "SECRET_INFRACOST"
	CatIntra42Secret          Category = "SECRET_INTRA42"
	CatIonicSecret            Category = "SECRET_IONIC"
	CatLangSmithSecret        Category = "SECRET_LANGSMITH"
	CatLichessSecret          Category = "SECRET_LICHESS"
	CatLinearSecret           Category = "SECRET_LINEAR"
	CatMailersendSecret       Category = "SECRET_MAILERSEND"
	CatMercurySecret          Category = "SECRET_MERCURY"
	CatMergifySecret          Category = "SECRET_MERGIFY"
	CatMicrosoftTeamsSecret   Category = "SECRET_MICROSOFT_TEAMS"
	CatMinimaxSecret          Category = "SECRET_MINIMAX"
	CatMongoDBAtlasSecret     Category = "SECRET_MONGODB_ATLAS"
	CatNeonSecret             Category = "SECRET_NEON"
	CatNotionSecret           Category = "SECRET_NOTION"
	CatNVIDIASecret           Category = "SECRET_NVIDIA"
	CatOctopusDeploySecret    Category = "SECRET_OCTOPUS_DEPLOY"
	CatOneSignalSecret        Category = "SECRET_ONESIGNAL"
	CatOnfidoSecret           Category = "SECRET_ONFIDO"
	CatOpenRouterSecret       Category = "SECRET_OPENROUTER"
	CatOpenShiftSecret        Category = "SECRET_OPENSHIFT"
	CatPaddleSecret           Category = "SECRET_PADDLE"
	CatPerplexitySecret       Category = "SECRET_PERPLEXITY"
	CatPersonaSecret          Category = "SECRET_PERSONA"
	CatPineconeSecret         Category = "SECRET_PINECONE"
	CatPinterestSecret        Category = "SECRET_PINTEREST"
	CatPlanetscaleSecret      Category = "SECRET_PLANETSCALE"
	CatPolarSecret            Category = "SECRET_POLAR"
	CatPosthogSecret          Category = "SECRET_POSTHOG"
	CatPostmanSecret          Category = "SECRET_POSTMAN"
	CatPrefectSecret          Category = "SECRET_PREFECT"
	CatProofSecret            Category = "SECRET_PROOF"
	CatPulumiSecret           Category = "SECRET_PULUMI"
	CatRampSecret             Category = "SECRET_RAMP"
	CatReadmeSecret           Category = "SECRET_README"
	CatRedirectPizzaSecret    Category = "SECRET_REDIRECT_PIZZA"
	CatRenderSecret           Category = "SECRET_RENDER"
	CatRootlySecret           Category = "SECRET_ROOTLY"
	CatRubygemsSecret         Category = "SECRET_RUBYGEMS"
	CatRunpodSecret           Category = "SECRET_RUNPOD"
	CatSalesforceSecret       Category = "SECRET_SALESFORCE"
	CatSamsaraSecret          Category = "SECRET_SAMSARA"
	CatScalingoSecret         Category = "SECRET_SCALINGO"
	CatSegmentSecret          Category = "SECRET_SEGMENT"
	CatBrevoSecret            Category = "SECRET_SENDINBLUE"
	CatSentrySecret           Category = "SECRET_SENTRY"
	CatSettlemintSecret       Category = "SECRET_SETTLEMINT"
	CatShippoSecret           Category = "SECRET_SHIPPO"
	CatShopifySecret          Category = "SECRET_SHOPIFY"
	CatSourcegraphSecret      Category = "SECRET_SOURCEGRAPH"
	CatSquareSecret           Category = "SECRET_SQUARE"
	CatSupabaseSecret         Category = "SECRET_SUPABASE"
	CatTailscaleSecret        Category = "SECRET_TAILSCALE"
	CatTemporalSecret         Category = "SECRET_TEMPORAL"
	CatThunderstoreSecret     Category = "SECRET_THUNDERSTORE"
	CatTogetherAISecret       Category = "SECRET_TOGETHERAI"
	CatUnkeySecret            Category = "SECRET_UNKEY"
	CatUpcloudSecret          Category = "SECRET_UPCLOUD"
	CatValTownSecret          Category = "SECRET_VAL_TOWN"
	CatVaultSecret            Category = "SECRET_VAULT"
	CatVercelSecret           Category = "SECRET_VERCEL"
	CatWakatimeSecret         Category = "SECRET_WAKATIME"
	CatWeightsAndBiasesSecret Category = "SECRET_WEIGHTS_AND_BIASES"
	CatWorkatoSecret          Category = "SECRET_WORKATO"
	CatZuploSecret            Category = "SECRET_ZUPLO"
)

// CategoryInfo is everything the engine knows about a category besides its
// regex.
type CategoryInfo struct {
	// Prefix names the category inside a token: "EMAIL" gives "[EMAIL_1]".
	Prefix string

	// Score is the confidence a match carries, 1-100. Where Verify is set it is
	// the score of a value that passes it.
	//
	// The scale orders candidates competing for the same span, and gates them
	// against the engine's reporting threshold. It is not a probability, and no
	// two categories need their scores to be comparable in any other sense.
	Score int

	// Verify is the rule the regex cannot express — a checksum, almost always.
	// Nil when the shape stands on its own.
	//
	// A value that fails it is not a low-confidence match, it is not a match at
	// all: fifteen digits failing the NIR key are not a NIR, and no sensitivity
	// setting should turn them into one. The engine drops it outright rather
	// than scoring it down, which is where Agent Veil left an opening — at its
	// most aggressive setting a failed checksum still scored 30 and was
	// reported.
	Verify func(string) bool

	// Secret marks a credential. Credentials outrank the confidence scale in
	// overlap resolution: several ordinary categories score above a connection
	// string, and without the rank "postgres://admin:pw@db" resolved to an
	// EMAIL match — over the password, not the host.
	Secret bool

	// Group is the family this category is listed under by anything that has to
	// put the catalogue in front of a person. Forty switches is not a list
	// anybody reads; see group.go for why the grouping is deliberately not the
	// same fact as Secret.
	Group Group

	// Label is the category's name in words, for a menu or a report. It is not
	// Pattern.Label, which describes one *shape* — Slack has a bot token and an
	// app token, and those need different labels under one category. This is the
	// one name the category itself carries.
	Label string

	// NoisyInCode marks a category whose shape is satisfied by things source code
	// is made of, so a value found inside code is not reported.
	//
	// Per category rather than a global sensitivity, because the answer differs by
	// category and one knob would have to be wrong for most of them. What is *not*
	// marked matters as much as what is:
	//
	//   - **No credential is ever marked.** A key in a `.env` a coding agent has
	//     just read is the most valuable thing this agent will see all day, and a
	//     rule that relaxed secrets in code would fire exactly there.
	//   - **EMAIL is not marked.** An address in a fixture is still somebody's
	//     address, and measuring third-party packages found real maintainers'
	//     addresses in their metadata.
	//   - **IP_ADDRESS is not marked**, which is a decision against the obvious: it
	//     is the largest category in every code measurement here. IPAddressCheck
	//     already declines the ranges that name nobody, and what it deliberately
	//     keeps is the private ranges, because an internal host in a pasted
	//     configuration is a topology — which is what must not reach a model. A
	//     pasted configuration *is* code, so marking this would undo that decision
	//     in the one place it was made for.
	NoisyInCode bool
}

// categoryRegistry is the whole catalogue, minus the regexes.
var categoryRegistry = map[Category]CategoryInfo{
	// --- locale-independent identifiers -------------------------------------
	CatEmail:      {Prefix: "EMAIL", Score: 95, Verify: DocumentationEmailCheck, Group: GroupPersonal, Label: "Email address"},
	CatCreditCard: {Prefix: "CARD", Score: 95, Verify: LuhnCheck, Group: GroupBanking, Label: "Payment card"},
	CatIBAN:       {Prefix: "IBAN", Score: 95, Verify: IBANCheck, Group: GroupBanking, Label: "Bank account (IBAN)"},
	// Two categories rather than one, and the split is what makes fake mode
	// honest: a stand-in is chosen by category, so a single "IP address" entry
	// would hand an IPv6 address an IPv4 stand-in — a value in the wrong notation,
	// which is the machine artefact fake mode exists to avoid. It also stops the
	// switch lying: labelled "IP address" while only half of them were read, it
	// told an operator their addresses were masked.
	//
	// Verify parses the value, which is the rule neither expression can state. A
	// regex can describe the shape of an address and not whether it is one.
	CatIPAddr: {Prefix: "IP", Score: 75, Verify: IPAddressCheck, Group: GroupTechnical,
		Label: "IPv4 address"},
	CatIPv6: {Prefix: "IPV6", Score: 75, Verify: IPAddressCheck, Group: GroupTechnical,
		Label: "IPv6 address"},
	CatMongoID: {Prefix: "MONGOID", Score: 85, Group: GroupTechnical, Label: "Database identifier"},
	// Marked noisy in code: a date in source is a changelog entry, a copyright
	// year or a fixture. Sixteen of the hundred and twenty findings left in
	// third-party TypeScript were dates of this kind, and DOBCheck cannot help —
	// a release date last March is as far in the past as a birth date.
	//
	// The prefix is DATE and not DOB, while the category code stays DOB: the
	// token is read by a model, and "[DATE_1]" says what the value was where
	// "[DOB_1]" is an acronym it has to guess at.
	CatDOB: {Prefix: "DATE", Score: 75, Verify: DOBCheck, Group: GroupPersonal,
		Label: "Date of birth", NoisyInCode: true},

	// --- France -------------------------------------------------------------
	CatNIR:   {Prefix: "NIR", Score: 95, Verify: NIRCheck, Group: GroupPersonal, Label: "Social security number (fr)"},
	CatSIREN: {Prefix: "SIREN", Score: 85, Verify: SIRENCheck, Group: GroupCompany, Label: "SIREN (fr)"},
	CatSIRET: {Prefix: "SIRET", Score: 90, Verify: SIRETCheck, Group: GroupCompany, Label: "SIRET (fr)"},

	// --- United Kingdom -----------------------------------------------------
	CatNHSNumber: {Prefix: "NHS", Score: 95, Verify: NHSNumberCheck, Group: GroupPersonal, Label: "NHS number (gb)"},
	// No checksum, but the letter rules are strict enough to stand on their
	// own: six of the twenty-six letters are excluded from the first position,
	// seven from the second, and seven whole prefixes are unissued.
	CatNINO: {Prefix: "NINO", Score: 90, Verify: NINOCheck, Group: GroupPersonal, Label: "National Insurance number (gb)"},

	// --- United States ------------------------------------------------------
	CatSSN:           {Prefix: "SSN", Score: 85, Verify: SSNCheck, Group: GroupPersonal, Label: "Social security number (us)"},
	CatEIN:           {Prefix: "EIN", Score: 75, Group: GroupCompany, Label: "Employer identification number (us)"}, // no checksum; the dashed shape is the evidence
	CatRoutingNumber: {Prefix: "ABA", Score: 90, Verify: RoutingNumberCheck, Group: GroupBanking, Label: "Bank routing number (us)"},

	// --- shapes several locales contribute to -------------------------------
	// Marked noisy in code: a byte array is a telephone number by shape. "01 02 03
	// 04 05" in a buffer example is exactly the French notation, and eighteen of
	// the hundred and twenty were runs like it. In prose the same digits really
	// are a number somebody gave out, which is why this is a placement question
	// rather than a narrower pattern.
	CatPhone: {Prefix: "PHONE", Score: 90, Group: GroupPersonal, Label: "Telephone",
		NoisyInCode: true},
	CatAddress: {Prefix: "ADDR", Score: 85, Group: GroupPersonal, Label: "Postal address"},
	// Marked noisy in code for the reason the telephone is: the shape is a short
	// run of digits and letters anchored on a capitalised word, and an identifier
	// followed by a constant satisfies it.
	CatPostalCode: {Prefix: "POSTCODE", Score: 80, Verify: PostcodeCheck, Group: GroupPersonal,
		Label: "Postcode", NoisyInCode: true}, // anchored on a commune, a state or an outward code
	CatLicPlate: {Prefix: "PLATE", Score: 80, Group: GroupPersonal, Label: "Vehicle registration"},

	// Declared by the deployment rather than guessed, so it outscores every
	// pattern. Which span it actually takes is arbitrated separately — see the
	// detector's overlap resolution.
	CatCustom: {Prefix: "CUSTOM", Score: 100, Group: GroupDeclared, Label: "Declared value"},

	// --- credentials --------------------------------------------------------
	CatOpenAIKey:      {Prefix: "OPENAI_KEY", Score: 98, Secret: true, Group: GroupSecrets, Label: "OpenAI key"},
	CatAnthropicKey:   {Prefix: "ANTHROPIC_KEY", Score: 98, Secret: true, Group: GroupSecrets, Label: "Anthropic key"},
	CatGoogleKey:      {Prefix: "GOOGLE_KEY", Score: 97, Secret: true, Group: GroupSecrets, Label: "Google key"},
	CatAWSAccessKey:   {Prefix: "AWS_AKEY", Score: 97, Secret: true, Group: GroupSecrets, Label: "AWS access key"},
	CatAWSSecretKey:   {Prefix: "AWS_SKEY", Score: 90, Secret: true, Group: GroupSecrets, Label: "AWS secret key"},
	CatGitHubToken:    {Prefix: "GH_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "GitHub token"},
	CatGitLabToken:    {Prefix: "GL_TOKEN", Score: 97, Secret: true, Group: GroupSecrets, Label: "GitLab token"},
	CatSlackToken:     {Prefix: "SLACK_TOKEN", Score: 95, Secret: true, Group: GroupSecrets, Label: "Slack token"},
	CatStripeKey:      {Prefix: "STRIPE_KEY", Score: 97, Secret: true, Group: GroupSecrets, Label: "Stripe key"},
	CatSendGridKey:    {Prefix: "SG_KEY", Score: 96, Secret: true, Group: GroupSecrets, Label: "SendGrid key"},
	CatTwilioKey:      {Prefix: "TWILIO_KEY", Score: 95, Secret: true, Group: GroupSecrets, Label: "Twilio key"},
	CatNPMToken:       {Prefix: "NPM_TOKEN", Score: 96, Secret: true, Group: GroupSecrets, Label: "npm token"},
	CatPyPIToken:      {Prefix: "PYPI_TOKEN", Score: 96, Secret: true, Group: GroupSecrets, Label: "PyPI token"},
	CatDockerToken:    {Prefix: "DOCKER_TOKEN", Score: 96, Secret: true, Group: GroupSecrets, Label: "Docker token"},
	CatHFToken:        {Prefix: "HF_TOKEN", Score: 95, Secret: true, Group: GroupSecrets, Label: "Hugging Face token"},
	CatReplicateToken: {Prefix: "REPLICATE_TOKEN", Score: 95, Secret: true, Group: GroupSecrets, Label: "Replicate token"},
	CatGroqKey:        {Prefix: "GROQ_KEY", Score: 95, Secret: true, Group: GroupSecrets, Label: "Groq API key"},
	CatXAIKey:         {Prefix: "XAI_KEY", Score: 95, Secret: true, Group: GroupSecrets, Label: "xAI API key"},
	CatPEMKey:         {Prefix: "PEM_KEY", Score: 99, Secret: true, Group: GroupSecrets, Label: "Private key"},
	CatJWT:            {Prefix: "JWT", Score: 92, Secret: true, Group: GroupSecrets, Label: "JSON web token"},
	CatConnStr:        {Prefix: "CONN_STR", Score: 92, Secret: true, Group: GroupConnection, Label: "Connection string"},
	CatGenericSecret:  {Prefix: "SECRET", Score: 80, Secret: true, Verify: GenericSecretCheck, Group: GroupSecrets, Label: "Secret in an assignment"},
	// Above the generic secret: both patterns claim the same "KEY=value" span,
	// and 64 hex characters behind that hint are not a coincidence, so the
	// specific category is the one worth reporting.
	CatHexSecret: {Prefix: "HEX_KEY", Score: 85, Secret: true, Group: GroupSecrets, Label: "Hexadecimal key"},

	// The second tier of vendor prefixes, in one block.
	//
	// 98 across the tier, one number rather than a spread: every one of these is
	// anchored on a prefix the vendor documents, so they all carry the same
	// evidence, and a number per vendor would be precision this catalogue has
	// never measured.
	//
	// 98 specifically, because that is what makes prefix containment come out
	// right, and the catalogue already had the case: "sk-ant-" contains "sk-", and
	// CatAnthropicKey and CatOpenAIKey are *both* 98. The tie is the mechanism —
	// overlap arbitration goes credential, then confidence, then the longer span,
	// so equal scores hand the decision to the span, and the longer prefix wins.
	// Scored below, the specific one loses: at 96, Cerebras's "csk-<48>" was taken
	// by openAILegacyRe ("sk-<20,>") from offset 1 and masked as an OpenAI key
	// with its leading "c" left in clear — a token bound to a fragment, which is
	// the failure Go's ASCII \b already caused once for an accented address.
	// "ops_eyJ" over "eyJ", and "mercury_production_" over "ion_", are the same
	// case and are settled the same way.
	//
	// Above SECRET_GENERIC (80) and SECRET_HEX_KEY (85), which is what makes a
	// documented prefix win the span a bare "TOKEN=..." hint also claims.
	CatOnePasswordSecret:      {Prefix: "ONEPASSWORD_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "1Password credential"},
	CatAdobeSecret:            {Prefix: "ADOBE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Adobe credential"},
	CatAgeSecret:              {Prefix: "AGE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "age credential"},
	CatAikidoSecret:           {Prefix: "AIKIDO_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Aikido credential"},
	CatAirtableSecret:         {Prefix: "AIRTABLE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Airtable credential"},
	CatAlibabaSecret:          {Prefix: "ALIBABA_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Alibaba Cloud credential"},
	CatApifySecret:            {Prefix: "APIFY_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Apify credential"},
	CatArtifactorySecret:      {Prefix: "ARTIFACTORY_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Artifactory credential"},
	CatAsaasSecret:            {Prefix: "ASAAS_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Asaas credential"},
	CatAuthressSecret:         {Prefix: "AUTHRESS_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Authress credential"},
	CatAzureAppConfigSecret:   {Prefix: "AZURE_APP_CONFIGURATION_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Azure App Configuration credential"},
	CatAzureServiceBusSecret:  {Prefix: "AZURE_SERVICEBUS_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Azure Service Bus credential"},
	CatBraveSearchSecret:      {Prefix: "BRAVE_SEARCH_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Brave Search credential"},
	CatBuildkiteSecret:        {Prefix: "BUILDKITE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Buildkite credential"},
	CatCanvaSecret:            {Prefix: "CANVA_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Canva credential"},
	CatCerebrasSecret:         {Prefix: "CEREBRAS_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Cerebras credential"},
	CatCircleciSecret:         {Prefix: "CIRCLECI_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "CircleCI credential"},
	CatClickhouseSecret:       {Prefix: "CLICKHOUSE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "ClickHouse credential"},
	CatClojarsSecret:          {Prefix: "CLOJARS_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Clojars credential"},
	CatCloudflareSecret:       {Prefix: "CLOUDFLARE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Cloudflare credential"},
	CatCloudsmithSecret:       {Prefix: "CLOUDSMITH_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Cloudsmith credential"},
	CatCockroachDBSecret:      {Prefix: "COCKROACHLABS_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "CockroachDB Cloud credential"},
	CatConfigcatSecret:        {Prefix: "CONFIGCAT_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "ConfigCat credential"},
	CatDatabricksSecret:       {Prefix: "DATABRICKS_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Databricks credential"},
	CatDataStaxAstraSecret:    {Prefix: "DATASTAX_ASTRA_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "DataStax Astra credential"},
	CatDenoSecret:             {Prefix: "DENO_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Deno Deploy credential"},
	CatDevcycleSecret:         {Prefix: "DEVCYCLE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "DevCycle credential"},
	CatDevinSecret:            {Prefix: "DEVIN_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Devin credential"},
	CatDigitaloceanSecret:     {Prefix: "DIGITALOCEAN_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "DigitalOcean credential"},
	CatDopplerSecret:          {Prefix: "DOPPLER_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Doppler credential"},
	CatDuffelSecret:           {Prefix: "DUFFEL_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Duffel credential"},
	CatDynatraceSecret:        {Prefix: "DYNATRACE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Dynatrace credential"},
	CatEasypostSecret:         {Prefix: "EASYPOST_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "EasyPost credential"},
	CatElasticSecret:          {Prefix: "ELASTIC_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Elastic Cloud credential"},
	CatExoscaleSecret:         {Prefix: "EXOSCALE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Exoscale credential"},
	CatFacebookSecret:         {Prefix: "FACEBOOK_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Facebook credential"},
	CatFigmaSecret:            {Prefix: "FIGMA_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Figma credential"},
	CatFlutterwaveSecret:      {Prefix: "FLUTTERWAVE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Flutterwave credential"},
	CatFlyIOSecret:            {Prefix: "FLYIO_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Fly.io credential"},
	CatFrameIOSecret:          {Prefix: "FRAMEIO_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Frame.io credential"},
	CatGCNotifySecret:         {Prefix: "GC_NOTIFY_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "GC Notify credential"},
	CatGoogleGeminiSecret:     {Prefix: "GOOGLE_GEMINI_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Google Gemini credential"},
	CatGrafanaSecret:          {Prefix: "GRAFANA_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Grafana credential"},
	CatHarnessSecret:          {Prefix: "HARNESS_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Harness credential"},
	CatHerokuSecret:           {Prefix: "HEROKU_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Heroku credential"},
	CatInfracostSecret:        {Prefix: "INFRACOST_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Infracost credential"},
	CatIntra42Secret:          {Prefix: "INTRA42_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "42 Intra credential"},
	CatIonicSecret:            {Prefix: "IONIC_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Ionic credential"},
	CatLangSmithSecret:        {Prefix: "LANGSMITH_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "LangSmith credential"},
	CatLichessSecret:          {Prefix: "LICHESS_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Lichess credential"},
	CatLinearSecret:           {Prefix: "LINEAR_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Linear credential"},
	CatMailersendSecret:       {Prefix: "MAILERSEND_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "MailerSend credential"},
	CatMercurySecret:          {Prefix: "MERCURY_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Mercury credential"},
	CatMergifySecret:          {Prefix: "MERGIFY_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Mergify credential"},
	CatMicrosoftTeamsSecret:   {Prefix: "MICROSOFT_TEAMS_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Microsoft Teams credential"},
	CatMinimaxSecret:          {Prefix: "MINIMAX_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "MiniMax credential"},
	CatMongoDBAtlasSecret:     {Prefix: "MONGODB_ATLAS_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "MongoDB Atlas credential"},
	CatNeonSecret:             {Prefix: "NEON_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Neon credential"},
	CatNotionSecret:           {Prefix: "NOTION_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Notion credential"},
	CatNVIDIASecret:           {Prefix: "NVIDIA_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "NVIDIA credential"},
	CatOctopusDeploySecret:    {Prefix: "OCTOPUS_DEPLOY_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Octopus Deploy credential"},
	CatOneSignalSecret:        {Prefix: "ONESIGNAL_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "OneSignal credential"},
	CatOnfidoSecret:           {Prefix: "ONFIDO_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Onfido credential"},
	CatOpenRouterSecret:       {Prefix: "OPENROUTER_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "OpenRouter credential"},
	CatOpenShiftSecret:        {Prefix: "OPENSHIFT_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "OpenShift credential"},
	CatPaddleSecret:           {Prefix: "PADDLE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Paddle credential"},
	CatPerplexitySecret:       {Prefix: "PERPLEXITY_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Perplexity credential"},
	CatPersonaSecret:          {Prefix: "PERSONA_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Persona credential"},
	CatPineconeSecret:         {Prefix: "PINECONE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Pinecone credential"},
	CatPinterestSecret:        {Prefix: "PINTEREST_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Pinterest credential"},
	CatPlanetscaleSecret:      {Prefix: "PLANETSCALE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "PlanetScale credential"},
	CatPolarSecret:            {Prefix: "POLAR_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Polar credential"},
	CatPosthogSecret:          {Prefix: "POSTHOG_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "PostHog credential"},
	CatPostmanSecret:          {Prefix: "POSTMAN_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Postman credential"},
	CatPrefectSecret:          {Prefix: "PREFECT_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Prefect credential"},
	CatProofSecret:            {Prefix: "PROOF_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Proof credential"},
	CatPulumiSecret:           {Prefix: "PULUMI_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Pulumi credential"},
	CatRampSecret:             {Prefix: "RAMP_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Ramp credential"},
	CatReadmeSecret:           {Prefix: "README_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "ReadMe credential"},
	CatRedirectPizzaSecret:    {Prefix: "REDIRECT_PIZZA_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "redirect.pizza credential"},
	CatRenderSecret:           {Prefix: "RENDER_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Render credential"},
	CatRootlySecret:           {Prefix: "ROOTLY_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Rootly credential"},
	CatRubygemsSecret:         {Prefix: "RUBYGEMS_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "RubyGems credential"},
	CatRunpodSecret:           {Prefix: "RUNPOD_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "RunPod credential"},
	CatSalesforceSecret:       {Prefix: "SALESFORCE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Salesforce credential"},
	CatSamsaraSecret:          {Prefix: "SAMSARA_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Samsara credential"},
	CatScalingoSecret:         {Prefix: "SCALINGO_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Scalingo credential"},
	CatSegmentSecret:          {Prefix: "SEGMENT_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Segment credential"},
	CatBrevoSecret:            {Prefix: "SENDINBLUE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Brevo credential"},
	CatSentrySecret:           {Prefix: "SENTRY_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Sentry credential"},
	CatSettlemintSecret:       {Prefix: "SETTLEMINT_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "SettleMint credential"},
	CatShippoSecret:           {Prefix: "SHIPPO_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Shippo credential"},
	CatShopifySecret:          {Prefix: "SHOPIFY_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Shopify credential"},
	CatSourcegraphSecret:      {Prefix: "SOURCEGRAPH_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Sourcegraph credential"},
	CatSquareSecret:           {Prefix: "SQUARE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Square credential"},
	CatSupabaseSecret:         {Prefix: "SUPABASE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Supabase credential"},
	CatTailscaleSecret:        {Prefix: "TAILSCALE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Tailscale credential"},
	CatTemporalSecret:         {Prefix: "TEMPORAL_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Temporal Cloud credential"},
	CatThunderstoreSecret:     {Prefix: "THUNDERSTORE_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Thunderstore credential"},
	CatTogetherAISecret:       {Prefix: "TOGETHERAI_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Together AI credential"},
	CatUnkeySecret:            {Prefix: "UNKEY_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Unkey credential"},
	CatUpcloudSecret:          {Prefix: "UPCLOUD_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "UpCloud credential"},
	CatValTownSecret:          {Prefix: "VAL_TOWN_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Val Town credential"},
	CatVaultSecret:            {Prefix: "VAULT_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "HashiCorp Vault credential"},
	CatVercelSecret:           {Prefix: "VERCEL_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Vercel credential"},
	CatWakatimeSecret:         {Prefix: "WAKATIME_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "WakaTime credential"},
	CatWeightsAndBiasesSecret: {Prefix: "WEIGHTS_AND_BIASES_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Weights & Biases credential"},
	CatWorkatoSecret:          {Prefix: "WORKATO_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Workato credential"},
	CatZuploSecret:            {Prefix: "ZUPLO_TOKEN", Score: 98, Secret: true, Group: GroupSecrets, Label: "Zuplo credential"},
}

func init() { validateCatalogue() }

// validateCatalogue checks the registry against the pattern sets at package
// initialisation: every category a pattern emits must be registered, and every
// entry must carry a prefix and a usable score.
//
// It panics, which is the point. Both failures it catches are silent at
// runtime — a value masked under "[_1]", or a category scored by a default
// nobody chose — and both are programming errors fixed in the same commit that
// introduces them.
func validateCatalogue() {
	if err := checkCatalogue(allPatternsUnvalidated(), categoryRegistry); err != nil {
		panic("pii: " + err.Error())
	}
}

// checkCatalogue is validateCatalogue's rule, separated so it can be tested on
// inputs that are meant to fail: a validator that accepts everything looks
// exactly like a catalogue with nothing wrong in it.
func checkCatalogue(patterns []Pattern, registry map[Category]CategoryInfo) error {
	for _, p := range patterns {
		if _, ok := registry[p.Category]; !ok {
			return fmt.Errorf("category %q is emitted by pattern %q but is not in the registry: "+
				"its tokens would have no name and its matches no score", p.Category, p.Label)
		}
	}

	cats := make([]Category, 0, len(registry))
	for cat := range registry {
		cats = append(cats, cat)
	}
	sort.Slice(cats, func(i, j int) bool { return cats[i] < cats[j] })

	// Labels are checked for collisions as prefixes are, and for the same reason
	// one level up: two categories sharing a label are two identical rows in the
	// menu that switches them, and somebody unticking one of them has no way to
	// know which. NIR and SSN are both "social security number" until the
	// catalogue says which country's.
	labels := make(map[string]Category, len(registry))

	for _, cat := range cats {
		info := registry[cat]
		switch {
		case info.Prefix == "":
			return fmt.Errorf("category %q has no token prefix: its tokens would render as \"[_1]\"", cat)
		case !IsToken("[" + info.Prefix + "_1]"):
			// The prefix has to make a token by the grammar tokenRe reads, or the
			// replacement it renders is not one: "1PASSWORD_TOKEN" opened on a digit,
			// so "[1PASSWORD_TOKEN_1]" was filed as a stand-in by the rehydrator —
			// whose contract says a stand-in cannot be a credential — and every
			// answer in that session left the token fast path.
			return fmt.Errorf("category %q has the prefix %q, which does not make a token: %q is not "+
				"one by tokenRe, so its replacements would be read back as stand-ins", cat, info.Prefix, "["+info.Prefix+"_1]")
		case info.Score <= 0 || info.Score > 100:
			return fmt.Errorf("category %q scores %d, outside 1-100: a score of zero is never reported", cat, info.Score)
		case info.Label == "":
			return fmt.Errorf("category %q has no label: every surface that lists it would have to "+
				"invent a name, and two of them would invent different ones", cat)
		case info.Group == "":
			return fmt.Errorf("category %q has no group: it would be absent from every surface that "+
				"lists the catalogue by family, which is the only way forty categories are listed at all", cat)
		}
		if _, ok := groupRegistry[info.Group]; !ok {
			return fmt.Errorf("category %q names group %q, which is not registered", cat, info.Group)
		}
		if other, clash := labels[info.Label]; clash {
			return fmt.Errorf("categories %q and %q share the label %q: a list of them would "+
				"show two identical rows", other, cat, info.Label)
		}
		labels[info.Label] = cat
	}
	return nil
}

// Info returns what the catalogue knows about a category. The second result is
// false for a category that is not registered, which validateCatalogue has
// already ruled out for anything a pattern emits.
func Info(cat Category) (CategoryInfo, bool) {
	info, ok := categoryRegistry[cat]
	return info, ok
}

// Prefix returns the token prefix of a category, or "" when it is unregistered.
func Prefix(cat Category) string {
	return categoryRegistry[cat].Prefix
}

// Label returns the category's name in words, or the code itself for a category
// that is not registered — which validateCatalogue has already ruled out.
func Label(cat Category) string {
	if info, ok := categoryRegistry[cat]; ok {
		return info.Label
	}
	return string(cat)
}

// IsSecret reports whether a category holds a credential.
func IsSecret(cat Category) bool {
	return categoryRegistry[cat].Secret
}

// Score returns the confidence a match of this category and value carries, and
// whether it is reportable at all. A value failing its category's checksum is
// not reportable at any sensitivity.
func Score(cat Category, value string) (int, bool) {
	info, ok := categoryRegistry[cat]
	if !ok {
		return 0, false
	}
	if info.Verify != nil && !info.Verify(value) {
		return 0, false
	}
	return info.Score, true
}

// Categories lists every registered category, sorted, for the tests and reports
// that enumerate the catalogue rather than hard-coding a list of it.
func Categories() []Category {
	out := make([]Category, 0, len(categoryRegistry))
	for cat := range categoryRegistry {
		out = append(out, cat)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// NoisyInCode reports whether a category should be left alone inside source code.
//
// Asked by the engine, which is the only thing that knows whether the text it is
// scanning is code. The catalogue states the property; it does not detect it.
func NoisyInCode(cat Category) bool { return categoryRegistry[cat].NoisyInCode }
