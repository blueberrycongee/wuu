package providers

import (
	"context"
	"errors"
	"testing"
)

func TestModelReceiptScopeRetainsAdmittedTailAfterSeal(t *testing.T) {
	ctx, scope := NewModelReceiptScope(context.Background())
	producerCtx, endProducer, err := BeginModelReceipt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ModelReceiptScopeFromContext(producerCtx) != scope {
		t.Fatal("producer lost the owning receipt scope")
	}
	endProducer(nil)
	select {
	case <-scope.Done():
		t.Fatal("producer completion ended the still-open logical scope")
	default:
	}
	producerCtx, endProducer, err = BeginModelReceipt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope.Seal()
	scope.Seal()
	if _, _, err := BeginModelReceipt(ctx); !errors.Is(err, ErrModelReceiptScopeClosed) {
		t.Fatalf("sealed root admitted new work: %v", err)
	}
	// An admitted producer can hand off receipt observation after logical
	// cancellation, before releasing its own ticket.
	canceledCtx, cancel := context.WithCancel(producerCtx)
	cancel()
	tailCtx, endTail, err := BeginModelReceipt(canceledCtx)
	if err != nil {
		t.Fatal(err)
	}
	if tailCtx.Err() != context.Canceled {
		t.Fatalf("tail handoff changed cancellation: %v", tailCtx.Err())
	}
	endProducer(nil)
	endProducer(nil)
	if _, _, err := BeginModelReceipt(producerCtx); !errors.Is(err, ErrModelReceiptScopeClosed) {
		t.Fatalf("ended producer admitted new work: %v", err)
	}
	select {
	case <-scope.Done():
		t.Fatal("parent completion skipped the outstanding receipt tail")
	default:
	}
	endTail(nil)
	if err := scope.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := BeginModelReceipt(tailCtx); !errors.Is(err, ErrModelReceiptScopeClosed) {
		t.Fatalf("completed tail reopened the scope: %v", err)
	}
}

func TestModelReceiptScopeTimeoutDoesNotCompleteProducer(t *testing.T) {
	ctx, scope := NewModelReceiptScope(context.Background())
	_, end, err := BeginModelReceipt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope.Seal()
	deadlineCtx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	if err := scope.Wait(deadlineCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unfinished producer wait = %v", err)
	}
	select {
	case <-scope.Done():
		t.Fatal("wait deadline falsely completed the producer")
	default:
	}
	transportErr := errors.New("original RPC transport failed")
	end(transportErr)
	end(errors.New("duplicate completion"))
	if err := scope.Wait(context.Background()); !errors.Is(err, transportErr) {
		t.Fatalf("completion lost transport failure: %v", err)
	}
}

func TestModelReceiptWithoutScopePreservesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	child, end, err := BeginModelReceipt(ctx)
	if err != nil || child != ctx || ModelReceiptScopeFromContext(child) != nil {
		t.Fatalf("unscoped call acquired a receipt owner: child=%v err=%v", child, err)
	}
	end(nil)
	end(nil)
}

func TestModelReceiptScopeAbortStopsObservationWithoutCompletingNativeProducer(t *testing.T) {
	modelCtx, cancelModel := context.WithCancel(context.Background())
	ctx, scope := NewModelReceiptScope(modelCtx)
	producerCtx, endNative, err := BeginModelReceipt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, endRPC, err := BeginModelReceipt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope.Seal()
	cancelModel()
	if err := scope.ObservationContext().Err(); err != nil {
		t.Fatalf("model cancellation stopped original-RPC observation: %v", err)
	}
	ownerErr := errors.New("owner receipt deadline expired")
	scope.Abort(ownerErr)
	scope.Abort(errors.New("later abort"))
	if cause := context.Cause(scope.ObservationContext()); cause != ownerErr {
		t.Fatalf("observation cause = %v, want original %v", cause, ownerErr)
	}
	if _, _, err := BeginModelReceipt(producerCtx); !errors.Is(err, ErrModelReceiptScopeClosed) {
		t.Fatalf("aborted observation window admitted new work: %v", err)
	}
	endRPC(context.Cause(scope.ObservationContext()))
	if err := scope.Wait(context.Background()); !errors.Is(err, ownerErr) {
		t.Fatalf("aborted wait = %v", err)
	}
	select {
	case <-scope.Done():
		t.Fatal("ending RPC observation falsely completed the native producer")
	default:
	}
	endNative(nil)
	<-scope.Done()
	if err := scope.Wait(context.Background()); !errors.Is(err, ownerErr) {
		t.Fatalf("eventual producer completion erased the incomplete observation: %v", err)
	}
}
