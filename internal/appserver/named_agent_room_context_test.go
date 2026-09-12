package appserver

import (
	"context"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func TestNamedAgentRoomContextChangesOnlyWithRoomStructure(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(server.Close)
	server.channelService.SetWakeSink(nil)
	ctx := context.Background()
	alpha, err := server.channelService.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Alpha", Role: "implementation"})
	if err != nil {
		t.Fatalf("CreateNamedAgent(Alpha) error = %v", err)
	}
	beta, err := server.channelService.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Beta"})
	if err != nil {
		t.Fatalf("CreateNamedAgent(Beta) error = %v", err)
	}
	room, err := server.channelService.CreateRoom(ctx, channels.CreateRoomParams{
		Kind: channels.RoomChannel, Name: "Design", CreatedBy: localChannelHumanID,
		Members: []channels.RoomMember{
			{MemberType: channels.MemberAgent, MemberID: alpha.Agent.ID},
			{MemberType: channels.MemberAgent, MemberID: beta.Agent.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateRoom(Design) error = %v", err)
	}

	before := server.namedAgentRoomContextBlocks(alpha.Agent.ID)
	if len(before) != 1 || before[0].Source != namedAgentRoomsContextSource {
		t.Fatalf("room context blocks = %#v", before)
	}
	for _, want := range []string{"Design", "Alpha", "Beta", "User", "implementation"} {
		if !strings.Contains(before[0].Content, want) {
			t.Fatalf("room context missing %q:\n%s", want, before[0].Content)
		}
	}

	if _, err := server.channelService.SendHuman(ctx, channels.HumanSendParams{
		RoomID: room.ID, HumanID: localChannelHumanID, Body: "message content must not alter room structure",
	}); err != nil {
		t.Fatalf("SendHuman() error = %v", err)
	}
	afterMessage := server.namedAgentRoomContextBlocks(alpha.Agent.ID)
	if len(afterMessage) != 1 || afterMessage[0].Content != before[0].Content {
		t.Fatalf("message changed room context:\nbefore: %q\nafter:  %#v", before[0].Content, afterMessage)
	}

	other, err := server.channelService.CreateRoom(ctx, channels.CreateRoomParams{
		Kind: channels.RoomChannel, Name: "Private", CreatedBy: localChannelHumanID,
		Members: []channels.RoomMember{{MemberType: channels.MemberAgent, MemberID: beta.Agent.ID}},
	})
	if err != nil {
		t.Fatalf("CreateRoom(Private) error = %v", err)
	}
	afterUnrelatedRoom := server.namedAgentRoomContextBlocks(alpha.Agent.ID)
	if afterUnrelatedRoom[0].Content != before[0].Content || strings.Contains(afterUnrelatedRoom[0].Content, other.Name) {
		t.Fatalf("unrelated room changed Alpha context: %q", afterUnrelatedRoom[0].Content)
	}

	joined, err := server.channelService.CreateRoom(ctx, channels.CreateRoomParams{
		Kind: channels.RoomChannel, Name: "Delivery", CreatedBy: localChannelHumanID,
		Members: []channels.RoomMember{{MemberType: channels.MemberAgent, MemberID: alpha.Agent.ID}},
	})
	if err != nil {
		t.Fatalf("CreateRoom(Delivery) error = %v", err)
	}
	afterMembershipChange := server.namedAgentRoomContextBlocks(alpha.Agent.ID)
	if afterMembershipChange[0].Content == before[0].Content || !strings.Contains(afterMembershipChange[0].Content, joined.Name) {
		t.Fatalf("membership change did not update room context: %q", afterMembershipChange[0].Content)
	}
}

func TestNamedAgentRoomContextIsNotAttachedToOrdinarySession(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	attachNamedAgentTestToolkit(t, rt)
	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(server.Close)
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{Name: "Alpha"})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	if rt.StreamRunner.BeforeRequestContext != nil {
		t.Fatal("ordinary session unexpectedly started with named-agent room context")
	}
	threadRuntime, err := server.newNamedAgentRuntime("named-agent-context-test", credential.Agent, runtime.ThreadModelSelection{
		Provider: rt.ProviderName,
		Model:    rt.Model,
	})
	if err != nil {
		t.Fatalf("newNamedAgentRuntime() error = %v", err)
	}
	t.Cleanup(func() { releaseDetachedThreadRuntime(detachedThreadRuntime{runtime: threadRuntime}) })
	if rt.StreamRunner.BeforeRequestContext != nil {
		t.Fatal("creating a named-agent runtime mutated the ordinary session runner")
	}
	found := false
	for _, segment := range threadRuntime.StreamRunner.BeforeRequestContext() {
		for _, block := range segment.Blocks {
			if block.Source == namedAgentRoomsContextSource {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("named-agent runtime omitted room membership request context")
	}
}
