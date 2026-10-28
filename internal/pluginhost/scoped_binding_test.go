package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
)

// Failure cases: missing or revoked delegate must not resolve the global wrapper;
// scoped selection grants no declaration, schema, preparation or caller authority;
// scoped dependency cycles must fail before activation and cleanup keeps the base.
func TestConsumerScopedProviderDelegation(t *testing.T) {
	bindings := []composition.Binding{
		{Service: "search.provider", Major: 1, Provider: "wrapper"},
		{Service: "search.provider", Major: 1, Provider: "base", Consumer: "wrapper"},
	}
	registry, err := NewServiceRegistryWithBindings(bindings)
	if err != nil {
		t.Fatal(err)
	}
	requirement := []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}
	base := &fakeServiceProcess{id: "base", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
	wrapper := &fakeServiceProcess{id: "wrapper", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}, required: requirement}
	consumer := &fakeServiceProcess{id: "reader", state: StateActive, required: requirement}
	wrapper.invoke = func(ctx context.Context, input ServiceInvokeParams) (json.RawMessage, error) {
		raw, err := registry.Call(ctx, wrapper.id, ServiceCallParams{Service: input.Service, Method: input.Method, Params: input.Params})
		if err != nil {
			return nil, err
		}
		return raw, nil
	}
	if conflicts := registry.RegisterClients(base, wrapper, consumer); len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	if err := registry.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
	host := New(consumer, wrapper, base)
	host.AttachServiceRegistry(registry, nil)
	if edges := host.Dependencies()[wrapper.id]; len(edges) != 1 || edges[0].Provider != base.id {
		t.Fatalf("delegate dependency missing: %+v", edges)
	}
	registry.Activate()
	if _, err := registry.Call(context.Background(), consumer.id, ServiceCallParams{Service: "search.provider", Method: "query"}); err != nil {
		t.Fatal(err)
	}
	if len(base.invokeCalls) != 1 || base.invokeCalls[0].Caller != wrapper.id {
		t.Fatalf("delegate caller: %+v", base.invokeCalls)
	}
	snapshot := registry.Snapshot(1)
	if len(snapshot.Services) != 2 || snapshot.Services[1].Consumer != wrapper.id || snapshot.Services[1].Provider != base.id || snapshot.Services[1].BindingSource != "composition" {
		t.Fatalf("scoped routes not discoverable: %+v", snapshot.Services)
	}
	base.state = StateFailed
	if _, err := registry.Call(context.Background(), consumer.id, ServiceCallParams{Service: "search.provider", Method: "query"}); err == nil {
		t.Fatal("failed delegate unexpectedly succeeded")
	}
	registry.RevokePlugin(context.Background(), base.id)
	if !registry.HasProvider("search.provider", 1) || registry.HasProviderFor(wrapper.id, "search.provider", 1) {
		t.Fatal("scoped capability probe fell back to live global wrapper")
	}
	if _, exists := registry.ServiceDescriptorFor(wrapper.id, "search.provider", 1); exists {
		t.Fatal("scoped contract probe returned global descriptor")
	}
	if _, err := registry.Call(context.Background(), consumer.id, ServiceCallParams{Service: "search.provider", Method: "query"}); err == nil {
		t.Fatal("removed delegate fell back to wrapper")
	}
}

func TestConsumerScopedBindingValidation(t *testing.T) {
	for _, scenario := range []string{"unknown-consumer", "undeclared", "wrong-major", "missing-target", "self", "schema", "cycle"} {
		t.Run(scenario, func(t *testing.T) {
			bindings := []composition.Binding{{Service: "search.provider", Major: 1, Provider: "wrapper"}, {Service: "search.provider", Major: 1, Provider: "base", Consumer: "wrapper"}}
			required := []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}
			wrapper := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "wrapper", state: StatePrepared, provided: []ServiceDescriptor{searchService("1.0.0")}, required: required}}
			base := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "base", state: StatePrepared, provided: []ServiceDescriptor{searchService("1.0.0")}}}
			switch scenario {
			case "unknown-consumer":
				bindings[1].Consumer = "unknown"
			case "undeclared":
				wrapper.required = nil
			case "wrong-major":
				bindings[1].Major = 2
			case "missing-target":
				bindings[1].Provider = "missing"
			case "self":
				bindings[1].Provider = "wrapper"
			case "schema":
				base.provided[0].Methods[0].InputSchema = "incompatible.query.input.v1"
			case "cycle":
				base.required = required
			case "stopped":
				base.state = StateStopped
			}
			registry, err := NewServiceRegistryWithBindings(bindings)
			if err != nil {
				t.Fatal(err)
			}
			registry.RegisterClients(wrapper, base)
			err = registry.ValidateBindings()
			if scenario == "cycle" {
				if err != nil {
					t.Fatal(err)
				}
				host := New(wrapper, base)
				host.AttachServiceRegistry(registry, nil)
				err = host.Activate(context.Background())
				if err == nil || !strings.Contains(err.Error(), "cycle") {
					t.Fatalf("cycle accepted: %v", err)
				}
				if wrapper.state != StatePrepared || base.state != StatePrepared {
					t.Fatal("cyclic candidate activated")
				}
			} else if err == nil {
				t.Fatal("invalid scoped binding accepted")
			}
		})
	}
}

func TestConsumerScopedDependencyCleanup(t *testing.T) {
	registry, err := NewServiceRegistryWithBindings([]composition.Binding{{Service: "search.provider", Major: 1, Provider: "wrapper"}, {Service: "search.provider", Major: 1, Provider: "base", Consumer: "wrapper"}})
	if err != nil {
		t.Fatal(err)
	}
	requirement := []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}
	base := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "base", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}}
	wrapper := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "wrapper", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}, required: requirement}}
	var events []string
	base.close = func(context.Context) error { events = append(events, "base"); return nil }
	wrapper.close = func(ctx context.Context) error {
		_, err := registry.Call(ctx, "wrapper", ServiceCallParams{Service: "search.provider", Method: "query"})
		if err != nil {
			return err
		}
		events = append(events, "wrapper")
		return nil
	}
	registry.RegisterClients(wrapper, base)
	host := New(base, wrapper)
	host.AttachServiceRegistry(registry, nil)
	registry.Activate()
	result, found := host.RetirePlugin(context.Background(), "base", errors.New("disabled"))
	if !found || result.Err != nil {
		t.Fatalf("retire: %+v %v", result, found)
	}
	if strings.Join(events, ",") != "wrapper,base" {
		t.Fatalf("cleanup order: %v", events)
	}
}

func TestConsumerScopedCancellationRetainsDelegates(t *testing.T) {
	registry, err := NewServiceRegistryWithBindings([]composition.Binding{{Service: "search.provider", Major: 1, Provider: "wrapper"}, {Consumer: "wrapper", Service: "search.provider", Major: 1, Provider: "base"}})
	if err != nil {
		t.Fatal(err)
	}
	required := []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}
	base := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "base", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}}
	wrapper := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "wrapper", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}, required: required}}
	reader := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "reader", state: StateActive, required: required}}
	entered, canceled, resume := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var baseClosed atomic.Bool
	base.close = func(context.Context) error { baseClosed.Store(true); return nil }
	base.invoke = func(ctx context.Context, input ServiceInvokeParams) (json.RawMessage, error) {
		if InShutdownCleanup(ctx) {
			if baseClosed.Load() {
				return nil, errors.New("delegate closed before adapter cleanup")
			}
			return json.RawMessage(`{}`), nil
		}
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-resume
		if baseClosed.Load() {
			return nil, errors.New("delegate disposed before admitted callback ended")
		}
		return json.RawMessage(`{}`), nil
	}
	wrapper.invoke = func(ctx context.Context, input ServiceInvokeParams) (json.RawMessage, error) {
		result, err := registry.Call(ctx, wrapper.id, ServiceCallParams{Service: input.Service, Method: input.Method})
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	var flushed atomic.Bool
	wrapper.close = func(ctx context.Context) error {
		_, err := registry.Call(ctx, wrapper.id, ServiceCallParams{Service: "search.provider", Method: "query"})
		if err != nil {
			return err
		}
		flushed.Store(true)
		return nil
	}
	registry.RegisterClients(base, wrapper, reader)
	host := New(reader, wrapper, base)
	host.AttachServiceRegistry(registry, nil)
	registry.Activate()
	callCtx, cancelCall := context.WithCancel(context.Background())
	result := make(chan *HostServiceError, 1)
	go func() {
		_, err := host.CallService(callCtx, reader.id, "search.provider", "query", json.RawMessage(`{}`))
		result <- err
	}()
	<-entered
	cancelCall()
	<-canceled
	closeCtx, cancelClose := context.WithCancel(context.Background())
	cancelClose()
	if err := host.Close(closeCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown cancellation: %v", err)
	}
	if baseClosed.Load() {
		t.Fatal("cancellation disposed selected delegate while callback still owned it")
	}
	close(resume)
	<-result
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !flushed.Load() || !baseClosed.Load() {
		t.Fatal("adapter cleanup did not precede delegate disposal")
	}
}

// Lifecycle selection follows only declarations of the decorated service.
// Unrelated dependencies, revoked targets and invalid cycles cannot activate
// a provider's autonomous policy, even when that provider remains registered.
func TestConsumerScopedProviderChain(t *testing.T) {
	for _, scenario := range []string{"direct", "chain", "unrelated", "undeclared-root", "undeclared-delegate", "renderer-only", "missing", "revoked", "retiring", "stopped", "cycle"} {
		t.Run(scenario, func(t *testing.T) {
			required := []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}
			reader := &fakeServiceProcess{id: "reader", state: StateActive, required: required}
			wrapper := &fakeServiceProcess{id: "wrapper", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}, required: required}
			base := &fakeServiceProcess{id: "base", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
			bindings := []composition.Binding{{Service: "search.provider", Major: 1, Provider: wrapper.id}, {Consumer: wrapper.id, Service: "search.provider", Major: 1, Provider: base.id}}
			switch scenario {
			case "direct":
				bindings = bindings[:1]
				bindings[0].Provider = base.id
			case "unrelated":
				wrapper.required = []ServiceRequirement{{Name: "unrelated.service", MajorVersion: 1, Required: true}}
				unrelated := searchService("1.0.0")
				unrelated.Name = "unrelated.service"
				base.provided = append(base.provided, unrelated)
				bindings = bindings[:1]
			case "undeclared-root":
				reader.required = nil
			case "undeclared-delegate":
				wrapper.required = nil
			case "missing":
				bindings[1].Provider = "disabled.base"
			case "cycle":
				base.required = required
			case "stopped":
				base.state = StateStopped
			}
			registry, err := NewServiceRegistryWithBindings(bindings)
			if err != nil {
				t.Fatal(err)
			}
			var wrapperClient Client = wrapper
			if scenario == "renderer-only" {
				wrapperClient = &rendererOnlyChainClient{wrapper}
			}
			registry.RegisterClients(reader, wrapperClient, base)
			if scenario == "revoked" {
				registry.RevokePlugin(context.Background(), base.id)
			}
			if scenario == "retiring" {
				registry.stopProviderAdmission(base.id)
			}
			want := scenario == "direct" || scenario == "chain"
			if got := registry.ProviderInChainFor(reader.id, "search.provider", 1, base.id); got != want {
				t.Fatalf("provider in chain = %v, want %v", got, want)
			}
		})
	}
}

// A package may expose a runtime service while only its renderer consumes a
// service. That UI dependency is not a backend delegation edge.
type rendererOnlyChainClient struct{ *fakeServiceProcess }

func (*rendererOnlyChainClient) RuntimeRequiredServices() []ServiceRequirement { return nil }
