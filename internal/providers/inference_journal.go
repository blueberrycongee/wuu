package providers

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// InferenceJournal is the durable, metadata-only write-ahead boundary for one
// inference operation. These methods are synchronous durability checkpoints:
// each commits before it returns, and each gates a wire or control-flow
// transition (record before send, terminalize before the next attempt). A
// caller uses their error to stop before crossing that boundary. Required writes
// run at wire or control-flow transitions, never once per streamed delta.
// UpsertSubmission checkpoints the initial send and the current physical
// observations before attempt completion, recovery admission, or operation
// completion. It does not infer a physical outcome from a logical outcome.
//
// Streaming cost observation is deliberately NOT part of this interface: it is
// best-effort telemetry, not a checkpoint, and belongs on InferenceProgressJournal.
//
// None of these records may contain prompts, credentials, request/response
// bodies, or raw provider errors. RequestHash is the only durable payload
// identity.
type InferenceJournal interface {
	PrepareOperation(InferenceOperationJournalRecord) error
	PrepareAttempt(InferenceAttemptJournalRecord) error
	UpsertSubmission(context.Context, InferenceSubmissionJournalRecord) error
	MarkAttemptFirstEvent(operationID, attemptID, submissionID string, at time.Time) error
	CompleteAttempt(InferenceAttemptTerminalRecord) error
	PrepareRecoveryAttempt(context.Context, InferenceRecoveryAttemptJournalRecord) error
	CompleteOperation(InferenceOperationTerminalRecord) error
	CompleteWorkflow(InferenceWorkflowTerminalRecord) error
}

// InferenceProgressJournal is an optional capability for journals that can
// absorb streaming cost observations off the caller's goroutine. It exists to
// keep bookkeeping subordinate to the user-facing stream: a per-delta cost
// estimate is telemetry, so recording it must never block token delivery and
// its failure must never abort a healthy stream. Implementations coalesce
// updates by submission id and flush them asynchronously; durability at the
// terminal boundary comes from required UpsertSubmission checkpoints before
// logical transitions, independently of pending progress. RecordSubmissionProgress
// therefore returns nothing — the caller cannot and must not wait on it.
type InferenceProgressJournal interface {
	RecordSubmissionProgress(InferenceSubmissionJournalRecord)
}

type InferenceOperationJournalRecord struct {
	Operation   InferenceOperation
	Workflow    InferenceWorkflowJournalRecord
	RequestHash string
	At          time.Time
}

type InferenceWorkflowJournalRecord struct {
	ID        string
	Profile   InferenceWorkloadProfile
	Budget    WorkflowBudgetSpec
	StartedAt time.Time
}

type InferenceAttemptJournalRecord struct {
	OperationID string
	WorkflowID  string
	AttemptID   string
	Ordinal     int
	RequestHash string
	At          time.Time
}

type InferenceSubmissionJournalRecord struct {
	OperationID     string
	AttemptID       string
	ID              string
	Ordinal         int
	AttemptOrdinal  int
	Provider        string
	Protocol        string
	Transport       string
	Mode            string
	Reason          string
	StartedAt       time.Time
	CompletedAt     time.Time
	Outcome         InferenceSubmissionOutcome
	FailureCategory FailureCategory
	CostState       InferenceCostState
	ReportedUsage   *TokenUsage
	EstimatedUsage  *TokenUsage
	OutputBytes     int
}

type InferenceAttemptTerminalRecord struct {
	OperationID string
	AttemptID   string
	Outcome     InferenceTerminalOutcome
	Failure     InferenceJournalFailure
	At          time.Time
}

type InferenceRecoveryJournalRecord struct {
	OperationID string
	AttemptID   string
	Action      RecoveryActionKind
	RetryAt     time.Time
	At          time.Time
}

type InferenceRecoveryAttemptJournalRecord struct {
	Recovery    InferenceRecoveryJournalRecord
	NextAttempt InferenceAttemptJournalRecord
}

type InferenceOperationTerminalRecord struct {
	OperationID string
	Outcome     InferenceTerminalOutcome
	Failure     InferenceJournalFailure
	At          time.Time
}

type InferenceWorkflowTerminalRecord struct {
	WorkflowID string
	Outcome    InferenceTerminalOutcome
	At         time.Time
}

// InferenceJournalFailure is the durable allowlist of failure metadata. Raw
// bodies and Cause deliberately do not have fields here; Message is the one
// exception, derived from Cause only after credential-shaped fragments are
// redacted, whitespace is collapsed, and the result is truncated.
type InferenceJournalFailure struct {
	Origin         FailureOrigin
	Category       FailureCategory
	ProviderFamily string
	ProviderCode   string
	HTTPStatus     int
	Confidence     ClassificationConfidence
	Message        string
}

func DurableInferenceFailure(failure NormalizedFailure) InferenceJournalFailure {
	return InferenceJournalFailure{
		Origin:         failure.Origin,
		Category:       failure.Category,
		ProviderFamily: failure.ProviderFamily,
		ProviderCode:   failure.ProviderCode,
		HTTPStatus:     failure.HTTPStatus,
		Confidence:     failure.ClassificationConfidence,
		Message:        sanitizeInferenceFailureMessage(failure.Cause),
	}
}

// inferenceFailureMessageSecrets matches credential-shaped fragments that
// transport errors occasionally echo (auth URLs, header values, SDK client
// options). The key prefix is kept so the message stays readable.
var inferenceFailureMessageSecrets = regexp.MustCompile(`(?i)(bearer\s+|sk-|api[-_]?key\s*[=:]\s*|token\s*[=:]\s*|secret\s*[=:]\s*|password\s*[=:]\s*)[^\s,;\)\]\}"']+`)

// sanitizeInferenceFailureMessage derives the durable one-line summary from a
// failure cause: redact credential-shaped fragments, collapse all whitespace
// (wrapped errors span lines), and cap the length. Everything else about the
// cause - stack, request body - never reaches the journal.
func sanitizeInferenceFailureMessage(cause error) string {
	if cause == nil {
		return ""
	}
	msg := inferenceFailureMessageSecrets.ReplaceAllString(cause.Error(), "${1}[redacted]")
	msg = strings.Join(strings.Fields(msg), " ")
	runes := []rune(msg)
	if len(runes) > 200 {
		msg = string(runes[:200])
	}
	return msg
}

type InferenceTerminalOutcome string

const (
	InferenceOutcomeSucceeded   InferenceTerminalOutcome = "succeeded"
	InferenceOutcomeFailed      InferenceTerminalOutcome = "failed"
	InferenceOutcomeCanceled    InferenceTerminalOutcome = "canceled"
	InferenceOutcomeAbandoned   InferenceTerminalOutcome = "abandoned"
	InferenceOutcomeInterrupted InferenceTerminalOutcome = "interrupted"
	InferenceOutcomeBlocked     InferenceTerminalOutcome = "blocked"
)

type inferenceJournalContextKey struct{}

// WithInferenceJournal binds a journal to every inference operation created
// under ctx, including nested compaction calls.
func WithInferenceJournal(ctx context.Context, journal InferenceJournal) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if journal == nil {
		return ctx
	}
	// Bind a per-operation clone so shared runtime handles never retain another
	// operation's execution identity or cancellation.
	if contextual, ok := journal.(interface {
		WithContext(context.Context) InferenceJournal
	}); ok {
		journal = contextual.WithContext(ctx)
	}
	return context.WithValue(ctx, inferenceJournalContextKey{}, journal)
}

func InferenceJournalFromContext(ctx context.Context) InferenceJournal {
	if ctx == nil {
		return nil
	}
	journal, _ := ctx.Value(inferenceJournalContextKey{}).(InferenceJournal)
	return journal
}
