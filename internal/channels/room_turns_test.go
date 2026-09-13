package channels

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func settleRoomMember(t *testing.T, s *Service, id, turn, reply string) {
	t.Helper()
	ctx := context.Background()
	member, binding := prepareIdentityTestTurn(t, s, id)
	if _, err := member.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: binding.SessionRef, State: CollaborationSessionRunning, TurnID: turn}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: binding.SessionRef, TurnID: turn, State: CollaborationSessionIdle, Result: "Turn finished", PublicReply: reply}); err != nil {
		t.Fatal(err)
	}
}

func pendingRoomMember(t *testing.T, s *Service, room Room) string {
	t.Helper()
	var id string
	err := s.db.QueryRow(`SELECT delivery.to_agent_id FROM room_turns turn JOIN collaboration_messages delivery ON delivery.id=turn.delivery_id WHERE turn.room_id=?`, room.ID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func assertRoomDiscussionEnded(t *testing.T, s *Service, room Room) {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM room_turns WHERE room_id=?`, room.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("discussion remains active: %d %v", n, err)
	}
	for _, member := range room.Members {
		if member.MemberType != MemberAgent {
			continue
		}
		pending, err := s.PendingCollaborationDispatches(context.Background(), member.MemberID)
		if err != nil || len(pending) != 0 {
			t.Fatalf("pending after discussion: %+v %v", pending, err)
		}
	}
}

func TestRoomDiscussionSerializesMembersRotatesAndStopsOnPass(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	alpha, beta := createTestAgent(t, s, "Alpha"), createTestAgent(t, s, "Beta")
	room := createTestRoom(t, s, alpha, beta)
	sent, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Explain the failure"})
	if err != nil {
		t.Fatal(err)
	}
	if id := pendingRoomMember(t, s, room); id != alpha.Agent.ID {
		t.Fatalf("first speaker %s", id)
	}
	pending, _ := s.PendingCollaborationDispatches(ctx, beta.Agent.ID)
	if len(pending) != 0 {
		t.Fatal("second member started concurrently")
	}
	settleRoomMember(t, s, alpha.Agent.ID, "alpha-1", "The delivery filter excludes the message.")
	if id := pendingRoomMember(t, s, room); id != beta.Agent.ID {
		t.Fatalf("second speaker %s", id)
	}
	pending, _ = s.PendingCollaborationDispatches(ctx, beta.Agent.ID)
	prompt, err := s.RoomTurnPrompt(ctx, pending[0].ID)
	if err != nil || !strings.Contains(prompt, sent.Message.ID) || !strings.Contains(prompt, "The delivery filter excludes the message.") {
		t.Fatalf("next speaker lost source or prior reply: %s %v", prompt, err)
	}
	settleRoomMember(t, s, beta.Agent.ID, "beta-1", "")
	if id := pendingRoomMember(t, s, room); id != beta.Agent.ID {
		t.Fatalf("next round did not rotate: %s", id)
	}
	settleRoomMember(t, s, beta.Agent.ID, "beta-2", "")
	settleRoomMember(t, s, alpha.Agent.ID, "alpha-2", "")
	assertRoomDiscussionEnded(t, s, room)
	runtimes, err := s.ListAgentRuntimes(ctx)
	if err != nil || len(runtimes) != 2 {
		t.Fatalf("unexpected hidden runtime: %+v %v", runtimes, err)
	}
}

func TestRoomDiscussionBoundsProductiveRoundsAndTotalTurns(t *testing.T) {
	for _, size := range []int{2, 6} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			ctx := context.Background()
			s := openTestService(t, nil)
			var agents []AgentCredential
			for i := 0; i < size; i++ {
				agents = append(agents, createTestAgent(t, s, fmt.Sprintf("Member%d", i)))
			}
			room := createTestRoom(t, s, agents...)
			if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "@all discuss"}); err != nil {
				t.Fatal(err)
			}
			limit := min(size*roomMaxRounds, roomMaxTurns)
			for i := 0; i < limit; i++ {
				id := pendingRoomMember(t, s, room)
				settleRoomMember(t, s, id, fmt.Sprintf("turn-%d", i), fmt.Sprintf("Finding %d", i))
			}
			assertRoomDiscussionEnded(t, s, room)
		})
	}
}

func TestRoomDiscussionAllPassNeedsNoRetry(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	alpha, beta := createTestAgent(t, s, "Alpha"), createTestAgent(t, s, "Beta")
	room := createTestRoom(t, s, alpha, beta)
	if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Discuss"}); err != nil {
		t.Fatal(err)
	}
	settleRoomMember(t, s, alpha.Agent.ID, "alpha-pass", "")
	settleRoomMember(t, s, beta.Agent.ID, "beta-pass", "")
	assertRoomDiscussionEnded(t, s, room)
}

func TestRoomDiscussionNewInputRetiresQueuedOldSpeaker(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	alpha, beta := createTestAgent(t, s, "Alpha"), createTestAgent(t, s, "Beta")
	room := createTestRoom(t, s, alpha, beta)
	if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Old question"}); err != nil {
		t.Fatal(err)
	}
	settleRoomMember(t, s, alpha.Agent.ID, "old-answer", "A first answer")
	sent, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "@Alpha answer this instead"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingCollaborationDispatches(ctx, beta.Agent.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("old speaker was not retired: %+v %v", pending, err)
	}
	pending, err = s.PendingCollaborationDispatches(ctx, alpha.Agent.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("new input not delivered: %+v %v", pending, err)
	}
	member, _ := prepareIdentityTestTurn(t, s, alpha.Agent.ID)
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM collaboration_messages WHERE source_message_id=? AND consumed_at IS NOT NULL`, sent.Message.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("new input not consumed once: %d %v", count, err)
	}
	_ = member
}

func TestRoomDiscussionRestartPreservesPendingSpeaker(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	alpha, beta := createTestAgent(t, s, "Alpha"), createTestAgent(t, s, "Beta")
	room := createTestRoom(t, s, alpha, beta)
	if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Discuss"}); err != nil {
		t.Fatal(err)
	}
	settleRoomMember(t, s, alpha.Agent.ID, "alpha", "Evidence")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if id := pendingRoomMember(t, s, room); id != beta.Agent.ID {
		t.Fatalf("restart repeated first speaker: %s", id)
	}
	pending, _ := s.PendingCollaborationDispatches(ctx, alpha.Agent.ID)
	if len(pending) != 0 {
		t.Fatalf("restart reconstructed duplicate deliveries: %+v", pending)
	}
	settleRoomMember(t, s, beta.Agent.ID, "beta", "")
	settleRoomMember(t, s, beta.Agent.ID, "beta-next", "")
	settleRoomMember(t, s, alpha.Agent.ID, "alpha-next", "")
	assertRoomDiscussionEnded(t, s, room)
}

func TestCompletedWorkFollowupCanBeReceivedAndDoesNotWakeAgain(t *testing.T) {
	for _, check := range []bool{false, true} {
		t.Run(fmt.Sprint(check), func(t *testing.T) {
			ctx := context.Background()
			s, _, agent, room, task := newIdentityTaskFixture(t)
			sender, err := bindTestRoomLead(t, ctx, s, room.ID)
			if err != nil {
				t.Fatal(err)
			}
			owner, binding := prepareIdentityTestTurn(t, s, agent.Agent.ID)
			if _, err := owner.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, State: TaskStateDone}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: binding.SessionRef, TurnID: "done", State: CollaborationSessionIdle, Result: "Done"}); err != nil {
				t.Fatal(err)
			}
			message, err := sender.SendCollaboration(ctx, CollaborationSendParams{RoomID: room.ID, ToAgentID: agent.Agent.ID, WorkID: task.ID, Body: "Follow up", Kind: CollaborationControl, RequestID: "followup"})
			if err != nil {
				t.Fatal(err)
			}
			runtime, _ := s.GetAgentRuntime(ctx, agent.Agent.ID)
			next, ready, err := s.PrepareIdentityConversation(ctx, runtime, CollaborationSessionBindParams{})
			if err != nil || !ready || next.WorkID != "" {
				t.Fatalf("follow-up admission: %+v %v %v", next, ready, err)
			}
			client, err := s.BindAgentSession(ctx, agent.Agent.ID, next.SessionRef)
			if err != nil {
				t.Fatal(err)
			}
			if check {
				inbox, err := client.Check(ctx)
				if err != nil || len(inbox.Collaboration) != 1 || inbox.Collaboration[0].ID != message.ID {
					t.Fatalf("follow-up missing from check: %+v %v", inbox, err)
				}
			} else {
				received, err := client.ReceiveCollaboration(ctx, 10)
				if err != nil || len(received) != 1 || received[0].ID != message.ID {
					t.Fatalf("follow-up missing from receive: %+v %v", received, err)
				}
				if err := client.AcknowledgeCollaboration(ctx, []string{message.ID}); err != nil {
					t.Fatal(err)
				}
			}
			pending, err := s.PendingCollaborationDispatches(ctx, agent.Agent.ID)
			if err != nil || len(pending) != 0 {
				t.Fatalf("consumed follow-up still wakes: %+v %v", pending, err)
			}
		})
	}
}

func TestRoomDiscussionSkipsDepartedMember(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	alpha, beta, gamma := createTestAgent(t, s, "Alpha"), createTestAgent(t, s, "Beta"), createTestAgent(t, s, "Gamma")
	room := createTestRoom(t, s, alpha, beta, gamma)
	if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Discuss"}); err != nil {
		t.Fatal(err)
	}
	settleRoomMember(t, s, alpha.Agent.ID, "alpha", "")
	members := []RoomMember{{MemberType: MemberAgent, MemberID: alpha.Agent.ID}, {MemberType: MemberAgent, MemberID: gamma.Agent.ID}}
	if _, err := s.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Members: &members}); err != nil {
		t.Fatal(err)
	}
	if id := pendingRoomMember(t, s, room); id != gamma.Agent.ID {
		t.Fatalf("removed member blocked discussion: %s", id)
	}
	settleRoomMember(t, s, gamma.Agent.ID, "gamma", "")
	assertRoomDiscussionEnded(t, s, room)
}

func TestRoomDiscussionCancellationFencesQueuedAndRunningTurns(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprint(running), func(t *testing.T) {
			ctx := context.Background()
			s := openTestService(t, nil)
			alpha, beta := createTestAgent(t, s, "Alpha"), createTestAgent(t, s, "Beta")
			room := createTestRoom(t, s, alpha, beta)
			if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Discuss"}); err != nil {
				t.Fatal(err)
			}
			agent, _ := s.GetAgentRuntime(ctx, alpha.Agent.ID)
			binding, _, err := s.PrepareIdentityConversation(ctx, agent, CollaborationSessionBindParams{})
			if err != nil {
				t.Fatal(err)
			}
			if running {
				member, err := s.BindAgentSession(ctx, alpha.Agent.ID, binding.SessionRef)
				if err != nil {
					t.Fatal(err)
				}
				received, err := member.ReceiveCollaboration(ctx, 10)
				if err != nil {
					t.Fatal(err)
				}
				if err := member.AcknowledgeCollaboration(ctx, []string{received[0].ID}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.AdmitCollaborationSession(ctx, binding.SessionRef); err != nil {
					t.Fatal(err)
				}
				if _, err := member.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: binding.SessionRef, State: CollaborationSessionRunning, TurnID: "cancelled-turn"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.CancelCollaborationSessions(ctx, binding.SessionRef); err != nil {
				t.Fatal(err)
			}
			assertRoomDiscussionEnded(t, s, room)
			if running {
				if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: binding.SessionRef, TurnID: "cancelled-turn", State: CollaborationSessionIdle, PublicReply: "Late reply"}); err == nil {
					t.Fatal("cancelled turn accepted a late result")
				}
			}
		})
	}
}

func TestRoomDiscussionAdmissionFailureAdvancesWithoutRepeatingInput(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	alpha, beta := createTestAgent(t, s, "Alpha"), createTestAgent(t, s, "Beta")
	room := createTestRoom(t, s, alpha, beta)
	if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Discuss"}); err != nil {
		t.Fatal(err)
	}
	agent, _ := s.GetAgentRuntime(ctx, alpha.Agent.ID)
	binding, _, err := s.PrepareIdentityConversation(ctx, agent, CollaborationSessionBindParams{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdmitCollaborationSession(ctx, binding.SessionRef); err != nil {
		t.Fatal(err)
	}
	member, _ := s.BindAgentSession(ctx, alpha.Agent.ID, binding.SessionRef)
	if _, err := member.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: binding.SessionRef, State: CollaborationSessionRunning, TurnID: "failed-admission"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: binding.SessionRef, TurnID: "failed-admission", State: CollaborationSessionFailed, AdmissionFailure: true, FailureReason: "Provider unavailable"}); err != nil {
		t.Fatal(err)
	}
	if id := pendingRoomMember(t, s, room); id != beta.Agent.ID {
		t.Fatalf("failed member still owns turn: %s", id)
	}
	pending, err := s.PendingCollaborationDispatches(ctx, alpha.Agent.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("failed input remains pending: %+v %v", pending, err)
	}
	settleRoomMember(t, s, beta.Agent.ID, "beta", "")
	assertRoomDiscussionEnded(t, s, room)
}

func TestCompletedQueuedAssignmentDoesNotStartEmptyTurn(t *testing.T) {
	ctx := context.Background()
	s, client, agent, _, task := newIdentityTaskFixture(t)
	if _, err := client.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, State: TaskStateDone}); err != nil {
		t.Fatal(err)
	}
	runtime, _ := s.GetAgentRuntime(ctx, agent.Agent.ID)
	_, ready, err := s.PrepareIdentityConversation(ctx, runtime, CollaborationSessionBindParams{})
	if err != nil || ready {
		t.Fatalf("finished queued assignment admitted a model turn: %v %v", ready, err)
	}
	pending, err := s.PendingCollaborationDispatches(ctx, agent.Agent.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("finished assignment stays pending: %+v %v", pending, err)
	}
}

func TestRoomAdmissionFailurePreservesLaterSessionInput(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	alpha, beta := createTestAgent(t, s, "Alpha"), createTestAgent(t, s, "Beta")
	room := createTestRoom(t, s, alpha, beta)
	if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Discuss"}); err != nil {
		t.Fatal(err)
	}
	agent, _ := s.GetAgentRuntime(ctx, alpha.Agent.ID)
	binding, _, err := s.PrepareIdentityConversation(ctx, agent, CollaborationSessionBindParams{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdmitCollaborationSession(ctx, binding.SessionRef); err != nil {
		t.Fatal(err)
	}
	member, _ := s.BindAgentSession(ctx, alpha.Agent.ID, binding.SessionRef)
	if _, err := member.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: binding.SessionRef, State: CollaborationSessionRunning, TurnID: "admission"}); err != nil {
		t.Fatal(err)
	}
	sender, _ := s.BindAgent(ctx, beta.Agent.ID)
	later, err := sender.SendCollaboration(ctx, CollaborationSendParams{RoomID: room.ID, TargetSessionRef: binding.SessionRef, Body: "New evidence arrived after admission", RequestID: "later-input"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: binding.SessionRef, TurnID: "admission", State: CollaborationSessionFailed, AdmissionFailure: true, FailureReason: "Provider unavailable"}); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingCollaborationDispatches(ctx, alpha.Agent.ID)
	if err != nil || len(pending) != 1 || pending[0].ID != later.ID {
		t.Fatalf("admission failure consumed a later delivery: %+v %v", pending, err)
	}
}
