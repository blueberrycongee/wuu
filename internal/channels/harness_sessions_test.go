package channels

import (
	"context"
	"errors"
	"testing"
)

func TestHarnessOutboxReplaysStableOperationsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	actor := HarnessSessionActor{AgentID: "agent", SessionRef: "source", TurnID: "turn", RoomID: "room"}
	p := HarnessSessionParams{Action: "create", Prompt: "Inspect docs", OperationID: "call"}
	first, created, err := s.ReserveHarnessOperation(ctx, actor, p, "reserved", 0)
	if err != nil || !created {
		t.Fatalf("reserve: %+v %v", first, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	replay, created, err := s.ReserveHarnessOperation(ctx, actor, p, "another-id", 0)
	if err != nil || created || replay.ID != first.ID || replay.Params.SessionID != "reserved" {
		t.Fatalf("replay duplicated execution: %+v %v", replay, err)
	}
	p.Prompt = "Different goal"
	if _, _, err := s.ReserveHarnessOperation(ctx, actor, p, "another-id", 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting retry accepted: %v", err)
	}
	pending, err := s.PendingHarnessOperations(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending outbox: %+v %v", pending, err)
	}
}

func TestHarnessAdmissionReservesManagerCapacityAndSharesUsage(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	agent := createTestAgent(t, s, "Manager")
	room := createTestRoom(t, s, agent)
	s.agentRunLimit, s.roomRunLimit, s.globalRunLimit = 3, 3, 3
	link := HarnessSessionLink{SessionID: "first", AgentID: agent.Agent.ID, RoomID: room.ID}
	if err := s.ReserveHarnessExecution(ctx, link); err != nil {
		t.Fatal(err)
	}
	second := link
	second.SessionID = "second"
	if err := s.ReserveHarnessExecution(ctx, second); err != nil {
		t.Fatal(err)
	}
	third := link
	third.SessionID = "third"
	if err := s.ReserveHarnessExecution(ctx, third); !errors.Is(err, ErrHarnessCapacity) {
		t.Fatalf("executors consumed the manager slot: %v", err)
	}
	if err := s.ReleaseHarnessExecution(ctx, link.SessionID); err != nil {
		t.Fatal(err)
	}
	s.roomInputTokenLimit = 10
	for range 2 {
		if err := s.RecordHarnessUsage(ctx, link, "turn", 10, 5); err != nil {
			t.Fatal(err)
		}
	}
	input, output, err := collaborationTokenUsage(ctx, s.db, room.ID, "")
	if err != nil || input != 10 || output != 5 {
		t.Fatalf("usage duplicate: %d %d %v", input, output, err)
	}
	if err := s.ReserveHarnessExecution(ctx, third); !errors.Is(err, ErrConflict) {
		t.Fatalf("executor bypassed room budget: %v", err)
	}
}

func TestHarnessStaleRecoveryCannotRestoreReleasedManagement(t *testing.T) {
	s := openTestService(t, nil)
	ctx := context.Background()
	original := HarnessSessionLink{SessionID: "session", AgentID: "agent", RoomID: "room", Active: true, ControlRevision: 1}
	if err := s.PutHarnessLink(ctx, original); err != nil {
		t.Fatal(err)
	}
	released := original
	released.Active = false
	released.ControlRevision = 2
	if err := s.PutHarnessLink(ctx, released); err != nil {
		t.Fatal(err)
	}
	original.LastTurnID = "late-turn"
	if err := s.PutHarnessLink(ctx, original); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale recovery restored management: %v", err)
	}
	actual, err := s.HarnessLink(ctx, "session")
	if err != nil || actual.Active || actual.ControlRevision != 2 {
		t.Fatalf("released management changed: %+v %v", actual, err)
	}
}

func TestHarnessExecutionSerializesSharedWriters(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	agent := createTestAgent(t, s, "Manager")
	room := createTestRoom(t, s, agent)
	s.agentRunLimit, s.roomRunLimit, s.globalRunLimit = 4, 4, 4
	first := HarnessSessionLink{SessionID: "writer-one", AgentID: agent.Agent.ID, RoomID: room.ID, Active: true, ExecutionRoot: "/project"}
	second := first
	second.SessionID = "writer-two"
	for _, link := range []HarnessSessionLink{first, second} {
		if err := s.PutHarnessLink(ctx, link); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ReserveHarnessExecution(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveHarnessExecution(ctx, second); !errors.Is(err, ErrHarnessCapacity) {
		t.Fatalf("shared writers overlapped: %v", err)
	}
	second.ExecutionRoot = "/isolated-project"
	if err := s.PutHarnessLink(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveHarnessExecution(ctx, second); err != nil {
		t.Fatalf("isolated writer blocked: %v", err)
	}
}

func TestHarnessParallelCandidateDoesNotLoseItsArtifact(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	owner := createTestAgent(t, s, "Owner")
	room := createTestRoom(t, s, owner)
	client, err := s.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := client.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, OwnerID: owner.Agent.ID, Title: "Two routes"})
	if err != nil {
		t.Fatal(err)
	}
	runs := []WorkRun{}
	for _, id := range []string{"first-route", "second-route"} {
		if err := s.PutHarnessLink(ctx, HarnessSessionLink{SessionID: id, AgentID: owner.Agent.ID, RoomID: room.ID, WorkID: task.ID, GoalRevision: 1, Active: true, Purpose: CollaborationSessionWork}); err != nil {
			t.Fatal(err)
		}
		run, err := s.StartHarnessWorkRun(ctx, id, id)
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
	}
	for i, run := range runs {
		artifact, err := client.AddWorkArtifact(ctx, WorkArtifactAddParams{WorkID: task.ID, RunID: run.ID, Kind: WorkArtifactCandidate, URI: "artifact://" + run.ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.FinishHarnessWorkRun(ctx, run.SessionRef, "turn", WorkRunFinishParams{RunID: run.ID, State: WorkRunCompleted, Qualified: true}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err := client.PromoteWorkCandidate(ctx, WorkCandidatePromoteParams{WorkID: task.ID, RunID: run.ID, ArtifactRef: artifact.ID, RequestID: "promote"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	work, err := s.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(work.Artifacts) != 2 || work.CandidateRevision != 1 || !work.Runs[1].Qualified {
		t.Fatalf("lost parallel result: %#v", work)
	}
}

func TestDiscardCandidateStopsItsVerification(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	owner := createTestAgent(t, s, "Owner")
	room := createTestRoom(t, s, owner)
	client, err := s.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := client.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, OwnerID: owner.Agent.ID, Title: "Review", VerificationRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		id      string
		purpose CollaborationSessionPurpose
	}{{"producer", CollaborationSessionWork}, {"verifier", CollaborationSessionVerification}} {
		if err := s.PutHarnessLink(ctx, HarnessSessionLink{SessionID: entry.id, AgentID: owner.Agent.ID, RoomID: room.ID, WorkID: task.ID, GoalRevision: 1, Active: true, Purpose: entry.purpose}); err != nil {
			t.Fatal(err)
		}
		run, err := s.StartHarnessWorkRun(ctx, entry.id, entry.id)
		if err != nil {
			t.Fatal(err)
		}
		if entry.id == "producer" {
			artifact, err := client.AddWorkArtifact(ctx, WorkArtifactAddParams{WorkID: task.ID, RunID: run.ID, Kind: WorkArtifactCandidate, URI: "artifact://result"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.FinishHarnessWorkRun(ctx, entry.id, "turn", WorkRunFinishParams{RunID: run.ID, State: WorkRunCompleted, Qualified: true}); err != nil {
				t.Fatal(err)
			}
			if _, err = client.PromoteWorkCandidate(ctx, WorkCandidatePromoteParams{WorkID: task.ID, RunID: run.ID, ArtifactRef: artifact.ID, RequestID: "promote"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	work, err := s.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCandidateDisposition(ctx, work.ID, work.CandidateArtifactRef, "discarded", work.Revision, nil); err != nil {
		t.Fatal(err)
	}
	work, err = s.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if work.CandidateArtifactRef != "" || work.State != WorkNeedsHuman {
		t.Fatalf("discard: %#v", work)
	}
	for _, run := range work.Runs {
		if run.Kind == WorkRunVerifier && run.State != WorkRunInterrupted {
			t.Fatalf("review still active: %#v", run)
		}
	}
	if err := client.RecordHarnessVerification(ctx, "verifier", HarnessVerificationReport{Decision: VerificationPass, Report: "Late review"}); err == nil {
		t.Fatal("discarded candidate accepted a late verdict")
	}
}
