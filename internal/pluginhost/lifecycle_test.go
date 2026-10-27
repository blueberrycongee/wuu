package pluginhost

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Generation retirement must await cleanup, retain its error, and never release
// the same transport twice, including when activation or disposal panics.
func TestHostLifecycleContainsProviderPanics(t *testing.T) {
	for _, phase := range []string{"prepare", "activate", "close"} {
		t.Run(phase, func(t *testing.T) {
			defer func() {
				if value := recover(); value != nil {
					t.Errorf("provider panic escaped %s: %v", phase, value)
				}
			}()
			var closed int
			client := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "provider", state: StatePrepared}}
			client.prepare = func(context.Context) error {
				if phase == "prepare" {
					panic("preparation exploded")
				}
				return nil
			}
			client.activate = func(context.Context) error {
				if phase == "activate" {
					panic("activation exploded")
				}
				return nil
			}
			client.close = func(context.Context) error {
				closed++
				if phase == "close" {
					panic("cleanup exploded")
				}
				return nil
			}
			host := New(client)
			err := host.Activate(context.Background())
			err = errors.Join(err, host.Close(context.Background()))
			if err == nil || !strings.Contains(err.Error(), "exploded") {
				t.Fatalf("panic was not reported: %v", err)
			}
			_ = host.Close(context.Background())
			if closed != 1 {
				t.Fatalf("transport closed %d times", closed)
			}
		})
	}
}

func TestHostConcurrentShutdownWaitsForConsumerFlush(t *testing.T) {
	flushing := make(chan struct{})
	release := make(chan struct{})
	var providerClosed atomic.Bool
	provider := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "provider", state: StateActive}}
	consumer := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "consumer", state: StateActive}}
	provider.close = func(context.Context) error { providerClosed.Store(true); return nil }
	consumer.close = func(context.Context) error {
		close(flushing)
		<-release
		if providerClosed.Load() {
			return errors.New("storage closed before flush")
		}
		return nil
	}
	host := New(consumer, provider)
	host.SetPackageRequirements(map[string][]string{"consumer": {"provider"}})
	first := make(chan error, 1)
	go func() { first <- host.Close(context.Background()) }()
	<-flushing
	second := make(chan error, 1)
	go func() { second <- host.Close(context.Background()) }()
	// An exclusion check needs a bounded observation window; cleanup itself is
	// coordinated by channels and cannot finish until release is closed.
	returnedEarly := false
	select {
	case err := <-second:
		returnedEarly = true
		t.Errorf("concurrent shutdown returned before flush: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if !returnedEarly {
		if err := <-second; err != nil {
			t.Fatal(err)
		}
	}
	if !providerClosed.Load() {
		t.Fatal("shutdown did not release provider")
	}
}

func TestFailedPreparationCannotActivateOnRetry(t *testing.T) {
	client := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "provider", state: StateInitialized}}
	client.prepare = func(context.Context) error { return errors.New("invalid persisted state") }
	host := New(client)
	defer host.Close(context.Background())
	if err := host.Prepare(context.Background()); err == nil {
		t.Fatal("corrupt state passed preparation")
	}
	if err := host.Activate(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid persisted state") {
		t.Fatalf("activation forgot preparation failure: %v", err)
	}
}

func TestOptionalPreparationFailureKeepsIndependentClientsUsable(t *testing.T) {
	for _, failure := range []string{"error", "panic", "timeout"} {
		t.Run(failure, func(t *testing.T) {
			provider := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "provider", state: StatePrepared, provided: []ServiceDescriptor{searchService("1.0.0")}}}
			consumer := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "consumer", state: StatePrepared, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}}
			leaf := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "leaf", state: StatePrepared}}
			independent := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "independent", state: StatePrepared, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1}}}}
			provider.prepare = func(context.Context) error {
				switch failure {
				case "panic":
					panic("preparation exploded")
				case "timeout":
					return context.DeadlineExceeded
				default:
					return errors.New("preparation refused")
				}
			}
			var closeOrder []string
			for _, client := range []*dependencyClient{provider, consumer, leaf} {
				client.close = func(context.Context) error { closeOrder = append(closeOrder, client.id); return nil }
			}
			host := New(leaf, consumer, provider, independent)
			host.SetPackageRequirements(map[string][]string{"leaf": {"consumer"}})
			registry, conflicts := BuildServiceRegistry(provider, consumer, independent)
			host.AttachServiceRegistry(registry, conflicts)
			defer host.Close(context.Background())
			if err := host.PrepareAvailable(context.Background(), map[string]bool{"independent": true}); err != nil {
				t.Fatalf("optional failure blocked preparation: %v", err)
			}
			if err := host.Activate(context.Background()); err != nil {
				t.Fatalf("quarantined client blocked activation: %v", err)
			}
			if independent.state != StateActive || registry.HasProvider("search.provider", 1) {
				t.Fatalf("failed provider remained available or independent client was blocked: %s", independent.state)
			}
			if strings.Join(closeOrder, ",") != "leaf,consumer,provider" {
				t.Fatalf("dependent cleanup order = %v", closeOrder)
			}
			failed := 0
			for _, status := range host.Statuses() {
				if status.State == StateFailed && status.Error != "" {
					failed++
				}
			}
			if failed != 3 {
				t.Fatalf("missing attributable quarantine diagnostics: %+v", host.Statuses())
			}
		})
	}
}

func TestAvailablePreparationDoesNotBypassRequiredClients(t *testing.T) {
	for _, required := range []string{"provider", "consumer"} {
		t.Run(required, func(t *testing.T) {
			provider := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "provider", state: StatePrepared, provided: []ServiceDescriptor{searchService("1.0.0")}}}
			consumer := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "consumer", state: StatePrepared, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}}
			provider.prepare = func(context.Context) error { return errors.New("invalid persisted state") }
			host := New(consumer, provider)
			registry, conflicts := BuildServiceRegistry(provider, consumer)
			host.AttachServiceRegistry(registry, conflicts)
			defer host.Close(context.Background())
			if err := host.PrepareAvailable(context.Background(), map[string]bool{required: true}); err == nil {
				t.Fatal("required selection survived failed preparation")
			}
			if err := host.Activate(context.Background()); err == nil {
				t.Fatal("activation forgot required preparation failure")
			}
		})
	}
}

func TestOptionalPreparationRetainsCleanupDiagnostics(t *testing.T) {
	client := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "optional", state: StatePrepared}}
	client.prepare = func(context.Context) error { return errors.New("preparation refused") }
	client.close = func(context.Context) error { return errors.New("cleanup refused") }
	host := New(client)
	defer host.Close(context.Background())
	if err := host.PrepareAvailable(context.Background(), map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	statuses := host.Statuses()
	if len(statuses) != 1 || !strings.Contains(statuses[0].Error, "preparation refused") || !strings.Contains(statuses[0].Error, "cleanup refused") {
		t.Fatalf("preparation or cleanup diagnostic lost: %+v", statuses)
	}
}

func TestOptionalPreparationCleanupKeepsShutdownBarrier(t *testing.T) {
	cleaning, release := make(chan struct{}), make(chan struct{})
	var closed atomic.Int32
	client := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "optional", state: StatePrepared}}
	client.prepare = func(context.Context) error { return errors.New("preparation refused") }
	client.close = func(context.Context) error {
		closed.Add(1)
		close(cleaning)
		<-release
		return nil
	}
	host := New(client)
	prepared := make(chan error, 1)
	go func() { prepared <- host.PrepareAvailable(context.Background(), map[string]bool{}) }()
	<-cleaning
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := host.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("shutdown waiter did not report pending cleanup: %v", err)
	}
	select {
	case <-host.ShutdownDone():
		t.Error("shutdown released resources before optional cleanup drained")
	default:
	}
	close(release)
	if err := <-prepared; err != nil {
		t.Fatal(err)
	}
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if closed.Load() != 1 {
		t.Fatalf("optional runtime was closed %d times", closed.Load())
	}
}

func TestAvailablePreparationDoesNotTreatCallerCancellationAsPluginFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "optional", state: StatePrepared}}
	client.prepare = func(context.Context) error { cancel(); return ctx.Err() }
	host := New(client)
	defer host.Close(context.Background())
	if err := host.PrepareAvailable(ctx, map[string]bool{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preparation published a degraded candidate: %v", err)
	}
}

func TestHostActivationCannotReopenClosedGeneration(t *testing.T) {
	var activated int
	client := &dependencyClient{fakeServiceProcess: &fakeServiceProcess{id: "provider", state: StatePrepared}}
	client.activate = func(context.Context) error { activated++; return nil }
	host := New(client)
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := host.Activate(context.Background()); err == nil {
		t.Fatal("closed generation accepted activation")
	}
	if activated != 0 {
		t.Fatal("closed generation ran activation effects")
	}
}
