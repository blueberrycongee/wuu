package loopdriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// AcquireLocal binds an explicitly selected in-process driver for embedding.
// The caller owns its lifetime. Its descriptor identifies the local provider;
// a persisted binding must match before any execution can start.
func AcquireLocal(driver Driver) AcquireFunc {
	return func(ctx context.Context, expected *Binding) (Driver, Binding, func(), error) {
		if driver == nil {
			return nil, Binding{}, nil, errors.New("loop driver is not configured")
		}
		descriptor, err := driver.Descriptor(ctx)
		if err != nil {
			return nil, Binding{}, nil, fmt.Errorf("resolve loop driver: %w", err)
		}
		binding := Binding{Profile: "default", ProviderID: descriptor.ID, ContractVersion: ContractVersion, DriverID: descriptor.ID, DriverVersion: descriptor.Version}
		if expected != nil && *expected != binding {
			return nil, binding, nil, fmt.Errorf("loop driver binding requires %+v; selected binding is %+v", *expected, binding)
		}
		return driver, binding, func() {}, nil
	}
}

func validateCheckpoint(descriptor Descriptor, checkpoint Checkpoint) error {
	if checkpoint.ContractVersion != ContractVersion {
		return fmt.Errorf("unsupported driver checkpoint contract %d", checkpoint.ContractVersion)
	}
	if checkpoint.DriverID != descriptor.ID || checkpoint.DriverVersion != descriptor.Version {
		return fmt.Errorf("driver checkpoint belongs to %s@%s", checkpoint.DriverID, checkpoint.DriverVersion)
	}
	if len(checkpoint.State) > 0 && !json.Valid(checkpoint.State) {
		return errors.New("driver checkpoint state is not valid JSON")
	}
	return nil
}

func checkpointFor(descriptor Descriptor, state any) Checkpoint {
	encoded, _ := json.Marshal(state)
	return Checkpoint{
		ContractVersion: ContractVersion,
		DriverID:        descriptor.ID,
		DriverVersion:   descriptor.Version,
		State:           encoded,
	}
}

// CancellationCause keeps an explicit stop reason diagnostic rather than an
// independent failure. Parent cancellation may already have fired before its
// propagation reaches the driver's child context; preserve that original cause.
func CancellationCause(parent context.Context, reason string) error {
	if cause := context.Cause(parent); cause != nil {
		return cause
	}
	return fmt.Errorf("%s: %w", reason, context.Canceled)
}

func terminalStatus(ctx context.Context, err error) TerminalStatus {
	if err == nil {
		return TerminalSucceeded
	}
	if ctx != nil && ctx.Err() != nil {
		return TerminalCanceled
	}
	return TerminalFailed
}

func cloneInput(input PersistedInput) PersistedInput {
	return PersistedInput{Messages: providers.CloneChatMessages(input.Messages)}
}

func cloneCheckpoint(checkpoint Checkpoint) Checkpoint {
	checkpoint.State = append(json.RawMessage(nil), checkpoint.State...)
	return checkpoint
}
