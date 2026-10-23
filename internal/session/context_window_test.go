package session

import (
	"reflect"
	"testing"
)

func TestContextWindowRestartAndLateFork(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		const id = "context-restart"
		if _, err := store.CreateWithMetadata(id, "/project"); err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"task", "completed tool result", "concurrent steering"} {
			if err := store.AppendHistoryRecord(id, HistoryRecord{Role: "user", Content: text}); err != nil {
				t.Fatal(err)
			}
		}
		note := CompactionNote{SessionID: id, ProviderKey: "notes", Markdown: "task progress", CoveredMessages: 1, CoveredHash: "new-anchor"}
		checkpoint, err := store.StoreContextWindow(id, []HistoryRecord{{Role: "system", Content: "bounded continuation"}}, 2, note)
		if err != nil {
			t.Fatal(err)
		}
		// Recovery must not depend on runner memory.
		restartSessionStore(t, store)
		snapshot, err := store.LoadProviderHistorySnapshot(id)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(snapshot.Records, checkpoint.Replacement) || len(snapshot.Records) != 2 || snapshot.Records[1].Content != "concurrent steering" {
			t.Fatalf("recovered checkpoint lost replacement or concurrent tail: %+v", snapshot)
		}
		stored, ok, err := store.LoadCompactionNote(id, "notes")
		if err != nil || !ok || stored.CoveredHash != note.CoveredHash {
			t.Fatalf("note not committed: %+v %v", stored, err)
		}
		if _, err := store.StoreContextWindow(id, checkpoint.Replacement, checkpoint.ThroughSeq+100, CompactionNote{ProviderKey: "notes"}); err == nil {
			t.Fatal("invalid baseline accepted")
		}
		after, _, err := store.LoadCompactionNote(id, "notes")
		if err != nil || after.CoveredHash != stored.CoveredHash {
			t.Fatal("failed transition changed note")
		}
		if _, err := store.StoreContextWindow(id, checkpoint.Replacement, checkpoint.ThroughSeq, CompactionNote{ProviderKey: "notes"}); err != nil {
			t.Fatal(err)
		}
		if swapped, err := store.CompareAndSwapCompactionNote(stored, true, note); err != nil || swapped {
			t.Fatalf("late fork replaced tombstone: %v %v", swapped, err)
		}
		tombstone, _, err := store.LoadCompactionNote(id, "notes")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.StoreContextWindow(id, checkpoint.Replacement, checkpoint.ThroughSeq, CompactionNote{ProviderKey: "notes"}); err != nil {
			t.Fatal(err)
		}
		if swapped, err := store.CompareAndSwapCompactionNote(tombstone, true, note); err != nil || swapped {
			t.Fatalf("late missing-note fork survived another reset: %v %v", swapped, err)
		}
		raw, err := store.LoadHistoryRecords(id, true)
		if err != nil || len(raw) != 4 || raw[1].Content != "completed tool result" {
			t.Fatalf("original facts lost: %+v %v", raw, err)
		}
	})
}
