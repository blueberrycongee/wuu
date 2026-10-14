package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type fakeServicePluginClient struct {
	id       string
	provided []pluginhost.ServiceDescriptor
	required []pluginhost.ServiceRequirement
	closed   bool
}

func (c *fakeServicePluginClient) ID() string { return c.id }
func (c *fakeServicePluginClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.id, State: pluginhost.StatePrepared}
}
func (c *fakeServicePluginClient) Close(context.Context) error { c.closed = true; return nil }
func (c *fakeServicePluginClient) ProvidedServices() []pluginhost.ServiceDescriptor {
	return c.provided
}
func (c *fakeServicePluginClient) RequiredServices() []pluginhost.ServiceRequirement {
	return c.required
}
func (c *fakeServicePluginClient) InvokeService(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func searchServiceDescriptor(version string) pluginhost.ServiceDescriptor {
	return pluginhost.ServiceDescriptor{
		Name:    "search.provider",
		Version: version,
		Methods: []pluginhost.ServiceMethodDescriptor{
			{Name: "query", InputSchema: "search.query.request.v1", OutputSchema: "search.query.response.v1"},
		},
	}
}

func buildServiceTestHost(t *testing.T, clients map[string]pluginhost.Client, pluginIDs ...string) *pluginhost.Host {
	t.Helper()
	plugins := make([]pluginpkg.Plugin, 0, len(pluginIDs))
	for _, id := range pluginIDs {
		plugins = append(plugins, testRuntimePlugin(id))
	}
	start := func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
		return clients[cfg.ID], nil
	}
	host, _, err := buildPluginHost(context.Background(), plugins, "", "", "", "", nil, start, nil, nil)
	if err != nil {
		t.Fatalf("buildPluginHost() = %v", err)
	}
	return host
}

func TestBuildPluginHostSatisfiedServiceRequirements(t *testing.T) {
	provider := &fakeServicePluginClient{id: "search", provided: []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.0.0")}}
	consumer := &fakeServicePluginClient{id: "notes", required: []pluginhost.ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
	host := buildServiceTestHost(t, map[string]pluginhost.Client{"search": provider, "notes": consumer}, "search", "notes")

	registry := host.ServiceRegistry()
	if registry == nil {
		t.Fatal("expected an attached service registry")
	}
	if !registry.HasProvider("search.provider", 1) {
		t.Fatal("provider must be registered")
	}
	if consumer.closed {
		t.Fatal("satisfied consumer must stay live")
	}
	for _, status := range host.Statuses() {
		if status.State == pluginhost.StateFailed {
			t.Fatalf("unexpected failed plugin: %+v", status)
		}
	}
}

func TestBuildPluginHostUnsatisfiedRequiredServiceBlocksConsumer(t *testing.T) {
	provider := &fakeServicePluginClient{id: "search", provided: []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.0.0")}}
	consumer := &fakeServicePluginClient{id: "notes", required: []pluginhost.ServiceRequirement{{Name: "memory.index", MajorVersion: 1, Required: true}}}
	host := buildServiceTestHost(t, map[string]pluginhost.Client{"search": provider, "notes": consumer}, "search", "notes")

	if !consumer.closed {
		t.Fatal("unsatisfied consumer must be closed before registration")
	}
	var notesStatus *pluginhost.Status
	statuses := host.Statuses()
	for index := range statuses {
		if statuses[index].ID == "notes" {
			notesStatus = &statuses[index]
		}
	}
	if notesStatus == nil || notesStatus.State != pluginhost.StateFailed || !strings.Contains(notesStatus.Error, "memory.index") {
		t.Fatalf("notes status = %+v, want failed with service diagnostic", notesStatus)
	}
	if !host.ServiceRegistry().HasProvider("search.provider", 1) {
		t.Fatal("unaffected provider must stay registered")
	}
}

func TestBuildPluginHostOptionalServiceRequirementDoesNotBlock(t *testing.T) {
	consumer := &fakeServicePluginClient{id: "notes", required: []pluginhost.ServiceRequirement{{Name: "memory.index", MajorVersion: 1}}}
	host := buildServiceTestHost(t, map[string]pluginhost.Client{"notes": consumer}, "notes")
	if consumer.closed {
		t.Fatal("optional requirement must not block activation")
	}
	for _, status := range host.Statuses() {
		if status.State == pluginhost.StateFailed {
			t.Fatalf("unexpected failed plugin: %+v", status)
		}
	}
}

func TestBuildPluginHostDuplicateProviderSurfacesDiagnostic(t *testing.T) {
	first := &fakeServicePluginClient{id: "search-a", provided: []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.0.0")}}
	second := &fakeServicePluginClient{id: "search-b", provided: []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.1.0")}}
	host := buildServiceTestHost(t, map[string]pluginhost.Client{"search-a": first, "search-b": second}, "search-a", "search-b")

	diagnostics := host.ContributionDiagnostics("search-b")
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "already provided by plugin \"search-a\"") {
		t.Fatalf("conflict diagnostics = %+v", diagnostics)
	}
	if !host.ServiceRegistry().HasProvider("search.provider", 1) {
		t.Fatal("first registration must win")
	}
}
