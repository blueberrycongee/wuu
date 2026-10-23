package session

import (
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type inferenceWorkflowDocument struct {
	providers.InferenceWorkflowJournalRecord
	RuntimeID, WorkspaceScope, OwnerID, Status              string
	CreatedAt, UpdatedAt, TerminalAt                        int64
	Used                                                    map[providers.WorkflowBudgetDimension]int64
	KnownSubmissions, EstimatedSubmissions, UnknownBillable int64
	KnownUsageTokens, EstimatedUsageTokens                  int64
	OperationIDs                                            []string
}

type inferenceOperationDocument struct {
	providers.InferenceOperation
	RuntimeID, WorkspaceScope, OwnerID, RequestHash, Status string
	CreatedAt, UpdatedAt, TerminalAt                        int64
	TerminalOutcome                                         providers.InferenceTerminalOutcome
	RecoveryAction                                          providers.RecoveryActionKind
	Failure                                                 providers.InferenceJournalFailure
	AttemptIDs                                              []string
}

type inferenceAttemptDocument struct {
	ID, OperationID, RequestHash, Phase                                  string
	Ordinal                                                              int
	PreparedAt, DispatchingAt, SentAt, FirstEventAt, TerminalAt, RetryAt int64
	TerminalOutcome                                                      providers.InferenceTerminalOutcome
	RecoveryAction                                                       providers.RecoveryActionKind
	Failure                                                              providers.InferenceJournalFailure
	SubmissionIDs                                                        []string
}

func requireInferenceDocument[T any](tx *persistence.Tx, table, id string) (T, error) {
	var record T
	found, err := tx.Get(documentNamespace(table), id, &record)
	if err == nil && !found {
		err = fmt.Errorf("%s %q not found", table, id)
	}
	return record, err
}

func workflowDocumentLimit(spec providers.WorkflowBudgetSpec, dimension providers.WorkflowBudgetDimension) providers.BudgetLimit {
	switch dimension {
	case providers.WorkflowBudgetOperations:
		return spec.MaxOperations
	case providers.WorkflowBudgetAttempts:
		return spec.MaxAttempts
	case providers.WorkflowBudgetSubmissions:
		return spec.MaxSubmissions
	case providers.WorkflowBudgetSamePayloadReplays:
		return spec.MaxSamePayloadReplays
	case providers.WorkflowBudgetTransportSwitches:
		return spec.MaxTransportSwitches
	case providers.WorkflowBudgetCredentialRefresh:
		return spec.MaxCredentialRefreshes
	case providers.WorkflowBudgetPayloadTransforms:
		return spec.MaxPayloadTransforms
	case providers.WorkflowBudgetChildOperations:
		return spec.MaxChildOperations
	case providers.WorkflowBudgetRecoveryWait:
		return spec.MaxRecoveryWaitMillis
	case providers.WorkflowBudgetUnknownBillable:
		return spec.MaxUnknownBillableSubmissions
	case providers.WorkflowBudgetUsageTokens:
		return spec.MaxUsageTokens
	default:
		panic("session: unknown workflow budget dimension")
	}
}

func reserveWorkflowDocument(tx *persistence.Tx, id string, dimension providers.WorkflowBudgetDimension, delta, stamp int64) error {
	if delta < 0 {
		panic("session: negative workflow budget reservation")
	}
	workflow, err := requireInferenceDocument[inferenceWorkflowDocument](tx, "inference_workflows", id)
	if err != nil {
		return err
	}
	if workflow.Status != "active" {
		return fmt.Errorf("workflow %q is already terminal (%s)", id, workflow.Status)
	}
	used := workflow.Used[dimension]
	limit := workflowDocumentLimit(workflow.Budget, dimension)
	if limit.Set && (uint64(used) > limit.Value || uint64(delta) > limit.Value-uint64(used)) {
		return &providers.WorkflowBudgetExceededError{WorkflowID: id, Dimension: dimension, Limit: limit.Value, Used: uint64(used), Requested: uint64(delta)}
	}
	if delta > (1<<63-1)-used {
		return errors.New("workflow budget counter overflow")
	}
	if workflow.Used == nil {
		workflow.Used = make(map[providers.WorkflowBudgetDimension]int64)
	}
	workflow.Used[dimension], workflow.UpdatedAt = used+delta, stamp
	return putInferenceWorkflow(tx, workflow)
}

func assertWorkflowDocumentCost(tx *persistence.Tx, id string) error {
	workflow, err := requireInferenceDocument[inferenceWorkflowDocument](tx, "inference_workflows", id)
	if err != nil {
		return err
	}
	if workflow.Status != "active" {
		return fmt.Errorf("workflow %q is already terminal (%s)", id, workflow.Status)
	}
	limit := workflow.Budget.MaxUsageTokens
	if !limit.Set {
		return nil
	}
	if workflow.UnknownBillable > 0 {
		return &providers.WorkflowCostIndeterminateError{WorkflowID: id, UnknownBillableSubmissions: uint64(workflow.UnknownBillable)}
	}
	used := uint64(workflow.KnownUsageTokens) + uint64(workflow.EstimatedUsageTokens)
	if used > limit.Value {
		return &providers.WorkflowBudgetExceededError{WorkflowID: id, Dimension: providers.WorkflowBudgetUsageTokens, Limit: limit.Value, Used: used}
	}
	return nil
}

func applyWorkflowDocumentCost(tx *persistence.Tx, id string, old, next providers.InferenceSubmissionJournalRecord, stamp int64) error {
	type contribution struct{ known, estimated, unknown, knownTokens, estimatedTokens int64 }
	contribute := func(record providers.InferenceSubmissionJournalRecord) contribution {
		switch record.CostState {
		case providers.InferenceCostKnown:
			return contribution{known: 1, knownTokens: journalUsageTokens(journalUsage(record.ReportedUsage))}
		case providers.InferenceCostEstimated:
			return contribution{estimated: 1, estimatedTokens: journalUsageTokens(journalUsage(record.EstimatedUsage))}
		case providers.InferenceCostUnknownBillable:
			return contribution{unknown: 1}
		default:
			return contribution{}
		}
	}
	before, after := contribute(old), contribute(next)
	if before == after {
		return nil
	}
	workflow, err := requireInferenceDocument[inferenceWorkflowDocument](tx, "inference_workflows", id)
	if err != nil {
		return err
	}
	oldUnknown := workflow.UnknownBillable
	workflow.KnownSubmissions += after.known - before.known
	workflow.EstimatedSubmissions += after.estimated - before.estimated
	workflow.UnknownBillable += after.unknown - before.unknown
	workflow.KnownUsageTokens += after.knownTokens - before.knownTokens
	workflow.EstimatedUsageTokens += after.estimatedTokens - before.estimatedTokens
	if workflow.KnownSubmissions < 0 || workflow.EstimatedSubmissions < 0 || workflow.UnknownBillable < 0 || workflow.KnownUsageTokens < 0 || workflow.EstimatedUsageTokens < 0 {
		return errors.New("workflow cost accounting underflow")
	}
	limit := workflow.Budget.MaxUnknownBillableSubmissions
	if limit.Set && uint64(workflow.UnknownBillable) > limit.Value {
		return &providers.WorkflowBudgetExceededError{WorkflowID: id, Dimension: providers.WorkflowBudgetUnknownBillable, Limit: limit.Value, Used: uint64(oldUnknown), Requested: uint64(after.unknown - before.unknown)}
	}
	workflow.UpdatedAt = stamp
	return putInferenceWorkflow(tx, workflow)
}

func mergeInferenceDocumentUsage(left, right *providers.TokenUsage) *providers.TokenUsage {
	if left == nil && right == nil {
		return nil
	}
	merged := mergeJournalUsage(journalUsage(left), journalUsage(right))
	return &providers.TokenUsage{InputTokens: merged.input, OutputTokens: merged.output,
		CacheCreationTokens: merged.cacheCreation, CacheReadTokens: merged.cacheRead, CacheCreationUnknown: merged.cacheUnknown != 0}
}
