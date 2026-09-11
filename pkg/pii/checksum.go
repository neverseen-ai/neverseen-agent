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

	// Mixed case is a CamelCase identifier, not an account.
	//
	// The expression carries (?i) so that an IBAN pasted in lower case is still
	// read, and that tolerance is what let `ED25519KeyPairOpti` through: two
	// letters, two digits and fourteen alphanumerics is the shape, "ED" is not a
	// country the length table knows so the length check waves it past, and one
	// arbitrary string in ninety-seven clears mod-97. Measured over third-party
	// TypeScript it was 7% of every finding, all of it identifiers.
	//
	// An account number is written in one case or the other and never in both. The
	// rule belongs here rather than in the expression because stating it there
	// means two parallel branches for the compact and grouped forms in each case —
	// four alternations to keep in step — where this is one question asked once.
	if hasUpper(iban) && hasLower(iban) {
		return false
	}

	s := compact.String()
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	if want, known := ibanLengths[s[:2]]; known && len(s) != want {
		return false
	}

	// ISO 13616 fixes the check digits to 02-98: the mod-97 key is 98 minus a
	// remainder in 0-96, so 00, 01 and 99 are values the standard cannot produce.
	//
	// Refusing them is what keeps the fake-mode stand-in out of the catalogue's
	// own reach. It is built as "FR00" plus an index (generators.go), and one
	// index in ninety-seven of those happens to clear mod-97 — so the stand-in was
	// re-detected as an IBAN on the next pass, which is exactly what a stand-in
	// must never be.
	switch s[2:4] {
	case "00", "01", "99":
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

// hasUpper and hasLower report which cases a value carries. ASCII only, which is
// all an IBAN may contain.
func hasUpper(v string) bool {
	for i := 0; i < len(v); i++ {
		if v[i] >= 'A' && v[i] <= 'Z' {
			return true
		}
	}
	return false
}

func hasLower(v string) bool {
	for i := 0; i < len(v); i++ {
		if v[i] >= 'a' && v[i] <= 'z' {
			return true
		}
	}
	return false
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

// PostcodeCheck rejects a code followed by an abbreviated month name.
//
// The French pattern takes the commune with the code, because five bare digits
// are not identifiable on their own — so any capitalised word that follows a
// five-digit run is a commune as far as the shape is concerned. A long listing
// puts one there: "13469 Mar  3 10:22 proxy.go" was masked as a postcode, and so
// was every line of every `ls -l`, every tar table and every log in that form.
// Nothing about the shape separates it — "13469 Mar" and "13290 Aix" are written
// the same way — so the word itself is what has to decide, which is a rule the
// regex cannot express.
//
// The three-letter abbreviations only, and only as a whole word. The full names
// are deliberately absent: March is a town in Cambridgeshire and Mars a commune
// in the Loire, so a list carrying those would drop real addresses to spare a
// listing. Whole-word matching is what keeps "May-sur-Orne" a commune.
//
// It hangs off the category, so it guards the British and American patterns too;
// neither puts a word after the code, so nothing there reaches the rule.
func PostcodeCheck(value string) bool {
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return true
	}
	_, isMonth := monthAbbreviations[strings.ToLower(fields[1])]
	return !isMonth
}

// monthAbbreviations is the set a long listing prints, in the C locale. Lower
// case because PostcodeCheck folds the field before looking here.
var monthAbbreviations = map[string]struct{}{
	"jan": {}, "feb": {}, "mar": {}, "apr": {}, "may": {}, "jun": {},
	"jul": {}, "aug": {}, "sep": {}, "oct": {}, "nov": {}, "dec": {},
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
	ip, err := netip.ParseAddr(value)
	if err != nil {
		return false
	}
	return !reservedAddress(ip)
}

// reservedAddress reports whether an address belongs to a range that cannot name
// anybody's machine.
//
// This is the catalogue declining to mask what it already hands out. The stand-in
// generators draw from RFC 5737 and RFC 3849 precisely because those blocks are
// "never routed", and until now the detector claimed them back: `192.0.2.14` in a
// comment was masked, and its replacement was another address from the same block.
// Measured over hand-written application code, a third of everything the catalogue
// found was of this kind.
//
// It is not a guess about likelihood. Every machine has 127.0.0.1, so it
// distinguishes none of them; a link-local address names a network whose DHCP
// failed; the documentation blocks belong to nobody by standard. That is the same
// argument localAddresses makes when it refuses to report loopback and link-local
// as this agent's own addresses.
//
// **Private ranges are deliberately not here.** 10.4.2.17 in a configuration
// somebody pasted is an internal host, and an internal topology is exactly what
// should not reach a model — localAddresses keeps private addresses for the same
// reason, and calling them noise here while reporting them there would be two
// answers to one question.
func reservedAddress(ip netip.Addr) bool {
	switch {
	case ip.IsLoopback(), ip.IsUnspecified(), ip.IsMulticast():
		return true
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return true
	}
	for _, block := range documentationBlocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// documentationBlocks are the ranges the standards set aside for examples,
// documentation and benchmarking. None of them is routed on the internet.
//
// The first three are the blocks fakeGenerators draws IPv4 stand-ins from, and the
// fourth is where the IPv6 ones come from — which is the whole point: a value this
// catalogue would hand out as an unattributable replacement cannot also be
// something worth replacing.
var documentationBlocks = []netip.Prefix{
	netip.MustParsePrefix("192.0.2.0/24"),    // RFC 5737 TEST-NET-1
	netip.MustParsePrefix("198.51.100.0/24"), // RFC 5737 TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // RFC 5737 TEST-NET-3
	netip.MustParsePrefix("2001:db8::/32"),   // RFC 3849
	netip.MustParsePrefix("192.0.0.0/24"),    // RFC 6890 IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),   // RFC 2544 benchmarking
	netip.MustParsePrefix("100::/64"),        // RFC 6666 discard-only
}

// DocumentationEmailCheck rejects an address at a domain that can never be
// registered, and therefore can never reach anybody.
//
// The same rule as reservedAddress and the same evidence: fakeGenerators mints
// stand-ins at example.org because RFC 2606 reserves it, and the detector was
// claiming those addresses back. An address at one of these is documentation
// wherever it appears — in code, in prose, in a support ticket.
//
// Strictly what the standards reserve, and no more. "example.fr" is a real
// registrable domain and stays masked even though this project's own fixtures use
// it: a rule that read "anything beginning example." would be a judgement dressed
// as a standard, and the value it would stop masking might belong to somebody.
func DocumentationEmailCheck(value string) bool {
	at := strings.LastIndexByte(value, '@')
	if at < 0 {
		return true
	}
	domain := strings.ToLower(value[at+1:])

	switch domain {
	case "example.com", "example.net", "example.org": // RFC 2606 §3
		return false
	}
	for _, tld := range documentationTLDs {
		if strings.HasSuffix(domain, tld) {
			return false
		}
	}
	return true
}

// documentationTLDs are the top-level domains RFC 2606 and RFC 6761 reserve. None
// of them resolves, so no address under one can be delivered to.
var documentationTLDs = []string{".test", ".example", ".invalid", ".localhost"}

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
// Three: a dotted identifier chain is a property path. Rule two reads a digit as
// evidence, and `c.S3.SecretAccessKey` gets one from a service name — so a member
// access satisfied both halves of rule two and went out as a token in the middle of
// the caller's own code.
//
// The slash and the plus are not code punctuation here: base64 is made of them, and
// a secret is often base64.
//
// TODO: a credential given as a command-line pair — `curl -u admin:hunter2` — is
// not read either, and it is deliberately not a pattern. There is nothing in the
// shape to separate it from `docker run -u 1000:1000`, which is a user and a
// group, or from any other `a:b` argument: the flag is the same, the position is
// the same, and only the strength of the right-hand side differs. Grading it on
// SecretStrength would work and would put a second, hidden floor under
// SECRET_GENERIC — one the secret level does not control and nobody could see in
// the menu — while masking `1000` on every `docker run` at the default level. The
// upgrade is a rule that reads the command the flag belongs to, which means
// knowing that `curl` and `docker` want different things from `-u`; that is a
// different engine, not a wider expression.
func GenericSecretCheck(value string) bool {
	// The bracket rule is not here. It is UnclosedBracketCheck, on the patterns
	// whose span is cut out of the surrounding text, because whether an unclosed
	// opener is syntax depends on how the span ended and this check is never told:
	// `password="Ab(12cd"` hands it the same value as `password=Ab(12cd`, and only
	// the second was cut out of an expression.
	// A dereference or an address-of is code, and what it points at decides.
	// `want.SecretLevel = *secretLevel` was claimed as a credential once the name
	// no longer had to end on its keyword: the star is not an identifier
	// character, so the identifier rules never looked at the name behind it.
	//
	// Stripped rather than refused outright, because a leading star is not syntax
	// the way an opening bracket is — `PASSWORD=*Hunter2*` is a real password.
	// Stripping hands the rest to the rules below, so it is only dropped when what
	// it points at is *also* code-shaped by them: `*secret123` keeps its digit and
	// stays a credential.
	//
	// Above every rule that reads the value as a word, and not below them. Placed
	// after the two, neither ever saw the stripped value: `SECRET=*string` was
	// masked as a credential while `SECRET=string` was correctly refused by
	// reservedWords, and a YAML alias — `password: *default_secret` — was a
	// credential for the same reason. Both are code, and what the star points at is
	// what says so.
	value = strings.TrimLeft(value, "*&")

	// Optional chaining, and not a bare question mark. `user?.token2`,
	// `user.password?.replace(/./g` and `user.totpSecret?.replace(/./g` are the
	// three code cases carrying a `?`, and all three carry `?.`; a `?` before a
	// letter is punctuation in a typed password, and refusing it left
	// `password="Wh4t?Really"` in clear.
	if strings.Contains(value, "?.") {
		return false
	}
	// The semicolon and the comma are gone from this rule entirely: no case in
	// TestGenericSecretCheckRejectsSourceCode contains either, while
	// `password="a;b;c1234x"`, `password="red,blue1x"` and
	// `API_TOKENS=abc12345,def67890` were all refused for holding one. A list of
	// two tokens behind a plural name is still two tokens.
	if slugNamingItselfRe.MatchString(value) {
		return false
	}
	// A word the language reserved is not a password.
	//
	// The same sentence as slugNamingItselfRe — refused for what it says rather
	// than for its shape — and needed for the same reason: once a lowercase word
	// counted as a credential, `secret_level: string` in this repository's own
	// TypeScript claimed the *type*, and `'X-Session-Id': 'default'` in its
	// extension claimed the session name. TestOurOwnSourceGrowsNoCredentials is
	// where both appeared, which is the measure that matters: this is what a code
	// review through this agent would have seen.
	//
	// A closed set, and short on purpose. Every entry is a token some language
	// spells exactly this way, so it can be checked rather than argued about, and
	// none of them is a password anybody's policy would accept. What it gives up is
	// somebody whose password is literally "default" — weighed against every
	// TypeScript interface in a review coming back with its types replaced by
	// tokens.
	if reservedWords[strings.ToLower(value)] {
		return false
	}
	// A variable reference is where a credential will be read from, not one.
	// `password: "${DB_PASSWORD}"` is the single most common value behind a secret
	// name in a docker-compose.yml, an appsettings.json or an application.yml — the
	// files pasted whole into a review — and once the bracket rule moved onto the
	// bare spans alone, a *quoted* reference was a credential while the bare
	// `POSTGRES_PASSWORD=${DB_PASSWORD}` stayed refused: one value, two answers,
	// decided by nothing but the quotes. The model then reviewed a configuration
	// with its references replaced by [SECRET_n]. A closed set of the four
	// interpolation syntaxes, whole — `${…}`, `{{…}}`, `$(…)` and `%(…)s` — because
	// a real password that opens on one of those and closes on its bracket is not a
	// shape anybody's policy produces.
	if interpolationRe.MatchString(value) {
		return false
	}
	// A number behind a keyword-prefixed name is a tunable, not a secret. Once a
	// keyword could fall anywhere in the name, `max_tokens=200000`,
	// `TOKEN_TTL_SECONDS=8640000` and `PASSWORD_MIN_LENGTH=12345678` were all
	// credentials — and `max_tokens` is the most common numeric field in this
	// agent's own domain, so a pasted model configuration reached the model with
	// its numbers replaced by [SECRET_n].
	//
	// TODO: this drops a password made of nothing but digits — `PASSWORD=12345678`
	// goes out in clear. The upgrade is reading the *name*: `_MAX_`, `_MIN_`,
	// `_TTL`, `_LENGTH`, `_COUNT` say tunable where a bare `PASSWORD=` does not,
	// and the name is available to the expression where it is not to this check.
	if allDigits(value) {
		return false
	}
	if !identifierOnlyRe.MatchString(value) {
		return true
	}
	// Two: a name is written in words joined by case, a password is not.
	//
	// The rule here used to be "identifier-shaped and carrying no digit", and the
	// digit was doing work it cannot do: `PASSWORD=correcthorse`,
	// `PASSWORD=changeme` and `password="correcthorse"` are real credentials of
	// nothing but lowercase letters, and all three were forwarded in clear. It is
	// the gap this catalogue was measured against betterleaks on, and the whole of
	// that difference was this one rule.
	//
	// What separates them from `newPassword` is the case. Every dotless digitless
	// code case asserted in the tree is camelCase — `publicKey`, `newPassword`,
	// `newPasswordInString`, `totpToken`, `updatedToken`, `initialToken` — because
	// that is how code joins words into one name. A passphrase does not.
	//
	// A camelCase *join*, then — a capital after a lowercase letter — and not any
	// capital. Not the first character: `MyPassword123!` and `Sup3rS3cr3tValue123`
	// are credentials that open on one. And not a capital after a capital: read
	// that way, `PASSWORD=HUNTER` was refused as a code identifier while
	// `PASSWORD=Hunter` was masked, one rule giving two answers decided by nothing
	// but the case the password was typed in. Every letter of an ALL-CAPS value is
	// a capital, so none of them is a join — the same reading genericSecretName
	// already applies to the name side. What it costs is a constant referenced by
	// name, `password = DEFAULT_PASSWORD`, which is over-masking in a review rather
	// than a password in clear.
	//
	// A trailing dot is excluded because it is not part of the value: `masked...` is
	// an elision in prose and the shape identifierOnlyRe admits on purpose, and it
	// carries no capital either. It falls to the digit rule at the bottom, which is
	// what refused it before. `hunter2.` passes that rule on its digit and stays a
	// credential, which is the row in TestGenericSecretCheckKeepsCredentials that
	// says a trailing dot must never be read as syntax.
	if !hasInteriorDot(value) && !hasCamelCaseJoin(value) && !strings.HasSuffix(value, ".") {
		return true
	}
	// Three: a dotted chain is a property path, whatever digits it carries.
	//
	// Rule two reads a digit as evidence of a credential, and a member access puts
	// one there for free: `c.S3.SecretAccessKey` was masked as [SECRET_1], and the
	// model received a review of Go code whose field access had been replaced by a
	// token. The digit came from `S3` — a service name, not a password — and every
	// cloud SDK is full of them: `s3`, `ec2`, `oauth2`, `sha256`, `v1`, `utf8`.
	//
	// Only dotted values move, and only those carrying a digit: an identifier chain
	// with no digit was already refused by rule two. `Sup3rS3cr3tValue123` has no
	// dot and stays a credential, which is the case rule two exists for. What is
	// given up is a password made of nothing but letters, digits and interior dots —
	// a shape no password policy asks for and every member access has.
	//
	// The dot has to be *interior*, and TestGenericSecretCheckKeepsCredentials is
	// what says so: `password="hunter2."` hands this the value `hunter2.`, because a
	// quoted value ends where its quote does and the punctuation inside is its own.
	// A trailing dot is the end of a sentence or an elision — which is why
	// identifierOnlyRe admits `masked...` — and reading it as a member access made
	// a real credential stop being masked, the one direction this rule must never
	// move in.
	//
	// A segment also has to be the size and shape of a name, or the dot is doing all
	// the work alone: WARP_READ_TOKEN=yrqUJ...Vhq8.37Zim...XlF went out in clear,
	// a hundred and eighty-two characters of base64url bought the promise written
	// here for c.S3.SecretAccessKey by carrying one interior dot. No field access is
	// a hundred and sixty-one characters long, and no language admits `.37Zim` as
	// one, so that token fails both halves at once.
	if propertyPathRe.MatchString(value) {
		if readsACredential(value) {
			return false
		}
		// A chain no member access is written like, so the name in front of it is
		// the evidence and a digit is not required as well. `secret=abcdef.ghijkl`
		// is the shape rule three gave up when it was written.
		return true
	}
	// Dotless and written in words joined by case: a name, unless it carries a
	// digit. `newPassword` and `totpToken` are how code names a variable;
	// `Sup3rS3cr3tValue123` is how somebody writes a password that has to contain
	// one of each.
	return strings.ContainsAny(value, "0123456789")
}

// readsACredential reports whether a dotted value is code *reading* a credential
// rather than the credential itself.
//
// Rule three used to refuse every property path, and what that gave up is written
// in its own TODO: `secret=abcdef.ghijkl` reads as a member access and nothing in
// the shape says otherwise. Nothing in the shape ever will — `query.current` is two
// lowercase segments of comparable length, and so is `abcdef.ghijkl`. Measured
// against every case in TestGenericSecretCheckRejectsSourceCode, there is no
// length, segment count or character mix that separates the two.
//
// What separates them is what the chain *says*. Two independent marks, either of
// which is enough:
//
//   - The last segment names the credential. `secret = config.password` is code
//     fetching a password, not a password; so are `req.cookies.token`,
//     `c.S3.SecretAccessKey`, `aws.Config.Credentials`, `client.oauth2.Token`,
//     `headers.authorization`. It is the same sentence slugNamingItselfRe already
//     writes for `reset-password`: a passphrase does not name the thing it unlocks,
//     and neither does it name where it was read from.
//   - The chain carries an interior capital. Code joins words by case, and this is
//     what keeps `opts.Sha256Digest`, `utf8.RuneCountInString`,
//     `process.env.LLM_API_KEY` and both of the long chains refused — the cases the
//     segment cap in propertyPathRe was added for.
//
// What it costs is recorded rather than hidden: six member accesses asserted in
// this tree flip to masked, all of them two lowercase segments naming nothing —
// `query.current`, `query.new`, `query.repeat`, `body.new`, `body.repeat`, `a.b2`.
// They are indistinguishable from the credential by every measure available here,
// and over-masking a property access in a code review is the recoverable half of
// the trade: the other half is a password forwarded in clear.
func readsACredential(value string) bool {
	segments := strings.Split(strings.Trim(value, "."), ".")
	if credentialNameTailRe.MatchString(segments[len(segments)-1]) {
		return true
	}
	return hasInteriorCapital(value)
}

// credentialNameTailRe matches a name that ends on the thing it holds — the last
// segment of `config.password` or `c.S3.SecretAccessKey`.
//
// "key" is here where genericSecretKeywordNames deliberately leaves it out, and the
// asymmetry is the point: as a *name* to look behind, a bare "key" is what half the
// configuration languages call the left-hand side of any pair, so it is evidence of
// nothing. As the tail of a member access it is `publicKey`, `SecretKey`,
// `AccessKey` — a field holding a credential, which is what this has to refuse.
//
// "key" and "auth" are short enough to be the end of an ordinary word, and written
// with no boundary in front of them they were: `password: monkey.donkey` had its
// last segment matched on the "key" of "donkey", so readsACredential called the
// chain code and a real password went to the model in clear. Anything ending in
// monkey, turkey, hotkey or oauth did the same, and refusing a credential is the one
// direction this rule must never move in. So the two short words need a boundary in
// front — the start of the segment, a separator, or the lowercase letter that makes
// a camelCase join — while the long ones, which no English word ends on by accident,
// keep the bare suffix match they had.
var credentialNameTailRe = regexp.MustCompile(
	`(?i:password|passwd|secret|token|api[_-]?key|access[_-]?key|credential|` +
		`authorization|authentication)s?$` +
		`|(?:^|[_\-])(?i:key|auth)s?$` +
		`|[a-z0-9](?:Key|Auth)s?$`)

// UnclosedBracketCheck refuses a bare span that opens a bracket it does not close,
// and the balance is the rule rather than the presence.
//
// A bare span was cut out of the surrounding text by its expression, so an opener
// with no closer inside it means the expression it belongs to carries on past where
// the value stopped: `security.authorize(plainUser`, `generateSecret(`,
// `${security.hash(req.body.password`, `CreationOptional<string`. Every code case
// in TestGenericSecretCheckRejectsSourceCode is unbalanced that way, and none of the
// credentials is.
//
// Presence alone was the rule before, and it refused a password for holding a
// matched pair: `password="pa(ren)th1s"` and `password="[brackets]1"` went out in
// clear. A pair that opens and closes inside a value is punctuation somebody typed.
//
// A stray *closer* stays allowed, which it always was: `hunter2)` is a real
// password ending on a bracket, and an expression cannot begin that way.
//
// It hangs off the bare patterns and not the category, because a quoted value ends
// where its quote does and the punctuation inside is the value's own —
// `password="Ab(12cd"` is somebody's "one special character" password, and under
// the category it was refused as code and forwarded in clear. GenericSecretCheck
// is handed the value and not the quotes, so it cannot tell the two apart.
func UnclosedBracketCheck(value string) bool { return !hasUnclosedBracket(value) }

// closerFor is package level because this runs once per candidate value: rebuilt
// inside the function it allocated a map on every call for a table that never
// changes.
var closerFor = map[byte]byte{'(': ')', '[': ']', '{': '}', '<': '>'}

// hasUnclosedBracket reports whether value opens a bracket it does not close.
//
// The span handed to GenericSecretCheck was cut out of the surrounding text, so an
// unclosed opener says the expression continues past the end of the value — which
// is what makes it code rather than a credential. A closer with nothing to match
// is not reported: `hunter2)` is a password whose last character is a bracket, and
// no expression begins on one.
func hasUnclosedBracket(value string) bool {
	var open []byte
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if _, isOpener := closerFor[ch]; isOpener {
			open = append(open, ch)
			continue
		}
		if len(open) > 0 && closerFor[open[len(open)-1]] == ch {
			open = open[:len(open)-1]
		}
	}
	return len(open) > 0
}

// reservedWords are the language tokens a value can be while saying nothing about
// a credential: a primitive type, a literal, or a keyword.
//
// Keyed lowercase and looked up that way, because a value is written `String` in
// one language and `string` in the next.
var reservedWords = map[string]bool{
	// Primitive and pseudo types, which is how `secret_level: string` arrived.
	"string": true, "number": true, "boolean": true, "object": true,
	"int": true, "uint": true, "bool": true, "float": true, "double": true,
	"char": true, "byte": true, "bytes": true, "long": true, "short": true,
	"any": true, "unknown": true, "never": true, "void": true, "map": true,
	"array": true, "list": true, "dict": true, "set": true,

	// Literals and keywords.
	"null": true, "nil": true, "none": true, "nul": true, "undefined": true,
	"true": true, "false": true, "yes": true, "no": true, "on": true, "off": true,
	"default": true, "auto": true, "self": true, "this": true, "super": true,
	"required": true, "optional": true, "enabled": true, "disabled": true,
	"public": true, "private": true, "protected": true, "static": true, "const": true,
}

// allDigits reports whether the value is a number and nothing else.
func allDigits(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return value != ""
}

// hasInteriorDot reports whether value carries a dot that joins two segments,
// rather than trailing punctuation.
//
// The distinction is the one propertyPathRe already draws and for the same reason:
// `masked...` and `hunter2.` end on dots that belong to the sentence, not to a
// member access, and reading them as one stopped a real credential being masked.
func hasInteriorDot(value string) bool {
	return strings.Contains(strings.Trim(value, "."), ".")
}

// hasCamelCaseJoin reports whether a capital follows a lowercase letter — the one
// place a capital says code joined two words into a name.
//
// Not the first character, and not a capital after a capital: `MyPassword123!`
// opens on one and `HUNTER` is made of them, and both are credentials. `newPassword`
// and `totpToken` carry the join.
func hasCamelCaseJoin(value string) bool {
	for i := 1; i < len(value); i++ {
		if value[i] >= 'A' && value[i] <= 'Z' && value[i-1] >= 'a' && value[i-1] <= 'z' {
			return true
		}
	}
	return false
}

// hasInteriorCapital reports whether a capital appears anywhere but the first
// character — the shape of a name code joined out of words.
//
// Interior, because the first character says nothing: `MyPassword123!` and
// `Sup3rS3cr3tValue123` are credentials that open on a capital, while
// `newPassword` and `totpToken` are names that carry one in the middle. Read by
// readsACredential over a dotted chain, where `process.env.LLM_API_KEY` has to
// count; the dotless rule above wants the narrower hasCamelCaseJoin.
func hasInteriorCapital(value string) bool {
	for i := 1; i < len(value); i++ {
		if value[i] >= 'A' && value[i] <= 'Z' {
			return true
		}
	}
	return false
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
//
// The two-word keywords are spelled with either joiner, because a slug is written
// with hyphens: `your-api-key-here` was masked while `your_api_key_here` was not,
// decided by nothing but which of the two a README happened to use.
//
// "auth" is deliberately not in the set. It was, for the header pattern's sake,
// and with no boundary in front of it a four-letter run refused every passphrase
// that merely contained it: `author-of-words`, `my-authentic-horse` and
// `unauthorised-visitors` behind `PASSWORD=` went to the model in clear, while
// `troisieme-valeur-longue`, the corpus's own credential of the identical shape,
// was masked — the `monkey.donkey` failure credentialNameTailRe was fixed for,
// reintroduced in the sibling rule. What the header pattern has to refuse is its
// own concern, and AuthHeaderCheck carries it on that pattern alone.
var slugNamingItselfRe = regexp.MustCompile(
	`^[a-z_-]*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key)[a-z_-]*$`)

// AuthHeaderCheck is the Verify of the two Authorization-header patterns: the bare
// span's bracket rule, and a refusal of a sentence *about* the scheme.
//
// "Authorization: Bearer authentication comme le schéma attendu" is a corpus
// negative, and the value it hands over is `authentication`. It used to be refused
// by GenericSecretCheck's rule two, which read a digitless identifier as code, and
// once that rule became a question about case an all-lowercase word no longer
// reached it. The word naming the mechanism is the evidence — the same sentence
// slugNamingItselfRe writes for `reset-password` — but it belongs here and not in
// that rule, because catalogue-wide it refused real passwords (see the note above
// slugNamingItselfRe). No bearer token, basic credential or GitHub token is a
// lowercase word carrying `auth`.
func AuthHeaderCheck(value string) bool {
	return UnclosedBracketCheck(value) && !authSchemeProseRe.MatchString(value)
}

// authSchemeProseRe is a lowercase slug carrying `auth` — `authentication`,
// `authorization`, `oauth` — which behind a scheme name is prose about the header.
var authSchemeProseRe = regexp.MustCompile(`^[a-z_-]*auth[a-z_-]*$`)

// interpolationRe is a value that is entirely one variable reference, in the four
// syntaxes configuration files use: shell and Compose (`${VAR}`), a template
// (`{{ .Values.x }}`), a subshell (`$(cat file)`) and Python's `%(name)s`.
var interpolationRe = regexp.MustCompile(`^(?:\$\{[^{}]*\}|\{\{[^{}]*\}\}|\$\([^()]*\)|%\([^()]*\)s?)$`)

// identifierOnlyRe is a name, or a chain of them: an identifier start, then nothing
// but identifier characters and dots. Trailing dots are allowed on purpose, because
// an elision ("masked...") is prose rather than a credential too.
var identifierOnlyRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$.]*$`)

// propertyPathRe is a chain of at least two names joined by dots, each of them the
// size and shape a name is: no leading digit, and no longer than a segment of code
// ever gets. Both bounds are what separate `c.S3.SecretAccessKey` from a credential
// that happens to carry a dot, and a token fails whichever it fails first — the
// observed one failed both.
//
// Forty characters, because the longest segments code really writes are type and
// method names (`authenticatedUsersTokenOfTheCurrentSession` is already past what
// anybody types) while the random run in a token is longer than that by an order of
// magnitude. A cap tight enough to cut a real member access would stop masking
// nothing — it would only start masking code again, which is the direction rule
// three exists to prevent.
//
// Trailing dots are admitted for the reason identifierOnlyRe admits them: a member
// chain at the end of a sentence keeps the full stop.
//
// TODO: the known ceiling is now the other way round, and readsACredential records
// it: a chain of two lowercase segments naming nothing — `query.current`, `body.new`
// — is masked, because nothing in the shape separates it from `secret=abcdef.ghijkl`.
// The upgrade is to read whether the value was quoted where it was found, which
// Verify cannot see: it is handed the group and not its surroundings.
var propertyPathRe = regexp.MustCompile(
	`^[A-Za-z_$][A-Za-z0-9_$]{0,39}(?:\.[A-Za-z_$][A-Za-z0-9_$]{0,39})+\.*$`)

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
