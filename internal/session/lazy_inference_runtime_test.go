package session

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/providers"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLazyInferenceRuntimeInitializesOnceAndReportsProviderFailure(t *testing.T) {
	var calls atomic.Int32
	providerErr := errors.New("selected provider has no journal")
	failed := NewLazyInferenceJournalRuntime(func(context.Context) (*InferenceJournalRuntime, error) { calls.Add(1); return nil, providerErr })
	owner := failed.ForOwner("owner")
	if calls.Load() != 0 || failed.RuntimeID() != "" {
		t.Fatal("metadata access initialized journal")
	}
	if err := owner.PrepareOperation(providers.InferenceOperationJournalRecord{}); !errors.Is(err, providerErr) {
		t.Fatalf("provider failure lost: %v", err)
	}
	if err := failed.Close(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("close retried failed initialization")
	}
	var initialized atomic.Int32
	client := NewClient(context.Background(), func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"values":[{"handle":"h","runtime_id":"r"}]}`), nil
	})
	lazy := NewLazyInferenceJournalRuntime(func(ctx context.Context) (*InferenceJournalRuntime, error) {
		initialized.Add(1)
		return client.WithContext(ctx).NewInferenceJournalRuntime("scope")
	})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := lazy.Ensure(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if initialized.Load() != 1 || lazy.RuntimeID() != "r" {
		t.Fatalf("initialization count=%d id=%q", initialized.Load(), lazy.RuntimeID())
	}
}

func TestLazyInferenceRuntimeCloseWaitsForInitializationAndPreservesCleanup(t *testing.T) {
	entered, resume := make(chan context.Context, 1), make(chan struct{})
	var closes atomic.Int32
	type cleanupKey struct{}
	client := NewClient(context.Background(), func(ctx context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		if method == "NewInferenceJournalRuntime" {
			return json.RawMessage(`{"values":[{"handle":"h","runtime_id":"r"}]}`), nil
		}
		if method != "Journal.Close" || ctx.Value(cleanupKey{}) != true {
			return nil, errors.New("cleanup authority lost")
		}
		closes.Add(1)
		return json.RawMessage(`{"values":[]}`), nil
	})
	lazy := NewLazyInferenceJournalRuntime(func(ctx context.Context) (*InferenceJournalRuntime, error) {
		entered <- ctx
		<-resume
		return client.NewInferenceJournalRuntime("scope")
	})
	initDone := make(chan error, 1)
	go func() { initDone <- lazy.Ensure(context.Background()) }()
	initCtx := <-entered
	closeDone := make(chan error, 1)
	go func() { closeDone <- lazy.CloseContext(context.WithValue(context.Background(), cleanupKey{}, true)) }()
	<-initCtx.Done()
	select {
	case err := <-closeDone:
		t.Fatalf("close returned before admitted init: %v", err)
	default:
	}
	close(resume)
	if err := <-initDone; err == nil {
		t.Fatal("closed initialization admitted use")
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if err := lazy.Close(); err != nil || closes.Load() != 1 {
		t.Fatalf("close duplicated: %v count=%d", err, closes.Load())
	}
	if err := lazy.Ensure(context.Background()); err == nil {
		t.Fatal("closed lazy runtime reopened")
	}
}

func TestUnusedLazyInferenceRuntimeClosesWithoutProvider(t *testing.T) {
	lazy := NewLazyInferenceJournalRuntime(func(context.Context) (*InferenceJournalRuntime, error) {
		t.Error("unused journal initialized")
		return nil, errors.New("unexpected")
	})
	if err := lazy.Close(); err != nil {
		t.Fatal(err)
	}
}
