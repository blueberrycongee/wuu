package toolservice

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type blockingDefinitions struct{ entered, release chan struct{} }

func (b *blockingDefinitions) Definitions() []providers.ToolDefinition {
	close(b.entered)
	<-b.release
	return nil
}
func (*blockingDefinitions) Execute(context.Context, providers.ToolCall) (string, error) {
	return "", nil
}
func invokeList(s *Service, scope *Scope) error {
	_, err := s.InvokeService(WithScope(context.Background(), scope), pluginhost.ServiceInvokeParams{Service: ServiceName, Method: "list", Params: json.RawMessage(`{}`)})
	return err
}
func waitSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s timed out", label)
	}
}

func TestScopeCloseWaitsForAdmittedInvocationAndIsolatesOthers(t *testing.T) {
	s := NewService()
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	clean := make(chan struct{})
	var count atomic.Int32
	scope := NewScope(func(context.Context) (Backend, error) {
		return Backend{Executor: &blockingDefinitions{entered, release}, Close: func(context.Context) error { count.Add(1); close(clean); return nil }}, nil
	})
	called := make(chan error, 1)
	go func() { called <- invokeList(s, scope) }()
	waitSignal(t, entered, "invocation")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scope.Close(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("close=%v", err)
	}
	if err := invokeList(s, scope); err == nil {
		t.Fatal("closed scope admitted invocation")
	}
	other := NewScope(func(context.Context) (Backend, error) { return Backend{Executor: &shutdownProbe{}}, nil })
	if err := invokeList(s, other); err != nil {
		t.Fatalf("other scope: %v", err)
	}
	select {
	case <-clean:
		t.Fatal("cleanup ran while invocation was active")
	default:
	}
	done := make(chan error, 1)
	go func() { done <- scope.Close(context.Background()) }()
	close(release)
	if err := <-called; err != nil {
		t.Fatalf("admitted call=%v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitSignal(t, clean, "cleanup")
	if count.Load() != 1 {
		t.Fatalf("cleanup %d", count.Load())
	}
}

func TestScopeAndProviderCloseJoinLateInitializationExactlyOnce(t *testing.T) {
	s := NewService()
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var built, closed atomic.Int32
	scope := NewScope(func(context.Context) (Backend, error) {
		built.Add(1)
		close(entered)
		<-release
		return Backend{Executor: &shutdownProbe{}, Close: func(ctx context.Context) error {
			if ctx.Err() != nil {
				t.Error("cleanup context canceled")
			}
			closed.Add(1)
			return nil
		}}, nil
	})
	called := make(chan error, 1)
	go func() { called <- invokeList(s, scope) }()
	waitSignal(t, entered, "factory")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scope.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("scope close=%v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); errs <- scope.Close(context.Background()) }()
		go func() { defer wg.Done(); errs <- s.Close(context.Background()) }()
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := <-called; err == nil {
		t.Fatal("initialized after close dispatched")
	}
	if built.Load() != 1 || closed.Load() != 1 {
		t.Fatalf("factory=%d cleanup=%d", built.Load(), closed.Load())
	}
	if err := invokeList(s, scope); err == nil {
		t.Fatal("closed scope restarted")
	}
}

func TestScopeCloseBeforeFirstUseNeverBuildsBackend(t *testing.T) {
	s := NewService()
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var built atomic.Int32
	scope := NewScope(func(context.Context) (Backend, error) { built.Add(1); return Backend{Executor: &shutdownProbe{}}, nil })
	if err := scope.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := invokeList(s, scope); err == nil {
		t.Fatal("closed scope accepted work")
	}
	if built.Load() != 0 {
		t.Fatal("factory started")
	}
}

func TestScopeCloseRetainsCleanupErrorsAndCleansFailedFactory(t *testing.T) {
	s := NewService()
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var closed atomic.Int32
	cleaned := make(chan struct{})
	factoryErr := errors.New("initialization failed")
	cleanupErr := errors.New("cleanup failed")
	scope := NewScope(func(context.Context) (Backend, error) {
		return Backend{Close: func(context.Context) error { closed.Add(1); close(cleaned); return cleanupErr }}, factoryErr
	})
	if err := invokeList(s, scope); !errors.Is(err, factoryErr) {
		t.Fatalf("factory error=%v", err)
	}
	if err := invokeList(s, scope); !errors.Is(err, factoryErr) {
		t.Fatalf("closed scope lost factory error=%v", err)
	}
	waitSignal(t, cleaned, "failed factory cleanup")
	if err := scope.Close(context.Background()); !errors.Is(err, cleanupErr) {
		t.Fatalf("first close=%v", err)
	}
	if err := scope.Close(context.Background()); !errors.Is(err, cleanupErr) {
		t.Fatalf("repeated close=%v", err)
	}
	if closed.Load() != 1 {
		t.Fatalf("cleanup=%d", closed.Load())
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("scope already retired; graph close=%v", err)
	}
}

func TestFailedScopeReportsFactoryErrorToConcurrentAndLaterCalls(t *testing.T) {
	s := NewService()
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	var built, cleaned atomic.Int32
	factoryErr := errors.New("native backend unavailable")
	partial := &shutdownProbe{}
	scope := NewScope(func(context.Context) (Backend, error) {
		built.Add(1)
		close(entered)
		<-release
		return Backend{Executor: partial, Close: func(context.Context) error { cleaned.Add(1); return nil }}, factoryErr
	})
	const callers = 24
	errs := make(chan error, callers)
	go func() { errs <- invokeList(s, scope) }()
	waitSignal(t, entered, "factory")
	for i := 1; i < callers; i++ {
		go func() { errs <- invokeList(s, scope) }()
	}
	close(release)
	for i := 0; i < callers; i++ {
		if err := <-errs; !errors.Is(err, factoryErr) {
			t.Errorf("call %d lost factory error: %v", i, err)
		}
	}
	if err := scope.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if built.Load() != 1 || cleaned.Load() != 1 {
		t.Fatalf("factory=%d cleanup=%d", built.Load(), cleaned.Load())
	}
	if partial.calls.Load() != 0 {
		t.Fatal("partially initialized executor was invoked")
	}
	if err := invokeList(s, scope); !errors.Is(err, factoryErr) {
		t.Fatalf("later call lost factory error: %v", err)
	}
}

func TestExplicitScopeCloseWinsOverLateFactoryFailure(t *testing.T) {
	s := NewService()
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	factoryErr := errors.New("late initialization failure")
	scope := NewScope(func(context.Context) (Backend, error) {
		close(entered)
		<-release
		return Backend{}, factoryErr
	})
	result := make(chan error, 1)
	go func() { result <- invokeList(s, scope) }()
	waitSignal(t, entered, "factory")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scope.Close(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("close=%v", err)
	}
	close(release)
	if err := <-result; err == nil || errors.Is(err, factoryErr) {
		t.Fatalf("late factory failure took precedence: %v", err)
	}
	if err := scope.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := invokeList(s, scope); err == nil || errors.Is(err, factoryErr) {
		t.Fatalf("late factory failure retained: %v", err)
	}
}

func TestProviderCloseDrainsAllScopesConcurrently(t *testing.T) {
	s := NewService()
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	blocked, finish := make(chan struct{}), make(chan struct{})
	otherClosed := make(chan struct{})
	first := NewScope(func(context.Context) (Backend, error) {
		return Backend{Executor: &shutdownProbe{}, Close: func(context.Context) error { close(blocked); <-finish; return nil }}, nil
	})
	second := NewScope(func(context.Context) (Backend, error) {
		return Backend{Executor: &shutdownProbe{}, Close: func(context.Context) error { close(otherClosed); return nil }}, nil
	})
	if err := invokeList(s, first); err != nil {
		t.Fatal(err)
	}
	if err := invokeList(s, second); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Close(context.Background()) }()
	waitSignal(t, blocked, "blocking cleanup")
	waitSignal(t, otherClosed, "independent cleanup")
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestScopeCloseReportsEarlierProviderCleanupFailure(t *testing.T) {
	s := NewService()
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	cleanupErr := errors.New("backend cleanup failed")
	scope := NewScope(func(context.Context) (Backend, error) {
		return Backend{Executor: &shutdownProbe{}, Close: func(context.Context) error { return cleanupErr }}, nil
	})
	if err := invokeList(s, scope); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); !errors.Is(err, cleanupErr) {
		t.Fatalf("provider close=%v", err)
	}
	if err := scope.Close(context.Background()); !errors.Is(err, cleanupErr) {
		t.Fatalf("scope lost provider cleanup failure: %v", err)
	}
}
