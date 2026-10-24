package providers

import (
	"context"
	"errors"
	"sync"
)

var ErrModelReceiptScopeClosed = errors.New("model receipt parent has completed")

// ModelReceiptScope joins an owner's model producers and receipt observers.
// Its owner controls cancellation, deadlines and authority release.
type ModelReceiptScope struct {
	mu      sync.Mutex
	root    *modelReceiptTicket
	pending int
	done    chan struct{}
	err     error
	aborted bool

	executions []*InferenceExecution

	observationCtx  context.Context
	stopObservation context.CancelCauseFunc
}

type modelReceiptTicket struct {
	scope *ModelReceiptScope
	ended bool
}

type modelReceiptContextKey struct{}

// NewModelReceiptScope creates an invocation owner with a live root ticket.
// Nested continuations use the existing context instead of creating an owner.
func NewModelReceiptScope(ctx context.Context) (context.Context, *ModelReceiptScope) {
	scope := &ModelReceiptScope{pending: 1, done: make(chan struct{})}
	scope.observationCtx, scope.stopObservation = context.WithCancelCause(context.Background())
	scope.root = &modelReceiptTicket{scope: scope}
	return context.WithValue(ctx, modelReceiptContextKey{}, scope.root), scope
}

func ModelReceiptScopeFromContext(ctx context.Context) *ModelReceiptScope {
	if ticket, ok := ctx.Value(modelReceiptContextKey{}).(*modelReceiptTicket); ok {
		return ticket.scope
	}
	return nil
}

// BeginModelReceipt registers work before it starts and returns its child
// context and idempotent completion function. Complete only after all producer
// cleanup and accepted receipt observations have ended. Pass an error only for
// a completion failure, not an ordinary model rejection or caller cancellation.
// An admitted producer may register a tail before completing, even after Seal.
// Context cancellation and execution authority are never changed by registration.
// Calls without a scope retain their original context and completion behavior.
func BeginModelReceipt(ctx context.Context) (context.Context, func(error), error) {
	parent, ok := ctx.Value(modelReceiptContextKey{}).(*modelReceiptTicket)
	if !ok {
		return ctx, func(error) {}, nil
	}
	scope := parent.scope
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if parent.ended || scope.aborted {
		return ctx, nil, ErrModelReceiptScopeClosed
	}
	scope.pending++
	ticket := &modelReceiptTicket{scope: scope}
	return context.WithValue(ctx, modelReceiptContextKey{}, ticket), ticket.complete, nil
}

func (ticket *modelReceiptTicket) complete(err error) {
	scope := ticket.scope
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if ticket.ended {
		return
	}
	ticket.ended = true
	if err != nil {
		scope.err = errors.Join(scope.err, err)
	}
	scope.pending--
	if scope.pending == 0 {
		close(scope.done)
	}
}

// Seal ends logical admission through the root context. Existing producers
// retain their ability to hand off tails until their own completion.
func (s *ModelReceiptScope) Seal() { s.root.complete(nil) }

// ObservationContext only controls internal completion observation. It carries
// no execution authority and is independent of ordinary model cancellation.
func (s *ModelReceiptScope) ObservationContext() context.Context { return s.observationCtx }

// Abort ends the owner's observation window with an incomplete result. It
// rejects new work and wakes observers, but does not complete native producers
// or prove remote termination. The owner supplies the shared deadline cause.
func (s *ModelReceiptScope) Abort(cause error) {
	s.mu.Lock()
	if s.aborted || s.pending == 0 {
		s.mu.Unlock()
		return
	}
	if cause == nil {
		cause = context.Canceled
	}
	s.aborted = true
	s.err = errors.Join(s.err, cause)
	s.mu.Unlock()
	s.stopObservation(cause)
}

// Done closes after logical admission is sealed and every admitted producer
// and observer has completed. A wait timeout does not close it.
func (s *ModelReceiptScope) Done() <-chan struct{} { return s.done }

// Wait joins receipt completion using the owner's deadline. Cancellation stops
// this wait only; it does not prove physical termination or finish any ticket.
func (s *ModelReceiptScope) Wait(ctx context.Context) error {
	var waitErr error
	select {
	case <-s.done:
	default:
		select {
		case <-s.done:
		case <-s.observationCtx.Done():
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(s.err, waitErr)
}

// CheckpointSubmissions waits for receipt quiescence and persists each actual
// execution's physical receipts in registration order. The owner must Seal
// first and supply its shared cleanup context; this method owns no cancellation
// policy. Completed receipt errors remain in the result, while healthy
// executions still checkpoint within the remaining context budget. Unfinished
// or aborted observation never permits a final checkpoint.
func (s *ModelReceiptScope) CheckpointSubmissions(ctx context.Context) error {
	waitErr := s.Wait(ctx)
	s.mu.Lock()
	if s.pending != 0 || s.aborted || ctx.Err() != nil {
		s.mu.Unlock()
		return errors.Join(waitErr, ctx.Err())
	}
	executions := append([]*InferenceExecution(nil), s.executions...)
	s.mu.Unlock()
	checkpointErr := waitErr
	for _, execution := range executions {
		if err := ctx.Err(); err != nil {
			return errors.Join(checkpointErr, err)
		}
		checkpointErr = errors.Join(checkpointErr, execution.CheckpointSubmissions(ctx))
	}
	return errors.Join(checkpointErr, ctx.Err())
}
