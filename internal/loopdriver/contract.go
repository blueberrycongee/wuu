// Package loopdriver defines the experimental boundary between a loop policy
// and the kernel-owned model/tool execution gateway.
package loopdriver

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const ContractVersion = 1

type Descriptor struct {
	ID           string   `json:"id"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// Binding is the durable identity of a selected loop implementation. A new
// default never changes an existing binding; switching requires an explicit
// migration of its state and checkpoint.
type Binding = pluginprotocol.DriverBinding

// AcquireFunc retains the selected provider until release is called. A nil
// expected binding selects the configured default; a saved binding must match
// exactly. Success returns a non-nil driver and release function.
type AcquireFunc func(ctx context.Context, expected *Binding) (driver Driver, binding Binding, release func(), err error)

type ExecutionContext struct {
	SessionID   string
	ExecutionID string
}

type PersistedInput struct {
	Messages []providers.ChatMessage
}

type LoopPolicy struct {
	ModelRoundLimit   int
	DisableTools      bool
	DisableCompaction bool
}

type ModelLoopReceipt struct {
	ID string
}

type TerminalStatus string

const (
	TerminalSucceeded TerminalStatus = "succeeded"
	TerminalFailed    TerminalStatus = "failed"
	TerminalCanceled  TerminalStatus = "canceled"
)

type Checkpoint struct {
	ContractVersion int             `json:"contract_version"`
	DriverID        string          `json:"driver_id"`
	DriverVersion   string          `json:"driver_version"`
	State           json.RawMessage `json:"state,omitempty"`
}

type TerminalOutcome struct {
	Status     TerminalStatus
	ReceiptID  string
	Checkpoint Checkpoint
}

type KernelGateway interface {
	ExecuteModelLoop(context.Context, PersistedInput, LoopPolicy) (ModelLoopReceipt, error)
	WriteCheckpoint(context.Context, Checkpoint) error
}

type Driver interface {
	Descriptor(context.Context) (Descriptor, error)
	Create(context.Context, ExecutionContext, PersistedInput) (Instance, error)
	Resume(context.Context, ExecutionContext, PersistedInput, Checkpoint) (Instance, error)
}

type Instance interface {
	Run(context.Context, KernelGateway) (TerminalOutcome, error)
	Checkpoint() Checkpoint
	Cancel(reason string)
	Shutdown()
}

// BindingStore commits admission before a driver can create an instance.
// Bind must compare an existing identity atomically and never replace it.
type BindingStore interface {
	LoadBinding(context.Context) (Binding, bool, error)
	Bind(context.Context, Binding) error
}

type CheckpointStore interface {
	Load(context.Context) (Checkpoint, bool, error)
	Save(context.Context, Checkpoint) error
}

type executionContextKey struct{}

func WithExecutionContext(ctx context.Context, execution ExecutionContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, executionContextKey{}, execution)
}

func ExecutionContextFromContext(ctx context.Context) ExecutionContext {
	if ctx == nil {
		return ExecutionContext{}
	}
	execution, _ := ctx.Value(executionContextKey{}).(ExecutionContext)
	return execution
}
