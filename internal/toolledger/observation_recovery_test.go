package toolledger

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

// Failed, interrupted and ambiguous remote transactions must not expose a
// result or permit replay until the same worker confirms its terminal state.
func TestObservationIntentSurvivesRestartAndRequiresMatchingConfirmation(t *testing.T) {
	forEachLedgerStore(t, func(t *testing.T, docs *ledgerTestDocuments) {
		ctx := context.Background()
		ledger, err := New(docs, "owner")
		if err != nil {
			t.Fatal(err)
		}
		batch, err := ledger.BeginBatch(ctx, "turn", 0)
		if err != nil {
			t.Fatal(err)
		}
		call, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "call", Name: "run_code", Arguments: `{}`}, ReplayAtMostOnce)
		if err != nil {
			t.Fatal(err)
		}
		if err = ledger.FinalizeBatch(ctx, batch); err != nil {
			t.Fatal(err)
		}
		if err = ledger.Start(ctx, call.ID); err != nil {
			t.Fatal(err)
		}
		intent := ObservationIntent{OperationID: "operation", WorkerInstance: "worker-one", Actor: "owner", EnvironmentIdentity: "env"}
		result := toolresult.FromText("finalized model result")
		if err = ledger.StageObservation(ctx, call.ID, intent, result); err != nil {
			t.Fatal(err)
		}
		if err = ledger.StageObservation(ctx, call.ID, intent, result); err != nil {
			t.Fatalf("idempotent stage: %v", err)
		}
		if err = ledger.StageObservation(ctx, call.ID, intent, toolresult.FromText("different")); err == nil {
			t.Fatal("changed stage accepted")
		}
		if err = ledger.Settle(ctx, call.ID, result); err == nil {
			t.Fatal("ordinary settlement bypassed pending observation")
		}
		if pending, err := ledger.PendingProjection(ctx); err != nil || len(pending) != 0 {
			t.Fatalf("premature projection: %+v %v", pending, err)
		}
		view, err := docs.Read(ctx, persistence.Query{Namespace: "wuu.tool_invocations.v1", Keys: []string{call.ID}})
		if err != nil || len(view.Documents) != 1 {
			t.Fatalf("stored invocation: %+v %v", view, err)
		}
		encoded, err := json.Marshal(view.Documents[0])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(encoded)), "receipt") || strings.Contains(strings.ToLower(string(encoded)), "token") {
			t.Fatalf("intent contains a credential field: %s", encoded)
		}
		restartLedgerStore(t, docs)
		ledger, err = New(docs, "owner")
		if err != nil {
			t.Fatal(err)
		}
		if err = ledger.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		if err = ledger.StageObservation(ctx, call.ID, intent, result); err == nil {
			t.Fatal("late stage retry escaped recovery fence")
		}
		pending, err := ledger.PendingObservations(ctx)
		if err != nil || len(pending) != 1 || pending[0].InvocationID != call.ID || pending[0].Intent != intent {
			t.Fatalf("pending intent: %+v %v", pending, err)
		}
		if err = ledger.ValidateContinuation(ctx); err == nil {
			t.Fatal("unknown outcome allowed replay")
		}
		wrong := intent
		wrong.WorkerInstance = "worker-two"
		if err = ledger.ResolveObservation(ctx, call.ID, wrong, true); err == nil {
			t.Fatal("different worker settled intent")
		}
		other, err := New(docs, "other")
		if err != nil {
			t.Fatal(err)
		}
		if err = other.ResolveObservation(ctx, call.ID, intent, true); err == nil {
			t.Fatal("other owner settled intent")
		}
		if err = ledger.ResolveObservation(ctx, call.ID, intent, true); err != nil {
			t.Fatal(err)
		}
		if err = ledger.ResolveObservation(ctx, call.ID, intent, true); err != nil {
			t.Fatalf("idempotent resolution: %v", err)
		}
		if err = ledger.ResolveObservation(ctx, call.ID, intent, false); err == nil {
			t.Fatal("conflicting resolution accepted")
		}
		projected, err := ledger.PendingProjection(ctx)
		if err != nil || len(projected) != 1 || projected[0].Result.TextProjection() != "finalized model result" {
			t.Fatalf("confirmed projection: %+v %v", projected, err)
		}
		if err = ledger.ValidateContinuation(ctx); err != nil {
			t.Fatalf("confirmed outcome still unknown: %v", err)
		}
		pending, err = ledger.PendingObservations(ctx)
		if err != nil || len(pending) != 0 {
			t.Fatalf("resolved intent still pending: %+v %v", pending, err)
		}
	})
}

func TestObservationIntentAbortedAndInvalidInputs(t *testing.T) {
	forEachLedgerStore(t, func(t *testing.T, docs *ledgerTestDocuments) {
		ctx := context.Background()
		ledger, err := New(docs, "owner")
		if err != nil {
			t.Fatal(err)
		}
		batch, err := ledger.BeginBatch(ctx, "turn", 0)
		if err != nil {
			t.Fatal(err)
		}
		call, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "call", Name: "read_file", Arguments: `{}`}, ReplayAtMostOnce)
		if err != nil {
			t.Fatal(err)
		}
		intent := ObservationIntent{OperationID: "operation", WorkerInstance: "worker", Actor: "owner", EnvironmentIdentity: "env"}
		if err = ledger.StageObservation(ctx, call.ID, intent, toolresult.FromText("value")); err == nil {
			t.Fatal("unstarted invocation accepted")
		}
		if err = ledger.FinalizeBatch(ctx, batch); err != nil {
			t.Fatal(err)
		}
		if err = ledger.Start(ctx, call.ID); err != nil {
			t.Fatal(err)
		}
		invalid := intent
		invalid.WorkerInstance = ""
		if err = ledger.StageObservation(ctx, call.ID, invalid, toolresult.FromText("value")); err == nil {
			t.Fatal("missing worker accepted")
		}
		if err = ledger.StageObservation(ctx, call.ID, intent, toolresult.FromErrorText("failed")); err == nil {
			t.Fatal("error result accepted")
		}
		if err = ledger.StageObservation(ctx, call.ID, intent, toolresult.FromText("value")); err != nil {
			t.Fatal(err)
		}
		if err = ledger.ResolveObservation(ctx, call.ID, intent, false); err != nil {
			t.Fatal(err)
		}
		if err = ledger.ResolveObservation(ctx, call.ID, intent, false); err != nil {
			t.Fatal(err)
		}
		pending, err := ledger.PendingProjection(ctx)
		if err != nil || len(pending) != 1 || !pending[0].Result.IsError || strings.Contains(pending[0].Result.TextProjection(), "value") {
			t.Fatalf("aborted projection: %+v %v", pending, err)
		}
	})
}

func TestObservationIntentLostPersistenceAcknowledgement(t *testing.T) {
	forEachLedgerStore(t, func(t *testing.T, docs *ledgerTestDocuments) {
		ctx := context.Background()
		ledger, err := New(docs, "owner")
		if err != nil {
			t.Fatal(err)
		}
		batch, err := ledger.BeginBatch(ctx, "turn", 0)
		if err != nil {
			t.Fatal(err)
		}
		call, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "call", Name: "read_file", Arguments: `{}`}, ReplayAtMostOnce)
		if err != nil {
			t.Fatal(err)
		}
		if err = ledger.FinalizeBatch(ctx, batch); err != nil {
			t.Fatal(err)
		}
		if err = ledger.Start(ctx, call.ID); err != nil {
			t.Fatal(err)
		}
		intent := ObservationIntent{OperationID: "operation", WorkerInstance: "worker", Actor: "owner", EnvironmentIdentity: "env"}
		result := toolresult.FromText("verified result")
		lost, err := New(&lostLedgerResponse{Documents: docs}, "owner")
		if err != nil {
			t.Fatal(err)
		}
		if err = lost.StageObservation(ctx, call.ID, intent, result); err == nil {
			t.Fatal("stage acknowledgement unexpectedly delivered")
		}
		if err = ledger.StageObservation(ctx, call.ID, intent, result); err != nil {
			t.Fatalf("stage retry: %v", err)
		}
		if err = lost.ResolveObservation(ctx, call.ID, intent, true); err == nil {
			t.Fatal("resolution acknowledgement unexpectedly delivered")
		}
		restartLedgerStore(t, docs)
		ledger, err = New(docs, "owner")
		if err != nil {
			t.Fatal(err)
		}
		if err = ledger.ResolveObservation(ctx, call.ID, intent, true); err != nil {
			t.Fatalf("resolution retry: %v", err)
		}
		projection, err := ledger.PendingProjection(ctx)
		if err != nil || len(projection) != 1 || projection[0].Result.TextProjection() != "verified result" {
			t.Fatalf("projection after lost response: %+v %v", projection, err)
		}
	})
}

func TestObservationIntentCanMarkUnknownWithoutRestart(t *testing.T) {
	forEachLedgerStore(t, func(t *testing.T, docs *ledgerTestDocuments) {
		ctx := context.Background()
		ledger, err := New(docs, "owner")
		if err != nil {
			t.Fatal(err)
		}
		batch, err := ledger.BeginBatch(ctx, "turn", 0)
		if err != nil {
			t.Fatal(err)
		}
		call, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "call", Name: "read_file", Arguments: `{}`}, ReplayAtMostOnce)
		if err != nil {
			t.Fatal(err)
		}
		if err = ledger.FinalizeBatch(ctx, batch); err != nil {
			t.Fatal(err)
		}
		if err = ledger.Start(ctx, call.ID); err != nil {
			t.Fatal(err)
		}
		intent := ObservationIntent{OperationID: "operation", WorkerInstance: "worker", Actor: "owner", EnvironmentIdentity: "env"}
		if err = ledger.StageObservation(ctx, call.ID, intent, toolresult.FromText("verified")); err != nil {
			t.Fatal(err)
		}
		wrong := intent
		wrong.Actor = "other"
		if err = ledger.MarkObservationUnknown(ctx, call.ID, wrong); err == nil {
			t.Fatal("different actor marked unknown")
		}
		if err = ledger.MarkObservationUnknown(ctx, call.ID, intent); err != nil {
			t.Fatal(err)
		}
		if err = ledger.MarkObservationUnknown(ctx, call.ID, intent); err != nil {
			t.Fatal(err)
		}
		decision, err := ledger.DecideReplay(ctx, batch)
		if err != nil || decision.Action != ReplayBlock || decision.Reason != ReplayReasonInvocationUnknown {
			t.Fatalf("unknown replay: %+v %v", decision, err)
		}
		pending, err := ledger.PendingObservations(ctx)
		if err != nil || len(pending) != 1 || pending[0].Intent != intent {
			t.Fatalf("pending: %+v %v", pending, err)
		}
		if err = ledger.ResolveObservation(ctx, call.ID, intent, true); err != nil {
			t.Fatal(err)
		}
		if err = ledger.MarkObservationUnknown(ctx, call.ID, intent); err == nil {
			t.Fatal("confirmed outcome marked unknown")
		}
	})
}

func TestObservationIntentRejectsAnotherActorAndCorruptStoredIdentity(t *testing.T) {
	forEachLedgerStore(t, func(t *testing.T, docs *ledgerTestDocuments) {
		ctx := context.Background()
		ledger, err := New(docs, "owner")
		if err != nil {
			t.Fatal(err)
		}
		batch, err := ledger.BeginBatch(ctx, "turn", 0)
		if err != nil {
			t.Fatal(err)
		}
		call, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "call", Name: "read_file", Arguments: `{}`}, ReplayAtMostOnce)
		if err != nil {
			t.Fatal(err)
		}
		if err = ledger.Start(ctx, call.ID); err != nil {
			t.Fatal(err)
		}
		intent := ObservationIntent{OperationID: "operation", WorkerInstance: "worker", Actor: "other-actor", EnvironmentIdentity: "env"}
		if err = ledger.StageObservation(ctx, call.ID, intent, toolresult.FromText("value")); err == nil {
			t.Fatal("another actor staged for this owner")
		}
		intent.Actor = "owner"
		if err = ledger.StageObservation(ctx, call.ID, intent, toolresult.FromText("value")); err != nil {
			t.Fatal(err)
		}
		if err = persistence.Update(ctx, docs, func(tx *persistence.Tx) error {
			var stored toolstate.Invocation
			found, err := tx.Get(toolstate.Invocations, call.ID, &stored)
			if err != nil {
				return err
			}
			if !found {
				t.Fatal("invocation missing")
			}
			stored.Observation.Actor = "other-actor"
			return tx.Put(toolstate.Invocations, call.ID, stored)
		}); err != nil {
			t.Fatal(err)
		}
		if _, err = ledger.PendingObservations(ctx); err == nil {
			t.Fatal("corrupt actor was returned to recoverer")
		}
		intent.Actor = "other-actor"
		if err = ledger.ResolveObservation(ctx, call.ID, intent, true); err == nil {
			t.Fatal("corrupt actor settled observation")
		}
	})
}
