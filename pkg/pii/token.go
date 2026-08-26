package pii

import (
	"fmt"
	"regexp"
)

// A token is what a masked value is called on the wire: "[EMAIL_1]".
//
// The bracket form is deliberate. It is unmistakably not data, so a reader of a
// log or a prompt cannot confuse one with a real value; and nothing in the
// catalogue matches it, so a second pass over an already-masked body finds
// nothing to mask again.
//
// The number is per category and per session, and it is what makes the mapping
// reversible: the same value keeps the same token for as long as the session
// lives, so a model asked about "[EMAIL_1]" twice is talking about one person.

// tokenRe matches exactly one whole token.
var tokenRe = regexp.MustCompile(`^\[[A-Z][A-Z0-9_]*_\d+\]$`)

// tokenScanRe finds tokens inside a larger text.
var tokenScanRe = regexp.MustCompile(`\[[A-Z][A-Z0-9_]*_\d+\]`)

// Token renders the name of the index-th value of a category.
func Token(cat Category, index int64) string {
	return fmt.Sprintf("[%s_%d]", Prefix(cat), index)
}

// IsToken reports whether s is exactly one token.
//
// The response path expands what a session minted, whatever shape it took, so
// this is no longer the only door in: a stand-in is expanded by matching its own
// text. What it still decides is which mapping entries take the token path —
// which is every credential, because a secret category never gets a stand-in
// (see Detector.render). Loosening it to match something that is not a token
// would put a value on that path with no shape to check it by.
func IsToken(s string) bool { return tokenRe.MatchString(s) }

// TokenAt returns the token text begins with, or empty when it does not begin
// with one.
//
// Anchored, for the callers that walk a text position by position rather than
// replacing matches: the response path, which expands, and the audit console,
// which marks. Both need "is there a token exactly here", and two spellings of
// that question would be two answers about what may be expanded.
func TokenAt(text string) string {
	if len(text) == 0 || text[0] != '[' {
		return ""
	}
	for i := 1; i < len(text) && i <= maxTokenLen; i++ {
		if text[i] != ']' {
			continue
		}
		if candidate := text[:i+1]; IsToken(candidate) {
			return candidate
		}
		return "" // the first bracket closes here; nothing longer can be a token
	}
	return ""
}

// FindTokens returns every token in text, in order, with duplicates.
func FindTokens(text string) []string { return tokenScanRe.FindAllString(text, -1) }

// ReplaceTokens rewrites every token in text that expand knows, and leaves the
// rest alone.
//
// Leaving an unknown token in place is the deliberate behaviour, not a gap. A
// token this process did not mint — from a session that has expired, or from a
// restart under a new key — means nothing here, and inventing something for it
// would put a value in front of the caller that no data supports.
func ReplaceTokens(text string, expand func(token string) (string, bool)) string {
	if !tokenScanRe.MatchString(text) {
		return text
	}
	return tokenScanRe.ReplaceAllStringFunc(text, func(token string) string {
		if original, ok := expand(token); ok {
			return original
		}
		return token
	})
}

// TokenTailLen returns the length of the trailing run of text that could be the
// beginning of a token but is not yet a whole one — "…[EMA" gives 4.
//
// Streaming needs it. Generated text arrives in pieces of a few characters, so a
// token the model echoed is regularly split across two of them, and a rehydrator
// that only ever sees one piece at a time would forward "[EMA" and then "IL_1]"
// to the caller as text. Holding back exactly this much until the next piece
// arrives is what reassembles it.
func TokenTailLen(text string) int {
	// Walk back over the characters a token is made of. Anything else cannot be
	// part of one, and an unopened bracket cannot start one.
	for i := len(text) - 1; i >= 0 && len(text)-i <= maxTokenLen; i-- {
		switch c := text[i]; {
		case c == '[':
			return len(text) - i
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
			// still possibly inside a token
		default:
			return 0
		}
	}
	return 0
}

// maxTokenLen bounds how far back TokenTailLen looks. A token is a prefix, an
// underscore, an index and two brackets; the longest prefix in the catalogue is
// well under thirty characters, and the index will not reach ten digits in a
// session that lives half an hour.
const maxTokenLen = 48
