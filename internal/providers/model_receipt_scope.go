package providers

import (
	"context"
	"errors"
	"sync"
)

var ErrModelReceiptScopeClosed = errors.New("model receipt parent has completed")

// ModelReceiptScope joins the producers and observers admitted by one model
// invocation. Its owner controls cancellation, deadlines and authority release.
type ModelReceiptScope struct {
	mu      sync.Mutex
	root    *modelReceiptTicket
	pending int
	done    chan struct{}
	err     error
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
	if parent.ended {
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
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(s.err, waitErr)
}
