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

// Recovery pages share the read_file byte cursor, including for long single
// lines. The serialized envelope, not just its payload, must fit the target.
const projectionPreviewBytes = 4_096

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
	return buildResultPage(envelope, path, []byte(contextual), 0, len(contextual), projectionPreviewBytes, contentSHA, budgetTokens)
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
				"continuation": encodeReadFileByteContinuation(path, offset+keep, limit, end, contentSHA),
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
		if newline := strings.LastIndexByte(string(data[:keep]), '\n'); newline >= 0 {
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
