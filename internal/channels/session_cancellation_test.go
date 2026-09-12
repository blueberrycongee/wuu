package channels

import (
	"context"
	"errors"
	"testing"
)

func TestCancelChildDeliversOutcomeWithoutRequiringTurn(t *testing.T) {
	for _, initial := range []CollaborationSessionState{CollaborationSessionRunning, CollaborationSessionQueued} {
		t.Run(string(initial), func(t *testing.T) {
			ctx := context.Background()
			service := openTestService(t, nil)
			owner := createTestAgent(t, service, "Owner")
			room := createTestRoom(t, service, owner)
			parent := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "parent")
			child := bindSessionChild(t, service, owner.Agent.ID, room.ID, parent.SessionRef(), "child")
			if _, err := parent.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: parent.SessionRef(), State: CollaborationSessionWaiting}); err != nil {
				t.Fatal(err)
			}
			turnID := ""
			if initial == CollaborationSessionRunning {
				turnID = "active-child-turn"
			}
			if _, err := child.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: child.SessionRef(), State: initial, TurnID: turnID}); err != nil {
				t.Fatal(err)
			}
			cancelled, err := service.CancelCollaborationSessions(ctx, child.SessionRef())
			if err != nil || len(cancelled) != 1 || cancelled[0].State != CollaborationSessionCancelled || cancelled[0].TurnID != turnID {
				t.Fatalf("cancelled child = %#v, %v", cancelled, err)
			}
			delivered, err := parent.ReceiveCollaboration(ctx, 10)
			if err != nil || len(delivered) != 1 || delivered[0].TerminalState != CollaborationTerminalCancelled || delivered[0].FromSessionRef != child.SessionRef() {
				t.Fatalf("parent outcome = %#v, %v", delivered, err)
			}
			if err := parent.AcknowledgeCollaboration(ctx, []string{delivered[0].ID}); err != nil {
				t.Fatal(err)
			}
			again, err := service.CancelCollaborationSessions(ctx, child.SessionRef())
			if err != nil || len(again) != 1 || again[0].State != CollaborationSessionCancelled {
				t.Fatalf("cancel retry = %#v, %v", again, err)
			}
			duplicate, err := parent.ReceiveCollaboration(ctx, 10)
			if err != nil || len(duplicate) != 0 {
				t.Fatalf("duplicate outcome = %#v, %v", duplicate, err)
			}
			resumed, err := service.AdmitCollaborationSession(ctx, parent.SessionRef())
			if err != nil || resumed.State != CollaborationSessionStarting {
				t.Fatalf("waiting parent cannot continue: %#v, %v", resumed, err)
			}
			if _, err := service.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: child.SessionRef(), State: CollaborationSessionCompleted, TurnID: "active-child-turn", Result: "Late result"}); !errors.Is(err, ErrConflict) {
				t.Fatalf("late callback changed cancellation: %v", err)
			}
		})
	}
}

func TestCancelSubtreeNotifiesOnlyExternalParentAndPreservesSibling(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	external := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "external")
	root := bindSessionChild(t, service, owner.Agent.ID, room.ID, external.SessionRef(), "root")
	child := bindSessionChild(t, service, owner.Agent.ID, room.ID, root.SessionRef(), "child")
	grandchild := bindSessionChild(t, service, owner.Agent.ID, room.ID, child.SessionRef(), "grandchild")
	sibling := bindSessionChild(t, service, owner.Agent.ID, room.ID, external.SessionRef(), "sibling")
	for _, client := range []*AgentClient{external, root, child} {
		if _, err := client.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: client.SessionRef(), State: CollaborationSessionWaiting}); err != nil {
			t.Fatal(err)
		}
	}
	for _, client := range []*AgentClient{grandchild, sibling} {
		if _, err := client.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: client.SessionRef(), State: CollaborationSessionRunning, TurnID: client.SessionRef() + "-turn"}); err != nil {
			t.Fatal(err)
		}
	}
	sink.take()
	cancelled, err := service.CancelCollaborationSessions(ctx, root.SessionRef())
	if err != nil || len(cancelled) != 3 {
		t.Fatalf("cancelled subtree = %#v, %v", cancelled, err)
	}
	for _, binding := range cancelled {
		if binding.State != CollaborationSessionCancelled {
			t.Fatalf("subtree partially active: %#v", binding)
		}
	}
	surviving, err := service.LookupCollaborationSession(ctx, sibling.SessionRef())
	if err != nil || surviving.State != CollaborationSessionRunning {
		t.Fatalf("unrelated sibling interrupted: %#v, %v", surviving, err)
	}
	messages, err := external.ReceiveCollaboration(ctx, 10)
	if err != nil || len(messages) != 1 || messages[0].FromSessionRef != root.SessionRef() || messages[0].TerminalState != CollaborationTerminalCancelled {
		t.Fatalf("external outcome = %#v, %v", messages, err)
	}
	dispatches, err := service.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, dispatch := range dispatches {
		if dispatch.TargetSessionRef == root.SessionRef() || dispatch.TargetSessionRef == child.SessionRef() || dispatch.TargetSessionRef == grandchild.SessionRef() {
			t.Fatalf("cancelled internal parent was sent a wake: %#v", dispatch)
		}
	}
	if wakes := sink.take(); len(wakes) != 1 || wakes[0] != owner.Agent.ID {
		t.Fatalf("unexpected subtree wake signals = %#v", wakes)
	}
	again, err := service.CancelCollaborationSessions(ctx, root.SessionRef())
	if err != nil || len(again) != 3 {
		t.Fatalf("retry did not return full interrupt subtree: %#v, %v", again, err)
	}
	if wakes := sink.take(); len(wakes) != 0 {
		t.Fatalf("retry repeated wake: %#v", wakes)
	}
}

func TestCancelSessionSettlesOnlyItsWorkRunsAndReleasesCapacity(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	service.agentRunLimit = 2
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, err := service.BindRuntime(ctx, room.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	parent := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "parent")
	if _, err := parent.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: parent.SessionRef(), State: CollaborationSessionWaiting}); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Independent attempts", OwnerID: owner.Agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	before, err := ownerClient.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	createRun := func(ref, parentRef string) WorkRun {
		t.Helper()
		if _, err := ownerClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{SessionRef: ref, RoomID: room.ID, WorkID: task.ID, ParentSessionRef: parentRef, Purpose: CollaborationSessionWork}); err != nil {
			t.Fatal(err)
		}
		client, err := service.BindAgentSession(ctx, owner.Agent.ID, ref)
		if err != nil {
			t.Fatal(err)
		}
		run, err := client.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	rootRun := createRun("root", parent.SessionRef())
	peerRun := createRun("peer", "")
	childRun := createRun("child", "root")
	queuedPeerRun := createRun("queued-peer", "")
	if rootRun.State != WorkRunRunning || peerRun.State != WorkRunRunning || childRun.State != WorkRunQueued || queuedPeerRun.State != WorkRunQueued {
		t.Fatalf("fixture run states = %s, %s, %s, %s", rootRun.State, peerRun.State, childRun.State, queuedPeerRun.State)
	}
	// The durable binding handle must also release a queued run whose session
	// address has not been restored yet.
	if _, err := service.db.Exec(`UPDATE work_runs SET session_ref = NULL WHERE id = ?`, childRun.ID); err != nil {
		t.Fatal(err)
	}
	cancelled, err := service.CancelCollaborationSessions(ctx, "root")
	if err != nil || len(cancelled) != 2 {
		t.Fatalf("work subtree cancellation = %#v, %v", cancelled, err)
	}
	for _, binding := range cancelled {
		if binding.RunID != "" || binding.State != CollaborationSessionCancelled {
			t.Fatalf("work session retained capacity: %#v", binding)
		}
	}
	after, err := ownerClient.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != before.State || after.GoalRevision != before.GoalRevision || after.VerificationState != before.VerificationState {
		t.Fatalf("session cancellation changed the Work contract: before=%#v after=%#v", before, after)
	}
	if findRunState(after.Runs, rootRun.ID) != WorkRunCancelled || findRunState(after.Runs, childRun.ID) != WorkRunCancelled || findRunState(after.Runs, peerRun.ID) != WorkRunRunning || findRunState(after.Runs, queuedPeerRun.ID) != WorkRunRunning {
		t.Fatalf("work run settlement/admission = %#v", after.Runs)
	}
	if after.CurrentRunRef == rootRun.ID || after.CurrentRunRef == childRun.ID || after.CurrentRunRef == "" {
		t.Fatalf("work retained a cancelled current run: %s", after.CurrentRunRef)
	}
	notifications, err := runtime.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancelledTerminals := map[string]int{}
	for _, message := range notifications.Collaboration {
		if message.Kind == CollaborationWorkRunTerminal && message.TerminalState == CollaborationTerminalCancelled {
			cancelledTerminals[message.CorrelationID]++
		}
	}
	if cancelledTerminals[rootRun.ID] != 1 || cancelledTerminals[childRun.ID] != 1 || len(cancelledTerminals) != 2 {
		t.Fatalf("coordinator cancelled run notifications = %#v", cancelledTerminals)
	}
	if _, err := service.CancelCollaborationSessions(ctx, "root"); err != nil {
		t.Fatal(err)
	}
	duplicates, err := runtime.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range duplicates.Collaboration {
		if message.Kind == CollaborationWorkRunTerminal && message.TerminalState == CollaborationTerminalCancelled {
			t.Fatalf("cancel retry duplicated work terminal: %#v", message)
		}
	}
}

func TestCancellationStateAndParentNotificationCommitTogether(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	parent := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "parent")
	child := bindSessionChild(t, service, owner.Agent.ID, room.ID, parent.SessionRef(), "child")
	if _, err := child.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: child.SessionRef(), State: CollaborationSessionRunning, TurnID: "child-turn"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(`CREATE TRIGGER reject_cancellation_notification BEFORE INSERT ON collaboration_messages WHEN NEW.terminal_state = 'cancelled' BEGIN SELECT RAISE(ABORT, 'injected notification persistence failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CancelCollaborationSessions(ctx, child.SessionRef()); err == nil {
		t.Fatal("injected parent notification failure was ignored")
	}
	current, err := service.LookupCollaborationSession(ctx, child.SessionRef())
	if err != nil || current.State != CollaborationSessionRunning {
		t.Fatalf("notification failure partially cancelled child: %#v, %v", current, err)
	}
	if _, err := service.db.Exec(`DROP TRIGGER reject_cancellation_notification`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CancelCollaborationSessions(ctx, child.SessionRef()); err != nil {
		t.Fatal(err)
	}
	messages, err := parent.ReceiveCollaboration(ctx, 10)
	if err != nil || len(messages) != 1 || messages[0].TerminalState != CollaborationTerminalCancelled {
		t.Fatalf("retry outcome = %#v, %v", messages, err)
	}
}
