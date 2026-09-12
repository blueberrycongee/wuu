package channels

import (
	"context"
	"testing"
)

func TestWorkParentYieldWaitsWithoutReassigningAndResumesAfterChild(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	service.SetCollaborationRunLimits(1, 8, 8)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	lead, _ := bindTestRoomLead(t, ctx, service, room.ID)
	task, err := lead.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Implement", OwnerID: owner.Agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	ownerClient, _ := service.BindAgent(ctx, owner.Agent.ID)
	parent := bindTestWorkSession(t, service, ownerClient, room.ID, task.ID, "parent")
	run, err := parent.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer, SessionRef: parent.SessionRef()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parent.AttachWorkRunTurn(ctx, WorkRunTurnParams{WorkID: task.ID, RunID: run.ID, SessionRef: parent.SessionRef(), TurnID: "parent-turn"}); err != nil {
		t.Fatal(err)
	}
	child := bindSessionChild(t, service, owner.Agent.ID, room.ID, parent.SessionRef(), "child")
	admitted, err := service.AdmitCollaborationSession(ctx, child.SessionRef())
	if err != nil || admitted.State != CollaborationSessionQueued {
		t.Fatalf("child admission = %#v, %v", admitted, err)
	}
	finish := WorkRunFinishParams{WorkID: task.ID, RunID: run.ID, State: WorkRunCompleted, Outcome: "Waiting for implementation child", InputTokens: 100, RequestID: "finish-parent"}
	if _, err := parent.FinishWorkRun(ctx, finish); err != nil {
		t.Fatal(err)
	}
	binding, err := service.LookupCollaborationSession(ctx, parent.SessionRef())
	if err != nil || binding.State != CollaborationSessionWaiting || binding.RunID != "" {
		t.Fatalf("yielded parent = %#v, %v", binding, err)
	}
	capacity, err := service.NamedAgentCapacity(ctx, owner.Agent.ID)
	if err != nil || capacity.Active != 0 {
		t.Fatalf("parent held capacity: %#v, %v", capacity, err)
	}
	check, err := lead.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, delivery := range check.Collaboration {
		if delivery.Kind == CollaborationWorkRunTerminal {
			t.Fatalf("yield woke coordinator: %#v", delivery)
		}
	}
	results, err := parent.ReadSessionResults(ctx, parent.SessionRef(), 0, 10)
	if err != nil || len(results.Results) != 1 || results.Results[0].State != string(CollaborationSessionWaiting) || results.Results[0].Body != finish.Outcome {
		t.Fatalf("parent result missing: %#v, %v", results, err)
	}
	if _, err := parent.FinishWorkRun(ctx, finish); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdmitCollaborationSession(ctx, child.SessionRef()); err != nil {
		t.Fatal(err)
	}
	if _, err := child.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: child.SessionRef(), State: CollaborationSessionRunning, TurnID: "child-turn"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: child.SessionRef(), State: CollaborationSessionCompleted, TurnID: "child-turn", Result: "Implementation ready"}); err != nil {
		t.Fatal(err)
	}
	inbox, err := parent.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, delivery := range inbox.Collaboration {
		if delivery.Kind == CollaborationCompletion && delivery.FromSessionRef == child.SessionRef() {
			found = true
		}
	}
	if !found {
		t.Fatalf("child did not wake exact parent: %#v", inbox)
	}
	next, err := parent.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer, SessionRef: parent.SessionRef(), RequestID: "integrate"})
	if err != nil || next.State != WorkRunRunning {
		t.Fatalf("resume parent = %#v, %v", next, err)
	}
	if _, err := parent.FinishWorkRun(ctx, WorkRunFinishParams{WorkID: task.ID, RunID: next.ID, State: WorkRunCompleted, Outcome: "Integrated"}); err != nil {
		t.Fatal(err)
	}
	check, err = lead.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, delivery := range check.Collaboration {
		if delivery.Kind == CollaborationWorkRunTerminal && delivery.CorrelationID == next.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("completed parent did not notify lead: %#v", check)
	}
}
