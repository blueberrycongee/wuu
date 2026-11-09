package agent

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/compactionservice"
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
	plan, err := compactionservice.Handoff(compactionservice.HandoffRequest{Messages: messages, PreviousNote: previous.Markdown, Intent: intent, SourceSessionID: sourceSessionID, SourceThroughSeq: sourceThroughSeq})
	return CompactionNotePlan{Prompt: plan.Prompt, MaxBytes: plan.MaxBytes}, err
}
