package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session/legacysqlite"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func TestLegacyMigrationPreservesInvalidTextAndRejectsLossyMarker(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			legacy := t.TempDir()
			original, err := Create(legacy, "original")
			if err != nil {
				t.Fatal(err)
			}
			content := string([]byte{'a', 0xff, 0xfe, 'z'})
			if err := AppendHistoryRecord(legacy, original.ID, HistoryRecord{Role: "user", Content: content}); err != nil {
				t.Fatal(err)
			}
			snapshot, _, err := legacysqlite.Read(ctx, DBPath(legacy))
			if err != nil {
				t.Fatal(err)
			}
			// Reproduce the pre-fix marker using its exact two-field row shape.
			type oldRow struct {
				Values      map[string]any `json:"values"`
				BlobColumns []string       `json:"blob_columns,omitempty"`
			}
			type oldTable struct {
				Name   string   `json:"name"`
				Schema string   `json:"schema"`
				Rows   []oldRow `json:"rows"`
			}
			var old struct {
				Tables []oldTable `json:"tables"`
			}
			for _, table := range snapshot.Tables {
				archived := oldTable{Name: table.Name, Schema: table.Schema}
				for _, row := range table.Rows {
					archived.Rows = append(archived.Rows, oldRow{Values: row.Values, BlobColumns: row.BlobColumns})
				}
				old.Tables = append(old.Tables, archived)
			}
			raw, err := json.Marshal(old)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(raw)
			lossy, err := persistence.Open(ctx, t.TempDir(), kind)
			if err != nil {
				t.Fatal(err)
			}
			defer lossy.Close()
			if err := persistence.Update(ctx, lossy, func(tx *persistence.Tx) error {
				return tx.Put(documentNamespace("legacy_sqlite_migrations"), dbFileName, LegacyMigrationResult{Fingerprint: hex.EncodeToString(digest[:])})
			}); err != nil {
				t.Fatal(err)
			}
			before, err := lossy.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if result, err := MigrateLegacySQLite(ctx, legacy, lossy); !errors.Is(err, persistence.ErrMigrationRequired) || result.AlreadyMigrated {
				t.Errorf("lossy marker was accepted: %+v, %v", result, err)
			}
			after, err := lossy.Export(ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("verification patched the lossy destination: %v", err)
			}
			provider, err := persistence.Open(ctx, t.TempDir(), kind)
			if err != nil {
				t.Fatal(err)
			}
			defer provider.Close()
			if _, err := MigrateLegacySQLite(ctx, legacy, provider); err != nil {
				t.Fatal(err)
			}
			view, err := provider.Read(ctx, persistence.Query{Namespace: documentNamespace("legacy_sqlite_rows"), Prefix: documentPrefix("session_messages")})
			if err != nil || len(view.Documents) != 1 {
				t.Fatalf("history archive: documents=%d, err=%v", len(view.Documents), err)
			}
			var row struct {
				TextBytes map[string][]byte `json:"text_bytes"`
			}
			if err := json.Unmarshal(view.Documents[0].Value, &row); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(row.TextBytes["content"], []byte(content)) {
				t.Errorf("archive lost invalid text bytes: got %x, want %x", row.TextBytes["content"], content)
			}
			if result, err := MigrateLegacySQLite(ctx, legacy, provider); err != nil || !result.AlreadyMigrated {
				t.Fatalf("byte-preserving marker rejected: %+v, %v", result, err)
			}
		})
	}
}

func TestLegacyMigrationCompletedMarkerSkipsConversion(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			legacy := t.TempDir()
			if _, err := Create(legacy, "original"); err != nil {
				t.Fatal(err)
			}
			db, err := openStore(legacy)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`CREATE TABLE custom_json (value TEXT); INSERT INTO custom_json VALUES ('source')`); err != nil {
				t.Fatal(err)
			}
			provider, err := persistence.Open(ctx, t.TempDir(), kind)
			if err != nil {
				t.Fatal(err)
			}
			defer provider.Close()
			converter := func(table string, _ []map[string]any) ([]persistence.Write, bool, error) {
				if table == "custom_json" {
					return []persistence.Write{{Namespace: "custom.converted", Key: "one", Value: json.RawMessage(`true`)}}, true, nil
				}
				return nil, false, nil
			}
			first, err := MigrateLegacySQLite(ctx, legacy, provider, converter)
			if err != nil || first.AlreadyMigrated {
				t.Fatalf("first migration: %+v, %v", first, err)
			}
			before, err := provider.Export(ctx)
			if err != nil || len(before.Receipts) != 1 {
				t.Fatalf("first migration must be atomic: receipts=%d, err=%v", len(before.Receipts), err)
			}
			calls := 0
			retiredConverter := func(string, []map[string]any) ([]persistence.Write, bool, error) {
				calls++
				return nil, false, errors.New("completed migration must not reconstruct typed writes")
			}
			bounded := &boundedSessionDocuments{Documents: provider}
			again, err := MigrateLegacySQLite(ctx, legacy, bounded, retiredConverter)
			if err != nil || !again.AlreadyMigrated || again.Fingerprint != first.Fingerprint || !reflect.DeepEqual(again.Rows, first.Rows) || calls != 0 {
				t.Fatalf("completed migration: %+v, calls=%d, err=%v", again, calls, err)
			}
			if _, err := db.Exec(`UPDATE custom_json SET value='changed'`); err != nil {
				t.Fatal(err)
			}
			if _, err := MigrateLegacySQLite(ctx, legacy, bounded, retiredConverter); !errors.Is(err, persistence.ErrMigrationRequired) || calls != 0 {
				t.Fatalf("changed source: calls=%d, err=%v", calls, err)
			}
			after, err := provider.Export(ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("verification changed destination: %v", err)
			}
		})
	}
}

type competingMigrationDocuments struct {
	*persistence.Store
	changed  bool
	attempts int
}

func (d *competingMigrationDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	d.attempts++
	if d.attempts != 1 {
		return persistence.Receipt{}, errors.New("completed competing migration was replayed")
	}
	tx.Writes = append([]persistence.Write(nil), tx.Writes...)
	if d.changed {
		for i, write := range tx.Writes {
			if write.Namespace == documentNamespace("legacy_sqlite_migrations") {
				var marker LegacyMigrationResult
				if err := json.Unmarshal(write.Value, &marker); err != nil {
					return persistence.Receipt{}, err
				}
				marker.Fingerprint = "a different stopped source"
				raw, err := json.Marshal(marker)
				if err != nil {
					return persistence.Receipt{}, err
				}
				tx.Writes[i].Value = raw
			}
		}
	}
	if _, err := d.Store.Commit(ctx, tx); err != nil {
		return persistence.Receipt{}, err
	}
	return persistence.Receipt{}, persistence.ErrConflict
}

func TestLegacyMigrationRechecksMarkerAfterConflict(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint("changed=", changed), func(t *testing.T) {
			ctx := context.Background()
			legacy := t.TempDir()
			if _, err := Create(legacy, "original"); err != nil {
				t.Fatal(err)
			}
			provider, err := persistence.Open(ctx, t.TempDir(), "sqlite")
			if err != nil {
				t.Fatal(err)
			}
			defer provider.Close()
			documents := &competingMigrationDocuments{Store: provider, changed: changed}
			result, err := MigrateLegacySQLite(ctx, legacy, documents)
			if changed {
				if !errors.Is(err, persistence.ErrMigrationRequired) {
					t.Fatalf("competing changed source accepted: %v", err)
				}
			} else if err != nil || !result.AlreadyMigrated {
				t.Fatalf("competing migration not recognized: %+v, %v", result, err)
			}
			if documents.attempts != 1 {
				t.Fatalf("migration replayed after conflict: %d commits", documents.attempts)
			}
		})
	}
}

type migrationRetryDocuments struct {
	*persistence.Store
	attempts int
}

func (d *migrationRetryDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	d.attempts++
	if d.attempts == 1 {
		_, err := d.Store.Commit(ctx, persistence.Transaction{ID: "concurrent-writer", ExpectedRevision: tx.ExpectedRevision, Writes: []persistence.Write{{Namespace: "unrelated", Key: "advance", Value: json.RawMessage(`true`)}}})
		if err != nil {
			return persistence.Receipt{}, err
		}
		return persistence.Receipt{}, persistence.ErrConflict
	}
	receipt, err := d.Store.Commit(ctx, tx)
	if err == nil && d.attempts == 2 {
		return persistence.Receipt{}, errors.New("migration reply lost")
	}
	return receipt, err
}

func TestLegacyMigrationNormalizedIntentSurvivesRetries(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			legacy := t.TempDir()
			if _, err := Create(legacy, "original"); err != nil {
				t.Fatal(err)
			}
			db, err := openStore(legacy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`CREATE TABLE custom_json (value TEXT); INSERT INTO custom_json VALUES ('source')`); err != nil {
				t.Fatal(err)
			}
			db.Close()
			provider, err := persistence.Open(ctx, t.TempDir(), kind)
			if err != nil {
				t.Fatal(err)
			}
			defer provider.Close()
			documents := &migrationRetryDocuments{Store: provider}
			raw := json.RawMessage(" { \"large\":9007199254740993, \"html\":\"<>&\", \"unicode\":\"\\u4f60\", \"fraction\":1e+00, \"nested\": [ true, null ] } ")
			want, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			converter := func(table string, _ []map[string]any) ([]persistence.Write, bool, error) {
				if table == "custom_json" {
					return []persistence.Write{{Namespace: "custom.converted", Key: "one", Value: raw}}, true, nil
				}
				return nil, false, nil
			}
			result, err := MigrateLegacySQLite(ctx, legacy, documents, converter)
			var uncertain *persistence.CommitError
			if !errors.As(err, &uncertain) || documents.attempts != 2 {
				t.Fatalf("CAS/uncertain boundary: attempts=%d, err=%v", documents.attempts, err)
			}
			seen := make(map[string]bool)
			for _, write := range uncertain.Transaction.Writes {
				seen[write.Namespace] = true
				if write.Namespace == "custom.converted" && !bytes.Equal(write.Value, want) {
					t.Fatalf("converter normalization changed: %s", write.Value)
				}
			}
			for _, namespace := range []string{"custom.converted", documentNamespace("sessions"), documentNamespace("legacy_sqlite_rows"), documentNamespace("legacy_sqlite_schema"), documentNamespace("legacy_sqlite_migrations")} {
				if !seen[namespace] {
					t.Fatalf("atomic intent omitted %s", namespace)
				}
			}
			for i := 0; i < 2; i++ {
				receipt, err := provider.Commit(ctx, uncertain.Transaction)
				if err != nil || receipt.Revision != 2 {
					t.Fatalf("exact retry: %+v, %v", receipt, err)
				}
			}
			snapshot, err := provider.Export(ctx)
			if err != nil || len(snapshot.Receipts) != 2 {
				t.Fatalf("migration split or replayed: receipts=%d, err=%v", len(snapshot.Receipts), err)
			}
			again, err := MigrateLegacySQLite(ctx, legacy, documents, converter)
			if err != nil || !again.AlreadyMigrated || again.Fingerprint != result.Fingerprint || documents.attempts != 2 {
				t.Fatalf("completed marker replayed: %+v, %v", again, err)
			}
		})
	}
}

type migrationAllocationDocuments struct {
	batch, ready, encoded runtime.MemStats
}

func (d *migrationAllocationDocuments) Read(_ context.Context, query persistence.Query) (persistence.View, error) {
	if query.Namespace == documentNamespace("legacy_sqlite_migrations") {
		runtime.ReadMemStats(&d.batch)
	}
	return persistence.View{Documents: []persistence.Document{}}, nil
}

func (d *migrationAllocationDocuments) Commit(_ context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	runtime.ReadMemStats(&d.ready)
	raw, err := json.Marshal(tx)
	runtime.ReadMemStats(&d.encoded)
	runtime.KeepAlive(raw)
	return persistence.Receipt{ID: tx.ID, Revision: 1}, err
}

// This probe isolates allocations after the converted batch exists; durable
// behavior is exercised separately by the real SQLite/JSONL migration tests.
func BenchmarkLegacyMigrationValueCopies(b *testing.B) {
	legacy := b.TempDir()
	if _, err := Create(legacy, "original"); err != nil {
		b.Fatal(err)
	}
	converter := func(table string, _ []map[string]any) ([]persistence.Write, bool, error) {
		if table != "" {
			return nil, false, nil
		}
		writes := make([]persistence.Write, 128)
		for i := range writes {
			raw, err := json.Marshal(strings.Repeat("<result>&\n", 4096))
			if err != nil {
				return nil, false, err
			}
			writes[i] = persistence.Write{Namespace: "custom.converted", Key: fmt.Sprint(i), Value: raw}
		}
		return writes, false, nil
	}
	var readyBytes, encodedBytes uint64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		probe := &migrationAllocationDocuments{}
		if _, err := MigrateLegacySQLite(context.Background(), legacy, probe, converter); err != nil {
			b.Fatal(err)
		}
		readyBytes += probe.ready.TotalAlloc - probe.batch.TotalAlloc
		encodedBytes += probe.encoded.TotalAlloc - probe.ready.TotalAlloc
	}
	b.ReportMetric(float64(readyBytes)/float64(b.N), "tx-ready-B/op")
	b.ReportMetric(float64(encodedBytes)/float64(b.N), "tx-encode-B/op")
}

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
			store := newDocumentStore(ctx, documents)
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
	store := newDocumentStore(ctx, documents)
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

func TestLegacyMigrationRecorded(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		documents := &boundedSessionDocuments{Documents: store.documents}
		recorded, err := LegacySQLiteMigrationRecorded(context.Background(), documents)
		if err != nil || recorded {
			t.Fatalf("new store migration marker: %v %v", recorded, err)
		}
		legacy := t.TempDir()
		if _, err := Create(legacy, "marker"); err != nil {
			t.Fatal(err)
		}
		if _, err := MigrateLegacySQLite(context.Background(), legacy, store.documents); err != nil {
			t.Fatal(err)
		}
		recorded, err = LegacySQLiteMigrationRecorded(context.Background(), documents)
		if err != nil || !recorded {
			t.Fatalf("committed migration marker: %v %v", recorded, err)
		}
	})
}

func TestLegacyPersonalReceiptMigrationPreservesScopeAndPayload(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		ctx := context.Background()
		dir := t.TempDir()
		if _, err := Create(dir, "personal"); err != nil {
			t.Fatal(err)
		}
		db, err := openStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, statement := range []string{
			`ALTER TABLE sessions ADD COLUMN user_space_id TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE sessions ADD COLUMN product_domain TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE sessions ADD COLUMN agent_id TEXT NOT NULL DEFAULT ''`,
			`UPDATE sessions SET user_space_id='local',product_domain='personal',agent_id='agent',owner='plugin' WHERE id='personal'`,
			`CREATE TABLE client_input_receipts(session_id TEXT,client_id TEXT,plugin_id TEXT,host_request_id TEXT,request_id TEXT,queue_id TEXT,summary_json TEXT,fingerprint TEXT,message_json TEXT,runtime_json TEXT,selection_json TEXT,live_owner TEXT)`,
		} {
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
		summary := providers.ClientInputReceipt{SessionID: "personal", ClientID: "client", PluginID: "plugin", HostRequestID: "host", RequestID: "request", QueueID: "queue", ReceiptID: "receipt", UserSpaceID: "local", ProductDomain: "personal", AgentID: "agent", Owner: "plugin", ReceiptState: "accepted", ExecutionState: "queued", Revision: 4}
		raw, _ := json.Marshal(summary)
		message := `{ "text": "original" }`
		runtime := `{"ExecutionControlBaseline":{"SessionID":"personal"}}`
		selection := `{"bytes":"original attachment"}`
		if _, err := db.Exec(`INSERT INTO client_input_receipts VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, "personal", "client", "plugin", "host", "request", "queue", string(raw), "fingerprint", message, runtime, selection, "owner"); err != nil {
			t.Fatal(err)
		}
		if _, err := MigrateLegacySQLite(ctx, dir, native.documents); err != nil {
			t.Fatal(err)
		}
		client := NewStore(ctx, native.documents)
		scope := ClientInputScope{Binding: userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}, SessionID: "personal", PluginID: "plugin"}
		got, found, err := client.ReadClientInput(scope, "host")
		if err != nil || !found || !reflect.DeepEqual(got.Receipt, summary) || string(got.MessageJSON) != message || string(got.RuntimeJSON) != runtime || string(got.SelectionJSON) != selection || got.LiveOwner != "owner" {
			t.Fatalf("migrated accepted input: %+v %v %v", got, found, err)
		}
		if _, err := MigrateLegacySQLite(ctx, dir, native.documents); err != nil {
			t.Fatal(err)
		}
		if err := client.RecoverClientInputs(t.TempDir(), scope); err != nil {
			t.Fatal(err)
		}
		got, found, err = client.ReadClientInput(scope, "host")
		if err != nil || !found || got.Receipt.ExecutionState != "held" || got.Receipt.Revision != 5 || string(got.SelectionJSON) != selection {
			t.Fatalf("recovered input: %+v %v %v", got, found, err)
		}
		if _, err := client.Delete("personal"); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"client_input_receipts", "client_input_payloads"} {
			view, err := native.documents.Read(ctx, persistence.Query{Namespace: documentNamespace(table)})
			if err != nil || len(view.Documents) != 0 {
				t.Fatalf("deleted receipt leaked in %s: %d %v", table, len(view.Documents), err)
			}
		}
	})
}
