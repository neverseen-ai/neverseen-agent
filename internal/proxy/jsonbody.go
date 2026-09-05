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
	encoded, err := encodeMasked(doc, err, f)
	return encoded, err == nil
}

// encodeMasked is the second half of mapJSONStrings for a caller that has already
// decoded the body, and reads the decode error so that caller keeps one branch.
func encodeMasked(doc any, decodeErr error, f func(string) string) ([]byte, error) {
	if decodeErr != nil {
		return nil, decodeErr
	}
	return encodeJSONBody(mapStrings(doc, f))
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
	case jsonObject:
		for i, m := range t {
			t[i].value = mapStrings(m.value, f)
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

	doc, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	// A document with trailing bytes is not one document. Accepting it would let
	// whatever follows through unmasked.
	if dec.More() {
		return nil, errTrailingJSON
	}
	return doc, nil
}

// decodeValue reads one value, reading objects a token at a time so their keys
// keep the order they arrived in.
//
// Decoding into a map[string]any is what the standard decoder does and it loses
// that order, because a Go map has none: the body forwarded to the provider then
// carried its fields in Go's sorted marshal order instead of the caller's. It is
// the same document to a parser and nothing depended on it — but the audit console
// prints the two halves of an exchange to be read against each other, and two
// bodies whose fields are in different orders cannot be. The agent rewrites values;
// it has no business rewriting the shape around them.
//
// It also stops a duplicated key being collapsed. Two members of the same name are
// pathological rather than useful, but silently keeping one of them is the agent
// deciding which — and whichever it dropped would have gone to the provider in the
// original.
func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}

	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		// A string, a json.Number, a bool or nil — already the value itself.
		return tok, nil
	}

	switch delim {
	case '{':
		obj := jsonObject{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				// Unreachable through a decoder that validates as it reads, but
				// silently treating a non-string key as one would be inventing a
				// field name.
				return nil, errNotAnObjectKey
			}
			value, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			obj = append(obj, jsonMember{key: name, value: value})
		}
		_, err := dec.Token() // the closing brace
		return obj, err

	case '[':
		arr := []any{}
		for dec.More() {
			item, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, item)
		}
		_, err := dec.Token() // the closing bracket
		return arr, err

	default:
		return nil, errTrailing("a document opening with " + delim.String())
	}
}

// jsonObject is a JSON object holding its members in the order they were read.
type jsonObject []jsonMember

type jsonMember struct {
	key   string
	value any
}

// MarshalJSON writes the members in that order.
//
// Each one goes through an encoder with HTML escaping off, for the reason
// encodeJSONBody has it off: the agent must not rewrite a "<" a caller wrote. A
// nested object reaches its own MarshalJSON from here and is compacted without
// being re-escaped, because the setting travels with the encoder doing the
// compacting rather than with this method.
func (o jsonObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := writeJSONValue(&buf, m.key); err != nil {
			return nil, err
		}
		buf.WriteByte(':')
		if err := writeJSONValue(&buf, m.value); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// writeJSONValue appends one value, without the newline Encode adds — which would
// be whitespace in the middle of a document.
func writeJSONValue(buf *bytes.Buffer, v any) error {
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	buf.Truncate(buf.Len() - 1)
	return nil
}

// value returns what a key holds, and whether the object has it at all.
//
// The last member of that name wins, which is what every JSON parser a provider
// might use would do with a duplicate. The members are all kept on the wire; this
// is only about which one a reader here acts on.
func (o jsonObject) value(key string) (any, bool) {
	for i := len(o) - 1; i >= 0; i-- {
		if o[i].key == key {
			return o[i].value, true
		}
	}
	return nil, false
}

// setValue replaces what an existing key holds.
//
// It replaces and never appends, and that is not a limitation to lift casually:
// appending can reallocate the slice, and the object whose member holds this one
// would go on referring to the old backing array — the write would be made and
// then thrown away. Every caller is rewriting a value it has just read through
// value, so a key that is not there cannot happen; if that ever changes, the
// caller has to be the one that notices, because a setter reporting "no" into a
// closure nobody can read from is a write silently lost.
func (o jsonObject) setValue(key string, v any) {
	for i := len(o) - 1; i >= 0; i-- {
		if o[i].key == key {
			o[i].value = v
			return
		}
	}
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

var errNotAnObjectKey = errTrailing("an object key is not a string")

type errTrailing string

func (e errTrailing) Error() string { return string(e) }
