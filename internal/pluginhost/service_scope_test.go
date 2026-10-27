package pluginhost

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRetainedServiceScopePartialFailureAndClosedAuthority(t *testing.T) {
	provider := &fakeServiceProcess{id: "provider", state: StateActive, provided: []ServiceDescriptor{{Name: "resource.capacity", Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "release", Cleanup: true, InputSchema: "resource.release.input.v1", OutputSchema: "resource.release.output.v1"}}}}}
	consumer := &fakeServiceProcess{id: "consumer", state: StateActive, required: []ServiceRequirement{{Name: "resource.capacity", MajorVersion: 1, Required: true}}}
	host := New(provider, consumer)
	registry, conflicts := BuildServiceRegistry(provider, consumer)
	if len(conflicts) > 0 {
		t.Fatalf("registration conflicts: %+v", conflicts)
	}
	host.AttachServiceRegistry(registry, conflicts)
	registry.Activate()
	if _, err := host.OpenServiceScope(context.Background(), "consumer", "resource.capacity", "undeclared"); err == nil {
		t.Fatal("undeclared resource gained retained authority")
	}
	registry.mu.RLock()
	live := len(registry.executions)
	registry.mu.RUnlock()
	if live != 0 {
		t.Fatalf("partial scope leaked %d executions", live)
	}
	scope, err := host.OpenServiceScope(context.Background(), "consumer", "resource.capacity")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scope.CallCleanup(context.Background(), "resource.capacity", "release", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := scope.CallCleanup(context.Background(), "resource.capacity", "release", json.RawMessage(`{}`)); err == nil {
		t.Fatal("closed scope retained cleanup authority")
	}
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
