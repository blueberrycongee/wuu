package providers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// BudgetLimit distinguishes an intentionally unlimited dimension from a hard
// zero. Limits are frozen when the workflow is created.
type BudgetLimit struct {
	Set   bool
	Value uint64
}

func LimitedBudget(value uint64) BudgetLimit {
	return BudgetLimit{Set: true, Value: value}
}

// WorkflowBudgetSpec is the provider-neutral spend contract shared by every
// operation in one user-visible workflow. Exact operation/attempt/submission
// totals are always counted even when a profile intentionally leaves their
// hard limit unset.
//
// Same-payload replay accounting is owned by the durable journal alone (see
// prepareAttemptTx in internal/session/inference_journal.go): charging a
// replay requires the failed attempt's terminal evidence, and replays of
// attempts that failed without any provider signal (network origin, no
// response event) are unbillable, so they are admitted without consuming
// MaxSamePayloadReplays. The in-memory budget deliberately does not mirror
// that dimension.
type WorkflowBudgetSpec struct {
	MaxOperations                 BudgetLimit
	MaxAttempts                   BudgetLimit
	MaxSubmissions                BudgetLimit
	MaxSamePayloadReplays         BudgetLimit
	MaxTransportSwitches          BudgetLimit
	MaxCredentialRefreshes        BudgetLimit
	MaxPayloadTransforms          BudgetLimit
	MaxChildOperations            BudgetLimit
	MaxRecoveryWaitMillis         BudgetLimit
	MaxUnknownBillableSubmissions BudgetLimit
	MaxUsageTokens                BudgetLimit
}

type WorkflowBudgetDimension string

const (
	WorkflowBudgetOperations         WorkflowBudgetDimension = "operations"
	WorkflowBudgetAttempts           WorkflowBudgetDimension = "attempts"
	WorkflowBudgetSubmissions        WorkflowBudgetDimension = "submissions"
	WorkflowBudgetSamePayloadReplays WorkflowBudgetDimension = "same_payload_replays"
	WorkflowBudgetTransportSwitches  WorkflowBudgetDimension = "transport_switches"
	WorkflowBudgetCredentialRefresh  WorkflowBudgetDimension = "credential_refreshes"
	WorkflowBudgetPayloadTransforms  WorkflowBudgetDimension = "payload_transforms"
	WorkflowBudgetChildOperations    WorkflowBudgetDimension = "child_operations"
	WorkflowBudgetRecoveryWait       WorkflowBudgetDimension = "recovery_wait_millis"
	WorkflowBudgetUnknownBillable    WorkflowBudgetDimension = "unknown_billable_submissions"
	WorkflowBudgetUsageTokens        WorkflowBudgetDimension = "usage_tokens"
)

// WorkflowBudgetExceededError is an admission failure. It is local and
// terminal for the current automatic recovery decision; callers must not turn
// it into another provider retry.
type WorkflowBudgetExceededError struct {
	WorkflowID string
	Dimension  WorkflowBudgetDimension
	Limit      uint64
	Used       uint64
	Requested  uint64
}

func (e *WorkflowBudgetExceededError) Error() string {
	if e == nil {
		return "inference workflow budget exceeded"
	}
	return fmt.Sprintf("inference workflow budget exceeded for %s: used %d, requested %d, limit %d", e.Dimension, e.Used, e.Requested, e.Limit)
}

// WorkflowCostIndeterminateError prevents a hard token/cost limit from being
// treated as satisfied while one or more physical submissions may be billable
// but have no authoritative or complete estimate.
type WorkflowCostIndeterminateError struct {
	WorkflowID                 string
	UnknownBillableSubmissions uint64
}

func (e *WorkflowCostIndeterminateError) Error() string {
	if e == nil {
		return "inference workflow cost is indeterminate"
	}
	return fmt.Sprintf("inference workflow cost is indeterminate: %d submission(s) may be billable without usage", e.UnknownBillableSubmissions)
}

// InferenceWorkflow is the stable parent identity and shared budget for one
// turn, agent run, or auxiliary job. The private budget pointer is process
// local; the public metadata is safe to persist in the inference journal.
type InferenceWorkflow struct {
	ID        string
	Profile   InferenceWorkloadProfile
	Budget    WorkflowBudgetSpec
	StartedAt time.Time

	mu          sync.Mutex
	spend       *WorkflowBudget
	terminal    bool
	terminalErr error
	outcome     InferenceTerminalOutcome
}

func NewInferenceWorkflow(profile InferenceWorkloadProfile) *InferenceWorkflow {
	if profile == "" {
		profile = InferenceProfileInteractive
	}
	workflow := &InferenceWorkflow{
		ID:        newInferenceID("iwf"),
		Profile:   profile,
		Budget:    WorkflowBudgetSpecForProfile(profile),
		StartedAt: time.Now().UTC(),
	}
	workflow.spend = newWorkflowBudget(workflow.ID, workflow.Budget)
	return workflow
}

func newInferenceWorkflowWithIdentity(id string, profile InferenceWorkloadProfile, spec WorkflowBudgetSpec, startedAt time.Time) *InferenceWorkflow {
	id = strings.TrimSpace(id)
	if id == "" {
		id = newInferenceID("iwf")
	}
	if profile == "" {
		profile = InferenceProfileInteractive
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	workflow := &InferenceWorkflow{ID: id, Profile: profile, Budget: spec, StartedAt: startedAt.UTC()}
	workflow.spend = newWorkflowBudget(workflow.ID, workflow.Budget)
	return workflow
}

func (w *InferenceWorkflow) SpendSnapshot() WorkflowBudgetSnapshot {
	if w == nil || w.spend == nil {
		return WorkflowBudgetSnapshot{}
	}
	return w.spend.Snapshot()
}

type workflowBudgetContextKey struct{}
type inferenceOperationLineageContextKey struct{}

// InferenceOperationLineage is a sequential parent cursor scoped to one
// nested inference flow such as multi-chunk compaction. It is intentionally
// not stored on the workflow: independent operations may run concurrently.
type InferenceOperationLineage struct {
	mu              sync.Mutex
	lastOperationID string
}

// WithInferenceWorkflow binds an existing workflow to nested inference calls.
func WithInferenceWorkflow(ctx context.Context, workflow *InferenceWorkflow) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if workflow == nil {
		return ctx
	}
	return context.WithValue(ctx, workflowBudgetContextKey{}, workflow)
}

// EnsureInferenceWorkflow creates a workflow only when the caller has not
// already provided one. Nested compaction therefore consumes the parent's
// budget instead of minting a fresh allowance.
func EnsureInferenceWorkflow(ctx context.Context, profile InferenceWorkloadProfile) (context.Context, *InferenceWorkflow, bool) {
	if existing := InferenceWorkflowFromContext(ctx); existing != nil {
		return ctx, existing, false
	}
	workflow := NewInferenceWorkflow(profile)
	return WithInferenceWorkflow(ctx, workflow), workflow, true
}

// StartInferenceWorkflow establishes a new product-work boundary even when
// ctx was inherited from a parent agent. Nested inference such as compaction
// should use EnsureInferenceWorkflow instead so it spends the parent's budget.
func StartInferenceWorkflow(ctx context.Context, profile InferenceWorkloadProfile) (context.Context, *InferenceWorkflow) {
	workflow := NewInferenceWorkflow(profile)
	return WithInferenceWorkflow(ctx, workflow), workflow
}

func (w *InferenceWorkflow) admitOperation(operation InferenceOperation) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.terminal {
		if w.terminalErr != nil {
			return w.terminalErr
		}
		return fmt.Errorf("inference workflow %q is already terminal (%s)", w.ID, w.outcome)
	}
	return w.spend.AdmitOperation(operation)
}

// CompleteInferenceWorkflow closes the outer workflow scope. A workflow with
// no prepared operations is terminal only in memory and has no durable row to
// update. Journal failures remain fail-closed in the process-local workflow.
func CompleteInferenceWorkflow(workflow *InferenceWorkflow, journal InferenceJournal, outcome InferenceTerminalOutcome) error {
	if workflow == nil {
		return nil
	}
	workflow.mu.Lock()
	defer workflow.mu.Unlock()
	if workflow.terminal {
		if workflow.outcome == outcome {
			return workflow.terminalErr
		}
		return fmt.Errorf("inference workflow %q already completed as %s", workflow.ID, workflow.outcome)
	}
	workflow.terminal = true
	workflow.outcome = outcome
	if journal == nil || workflow.spend.Snapshot().Operations == 0 {
		return nil
	}
	err := journal.CompleteWorkflow(InferenceWorkflowTerminalRecord{
		WorkflowID: workflow.ID,
		Outcome:    outcome,
		At:         time.Now().UTC(),
	})
	workflow.terminalErr = err
	return err
}

func InferenceWorkflowFromContext(ctx context.Context) *InferenceWorkflow {
	if ctx == nil {
		return nil
	}
	workflow, _ := ctx.Value(workflowBudgetContextKey{}).(*InferenceWorkflow)
	return workflow
}

// BeginInferenceOperationLineage creates a sequential child-operation scope.
// Every operation bound for an attempt under the returned context advances the cursor, so
// the next operation becomes its child. Callers may read LastOperationID to
// connect the next operation outside the nested flow.
func BeginInferenceOperationLineage(ctx context.Context, parentOperationID string) (context.Context, *InferenceOperationLineage) {
	if ctx == nil {
		ctx = context.Background()
	}
	lineage := &InferenceOperationLineage{lastOperationID: strings.TrimSpace(parentOperationID)}
	return context.WithValue(ctx, inferenceOperationLineageContextKey{}, lineage), lineage
}

func (l *InferenceOperationLineage) LastOperationID() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastOperationID
}

func (l *InferenceOperationLineage) advance(operationID string) {
	if l == nil || strings.TrimSpace(operationID) == "" {
		return
	}
	l.mu.Lock()
	l.lastOperationID = strings.TrimSpace(operationID)
	l.mu.Unlock()
}

func inferenceOperationLineageFromContext(ctx context.Context) *InferenceOperationLineage {
	if ctx == nil {
		return nil
	}
	lineage, _ := ctx.Value(inferenceOperationLineageContextKey{}).(*InferenceOperationLineage)
	return lineage
}

type WorkflowBudgetSnapshot struct {
	WorkflowID                 string
	Spec                       WorkflowBudgetSpec
	Operations                 uint64
	Attempts                   uint64
	Submissions                uint64
	TransportSwitches          uint64
	CredentialRefreshes        uint64
	PayloadTransforms          uint64
	ChildOperations            uint64
	RecoveryWaitMillis         uint64
	KnownSubmissions           uint64
	EstimatedSubmissions       uint64
	UnknownBillableSubmissions uint64
	KnownUsage                 TokenUsage
	EstimatedUsage             TokenUsage
}

type workflowOperationSpend struct {
	parentID string
	attempts map[string]struct{}
}

type workflowSubmissionSpend struct {
	state     InferenceCostState
	reported  *TokenUsage
	estimated *TokenUsage
}

// WorkflowBudget is the in-process admission mirror of the durable journal.
// Every mutation is ID-keyed and idempotent so provider callbacks can safely
// report late usage without double charging. It tracks the dimensions whose
// evidence is available in process; same-payload replay accounting is not
// mirrored here because its charge decision needs the failed attempt's
// terminal evidence, which only the durable journal has.
type WorkflowBudget struct {
	mu sync.Mutex

	workflowID  string
	spec        WorkflowBudgetSpec
	snapshot    WorkflowBudgetSnapshot
	operations  map[string]*workflowOperationSpend
	submissions map[string]workflowSubmissionSpend
	recoveries  map[string]RecoveryPlan
}

func newWorkflowBudget(workflowID string, spec WorkflowBudgetSpec) *WorkflowBudget {
	return &WorkflowBudget{
		workflowID:  workflowID,
		spec:        spec,
		snapshot:    WorkflowBudgetSnapshot{WorkflowID: workflowID, Spec: spec},
		operations:  make(map[string]*workflowOperationSpend),
		submissions: make(map[string]workflowSubmissionSpend),
		recoveries:  make(map[string]RecoveryPlan),
	}
}

func (b *WorkflowBudget) AdmitOperation(operation InferenceOperation) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.operations[operation.ID]; ok {
		return nil
	}
	if err := b.checkCostLocked(); err != nil {
		return err
	}
	if err := b.checkLocked(WorkflowBudgetOperations, b.spec.MaxOperations, b.snapshot.Operations, 1); err != nil {
		return err
	}
	childDelta := uint64(0)
	if strings.TrimSpace(operation.ParentOperationID) != "" {
		parent, ok := b.operations[operation.ParentOperationID]
		if !ok || parent == nil {
			return fmt.Errorf("parent inference operation %q is not registered in workflow %q", operation.ParentOperationID, b.workflowID)
		}
		if err := b.checkLocked(WorkflowBudgetChildOperations, b.spec.MaxChildOperations, b.snapshot.ChildOperations, 1); err != nil {
			return err
		}
		childDelta = 1
	}
	b.operations[operation.ID] = &workflowOperationSpend{
		parentID: strings.TrimSpace(operation.ParentOperationID),
		attempts: make(map[string]struct{}),
	}
	b.snapshot.Operations++
	b.snapshot.ChildOperations += childDelta
	return nil
}

func (b *WorkflowBudget) AdmitAttempt(operation InferenceOperation, attemptID string, ordinal int) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	spend, ok := b.operations[operation.ID]
	if !ok {
		return fmt.Errorf("inference operation %q is not registered in workflow %q", operation.ID, b.workflowID)
	}
	if _, ok := spend.attempts[attemptID]; ok {
		return nil
	}
	if err := b.checkCostLocked(); err != nil {
		return err
	}
	if operation.AttemptLimit > 0 && uint64(ordinal) > uint64(operation.AttemptLimit) {
		return &WorkflowBudgetExceededError{
			WorkflowID: b.workflowID, Dimension: WorkflowBudgetAttempts,
			Limit: uint64(operation.AttemptLimit), Used: uint64(len(spend.attempts)), Requested: 1,
		}
	}
	if err := b.checkLocked(WorkflowBudgetAttempts, b.spec.MaxAttempts, b.snapshot.Attempts, 1); err != nil {
		return err
	}
	spend.attempts[attemptID] = struct{}{}
	b.snapshot.Attempts++
	return nil
}

func (b *WorkflowBudget) AdmitSubmission(submissionID string) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.submissions[submissionID]; ok {
		return nil
	}
	if err := b.checkCostLocked(); err != nil {
		return err
	}
	if err := b.checkLocked(WorkflowBudgetSubmissions, b.spec.MaxSubmissions, b.snapshot.Submissions, 1); err != nil {
		return err
	}
	if err := b.checkLocked(WorkflowBudgetUnknownBillable, b.spec.MaxUnknownBillableSubmissions, b.snapshot.UnknownBillableSubmissions, 1); err != nil {
		return err
	}
	b.submissions[submissionID] = workflowSubmissionSpend{state: InferenceCostUnknownBillable}
	b.snapshot.Submissions++
	b.snapshot.UnknownBillableSubmissions++
	return nil
}

func (b *WorkflowBudget) SettleSubmission(submission InferenceSubmission) {
	if b == nil || strings.TrimSpace(submission.ID) == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	previous, ok := b.submissions[submission.ID]
	if !ok {
		return
	}
	b.removeCostLocked(previous)
	next := workflowSubmissionSpend{
		state:     submission.CostState,
		reported:  cloneTokenUsage(submission.ReportedUsage),
		estimated: cloneTokenUsage(submission.EstimatedUsage),
	}
	b.addCostLocked(next)
	b.submissions[submission.ID] = next
}

func (b *WorkflowBudget) AdmitRecoveryAttempt(
	operation InferenceOperation,
	attemptID string,
	nextAttemptID string,
	nextOrdinal int,
	plan RecoveryPlan,
	retryAt time.Time,
	persist func() error,
) error {
	if b == nil {
		if persist != nil {
			return persist()
		}
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	attemptID = strings.TrimSpace(attemptID)
	spend, ok := b.operations[operation.ID]
	if !ok {
		return fmt.Errorf("inference operation %q is not registered in workflow %q", operation.ID, b.workflowID)
	}
	if _, ok := spend.attempts[attemptID]; !ok {
		return fmt.Errorf("inference attempt %q is not registered in operation %q", attemptID, operation.ID)
	}
	if existing, ok := b.recoveries[attemptID]; ok {
		_, attemptExists := spend.attempts[nextAttemptID]
		if existing.Action == plan.Action && attemptExists {
			return nil
		}
		return fmt.Errorf("attempt %q already reserved recovery %q", attemptID, existing.Action)
	}
	if _, ok := spend.attempts[nextAttemptID]; ok {
		return fmt.Errorf("attempt %q exists without recovery from %q", nextAttemptID, attemptID)
	}
	if err := b.checkCostLocked(); err != nil {
		return err
	}
	if operation.AttemptLimit > 0 && uint64(nextOrdinal) > uint64(operation.AttemptLimit) {
		return &WorkflowBudgetExceededError{
			WorkflowID: b.workflowID, Dimension: WorkflowBudgetAttempts,
			Limit: uint64(operation.AttemptLimit), Used: uint64(len(spend.attempts)), Requested: 1,
		}
	}
	if err := b.checkLocked(WorkflowBudgetAttempts, b.spec.MaxAttempts, b.snapshot.Attempts, 1); err != nil {
		return err
	}
	dimension, limit, used := b.recoveryDimensionLocked(plan.Action)
	if dimension != "" {
		if err := b.checkLocked(dimension, limit, used, 1); err != nil {
			return err
		}
	}
	waitMillis := uint64(0)
	if !retryAt.IsZero() {
		delay := time.Until(retryAt)
		if delay > 0 {
			waitMillis = uint64((delay + time.Millisecond - 1) / time.Millisecond)
		}
	}
	if err := b.checkLocked(WorkflowBudgetRecoveryWait, b.spec.MaxRecoveryWaitMillis, b.snapshot.RecoveryWaitMillis, waitMillis); err != nil {
		return err
	}
	if persist != nil {
		if err := persist(); err != nil {
			return err
		}
	}
	spend.attempts[nextAttemptID] = struct{}{}
	b.snapshot.Attempts++
	b.recoveries[attemptID] = plan
	switch plan.Action {
	case RecoverySwitchTransport:
		b.snapshot.TransportSwitches++
	case RecoveryRefreshAuth:
		b.snapshot.CredentialRefreshes++
	case RecoveryTransformPayload:
		b.snapshot.PayloadTransforms++
	}
	b.snapshot.RecoveryWaitMillis += waitMillis
	return nil
}

func (b *WorkflowBudget) Snapshot() WorkflowBudgetSnapshot {
	if b == nil {
		return WorkflowBudgetSnapshot{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshot
}

func (b *WorkflowBudget) recoveryDimensionLocked(action RecoveryActionKind) (WorkflowBudgetDimension, BudgetLimit, uint64) {
	switch action {
	case RecoverySwitchTransport:
		return WorkflowBudgetTransportSwitches, b.spec.MaxTransportSwitches, b.snapshot.TransportSwitches
	case RecoveryRefreshAuth:
		return WorkflowBudgetCredentialRefresh, b.spec.MaxCredentialRefreshes, b.snapshot.CredentialRefreshes
	case RecoveryTransformPayload:
		return WorkflowBudgetPayloadTransforms, b.spec.MaxPayloadTransforms, b.snapshot.PayloadTransforms
	default:
		return "", BudgetLimit{}, 0
	}
}

func (b *WorkflowBudget) checkCostLocked() error {
	if !b.spec.MaxUsageTokens.Set {
		return nil
	}
	if b.snapshot.UnknownBillableSubmissions > 0 {
		return &WorkflowCostIndeterminateError{
			WorkflowID:                 b.workflowID,
			UnknownBillableSubmissions: b.snapshot.UnknownBillableSubmissions,
		}
	}
	used := workflowUsageTokens(b.snapshot.KnownUsage) + workflowUsageTokens(b.snapshot.EstimatedUsage)
	return b.checkLocked(WorkflowBudgetUsageTokens, b.spec.MaxUsageTokens, used, 0)
}

func (b *WorkflowBudget) checkLocked(dimension WorkflowBudgetDimension, limit BudgetLimit, used, requested uint64) error {
	if !limit.Set || (used <= limit.Value && requested <= limit.Value-used) {
		return nil
	}
	return &WorkflowBudgetExceededError{
		WorkflowID: b.workflowID,
		Dimension:  dimension,
		Limit:      limit.Value,
		Used:       used,
		Requested:  requested,
	}
}

func (b *WorkflowBudget) removeCostLocked(cost workflowSubmissionSpend) {
	switch cost.state {
	case InferenceCostKnown:
		if b.snapshot.KnownSubmissions > 0 {
			b.snapshot.KnownSubmissions--
		}
		subtractTokenUsage(&b.snapshot.KnownUsage, cost.reported)
	case InferenceCostEstimated:
		if b.snapshot.EstimatedSubmissions > 0 {
			b.snapshot.EstimatedSubmissions--
		}
		subtractTokenUsage(&b.snapshot.EstimatedUsage, cost.estimated)
	default:
		if b.snapshot.UnknownBillableSubmissions > 0 {
			b.snapshot.UnknownBillableSubmissions--
		}
	}
}

func (b *WorkflowBudget) addCostLocked(cost workflowSubmissionSpend) {
	switch cost.state {
	case InferenceCostKnown:
		b.snapshot.KnownSubmissions++
		if cost.reported != nil {
			addTokenUsage(&b.snapshot.KnownUsage, *cost.reported)
		}
	case InferenceCostEstimated:
		b.snapshot.EstimatedSubmissions++
		if cost.estimated != nil {
			addTokenUsage(&b.snapshot.EstimatedUsage, *cost.estimated)
		}
	default:
		b.snapshot.UnknownBillableSubmissions++
	}
}

func cloneTokenUsage(usage *TokenUsage) *TokenUsage {
	if usage == nil {
		return nil
	}
	copyUsage := *usage
	return &copyUsage
}

func subtractTokenUsage(total *TokenUsage, usage *TokenUsage) {
	if total == nil || usage == nil {
		return
	}
	total.InputTokens -= usage.InputTokens
	total.OutputTokens -= usage.OutputTokens
	total.CacheCreationTokens -= usage.CacheCreationTokens
	total.CacheReadTokens -= usage.CacheReadTokens
	if total.InputTokens < 0 || total.OutputTokens < 0 || total.CacheCreationTokens < 0 || total.CacheReadTokens < 0 {
		panic("providers: workflow usage accounting underflow")
	}
}

func workflowUsageTokens(usage TokenUsage) uint64 {
	total := usage.InputTokens + usage.OutputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
	if total <= 0 {
		return 0
	}
	return uint64(total)
}

func IsWorkflowBudgetError(err error) bool {
	var exceeded *WorkflowBudgetExceededError
	var indeterminate *WorkflowCostIndeterminateError
	return errors.As(err, &exceeded) || errors.As(err, &indeterminate)
}
