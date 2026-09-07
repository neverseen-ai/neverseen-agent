package detector

import (
	"fmt"
	"os"
	"strings"

	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
)

// The environment is read here and nowhere else.
//
// Every other package takes a Config it was handed. That is deliberate: the
// project this one replaces had two entrypoints, each reading its own subset of
// the environment, and they drifted — one applied a default role on the way
// back and the other did not, so the same request was masked in one mode and
// answered in clear in the other. One reader is what makes that impossible.
const (
	// EnvLocale selects which country pattern sets load: a code, several codes
	// separated by commas, or "none" for the locale-independent sets only.
	EnvLocale = "NEVERSEEN_PII_LOCALE"
	// EnvAllowList lists values never to mask, separated by commas.
	EnvAllowList = "NEVERSEEN_PII_ALLOWLIST"
	// EnvSubstitution selects what a masked value looks like: "token" or "fake".
	EnvSubstitution = "NEVERSEEN_PII_SUBSTITUTION"

	// EnvSecretLevel is how far down the strength scale a named secret is masked:
	// "weak", "medium" or "strong". The starting value only — PUT /policy moves it
	// while the agent runs, exactly as it does the substitution mode.
	EnvSecretLevel = "NEVERSEEN_SECRET_LEVEL"
)

// minConfidence is the score a match must reach to be reported.
//
// It sits just at the level of the weakest category worth reporting — nine bare
// digits read as a Vietnamese identity card — so every registered category is
// reportable and the checksums do the discriminating. A failed checksum is not a
// low score, it is not a match at all (see pii.Score).
//
// TODO: a fixed threshold, not a configurable sensitivity. A deployment wanting
// only high-confidence matches would need this exposed; it stays a constant
// until one asks, because a knob nothing sets is a knob nobody tested.
const minConfidence = 50

// Config is everything the detector needs to know about a deployment.
type Config struct {
	// Locales names the country pattern sets to load, by their code in the pii
	// registry. Empty means none: the locale-independent identifiers and the
	// credentials, which always load, are all that remain.
	Locales []string

	// AllowList holds values this deployment never wants masked — the
	// nine-digit fleet ids that read as a SIREN, the addresses of its own
	// offices. Compared ignoring case and spacing, so declaring an IBAN in its
	// compact form also covers the grouped spelling of the same account.
	AllowList map[string]bool

	// Substitution selects what a masked value looks like. The zero value is
	// SubstitutionToken, so a Config built by hand keeps the reversible
	// behaviour without having to say so.
	Substitution Substitution

	// SecretLevel is the weakest named secret this agent masks. The zero value is
	// pii.StrengthWeak, which masks everything the pattern finds — the behaviour
	// this agent had before the level existed, so a Config built by hand keeps it.
	SecretLevel pii.Strength
}

// DefaultConfig returns the configuration of a deployment that has said
// nothing: no country set, so only the locale-independent identifiers and the
// credentials are scanned.
//
// The default is deliberately not a country. A wrong locale is worse than none:
// scanning French data with the Vietnamese set on masks its invoice numbers and
// timestamps as identity cards, and an operator who never set the variable has
// not chosen that.
func DefaultConfig() Config { return Config{} }

// ParseLocales reads a locale selection: a comma-separated list of codes from
// the pii registry, or "none".
//
// The known codes come from the registry, and so does the error message, rather
// than from a copy of the list written here — a copy is what goes stale the
// first time a locale is added.
func ParseLocales(spec string) ([]string, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}

	var locales []string
	for _, field := range strings.Split(spec, ",") {
		switch code := strings.ToLower(strings.TrimSpace(field)); code {
		case "":
			// an empty element, from a trailing comma
		case "none":
			// the locale-independent sets only: nothing to add
		default:
			if _, ok := pii.LocaleByCode(code); !ok {
				return nil, fmt.Errorf("unknown PII locale %q (want %s, none, or a comma-separated list)",
					strings.TrimSpace(field), strings.Join(pii.LocaleCodes(), ", "))
			}
			locales = append(locales, code)
		}
	}
	return locales, nil
}

// FromEnv builds the detector this deployment runs.
func FromEnv() (*Detector, error) {
	cfg := DefaultConfig()

	locales, err := ParseLocales(os.Getenv(EnvLocale))
	if err != nil {
		return nil, err
	}
	cfg.Locales = locales
	cfg.AllowList = parseValueList(os.Getenv(EnvAllowList))

	if cfg.Substitution, err = ParseSubstitution(os.Getenv(EnvSubstitution)); err != nil {
		return nil, err
	}
	if cfg.SecretLevel, err = ParseSecretLevel(os.Getenv(EnvSecretLevel)); err != nil {
		return nil, err
	}

	return New(cfg), nil
}

// parseValueList reads a comma-separated list of literal values, returning nil
// for an empty spec so an unset variable stays indistinguishable from no list.
func parseValueList(spec string) map[string]bool {
	values := make(map[string]bool)
	for _, v := range strings.Split(spec, ",") {
		if v = strings.TrimSpace(v); v != "" {
			values[v] = true
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

// normalizeListValue folds a value to the form the allow list compares on:
// lower case, and every run of whitespace collapsed away entirely.
//
// Spacing is dropped rather than collapsed to a single space because the values
// this list holds are identifiers, and an identifier's grouping is decoration —
// "FR14 2004 1010" and "FR1420041010" are the same account, and an operator who
// declares one means both.
func normalizeListValue(v string) string {
	return strings.ToLower(strings.Join(strings.Fields(v), ""))
}
