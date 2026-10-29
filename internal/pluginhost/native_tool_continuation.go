package pluginhost

import (
	"context"
	"sync"
)

type nativeToolContinuationBindingKey struct{}
type nativeToolContinuationBinding struct {
	mu          sync.Mutex
	owner       string
	executionID string
	tracker     *ExecutionTracker
}

// WithNativeToolContinuationBinding limits a host-owned continuation to the
// first tool execution for its selected plugin.
func WithNativeToolContinuationBinding(ctx context.Context, owner string) context.Context {
	return context.WithValue(ctx, nativeToolContinuationBindingKey{}, &nativeToolContinuationBinding{owner: owner})
}

func bindNativeToolContinuation(ctx context.Context, owner, id string, tool bool, tracker *ExecutionTracker) {
	binding, ok := ctx.Value(nativeToolContinuationBindingKey{}).(*nativeToolContinuationBinding)
	if !ok || !tool {
		return
	}
	binding.mu.Lock()
	defer binding.mu.Unlock()
	if binding.owner == owner && binding.executionID == "" {
		binding.executionID = id
		binding.tracker = tracker
	}
}

// OwnsNativeToolContinuation checks the exact host-recorded execution owner.
func OwnsNativeToolContinuation(scope ExecutionScope) bool {
	binding, ok := scope.Context.Value(nativeToolContinuationBindingKey{}).(*nativeToolContinuationBinding)
	if !ok {
		return false
	}
	binding.mu.Lock()
	defer binding.mu.Unlock()
	return binding.executionID != "" && binding.owner == scope.PluginID && binding.executionID == scope.ID && binding.tracker == scope.tracker
}
