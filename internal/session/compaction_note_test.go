package session

import "testing"

func TestCompareAndSwapCompactionNoteRejectsStaleWriter(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		if _, err := store.CreateWithMetadata("note-cas", t.TempDir()); err != nil {
			t.Fatalf("create session: %v", err)
		}
		first := CompactionNote{
			SessionID: "note-cas", ProviderKey: "provider", Markdown: "first",
			CoveredMessages: 2, CoveredHash: "hash-first",
		}
		stored, err := store.CompareAndSwapCompactionNote(CompactionNote{}, false, first)
		if err != nil || !stored {
			t.Fatalf("insert note: stored=%v err=%v", stored, err)
		}
		if stored, err := store.CompareAndSwapCompactionNote(CompactionNote{}, false, first); err != nil || stored {
			t.Fatalf("duplicate absent write: stored=%v err=%v", stored, err)
		}

		second := CompactionNote{
			SessionID: "note-cas", ProviderKey: "provider", Markdown: "second",
			CoveredMessages: 4, CoveredHash: "hash-second",
		}
		stored, err = store.CompareAndSwapCompactionNote(first, true, second)
		if err != nil || !stored {
			t.Fatalf("replace current note: stored=%v err=%v", stored, err)
		}
		stale := CompactionNote{
			SessionID: "note-cas", ProviderKey: "provider", Markdown: "stale",
			CoveredMessages: 3, CoveredHash: "hash-stale",
		}
		if stored, err := store.CompareAndSwapCompactionNote(first, true, stale); err != nil || stored {
			t.Fatalf("stale replacement: stored=%v err=%v", stored, err)
		}
		got, ok, err := store.LoadCompactionNote("note-cas", "provider")
		if err != nil || !ok || got.Markdown != "second" || got.CoveredHash != "hash-second" {
			t.Fatalf("active note = %+v ok=%v err=%v", got, ok, err)
		}
	})
}
