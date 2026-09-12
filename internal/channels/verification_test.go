package channels

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func bindTestTaskLead(t *testing.T, ctx context.Context, service *Service, roomID string) (*AgentClient, error) {
	t.Helper()
	lead, err := bindTestRoomLead(t, ctx, service, roomID)
	if err != nil {
		return nil, err
	}
	sessionRef := "test-task-lead-" + roomID
	if _, err := lead.BindCollaborationSession(ctx, CollaborationSessionBindParams{SessionRef: sessionRef, RoomID: roomID, Purpose: CollaborationSessionConversation}); err != nil {
		return nil, err
	}
	return service.BindAgentSession(ctx, lead.AgentID(), sessionRef)
}

func completeIndependentVerifierRun(t *testing.T, ctx context.Context, leadClient *AgentClient, taskID, sessionRef string) string {
	t.Helper()
	run, err := leadClient.StartWorkRun(ctx, WorkRunStartParams{
		WorkID: taskID, Kind: WorkRunVerifier, NamedAgentID: leadClient.AgentID(), SessionRef: sessionRef,
	})
	if err != nil {
		t.Fatalf("StartWorkRun(independent verifier) error = %v", err)
	}
	if _, err := leadClient.FinishWorkRun(ctx, WorkRunFinishParams{
		WorkID: taskID, RunID: run.ID, State: WorkRunCompleted, Outcome: "completed independent check",
	}); err != nil {
		t.Fatalf("FinishWorkRun(independent verifier) error = %v", err)
	}
	return run.ID
}

func TestTaskVerificationPersistsAndWakesVisibleOwner(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	owner := createTestAgent(t, service, "Andy")
	room, err := service.CreateRoom(ctx, CreateRoomParams{
		Kind: RoomChannel, Name: "Build", CreatedBy: "local-user",
		Members: []RoomMember{
			{MemberType: MemberHuman, MemberID: "local-user"},
			{MemberType: MemberAgent, MemberID: owner.Agent.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	leadClient, err := bindTestTaskLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatalf("BindAgent(lead) error = %v", err)
	}
	task, err := leadClient.CreateTask(ctx, TaskCreateParams{
		RoomID: room.ID, Title: "Fix callback", Body: "Reject replayed state", OwnerID: owner.Agent.ID,
		VerificationRequired: true,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(owner) error = %v", err)
	}
	ownerSession := bindTestWorkSession(t, service, ownerClient, room.ID, task.ID, "verification-owner-work")
	sink.take() // Assignment wake is not part of the verification assertion.
	if _, err := ownerSession.Check(ctx); err != nil {
		t.Fatalf("CheckSession(owner assignment) error = %v", err)
	}
	if _, err := service.UpdateTask(ctx, TaskUpdateParams{
		TaskID: task.ID, State: TaskStateDone, AgentID: owner.Agent.ID, Token: owner.Token,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("unverified completion error = %v, want conflict", err)
	}
	checking := promoteTestCandidate(t, service, ownerSession, task.ID)
	if checking.TaskGoalRevision != 1 || checking.TaskCandidateRevision != 1 {
		t.Fatalf("initial checking task = %#v", checking)
	}
	sink.take() // Candidate promotion wake is not part of the feedback assertion.

	blockRunRef := completeIndependentVerifierRun(t, ctx, leadClient, task.ID, "block-check")
	blocked, err := leadClient.SubmitTaskVerification(ctx, TaskVerificationSubmitParams{
		RoomID: room.ID, TaskID: task.ID, Decision: VerificationBlock,
		Report: "The replay test still creates a second session.", GoalRevision: 1, CandidateRevision: 1, RunRef: blockRunRef,
	})
	if err != nil {
		t.Fatalf("SubmitTaskVerification(block) error = %v", err)
	}
	if blocked.Verification.Attempt != 1 || blocked.Verification.OwnerID != owner.Agent.ID {
		t.Fatalf("blocked verification = %#v", blocked.Verification)
	}
	tasks, err := service.ListTasks(ctx, TaskListParams{RoomID: room.ID, AgentID: owner.Agent.ID, Token: owner.Token})
	if err != nil || len(tasks) != 1 || tasks[0].TaskState != string(TaskStateRevising) {
		t.Fatalf("blocked task projection = %#v, err = %v", tasks, err)
	}
	if blocked.Delivery.Kind != CollaborationVerificationFeedback || blocked.Delivery.SourceMessageID != task.ID {
		t.Fatalf("verification delivery = %#v", blocked.Delivery)
	}
	if got := sink.take(); len(got) != 1 || got[0] != owner.Agent.ID {
		t.Fatalf("verification wakes = %v, want owner", got)
	}

	checked, err := ownerSession.Check(ctx)
	if err != nil {
		t.Fatalf("CheckSession(owner) error = %v", err)
	}
	found := false
	for _, delivery := range checked.Collaboration {
		if delivery.ID != blocked.Delivery.ID {
			continue
		}
		found = delivery.Kind == CollaborationVerificationFeedback &&
			strings.Contains(delivery.Body, "Verification block") &&
			strings.Contains(delivery.Body, "task is now revising")
	}
	if !found {
		t.Fatalf("owner collaboration missing verification feedback: %#v", checked.Collaboration)
	}
	if _, err := service.UpdateTask(ctx, TaskUpdateParams{
		TaskID: task.ID, State: TaskStateDoing, AgentID: owner.Agent.ID, Token: owner.Token,
	}); err != nil {
		t.Fatalf("UpdateTask(repair doing) error = %v", err)
	}
	checking = promoteTestCandidate(t, service, ownerSession, task.ID)
	if checking.TaskCandidateRevision != 2 {
		t.Fatalf("repair checking task = %#v", checking)
	}

	passRunRef := completeIndependentVerifierRun(t, ctx, leadClient, task.ID, "pass-check")
	passed, err := leadClient.SubmitTaskVerification(ctx, TaskVerificationSubmitParams{
		RoomID: room.ID, TaskID: task.ID, Decision: VerificationPass,
		Report: "Replay is rejected and focused tests pass.", GoalRevision: 1, CandidateRevision: 2, RunRef: passRunRef,
	})
	if err != nil {
		t.Fatalf("SubmitTaskVerification(pass) error = %v", err)
	}
	if passed.Verification.Attempt != 2 || !strings.Contains(passed.Delivery.Body, "Publish the result") {
		t.Fatalf("passed verification = %#v, delivery = %#v", passed.Verification, passed.Delivery)
	}
	tasks, err = service.ListTasks(ctx, TaskListParams{RoomID: room.ID, AgentID: owner.Agent.ID, Token: owner.Token})
	if err != nil || len(tasks) != 1 || tasks[0].TaskState != string(TaskStateChecking) {
		t.Fatalf("passed task projection = %#v, err = %v", tasks, err)
	}
	persisted, err := service.GetTaskVerification(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskVerification() error = %v", err)
	}
	if persisted.Decision != VerificationPass || persisted.Attempt != 2 || persisted.GoalRevision != 1 ||
		persisted.CandidateRevision != 2 || persisted.Report != "Replay is rejected and focused tests pass." {
		t.Fatalf("persisted verification = %#v", persisted)
	}
	if _, err := service.UpdateTask(ctx, TaskUpdateParams{
		TaskID: task.ID, State: TaskStateDone, AgentID: owner.Agent.ID, Token: owner.Token,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("completion before owner delivery error = %v, want conflict", err)
	}
	delivery, err := service.SendAgent(ctx, AgentSendParams{
		AgentID: owner.Agent.ID, Token: owner.Token, RoomID: room.ID, ThreadID: task.ID, ReplyTo: task.ID,
		Body: "Replay is rejected and the focused tests pass.", BasisSeq: task.Seq,
	})
	if err != nil || delivery.Status != SendCommitted {
		t.Fatalf("Send(owner delivery) = %#v, err = %v", delivery, err)
	}
	completed, err := service.UpdateTask(ctx, TaskUpdateParams{
		TaskID: task.ID, State: TaskStateDone, AgentID: owner.Agent.ID, Token: owner.Token,
	})
	if err != nil || completed.TaskState != string(TaskStateDone) {
		t.Fatalf("verified completion = %#v, err = %v", completed, err)
	}
}

func TestNamedVerifierReturnsAuditableResultToVisibleLead(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	verifier := createTestAgent(t, service, "Verifier")
	room, err := service.CreateRoom(ctx, CreateRoomParams{
		Kind: RoomChannel, Name: "Build", CreatedBy: "local-user",
		Members: []RoomMember{
			{MemberType: MemberAgent, MemberID: owner.Agent.ID},
			{MemberType: MemberAgent, MemberID: verifier.Agent.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	leadClient, _ := bindTestTaskLead(t, ctx, service, room.ID)
	ownerClient, _ := service.BindAgent(ctx, owner.Agent.ID)
	verifierClient, _ := service.BindAgent(ctx, verifier.Agent.ID)
	task, err := leadClient.CreateTask(ctx, TaskCreateParams{
		RoomID: room.ID, Title: "Fix callback", Body: "Reject replayed state",
		OwnerID: owner.Agent.ID, VerificationRequired: true,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	_, _ = ownerClient.Check(ctx)
	_, _ = verifierClient.Check(ctx)
	checking := promoteTestCandidate(t, service, ownerClient, task.ID)
	_, _ = leadClient.Check(ctx)
	run, err := leadClient.StartWorkRun(ctx, WorkRunStartParams{
		WorkID: task.ID, Kind: WorkRunVerifier, Profile: verifier.Agent.ID,
		SessionRef: "named-verifier-session",
	})
	if err != nil {
		t.Fatalf("StartWorkRun(verifier) error = %v", err)
	}
	if run.NamedAgentID != verifier.Agent.ID {
		t.Fatalf("verifier run named agent = %q, want %q", run.NamedAgentID, verifier.Agent.ID)
	}
	binding, err := service.GetCollaborationSession(ctx, verifier.Agent.ID, verifier.Token, run.SessionRef)
	if err != nil {
		t.Fatalf("GetCollaborationSession(verifier) error = %v", err)
	}
	if binding.Purpose != CollaborationSessionVerification || binding.WorkID != task.ID ||
		binding.RunID != run.ID || binding.State != CollaborationSessionRunning {
		t.Fatalf("verifier session binding = %#v", binding)
	}
	verifierClient, err = service.BindAgentSession(ctx, verifier.Agent.ID, run.SessionRef)
	if err != nil {
		t.Fatal(err)
	}
	control, err := leadClient.SendCollaboration(ctx, CollaborationSendParams{
		RoomID: room.ID, ToAgentID: verifier.Agent.ID, TargetSessionRef: run.SessionRef, Kind: CollaborationControl,
		SourceMessageID: task.ID, Body: "Independently verify the current candidate.",
	})
	if err != nil {
		t.Fatalf("SendCollaboration(verification control) error = %v", err)
	}
	_, _ = verifierClient.Check(ctx)
	result, err := verifierClient.SendCollaboration(ctx, CollaborationSendParams{
		RoomID: room.ID, Kind: CollaborationPeerResult, SourceMessageID: task.ID,
		ReplyTo: control.ID, Body: "PASS\nReplay is rejected and the focused checks pass.",
	})
	if err != nil {
		t.Fatalf("SendCollaboration(peer_result) error = %v", err)
	}
	if result.ToAgentID != leadClient.AgentID() || result.GoalRevision != checking.TaskGoalRevision || result.CandidateRevision != checking.TaskCandidateRevision {
		t.Fatalf("peer result = %#v", result)
	}
	if _, err := ownerClient.SendCollaboration(ctx, CollaborationSendParams{
		RoomID: room.ID, Kind: CollaborationPeerResult, SourceMessageID: task.ID, Body: "PASS\nSelf approved.",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("owner peer_result error = %v, want conflict", err)
	}
	pending, err := service.PendingCollaborationDispatches(ctx, leadClient.AgentID())
	foundPending := false
	for _, item := range pending {
		foundPending = foundPending || item.ID == result.ID
	}
	if err != nil || !foundPending {
		t.Fatalf("pending peer result = %#v, err = %v", pending, err)
	}
	dir := service.Dir()
	if err := service.Close(); err != nil {
		t.Fatalf("Close(before peer result recovery) error = %v", err)
	}
	service, err = Open(dir, nil)
	if err != nil {
		t.Fatalf("Open(peer result recovery) error = %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	leadClient, _ = bindTestTaskLead(t, ctx, service, room.ID)
	recovered, err := leadClient.Check(ctx)
	recoveredPeer := false
	for _, delivery := range recovered.Collaboration {
		recoveredPeer = recoveredPeer || delivery.ID == result.ID
	}
	if err != nil || !recoveredPeer {
		t.Fatalf("recovered peer result = %#v, err = %v", recovered.Collaboration, err)
	}
	if _, err := leadClient.FinishWorkRun(ctx, WorkRunFinishParams{
		WorkID: task.ID, RunID: run.ID, State: WorkRunCompleted,
		Outcome: "PASS", ChecksRerun: 1,
	}); err != nil {
		t.Fatalf("FinishWorkRun(verifier) error = %v", err)
	}
	if _, err := leadClient.SubmitTaskVerification(ctx, TaskVerificationSubmitParams{
		RoomID: room.ID, TaskID: task.ID, GoalRevision: checking.TaskGoalRevision,
		CandidateRevision: checking.TaskCandidateRevision, Decision: VerificationPass,
		Report: "Replay is rejected and the focused checks pass.", RunRef: run.ID,
	}); err != nil {
		t.Fatalf("SubmitTaskVerification() error = %v", err)
	}
	work, err := leadClient.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetWork() error = %v", err)
	}
	if len(work.Deliveries) < 4 {
		t.Fatalf("auditable deliveries = %#v", work.Deliveries)
	}
	foundResult := false
	for _, delivery := range work.Deliveries {
		foundResult = foundResult || delivery.ID == result.ID
	}
	if !foundResult {
		t.Fatalf("work deliveries omitted peer result: %#v", work.Deliveries)
	}
}

func TestTaskVerificationRejectsNonMemberAndInvalidDecision(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Andy")
	room, err := service.CreateRoom(ctx, CreateRoomParams{
		Kind: RoomChannel, Name: "Build", CreatedBy: "local-user",
		Members: []RoomMember{
			{MemberType: MemberHuman, MemberID: "local-user"},
			{MemberType: MemberAgent, MemberID: owner.Agent.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	leadClient, _ := bindTestTaskLead(t, ctx, service, room.ID)
	task, err := leadClient.CreateTask(ctx, TaskCreateParams{
		RoomID: room.ID, Title: "Fix", OwnerID: owner.Agent.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	outsider := createTestAgent(t, service, "Outside reviewer")
	outsiderClient, _ := service.BindAgent(ctx, outsider.Agent.ID)
	_, err = outsiderClient.SubmitTaskVerification(ctx, TaskVerificationSubmitParams{
		RoomID: room.ID, TaskID: task.ID, Decision: VerificationPass, Report: "looks good",
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("non-member verification error = %v, want unauthorized", err)
	}
	_, err = leadClient.SubmitTaskVerification(ctx, TaskVerificationSubmitParams{
		RoomID: room.ID, TaskID: task.ID, Decision: "maybe", Report: "unclear",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid verification decision") {
		t.Fatalf("invalid decision error = %v", err)
	}
}

func TestTaskVerificationRejectsStaleGoalAndCandidateRevisions(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	owner := createTestAgent(t, service, "Andy")
	room, err := service.CreateRoom(ctx, CreateRoomParams{
		Kind: RoomChannel, Name: "Build", CreatedBy: "local-user",
		Members: []RoomMember{
			{MemberType: MemberHuman, MemberID: "local-user"},
			{MemberType: MemberAgent, MemberID: owner.Agent.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	leadClient, _ := bindTestTaskLead(t, ctx, service, room.ID)
	task, err := leadClient.CreateTask(ctx, TaskCreateParams{
		RoomID: room.ID, Title: "Fix callback", Body: "Reject replayed state",
		OwnerID: owner.Agent.ID, VerificationRequired: true,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	ownerClient, _ := service.BindAgent(ctx, owner.Agent.ID)
	ownerSession := bindTestWorkSession(t, service, ownerClient, room.ID, task.ID, "stale-revision-owner-work")
	if _, err := ownerSession.Check(ctx); err != nil {
		t.Fatalf("CheckSession(owner assignment) error = %v", err)
	}
	sink.take()
	checking := promoteTestCandidate(t, service, ownerClient, task.ID)
	revised, err := leadClient.UpdateTask(ctx, TaskUpdateParams{
		TaskID: task.ID, GoalCorrection: "Reject replayed and expired state",
	})
	if err != nil {
		t.Fatalf("UpdateTask(goal correction) error = %v", err)
	}
	if revised.TaskGoalRevision != 2 || revised.TaskState != string(TaskStateOpen) {
		t.Fatalf("revised task = %#v", revised)
	}
	if interrupted := sink.takeInterrupts(); len(interrupted) != 0 {
		t.Fatalf("goal revision interrupted an idle session: %v", interrupted)
	}
	ownerInbox, err := ownerSession.Check(ctx)
	if err != nil {
		t.Fatalf("CheckSession(owner goal revision) error = %v", err)
	}
	foundRevisionNotice := false
	for _, delivery := range ownerInbox.Collaboration {
		foundRevisionNotice = foundRevisionNotice || delivery.WorkID == task.ID && strings.Contains(delivery.Body, "previous goal was not applied")
	}
	if !foundRevisionNotice {
		t.Fatalf("owner did not receive explicit goal revision notice: %#v", ownerInbox.Collaboration)
	}
	_, err = leadClient.SubmitTaskVerification(ctx, TaskVerificationSubmitParams{
		RoomID: room.ID, TaskID: task.ID, Decision: VerificationPass, Report: "old goal passed",
		GoalRevision: checking.TaskGoalRevision, CandidateRevision: checking.TaskCandidateRevision,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale goal verification error = %v, want conflict", err)
	}

	checking = promoteTestCandidate(t, service, ownerClient, task.ID)
	newGoalRunRef := completeIndependentVerifierRun(t, ctx, leadClient, task.ID, "new-goal-check")
	if _, err := leadClient.SubmitTaskVerification(ctx, TaskVerificationSubmitParams{
		RoomID: room.ID, TaskID: task.ID, Decision: VerificationPass, Report: "new goal passed",
		GoalRevision: checking.TaskGoalRevision, CandidateRevision: checking.TaskCandidateRevision, RunRef: newGoalRunRef,
	}); err != nil {
		t.Fatalf("SubmitTaskVerification(new goal) error = %v", err)
	}
	if _, err := ownerClient.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, State: TaskStateDoing}); err != nil {
		t.Fatalf("UpdateTask(post-pass doing) error = %v", err)
	}
	if _, err := ownerClient.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, State: TaskStateDone}); !errors.Is(err, ErrConflict) {
		t.Fatalf("completion after candidate changed error = %v, want conflict", err)
	}
}

func TestCandidateAndFeedbackDeliveriesRecoverAfterRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	owner := createTestAgent(t, service, "Andy")
	room, err := service.CreateRoom(ctx, CreateRoomParams{
		Kind: RoomChannel, Name: "Build", CreatedBy: "local-user",
		Members: []RoomMember{
			{MemberType: MemberHuman, MemberID: "local-user"},
			{MemberType: MemberAgent, MemberID: owner.Agent.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	leadClient, _ := bindTestTaskLead(t, ctx, service, room.ID)
	ownerClient, _ := service.BindAgent(ctx, owner.Agent.ID)
	task, err := leadClient.CreateTask(ctx, TaskCreateParams{
		RoomID: room.ID, Title: "Fix callback", OwnerID: owner.Agent.ID, VerificationRequired: true,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	const ownerSessionRef = "recovery-owner-work"
	ownerSession := bindTestWorkSession(t, service, ownerClient, room.ID, task.ID, ownerSessionRef)
	if _, err := ownerSession.Check(ctx); err != nil {
		t.Fatalf("CheckSession(owner assignment) error = %v", err)
	}
	checking := promoteTestCandidate(t, service, ownerClient, task.ID)
	promotedWork, err := ownerClient.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetWork(promoted) error = %v", err)
	}
	var delivery CollaborationMessage
	for _, candidateDelivery := range promotedWork.Deliveries {
		if candidateDelivery.Kind == CollaborationCandidateReady {
			delivery = candidateDelivery
		}
	}
	if delivery.ID == "" {
		t.Fatal("promoted candidate delivery was not persisted")
	}
	if delivery.GoalRevision != checking.TaskGoalRevision || delivery.CandidateRevision != checking.TaskCandidateRevision {
		t.Fatalf("candidate delivery = %#v", delivery)
	}
	if delivery.ToAgentID != leadClient.AgentID() || delivery.TargetSessionRef != leadClient.SessionRef() {
		t.Fatalf("candidate did not return to the initiating session: %#v", delivery)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close(before candidate recovery) error = %v", err)
	}

	service, err = Open(dir, nil)
	if err != nil {
		t.Fatalf("Open(candidate recovery) error = %v", err)
	}
	leadClient, _ = bindTestTaskLead(t, ctx, service, room.ID)
	recovered, err := leadClient.Check(ctx)
	foundCandidate := false
	for _, item := range recovered.Collaboration {
		foundCandidate = foundCandidate || item.ID == delivery.ID
	}
	if err != nil || !foundCandidate {
		t.Fatalf("recovered candidate = %#v, err = %v", recovered.Collaboration, err)
	}
	recoveredRunRef := completeIndependentVerifierRun(t, ctx, leadClient, task.ID, "recovered-candidate-check")
	blocked, err := leadClient.SubmitTaskVerification(ctx, TaskVerificationSubmitParams{
		RoomID: room.ID, TaskID: task.ID, Decision: VerificationBlock, Report: "Replay still succeeds.",
		GoalRevision: checking.TaskGoalRevision, CandidateRevision: checking.TaskCandidateRevision, RunRef: recoveredRunRef,
	})
	if err != nil {
		t.Fatalf("SubmitTaskVerification(block) error = %v", err)
	}
	ownerClient, _ = service.BindAgent(ctx, owner.Agent.ID)
	ownerSession, err = service.BindAgentSession(ctx, owner.Agent.ID, ownerSessionRef)
	if err != nil {
		t.Fatalf("BindAgentSession(owner after candidate recovery) error = %v", err)
	}
	pendingFeedback, err := service.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	foundFeedback := false
	for _, item := range pendingFeedback {
		foundFeedback = foundFeedback || item.ID == blocked.Delivery.ID
	}
	if err != nil || !foundFeedback {
		t.Fatalf("pending owner feedback = %#v, err = %v", pendingFeedback, err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close(before feedback recovery) error = %v", err)
	}

	service, err = Open(dir, nil)
	if err != nil {
		t.Fatalf("Open(feedback recovery) error = %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	ownerClient, _ = service.BindAgent(ctx, owner.Agent.ID)
	ownerSession, err = service.BindAgentSession(ctx, owner.Agent.ID, ownerSessionRef)
	if err != nil {
		t.Fatalf("BindAgentSession(owner after feedback recovery) error = %v", err)
	}
	recovered, err = ownerSession.Check(ctx)
	if err != nil || len(recovered.Collaboration) != 1 || recovered.Collaboration[0].ID != blocked.Delivery.ID {
		t.Fatalf("recovered feedback = %#v, err = %v", recovered.Collaboration, err)
	}
}

func TestVerifierAttemptExhaustionReturnsTheSameTaskToTheUser(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Andy")
	room, err := service.CreateRoom(ctx, CreateRoomParams{
		Kind: RoomChannel, Name: "Build", CreatedBy: "local-user",
		Members: []RoomMember{{MemberType: MemberHuman, MemberID: "local-user"}, {MemberType: MemberAgent, MemberID: owner.Agent.ID}},
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	leadClient, _ := bindTestTaskLead(t, ctx, service, room.ID)
	ownerClient, _ := service.BindAgent(ctx, owner.Agent.ID)
	task, err := leadClient.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Deliver", OwnerID: owner.Agent.ID, VerificationRequired: true})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := leadClient.UpdateWorkPolicy(ctx, WorkPolicyUpdateParams{
		WorkID: task.ID, MaxVerifierAttempts: 1, MaxCandidates: 1,
	}); err != nil {
		t.Fatalf("UpdateWorkPolicy() error = %v", err)
	}
	_, _ = ownerClient.Check(ctx)
	checking := promoteTestCandidate(t, service, ownerClient, task.ID)
	runRef := completeIndependentVerifierRun(t, ctx, leadClient, task.ID, "last-check")
	blocked, err := leadClient.SubmitTaskVerification(ctx, TaskVerificationSubmitParams{
		RoomID: room.ID, TaskID: task.ID, Decision: VerificationBlock, Report: "The result still misses the requested output.",
		GoalRevision: checking.TaskGoalRevision, CandidateRevision: checking.TaskCandidateRevision, RunRef: runRef,
	})
	if err != nil {
		t.Fatalf("SubmitTaskVerification() error = %v", err)
	}
	updated, err := leadClient.GetWork(ctx, task.ID)
	if err != nil || updated.State != WorkNeedsHuman || updated.ID != task.ID {
		t.Fatalf("exhausted work = %#v, err = %v", updated, err)
	}
	if !strings.Contains(blocked.Delivery.Body, "needs human input") || strings.Contains(blocked.Delivery.Body, "Start the repair") {
		t.Fatalf("exhausted feedback = %q", blocked.Delivery.Body)
	}
}
