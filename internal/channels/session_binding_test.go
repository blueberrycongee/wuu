package channels

import (
	"context"
	"errors"
	"testing"
)

func TestCollaborationSessionTargetedDeliveryIsolation(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	sender := createTestAgent(t, service, "Sender")
	recipient := createTestAgent(t, service, "Recipient")
	other := createTestAgent(t, service, "Other")
	room := createTestRoom(t, service, sender, recipient, other)

	senderClient, err := service.BindAgent(ctx, sender.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(sender) error = %v", err)
	}
	recipientClient, err := service.BindAgent(ctx, recipient.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(recipient) error = %v", err)
	}
	for _, sessionRef := range []string{"recipient-session-a", "recipient-session-b"} {
		if _, err := recipientClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{
			SessionRef: sessionRef,
			RoomID:     room.ID,
			Purpose:    CollaborationSessionCoordination,
		}); err != nil {
			t.Fatalf("BindCollaborationSession(%s) error = %v", sessionRef, err)
		}
	}

	targeted, err := senderClient.SendCollaboration(ctx, CollaborationSendParams{
		RoomID:           room.ID,
		ToAgentID:        recipient.Agent.ID,
		TargetSessionRef: "recipient-session-a",
		Kind:             CollaborationControl,
		Body:             "private control for session A",
	})
	if err != nil {
		t.Fatalf("SendCollaboration(targeted) error = %v", err)
	}
	untargeted, err := senderClient.SendCollaboration(ctx, CollaborationSendParams{
		RoomID:    room.ID,
		ToAgentID: recipient.Agent.ID,
		Kind:      CollaborationControl,
		Body:      "agent-level control",
	})
	if err != nil {
		t.Fatalf("SendCollaboration(untargeted) error = %v", err)
	}
	if _, err := senderClient.SendCollaboration(ctx, CollaborationSendParams{
		RoomID:           room.ID,
		ToAgentID:        other.Agent.ID,
		TargetSessionRef: "recipient-session-a",
		Kind:             CollaborationControl,
		Body:             "misrouted control",
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("cross-agent target error = %v, want unauthorized", err)
	}

	agentCheck, err := recipientClient.Check(ctx)
	if err != nil {
		t.Fatalf("Check(recipient) error = %v", err)
	}
	if len(agentCheck.Collaboration) != 1 || agentCheck.Collaboration[0].ID != untargeted.ID {
		t.Fatalf("agent-level collaboration = %#v, want only %q", agentCheck.Collaboration, untargeted.ID)
	}

	sessionB, err := service.BindAgentSession(ctx, recipient.Agent.ID, "recipient-session-b")
	if err != nil {
		t.Fatalf("BindAgentSession(B) error = %v", err)
	}
	checkB, err := sessionB.Check(ctx)
	if err != nil {
		t.Fatalf("CheckSession(B) error = %v", err)
	}
	if len(checkB.Collaboration) != 0 {
		t.Fatalf("session B received session A delivery: %#v", checkB.Collaboration)
	}
	wake, err := service.WakeState(ctx, recipient.Agent.ID)
	if err != nil {
		t.Fatalf("WakeState(recipient) error = %v", err)
	}
	if !wake.Outstanding {
		t.Fatalf("wake state = %#v, want outstanding while session A delivery remains", wake)
	}

	sessionA, err := service.BindAgentSession(ctx, recipient.Agent.ID, "recipient-session-a")
	if err != nil {
		t.Fatalf("BindAgentSession(A) error = %v", err)
	}
	checkA, err := sessionA.Check(ctx)
	if err != nil {
		t.Fatalf("CheckSession(A) error = %v", err)
	}
	if len(checkA.Collaboration) != 1 || checkA.Collaboration[0].ID != targeted.ID ||
		checkA.Collaboration[0].TargetSessionRef != sessionA.SessionRef() {
		t.Fatalf("session A collaboration = %#v, want targeted delivery %q", checkA.Collaboration, targeted.ID)
	}
	wake, err = service.WakeState(ctx, recipient.Agent.ID)
	if err != nil {
		t.Fatalf("WakeState(recipient after claim) error = %v", err)
	}
	if wake.Outstanding || wake.Pending {
		t.Fatalf("wake state after all claims = %#v, want settled", wake)
	}

	if _, err := sessionA.SendCollaboration(ctx, CollaborationSendParams{
		RoomID:           room.ID,
		ToAgentID:        recipient.Agent.ID,
		TargetSessionRef: sessionB.SessionRef(),
		Kind:             CollaborationControl,
		Body:             "session A handing off to session B",
	}); err != nil {
		t.Fatalf("SendCollaboration(same principal sessions) error = %v", err)
	}
	handoff, err := sessionB.Check(ctx)
	if err != nil {
		t.Fatalf("CheckSession(B handoff) error = %v", err)
	}
	if len(handoff.Collaboration) != 1 || handoff.Collaboration[0].FromSessionRef != sessionA.SessionRef() ||
		handoff.Collaboration[0].TargetSessionRef != sessionB.SessionRef() {
		t.Fatalf("same-principal session handoff = %#v", handoff.Collaboration)
	}
	if _, err := sessionA.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{
		SessionRef: sessionA.SessionRef(),
		State:      CollaborationSessionInterrupted,
	}); err != nil {
		t.Fatalf("UpdateCollaborationSessionState(interrupted) error = %v", err)
	}
	if _, err := sessionA.Check(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("CheckSession(interrupted) error = %v, want conflict", err)
	}
	if _, err := senderClient.SendCollaboration(ctx, CollaborationSendParams{
		RoomID:           room.ID,
		ToAgentID:        recipient.Agent.ID,
		TargetSessionRef: sessionA.SessionRef(),
		Kind:             CollaborationControl,
		Body:             "must not reach an interrupted session",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("SendCollaboration(interrupted target) error = %v, want conflict", err)
	}
}

func TestCollaborationSourceHumanMessageDoesNotImplyWork(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	sender := createTestAgent(t, service, "Sender")
	recipient := createTestAgent(t, service, "Recipient")
	room := createTestRoom(t, service, sender, recipient)
	source, err := service.SendHuman(ctx, HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "Please coordinate on this question.",
	})
	if err != nil {
		t.Fatalf("SendHuman() error = %v", err)
	}
	senderClient, err := service.BindAgent(ctx, sender.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(sender) error = %v", err)
	}
	delivery, err := senderClient.SendCollaboration(ctx, CollaborationSendParams{
		RoomID:          room.ID,
		ToAgentID:       recipient.Agent.ID,
		Kind:            CollaborationControl,
		SourceMessageID: source.Message.ID,
		Body:            "Coordinate on the user's message.",
	})
	if err != nil {
		t.Fatalf("SendCollaboration() error = %v", err)
	}
	if delivery.WorkID != "" {
		t.Fatalf("delivery work ID = %q, want empty for a human source message", delivery.WorkID)
	}
}

func TestTargetedTaskInboxIsPrivateToItsSession(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, err := bindTestRoomLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(owner) error = %v", err)
	}
	if _, err := ownerClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{
		SessionRef: "target-session",
		RoomID:     room.ID,
		Purpose:    CollaborationSessionCoordination,
	}); err != nil {
		t.Fatalf("BindCollaborationSession(target) error = %v", err)
	}
	task, err := runtime.CreateTask(ctx, TaskCreateParams{
		RoomID:           room.ID,
		Title:            "Private assignment",
		OwnerID:          owner.Agent.ID,
		TargetSessionRef: "target-session",
	})
	if err != nil {
		t.Fatalf("CreateTask(targeted) error = %v", err)
	}
	if _, err := ownerClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{
		SessionRef: "same-work-session",
		RoomID:     room.ID,
		WorkID:     task.ID,
		Purpose:    CollaborationSessionWork,
	}); err != nil {
		t.Fatalf("BindCollaborationSession(same work) error = %v", err)
	}

	legacyCheck, err := ownerClient.Check(ctx)
	if err != nil {
		t.Fatalf("Check(owner) error = %v", err)
	}
	if len(legacyCheck.Items) != 0 || len(legacyCheck.Collaboration) != 0 {
		t.Fatalf("legacy check consumed targeted task: %#v", legacyCheck)
	}
	otherSession, err := service.BindAgentSession(ctx, owner.Agent.ID, "same-work-session")
	if err != nil {
		t.Fatalf("BindAgentSession(same work) error = %v", err)
	}
	otherCheck, err := otherSession.Check(ctx)
	if err != nil {
		t.Fatalf("CheckSession(same work) error = %v", err)
	}
	if len(otherCheck.Items) != 0 || len(otherCheck.Collaboration) != 0 {
		t.Fatalf("wrong session consumed targeted task: %#v", otherCheck)
	}

	targetSession, err := service.BindAgentSession(ctx, owner.Agent.ID, "target-session")
	if err != nil {
		t.Fatalf("BindAgentSession(target) error = %v", err)
	}
	targetCheck, err := targetSession.Check(ctx)
	if err != nil {
		t.Fatalf("CheckSession(target) error = %v", err)
	}
	if len(targetCheck.Items) != 1 || targetCheck.Items[0].MessageID != task.ID ||
		len(targetCheck.Collaboration) != 1 || targetCheck.Collaboration[0].WorkID != task.ID ||
		targetCheck.Collaboration[0].TargetSessionRef != targetSession.SessionRef() {
		t.Fatalf("target session check = %#v", targetCheck)
	}
}

func TestCollaborationSessionsAtomicallyClaimUntargetedTask(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, err := bindTestRoomLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(owner) error = %v", err)
	}
	task, err := runtime.CreateTask(ctx, TaskCreateParams{
		RoomID:  room.ID,
		Title:   "Claim exactly once",
		Body:    "Only one worker session may claim this assignment.",
		OwnerID: owner.Agent.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	dispatches, err := service.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("PendingCollaborationDispatches() error = %v", err)
	}
	if len(dispatches) != 1 || dispatches[0].WorkID != task.ID || dispatches[0].TargetSessionRef != "" {
		t.Fatalf("pending dispatches = %#v, want one unassigned task delivery", dispatches)
	}

	clients := make(map[string]*AgentClient, 2)
	for _, sessionRef := range []string{"worker-session-a", "worker-session-b"} {
		if _, err := ownerClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{
			SessionRef: sessionRef,
			RoomID:     room.ID,
			WorkID:     task.ID,
			Purpose:    CollaborationSessionWork,
		}); err != nil {
			t.Fatalf("BindCollaborationSession(%s) error = %v", sessionRef, err)
		}
		client, err := service.BindAgentSession(ctx, owner.Agent.ID, sessionRef)
		if err != nil {
			t.Fatalf("BindAgentSession(%s) error = %v", sessionRef, err)
		}
		clients[sessionRef] = client
	}

	type checkOutcome struct {
		sessionRef string
		result     CheckResult
		err        error
	}
	start := make(chan struct{})
	outcomes := make(chan checkOutcome, len(clients))
	for sessionRef, client := range clients {
		go func(sessionRef string, client *AgentClient) {
			<-start
			result, err := client.Check(ctx)
			outcomes <- checkOutcome{sessionRef: sessionRef, result: result, err: err}
		}(sessionRef, client)
	}
	close(start)

	winner := ""
	for range clients {
		outcome := <-outcomes
		if outcome.err != nil {
			t.Fatalf("CheckSession(%s) error = %v", outcome.sessionRef, outcome.err)
		}
		claimedItems := len(outcome.result.Items)
		claimedDeliveries := len(outcome.result.Collaboration)
		if claimedItems == 0 && claimedDeliveries == 0 {
			continue
		}
		if winner != "" {
			t.Fatalf("multiple sessions claimed task: %q and %q", winner, outcome.sessionRef)
		}
		if claimedItems != 1 || outcome.result.Items[0].MessageID != task.ID ||
			claimedDeliveries != 1 || outcome.result.Collaboration[0].Kind != CollaborationAssignment ||
			outcome.result.Collaboration[0].WorkID != task.ID {
			t.Fatalf("CheckSession(%s) = %#v, want task item and assignment", outcome.sessionRef, outcome.result)
		}
		winner = outcome.sessionRef
	}
	if winner == "" {
		t.Fatal("no session claimed the untargeted task")
	}

	var targetSessionRef string
	var consumedAt int64
	if err := service.db.QueryRowContext(ctx, `
		SELECT COALESCE(target_session_ref, ''), COALESCE(consumed_at, 0)
		FROM collaboration_messages WHERE work_id = ? AND kind = 'assignment'`, task.ID,
	).Scan(&targetSessionRef, &consumedAt); err != nil {
		t.Fatalf("read claimed assignment: %v", err)
	}
	if targetSessionRef != winner || consumedAt == 0 {
		t.Fatalf("claimed assignment target = %q, consumed_at = %d, want %q and non-zero", targetSessionRef, consumedAt, winner)
	}
	dispatches, err = service.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("PendingCollaborationDispatches(after claim) error = %v", err)
	}
	if len(dispatches) != 0 {
		t.Fatalf("pending dispatches after atomic claim = %#v, want empty", dispatches)
	}
}

func TestAgentCheckCannotConsumeUntargetedWorkBeforeSessionRouting(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, err := bindTestRoomLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(owner) error = %v", err)
	}
	task, err := runtime.CreateTask(ctx, TaskCreateParams{
		RoomID: room.ID, Title: "Route before execution", OwnerID: owner.Agent.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	agentCheck, err := ownerClient.Check(ctx)
	if err != nil {
		t.Fatalf("Check(owner before route) error = %v", err)
	}
	if len(agentCheck.Items) != 0 || len(agentCheck.Collaboration) != 0 {
		t.Fatalf("agent check consumed unassigned Work delivery: %#v", agentCheck)
	}
	dispatches, err := service.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("PendingCollaborationDispatches() error = %v", err)
	}
	if len(dispatches) != 1 || dispatches[0].WorkID != task.ID || dispatches[0].TargetSessionRef != "" {
		t.Fatalf("pending dispatches after agent check = %#v", dispatches)
	}

	const sessionRef = "routed-work-session"
	if _, err := ownerClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{
		SessionRef: sessionRef,
		RoomID:     room.ID,
		WorkID:     task.ID,
		Purpose:    CollaborationSessionWork,
	}); err != nil {
		t.Fatalf("BindCollaborationSession() error = %v", err)
	}
	if err := service.RoutePendingCollaborationToSession(ctx, owner.Agent.ID, task.ID, sessionRef); err != nil {
		t.Fatalf("RoutePendingCollaborationToSession() error = %v", err)
	}
	sessionClient, err := service.BindAgentSession(ctx, owner.Agent.ID, sessionRef)
	if err != nil {
		t.Fatalf("BindAgentSession() error = %v", err)
	}
	sessionCheck, err := sessionClient.Check(ctx)
	if err != nil {
		t.Fatalf("CheckSession() error = %v", err)
	}
	if len(sessionCheck.Items) != 1 || sessionCheck.Items[0].MessageID != task.ID ||
		len(sessionCheck.Collaboration) != 1 || sessionCheck.Collaboration[0].WorkID != task.ID ||
		sessionCheck.Collaboration[0].TargetSessionRef != sessionRef {
		t.Fatalf("routed Work session check = %#v", sessionCheck)
	}
}

func TestTaskReassignmentHandsWorkToANewOwnerSession(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	oldOwner := createTestAgent(t, service, "Old owner")
	newOwner := createTestAgent(t, service, "New owner")
	room := createTestRoom(t, service, oldOwner, newOwner)
	runtime, err := bindTestRoomLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	oldClient, err := service.BindAgent(ctx, oldOwner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(old owner) error = %v", err)
	}
	newClient, err := service.BindAgent(ctx, newOwner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(new owner) error = %v", err)
	}
	task, err := runtime.CreateTask(ctx, TaskCreateParams{
		RoomID: room.ID, Title: "Transfer active Work", Body: "Use the corrected objective.", OwnerID: oldOwner.Agent.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	oldSession := bindTestWorkSession(t, service, oldClient, room.ID, task.ID, "old-owner-work")
	if _, err := oldSession.Check(ctx); err != nil {
		t.Fatalf("CheckSession(old assignment) error = %v", err)
	}
	run, err := oldSession.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer})
	if err != nil {
		t.Fatalf("StartWorkRun(old owner) error = %v", err)
	}
	staleDelivery, err := runtime.SendCollaboration(ctx, CollaborationSendParams{
		RoomID:           room.ID,
		ToAgentID:        oldOwner.Agent.ID,
		TargetSessionRef: oldSession.SessionRef(),
		WorkID:           task.ID,
		Kind:             CollaborationControl,
		Body:             "This pending instruction belongs to the previous owner.",
	})
	if err != nil {
		t.Fatalf("SendCollaboration(old owner control) error = %v", err)
	}
	sink.take()

	reassigned, err := runtime.UpdateTask(ctx, TaskUpdateParams{
		TaskID:         task.ID,
		OwnerID:        newOwner.Agent.ID,
		GoalCorrection: "Use the corrected objective and hand it to the new owner.",
	})
	if err != nil {
		t.Fatalf("UpdateTask(reassign) error = %v", err)
	}
	if reassigned.TaskOwner != newOwner.Agent.ID || reassigned.TaskGoalRevision != task.TaskGoalRevision+1 {
		t.Fatalf("reassigned task = %#v", reassigned)
	}
	if got := sink.takeSessionInterrupts(); len(got) != 1 || got[0] != (recordedSessionInterrupt{
		agentID: oldOwner.Agent.ID, sessionRef: oldSession.SessionRef(),
	}) {
		t.Fatalf("reassignment session interrupts = %#v", got)
	}
	if got := sink.take(); len(got) != 2 || got[0] != newOwner.Agent.ID || got[1] != runtime.AgentID() {
		t.Fatalf("reassignment wakes = %v, want new owner and room runtime", got)
	}
	oldBinding, err := service.GetCollaborationSession(ctx, oldOwner.Agent.ID, oldOwner.Token, oldSession.SessionRef())
	if err != nil {
		t.Fatalf("GetCollaborationSession(old owner) error = %v", err)
	}
	if oldBinding.State != CollaborationSessionInterrupted || oldBinding.RunID != "" {
		t.Fatalf("old owner binding after reassignment = %#v", oldBinding)
	}
	work, err := newClient.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetWork(after reassignment) error = %v", err)
	}
	if settledRun := workRunByID(work.Runs, run.ID); settledRun.State != WorkRunInterrupted {
		t.Fatalf("old owner run after reassignment = %#v", settledRun)
	}
	invalidated := false
	for _, delivery := range work.Deliveries {
		if delivery.ID == staleDelivery.ID {
			invalidated = !delivery.InvalidatedAt.IsZero()
		}
	}
	if !invalidated {
		t.Fatalf("old owner pending delivery was not invalidated: %#v", work.Deliveries)
	}
	if _, err := oldSession.Check(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("CheckSession(old owner after reassignment) error = %v, want conflict", err)
	}

	agentCheck, err := newClient.Check(ctx)
	if err != nil {
		t.Fatalf("Check(new owner before route) error = %v", err)
	}
	if len(agentCheck.Items) != 0 || len(agentCheck.Collaboration) != 0 {
		t.Fatalf("new owner agent check consumed reassigned Work: %#v", agentCheck)
	}
	dispatches, err := service.PendingCollaborationDispatches(ctx, newOwner.Agent.ID)
	if err != nil {
		t.Fatalf("PendingCollaborationDispatches(new owner) error = %v", err)
	}
	foundAssignment := false
	for _, dispatch := range dispatches {
		if dispatch.WorkID != task.ID || dispatch.TargetSessionRef != "" {
			t.Fatalf("reassignment dispatch = %#v, want untargeted current Work", dispatch)
		}
		foundAssignment = foundAssignment || dispatch.Kind == CollaborationAssignment
	}
	if !foundAssignment {
		t.Fatalf("reassignment dispatches have no assignment: %#v", dispatches)
	}

	const newSessionRef = "new-owner-work"
	newSession := bindTestWorkSession(t, service, newClient, room.ID, task.ID, newSessionRef)
	if err := service.RoutePendingCollaborationToSession(ctx, newOwner.Agent.ID, task.ID, newSessionRef); err != nil {
		t.Fatalf("RoutePendingCollaborationToSession(new owner) error = %v", err)
	}
	sessionCheck, err := newSession.Check(ctx)
	if err != nil {
		t.Fatalf("CheckSession(new owner) error = %v", err)
	}
	if len(sessionCheck.Items) != 1 || sessionCheck.Items[0].MessageID != task.ID {
		t.Fatalf("new owner Work items = %#v", sessionCheck.Items)
	}
	foundAssignment = false
	for _, delivery := range sessionCheck.Collaboration {
		if delivery.TargetSessionRef != newSessionRef || delivery.WorkID != task.ID {
			t.Fatalf("new owner Work delivery = %#v", delivery)
		}
		foundAssignment = foundAssignment || delivery.Kind == CollaborationAssignment
	}
	if !foundAssignment {
		t.Fatalf("new owner session did not receive assignment: %#v", sessionCheck.Collaboration)
	}
}

func TestGoalCorrectionReplansAfterInterruptingActiveSession(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, err := bindTestRoomLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(owner) error = %v", err)
	}
	task, err := runtime.CreateTask(ctx, TaskCreateParams{
		RoomID: room.ID, Title: "Revise active Work", Body: "Use the first goal.", OwnerID: owner.Agent.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	oldSession := bindTestWorkSession(t, service, ownerClient, room.ID, task.ID, "old-goal-session")
	if _, err := oldSession.Check(ctx); err != nil {
		t.Fatalf("CheckSession(old assignment) error = %v", err)
	}
	if _, err := oldSession.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer}); err != nil {
		t.Fatalf("StartWorkRun(old goal) error = %v", err)
	}
	sink.take()

	revised, err := runtime.UpdateTask(ctx, TaskUpdateParams{
		TaskID: task.ID, GoalCorrection: "Use the corrected goal.",
	})
	if err != nil {
		t.Fatalf("UpdateTask(goal correction) error = %v", err)
	}
	if revised.TaskGoalRevision != task.TaskGoalRevision+1 {
		t.Fatalf("revised task goal revision = %d, want %d", revised.TaskGoalRevision, task.TaskGoalRevision+1)
	}
	if got := sink.takeSessionInterrupts(); len(got) != 1 || got[0] != (recordedSessionInterrupt{
		agentID: owner.Agent.ID, sessionRef: oldSession.SessionRef(),
	}) {
		t.Fatalf("goal correction session interrupts = %#v", got)
	}
	oldBinding, err := service.GetCollaborationSession(ctx, owner.Agent.ID, owner.Token, oldSession.SessionRef())
	if err != nil {
		t.Fatalf("GetCollaborationSession(old goal) error = %v", err)
	}
	if oldBinding.State != CollaborationSessionInterrupted || oldBinding.RunID != "" {
		t.Fatalf("old goal binding = %#v, want interrupted without run", oldBinding)
	}
	if _, err := oldSession.Check(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("CheckSession(old goal after correction) error = %v, want conflict", err)
	}

	dispatches, err := service.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("PendingCollaborationDispatches() error = %v", err)
	}
	if len(dispatches) != 1 || dispatches[0].WorkID != task.ID ||
		dispatches[0].Kind != CollaborationControl || dispatches[0].TargetSessionRef != "" {
		t.Fatalf("goal correction dispatches = %#v, want one untargeted control", dispatches)
	}
	if err := service.RoutePendingCollaborationToSession(ctx, owner.Agent.ID, task.ID, oldSession.SessionRef()); !errors.Is(err, ErrConflict) {
		t.Fatalf("RoutePendingCollaborationToSession(interrupted) error = %v, want conflict", err)
	}

	replacement := bindTestWorkSession(t, service, ownerClient, room.ID, task.ID, "corrected-goal-session")
	if err := service.RoutePendingCollaborationToSession(ctx, owner.Agent.ID, task.ID, replacement.SessionRef()); err != nil {
		t.Fatalf("RoutePendingCollaborationToSession(replacement) error = %v", err)
	}
	check, err := replacement.Check(ctx)
	if err != nil {
		t.Fatalf("CheckSession(replacement) error = %v", err)
	}
	if len(check.Collaboration) != 1 || check.Collaboration[0].TargetSessionRef != replacement.SessionRef() ||
		check.Collaboration[0].GoalRevision != revised.TaskGoalRevision {
		t.Fatalf("replacement session check = %#v", check)
	}
}

func TestStartWorkRunRequiresOwnedNamedSession(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	other := createTestAgent(t, service, "Other")
	workRoom := createTestRoom(t, service, owner, other)
	otherRoom := createTestRoom(t, service, owner)
	runtime, err := bindTestRoomLead(t, ctx, service, workRoom.ID)
	if err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(owner) error = %v", err)
	}
	task, err := runtime.CreateTask(ctx, TaskCreateParams{
		RoomID:  workRoom.ID,
		Title:   "Integrate the result",
		OwnerID: owner.Agent.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	_, err = runtime.StartWorkRun(ctx, WorkRunStartParams{
		WorkID:     task.ID,
		Kind:       WorkRunIntegration,
		SessionRef: "hidden-integration-session",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("StartWorkRun(unbound integration) error = %v, want not found", err)
	}

	if _, err := ownerClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{
		SessionRef: "wrong-room-session",
		RoomID:     otherRoom.ID,
		Purpose:    CollaborationSessionWork,
	}); err != nil {
		t.Fatalf("BindCollaborationSession(wrong room) error = %v", err)
	}
	if _, err := ownerClient.StartWorkRun(ctx, WorkRunStartParams{
		WorkID:     task.ID,
		Kind:       WorkRunProducer,
		SessionRef: "wrong-room-session",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("StartWorkRun(wrong room session) error = %v, want conflict", err)
	}
	otherClient, err := service.BindAgent(ctx, other.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(other) error = %v", err)
	}
	if _, err := otherClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{
		SessionRef: "other-agent-session",
		RoomID:     workRoom.ID,
		WorkID:     task.ID,
		Purpose:    CollaborationSessionWork,
	}); err != nil {
		t.Fatalf("BindCollaborationSession(other agent) error = %v", err)
	}
	if _, err := ownerClient.StartWorkRun(ctx, WorkRunStartParams{
		WorkID:       task.ID,
		NamedAgentID: other.Agent.ID,
		Kind:         WorkRunProducer,
		SessionRef:   "other-agent-session",
	}); err != nil {
		t.Fatalf("StartWorkRun(delegated member session) error = %v", err)
	}

	if _, err := ownerClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{
		SessionRef: "named-worker-session",
		RoomID:     workRoom.ID,
		Purpose:    CollaborationSessionWork,
	}); err != nil {
		t.Fatalf("BindCollaborationSession(named worker) error = %v", err)
	}
	namedRun, err := ownerClient.StartWorkRun(ctx, WorkRunStartParams{
		WorkID:     task.ID,
		Kind:       WorkRunProducer,
		SessionRef: "named-worker-session",
	})
	if err != nil {
		t.Fatalf("StartWorkRun(named worker) error = %v", err)
	}
	if namedRun.NamedAgentID != owner.Agent.ID {
		t.Fatalf("named worker run = %#v", namedRun)
	}
	binding, err := service.GetCollaborationSession(ctx, owner.Agent.ID, owner.Token, namedRun.SessionRef)
	if err != nil {
		t.Fatalf("GetCollaborationSession(named worker) error = %v", err)
	}
	if binding.WorkID != task.ID || binding.RunID != namedRun.ID || binding.State != CollaborationSessionRunning {
		t.Fatalf("named worker binding = %#v", binding)
	}
}

func TestCollaborationSessionRunLifecycleSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if service != nil {
			_ = service.Close()
		}
	})
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, err := bindTestRoomLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(owner) error = %v", err)
	}
	if _, err := ownerClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{
		SessionRef: "lifecycle-session",
		RoomID:     room.ID,
		Purpose:    CollaborationSessionCoordination,
	}); err != nil {
		t.Fatalf("BindCollaborationSession(lifecycle) error = %v", err)
	}
	if _, err := ownerClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{
		SessionRef: "idle-session",
		RoomID:     room.ID,
		Purpose:    CollaborationSessionConversation,
	}); err != nil {
		t.Fatalf("BindCollaborationSession(idle) error = %v", err)
	}
	task, err := runtime.CreateTask(ctx, TaskCreateParams{
		RoomID:           room.ID,
		Title:            "Persist the active worker",
		OwnerID:          owner.Agent.ID,
		TargetSessionRef: "lifecycle-session",
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sessionClient, err := service.BindAgentSession(ctx, owner.Agent.ID, "lifecycle-session")
	if err != nil {
		t.Fatalf("BindAgentSession(lifecycle) error = %v", err)
	}
	if check, err := sessionClient.Check(ctx); err != nil || len(check.Items) != 1 || len(check.Collaboration) != 1 {
		t.Fatalf("CheckSession(task assignment) = %#v, %v", check, err)
	}
	run, err := sessionClient.StartWorkRun(ctx, WorkRunStartParams{
		WorkID: task.ID,
		Kind:   WorkRunProducer,
	})
	if err != nil {
		t.Fatalf("StartWorkRun() error = %v", err)
	}
	if run.NamedAgentID != owner.Agent.ID || run.SessionRef != sessionClient.SessionRef() {
		t.Fatalf("started run = %#v", run)
	}

	if err := service.Close(); err != nil {
		t.Fatalf("Close(before restart) error = %v", err)
	}
	service = nil
	service, err = Open(dir, nil)
	if err != nil {
		t.Fatalf("Open(after restart) error = %v", err)
	}
	ownerClient, err = service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(owner after restart) error = %v", err)
	}

	bindings, err := ownerClient.ListCollaborationSessions(ctx, CollaborationSessionListParams{})
	if err != nil {
		t.Fatalf("ListCollaborationSessions(after restart) error = %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("bindings after restart = %#v, want two", bindings)
	}
	active := collaborationSessionByRef(bindings, "lifecycle-session")
	if active.SessionRef == "" || active.WorkID != task.ID || active.RunID != run.ID ||
		active.State != CollaborationSessionRunning || active.NamedAgentID != owner.Agent.ID {
		t.Fatalf("active binding after restart = %#v", active)
	}
	idle := collaborationSessionByRef(bindings, "idle-session")
	if idle.SessionRef == "" || idle.State != CollaborationSessionIdle || idle.RunID != "" {
		t.Fatalf("idle binding after restart = %#v", idle)
	}
	unsettled, err := service.ListUnsettledWorkRuns(ctx)
	if err != nil {
		t.Fatalf("ListUnsettledWorkRuns() error = %v", err)
	}
	if len(unsettled) != 1 || unsettled[0].ID != run.ID || unsettled[0].NamedAgentID != owner.Agent.ID {
		t.Fatalf("unsettled runs = %#v, want %q", unsettled, run.ID)
	}

	if err := service.ReconcileWorkRuns(ctx, []WorkRunRecovery{{
		RunID: run.ID, SessionRef: "stale-session-ref", State: WorkRunRecoveryCompleted,
	}}); err != nil {
		t.Fatalf("ReconcileWorkRuns(stale handle) error = %v", err)
	}
	active, err = service.GetCollaborationSession(ctx, owner.Agent.ID, owner.Token, "lifecycle-session")
	if err != nil {
		t.Fatalf("GetCollaborationSession(after stale recovery) error = %v", err)
	}
	if active.State != CollaborationSessionRunning || active.RunID != run.ID {
		t.Fatalf("binding after stale recovery = %#v, want unchanged", active)
	}

	if err := service.ReconcileWorkRuns(ctx, []WorkRunRecovery{{
		RunID: run.ID, SessionRef: run.SessionRef, State: WorkRunRecoveryCompleted,
	}}); err != nil {
		t.Fatalf("ReconcileWorkRuns(completed) error = %v", err)
	}
	active, err = service.GetCollaborationSession(ctx, owner.Agent.ID, owner.Token, "lifecycle-session")
	if err != nil {
		t.Fatalf("GetCollaborationSession(after completed recovery) error = %v", err)
	}
	if active.State != CollaborationSessionIdle || active.RunID != "" {
		t.Fatalf("binding after completed recovery = %#v, want idle", active)
	}

	sessionClient, err = service.BindAgentSession(ctx, owner.Agent.ID, "lifecycle-session")
	if err != nil {
		t.Fatalf("BindAgentSession(after recovery) error = %v", err)
	}
	finishedRun, err := sessionClient.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer})
	if err != nil {
		t.Fatalf("StartWorkRun(after recovery) error = %v", err)
	}
	if _, err := sessionClient.FinishWorkRun(ctx, WorkRunFinishParams{
		WorkID: task.ID,
		RunID:  finishedRun.ID,
		State:  WorkRunCompleted,
	}); err != nil {
		t.Fatalf("FinishWorkRun() error = %v", err)
	}
	active, err = service.GetCollaborationSession(ctx, owner.Agent.ID, owner.Token, "lifecycle-session")
	if err != nil {
		t.Fatalf("GetCollaborationSession(after finish) error = %v", err)
	}
	if active.State != CollaborationSessionIdle || active.RunID != "" {
		t.Fatalf("binding after finish = %#v, want idle", active)
	}

	missingRun, err := sessionClient.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer})
	if err != nil {
		t.Fatalf("StartWorkRun(before missing recovery) error = %v", err)
	}
	if err := service.ReconcileWorkRuns(ctx, []WorkRunRecovery{{
		RunID: missingRun.ID, SessionRef: missingRun.SessionRef, State: WorkRunRecoveryMissing,
	}}); err != nil {
		t.Fatalf("ReconcileWorkRuns(missing) error = %v", err)
	}
	active, err = service.GetCollaborationSession(ctx, owner.Agent.ID, owner.Token, "lifecycle-session")
	if err != nil {
		t.Fatalf("GetCollaborationSession(after missing recovery) error = %v", err)
	}
	if active.State != CollaborationSessionMissing || active.RunID != "" {
		t.Fatalf("binding after missing recovery = %#v, want missing", active)
	}
	if _, err := sessionClient.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer}); !errors.Is(err, ErrConflict) {
		t.Fatalf("StartWorkRun(missing session) error = %v, want conflict", err)
	}
	work, err := ownerClient.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetWork(after missing recovery) error = %v", err)
	}
	if work.State != WorkInterrupted || len(work.Runs) != 3 || work.Runs[2].State != WorkRunInterrupted {
		t.Fatalf("work after missing recovery = %#v", work)
	}
}

func TestWorkRunBindingsSettleOnGoalCorrectionAndCancellation(t *testing.T) {
	ctx := context.Background()
	for _, testCase := range []struct {
		name        string
		expectedRun WorkRunState
		settle      func(*AgentClient, *AgentClient, string) error
	}{
		{
			name:        "goal correction",
			expectedRun: WorkRunInterrupted,
			settle: func(runtime, _ *AgentClient, taskID string) error {
				_, err := runtime.UpdateTask(ctx, TaskUpdateParams{TaskID: taskID, GoalCorrection: "Use the revised goal"})
				return err
			},
		},
		{
			name:        "cancellation",
			expectedRun: WorkRunCancelled,
			settle: func(_, owner *AgentClient, taskID string) error {
				_, err := owner.CancelWork(ctx, taskID, "No longer needed")
				return err
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := openTestService(t, nil)
			owner := createTestAgent(t, service, "Owner")
			room := createTestRoom(t, service, owner)
			runtime, err := bindTestRoomLead(t, ctx, service, room.ID)
			if err != nil {
				t.Fatalf("BindRuntime() error = %v", err)
			}
			ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
			if err != nil {
				t.Fatalf("BindAgent(owner) error = %v", err)
			}
			if _, err := ownerClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{
				SessionRef: "settlement-session",
				RoomID:     room.ID,
				Purpose:    CollaborationSessionCoordination,
			}); err != nil {
				t.Fatalf("BindCollaborationSession() error = %v", err)
			}
			task, err := runtime.CreateTask(ctx, TaskCreateParams{
				RoomID:           room.ID,
				Title:            "Settle the worker",
				OwnerID:          owner.Agent.ID,
				TargetSessionRef: "settlement-session",
			})
			if err != nil {
				t.Fatalf("CreateTask() error = %v", err)
			}
			sessionClient, err := service.BindAgentSession(ctx, owner.Agent.ID, "settlement-session")
			if err != nil {
				t.Fatalf("BindAgentSession() error = %v", err)
			}
			run, err := sessionClient.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer})
			if err != nil {
				t.Fatalf("StartWorkRun() error = %v", err)
			}
			if err := testCase.settle(runtime, ownerClient, task.ID); err != nil {
				t.Fatalf("settle work error = %v", err)
			}
			binding, err := service.GetCollaborationSession(ctx, owner.Agent.ID, owner.Token, sessionClient.SessionRef())
			if err != nil {
				t.Fatalf("GetCollaborationSession() error = %v", err)
			}
			if binding.State != CollaborationSessionInterrupted || binding.RunID != "" {
				t.Fatalf("settled binding = %#v, want interrupted without run", binding)
			}
			work, err := ownerClient.GetWork(ctx, task.ID)
			if err != nil {
				t.Fatalf("GetWork() error = %v", err)
			}
			settledRun := workRunByID(work.Runs, run.ID)
			if settledRun.ID == "" || settledRun.State != testCase.expectedRun || work.CurrentRunRef != "" {
				t.Fatalf("settled work = %#v", work)
			}
		})
	}
}

func TestCancelWorkInterruptsAllActiveNamedAgentSessions(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, err := bindTestRoomLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(owner) error = %v", err)
	}
	task, err := runtime.CreateTask(ctx, TaskCreateParams{
		RoomID: room.ID, Title: "Cancel parallel sessions", OwnerID: owner.Agent.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	for _, sessionRef := range []string{"cancel-session-a", "cancel-session-b"} {
		if _, err := ownerClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{
			SessionRef: sessionRef,
			RoomID:     room.ID,
			WorkID:     task.ID,
			Purpose:    CollaborationSessionWork,
		}); err != nil {
			t.Fatalf("BindCollaborationSession(%s) error = %v", sessionRef, err)
		}
		client, err := service.BindAgentSession(ctx, owner.Agent.ID, sessionRef)
		if err != nil {
			t.Fatalf("BindAgentSession(%s) error = %v", sessionRef, err)
		}
		if _, err := client.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer}); err != nil {
			t.Fatalf("StartWorkRun(%s) error = %v", sessionRef, err)
		}
	}

	if _, err := ownerClient.CancelWork(ctx, task.ID, "Stop all active attempts"); err != nil {
		t.Fatalf("CancelWork() error = %v", err)
	}
	want := []recordedSessionInterrupt{
		{agentID: owner.Agent.ID, sessionRef: "cancel-session-a"},
		{agentID: owner.Agent.ID, sessionRef: "cancel-session-b"},
	}
	got := sink.takeSessionInterrupts()
	if len(got) != len(want) {
		t.Fatalf("session interrupts = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("session interrupts = %#v, want %#v", got, want)
		}
	}
}

func TestGoalCorrectionAndCancellationInterruptNamedVerifierSessions(t *testing.T) {
	ctx := context.Background()
	for _, testCase := range []struct {
		name   string
		settle func(*AgentClient, *AgentClient, string) error
	}{
		{
			name: "goal correction",
			settle: func(runtime, _ *AgentClient, taskID string) error {
				_, err := runtime.UpdateTask(ctx, TaskUpdateParams{TaskID: taskID, GoalCorrection: "Use the revised goal"})
				return err
			},
		},
		{
			name: "cancellation",
			settle: func(_, owner *AgentClient, taskID string) error {
				_, err := owner.CancelWork(ctx, taskID, "Stop the hidden verifier")
				return err
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			sink := &recordingWakeSink{}
			service := openTestService(t, sink)
			owner := createTestAgent(t, service, "Owner")
			room := createTestRoom(t, service, owner)
			runtime, err := bindTestRoomLead(t, ctx, service, room.ID)
			if err != nil {
				t.Fatalf("BindRuntime() error = %v", err)
			}
			ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
			if err != nil {
				t.Fatalf("BindAgent(owner) error = %v", err)
			}
			task, err := runtime.CreateTask(ctx, TaskCreateParams{
				RoomID: room.ID, Title: "Verify the candidate", OwnerID: owner.Agent.ID, VerificationRequired: true,
			})
			if err != nil {
				t.Fatalf("CreateTask() error = %v", err)
			}
			_ = promoteTestCandidate(t, service, ownerClient, task.ID)
			const sessionRef = "hidden-verifier-session"
			if _, err := runtime.StartWorkRun(ctx, WorkRunStartParams{
				WorkID: task.ID, Kind: WorkRunVerifier, SessionRef: sessionRef, NamedAgentID: runtime.AgentID(),
			}); err != nil {
				t.Fatalf("StartWorkRun(verifier) error = %v", err)
			}
			if err := testCase.settle(runtime, ownerClient, task.ID); err != nil {
				t.Fatalf("settle work error = %v", err)
			}
			got := sink.takeSessionInterrupts()
			want := recordedSessionInterrupt{agentID: runtime.AgentID(), sessionRef: sessionRef}
			if len(got) != 1 || got[0] != want {
				t.Fatalf("hidden session interrupts = %#v, want %#v", got, []recordedSessionInterrupt{want})
			}
		})
	}
}

func TestWorkSessionRejectsWritesAfterGoalRevision(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, err := bindTestRoomLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(owner) error = %v", err)
	}
	task, err := runtime.CreateTask(ctx, TaskCreateParams{
		RoomID: room.ID, Title: "Guard revised work", Body: "Use the original goal", OwnerID: owner.Agent.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sessionClient := bindTestWorkSession(t, service, ownerClient, room.ID, task.ID, "revision-write-session")
	if _, err := sessionClient.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer}); err != nil {
		t.Fatalf("StartWorkRun() error = %v", err)
	}
	if _, err := sessionClient.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, State: TaskStateDoing}); err != nil {
		t.Fatalf("UpdateTask(active session) error = %v", err)
	}
	activeSend, err := sessionClient.Send(ctx, AgentSendParams{
		RoomID: room.ID, Body: "Progress on the current goal", BasisSeq: task.Seq,
	})
	if err != nil || activeSend.Status != SendCommitted {
		t.Fatalf("Send(active session) = %#v, err = %v", activeSend, err)
	}

	revised, err := runtime.UpdateTask(ctx, TaskUpdateParams{
		TaskID: task.ID, GoalCorrection: "Use the corrected goal",
	})
	if err != nil {
		t.Fatalf("UpdateTask(goal correction) error = %v", err)
	}
	if _, err := sessionClient.UpdateTask(ctx, TaskUpdateParams{
		TaskID: task.ID, State: TaskStateDoing,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateTask(stale session) error = %v, want conflict", err)
	}
	if _, err := sessionClient.Send(ctx, AgentSendParams{
		RoomID: room.ID, Body: "Late output for the old goal", BasisSeq: activeSend.Message.Seq,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Send(stale session) error = %v, want conflict", err)
	}
	if _, err := sessionClient.SendCollaboration(ctx, CollaborationSendParams{
		RoomID: room.ID, ToAgentID: runtime.AgentID(), WorkID: task.ID,
		Kind: CollaborationControl, Body: "Late control for the old goal",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("SendCollaboration(stale session) error = %v, want conflict", err)
	}
	work, err := ownerClient.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetWork() error = %v", err)
	}
	if work.GoalRevision != revised.TaskGoalRevision || work.State != WorkOpen {
		t.Fatalf("work after stale writes = %#v, want revised open work", work)
	}
}

func TestCancelledWorkRejectsSessionAndUnboundTaskWrites(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, err := bindTestRoomLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(owner) error = %v", err)
	}
	task, err := runtime.CreateTask(ctx, TaskCreateParams{
		RoomID: room.ID, Title: "Guard cancelled work", OwnerID: owner.Agent.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sessionClient := bindTestWorkSession(t, service, ownerClient, room.ID, task.ID, "cancelled-write-session")
	if _, err := sessionClient.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer}); err != nil {
		t.Fatalf("StartWorkRun() error = %v", err)
	}
	if _, err := ownerClient.CancelWork(ctx, task.ID, "Stop this work"); err != nil {
		t.Fatalf("CancelWork() error = %v", err)
	}

	conversation, err := ownerClient.Send(ctx, AgentSendParams{
		RoomID: room.ID, Body: "Unrelated room conversation still works", BasisSeq: task.Seq,
	})
	if err != nil || conversation.Status != SendCommitted {
		t.Fatalf("Send(unbound client) = %#v, err = %v", conversation, err)
	}
	if _, err := sessionClient.Send(ctx, AgentSendParams{
		RoomID: room.ID, Body: "Late cancelled output", BasisSeq: conversation.Message.Seq,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Send(cancelled session) error = %v, want conflict", err)
	}
	if _, err := sessionClient.UpdateTask(ctx, TaskUpdateParams{
		TaskID: task.ID, State: TaskStateDoing,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateTask(cancelled session) error = %v, want conflict", err)
	}
	if _, err := ownerClient.UpdateTask(ctx, TaskUpdateParams{
		TaskID: task.ID, State: TaskStateDoing,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateTask(unbound cancelled work) error = %v, want conflict", err)
	}
	work, err := ownerClient.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetWork() error = %v", err)
	}
	if work.State != WorkCancelled {
		t.Fatalf("work state after rejected writes = %s, want cancelled", work.State)
	}
}

func TestConversationAndCoordinationSessionsCanStillSend(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	agent := createTestAgent(t, service, "Sender")
	client, err := service.BindAgent(ctx, agent.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent() error = %v", err)
	}
	dm := createTestDirectMessage(t, service, agent)
	if result, err := client.Send(ctx, AgentSendParams{
		RoomID: dm.ID, Body: "Default session DM", BasisSeq: 0,
	}); err != nil || result.Status != SendCommitted {
		t.Fatalf("Send(default DM) = %#v, err = %v", result, err)
	}

	room := createTestRoom(t, service, agent)
	if _, err := client.BindCollaborationSession(ctx, CollaborationSessionBindParams{
		SessionRef: "coordination-send-session",
		RoomID:     room.ID,
		Purpose:    CollaborationSessionCoordination,
	}); err != nil {
		t.Fatalf("BindCollaborationSession() error = %v", err)
	}
	coordinationClient, err := service.BindAgentSession(ctx, agent.Agent.ID, "coordination-send-session")
	if err != nil {
		t.Fatalf("BindAgentSession() error = %v", err)
	}
	if result, err := coordinationClient.Send(ctx, AgentSendParams{
		RoomID: room.ID, Body: "Coordination session update", BasisSeq: 0,
	}); err != nil || result.Status != SendCommitted {
		t.Fatalf("Send(coordination session) = %#v, err = %v", result, err)
	}
	runtime, err := bindTestRoomLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	task, err := runtime.CreateTask(ctx, TaskCreateParams{
		RoomID: room.ID, Title: "Scoped task", OwnerID: agent.Agent.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := coordinationClient.UpdateTask(ctx, TaskUpdateParams{
		TaskID: task.ID, State: TaskStateDoing,
	}); err != nil {
		t.Fatalf("UpdateTask(authorized owner conversation) error = %v", err)
	}
}

func TestActiveSessionStateTransitionSettlesRunAndBlocksFurtherChecks(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, err := bindTestRoomLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(owner) error = %v", err)
	}
	if _, err := ownerClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{
		SessionRef: "missing-session",
		RoomID:     room.ID,
		Purpose:    CollaborationSessionCoordination,
	}); err != nil {
		t.Fatalf("BindCollaborationSession() error = %v", err)
	}
	task, err := runtime.CreateTask(ctx, TaskCreateParams{
		RoomID:           room.ID,
		Title:            "Detect the missing worker",
		OwnerID:          owner.Agent.ID,
		TargetSessionRef: "missing-session",
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sessionClient, err := service.BindAgentSession(ctx, owner.Agent.ID, "missing-session")
	if err != nil {
		t.Fatalf("BindAgentSession() error = %v", err)
	}
	if _, err := sessionClient.Check(ctx); err != nil {
		t.Fatalf("CheckSession(assignment) error = %v", err)
	}
	run, err := sessionClient.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer})
	if err != nil {
		t.Fatalf("StartWorkRun() error = %v", err)
	}
	if _, err := sessionClient.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{
		SessionRef: sessionClient.SessionRef(),
		State:      CollaborationSessionIdle,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateCollaborationSessionState(idle active run) error = %v, want conflict", err)
	}
	if _, err := runtime.SendCollaboration(ctx, CollaborationSendParams{
		RoomID:           room.ID,
		ToAgentID:        owner.Agent.ID,
		TargetSessionRef: sessionClient.SessionRef(),
		WorkID:           task.ID,
		Kind:             CollaborationControl,
		Body:             "Pending private recovery instruction",
	}); err != nil {
		t.Fatalf("SendCollaboration(private recovery) error = %v", err)
	}
	binding, err := sessionClient.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{
		SessionRef: sessionClient.SessionRef(),
		State:      CollaborationSessionMissing,
	})
	if err != nil {
		t.Fatalf("UpdateCollaborationSessionState(missing) error = %v", err)
	}
	if binding.State != CollaborationSessionMissing || binding.RunID != "" {
		t.Fatalf("missing binding = %#v", binding)
	}
	if _, err := sessionClient.Check(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("CheckSession(missing) error = %v, want conflict", err)
	}
	if _, err := runtime.SendCollaboration(ctx, CollaborationSendParams{
		RoomID:           room.ID,
		ToAgentID:        owner.Agent.ID,
		TargetSessionRef: sessionClient.SessionRef(),
		WorkID:           task.ID,
		Kind:             CollaborationControl,
		Body:             "This delivery must not reach a missing session",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("SendCollaboration(missing target) error = %v, want conflict", err)
	}
	dispatches, err := service.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("PendingCollaborationDispatches() error = %v", err)
	}
	if len(dispatches) != 1 || dispatches[0].TargetSessionRef != sessionClient.SessionRef() {
		t.Fatalf("pending missing-session dispatches = %#v", dispatches)
	}
	work, err := ownerClient.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetWork() error = %v", err)
	}
	settledRun := workRunByID(work.Runs, run.ID)
	if settledRun.ID == "" || settledRun.State != WorkRunInterrupted || work.CurrentRunRef != "" {
		t.Fatalf("work after missing state = %#v", work)
	}
}

func TestSettlingOneWorkRunPreservesActiveSibling(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, err := bindTestRoomLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(owner) error = %v", err)
	}
	task, err := runtime.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Parallel candidates", OwnerID: owner.Agent.ID})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	clients := make(map[string]*AgentClient, 2)
	for _, sessionRef := range []string{"parallel-session-a", "parallel-session-b"} {
		if _, err := ownerClient.BindCollaborationSession(ctx, CollaborationSessionBindParams{
			SessionRef: sessionRef,
			RoomID:     room.ID,
			WorkID:     task.ID,
			Purpose:    CollaborationSessionWork,
		}); err != nil {
			t.Fatalf("BindCollaborationSession(%s) error = %v", sessionRef, err)
		}
		client, err := service.BindAgentSession(ctx, owner.Agent.ID, sessionRef)
		if err != nil {
			t.Fatalf("BindAgentSession(%s) error = %v", sessionRef, err)
		}
		clients[sessionRef] = client
	}
	runA, err := clients["parallel-session-a"].StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer})
	if err != nil {
		t.Fatalf("StartWorkRun(A) error = %v", err)
	}
	runB, err := clients["parallel-session-b"].StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer})
	if err != nil {
		t.Fatalf("StartWorkRun(B) error = %v", err)
	}
	if _, err := clients["parallel-session-b"].FinishWorkRun(ctx, WorkRunFinishParams{
		WorkID: task.ID,
		RunID:  runB.ID,
		State:  WorkRunCompleted,
	}); err != nil {
		t.Fatalf("FinishWorkRun(B) error = %v", err)
	}
	work, err := ownerClient.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetWork(after B) error = %v", err)
	}
	if work.CurrentRunRef != runA.ID || workRunByID(work.Runs, runA.ID).State != WorkRunRunning {
		t.Fatalf("work after B settled = %#v, want A active", work)
	}

	runB2, err := clients["parallel-session-b"].StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer})
	if err != nil {
		t.Fatalf("StartWorkRun(B2) error = %v", err)
	}
	if err := service.ReconcileWorkRuns(ctx, []WorkRunRecovery{{
		RunID: runA.ID, SessionRef: runA.SessionRef, State: WorkRunRecoveryMissing,
	}}); err != nil {
		t.Fatalf("ReconcileWorkRuns(A missing) error = %v", err)
	}
	work, err = ownerClient.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetWork(after A missing) error = %v", err)
	}
	if work.CurrentRunRef != runB2.ID || work.State != WorkOpen || work.FailureReason != "" ||
		workRunByID(work.Runs, runA.ID).State != WorkRunInterrupted ||
		workRunByID(work.Runs, runB2.ID).State != WorkRunRunning {
		t.Fatalf("work after A missing = %#v, want B2 active without whole-work interruption", work)
	}
	bindingA, err := service.GetCollaborationSession(ctx, owner.Agent.ID, owner.Token, runA.SessionRef)
	if err != nil {
		t.Fatalf("GetCollaborationSession(A) error = %v", err)
	}
	bindingB, err := service.GetCollaborationSession(ctx, owner.Agent.ID, owner.Token, runB2.SessionRef)
	if err != nil {
		t.Fatalf("GetCollaborationSession(B) error = %v", err)
	}
	if bindingA.State != CollaborationSessionMissing || bindingA.RunID != "" ||
		bindingB.State != CollaborationSessionRunning || bindingB.RunID != runB2.ID {
		t.Fatalf("parallel bindings after recovery = A:%#v B:%#v", bindingA, bindingB)
	}
}

func TestRunningCollaborationSessionCannotBeRescoped(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	agent := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, agent)
	client, err := service.BindAgent(ctx, agent.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent() error = %v", err)
	}
	const sessionRef = "active-coordination-session"
	params := CollaborationSessionBindParams{
		SessionRef: sessionRef, RoomID: room.ID,
		Purpose: CollaborationSessionCoordination, State: CollaborationSessionRunning,
	}
	if _, err := client.BindCollaborationSession(ctx, params); err != nil {
		t.Fatalf("BindCollaborationSession() error = %v", err)
	}
	if _, err := client.BindCollaborationSession(ctx, params); err != nil {
		t.Fatalf("idempotent running bind error = %v", err)
	}
	rescope := params
	rescope.RoomID = ""
	if _, err := client.BindCollaborationSession(ctx, rescope); !errors.Is(err, ErrConflict) {
		t.Fatalf("running session rescope error = %v, want conflict", err)
	}
	settle := params
	settle.State = CollaborationSessionIdle
	if _, err := client.BindCollaborationSession(ctx, settle); !errors.Is(err, ErrConflict) {
		t.Fatalf("running session state change through bind error = %v, want conflict", err)
	}
}

func collaborationSessionByRef(bindings []CollaborationSessionBinding, sessionRef string) CollaborationSessionBinding {
	for _, binding := range bindings {
		if binding.SessionRef == sessionRef {
			return binding
		}
	}
	return CollaborationSessionBinding{}
}

func workRunByID(runs []WorkRun, runID string) WorkRun {
	for _, run := range runs {
		if run.ID == runID {
			return run
		}
	}
	return WorkRun{}
}
