package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestIdentityConversationQueuesNewWorkAndRetainsHistory(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	var first, second ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "First goal: inspect the spacing", RequestID: "first"}, &first)
	call := provider.next(t)
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Then adjust the buttons", RequestID: "second"}, &second)
	if first.Session.SessionRef != second.Session.SessionRef || !second.Session.Primary {
		t.Fatalf("new work changed identity conversation: %+v / %+v", first, second)
	}
	select {
	case <-provider.calls:
		t.Fatal("queued work ran in parallel under the same name")
	default:
	}
	call.response <- providers.ChatResponse{Content: "Spacing inspected; preserve the 8px baseline."}
	f.waitForCompletion(t)
	continued := provider.next(t)
	prompt := collaborationRequestText(continued.request)
	for _, want := range []string{"First goal: inspect the spacing", "preserve the 8px baseline", "Then adjust the buttons"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("continuation lost %q", want)
		}
	}
	continued.response <- providers.ChatResponse{Content: "Buttons adjusted to the same baseline."}
	f.waitForCompletion(t)
	bindings, err := f.server.channelService.ListAllCollaborationSessions(context.Background())
	if err != nil || len(bindings) != 1 || bindings[0].State != channels.CollaborationSessionIdle {
		t.Fatalf("identity did not settle to one idle conversation: %+v %v", bindings, err)
	}
}

func TestIdentityConversationKeepsRoomRepliesScopedWhileReusingHistory(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	room2, roomErr := f.server.channelService.CreateRoom(context.Background(), channels.CreateRoomParams{Kind: channels.RoomChannel, Name: "Second room", CreatedBy: "human-1", Members: []channels.RoomMember{{MemberType: channels.MemberAgent, MemberID: f.identity.ID}}})
	if roomErr != nil {
		t.Fatal(roomErr)
	}
	var first, second ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Room one request", RequestID: "room-one"}, &first)
	call := provider.next(t)
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: room2.ID, Prompt: "Room two request", RequestID: "room-two"}, &second)
	call.response <- providers.ChatResponse{Content: "ANSWER FOR ROOM ONE"}
	f.waitForCompletion(t)
	continued := provider.next(t)
	if !strings.Contains(collaborationRequestText(continued.request), "ANSWER FOR ROOM ONE") {
		t.Fatal("switching rooms discarded identity history")
	}
	responses, err := f.server.channelResponses(context.Background(), f.room.ID)
	if err != nil || len(responses) != 0 {
		t.Fatalf("room one exposed room two's live reply: %+v %v", responses, err)
	}
	continued.response <- providers.ChatResponse{Content: "ANSWER FOR ROOM TWO"}
	f.waitForCompletion(t)
	if first.Session.SessionRef != second.Session.SessionRef {
		t.Fatal("rooms created separate identity sessions")
	}
	for _, r := range []struct{ room, own, other string }{{f.room.ID, "ANSWER FOR ROOM ONE", "ANSWER FOR ROOM TWO"}, {room2.ID, "ANSWER FOR ROOM TWO", "ANSWER FOR ROOM ONE"}} {
		client, _ := f.server.channelService.BindAgent(context.Background(), f.identity.ID)
		messages, err := client.QueryRoomHistory(context.Background(), channels.RoomHistoryQuery{RoomID: r.room, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, m := range messages {
			if m.Body == r.other {
				t.Fatal("reply leaked into another room")
			}
			if m.Body == r.own {
				found = true
			}
		}
		if !found {
			t.Fatalf("reply missing from %s", r.room)
		}
	}
}

func TestIdentityConversationTaskAssignmentsDoNotCreateWorkers(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	ctx := context.Background()
	client, _ := f.server.channelService.BindAgent(ctx, f.identity.ID)
	first, err := client.CreateTask(ctx, channels.TaskCreateParams{RoomID: f.room.ID, OwnerID: f.identity.ID, Title: "First task", Body: "Inspect the source screenshot"})
	if err != nil {
		t.Fatal(err)
	}
	call := provider.next(t)
	second, err := client.CreateTask(ctx, channels.TaskCreateParams{RoomID: f.room.ID, OwnerID: f.identity.ID, Title: "Second task", Body: "Check the resulting screenshot"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(collaborationRequestText(call.request), first.ID) {
		t.Fatal("first assignment did not reach the identity")
	}
	call.response <- providers.ChatResponse{Content: "The original spacing is uneven."}
	f.waitForCompletion(t)
	continued := provider.next(t)
	if !strings.Contains(collaborationRequestText(continued.request), second.ID) || !strings.Contains(collaborationRequestText(continued.request), "original spacing is uneven") {
		t.Fatal("queued task lost the previous responsibility")
	}
	continued.response <- providers.ChatResponse{Content: "Compared the final rendering with the original."}
	f.waitForCompletion(t)
	bindings, err := f.server.channelService.ListAllCollaborationSessions(ctx)
	if err != nil || len(bindings) != 1 || !bindings[0].Primary || bindings[0].WorkID != "" {
		t.Fatalf("tasks created execution branches: %+v %v", bindings, err)
	}
}

func TestIdentityConversationStopResumeRetainsTheSameHistory(t *testing.T) {
	f := newCollaborationRPCFixture(t)
	binding, call := f.create(t, "Keep this original responsibility")
	var stopped, resumed ChannelSessionResult
	f.rpc(t, MethodChannelSessionStop, ChannelSessionRefParams{SessionRef: binding.SessionRef}, &stopped)
	f.waitForCompletion(t)
	if call.ctx.Err() == nil || stopped.Session.State != channels.CollaborationSessionCancelled {
		t.Fatal("stop did not fence the running turn")
	}
	f.rpc(t, MethodChannelSessionResume, ChannelSessionRefParams{SessionRef: binding.SessionRef}, &resumed)
	continued := f.nextCall(t)
	if resumed.Session.SessionRef != binding.SessionRef || !strings.Contains(collaborationRequestText(continued.request), "Keep this original responsibility") {
		t.Fatal("resume replaced the identity conversation")
	}
	close(continued.release)
	f.waitForCompletion(t)
}

func TestIdentityConversationGoalCorrectionFencesOldTurn(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	ctx := context.Background()
	task, err := f.server.channelService.CreateTaskHuman(ctx, channels.TaskCreateParams{RoomID: f.room.ID, HumanID: "human-1", OwnerID: f.identity.ID, Title: "Polish UI", Body: "First goal"})
	if err != nil {
		t.Fatal(err)
	}
	first := provider.next(t)
	ref := channels.NamedAgentConversationRef(agentRuntimeFromNamed(f.identity))
	var snapshot ChannelSessionReadResult
	f.rpc(t, MethodChannelSessionRead, ChannelSessionRefParams{SessionRef: ref}, &snapshot)
	if !snapshot.Thread.ReadOnly || snapshot.Thread.Status != ThreadStatusInProgress {
		t.Fatalf("invalid running snapshot: %+v", snapshot.Thread)
	}
	if _, err := f.server.channelService.UpdateTaskHuman(ctx, channels.TaskUpdateParams{TaskID: task.ID, HumanID: "human-1", GoalCorrection: "Use the source screenshot and adjust the session list"}); err != nil {
		t.Fatal(err)
	}
	f.waitForCompletion(t)
	next := provider.next(t)
	if !strings.Contains(collaborationRequestText(next.request), "Use the source screenshot") {
		t.Fatal("correction was lost")
	}
	first.response <- providers.ChatResponse{Content: "STALE RESULT"}
	next.response <- providers.ChatResponse{Content: "Corrected the session list"}
	f.waitForCompletion(t)
	bindings, err := f.server.channelService.ListAllCollaborationSessions(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].SessionRef != ref {
		t.Fatalf("correction branched history: %+v %v", bindings, err)
	}
	messages, err := f.server.channelService.ListMessages(ctx, f.room.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range messages {
		if strings.Contains(m.Body, "STALE RESULT") {
			t.Fatal("superseded turn published its result")
		}
	}
}

func TestIdentityConversationRecoveryRetainsHistoryAndFutureWake(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	ctx := context.Background()
	var created ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Remember the original responsibility", RequestID: "recover"}, &created)
	call := provider.next(t)
	client, err := f.server.channelService.BindAgentSession(ctx, f.identity.ID, created.Session.SessionRef)
	if err != nil {
		t.Fatal(err)
	}
	followup, err := client.SetFollowup(ctx, channels.FollowupSetParams{RequestID: "later", After: "1h", Note: "Review the original responsibility"})
	if err != nil {
		t.Fatal(err)
	}
	call.response <- providers.ChatResponse{Content: "Scheduled for later"}
	f.waitForCompletion(t)
	waitForThreadLeaseRelease(t, f.server.rt.SessionDir, created.Session.SessionRef)
	f.server.namedAgentMu.Lock()
	err = f.server.recoverIdentityConversationLocked(ctx, agentRuntimeFromNamed(f.identity))
	f.server.namedAgentMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.calls:
		t.Fatal("restart fired a future wake early")
	default:
	}
	// Reconstruct the persistent conversation in a different server-side cache.
	reader := &Server{rt: f.server.rt, channelService: f.server.channelService, threads: make(map[string]*threadState), out: &lockedBuffer{}}
	readerFixture := &collaborationRPCFixture{server: reader, out: reader.out.(*lockedBuffer)}
	var read ChannelSessionReadResult
	readerFixture.rpc(t, MethodChannelSessionRead, ChannelSessionRefParams{SessionRef: created.Session.SessionRef}, &read)
	if len(read.Thread.Turns) != 1 || read.Session.State != channels.CollaborationSessionWaiting {
		t.Fatalf("reconstructed history: %+v", read)
	}
	pending, err := client.ListFollowups(ctx, f.room.ID)
	if err != nil || len(pending) != 1 || !pending[0].NextAt.Equal(followup.NextAt) || !pending[0].NextAt.After(time.Now()) {
		t.Fatalf("future wake changed: %+v %v", pending, err)
	}
}

func TestIdentityConversationModelCannotCreateAnotherSelf(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	var created ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Inspect the UI", RequestID: "self"}, &created)
	call := provider.next(t)
	coordinatorModelTool(call, "self-child", "chat_session", map[string]any{"action": "create", "room_id": f.room.ID, "prompt": "Independent branch", "request_id": "branch"})
	continuation := provider.next(t)
	bindings, err := f.server.channelService.ListAllCollaborationSessions(context.Background())
	if err != nil || len(bindings) != 1 {
		t.Fatalf("self delegation created another session: %+v %v", bindings, err)
	}
	select {
	case <-provider.calls:
		t.Fatal("another self started")
	default:
	}
	continuation.response <- providers.ChatResponse{Content: "Continuing here"}
	f.waitForCompletion(t)
}
