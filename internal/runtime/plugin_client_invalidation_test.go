package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

// Invalidations are hints, not a channel for caller-supplied identities or content.
func TestClientInvalidationOwnershipAndLifecycle(t *testing.T) {
	router := NewPluginSessionRouter()
	var received []pluginhost.ClientInvalidation
	unbind := router.BindClientInvalidation(func(_ context.Context, _ *pluginhost.Host, event pluginhost.ClientInvalidation) error {
		received = append(received, event)
		return nil
	})
	defer unbind()
	s := newPluginHostServices(serviceTestPlugin("demo", "plugin:user:demo", "one"), t.TempDir(), t.TempDir(), router)
	s.binding = userspace.Binding{UserSpaceID: "alice", ProductDomain: "personal"}
	k := newKernelHostServices(nil, nil)
	k.add("demo", s)
	registry, conflicts := pluginhost.BuildServiceRegistry(k)
	if len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	registry.RegisterClients(&kernelConsumer{id: "demo", requirements: []pluginhost.ServiceRequirement{{Name: "host.client.invalidate", MajorVersion: 1, Required: true}}})
	activateServiceTestHost(t, k, registry)
	call := func(caller, raw string) *pluginhost.HostServiceError {
		_, err := registry.Call(context.Background(), caller, pluginhost.ServiceCallParams{Service: "host.client.invalidate", Method: "call", Params: json.RawMessage(raw)})
		return err
	}
	for _, raw := range []string{`{"topic":"reactions","plugin_id":"other"}`, `{"topic":"reactions","user_space_id":"bob"}`, `{"topic":"reactions","content":"secret"}`, `{"topic":""}`, `{"topic":"not a topic"}`} {
		if err := call("demo", raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if err := call("stranger", `{"topic":"reactions"}`); err == nil {
		t.Fatal("undeclared caller accepted")
	}
	if err := call("demo", `{"topic":"reactions"}`); err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 || received[0].PluginID != "plugin:user:demo" || received[0].Fingerprint != "one" || received[0].UserSpaceID != "alice" || received[0].ProductDomain != "personal" {
		t.Fatalf("wrong trusted envelope: %+v", received)
	}
	s.close()
	if err := call("demo", `{"topic":"reactions"}`); err == nil {
		t.Fatal("closed generation emitted")
	}
	if len(received) != 1 {
		t.Fatal("unexpected notification")
	}
}

func TestClientInvalidationRebindCleanup(t *testing.T) {
	router := NewPluginSessionRouter()
	first, second := 0, 0
	oldClose := router.BindClientInvalidation(func(context.Context, *pluginhost.Host, pluginhost.ClientInvalidation) error { first++; return nil })
	newClose := router.BindClientInvalidation(func(context.Context, *pluginhost.Host, pluginhost.ClientInvalidation) error { second++; return nil })
	oldClose()
	if err := router.invalidateClient(context.Background(), nil, pluginhost.ClientInvalidation{}); err != nil {
		t.Fatal(err)
	}
	if first != 0 || second != 1 {
		t.Fatalf("wrong route: old=%d new=%d", first, second)
	}
	newClose()
	if err := router.invalidateClient(context.Background(), nil, pluginhost.ClientInvalidation{}); err == nil {
		t.Fatal("closed route accepted")
	}
}
