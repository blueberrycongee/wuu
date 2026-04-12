package coordinator

import (
	"context"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// ErrorClass tags a worker failure so the orchestrator (and the user)
// can decide whether to retry the same worker, retry with a tweak, or
// abandon the task. The classifier is best-effort: it inspects the
// error string and any wrapped HTTPError, and falls back to "fatal"
// when nothing matches a known transient pattern.
type ErrorClass string

const (
	// ErrorClassRetryable means the failure looks transient: rate
	// limit, server error, network blip. Re-spawning the same worker
	// has a real chance of succeeding.
	ErrorClassRetryable ErrorClass = "retryable"
	// ErrorClassAuth means the credentials were rejected. Retrying
	// won't help — the user has to fix their config.
	ErrorClassAuth ErrorClass = "auth"
	// ErrorClassContextOverflow means the worker's prompt + history
	// exceeded the model's context window even after a compact pass.
	// Re-spawning won't help; the orchestrator should split the task.
	ErrorClassContextOverflow ErrorClass = "context_overflow"
	// ErrorClassCancelled means the worker was stopped on purpose
	// (Ctrl+C, stop_agent). Not really a "failure" — surface for UX.
	ErrorClassCancelled ErrorClass = "cancelled"
	// ErrorClassFatal is the default: an unknown / non-recoverable
	// error. The orchestrator should report it and stop, not retry.
	ErrorClassFatal ErrorClass = "fatal"
)

// ClassifyError inspects err and reports its class. Safe to call with
// nil (returns ErrorClassFatal so callers don't have to special-case).
func ClassifyError(err error) ErrorClass {
	if err == nil {
		return ErrorClassFatal
	}
	if errors.Is(err, context.Canceled) {
		return ErrorClassCancelled
	}
	if providers.IsContextOverflow(err) {
		return ErrorClassContextOverflow
	}
	if providers.IsAuthError(err) {
		return ErrorClassAuth
	}
	// Anything providers.IsRetryable says yes to is, by definition,
	// transient — even if HTTP-level retry already exhausted its
	// own attempts, the orchestrator's "wait a bit then re-spawn"
	// has different timing and may still succeed.
	if providers.IsRetryable(err) {
		return ErrorClassRetryable
	}
	// Last resort: scan the error message for the strings
	// providers.IsRetryable looks for, in case the error has been
	// re-wrapped without preserving the typed HTTPError. This is
	// belt-and-suspenders; the typed check above is preferred.
	msg := strings.ToLower(err.Error())
	for _, hint := range []string{
		"rate limit", "rate-limit", "too many requests",
		"503", "502", "500", "504",
		"connection refused", "connection reset", "timeout", "deadline",
	} {
		if strings.Contains(msg, hint) {
			return ErrorClassRetryable
		}
	}
	return ErrorClassFatal
}
