package pluginhost

import (
	"context"
	"encoding/json"
	"testing"
)

// Native host bridges start a fresh declared consumer callback even while
// forwarding cleanup from a different graph. Registry.Call still forbids
// impersonating that consumer with the original callback identity.
func TestNativeHostCleanupBridge(t *testing.T) {
	descriptor := ServiceDescriptor{Name: "state.example", Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "close", InputSchema: "fixture.close.input.v1", OutputSchema: "fixture.close.output.v1", Cleanup: true}}}
	provider := &fakeServiceProcess{id: "provider", state: StateActive, provided: []ServiceDescriptor{descriptor}}
	consumer := &fakeServiceProcess{id: "consumer", state: StateActive, required: []ServiceRequirement{{Name: descriptor.Name, MajorVersion: 1}}}
	registry, _ := BuildServiceRegistry(provider, consumer)
	host := New(provider, consumer)
	host.AttachServiceRegistry(registry, nil)
	registry.Activate()
	ctx := context.WithValue(context.Background(), serviceCleanupKey{}, true)
	ctx = context.WithValue(ctx, nativeInvocationKey{}, &nativeInvocation{registry: &ServiceRegistry{}, owner: "bridge", live: true})
	if _, err := registry.Call(ctx, consumer.ID(), ServiceCallParams{Service: descriptor.Name, Method: "close"}); err == nil {
		t.Fatal("foreign native callback impersonated the declared consumer")
	}
	if _, err := host.CallService(ctx, consumer.ID(), descriptor.Name, "close", nil); err != nil {
		t.Fatal(err)
	}
	if len(provider.invokeCalls) != 1 {
		t.Fatalf("cleanup calls = %d", len(provider.invokeCalls))
	}
}

// Native callbacks must retain admission without lending it to another caller,
// generation, or a future autonomous invocation after the callback returns.
func TestNativeCallbackContextAdmission(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		t.Run(map[bool]string{false: "untracked", true: "tracked"}[tracked], func(t *testing.T) {
			descriptor := func(name string) ServiceDescriptor {
				return ServiceDescriptor{Name: name, Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "run", InputSchema: "fixture.run.input.v1", OutputSchema: "fixture.run.output.v1"}}}
			}
			inner := &fakeServiceProcess{id: "inner", state: StateActive, provided: []ServiceDescriptor{descriptor("fixture.inner")}}
			outer := &fakeServiceProcess{id: "outer", state: StateActive, provided: []ServiceDescriptor{descriptor("fixture.outer")}, required: []ServiceRequirement{{Name: "fixture.inner", MajorVersion: 1}}}
			caller := &fakeServiceProcess{id: "caller", state: StateActive, required: []ServiceRequirement{{Name: "fixture.outer", MajorVersion: 1}, {Name: "fixture.inner", MajorVersion: 1}}}
			registry, _ := BuildServiceRegistry(inner, outer, caller)
			host := New(inner, outer, caller)
			if tracked {
				host.AttachServiceRegistry(registry, nil)
			}
			registry.Activate()
			var saved context.Context
			outer.invoke = func(ctx context.Context, _ ServiceInvokeParams) (json.RawMessage, error) {
				saved = context.WithoutCancel(ctx)
				registry.mu.Lock()
				registry.backgroundStopped = true
				registry.mu.Unlock()
				p := ServiceCallParams{Service: "fixture.inner", Method: "run"}
				if _, err := registry.Call(context.Background(), "outer", p); err == nil {
					t.Error("autonomous call admitted")
				}
				if _, err := registry.Call(ctx, "caller", p); err == nil {
					t.Error("foreign owner admitted")
				}
				foreign, _ := BuildServiceRegistry(inner, outer, caller)
				foreign.Activate()
				if _, err := foreign.Call(ctx, "outer", p); err == nil {
					t.Error("foreign registry admitted")
				}
				result, err := registry.Call(ctx, "outer", p)
				if err != nil {
					return nil, err
				}
				return result, nil
			}
			if _, err := registry.Call(context.Background(), "caller", ServiceCallParams{Service: "fixture.outer", Method: "run"}); err != nil {
				t.Fatal(err)
			}
			// Even reopening autonomous admission must not revive a captured identity.
			registry.mu.Lock()
			registry.backgroundStopped = false
			registry.mu.Unlock()
			if _, err := registry.Call(saved, "outer", ServiceCallParams{Service: "fixture.inner", Method: "run"}); err == nil {
				t.Fatal("stale callback context admitted")
			}
			if len(inner.invokeCalls) != 1 {
				t.Fatalf("dependency calls = %d", len(inner.invokeCalls))
			}
		})
	}
}

func TestNativeCapabilityRootContextAdmission(t *testing.T) {
	descriptor := ServiceDescriptor{Name: "fixture.inner", Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "run", InputSchema: "fixture.run.input.v1", OutputSchema: "fixture.run.output.v1"}}}
	inner := &fakeServiceProcess{id: "inner", state: StateActive, provided: []ServiceDescriptor{descriptor}}
	caller := &fakeServiceProcess{id: "caller", state: StateActive, required: []ServiceRequirement{{Name: "fixture.inner", MajorVersion: 1}}}
	registry, _ := BuildServiceRegistry(inner, caller)
	host := New(inner, caller)
	host.AttachServiceRegistry(registry, nil)
	registry.Activate()
	id, ctx := host.executions.begin("caller", context.Background(), ToolExecuteInput{}, false)
	marker := &nativeInvocation{registry: registry, owner: "caller", executionID: id, live: true, root: true}
	ctx = context.WithValue(ctx, nativeInvocationKey{}, marker)
	registry.mu.Lock()
	registry.backgroundStopped = true
	registry.mu.Unlock()
	params := ServiceCallParams{Service: "fixture.inner", Method: "run"}
	if _, err := registry.Call(ctx, "caller", params); err != nil {
		t.Fatalf("admitted capability continuation: %v", err)
	}
	host.executions.End(id)
	if _, err := registry.Call(ctx, "caller", params); err == nil {
		t.Fatal("ended capability root admitted")
	}
	marker.live = false
	if _, err := registry.Call(ctx, "caller", params); err == nil {
		t.Fatal("expired callback admitted")
	}
}
