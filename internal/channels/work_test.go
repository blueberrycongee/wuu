package channels

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func findRunState(runs []WorkRun, id string) WorkRunState {
	for _, run := range runs {
		if run.ID == id {
			return run.State
		}
	}
	return ""
}

func TestDurableWorkTracksDebtRunsArtifactsAndVerification(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	owner, _ := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Owner"})
	verifier, _ := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Verifier"})
	room, _ := service.CreateRoom(ctx, CreateRoomParams{
		Kind: RoomChannel, Name: "Work", CreatedBy: "local-user",
		Members: []RoomMember{{MemberType: MemberAgent, MemberID: owner.Agent.ID}, {MemberType: MemberAgent, MemberID: verifier.Agent.ID}},
	})
	source, err := service.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "local-user", Body: "Fix the callback"})
	if err != nil {
		t.Fatalf("SendHuman() error = %v", err)
	}
	leadClient, _ := bindTestTaskLead(t, ctx, service, room.ID)
	ownerClient, _ := service.BindAgent(ctx, owner.Agent.ID)
	task, err := leadClient.CreateTask(ctx, TaskCreateParams{
		RoomID: room.ID, SourceMessageID: source.Message.ID, Title: "Fix callback",
		Body: "Reject replayed state", OwnerID: owner.Agent.ID, VerificationRequired: true,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	ownerSession := bindTestWorkSession(t, service, ownerClient, room.ID, task.ID, "durable-owner-work")
	work, err := ownerClient.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetWork() error = %v", err)
	}
	if work.SourceMessageID != source.Message.ID || work.OwnerNamedAgentID != owner.Agent.ID || work.State != WorkOpen || work.VerificationState != WorkVerificationPending || len(work.PendingDeliveryRefs) != 1 {
		t.Fatalf("initial work = %#v", work)
	}
	listedMessages, err := service.ListMessages(ctx, room.ID, 0, 100)
	if err != nil || len(listedMessages) < 2 || listedMessages[1].Work == nil || len(listedMessages[1].Work.Events) != 1 {
		t.Fatalf("task Work projection = %#v, err = %v", listedMessages, err)
	}
	check, err := ownerSession.Check(ctx)
	if err != nil || len(check.Collaboration) != 1 || check.Collaboration[0].Kind != CollaborationAssignment || check.Collaboration[0].WorkID != task.ID {
		t.Fatalf("assignment delivery = %#v, err = %v", check.Collaboration, err)
	}
	if check.Collaboration[0].FromID != leadClient.AgentID() || check.Collaboration[0].FromType != MemberAgent || check.Collaboration[0].RecipientNamedAgentID != owner.Agent.ID {
		t.Fatalf("assignment lost visible sender or recipient: %#v", check.Collaboration[0])
	}
	if _, err := ownerClient.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, State: TaskStateDoing}); err != nil {
		t.Fatalf("start task: %v", err)
	}
	checking := promoteTestCandidate(t, service, ownerClient, task.ID)
	artifact, err := ownerClient.AddWorkArtifact(ctx, WorkArtifactAddParams{
		WorkID: task.ID, Kind: WorkArtifactDiff, URI: "artifact://diff-1",
		Summary: "2 files changed", WorkspaceRevision: "git:abc",
	})
	if err != nil {
		t.Fatalf("AddWorkArtifact() error = %v", err)
	}
	run, err := leadClient.StartWorkRun(ctx, WorkRunStartParams{
		WorkID: task.ID, Kind: WorkRunVerifier, Profile: verifier.Agent.ID,
		SessionRef: "session-verifier-1", WorkspaceRevision: "git:abc",
	})
	if err != nil {
		t.Fatalf("StartWorkRun() error = %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	service, err = Open(dir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer service.Close()
	leadClient, _ = bindTestTaskLead(t, ctx, service, room.ID)
	ownerClient, _ = service.BindAgent(ctx, owner.Agent.ID)
	restored, err := ownerClient.GetWork(ctx, task.ID)
	if err != nil || restored.CurrentRunRef != run.ID || len(restored.Runs) != 2 || len(restored.Artifacts) != 2 || restored.CandidateArtifactRef == "" || restored.CandidateArtifactRef == artifact.ID {
		t.Fatalf("restored work = %#v, err = %v", restored, err)
	}
	if _, err := leadClient.FinishWorkRun(ctx, WorkRunFinishParams{
		WorkID: task.ID, RunID: run.ID, State: WorkRunCompleted, Outcome: "pass",
		Provider: "openai", Model: "reviewer", InputTokens: 100, OutputTokens: 20, ChecksRerun: 1,
	}); err != nil {
		t.Fatalf("FinishWorkRun() error = %v", err)
	}
	result, err := leadClient.SubmitTaskVerification(ctx, TaskVerificationSubmitParams{
		TaskID: task.ID, RoomID: room.ID, GoalRevision: checking.TaskGoalRevision,
		CandidateRevision: checking.TaskCandidateRevision, Decision: VerificationPass,
		Report: "Focused replay check passed.", EvidenceRefs: []string{artifact.ID}, RunRef: run.ID,
	})
	if err != nil {
		t.Fatalf("SubmitTaskVerification() error = %v", err)
	}
	if result.Delivery.WorkID != task.ID || len(result.Delivery.ArtifactRefs) != 1 || result.Delivery.FromID != "" {
		t.Fatalf("verification delivery = %#v", result.Delivery)
	}
	verified, err := ownerClient.GetWork(ctx, task.ID)
	if err != nil || verified.VerificationState != WorkVerificationPass || verified.Verification == nil || verified.Verification.RunRef != run.ID || len(verified.Verification.EvidenceRefs) != 1 {
		t.Fatalf("verified work = %#v, err = %v", verified, err)
	}
	if len(verified.Events) < 4 || verified.Events[len(verified.Events)-1].Kind != "verification" {
		t.Fatalf("work event history = %#v", verified.Events)
	}
	delivery, err := ownerClient.Send(ctx, AgentSendParams{
		RoomID: room.ID, ThreadID: task.ID, ReplyTo: task.ID, Body: "Focused replay check passed.", BasisSeq: task.Seq,
	})
	if err != nil || delivery.Status != SendCommitted {
		t.Fatalf("Send(owner delivery) = %#v, err = %v", delivery, err)
	}
	if _, err := ownerClient.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, State: TaskStateDone}); err != nil {
		t.Fatalf("complete verified task: %v", err)
	}
	diagnostics, err := service.GetWorkDiagnostics(ctx, room.ID)
	if err != nil {
		t.Fatalf("GetWorkDiagnostics() error = %v", err)
	}
	if diagnostics.WorkCount != 1 || diagnostics.CompletedCount != 1 || diagnostics.ProducerVerifierCompletedCount != 1 || diagnostics.VerifierRunCount != 1 || diagnostics.InputTokens != 100 || diagnostics.OutputTokens != 20 || diagnostics.ChecksRerun != 1 {
		t.Fatalf("work diagnostics = %#v", diagnostics)
	}
}

func TestGoalRevisionInvalidatesPendingDeliveriesAndRunningHandles(t *testing.T) {
	ctx := context.Background()
	service, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer service.Close()
	owner, _ := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Owner"})
	verifier, _ := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Verifier"})
	room, _ := service.CreateRoom(ctx, CreateRoomParams{Kind: RoomChannel, Name: "Work", CreatedBy: "local-user", Members: []RoomMember{{MemberType: MemberAgent, MemberID: owner.Agent.ID}, {MemberType: MemberAgent, MemberID: verifier.Agent.ID}}})
	leadClient, _ := bindTestTaskLead(t, ctx, service, room.ID)
	ownerClient, _ := service.BindAgent(ctx, owner.Agent.ID)
	task, _ := leadClient.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Fix", OwnerID: owner.Agent.ID, VerificationRequired: true})
	ownerSession := bindTestWorkSession(t, service, ownerClient, room.ID, task.ID, "recovery-owner-work")
	_, _ = ownerSession.Check(ctx)
	_, _ = ownerClient.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, State: TaskStateDoing})
	checking := promoteTestCandidate(t, service, ownerClient, task.ID)
	run, err := leadClient.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunVerifier, Profile: verifier.Agent.ID, SessionRef: "session-stale"})
	if err != nil {
		t.Fatalf("StartWorkRun() error = %v", err)
	}
	revised, err := leadClient.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, GoalCorrection: "Fix and preserve audit events"})
	if err != nil {
		t.Fatalf("revise task: %v", err)
	}
	if revised.TaskGoalRevision != checking.TaskGoalRevision+1 {
		t.Fatalf("goal revision = %d", revised.TaskGoalRevision)
	}
	work, err := leadClient.GetWork(ctx, task.ID)
	if err != nil || work.CurrentRunRef != "" || work.State != WorkOpen || work.VerificationState != WorkVerificationPending {
		t.Fatalf("revised work = %#v, err = %v", work, err)
	}
	if len(work.Runs) != 2 || findRunState(work.Runs, run.ID) != WorkRunInterrupted {
		t.Fatalf("stale run not interrupted: %#v", work.Runs)
	}
	if _, err := leadClient.FinishWorkRun(ctx, WorkRunFinishParams{WorkID: task.ID, RunID: run.ID, State: WorkRunCompleted}); !errors.Is(err, ErrConflict) {
		t.Fatalf("FinishWorkRun(stale) error = %v, want conflict", err)
	}
}

func TestWorkRunRecoverySettlesMissingVerifierAsUnknown(t *testing.T) {
	ctx := context.Background()
	service, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer service.Close()
	owner, _ := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Owner"})
	verifier, _ := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Verifier"})
	room, _ := service.CreateRoom(ctx, CreateRoomParams{Kind: RoomChannel, Name: "Work", CreatedBy: "local-user", Members: []RoomMember{{MemberType: MemberAgent, MemberID: owner.Agent.ID}, {MemberType: MemberAgent, MemberID: verifier.Agent.ID}}})
	leadClient, _ := bindTestTaskLead(t, ctx, service, room.ID)
	ownerClient, _ := service.BindAgent(ctx, owner.Agent.ID)
	task, _ := leadClient.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Fix", OwnerID: owner.Agent.ID, VerificationRequired: true})
	ownerSession := bindTestWorkSession(t, service, ownerClient, room.ID, task.ID, "missing-recovery-owner-work")
	_, _ = ownerSession.Check(ctx)
	_, _ = ownerClient.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, State: TaskStateDoing})
	_ = promoteTestCandidate(t, service, ownerClient, task.ID)
	run, err := leadClient.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunVerifier, Profile: verifier.Agent.ID, SessionRef: "lost-session"})
	if err != nil {
		t.Fatalf("StartWorkRun() error = %v", err)
	}
	if err := service.ReconcileWorkRuns(ctx, []WorkRunRecovery{{RunID: run.ID, SessionRef: run.SessionRef, State: WorkRunRecoveryMissing}}); err != nil {
		t.Fatalf("ReconcileWorkRuns() error = %v", err)
	}
	work, err := ownerClient.GetWork(ctx, task.ID)
	if err != nil || work.State != WorkNeedsHuman || work.VerificationState != WorkVerificationUnknown || work.CurrentRunRef != "" || len(work.Runs) != 2 || findRunState(work.Runs, run.ID) != WorkRunInterrupted {
		t.Fatalf("recovered work = %#v, err = %v", work, err)
	}
	check, err := leadClient.Check(ctx)
	foundFeedback := false
	for _, item := range check.Collaboration {
		foundFeedback = foundFeedback || item.Kind == CollaborationVerificationFeedback && item.WorkID == task.ID
	}
	if err != nil || !foundFeedback {
		t.Fatalf("recovery feedback = %#v, err = %v", check.Collaboration, err)
	}
}

func TestMultiCandidateSelectorAndDomainVerifierAreOptIn(t *testing.T) {
	ctx := context.Background()
	service, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer service.Close()
	owner, _ := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Owner"})
	verifierAgent, _ := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Migration verifier"})
	room, _ := service.CreateRoom(ctx, CreateRoomParams{Kind: RoomChannel, Name: "Work", CreatedBy: "local-user", Members: []RoomMember{{MemberType: MemberAgent, MemberID: owner.Agent.ID}, {MemberType: MemberAgent, MemberID: verifierAgent.Agent.ID}}})
	leadClient, _ := bindTestTaskLead(t, ctx, service, room.ID)
	ownerClient, _ := service.BindAgent(ctx, owner.Agent.ID)
	task, _ := leadClient.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Compare", OwnerID: owner.Agent.ID, VerificationRequired: true})
	if _, err := leadClient.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunSelector}); !errors.Is(err, ErrConflict) {
		t.Fatalf("default selector error = %v, want conflict", err)
	}
	policy, err := leadClient.UpdateWorkPolicy(ctx, WorkPolicyUpdateParams{WorkID: task.ID, MaxVerifierAttempts: 4, MaxCandidates: 2, FanoutReason: "two mutually exclusive migration strategies"})
	if err != nil || policy.MaxCandidates != 2 {
		t.Fatalf("UpdateWorkPolicy() = %#v, %v", policy, err)
	}
	var candidates []WorkArtifact
	for index, uri := range []string{"artifact://candidate-a", "artifact://candidate-b"} {
		producer, err := ownerClient.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer, RequestID: fmt.Sprintf("producer-%d", index)})
		if err != nil {
			t.Fatalf("StartWorkRun(producer %d) error = %v", index, err)
		}
		artifact, err := ownerClient.AddWorkArtifact(ctx, WorkArtifactAddParams{WorkID: task.ID, RunID: producer.ID, Kind: WorkArtifactCandidate, URI: uri})
		if err != nil {
			t.Fatalf("AddWorkArtifact(%s) error = %v", uri, err)
		}
		candidates = append(candidates, artifact)
		if _, err := ownerClient.FinishWorkRun(ctx, WorkRunFinishParams{WorkID: task.ID, RunID: producer.ID, State: WorkRunCompleted, Qualified: true}); err != nil {
			t.Fatalf("FinishWorkRun(producer %d) error = %v", index, err)
		}
	}
	selector, err := leadClient.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, NamedAgentID: owner.Agent.ID, Kind: WorkRunSelector, Profile: "selection"})
	if err != nil || selector.Kind != WorkRunSelector {
		t.Fatalf("selector = %#v, %v", selector, err)
	}
	if _, err := leadClient.PromoteWorkCandidate(ctx, WorkCandidatePromoteParams{WorkID: task.ID, RunID: selector.ID, ArtifactRef: candidates[1].ID, RequestID: "select-b", SelectionReason: "candidate b covers migration"}); err != nil {
		t.Fatalf("promote selector result: %v", err)
	}
	if _, err := leadClient.FinishWorkRun(ctx, WorkRunFinishParams{WorkID: task.ID, RunID: selector.ID, State: WorkRunCompleted, Outcome: "candidate-b"}); err != nil {
		t.Fatalf("finish selector: %v", err)
	}
	verifier, err := leadClient.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunVerifier, Profile: verifierAgent.Agent.ID, SessionRef: "migration-verifier"})
	if err != nil || verifier.Profile != verifierAgent.Agent.ID {
		t.Fatalf("domain verifier = %#v, %v", verifier, err)
	}
}

func TestVerifierRequiresAVisibleIndependentMember(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	reviewer := createTestAgent(t, service, "Reviewer")
	room := createTestRoom(t, service, owner, reviewer)
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := ownerClient.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Deliver result", OwnerID: owner.Agent.ID, VerificationRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = promoteTestCandidate(t, service, ownerClient, task.ID)
	for _, tt := range []struct {
		params WorkRunStartParams
		want   error
	}{
		{WorkRunStartParams{WorkID: task.ID, Kind: WorkRunVerifier}, ErrUnauthorized},
		{WorkRunStartParams{WorkID: task.ID, Kind: WorkRunVerifier, NamedAgentID: owner.Agent.ID}, ErrConflict},
	} {
		if _, err := ownerClient.StartWorkRun(ctx, tt.params); !errors.Is(err, tt.want) {
			t.Fatalf("anonymous or self verification = %v, want %v", err, tt.want)
		}
	}
	run, err := ownerClient.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunVerifier, NamedAgentID: reviewer.Agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if run.NamedAgentID != reviewer.Agent.ID || run.SessionRef == "" {
		t.Fatalf("verifier run = %#v", run)
	}
	binding, err := service.LookupCollaborationSession(ctx, run.SessionRef)
	if err != nil || binding.PrincipalID != reviewer.Agent.ID || binding.RunID != run.ID || binding.Purpose != CollaborationSessionVerification {
		t.Fatalf("verifier binding = %#v, err = %v", binding, err)
	}
}

func TestCancelWorkRetiresPendingTaskWake(t *testing.T) {
	ctx := context.Background()
	service, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer service.Close()
	owner, err := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Owner"})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	room, err := service.CreateRoom(ctx, CreateRoomParams{
		Kind: RoomChannel, Name: "Work", CreatedBy: "local-user",
		Members: []RoomMember{
			{MemberType: MemberHuman, MemberID: "local-user"},
			{MemberType: MemberAgent, MemberID: owner.Agent.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	task, err := service.CreateTaskHuman(ctx, TaskCreateParams{
		RoomID: room.ID, HumanID: "local-user", Title: "Cancel before dispatch", OwnerID: owner.Agent.ID,
	})
	if err != nil {
		t.Fatalf("CreateTaskHuman() error = %v", err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent() error = %v", err)
	}
	if _, err := ownerClient.CancelWork(ctx, task.ID, "No longer needed"); err != nil {
		t.Fatalf("CancelWork() error = %v", err)
	}
	items, err := service.ListInbox(ctx, owner.Agent.ID, true)
	if err != nil {
		t.Fatalf("ListInbox() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("pending inbox after cancellation = %#v", items)
	}
	dispatches, err := service.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("PendingCollaborationDispatches() error = %v", err)
	}
	if len(dispatches) != 0 {
		t.Fatalf("pending collaboration after cancellation = %#v", dispatches)
	}
	wake, err := service.WakeState(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("WakeState() error = %v", err)
	}
	if wake.Outstanding || wake.Pending {
		t.Fatalf("wake after cancellation = %#v, want idle", wake)
	}
}
