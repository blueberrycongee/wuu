package pluginhost

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"sync"
)

const KernelModelReceiptService = "host.model.receipt"

type modelReceipt struct {
	owner  string
	ctx    context.Context
	mu     sync.Mutex
	closed bool
	call   func(string, json.RawMessage) (json.RawMessage, error)
}

// RegisterModelReceipt binds opaque physical-accounting authority to one exact
// adapter callback. The callback must drain its transports before release. Only
// observations/settlement remain admissible after logical cancellation; no new
// submission can be admitted. Peers receive only the random handle.
func (h *Host) RegisterModelReceipt(ctx context.Context, owner string, call func(string, json.RawMessage) (json.RawMessage, error)) (string, func()) {
	id := rand.Text()
	receipt := &modelReceipt{owner: owner, ctx: ctx, call: call}
	h.modelReceipts.Store(id, receipt)
	return id, sync.OnceFunc(func() { receipt.mu.Lock(); receipt.closed = true; receipt.mu.Unlock(); h.modelReceipts.Delete(id) })
}
func (h *Host) InvokeModelReceipt(caller, id, method string, input json.RawMessage) (json.RawMessage, error) {
	value, ok := h.modelReceipts.Load(id)
	if !ok {
		return nil, &HostServiceError{Code: "receipt_unavailable", Message: "model receipt is unavailable"}
	}
	receipt := value.(*modelReceipt)
	receipt.mu.Lock()
	defer receipt.mu.Unlock()
	if receipt.closed || receipt.owner != caller {
		return nil, &HostServiceError{Code: "receipt_unavailable", Message: "model receipt is unavailable"}
	}
	if method == "create" && receipt.ctx.Err() != nil {
		return nil, &HostServiceError{Code: "request_cancelled", Message: "model submission admission has ended"}
	}
	return receipt.call(method, input)
}
func KernelModelReceiptDescriptor() ServiceDescriptor {
	methods := make([]ServiceMethodDescriptor, 0, 4)
	for _, name := range []string{"create", "observe", "settle", "snapshot"} {
		methods = append(methods, ServiceMethodDescriptor{Name: name, Cleanup: name != "create", InputSchema: "host.model.receipt.input.v1", OutputSchema: "host.model.receipt.output.v1"})
	}
	return ServiceDescriptor{Name: KernelModelReceiptService, Version: "1.0.0", Methods: methods}
}
