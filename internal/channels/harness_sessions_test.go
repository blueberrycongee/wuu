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
