package proxy

import (
	"bytes"
	"encoding/json"
)

// A request body is a JSON document, and it has to be treated as one.
//
// Masking the raw bytes as if they were text is the obvious implementation and
// it is wrong, in a way that only shows up against a real client. A JSON string
// carries escapes, and a pattern reading the raw bytes sees the characters those
// escapes are made of: in a body containing "…pourquoi.\n\n@RTK.md", the email
// pattern matched "n@RTK.md" — taking the "n" out of the "\n" before it — and
// replaced it with a token, leaving a lone backslash in front of a bracket. The
// provider answered "the request body is not valid JSON: invalid escaped
// character", and every request failed.
//
// So the document is decoded, every string *value* is masked as the text it
// actually is, and the document is re-encoded. Escapes are then the encoder's
// problem, which is where they belong. It also means a pattern never sees a
// backslash that was punctuation rather than data.
//
// Keys are left alone. They are the field names of an API, not somebody's data,
// and masking one would break the request in a way no provider could interpret.
//
// TODO: a value sent as a JSON number — a card number written as
// {"card": 4532015112830366} — is not masked, because replacing it would have to
// change its type. Clients send these as strings in practice; if one does not,
// the honest fix is to refuse the request rather than to quietly rewrite the
// schema.

// mapJSONStrings decodes raw as a JSON document, applies f to every string value
// in it, and re-encodes it.
//
// Reports false when raw is not JSON, so the caller can fall back to treating it
// as text.
func mapJSONStrings(raw []byte, f func(string) string) ([]byte, bool) {
	doc, err := decodeJSONBody(raw)
	if err != nil {
		return nil, false
	}

	encoded, err := encodeJSONBody(mapStrings(doc, f))
	if err != nil {
		return nil, false
	}
	return encoded, true
}

// mapStrings walks a decoded document and applies f to every string it holds.
func mapStrings(v any, f func(string) string) any {
	switch t := v.(type) {
	case string:
		return f(t)
	case []any:
		for i, item := range t {
			t[i] = mapStrings(item, f)
		}
		return t
	case map[string]any:
		for key, item := range t {
			t[key] = mapStrings(item, f)
		}
		return t
	default:
		// Numbers, booleans and null carry nothing to mask, and json.Number
		// keeps the numbers exactly as they were written.
		return v
	}
}

// decodeJSONBody parses a document, keeping numbers exactly as written.
//
// Without UseNumber every integer round-trips through a float, so a token count
// of 1000000 goes back out as "1e+06". The agent rewrites text; it must not
// rewrite arithmetic.
func decodeJSONBody(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	// A document with trailing bytes is not one document. Accepting it would let
	// whatever follows through unmasked.
	if dec.More() {
		return nil, errTrailingJSON
	}
	return doc, nil
}

// encodeJSONBody re-encodes a document without HTML escaping.
//
// Go's encoder rewrites "<", ">" and "&" as < and friends by default, which
// is safe but changes bytes the agent has no business changing — a prompt full
// of HTML would come back to the caller looking mangled.
func encodeJSONBody(doc any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	// Encode appends a newline; the body it replaces did not have one.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

var errTrailingJSON = errTrailing("the body carries more than one JSON document")

type errTrailing string

func (e errTrailing) Error() string { return string(e) }
