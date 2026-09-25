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
	if err := ArchiveControlled(dir, c, "agent_deleted"); !errors.Is(err, ErrControlChanged) {
		t.Fatalf("stale cleanup archived a taken-over session: %v", err)
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

func TestArchiveControlledCanBeRestored(t *testing.T) {
	for _, state := range []string{ControlActive, ControlPaused, ControlTakenOver, ControlReleased} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := CreateWithMetadata(dir, "session", "/project"); err != nil {
				t.Fatal(err)
			}
			control, err := ChangeControl(dir, "session", "manager", state, 0)
			if err != nil {
				t.Fatal(err)
			}
			err = ArchiveControlled(dir, control, "agent_deleted")
			if state == ControlTakenOver || state == ControlReleased {
				if !errors.Is(err, ErrControlChanged) {
					t.Fatalf("cleanup accepted %s: %v", state, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			metadata, found, err := Find(dir, "session")
			if err != nil || !found || metadata.ArchivedAt == nil || metadata.ArchiveReason != "agent_deleted" || metadata.CWD != "/project" {
				t.Fatalf("archive not persisted: %+v %v", metadata, err)
			}
			if err := ValidateControl(dir, control); !errors.Is(err, ErrControlChanged) {
				t.Fatalf("archived session still accepts automatic input: %v", err)
			}
			metadata, err = UpdateArchived(dir, "session", false)
			if err != nil || metadata.ArchivedAt != nil || metadata.ArchiveReason != "" {
				t.Fatalf("restore failed: %+v %v", metadata, err)
			}
			if err := ArchiveControlled(dir, control, "agent_deleted"); !errors.Is(err, ErrControlChanged) {
				t.Fatalf("cleanup re-archived restored history: %v", err)
			}
		})
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
