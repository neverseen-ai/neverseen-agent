package pii

import "strings"

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
// TODO: the per-country length table is not checked, so a well-formed key on a
// body of the wrong length still passes — one string in 97 of the right shape.
// Add the table if IBAN false positives show up in the corpus.
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
