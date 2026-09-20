package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Result budgeting: large tool outputs are persisted to disk so the
// model receives a compact reference (file path + preview) instead of
// a truncated blob. The model can read_file the full result if needed.

const (
	// defaultResultBudget is the per-result char threshold above which
	// the result is persisted to disk. 50K chars ≈ ~12K tokens — large
	// enough that most tool outputs pass through unchanged, small enough
	// to prevent prompt bloat from runaway grep/shell output.
	defaultResultBudget = 50_000
	// defaultResultMaxLines catches highly fragmented output that is cheap in
	// bytes but still noisy and expensive for a model to reason over.
	defaultResultMaxLines  = 2_000
	projectionPreviewBytes = 4_096
)

// finalizeGenericToolResult is the settlement boundary for results that did
// not take a tool-specific projection. It bounds only the model-visible text,
// keeps structured metadata private and intact, and preserves native media.
// The full omitted text must be persisted before the result is changed; if an
// artifact cannot be written, the result fails open unchanged.
func finalizeGenericToolResult(sessionDir, callID string, raw toolresult.Result, threshold int) (toolresult.Result, string, bool) {
	if threshold <= 0 {
		threshold = defaultResultBudget
	}
	contextual := raw.TextProjection()
	if len(contextual) <= threshold && resultLineCount(contextual) <= defaultResultMaxLines {
		return raw, "", false
	}
	if strings.TrimSpace(sessionDir) == "" {
		return raw, "", false
	}
	path, err := persistResult(sessionDir, callID, contextual)
	if err != nil {
		return raw, "", false
	}
	preview := buildBoundedResultReference(path, contextual, raw, threshold)
	return replaceToolResultContext(raw, preview), path, true
}

func resultLineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

// replaceToolResultContext swaps all provider-textual parts for one bounded
// preview at the first textual position while retaining media order and all
// non-provider metadata. Resource bodies are textual provider context in Wuu,
// so they are archived with text instead of surviving as an unbounded bypass.
func replaceToolResultContext(raw toolresult.Result, preview string) toolresult.Result {
	out := raw.Clone()
	content := make([]toolresult.ContentPart, 0, len(raw.Content)+1)
	inserted := false
	for _, part := range raw.Content {
		switch part.Type {
		case toolresult.ContentTypeText, toolresult.ContentTypeResource, toolresult.ContentTypeResourceLink:
			if !inserted {
				content = append(content, toolresult.ContentPart{Type: toolresult.ContentTypeText, Text: preview})
				inserted = true
			}
		default:
			content = append(content, part)
		}
	}
	if !inserted {
		content = append([]toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: preview}}, content...)
	}
	out.Content = content
	return out
}

func buildBoundedResultReference(path, contextual string, raw toolresult.Result, threshold int) string {
	limit := projectionPreviewBytes
	if threshold > 0 && threshold < limit {
		limit = threshold
	}
	if len(raw.Content) == 0 && len(raw.StructuredContent) > 0 {
		if indexed := buildStructuredResultIndex(path, raw.StructuredContent, contextual, limit); indexed != "" {
			return indexed
		}
	}
	contentSHA := sha256Hex([]byte(contextual))
	build := func(evidenceBytes int) string {
		headBudget := min(evidenceBytes*2/3, len(contextual))
		head := utf8SafePrefix(contextual, headBudget)
		tailBudget := min(evidenceBytes-headBudget, len(contextual)-len(head))
		tail := utf8SafeSuffix(contextual, tailBudget)
		omittedStart := len(head)
		omittedEnd := len(contextual) - len(tail)
		envelope := map[string]any{
			"kind":                "archived_tool_result",
			"artifact_ref":        path,
			"content_sha256":      contentSHA,
			"original_characters": len(contextual),
			"preview_head":        head,
			"preview_tail":        tail,
		}
		hasMore := omittedStart < omittedEnd
		continuation := map[string]any{"has_more": hasMore}
		if hasMore {
			continuation["next"] = map[string]any{
				"continuation": encodeReadFileByteContinuation(path, omittedStart, projectionPreviewBytes, omittedEnd, contentSHA),
			}
		}
		envelope["continuation"] = continuation
		encoded, _ := json.Marshal(envelope)
		return string(encoded)
	}
	keep := largestFitting(len(contextual), limit, func(evidenceBytes int) int {
		return len(build(evidenceBytes))
	})
	return build(keep)
}

func utf8SafePrefix(s string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	if len(s) <= maximum {
		return s
	}
	for maximum > 0 && !utf8.RuneStart(s[maximum]) {
		maximum--
	}
	return s[:maximum]
}

func utf8SafeSuffix(s string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	if len(s) <= maximum {
		return s
	}
	start := len(s) - maximum
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

func buildStructuredResultIndex(path string, raw json.RawMessage, contextual string, limit int) string {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return ""
	}
	index := map[string]any{
		"kind":                "archived_structured_tool_result",
		"artifact_ref":        path,
		"content_sha256":      sha256Hex([]byte(contextual)),
		"original_characters": len(contextual),
		"instruction":         "Use continuation.next to inspect the omitted result without overlap.",
		"continuation": map[string]any{
			"has_more": true,
			"next": map[string]any{
				"continuation": encodeReadFileByteContinuation(path, 0, projectionPreviewBytes, len(contextual), sha256Hex([]byte(contextual))),
			},
		},
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		index["shape"] = "object"
		index["key_count"] = len(keys)
		// Key counts alone do not bound an index: names can be arbitrarily
		// long or expand when JSON-escaped. Keep only complete names that fit
		// alongside the recovery metadata in the serialized preview.
		setKeys := func(keep int) {
			index["keys"] = keys[:keep]
			index["keys_omitted"] = len(keys) - keep
		}
		keep := largestFitting(min(len(keys), 64), limit, func(keep int) int {
			setKeys(keep)
			encoded, _ := json.Marshal(index)
			return len(encoded)
		})
		setKeys(keep)
	case []any:
		index["shape"] = "array"
		index["item_count"] = len(typed)
	default:
		index["shape"] = fmt.Sprintf("%T", typed)
	}
	encoded, err := json.Marshal(index)
	if err != nil || len(encoded) > limit {
		return ""
	}
	return string(encoded)
}

// persistResult writes content to the session artifact directory
// and returns the absolute path.
func persistResult(sessionDir, callID, content string) (string, error) {
	dir := filepath.Join(sessionDir, "tool-results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, callID+".txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
