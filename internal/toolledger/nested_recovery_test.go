package toolledger

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"testing"
)

func TestNestedRecoveryKeepsLocalKeyAndRejectsUnknownUnsafe(t *testing.T) {
	forEachLedgerStore(t, func(t *testing.T, documents *ledgerTestDocuments) {
		ctx := context.Background()
		ledger, _ := New(documents, "owner")
		parentCall := providers.ToolCall{ID: "parent", Name: "program", Arguments: `{}`}
		parents, err := ledger.AdmitBatch(ctx, "original/batch", "driver@1", 1, []providers.ToolCall{parentCall}, []ReplayPolicy{ReplayRepeatable})
		if err != nil {
			t.Fatal(err)
		}
		parent := parents[0]
		if err := ledger.StartAttempt(ctx, parent.ID, parent.Attempt); err != nil {
			t.Fatal(err)
		}
		childCall := providers.ToolCall{ID: "1", Name: "write", Arguments: `{"v":1}`}
		child, err := ledger.PrepareScopedChild(ctx, parent.ID, parent.Attempt, childCall, ReplayAtMostOnce)
		if err != nil {
			t.Fatal(err)
		}
		if err := ledger.StartAttempt(ctx, child.ID, child.Attempt); err != nil {
			t.Fatal(err)
		}
		if err := ledger.SettleAttempt(ctx, child.ID, child.Attempt, toolresult.FromText("written")); err != nil {
			t.Fatal(err)
		}
		lostCall := providers.ToolCall{ID: "2", Name: "read", Arguments: `{}`}
		lost, err := ledger.PrepareScopedChild(ctx, parent.ID, parent.Attempt, lostCall, ReplayRepeatable)
		if err != nil {
			t.Fatal(err)
		}
		if err := ledger.StartAttempt(ctx, lost.ID, lost.Attempt); err != nil {
			t.Fatal(err)
		}
		if err := ledger.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		parents, err = ledger.RecoverBatch(ctx, "original/batch", "driver@1", []providers.ToolCall{parentCall}, []ReplayPolicy{ReplayRepeatable})
		if err != nil {
			t.Fatal(err)
		}
		current := parents[0]
		if err := ledger.StartAttempt(ctx, current.ID, current.Attempt); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.PrepareScopedChild(ctx, parent.ID, parent.Attempt, providers.ToolCall{ID: "new", Name: "write", Arguments: `{}`}, ReplayAtMostOnce); err == nil {
			t.Fatal("fenced parent admitted child")
		}
		replay, err := ledger.PrepareScopedChild(ctx, current.ID, current.Attempt, childCall, ReplayAtMostOnce)
		if err != nil {
			t.Fatal(err)
		}
		if replay.ID != child.ID || replay.ProviderCallID != child.ProviderCallID || replay.Result.TextProjection() != "written" {
			t.Fatalf("settled child repeated: %+v", replay)
		}
		changed := childCall
		changed.Arguments = `{"v":2}`
		if _, err := ledger.PrepareScopedChild(ctx, current.ID, current.Attempt, changed, ReplayAtMostOnce); err == nil {
			t.Fatal("nested key changed intent")
		}
		if _, err := ledger.PrepareScopedChild(ctx, current.ID, current.Attempt, lostCall, ReplayAtMostOnce); err == nil {
			t.Fatal("current policy revocation ignored")
		}
		recovered, err := ledger.PrepareScopedChild(ctx, current.ID, current.Attempt, lostCall, ReplayRepeatable)
		if err != nil {
			t.Fatal(err)
		}
		if recovered.ID != lost.ID || recovered.Attempt <= lost.Attempt {
			t.Fatalf("nested attempt not fenced: %+v", recovered)
		}
	})
}
