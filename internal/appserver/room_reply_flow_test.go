package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type roomReplyStreamCall struct {
	request providers.ChatRequest
	events  chan providers.StreamEvent
}

type roomReplyStreamProvider struct{ calls chan *roomReplyStreamCall }

func (provider *roomReplyStreamProvider) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("unexpected non-streaming room request")
}

func (provider *roomReplyStreamProvider) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	call := &roomReplyStreamCall{request: request, events: make(chan providers.StreamEvent, 8)}
	select {
	case provider.calls <- call:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	events := make(chan providers.StreamEvent)
	go func() {
		defer close(events)
		for {
			select {
			case event, ok := <-call.events:
				if !ok {
					return
				}
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

func readRoomReplies(t *testing.T, fixture *collaborationRPCFixture, roomID string) ChannelMessageListResult {
	t.Helper()
	var result ChannelMessageListResult
	fixture.rpc(t, MethodChannelMessageList, ChannelMessageListParams{RoomID: roomID, Limit: 100}, &result)
	return result
}

func waitForRoomResponse(t *testing.T, fixture *collaborationRPCFixture, roomID string, ready func(ChannelResponse) bool) ChannelResponse {
	t.Helper()
	deadline := time.NewTimer(collaborationTestWaitTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		result := readRoomReplies(t, fixture, roomID)
		for _, response := range result.Responses {
			if ready(response) {
				return response
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("room response did not reach expected state: %+v", result.Responses)
			return ChannelResponse{}
		case <-tick.C:
		}
	}
}

func sendRoomReplyObjective(t *testing.T, fixture *collaborationRPCFixture, body string) {
	t.Helper()
	if _, err := fixture.server.channelService.SendHuman(context.Background(), channels.HumanSendParams{RoomID: fixture.room.ID, HumanID: "human-1", Body: body}); err != nil {
		t.Fatal(err)
	}
}

func TestRoomReplyStreamsThenPersistsWithoutSendTool(t *testing.T) {
	fixture, _ := newCollaborationFlowFixture(t)
	fixture.room = createPeerRoom(t, fixture, "Natural replies", fixture.identity)
	provider := &roomReplyStreamProvider{calls: make(chan *roomReplyStreamCall, 4)}
	fixture.server.rt.StreamRunner.Client = provider
	sendRoomReplyObjective(t, fixture, "Explain the reconnect issue here")
	var call *roomReplyStreamCall
	select {
	case call = <-provider.calls:
	case <-time.After(collaborationTestWaitTimeout):
		t.Fatal("room request did not reach the streaming provider")
	}
	const privateReasoning = "PRIVATE REASONING: eliminate an unverified hypothesis"
	call.events <- providers.StreamEvent{Type: providers.EventThinkingDelta, Content: privateReasoning}
	thinking := waitForRoomResponse(t, fixture, fixture.room.ID, func(response ChannelResponse) bool { return response.State == "thinking" })
	if thinking.Body != "" || thinking.AgentID != fixture.identity.ID || thinking.SessionRef == "" || thinking.TurnID == "" {
		t.Fatalf("thinking exposed private content or lost its author: %+v", thinking)
	}
	const partial = "    reconnect("
	const final = partial + "socket)\n\nKeep the code indentation and Markdown line break.  \n"
	call.events <- providers.StreamEvent{Type: providers.EventThinkingDone}
	call.events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: partial, Phase: providers.MessagePhaseFinalAnswer}
	preview := waitForRoomResponse(t, fixture, fixture.room.ID, func(response ChannelResponse) bool {
		return response.State == "responding" && response.Body == partial
	})
	if preview.ID == "" || preview.ID != thinking.ID || preview.SessionRef != thinking.SessionRef {
		t.Fatalf("stream replaced the response identity: thinking=%+v preview=%+v", thinking, preview)
	}
	otherRoom := createPeerRoom(t, fixture, "Separate conversation", fixture.identity)
	if other := readRoomReplies(t, fixture, otherRoom.ID); len(other.Responses) != 0 || len(other.Messages) != 0 {
		t.Fatalf("live answer leaked into another room of the same identity: %+v", other)
	}
	call.events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: strings.TrimPrefix(final, partial), Phase: providers.MessagePhaseFinalAnswer}
	call.events <- providers.StreamEvent{Type: providers.EventDone}
	close(call.events)
	fixture.waitForCompletion(t)
	result := readRoomReplies(t, fixture, fixture.room.ID)
	if len(result.Messages) != 2 || result.Messages[1].Body != final || result.Messages[1].AuthorID != fixture.identity.ID || result.Messages[1].ID != preview.ID {
		t.Fatalf("normal final was not committed to its existing bubble: %+v", result)
	}
	if len(result.Responses) != 0 {
		t.Fatalf("finished bubble still has an active preview: %+v", result.Responses)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), privateReasoning) {
		t.Fatal("private reasoning leaked into the room API")
	}
}

func TestRoomReplyConcurrentMembersBothPublish(t *testing.T) {
	fixture, provider := newCollaborationFlowFixture(t)
	peer, err := fixture.server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{Name: "Reviewer", Autostart: true})
	if err != nil {
		t.Fatal(err)
	}
	fixture.room = createPeerRoom(t, fixture, "Parallel answers", fixture.identity, peer.Agent)
	sendRoomReplyObjective(t, fixture, "@all Give your independent observations")
	first, second := provider.next(t), provider.next(t)
	first.response <- providers.ChatResponse{Content: "The callback retains a stale socket."}
	second.response <- providers.ChatResponse{Content: "The subscription also needs to be replaced."}
	completed := make(map[string]bool)
	for range 2 {
		select {
		case identity := <-fixture.completed:
			completed[identity] = true
		case <-time.After(collaborationTestWaitTimeout):
			t.Fatal("independent room replies did not finish")
		}
	}
	result := readRoomReplies(t, fixture, fixture.room.ID)
	bodies := make(map[string]bool)
	authors := make(map[string]bool)
	for _, message := range result.Messages {
		if message.AuthorID == fixture.identity.ID || message.AuthorID == peer.Agent.ID {
			bodies[message.Body] = true
			authors[message.AuthorID] = true
		}
	}
	if len(result.Messages) != 3 || len(authors) != 2 || !completed[fixture.identity.ID] || !completed[peer.Agent.ID] || !bodies["The callback retains a stale socket."] || !bodies["The subscription also needs to be replaced."] {
		t.Fatalf("concurrent replies were held, duplicated, or misattributed: %+v", result)
	}
	select {
	case extra := <-provider.calls:
		t.Fatalf("an ordinary answer triggered another model turn: %s", collaborationRequestText(extra.request))
	default:
	}
}

func TestRoomReplyExplicitSendSuppressesOnlyDuplicateFinal(t *testing.T) {
	for _, delivered := range []bool{true, false} {
		name := "held draft"
		if delivered {
			name = "sent message"
		}
		t.Run(name, func(t *testing.T) {
			fixture, provider := newCollaborationFlowFixture(t)
			fixture.room = createPeerRoom(t, fixture, "Explicit replies", fixture.identity)
			sendRoomReplyObjective(t, fixture, "Report the result here")
			call := provider.next(t)
			basis := 1
			if !delivered {
				basis = 0
			}
			const final = "The callback retains the old socket."
			args, _ := json.Marshal(map[string]any{"room_id": fixture.room.ID, "kind": "text", "body": final, "basis_seq": basis})
			call.response <- providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: "send-result", Name: "chat_send", Arguments: string(args)}}}
			continuation := provider.next(t)
			ref := namedAgentRoomSessionID(agentRuntimeFromNamed(fixture.identity), fixture.room.ID)
			binding, err := fixture.server.channelService.LookupCollaborationSession(context.Background(), ref)
			if err != nil {
				t.Fatal(err)
			}
			continuation.response <- providers.ChatResponse{Content: final}
			fixture.waitForCompletion(t)
			turn := fixture.server.roomResponseTurn(ref, binding.TurnID)
			if turn == nil || roomReplySent(*turn, fixture.room.ID) != delivered {
				t.Fatalf("duplicate answer was not recognized as delivered=%t: %+v", delivered, turn)
			}
			result := readRoomReplies(t, fixture, fixture.room.ID)
			if len(result.Messages) != 2 || result.Messages[1].Body != final || result.Messages[1].AuthorID != fixture.identity.ID || len(result.Responses) != 0 {
				t.Fatalf("send result did not govern final publication: delivered=%t result=%+v", delivered, result)
			}
		})
	}
}

func TestRoomReplyPublishesFinalAfterExplicitProgress(t *testing.T) {
	fixture, provider := newCollaborationFlowFixture(t)
	fixture.room = createPeerRoom(t, fixture, "Progress and answer", fixture.identity)
	sendRoomReplyObjective(t, fixture, "Find the reconnect failure")
	call := provider.next(t)
	const progress = "I am checking which callback owns the reconnect socket."
	args, _ := json.Marshal(map[string]any{"room_id": fixture.room.ID, "kind": "text", "body": progress, "basis_seq": 1})
	call.response <- providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: "send-progress", Name: "chat_send", Arguments: string(args)}}}
	continuation := provider.next(t)
	const final = "The callback retains the old socket; rebuild it when the connection changes."
	continuation.response <- providers.ChatResponse{Content: final}
	fixture.waitForCompletion(t)
	result := readRoomReplies(t, fixture, fixture.room.ID)
	if len(result.Messages) != 3 || result.Messages[1].Body != progress || result.Messages[2].Body != final || result.Messages[2].AuthorID != fixture.identity.ID || len(result.Responses) != 0 {
		t.Fatalf("public progress swallowed the final answer: %+v", result)
	}
}

func TestRoomReplyChildStaysPrivateUntilParentIntegrates(t *testing.T) {
	fixture, provider := newCollaborationFlowFixture(t)
	fixture.room = createPeerRoom(t, fixture, "Private investigation", fixture.identity)
	sendRoomReplyObjective(t, fixture, "Investigate and explain the verified result")
	call := provider.next(t)
	parentRef := namedAgentRoomSessionID(agentRuntimeFromNamed(fixture.identity), fixture.room.ID)
	parent, err := fixture.server.channelService.LookupCollaborationSession(context.Background(), parentRef)
	if err != nil {
		t.Fatal(err)
	}
	_, child, childCall := startCollaborationChild(t, fixture, provider, parent, call)
	assertChildHidden := func() {
		t.Helper()
		result := readRoomReplies(t, fixture, fixture.room.ID)
		for _, response := range result.Responses {
			if response.SessionRef == child.SessionRef {
				t.Fatalf("private child execution appeared in the room: %+v", response)
			}
		}
		for _, message := range result.Messages {
			if strings.Contains(message.Body, "PRIVATE CHILD EVIDENCE") {
				t.Fatalf("raw child evidence was published: %+v", message)
			}
		}
	}
	assertChildHidden()
	const evidence = "PRIVATE CHILD EVIDENCE: the captured pointer refers to the old socket"
	childCall.response <- providers.ChatResponse{Content: evidence}
	fixture.waitForCompletion(t)
	continuation := provider.next(t)
	if !strings.Contains(collaborationRequestText(continuation.request), evidence) {
		t.Fatal("child result did not reach its parent for integration")
	}
	assertChildHidden()
	const integrated = "The experiment confirms that reconnect retains the previous socket."
	continuation.response <- providers.ChatResponse{Content: integrated}
	fixture.waitForCompletion(t)
	assertChildHidden()
	result := readRoomReplies(t, fixture, fixture.room.ID)
	var found bool
	for _, message := range result.Messages {
		if message.Body == integrated && message.AuthorID == fixture.identity.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("parent's integrated final did not reach the room: %+v", result)
	}
}

func TestRoomReplyFailureIsVisibleAndRecoverable(t *testing.T) {
	for _, resume := range []bool{true, false} {
		name := "new input"
		if resume {
			name = "resume"
		}
		t.Run(name, func(t *testing.T) {
			fixture, provider := newCollaborationFlowFixture(t)
			fixture.room = createPeerRoom(t, fixture, "Recoverable replies", fixture.identity)
			sendRoomReplyObjective(t, fixture, "Investigate the reconnect problem")
			provider.next(t).failure <- errors.New("permanent provider rejection")
			fixture.waitForCompletion(t)
			failed := waitForRoomResponse(t, fixture, fixture.room.ID, func(response ChannelResponse) bool { return response.State == "failed" })
			if failed.Error == "" || failed.AgentID != fixture.identity.ID || failed.SessionRef == "" || failed.TurnID == "" {
				t.Fatalf("room failure lost the error or recovery target: %+v", failed)
			}
			if resume {
				var result ChannelSessionResult
				fixture.rpc(t, MethodChannelSessionResume, ChannelSessionRefParams{SessionRef: failed.SessionRef}, &result)
			} else {
				sendRoomReplyObjective(t, fixture, "Continue from the evidence already collected")
			}
			call := provider.next(t)
			for _, response := range readRoomReplies(t, fixture, fixture.room.ID).Responses {
				if response.State == "failed" {
					t.Fatalf("restarted reply still shows the previous failure: %+v", response)
				}
			}
			call.response <- providers.ChatResponse{Content: "Recovered: the reconnect callback retains the old socket."}
			fixture.waitForCompletion(t)
			// A client retains the failed preview until the wire response explicitly
			// replaces it with an empty array; an omitted field leaves it visible.
			result := ChannelMessageListResult{Responses: []ChannelResponse{failed}}
			fixture.rpc(t, MethodChannelMessageList, ChannelMessageListParams{RoomID: fixture.room.ID, Limit: 100}, &result)
			last := result.Messages[len(result.Messages)-1]
			if last.AuthorID != fixture.identity.ID || !strings.HasPrefix(last.Body, "Recovered:") || result.Responses == nil || len(result.Responses) != 0 {
				t.Fatalf("recovered final did not replace the failed response: %+v", result)
			}
		})
	}
}

func TestRoomReplyIndependentSessionDoesNotPublish(t *testing.T) {
	fixture, provider := newCollaborationFlowFixture(t)
	fixture.room = createPeerRoom(t, fixture, "Independent work", fixture.identity)
	var created ChannelSessionResult
	fixture.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{
		AgentID: fixture.identity.ID, RoomID: fixture.room.ID,
		Prompt: "Investigate this privately", RequestID: "private-independent",
	}, &created)
	call := provider.next(t)
	if result := readRoomReplies(t, fixture, fixture.room.ID); len(result.Responses) != 0 {
		t.Fatalf("independent work was projected as a room response: %+v", result.Responses)
	}
	call.response <- providers.ChatResponse{Content: "PRIVATE INDEPENDENT RESULT"}
	fixture.waitForCompletion(t)
	result := readRoomReplies(t, fixture, fixture.room.ID)
	if len(result.Messages) != 0 || len(result.Responses) != 0 {
		t.Fatalf("independent final leaked into the public room: %+v", result)
	}
}

func TestRoomReplyInterruptionRemainsVisibleUntilResumed(t *testing.T) {
	fixture, provider := newCollaborationFlowFixture(t)
	fixture.room = createPeerRoom(t, fixture, "Interrupted reply", fixture.identity)
	sendRoomReplyObjective(t, fixture, "Investigate the reconnect issue")
	_ = provider.next(t)
	ref := namedAgentRoomSessionID(agentRuntimeFromNamed(fixture.identity), fixture.room.ID)
	var stopped ChannelSessionResult
	fixture.rpc(t, MethodChannelSessionStop, ChannelSessionRefParams{SessionRef: ref}, &stopped)
	fixture.waitForCompletion(t)
	interrupted := waitForRoomResponse(t, fixture, fixture.room.ID, func(response ChannelResponse) bool { return response.State == "interrupted" })
	if interrupted.SessionRef != ref || interrupted.AgentID != fixture.identity.ID {
		t.Fatalf("interruption lost its author or recovery target: %+v", interrupted)
	}
	var resumed ChannelSessionResult
	fixture.rpc(t, MethodChannelSessionResume, ChannelSessionRefParams{SessionRef: ref}, &resumed)
	provider.next(t).response <- providers.ChatResponse{Content: "The resumed investigation confirms the stale callback."}
	fixture.waitForCompletion(t)
	result := readRoomReplies(t, fixture, fixture.room.ID)
	if len(result.Messages) != 2 || result.Messages[1].AuthorID != fixture.identity.ID || len(result.Responses) != 0 {
		t.Fatalf("resumed reply did not replace the interrupted state: %+v", result)
	}
}
