package pluginhost

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

const (
	KernelBackgroundService    = pluginprotocol.KernelBackgroundService
	backgroundMaxJobsPerPlugin = 64
	backgroundMaxResultBytes   = 1 << 20
)

func KernelBackgroundDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: KernelBackgroundService, Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{
			{Name: "start", InputSchema: "host.background.start.input.v1", OutputSchema: "host.background.start.output.v1"},
			{Name: "inspect", InputSchema: "host.background.inspect.input.v1", OutputSchema: "host.background.inspect.output.v1"},
			{Name: "cancel", InputSchema: "host.background.cancel.input.v1", OutputSchema: "host.background.cancel.output.v1"},
		},
	}
}

// BackgroundStartParams dispatches one finite callback to a service provided
// by the caller. Timers, scheduling policy and durable results remain plugin-owned.
type BackgroundStartParams = pluginprotocol.BackgroundStartParams
type BackgroundStartResult = pluginprotocol.BackgroundHandle
type BackgroundJobParams = pluginprotocol.BackgroundJobParams

type BackgroundJobSnapshot struct {
	JobID  string            `json:"job_id"`
	State  string            `json:"state"`
	Result json.RawMessage   `json:"result,omitempty"`
	Error  *HostServiceError `json:"error,omitempty"`
}

type backgroundJob struct {
	owner    string
	key      string
	snapshot BackgroundJobSnapshot
	cancel   context.CancelFunc
}

// StartBackground admits a callback without borrowing a tool, conversation or
// renderer lifetime. A generation replacement closes this admission separately
// from its still-running foreground leases.
func (h *Host) StartBackground(provenance context.Context, caller string, input BackgroundStartParams) (BackgroundStartResult, *HostServiceError) {
	input.Key, input.Service, input.Method = strings.TrimSpace(input.Key), strings.TrimSpace(input.Service), strings.TrimSpace(input.Method)
	if input.Key == "" || len(input.Key) > 128 || input.Service == "" || input.Method == "" {
		return BackgroundStartResult{}, &HostServiceError{Code: "invalid_params", Message: "background start requires a key of 1 to 128 bytes, service and method"}
	}
	if input.TimeoutMS < 0 || int64(input.TimeoutMS) > int64((1<<63-1)/time.Millisecond) {
		return BackgroundStartResult{}, &HostServiceError{Code: "invalid_params", Message: "timeout_ms must be a non-negative duration"}
	}
	if len(input.Params) == 0 {
		input.Params = json.RawMessage(`{}`)
	}
	if !json.Valid(input.Params) || len(input.Params) > backgroundMaxResultBytes {
		return BackgroundStartResult{}, &HostServiceError{Code: "invalid_params", Message: "background params must be valid JSON within 1048576 bytes"}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.backgroundClosed || h.serviceRegistry == nil {
		return BackgroundStartResult{}, &HostServiceError{Code: "generation_closed", Message: "generation is not accepting background work"}
	}
	admitted := false
	for _, client := range h.clients {
		if client.ID() == caller && capabilityStateReady(client.Status().State) {
			admitted = true
			break
		}
	}
	if !admitted {
		return BackgroundStartResult{}, &HostServiceError{Code: "service_unavailable", Message: "background owner is unavailable"}
	}
	key, provider, targetErr := h.serviceRegistry.backgroundTarget(caller, input.Service, input.Method)
	if targetErr != nil {
		return BackgroundStartResult{}, targetErr
	}
	running := 0
	for _, job := range h.background {
		if job.owner != caller || job.snapshot.State != "running" {
			continue
		}
		if job.key == input.Key {
			return BackgroundStartResult{}, &HostServiceError{Code: "background_conflict", Message: "a background job with this key is already running"}
		}
		running++
	}
	if running >= backgroundMaxJobsPerPlugin {
		return BackgroundStartResult{}, &HostServiceError{Code: "limit_exceeded", Message: "plugin has too many running background jobs"}
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(provenance))
	if input.TimeoutMS > 0 {
		var deadlineCancel context.CancelFunc
		ctx, deadlineCancel = context.WithTimeout(ctx, time.Duration(input.TimeoutMS)*time.Millisecond)
		baseCancel := cancel
		cancel = func() { deadlineCancel(); baseCancel() }
	}
	executionID, executionCtx := h.executions.begin(caller, ctx, ToolExecuteInput{}, false)
	jobID := "background-" + rand.Text()
	job := &backgroundJob{owner: caller, key: input.Key, snapshot: BackgroundJobSnapshot{JobID: jobID, State: "running"}, cancel: cancel}
	if h.background == nil {
		h.background = make(map[string]*backgroundJob)
	}
	h.background[jobID] = job
	registry := h.serviceRegistry
	input.Params = append(json.RawMessage(nil), input.Params...)
	go h.runBackground(executionCtx, executionID, job, registry, key, provider, input)
	return BackgroundStartResult{JobID: jobID, ExecutionID: executionID}, nil
}

func (h *Host) runBackground(ctx context.Context, executionID string, job *backgroundJob, registry *ServiceRegistry, key serviceKey, provider serviceProvider, input BackgroundStartParams) {
	result, callErr := registry.invokeBackground(ctx, job.owner, key, provider, input.Method, input.Params, executionID)
	snapshot := BackgroundJobSnapshot{JobID: job.snapshot.JobID, State: "completed"}
	switch {
	case ctx.Err() != nil:
		snapshot.State = "cancelled"
		code, message := "request_cancelled", "background job was cancelled"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code, message = "deadline_exceeded", "background job deadline exceeded"
		}
		snapshot.Error = &HostServiceError{Code: code, Message: message}
	case callErr != nil:
		snapshot.State, snapshot.Error = "failed", callErr
	case len(result) > backgroundMaxResultBytes:
		snapshot.State = "failed"
		snapshot.Error = &HostServiceError{Code: "limit_exceeded", Message: "background result exceeds 1048576 bytes"}
	default:
		snapshot.Result = append(json.RawMessage(nil), result...)
	}
	if snapshot.Error != nil && len(snapshot.Error.Message) > backgroundMaxResultBytes {
		snapshot.Error = &HostServiceError{Code: "limit_exceeded", Message: "background error exceeds 1048576 bytes"}
	}
	h.executions.End(executionID)
	job.cancel()
	h.mu.Lock()
	job.snapshot = snapshot
	h.backgroundCompleted = append(h.backgroundCompleted, snapshot.JobID)
	// Keep a bounded receipt window per plugin. Long-term state belongs in
	// plugin storage, not in generation-owned execution bookkeeping.
	count := 0
	for index := len(h.backgroundCompleted) - 1; index >= 0; index-- {
		id := h.backgroundCompleted[index]
		completed := h.background[id]
		if completed == nil || completed.owner != job.owner {
			continue
		}
		count++
		if count > backgroundMaxJobsPerPlugin {
			delete(h.background, id)
			h.backgroundCompleted = append(h.backgroundCompleted[:index], h.backgroundCompleted[index+1:]...)
		}
	}
	h.mu.Unlock()
}

func (h *Host) InspectBackground(caller, jobID string) (BackgroundJobSnapshot, *HostServiceError) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	job := h.background[strings.TrimSpace(jobID)]
	if job == nil || job.owner != caller {
		return BackgroundJobSnapshot{}, &HostServiceError{Code: "background_not_found", Message: "background job is unavailable"}
	}
	result := job.snapshot
	result.Result = append(json.RawMessage(nil), result.Result...)
	if result.Error != nil {
		copy := *result.Error
		result.Error = &copy
	}
	return result, nil
}

func (h *Host) CancelBackground(caller, jobID string) (bool, *HostServiceError) {
	h.mu.RLock()
	job := h.background[strings.TrimSpace(jobID)]
	if job == nil || job.owner != caller {
		h.mu.RUnlock()
		return false, &HostServiceError{Code: "background_not_found", Message: "background job is unavailable"}
	}
	running := job.snapshot.State == "running"
	if running {
		job.cancel()
	}
	h.mu.RUnlock()
	return running, nil
}

// StopBackground revokes new work before cancelling admitted jobs. Foreground
// tools and capability invocations keep their established generation lease.
func (h *Host) StopBackground() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.backgroundClosed = true
	if h.serviceRegistry != nil {
		h.serviceRegistry.mu.Lock()
		h.serviceRegistry.backgroundStopped = true
		for _, execution := range h.serviceRegistry.executions {
			if execution.autonomous {
				execution.cancel()
			}
		}
		h.serviceRegistry.mu.Unlock()
	}
	for _, job := range h.background {
		if job.snapshot.State == "running" {
			job.cancel()
		}
	}
	h.mu.Unlock()
}

func (r *ServiceRegistry) backgroundTarget(caller, service, method string) (serviceKey, serviceProvider, *HostServiceError) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.active || !r.open {
		return serviceKey{}, serviceProvider{}, &HostServiceError{Code: "service_unavailable", Message: "background work requires an active generation"}
	}
	var target serviceProvider
	var targetKey serviceKey
	for key, provider := range r.providers {
		if key.name != service || provider.kernel || provider.pluginID != caller {
			continue
		}
		if target.invoker != nil {
			return serviceKey{}, serviceProvider{}, &HostServiceError{Code: "invalid_params", Message: "background service must have one registered major version"}
		}
		target, targetKey = provider, key
	}
	if target.invoker == nil {
		return serviceKey{}, serviceProvider{}, &HostServiceError{Code: "service_not_authorized", Message: "background target must be a registered service provided by the caller"}
	}
	if !capabilityStateReady(target.invoker.Status().State) {
		return serviceKey{}, serviceProvider{}, &HostServiceError{Code: "service_unavailable", Message: "background service provider is unavailable"}
	}
	for _, declared := range target.descriptor.Methods {
		if declared.Name == method {
			return targetKey, target, nil
		}
	}
	return serviceKey{}, serviceProvider{}, &HostServiceError{Code: "method_not_found", Message: "background target does not declare this method"}
}

func (r *ServiceRegistry) invokeBackground(ctx context.Context, caller string, key serviceKey, provider serviceProvider, method string, params json.RawMessage, executionID string) (json.RawMessage, *HostServiceError) {
	execCtx, executionID, release, admissionErr := r.acquireExecution(ctx, executionID, caller, key, provider.pluginID, true)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	result, err := provider.invoker.InvokeService(execCtx, ServiceInvokeParams{Service: key.name, Method: method, Caller: caller, Params: params, ExecutionID: executionID})
	if err != nil {
		var hostErr *HostServiceError
		if errors.As(err, &hostErr) && hostErr.Code != "" {
			return nil, hostErr
		}
		var remoteErr *remoteCallError
		if errors.As(err, &remoteErr) && remoteErr.code != "" {
			return nil, &HostServiceError{Code: remoteErr.code, Message: remoteErr.message}
		}
		return nil, &HostServiceError{Code: "service_unavailable", Message: fmt.Sprintf("background service %s failed: %v", key.name, err)}
	}
	if len(result) == 0 || !json.Valid(result) {
		return nil, &HostServiceError{Code: "invalid_response", Message: "background service returned invalid JSON"}
	}
	return result, nil
}
