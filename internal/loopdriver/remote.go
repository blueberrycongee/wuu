package loopdriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Remote protocol: a loop driver that runs inside a plugin process. The
// plugin provides a versioned service (conventionally "driver.<profile>")
// declaring the methods below; the kernel stays the execution gateway and
// only ever exchanges these DTOs across the wire.
const (
	RemoteMethodDescriptor = "descriptor"
	RemoteMethodCreate     = "create"
	RemoteMethodResume     = "resume"
	RemoteMethodRun        = "run"
	RemoteMethodShutdown   = "shutdown"
)

// RemoteInvoker is the transport the RemoteDriver delegates to. The runtime
// binds each call to a declared consumer and carries ctx cancellation across
// the service transport. Driver execution IDs remain in the domain payload.
type RemoteInvoker interface {
	InvokeDriver(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
}

// RemoteGatewayRegistry exposes per-execution kernel gateways so the
// plugin's gateway callbacks (model loop, checkpoint writes) reach the
// runner that owns the execution. Register returns an unregister func.
type RemoteGatewayRegistry interface {
	RegisterGateway(executionID string, gateway KernelGateway) (unregister func(), err error)
}

type remoteExecutionWire struct {
	SessionID   string `json:"session_id"`
	ExecutionID string `json:"execution_id"`
}

type remoteCreateParams struct {
	Execution remoteExecutionWire `json:"execution"`
	Input     PersistedInput      `json:"input"`
}

type remoteResumeParams struct {
	Execution  remoteExecutionWire `json:"execution"`
	Input      PersistedInput      `json:"input"`
	Checkpoint Checkpoint          `json:"checkpoint"`
}

type remoteInstanceResult struct {
	InstanceID string     `json:"instance_id"`
	Checkpoint Checkpoint `json:"checkpoint"`
}

type remoteRunParams struct {
	InstanceID string `json:"instance_id"`
}

type remoteRunResult struct {
	Status     TerminalStatus `json:"status"`
	ReceiptID  string         `json:"receipt_id,omitempty"`
	Checkpoint Checkpoint     `json:"checkpoint"`
}

type remoteShutdownParams struct {
	InstanceID string `json:"instance_id"`
}

// RemoteDriver adapts a plugin-provided driver service to the in-process
// Driver contract. It never sees Go session objects; every call is a DTO
// round trip through RemoteInvoker.
type RemoteDriver struct {
	Profile   string
	Invoker   RemoteInvoker
	Gateways  RemoteGatewayRegistry
	ServiceID string

	mu         sync.Mutex
	descriptor *Descriptor
}

// Descriptor resolves the remote descriptor once and caches it; the
// checkpoint compatibility check in the runner depends on a stable ID and
// version for the lifetime of the driver.
func (d *RemoteDriver) Descriptor() Descriptor {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.descriptor != nil {
		return *d.descriptor
	}
	result, err := d.Invoker.InvokeDriver(context.Background(), RemoteMethodDescriptor, nil)
	if err != nil {
		// Descriptor must not fail the runner before Create/Resume can
		// produce a typed error; degrade to the profile identity.
		return Descriptor{ID: d.fallbackID(), Version: "0.0.0"}
	}
	var descriptor Descriptor
	if err := json.Unmarshal(result, &descriptor); err != nil || strings.TrimSpace(descriptor.ID) == "" {
		return Descriptor{ID: d.fallbackID(), Version: "0.0.0"}
	}
	d.descriptor = &descriptor
	return descriptor
}

func (d *RemoteDriver) fallbackID() string {
	if trimmed := strings.TrimSpace(d.ServiceID); trimmed != "" {
		return trimmed
	}
	return "remote:" + strings.TrimSpace(d.Profile)
}

func (d *RemoteDriver) Create(execution ExecutionContext, input PersistedInput) (Instance, error) {
	if err := requireExecutionID(execution); err != nil {
		return nil, err
	}
	params, err := json.Marshal(remoteCreateParams{
		Execution: remoteExecutionWire{SessionID: execution.SessionID, ExecutionID: execution.ExecutionID},
		Input:     input,
	})
	if err != nil {
		return nil, fmt.Errorf("encode remote driver create params: %w", err)
	}
	result, err := d.Invoker.InvokeDriver(context.Background(), RemoteMethodCreate, params)
	if err != nil {
		return nil, fmt.Errorf("remote driver create: %w", err)
	}
	return d.decodeInstance(execution, input, result)
}

func (d *RemoteDriver) Resume(execution ExecutionContext, input PersistedInput, checkpoint Checkpoint) (Instance, error) {
	if err := requireExecutionID(execution); err != nil {
		return nil, err
	}
	params, err := json.Marshal(remoteResumeParams{
		Execution:  remoteExecutionWire{SessionID: execution.SessionID, ExecutionID: execution.ExecutionID},
		Input:      input,
		Checkpoint: checkpoint,
	})
	if err != nil {
		return nil, fmt.Errorf("encode remote driver resume params: %w", err)
	}
	result, err := d.Invoker.InvokeDriver(context.Background(), RemoteMethodResume, params)
	if err != nil {
		return nil, fmt.Errorf("remote driver resume: %w", err)
	}
	return d.decodeInstance(execution, input, result)
}

func (d *RemoteDriver) decodeInstance(execution ExecutionContext, input PersistedInput, result json.RawMessage) (*RemoteInstance, error) {
	var decoded remoteInstanceResult
	if err := json.Unmarshal(result, &decoded); err != nil {
		return nil, fmt.Errorf("decode remote driver instance: %w", err)
	}
	if strings.TrimSpace(decoded.InstanceID) == "" {
		return nil, errors.New("remote driver returned an empty instance id")
	}
	return &RemoteInstance{
		driver:     d,
		execution:  execution,
		input:      input,
		instanceID: decoded.InstanceID,
		checkpoint: decoded.Checkpoint,
	}, nil
}

func requireExecutionID(execution ExecutionContext) error {
	if strings.TrimSpace(execution.ExecutionID) == "" {
		return errors.New("remote loop driver requires a non-empty execution id")
	}
	return nil
}

// RemoteInstance is the kernel-side handle for a driver instance living in
// the plugin process.
type RemoteInstance struct {
	driver     *RemoteDriver
	execution  ExecutionContext
	input      PersistedInput
	instanceID string

	mu         sync.Mutex
	checkpoint Checkpoint
	cancel     context.CancelCauseFunc
}

func (i *RemoteInstance) Run(ctx context.Context, gateway KernelGateway) (TerminalOutcome, error) {
	unregister := func() {}
	if i.driver.Gateways != nil {
		reg, err := i.driver.Gateways.RegisterGateway(i.execution.ExecutionID, gateway)
		if err != nil {
			return TerminalOutcome{Status: TerminalFailed, Checkpoint: i.Checkpoint()}, fmt.Errorf("register remote driver gateway: %w", err)
		}
		unregister = reg
	}
	defer unregister()

	runCtx, cancel := context.WithCancelCause(ctx)
	i.mu.Lock()
	i.cancel = cancel
	i.mu.Unlock()
	defer cancel(nil)

	params, err := json.Marshal(remoteRunParams{InstanceID: i.instanceID})
	if err != nil {
		return TerminalOutcome{Status: TerminalFailed, Checkpoint: i.Checkpoint()}, fmt.Errorf("encode remote driver run params: %w", err)
	}
	result, err := i.driver.Invoker.InvokeDriver(runCtx, RemoteMethodRun, params)
	if err != nil {
		status := TerminalFailed
		if runCtx.Err() != nil {
			status = TerminalCanceled
		}
		return TerminalOutcome{Status: status, Checkpoint: i.Checkpoint()}, fmt.Errorf("remote driver run: %w", err)
	}
	var decoded remoteRunResult
	if uErr := json.Unmarshal(result, &decoded); uErr != nil {
		return TerminalOutcome{Status: TerminalFailed, Checkpoint: i.Checkpoint()}, fmt.Errorf("decode remote driver run result: %w", uErr)
	}
	i.setCheckpoint(decoded.Checkpoint)
	return TerminalOutcome{Status: decoded.Status, ReceiptID: decoded.ReceiptID, Checkpoint: decoded.Checkpoint}, nil
}

func (i *RemoteInstance) Checkpoint() Checkpoint {
	i.mu.Lock()
	defer i.mu.Unlock()
	return cloneCheckpoint(i.checkpoint)
}

// Cancel cancels the local run context; the execution plane translates that
// into the cross-process execution.cancel frame for the same execution id.
func (i *RemoteInstance) Cancel(reason string) {
	i.mu.Lock()
	cancel := i.cancel
	i.mu.Unlock()
	if cancel != nil {
		cancel(errors.New(reason))
	}
}

func (i *RemoteInstance) Shutdown() {
	i.Cancel("driver shutdown")
	params, err := json.Marshal(remoteShutdownParams{InstanceID: i.instanceID})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteShutdownTimeout)
	defer cancel()
	_, _ = i.driver.Invoker.InvokeDriver(ctx, RemoteMethodShutdown, params)
}

func (i *RemoteInstance) setCheckpoint(checkpoint Checkpoint) {
	i.mu.Lock()
	i.checkpoint = cloneCheckpoint(checkpoint)
	i.mu.Unlock()
}

const remoteShutdownTimeout = 5 * time.Second

// FailClosedDriver is bound when a session's selected driver profile has no
// provider (plugin removed, disabled, or version mismatch). Opening the
// session and reading history never touches the driver; starting a new run
// fails closed with a typed, diagnostic error.
type FailClosedDriver struct {
	Profile string
	Reason  string
}

func (d FailClosedDriver) Descriptor() Descriptor {
	return Descriptor{ID: "fail-closed:" + strings.TrimSpace(d.Profile), Version: "0.0.0"}
}

func (d FailClosedDriver) err() error {
	profile := strings.TrimSpace(d.Profile)
	if profile == "" {
		profile = "<default>"
	}
	reason := strings.TrimSpace(d.Reason)
	if reason == "" {
		reason = "no provider"
	}
	return fmt.Errorf("loop driver profile %q is unavailable (%s); the session is read-only until the driver plugin is restored", profile, reason)
}

func (d FailClosedDriver) Create(ExecutionContext, PersistedInput) (Instance, error) {
	return nil, d.err()
}

func (d FailClosedDriver) Resume(ExecutionContext, PersistedInput, Checkpoint) (Instance, error) {
	return nil, d.err()
}
