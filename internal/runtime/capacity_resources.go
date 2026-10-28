package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// CapacityResources is an explicit process-local physical-account authority.
// Embedders share one owner across sessions using the same accounts. Derived
// compositions and retained generations inherit its factories; independent
// owners intentionally isolate capacity. It does not promise cross-process
// account coordination. No credential is retained in its opaque scope keys.
type CapacityResources struct {
	refs      int
	autoClose bool
	closing   bool
	stopped   chan struct{}
	done      chan struct{}
	mu        sync.Mutex
	limit     int
	slots     map[string]chan struct{}
	policyV1  *providers.ProviderCoordinator
}

func NewCapacityResources(maxInFlight int) *CapacityResources {
	cfg := providers.DefaultProviderCoordinatorConfig()
	if maxInFlight <= 0 {
		maxInFlight = cfg.MaxInFlight
	}
	cfg.MaxInFlight = maxInFlight
	return &CapacityResources{stopped: make(chan struct{}), done: make(chan struct{}), limit: maxInFlight, slots: map[string]chan struct{}{}, policyV1: providers.NewProviderCoordinator(cfg)}
}

func (r *CapacityResources) acquire(ctx context.Context, scope string) (func(), error) {
	if r == nil || strings.TrimSpace(scope) == "" {
		return nil, errors.New("physical capacity scope is required")
	}
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return nil, errors.New("capacity resource owner is closed")
	}
	slots := r.slots[scope]
	if slots == nil {
		slots = make(chan struct{}, r.limit)
		r.slots[scope] = slots
	}
	r.mu.Unlock()
	select {
	case slots <- struct{}{}:
		release := sync.OnceFunc(func() { <-slots })
		r.mu.Lock()
		closing := r.closing
		r.mu.Unlock()
		if closing {
			release()
			return nil, errors.New("capacity resource owner is closed")
		}
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		return release, nil
	case <-r.stopped:
		return nil, errors.New("capacity resource owner is closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func withCapacityProviders(factories []NativeProviderFactory, resources *CapacityResources) []NativeProviderFactory {
	result := append([]NativeProviderFactory(nil), factories...)
	present := map[string]bool{}
	for _, factory := range result {
		present[factory.ID] = true
	}
	if present[capacityPolicyModule] && present[capacityResourceModule] {
		return result
	}
	if resources == nil {
		resources = NewCapacityResources(0)
		resources.autoClose = true
	}
	for _, factory := range []NativeProviderFactory{capacityProviderFactory(resources, false), capacityProviderFactory(resources, true)} {
		if !present[factory.ID] {
			result = append(result, factory)
		}
	}
	return result
}

func (r *CapacityResources) retain() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return errors.New("capacity resource owner is closed")
	}
	r.refs++
	return nil
}
func (r *CapacityResources) release() {
	r.mu.Lock()
	r.refs--
	if r.refs == 0 && r.autoClose && !r.closing {
		r.sealLocked()
	}
	if r.refs == 0 && r.closing {
		select {
		case <-r.done:
		default:
			close(r.done)
		}
	}
	r.mu.Unlock()
}
func (r *CapacityResources) sealLocked() { r.closing = true; close(r.stopped); r.policyV1.Close() }

// Close ends an explicitly shared owner's lifetime. It seals new admission but
// waits for active sessions/generations to drain; timeout reports pending cleanup
// and never releases physical slots. Close sessions before waiting indefinitely.
func (r *CapacityResources) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if !r.closing {
		r.sealLocked()
	}
	if r.refs == 0 {
		select {
		case <-r.done:
		default:
			close(r.done)
		}
	}
	done := r.done
	r.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.Join(errors.New("capacity owner cleanup pending"), ctx.Err())
	}
}

// Root leases survive temporary removal of all native capacity modules. A later
// repair reuses the same owner instead of reviving a disposed factory capture.
func retainCapacityRoot(factories []NativeProviderFactory) (func(), error) {
	owners := map[*CapacityResources]bool{}
	var retained []*CapacityResources
	for _, factory := range factories {
		owner := factory.capacityOwner
		if owner == nil || owners[owner] {
			continue
		}
		owners[owner] = true
		if err := owner.retain(); err != nil {
			for _, previous := range retained {
				previous.release()
			}
			return nil, err
		}
		retained = append(retained, owner)
	}
	return sync.OnceFunc(func() {
		for _, owner := range retained {
			owner.release()
		}
	}), nil
}
