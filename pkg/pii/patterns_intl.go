package pii

import "regexp"

// The locale-independent set: shapes that mean the same thing everywhere, and
// therefore stay on whatever locale a deployment selects. Turning off a country
// must never turn off email detection.

var (
	// The local part accepts letters of any script, and the leading class does
	// the work Go's \b cannot.
	//
	// An ASCII-only class did not merely miss "josé@example.fr". Go's \b is
	// ASCII, so it found a word boundary inside the accented run and matched the
	// tail: "andré.muller@example.fr" came out as "andré.mü[EMAIL_1]" — the
	// start of the address forwarded in clear, and the token bound to a fragment
	// that the response path would expand into the middle of a word. The leading
	// class replaces the boundary that cannot be trusted, and Group points past
	// it.
	//
	// The local part is {2,} rather than +, and the one character that buys is
	// the whole reason: an escape sequence leaves a single letter welded to the
	// address that follows it. In raw text, "…pourquoi.\n\n@RTK.md" reads as
	// "n@RTK.md" — the "n" taken out of the "\n", a lone backslash left before
	// the token, and the request refused for an invalid escape. Masking a JSON
	// body value by value is what fixes that properly (see
	// internal/proxy/jsonbody.go), but a flat-text body and the audit console
	// still meet the raw form, and a one-character local part is not worth
	// defending: the addresses that shape claims are overwhelmingly artefacts —
	// a path, a filename, a shell redirection — rather than mailboxes.
	emailRe = regexp.MustCompile(`(?:^|[^\p{L}\p{N}._%+\-@])([\p{L}\p{N}._%+\-]{2,}@[\p{L}\p{N}.\-]+\.[\p{L}]{2,})`)

	// Card numbers, in the groups of four they are printed in and therefore
	// pasted in. A digits-only expression missed "4532 0151 1283 0366"
	// entirely. Luhn is what keeps this looser shape from claiming arbitrary
	// runs of digits — an odometer reading, a counter.
	//
	// Luhn is only one digit of evidence: a tenth of arbitrary runs clear it,
	// measured. So the length has to be the other half, exactly as the per-country
	// table is for the IBAN. The Visa branch used to end on \d{1,4}, which admits
	// thirteen, fourteen, fifteen and sixteen digits — and no Visa card has ever had
	// fourteen or fifteen. Two lengths of pure false positive, each catching a tenth
	// of the numbers that reached them. The other three branches were already exact:
	// Mastercard and Discover sixteen, Amex fifteen.
	//
	// Each branch now carries exactly the lengths its network issues: Visa 13, 16
	// and 19, Amex 15, Mastercard and Discover 16. The nineteen-digit Visa was the
	// last gap and it was a *miss*, the worse direction — a real card forwarded in
	// clear rather than a reference masked for nothing.
	//
	// Discover is left at sixteen deliberately. ISO/IEC 7812 permits up to nineteen
	// and the network is widely said to use only sixteen; a length nobody could
	// confirm is a guess, and a guessed length here either misses real cards or
	// claims references, both silently.
	creditCardRe = regexp.MustCompile(`\b(?:` +
		`4\d{3}(?:[ \-]?\d{4}){2}[ \-]?(?:\d{4}(?:[ \-]?\d{3})?|\d)` + // Visa: 16, 19, or the older 13
		`|5[1-5]\d{2}(?:[ \-]?\d{4}){3}` + // Mastercard
		`|3[47]\d{2}[ \-]?\d{6}[ \-]?\d{5}` + // Amex
		`|6(?:011|5\d{2})(?:[ \-]?\d{4}){3}` + // Discover
		`)\b`)

	// Two letters, two check digits, then the body: either run together, or in
	// the conventional groups of four. Case-insensitive, because an IBAN pasted
	// in lowercase is still an IBAN.
	//
	// The two branches are the point, and writing them as one loose "optional
	// space then one to four characters, repeated" is the trap. Under (?i) that
	// class matches lowercase, so the repetition walked straight through the
	// sentence: "GB123456C in the l" was claimed as an IBAN — and its compacted
	// form is fifteen characters, which is a valid length, so roughly one such
	// span in ninety-seven also passes the mod-97 key and is masked as an
	// account. Requiring groups of exactly four kills that whole class of
	// match, because prose words are not four characters and a space apart.
	//
	// The shape is still broad enough to cover any reference code of the right
	// layout ("PO12ABCD3456"), so the key is what actually decides — and
	// trimToIBAN is what finds where the account ends when a currency or a BIC
	// is glued to it with no space to cut on.
	ibanRe = regexp.MustCompile(`(?i)\b[A-Z]{2}\d{2}(?:` +
		`[A-Z0-9]{11,30}` + // compact: the whole body, no separators
		`|(?:[ ][A-Z0-9]{4}){2,7}(?:[ ][A-Z0-9]{1,3})?` + // grouped by four, with a short last group
		`)\b`)

	// Twenty-four hex characters. Narrow enough to stand alone: an MD5 is 32, a
	// SHA-1 is 40, and a UUID carries dashes, so none of them can be mistaken
	// for one at these boundaries.
	mongoIDRe = regexp.MustCompile(`\b[0-9a-fA-F]{24}\b`)

	ipv4Octet = `(?:25[0-5]|2[0-4]\d|[01]?\d\d?)`
	ipv4Re    = regexp.MustCompile(`\b` + ipv4Octet + `\.` + ipv4Octet + `\.` + ipv4Octet + `\.` + ipv4Octet + `\b`)

	// IPv6, which went unmasked entirely while the category that holds it was
	// labelled "IP address" in every menu that offers to switch it off.
	//
	// Two branches, and what is left out of them is the decision. The full
	// eight-group form is unambiguous. The compressed form is not: "abc::def" is a
	// valid address by every rule of the notation, and it is also how C++ writes a
	// namespace — so the compressed branch requires two groups on one side of the
	// "::", which keeps "2001:db8::1" and drops the two-group shape entirely. What
	// that costs is the short well-known addresses: "::1" and "fe80::1" are not
	// read. Neither identifies a machine — every host has the same loopback, and a
	// link-local address names a network that failed rather than a host — which is
	// the same argument localAddresses makes when it refuses to report them.
	//
	// A MAC address cannot reach either branch: six groups is not eight, and it
	// carries no "::". Nor can a timestamp, for the same reason.
	//
	// IPAddressCheck is what actually decides. RE2 can describe the shape of an
	// address and not whether it is one, and the compression rule — where "::" may
	// appear and how many groups it stands for — is not something to re-implement
	// in an expression when the standard library has it.
	ipv6Group = `[0-9a-fA-F]{1,4}`
	ipv6Re    = regexp.MustCompile(
		`\b(?:` + ipv6Group + `:){7}` + ipv6Group + `\b` +
			`|\b(?:` + ipv6Group + `:){2,7}(?::` + ipv6Group + `){1,6}\b` +
			`|\b(?:` + ipv6Group + `:){1,6}(?::` + ipv6Group + `){2,7}\b`)

	// The one date order that is unambiguous everywhere: year first. Day-first and
	// month-first are the same string read two ways, so each lives in the locale
	// that reads it that way.
	//
	// One alternative per separator, because the separators have to agree and RE2
	// has no backreference: "2004-02/23" is not a date.
	dateRe = regexp.MustCompile(`\b(?:` +
		`(?:19|20)\d{2}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])` +
		`|(?:19|20)\d{2}/(?:0[1-9]|1[0-2])/(?:0[1-9]|[12]\d|3[01])` +
		`)\b`)
)

// InternationalPatterns returns the identifiers that are not tied to a locale.
func InternationalPatterns() []Pattern {
	return []Pattern{
		{Regex: emailRe, Group: 1, Category: CatEmail, Label: "Email address"},
		{Regex: creditCardRe, Category: CatCreditCard, Label: "Payment card number"},
		{Regex: ibanRe, Category: CatIBAN, Label: "IBAN", Refine: trimToIBAN},
		{Regex: mongoIDRe, Category: CatMongoID, Label: "MongoDB ObjectId"},
		{Regex: ipv4Re, Category: CatIPAddr, Label: "IPv4 address"},
		{Regex: ipv6Re, Category: CatIPv6, Label: "IPv6 address"},
		{Regex: dateRe, Category: CatDOB, Label: "Date (ISO)"},
	}
}
