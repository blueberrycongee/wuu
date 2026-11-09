package toolresult

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"unicode/utf8"
)

const (
	StructuredContentIndexMaxKeys     = 12
	StructuredContentIndexMaxKeyRunes = 64
	structuredPreviewMaxNodes         = 24
	structuredPreviewMaxDepth         = 4
	structuredPreviewMaxEntries       = 8
	structuredPreviewMaxStringRunes   = 96
)

// StructuredContentIndexJSON returns a deterministic, bounded model-facing
// index for structured tool data. The complete payload remains durable on the
// Result; the preview preserves scalar evidence so mixed text + structured
// results do not collapse into a list of field names with no usable meaning.
func StructuredContentIndexJSON(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return ""
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(canonical)
	index := map[string]any{
		"kind":            "structured_tool_result_index",
		"json_characters": len(trimmed),
		"sha256":          hex.EncodeToString(digest[:]),
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := sortedStructuredKeys(typed)
		index["shape"] = "object"
		index["key_count"] = len(keys)
		visible := keys
		if len(visible) > StructuredContentIndexMaxKeys {
			visible = visible[:StructuredContentIndexMaxKeys]
			index["keys_omitted"] = len(keys) - len(visible)
		}
		display := make([]string, 0, len(visible))
		for _, key := range visible {
			display = append(display, truncateStructuredIndexKey(key))
		}
		index["keys"] = display
	case []any:
		index["shape"] = "array"
		index["item_count"] = len(typed)
	case string:
		index["shape"] = "string"
	case json.Number:
		index["shape"] = "number"
	case bool:
		index["shape"] = "boolean"
	case nil:
		index["shape"] = "null"
	default:
		return ""
	}
	budget := structuredPreviewBudget{remaining: structuredPreviewMaxNodes}
	index["value_preview"] = budget.project(value, 0)
	if budget.truncated {
		index["preview_truncated"] = true
	}
	encoded, err := json.Marshal(index)
	if err != nil {
		return ""
	}
	return string(encoded)
}

type structuredPreviewBudget struct {
	remaining int
	truncated bool
}

func (b *structuredPreviewBudget) project(value any, depth int) any {
	if b.remaining <= 0 {
		b.truncated = true
		return "[omitted]"
	}
	b.remaining--
	switch typed := value.(type) {
	case map[string]any:
		if depth >= structuredPreviewMaxDepth {
			b.truncated = true
			return "[object omitted]"
		}
		keys := sortedStructuredPreviewKeys(typed)
		if len(keys) > structuredPreviewMaxEntries {
			keys = keys[:structuredPreviewMaxEntries]
			b.truncated = true
		}
		out := make(map[string]any, len(keys))
		for _, key := range keys {
			out[truncateStructuredIndexKey(key)] = b.project(typed[key], depth+1)
		}
		return out
	case []any:
		if depth >= structuredPreviewMaxDepth {
			b.truncated = true
			return "[array omitted]"
		}
		limit := len(typed)
		if limit > structuredPreviewMaxEntries {
			limit = structuredPreviewMaxEntries
			b.truncated = true
		}
		out := make([]any, 0, limit)
		for index := 0; index < limit; index++ {
			out = append(out, b.project(typed[index], depth+1))
		}
		return out
	case string:
		if utf8.RuneCountInString(typed) > structuredPreviewMaxStringRunes {
			b.truncated = true
		}
		return truncateStructuredIndexString(typed, structuredPreviewMaxStringRunes)
	case json.Number:
		// UseNumber preserves precision but also accepts arbitrarily long
		// numeric literals. Never let one scalar bypass the preview budget or
		// clip its digits into a different numeric value.
		if len(typed) > structuredPreviewMaxStringRunes {
			b.truncated = true
			return "[number omitted]"
		}
		return typed
	case bool, nil:
		return typed
	default:
		b.truncated = true
		return "[unsupported value]"
	}
}

func sortedStructuredKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStructuredPreviewKeys(value map[string]any) []string {
	keys := sortedStructuredKeys(value)
	sort.SliceStable(keys, func(i, j int) bool {
		iScalar := isStructuredScalar(value[keys[i]])
		jScalar := isStructuredScalar(value[keys[j]])
		if iScalar != jScalar {
			return iScalar
		}
		return keys[i] < keys[j]
	})
	return keys
}

func isStructuredScalar(value any) bool {
	switch value.(type) {
	case string, json.Number, bool, nil:
		return true
	default:
		return false
	}
}

func truncateStructuredIndexString(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum]) + "..."
}

func truncateStructuredIndexKey(value string) string {
	if utf8.RuneCountInString(value) <= StructuredContentIndexMaxKeyRunes {
		return value
	}
	digest := sha256.Sum256([]byte(value))
	return truncateStructuredIndexString(value, StructuredContentIndexMaxKeyRunes) + "#" + hex.EncodeToString(digest[:4])
}
