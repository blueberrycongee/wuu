package channels

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacyDeliverySchemaMigratesToVersionedEnvelope(t *testing.T) {
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(dir, databaseFileName)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF; DROP INDEX IF EXISTS idx_collaboration_inbox; DROP TABLE collaboration_messages; CREATE TABLE collaboration_messages (
		id TEXT PRIMARY KEY, room_id TEXT NOT NULL, from_type TEXT NOT NULL, from_id TEXT NOT NULL,
		from_session_ref TEXT, to_agent_id TEXT NOT NULL, target_session_ref TEXT,
		kind TEXT NOT NULL DEFAULT 'control' CHECK (kind IN ('control', 'assignment', 'peer_result', 'candidate_ready', 'verification_feedback', 'completion')),
		body TEXT NOT NULL, work_id TEXT, source_message_id TEXT, goal_revision INTEGER NOT NULL DEFAULT 0,
		candidate_revision INTEGER NOT NULL DEFAULT 0, artifact_refs_json TEXT NOT NULL DEFAULT '[]',
		reply_to TEXT, created_at INTEGER NOT NULL, pulled_at INTEGER, consumed_at INTEGER, invalidated_at INTEGER,
		FOREIGN KEY (to_agent_id) REFERENCES collaboration_principals(id) ON DELETE CASCADE
	)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	service, err = Open(dir, nil)
	if err != nil {
		t.Fatalf("Open(legacy envelope) error = %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	var schema string
	if err := service.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'collaboration_messages'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "'work_run_terminal'") || !strings.Contains(schema, "target_kind") || !strings.Contains(schema, "correlation_id") {
		t.Fatalf("migrated collaboration schema = %s", schema)
	}
	if hasCost, err := service.tableHasColumn("work_runs", "cost_usd"); err != nil || !hasCost {
		t.Fatalf("migrated work run cost column = %v, %v", hasCost, err)
	}
}

func TestWorkRunAdmissionQueuesAndPromotesDurably(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	service.SetCollaborationRunLimits(2, 8, 8)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, _ := bindTestRoomLead(t, ctx, service, room.ID)
	task, err := runtime.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Parallel", OwnerID: owner.Agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.UpdateWorkPolicy(ctx, WorkPolicyUpdateParams{
		WorkID: task.ID, LeadNamedAgentID: runtime.AgentID(), MaxVerifierAttempts: 3,
		MaxCandidates: 4, MaxRounds: 3, FanoutReason: "four differentiated implementation routes",
	}); err != nil {
		t.Fatal(err)
	}
	var runs []WorkRun
	for _, requestID := range []string{"route-a", "route-b", "route-c"} {
		run, err := runtime.StartWorkRun(ctx, WorkRunStartParams{
			WorkID: task.ID, NamedAgentID: owner.Agent.ID, Kind: WorkRunProducer,
			Profile: requestID, RequestID: requestID,
		})
		if err != nil {
			t.Fatalf("StartWorkRun(%s): %v", requestID, err)
		}
		runs = append(runs, run)
	}
	if runs[0].State != WorkRunRunning || runs[1].State != WorkRunRunning || runs[2].State != WorkRunQueued || runs[2].QueueReason != "named_agent_capacity" {
		t.Fatalf("admission states = %#v", runs)
	}
	retry, err := runtime.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, NamedAgentID: owner.Agent.ID, Kind: WorkRunProducer, RequestID: "route-c"})
	if err != nil || retry.ID != runs[2].ID {
		t.Fatalf("idempotent start = %#v, %v", retry, err)
	}
	finished, err := runtime.FinishWorkRun(ctx, WorkRunFinishParams{
		WorkID: task.ID, RunID: runs[0].ID, State: WorkRunCompleted,
		RequestID: "finish-a", Outcome: "route complete", Qualified: true, CostUSD: 0.0125,
	})
	if err != nil || finished.State != WorkRunCompleted {
		t.Fatalf("FinishWorkRun() = %#v, %v", finished, err)
	}
	finishedRetry, err := runtime.FinishWorkRun(ctx, WorkRunFinishParams{
		WorkID: task.ID, RunID: runs[0].ID, State: WorkRunCompleted, RequestID: "finish-a",
	})
	if err != nil || finishedRetry.ID != finished.ID {
		t.Fatalf("idempotent finish = %#v, %v", finishedRetry, err)
	}
	work, err := runtime.GetWork(ctx, task.ID)
	if err != nil || findRunState(work.Runs, runs[2].ID) != WorkRunRunning || work.TotalCostUSD != 0.0125 {
		t.Fatalf("queued run was not admitted: %#v, %v", work.Runs, err)
	}
	capacity, err := service.NamedAgentCapacity(ctx, owner.Agent.ID)
	if err != nil || capacity.Active+capacity.Starting != 2 || capacity.Queued != 0 || capacity.Limit != 2 {
		t.Fatalf("capacity = %#v, %v", capacity, err)
	}
	check, err := runtime.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundTerminal := false
	for _, message := range check.Collaboration {
		foundTerminal = foundTerminal || message.Kind == CollaborationWorkRunTerminal && message.CorrelationID == runs[0].ID && message.TerminalState == CollaborationTerminalCompleted
	}
	if !foundTerminal {
		t.Fatalf("terminal event missing from room runtime: %#v", check.Collaboration)
	}
}

func TestRoomStartedNamedRunPersistsLaunchAndWakesTargetSession(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, _ := bindTestRoomLead(t, ctx, service, room.ID)
	task, _ := runtime.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Launch", OwnerID: owner.Agent.ID})
	sink.take() // Assignment wake.
	run, err := runtime.StartWorkRun(ctx, WorkRunStartParams{
		WorkID: task.ID, NamedAgentID: owner.Agent.ID, Kind: WorkRunProducer, RequestID: "launch-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	if wakes := sink.take(); len(wakes) != 0 {
		// The assignment already owns the principal's outstanding wake, so the
		// launch coalesces as pending instead of causing a second concurrent turn.
		t.Fatalf("coalesced launch unexpectedly delivered another wake: %v", wakes)
	}
	dispatches, err := service.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, dispatch := range dispatches {
		found = found || dispatch.TargetSessionRef == run.SessionRef && dispatch.CorrelationID == run.ID && dispatch.TargetKind == CollaborationTargetSession
	}
	if !found {
		t.Fatalf("named run launch dispatch missing: %#v", dispatches)
	}
}

func TestRunDeadlineExpiresAndReleasesCapacity(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	service.SetCollaborationRunLimits(1, 4, 4)
	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return base }
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, _ := bindTestRoomLead(t, ctx, service, room.ID)
	task, _ := runtime.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Deadline", OwnerID: owner.Agent.ID})
	run, err := runtime.StartWorkRun(ctx, WorkRunStartParams{
		WorkID: task.ID, NamedAgentID: owner.Agent.ID, Kind: WorkRunProducer,
		RequestID: "deadline-run", Deadline: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	base = base.Add(2 * time.Second)
	count, err := service.ExpireWorkRuns(ctx)
	if err != nil || count != 1 {
		t.Fatalf("ExpireWorkRuns() = %d, %v", count, err)
	}
	work, _ := runtime.GetWork(ctx, task.ID)
	if findRunState(work.Runs, run.ID) != WorkRunTimedOut {
		t.Fatalf("expired run = %#v", work.Runs)
	}
	capacity, _ := service.NamedAgentCapacity(ctx, owner.Agent.ID)
	if capacity.Active+capacity.Starting != 0 {
		t.Fatalf("expired run retained capacity: %#v", capacity)
	}
}

func TestWorkAndRoomTokenBudgetsStopAnotherWave(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	service.roomInputTokenLimit = 10
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, _ := bindTestRoomLead(t, ctx, service, room.ID)
	firstTask, _ := runtime.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "First", OwnerID: owner.Agent.ID})
	first, err := runtime.StartWorkRun(ctx, WorkRunStartParams{WorkID: firstTask.ID, NamedAgentID: owner.Agent.ID, Kind: WorkRunProducer, RequestID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.FinishWorkRun(ctx, WorkRunFinishParams{WorkID: firstTask.ID, RunID: first.ID, State: WorkRunCompleted, InputTokens: 10}); err != nil {
		t.Fatal(err)
	}
	secondTask, _ := runtime.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Second", OwnerID: owner.Agent.ID})
	if _, err := runtime.StartWorkRun(ctx, WorkRunStartParams{WorkID: secondTask.ID, NamedAgentID: owner.Agent.ID, Kind: WorkRunProducer, RequestID: "second"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("room budget start error = %v, want conflict", err)
	}
	service.roomInputTokenLimit = 0
	if _, err := runtime.UpdateWorkPolicy(ctx, WorkPolicyUpdateParams{WorkID: secondTask.ID, MaxVerifierAttempts: 3, MaxCandidates: 1, MaxRounds: 2, MaxInputTokens: 1}); err != nil {
		t.Fatal(err)
	}
	run, err := runtime.StartWorkRun(ctx, WorkRunStartParams{WorkID: secondTask.ID, NamedAgentID: owner.Agent.ID, Kind: WorkRunProducer, RequestID: "within-work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.FinishWorkRun(ctx, WorkRunFinishParams{WorkID: secondTask.ID, RunID: run.ID, State: WorkRunCompleted, InputTokens: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StartWorkRun(ctx, WorkRunStartParams{WorkID: secondTask.ID, NamedAgentID: owner.Agent.ID, Kind: WorkRunProducer, RequestID: "over-work"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("work budget start error = %v, want conflict", err)
	}
}

func TestMultiCandidatePromotionAndProducerBroadcast(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	runtime, _ := bindTestRoomLead(t, ctx, service, room.ID)
	task, _ := runtime.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Compare", OwnerID: owner.Agent.ID, VerificationRequired: true})
	if _, err := runtime.UpdateWorkPolicy(ctx, WorkPolicyUpdateParams{
		WorkID: task.ID, LeadNamedAgentID: runtime.AgentID(), MaxVerifierAttempts: 3,
		MaxCandidates: 2, MaxRounds: 2, FanoutReason: "compare independent risk strategies",
	}); err != nil {
		t.Fatal(err)
	}
	var runs []WorkRun
	var artifacts []WorkArtifact
	for _, id := range []string{"a", "b"} {
		run, err := runtime.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, NamedAgentID: owner.Agent.ID, Kind: WorkRunProducer, RequestID: "producer-" + id})
		if err != nil {
			t.Fatal(err)
		}
		artifact, err := runtime.AddWorkArtifact(ctx, WorkArtifactAddParams{WorkID: task.ID, RunID: run.ID, Kind: WorkArtifactCandidate, URI: "artifact://" + id})
		if err != nil {
			t.Fatal(err)
		}
		runs, artifacts = append(runs, run), append(artifacts, artifact)
	}
	broadcast, err := runtime.SendCollaboration(ctx, CollaborationSendParams{
		RoomID: room.ID, ToAgentID: owner.Agent.ID, TargetKind: CollaborationTargetNamedAgent,
		Visibility: CollaborationVisibilityWorkPrivate, WorkID: task.ID, Kind: CollaborationControl,
		Body: "Apply the new compatibility constraint.", RequestID: "constraint-1", CorrelationID: task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if broadcast.TargetKind != CollaborationTargetSession {
		t.Fatalf("broadcast return target = %#v", broadcast)
	}
	work, _ := runtime.GetWork(ctx, task.ID)
	targets := map[string]bool{}
	for _, message := range work.Deliveries {
		if message.RequestID == "constraint-1" {
			targets[message.TargetSessionRef] = true
		}
	}
	if len(targets) != 2 || !targets[runs[0].SessionRef] || !targets[runs[1].SessionRef] {
		t.Fatalf("producer broadcast targets = %#v", targets)
	}
	if _, err := runtime.PromoteWorkCandidate(ctx, WorkCandidatePromoteParams{
		WorkID: task.ID, RunID: runs[0].ID, ArtifactRef: artifacts[0].ID,
		RequestID: "producer-promote", SelectionReason: "producer chose itself",
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("producer multi-candidate promotion error = %v", err)
	}
	for index, run := range runs {
		if _, err := runtime.FinishWorkRun(ctx, WorkRunFinishParams{WorkID: task.ID, RunID: run.ID, State: WorkRunCompleted, RequestID: "finish-producer-" + string(rune('a'+index)), Qualified: true}); err != nil {
			t.Fatal(err)
		}
	}
	selector, err := runtime.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, NamedAgentID: owner.Agent.ID, Kind: WorkRunSelector, RequestID: "selector"})
	if err != nil {
		t.Fatal(err)
	}
	lagging, err := runtime.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, NamedAgentID: owner.Agent.ID, Kind: WorkRunProducer, RequestID: "lagging-producer"})
	if err != nil {
		t.Fatal(err)
	}
	promoted, err := runtime.PromoteWorkCandidate(ctx, WorkCandidatePromoteParams{
		WorkID: task.ID, RunID: selector.ID, ArtifactRef: artifacts[1].ID,
		RequestID: "promotion-1", SelectionReason: "candidate b covers the compatibility risk",
	})
	if err != nil {
		t.Fatal(err)
	}
	settledLagging, err := runtime.FinishWorkRun(ctx, WorkRunFinishParams{WorkID: task.ID, RunID: lagging.ID, State: WorkRunCompleted, Qualified: true})
	if err != nil || settledLagging.Qualified || settledLagging.Outcome != "candidate revision superseded" {
		t.Fatalf("superseded lagging run = %#v, %v", settledLagging, err)
	}
	retry, err := runtime.PromoteWorkCandidate(ctx, WorkCandidatePromoteParams{
		WorkID: task.ID, RunID: selector.ID, ArtifactRef: artifacts[1].ID,
		RequestID: "promotion-1", SelectionReason: "candidate b covers the compatibility risk",
	})
	if err != nil || retry.CandidateRevision != promoted.CandidateRevision || promoted.CandidateArtifactRef != artifacts[1].ID {
		t.Fatalf("idempotent promotion = %#v / %#v, %v", promoted, retry, err)
	}
	if _, err := runtime.PromoteWorkCandidate(ctx, WorkCandidatePromoteParams{
		WorkID: task.ID, RunID: selector.ID, ArtifactRef: artifacts[1].ID,
		RequestID: "promotion-2", SelectionReason: "duplicate",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second promotion error = %v", err)
	}
}
