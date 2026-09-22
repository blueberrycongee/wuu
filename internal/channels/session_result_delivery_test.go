package channels

import (
	"context"
	"errors"
	"testing"
)

func prepareChildResultParent(t *testing.T) (*Service, *AgentClient, AgentCredential, Room, Message, SessionResultEnqueueParams) {
	t.Helper()
	s, _, agent, room, task := newIdentityTaskFixture(t)
	client, binding := prepareIdentityTestTurn(t, s, agent.Agent.ID)
	if _, err := client.UpdateCollaborationSessionState(context.Background(), CollaborationSessionStateParams{SessionRef: binding.SessionRef, TurnID: "parent-turn", State: CollaborationSessionRunning}); err != nil {
		t.Fatal(err)
	}
	return s, client, agent, room, task, SessionResultEnqueueParams{ParentSessionRef: binding.SessionRef, ParentTurnID: "parent-turn", SourceSessionRef: "temporary-child", RequestID: "child-result", Body: "Child found evidence"}
}

func TestChildResultReturnsToOriginalRoomAndTaskOnce(t *testing.T) {
	ctx := context.Background()
	s, _, agent, room, task, params := prepareChildResultParent(t)
	if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: params.ParentSessionRef, TurnID: params.ParentTurnID, State: CollaborationSessionIdle, Result: "Investigation started"}); err != nil {
		t.Fatal(err)
	}
	other := createTestRoom(t, s, agent)
	if _, err := s.EnqueueSessionInput(ctx, CollaborationSessionSendParams{SessionRef: params.ParentSessionRef, RoomID: other.ID, Body: "Independent room request", RequestID: "other-room"}); err != nil {
		t.Fatal(err)
	}
	current, binding := prepareIdentityTestTurn(t, s, agent.Agent.ID)
	if binding.RoomID != other.ID {
		t.Fatal("parent did not switch rooms")
	}
	params.TerminalState = CollaborationTerminalCompleted
	first, err := s.EnqueueSessionResult(ctx, params)
	if err != nil || first.Discarded || first.Message == nil || first.Message.RoomID != room.ID || first.Message.WorkID != task.ID || first.Message.GoalRevision != task.TaskGoalRevision || first.Message.Kind != CollaborationPeerResult {
		t.Fatalf("child result lost its original scope: %+v %v", first, err)
	}
	// A persisted receipt stays authoritative when the host's outcome projection
	// changes across an upgrade, including older receipts marked completed.
	replayParams := params
	replayParams.TerminalState = CollaborationTerminalFailed
	replay, err := s.EnqueueSessionResult(ctx, replayParams)
	if err != nil || replay.Message == nil || replay.Message.ID != first.Message.ID || replay.Message.TerminalState != first.Message.TerminalState {
		t.Fatalf("result replay duplicated or relabeled its receipt: %+v %v", replay, err)
	}
	conflict := params
	conflict.Body = "A different result"
	if _, err := s.EnqueueSessionResult(ctx, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("request id accepted conflicting evidence: %v", err)
	}
	wrongRoom, err := current.ReceiveCollaboration(ctx, 32)
	if err != nil || len(wrongRoom) != 0 {
		t.Fatalf("active room consumed another room's child result: %+v %v", wrongRoom, err)
	}
	if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: binding.SessionRef, TurnID: "other-turn", State: CollaborationSessionIdle, Result: "Independent request answered"}); err != nil {
		t.Fatal(err)
	}
	_, continued := prepareIdentityTestTurn(t, s, agent.Agent.ID)
	if continued.RoomID != room.ID || continued.WorkID != task.ID {
		t.Fatalf("child result resumed an unrelated task: %+v", continued)
	}
}

func TestChildResultOutcomeSurvivesRestartWithoutCompletingTask(t *testing.T) {
	for _, outcome := range []CollaborationTerminalState{"", CollaborationTerminalCompleted, CollaborationTerminalFailed, CollaborationTerminalInterrupted} {
		t.Run(string(outcome), func(t *testing.T) {
			ctx := context.Background()
			service, _, agent, room, task, params := prepareChildResultParent(t)
			params.TerminalState = outcome
			first, err := service.EnqueueSessionResult(ctx, params)
			if err != nil || first.Message == nil {
				t.Fatalf("enqueue result: %+v %v", first, err)
			}
			dir := service.dir
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			service, err = Open(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			replay, err := service.EnqueueSessionResult(ctx, params)
			if err != nil || replay.Message == nil || replay.Message.ID != first.Message.ID {
				t.Fatalf("recovery duplicated a result: %+v %v", replay, err)
			}
			client, err := service.BindAgentSession(ctx, agent.Agent.ID, params.ParentSessionRef)
			if err != nil {
				t.Fatal(err)
			}
			messages, err := client.ReceiveCollaboration(ctx, 32)
			if err != nil || len(messages) != 1 {
				t.Fatalf("receive persisted result: %+v %v", messages, err)
			}
			message := messages[0]
			if message.TerminalState != outcome || message.RoomID != room.ID || message.WorkID != task.ID {
				t.Fatalf("result changed its execution outcome or scope: %+v", message)
			}
			work, err := service.GetWork(ctx, task.ID)
			if err != nil || terminalWorkState(work.State) {
				t.Fatalf("execution result ended the responsibility: %+v %v", work, err)
			}
		})
	}
}

func TestChildResultDiscardsRevokedScope(t *testing.T) {
	for _, action := range []string{"revise", "cancel", "reassign", "unknown-turn", "remove-member"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			s, client, _, room, task, params := prepareChildResultParent(t)
			var err error
			switch action {
			case "revise":
				_, err = s.UpdateTaskHuman(ctx, TaskUpdateParams{TaskID: task.ID, HumanID: "human-1", GoalCorrection: "Changed requirements"})
			case "cancel":
				_, err = client.CancelWork(ctx, task.ID, "No longer needed")
			case "unknown-turn":
				params.ParentTurnID = "missing"
			case "remove-member":
				members := []RoomMember{{MemberType: MemberHuman, MemberID: "human-1"}}
				if _, err = client.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, State: TaskStateDone}); err == nil {
					_, err = s.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Members: &members})
				}
			case "reassign":
				peer := createTestAgent(t, s, "New owner")
				members := append(room.Members, RoomMember{MemberType: MemberAgent, MemberID: peer.Agent.ID})
				if _, err = s.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Members: &members}); err == nil {
					_, err = s.UpdateTaskHuman(ctx, TaskUpdateParams{TaskID: task.ID, HumanID: "human-1", OwnerID: peer.Agent.ID})
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			sink := &recordingWakeSink{}
			s.SetWakeSink(sink)
			result, err := s.EnqueueSessionResult(ctx, params)
			if err != nil || !result.Discarded || result.Message != nil || len(sink.take()) != 0 {
				t.Fatalf("revoked child result was delivered or woke the parent: %+v %v", result, err)
			}
		})
	}
}

func TestStoppedIdentityRetainsChildResultWithoutWake(t *testing.T) {
	ctx := context.Background()
	s, client, _, _, _, params := prepareChildResultParent(t)
	if _, err := client.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: params.ParentSessionRef, State: CollaborationSessionCancelled}); err != nil {
		t.Fatal(err)
	}
	sink := &recordingWakeSink{}
	s.SetWakeSink(sink)
	result, err := s.EnqueueSessionResult(ctx, params)
	if err != nil || result.Discarded || result.Message == nil || len(sink.take()) != 0 {
		t.Fatalf("stopped identity lost its result or was restarted: %+v %v", result, err)
	}
	if _, err := client.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: params.ParentSessionRef, State: CollaborationSessionIdle}); err != nil {
		t.Fatal(err)
	}
	messages, err := client.ReceiveCollaboration(ctx, 32)
	if err != nil || len(messages) != 1 || messages[0].ID != result.Message.ID {
		t.Fatalf("resume lost the pending child result: %+v %v", messages, err)
	}
}
