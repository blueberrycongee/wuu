package personalreview

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const viewSchema = "personal-review.v1"

// OwnerView is a versioned presentation contract, not an authorization token.
// ArgumentsJSON retains the exact canonical bytes included in Action.Digest;
// crossing JavaScript must never parse/re-encode its numbers for presentation.
type OwnerView struct {
	Schema   string       `json:"schema"`
	Kind     string       `json:"kind"`
	Requests []ViewRecord `json:"requests"`
	Settings *Settings    `json:"settings,omitempty"`
}
type ViewRecord struct {
	Revision    int64      `json:"revision"`
	Status      Status     `json:"status"`
	Outcome     string     `json:"outcome"`
	Reason      string     `json:"reason"`
	Action      ViewAction `json:"action"`
	ReceiptJSON string     `json:"receipt_json,omitempty"`
}
type ViewAction struct {
	ID            string    `json:"id"`
	Digest        string    `json:"digest"`
	Operation     string    `json:"operation"`
	Scope         Scope     `json:"scope"`
	Effect        Effect    `json:"effect"`
	Summary       Summary   `json:"summary"`
	ArgumentsJSON string    `json:"arguments_json"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// OwnerSnapshot must be served only through the authenticated owner transport.
// It projects the same stored actions resolved by Resolve, without dispatching.
func (e *Engine) OwnerSnapshot(ctx context.Context) (OwnerView, error) {
	records, err := e.List(ctx)
	if err != nil {
		return OwnerView{}, err
	}
	view := OwnerView{Schema: viewSchema, Kind: "ready", Requests: make([]ViewRecord, 0, len(records))}
	for _, record := range records {
		action := record.Action
		frozen := action
		frozen.Digest = ""
		if action.Digest != digest(frozen) {
			return OwnerView{}, errors.New("owner view action digest mismatch")
		}
		// Revisions participate in confirmation commands, so unlike parameter
		// text they must fit JavaScript's exact integer transport range.
		const maxExactInteger int64 = 1<<53 - 1
		if record.Revision < 1 || record.Revision > maxExactInteger || action.Scope.RequirementsRevision < 1 || action.Scope.RequirementsRevision > maxExactInteger {
			return OwnerView{}, errors.New("owner view revision exceeds exact transport range")
		}
		// Production results retain rich tool payloads for continuation. Show
		// only their bounded receipt, preserving JSON numbers without decoding
		// into floats. Oversized legacy results are omitted, never truncated.
		receipt := record.Result
		var envelope map[string]json.RawMessage
		if json.Unmarshal(record.Result, &envelope) == nil {
			if value, present := envelope["receipt"]; present {
				receipt = value
			}
		}
		if len(receipt) > 64*1024 {
			receipt = nil
		}
		view.Requests = append(view.Requests, ViewRecord{
			Revision: record.Revision, Status: record.Status, Outcome: record.Outcome, Reason: record.Reason, ReceiptJSON: string(receipt),
			Action: ViewAction{ID: action.ID, Digest: action.Digest, Operation: action.Operation, Scope: action.Scope,
				Effect: action.Effect, Summary: action.Summary, ArgumentsJSON: string(action.Arguments), ExpiresAt: action.ExpiresAt},
		})
	}
	return view, nil
}
