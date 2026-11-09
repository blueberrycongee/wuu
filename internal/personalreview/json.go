package personalreview

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
)

// Reject duplicate keys before hashing: reviewers and executors must not see
// different values through different JSON parsers. Preserve numeric precision.
func canonicalArguments(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) > 64*1024 {
		return nil, errors.New("complete action exceeds review budget")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := uniqueValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, ok := v.(map[string]any); !ok {
		return nil, errors.New("action arguments must be an object")
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, errors.New("trailing action data")
	}
	return json.Marshal(v)
}
func uniqueValue(d *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("action nesting exceeds limit")
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('{'):
		object := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("invalid object key")
			}
			if _, exists := object[name]; exists {
				return nil, errors.New("duplicate action key")
			}
			value, err := uniqueValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		_, err := d.Token()
		return object, err
	case json.Delim('['):
		array := []any{}
		for d.More() {
			value, err := uniqueValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err := d.Token()
		return array, err
	default:
		return t, nil
	}
}
func digest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ArgumentsDigest binds host consent to the same validated canonical parameters
// that the executor will receive. Invalid or ambiguous JSON has no digest.
func ArgumentsDigest(raw json.RawMessage) string {
	canonical, err := canonicalArguments(raw)
	if err != nil {
		return ""
	}
	return digest(canonical)
}
