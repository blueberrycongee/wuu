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

func coordinatorModelTool(call *collaborationFlowCall, id, name string, args map[string]any) {
	raw, _ := json.Marshal(args)
	call.response <- providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: id, Name: name, Arguments: string(raw)}}}
}

func splitCoordinatorCalls(t *testing.T, provider *collaborationFlowProvider, toolID string) (continuation, worker *collaborationFlowCall) {
	t.Helper()
	for range 2 {
		call := provider.next(t)
		isContinuation := false
		for _, message := range call.request.Messages {
			if message.Role == "tool" && message.ToolCallID == toolID {
				isContinuation = true
				var result struct {
					Error string `json:"error"`
				}
				if json.Unmarshal([]byte(message.Content), &result) == nil && result.Error != "" {
					t.Fatalf("%s failed: %s", toolID, result.Error)
				}
			}
		}
		if isContinuation {
			continuation = call
		} else {
			worker = call
		}
	}
	if continuation == nil || worker == nil {
		t.Fatal("delegation did not produce a worker and a coordinator continuation")
	}
	return
}

func TestRoomCoordinatorDelegatesStaysResponsiveAndPublishesThroughMember(t *testing.T) {
	fixture, provider := newCollaborationFlowFixture(t)
	ctx := context.Background()
	peer, err := fixture.server.channelService.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Reviewer", Autostart: true})
	if err != nil {
		t.Fatal(err)
	}
	fixture.room = createPeerRoom(t, fixture, "Coordinated investigation", fixture.identity, peer.Agent)
	sent, err := fixture.server.channelService.SendHuman(ctx, channels.HumanSendParams{RoomID: fixture.room.ID, HumanID: "human-1", Body: "Find the reconnect cause and report the evidence"})
	if err != nil {
		t.Fatal(err)
	}
	coordinatorCall := provider.next(t)
	bindings, err := fixture.server.channelService.ListAllCollaborationSessions(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].Purpose != channels.CollaborationSessionCoordination || bindings[0].NamedAgentID != "" {
		t.Fatalf("request did not reach only the hidden coordinator: %+v %v", bindings, err)
	}
	parent := bindings[0]

	status, err := fixture.server.channelCoordinatorStatus(ctx, fixture.room.ID)
	if err != nil || status.State != "working" || len(status.AgentIDs) != 0 {
		t.Fatalf("coordination status: %#v %v", status, err)
	}
	thread := fixture.server.thread(parent.SessionRef)
	thread.mu.Lock()
	kit := thread.execRuntime.Toolkit
	thread.mu.Unlock()
	for _, name := range []string{"bash", "write_file", "apply_patch", "chat_send", "thread_get", "exec"} {
		if _, err := kit.Execute(ctx, providers.ToolCall{Name: name, Arguments: `{}`}); err == nil {
			t.Fatalf("coordinator executed %s", name)
		}
	}
	coordinatorModelTool(coordinatorCall, "assign", "collaboration_send", map[string]any{
		"room_id": fixture.room.ID, "to_agent_id": fixture.identity.ID,
		"body":              "Inspect reconnect and report evidence directly in the room",
		"source_message_id": sent.Message.ID, "request_id": "reconnect-investigation",
	})
	continuation, worker := splitCoordinatorCalls(t, provider, "assign")
	if !strings.Contains(collaborationRequestText(worker.request), sent.Message.ID) {
		t.Fatal("delegation lost the original source message")
	}
	continuation.response <- providers.ChatResponse{Content: "Investigation assigned"}
	waitCoordinatorCompletion(t, fixture)
	const evidence = "The reconnect callback retained a stale cursor"
	worker.response <- providers.ChatResponse{Content: evidence}
	waitCoordinatorCompletion(t, fixture)
	messages, err := fixture.server.channelService.ListMessages(ctx, fixture.room.ID, 0, 50)
	if err != nil || len(messages) != 2 || messages[1].AuthorID != fixture.identity.ID || messages[1].Body != evidence {
		t.Fatalf("public delivery: %+v %v", messages, err)
	}
	bindings, err = fixture.server.channelService.ListAllCollaborationSessions(ctx)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("unexpected execution branches: %+v %v", bindings, err)
	}
	for _, binding := range bindings {
		if binding.NamedAgentID == peer.Agent.ID {
			t.Fatal("unselected member was started")
		}
		if binding.NamedAgentID == fixture.identity.ID && (!binding.Primary || binding.ParentSessionRef != "") {
			t.Fatalf("delegation did not use the member's conversation: %+v", binding)
		}
	}
	select {
	case <-provider.calls:
		t.Fatal("completion started an acknowledgement loop")
	default:
	}
}

func waitCoordinatorCompletion(t *testing.T, fixture *collaborationRPCFixture) {
	t.Helper()
	select {
	case id := <-fixture.completed:
		if id != fixture.identity.ID && id != fixture.room.RuntimeID {
			t.Fatalf("unexpected identity %s", id)
		}
	case <-time.After(collaborationTestWaitTimeout):
		t.Fatal("coordinator completion was not persisted")
	}
}

func TestRoomCoordinatorFailureCanBeResumedWithoutBroadcast(t *testing.T) {
	fixture, provider := newCollaborationFlowFixture(t)
	ctx := context.Background()
	peer, err := fixture.server.channelService.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Peer", Autostart: true})
	if err != nil {
		t.Fatal(err)
	}
	fixture.room = createPeerRoom(t, fixture, "Recovery", fixture.identity, peer.Agent)
	if _, err := fixture.server.channelService.SendHuman(ctx, channels.HumanSendParams{RoomID: fixture.room.ID, HumanID: "human-1", Body: "Investigate"}); err != nil {
		t.Fatal(err)
	}
	call := provider.next(t)
	call.failure <- errors.New("coordinator provider unavailable")
	waitCoordinatorCompletion(t, fixture)
	bindings, err := fixture.server.channelService.ListAllCollaborationSessions(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].FailureReason == "" {
		t.Fatalf("failure lost: %+v %v", bindings, err)
	}
	binding := bindings[0]
	status, err := fixture.server.channelCoordinatorStatus(ctx, fixture.room.ID)
	if err != nil || status.State != "failed" || status.SessionRef != binding.SessionRef || status.Error == "" {
		t.Fatalf("failed coordinator status: %#v %v", status, err)
	}
	waitForThreadLeaseRelease(t, fixture.server.rt.SessionDir, binding.SessionRef)
	if _, err := fixture.server.channelService.ResumeSession(ctx, channels.CollaborationSessionControlParams{SessionRef: binding.SessionRef}); err != nil {
		t.Fatal(err)
	}
	resumed := provider.next(t)
	if !strings.Contains(collaborationRequestText(resumed.request), "Investigate") {
		t.Fatal("retry lost room objective")
	}
	resumed.response <- providers.ChatResponse{Content: "Recovered"}
	waitCoordinatorCompletion(t, fixture)
	bindings, err = fixture.server.channelService.ListAllCollaborationSessions(ctx)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("retry broadcast to members: %+v %v", bindings, err)
	}
}

func TestRoomCoordinatorStatusDoesNotExposeAWorkerInEmptyRoomsOrDMs(t *testing.T) {
	fixture, _ := newCollaborationFlowFixture(t)
	ctx := context.Background()
	empty := createPeerRoom(t, fixture, "Unstaffed")
	status, err := fixture.server.channelCoordinatorStatus(ctx, empty.ID)
	if err != nil || status == nil || status.State != "needs_members" || status.SessionRef != "" {
		t.Fatalf("empty room status: %#v %v", status, err)
	}
	dm, err := fixture.server.channelService.OpenDirectMessage(ctx, "human-1", fixture.identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	status, err = fixture.server.channelCoordinatorStatus(ctx, dm.ID)
	if err != nil || status != nil {
		t.Fatalf("DM exposed a coordinator: %#v %v", status, err)
	}
}
