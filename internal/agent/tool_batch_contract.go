package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolbatch"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type batchResources struct {
	runtime *TurnToolRuntime
	calls   []providers.ToolCall
	mu      sync.Mutex
	closed  bool
}

func (b *batchResources) run(index int) (*toolRun, error) {
	if index < 0 || index >= len(b.calls) {
		return nil, errors.New("tool batch call index is out of range")
	}
	b.runtime.mu.Lock()
	defer b.runtime.mu.Unlock()
	return b.runtime.byID[b.calls[index].ID], nil
}
func (b *batchResources) Resolve(ctx context.Context, index int) (toolbatch.Resolution, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return toolbatch.Resolution{}, errors.New("tool batch is closed")
	}
	if err := ctx.Err(); err != nil {
		return toolbatch.Resolution{}, err
	}
	run, err := b.run(index)
	if err != nil {
		return toolbatch.Resolution{}, err
	}
	call := run.call
	result := toolbatch.Resolution{Call: providers.CloneToolCall(call), ConcurrencySafe: run.concurrencySafe, Orchestrator: run.orchestrator}
	if err := validateRuntimeToolArguments(call); err != nil {
		result.Error = err.Error()
	}
	return result, nil
}
func (b *batchResources) Execute(ctx context.Context, index int) (toolbatch.Outcome, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return toolbatch.Outcome{}, errors.New("tool batch is closed")
	}
	run, err := b.run(index)
	if err != nil {
		b.mu.Unlock()
		return toolbatch.Outcome{}, err
	}
	if err := ctx.Err(); err != nil {
		b.claimSettlement(run, toolresult.FromErrorText(errorJSON(err)), context.WithoutCancel(ctx))
	} else {
		b.runtime.mu.Lock()
		b.runtime.startRunLocked(ctx, run, false)
		b.runtime.mu.Unlock()
	}
	b.mu.Unlock()
	// The callback must retain its provider generation until the physical effect
	// and durable settlement finish, even if its transport cancels first.
	result, err := run.wait(context.WithoutCancel(ctx))
	return toolbatch.Outcome{Result: result.Clone(), InvocationID: run.invocationID}, err
}
func (b *batchResources) Settle(ctx context.Context, index int, result toolresult.Result) (toolbatch.Outcome, error) {
	if err := result.Validate(); err != nil {
		return toolbatch.Outcome{}, err
	}
	if !result.IsError {
		return toolbatch.Outcome{}, errors.New("unexecuted calls may only settle as errors")
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return toolbatch.Outcome{}, errors.New("tool batch is closed")
	}
	run, err := b.run(index)
	if err != nil {
		b.mu.Unlock()
		return toolbatch.Outcome{}, err
	}
	b.claimSettlement(run, result.Clone(), context.WithoutCancel(ctx))
	b.mu.Unlock()
	value, err := run.wait(context.WithoutCancel(ctx))
	return toolbatch.Outcome{Result: value.Clone(), InvocationID: run.invocationID}, err
}
func (b *batchResources) claimSettlement(run *toolRun, result toolresult.Result, ctx context.Context) {
	run.mu.Lock()
	if run.state != toolRunQueued {
		run.mu.Unlock()
		return
	}
	run.state = toolRunRunning
	run.mu.Unlock()
	// Malformed function arguments require the canonical error kind so history
	// validation and recovery can pair the original raw call with this rejection.
	if err := validateRuntimeToolArguments(run.call); err != nil {
		result = toolresult.FromErrorText(errorJSON(err))
	}
	if finalizer, ok := b.runtime.executor.(ToolResultFinalizer); ok {
		result = finalizer.FinalizeToolResult(run.call, result)
	}
	var err error
	if b.runtime.ledger != nil {
		// Rejection is an invocation outcome, not a tool effect. Enter the
		// ledger lifecycle before settling, just like argument-validation errors.
		err = b.runtime.ledger.Start(ctx, run.invocationID)
		if err == nil {
			err = b.runtime.ledger.Settle(ctx, run.invocationID, result)
		}
	}
	run.complete(result, err)
}
func (b *batchResources) Cancel(ctx context.Context, index int) (toolbatch.Outcome, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return toolbatch.Outcome{}, errors.New("tool batch is closed")
	}
	run, err := b.run(index)
	if err != nil {
		b.mu.Unlock()
		return toolbatch.Outcome{}, err
	}
	b.claimSettlement(run, toolresult.FromErrorText(errorJSON(context.Canceled)), context.WithoutCancel(ctx))
	run.mu.Lock()
	cancel := run.cancel
	run.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	b.mu.Unlock()
	value, err := run.wait(context.WithoutCancel(ctx))
	return toolbatch.Outcome{Result: value.Clone(), InvocationID: run.invocationID}, err
}

// executeSelectedToolBatch leaves the batch algorithm with the selected provider.
// The resource adapter preserves @1 hooks, authorization, nested dispatch, result
// budgeting and receipts. A lost or malformed provider reply is never replayed.
func executeSelectedToolBatch(ctx context.Context, executor toolbatch.Executor, request toolbatch.Request, config ToolRuntimeConfig, onResult func(providers.ToolCall, toolresult.Result), existing ...*TurnToolRuntime) ([]providers.ChatMessage, []ContextSegment, error) {
	if len(request.Calls) == 0 || len(request.Calls) > toolbatch.MaxCalls {
		return nil, nil, errors.New("tool batch must contain between 1 and 1024 calls")
	}
	bytes := 0
	for _, call := range request.Calls {
		bytes += len(call.Arguments)
	}
	if bytes > toolbatch.MaxArgumentBytes {
		return nil, nil, errors.New("tool batch arguments exceed 4 MiB")
	}
	if err := providers.ValidateAssistantToolCalls(request.Calls); err != nil {
		return nil, nil, err
	}
	request.Calls = providers.CloneToolCalls(request.Calls)
	var runtime *TurnToolRuntime
	if len(existing) > 0 {
		runtime = existing[0]
	}
	if runtime == nil {
		runtime = NewTurnToolRuntime(config)
	}
	runtime.SetResultCallback(onResult)
	if err := runtime.registerFinalCalls(ctx, request.Calls); err != nil {
		runtime.Close()
		return nil, nil, err
	}
	resources := &batchResources{runtime: runtime, calls: providers.CloneToolCalls(request.Calls)}
	runErr := func() (err error) {
		defer func() {
			if panicValue := recover(); panicValue != nil {
				err = fmt.Errorf("tool batch executor panicked: %v", panicValue)
			}
		}()
		providerRequest := request
		providerRequest.Calls = providers.CloneToolCalls(request.Calls)
		return executor.ExecuteBatch(ctx, providerRequest, resources)
	}()
	// Seal the callback scope before canceling work: late callbacks cannot start
	// effects after the executor returns, including an unawaited process callback.
	resources.mu.Lock()
	resources.closed = true
	resources.mu.Unlock()
	runtime.Close()
	settleCtx := context.WithoutCancel(ctx)
	messages := make([]providers.ChatMessage, 0, len(request.Calls))
	discovery, hasDiscovery := config.Executor.(ToolDiscoveryProvider)
	callContext, hasCallContext := config.Executor.(ToolCallContextProvider)
	legacyContext, hasLegacyContext := config.Executor.(ToolContextProvider)
	var segments []ContextSegment
	for i, call := range request.Calls {
		run, _ := resources.run(i)
		run.mu.Lock()
		pending := run.state == toolRunQueued
		run.mu.Unlock()
		if pending {
			reason := runErr
			if reason == nil {
				reason = ctx.Err()
			}
			if reason == nil {
				reason = errors.New("tool batch executor returned without settling every call")
				runErr = reason
			}
			resources.claimSettlement(run, toolresult.FromErrorText(errorJSON(reason)), settleCtx)
		}
		result, err := run.wait(settleCtx)
		if err != nil {
			runErr = errors.Join(runErr, err)
			continue
		}
		runtime.notifyResult(call, result, nil)
		messages = append(messages, toolResultMessage(call, run.invocationID, result, discovery, hasDiscovery))
		if extra := takeToolAdditionalContext(call, callContext, hasCallContext, legacyContext, hasLegacyContext); extra != "" {
			segments = append(segments, postToolAdditionalContextSegment(call.Name, extra))
		}
	}
	segments = append(segments, runtime.TakeRequestContextSegments()...)
	return messages, segments, errors.Join(runErr, ctx.Err())
}
