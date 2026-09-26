package pii

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Licence plates are found by one pass over the text and a set of signatures,
// not by a regular expression per country.
//
// A plate has no leading literal for RE2 to scan for — it opens on a letter or a
// digit class, so every position in the text is a candidate — and each country's
// expression cost 5–10ms over 88KB. Measured over the same text with the 48
// shapes Europe issues: 179ms as one expression per shape, 10.7ms as a single
// alternation, 1.0ms as this pass, which costs the same whatever the number of
// signatures. Thirty countries were affordable only this way.
//
// A signature is how a plate is written, character by character: L a letter, D a
// digit, and the separator itself — "LL-DDD-LL" is the French plate with its
// dashes, "LLDDDLL" the same plate run together. Two countries that write plates
// the same way share a signature, and that is not a conflict: each one's pattern
// claims the span, the value is masked once, and overlap arbitration hands it to
// the country loaded first.
//
// What a signature cannot say — which letters a country issues, a province list,
// an age that moves with the calendar — is the pattern's Verify, as for any other
// pattern.

// plateMaxChunk is the longest run of letters and digits a plate is made of, so
// the pass never considers a longer word as part of one.
const plateMaxChunk = 8

// plateFinder returns a Pattern.Find reading the given signatures.
//
// Each case whole, never mixed: "AB-123-CD" and "ab-123-cd" are both how people
// type a plate, "Ab-123-cD" is not. upperOnly refuses the lowercase form too, for
// a notation whose shape is too common in lowercase to carry any evidence.
//
// TODO: one pass per pattern, so the cost grows by about 1ms per 88KB with every
// plate notation a deployment loads. The upgrade is one pass shared by every loaded
// country, handing each span to the patterns whose signatures it matches.
func plateFinder(upperOnly bool, signatures ...string) func(string) [][]int {
	set := make(map[string]bool, len(signatures))
	maxLen, maxChunks := 0, 0
	for _, s := range signatures {
		set[s] = true
		maxLen = max(maxLen, len(s))
		chunks := 1
		for i := 0; i < len(s); i++ {
			if s[i] == ' ' || s[i] == '-' {
				chunks++
			}
		}
		maxChunks = max(maxChunks, chunks)
	}

	return func(text string) [][]int {
		var out [][]int
		chunks := plateChunks(text)
		sig := make([]byte, 0, maxLen)
		for i, first := range chunks {
			if !first.ok {
				continue
			}
			for k := i; k < len(chunks) && k < i+maxChunks; k++ {
				if !chunks[k].ok {
					break
				}
				if k > i {
					prev := chunks[k-1]
					if chunks[k].start-prev.end != 1 || (text[prev.end] != ' ' && text[prev.end] != '-') {
						break
					}
				}
				start, end := first.start, chunks[k].end
				if end-start > maxLen {
					break
				}
				sig = plateSignature(sig[:0], text[start:end])
				if !set[string(sig)] {
					continue
				}
				if plateGlued(text, chunks, i, k) {
					continue
				}
				if upper, lower := plateCase(text[start:end]); upper || (lower && !upperOnly) {
					out = append(out, []int{start, end})
				}
			}
		}
		return out
	}
}

// plateSeparators strips the separators a plate is written with, for a rule that
// reads the characters alone. One value, not one per call: the rules run for
// every candidate span on the scan.
var plateSeparators = strings.NewReplacer("-", "", " ", "")

// anyPlateFinder returns the spans every given finder returns, for a country
// that reads some notations on their own and others only behind a word.
func anyPlateFinder(finders ...func(string) [][]int) func(string) [][]int {
	return func(text string) [][]int {
		var out [][]int
		for _, f := range finders {
			out = append(out, f(text)...)
		}
		return out
	}
}

// namedPlateFinder is plateFinder for a shape too common to be read on its own:
// it keeps a span only when the word right in front of it names a vehicle.
//
// "DIN EN 1090" is a German plate from Dinslaken, "ISO 9001" a Greek one,
// "BE 2024" a Swiss one. Nothing in the value separates them, and no letter rule
// could: the word in front is the only evidence there is. Every case is read,
// since the word is what vouches for it.
//
// Right in front, not anywhere in the window: with "Fahrzeug HH XY 99E" the word
// vouches for the plate, not for "XY 99E" inside it, and with "Kfz DIN EN 1090"
// not for "EN 1090" either — a span that began mid-token would be masked where
// the whole was refused. Punctuation may stand between: "Kennzeichen: M-AB 1234".
func namedPlateFinder(signatures ...string) func(string) [][]int {
	find := plateFinder(false, signatures...)
	return func(text string) [][]int {
		spans := find(text)
		kept := spans[:0]
		for _, s := range spans {
			if namesAVehicle(lastWord(text[max(s[0]-WindowSize, 0):s[0]])) {
				kept = append(kept, s)
			}
		}
		return kept
	}
}

// lastWord returns the last run of letters and digits in text, with anything
// after it that is neither — so a word followed by a colon or a quote is still the
// word in front of what comes next.
func lastWord(text string) string {
	end := len(text)
	for end > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:end])
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			break
		}
		end -= size
	}
	start := end
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:start])
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			break
		}
		start -= size
	}
	return text[start:end]
}

// expandSignatures turns a compact notation into the signatures it stands for,
// so a country with dozens of them is written as the rule it follows: "L{1,3}"
// is one to three letters, "D{2,4}" two to four digits, "(-| )" a choice, and an
// empty alternative makes one optional — "(-| |)".
//
// Only L, D, the separators and those two constructs may appear: a signature
// reads every letter as L, so a literal "E" in a notation expands to a signature
// no text ever has, and a suffix like the German E is an L that a Verify
// narrows. A malformed notation panics: every call is a package-level
// declaration, so the panic is at start-up and in every test.
func expandSignatures(templates ...string) []string {
	var out []string
	for _, t := range templates {
		out = append(out, expandSignature(t)...)
	}
	return out
}

func expandSignature(t string) []string {
	var steps [][]string
	for i := 0; i < len(t); i++ {
		switch c := t[i]; {
		case c == '(':
			end := strings.IndexByte(t[i:], ')')
			if end < 0 {
				panic("pii: unclosed group in plate signature " + strconv.Quote(t))
			}
			steps = append(steps, strings.Split(t[i+1:i+end], "|"))
			i += end
		case (c == 'L' || c == 'D') && i+1 < len(t) && t[i+1] == '{':
			end := strings.IndexByte(t[i:], '}')
			lo, hi, ok := strings.Cut(t[i+2:i+end], ",")
			a, errA := strconv.Atoi(lo)
			b, errB := strconv.Atoi(hi)
			if end < 0 || !ok || errA != nil || errB != nil || a < 1 || b < a {
				panic("pii: bad repetition in plate signature " + strconv.Quote(t))
			}
			var alts []string
			for n := a; n <= b; n++ {
				alts = append(alts, strings.Repeat(string(c), n))
			}
			steps = append(steps, alts)
			i += end
		case c == 'L' || c == 'D' || c == '-' || c == ' ':
			steps = append(steps, []string{string(c)})
		default:
			panic("pii: plate signature " + strconv.Quote(t) + " holds " + strconv.QuoteRune(rune(c)) +
				", which no signature can: a letter is L, a digit D")
		}
	}

	out := []string{""}
	for _, alts := range steps {
		next := make([]string, 0, len(out)*len(alts))
		for _, prefix := range out {
			for _, a := range alts {
				next = append(next, prefix+a)
			}
		}
		out = next
	}
	return out
}

// plateRuns splits a plate into its runs of letters and of digits, uppercased,
// with the separators dropped: "m-ab 1234e" is M, AB, 1234, E. It is what a
// country's rule reads a district, a county or a number out of.
func plateRuns(value string) []string {
	var runs []string
	var cur []byte
	kind := byte(0)
	flush := func() {
		if len(cur) > 0 {
			runs = append(runs, string(cur))
			cur = cur[:0]
		}
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		var k byte
		switch {
		case c >= '0' && c <= '9':
			k = 'D'
		case c >= 'a' && c <= 'z':
			k, c = 'L', c-'a'+'A'
		case c >= 'A' && c <= 'Z':
			k = 'L'
		default:
			flush()
			kind = 0
			continue
		}
		if k != kind {
			flush()
			kind = k
		}
		cur = append(cur, c)
	}
	flush()
	return runs
}

// plateHasNone reports whether a plate carries none of the given letters, in
// either case.
func plateHasNone(value, letters string) bool {
	return !strings.ContainsAny(strings.ToUpper(value), letters)
}

// plateHasOnly reports whether every letter a plate carries is one of those given.
func plateHasOnly(value, letters string) bool {
	for i := 0; i < len(value); i++ {
		if c := value[i] &^ 0x20; c >= 'A' && c <= 'Z' && strings.IndexByte(letters, c) < 0 {
			return false
		}
	}
	return true
}

// plateNoLeadingZero reports whether no run of digits in a plate opens on 0,
// which most series never issue.
func plateNoLeadingZero(value string) bool {
	for _, r := range plateRuns(value) {
		if r[0] == '0' {
			return false
		}
	}
	return true
}

// plateNoZeroNumber reports whether no run of digits in a plate is all zeros —
// the number no series issues, and the one every stand-in in the commonest
// shapes carries, so that each country reading that shape refuses them all.
func plateNoZeroNumber(value string) bool {
	for _, r := range plateRuns(value) {
		if r[0] >= '0' && r[0] <= '9' && strings.Trim(r, "0") == "" {
			return false
		}
	}
	return true
}

// plateCodes is a closed list of districts, counties or provinces, from the
// codes written out one after another.
func plateCodes(fields string) map[string]bool {
	out := make(map[string]bool)
	for _, code := range strings.Fields(fields) {
		out[code] = true
	}
	return out
}

// plateGlued reports whether chunks[i..k] is joined by a dash to another run of
// letters and digits on either side, which makes it a fragment of a longer
// hyphenated token rather than a plate.
//
// A MAC address is the case: "BD-12-FD-34-BF-56" carries "BD-12-FD", a Dutch
// plate's shape in letters the Dutch series issues, and masking it would leave
// the rest of the address in clear beside a stand-in. A space is not the same:
// prose follows a spaced plate as a matter of course.
func plateGlued(text string, chunks []plateChunk, i, k int) bool {
	start, end := chunks[i].start, chunks[k].end
	before := i > 0 && chunks[i-1].end == start-1 && text[start-1] == '-'
	after := k+1 < len(chunks) && chunks[k+1].start == end+1 && text[end] == '-'
	return before || after
}

// plateChunk is one maximal run of letters and digits. ok is false for a run a
// plate cannot be made of: longer than plateMaxChunk, or carrying a character
// outside ASCII.
type plateChunk struct {
	start, end int
	ok         bool
}

// plateChunks splits text into maximal runs of letters and digits.
//
// Maximal in Unicode terms, which is what makes the boundary right. "éAB-123-CD"
// is one run from the é, so no plate starts at the A — Go's \b is ASCII and saw a
// boundary there. An underscore ends a run, so "plaque_AB-123-CD" still finds the
// plate, which \b did not.
func plateChunks(text string) []plateChunk {
	var out []plateChunk
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			i += size
			continue
		}
		start, ascii := i, true
		for i < len(text) {
			r, size = utf8.DecodeRuneInString(text[i:])
			if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
				break
			}
			if r >= utf8.RuneSelf {
				ascii = false
			}
			i += size
		}
		out = append(out, plateChunk{start: start, end: i, ok: ascii && i-start <= plateMaxChunk})
	}
	return out
}

// plateSignature appends the signature of an ASCII span: L, D, or the byte.
func plateSignature(sig []byte, span string) []byte {
	for i := 0; i < len(span); i++ {
		switch c := span[i]; {
		case c >= '0' && c <= '9':
			sig = append(sig, 'D')
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
			sig = append(sig, 'L')
		default:
			sig = append(sig, c)
		}
	}
	return sig
}

// plateCase reports whether every letter in span is uppercase, or every one
// lowercase. A span with no letter is neither.
func plateCase(span string) (upper, lower bool) {
	upper, lower = true, true
	letters := false
	for i := 0; i < len(span); i++ {
		switch c := span[i]; {
		case c >= 'A' && c <= 'Z':
			lower, letters = false, true
		case c >= 'a' && c <= 'z':
			upper, letters = false, true
		}
	}
	return upper && letters, lower && letters
}
