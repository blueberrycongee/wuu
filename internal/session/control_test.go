package session

import (
	"errors"
	"testing"
)

func TestControlFencesTakeoverAndDoesNotTransferOwnership(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "existing", "/project"); err != nil {
		t.Fatal(err)
	}
	c, err := ChangeControl(dir, "existing", "manager-a", ControlActive, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeControl(dir, "existing", "manager-b", ControlActive, c.Revision); !errors.Is(err, ErrControlChanged) {
		t.Fatalf("competing manager: %v", err)
	}
	paused, err := ChangeControl(dir, "existing", "manager-a", ControlTakenOver, c.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateControl(dir, c); !errors.Is(err, ErrControlChanged) {
		t.Fatalf("stale command accepted: %v", err)
	}
	if _, err := ChangeControl(dir, "existing", "manager-a", ControlActive, c.Revision); !errors.Is(err, ErrControlChanged) {
		t.Fatalf("stale resume accepted: %v", err)
	}
	released, err := ChangeControl(dir, "existing", "manager-a", ControlReleased, paused.Revision)
	if err != nil {
		t.Fatal(err)
	}
	adopted, err := ChangeControl(dir, "existing", "manager-b", ControlActive, released.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateControl(dir, adopted); err != nil {
		t.Fatal(err)
	}
	metadata, ok, err := Find(dir, "existing")
	if err != nil || !ok {
		t.Fatalf("find: %v", err)
	}
	if metadata.Owner != "" || metadata.ParentID != "" || metadata.CWD != "/project" || metadata.ArchivedAt != nil {
		t.Fatalf("management changed resources: %+v", metadata)
	}
}

func TestControlledInputIsNotPersistedAfterTakeover(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "session", "/project"); err != nil {
		t.Fatal(err)
	}
	control, err := ChangeControl(dir, "session", "manager", ControlActive, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AppendControlledHistoryRecord(dir, "session", HistoryRecord{Role: "user", Content: "accepted"}, &control); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeControl(dir, "session", "manager", ControlTakenOver, control.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendControlledHistoryRecord(dir, "session", HistoryRecord{Role: "user", Content: "stale"}, &control); !errors.Is(err, ErrControlChanged) {
		t.Fatalf("stale append: %v", err)
	}
	records, err := LoadHistoryRecords(dir, "session", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Content != "accepted" {
		t.Fatalf("history changed: %+v", records)
	}
}

// Continuations retain the original baseline through admission. Adoption alone
// is allowed; no later stop/release/readoption may authorize the old work.
func TestContinuationControlFencesAdoptionAtAppend(t *testing.T) {
	for _, state := range []string{"", ControlReleased, ControlTakenOver, ControlPaused, ControlActive} {
		t.Run("baseline_"+state, func(t *testing.T) {
			dir := t.TempDir()
			const id = "continued"
			if _, err := CreateWithMetadata(dir, id, "/project"); err != nil {
				t.Fatal(err)
			}
			baseline := Control{SessionID: id}
			if state != "" {
				var err error
				baseline, err = ChangeControl(dir, id, "original", state, 0)
				if err != nil {
					t.Fatal(err)
				}
			}
			appendInput := func(content string) error {
				_, err := AppendContinuationHistoryRecord(dir, id, HistoryRecord{Role: "user", Content: content}, nil, baseline)
				return err
			}
			if err := appendInput("unchanged"); err != nil {
				t.Fatal(err)
			}
			manager := "original"
			if state == "" || state == ControlReleased {
				manager = "adopter"
			}
			current, err := ChangeControl(dir, id, manager, ControlActive, baseline.Revision)
			if err != nil {
				t.Fatal(err)
			}
			allowed := state == "" || state == ControlReleased
			err = appendInput("after_change")
			if allowed && err != nil {
				t.Fatalf("adoption refused: %v", err)
			}
			if !allowed && !errors.Is(err, ErrControlChanged) {
				t.Fatalf("resume admitted old continuation: %v", err)
			}
			// The append performs its own check, including a stop after preflight.
			if allowed {
				if err := ValidateContinuationControl(dir, baseline); err != nil {
					t.Fatal(err)
				}
			}
			current, err = ChangeControl(dir, id, manager, ControlActive, current.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if err := appendInput("stopped"); !errors.Is(err, ErrControlChanged) {
				t.Fatalf("stop not fenced: %v", err)
			}
			current, err = ChangeControl(dir, id, manager, ControlReleased, current.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ChangeControl(dir, id, "new-adopter", ControlActive, current.Revision); err != nil {
				t.Fatal(err)
			}
			if err := appendInput("readopted"); !errors.Is(err, ErrControlChanged) {
				t.Fatalf("readoption revived old work: %v", err)
			}
			records, err := LoadHistoryRecords(dir, id, false)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if allowed {
				want = 2
			}
			if len(records) != want {
				t.Fatalf("fenced history changed: %+v", records)
			}
		})
	}
}
