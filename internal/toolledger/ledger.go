package toolledger

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type BatchState string

const (
	BatchCollecting  BatchState = "collecting"
	BatchFinalized   BatchState = "finalized"
	BatchSettled     BatchState = "settled"
	BatchProjected   BatchState = "projected"
	BatchAbandoned   BatchState = "abandoned"
	BatchInterrupted BatchState = "interrupted"
)

type InvocationState string

const (
	InvocationPrepared           InvocationState = "prepared"
	InvocationRunning            InvocationState = "running"
	InvocationSucceeded          InvocationState = "succeeded"
	InvocationFailed             InvocationState = "failed"
	InvocationOutcomeUnknown     InvocationState = "outcome_unknown"
	InvocationInterruptedUnknown InvocationState = "interrupted_unknown"
	InvocationAbandoned          InvocationState = "abandoned"
)

type ReplayPolicy string

const (
	ReplayAtMostOnce ReplayPolicy = "at_most_once"
	ReplayRepeatable ReplayPolicy = "repeatable"
	ReplayIdempotent ReplayPolicy = "idempotent"
)

type ReplayAction string

const (
	ReplayAllow ReplayAction = "allow"
	ReplayBlock ReplayAction = "block"
)

type ReplayReason string

const (
	ReplayReasonNoInvocation      ReplayReason = "no_invocation"
	ReplayReasonPreparedOnly      ReplayReason = "prepared_only"
	ReplayReasonInvocationRunning ReplayReason = "invocation_running"
	ReplayReasonInvocationSettled ReplayReason = "invocation_settled"
	ReplayReasonInvocationUnknown ReplayReason = "invocation_unknown"
)

type ReplayDecision struct {
	Action                ReplayAction
	Reason                ReplayReason
	BlockingInvocationIDs []string
	SupersedePartial      bool
}

type ReplayBlockedError struct {
	Decision ReplayDecision
}

func (e *ReplayBlockedError) Error() string {
	if e == nil {
		return "tool replay blocked"
	}
	return fmt.Sprintf("tool replay blocked: %s (%s)", e.Decision.Reason, strings.Join(e.Decision.BlockingInvocationIDs, ","))
}

func (e *ReplayBlockedError) ReplayReasonCode() string {
	if e == nil {
		return ""
	}
	return string(e.Decision.Reason)
}

type Invocation struct {
	ParentInvocationID string
	ID                 string
	BatchID            string
	ProviderCallID     string
	ToolName           string
	ToolKind           providers.ToolCallKind
	Arguments          string
	ReplayPolicy       ReplayPolicy
	State              InvocationState
	Result             toolresult.Result
	Projected          bool
}

type Ledger struct {
	sessDir string
	ownerID string
}

// New opens an owner's durable tool ledger without changing invocation state.
// Callers must hold the owner's execution lease before calling Reconcile.
func New(sessDir, ownerID string) (*Ledger, error) {
	sessDir = strings.TrimSpace(sessDir)
	ownerID = strings.TrimSpace(ownerID)
	if sessDir == "" || ownerID == "" {
		return nil, errors.New("tool ledger session directory and owner are required")
	}
	db, err := session.OpenStore(sessDir)
	if err != nil {
		return nil, fmt.Errorf("open tool ledger: %w", err)
	}
	if err := db.Close(); err != nil {
		return nil, fmt.Errorf("close tool ledger: %w", err)
	}
	return &Ledger{sessDir: sessDir, ownerID: ownerID}, nil
}

func (l *Ledger) BeginBatch(ctx context.Context, operationID string, stepIndex int) (string, error) {
	if l == nil {
		return "", errors.New("tool ledger is required")
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return "", errors.New("tool batch operation is required")
	}
	id := newID("tbatch")
	now := time.Now().UTC().UnixMilli()
	err := l.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO tool_batches (id, owner_id, operation_id, step_index, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`, id, l.ownerID, operationID, stepIndex, BatchCollecting, now, now)
		return err
	})
	return id, err
}

func (l *Ledger) FinalizeBatch(ctx context.Context, batchID string) error {
	now := time.Now().UTC().UnixMilli()
	return l.write(ctx, func(tx *sql.Tx) error {
		if err := l.assertBatchOwnerTx(ctx, tx, batchID, BatchCollecting, BatchFinalized, BatchSettled); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
UPDATE tool_batches SET status = ?, updated_at = ?
WHERE id = ? AND owner_id = ? AND status = ?`, BatchFinalized, now, batchID, l.ownerID, BatchCollecting)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed > 0 {
			return settleBatchIfReadyTx(ctx, tx, batchID, now)
		}
		return nil
	})
}

func (l *Ledger) Prepare(ctx context.Context, batchID string, call providers.ToolCall, policy ReplayPolicy) (Invocation, error) {
	return l.prepare(ctx, batchID, "", call, policy)
}

// PrepareNested records a child independently from its parent's model batch.
// The parent must belong to this ledger's owner. Nested results are not projected
// as provider tool messages: the provider never issued their generated call IDs.
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
	batchID = strings.TrimSpace(batchID)
	call.ID = strings.TrimSpace(call.ID)
	call.Name = strings.TrimSpace(call.Name)
	if batchID == "" || call.ID == "" || call.Name == "" {
		return Invocation{}, errors.New("tool batch, provider call id, and tool name are required")
	}
	if policy == "" {
		policy = ReplayAtMostOnce
	}
	id := newID("tinv")
	now := time.Now().UTC().UnixMilli()
	var out Invocation
	err := l.write(ctx, func(tx *sql.Tx) error {
		if err := l.assertBatchOwnerTx(ctx, tx, batchID, BatchCollecting); err != nil {
			return err
		}
		if parentID != "" {
			if _, err := l.invocationStateTx(ctx, tx, parentID); err != nil {
				return err
			}
		}
		var existing Invocation
		var kind, storedPolicy, state string
		err := tx.QueryRowContext(ctx, `
SELECT id, tool_name, tool_kind, arguments_json, replay_policy, state, parent_invocation_id
FROM tool_invocations WHERE batch_id = ? AND provider_call_id = ?`, batchID, call.ID).
			Scan(&existing.ID, &existing.ToolName, &kind, &existing.Arguments, &storedPolicy, &state, &existing.ParentInvocationID)
		if err == nil {
			if existing.ToolName != call.Name || kind != string(call.Kind) || existing.Arguments != call.Arguments || storedPolicy != string(policy) || existing.ParentInvocationID != parentID {
				return fmt.Errorf("provider tool call %q metadata changed within batch %q", call.ID, batchID)
			}
			existing.BatchID = batchID
			existing.ProviderCallID = call.ID
			existing.ToolKind = providers.ToolCallKind(kind)
			existing.ReplayPolicy = ReplayPolicy(storedPolicy)
			existing.State = InvocationState(state)
			out = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO tool_invocations (
    id, batch_id, provider_call_id, tool_name, tool_kind, arguments_json,
    replay_policy, state, prepared_at, parent_invocation_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, batchID, call.ID, call.Name, string(call.Kind), call.Arguments,
			policy, InvocationPrepared, now, parentID,
		)
		if err == nil {
			out = Invocation{
				ParentInvocationID: parentID,
				ID:                 id, BatchID: batchID, ProviderCallID: call.ID, ToolName: call.Name,
				ToolKind: call.Kind, Arguments: call.Arguments, ReplayPolicy: policy, State: InvocationPrepared,
			}
		}
		return err
	})
	return out, err
}

func (l *Ledger) Start(ctx context.Context, invocationID string) error {
	invocationID = strings.TrimSpace(invocationID)
	if invocationID == "" {
		return errors.New("tool invocation is required")
	}
	now := time.Now().UTC().UnixMilli()
	return l.write(ctx, func(tx *sql.Tx) error {
		state, err := l.invocationStateTx(ctx, tx, invocationID)
		if err != nil {
			return err
		}
		if state == InvocationRunning {
			return nil
		}
		if state != InvocationPrepared {
			return fmt.Errorf("tool invocation %q cannot start from %s", invocationID, state)
		}
		_, err = tx.ExecContext(ctx, `UPDATE tool_invocations SET state = ?, running_at = ? WHERE id = ?`, InvocationRunning, now, invocationID)
		return err
	})
}

func (l *Ledger) Settle(ctx context.Context, invocationID string, result toolresult.Result) error {
	state := InvocationSucceeded
	if result.IsError {
		state = InvocationFailed
	}
	return l.settle(ctx, invocationID, result, state)
}

// SettleUnknown preserves a completed host observation whose remote effects
// cannot be confirmed. The result is projected normally, but replay stays blocked.
func (l *Ledger) SettleUnknown(ctx context.Context, invocationID string, result toolresult.Result) error {
	if !result.IsError {
		return errors.New("unknown execution outcome requires an error result")
	}
	return l.settle(ctx, invocationID, result, InvocationOutcomeUnknown)
}

func (l *Ledger) settle(ctx context.Context, invocationID string, result toolresult.Result, state InvocationState) error {
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
	now := time.Now().UTC().UnixMilli()
	return l.write(ctx, func(tx *sql.Tx) error {
		var batchID, existingState, existingResult string
		var projectedAt int64
		if err := tx.QueryRowContext(ctx, `SELECT batch_id, state, result_json, projected_at FROM tool_invocations WHERE id = ? AND batch_id IN (SELECT id FROM tool_batches WHERE owner_id = ?)`, invocationID, l.ownerID).
			Scan(&batchID, &existingState, &existingResult, &projectedAt); err != nil {
			return err
		}
		if InvocationState(existingState) == state && (existingResult == string(payload) || projectedAt > 0) {
			return nil
		}
		if InvocationState(existingState) == InvocationInterruptedUnknown {
			// Recovery has fenced this executor after its ownership lease was
			// released. Its finalizer may still arrive while the old goroutine is
			// draining, but it must neither overwrite the ambiguous durable state
			// nor fail the already-abandoned turn.
			return nil
		}
		if InvocationState(existingState) != InvocationRunning {
			return fmt.Errorf("tool invocation %q cannot settle from %s", invocationID, existingState)
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE tool_invocations SET state = ?, result_json = ?, settled_at = ? WHERE id = ?`, state, string(payload), now, invocationID); err != nil {
			return err
		}
		return settleBatchIfReadyTx(ctx, tx, batchID, now)
	})
}

func (l *Ledger) MarkProjected(ctx context.Context, invocationIDs []string) error {
	if len(invocationIDs) == 0 {
		return nil
	}
	now := time.Now().UTC().UnixMilli()
	return l.write(ctx, func(tx *sql.Tx) error {
		batches := make(map[string]struct{})
		for _, id := range invocationIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			var batchID, state string
			if err := tx.QueryRowContext(ctx, `SELECT batch_id, state FROM tool_invocations WHERE id = ? AND batch_id IN (SELECT id FROM tool_batches WHERE owner_id = ?)`, id, l.ownerID).Scan(&batchID, &state); err != nil {
				return err
			}
			if state != string(InvocationSucceeded) && state != string(InvocationFailed) && state != string(InvocationOutcomeUnknown) {
				return fmt.Errorf("tool invocation %q cannot project from %s", id, state)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE tool_invocations SET projected_at = MAX(projected_at, ?), result_json = '' WHERE id = ?`, now, id); err != nil {
				return err
			}
			batches[batchID] = struct{}{}
		}
		for batchID := range batches {
			if err := projectBatchIfReadyTx(ctx, tx, batchID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func (l *Ledger) PendingProjection(ctx context.Context) ([]Invocation, error) {
	if l == nil {
		return nil, nil
	}
	db, err := session.OpenStore(l.sessDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `
SELECT i.id, i.batch_id, i.provider_call_id, i.tool_name, i.tool_kind,
       i.arguments_json, i.replay_policy, i.state, i.result_json
FROM tool_invocations i
JOIN tool_batches b ON b.id = i.batch_id
WHERE b.owner_id = ? AND i.state IN (?, ?, ?) AND i.projected_at = 0 AND i.parent_invocation_id = ''
ORDER BY b.created_at, b.id, i.prepared_at, i.id`, l.ownerID, InvocationSucceeded, InvocationFailed, InvocationOutcomeUnknown)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invocation
	for rows.Next() {
		var invocation Invocation
		var kind, policy, state, resultJSON string
		if err := rows.Scan(
			&invocation.ID, &invocation.BatchID, &invocation.ProviderCallID,
			&invocation.ToolName, &kind, &invocation.Arguments, &policy, &state, &resultJSON,
		); err != nil {
			return nil, err
		}
		invocation.ToolKind = providers.ToolCallKind(kind)
		invocation.ReplayPolicy = ReplayPolicy(policy)
		invocation.State = InvocationState(state)
		if err := json.Unmarshal([]byte(resultJSON), &invocation.Result); err != nil {
			return nil, fmt.Errorf("decode tool result %q: %w", invocation.ID, err)
		}
		out = append(out, invocation)
	}
	return out, rows.Err()
}

func (l *Ledger) DecideReplay(ctx context.Context, batchID string) (ReplayDecision, error) {
	if l == nil || strings.TrimSpace(batchID) == "" {
		return ReplayDecision{Action: ReplayAllow, Reason: ReplayReasonNoInvocation}, nil
	}
	db, err := session.OpenStore(l.sessDir)
	if err != nil {
		return ReplayDecision{}, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `
SELECT id, state, replay_policy FROM tool_invocations WHERE batch_id = ? ORDER BY prepared_at, id`, batchID)
	if err != nil {
		return ReplayDecision{}, err
	}
	defer rows.Close()
	decision := ReplayDecision{Action: ReplayAllow, Reason: ReplayReasonNoInvocation}
	priority := 0
	for rows.Next() {
		var id, state, policy string
		if err := rows.Scan(&id, &state, &policy); err != nil {
			return ReplayDecision{}, err
		}
		if ReplayPolicy(policy) != ReplayAtMostOnce {
			continue
		}
		switch InvocationState(state) {
		case InvocationPrepared:
			if priority < 1 {
				decision.Reason = ReplayReasonPreparedOnly
				decision.SupersedePartial = true
				priority = 1
			}
		case InvocationRunning:
			decision.Action = ReplayBlock
			if priority < 3 {
				decision.Reason = ReplayReasonInvocationRunning
				priority = 3
			}
			decision.BlockingInvocationIDs = append(decision.BlockingInvocationIDs, id)
		case InvocationSucceeded, InvocationFailed:
			decision.Action = ReplayBlock
			if priority < 2 {
				decision.Reason = ReplayReasonInvocationSettled
				priority = 2
			}
			decision.BlockingInvocationIDs = append(decision.BlockingInvocationIDs, id)
		case InvocationInterruptedUnknown, InvocationOutcomeUnknown:
			decision.Action = ReplayBlock
			if priority < 4 {
				decision.Reason = ReplayReasonInvocationUnknown
				priority = 4
			}
			decision.BlockingInvocationIDs = append(decision.BlockingInvocationIDs, id)
		}
	}
	return decision, rows.Err()
}

func (l *Ledger) SupersedePreparedBatch(ctx context.Context, batchID string) error {
	if l == nil || strings.TrimSpace(batchID) == "" {
		return nil
	}
	now := time.Now().UTC().UnixMilli()
	return l.write(ctx, func(tx *sql.Tx) error {
		if err := l.assertBatchOwnerTx(ctx, tx, batchID, BatchCollecting); err != nil {
			return err
		}
		var unsafe int
		if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM tool_invocations
WHERE batch_id = ? AND state != ?`, batchID, InvocationPrepared).Scan(&unsafe); err != nil {
			return err
		}
		if unsafe != 0 {
			return fmt.Errorf("tool batch %q cannot be superseded after execution started", batchID)
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE tool_invocations SET state = ?, settled_at = MAX(settled_at, ?)
WHERE batch_id = ? AND state = ?`, InvocationAbandoned, now, batchID, InvocationPrepared); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
UPDATE tool_batches SET status = ?, updated_at = ?, terminal_at = MAX(terminal_at, ?)
WHERE id = ? AND owner_id = ? AND status = ?`, BatchAbandoned, now, now, batchID, l.ownerID, BatchCollecting)
		return err
	})
}

// Reconcile marks work left by a previous executor as interrupted. The caller
// must hold exclusive execution ownership for this ledger owner.
func (l *Ledger) Reconcile(ctx context.Context) error {
	if l == nil {
		return nil
	}
	now := time.Now().UTC().UnixMilli()
	return l.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
UPDATE tool_invocations
SET state = ?, settled_at = MAX(settled_at, ?)
WHERE state = ? AND batch_id IN (SELECT id FROM tool_batches WHERE owner_id = ?)`,
			InvocationInterruptedUnknown, now, InvocationRunning, l.ownerID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE tool_invocations
SET state = ?, settled_at = MAX(settled_at, ?)
WHERE state = ? AND batch_id IN (SELECT id FROM tool_batches WHERE owner_id = ?)`,
			InvocationAbandoned, now, InvocationPrepared, l.ownerID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
UPDATE tool_batches
SET status = ?, updated_at = ?, terminal_at = MAX(terminal_at, ?)
WHERE owner_id = ? AND status IN (?, ?)`,
			BatchInterrupted, now, now, l.ownerID, BatchCollecting, BatchFinalized)
		return err
	})
}

func (l *Ledger) assertBatchOwnerTx(ctx context.Context, tx *sql.Tx, batchID string, allowed ...BatchState) error {
	var owner, state string
	if err := tx.QueryRowContext(ctx, `SELECT owner_id, status FROM tool_batches WHERE id = ?`, strings.TrimSpace(batchID)).Scan(&owner, &state); err != nil {
		return err
	}
	if owner != l.ownerID {
		return fmt.Errorf("tool batch %q is owned by another ledger", batchID)
	}
	for _, candidate := range allowed {
		if state == string(candidate) {
			return nil
		}
	}
	return fmt.Errorf("tool batch %q is %s", batchID, state)
}

func (l *Ledger) invocationStateTx(ctx context.Context, tx *sql.Tx, invocationID string) (InvocationState, error) {
	var state, owner string
	if err := tx.QueryRowContext(ctx, `
SELECT i.state, b.owner_id
FROM tool_invocations i JOIN tool_batches b ON b.id = i.batch_id
WHERE i.id = ?`, invocationID).Scan(&state, &owner); err != nil {
		return "", err
	}
	if owner != l.ownerID {
		return "", fmt.Errorf("tool invocation %q is owned by another ledger", invocationID)
	}
	return InvocationState(state), nil
}

func (l *Ledger) write(ctx context.Context, fn func(*sql.Tx) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := session.OpenStore(l.sessDir)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func settleBatchIfReadyTx(ctx context.Context, tx *sql.Tx, batchID string, now int64) error {
	var pending int
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM tool_invocations WHERE batch_id = ? AND state NOT IN (?, ?, ?, ?, ?)`,
		batchID, InvocationSucceeded, InvocationFailed, InvocationOutcomeUnknown, InvocationInterruptedUnknown, InvocationAbandoned).Scan(&pending); err != nil {
		return err
	}
	if pending != 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
UPDATE tool_batches SET status = ?, updated_at = ?, terminal_at = MAX(terminal_at, ?)
WHERE id = ? AND status = ?`, BatchSettled, now, now, batchID, BatchFinalized)
	return err
}

func projectBatchIfReadyTx(ctx context.Context, tx *sql.Tx, batchID string, now int64) error {
	var pending int
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM tool_invocations
WHERE batch_id = ? AND state IN (?, ?, ?) AND projected_at = 0`,
		batchID, InvocationSucceeded, InvocationFailed, InvocationOutcomeUnknown).Scan(&pending); err != nil {
		return err
	}
	if pending != 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
UPDATE tool_batches SET status = ?, updated_at = ?, terminal_at = MAX(terminal_at, ?)
WHERE id = ? AND status = ?`, BatchProjected, now, now, batchID, BatchSettled)
	return err
}

func newID(prefix string) string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("toolledger: generate id: %v", err))
	}
	return prefix + "-" + hex.EncodeToString(raw[:])
}
