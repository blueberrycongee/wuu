package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const modelInvocationMaxBytes = 16 << 20
const modelInvocationMaxEvents = 4096

type modelInvocation struct {
	mu        sync.Mutex
	owner     string
	execution string
	cancel    context.CancelFunc
	changed   chan struct{}
	events    []pluginhost.ModelStreamEventV1
	bytes     int
	done      bool
	result    *pluginhost.ModelInvokeResult
	err       *pluginhost.ModelErrorV1
	stopScope func() bool
}
type modelInvocationStore struct {
	mu       sync.Mutex
	seq      atomic.Uint64
	live     map[string]*modelInvocation
	reserved int
}

func newModelInvocationStore() *modelInvocationStore {
	return &modelInvocationStore{live: make(map[string]*modelInvocation)}
}

type modelInvokeInvoker struct {
	parent *kernelHostServices
	store  *modelInvocationStore
}

func (k *modelInvokeInvoker) ID() string                { return k.parent.ID() }
func (k *modelInvokeInvoker) Status() pluginhost.Status { return k.parent.Status() }

func (k *modelInvokeInvoker) InvokeService(callCtx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := callCtx.Err(); err != nil {
		return nil, err
	}
	k.parent.mu.RLock()
	active, session, host := k.parent.active, k.parent.modelSession, k.parent.modelHost
	k.parent.mu.RUnlock()
	if !active || session == nil || host == nil {
		return nil, serviceError("service_unavailable", "model service is unavailable")
	}
	scope, scopeErr := host.ResolveExecution(params.Caller, params.ExecutionID)
	if scopeErr != nil {
		return nil, scopeErr
	}
	if params.Method == "read" || params.Method == "cancel" {
		return k.readOrCancel(callCtx, scope, params)
	}
	if params.Method != "call" && params.Method != "start" {
		return nil, serviceError("method_not_found", "model invocation method is unavailable")
	}
	if len(params.Params) > modelInvocationMaxBytes {
		return nil, serviceError("limit_exceeded", "model input exceeds 16 MiB")
	}
	var input pluginhost.ModelInvokeParams
	if err := decodeServiceParams(params.Params, &input); err != nil {
		return nil, err
	}
	var client providers.StreamClient
	var request providers.ChatRequest
	var err error
	direct := input.ContinuationID != ""
	reserved := false
	if params.Method == "start" {
		k.store.mu.Lock()
		if len(k.store.live)+k.store.reserved >= 64 {
			k.store.mu.Unlock()
			return nil, serviceError("limit_exceeded", "too many live model invocations")
		}
		k.store.reserved++
		reserved = true
		k.store.mu.Unlock()
		defer func() {
			if reserved {
				k.store.mu.Lock()
				k.store.reserved--
				k.store.mu.Unlock()
			}
		}()
	}
	ctx, cancel := context.WithCancel(scope.Context)
	if params.Method == "call" {
		stop := context.AfterFunc(callCtx, cancel)
		defer stop()
	}
	if direct {
		client, request, err = resolveModelMiddlewareContinuation(scope, input)
	} else {
		if input.TimeoutMS == 0 {
			input.TimeoutMS = 120_000
		}
		if input.TimeoutMS < 1 || input.TimeoutMS > 600_000 {
			cancel()
			return nil, serviceError("invalid_params", "timeout_ms must be between 1 and 600000")
		}
		timeoutCtx, timeoutCancel := context.WithTimeout(ctx, time.Duration(input.TimeoutMS)*time.Millisecond)
		originalCancel := cancel
		cancel = func() { timeoutCancel(); originalCancel() }
		ctx = timeoutCtx
		client, request, err = k.prepareIndependent(ctx, session, host, scope, input)
	}
	if err != nil {
		cancel()
		return nil, err
	}
	origin := independentModelOrigin(scope, request)
	if parent, ok := scope.Context.Value(modelRequestOriginKey{}).(pluginhost.ModelInvocationOriginV1); direct && ok {
		origin = parent
	}
	if providers.InferenceJournalFromContext(ctx) == nil && !direct {
		journal := session.InferenceJournalForOwner("plugin:" + scope.PluginID)
		if journal == nil {
			cancel()
			return nil, serviceError("service_unavailable", "inference accounting is unavailable")
		}
		ctx = providers.WithInferenceJournal(ctx, journal)
	}
	if !direct {
		request, err = providers.EnsureInferenceExecutionContext(ctx, request, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
		if err != nil {
			cancel()
			return nil, modelRequestError(err)
		}
	}
	if params.Method == "call" {
		defer cancel()
		var response providers.ChatResponse
		if direct {
			response, err = client.Chat(ctx, request)
		} else {
			response, err = providers.ExecuteChat(ctx, client, request, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
		}
		if err != nil {
			if direct {
				recordModelContinuationError(ctx, err)
			}
			return nil, modelRequestError(err)
		}
		if err := ctx.Err(); err != nil {
			return nil, modelRequestError(err)
		}
		response = stampModelResponse(request, response)
		return marshalServiceResult(pluginhost.ModelInvokeResult{Response: logicalModelResponse(response), Origin: origin})
	}
	// A stream is a bounded child of its live invocation. A background service
	// execution may own it, but an expired capability/tool cannot leave it running.
	k.store.mu.Lock()
	k.store.reserved--
	reserved = false
	id := fmt.Sprintf("model-invoke-%d", k.store.seq.Add(1))
	job := &modelInvocation{owner: scope.PluginID, execution: scope.ID, cancel: cancel, changed: make(chan struct{})}
	k.store.live[id] = job
	k.store.mu.Unlock()
	job.stopScope = context.AfterFunc(scope.Context, func() { cancel(); k.store.mu.Lock(); delete(k.store.live, id); k.store.mu.Unlock() })
	go k.runStream(ctx, job, client, request, origin, direct)
	return marshalServiceResult(pluginhost.ModelInvocationHandle{InvocationID: id})
}

func (k *modelInvokeInvoker) prepareIndependent(ctx context.Context, session *Session, host *pluginhost.Host, scope pluginhost.ExecutionScope, input pluginhost.ModelInvokeParams) (providers.StreamClient, providers.ChatRequest, error) {
	request, err := applyLogicalModelRequest(providers.ChatRequest{}, input.Request)
	if err != nil {
		return nil, request, serviceError("invalid_params", err.Error())
	}
	input.ModelAlias = strings.TrimSpace(input.ModelAlias)
	cfg, _, err := session.LoadEffectiveConfig()
	if err != nil {
		return nil, request, serviceError("service_unavailable", "model configuration is unavailable")
	}
	var selection modelroles.Selection
	if input.ModelAlias != "" {
		selection, err = modelroles.ResolveAlias(cfg, input.ModelAlias)
	} else {
		roles, resolveErr := modelroles.Resolve(cfg, modelroles.ResolveOptions{ProviderName: request.Provider, Model: request.Model})
		err = resolveErr
		selection = roles.Main
	}
	if err != nil {
		return nil, request, serviceError("invalid_params", "configured model route is unavailable")
	}
	if input.ModelAlias != "" {
		// Retargeting opts into the alias's defaults. An exact replay without
		// an alias retains its snapshot, regardless of current model settings.
		defaults := selection.ProviderOptions
		if request.Effort != "" {
			// Resolve an explicit effort as a replacement variant rather than
			// leaking the alias variant's incompatible reasoning options.
			effort := modelvariant.ResolveForProvider(selection.RuleProvider, selection.RuleProviderConfig, selection.Model, "", request.Effort)
			defaults, request.Effort = effort.ProviderOptions, effort.LegacyEffort
		} else {
			request.Effort = selection.LegacyEffort
		}
		options := providers.CloneRequestObject(defaults)
		if options == nil {
			options = make(map[string]any)
		}
		if request.MaxTokens > 0 {
			for _, key := range []string{"maxOutputTokens", "max_output_tokens", "max_tokens", "max_completion_tokens"} {
				delete(options, key)
			}
		}
		for key, value := range request.ProviderOptions {
			options[key] = value
		}
		request.ProviderOptions = options
	}
	if input.MaxOutputTokens != 0 {
		if input.MaxOutputTokens < 1 || input.MaxOutputTokens > 1_000_000 {
			return nil, request, serviceError("invalid_params", "max_output_tokens must be between 1 and 1000000")
		}
		request.MaxTokens = input.MaxOutputTokens
		for _, key := range []string{"maxOutputTokens", "max_output_tokens", "max_tokens", "max_completion_tokens"} {
			delete(request.ProviderOptions, key)
		}
	}
	request, err = selectedModelAdapter(request)
	if err != nil {
		return nil, request, err
	}
	client, err := providerfactory.BuildRuntimeStreamClient(selection.RuleProviderConfig, selection.Provider)
	if err != nil && (client == nil || request.Adapter == "" || request.Adapter == "native") {
		return nil, request, serviceError("service_unavailable", "configured model client is unavailable")
	}
	// Exact request fields win over the current foreground selection. Aliases are
	// explicit retargeting and therefore obtain a fresh native compatibility scope.
	if input.ModelAlias != "" && (request.Provider != selection.Provider || request.Model != selection.APIModel) {
		request.ProviderStateScope = ""
	}
	request.Provider = selection.Provider
	request.Model = selection.APIModel
	request.MediaInput = providers.ResolveMediaInput(client, request.MediaInput, request.ProviderOptions)
	if request.MediaInput.VideoKnown {
		request, err = providers.PrepareVideoInput(request, request.MediaInput.Video)
		if err != nil {
			return nil, request, serviceError("invalid_params", "unsupported video input")
		}
	}
	if err := providers.ValidateRequiredMedia(providers.ApplyToolResultProjections(request.Messages), request.MediaInput); err != nil {
		return nil, request, serviceError("invalid_params", err.Error())
	}
	request.Messages = providers.ProjectMediaForPolicy(request.Messages, request.MediaInput)
	request.Operation = providers.NewInferenceOperation(providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if parent, ok := scope.Context.Value(modelRequestOriginKey{}).(pluginhost.ModelInvocationOriginV1); ok && providers.InferenceWorkflowFromContext(ctx) != nil {
		request.Operation.ParentOperationID = parent.OperationID
	}
	wrapped := pluginModelPipelineClient(host, client, independentModelOrigin(scope, request))
	return wrapped, request, nil
}

// Retain authenticated attribution without borrowing the parent's execution
// authority, cancellation, journal or workflow budget.
func independentModelOrigin(scope pluginhost.ExecutionScope, request providers.ChatRequest) pluginhost.ModelInvocationOriginV1 {
	origin := pluginhost.ModelInvocationOriginV1{Kind: "plugin", PluginID: scope.PluginID, OperationID: request.Operation.ID, ParentOperationID: request.Operation.ParentOperationID, SessionID: scope.SessionID, ThreadID: scope.ThreadID, StepIndex: request.StepIndex}
	if parent, ok := scope.Context.Value(modelRequestOriginKey{}).(pluginhost.ModelInvocationOriginV1); ok {
		if origin.ParentOperationID == "" {
			origin.ParentOperationID = parent.OperationID
		}
		if origin.SessionID == "" {
			origin.SessionID = parent.SessionID
		}
		if origin.ThreadID == "" {
			origin.ThreadID = parent.ThreadID
		}
	}
	return origin
}

func (k *modelInvokeInvoker) runStream(ctx context.Context, job *modelInvocation, client providers.StreamClient, request providers.ChatRequest, origin pluginhost.ModelInvocationOriginV1, direct bool) {
	defer job.cancel()
	var err error
	if !direct {
		request, err = providers.EnsureInferenceExecutionContext(ctx, request, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	}
	var stream <-chan providers.StreamEvent
	if err == nil {
		if direct {
			stream, err = client.StreamChat(ctx, request)
		} else {
			stream, err = providers.NewReliableStreamClient(client, nil).StreamChat(ctx, request)
		}
	}
	accumulated := modelResponseAccumulator{model: request.Model}
	sawDone := false
	if err == nil && stream == nil {
		err = errors.New("model stream is unavailable")
	}
	if err == nil {
	streamLoop:
		for {
			select {
			case <-ctx.Done():
				err = ctx.Err()
				break streamLoop
			case event, ok := <-stream:
				if !ok {
					break streamLoop
				}
				if event.Type == providers.EventError {
					err = event.Error
					if direct {
						recordModelContinuationError(ctx, err)
					}
				}
				if event.Type == providers.EventDone {
					sawDone = true
				}
				event = stampModelStreamEvent(request, event)
				accumulated.add(event)
				view := logicalModelStreamEvent(event)
				raw, marshalErr := json.Marshal(view)
				if marshalErr != nil {
					err = marshalErr
					break streamLoop
				}
				job.mu.Lock()
				if len(job.events) >= modelInvocationMaxEvents || job.bytes+len(raw) > modelInvocationMaxBytes {
					job.mu.Unlock()
					err = serviceError("limit_exceeded", "model stream exceeded its bounded event buffer")
					break streamLoop
				}
				job.events = append(job.events, view)
				job.bytes += len(raw)
				close(job.changed)
				job.changed = make(chan struct{})
				job.mu.Unlock()
			}
		}
	}
	if err == nil && !sawDone {
		err = providers.NewIncompleteStreamError("model stream closed before done")
	}
	if err != nil {
		job.cancel()
		if stream != nil {
			for range stream {
			}
		}
	}
	if !direct && request.Execution != nil {
		outcome := providers.InferenceOutcomeSucceeded
		failure := providers.NormalizedFailure{}
		if err != nil {
			outcome = providers.InferenceOutcomeFailed
			failure = providers.NormalizeFailure(err)
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				outcome = providers.InferenceOutcomeCanceled
			}
		}
		err = errors.Join(err, request.Execution.Complete(outcome, failure))
	}
	if direct && err != nil {
		recordModelContinuationError(ctx, err)
	}
	job.mu.Lock()
	job.done = true
	job.err = modelErrorView(err)
	if err == nil {
		job.result = &pluginhost.ModelInvokeResult{Response: logicalModelResponse(accumulated.response), Origin: origin}
	}
	close(job.changed)
	job.changed = make(chan struct{})
	job.mu.Unlock()
}

func (k *modelInvokeInvoker) readOrCancel(ctx context.Context, scope pluginhost.ExecutionScope, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	var input pluginhost.ModelInvocationReadParams
	if params.Method == "cancel" {
		var handle pluginhost.ModelInvocationHandle
		if err := decodeServiceParams(params.Params, &handle); err != nil {
			return nil, err
		}
		input.InvocationID = handle.InvocationID
	} else if err := decodeServiceParams(params.Params, &input); err != nil {
		return nil, err
	}
	k.store.mu.Lock()
	job := k.store.live[input.InvocationID]
	k.store.mu.Unlock()
	if job == nil {
		return nil, serviceError("invocation_not_found", "model invocation is unavailable")
	}
	if job.owner != scope.PluginID || job.execution != scope.ID {
		return nil, serviceError("service_not_authorized", "model invocation belongs to another execution")
	}
	if params.Method == "cancel" {
		job.cancel()
		return json.RawMessage(`{}`), nil
	}
	if input.Cursor < 0 || input.WaitMS < 0 || input.WaitMS > 30000 {
		return nil, serviceError("invalid_params", "invalid model stream cursor or wait_ms")
	}
	job.mu.Lock()
	if input.Cursor > len(job.events) {
		job.mu.Unlock()
		return nil, serviceError("invalid_params", "model stream cursor exceeds available events")
	}
	if input.Cursor == len(job.events) && !job.done && input.WaitMS > 0 {
		changed := job.changed
		job.mu.Unlock()
		timer := time.NewTimer(time.Duration(input.WaitMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-changed:
		case <-timer.C:
		case <-ctx.Done():
			return nil, modelRequestError(ctx.Err())
		case <-scope.Context.Done():
			return nil, modelRequestError(scope.Context.Err())
		}
		job.mu.Lock()
	}
	result := pluginhost.ModelInvocationReadResult{Events: append([]pluginhost.ModelStreamEventV1{}, job.events[input.Cursor:]...), Cursor: len(job.events), Done: job.done, Result: job.result, Error: job.err}
	job.mu.Unlock()
	if result.Done {
		job.stopScope()
		k.store.mu.Lock()
		delete(k.store.live, input.InvocationID)
		k.store.mu.Unlock()
	}
	return marshalServiceResult(result)
}
