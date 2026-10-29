package runtime

import (
	"context"
	"errors"
	"sync"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

// nativeToolScopeOwner keeps the main and worker scopes alive until all
// admitted work has drained. It does not cancel work or create native tools.
type nativeToolScopeOwner struct {
	mu     sync.Mutex
	scopes map[*toolservice.Scope]struct{}
	done   chan struct{}
	err    error
}

func newNativeToolScopeOwner() *nativeToolScopeOwner {
	return &nativeToolScopeOwner{scopes: make(map[*toolservice.Scope]struct{})}
}

func (o *nativeToolScopeOwner) add(scope *toolservice.Scope) error {
	if scope == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done != nil {
		go scope.Close(context.Background())
		return errors.New("native tool scope owner is closed")
	}
	o.scopes[scope] = struct{}{}
	return nil
}

func (o *nativeToolScopeOwner) close(ctx context.Context) error {
	o.mu.Lock()
	if o.done == nil {
		o.done = make(chan struct{})
		scopes := o.scopes
		o.scopes = nil
		go func() {
			var wg sync.WaitGroup
			results := make(chan error, len(scopes))
			for scope := range scopes {
				wg.Add(1)
				go func(scope *toolservice.Scope) { defer wg.Done(); results <- scope.Close(context.Background()) }(scope)
			}
			wg.Wait()
			close(results)
			var errs []error
			for err := range results {
				errs = append(errs, err)
			}
			o.mu.Lock()
			o.err = errors.Join(errs...)
			close(o.done)
			o.mu.Unlock()
		}()
	}
	done := o.done
	o.mu.Unlock()
	select {
	case <-done:
		o.mu.Lock()
		defer o.mu.Unlock()
		return o.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func selectedNativeToolScope(executor agent.ToolExecutor) *toolservice.Scope {
	for {
		wrapped, ok := executor.(*hooks.HookedExecutor)
		if !ok {
			break
		}
		executor = wrapped.Inner()
	}
	if selected, ok := executor.(*selectedToolExecutor); ok {
		return selected.scope
	}
	return nil
}

// retireTemporaryToolScope stops admission before returning while preserving the
// captured generation until any admitted native work has physically drained.
func (s *Session) retireTemporaryToolScope(scope *toolservice.Scope, generation *PluginGeneration) {
	if scope == nil {
		return
	}
	if generation != nil {
		generation.retain()
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_ = scope.Close(canceled)
	s.runNativeCleanup(func() {
		_ = scope.Close(context.Background())
		if generation != nil {
			_ = s.releasePluginGeneration(generation)
		}
	})
}
