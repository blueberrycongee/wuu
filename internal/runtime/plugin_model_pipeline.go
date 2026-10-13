package runtime

import (
	"context"
	"errors"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type modelRequestOriginKey struct{}

type modelPipelineClient struct {
	host   *pluginhost.Host
	native providers.StreamClient
	inner  providers.StreamClient
	origin pluginhost.ModelInvocationOriginV1
}

func pluginModelClientWrapper(host *pluginhost.Host, threadID string) func(providers.StreamClient) providers.StreamClient {
	if host == nil {
		return nil
	}
	return func(client providers.StreamClient) providers.StreamClient {
		return pluginModelPipelineClient(host, client, pluginhost.ModelInvocationOriginV1{Kind: "agent", SessionID: threadID, ThreadID: threadID})
	}
}

func pluginModelPipelineClient(host *pluginhost.Host, native providers.StreamClient, origin pluginhost.ModelInvocationOriginV1) providers.StreamClient {
	if host == nil {
		return native
	}
	inner := pluginModelAdapterClient(host, native)
	if origin.Kind != "plugin" {
		inner = pluginModelMiddlewareClient(host, inner, origin)
	}
	return &modelPipelineClient{host: host, native: native, inner: inner, origin: origin}
}

// The adapter boundary records physical evidence before middleware and
// response observers can transform its logical presentation.
func (c *modelPipelineClient) OwnsStreamAccounting() bool { return true }

func (c *modelPipelineClient) ResolveMediaInput(policy providers.MediaInputPolicy, options map[string]any) providers.MediaInputPolicy {
	return providers.ResolveMediaInput(c.native, policy, options)
}
func (c *modelPipelineClient) PrepareInferenceRequest(ctx context.Context, req providers.ChatRequest) (providers.ChatRequest, error) {
	var err error
	requestedLimit := req.MaxTokens
	if preparer, ok := c.inner.(providers.InferenceRequestPreparer); ok {
		req, err = preparer.PrepareInferenceRequest(ctx, req)
		if err != nil {
			return req, err
		}
	}
	if c.origin.Kind == "plugin" && requestedLimit > 0 && (req.MaxTokens <= 0 || req.MaxTokens > requestedLimit) {
		return req, serviceError("unsupported_limit", "configured provider cannot preserve the requested output limit")
	}
	if c.origin.Kind == "plugin" || req.Attempt.Valid() {
		return req, nil
	}
	req.Operation = providers.EnsureInferenceOperation(req.Operation, providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
	origin := c.requestOrigin(req)
	ctx = context.WithValue(ctx, modelRequestOriginKey{}, origin)
	for _, capability := range c.host.Capabilities(pluginhost.CapabilityModelRequestTransform) {
		var output pluginhost.ModelRequestMiddlewareOutput
		if err := c.host.InvokeCapability(ctx, capability, pluginhost.ModelRequestMiddlewareInput{Request: logicalModelRequest(req), Origin: origin}, &output); err != nil {
			if handled := c.host.HandleCapabilityError(capability, err); handled != nil {
				return req, handled
			}
			continue
		}
		if output.Request == nil {
			continue
		}
		replacement, err := applyLogicalModelRequest(req, *output.Request)
		if err == nil && (replacement.Provider != req.Provider || replacement.Model != req.Model) {
			err = errors.New("request transforms cannot change the configured model route; use independent inference")
		}
		if err != nil {
			if handled := c.host.HandleCapabilityError(capability, err); handled != nil {
				return req, handled
			}
			continue
		}
		req = replacement
	}
	// Provider normalization must remain the final wire-affecting step before
	// durable hashing, including when a transform selects another adapter.
	if preparer, ok := c.inner.(providers.InferenceRequestPreparer); ok {
		req, err = preparer.PrepareInferenceRequest(ctx, req)
		if err != nil {
			return req, err
		}
	}
	if err := providers.ValidateRequiredMedia(providers.ApplyToolResultProjections(req.Messages), req.MediaInput); err != nil {
		return req, err
	}
	return req, nil
}
func (c *modelPipelineClient) requestOrigin(req providers.ChatRequest) pluginhost.ModelInvocationOriginV1 {
	origin := c.origin
	origin.OperationID = req.Operation.ID
	if req.Operation.ParentOperationID != "" {
		origin.ParentOperationID = req.Operation.ParentOperationID
	}
	origin.StepIndex = req.StepIndex
	return origin
}
func (c *modelPipelineClient) observe(ctx context.Context, req providers.ChatRequest, phase string, result *providers.ChatResponse, event *providers.StreamEvent, cause error) error {
	if c.origin.Kind == "plugin" {
		return nil
	}
	origin := c.requestOrigin(req)
	input := pluginhost.ModelResponseObserveInput{Phase: phase, Request: logicalModelRequest(req), Origin: origin, Error: modelErrorView(cause)}
	if result != nil {
		input.Result = &pluginhost.ModelInvokeResult{Response: logicalModelResponse(*result), Origin: origin}
	}
	if event != nil {
		value := logicalModelStreamEvent(*event)
		input.Event = &value
	}
	ctx = context.WithValue(ctx, modelRequestOriginKey{}, origin)
	for _, capability := range c.host.Capabilities(pluginhost.CapabilityModelRequestObserve) {
		var output struct{}
		if err := c.host.InvokeCapability(ctx, capability, input, &output); err != nil {
			if handled := c.host.HandleCapabilityError(capability, err); handled != nil {
				return handled
			}
		}
	}
	return nil
}
func (c *modelPipelineClient) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	ctx = context.WithValue(ctx, modelRequestOriginKey{}, c.requestOrigin(req))
	if err := c.observe(ctx, req, "request", nil, nil, nil); err != nil {
		return providers.ChatResponse{}, err
	}
	response, err := c.inner.Chat(ctx, req)
	response = stampModelResponse(req, response)
	phase := "response"
	if err != nil {
		phase = "error"
	}
	observeErr := c.observe(ctx, req, phase, &response, nil, err)
	return response, errors.Join(err, observeErr)
}
func (c *modelPipelineClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	ctx = context.WithValue(ctx, modelRequestOriginKey{}, c.requestOrigin(req))
	if err := c.observe(ctx, req, "request", nil, nil, nil); err != nil {
		return nil, err
	}
	stream, err := c.inner.StreamChat(ctx, req)
	if err != nil {
		_ = c.observe(ctx, req, "error", nil, nil, err)
		return nil, err
	}
	out := make(chan providers.StreamEvent)
	go func() {
		defer close(out)
		accumulated := modelResponseAccumulator{model: req.Model}
		for event := range stream {
			event = stampModelStreamEvent(req, event)
			accumulated.add(event)
			if event.Type == providers.EventDone || event.Type == providers.EventError {
				phase := "response"
				if event.Error != nil {
					phase = "error"
				}
				if err := c.observe(ctx, req, phase, &accumulated.response, &event, event.Error); err != nil {
					event = providers.StreamEvent{Type: providers.EventError, Error: err}
				}
			}
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
func (c *modelPipelineClient) ApplyInferenceRecovery(ctx context.Context, plan providers.RecoveryPlan) error {
	if recovery, ok := c.native.(providers.InferenceRecoveryApplier); ok {
		return recovery.ApplyInferenceRecovery(ctx, plan)
	}
	return errors.New("configured model does not support authentication recovery")
}
func (c *modelPipelineClient) NativeCompactionAvailable() bool {
	native, ok := c.native.(providers.NativeCompactor)
	return ok && native.NativeCompactionAvailable()
}
func (c *modelPipelineClient) NativeCompact(ctx context.Context, req providers.ChatRequest) (providers.NativeCompactionResult, error) {
	selected, err := selectedModelAdapter(req)
	if err != nil || (selected.Adapter != "" && selected.Adapter != "native") {
		return providers.NativeCompactionResult{}, providers.ErrNativeCompactionUnavailable
	}
	if native, ok := c.native.(providers.NativeCompactor); ok {
		return native.NativeCompact(ctx, req)
	}
	return providers.NativeCompactionResult{}, providers.ErrNativeCompactionUnavailable
}
func (c *modelPipelineClient) PrewarmSession(ctx context.Context, id string) error {
	if native, ok := c.native.(providers.SessionPrewarmer); ok {
		return native.PrewarmSession(ctx, id)
	}
	return nil
}

// This accumulator is shared by observer summaries and isolated model streams.
// It never executes returned tool calls or writes conversation history.
type modelResponseAccumulator struct {
	response providers.ChatResponse
	model    string
}

func (a *modelResponseAccumulator) add(e providers.StreamEvent) {
	if e.Lifecycle != nil && e.Lifecycle.ResetPartial {
		a.response = providers.ChatResponse{}
	}
	switch e.Type {
	case providers.EventContentDelta:
		a.response.Content += e.Content
	case providers.EventContentReplace:
		a.response.Content = e.Content
	case providers.EventThinkingDelta:
		a.response.ReasoningContent += e.Content
	case providers.EventThinkingReplace:
		a.response.ReasoningContent = e.Content
	case providers.EventThinkingDone:
		if e.ReasoningBlock != nil {
			a.response.ReasoningBlocks = append(a.response.ReasoningBlocks, *e.ReasoningBlock)
		}
	case providers.EventImage:
		if e.Image != nil {
			a.response.Images = append(a.response.Images, *e.Image)
		}
	case providers.EventProviderItem:
		if e.ProviderItem != nil {
			a.response.ProviderItems = append(a.response.ProviderItems, *e.ProviderItem)
			a.response.ProviderItemModel = a.model
		}
	case providers.EventToolUseEnd:
		if e.ToolCall != nil {
			a.response.ToolCalls = append(a.response.ToolCalls, *e.ToolCall)
		}
	}
	if e.Phase != "" {
		a.response.Phase = e.Phase
	}
	if e.ProviderItemID != "" {
		a.response.ProviderItemID = e.ProviderItemID
		a.response.ProviderItemModel = a.model
	}
	if e.Usage != nil {
		usage := *e.Usage
		a.response.Usage = &usage
	}
	if e.Type == providers.EventDone {
		a.response.StopReason = e.StopReason
		a.response.FinishReason = e.FinishReason
		a.response.Truncated = e.Truncated
	}
}

func stampModelStreamEvent(req providers.ChatRequest, event providers.StreamEvent) providers.StreamEvent {
	if event.ProviderItem != nil {
		item := *event.ProviderItem
		if item.Provider == "" {
			item.Provider = req.Provider
		}
		if item.Scope == "" {
			item.Scope = req.ProviderStateScope
		}
		event.ProviderItem = &item
	}
	if event.ToolCall != nil && event.ToolCall.ProviderItemID != "" {
		call := *event.ToolCall
		if call.ProviderItemProvider == "" {
			call.ProviderItemProvider = req.Provider
		}
		if call.ProviderItemModel == "" {
			call.ProviderItemModel = req.Model
		}
		event.ToolCall = &call
	}

	return event
}

func stampModelResponse(req providers.ChatRequest, response providers.ChatResponse) providers.ChatResponse {
	for i, item := range response.ProviderItems {
		event := stampModelStreamEvent(req, providers.StreamEvent{ProviderItem: &item})
		response.ProviderItems[i] = *event.ProviderItem
	}
	for i, call := range response.ToolCalls {
		event := stampModelStreamEvent(req, providers.StreamEvent{ToolCall: &call})
		response.ToolCalls[i] = *event.ToolCall
	}
	if response.ProviderItemModel == "" && (response.ProviderItemID != "" || len(response.ProviderItems) > 0) {
		response.ProviderItemModel = req.Model
	}
	return response
}
