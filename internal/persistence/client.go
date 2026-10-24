package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Documents is the atomic storage boundary shared by local providers and bound
// service clients. A caller must retain the owning generation for its lifetime.
type Documents interface {
	Read(context.Context, Query) (View, error)
	Commit(context.Context, Transaction) (Receipt, error)
}

// Compactor is optional physical maintenance supplied by the selected provider.
// A provider without compact support fails visibly; callers must not fall back
// to opening a local store or infer its backend from a filesystem path.
type Compactor interface {
	Compact(context.Context) (Compaction, error)
}

// SnapshotImporter is explicit maintenance for an empty target. It preserves
// original operation receipts and never implies that source writers are stopped.
type SnapshotImporter interface {
	Import(context.Context, Snapshot) (ImportResult, error)
}

// Client routes every operation through the caller's selected service binding.
// The callback must be captured from that generation, never look up the latest
// host, and must preserve cleanup admission when a dependent is shutting down.
type Client struct {
	call func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func NewClient(call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) *Client {
	return &Client{call: call}
}

func (c *Client) Read(ctx context.Context, query Query) (View, error) {
	var view View
	err := c.invoke(ctx, "read", query, &view)
	return view, err
}

func (c *Client) Commit(ctx context.Context, tx Transaction) (Receipt, error) {
	raw, err := marshalTransaction(tx)
	if err != nil {
		return Receipt{}, err
	}
	return c.CommitEncoded(ctx, raw)
}

// CommitEncoded forwards an already encoded transaction through the same binding
// and error mapping as Commit. The provider validates it; raw must remain
// immutable until the call returns. This preserves an authority-validated wire
// request without encoding a second full copy at the provider boundary.
func (c *Client) CommitEncoded(ctx context.Context, raw json.RawMessage) (Receipt, error) {
	var receipt Receipt
	err := c.invokeEncoded(ctx, "commit", raw, &receipt)
	return receipt, err
}

func (c *Client) Compact(ctx context.Context) (Compaction, error) {
	var result Compaction
	err := c.invoke(ctx, "compact", struct{}{}, &result)
	return result, err
}

func (c *Client) Import(ctx context.Context, snapshot Snapshot) (ImportResult, error) {
	var result ImportResult
	err := c.invoke(ctx, "import", snapshot, &result)
	return result, err
}

func (c *Client) Export(ctx context.Context) (Snapshot, error) {
	var snapshot Snapshot
	err := c.invoke(ctx, "export", struct{}{}, &snapshot)
	return snapshot, err
}

func (c *Client) invoke(ctx context.Context, method string, request, response any) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return c.invokeEncoded(ctx, method, raw, response)
}

func (c *Client) callEncoded(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	raw, err := c.call(ctx, method, raw)
	if err != nil {
		var serviceErr *pluginhost.HostServiceError
		if !errors.As(err, &serviceErr) {
			return nil, err
		}
		var cause error
		switch serviceErr.Code {
		case "state_conflict":
			cause = ErrConflict
		case "state_operation_conflict":
			cause = ErrOperationConflict
		case "state_binding_changed":
			cause = ErrBindingChanged
		case "state_migration_required":
			cause = ErrMigrationRequired
		case "state_corrupt":
			cause = ErrCorrupt
		case "invalid_request":
			cause = ErrInvalid
		case "request_cancelled":
			cause = ctx.Err()
			if cause == nil {
				cause = context.Canceled
			}
		}
		return nil, errors.Join(cause, err)
	}
	return raw, nil
}

func (c *Client) invokeEncoded(ctx context.Context, method string, raw json.RawMessage, response any) error {
	raw, err := c.callEncoded(ctx, method, raw)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, response); err != nil {
		return fmt.Errorf("decode %s.%s result: %w", ServiceName, method, err)
	}
	return nil
}

func (c *Client) captureCheckpoint(ctx context.Context) (Checkpoint, error) {
	raw, err := c.callEncoded(ctx, "export", json.RawMessage(`{}`))
	if err != nil {
		return Checkpoint{}, err
	}
	// Borrow only while validating this response. Public Export still returns
	// independent values that survive callback buffer reuse and caller mutation.
	var snapshot Snapshot
	if err := decodeBorrowedValues(raw, &snapshot, false); err != nil {
		return Checkpoint{}, fmt.Errorf("decode %s.export result: %w", ServiceName, err)
	}
	return SnapshotCheckpoint(snapshot)
}
