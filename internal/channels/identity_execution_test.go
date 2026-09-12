package channels

import (
	"context"
	"errors"
	"testing"
)

func prepareIdentityTestTurn(t *testing.T, s *Service, id string) (*AgentClient, CollaborationSessionBinding) {
	t.Helper()
	ctx := context.Background()
	a, e := s.GetAgentRuntime(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	b, ready, e := s.PrepareIdentityConversation(ctx, a, CollaborationSessionBindParams{})
	if e != nil || !ready {
		t.Fatalf("prepare: %+v %v %v", b, ready, e)
	}
	c, e := s.BindAgentSession(ctx, id, b.SessionRef)
	if e != nil {
		t.Fatal(e)
	}
	if b.State == CollaborationSessionQueued {
		if _, e = s.AdmitCollaborationSession(ctx, b.SessionRef); e != nil {
			t.Fatal(e)
		}
		b, e = c.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: b.SessionRef, State: CollaborationSessionRunning})
		if e != nil {
			t.Fatal(e)
		}
	}
	messages, e := c.ReceiveCollaboration(ctx, 32)
	if e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	for _, m := range messages {
		ids = append(ids, m.ID)
	}
	if e = c.AcknowledgeCollaboration(ctx, ids); e != nil {
		t.Fatal(e)
	}
	return c, b
}
func newIdentityTaskFixture(t *testing.T) (*Service, *AgentClient, AgentCredential, Room, Message) {
	t.Helper()
	ctx := context.Background()
	s := openTestService(t, nil)
	a := createTestAgent(t, s, "Owner")
	r := createTestRoom(t, s, a)
	c, e := s.BindAgent(ctx, a.Agent.ID)
	if e != nil {
		t.Fatal(e)
	}
	task, e := c.CreateTask(ctx, TaskCreateParams{RoomID: r.ID, Title: "Review implementation", Body: "Inspect original requirement", OwnerID: a.Agent.ID})
	if e != nil {
		t.Fatal(e)
	}
	return s, c, a, r, task
}
func TestIdentityResultsRetainOriginalRoomScope(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	a := createTestAgent(t, s, "Owner")
	p := createTestAgent(t, s, "Peer")
	r1 := createTestRoom(t, s, a)
	r2 := createTestRoom(t, s, a, p)
	if _, e := s.SendHuman(ctx, HumanSendParams{RoomID: r1.ID, HumanID: "human-1", Body: "Private room request"}); e != nil {
		t.Fatal(e)
	}
	_, b := prepareIdentityTestTurn(t, s, a.Agent.ID)
	if _, e := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: b.SessionRef, TurnID: "private-turn", State: CollaborationSessionIdle, Result: "RESULT ONLY FOR FIRST ROOM"}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.EnqueueSessionInput(ctx, CollaborationSessionSendParams{SessionRef: b.SessionRef, RoomID: r2.ID, Body: "Request in second room", RequestID: "second-room"}); e != nil {
		t.Fatal(e)
	}
	_, b = prepareIdentityTestTurn(t, s, a.Agent.ID)
	if b.RoomID != r2.ID {
		t.Fatal("did not switch rooms")
	}
	reader := flexibleTestSession(t, s, p.Agent.ID, r2.ID, "reader-second-room")
	got, e := reader.ReadSessionResults(ctx, b.SessionRef, 0, 10)
	if e != nil {
		t.Fatal(e)
	}
	for _, item := range got.Results {
		if item.TurnID == "private-turn" {
			t.Fatalf("peer outside first room can read its result: %q", item.Body)
		}
	}
	owner, err := s.BindAgentSession(ctx, a.Agent.ID, b.SessionRef)
	if err != nil {
		t.Fatal(err)
	}
	private, err := owner.ReadSessionResults(ctx, b.SessionRef, 0, 10)
	if err != nil || len(private.Results) != 1 {
		t.Fatalf("identity lost its own result: %+v %v", private, err)
	}
	if _, err := reader.ReadSessionResultPage(ctx, b.SessionRef, private.Results[0].ID, 0); err == nil {
		t.Fatal("paged result bypassed the original room boundary")
	}
}
func TestIdentityAdmissionPreservesWorkRun(t *testing.T) {
	ctx := context.Background()
	s, c, a, _, task := newIdentityTaskFixture(t)
	run, e := c.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer, NamedAgentID: a.Agent.ID})
	if e != nil {
		t.Fatal(e)
	}
	_, b := prepareIdentityTestTurn(t, s, a.Agent.ID)
	w, e := c.GetWork(ctx, task.ID)
	if e != nil {
		t.Fatal(e)
	}
	state := findRunState(w.Runs, run.ID)
	if state != WorkRunRunning || b.WorkID != task.ID {
		t.Fatalf("new run after admission: state=%s binding.work=%q binding.run=%q", state, b.WorkID, b.RunID)
	}
}

func TestIdentityWorkReservationWaitsForCurrentTurn(t *testing.T) {
	ctx := context.Background()
	s, client, agent, room, _ := newIdentityTaskFixture(t)
	_, binding := prepareIdentityTestTurn(t, s, agent.Agent.ID)
	queuedTask, err := client.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, OwnerID: agent.Agent.ID, Title: "Queued investigation"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := client.StartWorkRun(ctx, WorkRunStartParams{WorkID: queuedTask.ID, Kind: WorkRunProducer})
	if err != nil || run.State != WorkRunQueued {
		t.Fatalf("reservation occupied a second identity slot: %+v %v", run, err)
	}
	if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: binding.SessionRef, TurnID: "first-turn", State: CollaborationSessionIdle, Result: "First investigation finished"}); err != nil {
		t.Fatal(err)
	}
	_, next := prepareIdentityTestTurn(t, s, agent.Agent.ID)
	if next.WorkID != queuedTask.ID || next.RunID != run.ID || next.State != CollaborationSessionRunning {
		t.Fatalf("released slot did not start the reserved work: %+v", next)
	}
}

func TestIdentityBudgetFailureDoesNotBlockAnotherRoom(t *testing.T) {
	ctx := context.Background()
	s, client, agent, _, task := newIdentityTaskFixture(t)
	run, err := client.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.FinishWorkRun(ctx, WorkRunFinishParams{WorkID: task.ID, RunID: run.ID, State: WorkRunCompleted, InputTokens: 100}); err != nil {
		t.Fatal(err)
	}
	s.roomInputTokenLimit = 50
	runtime, err := s.GetAgentRuntime(ctx, agent.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding, ready, err := s.PrepareIdentityConversation(ctx, runtime, CollaborationSessionBindParams{})
	if err != nil || !ready {
		t.Fatalf("prepare budgeted work: %+v %v", binding, err)
	}
	parent, err := s.BindAgentSession(ctx, agent.Agent.ID, binding.SessionRef)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parent.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer}); !errors.Is(err, ErrConflict) {
		t.Fatalf("exhausted work was admitted: %v", err)
	}
	other := createTestRoom(t, s, agent)
	if _, err := s.EnqueueSessionInput(ctx, CollaborationSessionSendParams{SessionRef: binding.SessionRef, RoomID: other.ID, Body: "Answer another room", RequestID: "other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := parent.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: binding.SessionRef, State: CollaborationSessionRunning, TurnID: "admission-failure"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: binding.SessionRef, TurnID: "admission-failure", State: CollaborationSessionFailed, Result: "Room token budget exhausted", FailureReason: "Room token budget exhausted", AdmissionFailure: true}); err != nil {
		t.Fatal(err)
	}
	work, err := client.GetWork(ctx, task.ID)
	if err != nil || work.State != WorkNeedsHuman || work.FailureReason == "" {
		t.Fatalf("budget failure was not recorded: %+v %v", work, err)
	}
	_, next := prepareIdentityTestTurn(t, s, agent.Agent.ID)
	if next.RoomID != other.ID || next.WorkID != "" {
		t.Fatalf("failed work blocked another room: %+v", next)
	}
}
func TestIdentityGoalCorrectionFencesPreviousTurn(t *testing.T) {
	ctx := context.Background()
	s, _, a, r, task := newIdentityTaskFixture(t)
	c, b := prepareIdentityTestTurn(t, s, a.Agent.ID)
	if _, e := c.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: b.SessionRef, State: CollaborationSessionRunning, TurnID: "old-turn"}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.UpdateTaskHuman(ctx, TaskUpdateParams{TaskID: task.ID, RoomID: r.ID, HumanID: "human-1", GoalCorrection: "New requirement: inspect cancellation instead"}); e != nil {
		t.Fatal(e)
	}
	result, e := c.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, State: TaskStateDone})
	if e == nil {
		t.Fatalf("old turn completed revised task without reading correction: revision=%d state=%s", result.TaskGoalRevision, result.TaskState)
	}
	if _, err := c.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: b.SessionRef, State: CollaborationSessionRunning, TurnID: "old-turn"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("late startup revived a superseded turn: %v", err)
	}
}

func TestIdentityWorkSettlementPersistsUsageAndBudgetAcrossRestart(t *testing.T) {
	ctx := context.Background()
	s, _, agent, _, task := newIdentityTaskFixture(t)
	owner, binding := prepareIdentityTestTurn(t, s, agent.Agent.ID)
	run, err := owner.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.AttachWorkRunTurn(ctx, WorkRunTurnParams{WorkID: task.ID, RunID: run.ID, TurnID: "measured"}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: binding.SessionRef, State: CollaborationSessionRunning, TurnID: "measured"}); err != nil {
		t.Fatal(err)
	}
	params := CollaborationSessionSettleParams{SessionRef: binding.SessionRef, TurnID: "measured", State: CollaborationSessionIdle, Result: "Recorded evidence", InputTokens: 120, OutputTokens: 30, Provider: "test", Model: "test"}
	dir := s.dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for range 2 {
		if _, err := reopened.SettleCollaborationSession(ctx, params); err != nil {
			t.Fatal(err)
		}
	}
	work, err := reopened.GetWork(ctx, task.ID)
	if err != nil || len(work.Runs) != 1 || work.Runs[0].State != WorkRunCompleted || work.Runs[0].InputTokens != 120 || work.Runs[0].OutputTokens != 30 {
		t.Fatalf("recovered settlement lost or duplicated usage: %+v %v", work.Runs, err)
	}
	client, err := reopened.BindAgent(ctx, agent.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	reopened.roomInputTokenLimit = 100
	if _, err := client.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer}); !errors.Is(err, ErrConflict) {
		t.Fatalf("accounted usage did not enforce the room budget: %v", err)
	}
}

func TestIdentityAdmissionKeepsVerifierRunPrivateAndRevisionBound(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	owner := createTestAgent(t, s, "Producer")
	verifier := createTestAgent(t, s, "Verifier")
	room := createTestRoom(t, s, owner, verifier)
	client, err := s.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := client.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, OwnerID: owner.Agent.ID, Title: "Review evidence", VerificationRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	checking := promoteTestCandidate(t, s, client, task.ID)
	run, err := client.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunVerifier, NamedAgentID: verifier.Agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, binding := prepareIdentityTestTurn(t, s, verifier.Agent.ID)
	if binding.Purpose != CollaborationSessionVerification || binding.RunID != run.ID {
		t.Fatalf("verifier became a public producer: %+v", binding)
	}
	work, err := client.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range work.Runs {
		if got.ID == run.ID && (got.Kind != WorkRunVerifier || got.CandidateRevision != checking.TaskCandidateRevision || got.SessionRef != binding.SessionRef) {
			t.Fatalf("admission changed verifier scope: %+v", got)
		}
	}
	if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: binding.SessionRef, TurnID: "verification", State: CollaborationSessionCompleted, Result: "Verified", PublicReply: "Private verdict"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("verifier published a public conversation reply: %v", err)
	}
}
func TestIdentityCancelledTaskRejectsLateReply(t *testing.T) {
	ctx := context.Background()
	s, c, a, _, task := newIdentityTaskFixture(t)
	session, b := prepareIdentityTestTurn(t, s, a.Agent.ID)
	if _, e := session.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: b.SessionRef, State: CollaborationSessionRunning, TurnID: "cancelled-work-turn"}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.CancelWork(ctx, task.ID, "User cancelled"); e != nil {
		t.Fatal(e)
	}
	_, e := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: b.SessionRef, TurnID: "cancelled-work-turn", State: CollaborationSessionIdle, Result: "Late completion", PublicReply: "Cancelled work is complete"})
	if e == nil {
		t.Fatal("cancelled work's active identity turn can still publish a final reply")
	}
}

func TestIdentityTaskCompletionReturnsToCoordinator(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	a := createTestAgent(t, s, "Owner")
	r := createTestRoom(t, s, a)
	runtime, e := s.BindRuntime(ctx, r.RuntimeID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = runtime.BindCollaborationSession(ctx, CollaborationSessionBindParams{SessionRef: "coordinator", RoomID: r.ID, Purpose: CollaborationSessionCoordination}); e != nil {
		t.Fatal(e)
	}
	coordinator, e := s.BindRuntimeSession(ctx, r.RuntimeID, "coordinator")
	if e != nil {
		t.Fatal(e)
	}
	task, e := coordinator.CreateTask(ctx, TaskCreateParams{RoomID: r.ID, Title: "Find evidence", Body: "Inspect task and report", OwnerID: a.Agent.ID})
	if e != nil {
		t.Fatal(e)
	}
	owner, b := prepareIdentityTestTurn(t, s, a.Agent.ID)
	if _, e = owner.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, State: TaskStateDone}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: b.SessionRef, TurnID: "finished", State: CollaborationSessionIdle, Result: "Evidence found", PublicReply: "Evidence found"}); e != nil {
		t.Fatal(e)
	}
	messages, e := coordinator.ReceiveCollaboration(ctx, 32)
	if e != nil {
		t.Fatal(e)
	}
	if len(messages) == 0 {
		t.Fatal("coordinator received no terminal event after its assignment completed and public result was published")
	}
}
