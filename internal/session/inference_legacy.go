package session

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// NewLegacyInferenceConverter consumes one immutable SQLite snapshot. Call the
// returned converter with an empty table name after all tables to validate and
// emit the workflow/operation/attempt/submission graph in one transaction.
func NewLegacyInferenceConverter() func(string, []map[string]any) ([]persistence.Write, bool, error) {
	tables := make(map[string][]map[string]any)
	return func(table string, rows []map[string]any) ([]persistence.Write, bool, error) {
		if table != "" {
			switch table {
			case "inference_journal_runtimes", "inference_workflows", "inference_operations", "inference_attempts", "inference_submissions":
				tables[table] = rows
				return nil, true, nil
			default:
				return nil, false, nil
			}
		}
		runtimes := make(map[string]InferenceRuntimeRecord)
		workflows := make(map[string]inferenceWorkflowDocument)
		operations := make(map[string]inferenceOperationDocument)
		attempts := make(map[string]inferenceAttemptDocument)
		submissions := make(map[string]providers.InferenceSubmissionJournalRecord)
		synthetic := make(map[string]bool)
		for _, row := range tables["inference_journal_runtimes"] {
			var record InferenceRuntimeRecord
			if err := decodeLegacyInferenceRow(row, &record); err != nil {
				return nil, true, err
			}
			if record.ID == "" {
				return nil, true, legacyInferenceCorrupt("runtime identity is missing")
			}
			if _, ok := runtimes[record.ID]; ok {
				return nil, true, legacyInferenceCorrupt("duplicate runtime identity")
			}
			runtimes[record.ID] = record
		}
		for _, row := range tables["inference_workflows"] {
			var record inferenceWorkflowDocument
			if err := decodeLegacyInferenceRow(row, &record); err != nil {
				return nil, true, err
			}
			if record.ID == "" {
				return nil, true, legacyInferenceCorrupt("workflow identity is missing")
			}
			if _, ok := workflows[record.ID]; ok {
				return nil, true, legacyInferenceCorrupt("duplicate workflow identity")
			}
			record.StartedAt = time.UnixMilli(record.CreatedAt).UTC()
			record.OperationIDs = []string{}
			if err := legacyInferenceBudget(row, &record); err != nil {
				return nil, true, err
			}
			workflows[record.ID] = record
		}
		for _, row := range tables["inference_operations"] {
			var record inferenceOperationDocument
			if err := decodeLegacyInferenceRow(row, &record); err != nil {
				return nil, true, err
			}
			if record.ID == "" {
				return nil, true, legacyInferenceCorrupt("operation identity is missing")
			}
			if _, ok := operations[record.ID]; ok {
				return nil, true, legacyInferenceCorrupt("duplicate operation identity")
			}
			record.AttemptIDs = []string{}
			if record.WorkflowID == "" {
				record.WorkflowID = "iwf-legacy-" + record.ID
				if _, exists := workflows[record.WorkflowID]; exists {
					return nil, true, legacyInferenceCorrupt("synthetic workflow collides with explicit identity")
				}
				workflows[record.WorkflowID] = inferenceWorkflowDocument{InferenceWorkflowJournalRecord: providers.InferenceWorkflowJournalRecord{ID: record.WorkflowID, Profile: record.WorkloadProfile, StartedAt: time.UnixMilli(record.CreatedAt).UTC()}, RuntimeID: record.RuntimeID, WorkspaceScope: record.WorkspaceScope, OwnerID: record.OwnerID, Status: record.Status, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, TerminalAt: record.TerminalAt, Used: make(map[providers.WorkflowBudgetDimension]int64), OperationIDs: []string{}}
				synthetic[record.WorkflowID] = true
			}
			workflow, ok := workflows[record.WorkflowID]
			if !ok || workflow.RuntimeID != record.RuntimeID || workflow.WorkspaceScope != record.WorkspaceScope || workflow.OwnerID != record.OwnerID {
				return nil, true, legacyInferenceCorrupt("operation has no matching workflow owner")
			}
			workflow.OperationIDs = append(workflow.OperationIDs, record.ID)
			workflows[workflow.ID] = workflow
			operations[record.ID] = record
		}
		for _, row := range tables["inference_attempts"] {
			var record inferenceAttemptDocument
			if err := decodeLegacyInferenceRow(row, &record); err != nil {
				return nil, true, err
			}
			operation, ok := operations[record.OperationID]
			if !ok || record.ID == "" || record.Ordinal < 1 {
				return nil, true, legacyInferenceCorrupt("attempt has no matching operation")
			}
			if _, ok := attempts[record.ID]; ok {
				return nil, true, legacyInferenceCorrupt("duplicate attempt identity")
			}
			for _, id := range operation.AttemptIDs {
				if attempts[id].Ordinal == record.Ordinal {
					return nil, true, legacyInferenceCorrupt("duplicate attempt ordinal")
				}
			}
			record.SubmissionIDs = []string{}
			operation.AttemptIDs = append(operation.AttemptIDs, record.ID)
			operations[operation.ID] = operation
			attempts[record.ID] = record
		}
		operationSubmissions := make(map[string]map[int]bool)
		for _, row := range tables["inference_submissions"] {
			var record providers.InferenceSubmissionJournalRecord
			fields := legacyInferenceFields(row)
			for _, column := range []string{"started_at", "completed_at"} {
				stamp, err := legacyInferenceInteger(row, column, 0)
				if err != nil {
					return nil, true, err
				}
				at := time.Time{}
				if stamp != 0 {
					at = time.UnixMilli(stamp).UTC()
				}
				fields[strings.ReplaceAll(column, "_", "")] = at
			}
			for _, kind := range []string{"reported", "estimated"} {
				present, err := legacyInferenceInteger(row, "has_"+kind+"_usage", 0)
				if err != nil {
					return nil, true, err
				}
				if present != 0 {
					usage := make(map[string]any)
					for _, column := range []struct{ source, target string }{{"input_tokens", "InputTokens"}, {"output_tokens", "OutputTokens"}, {"cache_creation", "CacheCreationTokens"}, {"cache_read", "CacheReadTokens"}} {
						value, err := legacyInferenceInteger(row, kind+"_"+column.source, 0)
						if err != nil {
							return nil, true, err
						}
						if value < 0 {
							return nil, true, legacyInferenceCorrupt("negative usage")
						}
						usage[column.target] = value
					}
					unknown, err := legacyInferenceInteger(row, kind+"_cache_unknown", 0)
					if err != nil {
						return nil, true, err
					}
					usage["CacheCreationUnknown"] = unknown != 0
					fields[kind+"usage"] = usage
				}
			}
			if err := decodeLegacyInferenceFields(fields, &record); err != nil {
				return nil, true, err
			}
			attempt, ok := attempts[record.AttemptID]
			if !ok || record.ID == "" || record.OperationID != attempt.OperationID || record.AttemptOrdinal != attempt.Ordinal || record.Ordinal < 1 {
				return nil, true, legacyInferenceCorrupt("submission has no matching attempt")
			}
			if _, ok := submissions[record.ID]; ok {
				return nil, true, legacyInferenceCorrupt("duplicate submission identity")
			}
			if operationSubmissions[record.OperationID] == nil {
				operationSubmissions[record.OperationID] = make(map[int]bool)
			}
			if operationSubmissions[record.OperationID][record.Ordinal] {
				return nil, true, legacyInferenceCorrupt("duplicate submission ordinal")
			}
			operationSubmissions[record.OperationID][record.Ordinal] = true
			attempt.SubmissionIDs = append(attempt.SubmissionIDs, record.ID)
			attempts[attempt.ID] = attempt
			submissions[record.ID] = record
		}
		for id, operation := range operations {
			if operation.ParentOperationID != "" {
				parent, ok := operations[operation.ParentOperationID]
				if !ok || parent.ID == operation.ID || parent.WorkflowID != operation.WorkflowID {
					return nil, true, legacyInferenceCorrupt("operation has no same-workflow parent")
				}
			}
			slices.SortFunc(operation.AttemptIDs, func(a, b string) int { return attempts[a].Ordinal - attempts[b].Ordinal })
			if synthetic[operation.WorkflowID] {
				operation.AttemptLimit = max(operation.AttemptLimit, len(operation.AttemptIDs), 1)
				workflow := workflows[operation.WorkflowID]
				workflow.Used[providers.WorkflowBudgetOperations] = 1
				workflow.Used[providers.WorkflowBudgetAttempts] = int64(len(operation.AttemptIDs))
				workflow.Used[providers.WorkflowBudgetSamePayloadReplays] = int64(max(0, len(operation.AttemptIDs)-1))
				for _, attemptID := range operation.AttemptIDs {
					for _, submissionID := range attempts[attemptID].SubmissionIDs {
						submission := submissions[submissionID]
						workflow.Used[providers.WorkflowBudgetSubmissions]++
						switch submission.CostState {
						case providers.InferenceCostKnown:
							workflow.KnownSubmissions++
							workflow.KnownUsageTokens += journalUsageTokens(journalUsage(submission.ReportedUsage))
						case providers.InferenceCostEstimated:
							workflow.EstimatedSubmissions++
							workflow.EstimatedUsageTokens += journalUsageTokens(journalUsage(submission.EstimatedUsage))
						case providers.InferenceCostUnknownBillable:
							workflow.UnknownBillable++
						}
					}
				}
				workflows[workflow.ID] = workflow
			}
			operations[id] = operation
		}
		var writes []persistence.Write
		add := func(table, id string, value any) error {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			writes = append(writes, persistence.Write{Namespace: documentNamespace(table), Key: id, Value: raw})
			return nil
		}
		for _, runtime := range runtimes {
			if err := add("inference_journal_runtimes", runtime.ID, runtime); err != nil {
				return nil, true, err
			}
		}
		for _, workflow := range workflows {
			if _, ok := runtimes[workflow.RuntimeID]; !ok {
				return nil, true, legacyInferenceCorrupt("workflow runtime is missing")
			}
			slices.SortFunc(workflow.OperationIDs, func(a, b string) int {
				if operations[a].CreatedAt < operations[b].CreatedAt {
					return -1
				}
				if operations[a].CreatedAt > operations[b].CreatedAt {
					return 1
				}
				return strings.Compare(a, b)
			})
			if err := add("inference_workflows_index", inferenceIndexKey(workflow.WorkspaceScope, workflow.Status, workflow.UpdatedAt, workflow.ID), workflow.ID); err != nil {
				return nil, true, err
			}
			if err := add("inference_workflows", workflow.ID, workflow); err != nil {
				return nil, true, err
			}
		}
		for _, operation := range operations {
			if err := add("inference_operations_index", inferenceIndexKey(operation.WorkspaceScope, operation.Status, operation.UpdatedAt, operation.ID), operation.ID); err != nil {
				return nil, true, err
			}
			if err := add("inference_operations", operation.ID, operation); err != nil {
				return nil, true, err
			}
		}
		for _, attempt := range attempts {
			slices.SortFunc(attempt.SubmissionIDs, func(a, b string) int { return submissions[a].Ordinal - submissions[b].Ordinal })
			if err := add("inference_attempts", attempt.ID, attempt); err != nil {
				return nil, true, err
			}
		}
		for _, submission := range submissions {
			if err := add("inference_submissions", submission.ID, submission); err != nil {
				return nil, true, err
			}
		}
		slices.SortFunc(writes, func(a, b persistence.Write) int {
			if n := strings.Compare(a.Namespace, b.Namespace); n != 0 {
				return n
			}
			return strings.Compare(a.Key, b.Key)
		})
		return writes, true, nil
	}
}

func legacyInferenceCorrupt(reason string) error {
	return fmt.Errorf("%w: legacy inference %s", persistence.ErrCorrupt, reason)
}

func legacyInferenceFields(row map[string]any) map[string]any {
	fields := make(map[string]any, len(row))
	for key, value := range row {
		fields[strings.ReplaceAll(key, "_", "")] = value
	}
	fields["profile"] = row["workload_profile"]
	failure := make(map[string]any)
	for _, column := range []struct{ source, target string }{{"failure_origin", "Origin"}, {"failure_category", "Category"}, {"provider_family", "ProviderFamily"}, {"provider_code", "ProviderCode"}, {"http_status", "HTTPStatus"}, {"confidence", "Confidence"}, {"failure_message", "Message"}} {
		if value, ok := row[column.source]; ok {
			failure[column.target] = value
		}
	}
	fields["failure"] = failure
	return fields
}

func decodeLegacyInferenceRow(row map[string]any, target any) error {
	return decodeLegacyInferenceFields(legacyInferenceFields(row), target)
}
func decodeLegacyInferenceFields(fields map[string]any, target any) error {
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode legacy inference: %w", err)
	}
	return nil
}
func legacyInferenceInteger(row map[string]any, column string, fallback int64) (int64, error) {
	value, exists := row[column]
	if !exists {
		return fallback, nil
	}
	number, ok := value.(int64)
	if !ok {
		return 0, legacyInferenceCorrupt("invalid integer column " + column)
	}
	return number, nil
}

func legacyInferenceBudget(row map[string]any, workflow *inferenceWorkflowDocument) error {
	workflow.Used = make(map[providers.WorkflowBudgetDimension]int64)
	fields := []struct {
		column    string
		dimension providers.WorkflowBudgetDimension
		limit     *providers.BudgetLimit
	}{
		{"operations", providers.WorkflowBudgetOperations, &workflow.Budget.MaxOperations}, {"attempts", providers.WorkflowBudgetAttempts, &workflow.Budget.MaxAttempts}, {"submissions", providers.WorkflowBudgetSubmissions, &workflow.Budget.MaxSubmissions}, {"replays", providers.WorkflowBudgetSamePayloadReplays, &workflow.Budget.MaxSamePayloadReplays}, {"transport_switches", providers.WorkflowBudgetTransportSwitches, &workflow.Budget.MaxTransportSwitches}, {"credential_refreshes", providers.WorkflowBudgetCredentialRefresh, &workflow.Budget.MaxCredentialRefreshes}, {"payload_transforms", providers.WorkflowBudgetPayloadTransforms, &workflow.Budget.MaxPayloadTransforms}, {"child_operations", providers.WorkflowBudgetChildOperations, &workflow.Budget.MaxChildOperations}, {"recovery_wait_ms", providers.WorkflowBudgetRecoveryWait, &workflow.Budget.MaxRecoveryWaitMillis}, {"unknown_billable", providers.WorkflowBudgetUnknownBillable, &workflow.Budget.MaxUnknownBillableSubmissions}, {"usage_tokens", providers.WorkflowBudgetUsageTokens, &workflow.Budget.MaxUsageTokens},
	}
	for _, field := range fields {
		limit, err := legacyInferenceInteger(row, "max_"+field.column, -1)
		if err != nil {
			return err
		}
		if limit < -1 {
			return legacyInferenceCorrupt("negative budget limit")
		}
		if limit >= 0 {
			*field.limit = providers.BudgetLimit{Set: true, Value: uint64(limit)}
		}
		used, err := legacyInferenceInteger(row, "used_"+field.column, 0)
		if err != nil {
			return err
		}
		if used < 0 {
			return legacyInferenceCorrupt("negative budget counter")
		}
		if field.dimension != providers.WorkflowBudgetUnknownBillable && field.dimension != providers.WorkflowBudgetUsageTokens {
			workflow.Used[field.dimension] = used
		}
	}
	if workflow.KnownSubmissions < 0 || workflow.EstimatedSubmissions < 0 || workflow.UnknownBillable < 0 || workflow.KnownUsageTokens < 0 || workflow.EstimatedUsageTokens < 0 {
		return legacyInferenceCorrupt("negative workflow usage")
	}
	return nil
}
