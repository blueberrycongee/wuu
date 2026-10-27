package session

import (
	"errors"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/participant"
)

func TestParticipantCRUD(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		p := participant.Participant{
			ID: participant.NewID(), Kind: participant.KindEphemeral,
			Name: "Reviewer·auth", Role: "reviewer", Avatar: "🧐",
		}
		if err := store.UpsertParticipant(p); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetParticipant(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != p.Name || got.Kind != p.Kind || got.Role != p.Role {
			t.Errorf("round-trip mismatch: %+v", got)
		}
		list, err := store.ListParticipants(participant.KindEphemeral)
		if err != nil || len(list) != 1 {
			t.Fatalf("list = %v, %v", list, err)
		}
	})
}

func TestCompleteParticipantRunUpsertsMonotonicTerminalState(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		participantID := participant.NewID()
		if err := store.UpsertParticipant(participant.Participant{
			ID: participantID, Kind: participant.KindNamed, Name: "Run owner",
		}); err != nil {
			t.Fatal(err)
		}

		const agentID = "agt-workflow-dispatched"
		// Workflow-dispatched named agents do not necessarily pass through the
		// participant/start handler that records the initial running row. Terminal
		// completion must still create an auditable durable run.
		if err := store.CompleteParticipantRun(participantID, agentID, "completed", "done"); err != nil {
			t.Fatal(err)
		}
		// A participant/start write can race behind an extremely fast worker. It may
		// fill in task/session metadata, but it must never downgrade the terminal row.
		if err := store.UpsertParticipantRun(ParticipantRun{
			ID: agentID, ParticipantID: participantID, AgentID: agentID,
			TaskID: "task-late", SessionID: "session-late",
			Outcome: "running", Summary: "working",
		}); err != nil {
			t.Fatal(err)
		}
		// Replaying the same terminal write is safe. App-server finalization may
		// retry after losing the response to a committed SQLite transaction.
		if err := store.CompleteParticipantRun(participantID, agentID, "completed", "done"); err != nil {
			t.Fatalf("replay terminal completion: %v", err)
		}
		runs, err := store.ListParticipantRuns(participantID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) != 1 || runs[0].ID != agentID || runs[0].Outcome != "completed" || runs[0].Summary != "done" ||
			runs[0].TaskID != "task-late" || runs[0].SessionID != "session-late" {
			t.Fatalf("completed participant run = %+v", runs)
		}

		// Existing stores may use an ID distinct from agent_id. Completion updates
		// that row in place instead of assuming the newer ID convention.
		if err := store.UpsertParticipantRun(ParticipantRun{
			ID: "run-custom-id", ParticipantID: participantID, AgentID: "agt-custom-id",
			Outcome: "running", Summary: "custom running",
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteParticipantRun(participantID, "agt-custom-id", "failed", "custom failed"); err != nil {
			t.Fatal(err)
		}
		runs, err = store.ListParticipantRuns(participantID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) != 2 {
			t.Fatalf("participant runs = %+v, want 2", runs)
		}
		var custom ParticipantRun
		for _, run := range runs {
			if run.AgentID == "agt-custom-id" {
				custom = run
			}
		}
		if custom.ID != "run-custom-id" || custom.Outcome != "failed" || custom.Summary != "custom failed" {
			t.Fatalf("custom-id participant run = %+v", custom)
		}
	})
}

func TestNamedParticipantUniqueName(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		a := participant.Participant{ID: participant.NewID(), Kind: participant.KindNamed, Name: "Noel", Role: "reviewer"}
		b := participant.Participant{ID: participant.NewID(), Kind: participant.KindNamed, Name: "Noel", Role: "qa"}
		if err := store.UpsertParticipant(a); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertParticipant(b); err == nil {
			t.Fatal("expected unique-name violation for active named participants")
		}
	})
}

func TestRetireParticipant(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		p := participant.Participant{
			ID: participant.NewID(), Kind: participant.KindEphemeral,
			Name: "Reviewer·auth", Role: "reviewer",
		}
		if err := store.UpsertParticipant(p); err != nil {
			t.Fatal(err)
		}
		if err := store.RetireParticipant(p.ID); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetParticipant(p.ID)
		if err != nil {
			t.Fatalf("retired participant should still be readable by ID: %v", err)
		}
		if got.RetiredAt == nil {
			t.Error("RetiredAt = nil, want non-nil after retire")
		}
		list, err := store.ListParticipants("")
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 0 {
			t.Errorf("ListParticipants after retire = %v, want empty", list)
		}
	})
}

// TestRetireParticipantIdempotent asserts a second retire keeps the original
// retired_at stamp (COALESCE) and succeeds, so cleanup callers can re-run
// the protocol after a partial failure.
func TestRetireParticipantIdempotent(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		p := participant.Participant{
			ID: participant.NewID(), Kind: participant.KindNamed, Name: "Ivy",
		}
		if err := store.UpsertParticipant(p); err != nil {
			t.Fatal(err)
		}
		if err := store.RetireParticipant(p.ID); err != nil {
			t.Fatal(err)
		}
		first, err := store.GetParticipant(p.ID)
		if err != nil || first.RetiredAt == nil {
			t.Fatalf("first retire: %+v, %v", first, err)
		}
		time.Sleep(5 * time.Millisecond)
		if err := store.RetireParticipant(p.ID); err != nil {
			t.Fatalf("second retire should be idempotent, got %v", err)
		}
		second, err := store.GetParticipant(p.ID)
		if err != nil || second.RetiredAt == nil {
			t.Fatalf("second retire: %+v, %v", second, err)
		}
		if !second.RetiredAt.Equal(*first.RetiredAt) {
			t.Errorf("retired_at changed on re-retire: %v -> %v", first.RetiredAt, second.RetiredAt)
		}
	})
}

func TestRetireParticipantNotFound(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		err := store.RetireParticipant("prt-doesnotexist0000")
		if !errors.Is(err, ErrParticipantNotFound) {
			t.Errorf("RetireParticipant unknown ID = %v, want ErrParticipantNotFound", err)
		}
	})
}

func TestRetiredNamedParticipantFreesName(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		a := participant.Participant{ID: participant.NewID(), Kind: participant.KindNamed, Name: "Noel", Role: "reviewer"}
		if err := store.UpsertParticipant(a); err != nil {
			t.Fatal(err)
		}
		if err := store.RetireParticipant(a.ID); err != nil {
			t.Fatal(err)
		}
		b := participant.Participant{ID: participant.NewID(), Kind: participant.KindNamed, Name: "Noel", Role: "qa"}
		if err := store.UpsertParticipant(b); err != nil {
			t.Errorf("upsert named participant with retired name = %v, want nil", err)
		}
	})
}

func TestFindRetiredParticipantByName(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		if _, ok, err := store.FindRetiredParticipantByName(participant.KindNamed, "Noel"); err != nil || ok {
			t.Fatalf("empty store: ok=%v err=%v, want no match", ok, err)
		}

		first := participant.Participant{ID: participant.NewID(), Kind: participant.KindNamed, Name: "Noel", Role: "reviewer"}
		if err := store.UpsertParticipant(first); err != nil {
			t.Fatal(err)
		}
		// Active rows never match: the guard is about ARCHIVED predecessors.
		if _, ok, err := store.FindRetiredParticipantByName(participant.KindNamed, "Noel"); err != nil || ok {
			t.Fatalf("active row matched: ok=%v err=%v", ok, err)
		}
		if err := store.RetireParticipant(first.ID); err != nil {
			t.Fatal(err)
		}
		got, ok, err := store.FindRetiredParticipantByName(participant.KindNamed, "noel")
		if err != nil || !ok {
			t.Fatalf("case-insensitive match failed: ok=%v err=%v", ok, err)
		}
		if got.ID != first.ID {
			t.Errorf("predecessor ID = %q, want %q", got.ID, first.ID)
		}

		// A second same-name generation retires later; the most recent
		// retirement must win.
		time.Sleep(5 * time.Millisecond)
		second := participant.Participant{ID: participant.NewID(), Kind: participant.KindNamed, Name: "Noel", Role: "qa"}
		if err := store.UpsertParticipant(second); err != nil {
			t.Fatal(err)
		}
		if err := store.RetireParticipant(second.ID); err != nil {
			t.Fatal(err)
		}
		got, ok, err = store.FindRetiredParticipantByName(participant.KindNamed, "Noel")
		if err != nil || !ok {
			t.Fatalf("two-generation match failed: ok=%v err=%v", ok, err)
		}
		if got.ID != second.ID {
			t.Errorf("most recent retiree = %q, want %q", got.ID, second.ID)
		}

		if _, ok, _ := store.FindRetiredParticipantByName(participant.KindNamed, "Unknown"); ok {
			t.Error("unknown name should not match")
		}
		if _, _, err := store.FindRetiredParticipantByName(participant.KindNamed, "  "); err == nil {
			t.Error("blank name should be rejected")
		}
	})
}

func TestCountParticipantsByKindIncludesRetired(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		if got, err := store.CountParticipantsByKind(participant.KindNamed); err != nil || got != 0 {
			t.Fatalf("empty dir: count=%d err=%v", got, err)
		}
		a := participant.Participant{ID: participant.NewID(), Kind: participant.KindNamed, Name: "Andy", Role: "general-purpose"}
		if err := store.UpsertParticipant(a); err != nil {
			t.Fatal(err)
		}
		if got, err := store.CountParticipantsByKind(participant.KindNamed); err != nil || got != 1 {
			t.Fatalf("after insert: count=%d err=%v", got, err)
		}
		if err := store.RetireParticipant(a.ID); err != nil {
			t.Fatal(err)
		}
		if got, err := store.CountParticipantsByKind(participant.KindNamed); err != nil || got != 1 {
			t.Errorf("retired row must still be counted: count=%d err=%v", got, err)
		}
		b := participant.Participant{ID: participant.NewID(), Kind: participant.KindEphemeral, Name: "Worker·task"}
		if err := store.UpsertParticipant(b); err != nil {
			t.Fatal(err)
		}
		if got, err := store.CountParticipantsByKind(participant.KindNamed); err != nil || got != 1 {
			t.Errorf("ephemeral insert should not change named count: count=%d err=%v", got, err)
		}
		if got, err := store.CountParticipantsByKind(participant.KindEphemeral); err != nil || got != 1 {
			t.Errorf("ephemeral count: count=%d err=%v", got, err)
		}
		if _, err := store.CountParticipantsByKind(""); err == nil {
			t.Error("empty kind should be rejected")
		}
	})
}
