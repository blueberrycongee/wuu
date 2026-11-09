package subagent

import (
	"github.com/blueberrycongee/wuu/internal/providers"
)

// deriveWorkerActivity maps a stream event to a short human-readable
// phrase for the sub-agent activity indicator. Returns "" for events
// that aren't worth advertising — including text/thinking deltas
// beyond the first (the caller dedupes so emitting per-delta is safe,
// but returning "" early avoids the lock-acquire/compare churn).
//
// The phrases are deliberately narrow: one or two words plus an
// optional tool name. The observer panel is a one-line-per-worker
// surface, so anything longer would just get truncated off.
func deriveWorkerActivity(ev providers.StreamEvent) string {
	switch ev.Type {
	case providers.EventThinkingDelta:
		return "thinking"
	case providers.EventContentDelta:
		return "responding"
	case providers.EventToolUseStart:
		if ev.ToolCall != nil && ev.ToolCall.Name != "" {
			return "→ " + ev.ToolCall.Name
		}
		return "→ tool"
	case providers.EventToolUseEnd:
		if ev.ToolCall != nil && ev.ToolCall.Name != "" {
			return "✓ " + ev.ToolCall.Name
		}
		return "✓ tool"
	default:
		return ""
	}
}
