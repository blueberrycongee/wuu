package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// These cases protect activation, failure propagation, and resource lifetime
// across service edges, including edges combined with package dependencies.
type dependencyClient struct {
	*fakeServiceProcess
	activate func(context.Context) error
	close    func(context.Context) error
}

func (c *dependencyClient) Activate(ctx context.Context) error {
	if c.activate != nil {
		if err := c.activate(ctx); err != nil {
			c.state = StateFailed
			return err
		}
	}
	c.state = StateActive
	return nil
}

func (c *dependencyClient) Close(ctx context.Context) error {
	var err error
	if c.close != nil {
		err = c.close(ctx)
	}
	c.state = StateStopped
	return err
}

func TestServiceDependenciesOrderActivationAndCleanup(t *testing.T) {
	var events []string
	provider := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "storage", state: StatePrepared, provided: []ServiceDescriptor{searchService("1.0.0")}}}
	consumer := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "consumer", state: StatePrepared, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}}
	host := New(consumer, provider)
	registry, conflicts := BuildServiceRegistry(consumer, provider)
	host.AttachServiceRegistry(registry, conflicts)
	registry.Activate()
	provider.activate = func(context.Context) error { events = append(events, "open storage"); return nil }
	provider.close = func(context.Context) error { events = append(events, "close storage"); return nil }
	provider.invoke = func(context.Context, ServiceInvokeParams) (json.RawMessage, error) {
		events = append(events, "persist")
		return json.RawMessage(`{}`), nil
	}
	consumer.activate = func(ctx context.Context) error {
		events = append(events, "open consumer")
		_, err := registry.Call(ctx, consumer.id, ServiceCallParams{Service: "search.provider", Method: "query"})
		if err != nil {
			return err
		}
		return nil
	}
	consumer.close = func(ctx context.Context) error {
		events = append(events, "close consumer")
		_, err := registry.Call(ctx, consumer.id, ServiceCallParams{Service: "search.provider", Method: "query"})
		if err != nil {
			return err
		}
		return nil
	}
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"open storage", "open consumer", "persist", "close consumer", "persist", "close storage"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("lifecycle = %v, want %v", events, want)
	}
}

func TestServiceDependenciesBlockTransitiveActivation(t *testing.T) {
	for _, scenario := range []string{"missing", "wrong-major", "activation-failed", "optional"} {
		t.Run(scenario, func(t *testing.T) {
			provider := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "provider", state: StatePrepared, provided: []ServiceDescriptor{searchService("1.0.0")}}}
			consumer := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "consumer", state: StatePrepared, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}}
			if scenario == "activation-failed" {
				provider.activate = func(context.Context) error { return errors.New("storage unavailable") }
			} else {
				provider.required = []ServiceRequirement{{Name: "missing.storage", MajorVersion: 2, Required: scenario != "optional"}}
			}
			other := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "other", state: StatePrepared}}
			if scenario == "wrong-major" {
				other.provided = []ServiceDescriptor{{Name: "missing.storage", Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "read"}}}}
			}
			host := New(consumer, provider, other)
			registry, conflicts := BuildServiceRegistry(consumer, provider, other)
			host.AttachServiceRegistry(registry, conflicts)
			registry.Activate()
			defer host.Close(context.Background())
			_ = host.Activate(context.Background())
			if scenario == "optional" {
				if consumer.state != StateActive || provider.state != StateActive {
					t.Fatal("missing optional dependency blocked activation")
				}
			} else {
				if consumer.state != StateStopped {
					t.Fatalf("dependent consumer state = %s", consumer.state)
				}
				if registry.HasProvider("search.provider", 1) {
					t.Fatal("failed provider remains routable")
				}
			}
			if other.state != StateActive {
				t.Fatal("independent provider was blocked")
			}
		})
	}
}

func TestMixedPackageAndServiceCycleRejectsActivation(t *testing.T) {
	provider := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "provider", state: StatePrepared, provided: []ServiceDescriptor{searchService("1.0.0")}}}
	consumer := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "consumer", state: StatePrepared, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}}
	host := New(consumer, provider)
	host.SetPackageRequirements(map[string][]string{"provider": {"consumer"}, "consumer": nil})
	registry, conflicts := BuildServiceRegistry(provider, consumer)
	host.AttachServiceRegistry(registry, conflicts)
	registry.Activate()
	defer host.Close(context.Background())
	if err := host.Activate(context.Background()); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle error = %v", err)
	}
	if provider.state != StatePrepared || consumer.state != StatePrepared {
		t.Fatal("cyclic candidate performed activation effects")
	}
}

func TestShutdownCleanupCrossesProviderTransports(t *testing.T) {
	storage := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "storage", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}}
	bridge := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "bridge", state: StateActive,
		provided: []ServiceDescriptor{{Name: "workflow.state", Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "flush"}}}},
		required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}},
	}}
	consumer := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "consumer", state: StateActive,
		required: []ServiceRequirement{{Name: "workflow.state", MajorVersion: 1, Required: true}},
	}}
	unrelated := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "unrelated", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}}
	host := New(storage, bridge, consumer, unrelated)
	registry, conflicts := BuildServiceRegistry(storage, bridge, consumer, unrelated)
	host.AttachServiceRegistry(registry, conflicts)
	registry.Activate()
	persisted := false
	storage.invoke = func(ctx context.Context, _ ServiceInvokeParams) (json.RawMessage, error) {
		persisted = InShutdownCleanup(ctx)
		return json.RawMessage(`{}`), nil
	}
	bridge.invoke = func(_ context.Context, params ServiceInvokeParams) (json.RawMessage, error) {
		// A process callback receives the routed execution ID, not a Go context.
		if _, err := registry.Call(context.Background(), unrelated.id, ServiceCallParams{Service: "search.provider", Method: "query", ExecutionID: params.ExecutionID}); err == nil {
			return nil, errors.New("another plugin borrowed cleanup authority")
		}
		result, err := registry.Call(context.Background(), bridge.id, ServiceCallParams{Service: "search.provider", Method: "query", ExecutionID: params.ExecutionID})
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	consumer.close = func(ctx context.Context) error {
		_, err := registry.Call(ctx, consumer.id, ServiceCallParams{Service: "workflow.state", Method: "flush"})
		if err != nil {
			return err
		}
		return nil
	}
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !persisted {
		t.Fatal("transport callback lost shutdown cleanup ownership")
	}
}

func TestProviderRetirementClosesDependentBeforeResource(t *testing.T) {
	var closed []string
	provider := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "provider", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}}
	consumer := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "consumer", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}}
	host := New(consumer, provider)
	registry, conflicts := BuildServiceRegistry(consumer, provider)
	host.AttachServiceRegistry(registry, conflicts)
	registry.Activate()
	consumer.close = func(ctx context.Context) error {
		closed = append(closed, consumer.id)
		_, err := registry.Call(ctx, consumer.id, ServiceCallParams{Service: "search.provider", Method: "query"})
		if err != nil {
			return err
		}
		return nil
	}
	provider.close = func(context.Context) error { closed = append(closed, provider.id); return nil }
	outcome, found := host.RetirePlugin(context.Background(), provider.id, errors.New("disabled"))
	if !found || outcome.Err != nil {
		t.Fatalf("retirement = %+v, %v", outcome, found)
	}
	if !reflect.DeepEqual(closed, []string{"consumer", "provider"}) {
		t.Fatalf("cleanup order = %v", closed)
	}
}
