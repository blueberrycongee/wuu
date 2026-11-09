package sidethread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func selectedDocuments(t *testing.T, backend string) *persistence.Store {
	t.Helper()
	docs, err := persistence.Open(context.Background(), t.TempDir(), backend)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := docs.Close(); err != nil {
			t.Error(err)
		}
	})
	return docs
}

func writeLegacySide(t *testing.T, dir, key string, staged bool) []byte {
	t.Helper()
	if staged {
		dir = filepath.Join(dir, ".deleting")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	st := sampleSideThread(key)
	st.Revision = 41
	st.Messages[0].Selection = &SelectionReference{Type: "response"}
	st.Messages[0].Items = []Item{{ID: "tool", Type: "tool", Result: "durable detail"}}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw[:len(raw)-1], []byte(`,"future_field":{"preserved":true}}`)...)
	if err := os.WriteFile(filepath.Join(dir, key+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDocumentsLegacyImportSurvivesPortableCopyAndDoesNotResurrect(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			docs := selectedDocuments(t, backend)
			store := NewStore(ctx, docs)
			legacy := t.TempDir()
			original := writeLegacySide(t, legacy, "main", false)
			staged := writeLegacySide(t, legacy, "pending", true)
			if err := store.ImportLegacy(legacy, false); err == nil {
				t.Fatal("live peer admitted unrecorded legacy import")
			}
			if err := store.ImportLegacy(legacy, true); err != nil {
				t.Fatal(err)
			}
			if err := store.ImportLegacy(legacy, false); err != nil {
				t.Fatalf("recorded import: %v", err)
			}
			loaded, err := store.Load("main")
			if err != nil {
				t.Fatal(err)
			}
			var want SideThread
			if err := json.Unmarshal(original, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*loaded, want) {
				t.Fatalf("import changed history: %+v", loaded)
			}
			snapshot, err := docs.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for key, raw := range map[string][]byte{"main": original, "pending": staged} {
				found := false
				for _, doc := range snapshot.Documents {
					if doc.Key == key && bytes.Contains(doc.Value, []byte(`"future_field"`)) {
						var gotJSON, wantJSON any
						if err := json.Unmarshal(doc.Value, &gotJSON); err != nil {
							t.Fatal(err)
						}
						if err := json.Unmarshal(raw, &wantJSON); err != nil {
							t.Fatal(err)
						}
						found = reflect.DeepEqual(gotJSON, wantJSON)
					}
				}
				if !found {
					t.Fatalf("portable snapshot lost raw side history %s", key)
				}
			}
			copied := selectedDocuments(t, map[string]string{"sqlite": "jsonl", "jsonl": "sqlite"}[backend])
			if _, err := copied.Import(ctx, snapshot); err != nil {
				t.Fatal(err)
			}
			migrated := NewStore(ctx, copied)
			if got, err := migrated.Load("main"); err != nil || !reflect.DeepEqual(*got, want) {
				t.Fatalf("cross-home copy: %+v %v", got, err)
			}
			stage, err := migrated.BeginDelete("pending")
			if err != nil {
				t.Fatal(err)
			}
			if err := stage.Rollback(); err != nil {
				t.Fatal(err)
			}
			if got, err := migrated.Load("pending"); err != nil || got.Revision != 41 {
				t.Fatalf("staged recovery: %+v %v", got, err)
			}
			if err := migrated.Delete("main"); err != nil {
				t.Fatal(err)
			}
			if err := migrated.ImportLegacy(legacy, true); err != nil {
				t.Fatal(err)
			}
			if _, err := migrated.Load("main"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("legacy resurrected deleted history: %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(legacy, "main.json")); err != nil || !bytes.Equal(got, original) {
				t.Fatalf("legacy source was modified: %v", err)
			}
			if err := os.WriteFile(filepath.Join(legacy, "main.json"), bytes.Replace(original, []byte("进度如何?"), []byte("new legacy write"), 1), 0600); err != nil {
				t.Fatal(err)
			}
			if err := migrated.ImportLegacy(legacy, true); err == nil {
				t.Fatal("changed legacy source silently accepted")
			}
		})
	}
}

func TestDocumentsLegacyConflictHasNoPartialImport(t *testing.T) {
	docs := selectedDocuments(t, "jsonl")
	store := NewStore(context.Background(), docs)
	legacy := t.TempDir()
	writeLegacySide(t, legacy, "a", false)
	writeLegacySide(t, legacy, "z", false)
	existing := sampleSideThread("z")
	existing.Messages[0].Text = "selected history"
	if err := store.Save(existing); err != nil {
		t.Fatal(err)
	}
	if err := store.ImportLegacy(legacy, true); err == nil {
		t.Fatal("conflicting import accepted")
	}
	if _, err := store.Load("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial import: %v", err)
	}
	got, err := store.Load("z")
	if err != nil || got.Messages[0].Text != "selected history" {
		t.Fatalf("selected history overwritten: %+v %v", got, err)
	}
}

type uncertainSideDocuments struct {
	persistence.Documents
	attempts []persistence.Transaction
	lose     bool
}

func (d *uncertainSideDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	d.attempts = append(d.attempts, tx)
	result, err := d.Documents.Commit(ctx, tx)
	if err == nil && d.lose {
		d.lose = false
		return persistence.Receipt{}, errors.New("commit response lost")
	}
	return result, err
}

func TestDocumentsCapturedStoreFinishesOldTurnAndRetriesExactIntent(t *testing.T) {
	ctx := context.Background()
	oldDocs, newDocs := selectedDocuments(t, "sqlite"), selectedDocuments(t, "jsonl")
	uncertain := &uncertainSideDocuments{Documents: oldDocs}
	captured := NewStore(ctx, uncertain)
	if _, err := captured.BeginTurn("same-main", "old request", "u", "a"); err != nil {
		t.Fatal(err)
	}
	current := NewStore(ctx, newDocs)
	if _, err := current.BeginTurn("same-main", "new request", "u2", "a2"); err != nil {
		t.Fatal(err)
	}
	uncertain.attempts = nil
	uncertain.lose = true
	if _, _, err := captured.FinishTurn("same-main", "a", "old result", StatusCompleted, ""); err != nil {
		t.Fatal(err)
	}
	if len(uncertain.attempts) != 2 || !reflect.DeepEqual(uncertain.attempts[0], uncertain.attempts[1]) {
		t.Fatalf("unknown commit was reconstructed: %+v", uncertain.attempts)
	}
	old, err := NewStore(ctx, oldDocs).Load("same-main")
	if err != nil {
		t.Fatal(err)
	}
	next, err := current.Load("same-main")
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != StatusCompleted || old.Messages[1].Text != "old result" || next.Status != StatusRunning || next.Messages[1].Text != "" {
		t.Fatalf("generation crossed: old=%+v next=%+v", old, next)
	}
}
