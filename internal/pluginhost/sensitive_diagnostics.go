package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const sensitiveDiagnostic = "plugin operation failed; diagnostic details withheld after sensitive model validation"

// Validation may carry unsaved credentials to the selected provider. This is a
// diagnostic boundary, not a sandbox: trusted plugin code can deliberately send
// data elsewhere. Neither native IDs nor transport implementations get a bypass.
func sensitiveModelValidation(capability string, input json.RawMessage) bool {
	if capability != CapabilityModelAdapter {
		return false
	}
	var operation struct {
		Operation string `json:"operation"`
	}
	return json.Unmarshal(input, &operation) == nil && operation.Operation == "validate"
}

func sanitizedSensitiveError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, providers.ErrModelReceiptScopeClosed) {
		return providers.ErrModelReceiptScopeClosed
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var remote *remoteCallError
	if errors.As(err, &remote) {
		return &remoteCallError{code: "sensitive_operation_failed", message: sensitiveDiagnostic}
	}
	return errors.New(sensitiveDiagnostic)
}

// Suppression is sticky for the process lifetime: a provider may echo a value
// later, or while another request is running. Clear already buffered diagnostics
// and discard future stderr rather than trying to recognize secret substrings.
func (c *ProcessClient) suppressSensitiveDiagnostics() {
	c.sensitiveDiagnostics.Store(true)
	c.stderr.suppress()
	c.mu.Lock()
	if c.status.Error != "" {
		c.status.Error = sensitiveDiagnostic
	}
	c.mu.Unlock()
	c.readerErrMu.Lock()
	c.readerErr = sanitizedSensitiveError(c.readerErr)
	c.readerErrMu.Unlock()
}
func (c *ProcessClient) diagnosticError(err error) error {
	if c.sensitiveDiagnostics.Load() {
		return sanitizedSensitiveError(err)
	}
	return err
}
func (b *lockedBuffer) suppress() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.suppressed = true
	clear(b.b.Bytes())
	b.b.Reset()
	b.truncated = false
}

func (h *Host) ownerDiagnosticError(owner string, err error) error {
	if _, sensitive := h.sensitiveOwners.Load(owner); sensitive {
		return sanitizedSensitiveError(err)
	}
	return err
}

func (h *Host) markSensitiveOwner(owner string) {
	h.sensitiveOwners.Store(owner, true)
	h.mu.Lock()
	defer h.mu.Unlock()
	for contribution := range h.diagnostics[owner] {
		h.diagnostics[owner][contribution] = sensitiveDiagnostic
	}
}
