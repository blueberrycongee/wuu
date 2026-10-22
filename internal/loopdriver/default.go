package loopdriver

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

const (
	DefaultDriverID      = "wuu.default-loop"
	DefaultDriverVersion = "1.0.0"
)

type DefaultDriver struct{}

func (DefaultDriver) Descriptor(ctx context.Context) (Descriptor, error) {
	if err := ctx.Err(); err != nil {
		return Descriptor{}, err
	}
	return Descriptor{
		ID:      DefaultDriverID,
		Version: DefaultDriverVersion,
		Capabilities: []string{
			"multi_round",
			"tool_use",
			"compaction",
		},
	}, nil
}

func (driver DefaultDriver) Create(ctx context.Context, execution ExecutionContext, input PersistedInput) (Instance, error) {
	descriptor, err := driver.Descriptor(ctx)
	if err != nil {
		return nil, err
	}
	return newDefaultInstance(descriptor, execution, input, nil)
}

func (driver DefaultDriver) Resume(ctx context.Context, execution ExecutionContext, input PersistedInput, checkpoint Checkpoint) (Instance, error) {
	descriptor, err := driver.Descriptor(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateCheckpoint(descriptor, checkpoint); err != nil {
		return nil, err
	}
	return newDefaultInstance(descriptor, execution, input, checkpoint.State)
}

type defaultInstance struct {
	descriptor Descriptor
	execution  ExecutionContext
	input      PersistedInput

	mu         sync.Mutex
	checkpoint Checkpoint
	cancel     context.CancelCauseFunc
}

type defaultCheckpointState struct {
	Phase         string `json:"phase"`
	ExecutionID   string `json:"execution_id,omitempty"`
	LastReceiptID string `json:"last_receipt_id,omitempty"`
}

func newDefaultInstance(descriptor Descriptor, execution ExecutionContext, input PersistedInput, state json.RawMessage) (*defaultInstance, error) {
	phase := "ready"
	if len(state) > 0 {
		var restored defaultCheckpointState
		if err := json.Unmarshal(state, &restored); err != nil {
			return nil, errors.New("decode default driver checkpoint: " + err.Error())
		}
		if restored.Phase != "" {
			phase = restored.Phase
		}
	}
	instance := &defaultInstance{descriptor: descriptor, execution: execution, input: cloneInput(input)}
	instance.checkpoint = checkpointFor(descriptor, defaultCheckpointState{Phase: phase, ExecutionID: execution.ExecutionID})
	return instance, nil
}

func (instance *defaultInstance) Run(ctx context.Context, gateway KernelGateway) (TerminalOutcome, error) {
	runCtx, cancel := context.WithCancelCause(ctx)
	instance.mu.Lock()
	instance.cancel = cancel
	instance.mu.Unlock()
	defer cancel(nil)

	running := checkpointFor(instance.descriptor, defaultCheckpointState{Phase: "running", ExecutionID: instance.execution.ExecutionID})
	instance.setCheckpoint(running)
	if err := gateway.WriteCheckpoint(runCtx, running); err != nil {
		return TerminalOutcome{Status: TerminalFailed, Checkpoint: running}, err
	}
	receipt, runErr := gateway.ExecuteModelLoop(runCtx, instance.input, LoopPolicy{})
	status := terminalStatus(runCtx, runErr)
	terminal := checkpointFor(instance.descriptor, defaultCheckpointState{
		Phase:         string(status),
		ExecutionID:   instance.execution.ExecutionID,
		LastReceiptID: receipt.ID,
	})
	instance.setCheckpoint(terminal)
	return TerminalOutcome{Status: status, ReceiptID: receipt.ID, Checkpoint: terminal}, runErr
}

func (instance *defaultInstance) Checkpoint() Checkpoint {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	return cloneCheckpoint(instance.checkpoint)
}

func (instance *defaultInstance) Cancel(reason string) {
	instance.mu.Lock()
	cancel := instance.cancel
	instance.mu.Unlock()
	if cancel != nil {
		cancel(errors.New(reason))
	}
}

func (instance *defaultInstance) Shutdown() {
	instance.Cancel("driver shutdown")
}

func (instance *defaultInstance) setCheckpoint(checkpoint Checkpoint) {
	instance.mu.Lock()
	instance.checkpoint = cloneCheckpoint(checkpoint)
	instance.mu.Unlock()
}
