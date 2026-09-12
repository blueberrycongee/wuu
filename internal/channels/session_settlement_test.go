package channels

import (
	"context"
	"errors"
	"testing"
)

func bindSessionChild(t *testing.T, service *Service, ownerID, roomID, parentRef, childRef string) *AgentClient {
	t.Helper()
	client, err := service.BindAgent(context.Background(), ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.BindCollaborationSession(context.Background(), CollaborationSessionBindParams{SessionRef: childRef, RoomID: roomID, ParentSessionRef: parentRef, Purpose: CollaborationSessionWork, Objective: "Test one independent hypothesis"}); err != nil {
		t.Fatal(err)
	}
	session, err := service.BindAgentSession(context.Background(), ownerID, childRef)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestSessionSettlementReplayCannotOverwriteNewTurn(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	parent := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "parent")
	child := bindSessionChild(t, service, owner.Agent.ID, room.ID, parent.SessionRef(), "child")
	if _, err := child.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: child.SessionRef(), State: CollaborationSessionRunning, TurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	params := CollaborationSessionSettleParams{SessionRef: child.SessionRef(), State: CollaborationSessionCompleted, Result: "Independent check found a counterexample", TurnID: "turn-1"}
	settled, err := service.SettleCollaborationSession(ctx, params)
	if err != nil || settled.State != CollaborationSessionCompleted || settled.TurnID != "turn-1" {
		t.Fatalf("settlement = %#v, %v", settled, err)
	}
	result, err := parent.ReceiveCollaboration(ctx, 10)
	if err != nil || len(result) != 1 || result[0].FromSessionRef != child.SessionRef() || result[0].FromID != owner.Agent.ID || result[0].TerminalState != CollaborationTerminalCompleted || result[0].Body != params.Result {
		t.Fatalf("parent result = %#v, %v", result, err)
	}
	if err := parent.AcknowledgeCollaboration(ctx, []string{result[0].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdmitCollaborationSession(ctx, child.SessionRef()); err != nil {
		t.Fatal(err)
	}
	if _, err := child.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: child.SessionRef(), State: CollaborationSessionRunning, TurnID: "turn-2"}); err != nil {
		t.Fatal(err)
	}
	replay, err := service.SettleCollaborationSession(ctx, params)
	if err != nil || replay.State != CollaborationSessionRunning || replay.TurnID != "turn-2" {
		t.Fatalf("old replay changed active turn = %#v, %v", replay, err)
	}
	duplicate, err := parent.ReceiveCollaboration(ctx, 10)
	if err != nil || len(duplicate) != 0 {
		t.Fatalf("duplicate parent result = %#v, %v", duplicate, err)
	}
	changed := params
	changed.Result = "Different result for the same turn"
	if _, err := service.SettleCollaborationSession(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting replay = %v", err)
	}
	stale := params
	stale.TurnID = "obsolete-turn"
	if _, err := service.SettleCollaborationSession(ctx, stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale turn result = %v", err)
	}
	current, err := service.LookupCollaborationSession(ctx, child.SessionRef())
	if err != nil || current.State != CollaborationSessionRunning || current.TurnID != "turn-2" {
		t.Fatalf("current binding = %#v, %v", current, err)
	}
}

func TestSessionSettlementSurvivesRestartAndReportsFailure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	parent := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "parent")
	child := bindSessionChild(t, service, owner.Agent.ID, room.ID, parent.SessionRef(), "child")
	if _, err := child.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: child.SessionRef(), State: CollaborationSessionFailed, TurnID: "failed-turn", FailureReason: "Provider rejected request"}); err != nil {
		t.Fatal(err)
	}
	params := CollaborationSessionSettleParams{SessionRef: child.SessionRef(), State: CollaborationSessionFailed, TurnID: "failed-turn", FailureReason: "Provider rejected request"}
	if _, err := service.SettleCollaborationSession(ctx, params); err != nil {
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
	if _, err := service.SettleCollaborationSession(ctx, params); err != nil {
		t.Fatal(err)
	}
	parent, err = service.BindAgentSession(ctx, owner.Agent.ID, "parent")
	if err != nil {
		t.Fatal(err)
	}
	results, err := parent.ReceiveCollaboration(ctx, 10)
	if err != nil || len(results) != 1 || results[0].TerminalState != CollaborationTerminalFailed || results[0].Body != params.FailureReason {
		t.Fatalf("failure recovery = %#v, %v", results, err)
	}
}

func TestCancelledSessionRejectsLateSettlement(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	parent := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "parent")
	child := bindSessionChild(t, service, owner.Agent.ID, room.ID, parent.SessionRef(), "child")
	if _, err := child.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: child.SessionRef(), State: CollaborationSessionRunning, TurnID: "cancelled-turn"}); err != nil {
		t.Fatal(err)
	}
	if _, err := child.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: child.SessionRef(), State: CollaborationSessionCancelled}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: child.SessionRef(), State: CollaborationSessionCompleted, TurnID: "cancelled-turn", Result: "Late output"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("late settlement = %v", err)
	}
	current, err := service.LookupCollaborationSession(ctx, child.SessionRef())
	if err != nil || current.State != CollaborationSessionCancelled {
		t.Fatalf("cancelled binding = %#v, %v", current, err)
	}
	messages, err := parent.ReceiveCollaboration(ctx, 10)
	if err != nil || len(messages) != 0 {
		t.Fatalf("cancelled child notified parent = %#v, %v", messages, err)
	}
}

func TestPausedParentKeepsChildOutcomeWithoutWake(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	parent := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "parent")
	child := bindSessionChild(t, service, owner.Agent.ID, room.ID, parent.SessionRef(), "child")
	if _, err := parent.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: parent.SessionRef(), State: CollaborationSessionInterrupted}); err != nil {
		t.Fatal(err)
	}
	if _, err := child.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: child.SessionRef(), State: CollaborationSessionRunning, TurnID: "child-turn"}); err != nil {
		t.Fatal(err)
	}
	sink.take()
	params := CollaborationSessionSettleParams{SessionRef: child.SessionRef(), State: CollaborationSessionInterrupted, TurnID: "child-turn", Result: "Network interrupted; partial evidence is available"}
	if _, err := service.SettleCollaborationSession(ctx, params); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SettleCollaborationSession(ctx, params); err != nil {
		t.Fatal(err)
	}
	if wakes := sink.take(); len(wakes) != 0 {
		t.Fatalf("paused parent was woken: %#v", wakes)
	}
	if _, err := parent.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: parent.SessionRef(), State: CollaborationSessionIdle}); err != nil {
		t.Fatal(err)
	}
	messages, err := parent.ReceiveCollaboration(ctx, 10)
	if err != nil || len(messages) != 1 || messages[0].TerminalState != CollaborationTerminalInterrupted {
		t.Fatalf("resumed parent result = %#v, %v", messages, err)
	}
}

func TestSessionWaitsForActiveChildrenWithoutOccupyingSlotOrReportingCompletion(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	service.agentRunLimit = 2
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	grandparent := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "grandparent")
	parent := bindSessionChild(t, service, owner.Agent.ID, room.ID, grandparent.SessionRef(), "parent")
	child := bindSessionChild(t, service, owner.Agent.ID, room.ID, parent.SessionRef(), "child")
	for _, session := range []*AgentClient{parent, child} {
		if _, err := session.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: session.SessionRef(), State: CollaborationSessionRunning, TurnID: session.SessionRef() + "-turn"}); err != nil {
			t.Fatal(err)
		}
	}
	waiting, err := service.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: parent.SessionRef(), State: CollaborationSessionCompleted, TurnID: "parent-turn", Result: "Child is still investigating"})
	if err != nil || waiting.State != CollaborationSessionWaiting {
		t.Fatalf("parent state = %#v, %v", waiting, err)
	}
	report, err := grandparent.ReceiveCollaboration(ctx, 10)
	if err != nil || len(report) != 0 {
		t.Fatalf("premature completion report = %#v, %v", report, err)
	}
	admitted, err := service.AdmitCollaborationSession(ctx, grandparent.SessionRef())
	if err != nil || admitted.State != CollaborationSessionStarting {
		t.Fatalf("waiting parent occupied slot = %#v, %v", admitted, err)
	}
	if _, err := service.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: child.SessionRef(), State: CollaborationSessionCompleted, TurnID: "child-turn", Result: "Evidence found"}); err != nil {
		t.Fatal(err)
	}
	resumed, err := service.AdmitCollaborationSession(ctx, parent.SessionRef())
	if err != nil || resumed.State != CollaborationSessionStarting {
		t.Fatalf("waiting parent resume = %#v, %v", resumed, err)
	}
	result, err := parent.ReceiveCollaboration(ctx, 10)
	if err != nil || len(result) != 1 || result[0].FromSessionRef != child.SessionRef() {
		t.Fatalf("child result = %#v, %v", result, err)
	}
}

func TestAddressedSessionMessageSignalsDispatcherWhileIdentityHasOutstandingWake(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	first := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "first")
	second := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "second")
	sink.take()
	for _, target := range []*AgentClient{first, second} {
		if _, err := service.EnqueueSessionInput(ctx, CollaborationSessionSendParams{SessionRef: target.SessionRef(), Body: "Run this independent investigation"}); err != nil {
			t.Fatal(err)
		}
	}
	wakes := sink.take()
	if len(wakes) != 2 || wakes[0] != owner.Agent.ID || wakes[1] != owner.Agent.ID {
		t.Fatalf("session dispatcher signals = %#v", wakes)
	}
	if _, err := first.SendCollaboration(ctx, CollaborationSendParams{RoomID: room.ID, TargetSessionRef: second.SessionRef(), Body: "New evidence for your running investigation"}); err != nil {
		t.Fatal(err)
	}
	if wakes := sink.take(); len(wakes) != 1 || wakes[0] != owner.Agent.ID {
		t.Fatalf("peer session signal = %#v", wakes)
	}
}
