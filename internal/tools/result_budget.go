package tools

import (
	"encoding/base64"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Bound each recovery read's allocation independently of the serialized token
// budget. The initial page already holds the full result and needs no byte cap.
const maxRecoveryPageBytes = 128 * 1024

// finalizeGenericToolResult is the settlement boundary for results that did
// not take a tool-specific projection. It bounds only the model-visible text,
// keeps structured metadata private and intact, and preserves native media.
// The full omitted text must be persisted before the result is changed; if an
// artifact cannot be written, the result fails open unchanged.
func finalizeGenericToolResult(sessionDir, callID string, raw toolresult.Result, budgetTokens int) (toolresult.Result, string, bool) {
	if raw.ModelText != nil {
		return raw, "", false
	}
	if budgetTokens <= 0 {
		budgetTokens = defaultProjectionTokenBudget
	}
	contextual := providers.ProjectToolResult(raw).ToolText
	if estimateResultTokens(contextual) <= budgetTokens {
		return settleModelText(raw, contextual), "", false
	}
	if strings.TrimSpace(sessionDir) == "" {
		return raw, "", false
	}
	path, err := persistResult(sessionDir, callID, contextual)
	if err != nil {
		return raw, "", false
	}
	preview, ok := buildBoundedResultReference(path, contextual, raw.IsError, budgetTokens)
	if !ok {
		return raw, "", false
	}
	return settleModelText(raw, preview), path, true
}

// Keep the producer payload for clients and recovery. Every downstream model
// projection consumes the same settled text, without appending another index.
func settleModelText(raw toolresult.Result, text string) toolresult.Result {
	out := raw.Clone()
	out.ModelText = &text
	return out
}

func buildBoundedResultReference(path, contextual string, isError bool, budgetTokens int) (string, bool) {
	contentSHA := sha256Hex([]byte(contextual))
	envelope := map[string]any{
		"kind":           "archived_tool_result",
		"artifact_ref":   path,
		"content_sha256": contentSHA,
		"total_bytes":    len(contextual),
		"is_error":       isError,
	}
	if page, ok := buildResultPage(envelope, path, []byte(contextual), 0, len(contextual), len(contextual), contentSHA, budgetTokens); ok {
		return page, true
	}
	// A tiny requested budget must not expose the full output or prevent an
	// otherwise valid program from running. Keep only enough to recover it.
	return marshalEnvelope(map[string]any{
		"kind": "archived_tool_result", "artifact_ref": path,
		"total_bytes": len(contextual), "is_error": isError,
	})
}

// buildResultPage is shared by the first archived page and read_file recovery.
// Prefer complete lines, but split an oversized line at a rune boundary so a
// cursor always advances. Binary ranges retain the existing base64 contract.
func buildResultPage(envelope map[string]any, path string, data []byte, offset, end, limit int, contentSHA string, budgetTokens int) (string, bool) {
	text := utf8.Valid(data) && !strings.ContainsRune(string(data), '\x00')
	if _, width := utf8.DecodeRune(data); width > limit {
		text = false
	}
	build := func(keep int) string {
		if text {
			for keep > 0 && keep < len(data) && !utf8.RuneStart(data[keep]) {
				keep--
			}
			envelope["content"] = string(data[:keep])
			envelope["encoding"] = "utf-8"
		} else {
			envelope["content_base64"] = base64.StdEncoding.EncodeToString(data[:keep])
			envelope["encoding"] = "base64"
		}
		envelope["byte_offset"] = offset
		envelope["byte_count"] = keep
		hasMore := offset+keep < end
		continuation := map[string]any{"has_more": hasMore}
		if hasMore {
			continuation["next"] = map[string]any{
				"continuation": encodeReadFileByteContinuation(path, offset+keep, min(limit, maxRecoveryPageBytes), end, contentSHA),
			}
		}
		envelope["continuation"] = continuation
		out, _ := marshalEnvelope(envelope)
		return out
	}
	keep := largestFitting(min(len(data), limit), budgetTokens, func(keep int) int {
		return estimateResultTokens(build(keep))
	})
	if text && keep < len(data) {
		// A short header before a long line must not consume the whole page.
		// Keep line boundaries only when they use at least half the byte budget.
		if newline := strings.LastIndexByte(string(data[:keep]), '\n'); newline >= 0 && newline+1 >= keep/2 {
			keep = newline + 1
		}
	}
	out := build(keep)
	if estimateResultTokens(out) > budgetTokens || (len(data) > 0 && envelope["byte_count"].(int) == 0) {
		return "", false
	}
	return out, true
}

// persistResult writes content to the session artifact directory
// and returns the absolute path.
func persistResult(sessionDir, callID, content string) (string, error) {
	dir := filepath.Join(sessionDir, "tool-results")
	// Provider call IDs can repeat across turns and may contain path separators.
	// A content-bound identity keeps earlier recovery cursors valid in both cases.
	path := filepath.Join(dir, sha256Hex([]byte(callID+"\x00"+content))+".txt")
	// Atomic publication also protects readers when identical calls settle
	// concurrently: the content-bound path never exposes a partially written file.
	if err := securefs.WriteFileAtomic(path, []byte(content)); err != nil {
		return "", err
	}
	return path, nil
}
