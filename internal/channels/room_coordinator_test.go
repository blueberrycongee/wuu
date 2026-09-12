package channels

import (
	"context"
	"errors"
	"testing"
)

func prepareTestCoordinator(t *testing.T, service *Service, room Room) *AgentClient {
	t.Helper()
	runtime, err := service.GetRoomRuntime(context.Background(), room.RuntimeID)
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

func TestRoomCoordinatorRoutesUnaddressedInputAndPreservesExplicitRecipients(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	alice, bob := createTestAgent(t, service, "Alice"), createTestAgent(t, service, "Bob")
	room := createTestRoom(t, service, alice, bob)
	coordinator := prepareTestCoordinator(t, service, room)
	for _, tc := range []struct {
		body    string
		targets []string
	}{
		{"Find the cause", []string{coordinator.AgentID()}},
		{"@Alice inspect it", []string{alice.Agent.ID}},
		{"@all compare independent explanations", []string{alice.Agent.ID, bob.Agent.ID}},
	} {
		sent, err := service.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: tc.body})
		if err != nil {
			t.Fatal(err)
		}
		got := sink.take()
		if len(got) != len(tc.targets) {
			t.Fatalf("%s wakes: %v", tc.body, got)
		}
		for _, id := range tc.targets {
			found := false
			for _, target := range got {
				if target == id {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s missing %s: %v", tc.body, id, got)
			}
			if _, err := service.db.ExecContext(ctx, `UPDATE collaboration_messages SET pulled_at=1,consumed_at=1 WHERE to_agent_id=? AND source_message_id=?`, id, sent.Message.ID); err != nil {
				t.Fatal(err)
			}
			if err := service.ClearWakeOnCheck(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	aliceClient, _ := service.BindAgent(ctx, alice.Agent.ID)
	messages, _ := service.ListMessages(ctx, room.ID, 0, 50)
	reply, err := aliceClient.Send(ctx, AgentSendParams{RoomID: room.ID, Body: "A finding", BasisSeq: messages[len(messages)-1].Seq})
	if err != nil {
		t.Fatal(err)
	}
	if got := sink.take(); len(got) != 0 {
		t.Fatalf("passive post woke %v", got)
	}
	if _, err := service.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Explain that", ReplyTo: reply.Message.ID}); err != nil {
		t.Fatal(err)
	}
	if got := sink.take(); len(got) != 1 || got[0] != alice.Agent.ID {
		t.Fatalf("reply recipients: %v", got)
	}
}

func TestRoomCoordinatorUpgradeAndRestartPreserveDelegation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	alice := createTestAgent(t, service, "Alice")
	room := createTestRoom(t, service, alice)
	coordinator := prepareTestCoordinator(t, service, room)
	if _, err := coordinator.BindCollaborationSession(ctx, CollaborationSessionBindParams{SessionRef: "old-coordinator", RoomID: room.ID, Purpose: CollaborationSessionCoordination}); err != nil {
		t.Fatal(err)
	}
	// Emulate a database from the retired-coordinator release.
	for _, query := range []string{`DELETE FROM channel_metadata WHERE key='room_coordinator_version'`, `UPDATE room_runtimes SET autostart=0`, `UPDATE collaboration_session_bindings SET state='cancelled' WHERE session_ref='old-coordinator'`} {
		if _, err := service.db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{RoomID: room.ID, ToAgentID: coordinator.AgentID(), TargetSessionRef: "old-coordinator", Kind: CollaborationCompletion, Body: "Saved result", CreatedAt: service.now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err = service.BindRuntime(ctx, room.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.BindCollaborationSession(ctx, CollaborationSessionBindParams{SessionRef: "new-coordinator", RoomID: room.ID, Purpose: CollaborationSessionCoordination}); err != nil {
		t.Fatal(err)
	}
	coordinator, err = service.BindRuntimeSession(ctx, room.RuntimeID, "new-coordinator")
	if err != nil {
		t.Fatal(err)
	}
	received, err := coordinator.ReceiveCollaboration(ctx, 10)
	if err != nil || len(received) != 1 || received[0].ID != pending.ID {
		t.Fatalf("upgrade lost pending result: %#v %v", received, err)
	}
	if err := coordinator.AcknowledgeCollaboration(ctx, []string{pending.ID}); err != nil {
		t.Fatal(err)
	}
	service.SetSessionController(&recordingSessionController{service: service})
	child, err := coordinator.CreateSession(ctx, CollaborationSessionCreateParams{NamedAgentID: alice.Agent.ID, RoomID: room.ID, Objective: "Inspect the reconnect", RequestID: "job-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: "new-coordinator", State: CollaborationSessionWaiting}); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: child.SessionRef, TurnID: "worker-turn", State: CollaborationSessionCompleted, Result: "The retry lost its cursor"}); err != nil {
		t.Fatal(err)
	}
	coordinator, err = service.BindRuntimeSession(ctx, room.RuntimeID, "new-coordinator")
	if err != nil {
		t.Fatal(err)
	}
	received, err = coordinator.ReceiveCollaboration(ctx, 10)
	if err != nil || len(received) != 1 || received[0].Body != "The retry lost its cursor" {
		t.Fatalf("restart lost delegated result: %#v %v", received, err)
	}
	service.SetSessionController(&recordingSessionController{service: service})
	duplicate, err := coordinator.CreateSession(ctx, CollaborationSessionCreateParams{NamedAgentID: alice.Agent.ID, RoomID: room.ID, Objective: "Inspect the reconnect", RequestID: "job-1"})
	if err != nil || duplicate.SessionRef != child.SessionRef {
		t.Fatalf("duplicate job after restart: %#v %v", duplicate, err)
	}
}

func TestRoomCoordinatorOwnsWorkResultsAndMembershipChanges(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	alice, bob := createTestAgent(t, service, "Alice"), createTestAgent(t, service, "Bob")
	room := createTestRoom(t, service, alice, bob)
	coordinator := prepareTestCoordinator(t, service, room)
	if _, err := coordinator.BindCollaborationSession(ctx, CollaborationSessionBindParams{SessionRef: "manager", RoomID: room.ID, Purpose: CollaborationSessionCoordination}); err != nil {
		t.Fatal(err)
	}
	coordinator, err := service.BindRuntimeSession(ctx, room.RuntimeID, "manager")
	if err != nil {
		t.Fatal(err)
	}
	task, err := coordinator.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Investigate", Body: "Inspect evidence", OwnerID: alice.Agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if task.AuthorID != alice.Agent.ID || task.Work == nil || task.Work.LeadNamedAgentID != alice.Agent.ID {
		t.Fatalf("hidden author leaked into task: %#v", task)
	}
	updated, err := coordinator.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, GoalCorrection: "Inspect the latest reconnect evidence"})
	if err != nil || updated.TaskGoalRevision != 2 {
		t.Fatalf("coordinator could not revise the room goal: %#v %v", updated, err)
	}

	run, err := coordinator.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, NamedAgentID: alice.Agent.ID, Kind: WorkRunProducer, RequestID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := service.BindAgent(ctx, alice.Agent.ID)
	if _, err := worker.FinishWorkRun(ctx, WorkRunFinishParams{WorkID: task.ID, RunID: run.ID, State: WorkRunFailed, Outcome: "Missing evidence"}); err != nil {
		t.Fatal(err)
	}
	results, err := coordinator.ReceiveCollaboration(ctx, 10)
	if err != nil || len(results) != 1 || results[0].Kind != CollaborationWorkRunTerminal || results[0].WorkID != task.ID {
		t.Fatalf("room lost work result: %#v %v", results, err)
	}
	if err := coordinator.AcknowledgeCollaboration(ctx, []string{results[0].ID}); err != nil {
		t.Fatal(err)
	}
	members := []RoomMember{{MemberType: MemberAgent, MemberID: alice.Agent.ID}}
	if _, err := service.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Members: &members}); err != nil {
		t.Fatal(err)
	}
	results, err = coordinator.ReceiveCollaboration(ctx, 10)
	if err != nil || len(results) != 1 || results[0].SourceMessageID == "" {
		t.Fatalf("membership change lost: %#v %v", results, err)
	}
	loaded, err := service.GetRoom(ctx, room.ID)
	if err != nil || loaded.RuntimeID != room.RuntimeID {
		t.Fatal("membership changed coordinator identity")
	}
}
