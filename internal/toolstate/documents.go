// Package toolstate defines durable tool receipts shared by execution and
// history projection. Both operations use the selected document authority.
package toolstate

import (
	"encoding/json"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

const (
	Batches     = "wuu.tool_batches.v1"
	Invocations = "wuu.tool_invocations.v1"
)

type Batch struct {
	ID            string
	OwnerID       string
	OperationID   string
	StepIndex     int
	Status        string
	CreatedAt     int64
	UpdatedAt     int64
	TerminalAt    int64
	InvocationIDs []string
}

type Invocation struct {
	ID                 string
	BatchID            string
	ParentInvocationID string
	ProviderCallID     string
	ToolName           string
	ToolKind           providers.ToolCallKind
	Arguments          string
	ReplayPolicy       string
	State              string
	Result             *toolresult.Result
	PreparedAt         int64
	RunningAt          int64
	SettledAt          int64
	ProjectedAt        int64
}

// Project joins history admission and result disposal in the caller's atomic
// transaction. A receipt from another owner cannot settle this conversation.
func Project(tx *persistence.Tx, ownerID, invocationID string, now int64) error {
	var invocation Invocation
	found, err := tx.Get(Invocations, invocationID, &invocation)
	if err != nil {
		return err
	}
	if !found || invocation.State != "succeeded" && invocation.State != "failed" {
		return fmt.Errorf("tool invocation %q is not settled for owner %q", invocationID, ownerID)
	}
	var batch Batch
	found, err = tx.Get(Batches, invocation.BatchID, &batch)
	if err != nil {
		return err
	}
	if !found || batch.OwnerID != ownerID {
		return fmt.Errorf("tool invocation %q is not settled for owner %q", invocationID, ownerID)
	}
	invocation.ProjectedAt, invocation.Result = max(invocation.ProjectedAt, now), nil
	if err := tx.Put(Invocations, invocationID, invocation); err != nil {
		return err
	}
	if batch.Status != "settled" {
		return nil
	}
	rows, err := tx.Read(persistence.Query{Namespace: Invocations, Keys: batch.InvocationIDs})
	if err != nil {
		return err
	}
	for _, row := range rows {
		var other Invocation
		if err := json.Unmarshal(row.Value, &other); err != nil {
			return fmt.Errorf("%w: tool invocation %s: %v", persistence.ErrCorrupt, row.Key, err)
		}
		if other.BatchID == batch.ID && (other.State == "succeeded" || other.State == "failed") && other.ProjectedAt == 0 {
			return nil
		}
	}
	batch.Status, batch.UpdatedAt, batch.TerminalAt = "projected", now, max(batch.TerminalAt, now)
	return tx.Put(Batches, batch.ID, batch)
}
