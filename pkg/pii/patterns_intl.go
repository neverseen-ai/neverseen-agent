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
	// Mastercard has two BIN ranges and only 51-55 was read. The 2-series,
	// 222100-272099, has been issued since 2017, so a card of that range — an
	// ordinary card in an ordinary wallet — went to the model in clear. Same
	// sixteen-digit grouped shape, its own branch because the leading digit
	// differs; the range is spelled out rather than left as `2\d{3}` because a
	// loose leading class is four digits of nothing and Luhn alone clears a tenth
	// of what reaches it.
	//
	// Discover is left at sixteen deliberately. ISO/IEC 7812 permits up to nineteen
	// and the network is widely said to use only sixteen; a length nobody could
	// confirm is a guess, and a guessed length here either misses real cards or
	// claims references, both silently.
	creditCardRe = regexp.MustCompile(`\b(?:` +
		`4\d{3}(?:[ \-]?\d{4}){2}[ \-]?(?:\d{4}(?:[ \-]?\d{3})?|\d)` + // Visa: 16, 19, or the older 13
		`|5[1-5]\d{2}(?:[ \-]?\d{4}){3}` + // Mastercard, 51-55
		`|2(?:22[1-9]|2[3-9]\d|[3-6]\d\d|7[01]\d|720)(?:[ \-]?\d{4}){3}` + // Mastercard, 222100-272099
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
	// The geographic notations come first: a Maps link is a long span carrying
	// digits and an occasional address inside it, and the ordering rule is that
	// the first pattern to claim a literal wins. Behind the narrower shapes, the
	// link would be cut around whatever was found in its query string.
	return append(geoPointPatterns(), []Pattern{
		{Regex: emailRe, Group: 1, Category: CatEmail, Label: "Email address"},
		{Regex: creditCardRe, Category: CatCreditCard, Label: "Payment card number"},
		{Regex: ibanRe, Category: CatIBAN, Label: "IBAN", Refine: trimToIBAN},
		{Regex: mongoIDRe, Category: CatMongoID, Label: "MongoDB ObjectId"},
		{Regex: ipv4Re, Category: CatIPAddr, Label: "IPv4 address"},
		{Regex: ipv6Re, Category: CatIPv6, Label: "IPv6 address"},
		{Regex: dateRe, Category: CatDOB, Label: "Date (ISO)"},
	}...)
}

// --- Geographic points ------------------------------------------------------
//
// A place is personal data in the same way a postal address is, and it travels
// in notations an address pattern cannot see: a link somebody pasted out of the
// Share button, a pair of degrees out of a GPS unit, a Plus Code.
//
// **Only self-anchored notations are read.** Every expression below is anchored
// on a literal somebody wrote on purpose — "geo:", a Maps host, "POINT(", a
// degree sign, "///". The notation this deliberately leaves out is the most
// common one of all: a bare decimal pair, "48.8584, 2.2945". There is nothing in
// it but two floats in range, and a float pair in range is also a translate(), a
// vector, a couple of measurements. Reading it needs the key beside it
// ("lat"/"lng") rather than the value, which is the shape PHONE and POSTCODE
// already have and the reason both carry NoisyInCode. A geohash ("u09tvw0f6szy")
// is out for the same reason: base-32 with no anchor is an identifier.
//
// TODO: the bare decimal pair and the geohash are the known ceiling. The upgrade
// path is a key-anchored pattern (`"lat"\s*:\s*<float>`) plus NoisyInCode, paid
// for with its own precision floor in the corpus — not a widening of these.
//
// **GeoJSON is not here and cannot be.** `{"coordinates":[2.2945,48.8584]}` never
// reaches a pattern as text: a body is masked value by value and numbers carry
// nothing to mask (internal/proxy/jsonbody.go). The two floats arrive as two
// separate JSON numbers, so no expression over a single value can see a pair.
// Masking it would mean reading the *shape* of the document in jsonbody, which is
// a different feature from a catalogue entry.
var (
	// The bounds live in the shapes rather than in a CategoryInfo.Verify, which
	// would receive whole spans of six different notations and have to re-parse
	// each of them to check two numbers. Spelled out the way the Mastercard BIN
	// range is, and for the same reason: a loose `\d{1,3}` is three digits of
	// nothing, and "999.888" would be masked as a place.
	//
	// The alternatives are ordered widest-value-first. Go's regexp prefers the
	// leftmost alternative that lets the *rest* of the pattern match, so "180"
	// has to be offered before "1[0-7]\d" — behind it, "18" matches and the
	// trailing "0" is left for a comma that never comes.
	geoLat = `[+-]?(?:90(?:\.0+)?|[0-8]?\d(?:\.\d+)?)`
	geoLon = `[+-]?(?:180(?:\.0+)?|1[0-7]\d(?:\.\d+)?|\d{1,2}(?:\.\d+)?)`

	// What a URL may contain, closing on a character that is not sentence
	// punctuation. The final class is the rule noSentenceTail exists for: a
	// permissive body one character shorter, so the span stops before the full
	// stop that ends the sentence and before the ")" that closes a Markdown link.
	// A Maps URL is full of "," and "!" — they are interior characters here, only
	// the last one is constrained.
	geoURLTail = `[^\s<>"']*[^\s<>"'` + noSentenceTail + `]`

	// RFC 5870. WGS-84 unless ";crs=" says otherwise, an optional altitude, and
	// ";u=" for the uncertainty radius.
	//
	// **No leading \b, deliberately**, and it is worth 350x: measured over
	// docs/testCorpus.txt, `\bgeo:…` takes 301µs and `geo:…` takes 866ns. The
	// boundary is what stops LiteralPrefix returning "geo:", and without a literal
	// Go walks the whole text instead of scanning for four bytes. The rule is the
	// one the second-tier vendor prefixes are split for; what is new here is that
	// the documented parade — consume the preceding character and point Group at
	// the value — is *worse* than the disease at 676µs, because a leading
	// character class has no literal either.
	//
	// What the boundary bought, in exchange: "…ageo:48.8,2.2" now matches from
	// "geo:". The pattern is case-sensitive and no word ends in a lowercase "ageo",
	// so the shape is unreachable in practice. POINT below keeps its \b because
	// there the same trade is real — see the note on it.
	geoURIRe = regexp.MustCompile(`geo:` + geoLat + `,` + geoLon +
		`(?:,[+-]?\d+(?:\.\d+)?)?` + // altitude in metres
		`(?:;[a-zA-Z0-9\-]+=[a-zA-Z0-9.:_\-]+)*`)

	// The whole link, not the coordinates inside it.
	//
	// A Maps URL carries the place in clear twice over: "/place/Eiffel+Tower/"
	// and "&q=Eiffel+Tower" are the name of the destination, and a home address
	// is a street name before it is a pair of floats. Masking "@48.8584,2.2945"
	// and forwarding the rest masks nothing. CatConnStr takes a whole span for
	// the same reason — the password alone is not what identifies the database.
	//
	// The scheme is optional because people paste hosts, and lowercase-only
	// because that is how hosts are written; (?i) here would cost the literal
	// scan for every match in the body.
	//
	// TODO: Waze ("waze.com/ul?ll=") and Bing ("bing.com/maps?cp=") use the same
	// shape and are not read. They are one pattern each when somebody asks.
	geoGoogleMapsRe = regexp.MustCompile(`\b(?:https?://)?(?:www\.)?(?:` +
		`google\.[a-z]{2,3}(?:\.[a-z]{2})?/maps/` + // /maps/@, /maps/place/, /maps/dir/, /maps/search/
		`|google\.[a-z]{2,3}(?:\.[a-z]{2})?/maps\?` + // ?q=48.8584,2.2945
		`|maps\.google\.[a-z]{2,3}(?:\.[a-z]{2})?/` +
		`|maps\.app\.goo\.gl/` + // what the Share button actually produces
		`|goo\.gl/maps/` +
		`)` + geoURLTail)

	// maps.apple.com/?ll=48.8584,2.2945, and the unified "maps.apple/" short
	// host. The host is already maps-specific, so any path below it is a place.
	// "daddr=" and "saddr=" are the ones that matter most: a route names both
	// ends, which is usually home and work.
	geoAppleMapsRe = regexp.MustCompile(`\b(?:https?://)?maps\.apple(?:\.com)?/` + geoURLTail)

	// OpenStreetMap is narrowed where the other two are not: its host serves a
	// whole site, so "openstreetmap.org/copyright" is not a place. Only the two
	// forms that actually carry a position are read.
	geoOSMRe = regexp.MustCompile(`\b(?:https?://)?(?:www\.)?openstreetmap\.org/(?:` +
		`[^\s<>"']*#map=\d{1,2}/` + geoLat + `/` + geoLon +
		`|\?[^\s<>"']*mlat=` + geoLat + `[^\s<>"']*mlon=` + geoLon +
		`)`)

	// Well-known text, as PostGIS and the OGC write it. **Longitude first** — the
	// opposite of every other notation here, which is why the two bounds are not
	// interchangeable in this one expression.
	//
	// TODO: uppercase only. "POINT" is the canonical OGC spelling, and (?i) or a
	// "POINT|point" alternation would each cost the literal for a form nothing
	// emits.
	//
	// The leading \b stays, unlike the geo: URI above, and it costs 290µs over the
	// corpus. Without it any identifier ending in POINT takes the match:
	// "ENDPOINT(2.2945 48.8584)" comes out as "POINT(2.2945 48.8584)" — a token
	// bound to a fragment, with "END" left in clear before it, which is the failure
	// Go's ASCII \b already caused once for an accented email address.
	//
	// WKT's own MULTIPOINT is *not* the case that forces this: both of its
	// notations, "MULTIPOINT(2.2945 48.8584, 3.1 49.2)" and the parenthesised
	// "MULTIPOINT((2.2945 48.8584), …)", are already refused by the closing "\)"
	// this expression requires. Measured before the comment was written, because
	// the obvious collision and the real one were not the same one.
	geoWKTRe = regexp.MustCompile(`\bPOINT[ \t]*(?:ZM|Z|M)?[ \t]*\([ \t]*` +
		geoLon + `[ \t]+` + geoLat +
		`(?:[ \t]+[+-]?\d+(?:\.\d+)?)?` + // altitude, for POINT Z
		`[ \t]*\)`)

	// Degrees-minutes-seconds and degrees-decimal-minutes in one expression: DDM
	// is DMS with the seconds left out and the minutes carrying the fraction, so
	// the seconds group is simply optional.
	//
	// Horizontal whitespace only, never \s: with \s a span that is already this
	// permissive swallows the following line.
	//
	// The variants are the characters keyboards and word processors actually
	// produce — the masculine ordinal for the degree sign, the typographic quotes
	// for the prime and double prime. "O" is west in French.
	geoDegreeSign  = `[°º]`
	geoPrime       = `['\x{2032}\x{2019}]`
	geoDoublePrime = `["\x{2033}\x{201D}]`
	geoMinutes     = `[0-5]?\d(?:\.\d+)?`
	geoDMSRe       = regexp.MustCompile(
		`\b(?:90|[0-8]?\d)[ \t]*` + geoDegreeSign + `[ \t]*` + geoMinutes + `[ \t]*` + geoPrime +
			`(?:[ \t]*` + geoMinutes + `[ \t]*` + geoDoublePrime + `)?[ \t]*[NSns]` +
			`[,;]?[ \t]*` +
			`(?:180|1[0-7]\d|\d{1,2})[ \t]*` + geoDegreeSign + `[ \t]*` + geoMinutes + `[ \t]*` + geoPrime +
			`(?:[ \t]*` + geoMinutes + `[ \t]*` + geoDoublePrime + `)?[ \t]*[EWOewo]`)

	// Open Location Code. Base-20 over an alphabet chosen to avoid vowels, so
	// eight of those characters followed by "+" is not a coincidence any more
	// than a vendor prefix is.
	//
	// TODO: the short form ("V75V+8Q Paris") is not read. Four characters and a
	// "+" is a shape ordinary text satisfies, and the locality that disambiguates
	// it is a word list this catalogue does not have.
	geoPlusCodeRe = regexp.MustCompile(`\b[23456789CFGHJMPQRVWX]{8}\+[23456789CFGHJMPQRVWX]{2,7}\b`)

	// what3words. Anchored on "///" with **no space after it**, which is what
	// keeps it off a Rust or C# documentation comment: "/// the.value.returned"
	// is prose behind a separator, "///the.value.returned" is not a comment
	// anybody writes.
	//
	// TODO: ASCII words only. what3words issues the same square in forty-odd
	// languages and the accented ones are not read; widening the class needs its
	// own negatives, because three dot-separated accented words is a much commoner
	// shape than three ASCII ones.
	geoWhat3WordsRe = regexp.MustCompile(`///[a-z]{3,}\.[a-z]{3,}\.[a-z]{3,}\b`)
)

// geoPointPatterns returns the geographic-point notations, ordered specific
// before broad.
func geoPointPatterns() []Pattern {
	return []Pattern{
		{Regex: geoURIRe, Category: CatGeoPoint, Label: "geo: URI"},
		{Regex: geoGoogleMapsRe, Category: CatGeoPoint, Label: "Google Maps link"},
		{Regex: geoAppleMapsRe, Category: CatGeoPoint, Label: "Apple Maps link"},
		{Regex: geoOSMRe, Category: CatGeoPoint, Label: "OpenStreetMap link"},
		{Regex: geoWKTRe, Category: CatGeoPoint, Label: "WKT point"},
		{Regex: geoDMSRe, Category: CatGeoPoint, Label: "Degrees, minutes, seconds"},
		{Regex: geoPlusCodeRe, Category: CatGeoPoint, Label: "Plus Code"},
		{Regex: geoWhat3WordsRe, Category: CatGeoPoint, Label: "what3words address"},
	}
}
