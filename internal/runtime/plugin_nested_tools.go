package runtime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
)

type nestedToolInvoker struct{ parent *kernelHostServices }

func (k *nestedToolInvoker) ID() string                { return k.parent.ID() }
func (k *nestedToolInvoker) Status() pluginhost.Status { return k.parent.Status() }
func (k *nestedToolInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if params.Method != pluginhost.KernelServiceMethod {
		return nil, serviceError("method_not_found", "kernel service method is unavailable")
	}
	var input pluginhost.InvokeToolParams
	if err := json.Unmarshal(params.Params, &input); err != nil || strings.TrimSpace(input.CallID) == "" || strings.TrimSpace(input.Name) == "" || !json.Valid(input.Arguments) {
		return nil, serviceError("invalid_request", "invoke-tool requires execution_id, call_id, name and JSON arguments")
	}
	k.parent.mu.RLock()
	executions := k.parent.executions
	k.parent.mu.RUnlock()
	if executions == nil {
		return nil, serviceError("service_unavailable", "execution scope is unavailable")
	}
	scope, scopeErr := executions.ResolveToolExecution(params.Caller, input.ExecutionID)
	if scopeErr != nil {
		return nil, scopeErr
	}
	if err := scope.Context.Err(); err != nil {
		return nil, err
	}
	executor, ok := toolctx.Nested(scope.Context)
	if !ok {
		return nil, serviceError("invalid_execution_scope", "invoke-tool requires an orchestrator tool execution")
	}
	// A service transport cancellation and generation retirement must each stop
	// the child, even if the other context remains live.
	childCtx, cancel := context.WithCancel(scope.Context)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	result, err := executor.Invoke(childCtx, providers.ToolCall{ID: input.CallID, Name: input.Name, Arguments: string(input.Arguments)})
	if err != nil {
		return nil, err
	}
	return marshalServiceResult(result)
}
