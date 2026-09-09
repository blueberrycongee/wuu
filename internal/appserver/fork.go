package appserver

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
)

var (
	errForkTargetNotFound      = errors.New("fork target not found")
	errForkToolResultsNotFound = errors.New("fork target tool results not found")
)

func forkHistoryAtTarget(history []providers.ChatMessage, sourceThreadID string, turns []Turn, targetTurnID, targetItemID string) ([]providers.ChatMessage, error) {
	return forkHistoryAtTargetWithIdentity(history, sourceThreadID, turns, targetTurnID, targetItemID, ThreadItem{})
}

func forkHistoryAtTargetWithIdentity(history []providers.ChatMessage, sourceThreadID string, turns []Turn, targetTurnID, targetItemID string, target ThreadItem) ([]providers.ChatMessage, error) {
	targetTurnID = strings.TrimSpace(targetTurnID)
	targetItemID = strings.TrimSpace(targetItemID)
	if targetTurnID == "" && targetItemID == "" {
		return cloneForkHistory(history), nil
	}

	projection := projectHistory(sourceThreadID, history, time.Time{})
	origin, err := forkOriginAtTarget(projection, turns, targetTurnID, targetItemID, target)
	if err != nil {
		return nil, err
	}
	if origin.EndIndex < 0 || origin.EndIndex >= len(history) {
		return nil, errForkTargetNotFound
	}
	return cloneForkHistory(history[:origin.EndIndex+1]), nil
}

func forkPersistedHistoryAtTarget(history []persistedMessage, sourceThreadID string, turns []Turn, targetTurnID, targetItemID string, target ThreadItem) ([]providers.ChatMessage, error) {
	targetTurnID = strings.TrimSpace(targetTurnID)
	targetItemID = strings.TrimSpace(targetItemID)
	if targetTurnID == "" && targetItemID == "" {
		return cloneForkHistory(chatMessagesFromPersistedMessages(history)), nil
	}
	projection := projectPersistedHistory(sourceThreadID, history, time.Time{}, nil)
	origin, err := forkOriginAtTarget(projection, turns, targetTurnID, targetItemID, target)
	if err != nil {
		return nil, err
	}
	if origin.EndIndex < 0 || origin.EndIndex >= len(history) {
		return nil, errForkTargetNotFound
	}
	return cloneForkHistory(chatMessagesFromPersistedMessages(history[:origin.EndIndex+1])), nil
}

// forkLiveAnswerHistory materializes a branch from a completed final answer
// while provider-level turn settlement is still in progress. The admitted user
// message is already present in baseHistory. Completed tool items are projected
// as portable call/result pairs before the answer, without retaining execution
// ledger ownership from the source thread.
func forkLiveAnswerHistory(baseHistory []providers.ChatMessage, turn Turn, targetItemID string) ([]providers.ChatMessage, error) {
	targetItemID = strings.TrimSpace(targetItemID)
	if targetItemID == "" || turn.Status != TurnStatusInProgress {
		return nil, errForkTargetNotFound
	}
	history := cloneHistory(baseHistory)
	found := false
	for _, item := range turn.Items {
		if item.ID == targetItemID {
			if item.Type != ThreadItemAgentMessage || item.Status != ThreadItemStatusCompleted || !item.Terminal || strings.TrimSpace(item.Text) == "" {
				return nil, errForkTargetNotFound
			}
			history = append(history, providers.ChatMessage{
				Role:           "assistant",
				Content:        item.Text,
				Phase:          providers.MessagePhaseFinalAnswer,
				ProviderItemID: item.SourceID,
				FinishReason:   providers.FinishReason(item.FinishReason),
				StopReason:     item.StopReason,
				Truncated:      item.Truncated,
			})
			found = true
			break
		}
		if item.Type != ThreadItemToolCall {
			continue
		}
		if item.Status != ThreadItemStatusCompleted || strings.TrimSpace(item.SourceID) == "" {
			return nil, errForkToolResultsNotFound
		}
		history = append(history,
			providers.ChatMessage{
				Role: "assistant",
				ToolCalls: []providers.ToolCall{{
					ID:        item.SourceID,
					Name:      item.Name,
					Arguments: item.Arguments,
					Display:   cloneToolCallDisplay(item.Display),
				}},
			},
			providers.ChatMessage{
				Role:       "tool",
				Name:       item.Name,
				ToolCallID: item.SourceID,
				Content:    item.Result,
				ToolResult: cloneToolResult(item.ResultDetail),
			},
		)
	}
	if !found {
		return nil, errForkTargetNotFound
	}
	return cloneForkHistory(history), nil
}

func forkOriginAtTarget(projection historyProjection, turns []Turn, targetTurnID, targetItemID string, target ThreadItem) (historyItemOrigin, error) {
	var origin historyItemOrigin
	var ok bool
	if targetItemID != "" {
		if target.Type == "" {
			if turn, found := turnByID(turns, targetTurnID); found {
				for _, item := range turn.Items {
					if item.ID == targetItemID {
						target = item
						break
					}
				}
			}
		}
		origin, ok = projectionOriginForIdentity(projection, target)
		// Positional ids can be reused after checkpointing or display pruning.
		// A missing durable identity must not resolve to an unrelated item that
		// happens to occupy the same position in another projection.
		if !ok && target.Seq <= 0 && strings.TrimSpace(target.SourceID) == "" {
			origin, ok = projectionOriginForTarget(projection, turns, targetTurnID, targetItemID)
			if ok && target.Type != "" && origin.Item.Type != target.Type {
				ok = false
			}
		}
		if ok && !origin.Complete {
			return historyItemOrigin{}, errForkToolResultsNotFound
		}
	} else {
		origin, ok = projection.TurnSpans[targetTurnID]
		if !ok {
			if turn, found := turnByID(turns, targetTurnID); found {
				for i := len(turn.Items) - 1; i >= 0 && !ok; i-- {
					origin, ok = projectionOriginForItem(projection, targetTurnID, turn.Items[i])
				}
			}
		}
	}
	if !ok {
		return historyItemOrigin{}, errForkTargetNotFound
	}
	return origin, nil
}

func projectionOriginForIdentity(projection historyProjection, target ThreadItem) (historyItemOrigin, bool) {
	if target.Type == "" {
		return historyItemOrigin{}, false
	}
	sourceID := strings.TrimSpace(target.SourceID)
	var best historyItemOrigin
	found := false
	for _, origin := range projection.ItemOrigins {
		if origin.Item.Type != target.Type {
			continue
		}
		// Prefer the exact durable address (seq + provider source id). Live
		// turns created from the current in-memory history often carry seq 0
		// until the messages are durably assigned addresses, so fall back to
		// the provider source id alone — it is stable across the live turn,
		// the in-memory history, and the persisted raw/display histories.
		if target.Seq > 0 && origin.Item.Seq == target.Seq && (sourceID == "" || origin.Item.SourceID == sourceID) {
			return origin, true
		}
		if sourceID == "" || origin.Item.SourceID != sourceID {
			continue
		}
		if !found || origin.EndIndex > best.EndIndex {
			best = origin
			found = true
		}
	}
	if found {
		return best, true
	}
	return historyItemOrigin{}, false
}

func projectionOriginForTarget(projection historyProjection, turns []Turn, turnID, itemID string) (historyItemOrigin, bool) {
	if origin, ok := projection.ItemOrigins[historyItemAddress{TurnID: turnID, ItemID: itemID}]; ok {
		return origin, true
	}
	turn, ok := turnByID(turns, turnID)
	if !ok {
		return historyItemOrigin{}, false
	}
	for _, item := range turn.Items {
		if item.ID == itemID {
			return projectionOriginForItem(projection, turnID, item)
		}
	}
	return historyItemOrigin{}, false
}

func projectionOriginForItem(projection historyProjection, turnID string, target ThreadItem) (historyItemOrigin, bool) {
	if origin, ok := projection.ItemOrigins[historyItemAddress{TurnID: turnID, ItemID: target.ID}]; ok {
		return origin, true
	}
	if target.Seq <= 0 {
		return historyItemOrigin{}, false
	}
	for _, origin := range projection.ItemOrigins {
		if origin.Item.Seq == target.Seq && origin.Item.Type == target.Type && origin.Item.SourceID == target.SourceID {
			return origin, true
		}
	}
	return historyItemOrigin{}, false
}

func turnByID(turns []Turn, turnID string) (Turn, bool) {
	for _, turn := range turns {
		if turn.ID == turnID {
			return turn, true
		}
	}
	return Turn{}, false
}

func cloneForkHistory(history []providers.ChatMessage) []providers.ChatMessage {
	cloned := cloneHistory(history)
	for i := range cloned {
		// Tool invocation ids address the execution ledger owned by the source
		// thread. The semantic tool call/result history is safe to copy, but the
		// source ledger address must not be projected into the new fork owner.
		cloned[i].ToolInvocationID = ""
	}
	return cloned
}

func editHistoryBeforeUserMessage(history []providers.ChatMessage, sourceThreadID string, turns []Turn, targetTurnID, targetItemID string) ([]providers.ChatMessage, ThreadEditDraft, error) {
	targetTurnID = strings.TrimSpace(targetTurnID)
	targetItemID = strings.TrimSpace(targetItemID)
	if targetTurnID == "" {
		return nil, ThreadEditDraft{}, fmt.Errorf("turn_id is required")
	}
	if targetItemID == "" {
		return nil, ThreadEditDraft{}, fmt.Errorf("item_id is required")
	}
	projection := projectHistory(sourceThreadID, history, time.Time{})
	origin, ok := projectionOriginForTarget(projection, turns, targetTurnID, targetItemID)
	if !ok || origin.StartIndex < 0 || origin.StartIndex >= len(history) {
		return nil, ThreadEditDraft{}, fmt.Errorf("editable user message not found")
	}
	if origin.Item.Type != ThreadItemUserMessage {
		return nil, ThreadEditDraft{}, fmt.Errorf("only regular user messages can be edited")
	}
	msg := history[origin.StartIndex]
	if msg.Steered {
		return nil, ThreadEditDraft{}, fmt.Errorf("only regular user messages can be edited")
	}
	if compactedAttachmentOmission(msg) {
		return nil, ThreadEditDraft{}, fmt.Errorf("this message contains compacted attachment placeholders and cannot be restored for editing")
	}
	return cloneHistory(history[:origin.StartIndex]), editDraftFromChatMessage(msg), nil
}

func editDraftFromChatMessage(msg providers.ChatMessage) ThreadEditDraft {
	draft := ThreadEditDraft{
		Prompt: chatMessageDisplayContent(msg),
		Images: make([]TurnStartImage, 0, len(msg.Images)),
		Files:  make([]TurnStartFile, 0, len(msg.Files)),
	}
	for _, image := range msg.Images {
		data := strings.TrimSpace(image.Data)
		if data == "" {
			continue
		}
		draft.Images = append(draft.Images, TurnStartImage{
			MediaType: strings.TrimSpace(image.MediaType),
			Data:      data,
		})
	}
	for _, file := range msg.Files {
		data := strings.TrimSpace(file.Data)
		if data == "" {
			continue
		}
		draft.Files = append(draft.Files, TurnStartFile{
			MediaType: strings.TrimSpace(file.MediaType),
			Data:      data,
			Filename:  strings.TrimSpace(file.Filename),
		})
	}
	return draft
}

func compactedAttachmentOmission(msg providers.ChatMessage) bool {
	if len(msg.Images) > 0 || len(msg.Files) > 0 {
		return false
	}
	content := strings.ToLower(msg.Content)
	return strings.Contains(content, "attachment omitted from compacted history")
}

func cloneHistory(history []providers.ChatMessage) []providers.ChatMessage {
	return providers.CloneChatMessages(history)
}

func cloneContextSegments(segments []agent.ContextSegment) []agent.ContextSegment {
	if len(segments) == 0 {
		return nil
	}
	out := make([]agent.ContextSegment, 0, len(segments))
	for _, segment := range segments {
		cloned := segment
		cloned.Messages = providers.CloneChatMessages(segment.Messages)
		cloned.Blocks = append([]wuucontext.Block(nil), segment.Blocks...)
		out = append(out, cloned)
	}
	return out
}
