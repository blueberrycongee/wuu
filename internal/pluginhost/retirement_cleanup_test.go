package pluginhost

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetirementCleanupPrecedesScopeDrainAndKeepsCleanupAuthority(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "disable", true: "shutdown"}[shutdown], func(t *testing.T) {
			provider := &fakeServiceProcess{id: "provider", state: StateActive, provided: []ServiceDescriptor{{Name: "resource.capacity", Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "release", Cleanup: true, InputSchema: "release.input.v1", OutputSchema: "release.output.v1"}}}}}
			consumer := &fakeServiceProcess{id: "consumer", state: StateActive, required: []ServiceRequirement{{Name: "resource.capacity", MajorVersion: 1, Required: true}}}
			host := New(provider, consumer)
			registry, conflicts := BuildServiceRegistry(provider, consumer)
			host.AttachServiceRegistry(registry, conflicts)
			registry.Activate()
			scope, err := host.OpenServiceScope(context.Background(), consumer.id, "resource.capacity")
			if err != nil {
				t.Fatal(err)
			}
			var called atomic.Int32
			releaseDone := make(chan struct{})
			_, err = registry.RegisterRetirementCleanup(consumer.id, func(ctx context.Context) error {
				called.Add(1)
				if _, err := registry.RegisterRetirementCleanup(consumer.id, func(context.Context) error { return nil }); err == nil {
					t.Error("retiring owner admitted another resource")
				}
				if _, err := scope.CallCleanup(ctx, "resource.capacity", "release", json.RawMessage(`{}`)); err != nil {
					return err
				}
				if !DeferServiceCompletion(ctx, func() error {
					defer close(releaseDone)
					if shutdown {
						return host.Close(context.Background())
					}
					return nil
				}) {
					t.Error("missing retirement completion boundary")
				}
				return scope.Close()
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if shutdown {
				if err := host.Close(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				outcome, found := host.RetireFailedPlugin(ctx, Status{ID: consumer.id, Error: "crashed"}, nil)
				if !found || outcome.Err != nil {
					t.Fatalf("retirement=%+v found=%v", outcome, found)
				}
				defer host.Close(context.Background())
			}
			select {
			case <-releaseDone:
			case <-ctx.Done():
				t.Fatal("deferred reference release self-waited on host shutdown")
			}
			if called.Load() != 1 {
				t.Fatalf("cleanup calls=%d", called.Load())
			}
		})
	}
}

func TestRetirementCleanupUnregisterAndLateAdmission(t *testing.T) {
	registry := NewServiceRegistry()
	var calls atomic.Int32
	unregister, err := registry.RegisterRetirementCleanup("owner", func(context.Context) error { calls.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	unregister()
	unregister()
	registry.stopProviderAdmission("owner")
	if err := registry.waitRetirementCleanup("owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.RegisterRetirementCleanup("owner", func(context.Context) error { return nil }); err == nil {
		t.Fatal("retired owner registered resource")
	}
	if calls.Load() != 0 {
		t.Fatal("unregistered cleanup invoked")
	}
}

func TestRetirementCleanupPanicStillCompletesBarrier(t *testing.T) {
	registry := NewServiceRegistry()
	_, err := registry.RegisterRetirementCleanup("owner", func(context.Context) error { panic("broken cleanup") })
	if err != nil {
		t.Fatal(err)
	}
	registry.stopProviderAdmission("owner")
	if err := registry.waitRetirementCleanup("owner"); err == nil {
		t.Fatal("cleanup panic was lost")
	}
	registry.Close(context.Background())
	if _, err := registry.RegisterRetirementCleanup("different-owner", func(context.Context) error { return nil }); err == nil {
		t.Fatal("closed registry admitted resource")
	}
}

func TestRetirementResourceCleanupPreservesProviderUntilConsumerCloses(t *testing.T) {
	provider := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "storage", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}}
	consumer := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "consumer", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}}
	host := New(consumer, provider)
	registry, conflicts := BuildServiceRegistry(consumer, provider)
	host.AttachServiceRegistry(registry, conflicts)
	registry.Activate()
	var consumerClosed, providerReleased atomic.Bool
	consumer.close = func(context.Context) error {
		if providerReleased.Load() {
			t.Error("provider resource disposed before consumer cleanup")
		}
		consumerClosed.Store(true)
		return nil
	}
	_, err := registry.RegisterRetirementCleanup(provider.id, func(context.Context) error {
		if !consumerClosed.Load() {
			t.Error("provider resource cleanup preceded dependent consumer")
		}
		providerReleased.Store(true)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !providerReleased.Load() {
		t.Fatal("provider resource not released")
	}
}
