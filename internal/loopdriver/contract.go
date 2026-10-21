// Package loopdriver defines the experimental boundary between a loop policy
// and the kernel-owned model/tool execution gateway.
package loopdriver

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const ContractVersion = 1

type Descriptor struct {
	ID           string
	Version      string
	Capabilities []string
}

type ExecutionContext struct {
	// SourceMessage is captured when the human input is admitted, not inferred from history.
	SourceMessage   *providers.MessageRef
	MessageRelation *providers.MessageRelation
	SessionID       string
	ExecutionID     string
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
	Descriptor() Descriptor
	Create(ExecutionContext, PersistedInput) (Instance, error)
	Resume(ExecutionContext, PersistedInput, Checkpoint) (Instance, error)
}

type Instance interface {
	Run(context.Context, KernelGateway) (TerminalOutcome, error)
	Checkpoint() Checkpoint
	Cancel(reason string)
	Shutdown()
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
