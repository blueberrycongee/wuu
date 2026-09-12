package channels

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoomMemberCanInviteAndProposePersistentNamedAgents(t *testing.T) {
	ctx := context.Background()
	wake := &recordingWakeSink{}
	service := openTestService(t, wake)
	andy := createTestAgent(t, service, "Andy")
	reviewer := createTestAgent(t, service, "Reviewer")
	if err := os.WriteFile(filepath.Join(andy.Agent.MemoryDir, agentMemoryIndexFile), []byte("- [Sessions](sessions.md) - Recovers interrupted work safely\n"), 0o600); err != nil {
		t.Fatalf("write member memory index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reviewer.Agent.MemoryDir, agentMemoryIndexFile), []byte("- [Private](private.md) - Outside-room memory\n"), 0o600); err != nil {
		t.Fatalf("write available agent memory index: %v", err)
	}
	room := createTestRoom(t, service, andy)
	client, err := service.BindAgent(ctx, andy.Agent.ID)
	if err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	bound, err := client.BindCollaborationSession(ctx, CollaborationSessionBindParams{
		SessionRef: "andy-room-session", PrincipalID: andy.Agent.ID, RoomID: room.ID,
		Purpose: CollaborationSessionCoordination, State: CollaborationSessionRunning,
	})
	if err != nil {
		t.Fatalf("BindCollaborationSession() error = %v", err)
	}

	roster, err := client.RoomRoster(ctx, room.ID)
	if err != nil {
		t.Fatalf("RoomRoster() error = %v", err)
	}
	if roster.MembershipRevision != 1 || len(roster.Members) != 1 || len(roster.AvailableAgents) != 1 || roster.AvailableAgents[0].Name != "Reviewer" {
		t.Fatalf("initial roster = %#v", roster)
	}
	if len(roster.Members[0].Sessions) != 1 || roster.Members[0].Sessions[0].SessionRef != bound.SessionRef ||
		roster.Members[0].Sessions[0].Purpose != CollaborationSessionCoordination ||
		roster.Members[0].Sessions[0].State != CollaborationSessionRunning {
		t.Fatalf("member sessions = %#v", roster.Members[0].Sessions)
	}
	if roster.Members[0].MemoryIndex != "- [Sessions](sessions.md) - Recovers interrupted work safely" {
		t.Fatalf("member memory index = %q", roster.Members[0].MemoryIndex)
	}
	if roster.AvailableAgents[0].MemoryIndex != "" {
		t.Fatalf("available agent memory index = %q, want hidden", roster.AvailableAgents[0].MemoryIndex)
	}
	if roster.AvailableAgents[0].Sessions == nil || len(roster.AvailableAgents[0].Sessions) != 0 {
		t.Fatalf("available agent sessions = %#v, want an empty current-room inventory", roster.AvailableAgents[0].Sessions)
	}

	invited, err := client.InviteRoomAgent(ctx, room.ID, reviewer.Agent.ID)
	if err != nil {
		t.Fatalf("InviteRoomAgent() error = %v", err)
	}
	if invited.MembershipRevision != 2 || len(invited.Members) != 3 {
		t.Fatalf("invited room = %#v", invited)
	}

	proposal, err := client.ProposeRoomAgent(ctx, room.ID, "Designer", "界面设计与交互评审")
	if err != nil {
		t.Fatalf("ProposeRoomAgent() error = %v", err)
	}
	if proposal.Name != "Designer" || proposal.Role != "界面设计与交互评审" || proposal.State != AgentCreationPending {
		t.Fatalf("proposal = %#v", proposal)
	}
	unchanged, err := service.GetRoom(ctx, room.ID)
	if err != nil || unchanged.MembershipRevision != 2 || len(unchanged.Members) != 3 {
		t.Fatalf("proposal changed membership: room = %#v, err = %v", unchanged, err)
	}
	messages, err := service.ListMessages(ctx, room.ID, 0, 100)
	if err != nil || len(messages) != 2 || messages[1].AgentCreationProposal == nil {
		t.Fatalf("proposal message = %#v, err = %v", messages, err)
	}

	approved, err := service.ResolveAgentCreationProposal(ctx, ResolveAgentCreationProposalParams{
		ProposalID: proposal.ID, HumanID: "human-1", Approve: true, Provider: "openai", Model: "gpt-5",
	})
	if err != nil {
		t.Fatalf("ResolveAgentCreationProposal() error = %v", err)
	}
	if approved.State != AgentCreationApproved || approved.CreatedAgentID == "" || approved.Provider != "openai" || approved.Model != "gpt-5" {
		t.Fatalf("approved proposal = %#v", approved)
	}
	persisted, err := service.GetNamedAgent(ctx, approved.CreatedAgentID)
	if err != nil || !persisted.Autostart || persisted.ProviderOverride != "openai" || persisted.ModelOverride != "gpt-5" {
		t.Fatalf("approved named agent = %#v, err = %v", persisted, err)
	}

	cancelProposal, err := client.ProposeRoomAgent(ctx, room.ID, "Writer", "Documentation")
	if err != nil {
		t.Fatalf("ProposeRoomAgent(cancel) error = %v", err)
	}
	cancelled, err := service.ResolveAgentCreationProposal(ctx, ResolveAgentCreationProposalParams{
		ProposalID: cancelProposal.ID, HumanID: "human-1", Approve: false,
	})
	if err != nil || cancelled.State != AgentCreationCancelled {
		t.Fatalf("cancelled proposal = %#v, err = %v", cancelled, err)
	}
	messages, err = service.ListMessages(ctx, room.ID, 0, 100)
	if err != nil || messages[len(messages)-1].Body != "用户取消了创建新角色：Writer" {
		t.Fatalf("cancel notification = %#v, err = %v", messages, err)
	}
}

func TestNonMemberCannotManageRoomRoster(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	andy := createTestAgent(t, service, "Andy")
	room := createTestRoom(t, service, andy)
	outsider := createTestAgent(t, service, "Outsider")
	client, err := service.BindAgent(ctx, outsider.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent() error = %v", err)
	}
	if _, err := client.RoomRoster(ctx, room.ID); err != ErrUnauthorized {
		t.Fatalf("RoomRoster() error = %v, want ErrUnauthorized", err)
	}
}

func TestMembershipChangesPreserveContextWithoutWakingMembers(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	alpha := createTestAgent(t, service, "Alpha")
	beta := createTestAgent(t, service, "Beta")
	room := createTestRoom(t, service, alpha)
	client, err := service.BindAgent(ctx, alpha.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.InviteRoomAgent(ctx, room.ID, beta.Agent.ID); err != nil {
		t.Fatal(err)
	}
	members := []RoomMember{{MemberType: MemberAgent, MemberID: alpha.Agent.ID}}
	if _, err := service.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Members: &members}); err != nil {
		t.Fatal(err)
	}
	members = append(members, RoomMember{MemberType: MemberAgent, MemberID: beta.Agent.ID})
	if _, err := service.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Members: &members}); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteNamedAgent(ctx, beta.Agent.ID); err != nil {
		t.Fatal(err)
	}
	if wakes := sink.take(); len(wakes) != 0 {
		t.Fatalf("membership changes scheduled inference without input: %v", wakes)
	}
	if _, err := client.BindCollaborationSession(ctx, CollaborationSessionBindParams{RoomID: room.ID, SessionRef: "membership-context", Purpose: CollaborationSessionConversation}); err != nil {
		t.Fatal(err)
	}
	session, err := service.BindAgentSession(ctx, alpha.Agent.ID, "membership-context")
	if err != nil {
		t.Fatal(err)
	}
	if received, err := session.ReceiveCollaboration(ctx, 20); err != nil || len(received) != 0 {
		t.Fatalf("membership changes added active input: %#v, %v", received, err)
	}
	messages, err := service.ListMessages(ctx, room.ID, 0, 20)
	if err != nil || len(messages) != 4 {
		t.Fatalf("membership context = %#v, %v", messages, err)
	}
	for _, message := range messages {
		if message.Kind != MessageSystem {
			t.Fatalf("membership context is not a system record: %#v", message)
		}
	}
}

func TestAgentCreationDecisionsResumeMemberConversationsExactlyOnce(t *testing.T) {
	for _, approve := range []bool{true, false} {
		name := "cancelled"
		decision := "取消"
		if approve {
			name, decision = "approved", "批准"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			sink := &recordingWakeSink{}
			service := openTestService(t, sink)
			alpha := createTestAgent(t, service, "Alpha")
			beta := createTestAgent(t, service, "Beta")
			room := createTestRoom(t, service, alpha, beta)
			client, err := service.BindAgent(ctx, alpha.Agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			proposal, err := client.ProposeRoomAgent(ctx, room.ID, "Designer", "Review interaction design")
			if err != nil {
				t.Fatal(err)
			}
			if wakes := sink.take(); len(wakes) != 0 {
				t.Fatalf("undecided proposal woke members: %v", wakes)
			}
			params := ResolveAgentCreationProposalParams{ProposalID: proposal.ID, HumanID: "human-1", Approve: approve}
			resolved, err := service.ResolveAgentCreationProposal(ctx, params)
			if err != nil {
				t.Fatal(err)
			}
			ids := []string{alpha.Agent.ID, beta.Agent.ID}
			if approve {
				ids = append(ids, resolved.CreatedAgentID)
			}
			if wakes := sink.take(); len(wakes) != len(ids) {
				t.Fatalf("decision wakes = %v, want %d members", wakes, len(ids))
			}
			if _, err := service.ResolveAgentCreationProposal(ctx, params); !errors.Is(err, ErrConflict) {
				t.Fatalf("repeated decision = %v, want conflict", err)
			}
			for _, id := range ids {
				member, err := service.BindAgent(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				ref := "decision-session-" + id
				if _, err := member.BindCollaborationSession(ctx, CollaborationSessionBindParams{RoomID: room.ID, SessionRef: ref, Purpose: CollaborationSessionConversation}); err != nil {
					t.Fatal(err)
				}
				session, err := service.BindAgentSession(ctx, id, ref)
				if err != nil {
					t.Fatal(err)
				}
				received, err := session.ReceiveCollaboration(ctx, 20)
				if err != nil || len(received) != 1 {
					t.Fatalf("member decision input = %#v, %v", received, err)
				}
				receipt := received[0]
				if receipt.FromType != MemberHuman || receipt.FromID != params.HumanID || receipt.WorkID != "" || receipt.SourceMessageID != proposal.MessageID || receipt.CorrelationID != proposal.ID || !strings.Contains(receipt.Body, proposal.Name) || !strings.Contains(receipt.Body, decision) {
					t.Fatalf("decision provenance or outcome was lost: %#v", receipt)
				}
				if err := session.AcknowledgeCollaboration(ctx, []string{receipt.ID}); err != nil {
					t.Fatal(err)
				}
				checked, err := session.Check(ctx)
				if err != nil || len(checked.Collaboration) != 0 {
					t.Fatalf("decision was delivered more than once: %#v, %v", checked, err)
				}
			}
		})
	}
}
