package pii

import (
	"testing"
	"time"
)

// The checksums are what let the shapes be loose. A checksum that accepts
// everything turns every pattern into its widest reading — and reports nothing
// wrong while doing it — so each one is exercised on values that must pass and
// values that must fail.

func TestLuhnCheck(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"visa", "4532015112830366", true},
		{"mastercard", "5425233430109903", true},
		{"visa grouped by four", "4532 0151 1283 0366", true},
		{"visa written with dashes", "4532-0151-1283-0366", true},
		// The separators must not be counted as digits: when they were, every
		// grouped number — the form people actually paste — failed both the
		// length test and the sum.
		{"one digit changed", "4532015112830367", false},
		{"too short to be a card", "453201511283", false},
		{"too long to be a card", "45320151128303661234", false},
		{"empty", "", false},
		{"not digits at all", "4532O15112830366", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LuhnCheck(tt.value); got != tt.want {
				t.Errorf("LuhnCheck(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// The length a country fixes is what separates an account from a hash whose key
// happens to verify. One arbitrary string in ninety-seven does.
// Luhn is one digit of evidence — a tenth of arbitrary runs clear it — so the length
// has to be the other half, exactly as the per-country table is for the IBAN.
//
// Every value here passes Luhn. What separates them is that no Visa card has ever had
// fourteen or fifteen digits, so those two lengths were pure false positive catching a
// tenth of the numbers that reached them.
func TestCreditCardShapeRejectsLengthsNoVisaHas(t *testing.T) {
	tests := []struct {
		value string
		want  bool
		why   string
	}{
		{"4333333333335", true, "thirteen — the older Visa"},
		{"43333333333338", false, "fourteen — no card has this"},
		{"433333333333336", false, "fifteen — nor this"},
		{"4333333333333339", true, "sixteen — the usual Visa"},
		{"4532015112830366120", true, "nineteen — issued, and missed until the third group"},
		{"4532 0151 1283 0366 120", true, "nineteen, grouped 4-4-4-4-3"},

		// Seventeen and eighteen sit between two real lengths and are neither.
		{"43333333333333337", false, "seventeen"},
		{"433333333333333330", false, "eighteen"},

		{"4532 0151 1283 0366", true, "grouped by four"},
		{"4532-0151-1283-0366", true, "grouped with dashes"},
		{"5555555555554444", true, "Mastercard, sixteen"},
		{"371449635398431", true, "Amex, fifteen"},
	}

	for _, tt := range tests {
		if !LuhnCheck(tt.value) {
			t.Fatalf("%q does not pass Luhn, so this case proves nothing", tt.value)
		}
		got := creditCardRe.FindString("carte "+tt.value+" fin") != ""
		if got != tt.want {
			t.Errorf("the card shape claims %q = %v, want %v — %s", tt.value, got, tt.want, tt.why)
		}
	}
}

func TestIBANCheckAppliesThePerCountryLength(t *testing.T) {
	tests := []struct {
		value string
		want  bool
		why   string
	}{
		// The short git object id that was masked as an account. "AE" is a real
		// country code and the mod-97 key verifies; an Emirati IBAN is 23.
		{"ae5917ce58a7f1e2", false, "sixteen characters under a country that fixes 23"},
		{"AE5917CE58A7F1E2", false, "the same, in the case it was stored as"},

		// Real accounts, at the length their country fixes.
		{"FR1420041010050500013M02606", true, "France, 27"},
		{"DE89370400440532013000", true, "Germany, 22"},
		{"GB33BUKB20201555555555", true, "United Kingdom, 22"},
		{"BE68539007547034", true, "Belgium, 16 — the one length a hex blob can still reach"},
		{"NL91ABNA0417164300", true, "Netherlands, 18"},
	}

	for _, tt := range tests {
		if got := IBANCheck(tt.value); got != tt.want {
			t.Errorf("IBANCheck(%q) = %v, want %v — %s", tt.value, got, tt.want, tt.why)
		}
	}
}

func TestIBANCheck(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"french, compact", "FR1420041010050500013M02606", true},
		{"french, grouped by four", "FR14 2004 1010 0505 0001 3M02 606", true},
		// An IBAN pasted in lowercase is still an IBAN, and refusing it there
		// means forwarding the account in clear.
		{"french, lowercase", "fr1420041010050500013m02606", true},
		{"german", "DE89370400440532013000", true},
		{"belgian", "BE68539007547034", true},
		// The shape the pattern cannot tell from an IBAN: a purchase order of
		// the same layout. The key is the only thing that separates them.
		{"purchase order of the same shape", "PO12ABCD3456EFGH", false},
		{"one check digit changed", "FR1520041010050500013M02606", false},
		{"too short", "FR142004", false},
		{"punctuation inside", "FR14-2004-1010-0505", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IBANCheck(tt.value); got != tt.want {
				t.Errorf("IBANCheck(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestNIRCheck(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"spaced, as printed", "2 69 05 49 588 157 80", true},
		{"compact", "184037511600176", true},
		// Fifteen digits of the right shape whose key does not verify: an
		// internal identifier in a legacy table, which the corpus carries as a
		// precision case.
		{"lookalike with a wrong key", "269054958815781", false},
		{"too short", "26905495881", false},
		{"letters where the key belongs", "2690549588157AB", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NIRCheck(tt.value); got != tt.want {
				t.Errorf("NIRCheck(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// A Corsican birth department is written 2A or 2B, and the official rule folds
// the letter to 0 and subtracts a constant before the modulo. Without that
// branch every Corsican number is rejected and goes out unmasked.
func TestNIRCheck_Corsica(t *testing.T) {
	// Built from the rule itself: fold the letter to a zero — so "2A" is read as
	// the digits "20" — apply the offset, take the key. Both halves of the
	// branch are exercised, because 2A subtracts 1000000 and 2B subtracts
	// 2000000: one working does not mean the other does.
	for _, nir := range []string{"1 88 06 2A 001 001 40", "1 88 06 2B 001 001 67"} {
		if !NIRCheck(nir) {
			t.Errorf("NIRCheck(%q) rejected a Corsican number the official rule accepts", nir)
		}
	}

	// The offset genuinely applies. The same thirteen digits read as a mainland
	// department key to 13, so a Corsican key must not verify against them — if
	// it does, the offset is being ignored and every Corsican number is accepted
	// or rejected by accident.
	if NIRCheck("1 88 06 20 001 001 40") {
		t.Error("a Corsican key verified against a mainland body: the offset is not being applied")
	}
	if !NIRCheck("1 88 06 20 001 001 13") {
		t.Error("a mainland number with the correct key was rejected")
	}
}

func TestSIRENAndSIRETCheck(t *testing.T) {
	t.Run("SIREN", func(t *testing.T) {
		tests := []struct {
			value string
			want  bool
		}{
			{"443061841", true},
			{"732829320", true},
			{"732 829 320", true}, // as printed, in threes
			{"443061842", false},  // one digit changed
			{"44306184", false},   // eight digits is not a SIREN
			{"4430618411", false}, // nor is ten
			{"", false},
		}
		for _, tt := range tests {
			if got := SIRENCheck(tt.value); got != tt.want {
				t.Errorf("SIRENCheck(%q) = %v, want %v", tt.value, got, tt.want)
			}
		}
	})

	t.Run("SIRET", func(t *testing.T) {
		tests := []struct {
			value string
			want  bool
		}{
			{"55210055400013", true},
			{"552 100 554 00013", true}, // as printed
			{"55210055400014", false},   // one digit changed
			{"552100554", false},        // a SIREN is not a SIRET
			{"", false},
		}
		for _, tt := range tests {
			if got := SIRETCheck(tt.value); got != tt.want {
				t.Errorf("SIRETCheck(%q) = %v, want %v", tt.value, got, tt.want)
			}
		}
	})
}

func TestNHSNumberCheck(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"grouped as printed", "943 476 5919", true},
		{"compact, as exported", "9434765919", true},
		{"all nines, whose check digit is nine", "9999999999", true},
		{"last digit changed", "943 476 5918", false},
		{"nine digits", "943476591", false},
		{"eleven digits", "94347659191", false},
		// A US telephone number is written in the same 3-3-4 layout, which is
		// what the pattern matches on. Only the check digit tells them apart.
		{"a telephone number in the same layout", "555 234 5678", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NHSNumberCheck(tt.value); got != tt.want {
				t.Errorf("NHSNumberCheck(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// The National Insurance number has no checksum, so these letter rules are the
// entire difference between the pattern and "any two letters and six digits".
// Each rule gets a case: with one of them missing the pattern still looks like
// it works, on everything except the shape that rule excluded.
func TestNINOCheck(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"spaced as printed", "AB 12 34 56 C", true},
		{"compact", "AB123456C", true},
		{"without the optional suffix", "AB123456", true},
		{"lower case", "ab123456c", true},
		{"first letter excluded", "DA123456C", false},
		{"second letter excluded", "AO123456C", false},
		{"prefix never issued", "GB123456C", false},
		// The placeholder every HMRC document uses. It is invalid on purpose,
		// and masking it tokenizes the documentation someone pasted in to ask a
		// question about.
		{"the HMRC example", "QQ123456C", false},
		{"suffix beyond D", "AB123456E", false},
		{"too few digits", "AB12345C", false},
		{"digits where the letters belong", "12345678", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NINOCheck(tt.value); got != tt.want {
				t.Errorf("NINOCheck(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// The social security number has no checksum either. What it has is four ranges
// the issuer never allocates — and those ranges are exactly what documentation
// and test fixtures are written with, so they are most of what a bare
// three-two-four pattern would otherwise claim.
func TestSSNCheck(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"allocated", "123-45-6789", true},
		{"compact", "123456789", true},
		{"area 000", "000-45-6789", false},
		{"area 666", "666-45-6789", false},
		{"area in the 9xx range reserved for taxpayer ids", "900-45-6789", false},
		{"group 00", "123-00-6789", false},
		{"serial 0000", "123-45-0000", false},
		{"too short", "123-45-678", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SSNCheck(tt.value); got != tt.want {
				t.Errorf("SSNCheck(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestRoutingNumberCheck(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"a commercial bank", "021000021", true},
		{"a reserve bank", "011000015", true},
		{"last digit changed", "021000022", false},
		// The checksum passes on this one; the district does not exist. Nine
		// bare digits is broad enough that the weighted sum alone lets roughly
		// one arbitrary run in ten through, so both halves are needed.
		{"checksum passes but the district is unassigned", "991000021", false},
		{"a plain counter", "123456789", false},
		{"eight digits", "02100002", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RoutingNumberCheck(tt.value); got != tt.want {
				t.Errorf("RoutingNumberCheck(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// The trim is what keeps the IBAN pattern's tolerance for grouping from
// swallowing the following word. Without it the over-long span fails the key,
// the match is discarded, and the account is forwarded in clear — a leak caused
// by a checksum working correctly on the wrong span.
func TestTrimToIBAN(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "an exact span is left alone",
			text: "FR1420041010050500013M02606",
			want: "FR1420041010050500013M02606",
		},
		{
			name: "a trailing currency group is dropped",
			text: "FR14 2004 1010 0505 0001 3M02 606 EUR",
			want: "FR14 2004 1010 0505 0001 3M02 606",
		},
		{
			name: "two trailing words are dropped",
			text: "FR14 2004 1010 0505 0001 3M02 606 EUR ce",
			want: "FR14 2004 1010 0505 0001 3M02 606",
		},
		{
			// No space to cut on, so the group loop cannot help and the
			// character fallback has to run on the original span. When it
			// inherited the loop's mutilated copy instead, this case went out
			// unmasked.
			name: "a word glued to the account with no space",
			text: "DE89370400440532013000EUR",
			want: "DE89370400440532013000",
		},
		{
			name: "nothing valid inside leaves the span alone for the score to reject",
			text: "PO12ABCD3456EFGH",
			want: "PO12ABCD3456EFGH",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			end := trimToIBAN(tt.text, 0, len(tt.text))
			if got := tt.text[:end]; got != tt.want {
				t.Errorf("trimToIBAN kept %q, want %q", got, tt.want)
			}
		})
	}
}

// The boundary is pinned to a fixed day on purpose. Against time.Now the cases
// below would age out one by one and the suite would go green over a rule that
// had stopped being exercised.
func TestDOBCheck(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		value string
		want  bool
		why   string
	}{
		// Old enough, in every notation the three patterns accept.
		{"23/02/2004", true, "day-first, slashes"},
		{"23-02-2004", true, "day-first, dashes"},
		{"23.02.2004", true, "day-first, dots"},
		{"23 03 2004", true, "day-first, spaces"},
		{"23 février 2004", true, "month spelled out"},
		{"5 aout 1999", true, "month spelled out, unaccented"},
		{"1er mars 2004", true, "the French ordinal first"},
		{"03/14/1987", true, "month-first"},
		{"1987-03-14", true, "ISO"},
		{"1987/03/14", true, "ISO with slashes"},

		// The future is nobody's birth date, whatever the notation.
		{"31/12/2099", false, "day-first, far future"},
		{"12/31/2099", false, "month-first, far future"},
		{"2099-12-31", false, "ISO, far future"},
		{"31 décembre 2099", false, "month spelled out, far future"},

		// The year is the whole point of the threshold: a date since January is
		// a renewal or a deadline, not a birth.
		{"2026-01-15", false, "earlier this year"},
		{"15/01/2026", false, "earlier this year, day-first"},
		{"2025-12-01", false, "within the last year"},

		// Just either side of the cut-off.
		{"2025-08-27", true, "a day past the threshold"},
		{"2025-08-29", false, "a day short of it"},

		// Ambiguous numerics: either reading being old enough is enough.
		{"05/06/2024", true, "day-first or month-first, both old enough"},

		// A shape this rule cannot read goes on being masked.
		{"not a date at all", true, "unreadable, so kept"},
	}

	for _, tt := range tests {
		if got := dobCheckAt(tt.value, now); got != tt.want {
			t.Errorf("dobCheckAt(%q) = %v, want %v — %s", tt.value, got, tt.want, tt.why)
		}
	}
}

// Every value here was observed on one real run: an agent pointed at a repository,
// masking the source code around the secrets instead of the secrets. The model
// received [SECRET_n] where the code said `newPassword`, which makes a review of
// that code unreadable.
func TestGenericSecretCheckRejectsSourceCode(t *testing.T) {
	code := []string{
		// Property chains and bare names — the half a shape rule alone would miss.
		"publicKey", "newPassword", "newPasswordInString", "totpToken",
		"updatedToken", "initialToken", "masked...",
		"req.cookies.token", "query.current", "query.new", "query.repeat",
		"body.new", "body.repeat", "headers.authorization", "user.totpSecret",
		"process.env.LLM_API_KEY",

		// Calls, and the syntax around them.
		"utils.jwtFrom(req", "verify(utils.jwtFrom(req", "decode(userToken",
		"generateSecret(", "security.authorize({", "security.authorize(plainUser",
		"security.authorize(authenticatedUser", "security.authorize(userWithStatus",
		"security.authenticatedUsers.tokenOf(user", "security.deluxeToken(user.email",
		"${security.hash(req.body.password",
		"user.password?.replace(/./g", "user.totpSecret?.replace(/./g",

		// A TypeScript annotation, from a Sequelize model.
		"CreationOptional<string",

		// A route name behind `password:`. Neither syntax nor an identifier — it has
		// the shape of troisieme-valeur-longue, which is a real credential in the
		// corpus. What separates them is that this one names the thing it unlocks.
		"reset-password", "forgot-password", "change-password", "reset_password",
		"access-token", "refresh-token", "api_key",
	}
	for _, v := range code {
		if GenericSecretCheck(v) {
			t.Errorf("GenericSecretCheck(%q) = true, want false — this is code", v)
		}
	}
}

// The other half of the gate. Narrowing a credential pattern is the change that
// leaks, so what must still be caught is asserted beside what must not.
func TestGenericSecretCheckKeepsCredentials(t *testing.T) {
	secrets := []struct{ value, why string }{
		{"hunter2-correct-horse", "the reference in pkg/pii/sample.go"},
		{"troisieme-valeur-longue", "the corpus case"},
		{"hunter2)", "eight characters ending on a bracket — a closer is not syntax"},
		{"Sup3rS3cr3tValue123", "identifier-shaped, but it carries digits"},
		{"p@ssw0rd!", "punctuation a name never has"},
		{"sk-ant-api03-AbCdEf", "separators"},
		{"aGVsbG8gd29ybGQrLw==", "base64: the slash and the plus are not code"},
		{"correct-horse-battery", "a passphrase does not name what it unlocks"},
		{"MyPassword123!", "carries the word, but capitals and punctuation say it was typed as one"},
	}
	for _, s := range secrets {
		if !GenericSecretCheck(s.value) {
			t.Errorf("GenericSecretCheck(%q) = false, want true — %s", s.value, s.why)
		}
	}
}
