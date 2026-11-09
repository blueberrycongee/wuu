package providers

import (
	"errors"
	"sync"
	"time"
)

// RemoteSubmissionUpdate carries physical evidence, never operation authority,
// request bodies or raw provider errors. The host retains the durable attempt.
type RemoteSubmissionUpdate struct {
	RetryAfter time.Duration              `json:"retry_after,omitempty"`
	ID         string                     `json:"id"`
	Output     string                     `json:"output,omitempty"`
	Usage      *TokenUsage                `json:"usage,omitempty"`
	FromStream bool                       `json:"from_stream,omitempty"`
	Estimated  bool                       `json:"estimated,omitempty"`
	Outcome    InferenceSubmissionOutcome `json:"outcome,omitempty"`
	Failure    InferenceJournalFailure    `json:"failure,omitempty"`
}

type remoteAccounting struct {
	mu        sync.Mutex
	operation InferenceOperation
	call      func(string, any, any) error
	err       error
}

// NewRemoteInferenceAttempt continues an admitted host attempt through a
// receipt transport. Only physical accounting is delegated; callers cannot
// create retries or terminalize the owning logical operation. The transport
// must authenticate an execution-scoped receipt and commit before acknowledging
// create. A failed/uncertain call seals this proxy rather than retrying it.
func NewRemoteInferenceAttempt(operation InferenceOperation, id string, ordinal int, call func(string, any, any) error) InferenceAttempt {
	return InferenceAttempt{ID: id, Ordinal: ordinal, remote: &remoteAccounting{operation: operation, call: call}}
}
func (r *remoteAccounting) invoke(method string, input, output any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	if r.call == nil {
		r.err = errors.New("model receipt transport is unavailable")
		return r.err
	}
	r.err = r.call(method, input, output)
	return r.err
}
func (r *remoteAccounting) journalError() error { r.mu.Lock(); defer r.mu.Unlock(); return r.err }
func (r *remoteAccounting) observe(update RemoteSubmissionUpdate) {
	_ = r.invoke("observe", update, nil)
}

// ApplyRemoteSubmissionUpdate preserves physical-receipt precedence over
// delayed logical stream observations on both local and remote transports.
func (s InferenceSubmission) ApplyRemoteSubmissionUpdate(update RemoteSubmissionUpdate, settle bool) error {
	if !settle {
		s.ObserveOutput(update.Output)
		if update.Estimated {
			s.ObserveEstimatedUsage(update.Usage)
		} else {
			s.observeUsage(update.Usage, update.FromStream)
		}
		return nil
	}
	failure := NormalizedFailure{RetryAfter: update.RetryAfter, Origin: update.Failure.Origin, Category: update.Failure.Category, ProviderFamily: update.Failure.ProviderFamily, ProviderCode: update.Failure.ProviderCode, HTTPStatus: update.Failure.HTTPStatus, ClassificationConfidence: update.Failure.Confidence}
	if update.Failure.Message != "" {
		failure.Cause = errors.New(update.Failure.Message)
	}
	switch update.Outcome {
	case InferenceSubmissionSucceeded, InferenceSubmissionFailed, InferenceSubmissionFallback, InferenceSubmissionAbandoned:
	default:
		return errors.New("invalid physical submission outcome")
	}
	s.complete(update.Outcome, failure, update.Usage, update.FromStream)
	return nil
}
