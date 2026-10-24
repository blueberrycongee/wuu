package acceptance_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/stretchr/testify/require"
)

// Exercise multiple real domain consumers on the same authority. Failure cases:
// dropped identities/receipts during transfer, normalized model-input bytes,
// stale writers, lost admission indexes, or replay of an uncertain external call.
// This is a document-backend migration; it does not validate legacy SQL import or
// runtime composition wiring, which require their own product-level journeys.
func TestAcceptanceCrossDomainMigration(t *testing.T) {
	for _, source := range []string{"sqlite", "jsonl"} {
		t.Run(source, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			target := "jsonl"
			if source == target {
				target = "sqlite"
			}
			documents, err := persistence.Open(ctx, dir, source)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, documents.Close()) })
			stale, err := persistence.Open(ctx, dir, source)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, stale.Close()) })

			// A known operation receipt must survive all subsequent domain writes.
			tx := persistence.Transaction{ID: "acceptance-import-receipt", Writes: []persistence.Write{
				{Namespace: "acceptance.notes", Key: "original", Value: json.RawMessage(`{"value":"保全"}`)},
			}}
			receipt, err := documents.Commit(ctx, tx)
			require.NoError(t, err)
			sessions := session.NewStore(ctx, documents)
			const thread = "acceptance/thread:一"
			_, err = sessions.CreateInitialized(session.Session{ID: thread, CWD: "/acceptance", Title: "Retained identity"}, []session.HistoryRecord{{Role: "user", Content: "original prompt"}})
			require.NoError(t, err)
			binding := pluginprotocol.DriverBinding{Profile: "explicit", ProviderID: "example.driver", ContractVersion: 1, DriverID: "retained-loop", DriverVersion: "1.0.0"}
			require.NoError(t, sessions.BindDriver(thread, binding))
			checkpoint := session.DriverCheckpointRecord{ContractVersion: 1, DriverID: binding.DriverID, DriverVersion: binding.DriverVersion, State: json.RawMessage(`{"continuation":"retained"}`)}
			require.NoError(t, sessions.SaveDriverCheckpoint(thread, checkpoint))
			raw := json.RawMessage("{\n  \"number\": 1e+00, \"escaped\": \"\\u4f60\", \"ordered\": [ 1, 2 ]\n}\n")
			input := session.ModelInputReceiptRecord{OperationID: "model-operation", ContractVersion: 1, DriverID: binding.DriverID, DriverVersion: binding.DriverVersion, Payload: raw}
			require.NoError(t, sessions.SaveModelInputReceipt(thread, input))
			control, err := sessions.ChangeControl(thread, "coordinator", session.ControlActive, 0)
			require.NoError(t, err)
			_, err = sessions.AppendControlledHistoryRecord(thread, session.HistoryRecord{Role: "assistant", Content: "physical reply"}, &control)
			require.NoError(t, err)
			_, err = sessions.StoreHistoryCheckpointAtBaseline(thread, "compaction", []session.HistoryRecord{{Role: "assistant", Content: "summary"}}, 2)
			require.NoError(t, err)
			require.NoError(t, sessions.AppendHistoryRecord(thread, session.HistoryRecord{Role: "user", Content: "tail"}))
			stamp := time.Now().UTC()
			for _, id := range []string{"z-first", "a-second", "delivered"} {
				require.NoError(t, sessions.EnqueueInbox(session.InboxMessage{SessionID: thread, ClientID: id, Content: id, CreatedAt: stamp}))
			}
			require.NoError(t, sessions.SettleInbox("delivered", thread))

			ledger, err := toolledger.New(documents, thread)
			require.NoError(t, err)
			batch, err := ledger.BeginBatch(ctx, input.OperationID, 0)
			require.NoError(t, err)
			invocation, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "external-call", Name: "send_external_effect", Arguments: `{}`}, toolledger.ReplayAtMostOnce)
			require.NoError(t, err)
			require.NoError(t, ledger.FinalizeBatch(ctx, batch))
			require.NoError(t, ledger.Start(ctx, invocation.ID))

			journal, err := sessions.NewInferenceJournalRuntime("acceptance-workspace")
			require.NoError(t, err)
			runs, err := execution.NewStore(documents)
			require.NoError(t, err)
			params := execution.CreateParams{RuntimeID: journal.RuntimeID(), ThreadID: thread,
				Request:   execution.Request{Mode: execution.ModeStart, HasPrompt: true},
				Runtime:   execution.RuntimeManifest{Resolved: execution.Selection{Provider: "synthetic", Model: "local", PermissionMode: "read-only"}, ProtocolVersion: "wuu-app-server/v0.1"},
				Workspace: execution.WorkspaceRef{ID: "acceptance-workspace", Root: "/acceptance"}}
			run, err := runs.Create(ctx, params)
			require.NoError(t, err)
			_, err = runs.AttachTurn(ctx, run.ID, thread, "retained-turn", stamp)
			require.NoError(t, err)
			// Stop the writer and background flush before migrating its authority.
			require.NoError(t, journal.Close())
			before, err := documents.Export(ctx)
			require.NoError(t, err)
			require.NoError(t, documents.Close())
			require.NoError(t, persistence.Migrate(ctx, dir, source, target))
			_, err = stale.Commit(ctx, tx)
			require.ErrorIs(t, err, persistence.ErrBindingChanged)
			_, err = stale.Read(ctx, persistence.Query{Namespace: "acceptance.notes"})
			require.ErrorIs(t, err, persistence.ErrBindingChanged)

			documents, err = persistence.Open(ctx, dir, target)
			require.NoError(t, err)
			after, err := documents.Export(ctx)
			require.NoError(t, err)
			require.Equal(t, before, after, "all domain documents and durable operation receipts must survive")
			actualReceipt, err := documents.Commit(ctx, tx)
			require.NoError(t, err)
			require.Equal(t, receipt, actualReceipt)
			changed := tx
			changed.Writes = []persistence.Write{{Namespace: "acceptance.notes", Key: "original", Value: json.RawMessage(`{"value":"changed"}`)}}
			_, err = documents.Commit(ctx, changed)
			require.ErrorIs(t, err, persistence.ErrOperationConflict)

			sessions = session.NewStore(ctx, documents)
			actualBinding, found, err := sessions.LoadDriverBinding(thread)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, binding, actualBinding)
			actualCheckpoint, found, err := sessions.LoadDriverCheckpoint(thread)
			require.NoError(t, err)
			require.True(t, found)
			require.JSONEq(t, string(checkpoint.State), string(actualCheckpoint.State))
			inputs, err := sessions.LoadModelInputReceipts(thread)
			require.NoError(t, err)
			require.Len(t, inputs, 1)
			require.Equal(t, []byte(raw), []byte(inputs[0].Payload))
			require.NoError(t, sessions.SaveModelInputReceipt(thread, input))
			input.Payload = json.RawMessage(`{"number":1,"escaped":"你","ordered":[1,2]}`)
			require.Error(t, sessions.SaveModelInputReceipt(thread, input), "equivalent JSON is not the admitted byte receipt")
			physical, err := sessions.LoadHistoryRecords(thread, true)
			require.NoError(t, err)
			require.Len(t, physical, 4)
			history, err := sessions.LoadProviderHistorySnapshot(thread)
			require.NoError(t, err)
			require.Len(t, history.Records, 2)
			require.Equal(t, "summary", history.Records[0].Content)
			require.Equal(t, "tail", history.Records[1].Content)
			pending, err := sessions.PendingInbox(thread)
			require.NoError(t, err)
			require.Len(t, pending, 2)
			require.Equal(t, "z-first", pending[0].ClientID)
			require.Equal(t, "a-second", pending[1].ClientID)
			_, err = sessions.ChangeControl(thread, "coordinator", session.ControlTakenOver, control.Revision)
			require.NoError(t, err)
			_, err = sessions.AppendControlledHistoryRecord(thread, session.HistoryRecord{Role: "user", Content: "stale"}, &control)
			require.ErrorIs(t, err, session.ErrControlChanged)

			ledger, err = toolledger.New(documents, thread)
			require.NoError(t, err)
			require.NoError(t, ledger.Reconcile(ctx))
			decision, err := ledger.DecideReplay(ctx, batch)
			require.NoError(t, err)
			require.Equal(t, toolledger.ReplayBlock, decision.Action)
			require.Contains(t, decision.BlockingInvocationIDs, invocation.ID)
			journal, err = sessions.NewInferenceJournalRuntime("acceptance-workspace")
			require.NoError(t, err)
			defer func() { require.NoError(t, journal.Close()) }()
			params.RuntimeID = journal.RuntimeID()
			runs, err = execution.NewStore(documents)
			require.NoError(t, err)
			_, err = runs.Create(ctx, params)
			require.ErrorIs(t, err, execution.ErrConflict, "active-thread admission index must survive migration")
			_, err = runs.Complete(ctx, run.ID, execution.Result{FinalTurnID: "retained-turn"}, time.Now().UTC())
			require.NoError(t, err)
			successor, err := runs.Create(ctx, params)
			require.NoError(t, err)
			_, err = runs.AttachTurn(ctx, successor.ID, thread, "retained-turn", time.Now().UTC())
			require.ErrorIs(t, err, execution.ErrConflict, "a prior run retains ownership of its turn")
			_, err = runs.AttachTurn(ctx, successor.ID, thread, "successor-turn", time.Now().UTC())
			require.NoError(t, err)
			t.Logf("%s -> %s: %d documents, %d receipts retained; byte receipts, control, inbox, replay and execution indexes verified", source, target, len(after.Documents), len(after.Receipts))
		})
	}
}
