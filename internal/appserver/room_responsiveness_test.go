package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestRoomFollowupReachesIdlePeerWhileFirstMemberKeepsWorking(t *testing.T) {
	fixture, provider := newCollaborationFlowFixture(t)
	ctx := context.Background()
	peer, err := fixture.server.channelService.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Writer", Autostart: true})
	if err != nil {
		t.Fatal(err)
	}
	room, err := fixture.server.channelService.CreateRoom(ctx, channels.CreateRoomParams{Kind: channels.RoomChannel, Name: "Project work", CreatedBy: localChannelHumanID, Members: []channels.RoomMember{
		{MemberType: channels.MemberAgent, MemberID: fixture.identity.ID},
		{MemberType: channels.MemberAgent, MemberID: peer.Agent.ID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var original, followup ChannelMessageSendResult
	fixture.rpc(t, MethodChannelMessageSend, ChannelMessageSendParams{RoomID: room.ID, Body: "Inspect the uncommitted changes"}, &original)
	working := provider.next(t)
	ref := channels.NamedAgentConversationRef(agentRuntimeFromNamed(fixture.identity))
	before, err := fixture.server.channelService.LookupCollaborationSession(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	fixture.rpc(t, MethodChannelMessageSend, ChannelMessageSendParams{RoomID: room.ID, Body: "Also simplify the README and documentation"}, &followup)
	available := provider.next(t)
	if prompt := collaborationRequestText(available.request); !strings.Contains(prompt, "Also simplify the README and documentation") || !strings.Contains(prompt, "Inspect the uncommitted changes") {
		t.Fatalf("available member lost the follow-up or room context: %s", prompt)
	}
	current, err := fixture.server.channelService.LookupCollaborationSession(ctx, ref)
	if err != nil || current.State != channels.CollaborationSessionRunning || current.TurnID != before.TurnID {
		t.Fatalf("follow-up interrupted or replaced the original work: %+v %v", current, err)
	}
	const reply = "I will simplify the README while the review continues."
	// Assistant text stays private after a turn ends, so the peer publishes the
	// answer to the room with chat_send, its own versioned bubble.
	args, err := json.Marshal(map[string]any{"room_id": room.ID, "kind": "text", "body": reply, "basis_seq": followup.Message.Seq})
	if err != nil {
		t.Fatal(err)
	}
	available.response <- providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: "send-followup", Name: "chat_send", Arguments: string(args)}}}
	continuation := provider.next(t)
	continuation.response <- providers.ChatResponse{StopReason: "completed"}
	select {
	case id := <-fixture.completed:
		if id != peer.Agent.ID {
			t.Fatalf("first completion was not the available peer: %s", id)
		}
	case <-time.After(collaborationTestWaitTimeout):
		t.Fatal("available peer did not finish while the first member was busy")
	}
	visible := false
	for _, message := range readRoomReplies(t, fixture, room.ID).Messages {
		if message.AuthorID == peer.Agent.ID && message.Body == reply {
			visible = true
		}
	}
	if !visible {
		t.Fatal("available peer's response was not published in the room")
	}
	select {
	case <-provider.calls:
		t.Fatal("the busy identity started a concurrent turn")
	default:
	}
	working.response <- providers.ChatResponse{Content: "Original inspection finished."}
	for i := 0; i < 3; i++ {
		next := provider.next(t)
		if i == 0 && !strings.Contains(collaborationRequestText(next.request), reply) {
			t.Fatal("deferred member lost the available peer's response")
		}
		next.response <- providers.ChatResponse{StopReason: "completed"}
	}
	for range 4 {
		select {
		case <-fixture.completed:
		case <-time.After(collaborationTestWaitTimeout):
			t.Fatal("discussion did not settle")
		}
	}
	select {
	case <-provider.calls:
		t.Fatal("discussion continued after all members passed")
	default:
	}
}
