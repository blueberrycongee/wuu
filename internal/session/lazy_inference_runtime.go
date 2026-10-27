package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// NewLazyInferenceJournalRuntime defers provider resource creation until a
// required journal operation or Ensure. It does not synthesize journal success.
func NewLazyInferenceJournalRuntime(initialize func(context.Context) (*InferenceJournalRuntime, error)) *InferenceJournalRuntime {
	return &InferenceJournalRuntime{lazy: &lazyInferenceRuntime{initialize: initialize}}
}

type journalInitialization struct {
	done    chan struct{}
	cancel  context.CancelFunc
	runtime *InferenceJournalRuntime
	err     error
}
type lazyInferenceRuntime struct {
	mu         sync.Mutex
	initialize func(context.Context) (*InferenceJournalRuntime, error)
	pending    *journalInitialization
	runtime    *InferenceJournalRuntime
	closed     bool
	closeOnce  sync.Once
	closeErr   error
}

var errJournalClosed = errors.New("inference journal runtime is closed")

// Ensure establishes the selected provider's real runtime identity. Callers
// needing RuntimeID before any journal write must first check this result.
func (r *InferenceJournalRuntime) Ensure(ctx context.Context) error {
	if r == nil {
		return errors.New("inference journal runtime is not initialized")
	}
	if r.lazy != nil {
		_, err := r.lazy.resolve(ctx)
		return err
	}
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.closing {
		return errJournalClosed
	}
	return ctx.Err()
}

func (l *lazyInferenceRuntime) resolve(ctx context.Context) (*InferenceJournalRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, errJournalClosed
	}
	if l.runtime != nil {
		runtime := l.runtime
		l.mu.Unlock()
		return runtime, nil
	}
	attempt := l.pending
	if attempt == nil {
		initCtx, cancel := context.WithCancel(ctx)
		attempt = &journalInitialization{done: make(chan struct{}), cancel: cancel}
		l.pending = attempt
		l.mu.Unlock()
		runtime, err := l.initialize(initCtx)
		cancel()
		if err == nil && runtime == nil {
			err = errors.New("inference journal initializer returned no runtime")
		}
		l.mu.Lock()
		attempt.runtime, attempt.err = runtime, err
		if err == nil {
			l.runtime = runtime
		}
		l.pending = nil
		close(attempt.done)
		closed := l.closed
		l.mu.Unlock()
		if closed {
			return nil, errJournalClosed
		}
		return runtime, err
	}
	l.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-attempt.done:
	}
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed {
		return nil, errJournalClosed
	}
	return attempt.runtime, attempt.err
}
func (l *lazyInferenceRuntime) close(ctx context.Context) error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		attempt := l.pending
		if attempt != nil {
			attempt.cancel()
		}
		l.mu.Unlock()
		if attempt != nil {
			<-attempt.done
		}
		l.mu.Lock()
		runtime := l.runtime
		l.mu.Unlock()
		if runtime != nil {
			l.closeErr = runtime.CloseContext(context.WithoutCancel(ctx))
		}
	})
	return l.closeErr
}
func (l *lazyInferenceRuntime) runtimeID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.runtime == nil {
		return ""
	}
	return l.runtime.RuntimeID()
}

type lazyInferenceJournal struct {
	runtime *lazyInferenceRuntime
	owner   string
	ctx     context.Context
}

// WithContext binds initialization and writes to this operation only.
func (j *lazyInferenceJournal) WithContext(ctx context.Context) providers.InferenceJournal {
	clone := *j
	clone.ctx = context.WithoutCancel(ctx)
	return &clone
}

func (j *lazyInferenceJournal) withJournal(ctx context.Context, call func(providers.InferenceJournal) error) error {
	ctx = journalCallContext(ctx, j.ctx)
	runtime, err := j.runtime.resolve(ctx)
	if err != nil {
		return err
	}
	return call(providers.InferenceJournalFromContext(providers.WithInferenceJournal(ctx, runtime.ForOwner(j.owner))))
}

func (j *lazyInferenceJournal) PrepareOperation(record providers.InferenceOperationJournalRecord) error {
	return j.withJournal(j.ctx, func(journal providers.InferenceJournal) error { return journal.PrepareOperation(record) })
}

func (j *lazyInferenceJournal) PrepareAttempt(record providers.InferenceAttemptJournalRecord) error {
	return j.withJournal(j.ctx, func(journal providers.InferenceJournal) error { return journal.PrepareAttempt(record) })
}

func (j *lazyInferenceJournal) UpsertSubmission(ctx context.Context, record providers.InferenceSubmissionJournalRecord) error {
	return j.withJournal(ctx, func(journal providers.InferenceJournal) error { return journal.UpsertSubmission(ctx, record) })
}

func (j *lazyInferenceJournal) MarkAttemptFirstEvent(operationID, attemptID, submissionID string, at time.Time) error {
	return j.withJournal(j.ctx, func(journal providers.InferenceJournal) error {
		return journal.MarkAttemptFirstEvent(operationID, attemptID, submissionID, at)
	})
}

func (j *lazyInferenceJournal) PrepareRecoveryAttempt(ctx context.Context, record providers.InferenceRecoveryAttemptJournalRecord) error {
	return j.withJournal(ctx, func(journal providers.InferenceJournal) error { return journal.PrepareRecoveryAttempt(ctx, record) })
}

func (j *lazyInferenceJournal) CompleteAttempt(record providers.InferenceAttemptTerminalRecord) error {
	return j.withJournal(j.ctx, func(journal providers.InferenceJournal) error { return journal.CompleteAttempt(record) })
}

func (j *lazyInferenceJournal) CompleteOperation(record providers.InferenceOperationTerminalRecord) error {
	return j.withJournal(j.ctx, func(journal providers.InferenceJournal) error { return journal.CompleteOperation(record) })
}

func (j *lazyInferenceJournal) CompleteWorkflow(ctx context.Context, record providers.InferenceWorkflowTerminalRecord) error {
	return j.withJournal(ctx, func(journal providers.InferenceJournal) error { return journal.CompleteWorkflow(ctx, record) })
}
