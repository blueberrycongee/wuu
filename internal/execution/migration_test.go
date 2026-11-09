package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

// Import must retain accounting and all ownership indexes, reject orphaned
// rows atomically, and recover unknown external effects without replaying them.
func TestLegacyJournalMigrationRecovery(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			legacyDir := t.TempDir()
			db, err := sql.Open("sqlite", filepath.Join(legacyDir, "sessions.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			statements := []string{
				`CREATE TABLE inference_journal_runtimes (id TEXT PRIMARY KEY, workspace_scope TEXT, pid INTEGER, started_at INTEGER, heartbeat_at INTEGER, closed_at INTEGER)`,
				`CREATE TABLE inference_workflows (id TEXT PRIMARY KEY, runtime_id TEXT, workspace_scope TEXT, owner_id TEXT, workload_profile TEXT, max_attempts INTEGER, used_operations INTEGER, used_attempts INTEGER, used_submissions INTEGER, used_replays INTEGER, known_submissions INTEGER, unknown_billable INTEGER, known_usage_tokens INTEGER, status TEXT, created_at INTEGER, updated_at INTEGER)`,
				`CREATE TABLE inference_operations (id TEXT PRIMARY KEY, workflow_id TEXT, attempt_limit INTEGER, runtime_id TEXT, workspace_scope TEXT, owner_id TEXT, kind TEXT, workload_profile TEXT, payload_version INTEGER, request_hash TEXT, status TEXT, created_at INTEGER, updated_at INTEGER)`,
				`CREATE TABLE inference_attempts (id TEXT PRIMARY KEY, operation_id TEXT, ordinal INTEGER, request_hash TEXT, phase TEXT, prepared_at INTEGER)`,
				`CREATE TABLE inference_submissions (id TEXT PRIMARY KEY, operation_id TEXT, attempt_id TEXT, ordinal INTEGER, attempt_ordinal INTEGER, outcome TEXT, cost_state TEXT, reported_input_tokens INTEGER, reported_output_tokens INTEGER, has_reported_usage INTEGER, started_at INTEGER, completed_at INTEGER)`,
				`CREATE TABLE tool_batches (id TEXT PRIMARY KEY, owner_id TEXT, operation_id TEXT, step_index INTEGER, status TEXT, created_at INTEGER, updated_at INTEGER)`,
				`CREATE TABLE tool_invocations (id TEXT PRIMARY KEY, batch_id TEXT, provider_call_id TEXT, tool_name TEXT, arguments_json TEXT, replay_policy TEXT, state TEXT, prepared_at INTEGER, running_at INTEGER, result_json TEXT DEFAULT '')`,
				`CREATE TABLE execution_runs (id TEXT PRIMARY KEY, runtime_id TEXT, status TEXT, request_json TEXT, runtime_json TEXT, thread_id TEXT, workspace_id TEXT, workspace_root TEXT, created_at INTEGER, updated_at INTEGER)`,
				`CREATE TABLE execution_run_turns (run_id TEXT, thread_id TEXT, turn_id TEXT, ordinal INTEGER, trace_path TEXT, attached_at INTEGER)`,

				`INSERT INTO inference_journal_runtimes VALUES ('old-runtime','migration',-1,1,2,3)`,
				`INSERT INTO inference_workflows (id,runtime_id,workspace_scope,owner_id,workload_profile,max_attempts,used_operations,used_attempts,used_submissions,used_replays,known_submissions,unknown_billable,known_usage_tokens,status,created_at,updated_at) VALUES ('workflow','old-runtime','migration','thread','interactive',8,1,2,2,1,1,1,37,'active',1,2)`,
				`INSERT INTO inference_operations (id,workflow_id,attempt_limit,runtime_id,workspace_scope,owner_id,kind,workload_profile,payload_version,request_hash,status,created_at,updated_at) VALUES ('operation','workflow',8,'old-runtime','migration','thread','agent_round','interactive',1,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','active',1,2)`,
				`INSERT INTO inference_attempts (id,operation_id,ordinal,request_hash,phase,prepared_at) VALUES ('attempt-1','operation',1,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','terminal',1),('attempt-2','operation',2,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','sent',2)`,
				`INSERT INTO inference_submissions (id,operation_id,attempt_id,ordinal,attempt_ordinal,outcome,cost_state,reported_input_tokens,reported_output_tokens,has_reported_usage,started_at,completed_at) VALUES ('submission-1','operation','attempt-1',1,1,'succeeded','known',30,7,1,1,2),('submission-2','operation','attempt-2',2,2,'in_flight','unknown_but_billable',0,0,0,2,0)`,
				`INSERT INTO tool_batches (id,owner_id,operation_id,step_index,status,created_at,updated_at) VALUES ('batch','thread','operation',1,'finalized',1,2)`,
				`INSERT INTO tool_invocations (id,batch_id,provider_call_id,tool_name,arguments_json,replay_policy,state,prepared_at,running_at) VALUES ('invocation','batch','call','write_file','{"text":"retained"}','at_most_once','running',1,2)`,
				`INSERT INTO execution_runs (id,runtime_id,status,request_json,runtime_json,thread_id,workspace_id,workspace_root,created_at,updated_at) VALUES ('legacy-run','old-runtime','running','{"mode":"start","has_prompt":true}','{"resolved":{"provider":"fixture","model":"fixture"},"protocol_version":"v1"}','thread','workspace','/workspace',1,2)`,
				`INSERT INTO execution_run_turns VALUES ('legacy-run','thread','turn',1,'/trace',1)`,
			}
			for _, stmt := range statements {
				if _, err := db.Exec(stmt); err != nil {
					t.Fatal(err)
				}
			}
			converters := []func(string, []map[string]any) ([]persistence.Write, bool, error){session.NewLegacyInferenceConverter(), toolstate.NewLegacyConverter(), NewLegacyConverter()}
			source, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Rollback()
			// Deliberately visit children first; conversion must not depend on table order.
			tables := []string{"execution_run_turns", "inference_submissions", "tool_invocations", "inference_attempts", "execution_runs", "inference_operations", "tool_batches", "inference_workflows", "inference_journal_runtimes"}
			for _, table := range tables {
				rows := legacyRowsForTest(t, source, table)
				claimed := false
				for _, convert := range converters {
					writes, ok, err := convert(table, rows)
					if err != nil {
						t.Fatal(err)
					}
					if len(writes) != 0 {
						t.Fatal("partial conversion before relationships validated")
					}
					claimed = claimed || ok
				}
				if !claimed {
					t.Fatalf("unclaimed %s", table)
				}
			}
			var writes []persistence.Write
			for _, convert := range converters {
				next, _, err := convert("", nil)
				if err != nil {
					t.Fatal(err)
				}
				writes = append(writes, next...)
			}
			if len(writes) == 0 {
				t.Fatal("empty conversion")
			}
			documents, err := persistence.Open(context.Background(), t.TempDir(), backend)
			if err != nil {
				t.Fatal(err)
			}
			defer documents.Close()
			migrate := func() (session.LegacyMigrationResult, error) {
				return session.MigrateLegacySQLite(context.Background(), legacyDir, documents, session.NewLegacyInferenceConverter(), toolstate.NewLegacyConverter(), NewLegacyConverter())
			}
			result, err := migrate()
			if err != nil || result.AlreadyMigrated {
				t.Fatalf("migration: %+v %v", result, err)
			}
			again, err := migrate()
			if err != nil || !again.AlreadyMigrated {
				t.Fatalf("migration replay: %+v %v", again, err)
			}
			executionStore, err := NewStore(documents)
			if err != nil {
				t.Fatal(err)
			}
			journalRuntime, err := session.NewStore(context.Background(), documents).NewInferenceJournalRuntime("test-workspace")
			if err != nil {
				t.Fatal(err)
			}
			defer journalRuntime.Close()
			store := &testExecutionStore{Store: executionStore, documents: &executionTestDocuments{Store: documents}, runtime: journalRuntime}

			view, err := store.documents.Read(context.Background(), persistence.Query{Namespace: "wuu.inference_workflows.v1", Keys: []string{"workflow"}})
			if err != nil {
				t.Fatal(err)
			}
			var workflow struct {
				OperationIDs                      []string
				Used                              map[string]int64
				KnownUsageTokens, UnknownBillable int64
			}
			if err := json.Unmarshal(view.Documents[0].Value, &workflow); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(workflow.OperationIDs, []string{"operation"}) || workflow.KnownUsageTokens != 37 || workflow.UnknownBillable != 1 || workflow.Used["attempts"] != 2 {
				t.Fatalf("accounting changed: %+v", workflow)
			}
			ledger, err := toolledger.New(store.documents, "thread")
			if err != nil {
				t.Fatal(err)
			}
			if decision, err := ledger.DecideReplay(context.Background(), "batch"); err != nil || decision.Action != toolledger.ReplayBlock {
				t.Fatalf("running effect replay: %+v %v", decision, err)
			}
			other, err := toolledger.New(store.documents, "other")
			if err != nil {
				t.Fatal(err)
			}
			if err := other.Start(context.Background(), "invocation"); err == nil {
				t.Fatal("migrated owner bypass")
			}
			if _, err := store.Create(context.Background(), testCreateParams(store.runtime.RuntimeID(), ModeStart, "", "thread")); !errors.Is(err, ErrConflict) {
				t.Fatalf("lost active index: %v", err)
			}
			recovered, err := store.ReconcileOrphans(context.Background(), store.runtime.RuntimeID(), time.Now())
			if err != nil || len(recovered) != 1 {
				t.Fatalf("run recovery: %+v %v", recovered, err)
			}
			run, err := store.Get(context.Background(), "legacy-run")
			if err != nil || len(run.Turns) != 1 || run.Turns[0].TracePath != "/trace" {
				t.Fatalf("lost turn: %+v %v", run, err)
			}
			successor, err := store.Create(context.Background(), testCreateParams(store.runtime.RuntimeID(), ModeStart, "", "thread"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.AttachTurn(context.Background(), successor.ID, "thread", "turn", time.Now()); !errors.Is(err, ErrConflict) {
				t.Fatalf("lost turn owner: %v", err)
			}
			runtime, err := session.NewStore(context.Background(), store.documents).NewInferenceJournalRuntime("migration")
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			recovery, err := runtime.ReconcileOrphans(time.Now())
			if err != nil || len(recovery) != 1 || string(recovery[0].Outcome) != "blocked" {
				t.Fatalf("inference recovery: %+v %v", recovery, err)
			}
			if err := ledger.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			if decision, err := ledger.DecideReplay(context.Background(), "batch"); err != nil || decision.Reason != toolledger.ReplayReasonInvocationUnknown {
				t.Fatalf("ambiguous effect replayed: %+v %v", decision, err)
			}
			var status string
			if err := source.QueryRow(`SELECT state FROM tool_invocations WHERE id='invocation'`).Scan(&status); err != nil || status != "running" {
				t.Fatalf("legacy source mutated: %s %v", status, err)
			}
		})
	}
}

func legacyRowsForTest(t *testing.T, tx *sql.Tx, table string) []map[string]any {
	t.Helper()
	rows, err := tx.Query(`SELECT * FROM "` + table + `"`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		row := make(map[string]any)
		for i, key := range columns {
			row[key] = values[i]
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestLegacyExecutionRejectsMissingRuntimeBeforeWrites(t *testing.T) {
	convert := NewLegacyConverter()
	_, claimed, err := convert("execution_runs", []map[string]any{{"id": "run", "runtime_id": "missing", "thread_id": "thread", "status": "accepted", "request_json": `{"mode":"start"}`, "runtime_json": `{}`, "created_at": int64(1), "updated_at": int64(1)}})
	if err != nil || !claimed {
		t.Fatalf("collect: %v %v", claimed, err)
	}
	writes, _, err := convert("", nil)
	if !errors.Is(err, persistence.ErrCorrupt) || len(writes) != 0 {
		t.Fatalf("orphan runtime imported: writes=%d err=%v", len(writes), err)
	}
}
