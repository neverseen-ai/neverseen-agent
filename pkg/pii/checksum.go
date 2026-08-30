package pii

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The checksums a shape cannot express. Each one is what separates an
// identifier from any other run of digits of the same length, and the engine
// treats a failure as "not this category" rather than as a weak match: a
// nine-digit order number is not a SIREN that happens to score low.

// LuhnCheck reports whether a card number passes the Luhn checksum, ignoring
// the spaces or dashes a card is printed with — and therefore pasted with.
// Counting a separator as a digit made every grouped number fail both the
// length test and the sum, so the pattern matched and the value was then
// dropped as unverifiable.
func LuhnCheck(number string) bool {
	digits := strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, number)

	if n := len(digits); n < 13 || n > 19 {
		return false
	}
	return luhn(digits)
}

// SIRENCheck reports whether s is nine digits passing Luhn.
func SIRENCheck(s string) bool { return luhn(onlyDigits(s, 9)) }

// SIRETCheck reports whether s is fourteen digits passing Luhn.
//
// TODO: La Poste establishments (SIREN 356000000) use a digit-sum-mod-5 rule
// instead of Luhn and are rejected here. Add the special case if a French
// deployment reports its own SIRET going unmasked.
func SIRETCheck(s string) bool { return luhn(onlyDigits(s, 14)) }

// NIRCheck validates the two trailing check digits of a French social security
// number: key = 97 - (first thirteen digits mod 97).
//
// A Corsican birth department is written 2A or 2B. The official rule replaces
// the letter with 0 and subtracts 1000000 (2A) or 2000000 (2B) before the
// modulo, which is why the letters are folded here rather than rejected.
func NIRCheck(nir string) bool {
	var digits strings.Builder
	corsica := 0
	for _, r := range nir {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == 'A':
			corsica = 1000000
			digits.WriteByte('0')
		case r == 'B':
			corsica = 2000000
			digits.WriteByte('0')
		case r == ' ' || r == '.':
			// separators the pattern tolerates
		default:
			return false
		}
	}

	s := digits.String()
	if len(s) != 15 {
		return false
	}

	body := 0
	for _, r := range s[:13] {
		body = (body*10 + int(r-'0')) % 97
	}
	body = ((body-corsica%97)%97 + 97) % 97

	key := 0
	for _, r := range s[13:] {
		key = key*10 + int(r-'0')
	}
	return 97-body == key
}

// IBANCheck validates an IBAN's mod-97 key (ISO 13616): move the first four
// characters to the end, replace each letter by its position in the alphabet
// plus nine, and the result must be congruent to 1 modulo 97.
//
// Case-insensitive, because an IBAN pasted in lowercase is still an IBAN.
//
// The per-country length is checked, and the false positive that earned it is worth
// recording: "ae5917ce58a7f1e2" — sixteen hex characters, a short git object id —
// was masked as an account. It opens on "AE", which is a real country code, and its
// mod-97 key happens to verify. One string in ninety-seven of the right shape does.
//
// Length is what kills that class, because a hex blob can only open on letters a–f
// and almost none of those pairs is a country whose IBAN is as short as the blob:
// AE is 23, AD 24, BA 20, DE 22, EE 20. Only BE, at 16, still collides — and a
// sixteen-character string opening "BE" whose key verifies has every property a
// Belgian account has.
//
// An unknown country code keeps the old behaviour rather than being refused. The
// registry gains members, and refusing one would silently stop masking a real
// account the day a country joined — a leak, against false positives on the handful
// of two-letter prefixes that are not countries at all and must still clear mod-97.
func IBANCheck(iban string) bool {
	var compact strings.Builder
	for _, r := range strings.ToUpper(iban) {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z':
			compact.WriteRune(r)
		case r == ' ':
			// the conventional grouping by four
		default:
			return false
		}
	}

	s := compact.String()
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	if want, known := ibanLengths[s[:2]]; known && len(s) != want {
		return false
	}

	rearranged := s[4:] + s[:4]
	rem := 0
	for _, r := range rearranged {
		if r >= '0' && r <= '9' {
			rem = (rem*10 + int(r-'0')) % 97
			continue
		}
		rem = (rem*100 + int(r-'A') + 10) % 97 // 'A' -> 10 … 'Z' -> 35, two decimal digits each
	}
	return rem == 1
}

// trimToIBAN shortens text[start:end] until its key verifies, and returns the
// end offset of the longest prefix that is a valid IBAN — or end when none is.
//
// The IBAN pattern has to tolerate the grouping by four, which makes it greedy
// enough to take the next word with it: in "FR14 2004 1010 0505 0001 3M02 606
// EUR" the span reaches into "EUR", the key then fails on the over-long span,
// and the account is discarded and forwarded in clear. Trimming keeps the
// tolerance without the leak.
func trimToIBAN(text string, start, end int) int {
	candidate := text[start:end]
	if IBANCheck(candidate) {
		return end
	}

	// Whole groups first — the common case is an IBAN followed by a currency or
	// a BIC — then characters, for a value glued to the next word with no space
	// to cut on ("DE89370400440532013000EUR").
	grouped := candidate
	for cut := strings.LastIndexByte(grouped, ' '); cut > 0; cut = strings.LastIndexByte(grouped, ' ') {
		grouped = grouped[:cut]
		if IBANCheck(grouped) {
			return start + len(grouped)
		}
	}
	// Run on the original span, not on what the group loop left behind: reusing
	// the shortened copy meant a last group glued to a word was never tried.
	for i := len(candidate) - 1; i >= 15; i-- {
		if IBANCheck(candidate[:i]) {
			return start + i
		}
	}
	return end // nothing valid inside: leave the span, the score will reject it
}

// NHSNumberCheck validates the trailing check digit of an NHS number: weight
// the first nine digits by ten down to two, take the sum modulo eleven, and
// subtract it from eleven. A result of eleven means zero; a result of ten means
// the number is invalid, because there is no digit to write it with.
func NHSNumberCheck(s string) bool {
	d := onlyDigits(s, 10)
	if d == "" {
		return false
	}

	sum := 0
	for i := range 9 {
		sum += int(d[i]-'0') * (10 - i)
	}

	check := 11 - sum%11
	switch check {
	case 11:
		check = 0
	case 10:
		return false
	}
	return check == int(d[9]-'0')
}

// ninoUnissuedPrefixes are the two-letter prefixes that are never allocated,
// because they are reserved or because they collide with something else.
var ninoUnissuedPrefixes = map[string]bool{
	"BG": true, "GB": true, "KN": true, "NK": true, "NT": true, "TN": true, "ZZ": true,
}

// NINOCheck validates a UK National Insurance number's letters. It carries no
// checksum, so the letter rules are all there is, and they are what keeps the
// shape off any other run of two letters and six digits:
//
//   - the first letter is never D, F, I, Q, U or V;
//   - the second is never D, F, I, O, Q, U or V;
//   - seven whole prefixes are never issued (see above);
//   - the suffix, when written, is A, B, C or D.
func NINOCheck(s string) bool {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r == ' ' || r == '-':
			// the spacing it is printed with
		default:
			return false
		}
	}

	v := b.String()
	if len(v) != 8 && len(v) != 9 {
		return false
	}

	// The two leading characters must be letters at all. The pattern that feeds
	// this asks for them, but a check that leans on its caller's expression is a
	// check that passes whatever a future edit lets through: without this,
	// "12345678" verified as a National Insurance number, because eight digits
	// pass every rule below.
	for _, r := range v[:2] {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	if strings.ContainsAny(v[:1], "DFIQUV") || strings.ContainsAny(v[1:2], "DFIOQUV") {
		return false
	}
	if ninoUnissuedPrefixes[v[:2]] {
		return false
	}

	for _, r := range v[2:8] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(v) == 8 || strings.ContainsAny(v[8:9], "ABCD")
}

// SSNCheck applies the ranges the US Social Security Administration never
// issues. There is no checksum, so these are what separate a real number from
// any other three-two-four run of digits — and they exclude the placeholders
// that fill test fixtures and documentation, which is most of what a broad
// pattern would otherwise flag.
func SSNCheck(s string) bool {
	d := onlyDigits(s, 9)
	if d == "" {
		return false
	}

	area, group, serial := d[:3], d[3:5], d[5:]
	switch {
	case area == "000", area == "666", area[0] == '9':
		return false // never allocated, and 9xx is reserved for taxpayer ids
	case group == "00", serial == "0000":
		return false
	}
	return true
}

// RoutingNumberCheck validates an ABA routing number: three weights repeating
// over the nine digits, summing to a multiple of ten.
//
// The leading pair is checked too. It names the Federal Reserve district, only
// some ranges are assigned, and nine bare digits is broad enough that the
// checksum alone would let roughly one arbitrary run in ten through.
func RoutingNumberCheck(s string) bool {
	d := onlyDigits(s, 9)
	if d == "" {
		return false
	}

	district := int(d[0]-'0')*10 + int(d[1]-'0')
	assigned := district <= 12 ||
		(district >= 21 && district <= 32) ||
		(district >= 61 && district <= 72) ||
		district == 80
	if !assigned {
		return false
	}

	at := func(i int) int { return int(d[i] - '0') }
	sum := 3*(at(0)+at(3)+at(6)) + 7*(at(1)+at(4)+at(7)) + (at(2) + at(5) + at(8))
	return sum%10 == 0
}

// onlyDigits returns the digits of s, or "" unless exactly want of them remain.
// A caller's checksum then sees digits or nothing.
//
// The separators are the ones identifiers are printed with — a space, a dot, a
// dash. Which of them a given shape actually admits is the pattern's business,
// not this function's: it only ever sees text a pattern already matched.
func onlyDigits(s string, want int) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '.' || r == '-':
			// separators the patterns tolerate
		default:
			return ""
		}
	}
	if b.Len() != want {
		return ""
	}
	return b.String()
}

// luhn runs the Luhn checksum over any length of digit string.
func luhn(number string) bool {
	if number == "" {
		return false
	}

	sum, alt := 0, false
	for i := len(number) - 1; i >= 0; i-- {
		d := int(number[i] - '0')
		if d < 0 || d > 9 {
			return false
		}
		if alt {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}

// DOBCheck rejects a date that is not far enough in the past to be one somebody
// was born on.
//
// Not a checksum: a date has none, and its shape is satisfied by every deadline,
// meeting and invoice date in a prompt. What separates a birth date from those is
// the one thing the regex cannot express — where it sits relative to today. So it
// goes here, where a failure means "not this category" rather than a weak match,
// and it guards all three date patterns at once: day-first, month-first and ISO
// share CatDOB, and Score is the single place Verify is called.
//
// The threshold is a year rather than zero. "Not in the future" alone still admits
// every date since January, which is where a renewal or a delivery lands.
//
// TODO: the cost is infants. Somebody born four months ago has a real date of
// birth and it is personal data, and this drops it. The upgrade is a rule that
// reads the words around the value rather than the value alone — which is a
// different engine, not a wider expression.
func DOBCheck(date string) bool { return dobCheckAt(date, time.Now()) }

// dobCheckAt is DOBCheck against a given day, so a test can pin one. Reading
// time.Now inside the rule would make the suite that exercises the boundary rot
// into a pass — the failure it is there to catch would simply age out.
func dobCheckAt(date string, now time.Time) bool {
	fields := strings.FieldsFunc(strings.ToLower(date), func(r rune) bool {
		return r == '/' || r == '-' || r == '.' || r == ' ' || r == '\t'
	})
	if len(fields) != 3 {
		// A shape this does not read. Kept rather than dropped: the patterns and
		// this rule are meant to agree, and where they do not, the safe direction
		// for a masking agent is to go on masking.
		return true
	}

	cutoff := now.AddDate(-1, 0, 0)

	// Year first is the ISO form, and the only one that is unambiguous.
	if len(fields[0]) == 4 {
		return notAfter(fields[0], fields[1], fields[2], cutoff)
	}

	// A month spelled out fixes the order, whatever the locale.
	if month, ok := monthNumber(fields[1]); ok {
		// The ordinal the French first of the month carries: "1er mars 2004".
		return notAfter(fields[2], month, strings.TrimSuffix(fields[0], "er"), cutoff)
	}

	// Numeric, and the locale that read it is not carried this far: "05/06/2024"
	// is day-first in France and month-first in the US. Either reading being old
	// enough is enough to go on masking — the two differ by months, and choosing
	// wrong would drop a real birth date to spare an ordinary one.
	return notAfter(fields[2], fields[1], fields[0], cutoff) ||
		notAfter(fields[2], fields[0], fields[1], cutoff)
}

// notAfter reports whether the date these fields spell is at or before cutoff.
func notAfter(year, month, day string, cutoff time.Time) bool {
	y, errY := strconv.Atoi(year)
	m, errM := strconv.Atoi(month)
	d, errD := strconv.Atoi(day)
	if errY != nil || errM != nil || errD != nil {
		return true // unreadable: go on masking, as above
	}
	if m < 1 || m > 12 {
		// The other reading of an ambiguous pair, which the caller ORs with this
		// one. Not "keep masking": that would make every numeric date pass.
		return false
	}
	return !time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC).After(cutoff)
}

// monthNumber reads a month written out, in any language a pattern here accepts.
//
// One lookup over both tables rather than one per language, because the rule this
// feeds is what separates a birth date from a deadline. A name it could not read
// falls through to the numeric branch, where Atoi fails and notAfter answers "go on
// masking" — so a language missing from here does not under-mask, it *stops the
// rule applying at all* and every future date in that language is masked as a birth
// date. Adding a month name to a pattern means adding it here in the same commit.
func monthNumber(name string) (string, bool) {
	if n, ok := frenchMonths[name]; ok {
		return n, true
	}
	n, ok := englishMonths[name]
	return n, ok
}

// frenchMonths maps every spelling frMonthName accepts, accents included and
// omitted, to its number. Lower case because dobCheckAt folds the input.
var frenchMonths = map[string]string{
	"janvier": "1",
	"février": "2", "fevrier": "2",
	"mars": "3", "avril": "4", "mai": "5", "juin": "6", "juillet": "7",
	"août": "8", "aout": "8",
	"septembre": "9", "octobre": "10", "novembre": "11",
	"décembre": "12", "decembre": "12",
}

// englishMonths maps every spelling gbMonthName accepts to its number. Lower case
// for the reason frenchMonths is: dobCheckAt folds the input before looking here.
var englishMonths = map[string]string{
	"january": "1", "february": "2", "march": "3", "april": "4",
	"may": "5", "june": "6", "july": "7", "august": "8",
	"september": "9", "october": "10", "november": "11", "december": "12",
}

// IPAddressCheck reports whether a value really is an IP address.
//
// Not a checksum, and the second case after DOBCheck of Verify carrying a rule the
// regex cannot express. An address has no check digit; what it has is a grammar,
// and the standard library already implements it exactly. A hand-written
// expression for IPv6 has to encode the compression rule — where "::" may appear
// and how many groups it stands for — and every version of that expression anybody
// writes is either too loose or wrong about an edge of the notation.
//
// So the expression finds candidates and this decides, which is what lets the
// expression stay readable. It guards both families at once because it hangs off
// the category: whatever a pattern claims, a value that does not parse is not an
// address and is dropped outright rather than scored down.
func IPAddressCheck(value string) bool {
	_, err := netip.ParseAddr(value)
	return err == nil
}

// GenericSecretCheck rejects a value that is the source code around a secret
// rather than the secret.
//
// genericSecretRe reads "NAME=value" and treats the name as the evidence. That
// premise holds in configuration — a .env line, a YAML key, a shell export — and
// collapses in source code, where `password:` is a *field* name and what follows is
// an expression, a type or an identifier. Pointed at a repository the pattern
// claimed `newPassword`, `req.cookies.token`, `process.env.LLM_API_KEY`,
// `security.authorize(plainUser` and `CreationOptional<string`, none of which is a
// credential and all of which the model then received as [SECRET_n] — a review of
// code whose identifiers had been replaced by opaque tokens.
//
// Two rules, because the false positives came in two shapes, and each is narrower
// than it first looks. The tree already held a case against each over-reach.
//
// One: an *opening* bracket, or the punctuation that only syntax uses. Closing
// brackets are deliberately absent — `PASSWORD=hunter2)` is a real credential whose
// last character is a bracket, which is the case bound-generic-secret-eight-chars-
// ending-on-punctuation exists to hold. An opener cannot arrive that way.
//
// Two: identifier-shaped *and* carrying no digit. Shape alone was too much:
// `Sup3rS3cr3tValue123` is a name by shape and a password in fact
// (TestMaskKeepsJSONEscapingIntact). What separates it from `newPassword` and
// `totpToken` is that code names things in words and a credential almost always
// carries a digit.
//
// The slash and the plus are not code punctuation here: base64 is made of them, and
// a secret is often base64.
//
// TODO: what remains is a credential of nothing but letters and dots — an unquoted
// `PASSWORD=correcthorse` goes out in clear. The upgrade is to read whether the
// value was quoted where it was found, which Verify cannot see: it is handed the
// group and not its surroundings.
func GenericSecretCheck(value string) bool {
	// Openers only. A closing bracket is what a credential ends on; an opening one
	// is what an expression begins.
	if strings.ContainsAny(value, "([{<?;,") {
		return false
	}
	if slugNamingItselfRe.MatchString(value) {
		return false
	}
	if !identifierOnlyRe.MatchString(value) {
		return true
	}
	return strings.ContainsAny(value, "0123456789")
}

// slugNamingItselfRe is a lowercase slug that contains the very word which made
// genericSecretRe look at it: "reset-password" behind `password:`, a route name in an
// object literal or a JSON body.
//
// It is the third rule because the first two cannot reach it. A slug is neither
// syntax nor an identifier — `reset-password` has the shape of
// `troisieme-valeur-longue`, which is a real credential in the corpus. Lowercase
// words joined by hyphens describes both, so shape alone was never going to separate
// them, and the keyword is what does: a passphrase does not name the thing it
// unlocks.
//
// Restricted to lowercase, hyphens and underscores on purpose, which is what keeps a
// weak-but-real password out of it. "MyPassword123!" carries the word too, and its
// capitals, digits and punctuation say it was typed as a secret rather than written
// as a route.
var slugNamingItselfRe = regexp.MustCompile(
	`^[a-z_-]*(?:password|passwd|secret|token|apikey|api_key|access_key)[a-z_-]*$`)

// identifierOnlyRe is a name, or a chain of them: an identifier start, then nothing
// but identifier characters and dots. Trailing dots are allowed on purpose, because
// an elision ("masked...") is prose rather than a credential too.
var identifierOnlyRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$.]*$`)

// ibanLengths is the length ISO 13616 fixes for each country that issues IBANs,
// including the two check digits.
//
// A table rather than a range, because the range is the whole problem: every length
// from 15 to 34 is valid *somewhere*, so without knowing the country the only
// evidence left is the mod-97 key, which one arbitrary string in ninety-seven
// clears. The country code is in the value itself and costs nothing to read.
//
// TODO: transcribed from the ISO 13616 registry rather than generated from it. A
// country joining needs a line here, and the symptom of forgetting is a real account
// masked as before — the safe direction, because an unknown code is not refused.
var ibanLengths = map[string]int{
	"AD": 24, "AE": 23, "AL": 28, "AT": 20, "AZ": 28,
	"BA": 20, "BE": 16, "BG": 22, "BH": 22, "BI": 27, "BR": 29, "BY": 28,
	"CH": 21, "CR": 22, "CY": 28, "CZ": 24,
	"DE": 22, "DJ": 27, "DK": 18, "DO": 28,
	"EE": 20, "EG": 29, "ES": 24,
	"FI": 18, "FK": 18, "FO": 18, "FR": 27,
	"GB": 22, "GE": 22, "GI": 23, "GL": 18, "GR": 27, "GT": 28,
	"HN": 28, "HR": 21, "HU": 28,
	"IE": 22, "IL": 23, "IQ": 23, "IS": 26, "IT": 27,
	"JO": 30,
	"KW": 30, "KZ": 20,
	"LB": 28, "LC": 32, "LI": 21, "LT": 20, "LU": 20, "LV": 21, "LY": 25,
	"MC": 27, "MD": 24, "ME": 22, "MK": 19, "MN": 20, "MR": 27, "MT": 31,
	"MU": 30, "MZ": 25,
	"NI": 28, "NL": 18, "NO": 15,
	"OM": 23,
	"PK": 24, "PL": 28, "PS": 29, "PT": 25,
	"QA": 29,
	"RO": 24, "RS": 22, "RU": 33,
	"SA": 24, "SC": 31, "SD": 18, "SE": 24, "SI": 19, "SK": 24, "SM": 27,
	"SO": 23, "ST": 25, "SV": 28,
	"TL": 23, "TN": 24, "TR": 26,
	"UA": 29,
	"VA": 22, "VG": 24,
	"XK": 20,
	"YE": 30,
}
