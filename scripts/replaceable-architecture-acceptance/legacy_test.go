package acceptance_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolstate"
	"github.com/stretchr/testify/require"
)

type lostMigrationReply struct{ persistence.Documents }

func (d lostMigrationReply) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	if _, err := d.Documents.Commit(ctx, tx); err != nil {
		return persistence.Receipt{}, err
	}
	return persistence.Receipt{}, errors.New("acceptance: lost durable migration reply")
}

// Ordinary sessions, inference accounting, tool receipts and execution ownership
// must import together. Exercise an actual closed SQLite file, an omitted
// converter, a lost commit reply, restart, and a subsequent physical migration.
func TestAcceptanceJointLegacyMigration(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			legacy := t.TempDir()
			source := filepath.Join(legacy, "sessions.sqlite3")
			db, err := sql.Open("sqlite", source)
			require.NoError(t, err)
			fixture, err := os.ReadFile("fixtures/legacy.sql")
			require.NoError(t, err)
			_, err = db.Exec(string(fixture))
			require.NoError(t, err)
			raw := []byte("{\n  \"integer\": 9007199254740993, \"escaped\": \"\\u4f60\", \"fraction\": 1e+00\n}\n")
			_, err = db.Exec(`INSERT INTO session_model_input_receipts VALUES ('thread','operation',1,'external.driver','2.3.4',?,'2026-10-22T01:02:04Z')`, raw)
			require.NoError(t, err)
			require.NoError(t, db.Close())
			sourceBytes, err := os.ReadFile(source)
			require.NoError(t, err)
			sourceHash := sha256.Sum256(sourceBytes)
			dir := t.TempDir()
			documents, err := persistence.Open(ctx, dir, backend)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, documents.Close()) })
			_, err = session.MigrateLegacySQLite(ctx, legacy, documents, session.NewLegacyInferenceConverter(), toolstate.NewLegacyConverter())
			require.ErrorIs(t, err, persistence.ErrMigrationRequired, "missing execution converter must not partially migrate sessions")
			empty, err := documents.Export(ctx)
			require.NoError(t, err)
			require.Zero(t, empty.Revision)
			require.Empty(t, empty.Documents)
			migrate := func(target persistence.Documents) (session.LegacyMigrationResult, error) {
				return session.MigrateLegacySQLite(ctx, legacy, target, session.NewLegacyInferenceConverter(), toolstate.NewLegacyConverter(), execution.NewLegacyConverter())
			}
			_, err = migrate(lostMigrationReply{documents})
			var uncertain *persistence.CommitError
			require.ErrorAs(t, err, &uncertain)
			require.NoError(t, documents.Close())
			documents, err = persistence.Open(ctx, dir, backend)
			require.NoError(t, err)
			receipt, err := documents.Commit(ctx, uncertain.Transaction)
			require.NoError(t, err)
			require.Equal(t, persistence.Revision(1), receipt.Revision, "joint import is exactly one atomic durable commit")
			result, err := migrate(documents)
			require.NoError(t, err)
			require.True(t, result.AlreadyMigrated)
			snapshot, err := documents.Export(ctx)
			require.NoError(t, err)
			require.Len(t, snapshot.Receipts, 1)
			var archivedRows, archivedSchemas int
			for _, row := range snapshot.Documents {
				if row.Namespace == "wuu.legacy_sqlite_rows.v1" {
					archivedRows++
				}
				if row.Namespace == "wuu.legacy_sqlite_schema.v1" {
					archivedSchemas++
				}
			}
			var sourceRows int
			for _, count := range result.Rows {
				sourceRows += count
			}
			require.Equal(t, sourceRows, archivedRows)
			require.Equal(t, len(result.Rows), archivedSchemas)

			sessions := session.NewStore(ctx, documents)
			thread, found, err := sessions.Find("thread")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "Legacy identity", thread.Title)
			require.Equal(t, "legacy-turn", thread.LatestCompletedTurnID)
			binding, found, err := sessions.LoadDriverBinding("thread")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "external.loop", binding.ProviderID)
			require.Equal(t, "explicit-profile", binding.Profile)
			checkpoint, found, err := sessions.LoadDriverCheckpoint("thread")
			require.NoError(t, err)
			require.True(t, found)
			require.JSONEq(t, `{"continuation":"preserved"}`, string(checkpoint.State))
			inputs, err := sessions.LoadModelInputReceipts("thread")
			require.NoError(t, err)
			require.Len(t, inputs, 1)
			require.Equal(t, raw, []byte(inputs[0].Payload))
			history, err := sessions.LoadHistoryRecords("thread", true)
			require.NoError(t, err)
			require.Len(t, history, 3)
			require.Equal(t, 3, history[2].Seq)
			control, found, err := sessions.ReadControl("thread")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, int64(7), control.Revision)
			held, err := sessions.LoadHeldUserWork("thread")
			require.NoError(t, err)
			require.Len(t, held, 1)
			require.True(t, held[0].DeliveryUnconfirmed)
			require.Equal(t, []byte(`{"content":"held bytes"}`), held[0].MessageJSON)
			pending, err := sessions.PendingInbox("thread")
			require.NoError(t, err)
			require.Len(t, pending, 2)
			require.Equal(t, "z-first", pending[0].ClientID)
			require.Equal(t, "a-second", pending[1].ClientID)
			work, found, err := sessions.ProjectWorkOperation("work-operation")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "work-1", work.ID)
			require.Equal(t, 3, work.Version)

			workflowView, err := documents.Read(ctx, persistence.Query{Namespace: "wuu.inference_workflows.v1", Keys: []string{"workflow"}})
			require.NoError(t, err)
			require.Len(t, workflowView.Documents, 1)
			var workflow struct {
				OperationIDs                      []string
				KnownUsageTokens, UnknownBillable int64
				Used                              map[string]int64
			}
			require.NoError(t, json.Unmarshal(workflowView.Documents[0].Value, &workflow))
			require.Equal(t, []string{"operation"}, workflow.OperationIDs)
			require.Equal(t, int64(37), workflow.KnownUsageTokens)
			require.Equal(t, int64(1), workflow.UnknownBillable)
			require.Equal(t, int64(2), workflow.Used["attempts"])
			ledger, err := toolledger.New(documents, "thread")
			require.NoError(t, err)
			require.NoError(t, ledger.Reconcile(ctx))
			decision, err := ledger.DecideReplay(ctx, "batch")
			require.NoError(t, err)
			require.Equal(t, toolledger.ReplayReasonInvocationUnknown, decision.Reason)
			require.Equal(t, toolledger.ReplayBlock, decision.Action)
			require.Equal(t, []string{"invocation"}, decision.BlockingInvocationIDs)
			runs, err := execution.NewStore(documents)
			require.NoError(t, err)
			run, err := runs.Get(ctx, "legacy-run")
			require.NoError(t, err)
			require.Len(t, run.Turns, 1)
			require.Equal(t, "legacy-turn", run.Turns[0].TurnID)
			journal, err := sessions.NewInferenceJournalRuntime("acceptance")
			require.NoError(t, err)
			params := execution.CreateParams{RuntimeID: journal.RuntimeID(), ThreadID: "thread", Request: execution.Request{Mode: execution.ModeStart, HasPrompt: true}, Runtime: run.Runtime, Workspace: run.Workspace}
			_, err = runs.Create(ctx, params)
			require.ErrorIs(t, err, execution.ErrConflict)
			recovered, err := runs.ReconcileOrphans(ctx, journal.RuntimeID(), time.Now())
			require.NoError(t, err)
			require.Len(t, recovered, 1)
			successor, err := runs.Create(ctx, params)
			require.NoError(t, err)
			_, err = runs.AttachTurn(ctx, successor.ID, "thread", "legacy-turn", time.Now())
			require.ErrorIs(t, err, execution.ErrConflict)
			require.NoError(t, journal.Close())
			_, err = sessions.UpdateTitle("thread", "post-migration edit")
			require.NoError(t, err)
			result, err = migrate(documents)
			require.NoError(t, err)
			require.True(t, result.AlreadyMigrated)
			thread, _, err = sessions.Find("thread")
			require.NoError(t, err)
			require.Equal(t, "post-migration edit", thread.Title)
			beforeTransfer, err := documents.Export(ctx)
			require.NoError(t, err)
			require.NoError(t, documents.Close())
			target := "jsonl"
			if backend == target {
				target = "sqlite"
			}
			require.NoError(t, persistence.Migrate(ctx, dir, backend, target))
			documents, err = persistence.Open(ctx, dir, target)
			require.NoError(t, err)
			afterTransfer, err := documents.Export(ctx)
			require.NoError(t, err)
			require.Equal(t, beforeTransfer, afterTransfer)
			unchanged, err := os.ReadFile(source)
			require.NoError(t, err)
			require.Equal(t, sourceHash, sha256.Sum256(unchanged))
			t.Logf("legacy -> %s -> %s: %d source tables/%d raw rows retained; uncertain commit resolved, all domain converters/recovery verified", backend, target, archivedSchemas, archivedRows)
		})
	}
}
