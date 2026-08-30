package pii

import "unicode"

// Strength is how much a value looks like a credential rather than a word.
//
// It exists because "is this a secret" has no yes and no. A checksum has one — a
// NIR either keys or it does not — and that is why Verify drops a failure outright.
// The strength of a password is a judgement on a spectrum, so it is reported as one
// and the engine decides what to do with it. The catalogue does not know there is a
// policy; see internal/detector.
//
// Ordered so that a level compares: a value is masked when its strength is at or
// above the level the agent runs at.
type Strength int

const (
	// StrengthWeak is a value made of one kind of character — words, possibly
	// joined by separators. "correcthorse", "troisieme-valeur-longue". A real
	// passphrase looks like this, and so does a great deal of ordinary prose and
	// code, which is what makes this the level that over-masks.
	StrengthWeak Strength = iota

	// StrengthMedium mixes two kinds. "hunter2", "secret_value".
	StrengthMedium

	// StrengthStrong mixes three or more, or is long enough that nothing typed by
	// hand reaches it. "Sup3rS3cr3tValue123", a base64 blob, a 64-character hex
	// key. Almost nothing in source code looks like this.
	//
	// It is **not** the answer to over-masking source code, and this comment used
	// to say it was. Measured over four megabytes of real TypeScript, weak and
	// strong claim the same values: the level grades one pattern, and almost all of
	// what a code review over-masks is personal-data categories that no level
	// touches. See plans/source-code-false-positives.md.
	StrengthStrong
)

// isSeparator reports whether a rune only joins parts of a value.
func isSeparator(r rune) bool { return r == '-' || r == '_' || r == '.' }

func (s Strength) String() string {
	switch s {
	case StrengthStrong:
		return "strong"
	case StrengthMedium:
		return "medium"
	default:
		return "weak"
	}
}

// generatedLength is where a value stops being something a person typed.
//
// Thirty-two is the shortest of the key formats this catalogue carries that has no
// prefix to identify it, so anything at least this long and mixed at all is a
// generated credential rather than a chosen one.
const generatedLength = 32

// SecretStrength classifies a value by how many kinds of character it mixes.
//
// Counting classes rather than measuring entropy, and the difference is the whole
// point. Shannon entropy at the threshold gitleaks uses (3.5) was measured against
// this project's own corpus and did worse than counting: it missed
// "hunter2-correct-horse" and "p@ssw0rd!" — both real credentials, both short — while
// still claiming "security.authorize(plainUser" and "process.env.LLM_API_KEY". Entropy
// rewards length and character spread, which is what a long code expression has and a
// short password has not.
func SecretStrength(value string) Strength {
	var lower, upper, digit, symbol bool
	for _, r := range value {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		case isSeparator(r):
			// Not a class. A hyphen joins words, it does not strengthen them:
			// counted, "troisieme-valeur-longue" came out a class above
			// "troisiemevaleurlongue", which made the weak level unreachable and
			// therefore meaningless — every passphrase of plain words landed in
			// medium. A separator says how a value is written, not how hard it is.
			continue
		default:
			symbol = true
		}
	}

	classes := 0
	for _, has := range []bool{lower, upper, digit, symbol} {
		if has {
			classes++
		}
	}

	switch {
	case classes >= 3, classes >= 2 && len(value) >= generatedLength:
		return StrengthStrong
	case classes == 2:
		return StrengthMedium
	default:
		return StrengthWeak
	}
}
