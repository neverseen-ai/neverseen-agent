package pii

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Whether the name a value was assigned to says what the value is.
//
// The other half of NoisyInCode. Inside source, a date and a run of digit pairs are
// a changelog and a byte array far more often than a birth date and a telephone
// number, so those categories are not reported there. A real customer record in a
// test fixture was the case that relaxation had never been measured against, and it
// sent the record's telephone and date of birth out in clear beside its masked email
// and address. What tells the two apart is the name: `BirthDate: "14/03/1987"` and
// `"phone": "06 12 34 56 78"` say what they hold, `Date: "2024-03-14"` and
// `[]byte{01, 02, 03, 04, 05}` do not.
//
// Only the name the value is assigned to — the identifier right before it — and not
// any word in the window: in `{"phone": "x", "version": "01 02 03 04 05"}` the word
// is there and names another field.
//
// This is not a placement rule and does not promote a match: it only decides that the
// code relaxation does not apply to a value whose name says what it is. Outside code it
// is never asked.
//
// TODO: a value with no name of its own is still relaxed — a positional argument, a
// tuple, an SQL INSERT's VALUES list, a CSV row whose header is further than
// WindowSize away. The upgrade is reading the column or the parameter a value fills,
// which is the plate rule's CSV ceiling too.

// keyWords are the names that say a value is what its category claims, per category
// that NoisyInCode relaxes. A word is matched whole against one word of the name —
// split on punctuation and on camel case, so `dateOfBirth`, `birth_date` and
// `BirthDate` all read "birth" — and an entry ending in "*" is a stem that may run
// on: "birth*" covers "birthdate" and "birthday", "phone*" covers "phonenumber".
//
// Whole otherwise, because a longer word is a different word: "tel" opens
// "telemetry", "zip" opens "zipfile", "cell" opens "cellar" and "cellspacing". "date"
// is deliberately absent: a release date is a date, and it is the changelog this
// relaxation exists for.
var keyWords = map[Category][]string{
	CatDOB: {"birth*", "dob", "born", "naissance", "naiss", "ddn", "geburt*", "nacimiento", "nascita"},
	CatPhone: {"phone*", "telephone*", "téléphone*", "tel", "tél", "telefon*", "teléfono", "mobile",
		"mobil", "portable", "cellphone", "gsm", "fax", "msisdn"},
	CatPostalCode: {"postcode*", "postal", "zip", "zipcode", "cp", "codepostal", "plz", "postleitzahl"},
}

// NamedByItsKey reports whether the identifier a value is assigned to names the
// value's category.
func NamedByItsKey(cat Category, p Placement) bool {
	words, ok := keyWords[cat]
	if !ok {
		return false
	}
	for _, part := range nameParts(assignedName(p.Before)) {
		for _, w := range words {
			if stem, ok := strings.CutSuffix(w, "*"); ok {
				if strings.HasPrefix(part, stem) {
					return true
				}
			} else if part == w {
				return true
			}
		}
	}
	return false
}

// assignedName is the identifier right before a value: what is left once the quote,
// the separator and the space between the two are stepped back over — `"phone": "`,
// `BirthDate:   "`, `tel='`, `<telephone>`, `zip_code: `.
func assignedName(before string) string {
	end := len(strings.TrimRightFunc(before, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(`"'`+"`"+`:=>`, r)
	}))
	start := strings.LastIndexFunc(before[:end], func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '.'
	})
	if start < 0 {
		return before[:end]
	}
	// Stepped over by its width: the character before a name can be « or a
	// non-breaking space, and one byte past its start is the middle of it.
	_, size := utf8.DecodeRuneInString(before[start:])
	return before[start+size : end]
}

// nameParts splits an identifier into lowercase words, on punctuation and on a
// lowercase letter followed by an uppercase one.
func nameParts(name string) []string {
	var parts []string
	var word strings.Builder
	var prev rune
	flush := func() {
		if word.Len() > 0 {
			parts = append(parts, word.String())
			word.Reset()
		}
	}
	for _, r := range name {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && unicode.IsLower(prev):
			flush()
			word.WriteRune(unicode.ToLower(r))
		default:
			word.WriteRune(unicode.ToLower(r))
		}
		prev = r
	}
	flush()
	return parts
}
