// Package toolbatch defines the versioned tool-batch execution boundary.
// Batch providers own planning, ordering, parallelism and cancellation. Resources
// retain authenticated tool dispatch, effect receipts and exactly-once settlement.
package toolbatch

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

const ServiceName = "agent.tool.batch"
const ResourceService = "tool.batch.resources"
const AdapterModule = "wuu.tools.v1-batch"
const MaxCalls = 1024
const MaxArgumentBytes = 4 << 20

type Request struct {
	ID          string               `json:"id"`
	Calls       []providers.ToolCall `json:"calls"`
	Serial      bool                 `json:"serial"`
	MaxParallel int                  `json:"max_parallel"`
}
type Resolution struct {
	Call            providers.ToolCall `json:"call"`
	ConcurrencySafe bool               `json:"concurrency_safe"`
	Orchestrator    bool               `json:"orchestrator"`
	Error           string             `json:"error,omitempty"`
}
type Outcome struct {
	Result       toolresult.Result `json:"result"`
	InvocationID string            `json:"invocation_id,omitempty"`
}

// Resources accepts an index into the immutable admitted batch, never a caller-
// supplied tool identity. Execute is idempotent within the batch. Settle may only
// reject an unstarted call; it cannot overwrite an effect's observed result.
// Cancel closes admission for one call and waits for its physical completion.
// All returned outcomes are durable when a ledger is configured.
type Resources interface {
	Resolve(context.Context, int) (Resolution, error)
	Execute(context.Context, int) (Outcome, error)
	Settle(context.Context, int, toolresult.Result) (Outcome, error)
	Cancel(context.Context, int) (Outcome, error)
}
type Executor interface {
	ExecuteBatch(context.Context, Request, Resources) error
}
type ExecutorFunc func(context.Context, Request, Resources) error

func (f ExecutorFunc) ExecuteBatch(ctx context.Context, r Request, g Resources) error {
	return f(ctx, r, g)
}

// StreamingExecutor is optional. A selected provider must explicitly admit each
// completed safe-prefix call before it can start during model streaming. Deferred
// calls remain in the final batch; admitted calls share its effect receipt.
type StreamingExecutor interface {
	SupportsStreaming() bool
	AdmitStream(context.Context, StreamRequest) (bool, error)
}
type StreamRequest struct {
	BatchID         string             `json:"batch_id"`
	Call            providers.ToolCall `json:"call"`
	ReadOnly        bool               `json:"read_only"`
	ConcurrencySafe bool               `json:"concurrency_safe"`
}
type StreamAdmission struct {
	Admit bool `json:"admit"`
}

func SupportsStreaming(executor Executor) bool {
	if executor == nil {
		return true
	} // Standalone callers retain the existing runtime.
	streaming, ok := executor.(StreamingExecutor)
	return ok && streaming.SupportsStreaming()
}
