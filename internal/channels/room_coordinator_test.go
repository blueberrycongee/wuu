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
