package exec

import (
	"context"
	"io"
	"time"
)

// outputWriter serializes writes and bounds cancellation even when the consumer
// stops reading. io.Writer has no cancellation contract: one underlying Write
// may outlive Run, but after abandoning it we never submit another write. Its
// buffer is private, so the encoder can safely reuse its own memory.
type outputWriter struct {
	w        io.Writer
	ctx      context.Context
	cancel   context.CancelFunc
	err      error
	deadline time.Time
}

func (w *outputWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	type result struct {
		n   int
		err error
	}
	data := append([]byte(nil), p...)
	done := make(chan result, 1)
	go func() {
		n, err := w.w.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		done <- result{n, err}
	}()
	var written result
	select {
	case written = <-done:
	case <-w.ctx.Done():
		// Give writable output one shared grace period for terminal events. A
		// stalled consumer must not prevent Run interruption or process exit.
		if w.deadline.IsZero() {
			w.deadline = time.Now().Add(250 * time.Millisecond)
		}
		timer := time.NewTimer(time.Until(w.deadline))
		defer timer.Stop()
		select {
		case written = <-done:
		case <-timer.C:
			w.err = w.ctx.Err()
			return 0, w.err
		}
	}
	if written.err != nil {
		w.err = written.err
		w.cancel()
	}
	return written.n, written.err
}
