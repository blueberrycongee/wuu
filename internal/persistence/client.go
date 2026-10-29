package persistence

import (
	"bytes"
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
		case "namespace_ownership":
			cause = ErrNamespaceOwnership
		case "inspection_active":
			cause = ErrInspectionActive
		case "inspection_released":
			cause = ErrInspectionReleased
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

// ExportPage returns owned values through the same captured binding as Read.
// The wire decoder requires every page envelope field; a legacy full Snapshot
// or a missing terminal marker cannot be mistaken for a partial page.
func (c *Client) ExportPage(ctx context.Context, query ExportQuery) (ExportPage, error) {
	page, _, err := c.exportPage(ctx, query)
	return page, err
}

// ExportPageEncoded also returns the owned, validated provider encoding. Host
// wrappers forward it unchanged: canonical checkpoint hashing must not expand
// HTML escapes in a page that already fits the caller's actual wire budget.
func (c *Client) ExportPageEncoded(ctx context.Context, query ExportQuery) (ExportPage, json.RawMessage, error) {
	page, raw, err := c.exportPage(ctx, query)
	return page, bytes.Clone(raw), err
}

func (c *Client) exportPage(ctx context.Context, query ExportQuery) (ExportPage, json.RawMessage, error) {
	if err := validateExportQuery(query); err != nil {
		return ExportPage{}, nil, err
	}
	request, err := json.Marshal(struct {
		Page ExportQuery `json:"page"`
	}{query})
	if err != nil {
		return ExportPage{}, nil, err
	}
	raw, err := c.callEncoded(ctx, "export", request)
	if err != nil {
		return ExportPage{}, nil, err
	}
	var wire struct {
		Format     string          `json:"format"`
		Revision   *Revision       `json:"revision,string"`
		Generation string          `json:"generation"`
		Section    string          `json:"section"`
		Documents  *[]Document     `json:"documents"`
		Receipts   *[]CommitRecord `json:"receipts"`
		Done       *bool           `json:"done"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return ExportPage{}, nil, fmt.Errorf("%w: decode export page: %v", ErrCorrupt, err)
	}
	if wire.Revision == nil || wire.Documents == nil || wire.Receipts == nil || wire.Done == nil {
		return ExportPage{}, nil, fmt.Errorf("%w: export page is missing required fields", ErrCorrupt)
	}
	page := ExportPage{Format: wire.Format, Revision: *wire.Revision, Generation: wire.Generation, Section: wire.Section, Documents: *wire.Documents, Receipts: *wire.Receipts, Done: *wire.Done}
	if len(raw) > query.TargetBytes && len(page.Documents)+len(page.Receipts) != 1 {
		return ExportPage{}, nil, fmt.Errorf("%w: export page exceeds the actual wire budget", ErrCorrupt)
	}
	if err := validateExportPage(page, query); err != nil {
		return ExportPage{}, nil, err
	}
	return page, raw, nil
}
