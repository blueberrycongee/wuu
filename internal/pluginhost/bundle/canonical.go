// Package bundle defines the cross-runtime bundle contract: the v2 manifest
// shape and the byte-stable canonical form used to derive a generation string
// that Go and TypeScript compute identically.
package bundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ContractVersion is the canonical/generation protocol version. It changes only
// when the canonical form itself changes, not when the manifest schema changes.
const ContractVersion = 1

var integerLiteralRE = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// GenerationInput is everything that participates in a generation identity.
// Manifest must be the raw JSON value tree produced by ParseManifestValue, so
// Go and TS hash the same authored fields. Content maps package-relative
// resource paths to their SHA-256 hex digests; both runtimes must feed the
// same set of resources.
type GenerationInput struct {
	ContractVersion int
	Manifest        any
	Content         map[string]string
}

// Generation returns the canonical, cross-language generation string for a
// bundle. The result is the SHA-256 hex digest of the canonical bytes.
func Generation(input GenerationInput) (string, error) {
	content := make(map[string]any, len(input.Content))
	for key, digest := range input.Content {
		content[key] = digest
	}
	doc := map[string]any{
		"contract_version": json.Number(strconv.Itoa(input.ContractVersion)),
		"manifest":         input.Manifest,
		"content":          content,
	}
	canonical, err := Canonicalize(doc)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// Canonicalize serializes a JSON value deterministically: object keys are
// sorted, no insignificant whitespace is emitted, and strings use a minimal
// escape set. For the same input tree Go and TypeScript produce identical
// UTF-8 bytes.
func Canonicalize(value any) ([]byte, error) {
	out, err := appendCanonicalValue(nil, value)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func appendCanonicalValue(dst []byte, value any) ([]byte, error) {
	switch v := value.(type) {
	case nil:
		return append(dst, "null"...), nil
	case bool:
		if v {
			return append(dst, "true"...), nil
		}
		return append(dst, "false"...), nil
	case string:
		return appendCanonicalString(dst, v), nil
	case json.Number:
		return appendCanonicalNumber(dst, v.String())
	case int:
		return append(dst, strconv.Itoa(v)...), nil
	case []any:
		dst = append(dst, '[')
		for i, item := range v {
			if i > 0 {
				dst = append(dst, ',')
			}
			var err error
			dst, err = appendCanonicalValue(dst, item)
			if err != nil {
				return nil, err
			}
		}
		return append(dst, ']'), nil
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			if objectValueIncluded(v[key]) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		dst = append(dst, '{')
		for i, key := range keys {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendCanonicalString(dst, key)
			dst = append(dst, ':')
			var err error
			dst, err = appendCanonicalValue(dst, v[key])
			if err != nil {
				return nil, err
			}
		}
		return append(dst, '}'), nil
	default:
		return nil, fmt.Errorf("bundle: unsupported canonical value type %T", value)
	}
}

// objectValueIncluded mirrors the TypeScript inclusion rule: empty/null values
// are omitted from objects so omitempty semantics stay identical across
// runtimes.
func objectValueIncluded(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return true
	}
}

func appendCanonicalString(dst []byte, value string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(value); i++ {
		b := value[i]
		switch b {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			if b < 0x20 {
				dst = append(dst, fmt.Sprintf("\\u%04x", b)...)
			} else {
				dst = append(dst, b)
			}
		}
	}
	return append(dst, '"')
}

func appendCanonicalNumber(dst []byte, raw string) ([]byte, error) {
	value := strings.TrimSpace(raw)
	if !integerLiteralRE.MatchString(value) {
		return nil, fmt.Errorf("bundle: canonical number must be a base-10 integer, got %q", raw)
	}
	return append(dst, value...), nil
}

// ParseManifestValue decodes manifest JSON into the raw value tree used by
// Generation. Numbers are preserved exactly via json.Number.
func ParseManifestValue(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errors.New("bundle: manifest is empty")
	}
	return value, nil
}
