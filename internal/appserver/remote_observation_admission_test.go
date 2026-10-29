package appserver

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestThreadAdmissionBlocksUnrecoverableRemoteObservation(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := rt.Sessions.CreateWithMetadata("observation-admission", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := toolledger.New(rt.Documents, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	batch, err := ledger.BeginBatch(ctx, "turn", 0)
	if err != nil {
		t.Fatal(err)
	}
	call, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "call", Name: "read_file", Arguments: `{}`}, toolledger.ReplayAtMostOnce)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.FinalizeBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Start(ctx, call.ID); err != nil {
		t.Fatal(err)
	}
	intent := toolledger.ObservationIntent{OperationID: "operation", WorkerInstance: "old-worker", Actor: sess.ID, EnvironmentIdentity: "old-env"}
	if err := ledger.StageObservation(ctx, call.ID, intent, toolresult.FromText("staged")); err != nil {
		t.Fatal(err)
	}
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	th, err := srv.ensureThreadLoaded(testRuntimeState(t, rt), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.ensureThreadRuntime(th); err != nil {
		t.Fatal(err)
	}
	assertToolInvocationState(t, rt.Documents, call.ID, toolledger.InvocationRunning)
	th.mu.Lock()
	acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(th, 0)
	th.mu.Unlock()
	if err != nil || !acquired {
		t.Fatalf("lease: %v %v", acquired, err)
	}
	defer func() { th.mu.Lock(); th.releaseThreadExecutionLeaseLocked(); th.mu.Unlock() }()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := srv.ensureThreadRuntimeAfterAdmission(canceled, th); err == nil {
		t.Fatal("canceled admission continued")
	}
	assertToolInvocationState(t, rt.Documents, call.ID, toolledger.InvocationRunning)
	if _, err := srv.ensureThreadRuntimeAfterAdmission(context.Background(), th); err == nil {
		t.Fatal("unavailable original worker allowed admission")
	}
	assertToolInvocationState(t, rt.Documents, call.ID, toolledger.InvocationInterruptedUnknown)
	pending, err := ledger.PendingObservations(ctx)
	if err != nil || len(pending) != 1 || pending[0].Intent != intent {
		t.Fatalf("pending after failed admission: %+v %v", pending, err)
	}
}
