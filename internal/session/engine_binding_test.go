package session

import "testing"

func TestSetEngineRoundTrip(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		sess, err := store.CreateWithMetadata("engine-binding", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if sess.EngineID != "" {
			t.Fatalf("fresh session engine_id = %q, want empty (legacy default reads as wuu)", sess.EngineID)
		}
		updated, err := store.SetEngine(sess.ID, "wuu")
		if err != nil {
			t.Fatalf("SetEngine: %v", err)
		}
		if updated.EngineID != "wuu" {
			t.Fatalf("SetEngine persisted %q, want wuu", updated.EngineID)
		}
		loaded, ok, err := store.Find(sess.ID)
		if err != nil || !ok {
			t.Fatalf("Find: ok=%v err=%v", ok, err)
		}
		if loaded.EngineID != "wuu" {
			t.Fatalf("loaded engine_id = %q, want wuu", loaded.EngineID)
		}
		if _, err := store.SetEngine(sess.ID, "  "); err == nil {
			t.Fatal("SetEngine with an empty id must fail")
		}
	})
}
