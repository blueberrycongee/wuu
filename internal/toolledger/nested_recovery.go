package toolledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

// PrepareScopedChild reattaches a local key to its parent's immutable child
// receipt. Only the live parent attempt can admit work; child policy never comes
// from the program. The caller must replay its program journal before new keys.
func (l *Ledger) PrepareScopedChild(ctx context.Context, parentID string, parentAttempt uint64, call providers.ToolCall, policy ReplayPolicy) (Invocation, error) {
	if l == nil || parentID == "" || call.ID == "" || call.Name == "" || (policy != ReplayAtMostOnce && policy != ReplayRepeatable) {
		return Invocation{}, errors.New("invalid durable nested call")
	}
	key, _ := json.Marshal([]string{parentID, call.ID})
	batchID := recoveryBatchID(l.ownerID, "nested:"+string(key))
	id, now := newID("tinv"), time.Now().UTC().UnixMilli()
	var out Invocation
	err := l.write(ctx, func(tx *persistence.Tx) error {
		parent, err := l.invocation(tx, parentID)
		if err != nil {
			return err
		}
		if parent.Attempt != parentAttempt || parent.State != string(InvocationRunning) {
			return errors.New("nested parent attempt is no longer running")
		}
		parentBatch, err := l.batch(tx, parent.BatchID)
		if err != nil {
			return err
		}
		if parentBatch.RecoveryScope == "" {
			return errors.New("nested parent has no durable admission")
		}
		var batch toolstate.Batch
		found, err := tx.Get(toolstate.Batches, batchID, &batch)
		if err != nil {
			return err
		}
		if found {
			if batch.OwnerID != l.ownerID || batch.RecoveryScope != parentBatch.RecoveryScope {
				return errors.New("nested call belongs to another owner")
			}
			children, err := batchInvocations(tx, batch)
			if err != nil {
				return err
			}
			if len(children) != 1 {
				return fmt.Errorf("%w: nested call receipt count", persistence.ErrCorrupt)
			}
			child := children[0]
			if child.ParentInvocationID != parentID || child.ToolName != call.Name || child.ToolKind != call.Kind || child.Arguments != call.Arguments {
				return errors.New("nested call key reused with different intent")
			}
			switch InvocationState(child.State) {
			case InvocationSucceeded, InvocationFailed:
				if child.Result == nil {
					return fmt.Errorf("%w: nested result missing", persistence.ErrCorrupt)
				}
				out = publicInvocation(child)
				return nil
			case InvocationPrepared, InvocationAbandoned:
				if child.RunningAt != 0 && (child.ReplayPolicy != string(ReplayRepeatable) || policy != ReplayRepeatable) {
					return &ReplayBlockedError{Decision: ReplayDecision{Action: ReplayBlock, Reason: ReplayReasonInvocationUnknown, BlockingInvocationIDs: []string{child.ID}}}
				}
			case InvocationInterruptedUnknown:
				if child.ReplayPolicy != string(ReplayRepeatable) || policy != ReplayRepeatable {
					return &ReplayBlockedError{Decision: ReplayDecision{Action: ReplayBlock, Reason: ReplayReasonInvocationUnknown, BlockingInvocationIDs: []string{child.ID}}}
				}
			default:
				return &ReplayBlockedError{Decision: ReplayDecision{Action: ReplayBlock, Reason: ReplayReasonInvocationRunning, BlockingInvocationIDs: []string{child.ID}}}
			}
			child.State, child.SettledAt = string(InvocationPrepared), 0
			child.Attempt++
			if err := tx.Put(toolstate.Invocations, child.ID, child); err != nil {
				return err
			}
			batch.Status, batch.TerminalAt, batch.UpdatedAt = string(BatchFinalized), 0, now
			if err := toolstate.PutBatch(tx, batch); err != nil {
				return err
			}
			out = publicInvocation(child)
			return nil
		}
		batch = toolstate.Batch{ID: batchID, OwnerID: l.ownerID, OperationID: parentBatch.OperationID, RecoveryScope: parentBatch.RecoveryScope, StepIndex: parentBatch.StepIndex, Status: string(BatchFinalized), CreatedAt: now, UpdatedAt: now, InvocationIDs: []string{id}}
		child := toolstate.Invocation{ID: id, BatchID: batchID, ParentInvocationID: parentID, ProviderCallID: "nested-" + batchID, ToolName: call.Name, ToolKind: call.Kind, Arguments: call.Arguments, ReplayPolicy: string(policy), State: string(InvocationPrepared), PreparedAt: now, Attempt: 1}
		if err := tx.Put(toolstate.Invocations, id, child); err != nil {
			return err
		}
		if err := toolstate.PutBatch(tx, batch); err != nil {
			return err
		}
		out = publicInvocation(child)
		return nil
	})
	return out, err
}
