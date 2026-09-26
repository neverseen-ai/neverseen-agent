package pii

import (
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
				if upper, lower := plateCase(text[start:end]); upper || (lower && !upperOnly) {
					out = append(out, []int{start, end})
				}
			}
		}
		return out
	}
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
