package agent

import (
	"context"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
)

// PrepareHistoricalState binds opt-in snapshot semantics before the first user
// entry is committed. It does not create a driver instance or execute effects.
// The caller holds the ordinary current turn admission and execution lease.
func (r *StreamRunner) PrepareHistoricalState(ctx context.Context) error {
	if r == nil || r.AcquireLoopDriver == nil || r.DriverBindingStore == nil {
		return nil
	}
	prior, found, err := r.DriverBindingStore.LoadBinding(ctx)
	if err != nil {
		return err
	}
	var expected *loopdriver.Binding
	if found {
		expected = &prior
	}
	_, binding, release, err := r.AcquireLoopDriver(ctx, expected)
	if err != nil {
		return fmt.Errorf("prepare historical state: %w", err)
	}
	defer release()
	if !binding.HistoryState {
		return nil
	}
	return r.DriverBindingStore.Bind(ctx, binding)
}
