package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// retirementCleanup separates physical resource quiescence from reference
// release: releasing the final generation reference can itself close the host.
type retirementCleanup struct {
	callback func(context.Context) error
	started  bool // ServiceRegistry.mu
	done     chan struct{}
	err      error // published by done
}

// RegisterRetirementCleanup attaches a host-owned resource to a consumer's
// lifetime, independent of a single service callback. Admission seals atomically
// with consumer retirement. unregister removes only a callback not yet started;
// it never waits for an executing callback. This API grants no service authority.
//
// Cleanup must cancel and drain effects before returning. Use
// DeferServiceCompletion(ctx, release) for generation/state reference releases
// that may reenter host shutdown. Those finalizers run after the physical cleanup
// barrier, outside host locks. The callback must not synchronously await its own
// owner's retirement. Process providers use their declared mechanisms; they do
// not supply Go cleanup callbacks through wire data.
func (r *ServiceRegistry) RegisterRetirementCleanup(owner string, cleanup func(context.Context) error) (func(), error) {
	owner = strings.TrimSpace(owner)
	if r == nil || owner == "" || cleanup == nil {
		return nil, errors.New("retirement cleanup requires a registry, owner and callback")
	}
	r.mu.Lock()
	if !r.open || r.retirementClosed || r.retiringProviders[owner] {
		r.mu.Unlock()
		return nil, errors.New("resource owner is retiring")
	}
	if r.retirementCleanups == nil {
		r.retirementCleanups = make(map[string]map[*retirementCleanup]struct{})
	}
	if r.retirementCleanups[owner] == nil {
		r.retirementCleanups[owner] = make(map[*retirementCleanup]struct{})
	}
	hook := &retirementCleanup{callback: cleanup, done: make(chan struct{})}
	r.retirementCleanups[owner][hook] = struct{}{}
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		if !hook.started {
			delete(r.retirementCleanups[owner], hook)
			hook.callback = nil
		}
		r.mu.Unlock()
	}, nil
}
func (r *ServiceRegistry) startRetirementCleanup(owner string, hooks []*retirementCleanup) {
	for _, hook := range hooks {
		go func() {
			finish := r.allowShutdownCleanup(owner)
			ctx := retainServiceCompletion(context.WithValue(context.Background(), serviceCleanupKey{}, true))
			completion := ctx.Value(serviceCompletionKey{}).(*serviceCompletion)
			func() {
				defer func() {
					if value := recover(); value != nil {
						hook.err = fmt.Errorf("retirement cleanup for %q panicked: %v", owner, value)
					}
				}()
				hook.err = hook.callback(ctx)
			}()
			hook.callback = nil
			finish()
			// No physical effects remain. Reference finalizers may now reenter Close
			// without awaiting this same callback through the host's cleanup barrier.
			close(hook.done)
			_ = completion.release()
		}()
	}
}
func (r *ServiceRegistry) waitRetirementCleanup(owner string) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	var hooks, starting []*retirementCleanup
	for hook := range r.retirementCleanups[owner] {
		hooks = append(hooks, hook)
		if !hook.started {
			hook.started = true
			starting = append(starting, hook)
		}
	}
	r.mu.Unlock()
	// Sealing is global, but resource disposal follows the host's consumer-first
	// order. Providers must stay usable throughout their consumers' cleanup.
	r.startRetirementCleanup(owner, starting)
	var err error
	for _, hook := range hooks {
		<-hook.done
		err = errors.Join(err, hook.err)
	}
	return err
}
