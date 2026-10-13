package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

type modelAdapterClient struct {
	host  *pluginhost.Host
	inner providers.StreamClient
}

// The wrapper retains its generation's host rather than consulting the current
// session on every event. Revocation still cancels the exact capability scope.
func pluginModelAdapterClient(host *pluginhost.Host, client providers.StreamClient) providers.StreamClient {
	return &modelAdapterClient{host: host, inner: client}
}

func selectedModelAdapter(req providers.ChatRequest) (providers.ChatRequest, error) {
	if value, exists := req.ProviderOptions["adapter"]; exists {
		adapter, ok := value.(string)
		if !ok {
			return req, errors.New("model adapter option must be a string")
		}
		if req.Adapter == "" {
			req.Adapter = adapter
		}
		req.ProviderOptions = providers.CloneRequestObject(req.ProviderOptions)
		delete(req.ProviderOptions, "adapter")
	}
	req.Adapter = strings.TrimSpace(req.Adapter)
	return req, nil
}

func (c *modelAdapterClient) PrepareInferenceRequest(ctx context.Context, req providers.ChatRequest) (providers.ChatRequest, error) {
	req, err := selectedModelAdapter(req)
	if err != nil {
		return req, err
	}
	if req.Adapter != "" && req.Adapter != "native" {
		if _, err := c.host.ModelAdapter(req.Adapter); err != nil {
			return req, err
		}
		return req, nil
	}
	if preparer, ok := c.inner.(providers.InferenceRequestPreparer); ok {
		return preparer.PrepareInferenceRequest(ctx, req)
	}
	return req, nil
}

func (c *modelAdapterClient) ResolveMediaInput(policy providers.MediaInputPolicy, options map[string]any) providers.MediaInputPolicy {
	return providers.ResolveMediaInput(c.inner, policy, options)
}

func (c *modelAdapterClient) NativeCompactionAvailable() bool {
	compactor, ok := c.inner.(providers.NativeCompactor)
	return ok && compactor.NativeCompactionAvailable()
}

func (c *modelAdapterClient) NativeCompact(ctx context.Context, req providers.ChatRequest) (providers.NativeCompactionResult, error) {
	req, err := selectedModelAdapter(req)
	if err != nil {
		return providers.NativeCompactionResult{}, err
	}
	if req.Adapter != "" && req.Adapter != "native" {
		return providers.NativeCompactionResult{}, providers.ErrNativeCompactionUnavailable
	}
	compactor, ok := c.inner.(providers.NativeCompactor)
	if !ok {
		return providers.NativeCompactionResult{}, providers.ErrNativeCompactionUnavailable
	}
	return compactor.NativeCompact(ctx, req)
}

func (c *modelAdapterClient) PrewarmSession(ctx context.Context, sessionID string) error {
	if prewarmer, ok := c.inner.(providers.SessionPrewarmer); ok {
		return prewarmer.PrewarmSession(ctx, sessionID)
	}
	return nil
}

func (c *modelAdapterClient) ApplyInferenceRecovery(ctx context.Context, plan providers.RecoveryPlan) error {
	if applier, ok := c.inner.(providers.InferenceRecoveryApplier); ok {
		return applier.ApplyInferenceRecovery(ctx, plan)
	}
	return errors.New("model provider does not support this recovery action")
}

func (c *modelAdapterClient) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	req, err := selectedModelAdapter(req)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	if req.Adapter == "" || req.Adapter == "native" {
		return c.inner.Chat(ctx, req)
	}
	capability, err := c.host.ModelAdapter(req.Adapter)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	req, err = providers.EnsureInferenceAttemptContext(ctx, req, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	submission, err := admitPluginModelSubmission(req, capability.Adapter.Mode)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	response, callErr := invokePluginModelChat(ctx, c.host, capability.RegisteredCapability, pluginapi.ModelAdapterInput{
		AdapterID: req.Adapter, Request: logicalModelRequest(req), Stream: false,
	})
	callErr = errors.Join(callErr, settlePluginModelSubmission(req, submission, response.Usage, callErr))
	return response, callErr
}

func (c *modelAdapterClient) OwnsStreamAccounting() bool { return true }

func (c *modelAdapterClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	req, err := selectedModelAdapter(req)
	if err != nil {
		return nil, err
	}
	if req.Adapter == "" || req.Adapter == "native" {
		return c.nativeModelStream(ctx, req)
	}
	capability, err := c.host.ModelAdapter(req.Adapter)
	if err != nil {
		return nil, err
	}
	req, err = providers.EnsureInferenceAttemptContext(ctx, req, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		return nil, err
	}
	submission, err := admitPluginModelSubmission(req, capability.Adapter.Mode)
	if err != nil {
		return nil, err
	}
	stream, err := invokePluginModelStream(ctx, c.host, capability.RegisteredCapability, pluginapi.ModelAdapterInput{
		AdapterID: req.Adapter, Request: logicalModelRequest(req), Stream: true,
	}, submission)
	if err != nil {
		return nil, errors.Join(err, settlePluginModelSubmission(req, submission, nil, err))
	}
	out := make(chan providers.StreamEvent, 32)
	go func() {
		defer close(out)
		settled := false
		defer func() {
			if !settled {
				cause := ctx.Err()
				if cause == nil {
					cause = providers.NewIncompleteStreamError("model adapter stream ended without a terminal event")
				}
				_ = settlePluginModelSubmission(req, submission, nil, cause)
			}
		}()
		for event := range stream {
			// Usage is recorded at the sink before buffering. Replaying it here
			// could replace a newer receipt with an older queued event.
			observation := event
			observation.Usage = nil
			if err := req.Attempt.ObserveStreamEvent(observation); err != nil {
				event = providers.StreamEvent{Type: providers.EventError, Error: err}
			}
			if event.Type == providers.EventDone || event.Type == providers.EventError {
				settled = true
				if err := settlePluginModelSubmission(req, submission, nil, event.Error); err != nil {
					event = providers.StreamEvent{Type: providers.EventError, Error: err}
				}
			}
			select {
			case out <- event:
			case <-ctx.Done():
				if !settled {
					settled = true
					_ = req.Attempt.ObserveStreamEvent(providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()})
				}
				// The sink already owns receipts; only release its producer.
				go func() {
					for range stream {
					}
				}()
				return
			}
		}
	}()
	return out, nil
}

// Native streams are observed at the selected physical provider boundary,
// including continuations that deliberately bypass a second retry executor.
func (c *modelAdapterClient) nativeModelStream(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	stream, err := c.inner.StreamChat(ctx, req)
	if err == nil && stream == nil {
		err = errors.New("native model stream is unavailable")
	}
	if err != nil {
		return nil, errors.Join(err, req.Attempt.ObserveStreamEvent(providers.StreamEvent{Type: providers.EventError, Error: err}))
	}
	out := make(chan providers.StreamEvent, 32)
	go func() {
		defer close(out)
		terminal := false
		defer func() {
			if !terminal {
				cause := ctx.Err()
				if cause == nil {
					cause = providers.NewIncompleteStreamError("native model stream ended without a terminal event")
				}
				_ = req.Attempt.ObserveStreamEvent(providers.StreamEvent{Type: providers.EventError, Error: cause})
			}
		}()
		for {
			var event providers.StreamEvent
			var open bool
			select {
			case event, open = <-stream:
				if !open {
					return
				}
			case <-ctx.Done():
				drainModelReceipt(stream, req.Attempt, false)
				return
			}
			if err := req.Attempt.ObserveStreamEvent(event); err != nil {
				event = providers.StreamEvent{Type: providers.EventError, Error: err}
			}
			if event.Type == providers.EventDone || event.Type == providers.EventError {
				terminal = true
			}
			select {
			case out <- event:
			case <-ctx.Done():
				drainModelReceipt(stream, req.Attempt, false)
				return
			}
			if event.Type == providers.EventDone || event.Type == providers.EventError {
				drainModelReceipt(stream, req.Attempt, event.Type == providers.EventDone)
				return
			}
		}
	}()
	return out, nil
}

// Cancellation must release the logical consumer immediately. Continue draining
// the physical producer so buffered usage is retained and its lease can finish;
// transport clients close promptly on cancellation under the stream contract.
func drainModelReceipt(stream <-chan providers.StreamEvent, attempt providers.InferenceAttempt, done bool) {
	go func() {
		for event := range stream {
			if done {
				continue
			}
			_ = attempt.ObserveStreamEvent(event)
			done = event.Type == providers.EventDone
		}
	}()
}

// Direct adapters receive durable, potentially billable admission before any
// callback runs. Delegating adapters have only logical operation accounting;
// each child host invocation admits and reports its own physical submissions.
func admitPluginModelSubmission(req providers.ChatRequest, mode string) (providers.InferenceSubmission, error) {
	if mode == "delegate" {
		return providers.InferenceSubmission{}, nil
	}
	return req.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{
		Provider: req.Provider, Protocol: "plugin:" + req.Adapter, Transport: "extension", Mode: "model_adapter",
	})
}

func settlePluginModelSubmission(req providers.ChatRequest, submission providers.InferenceSubmission, usage *providers.TokenUsage, callErr error) error {
	if !submission.Valid() {
		return nil
	}
	if callErr != nil {
		submission.ObserveUsage(usage)
		submission.CompleteFailure(providers.NormalizeFailure(callErr))
	} else {
		submission.CompleteSuccess(usage)
	}
	return req.Attempt.JournalError()
}

func invokePluginModelChat(ctx context.Context, host *pluginhost.Host, capability pluginhost.RegisteredCapability, input any) (providers.ChatResponse, error) {
	var output pluginapi.ModelAdapterOutput
	if err := host.InvokeCapability(ctx, capability, input, &output); err != nil {
		return providers.ChatResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return providers.ChatResponse{}, err
	}
	if output.Response == nil {
		return providers.ChatResponse{}, errors.New("model capability returned no response")
	}
	return modelResponseFromLogical(*output.Response)
}

// Streaming capability invocations share one execution-scoped event sink.
// Terminal events are held until the callback returns, so an exception after
// emitting done cannot silently turn an unsuccessful invocation into success.
func invokePluginModelStream(ctx context.Context, host *pluginhost.Host, capability pluginhost.RegisteredCapability, input any, submission providers.InferenceSubmission) (<-chan providers.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(chan providers.StreamEvent, 32)
	go func() {
		defer close(out)
		var mu sync.Mutex
		var terminal *providers.StreamEvent
		var emitErr error
		emitted := false
		closed := false
		streamCtx := pluginhost.WithModelEventSink(ctx, capability.PluginID, func(callCtx context.Context, wire pluginapi.ModelStreamEventV1) error {
			mu.Lock()
			defer mu.Unlock()
			if emitErr != nil {
				return emitErr
			}
			if closed || terminal != nil {
				emitErr = errors.New("model stream is already terminal")
				return emitErr
			}
			event, err := modelStreamEventFromLogical(wire)
			if err != nil {
				emitErr = err
				return err
			}
			// Only direct adapters receive a physical submission. Preserve their
			// validated receipts even if callback failure or cancellation prevents
			// delivery of the held terminal event.
			submission.ObserveUsage(event.Usage)
			emitted = true
			if event.Type == providers.EventDone || event.Type == providers.EventError {
				terminal = &event
				return nil
			}
			select {
			case out <- event:
				return nil
			case <-ctx.Done():
				emitErr = ctx.Err()
			case <-callCtx.Done():
				emitErr = callCtx.Err()
			}
			return emitErr
		})
		var output pluginapi.ModelAdapterOutput
		callErr := host.InvokeCapability(streamCtx, capability, input, &output)
		mu.Lock()
		closed = true
		callErr = errors.Join(callErr, emitErr)
		if callErr == nil && emitted && output.Response != nil {
			callErr = errors.New("model capability cannot both emit events and return a response")
		}
		if callErr == nil && !emitted && output.Response != nil {
			response, err := modelResponseFromLogical(*output.Response)
			submission.ObserveUsage(response.Usage)
			if err != nil {
				callErr = err
			} else {
				events, err := providers.AdaptStreamClient(modelResponseClient{response: response}).StreamChat(ctx, providers.ChatRequest{})
				if err != nil {
					callErr = err
				} else {
					mu.Unlock()
					for event := range events {
						select {
						case out <- event:
						case <-ctx.Done():
							return
						}
					}
					return
				}
			}
		}
		if callErr == nil && terminal == nil {
			callErr = errors.New("model capability stream ended without a terminal event")
		}
		if callErr != nil {
			failure := providers.StreamEvent{Type: providers.EventError, Error: callErr}
			if terminal != nil {
				failure.Usage = terminal.Usage
			}
			terminal = &failure
		}
		mu.Unlock()
		select {
		case out <- *terminal:
		case <-ctx.Done():
		}
	}()
	return out, nil
}

type modelResponseClient struct{ response providers.ChatResponse }

func (c modelResponseClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return c.response, nil
}

type modelEmitInvoker struct{ parent *kernelHostServices }

func (k *modelEmitInvoker) ID() string                { return k.parent.ID() }
func (k *modelEmitInvoker) Status() pluginhost.Status { return k.parent.Status() }

func (k *modelEmitInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if params.Method != pluginhost.KernelServiceMethod {
		return nil, serviceError("method_not_found", "model event method is unavailable")
	}
	k.parent.mu.RLock()
	host, active := k.parent.modelHost, k.parent.active
	k.parent.mu.RUnlock()
	if !active || host == nil {
		return nil, serviceError("service_unavailable", "model event service is unavailable")
	}
	var event pluginapi.ModelStreamEventV1
	if err := decodeServiceParams(params.Params, &event); err != nil {
		return nil, err
	}
	if err := host.EmitModelEvent(ctx, params.Caller, params.ExecutionID, event); err != nil {
		var serviceErr *pluginhost.HostServiceError
		if errors.As(err, &serviceErr) {
			return nil, serviceErr
		}
		return nil, serviceError("invalid_model_event", fmt.Sprintf("model event rejected: %v", err))
	}
	return json.RawMessage(`{}`), nil
}
