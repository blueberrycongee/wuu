package toolledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

// Ledger uses the selected authority retained by its execution owner.
// Construction does not reconcile state; recovery requires exclusive ownership.
type Ledger struct {
	documents persistence.Documents
	ownerID   string
}

func New(documents persistence.Documents, ownerID string) (*Ledger, error) {
	ownerID = strings.TrimSpace(ownerID)
	if documents == nil || ownerID == "" {
		return nil, errors.New("tool ledger document authority and owner are required")
	}
	return &Ledger{documents: documents, ownerID: ownerID}, nil
}

func (l *Ledger) write(ctx context.Context, body func(*persistence.Tx) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return persistence.Update(ctx, l.documents, body)
}

func (l *Ledger) batch(tx *persistence.Tx, id string, allowed ...BatchState) (toolstate.Batch, error) {
	var batch toolstate.Batch
	found, err := tx.Get(toolstate.Batches, strings.TrimSpace(id), &batch)
	if err != nil {
		return batch, err
	}
	if !found {
		return batch, fmt.Errorf("tool batch %q not found", id)
	}
	if batch.OwnerID != l.ownerID {
		return batch, fmt.Errorf("tool batch %q is owned by another ledger", id)
	}
	if len(allowed) > 0 && !slices.Contains(allowed, BatchState(batch.Status)) {
		return batch, fmt.Errorf("tool batch %q is %s", id, batch.Status)
	}
	return batch, nil
}

func (l *Ledger) invocation(tx *persistence.Tx, id string) (toolstate.Invocation, error) {
	var invocation toolstate.Invocation
	found, err := tx.Get(toolstate.Invocations, id, &invocation)
	if err != nil {
		return invocation, err
	}
	if !found {
		return invocation, fmt.Errorf("tool invocation %q not found", id)
	}
	_, err = l.batch(tx, invocation.BatchID)
	return invocation, err
}

func batchInvocations(tx *persistence.Tx, batch toolstate.Batch) ([]toolstate.Invocation, error) {
	rows, err := tx.Read(persistence.Query{Namespace: toolstate.Invocations, Keys: batch.InvocationIDs})
	if err != nil {
		return nil, err
	}
	if len(rows) != len(batch.InvocationIDs) {
		return nil, fmt.Errorf("%w: tool batch %q missing invocation receipts", persistence.ErrCorrupt, batch.ID)
	}
	out := make([]toolstate.Invocation, 0, len(rows))
	for _, row := range rows {
		var invocation toolstate.Invocation
		if err := json.Unmarshal(row.Value, &invocation); err != nil {
			return nil, err
		}
		if invocation.BatchID != batch.ID {
			return nil, fmt.Errorf("%w: tool batch %q has another batch's invocation", persistence.ErrCorrupt, batch.ID)
		}
		out = append(out, invocation)
	}
	slices.SortFunc(out, func(a, b toolstate.Invocation) int {
		if a.PreparedAt < b.PreparedAt {
			return -1
		}
		if a.PreparedAt > b.PreparedAt {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func publicInvocation(stored toolstate.Invocation) Invocation {
	out := Invocation{ParentInvocationID: stored.ParentInvocationID, ID: stored.ID, BatchID: stored.BatchID, ProviderCallID: stored.ProviderCallID,
		ToolName: stored.ToolName, ToolKind: stored.ToolKind, Arguments: stored.Arguments, ReplayPolicy: ReplayPolicy(stored.ReplayPolicy),
		State: InvocationState(stored.State), Projected: stored.ProjectedAt > 0}
	if stored.Result != nil {
		out.Result = *stored.Result
	}
	return out
}

func (l *Ledger) BeginBatch(ctx context.Context, operationID string, stepIndex int) (string, error) {
	if l == nil {
		return "", errors.New("tool ledger is required")
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return "", errors.New("tool batch operation is required")
	}
	id, now := newID("tbatch"), time.Now().UTC().UnixMilli()
	err := l.write(ctx, func(tx *persistence.Tx) error {
		return toolstate.PutBatch(tx, toolstate.Batch{ID: id, OwnerID: l.ownerID, OperationID: operationID, StepIndex: stepIndex,
			Status: string(BatchCollecting), CreatedAt: now, UpdatedAt: now, InvocationIDs: []string{}})
	})
	return id, err
}

func (l *Ledger) FinalizeBatch(ctx context.Context, batchID string) error {
	now := time.Now().UTC().UnixMilli()
	return l.write(ctx, func(tx *persistence.Tx) error {
		batch, err := l.batch(tx, batchID, BatchCollecting, BatchFinalized, BatchSettled)
		if err != nil {
			return err
		}
		if batch.Status != string(BatchCollecting) {
			return nil
		}
		batch.Status, batch.UpdatedAt = string(BatchFinalized), now
		if err := toolstate.PutBatch(tx, batch); err != nil {
			return err
		}
		return settleDocumentBatch(tx, batch, now)
	})
}

func (l *Ledger) Prepare(ctx context.Context, batchID string, call providers.ToolCall, policy ReplayPolicy) (Invocation, error) {
	return l.prepare(ctx, batchID, "", call, policy)
}

func (l *Ledger) PrepareNested(ctx context.Context, batchID, parentID string, call providers.ToolCall, policy ReplayPolicy) (Invocation, error) {
	if strings.TrimSpace(parentID) == "" {
		return Invocation{}, errors.New("nested tool requires a parent invocation")
	}
	return l.prepare(ctx, batchID, parentID, call, policy)
}

func (l *Ledger) prepare(ctx context.Context, batchID, parentID string, call providers.ToolCall, policy ReplayPolicy) (Invocation, error) {
	if l == nil {
		return Invocation{}, errors.New("tool ledger is required")
	}
	batchID, call.ID, call.Name = strings.TrimSpace(batchID), strings.TrimSpace(call.ID), strings.TrimSpace(call.Name)
	if batchID == "" || call.ID == "" || call.Name == "" {
		return Invocation{}, errors.New("tool batch, provider call id, and tool name are required")
	}
	if policy == "" {
		policy = ReplayAtMostOnce
	}
	id, now := newID("tinv"), time.Now().UTC().UnixMilli()
	var out Invocation
	err := l.write(ctx, func(tx *persistence.Tx) error {
		out = Invocation{}
		batch, err := l.batch(tx, batchID, BatchCollecting)
		if err != nil {
			return err
		}
		if parentID != "" {
			if _, err := l.invocation(tx, parentID); err != nil {
				return err
			}
		}
		invocations, err := batchInvocations(tx, batch)
		if err != nil {
			return err
		}
		for _, existing := range invocations {
			if existing.ProviderCallID != call.ID {
				continue
			}
			if existing.ToolName != call.Name || existing.ToolKind != call.Kind || existing.Arguments != call.Arguments || existing.ReplayPolicy != string(policy) || existing.ParentInvocationID != parentID {
				return fmt.Errorf("provider tool call %q metadata changed within batch %q", call.ID, batchID)
			}
			out = publicInvocation(existing)
			return nil
		}
		stored := toolstate.Invocation{ID: id, BatchID: batchID, ParentInvocationID: parentID, ProviderCallID: call.ID, ToolName: call.Name,
			ToolKind: call.Kind, Arguments: call.Arguments, ReplayPolicy: string(policy), State: string(InvocationPrepared), PreparedAt: now}
		if err := tx.Put(toolstate.Invocations, id, stored); err != nil {
			return err
		}
		batch.InvocationIDs = append(batch.InvocationIDs, id)
		if err := toolstate.PutBatch(tx, batch); err != nil {
			return err
		}
		out = publicInvocation(stored)
		return nil
	})
	return out, err
}

func (l *Ledger) Start(ctx context.Context, invocationID string) error {
	invocationID = strings.TrimSpace(invocationID)
	if invocationID == "" {
		return errors.New("tool invocation is required")
	}
	now := time.Now().UTC().UnixMilli()
	return l.write(ctx, func(tx *persistence.Tx) error {
		invocation, err := l.invocation(tx, invocationID)
		if err != nil {
			return err
		}
		if invocation.State == string(InvocationRunning) {
			return nil
		}
		if invocation.State != string(InvocationPrepared) {
			return fmt.Errorf("tool invocation %q cannot start from %s", invocationID, invocation.State)
		}
		invocation.State, invocation.RunningAt = string(InvocationRunning), now
		return tx.Put(toolstate.Invocations, invocationID, invocation)
	})
}

func (l *Ledger) Settle(ctx context.Context, invocationID string, result toolresult.Result) error {
	return l.settle(ctx, invocationID, result, false)
}

// SettleOrdinary records an invocation whose closed root execution scope proved
// it had no provisional remote reads or unfinished descendants.
func (l *Ledger) SettleOrdinary(ctx context.Context, invocationID string, result toolresult.Result) error {
	return l.settle(ctx, invocationID, result, true)
}

func (l *Ledger) settle(ctx context.Context, invocationID string, result toolresult.Result, ordinary bool) error {
	invocationID = strings.TrimSpace(invocationID)
	if invocationID == "" {
		return errors.New("tool invocation is required")
	}
	if err := result.Validate(); err != nil {
		return fmt.Errorf("validate tool result: %w", err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode tool result: %w", err)
	}
	state := InvocationSucceeded
	if result.IsError {
		state = InvocationFailed
	}
	now := time.Now().UTC().UnixMilli()
	return l.write(ctx, func(tx *persistence.Tx) error {
		invocation, err := l.invocation(tx, invocationID)
		if err != nil {
			return err
		}
		if invocation.Observation != nil && invocation.Observation.Outcome == "" {
			return errors.New("remote observation outcome requires confirmation")
		}
		existing, err := json.Marshal(invocation.Result)
		if err != nil {
			return err
		}
		if invocation.State == string(state) && (string(existing) == string(payload) || invocation.ProjectedAt > 0) {
			return nil
		}
		// Recovery fenced the previous executor. A late finalizer cannot make
		// an ambiguous external side effect appear safe to replay.
		if invocation.State == string(InvocationInterruptedUnknown) {
			return nil
		}
		if invocation.State != string(InvocationRunning) {
			return fmt.Errorf("tool invocation %q cannot settle from %s", invocationID, invocation.State)
		}
		invocation.State, invocation.Result, invocation.SettledAt = string(state), &result, now
		if ordinary && invocation.ParentInvocationID == "" {
			invocation.ProjectionSafetyVersion = 1
		}
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

func settleDocumentBatch(tx *persistence.Tx, batch toolstate.Batch, now int64) error {
	if batch.Status != string(BatchFinalized) {
		return nil
	}
	invocations, err := batchInvocations(tx, batch)
	if err != nil {
		return err
	}
	for _, invocation := range invocations {
		switch InvocationState(invocation.State) {
		case InvocationSucceeded, InvocationFailed, InvocationInterruptedUnknown, InvocationAbandoned:
		default:
			return nil
		}
	}
	batch.Status, batch.UpdatedAt, batch.TerminalAt = string(BatchSettled), now, max(batch.TerminalAt, now)
	return toolstate.PutBatch(tx, batch)
}

func (l *Ledger) MarkProjected(ctx context.Context, invocationIDs []string) error {
	if len(invocationIDs) == 0 {
		return nil
	}
	now := time.Now().UTC().UnixMilli()
	var observations []ObservationIntent
	err := l.write(ctx, func(tx *persistence.Tx) error {
		observations = nil
		for _, id := range invocationIDs {
			id = strings.TrimSpace(id)
			if id != "" {
				invocation, err := l.invocation(tx, id)
				if err != nil {
					return err
				}
				if err := toolstate.Project(tx, l.ownerID, id, now); err != nil {
					return err
				}
				if invocation.Observation != nil {
					observations = append(observations, observationIntent(invocation.Observation))
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, i := range observations {
		toolctx.ForgetObservationAuthorization(toolctx.SettlementIdentity{OperationID: i.OperationID, WorkerInstance: i.WorkerInstance, Actor: i.Actor, EnvironmentIdentity: i.EnvironmentIdentity}, l)
	}
	return nil
}

func (l *Ledger) PendingProjection(ctx context.Context) ([]Invocation, error) {
	if l == nil {
		return nil, nil
	}
	var out []Invocation
	var observations []ObservationIntent
	err := l.write(ctx, func(tx *persistence.Tx) error {
		out = nil
		observations = nil
		batches, err := toolstate.ReadOwnerBatches(tx, l.ownerID)
		if err != nil {
			return err
		}
		children := make(map[string][]toolstate.Invocation)
		var candidates []toolstate.Invocation
		for _, batch := range batches {
			invocations, err := batchInvocations(tx, batch)
			if err != nil {
				return err
			}
			for _, invocation := range invocations {
				if invocation.ParentInvocationID != "" {
					children[invocation.ParentInvocationID] = append(children[invocation.ParentInvocationID], invocation)
				}
				if (invocation.State == string(InvocationSucceeded) || invocation.State == string(InvocationFailed)) && invocation.ProjectedAt == 0 && invocation.ParentInvocationID == "" {
					if invocation.Result == nil {
						return fmt.Errorf("%w: settled tool invocation %q has no result", persistence.ErrCorrupt, invocation.ID)
					}
					candidates = append(candidates, invocation)
				}
			}
		}
		for _, invocation := range candidates {
			if invocation.Observation != nil && ((invocation.State == string(InvocationSucceeded) && invocation.Observation.Outcome != "committed") || (invocation.State == string(InvocationFailed) && invocation.Observation.Outcome != "aborted")) {
				return fmt.Errorf("%w: inconsistent observation outcome", persistence.ErrCorrupt)
			}
			if invocation.ProjectionSafetyVersion != 1 && invocation.Observation == nil {
				unsafe := invocation.ToolName == "read_file"
				seen := map[string]bool{}
				queue := []string{invocation.ID}
				for len(queue) > 0 && !unsafe {
					id := queue[len(queue)-1]
					queue = queue[:len(queue)-1]
					if seen[id] {
						continue
					}
					seen[id] = true
					for _, child := range children[id] {
						if child.ToolName == "read_file" {
							unsafe = true
							break
						}
						queue = append(queue, child.ID)
					}
				}
				if unsafe {
					return errors.New("legacy read result lacks a recoverable authorization binding")
				}
			}
			out = append(out, publicInvocation(invocation))
			if invocation.Observation != nil && invocation.Observation.Outcome == "committed" {
				observations = append(observations, observationIntent(invocation.Observation))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, intent := range observations {
		if err = l.AuthorizeObservation(ctx, intent); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (l *Ledger) DecideReplay(ctx context.Context, batchID string) (ReplayDecision, error) {
	if l == nil || strings.TrimSpace(batchID) == "" {
		return ReplayDecision{Action: ReplayAllow, Reason: ReplayReasonNoInvocation}, nil
	}
	var decision ReplayDecision
	err := l.write(ctx, func(tx *persistence.Tx) error {
		decision = ReplayDecision{Action: ReplayAllow, Reason: ReplayReasonNoInvocation}
		batch, err := l.batch(tx, batchID)
		if err != nil {
			return err
		}
		invocations, err := batchInvocations(tx, batch)
		if err != nil {
			return err
		}
		priority := 0
		for _, invocation := range invocations {
			if invocation.ReplayPolicy != string(ReplayAtMostOnce) {
				continue
			}
			switch InvocationState(invocation.State) {
			case InvocationPrepared:
				if priority < 1 {
					decision.Reason, decision.SupersedePartial, priority = ReplayReasonPreparedOnly, true, 1
				}
			case InvocationRunning:
				decision.Action = ReplayBlock
				if priority < 3 {
					decision.Reason, priority = ReplayReasonInvocationRunning, 3
				}
				decision.BlockingInvocationIDs = append(decision.BlockingInvocationIDs, invocation.ID)
			case InvocationSucceeded, InvocationFailed:
				decision.Action = ReplayBlock
				if priority < 2 {
					decision.Reason, priority = ReplayReasonInvocationSettled, 2
				}
				decision.BlockingInvocationIDs = append(decision.BlockingInvocationIDs, invocation.ID)
			case InvocationInterruptedUnknown:
				decision.Action = ReplayBlock
				if priority < 4 {
					decision.Reason, priority = ReplayReasonInvocationUnknown, 4
				}
				decision.BlockingInvocationIDs = append(decision.BlockingInvocationIDs, invocation.ID)
			}
		}
		return nil
	})
	return decision, err
}

func (l *Ledger) SupersedePreparedBatch(ctx context.Context, batchID string) error {
	if l == nil || strings.TrimSpace(batchID) == "" {
		return nil
	}
	now := time.Now().UTC().UnixMilli()
	return l.write(ctx, func(tx *persistence.Tx) error {
		batch, err := l.batch(tx, batchID, BatchCollecting)
		if err != nil {
			return err
		}
		invocations, err := batchInvocations(tx, batch)
		if err != nil {
			return err
		}
		for _, invocation := range invocations {
			if invocation.State != string(InvocationPrepared) {
				return fmt.Errorf("tool batch %q cannot be superseded after execution started", batchID)
			}
			invocation.State, invocation.SettledAt = string(InvocationAbandoned), max(invocation.SettledAt, now)
			if err := tx.Put(toolstate.Invocations, invocation.ID, invocation); err != nil {
				return err
			}
		}
		batch.Status, batch.UpdatedAt, batch.TerminalAt = string(BatchAbandoned), now, max(batch.TerminalAt, now)
		return toolstate.PutBatch(tx, batch)
	})
}

// ValidateContinuation requires the execution lease and reconciled receipts.
// Fresh sampling cannot make an unconfirmed at-most-once effect safe to repeat.
func (l *Ledger) ValidateContinuation(ctx context.Context) error {
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
			for _, invocation := range invocations {
				if invocation.ReplayPolicy == string(ReplayAtMostOnce) &&
					(invocation.State == string(InvocationRunning) || invocation.State == string(InvocationInterruptedUnknown)) {
					return &ReplayBlockedError{Decision: ReplayDecision{Action: ReplayBlock, Reason: ReplayReasonInvocationUnknown, BlockingInvocationIDs: []string{invocation.ID}}}
				}
			}
		}
		return nil
	})
}

// Reconcile requires the caller's exclusive execution lease, and never retries
// an external effect whose result was not durably settled.
func (l *Ledger) Reconcile(ctx context.Context) error {
	if l == nil {
		return nil
	}
	now := time.Now().UTC().UnixMilli()
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
			for _, invocation := range invocations {
				switch InvocationState(invocation.State) {
				case InvocationRunning:
					invocation.State = string(InvocationInterruptedUnknown)
				case InvocationPrepared:
					invocation.State = string(InvocationAbandoned)
				default:
					continue
				}
				invocation.SettledAt = max(invocation.SettledAt, now)
				if err := tx.Put(toolstate.Invocations, invocation.ID, invocation); err != nil {
					return err
				}
			}
			if batch.Status == string(BatchCollecting) || batch.Status == string(BatchFinalized) {
				batch.Status, batch.UpdatedAt, batch.TerminalAt = string(BatchInterrupted), now, max(batch.TerminalAt, now)
				if err := toolstate.PutBatch(tx, batch); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
