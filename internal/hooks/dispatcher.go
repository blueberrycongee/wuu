package hooks

import (
	"context"
	"sync"
)

// Dispatcher executes matching hooks for a given event. It is a thin
// coordinator: the real work lives in the Hook implementations and the
// Registry's matching logic.
type Dispatcher struct {
	mu       sync.RWMutex
	registry *Registry
}

// NewDispatcher creates a dispatcher backed by a registry. A nil registry
// is allowed and yields a no-op dispatcher.
func NewDispatcher(r *Registry) *Dispatcher {
	if r == nil {
		r = NewRegistry(nil)
	}
	return &Dispatcher{registry: r}
}

// Dispatch runs all hooks that match the event sequentially. Execution
// stops at the first error, preserving output already collected. Context is
// appended in execution order; other fields use the last supplied value.
// PreToolUse rewrites are passed to subsequent hooks so validators inspect
// the arguments that will actually execute.
//
// The input's Event field is overwritten to guarantee consistency between
// the caller's intent and the payload delivered to hook processes.
func (d *Dispatcher) Dispatch(ctx context.Context, ev Event, input *Input) (*Output, error) {
	if input == nil {
		input = &Input{}
	}
	input.Event = ev

	d.mu.RLock()
	registry := d.registry
	d.mu.RUnlock()
	hooks := registry.Match(ev, input.ToolName)
	if len(hooks) == 0 {
		return &Output{}, nil
	}

	current := *input
	merged := &Output{}
	for _, h := range hooks {
		out, err := h.Execute(ctx, &current)
		if err != nil && !IsBlocked(err) {
			return merged, err
		}
		if out == nil {
			if err != nil {
				return merged, err
			}
			continue
		}
		if len(out.UpdatedInput) > 0 {
			merged.UpdatedInput = out.UpdatedInput
			if ev == PreToolUse {
				current.ToolInput = out.UpdatedInput
			}
		}
		if out.Context != "" {
			if merged.Context != "" {
				merged.Context += "\n\n"
			}
			merged.Context += out.Context
		}
		if out.Continue != nil {
			merged.Continue = out.Continue
		}
		if out.Decision != "" {
			merged.Decision = out.Decision
		}
		if out.Reason != "" {
			merged.Reason = out.Reason
		}
		if err != nil {
			return merged, err
		}
	}
	return merged, nil
}

// HasHooks reports whether any hooks are registered for the event.
func (d *Dispatcher) HasHooks(ev Event) bool {
	d.mu.RLock()
	registry := d.registry
	d.mu.RUnlock()
	return registry.HasHooks(ev)
}

func (d *Dispatcher) hasMatchingHooks(ev Event, toolName string) bool {
	d.mu.RLock()
	registry := d.registry
	d.mu.RUnlock()
	return len(registry.Match(ev, toolName)) > 0
}

// Replace swaps the backing registry while keeping the dispatcher identity
// stable for executors that captured it during session construction.
func (d *Dispatcher) Replace(next *Dispatcher) {
	registry := NewRegistry(nil)
	if next != nil {
		next.mu.RLock()
		registry = next.registry
		next.mu.RUnlock()
	}
	d.mu.Lock()
	d.registry = registry
	d.mu.Unlock()
}
