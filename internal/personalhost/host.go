// Package personalhost binds Personal review and memory controls to live owner
// transport admission. It never exposes a credential API to plugins.
package personalhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

type Options struct {
	// Authorize is the selected resource policy. Live owner transport admission
	// and verified session identity are additional proof, never substitutes.
	Authorize func(context.Context, string, string) error
	// Review is the host-owned durable action coordinator, never a plugin callback.
	Review func(context.Context, string, json.RawMessage, string) (json.RawMessage, error)
	// Memory publishes exact source-bound records through selected durable providers.
	Memory func(context.Context, string, json.RawMessage, string) (json.RawMessage, error)
}
type Host struct {
	mu      sync.Mutex
	binding userspace.Binding
	options Options
	closed  bool
}

func New(binding userspace.Binding, options Options) *Host {
	return &Host{binding: binding, options: options}
}
func (h *Host) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	return nil
}

// Call's verifiedSession is resolved from host session metadata before dispatch.
// Control operations derive their actor from the separate owner admission.
func (h *Host) Call(ctx context.Context, operation string, raw json.RawMessage, verifiedSession string) (json.RawMessage, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, errors.New("Personal host is closed")
	}
	h.mu.Unlock()

	if _, _, known := OperationAccess(operation); !known {
		return nil, errors.New("Personal host operation is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if h.options.Authorize == nil {
		return nil, errors.New("Personal resource authorization is unavailable")
	}
	if err := h.options.Authorize(ctx, operation, verifiedSession); err != nil {
		return nil, err
	}

	if strings.HasPrefix(operation, "memory.") {
		h.mu.Lock()
		closed, handler := h.closed, h.options.Memory
		h.mu.Unlock()
		if closed || handler == nil {
			return nil, errors.New("Personal memory coordinator unavailable")
		}
		if operation == "memory.operations" || operation == "memory.reconcile" {
			if err := clientauthority.RequireOwner(ctx, h.binding); err != nil {
				return nil, err
			}
		} else if verifiedSession == "" {
			return nil, errors.New("memory requires a host-resolved session")
		}
		return handler(ctx, operation, raw, verifiedSession)
	}
	if strings.HasPrefix(operation, "review.") {
		h.mu.Lock()
		closed, handler := h.closed, h.options.Review
		h.mu.Unlock()
		if closed || ctx.Err() != nil {
			return nil, errors.New("Personal host is closed")
		}
		if h.binding.Validate() != nil || !h.binding.IsUser() || h.binding.Domain() != "personal" {
			return nil, errors.New("Personal host binding is required")
		}
		if operation == "review.execution.status" {
			if verifiedSession == "" {
				return nil, errors.New("host-resolved execution is required")
			}
		} else if err := clientauthority.RequireOwner(ctx, h.binding); err != nil {
			return nil, err
		}
		// The coordinator holds its own dispatch boundary. Model calls cannot
		// block unrelated owner controls.
		if handler != nil {
			return handler(ctx, operation, raw, verifiedSession)
		}
		if operation == "review.list" {
			if err := decode(raw, &struct{}{}); err != nil {
				return nil, err
			}
			return json.Marshal(map[string]string{"schema": "personal-review.v1", "kind": "unavailable", "reason": "动作审批当前不可用。"})
		}
		return nil, errors.New("action review is unavailable")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || ctx.Err() != nil {
		return nil, errors.New("Personal host is closed")
	}
	if h.binding.Validate() != nil || !h.binding.IsUser() || h.binding.Domain() != "personal" {
		return nil, errors.New("Personal host binding is required")
	}
	if err := clientauthority.RequireOwner(ctx, h.binding); err != nil {
		return nil, err
	}
	if err := decode(raw, &struct{}{}); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]bool{"ok": true})
}

func decode(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid Personal host parameters")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid Personal host parameters")
	}
	return nil
}
