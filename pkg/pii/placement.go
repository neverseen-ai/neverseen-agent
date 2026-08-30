package pii

import "strings"

// Where a value sits, for the rules that cannot be settled by the value alone.
//
// This is the plan's stage 3 and the TODO GenericSecretCheck has carried since it
// was written: "which Verify cannot see: it is handed the group and not its
// surroundings". Cloud DLP calls the same idea a proximity window; Presidio calls
// using it to *invalidate* a finding anti-context, and has no built-in answer for
// it either.
//
// It is deliberately a second mechanism beside Verify rather than a wider Verify,
// because the two answer different questions from different evidence. Verify asks
// whether the value is well-formed — a checksum, a range, a case rule — and needs
// nothing but the value. A placement rule asks whether the text around it says the
// value is something else entirely, and is useless without it. Folding them
// together would mean every checksum in this file growing an argument it ignores.
//
// Three rules only, each with a measurement behind it. The temptation is a general
// "this looks like code" test here, and that belongs one level up: a rule that
// fires on punctuation would drop a person's telephone number out of a YAML file
// as readily as a CSS length.
//
// **A placement rule may only ever drop a match.** It cannot promote one, and it
// is never asked about a credential — being the right-hand side of an assignment
// is what makes a secret a secret, and the same shape is what makes an ordinary
// number a constant.

// WindowSize is how much text either side of a value a placement rule reads.
//
// Thirty-two bytes: enough to carry the name a value was assigned to and the
// punctuation around it, short enough that the rule cannot quietly become a scan
// of the whole document.
const WindowSize = 32

// Placement is the text on each side of a value, clipped to WindowSize.
type Placement struct {
	Before string
	After  string
}

// PlacementAt returns the window around text[start:end].
func PlacementAt(text string, start, end int) Placement {
	from := max(start-WindowSize, 0)
	to := min(end+WindowSize, len(text))
	return Placement{Before: text[from:start], After: text[end:to]}
}

// RejectedByPlacement reports whether where a value sits says it is not what its
// pattern claimed.
//
// Credentials are never asked. A secret is recognised *by* its placement — a name
// and an assignment — so a rule that read the same evidence in the other direction
// would be the one that stops masking keys in configuration files.
func RejectedByPlacement(cat Category, value string, p Placement) bool {
	if IsSecret(cat) {
		return false
	}

	switch cat {
	case CatIPAddr, CatIPv6:
		return isNetworkPrefix(p) || isObjectIdentifier(p)
	case CatPhone:
		return isPropertyValue(value, p)
	}
	return false
}

// isNetworkPrefix reports whether the value is followed by a mask length, which
// makes it a network rather than a host.
//
// "10.0.0.0/8" in a trusted-network list names a range, and a range is a routing
// decision rather than somebody's machine. Three of the eleven findings left in
// the hand-written samples after the reserved-range rule were exactly this, all in
// one allow list.
//
// It reads the mask rather than the address, so it holds for a private range and a
// public one alike: an operator pasting "203.0.113.0/24" is describing topology
// either way.
func isNetworkPrefix(p Placement) bool {
	rest, ok := strings.CutPrefix(p.After, "/")
	if !ok || rest == "" {
		return false
	}
	// A mask length and nothing else. "10.0.0.1/path" is a URL, not a prefix.
	for i := 0; i < len(rest) && i < 3; i++ {
		if rest[i] < '0' || rest[i] > '9' {
			return i > 0
		}
	}
	return true
}

// isObjectIdentifier reports whether an ASN.1 OID was named as one.
//
// "1.3.101.112" is the Ed25519 signature suite and a syntactically perfect IPv4
// address, and nothing inside the value separates them. What does is that code
// says so: `oidEd25519 = "1.3.101.112"`, `case OID_X25519:`, a comment naming the
// arc. Over four megabytes of third-party TypeScript, object identifiers were the
// single largest remaining class after the reserved ranges were dropped.
//
// The word is required, not guessed at. An address in a log line has no "oid" near
// it and stays masked.
func isObjectIdentifier(p Placement) bool {
	return strings.Contains(strings.ToLower(p.Before), "oid")
}

// isPropertyValue reports whether a separator-free number is the right-hand side
// of a code assignment rather than a telephone number.
//
// "z-index:2147483647" is a CSS length, and the NANP rules cannot say otherwise:
// area 214 and exchange 748 are both allocatable, so the shape is satisfied. What
// separates them is that nobody writes a telephone number glued to a colon with no
// space — "call me at 5552345678" has one, "z-index:2147483647" does not.
//
// Restricted to the separator-free notation, which is the weakest of the four and
// the only one an integer can satisfy. "(555) 234-5678" and "+1 555 234 5678"
// cannot be a constant whatever precedes them.
//
// It cannot misfire on the request path proper: a JSON body is masked value by
// value, so a number in `{"phone":"5552345678"}` arrives as its own string with
// the colon left in the document structure and Before empty. This only reaches
// flat text — source code, a stylesheet inside a string, a pasted file.
func isPropertyValue(value string, p Placement) bool {
	if strings.ContainsAny(value, " .-()+") {
		return false
	}
	before := strings.TrimRight(p.Before, "\"'")
	if before == "" {
		return false
	}
	switch before[len(before)-1] {
	case ':', '=':
		return true
	}
	return false
}
