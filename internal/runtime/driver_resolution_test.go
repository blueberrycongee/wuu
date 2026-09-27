package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type driverProviderClient struct {
	id         string
	descriptor pluginhost.ServiceDescriptor
	invoke     func(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error)
}

func (c *driverProviderClient) ID() string { return c.id }
func (c *driverProviderClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.id, State: pluginhost.StateActive}
}
func (c *driverProviderClient) Close(context.Context) error { return nil }
func (c *driverProviderClient) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{c.descriptor}
}
func (c *driverProviderClient) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (c *driverProviderClient) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	return c.invoke(ctx, params)
}

func newDriverTestRegistry(t *testing.T, clients ...pluginhost.Client) *pluginhost.ServiceRegistry {
	t.Helper()
	kernel := newKernelHostServices(nil, nil)
	registry, conflicts := pluginhost.BuildServiceRegistry(kernel)
	kernel.bindRegistry(registry)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	if len(clients) > 0 {
		if registrationConflicts := registry.RegisterClients(clients...); len(registrationConflicts) != 0 {
			t.Fatalf("registration conflicts = %+v", registrationConflicts)
		}
	}
	registry.Activate()
	t.Cleanup(func() { registry.Close(context.Background()) })
	return registry
}

func TestResolveLoopDriverFailsClosedWithoutProvider(t *testing.T) {
	registry := newDriverTestRegistry(t)
	if _, ok := resolveLoopDriver("ghost", nil, nil).(loopdriver.FailClosedDriver); !ok {
		t.Fatalf("nil host must fail closed")
	}
	host := new(pluginhost.Host)
	host.AttachServiceRegistry(registry, nil)
	driver := resolveLoopDriver("ghost", host, nil)
	failClosed, ok := driver.(loopdriver.FailClosedDriver)
	if !ok {
		t.Fatalf("driver = %#v, want FailClosedDriver", driver)
	}
	_, err := failClosed.Create(loopdriver.ExecutionContext{SessionID: "s", ExecutionID: "e"}, loopdriver.PersistedInput{})
	if err == nil || !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "driver.ghost") {
		t.Fatalf("create error = %v, want fail-closed diagnostic naming the profile and service", err)
	}
	if _, err := failClosed.Resume(loopdriver.ExecutionContext{SessionID: "s", ExecutionID: "e"}, loopdriver.PersistedInput{}, loopdriver.Checkpoint{}); err == nil {
		t.Fatalf("resume must fail closed")
	}
}

func TestResolveLoopDriverRemoteRoutesThroughRegistry(t *testing.T) {
	var gotExecutionID, gotMethod, gotService string
	provider := &driverProviderClient{
		id: "driver-demo",
		descriptor: pluginhost.ServiceDescriptor{
			Name:    "driver.demo",
			Version: "1.0.0",
			Methods: []pluginhost.ServiceMethodDescriptor{{Name: "descriptor"}, {Name: "create"}, {Name: "resume"}, {Name: "run"}, {Name: "shutdown"}},
		},
		invoke: func(_ context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
			gotService, gotMethod, gotExecutionID = params.Service, params.Method, params.ExecutionID
			switch params.Method {
			case loopdriver.RemoteMethodDescriptor:
				return json.Marshal(map[string]any{"id": "demo", "version": "1.0.0"})
			case loopdriver.RemoteMethodCreate:
				return json.Marshal(map[string]any{"instance_id": "inst-1"})
			case loopdriver.RemoteMethodRun:
				return json.Marshal(map[string]any{"status": string(loopdriver.TerminalSucceeded)})
			}
			return nil, fmt.Errorf("unexpected method %q", params.Method)
		},
	}
	registry := newDriverTestRegistry(t, provider)
	host := new(pluginhost.Host)
	host.AttachServiceRegistry(registry, nil)
	table := newDriverGatewayTable()

	driver := resolveLoopDriver("demo", host, func() *driverGatewayTable { return table })
	remote, ok := driver.(*loopdriver.RemoteDriver)
	if !ok {
		t.Fatalf("driver = %#v, want *RemoteDriver", driver)
	}
	if remote.ServiceID != "driver.demo" {
		t.Fatalf("ServiceID = %q", remote.ServiceID)
	}
	if got := remote.Descriptor().ID; got != "demo" {
		t.Fatalf("Descriptor().ID = %q", got)
	}
	instance, err := remote.Create(loopdriver.ExecutionContext{SessionID: "s", ExecutionID: "exec-1"}, loopdriver.PersistedInput{})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	outcome, err := instance.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if outcome.Status != loopdriver.TerminalSucceeded {
		t.Fatalf("outcome = %+v", outcome)
	}
	if gotService != "driver.demo" || gotMethod != "run" || gotExecutionID != "exec-1" {
		t.Fatalf("invoke = (%q, %q, %q)", gotService, gotMethod, gotExecutionID)
	}
	// The resolved driver must register its gateway into the current
	// generation's table so kernel gateway invokers can route back, and must
	// unregister when the run completes.
	if _, ok := table.lookup("exec-1"); ok {
		t.Fatalf("gateway must be unregistered after the run completes")
	}
}
