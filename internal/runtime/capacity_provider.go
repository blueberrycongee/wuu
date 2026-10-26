package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const capacityPolicyModule = "wuu.model.capacity.default"
const capacityResourceModule = "wuu.model.capacity.resources"
const capacityPolicyService = "model.capacity"
const capacityResourceService = "host.model.capacity"

type capacityServiceLease struct {
	caller  string
	policy  *providers.ProviderLease
	release func()
}
type capacityProvider struct {
	mu        sync.Mutex
	resources *CapacityResources
	physical  bool
	state     pluginhost.State
	retained  bool
	leases    map[string]capacityServiceLease
}

func capacityProviderFactory(resources *CapacityResources, physical bool) NativeProviderFactory {
	id := capacityPolicyModule
	if physical {
		id = capacityResourceModule
	}
	return NativeProviderFactory{capacityOwner: resources, ID: id, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		return &capacityProvider{resources: resources, physical: physical, state: pluginhost.StatePrepared, leases: map[string]capacityServiceLease{}}, nil
	}}
}
func (p *capacityProvider) ID() string {
	if p.physical {
		return capacityResourceModule
	}
	return capacityPolicyModule
}
func (p *capacityProvider) Status() pluginhost.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return pluginhost.Status{ID: p.ID(), State: p.state}
}
func (p *capacityProvider) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	if p.state == pluginhost.StateActive {
		p.mu.Unlock()
		return nil
	}
	if !p.retained {
		if err := p.resources.retain(); err != nil {
			p.mu.Unlock()
			return err
		}
		p.retained = true
	}
	p.state = pluginhost.StateActive
	p.mu.Unlock()
	return nil
}
func (p *capacityProvider) Close(context.Context) error {
	p.mu.Lock()
	p.state = pluginhost.StateStopped
	retained := p.retained
	p.retained = false
	leases := p.leases
	p.leases = map[string]capacityServiceLease{}
	p.mu.Unlock()
	for _, lease := range leases {
		if lease.policy != nil {
			lease.policy.Release()
		}
		if lease.release != nil {
			lease.release()
		}
	}
	if retained {
		p.resources.release()
	}
	return nil
}
func (p *capacityProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	name := capacityPolicyService
	if p.physical {
		name = capacityResourceService
	}
	return []pluginhost.ServiceDescriptor{{Name: name, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{
		{Name: "acquire", InputSchema: "model.capacity.acquire.input.v1", OutputSchema: "model.capacity.acquire.output.v1"},
		{Name: "settle", Cleanup: true, InputSchema: "model.capacity.settle.input.v1", OutputSchema: "model.capacity.settle.output.v1"},
	}}}
}
func (p *capacityProvider) InvokeService(ctx context.Context, input pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	switch input.Method {
	case "acquire":
		var request pluginprotocol.ModelCapacityAcquire
		if err := decodeServiceParams(input.Params, &request); err != nil {
			return nil, err
		}
		if len(request.Scope) == 0 || len(request.Scope) > 1024 {
			return nil, errors.New("invalid capacity scope")
		}
		var lease capacityServiceLease
		lease.caller = input.Caller
		var err error
		if p.physical {
			lease.release, err = p.resources.acquire(ctx, request.Scope)
		} else {
			lease.policy, err = p.resources.policyV1.Acquire(ctx, providers.ProviderScope(request.Scope), providers.ProviderPriorityForProfile(providers.InferenceWorkloadProfile(request.Profile)))
		}
		if err != nil {
			return nil, err
		}
		id := rand.Text()
		p.mu.Lock()
		if p.state != pluginhost.StateActive {
			p.mu.Unlock()
			if lease.policy != nil {
				lease.policy.Release()
			}
			if lease.release != nil {
				lease.release()
			}
			return nil, errors.New("capacity provider unavailable")
		}
		p.leases[id] = lease
		p.mu.Unlock()
		return json.Marshal(pluginprotocol.ModelCapacityLease{Lease: id})
	case "settle":
		var request pluginprotocol.ModelCapacitySettle
		if err := decodeServiceParams(input.Params, &request); err != nil {
			return nil, err
		}
		switch request.Outcome {
		case "succeeded", "failed", "transport_fallback", "abandoned":
		default:
			return nil, errors.New("invalid capacity settlement")
		}
		p.mu.Lock()
		lease, found := p.leases[request.Lease]
		if found && lease.caller != input.Caller {
			p.mu.Unlock()
			return nil, errors.New("capacity lease belongs to another consumer")
		}
		delete(p.leases, request.Lease)
		p.mu.Unlock()
		if !found {
			return json.RawMessage(`{}`), nil
		}
		if lease.policy != nil {
			if request.Outcome == "succeeded" {
				lease.policy.Succeed()
			} else if request.Outcome == "failed" && request.Failure != nil {
				f := request.Failure
				lease.policy.Fail(providers.NormalizedFailure{Category: providers.FailureCategory(f.Category), RetryAfter: time.Duration(f.RetryAfterMS) * time.Millisecond})
			} else {
				lease.policy.Release()
			}
		}
		if lease.release != nil {
			lease.release()
		}
		return json.RawMessage(`{}`), nil
	default:
		return nil, errors.New("unknown capacity method")
	}
}

func (p *capacityProvider) RequiredServices() []pluginhost.ServiceRequirement { return nil }

// Prepared candidates retain the owner even before they can acquire capacity.
func (p *capacityProvider) Prepare(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.retained {
		return nil
	}
	if err := p.resources.retain(); err != nil {
		return err
	}
	p.retained = true
	return nil
}
