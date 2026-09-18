package appserver

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func TestNamedAgentInboxContextCoalescesSnapshots(t *testing.T) {
	now := time.Unix(100, 0)
	summary := channels.InboxSummary{RoomID: "room"}
	peeks := 0
	var peekErr error
	inbox := &namedAgentInboxContext{now: func() time.Time { return now }, peek: func(context.Context) (channels.InboxSummary, error) {
		peeks++
		return summary, peekErr
	}}
	if got := inbox.segments(); len(got) != 0 {
		t.Fatal("empty inbox produced a reminder")
	}
	for count := 1; count <= 2; count++ {
		now = now.Add(time.Second)
		summary.Unread, summary.DeliverySequence = count, int64(count)
		if got := inbox.segments(); len(got) != 0 || peeks != 1 {
			t.Fatalf("burst was sampled before the interval: %+v, peeks=%d", got, peeks)
		}
	}
	now = now.Add(time.Second)
	first := inbox.segments()
	if len(first) != 1 || !strings.Contains(first[0].Messages[0].Content, "2 unread message(s)") || peeks != 2 {
		t.Fatalf("burst was not coalesced: %+v, peeks=%d", first, peeks)
	}
	summary.Unread, summary.DeliverySequence = 3, 3
	if got := inbox.segments(); !reflect.DeepEqual(first, got) {
		t.Fatal("throttling removed or rewrote the last snapshot")
	}
	// A failed refresh retains the snapshot instead of toggling it inactive.
	peekErr = errors.New("temporary store error")
	now = now.Add(namedAgentInboxInterval)
	if got := inbox.segments(); !reflect.DeepEqual(first, got) {
		t.Fatal("failed refresh discarded the previous snapshot")
	}
	peekErr = nil
	now = now.Add(namedAgentInboxInterval)
	if got := inbox.segments(); len(got) != 1 || !strings.Contains(got[0].Messages[0].Content, "3 unread message(s)") {
		t.Fatalf("latest count did not replace the snapshot: %+v", got)
	}
	summary.Unread = 0
	now = now.Add(namedAgentInboxInterval)
	if got := inbox.segments(); len(got) != 0 {
		t.Fatal("received inbox retained an active reminder")
	}
}

func TestNamedAgentInboxContextRefreshesAtTurnStart(t *testing.T) {
	f := newCollaborationRPCFixture(t)
	binding, _ := f.create(t, "Original work")
	client, err := f.server.channelService.BindAgentSession(context.Background(), f.identity.ID, binding.SessionRef)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.channelService.EnqueueSessionInput(context.Background(), channels.CollaborationSessionSendParams{
		SessionRef: binding.SessionRef, Body: "Queued followup", RequestID: "refresh-followup",
	}); err != nil {
		t.Fatal(err)
	}
	baseCalled := false
	rt := &runtime.ThreadRuntime{StreamRunner: &agent.StreamRunner{BeforeModelStep: func(context.Context, int, []providers.ChatMessage) ([]providers.ChatMessage, error) {
		baseCalled = true
		return nil, nil
	}}}
	attachNamedAgentInboxContext(rt, client)
	if got := rt.StreamRunner.BeforeRequestContext(); len(got) != 1 {
		t.Fatalf("followup reminder missing: %+v", got)
	}
	if _, err := client.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rt.StreamRunner.BeforeRequestContext(); len(got) != 1 {
		t.Fatal("snapshot was not retained within the sampling window")
	}
	messages, err := rt.StreamRunner.BeforeModelStep(context.Background(), 0, nil)
	if err != nil || len(messages) != 0 || !baseCalled {
		t.Fatalf("turn refresh changed durable history or bypassed the existing hook: %+v, %v", messages, err)
	}
	if got := rt.StreamRunner.BeforeRequestContext(); len(got) != 0 {
		t.Fatal("new turn reused a stale inbox snapshot")
	}
}

type inboxContextStep func(providers.ChatRequest) agent.StepResult

func (step inboxContextStep) Execute(_ context.Context, request providers.ChatRequest) (agent.StepResult, error) {
	return step(request), nil
}

type inboxContextTools struct{}

func (inboxContextTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "continue_work"}}
}

func (inboxContextTools) Execute(context.Context, providers.ToolCall) (string, error) {
	return "Work continued", nil
}

func TestNamedAgentInboxUpdatesPreserveRequestPrefixAcrossTurns(t *testing.T) {
	now := time.Unix(100, 0)
	summary := channels.InboxSummary{RoomID: "room"}
	inbox := &namedAgentInboxContext{now: func() time.Time { return now }, peek: func(context.Context) (channels.InboxSummary, error) { return summary, nil }}
	var requests []providers.ChatRequest
	var retained *agent.RetainedRequestContextState
	history := []providers.ChatMessage{{Role: "system", Content: "Stable system instructions"}}
	// Empty -> two coalesced messages -> unchanged -> same-count new mail ->
	// cleared -> new mail. Every update must extend, never edit, the prefix.
	for turn := 0; turn < 2; turn++ {
		history = append(history, providers.ChatMessage{Role: "user", Content: "Continue the task"})
		round := 0
		result, err := agent.RunToolLoop(context.Background(), history, agent.LoopConfig{
			Model: "test", Tools: inboxContextTools{}, BeforeRequestContext: inbox.segments, RetainedRequestContext: retained,
		}, inboxContextStep(func(request providers.ChatRequest) agent.StepResult {
			requests = append(requests, request)
			round++
			if turn == 0 {
				switch round {
				case 1:
					summary.Unread, summary.DeliverySequence = 1, 1
					now = now.Add(time.Second)
				case 2:
					summary.Unread, summary.DeliverySequence = 2, 2
					now = now.Add(2 * time.Second)
				case 3:
					now = now.Add(namedAgentInboxInterval)
				case 4:
					return agent.StepResult{Content: "Work progressed"}
				}
			} else {
				now = now.Add(namedAgentInboxInterval)
				switch round {
				case 1:
					summary.DeliverySequence = 3
				case 2:
					summary.Unread = 0
				case 3:
					summary.Unread, summary.DeliverySequence = 1, 4
				case 4:
					return agent.StepResult{Content: "Finished"}
				}
			}
			return agent.StepResult{ToolCalls: []providers.ToolCall{{ID: fmt.Sprintf("work-%d-%d", turn, round), Name: "continue_work", Arguments: `{}`}}}
		}))
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range result.NewMessages {
			if strings.Contains(message.Content, "Inbox snapshot:") {
				t.Fatal("request-only reminder entered durable conversation history")
			}
		}
		history = append(history, result.NewMessages...)
		retained = result.RetainedRequestContext
	}
	for i := 1; i < len(requests); i++ {
		previous, next := requests[i-1].Messages, requests[i].Messages
		if len(next) < len(previous) || !reflect.DeepEqual(previous, next[:len(previous)]) {
			t.Fatalf("inbox update broke the request prefix at request %d", i)
		}
		if !reflect.DeepEqual(requests[i-1].Tools, requests[i].Tools) {
			t.Fatal("reminder changed the tool prefix")
		}
	}
	final := requests[len(requests)-1].Messages
	for _, snapshot := range []string{"Inbox snapshot: 2/0.", "Inbox snapshot: 3/0.", "Inbox snapshot: 4/0."} {
		count := 0
		for _, message := range final {
			if strings.Contains(message.Content, snapshot) {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("snapshot %q was duplicated or lost: %d", snapshot, count)
		}
	}
	for _, message := range final {
		if strings.Contains(message.Content, "Inbox snapshot: 1/0.") {
			t.Fatal("burst emitted the intermediate one-message snapshot")
		}
	}
}

func TestNamedAgentInboxFollowupsArePulledWithinRunningTurn(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	var initial ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{
		AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Inspect the implementation", RequestID: "inbox-initial",
	}, &initial)
	first := provider.next(t)
	th := f.server.thread(initial.Session.SessionRef)
	th.mu.Lock()
	turnID := th.currentTurn
	th.mu.Unlock()
	for index, body := range []string{"PRIVATE FOLLOWUP ONE", "PRIVATE FOLLOWUP TWO"} {
		var queued ChannelSessionResult
		f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{
			AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: body, RequestID: fmt.Sprintf("inbox-followup-%d", index),
		}, &queued)
	}
	// The clock only controls sampling. Let its short window expire while the
	// original provider request is still running: it must not trigger inference.
	time.Sleep(namedAgentInboxInterval + 20*time.Millisecond)
	select {
	case <-provider.calls:
		t.Fatal("incoming mail interrupted the active request or started another turn")
	default:
	}
	first.response <- providers.ChatResponse{ToolCalls: []providers.ToolCall{{
		ID: "read-roster", Name: "chat_roster", Arguments: fmt.Sprintf(`{"action":"list","room_id":%q}`, f.room.ID),
	}}}
	notified := provider.next(t)
	text := collaborationRequestText(notified.request)
	if !strings.Contains(text, "2 unread message(s)") {
		t.Fatalf("next normal request missed coalesced inbox reminder: %s", text)
	}
	if strings.Contains(text, "PRIVATE FOLLOWUP") {
		t.Fatal("reminder injected private message bodies before chat_check")
	}
	previous := first.request.Messages
	if len(notified.request.Messages) < len(previous) || !reflect.DeepEqual(previous, notified.request.Messages[:len(previous)]) {
		t.Fatal("reminder rewrote the running provider request's prefix")
	}
	notified.response <- providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: "pull-inbox", Name: "chat_check", Arguments: `{}`}}}
	pulled := provider.next(t)
	for _, body := range []string{"PRIVATE FOLLOWUP ONE", "PRIVATE FOLLOWUP TWO"} {
		if !strings.Contains(collaborationRequestText(pulled.request), body) {
			t.Fatalf("explicit pull did not receive %q", body)
		}
	}
	th.mu.Lock()
	continuedTurn := th.currentTurn
	th.mu.Unlock()
	if continuedTurn != turnID {
		t.Fatal("inbox handling moved to a new turn")
	}
	pulled.response <- providers.ChatResponse{Content: "Applied both followups in this turn."}
	f.waitForCompletion(t)
	wake, err := f.server.channelService.WakeState(context.Background(), f.identity.ID)
	if err != nil || wake.Outstanding || wake.Pending {
		t.Fatalf("pulled followups retained an extra wake: %+v, %v", wake, err)
	}
	select {
	case <-provider.calls:
		t.Fatal("received followups caused an extra model turn")
	default:
	}
}
