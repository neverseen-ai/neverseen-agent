package pii

import (
	"regexp"
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
		"req.cookies.token", "headers.authorization", "user.totpSecret",
		"process.env.LLM_API_KEY",

		// A property chain whose digit comes from a service name, not a password.
		// `c.S3.SecretAccessKey` was masked as [SECRET_1] in a review of Go code:
		// identifier-shaped and carrying a digit, so it satisfied both halves of the
		// digit rule. Every cloud SDK spells its services this way.
		"c.S3.SecretAccessKey", "cfg.S3.SecretKey", "aws.Config.Credentials",

		// What a leading star points at, which is what decides. The strip used to sit
		// below the two rules that read the value as a word, so neither ever saw it:
		// `SECRET=*string` was a credential while `SECRET=string` was correctly
		// refused, and a YAML alias was one for the same reason.
		"*string", "*default_secret", "*reset-password",
		"client.oauth2.Token", "opts.Sha256Digest", "req.v1.AccessKey",
		"utf8.RuneCountInString",

		// The longest chains code really writes. The segment cap in propertyPathRe
		// is what a credential carrying a dot fails, so these are what says the cap
		// is not tight enough to start claiming member accesses again.
		"security.authenticatedUsers.tokenOfTheCurrentSession",
		"cfg.Credentials.SessionTokenRefreshIntervalSeconds",
		"process.env.NEXT_PUBLIC_SUPABASE_SERVICE_ROLE_KEY",

		// Calls, and the syntax around them.
		"utils.jwtFrom(req", "verify(utils.jwtFrom(req", "decode(userToken",
		"generateSecret(", "security.authorize({", "security.authorize(plainUser",
		"security.authorize(authenticatedUser", "security.authorize(userWithStatus",
		"security.authenticatedUsers.tokenOf(user", "security.deluxeToken(user.email",
		"${security.hash(req.body.password",
		"user.password?.replace(/./g", "user.totpSecret?.replace(/./g",

		// A TypeScript annotation, from a Sequelize model.
		"CreationOptional<string",

		// A dereference and an address-of. `want.SecretLevel = *secretLevel` is a
		// line in this repository's own mask command, and it was claimed the moment
		// a keyword no longer had to be the last segment of the name: the star is
		// not an identifier character, so the digit rule never looked at the name
		// behind it.
		"*secretLevel", "&cfg.Token", "*opts.apiKey", "**passwordPtr",

		// A route name behind `password:`. Neither syntax nor an identifier — it has
		// the shape of troisieme-valeur-longue, which is a real credential in the
		// corpus. What separates them is that this one names the thing it unlocks.
		"reset-password", "forgot-password", "change-password", "reset_password",
		"access-token", "refresh-token", "api_key",
		// The same slug with the joiner a README writes. `your-api-key-here` was
		// masked while `your_api_key_here` was not.
		"your-api-key-here", "your_api_key_here", "my-access-key",

		// A variable reference behind a quoted name. The bare `${DB_PASSWORD` is
		// refused by the bracket rule because the expression consumed its closer;
		// quoted, the pair is balanced and the value is whole — and it is still
		// where a password will be read from, not one.
		"${DB_PASSWORD}", "${env.DB_PASSWORD}", "{{ .Values.db.password }}",
		"$(cat /run/secrets/db_password)", "%(DB_PASSWORD)s",
	}
	// Every value here arrived through the bare expression — cut out of the text
	// around it — so it is judged as that pattern judges it: the category's check
	// and the span's. The bracket rule lives on the span, because a quoted value
	// that carries an unclosed bracket is a password and must not be refused by a
	// test that never quoted anything.
	for _, v := range code {
		if GenericSecretCheck(v) && UnclosedBracketCheck(v) {
			t.Errorf("GenericSecretCheck(%q) = true, want false — this is code", v)
		}
	}
}

// The price of masking `secret=abcdef.ghijkl`, asserted so that it stays visible.
//
// These six were in the list above until readsACredential replaced rule three's
// blanket refusal of property paths. They are member accesses, they are masked, and
// nothing available here can tell them from the credential: two lowercase segments
// of ordinary length, naming nothing, carrying no capital. `query.current` and
// `abcdef.ghijkl` are the same value as far as any shape rule can see.
//
// The trade was made deliberately and in this direction because the two halves are
// not equal: a property access masked in a code review is over-masking somebody can
// see and undo, while `secret=abcdef.ghijkl` forwarded in clear is a password
// delivered to a model. If a later rule recovers them, this test fails and the
// recovery gets noticed rather than being mistaken for a bug.
func TestGenericSecretCheckOverMasksLowercaseMemberAccess(t *testing.T) {
	for _, v := range []string{
		"query.current", "query.new", "query.repeat",
		"body.new", "body.repeat", "a.b2",
	} {
		if !GenericSecretCheck(v) {
			t.Errorf("GenericSecretCheck(%q) = false — this is now refused again, which "+
				"is an improvement: move it back into TestGenericSecretCheckRejectsSourceCode", v)
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
		{"S3cretAccessKey1", "identifier-shaped with a digit and no dot: rule three must not reach it"},
		{"hunter2.", "a trailing dot is prose punctuation, not a member access"},

		// The short words in credentialNameTailRe had no boundary in front of them,
		// so an ordinary English word ending in "key" or "auth" read as a field
		// holding a credential and the password went out in clear. Refusing a
		// credential is the one direction that rule must never move in.
		{"monkey.donkey", "\"donkey\" ends on \"key\" and names nothing"},
		{"turkey.oauth1", "the same on both segments"},
		// The same failure in slugNamingItselfRe: a bare `auth` in it, with no
		// boundary, refused every passphrase containing the letters.
		{"author-of-words", "\"author\" carries \"auth\" and names no mechanism"},
		{"my-authentic-horse", "the same, mid-slug"},
		{"unauthorised-visitors", "the same, where the keyword is a real word's stem"},
		{"*Hunter2*", "a star is not syntax the way a bracket is: stripped, the digit still decides"},
		{"*p@ssw0rd", "punctuation a name never has, whatever leads it"},

		// Observed: a WARP_READ_TOKEN forwarded in clear. One interior dot in a
		// hundred and eighty-two characters of base64url was enough to buy the
		// promise rule three writes for c.S3.SecretAccessKey. The value here is
		// synthetic — same length, same shape, same two failing segments — because
		// the real one is a live credential and this file is committed.
		{
			"yrqUJHsihujAc7e1yqb3RRNqMaZjMPsqYjB0dsj68QnzpRAUOg6W7jyMNpgoCMpq78Lk" +
				"AmawnBq7a7PhltL1KimflP9a9V6WIfo74hFBWThWcZkUov5963yQhnD6LtEe4mYpu" +
				"lmDwlJLSdcWsbSh8N1JnhGmcVhq8.37Zimmlrk4sWXTTU9XlF",
			"a token with one interior dot: no member access has a segment this long",
		},
		{
			"Vhq8ZimmlrkAsWXTTU9XlFqbRRNqMaZjMPsqYjB0dsj.Token",
			"the segment cap alone — every segment here is letter-led and only the first is long",
		},
		{
			"ZimmlrkAsWXTTU9XlF.37Zimmlrk4sWXTTU9XlF",
			"the leading digit alone — both segments are short, and no language names a member 37Zim",
		},

		// Qualified by hand in docs/secret-shapes-to-label.md. Each of these was
		// forwarded in clear, and each was refused by a rule that read punctuation,
		// length or the absence of a digit as evidence of code.
		{"Wh4t?Really", "a question mark before a letter is typed punctuation; only `?.` is code"},
		{"a;b;c1234x", "no code case in the set above carries a semicolon"},
		{"red,blue1x", "nor a comma"},
		{"abc12345,def67890", "two tokens behind a plural name are still two tokens"},
		{"pa(ren)th1s", "a pair that opens and closes inside the value is punctuation, not syntax"},
		{"[brackets]1", "the same, in square brackets"},
		{"correcthorse", "lowercase words are how a passphrase is written and not how code names things"},
		{"changeme", "the placeholder everybody forgets to change is a real password"},
		{"mcjrx4", "six characters is a bad password, not an absent one"},
		{"abcdef.ghijkl", "a chain that names nothing and carries no capital is a password with a dot"},
	}
	for _, s := range secrets {
		if !GenericSecretCheck(s.value) {
			t.Errorf("GenericSecretCheck(%q) = false, want true — %s", s.value, s.why)
		}
	}
}

// A sentence about the scheme, behind authHeaderRe. "Authorization: Bearer
// authentication comme le schéma attendu" is a corpus negative, and the header
// patterns refuse it on their own Verify — not through slugNamingItselfRe, where a
// bare `auth` refused the passphrases above catalogue-wide.
func TestAuthHeaderCheckRefusesProseAboutTheScheme(t *testing.T) {
	for _, v := range []string{"authentication", "authorization", "oauth", "auth-required"} {
		if AuthHeaderCheck(v) {
			t.Errorf("AuthHeaderCheck(%q) = true, want false — prose about the scheme", v)
		}
	}
	for _, v := range []string{"aGVsbG8gd29ybGQrLw==", "ghp_AbCdEf0123456789", "hunter2-correct-horse"} {
		if !AuthHeaderCheck(v) {
			t.Errorf("AuthHeaderCheck(%q) = false, want true — a credential", v)
		}
	}
}

// The long listing this came from: `ls -l` puts the month right after the size,
// which is exactly where the postcode pattern expects a commune.
func TestPostcodeCheckRejectsALongListing(t *testing.T) {
	for _, v := range []string{
		"13469 Mar", "11175 Mar", "87617 Aug", // the three observed on one run
		"13469 Jan", "13469 Feb", "13469 Apr", "13469 May", "13469 Jun",
		"13469 Jul", "13469 Sep", "13469 Oct", "13469 Nov", "13469 Dec",
	} {
		if PostcodeCheck(v) {
			t.Errorf("PostcodeCheck(%q) = true, want false — that is a month, not a commune", v)
		}
	}
}

// The other half. Narrowing a pattern is the change that leaks, so what must
// still be masked is asserted beside what must not.
func TestPostcodeCheckKeepsRealCodes(t *testing.T) {
	codes := []struct{ value, why string }{
		{"13290 Aix Les Milles", "the sample's own case"},
		{"75002 Paris", "one commune"},
		{"42750 Mars", "a commune in the Loire, and the French month — the list carries neither full name"},
		{"14320 May-sur-Orne", "a commune opening on a month: whole words only"},
		{"PE15 8NF", "March, Cambridgeshire — the gb pattern, and a town named after a month"},
		{"SW1A 1AA", "the gb pattern: no word follows the code"},
		{"IL 62704", "the us pattern: the state comes first"},
		{"62704-1234", "the us pattern, ZIP+4"},
	}
	for _, c := range codes {
		if !PostcodeCheck(c.value) {
			t.Errorf("PostcodeCheck(%q) = false, want true — %s", c.value, c.why)
		}
	}
}

// The name expression, both directions at once.
//
// Two rules were wrong here in the same place, and each is only visible against
// the other. The separator had to follow the keyword immediately, so a prefix was
// free and a suffix was fatal: `VERY_SECRET=` was masked while `VERY_SECRET_TOO=`,
// `SUPER_SECRET_VALUE=` and `STRIPE_SECRET_KEY=` went out in clear — the last of
// them the ordinary way to name a Stripe or an AWS key, missed by nothing but
// where the word fell in the name. And the keyword had to be spelled exactly, so
// `SUPER_SEECRET_VALUE=` was invisible: a typo, and the shape somebody reaches for
// to slip a value past a scanner.
//
// What must not move is the other half. The tail opens on a new word or it runs on
// through `secretary_id` and `tokenised_at` — a separator always, a capital only
// where the keyword's own last letter was lowercase, because that is what a
// camelCase boundary is and an all-capitals name has no such thing. And the letters
// tolerate insertions only, never arbitrary gaps, which is what the last case here
// pins.
func TestGenericSecretNameReadsTheKeywordWhereverItFalls(t *testing.T) {
	const value = `f6CGV4aMM9zedoh3OUNbSakBymo7yplB`

	cases := []struct {
		name string
		want bool
		why  string
	}{
		{"SECRET", true, "the keyword alone, as it always was"},
		{"VERY_SECRET", true, "a prefix was always free"},
		{"VERY_SECRET_TOO", true, "and a suffix was always fatal"},
		{"SUPER_SECRET_VALUE", true, "the same, one segment further out"},
		{"STRIPE_SECRET_KEY", true, "how a Stripe or an AWS key is actually named"},
		{"accessTokenValue", true, "camelCase: a capital opens a word as a separator does"},
		{"SUPER_SEECRET_VALUE", true, "a repeated letter — a typo, or a scanner being dodged"},
		{"S_E_C_R_E_T", true, "one separator between the letters"},
		{"api-key", true, "the keyword list carries no separators; the expression tolerates them"},
		{"API_TOKENS", true, "a plural is the same name, and it ends the word the tail opens after"},
		{"api_tokens", true, "the same in the case a JSON body writes it"},
		{"accessTokensValue", true, "and a plural is a camelCase boundary too"},

		{"secretary_id", false, "the tail has to open on a new word, or a word containing one is a name"},
		{"tokenised_at", false, "the same, in the other tense"},
		{"sha256Digest", false, "no keyword in it at all"},

		// The same three names in the case a .env file writes them, and the half the
		// tail rule used to read nothing at all. Every letter of a SCREAMING_SNAKE
		// name is a capital, so a tail that opened on "a separator or a capital"
		// opened on the `A` of `SECRETARY` and the guard was a no-op: these were
		// masked while their lowercase twins above were clean, decided by nothing but
		// the case they were typed in. The third is the one that cost something —
		// a credential wins every overlap, so the address behind it was replaced by
		// [SECRET_n] rather than by a stand-in address.
		{"SECRETARY_ID", false, "a capital opens a word only where the letter before it was lowercase"},
		{"TOKENISED_AT", false, "the same, in the other tense"},
		{"SECRETARIAT_EMAIL", false, "and this one had the address itself claimed as the credential"},
		{"SESSION_IDLE_TIMEOUT", false, "SESSION_ID is the keyword; IDLE is the rest of a longer word"},

		// The refutation of the obvious reading. A free sub-sequence — the letters
		// in order with anything between them — matches a random base64 blob 7% of
		// the time at sixty characters, 20% at eighty and 94% at a hundred and
		// eighty-two. Every long hash and integrity field in a lockfile would
		// become a *name*, and it is whatever follows it that then gets masked.
		{
			"vo7J4YHb6t9sBFLyY03WYhXET37qA4zOYUjBWFCRHO7pS1B9khERtY0f5JXPQ", false,
			"a blob carrying s-e-c-r-e-t in order is not a name",
		},
	}

	for _, c := range cases {
		text := c.name + ` = "` + value + `"`
		got := genericSecretQuotedRe.FindStringSubmatch(text)
		if (got != nil) != c.want {
			t.Errorf("%q behind %q: matched = %v, want %v — %s",
				value, c.name, got != nil, c.want, c.why)
			continue
		}
		if c.want && got[1] != value {
			t.Errorf("behind %q the group is %q, want the value %q", c.name, got[1], value)
		}
	}
}

// A four-hex-character prefix occurs inside any long hash, so the length is the
// whole of the evidence and both ends have to be bounded.
//
// Observed: `sha512-4b1d0123…` in a lockfile had forty-two characters cut out of the
// middle of it and masked as a ClickHouse key, leaving the rest of the hash in clear
// — a pasted package-lock.json came back to the model with fragments of its
// integrity fields replaced by tokens. It is the openAILegacyRe class of failure
// with nothing to rescue it: no other pattern claims that span, so no equal-score
// tie-break ever runs.
func TestClickHouseKeyIsNotCutOutOfAHash(t *testing.T) {
	const key = "4b1d0123456789abcdef0123456789abcdef012345"

	cases := []struct {
		text string
		want string
		why  string
	}{
		{"CLICKHOUSE_KEY=" + key, key, "the key on its own, which is what must still be masked"},
		{"the key is " + key + ".", key, "a sentence ends it; the full stop is a boundary"},
		{"sha512-4b1d0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd", "",
			"a lockfile integrity hash that happens to carry the prefix"},
		{key + "0123", "", "four characters too long is not a key of this length"},
		{"x" + key, "", "and nor is one with a character in front of it"},
	}

	for _, c := range cases {
		got := ""
		if m := clickhouseRe.FindStringSubmatch(c.text); m != nil {
			got = m[1]
		}
		if got != c.want {
			t.Errorf("clickhouseRe over %q = %q, want %q — %s", c.text, got, c.want, c.why)
		}
	}
}

// XML attribute order is not significant, and NuGet tooling writes both.
//
// The rule read `key` first and matched the literal `Password` exactly, so a
// NuGet.config with a private feed in it — the file somebody pastes whole to ask why
// a restore fails — leaked its password whenever the writer had put `value` first or
// spelled the attribute in lower case.
func TestNuGetPasswordIsReadInEitherAttributeOrder(t *testing.T) {
	const password = "hunter2horse"

	cases := []struct {
		text string
		want string
		why  string
	}{
		{`<add key="ClearTextPassword" value="` + password + `" />`, password, "the documented order"},
		{`<add key="Password" value="` + password + `" />`, password, "and its encrypted spelling"},
		{`<add value="` + password + `" key="Password" />`, password, "the same pair, written the other way round"},
		{`<add key="password" value="` + password + `" />`, password, "lower case: the tooling emits both"},
		{`<add key="Username" value="` + password + `" />`, "", "a different attribute is not a password"},
	}

	for _, c := range cases {
		got := ""
		for _, re := range []*regexp.Regexp{nugetPasswordRe, nugetPasswordReversedRe} {
			if m := re.FindStringSubmatch(c.text); m != nil {
				got = m[1]
				break
			}
		}
		if got != c.want {
			t.Errorf("the NuGet rules over %q = %q, want %q — %s", c.text, got, c.want, c.why)
		}
	}
}
