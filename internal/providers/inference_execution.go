package providers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// InferenceSubmissionMeta describes one physical request submission without
// carrying credentials, prompt text, response bodies, or provider receipts.
type InferenceSubmissionMeta struct {
	Provider  string
	Protocol  string
	Transport string
	Mode      string
	Reason    string
	// RequestBytes is the serialized request body size. It seeds a
	// conservative input-token estimate so a submission whose endpoint never
	// reports usage can settle as estimated instead of occupying the
	// workflow's unknown-billable budget forever.
	RequestBytes int
}

type InferenceSubmissionOutcome string

const (
	InferenceSubmissionInFlight  InferenceSubmissionOutcome = "in_flight"
	InferenceSubmissionSucceeded InferenceSubmissionOutcome = "succeeded"
	InferenceSubmissionFailed    InferenceSubmissionOutcome = "failed"
	InferenceSubmissionFallback  InferenceSubmissionOutcome = "transport_fallback"
	InferenceSubmissionAbandoned InferenceSubmissionOutcome = "abandoned"
	// Interrupted is written by crash recovery when a submission may have
	// crossed the wire but the process died before observing a terminal result.
	InferenceSubmissionInterrupted InferenceSubmissionOutcome = "interrupted"
)

type InferenceCostState string

const (
	// Unknown-but-billable is the safe initial state once a payload may have
	// crossed the wire. It must never be aggregated as zero cost.
	InferenceCostUnknownBillable InferenceCostState = "unknown_but_billable"
	InferenceCostEstimated       InferenceCostState = "estimated"
	InferenceCostKnown           InferenceCostState = "known"
)

// InferenceSubmission is one physical request that may have crossed the wire
// boundary. An attempt owns at most one submission.
type InferenceSubmission struct {
	ID              string
	Ordinal         int
	AttemptID       string
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

	execution *InferenceExecution
}

func (s InferenceSubmission) Valid() bool {
	return strings.TrimSpace(s.ID) != ""
}

func (s InferenceSubmission) mutable() bool {
	return s.execution != nil && s.Valid()
}

// ObserveOutput records a conservative partial-output estimate. Provider
// usage, when it arrives, always supersedes this estimate.
func (s InferenceSubmission) ObserveOutput(content string) {
	if !s.mutable() || content == "" {
		return
	}
	s.execution.updateSubmission(s.ID, func(current *InferenceSubmission) {
		current.OutputBytes += len([]byte(content))
		if current.CostState == InferenceCostKnown {
			return
		}
		if current.EstimatedUsage == nil {
			current.EstimatedUsage = &TokenUsage{}
		}
		current.EstimatedUsage.OutputTokens = estimateTokensFromBytes(current.OutputBytes)
	})
}

// ObserveEstimatedUsage promotes a submission only when the caller has a
// complete conservative estimate, including input. Partial output evidence
// alone remains unknown-but-billable because input often dominates cost.
func (s InferenceSubmission) ObserveEstimatedUsage(usage *TokenUsage) {
	if !s.mutable() || usage == nil {
		return
	}
	copyUsage := *usage
	s.execution.updateSubmission(s.ID, func(current *InferenceSubmission) {
		if current.CostState == InferenceCostKnown {
			return
		}
		if current.EstimatedUsage != nil && current.EstimatedUsage.OutputTokens > copyUsage.OutputTokens {
			copyUsage.OutputTokens = current.EstimatedUsage.OutputTokens
		}
		current.CostState = InferenceCostEstimated
		current.EstimatedUsage = &copyUsage
	})
}

func (s InferenceSubmission) ObserveUsage(usage *TokenUsage) {
	if !s.mutable() || usage == nil {
		return
	}
	copyUsage := *usage
	s.execution.updateSubmission(s.ID, func(current *InferenceSubmission) {
		current.CostState = InferenceCostKnown
		current.ReportedUsage = &copyUsage
	})
}

func (s InferenceSubmission) CompleteSuccess(usage *TokenUsage) {
	s.complete(InferenceSubmissionSucceeded, NormalizedFailure{}, usage)
}

func (s InferenceSubmission) CompleteFailure(failure NormalizedFailure) {
	s.complete(InferenceSubmissionFailed, failure, nil)
}

func (s InferenceSubmission) CompleteFallback(failure NormalizedFailure) {
	s.complete(InferenceSubmissionFallback, failure, nil)
}

func (s InferenceSubmission) Abandon() {
	s.complete(InferenceSubmissionAbandoned, NormalizedFailure{}, nil)
}

func (s InferenceSubmission) complete(outcome InferenceSubmissionOutcome, failure NormalizedFailure, usage *TokenUsage) {
	if !s.mutable() {
		return
	}
	var copyUsage *TokenUsage
	if usage != nil {
		value := *usage
		copyUsage = &value
	}
	s.execution.updateSubmission(s.ID, func(current *InferenceSubmission) {
		if current.Outcome != InferenceSubmissionInFlight {
			if copyUsage != nil && current.CostState != InferenceCostKnown {
				current.CostState = InferenceCostKnown
				current.ReportedUsage = copyUsage
			}
			return
		}
		current.Outcome = outcome
		current.CompletedAt = time.Now().UTC()
		current.FailureCategory = failure.Category
		if copyUsage != nil {
			current.CostState = InferenceCostKnown
			current.ReportedUsage = copyUsage
			return
		}
		// Terminal without reported usage: settle on the conservative
		// estimate instead of occupying the workflow's unknown-billable
		// budget forever. Endpoints that never report usage would otherwise
		// kill the turn once the cumulative cap is reached, and every
		// canceled or abandoned stream would permanently burn a slot. The
		// promotion requires input evidence (the request-size seed from
		// RecordSubmission or a full ObserveEstimatedUsage); partial output
		// alone stays unknown-but-billable because input often dominates.
		if current.CostState == InferenceCostUnknownBillable &&
			current.EstimatedUsage != nil && current.EstimatedUsage.InputTokens > 0 {
			current.CostState = InferenceCostEstimated
		}
	})
}

func estimateTokensFromBytes(byteCount int) int {
	if byteCount <= 0 {
		return 0
	}
	return (byteCount + 3) / 4
}

// InferenceExecutionSnapshot is a race-free diagnostic view of one logical
// operation. It is intentionally metadata-only.
type InferenceExecutionSnapshot struct {
	Operation   InferenceOperation
	Attempts    int
	Submissions []InferenceSubmission
	Workflow    WorkflowBudgetSnapshot
}

type InferenceCostSummary struct {
	KnownSubmissions           int
	EstimatedSubmissions       int
	UnknownBillableSubmissions int
	KnownUsage                 TokenUsage
	EstimatedUsage             TokenUsage
}

// CostSummary preserves cost confidence instead of silently adding unknown
// billable submissions as zero usage.
func (s InferenceExecutionSnapshot) CostSummary() InferenceCostSummary {
	var summary InferenceCostSummary
	for _, submission := range s.Submissions {
		switch submission.CostState {
		case InferenceCostKnown:
			summary.KnownSubmissions++
			if submission.ReportedUsage != nil {
				addTokenUsage(&summary.KnownUsage, *submission.ReportedUsage)
			}
		case InferenceCostEstimated:
			summary.EstimatedSubmissions++
			if submission.EstimatedUsage != nil {
				addTokenUsage(&summary.EstimatedUsage, *submission.EstimatedUsage)
			}
		default:
			summary.UnknownBillableSubmissions++
		}
	}
	return summary
}

func addTokenUsage(total *TokenUsage, usage TokenUsage) {
	if total == nil {
		return
	}
	total.InputTokens += usage.InputTokens
	total.OutputTokens += usage.OutputTokens
	total.CacheCreationTokens += usage.CacheCreationTokens
	total.CacheReadTokens += usage.CacheReadTokens
	total.CacheCreationUnknown = total.CacheCreationUnknown || usage.CacheCreationUnknown
}

// InferenceExecution owns attempt and submission ordinals for one operation.
// It is safe to share through copied ChatRequest values and provider goroutines.
type InferenceExecution struct {
	mu sync.Mutex

	operation       InferenceOperation
	workflow        *InferenceWorkflow
	nextAttempt     int
	nextSubmission  int
	submissions     []InferenceSubmission
	journal         InferenceJournal
	requestHash     string
	journalErr      error
	journalDegraded error
	firstEvents     map[string]bool
	terminal        bool
	ownsWorkflow    bool
}

// InferenceAttempt identifies one ExecuteOnce call. The execution pointer is
// deliberately private; provider implementations record wire submissions via
// RecordSubmission instead of mutating the ledger directly.
type InferenceAttempt struct {
	ID      string
	Ordinal int

	execution *InferenceExecution
}

func NewInferenceExecution(operation InferenceOperation) *InferenceExecution {
	execution, err := newInferenceExecution(operation, nil)
	if err != nil {
		panic(fmt.Sprintf("providers: create inference execution: %v", err))
	}
	return execution
}

func newInferenceExecution(operation InferenceOperation, workflow *InferenceWorkflow) (*InferenceExecution, error) {
	operation = EnsureInferenceOperation(operation, InferenceOperationAuxiliary, InferenceProfileInteractive)
	ownsWorkflow := workflow == nil && strings.TrimSpace(operation.WorkflowID) == ""
	if workflow == nil {
		if strings.TrimSpace(operation.WorkflowID) != "" {
			workflow = newInferenceWorkflowWithIdentity(
				operation.WorkflowID,
				operation.WorkloadProfile,
				WorkflowBudgetSpecForProfile(operation.WorkloadProfile),
				time.Time{},
			)
		} else {
			workflow = NewInferenceWorkflow(operation.WorkloadProfile)
		}
	}
	if workflow.spend == nil {
		workflow.spend = newWorkflowBudget(workflow.ID, workflow.Budget)
	}
	if strings.TrimSpace(operation.WorkflowID) == "" {
		operation.WorkflowID = workflow.ID
	}
	if operation.WorkflowID != workflow.ID {
		return nil, fmt.Errorf("operation %q belongs to workflow %q, not %q", operation.ID, operation.WorkflowID, workflow.ID)
	}
	if err := workflow.admitOperation(operation); err != nil {
		return nil, err
	}
	return &InferenceExecution{operation: operation, workflow: workflow, ownsWorkflow: ownsWorkflow}, nil
}

// ensureInferenceExecution attaches the process-local ledger while preserving
// caller-supplied operation identity.
func ensureInferenceExecution(ctx context.Context, req ChatRequest, fallbackKind InferenceOperationKind, fallbackProfile InferenceWorkloadProfile) (ChatRequest, error) {
	if req.Execution != nil && strings.TrimSpace(req.Operation.ID) == "" {
		req.Operation = req.Execution.Operation()
	}
	req.Operation = EnsureInferenceOperation(req.Operation, fallbackKind, fallbackProfile)
	if req.Execution != nil && req.Execution.Operation().ID == req.Operation.ID {
		existing := req.Execution.Operation()
		if req.Operation != existing {
			return req, errors.New("inference operation metadata changed after execution creation")
		}
		return req, nil
	}
	workflow := InferenceWorkflowFromContext(ctx)
	if strings.TrimSpace(req.Operation.WorkflowID) == "" && workflow != nil {
		req.Operation.WorkflowID = workflow.ID
	}
	if strings.TrimSpace(req.Operation.ParentOperationID) == "" {
		if lineage := inferenceOperationLineageFromContext(ctx); lineage != nil {
			req.Operation.ParentOperationID = lineage.LastOperationID()
		}
	}
	execution, err := newInferenceExecution(req.Operation, workflow)
	if err != nil {
		return req, err
	}
	req.Execution = execution
	req.Operation = execution.Operation()
	return req, nil
}

// EnsureInferenceExecutionContext attaches the process-local execution
// identity. Durable request hashing is intentionally deferred until an attempt
// is prepared, after provider-specific request normalization has completed.
func EnsureInferenceExecutionContext(ctx context.Context, req ChatRequest, fallbackKind InferenceOperationKind, fallbackProfile InferenceWorkloadProfile) (ChatRequest, error) {
	return ensureInferenceExecution(ctx, req, fallbackKind, fallbackProfile)
}

func bindInferenceJournalContext(ctx context.Context, req ChatRequest) (ChatRequest, error) {
	journal := InferenceJournalFromContext(ctx)
	if journal != nil {
		requestHash, err := InferenceRequestHash(req)
		if err != nil {
			return req, err
		}
		if err := req.Execution.bindJournal(journal, requestHash); err != nil {
			return req, err
		}
	}
	// Capability probes can allocate an execution without preparing an attempt
	// (for example unavailable native compaction). Such identities must never
	// become parents: the next operation's journal requires a persisted parent.
	if lineage := inferenceOperationLineageFromContext(ctx); lineage != nil {
		lineage.advance(req.Operation.ID)
	}
	return req, nil
}

func EnsureInferenceAttemptContext(ctx context.Context, req ChatRequest, fallbackKind InferenceOperationKind, fallbackProfile InferenceWorkloadProfile) (ChatRequest, error) {
	if err := inferenceContextError(ctx); err != nil {
		return req, err
	}
	var err error
	req, err = EnsureInferenceExecutionContext(ctx, req, fallbackKind, fallbackProfile)
	if err != nil {
		return req, err
	}
	req, err = bindInferenceJournalContext(ctx, req)
	if err != nil {
		return req, err
	}
	if err := inferenceContextError(ctx); err != nil {
		return req, err
	}
	if req.Attempt.Valid() {
		return req, req.Attempt.JournalError()
	}
	req.Attempt, err = req.Execution.BeginAttemptChecked()
	return req, err
}

func BeginInferenceAttemptContext(ctx context.Context, req ChatRequest, fallbackKind InferenceOperationKind, fallbackProfile InferenceWorkloadProfile) (ChatRequest, error) {
	if err := inferenceContextError(ctx); err != nil {
		return req, err
	}
	var err error
	req, err = EnsureInferenceExecutionContext(ctx, req, fallbackKind, fallbackProfile)
	if err != nil {
		return req, err
	}
	req, err = bindInferenceJournalContext(ctx, req)
	if err != nil {
		return req, err
	}
	if err := inferenceContextError(ctx); err != nil {
		return req, err
	}
	req.Attempt, err = req.Execution.BeginAttemptChecked()
	return req, err
}

func inferenceContextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func (e *InferenceExecution) Operation() InferenceOperation {
	if e == nil {
		return InferenceOperation{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.operation
}

func (e *InferenceExecution) BeginAttempt() InferenceAttempt {
	attempt, _ := e.BeginAttemptChecked()
	return attempt
}

func (e *InferenceExecution) BeginAttemptChecked() (InferenceAttempt, error) {
	if e == nil {
		panic("providers: begin inference attempt on nil execution")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.terminal {
		return InferenceAttempt{}, errors.New("inference operation is already terminal")
	}
	if e.journalErr != nil {
		return InferenceAttempt{}, e.journalErr
	}
	nextOrdinal := e.nextAttempt + 1
	attempt := InferenceAttempt{
		ID:        e.operation.AttemptID(nextOrdinal),
		Ordinal:   nextOrdinal,
		execution: e,
	}
	if e.workflow != nil && e.workflow.spend != nil {
		if err := e.workflow.spend.AdmitAttempt(e.operation, attempt.ID, attempt.Ordinal); err != nil {
			return InferenceAttempt{}, err
		}
	}
	e.nextAttempt = nextOrdinal
	if e.journal != nil {
		if err := e.journal.PrepareAttempt(InferenceAttemptJournalRecord{
			OperationID: e.operation.ID,
			WorkflowID:  e.operation.WorkflowID,
			AttemptID:   attempt.ID,
			Ordinal:     attempt.Ordinal,
			RequestHash: e.requestHash,
			At:          time.Now().UTC(),
		}); err != nil {
			e.nextAttempt--
			if !IsWorkflowBudgetError(err) {
				e.journalErr = err
			}
			return InferenceAttempt{}, err
		}
	}
	return attempt, nil
}

func (a InferenceAttempt) Valid() bool {
	return a.execution != nil && strings.TrimSpace(a.ID) != "" && a.Ordinal > 0
}

// Operation returns the immutable logical-operation metadata associated with
// this attempt. Invalid attempts return the zero value so scheduling callers
// can safely fall back to the interactive profile.
func (a InferenceAttempt) Operation() InferenceOperation {
	if !a.Valid() {
		return InferenceOperation{}
	}
	return a.execution.Operation()
}

func (a InferenceAttempt) RecordSubmission(meta InferenceSubmissionMeta) (InferenceSubmission, error) {
	if !a.Valid() {
		return InferenceSubmission{}, errors.New("record submission without a valid inference attempt")
	}
	e := a.execution
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.journalErr != nil {
		return InferenceSubmission{}, e.journalErr
	}
	for _, existing := range e.submissions {
		if existing.AttemptID == a.ID {
			return InferenceSubmission{}, fmt.Errorf("attempt %q already owns submission %q", a.ID, existing.ID)
		}
	}
	nextOrdinal := e.nextSubmission + 1
	submission := InferenceSubmission{
		ID:             fmt.Sprintf("%s-s%d", e.operation.ID, nextOrdinal),
		Ordinal:        nextOrdinal,
		AttemptID:      a.ID,
		AttemptOrdinal: a.Ordinal,
		Provider:       strings.TrimSpace(meta.Provider),
		Protocol:       strings.TrimSpace(meta.Protocol),
		Transport:      strings.TrimSpace(meta.Transport),
		Mode:           strings.TrimSpace(meta.Mode),
		Reason:         strings.TrimSpace(meta.Reason),
		StartedAt:      time.Now().UTC(),
		Outcome:        InferenceSubmissionInFlight,
		CostState:      InferenceCostUnknownBillable,
		execution:      e,
	}
	if meta.RequestBytes > 0 {
		submission.EstimatedUsage = &TokenUsage{InputTokens: estimateTokensFromBytes(meta.RequestBytes)}
	}
	if e.workflow != nil && e.workflow.spend != nil {
		if err := e.workflow.spend.AdmitSubmission(submission.ID); err != nil {
			return InferenceSubmission{}, err
		}
	}
	e.nextSubmission = nextOrdinal
	if e.journal != nil {
		if err := e.journal.UpsertSubmission(journalSubmission(e.operation.ID, submission)); err != nil {
			e.nextSubmission--
			if !IsWorkflowBudgetError(err) {
				e.journalErr = err
			}
			return InferenceSubmission{}, err
		}
	}
	e.submissions = append(e.submissions, submission)
	return submission, nil
}

// ObserveStreamEvent attributes provider-neutral stream evidence to the most
// recent physical submission in this attempt. Protocol adapters may also
// update an exact submission handle; both paths are idempotent at terminal.
func (a InferenceAttempt) ObserveStreamEvent(event StreamEvent) error {
	if !a.Valid() {
		return nil
	}
	submission, ok := a.latestSubmission()
	if !ok {
		return a.JournalError()
	}
	if isProviderResponseEvidence(event.Type) {
		a.markFirstEvent(submission.ID)
	}
	switch event.Type {
	case EventContentDelta, EventThinkingDelta, EventToolUseDelta:
		submission.ObserveOutput(event.Content)
	case EventUsage:
		submission.ObserveUsage(event.Usage)
	case EventDone:
		submission.CompleteSuccess(event.Usage)
	case EventError:
		err := event.Error
		if err == nil {
			err = NewIncompleteStreamError("provider stream failed")
		}
		submission.CompleteFailure(NormalizeFailure(err))
	}
	return a.JournalError()
}

func (a InferenceAttempt) markFirstEvent(submissionID string) {
	e := a.execution
	e.mu.Lock()
	if e.journal == nil {
		e.mu.Unlock()
		return
	}
	if e.firstEvents == nil {
		e.firstEvents = make(map[string]bool)
	}
	if e.firstEvents[a.ID] {
		e.mu.Unlock()
		return
	}
	// This milestone is best-effort telemetry recorded once per attempt. Mark
	// it attempted before the write so a failure does not retry on every
	// following delta, run the write off the execution lock, and degrade
	// (never abort the stream) if it fails.
	e.firstEvents[a.ID] = true
	journal := e.journal
	operationID := e.operation.ID
	attemptID := a.ID
	e.mu.Unlock()
	if err := journal.MarkAttemptFirstEvent(operationID, attemptID, submissionID, time.Now().UTC()); err != nil {
		e.noteJournalDegraded(err)
	}
}

// noteJournalDegraded records that best-effort journaling failed without
// poisoning journalErr. journalErr is reserved for synchronous durability
// checkpoints that gate a wire transition; streaming telemetry failures must
// only degrade bookkeeping, so they surface here (and in the debug log) but
// never abort a healthy stream.
func (e *InferenceExecution) noteJournalDegraded(err error) {
	if e == nil || err == nil {
		return
	}
	e.mu.Lock()
	if e.journalDegraded == nil {
		e.journalDegraded = err
	}
	e.mu.Unlock()
	DebugLogf("inference journal: streaming observation degraded: %v", err)
}

// JournalDegraded returns the first best-effort journaling failure observed
// during streaming, or nil. It is diagnostics only and never affects control
// flow.
func (e *InferenceExecution) JournalDegraded() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.journalDegraded
}

func isProviderResponseEvidence(eventType StreamEventType) bool {
	switch eventType {
	case EventContentDelta, EventContentReplace, EventThinkingDelta, EventThinkingReplace,
		EventThinkingDone, EventToolUseStart, EventToolUseDelta, EventToolUseEnd,
		EventUsage, EventMessage, EventDone:
		return true
	default:
		return false
	}
}

func (a InferenceAttempt) JournalError() error {
	if !a.Valid() {
		return nil
	}
	e := a.execution
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.journalErr
}

func (a InferenceAttempt) Complete(outcome InferenceTerminalOutcome, failure NormalizedFailure) error {
	if !a.Valid() {
		return errors.New("complete without a valid inference attempt")
	}
	e := a.execution
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.journalErr != nil {
		return e.journalErr
	}
	if e.journal == nil {
		return nil
	}
	if err := e.journal.CompleteAttempt(InferenceAttemptTerminalRecord{
		OperationID: e.operation.ID,
		AttemptID:   a.ID,
		Outcome:     outcome,
		Failure:     DurableInferenceFailure(failure),
		At:          time.Now().UTC(),
	}); err != nil {
		e.journalErr = err
		return err
	}
	return nil
}

func (a InferenceAttempt) PrepareRecoveryAttempt(ctx context.Context, plan RecoveryPlan, retryAt time.Time) (InferenceAttempt, error) {
	if !a.Valid() {
		return InferenceAttempt{}, errors.New("prepare recovery without a valid inference attempt")
	}
	e := a.execution
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.journalErr != nil {
		return InferenceAttempt{}, e.journalErr
	}
	if e.terminal {
		return InferenceAttempt{}, errors.New("inference operation is already terminal")
	}
	nextOrdinal := e.nextAttempt + 1
	nextAttempt := InferenceAttempt{
		ID: e.operation.AttemptID(nextOrdinal), Ordinal: nextOrdinal, execution: e,
	}
	now := time.Now().UTC()
	persist := func() error {
		if err := inferenceContextError(ctx); err != nil {
			return err
		}
		if e.journal == nil {
			return nil
		}
		return e.journal.PrepareRecoveryAttempt(ctx, InferenceRecoveryAttemptJournalRecord{
			Recovery: InferenceRecoveryJournalRecord{
				OperationID: e.operation.ID, AttemptID: a.ID,
				Action: plan.Action, RetryAt: retryAt, At: now,
			},
			NextAttempt: InferenceAttemptJournalRecord{
				OperationID: e.operation.ID, WorkflowID: e.operation.WorkflowID,
				AttemptID: nextAttempt.ID, Ordinal: nextAttempt.Ordinal,
				RequestHash: e.requestHash, At: now,
			},
		})
	}
	var err error
	if e.workflow != nil && e.workflow.spend != nil {
		err = e.workflow.spend.AdmitRecoveryAttempt(
			e.operation, a.ID, nextAttempt.ID, nextAttempt.Ordinal, plan, retryAt, persist,
		)
	} else {
		err = persist()
	}
	if err != nil {
		if !IsWorkflowBudgetError(err) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			e.journalErr = err
		}
		return InferenceAttempt{}, err
	}
	e.nextAttempt = nextOrdinal
	return nextAttempt, nil
}

func (e *InferenceExecution) Complete(outcome InferenceTerminalOutcome, failure NormalizedFailure) error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.terminal {
		return e.journalErr
	}
	if e.journalErr != nil {
		return e.journalErr
	}
	if e.journal != nil {
		if err := e.journal.CompleteOperation(InferenceOperationTerminalRecord{
			OperationID: e.operation.ID,
			Outcome:     outcome,
			Failure:     DurableInferenceFailure(failure),
			At:          time.Now().UTC(),
		}); err != nil {
			e.journalErr = err
			return err
		}
	}
	e.terminal = true
	if e.ownsWorkflow {
		if err := CompleteInferenceWorkflow(e.workflow, e.journal, outcome); err != nil {
			e.journalErr = err
			return err
		}
	}
	return nil
}

func (e *InferenceExecution) LatestAttempt() InferenceAttempt {
	if e == nil {
		return InferenceAttempt{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.nextAttempt < 1 {
		return InferenceAttempt{}
	}
	return InferenceAttempt{
		ID:        e.operation.AttemptID(e.nextAttempt),
		Ordinal:   e.nextAttempt,
		execution: e,
	}
}

func (e *InferenceExecution) bindJournal(journal InferenceJournal, requestHash string) error {
	if e == nil || journal == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.journalErr != nil {
		return e.journalErr
	}
	if e.journal != nil {
		if e.requestHash != requestHash {
			return errors.New("inference request changed without a new operation or payload version")
		}
		return nil
	}
	if err := journal.PrepareOperation(InferenceOperationJournalRecord{
		Operation: e.operation,
		Workflow: InferenceWorkflowJournalRecord{
			ID:        e.workflow.ID,
			Profile:   e.workflow.Profile,
			Budget:    e.workflow.Budget,
			StartedAt: e.workflow.StartedAt,
		},
		RequestHash: requestHash,
		At:          time.Now().UTC(),
	}); err != nil {
		e.journalErr = err
		return err
	}
	e.journal = journal
	e.requestHash = requestHash
	return nil
}

func (a InferenceAttempt) latestSubmission() (InferenceSubmission, bool) {
	e := a.execution
	e.mu.Lock()
	defer e.mu.Unlock()
	for index := len(e.submissions) - 1; index >= 0; index-- {
		if e.submissions[index].AttemptID == a.ID {
			return e.submissions[index], true
		}
	}
	return InferenceSubmission{}, false
}

func (a InferenceAttempt) SubmissionSnapshot() []InferenceSubmission {
	if !a.Valid() {
		return nil
	}
	e := a.execution
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]InferenceSubmission, 0)
	for _, submission := range e.submissions {
		if submission.AttemptID == a.ID {
			out = append(out, snapshotSubmission(submission))
		}
	}
	return out
}

func (e *InferenceExecution) Snapshot() InferenceExecutionSnapshot {
	if e == nil {
		return InferenceExecutionSnapshot{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	submissions := make([]InferenceSubmission, len(e.submissions))
	for index, submission := range e.submissions {
		submissions[index] = snapshotSubmission(submission)
	}
	return InferenceExecutionSnapshot{
		Operation:   e.operation,
		Attempts:    e.nextAttempt,
		Submissions: submissions,
		Workflow: func() WorkflowBudgetSnapshot {
			if e.workflow == nil {
				return WorkflowBudgetSnapshot{}
			}
			return e.workflow.SpendSnapshot()
		}(),
	}
}

func (e *InferenceExecution) updateSubmission(id string, update func(*InferenceSubmission)) {
	if e == nil || strings.TrimSpace(id) == "" || update == nil {
		return
	}
	e.mu.Lock()
	for index := range e.submissions {
		if e.submissions[index].ID == id {
			update(&e.submissions[index])
			submission := snapshotSubmission(e.submissions[index])
			workflow := e.workflow
			journal := e.journal
			operationID := e.operation.ID
			e.mu.Unlock()
			if workflow != nil && workflow.spend != nil {
				workflow.spend.SettleSubmission(submission)
			}
			// A per-delta submission update is streaming cost telemetry, not a
			// durability checkpoint. Hand it to the journal's async progress
			// path when available so the write never blocks token delivery, and
			// never let a bookkeeping failure poison the execution or abort the
			// stream — the terminal record (via CompleteAttempt) is the real
			// durability boundary and stays synchronous.
			if journal != nil {
				record := journalSubmission(operationID, submission)
				if progress, ok := journal.(InferenceProgressJournal); ok {
					progress.RecordSubmissionProgress(record)
				} else if err := journal.UpsertSubmission(record); err != nil {
					e.noteJournalDegraded(err)
				}
			}
			return
		}
	}
	e.mu.Unlock()
}

func journalSubmission(operationID string, submission InferenceSubmission) InferenceSubmissionJournalRecord {
	return InferenceSubmissionJournalRecord{
		OperationID:     operationID,
		AttemptID:       submission.AttemptID,
		ID:              submission.ID,
		Ordinal:         submission.Ordinal,
		AttemptOrdinal:  submission.AttemptOrdinal,
		Provider:        submission.Provider,
		Protocol:        submission.Protocol,
		Transport:       submission.Transport,
		Mode:            submission.Mode,
		Reason:          submission.Reason,
		StartedAt:       submission.StartedAt,
		CompletedAt:     submission.CompletedAt,
		Outcome:         submission.Outcome,
		FailureCategory: submission.FailureCategory,
		CostState:       submission.CostState,
		ReportedUsage:   submission.ReportedUsage,
		EstimatedUsage:  submission.EstimatedUsage,
		OutputBytes:     submission.OutputBytes,
	}
}

func snapshotSubmission(submission InferenceSubmission) InferenceSubmission {
	submission.execution = nil
	if submission.ReportedUsage != nil {
		usage := *submission.ReportedUsage
		submission.ReportedUsage = &usage
	}
	if submission.EstimatedUsage != nil {
		usage := *submission.EstimatedUsage
		submission.EstimatedUsage = &usage
	}
	return submission
}
