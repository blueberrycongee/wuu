package appserver

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestThreadSnapshotExposesInterruptedOrchestration(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/tmp", false, now)
	th.workerTreeFrozen = true

	snapshot := th.snapshotLocked()
	if !snapshot.TreeInterrupted {
		t.Fatal("snapshot must expose the frozen worker tree as an interrupted orchestration")
	}
}

func TestThreadStateStreamSnapshotsSurviveReplaceAndCompletion(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	for _, kind := range []string{"content", "reasoning", "arguments"} {
		t.Run(kind, func(t *testing.T) {
			th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
			th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)
			delta, replace := providers.EventContentDelta, providers.EventContentReplace
			if kind == "reasoning" {
				delta, replace = providers.EventThinkingDelta, providers.EventThinkingReplace
			} else if kind == "arguments" {
				delta, replace = providers.EventToolUseDelta, providers.EventToolUseStart
				th.applyStreamEventLocked("turn", providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: "call", Name: "read_file"}}, now)
			}
			read := func(snapshot Thread) string {
				items := snapshot.Turns[0].Items
				item := items[len(items)-1]
				if kind == "arguments" {
					return item.Arguments
				}
				return item.Text
			}
			th.applyStreamEventLocked("turn", providers.StreamEvent{Type: delta, Content: "abcdefgh"}, now)
			beforeAppend := th.snapshotLocked()
			th.applyStreamEventLocked("turn", providers.StreamEvent{Type: delta, Content: "ijk"}, now)
			beforeReplace := th.snapshotLocked()
			th.applyStreamEventLocked("turn", providers.StreamEvent{
				Type: replace, Content: "new-", ToolCall: &providers.ToolCall{ID: "call", Name: "read_file", Arguments: "new-"},
			}, now)
			th.applyStreamEventLocked("turn", providers.StreamEvent{Type: delta, Content: "tail"}, now)
			if got := read(th.snapshotLocked()); got != "new-tail" {
				t.Fatalf("replacement followed by delta = %q", got)
			}
			th.finishTurnLocked("turn", TurnStatusCompleted, nil, now, "stop", "", false)
			if got := read(th.snapshotLocked()); got != "new-tail" {
				t.Fatalf("completed snapshot = %q", got)
			}
			th.startTurnLocked("next", providers.ChatMessage{Role: "user", Content: "continue"}, now)
			th.applyStreamEventLocked("next", providers.StreamEvent{Type: providers.EventContentDelta, Content: "another answer"}, now)
			if read(beforeAppend) != "abcdefgh" || read(beforeReplace) != "abcdefghijk" {
				t.Fatal("later stream events mutated an earlier snapshot")
			}
		})
	}
}

func TestEmptyTurnsKeepItemsAsAnArray(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	tests := []struct {
		name  string
		start func(*threadState) Turn
	}{
		{
			name: "internal turn",
			start: func(th *threadState) Turn {
				return th.startInternalTurnLocked("turn", now)
			},
		},
		{
			name: "agent turn",
			start: func(th *threadState) Turn {
				turn, _ := th.startAgentTurnLocked(now)
				return turn
			},
		},
		{
			name: "ensured turn",
			start: func(th *threadState) Turn {
				return th.ensureTurnLocked("turn", now)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			turn := test.start(newThreadState("thread", nil, "provider", "model", "/repo", false, now))
			if turn.Items == nil {
				t.Fatal("empty turn items must be an initialized slice")
			}
		})
	}
}

func TestTurnJSONEncodesNilItemsAsEmptyArray(t *testing.T) {
	encoded, err := json.Marshal(Turn{ID: "turn"})
	if err != nil {
		t.Fatalf("marshal turn: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal turn JSON: %v", err)
	}
	if got := string(payload["items"]); got != "[]" {
		t.Fatalf("items JSON = %s, want []", got)
	}
}

func TestThreadStateRetainsExecutionLeaseUntilRelease(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)
	th.cancel = func() {}

	turn := th.finishTurnLocked("turn", TurnStatusCompleted, nil, now.Add(time.Second), "stop", "", false)
	if turn.Status != TurnStatusCompleted {
		t.Fatalf("turn status = %q, want completed", turn.Status)
	}
	if !th.running || th.currentTurn != "turn" || th.cancel == nil {
		t.Fatalf("finishing should retain execution lease: running=%v current=%q cancel_nil=%v", th.running, th.currentTurn, th.cancel == nil)
	}

	th.releaseTurnExecutionLocked("turn")
	if th.running || th.currentTurn != "" || th.cancel != nil {
		t.Fatalf("release should make thread idle: running=%v current=%q cancel_nil=%v", th.running, th.currentTurn, th.cancel == nil)
	}
}

func TestCurrentTurnIsAnswerReadyFromCompletedTerminalItem(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)
	th.Turns[0].Items = append(th.Turns[0].Items, ThreadItem{
		ID:       "answer",
		Type:     ThreadItemAgentMessage,
		Status:   ThreadItemStatusCompleted,
		Terminal: true,
		Text:     "done",
	})
	if !currentTurnIsAnswerReadyLocked(th) {
		t.Fatal("completed terminal agent message should make the current turn answer-ready")
	}
}

func TestThreadStateMarksAnswerReadyBeforeTurnSettlement(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)

	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventMessage,
		Message: &providers.ChatMessage{
			Role:    "assistant",
			Content: "done",
			Phase:   providers.MessagePhaseFinalAnswer,
		},
	}, now.Add(time.Second))

	turn := th.ensureTurnLocked("turn", now)
	if turn.Status != TurnStatusInProgress {
		t.Fatalf("turn status = %q, want in_progress", turn.Status)
	}
	if turn.AnswerReadyAt == nil || !turn.AnswerReadyAt.Equal(now.Add(time.Second)) {
		t.Fatalf("answer_ready_at = %v", turn.AnswerReadyAt)
	}
	if !th.running || th.currentTurn != "turn" {
		t.Fatalf("answer readiness released execution: running=%v current=%q", th.running, th.currentTurn)
	}
}

func TestThreadStateMarksStreamedFinalAnswerTerminalOnTurnCompletion(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)
	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:    providers.EventContentDelta,
		Content: "The final answer.",
	}, now)

	turn := th.finishTurnLocked("turn", TurnStatusCompleted, nil, now.Add(time.Second), "stop", "", false)
	if len(turn.Items) != 2 {
		t.Fatalf("turn items = %+v, want user and assistant", turn.Items)
	}
	answer := turn.Items[1]
	if answer.Type != ThreadItemAgentMessage || answer.Status != ThreadItemStatusCompleted || !answer.Terminal {
		t.Fatalf("final streamed answer = %+v, want completed terminal agent message", answer)
	}
}

func TestThreadStateCompletesPreambleBeforeToolStart(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)

	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:    providers.EventContentDelta,
		Content: "I will inspect the current prompt path.",
	}, now)
	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventToolUseStart,
		ToolCall: &providers.ToolCall{
			ID:   "call_1",
			Name: "read_file",
		},
	}, now)

	turn := th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 3 {
		t.Fatalf("expected user, preamble, and tool items, got %+v", turn.Items)
	}
	if turn.Items[1].Type != ThreadItemAgentMessage || turn.Items[1].Status != ThreadItemStatusCompleted {
		t.Fatalf("preamble should be a completed assistant item before the tool row, got %+v", turn.Items[1])
	}
	if turn.Items[1].Terminal {
		t.Fatalf("preamble should not be terminal, got %+v", turn.Items[1])
	}
	if turn.Items[2].Type != ThreadItemToolCall || turn.Items[2].Status != ThreadItemStatusInProgress {
		t.Fatalf("tool should follow the completed preamble, got %+v", turn.Items[2])
	}

	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventMessage,
		Message: &providers.ChatMessage{
			Role:    "assistant",
			Content: "I will inspect the current prompt path.",
			ToolCalls: []providers.ToolCall{{
				ID:   "call_1",
				Name: "read_file",
			}},
		},
	}, now)

	turn = th.ensureTurnLocked("turn", now)
	agentItems := 0
	for _, item := range turn.Items {
		if item.Type == ThreadItemAgentMessage {
			agentItems++
		}
	}
	if agentItems != 1 {
		t.Fatalf("final assistant message should not duplicate streamed preamble, got %+v", turn.Items)
	}
}

func TestThreadStatePreservesRichToolResultDetail(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)
	call := providers.ToolCall{ID: "call-1", Name: "browser_observe"}
	th.applyStreamEventLocked("turn", providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &call}, now)
	detail := toolresult.Result{
		Content:           []toolresult.ContentPart{{Type: "image", Data: "aW1hZ2U=", MIMEType: "image/png"}},
		StructuredContent: []byte(`{"url":"https://example.test"}`),
		Meta:              []byte(`{"private":true}`),
	}
	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:             providers.EventToolUseEnd,
		ToolCall:         &call,
		ToolResult:       detail.TextProjection(),
		ToolResultDetail: &detail,
	}, now)

	turn := th.ensureTurnLocked("turn", now)
	item := turn.Items[len(turn.Items)-1]
	if item.ResultDetail == nil || item.ResultDetail.JSONProjection() != detail.JSONProjection() {
		t.Fatalf("rich tool detail missing from item: %+v", item)
	}
}

func TestTurnsFromHistoryPreservesRichToolResultDetail(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	detail := toolresult.Result{
		Content: []toolresult.ContentPart{{
			Type: toolresult.ContentTypeText,
			Text: "Success. Updated the following files:\nM desktop/src/renderer/MessageActions.tsx",
		}},
		StructuredContent: json.RawMessage(`{"files":[{"path":"desktop/src/renderer/MessageActions.tsx","action":"update"}]}`),
	}
	turns := turnsFromHistory("thread", []providers.ChatMessage{
		{Role: "user", Content: "fix the alignment"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{
			ID:   "call-1",
			Name: "apply_patch",
		}}},
		{
			Role:       "tool",
			Name:       "apply_patch",
			ToolCallID: "call-1",
			Content:    detail.TextProjection(),
			ToolResult: &detail,
		},
		{Role: "assistant", Content: "Fixed."},
	}, now)

	if len(turns) != 1 {
		t.Fatalf("expected one turn, got %+v", turns)
	}
	for _, item := range turns[0].Items {
		if item.Type != ThreadItemToolCall || item.Name != "apply_patch" {
			continue
		}
		if item.ResultDetail == nil || item.ResultDetail.JSONProjection() != detail.JSONProjection() {
			t.Fatalf("history projection lost rich tool detail: %+v", item)
		}
		return
	}
	t.Fatalf("apply_patch item missing from projected turn: %+v", turns[0].Items)
}

func TestThreadStateDiscardsToolDraftsOnInferenceReplay(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)
	calls := []providers.ToolCall{
		{ID: "call-1", Name: "read_file"},
		{ID: "call-2", Name: "grep"},
	}
	for i := range calls {
		th.applyStreamEventLocked("turn", providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &calls[i]}, now)
	}
	out := th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventLifecycle,
		Lifecycle: &providers.StreamLifecycle{
			Phase: providers.StreamPhaseReconnecting, ResetPartial: true,
		},
	}, now.Add(time.Second))

	turn := th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 2 || turn.Items[0].Type != ThreadItemUserMessage ||
		turn.Items[1].Type != ThreadItemStreamReconnect || turn.Items[1].Status != ThreadItemStatusInProgress {
		t.Fatalf("discarded tool drafts remained in turn = %+v", turn.Items)
	}
	// The reconnecting event upserts the reconnect row first, then notifies
	// one removal per discarded tool draft.
	if len(out) != 1+len(calls) {
		t.Fatalf("discard notifications = %+v", out)
	}
	if out[0].method != NotificationItemStarted {
		t.Fatalf("reconnect row upsert notification = %+v, want item/started", out[0])
	}
	for i, notification := range out[1:] {
		if notification.method != NotificationItemRemoved {
			t.Fatalf("discard notification %d = %+v", i, notification)
		}
		removed, ok := notification.params.(ItemRemovedNotification)
		if !ok || removed.ItemID == "" {
			t.Fatalf("discard notification params %d = %#v", i, notification.params)
		}
	}
	for _, call := range calls {
		if _, exists := th.toolItems[call.ID]; exists {
			t.Fatalf("discarded provider call id %q remained registered", call.ID)
		}
	}

	replacement := providers.ToolCall{ID: "call-3", Name: "read_file", Arguments: `{"path":"README.md"}`}
	th.applyStreamEventLocked("turn", providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &replacement}, now.Add(2*time.Second))
	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventToolUseEnd, ToolCall: &replacement, ToolResult: "done",
	}, now.Add(3*time.Second))
	turn = th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 3 || turn.Items[2].SourceID != replacement.ID || turn.Items[2].Status != ThreadItemStatusCompleted {
		t.Fatalf("successful retry turn = %+v", turn.Items)
	}
}

func TestThreadStateRetryTerminalFailureDoesNotRestoreDiscardedToolDraft(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)
	call := providers.ToolCall{ID: "call-1", Name: "read_file"}
	th.applyStreamEventLocked("turn", providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &call}, now)
	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventLifecycle,
		Lifecycle: &providers.StreamLifecycle{
			Phase: providers.StreamPhaseReconnecting, ResetPartial: true,
		},
	}, now.Add(time.Second))

	turn := th.finishTurnLocked("turn", TurnStatusFailed, errors.New("provider unavailable"), now.Add(2*time.Second), "", "", false)
	if len(turn.Items) != 2 || turn.Items[0].Type != ThreadItemUserMessage ||
		turn.Items[1].Type != ThreadItemStreamReconnect || turn.Items[1].Status != ThreadItemStatusFailed {
		t.Fatalf("terminal retry failure restored discarded tool draft = %+v", turn.Items)
	}
	if turn.Error == nil || turn.Error.Message != "provider unavailable" {
		t.Fatalf("terminal provider failure missing from turn = %+v", turn)
	}
}

func TestThreadStateLeavesUnresolvedTextPhaseUnknown(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)

	out := th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:    providers.EventContentDelta,
		Content: "I will inspect the current prompt path.",
	}, now)

	turn := th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 2 {
		t.Fatalf("expected user and live assistant items, got %+v", turn.Items)
	}
	live := turn.Items[1]
	if live.Type != ThreadItemAgentMessage || live.Status != ThreadItemStatusInProgress {
		t.Fatalf("expected live assistant item, got %+v", live)
	}
	if live.Terminal {
		t.Fatalf("unresolved assistant text should not be terminal, got %+v", live)
	}
	started, ok := out[0].params.(ItemStartedNotification)
	if !ok || started.Item.Terminal {
		t.Fatalf("started notification should leave the item non-terminal, got %#v", out[0].params)
	}
}

func TestThreadStateDoesNotPromoteProvisionalFinalAnswer(t *testing.T) {
	for _, eventType := range []providers.StreamEventType{providers.EventContentDelta, providers.EventContentReplace} {
		t.Run(string(eventType), func(t *testing.T) {
			now := time.Unix(0, 0).UTC()
			th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
			th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)
			// DeepSeek Responses labels the live message final_answer, then
			// revises it to commentary when the following tool call is known.
			out := th.applyStreamEventLocked("turn", providers.StreamEvent{
				Type: eventType, Content: "I will read the version first.", Phase: providers.MessagePhaseFinalAnswer,
			}, now)
			started, ok := out[0].params.(ItemStartedNotification)
			if !ok || started.Item.Terminal {
				t.Fatalf("provisional phase must not request answer handoff: %#v", out)
			}
			itemID := started.Item.ID
			th.applyStreamEventLocked("turn", providers.StreamEvent{
				Type: providers.EventContentDelta, Phase: providers.MessagePhaseCommentary,
			}, now)
			call := providers.ToolCall{ID: "read-1", Name: "read_file"}
			th.applyStreamEventLocked("turn", providers.StreamEvent{
				Type: providers.EventToolUseStart, ToolCall: &call,
			}, now)
			item, ok := th.itemLocked("turn", itemID)
			if !ok || item.Terminal || item.Text != "I will read the version first." {
				t.Fatalf("tool handoff lost or promoted process text: %+v", item)
			}
			th.applyStreamEventLocked("turn", providers.StreamEvent{
				Type:    providers.EventMessage,
				Message: &providers.ChatMessage{Role: "assistant", Content: item.Text, Phase: providers.MessagePhaseCommentary, ToolCalls: []providers.ToolCall{call}},
			}, now)
			th.applyStreamEventLocked("turn", providers.StreamEvent{
				Type: providers.EventContentDelta, Content: "The version is current.", Phase: providers.MessagePhaseFinalAnswer,
			}, now)
			turn := th.ensureTurnLocked("turn", now)
			if turn.AnswerReadyAt != nil || turn.Items[len(turn.Items)-1].Terminal {
				t.Fatalf("answer handoff happened before message completion: %+v", turn)
			}
			th.applyStreamEventLocked("turn", providers.StreamEvent{
				Type:    providers.EventMessage,
				Message: &providers.ChatMessage{Role: "assistant", Content: "The version is current.", Phase: providers.MessagePhaseFinalAnswer},
			}, now)
			turn = th.ensureTurnLocked("turn", now)
			answer := turn.Items[len(turn.Items)-1]
			if !answer.Terminal || answer.Status != ThreadItemStatusCompleted || turn.AnswerReadyAt == nil {
				t.Fatalf("confirmed answer did not hand off: %+v", turn)
			}
		})
	}
}

func TestThreadStateCompletesPhasedProviderMessageBeforeTurnSettlement(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)

	out := th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventMessage,
		Message: &providers.ChatMessage{
			Role:    "assistant",
			Content: "The result is clear.",
			Phase:   providers.MessagePhaseFinalAnswer,
		},
	}, now)

	turn := th.ensureTurnLocked("turn", now)
	answer := turn.Items[len(turn.Items)-1]
	if answer.Status != ThreadItemStatusCompleted || !answer.Terminal {
		t.Fatalf("completed final answer = %+v, want completed terminal item", answer)
	}
	if got := countNotifications(out, NotificationItemCompleted); got != 1 {
		t.Fatalf("item/completed notifications = %d, want 1", got)
	}
}

func TestThreadStateKeepsCompletedCommentaryOutOfFinalAnswer(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)

	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventMessage,
		Message: &providers.ChatMessage{
			Role:    "assistant",
			Content: "I will inspect that now.",
			Phase:   providers.MessagePhaseCommentary,
		},
	}, now)

	turn := th.ensureTurnLocked("turn", now)
	commentary := turn.Items[len(turn.Items)-1]
	if commentary.Status != ThreadItemStatusCompleted || commentary.Terminal {
		t.Fatalf("completed commentary = %+v, want completed non-terminal item", commentary)
	}
}

func TestThreadStateToolResultMessageDoesNotDuplicateCompletion(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)

	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:     providers.EventToolUseStart,
		ToolCall: &providers.ToolCall{ID: "call_1", Name: "read_file"},
	}, now)
	// The agent loop first streams the (display-truncated) tool result...
	out := th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:       providers.EventToolUseEnd,
		ToolCall:   &providers.ToolCall{ID: "call_1", Name: "read_file"},
		ToolResult: "package appserver",
	}, now)
	if got := countNotifications(out, NotificationItemCompleted); got != 1 {
		t.Fatalf("tool-use end should complete the item once, got %d", got)
	}
	// ...then forwards the recorded history message for the same call.
	out = th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:    providers.EventMessage,
		Message: &providers.ChatMessage{Role: "tool", ToolCallID: "call_1", Content: "package appserver\n\nfunc more() {}"},
	}, now)
	if got := countNotifications(out, NotificationItemCompleted); got != 0 {
		t.Fatalf("tool history message must not re-announce completion, got %d notifications: %#v", got, out)
	}

	turn := th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 2 {
		t.Fatalf("expected user and tool items, got %+v", turn.Items)
	}
	toolItem := turn.Items[1]
	if toolItem.Result != "package appserver\n\nfunc more() {}" {
		t.Fatalf("tool result should be upgraded to full message content without doubling, got %q", toolItem.Result)
	}
	if toolItem.Status != ThreadItemStatusCompleted {
		t.Fatalf("tool item should stay completed, got %+v", toolItem)
	}
}

func TestThreadStateToolResultMessageAloneCompletesItem(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)

	// No streamed tool-result event (e.g. replayed or non-streaming path):
	// the history message must still create and complete the item.
	out := th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:    providers.EventMessage,
		Message: &providers.ChatMessage{Role: "tool", ToolCallID: "call_9", Content: "done"},
	}, now)
	if got := countNotifications(out, NotificationItemCompleted); got != 1 {
		t.Fatalf("tool message without prior stream event should complete the item once, got %d", got)
	}
	turn := th.ensureTurnLocked("turn", now)
	toolItem := turn.Items[len(turn.Items)-1]
	if toolItem.Result != "done" || toolItem.Status != ThreadItemStatusCompleted {
		t.Fatalf("unexpected tool item %+v", toolItem)
	}
}

func countNotifications(out []outboundNotification, method string) int {
	count := 0
	for _, n := range out {
		if n.method == method {
			count++
		}
	}
	return count
}

func TestThreadStateCarriesToolCallDisplay(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)

	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventToolUseStart,
		ToolCall: &providers.ToolCall{
			ID:      "call_1",
			Name:    "read_file",
			Display: &providers.ToolCallDisplay{Kind: "read", Text: "读取 文件"},
		},
	}, now)
	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventToolUseEnd,
		ToolCall: &providers.ToolCall{
			ID:        "call_1",
			Name:      "read_file",
			Arguments: `{"path":"internal/appserver/model.go"}`,
			Display:   &providers.ToolCallDisplay{Kind: "read", Text: "读取 model.go"},
		},
	}, now)

	turn := th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 2 {
		t.Fatalf("expected user and tool items, got %+v", turn.Items)
	}
	toolItem := turn.Items[1]
	if toolItem.Display == nil || toolItem.Display.Text != "读取 model.go" || toolItem.Display.Kind != "read" {
		t.Fatalf("expected updated display metadata, got %+v", toolItem.Display)
	}
}

func TestThreadStateCarriesProviderToolCallIDAsSourceID(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)

	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventToolUseStart,
		ToolCall: &providers.ToolCall{
			ID:   "call_provider_1",
			Name: "run_shell",
		},
	}, now)

	turn := th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 2 {
		t.Fatalf("expected user and tool items, got %+v", turn.Items)
	}
	toolItem := turn.Items[1]
	if toolItem.ID == "call_provider_1" {
		t.Fatalf("tool item should keep UI item id separate from provider call id: %+v", toolItem)
	}
	if toolItem.SourceID != "call_provider_1" {
		t.Fatalf("tool item SourceID = %q, want provider call id", toolItem.SourceID)
	}
}

func TestChatMessageItemUsesDisplayContentForUserMessage(t *testing.T) {
	item := chatMessageItem("item-1", providers.ChatMessage{
		Role:           "user",
		Content:        "expanded model prompt",
		DisplayContent: "/debug login failure",
	})

	if item.Text != "/debug login failure" {
		t.Fatalf("item.Text = %q, want display content", item.Text)
	}
}

func TestThreadPreviewUsesDisplayContent(t *testing.T) {
	preview := threadPreview([]providers.ChatMessage{{
		Role:           "user",
		Content:        "expanded model prompt",
		DisplayContent: "/debug login failure",
	}})

	if preview != "/debug login failure" {
		t.Fatalf("preview = %q, want display content", preview)
	}
}

func TestThreadPreviewSkipsInternalContextMessages(t *testing.T) {
	preview := threadPreview([]providers.ChatMessage{
		{Role: "user", Name: "wuu_context_anchor", Content: "<system>CHECKPOINT 0</system>", Hidden: true},
		{Role: "user", Name: "wuu_context_continuation", Content: "<system-reminder>\n[Wuu context continuation]\nContinue.\n</system-reminder>", Hidden: true},
		{Role: "user", Name: "wuu_system_reminder", Content: "hidden environment", Hidden: true},
		{Role: "user", Content: "visible request"},
	})

	if preview != "visible request" {
		t.Fatalf("preview = %q, want visible request", preview)
	}
}

func TestThreadPreviewSkipsAgentNotificationEnvelope(t *testing.T) {
	preview := threadPreview([]providers.ChatMessage{
		{
			Role:    "user",
			Name:    "wuu_agent_notification",
			Content: `{"author":"/root/reviewer","recipient":"/root","content":"<subagent_notification>done</subagent_notification>","trigger_turn":true}`,
		},
		{Role: "assistant", Content: "acknowledged"},
		{Role: "user", Content: "检查分叉后的标题"},
	})

	if preview != "检查分叉后的标题" {
		t.Fatalf("preview = %q, want visible user request", preview)
	}
}

func TestThreadPreviewSkipsUnnamedInterAgentMessageEnvelope(t *testing.T) {
	preview := threadPreview([]providers.ChatMessage{
		{
			Role:    "user",
			Content: `{"author":"/root/review_plugin_platform","recipient":"/root","content":"continue with the desktop loader","trigger_turn":false}`,
		},
		{Role: "user", Content: "完成插件平台"},
	})

	if preview != "完成插件平台" {
		t.Fatalf("preview = %q, want visible user request", preview)
	}
}

func TestTurnsFromHistorySkipsHiddenMessages(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	turns := turnsFromHistory("thread", []providers.ChatMessage{
		{Role: "user", Name: "wuu_system_reminder", Content: "hidden environment", Hidden: true},
		{Role: "user", Content: "visible request"},
		{Role: "user", Name: "wuu_task_contract", Content: "hidden task contract", Hidden: true},
		{Role: "assistant", Content: "done"},
	}, now)

	if len(turns) != 1 {
		t.Fatalf("expected one visible turn, got %+v", turns)
	}
	items := turns[0].Items
	if len(items) != 2 {
		t.Fatalf("expected only visible user and assistant items, got %+v", items)
	}
	if items[0].Type != ThreadItemUserMessage || items[0].Text != "visible request" {
		t.Fatalf("expected visible user item, got %+v", items[0])
	}
	if items[1].Type != ThreadItemAgentMessage || items[1].Text != "done" {
		t.Fatalf("expected visible assistant item, got %+v", items[1])
	}
}

func TestTurnsFromHistoryLoadsRetiredContextRewriteArtifact(t *testing.T) {
	const retiredToolName = "inception"
	now := time.Unix(0, 0).UTC()
	turns := turnsFromHistory("thread", []providers.ChatMessage{
		{Role: "user", Content: "start"},
		{Role: "user", Name: "wuu_context_anchor", Content: "<system>CHECKPOINT 0</system>", Hidden: true},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_retired",
				Name:      retiredToolName,
				Arguments: `{"anchor_id":0,"summary":"state"}`,
			}},
		},
		{Role: "tool", Name: retiredToolName, ToolCallID: "call_retired", Content: `{"action":"inception","status":"completed"}`},
		{Role: "user", Name: "wuu_context_continuation", Content: "<system-reminder>\n[Wuu context continuation]\nContinue.\n</system-reminder>", Hidden: true},
		{Role: "assistant", Content: "done"},
	}, now)

	if len(turns) != 1 {
		t.Fatalf("expected one visible turn, got %+v", turns)
	}
	items := turns[0].Items
	if len(items) != 3 {
		t.Fatalf("expected user, retired tool call, and assistant items, got %+v", items)
	}
	if items[0].Type != ThreadItemUserMessage || items[0].Text != "start" {
		t.Fatalf("expected visible user item, got %+v", items[0])
	}
	if items[1].Name != retiredToolName {
		t.Fatalf("expected retired tool call as second item, got %+v", items[1])
	}
	if items[2].Type != ThreadItemAgentMessage || items[2].Text != "done" {
		t.Fatalf("expected visible assistant item, got %+v", items[2])
	}
}

func TestThreadStateUpdatesContextCompactionProgressItem(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "continue"}, now)

	started := th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:          providers.EventCompact,
		CompactReason: "proactive",
		CompactPhase:  providers.CompactPhaseStarted,
	}, now)
	turn := th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 2 || turn.Items[1].Type != ThreadItemContextCompaction || turn.Items[1].Status != ThreadItemStatusInProgress {
		t.Fatalf("compact start should add one in-progress item, got %+v", turn.Items)
	}
	if len(started) != 1 || started[0].method != NotificationItemStarted {
		t.Fatalf("compact start notifications = %+v, want one item/started", started)
	}
	itemID := turn.Items[1].ID

	completed := th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:          providers.EventCompact,
		Content:       "✦ Compacted history: 8 → 3 messages",
		CompactReason: "proactive",
		CompactPhase:  providers.CompactPhaseCompleted,
	}, now.Add(time.Second))
	turn = th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 2 || turn.Items[1].ID != itemID || turn.Items[1].Status != ThreadItemStatusCompleted {
		t.Fatalf("compact completion should update the progress item in place, got %+v", turn.Items)
	}
	if len(completed) != 1 || completed[0].method != NotificationItemCompleted {
		t.Fatalf("compact completion notifications = %+v, want one item/completed", completed)
	}
}

func TestThreadStateFailedContextTransitionSettlesProgress(t *testing.T) {
	for _, phase := range []providers.CompactPhase{providers.CompactPhaseFailed, providers.CompactPhaseCompleted} {
		t.Run(string(phase), func(t *testing.T) {
			now := time.Unix(0, 0).UTC()
			th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
			th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "continue"}, now)
			th.applyStreamEventLocked("turn", providers.StreamEvent{Type: providers.EventCompact, CompactPhase: providers.CompactPhaseStarted}, now)
			id := th.ensureTurnLocked("turn", now).Items[1].ID
			text := "Fresh context could not be installed; active history is unchanged."
			if phase == providers.CompactPhaseFailed {
				text = "arbitrary diagnostic, not a status contract"
			}
			out := th.applyStreamEventLocked("turn", providers.StreamEvent{
				Type: providers.EventCompact, CompactReason: "new_context", CompactPhase: phase, Content: text,
			}, now)
			items := th.ensureTurnLocked("turn", now).Items
			if len(items) != 2 || items[1].ID != id || items[1].Status != ThreadItemStatusFailed {
				t.Fatalf("failed transition must settle the existing row: %+v", items)
			}
			if len(out) != 1 || out[0].method != NotificationItemCompleted {
				t.Fatalf("failure must finish progress: %+v", out)
			}
		})
	}
}

func TestThreadStateStreamReconnectLifecycle(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "go"}, now)

	reconnecting := func(retryCount int, retryIn time.Duration) []outboundNotification {
		return th.applyStreamEventLocked("turn", providers.StreamEvent{
			Type: providers.EventLifecycle,
			Lifecycle: &providers.StreamLifecycle{
				Phase:           providers.StreamPhaseReconnecting,
				Reason:          "connection reset by peer",
				FailureCategory: "network",
				RetryCount:      retryCount,
				MaxRetries:      5,
				RetryIn:         retryIn,
			},
		}, now)
	}

	started := reconnecting(1, 2*time.Second)
	turn := th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 2 || turn.Items[1].Type != ThreadItemStreamReconnect || turn.Items[1].Status != ThreadItemStatusInProgress {
		t.Fatalf("reconnecting should add one in-progress stream_reconnect item, got %+v", turn.Items)
	}
	item := turn.Items[1]
	if item.RetryCount != 1 || item.MaxRetries != 5 || item.RetryAtMS != now.Add(2*time.Second).UnixMilli() {
		t.Fatalf("reconnect row should carry retry counters and deadline, got %+v", item)
	}
	if len(started) != 1 || started[0].method != NotificationItemStarted {
		t.Fatalf("reconnecting notifications = %+v, want one item/started", started)
	}

	// A later attempt refreshes the same row instead of stacking a new one.
	refreshed := reconnecting(2, 4*time.Second)
	turn = th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 2 || turn.Items[1].ID != item.ID || turn.Items[1].RetryCount != 2 {
		t.Fatalf("retry should update the existing row in place, got %+v", turn.Items)
	}
	if len(refreshed) != 1 || refreshed[0].method != NotificationItemStarted {
		t.Fatalf("retry refresh notifications = %+v, want one item/started", refreshed)
	}

	connected := th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:      providers.EventLifecycle,
		Lifecycle: &providers.StreamLifecycle{Phase: providers.StreamPhaseConnected},
	}, now)
	turn = th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 1 {
		t.Fatalf("a successful reconnect must erase the row, got %+v", turn.Items)
	}
	if len(connected) != 1 || connected[0].method != NotificationItemRemoved {
		t.Fatalf("connected notifications = %+v, want one item/removed", connected)
	}
}

func TestThreadStateSettlesStreamReconnectWhenProviderGivesUp(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "go"}, now)

	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventLifecycle,
		Lifecycle: &providers.StreamLifecycle{
			Phase:           providers.StreamPhaseReconnecting,
			Reason:          "connection reset by peer",
			FailureCategory: "network",
			RetryCount:      5,
			MaxRetries:      5,
			RetryIn:         2 * time.Second,
		},
	}, now)

	settled := th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventLifecycle,
		Lifecycle: &providers.StreamLifecycle{
			Phase:           providers.StreamPhaseFailed,
			Reason:          "connection reset by peer",
			FailureCategory: "network",
			RetryCount:      5,
			MaxRetries:      5,
		},
	}, now)
	turn := th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 2 || turn.Items[1].Type != ThreadItemStreamReconnect || turn.Items[1].Status != ThreadItemStatusFailed {
		t.Fatalf("giving up should freeze the row as failed, got %+v", turn.Items)
	}
	item := turn.Items[1]
	if item.RetryCount != 5 || item.MaxRetries != 5 || item.RetryAtMS != 0 {
		t.Fatalf("settled row keeps final counters and clears the deadline, got %+v", item)
	}
	if len(settled) != 1 || settled[0].method != NotificationItemCompleted {
		t.Fatalf("settle notifications = %+v, want one item/completed", settled)
	}
}

func TestThreadStateStreamFailureWithoutReconnectKeepsOrdinaryNotice(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "go"}, now)

	out := th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventLifecycle,
		Lifecycle: &providers.StreamLifecycle{
			Phase:           providers.StreamPhaseFailed,
			Reason:          "prompt too long",
			FailureCategory: "budget",
		},
	}, now)
	if len(out) != 0 {
		t.Fatalf("a failure without a reconnect cycle must not synthesize a row: %+v", out)
	}
	for _, item := range th.ensureTurnLocked("turn", now).Items {
		if item.Type == ThreadItemStreamReconnect {
			t.Fatalf("no stream_reconnect item expected without a reconnect cycle: %+v", item)
		}
	}
}

func TestThreadStateFinishTurnFreezesPendingStreamReconnect(t *testing.T) {
	for _, status := range []TurnStatus{TurnStatusInterrupted, TurnStatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			now := time.Unix(0, 0).UTC()
			th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
			th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "go"}, now)
			th.applyStreamEventLocked("turn", providers.StreamEvent{
				Type: providers.EventLifecycle,
				Lifecycle: &providers.StreamLifecycle{
					Phase:           providers.StreamPhaseReconnecting,
					Reason:          "connection reset by peer",
					FailureCategory: "network",
					RetryCount:      2,
					MaxRetries:      5,
					RetryIn:         3 * time.Second,
				},
			}, now)

			turn := th.finishTurnLocked("turn", status, nil, now, "", "", false)
			if len(turn.Items) != 2 {
				t.Fatalf("turn ending mid-reconnect keeps the frozen row, got %+v", turn.Items)
			}
			item := turn.Items[1]
			if item.Type != ThreadItemStreamReconnect || item.Status != ThreadItemStatusFailed {
				t.Fatalf("pending reconnect row must freeze as failed, got %+v", item)
			}
			if item.RetryCount != 2 || item.MaxRetries != 5 || item.RetryAtMS != 0 {
				t.Fatalf("frozen row stops at the last reported retry, got %+v", item)
			}
		})
	}
}

func TestThreadStateCompletedTurnDropsPendingStreamReconnect(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "go"}, now)
	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventLifecycle,
		Lifecycle: &providers.StreamLifecycle{
			Phase:           providers.StreamPhaseReconnecting,
			Reason:          "flaky",
			FailureCategory: "network",
			RetryCount:      1,
			MaxRetries:      5,
		},
	}, now)
	turn := th.finishTurnLocked("turn", TurnStatusCompleted, nil, now, "", "", false)
	for _, item := range turn.Items {
		if item.Type == ThreadItemStreamReconnect {
			t.Fatalf("a completed turn must not retain a reconnect row: %+v", item)
		}
	}
}

func TestTurnsFromHistoryKeepsSteeredUserMessageInCurrentTurn(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	turns := turnsFromHistory("thread", []providers.ChatMessage{
		{Role: "user", Content: "start"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_1",
				Name:      "read_file",
				Arguments: `{}`,
			}},
		},
		{Role: "tool", ToolCallID: "call_1", Name: "read_file", Content: "file"},
		{Role: "user", ClientID: "steer-1", Content: "steer now", Steered: true},
		{Role: "assistant", Content: "done"},
	}, now)

	if len(turns) != 1 {
		t.Fatalf("steered user message should not create a new turn, got %+v", turns)
	}
	var userItems []ThreadItem
	for _, item := range turns[0].Items {
		if item.Type == ThreadItemUserMessage {
			userItems = append(userItems, item)
		}
	}
	if len(userItems) != 2 {
		t.Fatalf("expected original and steered user items, got %+v", turns[0].Items)
	}
	if userItems[1].Text != "steer now" || userItems[1].SourceID != "steer-1" {
		t.Fatalf("unexpected steered user item: %+v", userItems[1])
	}
}

func TestThreadStateReplacesActiveAgentMessageText(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)

	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:    providers.EventContentDelta,
		Content: "stale partial",
	}, now)
	out := th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventContentReplace,
	}, now)
	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:    providers.EventContentDelta,
		Content: "fresh answer",
	}, now)

	turn := th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 2 {
		t.Fatalf("expected user and active assistant item, got %+v", turn.Items)
	}
	if turn.Items[1].Text != "fresh answer" {
		t.Fatalf("expected stale text to be replaced before new deltas, got %q", turn.Items[1].Text)
	}
	if len(out) != 1 || out[0].method != NotificationAgentMessageReplace {
		t.Fatalf("expected replace notification, got %+v", out)
	}
	params, ok := out[0].params.(AgentMessageReplaceNotification)
	if !ok || params.Text != "" || params.ItemID != turn.Items[1].ID {
		t.Fatalf("unexpected replace params: %#v", out[0].params)
	}
}

func TestThreadStateLeavesPostToolStreamingTextPhaseUnknownOnFirstDelta(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)

	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventToolUseStart,
		ToolCall: &providers.ToolCall{
			ID:   "call_1",
			Name: "read_file",
		},
	}, now)
	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:       providers.EventToolUseEnd,
		ToolCall:   &providers.ToolCall{ID: "call_1", Name: "read_file"},
		ToolResult: "file contents",
	}, now)
	out := th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:    providers.EventContentDelta,
		Content: "The result is clear.",
	}, now)

	turn := th.ensureTurnLocked("turn", now)
	if len(turn.Items) != 3 {
		t.Fatalf("expected user, tool, and live agent items, got %+v", turn.Items)
	}
	streamed := turn.Items[2]
	if streamed.Type != ThreadItemAgentMessage || streamed.Status != ThreadItemStatusInProgress {
		t.Fatalf("post-tool text should start a live assistant item, got %+v", streamed)
	}
	if streamed.Terminal {
		t.Fatalf("post-tool streaming text should not be terminal, got %+v", streamed)
	}
	if len(out) == 0 {
		t.Fatal("expected notifications for first text delta")
	}
	started, ok := out[0].params.(ItemStartedNotification)
	if !ok || started.Item.Terminal {
		t.Fatalf("started notification should leave the item non-terminal, got %#v", out[0].params)
	}
}

func TestThreadStateMovesStreamingTextToFinalAnswerOnAssistantMessage(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)

	// 1. Stream preamble + tool_use + tool_result + streamed "final" text.
	//    While streaming, the assistant item phase is unknown.
	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventToolUseStart,
		ToolCall: &providers.ToolCall{
			ID:   "call_1",
			Name: "read_file",
		},
	}, now)
	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:       providers.EventToolUseEnd,
		ToolCall:   &providers.ToolCall{ID: "call_1", Name: "read_file"},
		ToolResult: "file contents",
	}, now)
	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type:    providers.EventContentDelta,
		Content: "The result is clear.",
	}, now)

	// 2. The complete assistant message arrives (no ToolCalls → final_answer).
	th.applyStreamEventLocked("turn", providers.StreamEvent{
		Type: providers.EventMessage,
		Message: &providers.ChatMessage{
			Role:    "assistant",
			Content: "The result is clear.",
		},
	}, now)

	turn := th.ensureTurnLocked("turn", now)
	var agentItems []ThreadItem
	for _, item := range turn.Items {
		if item.Type == ThreadItemAgentMessage {
			agentItems = append(agentItems, item)
		}
	}
	if len(agentItems) != 1 {
		t.Fatalf("expected exactly one agent item, got %+v", agentItems)
	}
	if !agentItems[0].Terminal {
		t.Fatalf("EventAssistantMessage should mark the no-tool-call message terminal, got %+v", agentItems[0])
	}
	if agentItems[0].Status != ThreadItemStatusCompleted {
		t.Fatalf("EventAssistantMessage should mark the agent item completed, got %+v", agentItems[0])
	}
}

func TestTurnsFromHistoryMarksAssistantMessagePhases(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	turns := turnsFromHistory("thread", []providers.ChatMessage{
		{Role: "user", Content: "inspect"},
		{
			Role:    "assistant",
			Content: "I will inspect first.",
			ToolCalls: []providers.ToolCall{{
				ID:   "call_1",
				Name: "read_file",
			}},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "file contents"},
		{Role: "assistant", Content: "The result is clear."},
	}, now)

	if len(turns) != 1 {
		t.Fatalf("expected one turn, got %+v", turns)
	}
	var terminals []bool
	for _, item := range turns[0].Items {
		if item.Type == ThreadItemAgentMessage {
			terminals = append(terminals, item.Terminal)
		}
	}
	if len(terminals) != 2 || terminals[0] || !terminals[1] {
		t.Fatalf("unexpected assistant terminal flags: %+v", terminals)
	}
}

func TestTurnsFromHistoryPreservesProviderAssistantPhase(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	turns := turnsFromHistory("thread", []providers.ChatMessage{
		{Role: "user", Content: "inspect"},
		{
			Role:    "assistant",
			Content: "I checked the files.",
			Phase:   providers.MessagePhaseCommentary,
		},
	}, now)

	if len(turns) != 1 || len(turns[0].Items) != 2 {
		t.Fatalf("expected one turn with user and assistant, got %+v", turns)
	}
	item := turns[0].Items[1]
	if item.Type != ThreadItemAgentMessage || item.Terminal {
		t.Fatalf("history should preserve explicit commentary as non-terminal, got %+v", item)
	}
}

func TestApplyTokenUsageMetasToTurnsAlignsFromNewestTurn(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	turns := turnsFromHistory("thread", []providers.ChatMessage{
		{Role: "user", Content: "old"},
		{Role: "assistant", Content: "old done"},
		{Role: "user", Content: "new"},
		{Role: "assistant", Content: "new done"},
	}, now)
	turns = applyTokenUsageMetasToTurns(turns, []persistedMessage{{
		Role:            "meta",
		Content:         "token_usage",
		Model:           "minimax-m3",
		InputTokens:     19_600,
		ContextTokens:   88_000,
		CacheReadTokens: 113_000,
	}})

	if len(turns) != 2 {
		t.Fatalf("expected two turns, got %+v", turns)
	}
	if turns[0].InputTokens != 0 || turns[0].CacheReadTokens != 0 {
		t.Fatalf("usage should not attach to legacy first turn: %+v", turns[0])
	}
	if turns[1].InputTokens != 19_600 || turns[1].CacheReadTokens != 113_000 || turns[1].ContextTokens != 88_000 || turns[1].UsageModel != "minimax-m3" {
		t.Fatalf("usage should attach to newest turn: %+v", turns[1])
	}
}

func TestChatMessageInputTextExposesOnlyHiddenDeliveredPrompts(t *testing.T) {
	cases := []struct {
		name string
		msg  providers.ChatMessage
		want string
	}{
		{
			name: "ordinary user message exposes nothing",
			msg:  providers.ChatMessage{Role: "user", Content: "帮我看看这个报错"},
			want: "",
		},
		{
			name: "plugin wake with a hidden prompt exposes the raw input",
			msg: providers.ChatMessage{
				Role:           "user",
				Content:        "internal inspect prompt",
				DisplayContent: "后台任务已唤醒 Agent",
			},
			want: "internal inspect prompt",
		},
		{
			name: "explicit display text equal to content exposes nothing",
			msg: providers.ChatMessage{
				Role:           "user",
				Content:        "visible prompt",
				DisplayContent: "visible prompt",
			},
			want: "",
		},
		{
			name: "empty content exposes nothing",
			msg:  providers.ChatMessage{Role: "user", Content: "   "},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chatMessageInputText(tc.msg); got != tc.want {
				t.Fatalf("chatMessageInputText(%+v) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
}
