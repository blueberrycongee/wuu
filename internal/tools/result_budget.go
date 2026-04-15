package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/stringutil"
)

// Result budgeting: large tool outputs are persisted to disk so the
// model receives a compact reference (file path + preview) instead of
// a truncated blob. The model can read_file the full result if needed.
// Aligned with Claude Code's toolResultStorage / maxResultSizeChars.

const (
	// defaultResultBudget is the per-result char threshold above which
	// the result is persisted to disk. 50K chars ≈ ~12K tokens — large
	// enough that most tool outputs pass through unchanged, small enough
	// to prevent prompt bloat from runaway grep/shell output.
	defaultResultBudget = 50_000
	// MaxAggregateResultChars is the per-message aggregate cap for all
	// tool results in one turn. Prevents N parallel tools × 50K each
	// from creating an enormous message. Aligned with Claude Code's
	// per-message 200K aggregate budget.
	MaxAggregateResultChars = 200_000
	// previewHeadChars / previewTailChars control the preview shown to
	// the model when a result is persisted. Enough to see the beginning
	// and end without wasting context.
	previewHeadChars = 2000
	previewTailChars = 1000
)

// MaybePersistResult checks whether result exceeds threshold chars.
// If so it writes the full content to disk under sessionDir and
// returns a compact reference the model can read_file. Otherwise it
// returns the original result unchanged.
//
// sessionDir may be empty — in that case persistence is skipped and
// the result is truncated to threshold as a fallback (matching the
// old behaviour).
func MaybePersistResult(sessionDir, toolName, callID, result string, threshold int) string {
	if threshold <= 0 {
		threshold = defaultResultBudget
	}
	if len(result) <= threshold {
		return result
	}

	// Try disk persistence first.
	if sessionDir != "" {
		path, err := persistResult(sessionDir, callID, result)
		if err == nil {
			return buildReference(path, result, len(result))
		}
		// Fall through to truncation on write failure.
	}

	// Fallback: hard truncation (preserves old behaviour when no
	// session dir is available).
	return result[:threshold] + "\n\n[truncated — output too large]"
}

// EnforceAggregateBudget trims tool result messages so that their
// total content length stays within MaxAggregateResultChars. Results
// are trimmed in reverse order (newest first) since earlier results
// are more likely to have been referenced by subsequent tool calls.
func EnforceAggregateBudget(results []string) []string {
	total := 0
	for _, r := range results {
		total += len(r)
	}
	if total <= MaxAggregateResultChars {
		return results
	}

	out := make([]string, len(results))
	copy(out, results)

	// Trim from the end — later results are more expendable.
	for i := len(out) - 1; i >= 0 && total > MaxAggregateResultChars; i-- {
		excess := total - MaxAggregateResultChars
		if len(out[i]) > excess+500 {
			// Partial trim is enough.
			out[i] = stringutil.Truncate(out[i], len(out[i])-excess, "\n[trimmed to fit aggregate budget]")
			break
		}
		// Full replacement with a short stub.
		saved := len(out[i])
		out[i] = fmt.Sprintf("[Result truncated to fit aggregate budget — original was %d chars]", saved)
		total -= saved - len(out[i])
	}
	return out
}

// persistResult writes content to .wuu/sessions/{sid}/tool-results/{callID}.txt
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

// buildReference produces the compact JSON-ish message the model sees
// when a large result has been persisted. It includes a head/tail
// preview and instructions to use read_file for the full content.
func buildReference(path, content string, originalSize int) string {
	head := stringutil.Truncate(content, previewHeadChars, "")
	tail := ""
	if len(content) > previewHeadChars+previewTailChars {
		tail = stringutil.Truncate(content[len(content)-previewTailChars:], previewTailChars, "")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[Result too large (%d chars) — saved to disk. Use read_file to see the full output.]\n\n", originalSize)
	fmt.Fprintf(&b, "File: %s\n\n", path)
	b.WriteString("--- preview (first ~2000 chars) ---\n")
	b.WriteString(head)
	if tail != "" {
		b.WriteString("\n\n--- preview (last ~1000 chars) ---\n")
		b.WriteString(tail)
	}
	return b.String()
}
