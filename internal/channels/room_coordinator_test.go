package channels

import (
	"context"
	"errors"
	"testing"
)

func prepareTestCoordinator(t *testing.T, service *Service, room Room) *AgentClient {
	t.Helper()
	runtime, err := service.createRoomRuntime(context.Background(), room.ID, room.Name)
	if err != nil {
		t.Fatal(err)
	}
	client, err := service.BindRuntime(context.Background(), runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestRoomCoordinatorIdentityAndSessionBoundaries(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	alice := createTestAgent(t, service, "Alice")
	room := createTestRoom(t, service, alice)
	other := createTestRoom(t, service, alice)
	coordinator := prepareTestCoordinator(t, service, room)
	if _, err := coordinator.BindCollaborationSession(ctx, CollaborationSessionBindParams{SessionRef: "coordinator", RoomID: room.ID, Purpose: CollaborationSessionCoordination}); err != nil {
		t.Fatal(err)
	}
	coordinator, err := service.BindRuntimeSession(ctx, coordinator.AgentID(), "coordinator")
	if err != nil {
		t.Fatal(err)
	}
	worker := flexibleTestSession(t, service, alice.Agent.ID, room.ID, "worker")
	outsider := flexibleTestSession(t, service, alice.Agent.ID, other.ID, "other-worker")
	for _, params := range []CollaborationSessionListParams{{}, {RoomID: room.ID}} {
		bindings, err := coordinator.ListCollaborationSessions(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		if len(bindings) != 2 {
			t.Fatalf("room session list = %#v, want coordinator and worker", bindings)
		}
		for _, binding := range bindings {
			if binding.RoomID != room.ID {
				t.Fatalf("coordinator listed another room's session: %#v", binding)
			}
		}
	}
	if _, err := coordinator.GetCollaborationSession(ctx, worker.SessionRef()); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.GetCollaborationSession(ctx, outsider.SessionRef()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("cross-room metadata: %v", err)
	}
	if _, err := coordinator.ReadRoom(ctx, other.ID, 0, 10); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("cross-room history: %v", err)
	}
	if _, err := coordinator.ListCollaborationSessions(ctx, CollaborationSessionListParams{RoomID: other.ID}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("cross-room list: %v", err)
	}
	if _, err := coordinator.BindCollaborationSession(ctx, CollaborationSessionBindParams{SessionRef: "wrong-room", RoomID: other.ID, Purpose: CollaborationSessionCoordination}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("cross-room binding: %v", err)
	}
	if _, err := coordinator.BindCollaborationSession(ctx, CollaborationSessionBindParams{SessionRef: "wrong-role", RoomID: room.ID, Purpose: CollaborationSessionWork}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("execution binding: %v", err)
	}
	if _, err := coordinator.Send(ctx, AgentSendParams{RoomID: room.ID, Body: "I am Alice"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("public impersonation: %v", err)
	}
	if _, err := service.ReceiveCollaboration(ctx, coordinator.agentID, coordinator.token, worker.SessionRef(), 10); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("worker inbox: %v", err)
	}
	visible, err := service.ListNamedAgents(ctx)
	if err != nil || len(visible) != 1 || visible[0].ID != alice.Agent.ID {
		t.Fatalf("visible identities: %#v %v", visible, err)
	}
	loaded, err := service.GetRoom(ctx, room.ID)
	if err != nil || len(loaded.Members) != len(room.Members) {
		t.Fatalf("coordinator became a member: %#v %v", loaded, err)
	}
}

func TestCoordinatorRetirementPreservesOriginalRequestAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	owner := createTestAgent(t, s, "Owner")
	room := createTestRoom(t, s, owner)
	coordinator := prepareTestCoordinator(t, s, room)
	if _, err := coordinator.BindCollaborationSession(ctx, CollaborationSessionBindParams{SessionRef: "legacy-room", RoomID: room.ID, Purpose: CollaborationSessionCoordination}); err != nil {
		t.Fatal(err)
	}
	source, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Explain yield_turn"})
	if err != nil {
		t.Fatal(err)
	}
	// The old release routed this original request only to its hidden runtime.
	if _, err := s.db.Exec(`UPDATE collaboration_messages SET to_agent_id=?,target_session_ref='legacy-room' WHERE source_message_id=?`, coordinator.AgentID(), source.Message.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindRuntime(ctx, coordinator.AgentID()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("retired runtime remains callable: %v", err)
	}
	pending, err := s.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("original request not recovered: %+v %v", pending, err)
	}
	member, binding := prepareIdentityTestTurn(t, s, owner.Agent.ID)
	if _, err := member.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: binding.SessionRef, State: CollaborationSessionRunning, TurnID: "answer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: binding.SessionRef, TurnID: "answer", State: CollaborationSessionIdle, PublicReply: "It releases this turn."}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("restart repeated the request: %+v %v", pending, err)
	}
	history, err := s.ListMessages(ctx, room.ID, 0, 10)
	if err != nil || len(history) != 2 || history[0].Body != "Explain yield_turn" {
		t.Fatalf("history lost: %+v %v", history, err)
	}
}

func TestCoordinatorRetirementDiscardsUnexecutedRewriteOfAnsweredRequest(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	owner := createTestAgent(t, s, "Owner")
	room := createTestRoom(t, s, owner)
	coordinator := prepareTestCoordinator(t, s, room)
	source, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Continue"})
	if err != nil {
		t.Fatal(err)
	}
	settleRoomMember(t, s, owner.Agent.ID, "answer", "Here is the explanation you asked for.")
	rewrite, err := coordinator.SendCollaboration(ctx, CollaborationSendParams{RoomID: room.ID, ToAgentID: owner.Agent.ID, SourceMessageID: source.Message.ID, Body: "Open a pull request for the old task", RequestID: "wrong-rewrite"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.initializeRoomScheduling(ctx); err != nil {
		t.Fatal(err)
	}
	var invalid bool
	if err := s.db.QueryRow(`SELECT invalidated_at IS NOT NULL FROM collaboration_messages WHERE id=?`, rewrite.ID).Scan(&invalid); err != nil || !invalid {
		t.Fatalf("unexecuted rewrite survived retirement: %v %v", invalid, err)
	}
	pending, err := s.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("answered request started more work: %+v %v", pending, err)
	}
}
