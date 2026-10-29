package subagent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestWorkerLedgerRecoveryFencesUnknownBeforeAdmission(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	ledger, err := toolledger.New(docs, "worker")
	if err != nil {
		t.Fatal(err)
	}
	batch, err := ledger.BeginBatch(ctx, "turn", 0)
	if err != nil {
		t.Fatal(err)
	}
	call, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "call", Name: "read_file", Arguments: `{}`}, toolledger.ReplayAtMostOnce)
	if err != nil {
		t.Fatal(err)
	}
	if err = ledger.Start(ctx, call.ID); err != nil {
		t.Fatal(err)
	}
	intent := toolledger.ObservationIntent{OperationID: "operation", WorkerInstance: "worker-instance", Actor: "worker", EnvironmentIdentity: "env"}
	if err = ledger.StageObservation(ctx, call.ID, intent, toolresult.FromText("staged")); err != nil {
		t.Fatal(err)
	}
	called := false
	manager := NewManagerWithOptions(&fakeClient{}, "model", ManagerOptions{RecoverToolObservations: func(ctx context.Context, ledger *toolledger.Ledger) error {
		called = true
		decision, err := ledger.DecideReplay(ctx, batch)
		if err != nil || decision.Reason != toolledger.ReplayReasonInvocationUnknown {
			t.Fatalf("recover ran before reconcile: %+v %v", decision, err)
		}
		return errors.New("original worker unavailable")
	}})
	manager.agents["worker"] = &SubAgent{ID: "worker", toolLedger: ledger}
	if err = manager.ReconcileToolLedger(ctx, "worker"); err == nil || !called {
		t.Fatalf("recovery did not block admission: called=%v err=%v", called, err)
	}
	pending, err := ledger.PendingObservations(ctx)
	if err != nil || len(pending) != 1 || pending[0].Intent != intent {
		t.Fatalf("pending: %+v %v", pending, err)
	}
	manager.recoverToolObservations = nil
	if err = manager.ReconcileToolLedger(ctx, "worker"); err == nil {
		t.Fatal("missing recovery environment allowed admission")
	}
	called = false
	manager.recoverToolObservations = func(context.Context, *toolledger.Ledger) error { called = true; return nil }
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = manager.ReconcileToolLedger(canceled, "worker"); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("canceled admission reached recovery: called=%v err=%v", called, err)
	}
}
