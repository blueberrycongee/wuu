package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
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
	output, err := c.auxiliary(ctx, "prepare", req, "", "")
	if err != nil {
		return req, err
	}
	if output.Request == nil {
		return req, errors.New("model adapter omitted prepared request")
	}
	return modelwire.ApplyRequest(req, *output.Request)
}
func (c *modelAdapterClient) ResolveMediaInput(policy providers.MediaInputPolicy, options map[string]any) providers.MediaInputPolicy {
	req := c.baseRequest()
	req.MediaInput = policy
	req.ProviderOptions = options
	output, err := c.auxiliary(context.Background(), "media", req, "", "")
	if err != nil || output.Media == nil {
		return policy
	}
	wire := modelwire.RequestToWire(req)
	wire.MediaInput = *output.Media
	result, err := modelwire.ApplyRequest(req, wire)
	if err != nil {
		return policy
	}
	return result.MediaInput
}
func (c *modelAdapterClient) NativeCompactionAvailable() bool {
	_, capability, err := c.selected(context.Background(), c.baseRequest())
	if err != nil || !slices.Contains(capability.Adapter.Operations, "compact") {
		return false
	}
	if slices.Contains(capability.Adapter.Operations, "compact.supported") {
		output, err := c.auxiliary(context.Background(), "compact.supported", c.baseRequest(), "", "")
		return err == nil && output.Supported
	}
	return true
}
func (c *modelAdapterClient) NativeCompact(ctx context.Context, req providers.ChatRequest) (providers.NativeCompactionResult, error) {
	output, err := c.auxiliary(ctx, "compact", req, "", "")
	if err != nil {
		return providers.NativeCompactionResult{}, err
	}
	if !output.Supported {
		return providers.NativeCompactionResult{}, providers.ErrNativeCompactionUnavailable
	}
	wire := modelwire.RequestToWire(req)
	wire.Messages = output.Replacement
	result, err := modelwire.ApplyRequest(req, wire)
	if err != nil {
		return providers.NativeCompactionResult{}, err
	}
	response := modelwire.ChatResponseFromWire(pluginprotocol.ModelResponseV1{Usage: output.Usage})
	return providers.NativeCompactionResult{Replacement: result.Messages, Usage: response.Usage, ProviderStateScope: output.ProviderStateScope}, nil
}
func (c *modelAdapterClient) PrewarmSession(ctx context.Context, id string) error {
	_, err := c.auxiliary(ctx, "prewarm", c.baseRequest(), id, "")
	return err
}
func (c *modelAdapterClient) ApplyInferenceRequestRecovery(ctx context.Context, req providers.ChatRequest, plan providers.RecoveryPlan) error {
	_, err := c.auxiliary(ctx, "recover", req, "", string(plan.Action))
	return err
}
func (c *modelAdapterClient) ApplyInferenceRecovery(ctx context.Context, plan providers.RecoveryPlan) error {
	_, err := c.auxiliary(ctx, "recover", c.baseRequest(), "", string(plan.Action))
	return err
}

func (c *modelAdapterClient) Chat(ctx context.Context, req providers.ChatRequest) (responseResult providers.ChatResponse, resultErr error) {
	req, capability, err := c.selected(ctx, req)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	req, err = providers.EnsureInferenceAttemptContext(ctx, req, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	metrics := beginModelRequestMetrics(ctx, c.host, req, false)
	logicalCtx := ctx
	defer func() {
		metrics.usage(responseResult.Usage)
		metrics.finishError(logicalCtx, resultErr)
	}()
	scope, err := c.host.OpenServiceScope(ctx, modelExecutionConsumer, capacityPolicyService, capacityResourceService)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, scope.Close()) }()
	ctx = scope.Context
	submission, capacity, err := admitPluginModelSubmission(ctx, c.host, req, capability.Adapter.Mode)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	receipt, release := adapterReceipt(ctx, c.host, capability, req)
	defer release()
	response, callErr := invokePluginModelChat(ctx, c.host, capability.RegisteredCapability, pluginprotocol.ModelAdapterInput{ConfiguredModel: c.configuredModel(), AdapterID: req.Adapter, Request: modelwire.RequestToWire(req), Stream: false, Receipt: receipt})
	callErr = errors.Join(callErr, settlePluginModelSubmission(req, submission, response.Usage, callErr))
	callErr = errors.Join(callErr, settleModelCapacityError(capacity, callErr))
	return response, callErr
}

func (c *modelAdapterClient) OwnsStreamAccounting() bool { return true }

func (c *modelAdapterClient) StreamChat(ctx context.Context, req providers.ChatRequest) (result <-chan providers.StreamEvent, resultErr error) {
	req, capability, err := c.selected(ctx, req)
	if err != nil {
		return nil, err
	}
	ctx, endReceipt, err := providers.BeginModelReceipt(ctx)
	if err != nil {
		return nil, err
	}
	streamOwnsReceipt := false
	defer func() {
		if !streamOwnsReceipt {
			endReceipt(nil)
		}
	}()
	req, err = providers.EnsureInferenceAttemptContext(ctx, req, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		return nil, err
	}
	metrics := beginModelRequestMetrics(ctx, c.host, req, true)
	logicalCtx := ctx
	streamOwnsMetrics := false
	defer func() {
		if !streamOwnsMetrics {
			metrics.finishError(logicalCtx, resultErr)
		}
	}()
	scope, err := c.host.OpenServiceScope(ctx, modelExecutionConsumer, capacityPolicyService, capacityResourceService)
	if err != nil {
		return nil, err
	}
	ownsScope := false
	defer func() {
		if !ownsScope {
			_ = scope.Close()
		}
	}()
	ctx = scope.Context
	releasePhysical, err := req.Attempt.RetainPhysicalReceipt()
	if err != nil {
		return nil, err
	}
	physicalTransferred := false
	defer func() {
		if !physicalTransferred {
			_ = releasePhysical()
		}
	}()
	submission, capacity, err := admitPluginModelSubmission(ctx, c.host, req, capability.Adapter.Mode)
	if err != nil {
		return nil, err
	}
	receipt, release := adapterReceipt(ctx, c.host, capability, req)
	streamOwnsReceipt = true
	ownsScope = true
	physicalTransferred = true
	stream, err := invokePluginModelStreamWithCapacity(ctx, c.host, capability.RegisteredCapability, pluginprotocol.ModelAdapterInput{ConfiguredModel: c.configuredModel(), AdapterID: req.Adapter, Request: modelwire.RequestToWire(req), Stream: true, Receipt: receipt}, submission, capacity, func() { endReceipt(errors.Join(releasePhysical(), req.Attempt.JournalError(), scope.Close())) }, release)
	if err != nil {
		return nil, errors.Join(err, settlePluginModelSubmission(req, submission, nil, err))
	}
	out := make(chan providers.StreamEvent, 32)
	streamOwnsReceipt = true
	streamOwnsMetrics = true
	go func() {
		defer close(out)
		defer func() {
			if logicalCtx.Err() != nil {
				metrics.finish("cancelled")
			} else {
				metrics.finish("incomplete")
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
			case <-logicalCtx.Done():
				return
			}
			// Usage is recorded at the sink before buffering. Replaying it here
			// could replace a newer receipt with an older queued event.
			observation := event
			observation.Usage = nil
			if err := req.Attempt.ObserveStreamEvent(observation); err != nil {
				event = providers.StreamEvent{Type: providers.EventError, Error: err}
			}
			metrics.observe(event)
			if event.Type == providers.EventDone || event.Type == providers.EventError {
				metrics.finishError(logicalCtx, event.Error)
			}
			select {
			case out <- event:
			case <-logicalCtx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Direct adapters receive durable, potentially billable admission before any
// callback runs. Delegating adapters have only logical operation accounting;
// each child host invocation admits and reports its own physical submissions.
func admitPluginModelSubmission(ctx context.Context, host *pluginhost.Host, req providers.ChatRequest, mode string) (providers.InferenceSubmission, *modelCapacityAdmission, error) {
	if mode == "delegate" || mode == "receipt" {
		return providers.InferenceSubmission{}, nil, nil
	}
	capacity, err := acquireModelCapacity(ctx, host, "provider:"+req.Provider, req.Attempt)
	if err != nil {
		return providers.InferenceSubmission{}, nil, err
	}
	submission, err := req.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: req.Provider, Protocol: "plugin:" + req.Adapter, Transport: "extension", Mode: "model_adapter"})
	if err != nil {
		return providers.InferenceSubmission{}, nil, errors.Join(err, capacity.settle("abandoned", nil))
	}
	return submission, capacity, nil
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
	var output pluginprotocol.ModelAdapterOutput
	callErr := host.InvokeCapability(ctx, capability, input, &output)
	callErr = errors.Join(callErr, ctx.Err())
	if output.Error != nil {
		callErr = errors.Join(callErr, modelErrorFromWire(output.Error))
	}
	if output.Response == nil {
		if callErr == nil {
			callErr = errors.New("model capability returned no response")
		}
		return providers.ChatResponse{}, callErr
	}
	response, decodeErr := modelResponseFromLogical(*output.Response)
	if callErr != nil {
		// A late physical receipt is accounting evidence, never canceled content.
		return providers.ChatResponse{Usage: response.Usage}, errors.Join(callErr, decodeErr)
	}
	return response, decodeErr
}

// Streaming capability invocations share one execution-scoped event sink.
// Terminal events are held until the callback returns, so an exception after
// emitting done cannot silently turn an unsuccessful invocation into success.
func invokePluginModelStream(ctx context.Context, host *pluginhost.Host, capability pluginhost.RegisteredCapability, input any, submission providers.InferenceSubmission, cleanup ...func()) (<-chan providers.StreamEvent, error) {
	return invokePluginModelStreamWithCapacity(ctx, host, capability, input, submission, nil, cleanup...)
}
func invokePluginModelStreamWithCapacity(ctx context.Context, host *pluginhost.Host, capability pluginhost.RegisteredCapability, input any, submission providers.InferenceSubmission, capacity *modelCapacityAdmission, cleanup ...func()) (<-chan providers.StreamEvent, error) {
	launched := false
	defer func() {
		if !launched {

			if capacity != nil {
				_ = capacity.settle("abandoned", nil)
			}
			for i := len(cleanup) - 1; i >= 0; i-- {
				cleanup[i]()
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, endReceipt, err := providers.BeginModelReceipt(ctx)
	if err != nil {
		return nil, err
	}
	out := make(chan providers.StreamEvent, 32)
	launched = true
	go func() {
		for _, release := range cleanup {
			defer release()
		}
		if capacity != nil {
			defer func() {
				if err := capacity.settle("abandoned", nil); err != nil {
					providers.DebugLogf("model capacity cleanup: %v", err)
				}
			}()
		}
		defer endReceipt(nil)
		defer close(out)
		var mu sync.Mutex
		var terminal *providers.StreamEvent
		var emitErr error
		emitted := false
		closed := false
		streamCtx := pluginhost.WithModelEventSink(ctx, capability.PluginID, func(callCtx context.Context, wire pluginprotocol.ModelStreamEventV1) error {
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
			// The decoder keeps only independently valid physical usage even
			// when logical event metadata is rejected.
			submission.ObserveUsage(event.Usage)
			if err != nil {
				emitErr = err
				return err
			}
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
		var output pluginprotocol.ModelAdapterOutput
		callErr := host.InvokeCapability(streamCtx, capability, input, &output)
		if callErr == nil && output.Error != nil {
			callErr = modelErrorFromWire(output.Error)
		}
		mu.Lock()
		closed = true
		callErr = errors.Join(callErr, emitErr)
		if output.Response != nil {
			response, decodeErr := modelResponseFromLogical(*output.Response)
			// Usage is validated independently and cleared by the decoder when
			// invalid. Unrelated logical metadata must not erase physical cost.
			submission.ObserveUsage(response.Usage)
			callErr = errors.Join(callErr, decodeErr)
		}
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
					submission.CompleteSuccess(response.Usage)
					if capacityErr := settleModelCapacityError(capacity, nil); capacityErr != nil {
						mu.Unlock()
						select {
						case out <- providers.StreamEvent{Type: providers.EventError, Error: capacityErr}:
						case <-ctx.Done():
						}
						return
					}
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
		if terminal != nil {
			if terminal.Error != nil {
				submission.CompleteFailure(providers.NormalizeFailure(terminal.Error))
			} else {
				submission.CompleteSuccess(terminal.Usage)
			}
			if capacityErr := settleModelCapacityError(capacity, terminal.Error); capacityErr != nil {
				terminal = &providers.StreamEvent{Type: providers.EventError, Error: errors.Join(terminal.Error, capacityErr)}
			}
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
	var event pluginprotocol.ModelStreamEventV1
	if err := decodeServiceParams(params.Params, &event); err != nil {
		return nil, err
	}
	scope, scopeErr := params.CallerExecution(false)
	if scopeErr != nil {
		return nil, scopeErr
	}
	if err := host.EmitModelEvent(ctx, scope.PluginID, scope.ID, event); err != nil {
		var serviceErr *pluginhost.HostServiceError
		if errors.As(err, &serviceErr) {
			return nil, serviceErr
		}
		return nil, serviceError("invalid_model_event", fmt.Sprintf("model event rejected: %v", err))
	}
	return json.RawMessage(`{}`), nil
}
