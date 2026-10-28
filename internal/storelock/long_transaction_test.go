package storelock

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// Admission verifies every persisted record while retaining the writer fence.
// A healthy large-store traversal may exceed the ordinary transaction timeout;
// waiting must retain exclusivity, honor cancellation, and resume on release.
func TestLongTransactionWaitsForOwnerBeyondOrdinaryTimeout(t *testing.T) {
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		owner, err := Acquire(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Release()
		result := make(chan error, 1)
		go func() {
			next, err := AcquireUntilCanceled(context.Background(), dir)
			if next != nil {
				err = errors.Join(err, next.Release())
			}
			result <- err
		}()
		time.Sleep(2 * acquireTimeout)
		select {
		case err := <-result:
			t.Fatalf("waiter abandoned or stole a live owner's lock: %v", err)
		default:
		}
		if err := owner.Release(); err != nil {
			t.Fatal(err)
		}
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	})
}

func TestLongTransactionWaitHonorsCancellation(t *testing.T) {
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		owner, err := Acquire(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Release()
		ctx, cancel := context.WithTimeout(context.Background(), 2*acquireTimeout)
		defer cancel()
		if _, err := AcquireUntilCanceled(ctx, dir); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected caller deadline, got %v", err)
		}
		if next, acquired, err := TryAcquire(dir); next != nil || acquired || err != nil {
			t.Fatalf("canceled waiter changed ownership: %v %v", acquired, err)
		}
	})
}
