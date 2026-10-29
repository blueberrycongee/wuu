package toolledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

// ObservationIntent identifies a worker-side operation. Transport credentials
// and individual prepared-read receipts must never be stored here.
type ObservationIntent struct {
	OperationID, WorkerInstance, Actor, EnvironmentIdentity string
}

type PendingObservation struct {
	InvocationID string
	Intent       ObservationIntent
}

func (i ObservationIntent) valid() bool {
	for _, field := range []string{i.OperationID, i.WorkerInstance, i.Actor, i.EnvironmentIdentity} {
		if strings.TrimSpace(field) != field || field == "" || len(field) > 512 {
			return false
		}
	}
	return true
}

func observationIntent(o *toolstate.Observation) ObservationIntent {
	return ObservationIntent{o.OperationID, o.WorkerInstance, o.Actor, o.EnvironmentIdentity}
}

func (l *Ledger) AuthorizeObservation(ctx context.Context, intent ObservationIntent) error {
	if l == nil || !intent.valid() || intent.Actor != l.ownerID {
		return errors.New("invalid observation authorization identity")
	}
	id := toolctx.SettlementIdentity{OperationID: intent.OperationID, WorkerInstance: intent.WorkerInstance, Actor: intent.Actor, EnvironmentIdentity: intent.EnvironmentIdentity}
	if err := toolctx.AuthorizeObservation(ctx, id, l); err != nil {
		return fmt.Errorf("observation result unavailable: %w", err)
	}
	return nil
}

// AuthorizeInvocations checks observation-backed results before a live
// callback or a model request receives them, including after history storage.
func (l *Ledger) AuthorizeInvocations(ctx context.Context, ids []string) error {
	if l == nil || len(ids) == 0 {
		return nil
	}
	var observations []ObservationIntent
	err := l.write(ctx, func(tx *persistence.Tx) error {
		observations = nil
		for _, id := range ids {
			if strings.TrimSpace(id) == "" {
				continue
			}
			inv, err := l.invocation(tx, id)
			if err != nil {
				return err
			}
			if inv.Observation != nil && inv.Observation.Outcome == "committed" {
				observations = append(observations, observationIntent(inv.Observation))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, intent := range observations {
		if err := l.AuthorizeObservation(ctx, intent); err != nil {
			return err
		}
	}
	return nil
}

// ForgetProjectedObservationBindings drops retained closures only after the
// corresponding history write has durably marked the invocation projected.
func (l *Ledger) ForgetProjectedObservationBindings(ctx context.Context, ids []string) error {
	if l == nil || len(ids) == 0 {
		return nil
	}
	var done []ObservationIntent
	err := l.write(ctx, func(tx *persistence.Tx) error {
		done = nil
		for _, id := range ids {
			if strings.TrimSpace(id) == "" {
				continue
			}
			inv, err := l.invocation(tx, id)
			if err != nil {
				return err
			}
			if inv.ProjectedAt > 0 && inv.Observation != nil {
				done = append(done, observationIntent(inv.Observation))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, i := range done {
		toolctx.ForgetObservationAuthorization(toolctx.SettlementIdentity{OperationID: i.OperationID, WorkerInstance: i.WorkerInstance, Actor: i.Actor, EnvironmentIdentity: i.EnvironmentIdentity}, l)
	}
	return nil
}

// StageObservation atomically saves the identity and final validated result
// before the host sends a commit. Repeating the identical stage is harmless.
func (l *Ledger) StageObservation(ctx context.Context, invocationID string, intent ObservationIntent, result toolresult.Result) error {
	if l == nil || strings.TrimSpace(invocationID) == "" || !intent.valid() || intent.Actor != l.ownerID {
		return errors.New("valid observation intent and invocation are required")
	}
	if result.IsError {
		return errors.New("cannot stage an unsuccessful observation result")
	}
	if err := result.Validate(); err != nil {
		return fmt.Errorf("validate staged observation result: %w", err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return l.write(ctx, func(tx *persistence.Tx) error {
		invocation, err := l.invocation(tx, invocationID)
		if err != nil {
			return err
		}
		if invocation.Observation != nil {
			previous := invocation.Observation
			encoded, err := json.Marshal(previous.Result)
			if err != nil {
				return err
			}
			if invocation.State == string(InvocationRunning) && previous.Outcome == "" && observationIntent(previous) == intent && string(encoded) == string(payload) {
				return nil
			}
			return errors.New("observation intent or result conflicts with existing stage")
		}
		if invocation.State != string(InvocationRunning) || invocation.ParentInvocationID != "" || invocation.ReplayPolicy != string(ReplayAtMostOnce) {
			return errors.New("observation staging requires a running top-level at-most-once invocation")
		}
		copy := result.Clone()
		invocation.Observation = &toolstate.Observation{OperationID: intent.OperationID, WorkerInstance: intent.WorkerInstance, Actor: intent.Actor, EnvironmentIdentity: intent.EnvironmentIdentity, Result: &copy}
		return tx.Put(toolstate.Invocations, invocationID, invocation)
	})
}

// PendingObservations returns unresolved intents owned by this ledger. It does
// not decide whether the worker outcome can be safely recovered.
func (l *Ledger) PendingObservations(ctx context.Context) ([]PendingObservation, error) {
	if l == nil {
		return nil, errors.New("tool ledger is required")
	}
	var out []PendingObservation
	err := l.write(ctx, func(tx *persistence.Tx) error {
		out = nil
		batches, err := toolstate.ReadOwnerBatches(tx, l.ownerID)
		if err != nil {
			return err
		}
		for _, batch := range batches {
			invocations, err := batchInvocations(tx, batch)
			if err != nil {
				return err
			}
			for _, invocation := range invocations {
				o := invocation.Observation
				if o == nil || o.Outcome != "" {
					continue
				}
				if (invocation.State != string(InvocationRunning) && invocation.State != string(InvocationInterruptedUnknown)) || o.Result == nil || !observationIntent(o).valid() || o.Actor != l.ownerID {
					return fmt.Errorf("%w: invalid pending observation on %q", persistence.ErrCorrupt, invocation.ID)
				}
				out = append(out, PendingObservation{InvocationID: invocation.ID, Intent: observationIntent(o)})
			}
		}
		return nil
	})
	return out, err
}

// MarkObservationUnknown fences replay after a dispatched commit loses its
// acknowledgement. A failed write is recovered by the usual reconciliation.
func (l *Ledger) MarkObservationUnknown(ctx context.Context, invocationID string, intent ObservationIntent) error {
	if l == nil || strings.TrimSpace(invocationID) == "" || !intent.valid() || intent.Actor != l.ownerID {
		return errors.New("valid observation intent and invocation are required")
	}
	return l.write(ctx, func(tx *persistence.Tx) error {
		invocation, err := l.invocation(tx, invocationID)
		if err != nil {
			return err
		}
		o := invocation.Observation
		if o == nil || observationIntent(o) != intent || o.Outcome != "" {
			return errors.New("pending observation identity mismatch")
		}
		if invocation.State == string(InvocationInterruptedUnknown) {
			return nil
		}
		if invocation.State != string(InvocationRunning) {
			return errors.New("observation invocation is not running")
		}
		invocation.State = string(InvocationInterruptedUnknown)
		return tx.Put(toolstate.Invocations, invocationID, invocation)
	})
}

// ResolveObservation records only a caller-confirmed worker outcome. A caller
// must authenticate the captured worker and fence an abort before invoking it.
func (l *Ledger) ResolveObservation(ctx context.Context, invocationID string, intent ObservationIntent, committed bool) error {
	if l == nil || strings.TrimSpace(invocationID) == "" || !intent.valid() || intent.Actor != l.ownerID {
		return errors.New("valid observation intent and invocation are required")
	}
	outcome := "aborted"
	if committed {
		outcome = "committed"
	}
	now := time.Now().UTC().UnixMilli()
	return l.write(ctx, func(tx *persistence.Tx) error {
		invocation, err := l.invocation(tx, invocationID)
		if err != nil {
			return err
		}
		o := invocation.Observation
		if o == nil || observationIntent(o) != intent {
			return errors.New("observation identity mismatch")
		}
		if o.Outcome != "" {
			if o.Outcome == outcome {
				return nil
			}
			return errors.New("observation outcome conflicts with confirmed outcome")
		}
		if invocation.State != string(InvocationRunning) && invocation.State != string(InvocationInterruptedUnknown) {
			return errors.New("observation invocation is not recoverable")
		}
		if o.Result == nil || o.Result.IsError || o.Result.Validate() != nil {
			return fmt.Errorf("%w: invalid staged observation result", persistence.ErrCorrupt)
		}
		result := toolresult.FromErrorText("remote read observation was aborted")
		state := InvocationFailed
		if committed {
			result, state = o.Result.Clone(), InvocationSucceeded
		}
		o.Outcome, o.Result = outcome, nil
		invocation.State, invocation.Result, invocation.SettledAt, invocation.ProjectionSafetyVersion = string(state), &result, now, 1
		if err := tx.Put(toolstate.Invocations, invocationID, invocation); err != nil {
			return err
		}
		batch, err := l.batch(tx, invocation.BatchID)
		if err != nil {
			return err
		}
		return settleDocumentBatch(tx, batch, now)
	})
}
