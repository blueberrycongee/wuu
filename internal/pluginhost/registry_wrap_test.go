package pluginhost

import (
	"context"
	"encoding/json"
	"testing"
)

// Interception must cover every declared caller without changing provider
// identity, callback ownership, caller attribution, or ordinary retirement.
func TestWrappedServicePreservesExecutionAndAllCallers(t *testing.T) {
	provider := &fakeServiceProcess{id: "selected", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
	first := &fakeServiceProcess{id: "first", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
	second := &fakeServiceProcess{id: "second", state: StateActive, required: first.required}
	host := New(provider, first, second)
	registry, conflicts := BuildServiceRegistry(provider, first, second)
	host.AttachServiceRegistry(registry, conflicts)
	seen := map[string]bool{}
	if err := registry.WrapServiceInvoker("search.provider", 1, func(raw ServiceInvoker) ServiceInvoker {
		return &wrappedTestInvoker{ServiceInvoker: raw, before: func(params ServiceInvokeParams) {
			if raw.ID() != provider.ID() || raw.Status().ID != provider.ID() {
				t.Fatal("provider identity changed")
			}
			if params.CallerScope == nil || params.CallerScope.PluginID != params.Caller || params.ExecutionID == params.CallerScope.ID {
				t.Fatalf("scope changed: %+v", params)
			}
			if _, err := host.ResolveExecution(provider.ID(), params.ExecutionID); err != nil {
				t.Fatalf("provider callback lost ownership: %v", err)
			}
			seen[params.Caller] = true
		}}
	}); err != nil {
		t.Fatal(err)
	}
	registry.Activate()
	for _, caller := range []string{first.ID(), second.ID()} {
		if _, err := host.CallService(context.Background(), caller, "search.provider", "query", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || len(provider.invokeCalls) != 2 {
		t.Fatalf("caller bypassed wrapper: %+v", seen)
	}
	if err := registry.WrapServiceInvoker("search.provider", 1, func(raw ServiceInvoker) ServiceInvoker { return raw }); err == nil {
		t.Fatal("active binding was replaceable")
	}
	if outcome, found := host.RetirePlugin(context.Background(), first.ID(), nil); !found || outcome.Err != nil {
		t.Fatalf("retire caller: %+v %v", outcome, found)
	}
	if _, err := host.CallService(context.Background(), first.ID(), "search.provider", "query", json.RawMessage(`{}`)); err == nil {
		t.Fatal("wrapper bypassed retirement")
	}
	if len(provider.invokeCalls) != 2 {
		t.Fatal("retired call reached provider")
	}
}

type wrappedTestInvoker struct {
	ServiceInvoker
	before func(ServiceInvokeParams)
}

func (w *wrappedTestInvoker) InvokeService(ctx context.Context, p ServiceInvokeParams) (json.RawMessage, error) {
	w.before(p)
	return w.ServiceInvoker.InvokeService(ctx, p)
}
