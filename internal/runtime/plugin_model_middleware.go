package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Middleware runs inside the prepared operation. Its continuation preserves
// the request and the host's attempt; request transforms run before this layer.
type modelMiddlewareClient struct {
	host       *pluginhost.Host
	capability pluginhost.RegisteredCapability
	next       providers.StreamClient
	origin     pluginhost.ModelInvocationOriginV1
}

type modelMiddlewareContinuationKey struct{}

type modelMiddlewareContinuation struct {
	id          string
	pluginID    string
	next        providers.StreamClient
	request     providers.ChatRequest
	wire        []byte
	consumed    atomic.Bool
	errorMu     sync.Mutex
	nativeError error
}

var modelMiddlewareSequence atomic.Uint64

func pluginModelMiddlewareClient(host *pluginhost.Host, client providers.StreamClient, origin pluginhost.ModelInvocationOriginV1) providers.StreamClient {
	if host == nil || client == nil || origin.Kind == "plugin" {
		return client
	}
	capabilities := host.Capabilities(pluginhost.CapabilityModelMiddleware)
	for index := len(capabilities) - 1; index >= 0; index-- {
		client = &modelMiddlewareClient{host: host, capability: capabilities[index], next: client, origin: origin}
	}
	return client
}

func (c *modelMiddlewareClient) PrepareInferenceRequest(ctx context.Context, request providers.ChatRequest) (providers.ChatRequest, error) {
	if preparer, ok := c.next.(providers.InferenceRequestPreparer); ok {
		return preparer.PrepareInferenceRequest(ctx, request)
	}
	return request, nil
}

func (c *modelMiddlewareClient) OwnsStreamAccounting() bool {
	owner, ok := c.next.(providers.StreamAccountingOwner)
	return ok && owner.OwnsStreamAccounting()
}

func (c *modelMiddlewareClient) ResolveMediaInput(policy providers.MediaInputPolicy, options map[string]any) providers.MediaInputPolicy {
	return providers.ResolveMediaInput(c.next, policy, options)
}

func (c *modelMiddlewareClient) Chat(ctx context.Context, request providers.ChatRequest) (providers.ChatResponse, error) {
	// A disabled plugin is removed even while a turn retains this generation.
	if _, active := c.host.Capability(c.capability.PluginID, pluginhost.CapabilityModelMiddleware); !active {
		return c.next.Chat(ctx, request)
	}
	ctx, input, err := c.invocation(ctx, request, false)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	response, callErr := invokePluginModelChat(ctx, c.host, c.capability, input)
	if callErr != nil {
		callErr = errors.Join(callErr, modelContinuationError(ctx))
	}
	return response, callErr
}

func (c *modelMiddlewareClient) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	if _, active := c.host.Capability(c.capability.PluginID, pluginhost.CapabilityModelMiddleware); !active {
		return c.next.StreamChat(ctx, request)
	}
	ctx, input, err := c.invocation(ctx, request, true)
	if err != nil {
		return nil, err
	}
	stream, callErr := invokePluginModelStream(ctx, c.host, c.capability, input, providers.InferenceSubmission{})
	if callErr != nil {
		return nil, errors.Join(callErr, modelContinuationError(ctx))
	}
	out := make(chan providers.StreamEvent)
	go func() {
		defer close(out)
		for event := range stream {
			if event.Type == providers.EventError {
				event.Error = errors.Join(event.Error, modelContinuationError(ctx))
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

func (c *modelMiddlewareClient) invocation(ctx context.Context, request providers.ChatRequest, stream bool) (context.Context, pluginhost.ModelMiddlewareInput, error) {
	logical := logicalModelRequest(request)
	wire, err := canonicalModelContinuationRequest(logical)
	if err != nil {
		return ctx, pluginhost.ModelMiddlewareInput{}, fmt.Errorf("encode model middleware request: %w", err)
	}
	continuation := &modelMiddlewareContinuation{
		id:       fmt.Sprintf("model-next-%d", modelMiddlewareSequence.Add(1)),
		pluginID: c.capability.PluginID,
		next:     c.next,
		request:  request,
		wire:     wire,
	}
	origin := c.origin
	origin.OperationID = request.Operation.ID
	origin.ParentOperationID = request.Operation.ParentOperationID
	origin.StepIndex = request.StepIndex
	ctx = context.WithValue(ctx, modelRequestOriginKey{}, origin)
	ctx = pluginhost.WithModelContinuation(ctx, c.capability.PluginID, continuation.id)
	return context.WithValue(ctx, modelMiddlewareContinuationKey{}, continuation), pluginhost.ModelMiddlewareInput{
		Version: 1, Request: logical, Origin: origin, Stream: stream, ContinuationID: continuation.id,
	}, nil
}

// The capability's existing execution context carries the continuation. There
// is no global handle registry, and returning from that capability revokes it.
func resolveModelMiddlewareContinuation(scope pluginhost.ExecutionScope, input pluginhost.ModelInvokeParams) (providers.StreamClient, providers.ChatRequest, error) {
	continuation, ok := scope.Context.Value(modelMiddlewareContinuationKey{}).(*modelMiddlewareContinuation)
	if !ok || continuation.id != input.ContinuationID || continuation.pluginID != scope.PluginID || !pluginhost.ModelContinuationAuthorized(scope, input.ContinuationID) {
		return nil, providers.ChatRequest{}, serviceError("invalid_continuation", "model continuation is unavailable in this execution")
	}
	if err := scope.Context.Err(); err != nil {
		return nil, providers.ChatRequest{}, modelRequestError(err)
	}
	if input.ModelAlias != "" || input.MaxOutputTokens != 0 || input.TimeoutMS != 0 {
		return nil, providers.ChatRequest{}, serviceError("invalid_params", "model continuation cannot override model selection or limits")
	}
	wire, err := canonicalModelContinuationRequest(input.Request)
	if err != nil || !bytes.Equal(wire, continuation.wire) {
		return nil, providers.ChatRequest{}, serviceError("invalid_params", "model continuation requires the unchanged request; use model.request.transform to edit input")
	}
	if !continuation.consumed.CompareAndSwap(false, true) {
		return nil, providers.ChatRequest{}, serviceError("continuation_used", "model continuation can only be invoked once")
	}
	return continuation.next, continuation.request, nil
}

// Keep provider failure identity on the host side while RPC errors remain
// sanitized. A successful middleware fallback intentionally suppresses it.
func recordModelContinuationError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	if continuation, ok := ctx.Value(modelMiddlewareContinuationKey{}).(*modelMiddlewareContinuation); ok {
		continuation.errorMu.Lock()
		continuation.nativeError = err
		continuation.errorMu.Unlock()
	}
}
func modelContinuationError(ctx context.Context) error {
	if continuation, ok := ctx.Value(modelMiddlewareContinuationKey{}).(*modelMiddlewareContinuation); ok {
		continuation.errorMu.Lock()
		defer continuation.errorMu.Unlock()
		return continuation.nativeError
	}
	return nil
}

// JSON RPC clients round-trip RawMessage numbers and object key order through
// their own JSON parsers. Compare values, while dispatching the original Go
// request so its native state and immutable execution authority stay intact.
func canonicalModelContinuationRequest(request pluginhost.LogicalModelRequestV1) ([]byte, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
