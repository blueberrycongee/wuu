package pluginhost

import (
	"context"
	"testing"
)

func TestServiceRegistryRevokePluginPreservesOtherProviders(t *testing.T) {
	search := &fakeServiceProcess{id: "search", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
	memoryService := ServiceDescriptor{
		Name: "memory.index", Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{{Name: "query", InputSchema: "memory.query.request.v1", OutputSchema: "memory.query.response.v1"}},
	}
	memory := &fakeServiceProcess{id: "memory", state: StateActive, provided: []ServiceDescriptor{memoryService}}
	consumer := &fakeServiceProcess{id: "notes", state: StateActive, required: []ServiceRequirement{
		{Name: "search.provider", MajorVersion: 1, Required: true},
		{Name: "memory.index", MajorVersion: 1, Required: true},
	}}
	registry, conflicts := BuildServiceRegistry(search, memory, consumer)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	registry.Activate()
	registry.RevokePlugin(context.Background(), search.id)

	if registry.HasProvider("search.provider", 1) {
		t.Fatal("retired provider remains routable")
	}
	if !registry.HasProvider("memory.index", 1) {
		t.Fatal("unrelated provider was revoked")
	}
	if _, err := registry.Call(context.Background(), consumer.id, ServiceCallParams{Service: "search.provider", Method: "query"}); err == nil || err.Code != "service_unavailable" {
		t.Fatalf("retired provider call = %v, want service_unavailable", err)
	}
	if _, err := registry.Call(context.Background(), consumer.id, ServiceCallParams{Service: "memory.index", Method: "query"}); err != nil {
		t.Fatalf("unrelated provider call failed: %v", err)
	}
	// The provider's status check falls after declaration lookup and before
	// admission. Revoke at that boundary to exercise the stale caller race.
	memory.onStatus = func() { registry.RevokePlugin(context.Background(), consumer.id) }
	callsBefore := len(memory.invokeCalls)
	if _, err := registry.Call(context.Background(), consumer.id, ServiceCallParams{Service: "memory.index", Method: "query"}); err == nil || err.Code != "service_not_authorized" {
		t.Fatalf("revoked caller passed stale admission: %v", err)
	}
	if len(memory.invokeCalls) != callsBefore {
		t.Fatal("revoked caller reached the provider")
	}
}
