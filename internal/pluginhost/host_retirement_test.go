package pluginhost

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestHostRetirePluginLeavesUnrelatedPluginActive(t *testing.T) {
	retired := &fakeCapabilityClient{
		fakeClient:   &fakeClient{id: "plugin-a", status: Status{ID: "plugin-a", State: StateActive}},
		capabilities: []CapabilityDescriptor{{ID: "capability.a", Kind: "observe", Version: 1}},
	}
	retained := &fakeCapabilityClient{
		fakeClient:   &fakeClient{id: "plugin-b", status: Status{ID: "plugin-b", State: StateActive}},
		capabilities: []CapabilityDescriptor{{ID: "capability.b", Kind: "observe", Version: 1}},
	}
	host := New(retired, retained)
	staleCapability := host.Capabilities("capability.a")[0]
	retiredExecutionID := host.executions.Begin(retired.id)
	retiredContext := host.executions.live[retiredExecutionID].ctx
	retainedExecutionID := host.executions.Begin(retained.id)
	retainedContext := host.executions.live[retainedExecutionID].ctx

	outcome, found := host.RetirePlugin(context.Background(), retired.id, errors.New("retired"))
	if !found || outcome.PluginID != retired.id || outcome.Err != nil {
		t.Fatalf("retirement = (%+v, %v)", outcome, found)
	}
	if !retired.closed || retained.closed {
		t.Fatalf("closed clients: retired=%v retained=%v", retired.closed, retained.closed)
	}
	if context.Cause(retiredContext) == nil {
		t.Fatal("retired plugin execution was not canceled")
	}
	if context.Cause(retainedContext) != nil {
		t.Fatalf("retained plugin execution canceled: %v", context.Cause(retainedContext))
	}
	if got := host.Statuses(); len(got) != 1 || got[0].ID != retained.id {
		t.Fatalf("statuses = %+v", got)
	}
	if got := host.Capabilities("capability.a"); len(got) != 0 {
		t.Fatalf("retired capabilities remain: %+v", got)
	}
	if got := host.Capabilities("capability.b"); len(got) != 1 || got[0].PluginID != retained.id {
		t.Fatalf("retained capabilities = %+v", got)
	}
	if err := host.InvokeCapability(context.Background(), staleCapability, struct{}{}, &struct{}{}); err == nil || !strings.Contains(err.Error(), "is not active") {
		t.Fatalf("stale capability invocation error = %v", err)
	}
}

type cleanupServiceClient struct {
	*fakeServiceProcess
	cleanup func(context.Context) error
}

func (c *cleanupServiceClient) Close(ctx context.Context) error { return c.cleanup(ctx) }

func TestRetirementAllowsCleanupBeforeRevokingServices(t *testing.T) {
	provider := &fakeServiceProcess{id: "storage", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
	stateService := ServiceDescriptor{Name: "workflow.state", Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "save"}}}
	consumer := &cleanupServiceClient{fakeServiceProcess: &fakeServiceProcess{
		id: "workflow", state: StateActive, provided: []ServiceDescriptor{stateService},
		required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}, {Name: stateService.Name, MajorVersion: 1, Required: true}},
	}}
	observer := &fakeServiceProcess{id: "observer", state: StateActive, required: []ServiceRequirement{{Name: stateService.Name, MajorVersion: 1, Required: true}}}
	host := New(provider, consumer, observer)
	registry, conflicts := BuildServiceRegistry(provider, consumer, observer)
	host.AttachServiceRegistry(registry, conflicts)
	registry.Activate()
	consumer.cleanup = func(ctx context.Context) error {
		for _, status := range host.Statuses() {
			if status.ID == consumer.id {
				t.Error("plugin still registered during shutdown")
			}
		}
		if _, err := registry.Call(ctx, observer.id, ServiceCallParams{Service: stateService.Name, Method: "save"}); err == nil {
			return errors.New("retiring provider accepted another consumer during shutdown")
		}
		if _, err := registry.CallProvider(ctx, stateService.Name, 1, "save", nil, "late-driver"); err == nil {
			return errors.New("retiring provider accepted new kernel work during shutdown")
		}
		if _, err := registry.Call(ctx, consumer.id, ServiceCallParams{Service: stateService.Name, Method: "save"}); err != nil {
			return err
		}
		_, err := registry.Call(ctx, consumer.id, ServiceCallParams{Service: "search.provider", Method: "query"})
		if err != nil {
			return err
		}
		return nil
	}
	result, ok := host.RetirePlugin(context.Background(), consumer.id, nil)
	if !ok || result.Err != nil {
		t.Fatalf("shutdown cleanup failed: %+v", result)
	}
	if _, err := registry.Call(context.Background(), consumer.id, ServiceCallParams{Service: "search.provider", Method: "query"}); err == nil {
		t.Fatal("services remained available after retirement")
	}
}
