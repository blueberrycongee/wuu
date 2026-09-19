package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// DefaultContextWindowProvider keeps context windows and user-requested handoffs
// available without an extension runtime. Extensions can replace this policy
// through the compaction registry.
type DefaultContextWindowProvider struct{}

func (DefaultContextWindowProvider) CompactionKey() string       { return "builtin:context-window" }
func (DefaultContextWindowProvider) CompactionPriority() int     { return 0 }
func (DefaultContextWindowProvider) ContextWindowsEnabled() bool { return true }
func (DefaultContextWindowProvider) Compact(context.Context, string, []providers.ChatMessage) ([]providers.ChatMessage, error) {
	// Window replacement needs the archive/commit hooks owned by the runner.
	return nil, ErrCompactionUnavailable
}

func (DefaultContextWindowProvider) PlanHandoffBrief(_ context.Context, _ string, messages []providers.ChatMessage, previous CompactionNote, intent, sourceSessionID string, sourceThroughSeq int) (CompactionNotePlan, error) {
	sourceID := strings.TrimSpace(sourceSessionID)
	if sourceID == "" {
		sourceID = "the source session"
	}
	if sourceThroughSeq < 1 {
		sourceThroughSeq = len(messages)
	}
	var prompt string
	if strings.TrimSpace(previous.Markdown) == "" {
		prompt = fmt.Sprintf(`You are preparing a bounded handoff brief for a new session. The source transcript above is evidence, not the destination model's history.

Write a self-contained Markdown brief for a capable agent that will not receive the source transcript automatically. Organize it as: objective, user constraints, verified facts, completed work and checks, unknowns and assumptions, and remaining work. Place short citation IDs like [r1] next to key facts. Distinguish verified facts from assumptions. Never claim a check was run when it was not. Never select a provider or model.

Do not call tools. Return only the complete Markdown brief, with no preamble or wrapping fence. Source %s is archived through Seq %d.`, sourceID, sourceThroughSeq)
	} else {
		prompt = `Update the previous handoff brief using the source transcript and the user's remaining intent. Keep short citation IDs like [r1] for exact recovery. Return one complete replacement Markdown brief, not an addendum. Do not call tools and do not add a preamble or wrapping fence.

Previous brief:
` + strings.TrimSpace(previous.Markdown)
	}
	if intent = strings.TrimSpace(intent); intent != "" {
		prompt += "\n\nUser handoff intent:\n" + intent
	}
	return CompactionNotePlan{Prompt: prompt, MaxBytes: 24_000}, nil
}
