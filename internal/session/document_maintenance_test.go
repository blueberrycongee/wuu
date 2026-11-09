package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Heartbeats may invalidate one batch, but must not restart the entire sweep.
// A completed sweep stays complete because ordinary writes already compact.
func TestRedundantStorageMakesProgressAcrossConcurrentWrites(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		payload, _ := json.Marshal(toolresult.FromText("projection"))
		if err := store.transaction(func(tx *persistence.Tx) error {
			for i := 0; i < 600; i++ {
				if err := tx.Put(documentNamespace("session_messages"), fmt.Sprintf("row-%04d", i), HistoryRecord{Role: "tool", Content: "projection", ToolResult: payload}); err != nil {
					return err
				}
			}
			return tx.Put(documentNamespace("session_messages"), "row-broken", "unrecoverable legacy record")
		}); err != nil {
			t.Fatal(err)
		}
		probe := &maintenanceHeartbeat{Documents: store.documents, starts: make(map[string]bool)}
		store.documents = probe
		result, err := store.MaintainRedundantStorage()
		if err != nil || result.ToolMessageContentsCompacted != 600 {
			t.Fatalf("maintenance: %+v %v", result, err)
		}
		if probe.reads > 20 {
			t.Fatalf("sweep restarted: %d history reads", probe.reads)
		}
		probe.reads = 0
		if _, err := store.MaintainRedundantStorage(); err != nil || probe.reads != 0 {
			t.Fatalf("completed sweep repeated: reads=%d err=%v", probe.reads, err)
		}
		view, err := probe.Documents.Read(context.Background(), persistence.Query{Namespace: documentNamespace("session_messages"), Keys: []string{"row-broken"}})
		if err != nil || len(view.Documents) != 1 {
			t.Fatalf("legacy record was removed: %+v %v", view, err)
		}
	})
}

type maintenanceHeartbeat struct {
	persistence.Documents
	starts map[string]bool
	reads  int
}

func (p *maintenanceHeartbeat) Read(ctx context.Context, q persistence.Query) (persistence.View, error) {
	view, err := p.Documents.Read(ctx, q)
	if err != nil || q.Namespace != documentNamespace("session_messages") {
		return view, err
	}
	p.reads++
	if !p.starts[q.Start] {
		p.starts[q.Start] = true
		_, err = p.Documents.Commit(ctx, persistence.Transaction{ID: fmt.Sprintf("heartbeat-%d", len(p.starts)), ExpectedRevision: view.Revision, Writes: []persistence.Write{{Namespace: "heartbeat", Key: "live", Value: json.RawMessage(`true`)}}})
	}
	return view, err
}

// Diagnostic retention must not delete conversation, recovery checkpoints, or
// recent exact receipt bytes; imported over-budget diagnostics are bounded too.
func TestDocumentDiagnosticRetention(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		if _, err := store.CreateInitialized(Session{ID: "retention"}, []HistoryRecord{{Role: "user", Content: "retain forever"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StoreHistoryCheckpointAtBaseline("retention", "compaction", []HistoryRecord{{Role: "user", Content: "resume exactly"}}, 1); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		payload := []byte("{\n \"exact\": \"<value>\"\n}")
		if err := store.transaction(func(tx *persistence.Tx) error {
			for i := 0; i < 70; i++ {
				stamp := now.Add(-time.Duration(i) * time.Minute)
				if i == 69 {
					stamp = now.Add(-8 * 24 * time.Hour)
				}
				receipt := modelReceiptDocument{OperationID: fmt.Sprintf("op-%02d", i), ContractVersion: 1, DriverID: "driver", DriverVersion: "1", Payload: payload, CreatedAt: stamp}
				if err := tx.Put(documentNamespace("session_model_input_receipts"), documentPrefix("retention")+receipt.OperationID, receipt); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		result, err := store.MaintainModelInputReceipts(now)
		if err != nil || result.Deleted != 6 {
			t.Fatalf("retention: %+v %v", result, err)
		}
		restartSessionStore(t, store)
		receipts, err := store.LoadModelInputReceipts("retention")
		if err != nil || len(receipts) != 64 {
			t.Fatalf("receipts: %d %v", len(receipts), err)
		}
		for _, receipt := range receipts {
			if !bytes.Equal(receipt.Payload, payload) {
				t.Fatal("receipt normalized")
			}
		}
		history, err := store.LoadHistoryRecords("retention", true)
		if err != nil || len(history) != 2 || history[0].Content != "retain forever" {
			t.Fatalf("history: %+v %v", history, err)
		}
		checkpoint, found, err := store.LatestHistoryCheckpoint("retention")
		if err != nil || !found || checkpoint.Replacement[0].Content != "resume exactly" {
			t.Fatalf("checkpoint: %+v %v", checkpoint, err)
		}
		result, err = store.MaintainModelInputReceipts(now)
		if err != nil || result.Deleted != 0 {
			t.Fatalf("repeat: %+v %v", result, err)
		}
	})
}

func TestDocumentRedundantStoragePreservesProjection(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		if _, err := store.Create("legacy-tools"); err != nil {
			t.Fatal(err)
		}
		result := toolresult.FromText("projection")
		payload, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.transaction(func(tx *persistence.Tx) error {
			return tx.Put(documentNamespace("session_messages"), historyDocumentKey("legacy-tools", 1), HistoryRecord{Seq: 1, Role: "tool", Content: "projection", ToolResult: payload})
		}); err != nil {
			t.Fatal(err)
		}
		maintenance, err := store.MaintainRedundantStorage()
		if err != nil || maintenance.ToolMessageContentsCompacted != 1 {
			t.Fatalf("maintenance: %+v %v", maintenance, err)
		}
		history, err := store.LoadHistoryRecords("legacy-tools", false)
		if err != nil || len(history) != 1 || history[0].Content != "projection" {
			t.Fatalf("projection: %+v %v", history, err)
		}
	})
}
