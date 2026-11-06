package toolledger

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

// Recovery must keep completed effects, require both declarations for uncertain
// effects, and fence a previous executor before admitting another attempt.
func TestExactBatchRecoveryKeepsReceiptsAndFencesAttempts(t *testing.T) {
	forEachLedgerStore(t, func(t *testing.T, documents *ledgerTestDocuments) {
		ctx := context.Background()
		ledger, _ := New(documents, "owner")
		calls := []providers.ToolCall{{ID: "write", Name: "write", Arguments: `{"value":1}`}, {ID: "read", Name: "read", Arguments: `{}`}}
		policies := []ReplayPolicy{ReplayAtMostOnce, ReplayRepeatable}
		admitted, err := ledger.AdmitBatch(ctx, "original/batch", "driver@1", 1, calls, policies)
		if err != nil {
			t.Fatal(err)
		}
		for _, invocation := range admitted {
			if err := ledger.StartAttempt(ctx, invocation.ID, invocation.Attempt); err != nil {
				t.Fatal(err)
			}
		}
		if err := ledger.SettleAttempt(ctx, admitted[0].ID, admitted[0].Attempt, toolresult.FromText("written once")); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.RecoverBatch(ctx, "original/batch", "driver@1", calls, policies); err == nil {
			t.Fatal("live writer recovered without reconciliation")
		}
		restartLedgerStore(t, documents)
		ledger, _ = New(documents, "owner")
		if err := ledger.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		restored, err := ledger.RecoverBatch(ctx, "original/batch", "driver@1", calls, policies)
		if err != nil {
			t.Fatal(err)
		}
		if restored[0].ID != admitted[0].ID || restored[0].Result.TextProjection() != "written once" || restored[0].Attempt != admitted[0].Attempt {
			t.Fatalf("settled receipt changed: %+v", restored[0])
		}
		if restored[1].ID != admitted[1].ID || restored[1].Attempt <= admitted[1].Attempt {
			t.Fatalf("retry did not preserve identity/fence attempt: %+v", restored[1])
		}
		if err := ledger.StartAttempt(ctx, restored[1].ID, restored[1].Attempt); err != nil {
			t.Fatal(err)
		}
		if err := ledger.SettleAttempt(ctx, admitted[1].ID, admitted[1].Attempt, toolresult.FromText("stale")); err == nil {
			t.Fatal("late finalizer settled new attempt")
		}
		if err := ledger.MarkUnknownAttempt(ctx, admitted[1].ID, admitted[1].Attempt); err == nil {
			t.Fatal("late failure fenced new attempt")
		}
		if err := ledger.SettleAttempt(ctx, restored[1].ID, restored[1].Attempt, toolresult.FromText("current")); err != nil {
			t.Fatal(err)
		}
		stored := readLedgerDocument[toolstate.Invocation](t, documents, toolstate.Invocations, restored[1].ID)
		if stored.Result.TextProjection() != "current" {
			t.Fatalf("result=%+v", stored)
		}
	})
}

func TestExactBatchRecoveryFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name           string
		saved, current ReplayPolicy
		change         string
	}{
		{"saved-unsafe", ReplayAtMostOnce, ReplayRepeatable, ""},
		{"current-revoked", ReplayRepeatable, ReplayAtMostOnce, ""},
		{"unknown-policy", ReplayPolicy("future"), ReplayRepeatable, ""},
		{"changed-arguments", ReplayRepeatable, ReplayRepeatable, "arguments"},
		{"changed-definition-owner", ReplayRepeatable, ReplayRepeatable, "scope"},
		{"foreign-owner", ReplayRepeatable, ReplayRepeatable, "owner"},
		{"missing-admission", ReplayRepeatable, ReplayRepeatable, "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forEachLedgerStore(t, func(t *testing.T, documents *ledgerTestDocuments) {
				ctx := context.Background()
				ledger, _ := New(documents, "owner")
				calls := []providers.ToolCall{{ID: "call", Name: "effect", Arguments: `{}`}}
				admitted, err := ledger.AdmitBatch(ctx, "original/batch", "driver@1", 1, calls, []ReplayPolicy{tc.saved})
				if tc.change == "" && tc.saved == ReplayPolicy("future") {
					if err == nil {
						t.Fatal("unknown policy admitted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := ledger.StartAttempt(ctx, admitted[0].ID, admitted[0].Attempt); err != nil {
					t.Fatal(err)
				}
				if err := ledger.Reconcile(ctx); err != nil {
					t.Fatal(err)
				}
				scope, operation := "driver@1", "original/batch"
				switch tc.change {
				case "arguments":
					calls[0].Arguments = `{"other":true}`
				case "scope":
					scope = "other@1"
				case "owner":
					ledger, _ = New(documents, "other")
				case "missing":
					operation = "missing/batch"
				}
				if _, err := ledger.RecoverBatch(ctx, operation, scope, calls, []ReplayPolicy{tc.current}); err == nil {
					t.Fatal("unproven recovery allowed")
				}
				stored := readLedgerDocument[toolstate.Invocation](t, documents, toolstate.Invocations, admitted[0].ID)
				if stored.State != string(InvocationInterruptedUnknown) || stored.Attempt != admitted[0].Attempt {
					t.Fatalf("blocked recovery mutated original: %+v", stored)
				}
			})
		})
	}
}

func TestExactBatchAdmissionIsImmutableAndNeverStartedProofSurvivesRestart(t *testing.T) {
	forEachLedgerStore(t, func(t *testing.T, documents *ledgerTestDocuments) {
		ctx := context.Background()
		ledger, _ := New(documents, "owner")
		calls := []providers.ToolCall{{ID: "call", Name: "unsafe", Arguments: `{}`}}
		admitted, err := ledger.AdmitBatch(ctx, "original/batch", "driver@1", 1, calls, []ReplayPolicy{ReplayAtMostOnce})
		if err != nil {
			t.Fatal(err)
		}
		if err := ledger.RequireSettled(ctx); err != nil {
			t.Fatalf("never-started intent fabricated an uncertain effect: %v", err)
		}
		if _, err := ledger.AdmitBatch(ctx, "original/batch", "driver@1", 1, calls, []ReplayPolicy{ReplayAtMostOnce}); err == nil {
			t.Fatal("duplicate admission accepted")
		}
		if err := ledger.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		restored, err := ledger.RecoverBatch(ctx, "original/batch", "driver@1", calls, []ReplayPolicy{ReplayAtMostOnce})
		if err != nil {
			t.Fatal(err)
		}
		if restored[0].ID != admitted[0].ID || restored[0].State != InvocationPrepared {
			t.Fatalf("lost never-started proof: %+v", restored)
		}
	})
}
