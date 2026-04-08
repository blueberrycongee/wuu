package hooks

import "context"

// Dispatcher executes matching hooks for a given event. It is a thin
// coordinator: the real work lives in the Hook implementations and the
// Registry's matching logic.
type Dispatcher struct {
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
// stops at the first hook that returns a blocking error. The returned
// Output is the merged result of all non-blocking hooks that ran; for
// fields with a single "last writer wins" semantic (UpdatedInput, Context,
// Decision, Reason), later hooks override earlier ones.
//
// The input's Event field is overwritten to guarantee consistency between
// the caller's intent and the payload delivered to hook processes.
func (d *Dispatcher) Dispatch(ctx context.Context, ev Event, input *Input) (*Output, error) {
	if input == nil {
		input = &Input{}
	}
	input.Event = ev

	hooks := d.registry.Match(ev, input.ToolName)
	if len(hooks) == 0 {
		return &Output{}, nil
	}

	merged := &Output{}
	for _, h := range hooks {
		out, err := h.Execute(ctx, input)
		if err != nil {
			return out, err
		}
		if out == nil {
			continue
		}
		if len(out.UpdatedInput) > 0 {
			merged.UpdatedInput = out.UpdatedInput
		}
		if out.Context != "" {
			merged.Context = out.Context
		}
		if out.Decision != "" {
			merged.Decision = out.Decision
		}
		if out.Reason != "" {
			merged.Reason = out.Reason
		}
	}
	return merged, nil
}

// HasHooks reports whether any hooks are registered for the event.
func (d *Dispatcher) HasHooks(ev Event) bool {
	return d.registry.HasHooks(ev)
}
