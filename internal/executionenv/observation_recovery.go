package executionenv

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/blueberrycongee/wuu/internal/toolledger"
)

// ReadBatchRecoverer must bind the supplied identity to the authenticated
// original worker instance. Reconnecting may start a new worker, but recovery
// must not run against a different instance.
type ReadBatchRecoverer interface {
	RecoverReadBatch(context.Context, ReadBatchIdentity) (*PreparedReadBatch, error)
}

// RecoverLedgerObservations is called after Reconcile, under the execution
// owner's lease. It never dispatches a commit: an uncommitted batch is fenced
// with a confirmed abort before the ledger is allowed to resolve it.
func RecoverLedgerObservations(ctx context.Context, ledger *toolledger.Ledger, recoverer ReadBatchRecoverer) error {
	if ledger == nil {
		return nil
	}
	pending, err := ledger.PendingObservations(ctx)
	if err != nil {
		return err
	}
	if len(pending) != 0 && recoverer == nil {
		return errors.New("remote observation outcome unknown: original execution environment unavailable")
	}
	for _, item := range pending {
		intent := item.Intent
		identity := ReadBatchIdentity{OperationID: intent.OperationID, WorkerInstance: intent.WorkerInstance, Actor: intent.Actor, EnvironmentIdentity: intent.EnvironmentIdentity}
		batch, err := recoverer.RecoverReadBatch(ctx, identity)
		if err != nil {
			return fmt.Errorf("remote observation outcome unknown: %w", err)
		}
		state, err := batch.Status(ctx)
		if err != nil {
			return fmt.Errorf("remote observation outcome unknown: %w", err)
		}
		committed := false
		switch state {
		case "committed":
			committed = true
		case "aborted":
		case "prepared":
			if err := batch.Abort(ctx); err != nil {
				return fmt.Errorf("remote observation abort outcome unknown: %w", err)
			}
		default:
			return fmt.Errorf("remote observation outcome unknown: worker state %q", state)
		}
		if err := ledger.ResolveObservation(ctx, item.InvocationID, intent, committed); err != nil {
			return fmt.Errorf("remote observation ledger outcome unknown: %w", err)
		}
		// Terminal worker records are bounded even if this best-effort release fails.
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = batch.Release(cleanup)
		cancel()
	}
	return nil
}
