package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// Migration must preserve exact receipts and attachments, retain physical
// history across edits, reject partial/conflicting imports, and be restartable.
func TestLegacySessionMigration(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			legacy := t.TempDir()
			original, err := CreateWithMetadata(legacy, "legacy/one", "/workspace")
			if err != nil {
				t.Fatal(err)
			}
			record := HistoryRecord{Role: "user", Content: "old", Images: json.RawMessage(`[{"url":"attachment://image"}]`), Files: json.RawMessage(`[{"name":"report.pdf","path":"attachment://file"}]`), ContentParts: json.RawMessage(`[{"type":"text","text":"old"}]`)}
			if err := AppendHistoryRecord(legacy, original.ID, record); err != nil {
				t.Fatal(err)
			}
			if err := RewriteHistoryRecordsForEdit(legacy, original.ID, []HistoryRecord{{Role: "user", Content: "edited"}}, 1, 1); err != nil {
				t.Fatal(err)
			}
			receipt := ModelInputReceiptRecord{OperationID: "receipt", ContractVersion: 1, DriverID: "driver", DriverVersion: "v1", Payload: json.RawMessage("{\n  \"n\": 9007199254740993, \"text\": \"<raw>\"\n}"), CreatedAt: time.Now().UTC()}
			if err := SaveModelInputReceipt(legacy, original.ID, receipt); err != nil {
				t.Fatal(err)
			}
			if err := ReplaceHeldUserWork(legacy, original.ID, []HeldUserWork{{ID: "held", Origin: HeldUserWorkOriginQueue, MessageJSON: []byte(`{"content":"queued"}`), RuntimeJSON: []byte(`{}`)}}); err != nil {
				t.Fatal(err)
			}
			if _, err := UpdateArchived(legacy, original.ID, true); err != nil {
				t.Fatal(err)
			}
			targetDir := t.TempDir()
			documents, err := persistence.Open(ctx, targetDir, backend)
			if err != nil {
				t.Fatal(err)
			}
			result, err := MigrateLegacySQLite(ctx, legacy, documents)
			if err != nil || result.AlreadyMigrated || result.Fingerprint == "" {
				t.Fatalf("migrate: %+v %v", result, err)
			}
			if err := documents.Close(); err != nil {
				t.Fatal(err)
			}
			documents, err = persistence.Open(ctx, targetDir, backend)
			if err != nil {
				t.Fatal(err)
			}
			defer documents.Close()
			store := NewStore(ctx, documents)
			got, found, err := store.Find(original.ID)
			if err != nil || !found || got.ArchivedAt == nil {
				t.Fatalf("session: %+v %v %v", got, found, err)
			}
			history, err := store.LoadHistoryRecords(original.ID, true)
			if err != nil || len(history) != 2 || !bytes.Equal(history[0].Images, record.Images) || !bytes.Equal(history[0].Files, record.Files) {
				t.Fatalf("history: %+v %v", history, err)
			}
			active, err := store.LoadActiveHistoryRecords(original.ID, false)
			if err != nil || len(active) != 1 || active[0].Content != "edited" {
				t.Fatalf("active: %+v %v", active, err)
			}
			receipts, err := store.LoadModelInputReceipts(original.ID)
			if err != nil || len(receipts) != 1 || !bytes.Equal(receipts[0].Payload, receipt.Payload) {
				t.Fatalf("receipt bytes changed: %+v %v", receipts, err)
			}
			held, err := store.LoadHeldUserWork(original.ID)
			if err != nil || len(held) != 1 || string(held[0].MessageJSON) != `{"content":"queued"}` {
				t.Fatalf("held: %+v %v", held, err)
			}
			// A retry after the documents have legitimately evolved never reimports old rows.
			if _, err := store.UpdateTitle(original.ID, "new title"); err != nil {
				t.Fatal(err)
			}
			again, err := MigrateLegacySQLite(ctx, legacy, documents)
			if err != nil || !again.AlreadyMigrated || again.Fingerprint != result.Fingerprint {
				t.Fatalf("repeat: %+v %v", again, err)
			}
			got, _, _ = store.Find(original.ID)
			if got.Title != "new title" {
				t.Fatal("retry overwrote current data")
			}
			if _, err := UpdateTitle(legacy, original.ID, "late legacy writer"); err != nil {
				t.Fatal(err)
			}
			if _, err := MigrateLegacySQLite(ctx, legacy, documents); !errors.Is(err, persistence.ErrMigrationRequired) {
				t.Fatalf("changed source admitted: %v", err)
			}
		})
	}
}

func TestLegacyMigrationRequiresCompleteComposition(t *testing.T) {
	ctx := context.Background()
	legacy := t.TempDir()
	if _, err := Create(legacy, "one"); err != nil {
		t.Fatal(err)
	}
	db, err := openStore(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO inference_journal_runtimes(id, workspace_scope, heartbeat_at, started_at, closed_at, pid) VALUES('runtime','scope',1,1,0,42)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	documents, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer documents.Close()
	if _, err := MigrateLegacySQLite(ctx, legacy, documents); err == nil {
		t.Fatal("unconverted inference rows admitted")
	}
	view, err := documents.Read(ctx, persistence.Query{Namespace: documentNamespace("sessions")})
	if err != nil || len(view.Documents) != 0 {
		t.Fatalf("partial import: %+v %v", view, err)
	}
	converter := func(table string, rows []map[string]any) ([]persistence.Write, bool, error) {
		if table != "inference_journal_runtimes" {
			return nil, false, nil
		}
		if len(rows) != 1 || rows[0]["pid"] != int64(42) {
			t.Fatalf("converter input: %+v", rows)
		}
		return []persistence.Write{{Namespace: "test.inference", Key: "runtime", Value: json.RawMessage(`{"converted":true}`)}}, true, nil
	}
	if _, err := MigrateLegacySQLite(ctx, legacy, documents, converter); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyMigrationRejectsConflictingTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	legacy := t.TempDir()
	if _, err := Create(legacy, "one"); err != nil {
		t.Fatal(err)
	}
	documents, err := persistence.Open(ctx, t.TempDir(), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer documents.Close()
	store := NewStore(ctx, documents)
	if _, err := store.Create("already-owned"); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateLegacySQLite(ctx, legacy, documents); !errors.Is(err, persistence.ErrMigrationRequired) {
		t.Fatalf("conflict: %v", err)
	}
	if _, found, err := store.Find("one"); err != nil || found {
		t.Fatalf("partial import: %v %v", found, err)
	}
}

type uncertainMigrationDocuments struct {
	persistence.Documents
	lost bool
}

func (d *uncertainMigrationDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	receipt, err := d.Documents.Commit(ctx, tx)
	if err == nil && !d.lost {
		d.lost = true
		return persistence.Receipt{}, errors.New("lost response")
	}
	return receipt, err
}
func TestLegacyMigrationRecoversUncertainCommit(t *testing.T) {
	ctx := context.Background()
	legacy := t.TempDir()
	if _, err := Create(legacy, "one"); err != nil {
		t.Fatal(err)
	}
	provider, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	documents := &uncertainMigrationDocuments{Documents: provider}
	_, err = MigrateLegacySQLite(ctx, legacy, documents)
	var uncertain *persistence.CommitError
	if !errors.As(err, &uncertain) {
		t.Fatalf("missing exact retry: %v", err)
	}
	if _, err := documents.Commit(ctx, uncertain.Transaction); err != nil {
		t.Fatal(err)
	}
	result, err := MigrateLegacySQLite(ctx, legacy, documents)
	if err != nil || !result.AlreadyMigrated {
		t.Fatalf("recovery: %+v %v", result, err)
	}
}
