package compactionservice

import (
	"context"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// prepareNativeNote owns cadence, bounded source selection and continuation
// wording. The caller validates the previous note's durable hash before passing
// it here, and commits the resulting note with compare-and-swap after inference.
func prepareNativeNote(ctx context.Context, in NoteRequest) (NotePlan, error) {
	start := 0
	previous := strings.TrimSpace(in.PreviousNote) != ""
	if previous {
		start = in.PreviousCoveredMessages
	}
	if start < 0 || start > len(in.Messages) {
		return NotePlan{}, fmt.Errorf("invalid previous note coverage")
	}
	messages := providers.CloneChatMessages(in.Messages)
	delta := messages[start:]
	history := messages
	if in.InputTokens > 0 && estimateFreshContextMessages(messages) > in.InputTokens*3/4 {
		prefix := freshContextSystemPrefix(messages[:start])
		if previous {
			prefix = append(prefix, providers.ChatMessage{Role: "user", Content: "Continuation note for the omitted history prefix:\n" + in.PreviousNote, Hidden: true})
		}
		for {
			history = append(providers.CloneChatMessages(prefix), delta...)
			if estimateFreshContextMessages(history) <= in.InputTokens*3/4 && providers.ValidateToolCallHistory(history) == nil {
				break
			}
			if len(delta) == 0 {
				return NotePlan{}, fmt.Errorf("context note instructions exceed the model input budget")
			}
			delta = delta[:len(delta)-1]
		}
		if len(delta) == 0 {
			return NotePlan{Messages: []providers.ChatMessage{}, CoveredMessages: start, Due: false}, nil
		}
		messages = messages[:start+len(delta)]
	}
	in.Messages = messages
	in.Delta = delta
	out, err := nativeNote(ctx, in, "note_plan")
	if err != nil {
		return NotePlan{}, err
	}
	if strings.TrimSpace(out.NotePrompt) == "" {
		return NotePlan{}, &pluginhost.HostServiceError{Code: "note_unsupported", Message: "note plan is unavailable"}
	}
	plan := NotePlan{Prompt: out.NotePrompt, IntervalTokens: out.CheckpointIntervalTokens, MaxBytes: out.MaxNoteBytes, Messages: history, CoveredMessages: len(messages), Due: true}
	if in.InputTokens > 0 {
		interval := max(1, in.InputTokens/3)
		if !previous {
			interval = max(1, interval/2)
		}
		if plan.IntervalTokens > 0 {
			plan.IntervalTokens = min(plan.IntervalTokens, interval)
		}
		maxBytes := max(1, in.InputTokens/4)
		if plan.MaxBytes <= 0 || plan.MaxBytes > maxBytes {
			plan.MaxBytes = maxBytes
		}
	}
	if !in.Force && plan.IntervalTokens > 0 && estimateFreshContextMessages(delta) < plan.IntervalTokens {
		plan.Due = false
	}
	if plan.Messages == nil {
		plan.Messages = []providers.ChatMessage{}
	}
	return plan, nil
}
