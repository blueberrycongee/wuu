package persistence

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Preparation cannot open a writer, errors must survive the shared registry,
// and revocation cannot leave the native implementation callable as a fallback.
func TestServiceLifecycleAndErrorContract(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			provider := NewService("test.store", t.TempDir(), kind)
			consumer := &storeConsumer{}
			registry, conflicts := pluginhost.BuildServiceRegistry(provider, consumer)
			if len(conflicts) != 0 {
				t.Fatal(conflicts)
			}
			defer registry.Close(ctx)
			call := func(method string, request any) (json.RawMessage, *pluginhost.HostServiceError) {
				raw, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				return registry.Call(ctx, consumer.ID(), pluginhost.ServiceCallParams{Service: ServiceName, Method: method, Params: raw})
			}
			registry.Activate()
			if _, err := call("read", Query{Namespace: "sessions"}); err == nil {
				t.Fatal("read before activation succeeded")
			}
			if err := provider.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			defer provider.Close(ctx)
			tx := Transaction{ID: "create", Writes: []Write{{Namespace: "sessions", Key: "one", Value: json.RawMessage(`{}`)}}}
			if _, err := call("commit", tx); err != nil {
				t.Fatal(err)
			}
			tx.ID = "conflict"
			if _, err := call("commit", tx); err == nil || err.Code != "state_conflict" {
				t.Fatalf("error lost: %v", err)
			}
			if _, err := call("read", Query{Namespace: "sessions"}); err != nil {
				t.Fatal(err)
			}
			registry.RevokePlugin(ctx, provider.ID())
			if _, err := call("read", Query{Namespace: "sessions"}); err == nil {
				t.Fatal("revoked provider remained callable")
			}
		})
	}
}

type storeConsumer struct{}

func (*storeConsumer) ID() string { return "test.consumer" }
func (*storeConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: "test.consumer", State: pluginhost.StateActive}
}
func (*storeConsumer) Close(context.Context) error                      { return nil }
func (*storeConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*storeConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: ServiceName, MajorVersion: 1, Required: true}}
}
