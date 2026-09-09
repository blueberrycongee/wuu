package appserver

import (
	"errors"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestForkHistoryAtToolCallTargetKeepsWholeToolBatch(t *testing.T) {
	threadID := "thread-tool-batch"
	history := []providers.ChatMessage{
		{Role: "user", Content: "check alignment"},
		{
			Role:    "assistant",
			Content: "I will inspect both files.",
			Phase:   providers.MessagePhaseCommentary,
			ToolCalls: []providers.ToolCall{
				{ID: "call_read", Name: "read_file", Arguments: `{"path":"desktop/src/renderer/styles/sidebar.css"}`},
				{ID: "call_grep", Name: "bash", Arguments: `{"command":"grep -n composer desktop/src/renderer/styles/composer.css"}`},
			},
		},
		{Role: "tool", ToolCallID: "call_read", ToolInvocationID: "invocation_read", Name: "read_file", Content: "sidebar css"},
		{Role: "tool", ToolCallID: "call_grep", ToolInvocationID: "invocation_grep", Name: "bash", Content: "composer css"},
		{
			Role:    "assistant",
			Content: "Use the same bottom padding.",
			Phase:   providers.MessagePhaseFinalAnswer,
		},
	}
	turns := turnsFromHistory(threadID, history, time.Unix(0, 0).UTC())
	if len(turns) != 1 {
		t.Fatalf("expected one turn, got %+v", turns)
	}
	targetItem := itemBySourceIDForForkTest(t, turns[0], "call_read")

	forked, err := forkHistoryAtTarget(history, threadID, turns, turns[0].ID, targetItem.ID)
	if err != nil {
		t.Fatalf("forkHistoryAtTarget returned error: %v", err)
	}

	visible := visibleMessagesForTest(forked)
	if len(visible) != 4 {
		t.Fatalf("expected fork to include user, assistant, and both tool results, got %+v", visible)
	}
	if visible[1].Role != "assistant" || len(visible[1].ToolCalls) != 2 {
		t.Fatalf("expected assistant tool-call batch, got %+v", visible[1])
	}
	if visible[2].Role != "tool" || visible[2].ToolCallID != "call_read" {
		t.Fatalf("expected first tool result, got %+v", visible[2])
	}
	if visible[3].Role != "tool" || visible[3].ToolCallID != "call_grep" {
		t.Fatalf("expected second tool result, got %+v", visible[3])
	}
	if visible[2].ToolInvocationID != "" || visible[3].ToolInvocationID != "" {
		t.Fatalf("fork must not retain source tool invocation ownership: %+v", visible)
	}
}

func TestForkDoesNotUseRecycledPositionWhenIdentityIsMissing(t *testing.T) {
	threadID := "thread-recycled-position"
	full := []providers.ChatMessage{
		{Seq: 1, Role: "user", Content: "older prompt"},
		{Seq: 2, Role: "assistant", Content: "selected answer", Phase: providers.MessagePhaseFinalAnswer, ProviderItemID: "selected"},
		{Seq: 3, Role: "user", Content: "later prompt"},
		{Seq: 4, Role: "assistant", Content: "later answer", Phase: providers.MessagePhaseFinalAnswer, ProviderItemID: "later"},
	}
	turns := turnsFromHistory(threadID, full, time.Unix(0, 0).UTC())
	turn, item := finalAnswerItemForForkTest(t, turns, "selected answer")
	for _, target := range []ThreadItem{item, {Type: item.Type, SourceID: item.SourceID}, {Type: item.Type, Seq: item.Seq}} {
		_, err := forkHistoryAtTargetWithIdentity(full[2:], threadID, turns, turn.ID, item.ID, target)
		if !errors.Is(err, errForkTargetNotFound) {
			t.Fatalf("missing identity %+v resolved to a recycled position: %v", target, err)
		}
	}
}

func TestForkHistoryAtFinalAnswerAfterCompaction(t *testing.T) {
	threadID := "thread-compaction"
	history := []providers.ChatMessage{
		{Role: "user", Content: "first prompt"},
		{Role: "assistant", Content: "first answer", Phase: providers.MessagePhaseFinalAnswer},
		{Role: "system", Content: compact.ConversationSummaryPrefix + "\nEarlier context"},
		{Role: "user", Content: "second prompt"},
		{Role: "assistant", Content: "working", Phase: providers.MessagePhaseCommentary},
		{Role: "assistant", Content: "second answer", Phase: providers.MessagePhaseFinalAnswer},
	}
	turns := turnsFromHistory(threadID, history, time.Unix(0, 0).UTC())
	targetTurn, targetItem := finalAnswerItemForForkTest(t, turns, "second answer")

	forked, err := forkHistoryAtTarget(history, threadID, turns, targetTurn.ID, targetItem.ID)
	if err != nil {
		t.Fatalf("forkHistoryAtTarget returned error: %v", err)
	}
	if last := forked[len(forked)-1]; last.Role != "assistant" || last.Content != "second answer" {
		t.Fatalf("fork stopped before the selected final answer: %+v", forked)
	}
}

func TestForkHistoryUsesStableOriginsAcrossProviderCheckpoint(t *testing.T) {
	threadID := "thread-provider-checkpoint"
	displayHistory := []providers.ChatMessage{
		{Seq: 1, Role: "user", Content: "older prompt"},
		{Seq: 2, Role: "assistant", Content: "older answer", Phase: providers.MessagePhaseFinalAnswer},
		{Seq: 3, Role: "system", Content: compact.ConversationSummaryPrefix + "\nOlder context"},
		{Seq: 4, Role: "user", Content: "recent prompt"},
		{Seq: 5, Role: "assistant", Content: "recent answer", Phase: providers.MessagePhaseFinalAnswer},
	}
	providerHistory := displayHistory[2:]
	turns := turnsFromHistory(threadID, displayHistory, time.Unix(0, 0).UTC())
	recentTurn, recentItem := finalAnswerItemForForkTest(t, turns, "recent answer")

	forked, err := forkHistoryAtTarget(providerHistory, threadID, turns, recentTurn.ID, recentItem.ID)
	if err != nil {
		t.Fatalf("fork retained target after provider checkpoint: %v", err)
	}
	if last := forked[len(forked)-1]; last.Seq != 5 || last.Content != "recent answer" {
		t.Fatalf("fork resolved the wrong retained origin: %+v", forked)
	}

	olderTurn, olderItem := finalAnswerItemForForkTest(t, turns, "older answer")
	if _, err := forkHistoryAtTarget(providerHistory, threadID, turns, olderTurn.ID, olderItem.ID); !errors.Is(err, errForkTargetNotFound) {
		t.Fatalf("provider history should not claim an origin before its checkpoint, got %v", err)
	}
	forked, err = forkHistoryAtTarget(displayHistory, threadID, turns, olderTurn.ID, olderItem.ID)
	if err != nil {
		t.Fatalf("fork visible target before provider checkpoint: %v", err)
	}
	if last := forked[len(forked)-1]; last.Seq != 2 || last.Content != "older answer" {
		t.Fatalf("fork resolved the wrong visible origin: %+v", forked)
	}
}

func TestForkRawHistoryResolvesLiveItemIDAfterDisplayPruning(t *testing.T) {
	threadID := "thread-live-checkpoint"
	rawMessages := []providers.ChatMessage{
		{Seq: 1, Role: "user", Content: "older prompt"},
		{
			Seq:              2,
			Role:             "assistant",
			Content:          "checking the repository",
			ReasoningContent: "plan the inspection",
			ToolCalls: []providers.ToolCall{{
				ID: "call-read", Name: "read_file", Arguments: `{"path":"README.md"}`,
			}},
		},
		{Seq: 3, Role: "tool", ToolCallID: "call-read", Content: "contents"},
		{Seq: 4, Role: "assistant", Content: "older answer", Phase: providers.MessagePhaseFinalAnswer},
		{Seq: 5, Role: "system", Content: compact.ConversationSummaryPrefix + "\nOlder context"},
		{Seq: 6, Role: "user", Content: "recent prompt"},
	}
	raw := make([]persistedMessage, 0, len(rawMessages))
	for _, msg := range rawMessages {
		raw = append(raw, persistedMessageFromChatMessage(msg))
	}
	liveTurns := turnsFromPersistedHistory(threadID, raw[:4], time.Unix(0, 0).UTC(), nil)
	targetTurn, targetItem := finalAnswerItemForForkTest(t, liveTurns, "older answer")
	if targetItem.ID != targetTurn.ID+"-item-5" {
		t.Fatalf("live target ID = %q, want item-5 after reasoning and tool items", targetItem.ID)
	}
	// Live items created from stream events in v0.11.1 did not carry a durable
	// seq or provider source id, so only their original positional id survives.
	targetItem.Seq = 0
	targetItem.SourceID = ""
	for turnIndex := range liveTurns {
		for itemIndex := range liveTurns[turnIndex].Items {
			if liveTurns[turnIndex].Items[itemIndex].ID == targetItem.ID {
				liveTurns[turnIndex].Items[itemIndex].Seq = 0
				liveTurns[turnIndex].Items[itemIndex].SourceID = ""
			}
		}
	}

	display := displayHistoryAcrossProviderCheckpoint(raw, raw[4:])
	displayMessages := chatMessagesFromPersistedMessages(display)
	if _, err := forkHistoryAtTargetWithIdentity(displayMessages, threadID, liveTurns, targetTurn.ID, targetItem.ID, targetItem); !errors.Is(err, errForkTargetNotFound) {
		t.Fatalf("pruned display history unexpectedly resolved stale live ID: %v", err)
	}

	forked, err := forkPersistedHistoryAtTarget(raw, threadID, liveTurns, targetTurn.ID, targetItem.ID, targetItem)
	if err != nil {
		t.Fatalf("fork raw history at stale live ID: %v", err)
	}
	if last := forked[len(forked)-1]; last.Seq != 4 || last.Content != "older answer" {
		t.Fatalf("fork resolved the wrong raw origin: %+v", forked)
	}
}

func TestForkHistoryAtFinalAnswerSkipsRetiredContextArtifacts(t *testing.T) {
	threadID := "thread-retired-context"
	history := []providers.ChatMessage{
		{Role: "user", Content: "prompt"},
		{Role: "user", Name: "wuu_context_anchor", Content: "<system>CHECKPOINT 0</system>"},
		{Role: "assistant", Content: "working", Phase: providers.MessagePhaseCommentary},
		{Role: "assistant", Content: "answer", Phase: providers.MessagePhaseFinalAnswer},
	}
	turns := turnsFromHistory(threadID, history, time.Unix(0, 0).UTC())
	targetTurn, targetItem := finalAnswerItemForForkTest(t, turns, "answer")

	forked, err := forkHistoryAtTarget(history, threadID, turns, targetTurn.ID, targetItem.ID)
	if err != nil {
		t.Fatalf("forkHistoryAtTarget returned error: %v", err)
	}
	if last := forked[len(forked)-1]; last.Role != "assistant" || last.Content != "answer" {
		t.Fatalf("fork stopped before the selected final answer: %+v", forked)
	}
}

func TestEditHistorySkipsRetiredContextArtifactsWithoutSequenceIDs(t *testing.T) {
	threadID := "thread-edit-retired-context"
	history := []providers.ChatMessage{
		{Role: "user", Content: "first prompt"},
		{Role: "assistant", Content: "first answer", Phase: providers.MessagePhaseFinalAnswer},
		{Role: "user", Name: "wuu_context_anchor", Content: "<system>CHECKPOINT 0</system>"},
		{Role: "user", Content: "second prompt"},
	}
	turns := turnsFromHistory(threadID, history, time.Unix(0, 0).UTC())
	targetTurn := turns[len(turns)-1]
	targetItem := targetTurn.Items[0]

	prefix, draft, err := editHistoryBeforeUserMessage(history, threadID, turns, targetTurn.ID, targetItem.ID)
	if err != nil {
		t.Fatalf("editHistoryBeforeUserMessage returned error: %v", err)
	}
	if draft.Prompt != "second prompt" {
		t.Fatalf("edit selected internal context artifact instead of user message: %+v", draft)
	}
	if len(prefix) != 3 {
		t.Fatalf("edit history stopped at the wrong message: %+v", prefix)
	}
}

func TestForkLiveAnswerHistoryIncludesCompletedToolsAndFinalAnswer(t *testing.T) {
	base := []providers.ChatMessage{{Role: "user", Content: "inspect it"}}
	turn := Turn{
		ID:     "turn-live",
		Status: TurnStatusInProgress,
		Items: []ThreadItem{
			{ID: "user", Type: ThreadItemUserMessage, Status: ThreadItemStatusCompleted, Text: "inspect it"},
			{ID: "tool", SourceID: "call-1", Type: ThreadItemToolCall, Status: ThreadItemStatusCompleted, Name: "read_file", Arguments: `{"path":"a.txt"}`, Result: "contents"},
			{ID: "answer", SourceID: "msg-1", Type: ThreadItemAgentMessage, Status: ThreadItemStatusCompleted, Terminal: true, Text: "done"},
		},
	}

	forked, err := forkLiveAnswerHistory(base, turn, "answer")
	if err != nil {
		t.Fatalf("forkLiveAnswerHistory: %v", err)
	}
	if len(forked) != 4 {
		t.Fatalf("forked history = %+v", forked)
	}
	if len(forked[1].ToolCalls) != 1 || forked[1].ToolCalls[0].ID != "call-1" {
		t.Fatalf("tool call = %+v", forked[1])
	}
	if forked[2].Role != "tool" || forked[2].ToolCallID != "call-1" || forked[2].Content != "contents" {
		t.Fatalf("tool result = %+v", forked[2])
	}
	if forked[3].Role != "assistant" || forked[3].Content != "done" || forked[3].Phase != providers.MessagePhaseFinalAnswer || forked[3].ProviderItemID != "msg-1" {
		t.Fatalf("final answer = %+v", forked[3])
	}
}

func TestForkLiveAnswerHistoryRejectsIncompleteTool(t *testing.T) {
	turn := Turn{
		ID:     "turn-live",
		Status: TurnStatusInProgress,
		Items: []ThreadItem{
			{ID: "tool", SourceID: "call-1", Type: ThreadItemToolCall, Status: ThreadItemStatusInProgress},
			{ID: "answer", Type: ThreadItemAgentMessage, Status: ThreadItemStatusCompleted, Terminal: true, Text: "done"},
		},
	}
	if _, err := forkLiveAnswerHistory(nil, turn, "answer"); !errors.Is(err, errForkToolResultsNotFound) {
		t.Fatalf("error = %v, want %v", err, errForkToolResultsNotFound)
	}
}

func finalAnswerItemForForkTest(t *testing.T, turns []Turn, text string) (Turn, ThreadItem) {
	t.Helper()
	for _, turn := range turns {
		for _, item := range turn.Items {
			if item.Type == ThreadItemAgentMessage && item.Terminal && item.Text == text {
				return turn, item
			}
		}
	}
	t.Fatalf("final answer %q not found in turns %+v", text, turns)
	return Turn{}, ThreadItem{}
}

func itemBySourceIDForForkTest(t *testing.T, turn Turn, sourceID string) ThreadItem {
	t.Helper()
	for _, item := range turn.Items {
		if item.SourceID == sourceID {
			return item
		}
	}
	t.Fatalf("item with source id %q not found in turn %+v", sourceID, turn)
	return ThreadItem{}
}
