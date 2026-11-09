package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/workingnotes"
)

const (
	FreshContextTargetTokens   = compactionservice.FreshContextTargetTokens
	freshContextReminderTokens = 16_000
	newContextToolName         = "new_context"
)

var (
	ErrFreshContextTooLarge   = compactionservice.ErrFreshContextTooLarge
	ErrFreshContextNotSmaller = compactionservice.ErrFreshContextNotSmaller
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
func buildFreshContext(messages []providers.ChatMessage, historyHeadSeq, fixedTokens, targetTokens int, policies ...providers.MediaInputPolicy) ([]providers.ChatMessage, error) {
	var media providers.MediaInputPolicy
	if len(policies) > 0 {
		media = policies[0]
	}
	out, err := compactionservice.BuildWindow(compactionservice.WindowRequest{Messages: messages, HistoryHead: historyHeadSeq, FixedTokens: fixedTokens, TargetTokens: targetTokens, MediaInput: media})
	return out.Messages, err
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

func estimateFreshContextMessages(messages []providers.ChatMessage, policies ...providers.MediaInputPolicy) int {
	var policy providers.MediaInputPolicy
	if len(policies) > 0 {
		policy = policies[0]
	}
	return estimateOutboundRequestTokens(providers.ChatRequest{Messages: messages, MediaInput: policy})
}

type newContextRequest struct {
	Requested  bool                     `json:"requested"`
	Checkpoint *workingnotes.Checkpoint `json:"checkpoint,omitempty"`
}

func acceptedNewContextRequest(results []providers.ChatMessage) *newContextRequest {
	// Only the latest tool batch can request a transition, even if steering
	// arrived after it. Do not reuse a reference from an earlier assistant step.
	// Failed or truncated calls have no control effect.
	for index := len(results) - 1; index >= 0 && results[index].Role != "assistant"; index-- {
		message := results[index]
		if message.Role != "tool" || message.Name != newContextToolName || message.ToolResult == nil || message.ToolResult.IsError {
			continue
		}
		var signal newContextRequest
		if json.Unmarshal([]byte(message.ToolResult.TextProjection()), &signal) == nil && signal.Requested {
			return &signal
		}
	}
	return nil
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
		segments = append(segments, RequestOnlyContextMessages(compactionservice.Describe(compactionservice.DescribeRequest{}).Guidance)...)
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
