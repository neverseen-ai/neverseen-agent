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
	CatPEMKey         Category = "SECRET_PEM_KEY"
	CatJWT            Category = "SECRET_JWT"
	CatConnStr        Category = "SECRET_CONN_STR"
	CatGenericSecret  Category = "SECRET_GENERIC"
	CatHexSecret      Category = "SECRET_HEX_KEY"
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
}

// categoryRegistry is the whole catalogue, minus the regexes.
var categoryRegistry = map[Category]CategoryInfo{
	// --- locale-independent identifiers -------------------------------------
	CatEmail:      {Prefix: "EMAIL", Score: 95},
	CatCreditCard: {Prefix: "CARD", Score: 95, Verify: LuhnCheck},
	CatIBAN:       {Prefix: "IBAN", Score: 95, Verify: IBANCheck},
	CatIPAddr:     {Prefix: "IP", Score: 75},
	CatMongoID:    {Prefix: "MONGOID", Score: 85},
	CatDOB:        {Prefix: "DOB", Score: 75},

	// --- France -------------------------------------------------------------
	CatNIR:   {Prefix: "NIR", Score: 95, Verify: NIRCheck},
	CatSIREN: {Prefix: "SIREN", Score: 85, Verify: SIRENCheck},
	CatSIRET: {Prefix: "SIRET", Score: 90, Verify: SIRETCheck},

	// --- United Kingdom -----------------------------------------------------
	CatNHSNumber: {Prefix: "NHS", Score: 95, Verify: NHSNumberCheck},
	// No checksum, but the letter rules are strict enough to stand on their
	// own: six of the twenty-six letters are excluded from the first position,
	// seven from the second, and seven whole prefixes are unissued.
	CatNINO: {Prefix: "NINO", Score: 90, Verify: NINOCheck},

	// --- United States ------------------------------------------------------
	CatSSN:           {Prefix: "SSN", Score: 85, Verify: SSNCheck},
	CatEIN:           {Prefix: "EIN", Score: 75}, // no checksum; the dashed shape is the evidence
	CatRoutingNumber: {Prefix: "ABA", Score: 90, Verify: RoutingNumberCheck},

	// --- shapes several locales contribute to -------------------------------
	CatPhone:      {Prefix: "PHONE", Score: 90},
	CatAddress:    {Prefix: "ADDR", Score: 85},
	CatPostalCode: {Prefix: "POSTCODE", Score: 80}, // anchored on a commune, a state or an outward code
	CatLicPlate:   {Prefix: "PLATE", Score: 80},

	// Declared by the deployment rather than guessed, so it outscores every
	// pattern. Which span it actually takes is arbitrated separately — see the
	// detector's overlap resolution.
	CatCustom: {Prefix: "CUSTOM", Score: 100},

	// --- credentials --------------------------------------------------------
	CatOpenAIKey:      {Prefix: "OPENAI_KEY", Score: 98, Secret: true},
	CatAnthropicKey:   {Prefix: "ANTHROPIC_KEY", Score: 98, Secret: true},
	CatGoogleKey:      {Prefix: "GOOGLE_KEY", Score: 97, Secret: true},
	CatAWSAccessKey:   {Prefix: "AWS_AKEY", Score: 97, Secret: true},
	CatAWSSecretKey:   {Prefix: "AWS_SKEY", Score: 90, Secret: true},
	CatGitHubToken:    {Prefix: "GH_TOKEN", Score: 98, Secret: true},
	CatGitLabToken:    {Prefix: "GL_TOKEN", Score: 97, Secret: true},
	CatSlackToken:     {Prefix: "SLACK_TOKEN", Score: 95, Secret: true},
	CatStripeKey:      {Prefix: "STRIPE_KEY", Score: 97, Secret: true},
	CatSendGridKey:    {Prefix: "SG_KEY", Score: 96, Secret: true},
	CatTwilioKey:      {Prefix: "TWILIO_KEY", Score: 95, Secret: true},
	CatNPMToken:       {Prefix: "NPM_TOKEN", Score: 96, Secret: true},
	CatPyPIToken:      {Prefix: "PYPI_TOKEN", Score: 96, Secret: true},
	CatDockerToken:    {Prefix: "DOCKER_TOKEN", Score: 96, Secret: true},
	CatHFToken:        {Prefix: "HF_TOKEN", Score: 95, Secret: true},
	CatReplicateToken: {Prefix: "REPLICATE_TOKEN", Score: 95, Secret: true},
	CatPEMKey:         {Prefix: "PEM_KEY", Score: 99, Secret: true},
	CatJWT:            {Prefix: "JWT", Score: 92, Secret: true},
	CatConnStr:        {Prefix: "CONN_STR", Score: 92, Secret: true},
	CatGenericSecret:  {Prefix: "SECRET", Score: 80, Secret: true},
	// Above the generic secret: both patterns claim the same "KEY=value" span,
	// and 64 hex characters behind that hint are not a coincidence, so the
	// specific category is the one worth reporting.
	CatHexSecret: {Prefix: "HEX_KEY", Score: 85, Secret: true},
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

	for _, cat := range cats {
		switch info := registry[cat]; {
		case info.Prefix == "":
			return fmt.Errorf("category %q has no token prefix: its tokens would render as \"[_1]\"", cat)
		case info.Score <= 0 || info.Score > 100:
			return fmt.Errorf("category %q scores %d, outside 1-100: a score of zero is never reported", cat, info.Score)
		}
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
