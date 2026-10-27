package toolledger

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

func TestProjectedInvocationDropsRecoveryPayload(t *testing.T) {
	forEachLedgerStore(t, func(t *testing.T, documents *ledgerTestDocuments) {
		ledger, err := New(documents, "thread-projected-storage")
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		batchID, err := ledger.BeginBatch(ctx, "operation-1", 1)
		if err != nil {
			t.Fatal(err)
		}
		invocation, err := ledger.Prepare(ctx, batchID, providers.ToolCall{ID: "call-1", Name: "read_file", Arguments: `{}`}, ReplayAtMostOnce)
		if err != nil {
			t.Fatal(err)
		}
		if err := ledger.FinalizeBatch(ctx, batchID); err != nil {
			t.Fatal(err)
		}
		if err := ledger.Start(ctx, invocation.ID); err != nil {
			t.Fatal(err)
		}
		result := toolresult.FromText("durable result")
		if err := ledger.Settle(ctx, invocation.ID, result); err != nil {
			t.Fatal(err)
		}
		if err := ledger.MarkProjected(ctx, []string{invocation.ID}); err != nil {
			t.Fatal(err)
		}
		stored := readLedgerDocument[toolstate.Invocation](t, documents, toolstate.Invocations, invocation.ID)
		if stored.Result != nil {
			t.Fatalf("projected recovery payload retained: %+v", stored.Result)
		}
		if err := ledger.Settle(ctx, invocation.ID, result); err != nil {
			t.Fatalf("idempotent settlement after payload cleanup: %v", err)
		}
	})
}
