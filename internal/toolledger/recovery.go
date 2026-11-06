package toolledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

func recoveryBatchID(owner, operation string) string {
	raw, _ := json.Marshal([]string{owner, operation})
	sum := sha256.Sum256(raw)
	return "tbatch-r-" + hex.EncodeToString(sum[:])
}

// AdmitBatch commits all intents before any effect can start. The stable owner /
// operation address is positive admission proof, not caller-provided authority.
// Streaming batches retain their separate conservative recovery contract.
func (l *Ledger) AdmitBatch(ctx context.Context, operation, scope string, step int, calls []providers.ToolCall, policies []ReplayPolicy) ([]Invocation, error) {
	if l == nil || operation == "" || scope == "" || len(calls) == 0 || len(calls) != len(policies) {
		return nil, errors.New("complete tool recovery admission is required")
	}
	if err := providers.ValidateAssistantToolCalls(calls); err != nil {
		return nil, err
	}
	for _, policy := range policies {
		if policy != ReplayAtMostOnce && policy != ReplayRepeatable {
			return nil, errors.New("unsupported exact tool replay policy")
		}
	}
	batchID, now := recoveryBatchID(l.ownerID, operation), time.Now().UTC().UnixMilli()
	ids := make([]string, len(calls))
	for i := range ids {
		ids[i] = newID("tinv")
	}
	var out []Invocation
	err := l.write(ctx, func(tx *persistence.Tx) error {
		out = nil
		var existing toolstate.Batch
		found, err := tx.Get(toolstate.Batches, batchID, &existing)
		if err != nil {
			return err
		}
		if found {
			return errors.New("tool batch was already admitted; use exact recovery")
		}
		batch := toolstate.Batch{ID: batchID, OwnerID: l.ownerID, OperationID: operation, RecoveryScope: scope, StepIndex: step, Status: string(BatchFinalized), CreatedAt: now, UpdatedAt: now, InvocationIDs: ids}
		for i, call := range calls {
			stored := toolstate.Invocation{ID: ids[i], BatchID: batchID, ProviderCallID: call.ID, ToolName: call.Name, ToolKind: call.Kind, Arguments: call.Arguments, ThoughtSignature: call.ThoughtSignature, ProviderItemProvider: call.ProviderItemProvider, ProviderItemModel: call.ProviderItemModel, ReplayPolicy: string(policies[i]), State: string(InvocationPrepared), PreparedAt: now, Attempt: 1}
			if err := tx.Put(toolstate.Invocations, stored.ID, stored); err != nil {
				return err
			}
			out = append(out, publicInvocation(stored))
		}
		return toolstate.PutBatch(tx, batch)
	})
	return out, err
}

// RecoverBatch requires exclusive execution ownership and prior reconciliation.
// It validates the whole batch before reopening any call. A missing admission or
// receipt is uncertainty, never proof that an effect did not happen.
func (l *Ledger) RecoverBatch(ctx context.Context, operation, scope string, calls []providers.ToolCall, current []ReplayPolicy) ([]Invocation, error) {
	if l == nil || operation == "" || scope == "" || len(calls) == 0 || len(calls) != len(current) {
		return nil, errors.New("complete tool recovery identity is required")
	}
	var out []Invocation
	err := l.write(ctx, func(tx *persistence.Tx) error {
		out = nil
		batch, err := l.batch(tx, recoveryBatchID(l.ownerID, operation))
		if err != nil {
			return err
		}
		if batch.OperationID != operation || batch.RecoveryScope != scope {
			return errors.New("tool recovery binding differs from admission")
		}
		stored, err := batchInvocations(tx, batch)
		if err != nil {
			return err
		}
		if len(stored) != len(calls) {
			return errors.New("tool recovery call count differs from admission")
		}
		for i, inv := range stored {
			call := calls[i]
			if inv.ParentInvocationID != "" || inv.ProviderCallID != call.ID || inv.ToolName != call.Name || inv.ToolKind != call.Kind || inv.Arguments != call.Arguments || inv.ThoughtSignature != call.ThoughtSignature || inv.ProviderItemProvider != call.ProviderItemProvider || inv.ProviderItemModel != call.ProviderItemModel {
				return errors.New("tool recovery intent differs from admission")
			}
			switch InvocationState(inv.State) {
			case InvocationSucceeded, InvocationFailed:
				if inv.Result == nil && inv.ProjectedAt == 0 {
					return fmt.Errorf("%w: settled tool receipt has no result", persistence.ErrCorrupt)
				}
			case InvocationPrepared, InvocationAbandoned:
				if inv.RunningAt != 0 && (inv.ReplayPolicy != string(ReplayRepeatable) || current[i] != ReplayRepeatable) {
					return errors.New("tool recovery has no never-started proof")
				}
			case InvocationInterruptedUnknown:
				if inv.ReplayPolicy != string(ReplayRepeatable) || current[i] != ReplayRepeatable {
					return &ReplayBlockedError{Decision: ReplayDecision{Action: ReplayBlock, Reason: ReplayReasonInvocationUnknown, BlockingInvocationIDs: []string{inv.ID}}}
				}
			default:
				return &ReplayBlockedError{Decision: ReplayDecision{Action: ReplayBlock, Reason: ReplayReasonInvocationRunning, BlockingInvocationIDs: []string{inv.ID}}}
			}
		}
		changed := false
		for _, inv := range stored {
			if inv.State != string(InvocationSucceeded) && inv.State != string(InvocationFailed) {
				inv.State, inv.SettledAt = string(InvocationPrepared), 0
				// RunningAt records whether any previous attempt could have effected work.
				// StartAttempt will refresh it; never use a reopened prepared state as proof.
				inv.Attempt++
				if err := tx.Put(toolstate.Invocations, inv.ID, inv); err != nil {
					return err
				}
				changed = true
			}
			out = append(out, publicInvocation(inv))
		}
		if changed {
			batch.Status, batch.UpdatedAt, batch.TerminalAt = string(BatchFinalized), time.Now().UTC().UnixMilli(), 0
			return toolstate.PutBatch(tx, batch)
		}
		return nil
	})
	return out, err
}

// RequireSettled prevents new sampling or new calls from bypassing unfinished
// admitted effects, even when their declarations allow exact recovery.
func (l *Ledger) RequireSettled(ctx context.Context) error {
	if l == nil {
		return nil
	}
	return l.write(ctx, func(tx *persistence.Tx) error {
		batches, err := toolstate.ReadOwnerBatches(tx, l.ownerID)
		if err != nil {
			return err
		}
		for _, batch := range batches {
			invocations, err := batchInvocations(tx, batch)
			if err != nil {
				return err
			}
			for _, inv := range invocations {
				if inv.State == string(InvocationRunning) || inv.State == string(InvocationInterruptedUnknown) || (inv.RunningAt != 0 && (inv.State == string(InvocationPrepared) || inv.State == string(InvocationAbandoned))) {
					return &ReplayBlockedError{Decision: ReplayDecision{Action: ReplayBlock, Reason: ReplayReasonInvocationUnknown, BlockingInvocationIDs: []string{inv.ID}}}
				}
			}
		}
		return nil
	})
}
