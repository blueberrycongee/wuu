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
