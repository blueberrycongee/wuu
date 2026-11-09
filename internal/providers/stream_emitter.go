package providers

import "context"

// StreamEmitter delivers events from a provider stream producer to its output
// channel without ever blocking past request cancellation.
//
// This is part of the StreamClient producer contract: the channel returned by
// StreamChat has a bounded buffer and the consumer may stop reading at any
// point (user cancellation, replay-guard rejection, journal failure). A bare
// `ch <- ev` on a full buffer then blocks forever, which strands the producer
// goroutine and — because lease release rides on the producer's defers — leaks
// the provider-scope admission slot. Every producer send must therefore also
// select on the request context, which the reliable layer cancels whenever it
// abandons an attempt.
//
// Send reports false once the context is done. The producer should stop
// promptly: return, run its defers (release the lease, close the channel), and
// write nothing further.
type StreamEmitter struct {
	ctx context.Context
	ch  chan<- StreamEvent
}

// NewStreamEmitter binds a producer output channel to its request context.
func NewStreamEmitter(ctx context.Context, ch chan<- StreamEvent) *StreamEmitter {
	return &StreamEmitter{ctx: ctx, ch: ch}
}

// Send delivers one event, or reports false when the request context ended
// before the consumer accepted it.
func (e *StreamEmitter) Send(ev StreamEvent) bool {
	select {
	case e.ch <- ev:
		return true
	case <-e.ctx.Done():
		return false
	}
}

// Aborted reports whether the request context has ended. Producers can check
// it at loop boundaries to stop early without attempting another send.
func (e *StreamEmitter) Aborted() bool {
	return e.ctx.Err() != nil
}

// drainStream discards the abandoned channel without accounting events again.
// Register the observer before its runner ends; ordinary model cancellation does
// not end observation, but the owner's shared observation window can abort it.
func drainStream(ctx context.Context, ch <-chan StreamEvent) error {
	if ch == nil {
		return nil
	}
	ctx, endReceipt, err := BeginModelReceipt(ctx)
	if err != nil {
		return err
	}
	var observationCtx context.Context
	var observationDone <-chan struct{}
	if scope := ModelReceiptScopeFromContext(ctx); scope != nil {
		observationCtx = scope.ObservationContext()
		observationDone = observationCtx.Done()
	}
	go func() {
		var completionErr error
		defer func() { endReceipt(completionErr) }()
		for {
			select {
			case _, open := <-ch:
				if !open {
					return
				}
			case <-observationDone:
				completionErr = context.Cause(observationCtx)
				return
			}
		}
	}()
	return nil
}
