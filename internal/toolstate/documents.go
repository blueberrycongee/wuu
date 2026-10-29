// Package toolstate defines durable tool receipts shared by execution and
// history projection. Both operations use the selected document authority.
package toolstate

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

const (
	Batches        = "wuu.tool_batches.v1"
	Invocations    = "wuu.tool_invocations.v1"
	batchesByOwner = "wuu.tool_batches_owner.v1"
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
	ID                      string
	BatchID                 string
	ParentInvocationID      string
	ProviderCallID          string
	ToolName                string
	ToolKind                providers.ToolCallKind
	Arguments               string
	ReplayPolicy            string
	State                   string
	Result                  *toolresult.Result
	PreparedAt              int64
	RunningAt               int64
	SettledAt               int64
	ProjectedAt             int64
	ProjectionSafetyVersion int          `json:",omitempty"`
	Observation             *Observation `json:",omitempty"`
}

// Observation stores recovery identifiers and a validated result, never the
// worker transport credential or individual prepared-read receipts.
type Observation struct {
	OperationID         string
	WorkerInstance      string
	Actor               string
	EnvironmentIdentity string
	Outcome             string             `json:",omitempty"`
	Result              *toolresult.Result `json:",omitempty"`
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
	return PutBatch(tx, batch)
}

func batchOwnerKey(batch Batch) string {
	return base64.RawURLEncoding.EncodeToString([]byte(batch.OwnerID)) + "/" + fmt.Sprintf("%020d", uint64(batch.CreatedAt)^(1<<63)) + "/" + batch.ID
}

// PutBatch keeps owner lookup and the batch receipt in the same transaction.
func PutBatch(tx *persistence.Tx, batch Batch) error {
	if err := tx.Put(batchesByOwner, batchOwnerKey(batch), batch.ID); err != nil {
		return err
	}
	return tx.Put(Batches, batch.ID, batch)
}

func ReadOwnerBatches(tx *persistence.Tx, ownerID string) ([]Batch, error) {
	query := persistence.Query{Namespace: batchesByOwner, Prefix: base64.RawURLEncoding.EncodeToString([]byte(ownerID)) + "/", Limit: 256}
	var batches []Batch
	for {
		page, err := tx.ReadView(query)
		if err != nil {
			return nil, err
		}
		for _, row := range page.Documents {
			var id string
			if err := json.Unmarshal(row.Value, &id); err != nil {
				return nil, err
			}
			var batch Batch
			found, err := tx.Get(Batches, id, &batch)
			if err != nil {
				return nil, err
			}
			if !found || batch.OwnerID != ownerID {
				return nil, fmt.Errorf("%w: invalid tool batch owner index", persistence.ErrCorrupt)
			}
			batches = append(batches, batch)
		}
		if page.NextCursor == "" {
			return batches, nil
		}
		query.Cursor = page.NextCursor
		query.AtRevision = &page.Revision
	}
}
