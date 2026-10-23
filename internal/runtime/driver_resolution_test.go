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

func newDriverTestHost(t *testing.T, profile string, clients ...pluginhost.Client) *pluginhost.Host {
	t.Helper()
	kernel := newKernelHostServices(nil, nil)
	clients = append(clients, kernel)
	clients = append(clients, runtimeServiceConsumers(profile)...)
	host := pluginhost.New(clients...)
	registry, conflicts := pluginhost.BuildServiceRegistry(clients...)
	kernel.bindRegistry(registry)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	host.AttachServiceRegistry(registry, conflicts)
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	return host
}

func TestResolveLoopDriverFailsClosedWithoutProvider(t *testing.T) {
	host := newDriverTestHost(t, "")
	if _, ok := resolveLoopDriver("ghost", nil, nil).(loopdriver.FailClosedDriver); !ok {
		t.Fatalf("nil host must fail closed")
	}
	driver := resolveLoopDriver("ghost", host, nil)
	failClosed, ok := driver.(loopdriver.FailClosedDriver)
	if !ok {
		t.Fatalf("driver = %#v, want FailClosedDriver", driver)
	}
	_, err := failClosed.Create(context.Background(), loopdriver.ExecutionContext{SessionID: "s", ExecutionID: "e"}, loopdriver.PersistedInput{})
	if err == nil || !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "driver.ghost") {
		t.Fatalf("create error = %v, want fail-closed diagnostic naming the profile and service", err)
	}
	if _, err := failClosed.Resume(context.Background(), loopdriver.ExecutionContext{SessionID: "s", ExecutionID: "e"}, loopdriver.PersistedInput{}, loopdriver.Checkpoint{}); err == nil {
		t.Fatalf("resume must fail closed")
	}
}

func TestResolveLoopDriverRemoteRoutesThroughRegistry(t *testing.T) {
	var gotExecutionID, gotMethod, gotService string
	table := newDriverGatewayTable()
	selectedTable := table
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
				return json.Marshal(map[string]any{"instance_id": "inst-1", "checkpoint": loopdriver.Checkpoint{ContractVersion: loopdriver.ContractVersion, DriverID: "demo", DriverVersion: "1.0.0"}})
			case loopdriver.RemoteMethodRun:
				if _, ok := table.lookup("exec-1"); !ok {
					return nil, fmt.Errorf("driver callback moved to another generation")
				}
				if _, ok := selectedTable.lookup("exec-1"); ok {
					return nil, fmt.Errorf("old execution registered in the new generation")
				}
				return json.Marshal(map[string]any{"status": string(loopdriver.TerminalSucceeded), "checkpoint": loopdriver.Checkpoint{ContractVersion: loopdriver.ContractVersion, DriverID: "demo", DriverVersion: "1.0.0"}})
			}
			return nil, fmt.Errorf("unexpected method %q", params.Method)
		},
	}
	host := newDriverTestHost(t, "demo", provider)
	driver := resolveLoopDriver("demo", host, selectedTable)
	selectedTable = newDriverGatewayTable()
	remote, ok := driver.(*loopdriver.RemoteDriver)
	if !ok {
		t.Fatalf("driver = %#v, want *RemoteDriver", driver)
	}
	if remote.ServiceID != "driver.demo" {
		t.Fatalf("ServiceID = %q", remote.ServiceID)
	}
	if got, err := remote.Descriptor(context.Background()); err != nil || got.ID != "demo" {
		t.Fatalf("Descriptor() = %+v, %v", got, err)
	}
	instance, err := remote.Create(context.Background(), loopdriver.ExecutionContext{SessionID: "s", ExecutionID: "exec-1"}, loopdriver.PersistedInput{})
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
	if gotService != "driver.demo" || gotMethod != "run" || gotExecutionID == "" {
		t.Fatalf("invoke = (%q, %q, %q)", gotService, gotMethod, gotExecutionID)
	}
	// The resolved driver registers into its original provider generation and
	// unregisters there when the run completes.
	if _, ok := table.lookup("exec-1"); ok {
		t.Fatalf("gateway must be unregistered after the run completes")
	}
	firstRelease, err := table.register("exec-1", nil, "first")
	if err != nil {
		t.Fatal(err)
	}
	firstRelease()
	secondRelease, err := table.register("exec-1", nil, "second")
	if err != nil {
		t.Fatal(err)
	}
	defer secondRelease()
	firstRelease()
	if entry, ok := table.lookup("exec-1"); !ok || entry.ownerPluginID != "second" {
		t.Fatal("old gateway disposer removed the replacement registration")
	}
}
