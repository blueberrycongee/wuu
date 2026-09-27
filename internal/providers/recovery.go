package providers

import (
	"context"
	"errors"
	"strings"
	"time"
)

type FailureOrigin string

const (
	FailureOriginCaller   FailureOrigin = "caller"
	FailureOriginLocal    FailureOrigin = "local"
	FailureOriginNetwork  FailureOrigin = "network"
	FailureOriginProvider FailureOrigin = "provider"
	FailureOriginProtocol FailureOrigin = "protocol"
)

type FailureCategory string

const (
	FailureCanceled          FailureCategory = "canceled"
	FailureDeadline          FailureCategory = "deadline"
	FailureAuthentication    FailureCategory = "authentication"
	FailureQuota             FailureCategory = "quota"
	FailureRateLimit         FailureCategory = "rate_limit"
	FailureOverloaded        FailureCategory = "overloaded"
	FailureServer            FailureCategory = "server"
	FailureNetwork           FailureCategory = "network"
	FailureIncompleteStream  FailureCategory = "incomplete_stream"
	FailureContextOverflow   FailureCategory = "context_overflow"
	FailureRequestTooLarge   FailureCategory = "request_too_large"
	FailureResponseTooLarge  FailureCategory = "response_too_large"
	FailureNotFound          FailureCategory = "not_found"
	FailureInvalidRequest    FailureCategory = "invalid_request"
	FailureLocalBackpressure FailureCategory = "local_backpressure"
	FailureReplayUnsafe      FailureCategory = "replay_unsafe"
	FailureBudgetExceeded    FailureCategory = "workflow_budget_exceeded"
	FailureCostIndeterminate FailureCategory = "workflow_cost_indeterminate"
	FailureUnknown           FailureCategory = "unknown"
)

type ClassificationConfidence string

const (
	ConfidenceHigh   ClassificationConfidence = "high"
	ConfidenceMedium ClassificationConfidence = "medium"
	ConfidenceLow    ClassificationConfidence = "low"
)

// NormalizedFailure retains the evidence required for a recovery decision.
// RawBody is in-memory only; durable journals must apply their redaction and
// size policy instead of serializing it directly.
type NormalizedFailure struct {
	Origin                   FailureOrigin
	Category                 FailureCategory
	ProviderFamily           string
	ProviderCode             string
	HTTPStatus               int
	RetryAfter               time.Duration
	ContextOverflow          bool
	AuthRefreshable          bool
	ClassificationConfidence ClassificationConfidence
	SuggestedRecovery        RecoveryActionKind
	RawBody                  string
	Cause                    error
}

type RecoveryActionKind string

const (
	RecoveryStop             RecoveryActionKind = "stop"
	RecoveryReplaySame       RecoveryActionKind = "replay_same_payload"
	RecoveryWaitThenReplay   RecoveryActionKind = "wait_then_replay"
	RecoveryTransformPayload RecoveryActionKind = "transform_payload"
	RecoveryRefreshAuth      RecoveryActionKind = "refresh_credential"
	RecoverySwitchTransport  RecoveryActionKind = "switch_transport"
	RecoveryBlockUnsafe      RecoveryActionKind = "block_unsafe_replay"
	// RecoveryRescheduleSafe is a crash-recovery marker for an attempt that
	// was durably prepared but never entered the transport boundary.
	RecoveryRescheduleSafe RecoveryActionKind = "reschedule_safe"
	// RecoveryBlockAmbiguous marks a crash orphan whose request may have
	// crossed the wire without an idempotency key or queryable receipt.
	RecoveryBlockAmbiguous RecoveryActionKind = "block_ambiguous_commit"
)

type RecoveryPlan struct {
	Action     RecoveryActionKind
	RetryAfter time.Duration
	Reason     string
}

type InferenceRecoveryHint interface {
	InferenceRecoveryAction() RecoveryActionKind
}

type RefreshableAuthError struct {
	Err error
}

func (e *RefreshableAuthError) Error() string {
	if e == nil || e.Err == nil {
		return "authentication failed; credential refresh is available"
	}
	return e.Err.Error()
}

func (e *RefreshableAuthError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func MarkAuthRefreshable(err error) error {
	if err == nil {
		return nil
	}
	return &RefreshableAuthError{Err: err}
}

// LocalBackpressureError identifies consumer-side overload. Replaying the
// provider request would increase local pressure and duplicate billable work.
type LocalBackpressureError struct {
	Component string
}

func (e *LocalBackpressureError) Error() string {
	component := strings.TrimSpace(e.Component)
	if component == "" {
		component = "stream consumer"
	}
	return component + " could not keep up with provider events"
}

func NormalizeFailure(err error) NormalizedFailure {
	failure := normalizeFailure(err)
	var hint InferenceRecoveryHint
	if errors.As(err, &hint) {
		failure.SuggestedRecovery = hint.InferenceRecoveryAction()
	}
	return failure
}

func normalizeFailure(err error) NormalizedFailure {
	failure := NormalizedFailure{
		Origin:                   FailureOriginProtocol,
		Category:                 FailureUnknown,
		ClassificationConfidence: ConfidenceLow,
		Cause:                    err,
	}
	if err == nil {
		return failure
	}
	var refreshable *RefreshableAuthError
	if errors.As(err, &refreshable) {
		failure = NormalizeFailure(refreshable.Err)
		failure.AuthRefreshable = true
		failure.Cause = err
		return failure
	}

	var replayBlocked *ReplayBlockedError
	if errors.As(err, &replayBlocked) {
		failure.Origin = FailureOriginLocal
		failure.Category = FailureReplayUnsafe
		failure.ClassificationConfidence = ConfidenceHigh
		return failure
	}
	var budgetExceeded *WorkflowBudgetExceededError
	if errors.As(err, &budgetExceeded) {
		failure.Origin = FailureOriginLocal
		failure.Category = FailureBudgetExceeded
		failure.ClassificationConfidence = ConfidenceHigh
		return failure
	}
	var costIndeterminate *WorkflowCostIndeterminateError
	if errors.As(err, &costIndeterminate) {
		failure.Origin = FailureOriginLocal
		failure.Category = FailureCostIndeterminate
		failure.ClassificationConfidence = ConfidenceHigh
		return failure
	}
	var backpressure *LocalBackpressureError
	if errors.As(err, &backpressure) {
		failure.Origin = FailureOriginLocal
		failure.Category = FailureLocalBackpressure
		failure.ClassificationConfidence = ConfidenceHigh
		return failure
	}
	var eventTooLarge *StreamEventTooLargeError
	if errors.As(err, &eventTooLarge) {
		failure.Origin = FailureOriginLocal
		failure.Category = FailureResponseTooLarge
		failure.ClassificationConfidence = ConfidenceHigh
		return failure
	}
	if errors.Is(err, context.Canceled) {
		failure.Origin = FailureOriginCaller
		failure.Category = FailureCanceled
		failure.ClassificationConfidence = ConfidenceHigh
		return failure
	}
	if errors.Is(err, context.DeadlineExceeded) {
		failure.Origin = FailureOriginCaller
		failure.Category = FailureDeadline
		failure.ClassificationConfidence = ConfidenceHigh
		return failure
	}

	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		failure.Origin = FailureOriginProvider
		failure.ProviderFamily = strings.ToLower(strings.TrimSpace(httpErr.ProviderFamily))
		failure.ProviderCode = strings.TrimSpace(httpErr.ProviderCode)
		failure.HTTPStatus = httpErr.StatusCode
		failure.RetryAfter = httpErr.RetryAfter
		failure.ContextOverflow = httpErr.ContextOverflow
		failure.AuthRefreshable = httpErr.AuthRefreshable
		failure.RawBody = httpErr.Body
		failure.ClassificationConfidence = ConfidenceHigh
		switch {
		case httpErr.ContextOverflow:
			failure.Category = FailureContextOverflow
		case (httpErr.StatusCode == 403 || httpErr.StatusCode == 429) && isTerminalUsageLimit(httpErr.ProviderCode, httpErr.Body):
			failure.Category = FailureQuota
		case httpErr.StatusCode == 401 || httpErr.StatusCode == 403:
			failure.Category = FailureAuthentication
		case httpErr.StatusCode == 429:
			failure.Category = FailureRateLimit
		case httpErr.StatusCode == 529:
			failure.Category = FailureOverloaded
		case httpErr.StatusCode == 404:
			failure.Category = FailureNotFound
		case httpErr.StatusCode == 413:
			failure.Category = FailureRequestTooLarge
		case httpErr.StatusCode == 408 || httpErr.StatusCode == 425 || httpErr.StatusCode >= 500:
			failure.Category = FailureServer
		case httpErr.StatusCode >= 400:
			failure.Category = FailureInvalidRequest
		}
		return failure
	}

	var streamErr *StreamError
	if errors.As(err, &streamErr) {
		failure.Origin = FailureOriginProvider
		failure.ProviderFamily = strings.ToLower(strings.TrimSpace(streamErr.ProviderFamily))
		failure.ProviderCode = strings.TrimSpace(streamErr.Code)
		failure.RawBody = streamErr.Message
		failure.ClassificationConfidence = ConfidenceHigh
		switch {
		case streamErr.ContextOverflow:
			failure.Category = FailureContextOverflow
			failure.ContextOverflow = true
		case streamErr.Auth:
			failure.Category = FailureAuthentication
		case isLocalBackpressureMessage(streamErr.Code + " " + streamErr.Message):
			failure.Origin = FailureOriginLocal
			failure.Category = FailureLocalBackpressure
		case isTerminalUsageLimit(streamErr.Code, streamErr.Message):
			failure.Category = FailureQuota
		case isProviderRateLimited(streamErr.Code, streamErr.Message):
			failure.Category = FailureRateLimit
		case isProviderOverloaded(streamErr.Code, streamErr.Message):
			failure.Category = FailureOverloaded
		case streamErr.Retryable && isTemporaryProviderFailure(streamErr.Code, streamErr.Message):
			failure.Category = FailureServer
		case streamErr.Code == "400" || streamErr.Code == "invalid_request" || streamErr.Code == "invalid_request_error":
			failure.Category = FailureInvalidRequest
		case streamErr.Retryable:
			failure.Category = FailureIncompleteStream
		default:
			failure.Category = FailureUnknown
		}
		return failure
	}

	if isLocalBackpressureMessage(err.Error()) {
		failure.Origin = FailureOriginLocal
		failure.Category = FailureLocalBackpressure
		failure.ClassificationConfidence = ConfidenceMedium
		return failure
	}
	if isNetworkError(err) {
		failure.Origin = FailureOriginNetwork
		failure.Category = FailureNetwork
		failure.ClassificationConfidence = ConfidenceMedium
	}
	return failure
}

func PlanRecovery(failure NormalizedFailure) RecoveryPlan {
	if failure.SuggestedRecovery != "" {
		return RecoveryPlan{Action: failure.SuggestedRecovery, RetryAfter: failure.RetryAfter, Reason: "protocol recovery hint"}
	}
	switch failure.Category {
	case FailureRateLimit:
		return RecoveryPlan{Action: RecoveryWaitThenReplay, RetryAfter: failure.RetryAfter, Reason: "temporary provider rate limit"}
	case FailureOverloaded:
		return RecoveryPlan{Action: RecoveryWaitThenReplay, RetryAfter: failure.RetryAfter, Reason: "provider overloaded"}
	case FailureServer, FailureNetwork, FailureIncompleteStream, FailureDeadline:
		return RecoveryPlan{Action: RecoveryReplaySame, RetryAfter: failure.RetryAfter, Reason: string(failure.Category)}
	case FailureNotFound:
		if failure.ProviderFamily == "openai" && !isTerminalOpenAINotFound(failure.ProviderCode, failure.RawBody) {
			return RecoveryPlan{Action: RecoveryReplaySame, RetryAfter: failure.RetryAfter, Reason: "openai-compatible route not found may be transient"}
		}
		return RecoveryPlan{Action: RecoveryStop, Reason: "resource not found"}
	case FailureContextOverflow:
		return RecoveryPlan{Action: RecoveryTransformPayload, Reason: "context compaction required"}
	case FailureAuthentication:
		if failure.AuthRefreshable {
			return RecoveryPlan{Action: RecoveryRefreshAuth, Reason: "credential refresh available"}
		}
		return RecoveryPlan{Action: RecoveryStop, Reason: "authentication failed"}
	case FailureReplayUnsafe:
		return RecoveryPlan{Action: RecoveryBlockUnsafe, Reason: "replay safety fence"}
	case FailureCanceled, FailureQuota, FailureRequestTooLarge, FailureResponseTooLarge, FailureInvalidRequest, FailureLocalBackpressure,
		FailureBudgetExceeded, FailureCostIndeterminate, FailureUnknown:
		return RecoveryPlan{Action: RecoveryStop, Reason: string(failure.Category)}
	default:
		return RecoveryPlan{Action: RecoveryStop, Reason: "unclassified failure"}
	}
}

func (p RecoveryPlan) Retryable() bool {
	switch p.Action {
	case RecoveryReplaySame, RecoveryWaitThenReplay, RecoverySwitchTransport, RecoveryRefreshAuth:
		return true
	default:
		return false
	}
}

func isTerminalOpenAINotFound(code, body string) bool {
	msg := strings.ToLower(strings.TrimSpace(code + " " + body))
	for _, marker := range []string{
		"model_not_found",
		"model not found",
		"unknown model",
		"invalid model",
		"does not exist",
		"do not have access to model",
		"don't have access to model",
		"endpoint not found",
		"404 page not found",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func isLocalBackpressureMessage(message string) bool {
	msg := strings.ToLower(message)
	return strings.Contains(msg, "read_backpressure") ||
		strings.Contains(msg, "local backpressure") ||
		strings.Contains(msg, "consumer could not keep up")
}
