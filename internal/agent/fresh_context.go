package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const (
	FreshContextTargetTokens = 50_000
	// Reserve space for provider and extension request transforms.
	freshContextTransformReserveTokens = 4_000
	freshContextReminderTokens         = 16_000
	newContextToolName                 = "new_context"
)

var (
	ErrFreshContextTooLarge   = errors.New("fresh context cannot fit the target budget")
	ErrFreshContextNotSmaller = errors.New("fresh context would not shrink active history")
)

// ContextWindowProvider opts into summary-free resets. The host supplies working
// notes, archive and checkpoint safety. Extensions may replace the window policy.
// This contract does not require a second inference provider.
type ContextWindowProvider interface {
	CompactionProvider
	ContextWindowsEnabled() bool
}

// HistoryArchive records the physical addresses assigned to one append. Seqs is
// aligned with the input messages; zero means that message was not persisted.
type HistoryArchive struct {
	Seqs    []int
	HeadSeq int
}

type HistoryArchiveFunc func(context.Context, []providers.ChatMessage) (HistoryArchive, error)

// FreshContextCommitFunc atomically installs the checkpoint. Empty note content
// invalidates legacy host-generated notes; working memory is independent.
// The result includes assigned addresses and any concurrently saved tail.
type FreshContextCommitFunc func(context.Context, []providers.ChatMessage, int, string, CompactionNote) ([]providers.ChatMessage, int, error)

type FreshContextBuilder func(context.Context, []providers.ChatMessage, int, int, int) ([]providers.ChatMessage, error)

// buildFreshContext releases the archived transcript without summarizing it.
// BYOK models get an explicit recovery address and, when it fits, the latest user
// instruction. Working notes remain in persistent storage and are read on demand.
func buildFreshContext(messages []providers.ChatMessage, historyHeadSeq, fixedTokens, targetTokens int) ([]providers.ChatMessage, error) {
	if targetTokens <= 0 {
		targetTokens = FreshContextTargetTokens
	}
	fixedTokens = max(0, fixedTokens)
	var task *providers.ChatMessage
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.EqualFold(messages[index].Role, "user") && !messages[index].Hidden {
			task = &messages[index]
			break
		}
	}
	recovery := fmt.Sprintf("[Context window]\nThe previous active transcript is archived through History Seq %d. Files, processes and other environment state are unchanged. Read your persistent working notes with notes. Recover missing decisions, progress and tool outcomes with history_search and history_read before repeating actions. Start at Seq %d if no useful note exists.", historyHeadSeq, max(1, historyHeadSeq-24))
	if task != nil && task.Seq > 0 {
		recovery += fmt.Sprintf(" The latest user instruction is at Seq %d.", task.Seq)
	}
	replacement := append(freshContextSystemPrefix(messages), providers.ChatMessage{
		Role: "system", Content: recovery, Hidden: true, Origin: "internal", Cause: "fresh_context",
	})
	budget := targetTokens - fixedTokens - min(freshContextTransformReserveTokens, targetTokens/10)
	if estimateFreshContextMessages(replacement) > budget {
		return nil, ErrFreshContextTooLarge
	}
	if task != nil {
		candidate := append(providers.CloneChatMessages(replacement), providers.CloneChatMessage(*task))
		if estimateFreshContextMessages(candidate) <= budget && estimateFreshContextMessages(candidate) < estimateFreshContextMessages(messages) {
			replacement = candidate
		}
	}
	if estimateFreshContextMessages(replacement) >= estimateFreshContextMessages(messages) {
		return nil, ErrFreshContextNotSmaller
	}
	if err := providers.ValidateToolCallHistory(replacement); err != nil {
		return nil, fmt.Errorf("fresh context has invalid tool-call history: %w", err)
	}
	return replacement, nil
}

func freshContextSystemPrefix(messages []providers.ChatMessage) []providers.ChatMessage {
	end := 0
	for end < len(messages) && strings.EqualFold(strings.TrimSpace(messages[end].Role), "system") {
		if compact.IsConversationSummaryContent(messages[end].Content) || messages[end].Cause == "fresh_context" {
			break
		}
		end++
	}
	return providers.CloneChatMessages(messages[:end])
}

func estimateFreshContextMessages(messages []providers.ChatMessage) int {
	return estimateOutboundRequestTokens(providers.ChatRequest{Messages: messages})
}

func acceptedNewContextRequest(results []providers.ChatMessage) bool {
	// Failed or truncated tool calls have no control effect.
	for _, message := range results {
		if message.Role != "tool" || message.Name != newContextToolName || message.ToolResult == nil || message.ToolResult.IsError {
			continue
		}
		var signal struct {
			Requested bool `json:"requested"`
		}
		if json.Unmarshal([]byte(message.ToolResult.TextProjection()), &signal) == nil && signal.Requested {
			return true
		}
	}
	return false
}

func deferRepeatedContextTransition(messages []providers.ChatMessage, currentTokens, targetTokens int) bool {
	if targetTokens <= 0 {
		targetTokens = FreshContextTargetTokens
	}
	if currentTokens > targetTokens {
		return false
	}
	for _, message := range messages {
		if message.Cause == "fresh_context" {
			return true
		}
	}
	return false
}

func withContextWindowGuidance(base func() []ContextSegment) func() []ContextSegment {
	return func() []ContextSegment {
		var segments []ContextSegment
		if base != nil {
			segments = append(segments, base()...)
		}
		segments = append(segments, RequestOnlyContextMessages([]providers.ChatMessage{
			contextWindowReminder(`Maintain persistent working notes with notes. Record objectives, constraints, decisions, completed work, verification and next steps as work progresses. Include useful History Seq addresses for exact recovery. Before calling new_context, save anything needed to continue. The host releases the old transcript after the full tool batch without generating a summary. Files and running processes are unchanged. After a switch, read your notes and recover missing facts with history_read or history_search before acting.`),
		})...)
		return segments
	}
}

func contextWindowReminder(content string) providers.ChatMessage {
	return providers.ChatMessage{Role: "user", Name: "wuu_context_window", Content: "<system-reminder>\n" + strings.TrimSpace(content) + "\n</system-reminder>", Hidden: true}
}

func (r *StreamRunner) freshContextTargetTokens(compactThresholdTokens int) int {
	target := FreshContextTargetTokens
	if compactThresholdTokens > 0 && compactThresholdTokens < target {
		target = compactThresholdTokens
	}
	inputLimit := r.MaxInputTokens
	if inputLimit <= 0 && r.ContextWindowOverride > 0 {
		inputLimit = r.ContextWindowOverride - max(0, r.OutputReserveTokens)
	}
	if inputLimit > 0 && inputLimit < target {
		target = inputLimit
	}
	return max(1, target)
}

// A rejected request is negative capacity evidence even without model metadata.
func reactiveFreshContextTarget(target, lastSuccessful, failed int) int {
	if target <= 0 {
		target = FreshContextTargetTokens
	}
	target = reactiveCompactTarget(target, lastSuccessful)
	probe := min(target, failed)
	margin := min(max(probe/observedCompactSafetyDivisor, observedCompactMinMargin), probe/2)
	return max(1, probe-margin)
}
