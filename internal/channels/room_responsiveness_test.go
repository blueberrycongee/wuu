package channels

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func startBusyRoomMember(t *testing.T, s *Service, room Room, agent AgentCredential) CollaborationSessionBinding {
	t.Helper()
	ctx := context.Background()
	if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "@" + agent.Agent.Name + " keep working"}); err != nil {
		t.Fatal(err)
	}
	client, binding := prepareIdentityTestTurn(t, s, agent.Agent.ID)
	binding, err := client.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: binding.SessionRef, State: CollaborationSessionRunning, TurnID: "busy-" + agent.Agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func TestRoomDiscussionNewInputPrefersAvailableMember(t *testing.T) {
	for _, otherRoom := range []bool{false, true} {
		t.Run(fmt.Sprint(otherRoom), func(t *testing.T) {
			ctx := context.Background()
			s := openTestService(t, nil)
			alpha, beta := createTestAgent(t, s, "Alpha"), createTestAgent(t, s, "Beta")
			room := createTestRoom(t, s, alpha, beta)
			workRoom := room
			if otherRoom {
				workRoom = createTestRoom(t, s, alpha)
			}
			busy := startBusyRoomMember(t, s, workRoom, alpha)
			sent, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Also update the documentation"})
			if err != nil {
				t.Fatal(err)
			}
			if len(sent.WakeAgentIDs) != 1 || sent.WakeAgentIDs[0] != beta.Agent.ID {
				t.Fatalf("idle peer did not receive new input: %v", sent.WakeAgentIDs)
			}
			settleRoomMember(t, s, beta.Agent.ID, "beta-new-input", "")
			pending, err := s.PendingCollaborationDispatches(ctx, alpha.Agent.ID)
			if err != nil || len(pending) != 1 || pending[0].RoomID != sent.Message.RoomID {
				t.Fatalf("busy member lost its later turn: %+v %v", pending, err)
			}
			current, err := s.LookupCollaborationSession(ctx, busy.SessionRef)
			if err != nil || current.State != CollaborationSessionRunning || current.TurnID != busy.TurnID || current.RoomID != workRoom.ID {
				t.Fatalf("new discussion changed active work: %+v %v", current, err)
			}
			if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: busy.SessionRef, TurnID: busy.TurnID, State: CollaborationSessionIdle, Result: "Original work finished"}); err != nil {
				t.Fatal(err)
			}
			settleRoomMember(t, s, alpha.Agent.ID, "alpha-new-input", "")
			assertRoomDiscussionEnded(t, s, room)
		})
	}
}

func TestRoomDiscussionRechecksAvailabilityBeforeNextSpeaker(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	alpha, beta, gamma := createTestAgent(t, s, "Alpha"), createTestAgent(t, s, "Beta"), createTestAgent(t, s, "Gamma")
	room := createTestRoom(t, s, alpha, beta, gamma)
	if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "@all discuss the change"}); err != nil {
		t.Fatal(err)
	}
	busy := startBusyRoomMember(t, s, createTestRoom(t, s, beta), beta)
	settleRoomMember(t, s, alpha.Agent.ID, "alpha-first", "")
	if id := pendingRoomMember(t, s, room); id != gamma.Agent.ID {
		t.Fatalf("busy next speaker blocked an available member: %s", id)
	}
	settleRoomMember(t, s, gamma.Agent.ID, "gamma-first", "")
	if id := pendingRoomMember(t, s, room); id != beta.Agent.ID {
		t.Fatalf("deferred member lost its turn: %s", id)
	}
	if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: busy.SessionRef, TurnID: busy.TurnID, State: CollaborationSessionIdle, Result: "Finished"}); err != nil {
		t.Fatal(err)
	}
	settleRoomMember(t, s, beta.Agent.ID, "beta-later", "")
	assertRoomDiscussionEnded(t, s, room)
}

func TestRoomDiscussionAllBusyRetainsQueuedInput(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	alpha, beta := createTestAgent(t, s, "Alpha"), createTestAgent(t, s, "Beta")
	room := createTestRoom(t, s, alpha, beta)
	busy := startBusyRoomMember(t, s, createTestRoom(t, s, alpha), alpha)
	startBusyRoomMember(t, s, createTestRoom(t, s, beta), beta)
	if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Discuss when available"}); err != nil {
		t.Fatal(err)
	}
	if id := pendingRoomMember(t, s, room); id != alpha.Agent.ID {
		t.Fatalf("all-busy room lost its queued speaker: %s", id)
	}
	if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: busy.SessionRef, TurnID: busy.TurnID, State: CollaborationSessionIdle, Result: "Finished"}); err != nil {
		t.Fatal(err)
	}
	settleRoomMember(t, s, alpha.Agent.ID, "alpha-later", "")
	if id := pendingRoomMember(t, s, room); id != beta.Agent.ID {
		t.Fatalf("queued discussion failed to advance: %s", id)
	}
}

func TestRoomMentionStillTargetsBusyMember(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	alpha, beta := createTestAgent(t, s, "Alpha"), createTestAgent(t, s, "Beta")
	room := createTestRoom(t, s, alpha, beta)
	startBusyRoomMember(t, s, room, alpha)
	sent, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "@Alpha change your current task"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingCollaborationDispatches(ctx, alpha.Agent.ID)
	if err != nil || len(pending) != 1 || pending[0].RoomID != sent.Message.RoomID {
		t.Fatalf("addressed follow-up did not reach busy member: %+v %v", pending, err)
	}
	pending, err = s.PendingCollaborationDispatches(ctx, beta.Agent.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("addressed follow-up was reassigned: %+v %v", pending, err)
	}
}

func TestRoomDiscussionPromptIncludesLatestHistory(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	alpha, beta := createTestAgent(t, s, "Alpha"), createTestAgent(t, s, "Beta")
	room := createTestRoom(t, s, alpha, beta)
	for i := 0; i < 25; i++ {
		if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: fmt.Sprintf("Earlier request %02d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	sent, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Now review the documentation"})
	if err != nil {
		t.Fatal(err)
	}
	settleRoomMember(t, s, alpha.Agent.ID, "alpha-review", "I will update the installation guide.")
	pending, err := s.PendingCollaborationDispatches(ctx, beta.Agent.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("next member: %+v %v", pending, err)
	}
	roomContext, err := s.RoomTurnContext(ctx, pending[0].ID)
	prompt := roomContext.Prompt
	if err != nil || !strings.Contains(prompt, sent.Message.Body) || !strings.Contains(prompt, "I will update the installation guide.") || strings.Contains(prompt, "Earlier request 00") {
		t.Fatalf("next member received stale discussion context: %s %v", prompt, err)
	}
}
