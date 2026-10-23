package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/providers"
	"strings"
	"time"
)

const inferenceJournalRetention = 30 * 24 * time.Hour

const (
	inferenceJournalHeartbeatInterval = 3 * time.Second
	inferenceJournalStaleAfter        = 12 * time.Second
	// inferenceJournalProgressFlushInterval bounds how often coalesced streaming
	// submission estimates reach disk. It only keeps the durable cost estimate
	// roughly current; terminal accuracy is guaranteed by the synchronous flush
	// barrier at CompleteAttempt / CompleteOperation, not by this cadence.
	inferenceJournalProgressFlushInterval = 250 * time.Millisecond
)

func InferenceJournalRecoveryInterval() time.Duration {
	return inferenceJournalStaleAfter
}

func InferenceRuntimeProcessAlive(pid int) bool {
	return inferenceRuntimeProcessAlive(pid)
}

func normalizeInferenceAttemptJournalRecord(record *providers.InferenceAttemptJournalRecord) error {
	record.OperationID = strings.TrimSpace(record.OperationID)
	record.WorkflowID = strings.TrimSpace(record.WorkflowID)
	record.AttemptID = strings.TrimSpace(record.AttemptID)
	record.RequestHash = strings.TrimSpace(record.RequestHash)
	if record.OperationID == "" || record.AttemptID == "" || !validInferenceRequestHash(record.RequestHash) || record.Ordinal < 1 {
		return errors.New("prepare inference attempt: operation, attempt, ordinal, and request hash are required")
	}
	return nil
}

func inferenceJournalContextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func normalizeInferenceWorkflowRecord(record providers.InferenceWorkflowJournalRecord, operation providers.InferenceOperation, at time.Time) providers.InferenceWorkflowJournalRecord {
	emptyRecord := strings.TrimSpace(record.ID) == ""
	if emptyRecord {
		record.ID = strings.TrimSpace(operation.WorkflowID)
		if record.ID == "" {
			record.ID = "iwf-" + operation.ID
		}
	}
	if record.Profile == "" {
		record.Profile = operation.WorkloadProfile
	}
	if record.Profile == "" {
		record.Profile = providers.InferenceProfileInteractive
	}
	if emptyRecord {
		record.Budget = providers.WorkflowBudgetSpecForProfile(record.Profile)
	}
	if record.StartedAt.IsZero() {
		record.StartedAt = at
	}
	return record
}

type inferenceWorkflowLimitColumns struct {
	operations, attempts, submissions, replays int64
	transportSwitches, credentialRefreshes     int64
	payloadTransforms, childOperations         int64
	recoveryWaitMillis, unknownBillable        int64
	usageTokens                                int64
}

func inferenceWorkflowLimits(spec providers.WorkflowBudgetSpec) (inferenceWorkflowLimitColumns, error) {
	convert := func(name string, limit providers.BudgetLimit) (int64, error) {
		if !limit.Set {
			return -1, nil
		}
		if limit.Value > uint64(1<<63-1) {
			return 0, fmt.Errorf("workflow budget %s exceeds signed integer range", name)
		}
		return int64(limit.Value), nil
	}
	var out inferenceWorkflowLimitColumns
	var err error
	fields := []struct {
		name  string
		limit providers.BudgetLimit
		dest  *int64
	}{
		{"operations", spec.MaxOperations, &out.operations},
		{"attempts", spec.MaxAttempts, &out.attempts},
		{"submissions", spec.MaxSubmissions, &out.submissions},
		{"replays", spec.MaxSamePayloadReplays, &out.replays},
		{"transport switches", spec.MaxTransportSwitches, &out.transportSwitches},
		{"credential refreshes", spec.MaxCredentialRefreshes, &out.credentialRefreshes},
		{"payload transforms", spec.MaxPayloadTransforms, &out.payloadTransforms},
		{"child operations", spec.MaxChildOperations, &out.childOperations},
		{"recovery wait", spec.MaxRecoveryWaitMillis, &out.recoveryWaitMillis},
		{"unknown billable", spec.MaxUnknownBillableSubmissions, &out.unknownBillable},
		{"usage tokens", spec.MaxUsageTokens, &out.usageTokens},
	}
	for _, field := range fields {
		*field.dest, err = convert(field.name, field.limit)
		if err != nil {
			return inferenceWorkflowLimitColumns{}, err
		}
	}
	return out, nil
}

func journalUsageTokens(usage inferenceJournalUsage) int64 {
	if usage.present == 0 {
		return 0
	}
	return int64(usage.input + usage.output + usage.cacheCreation + usage.cacheRead)
}

func recoveryWorkflowDimension(action providers.RecoveryActionKind) providers.WorkflowBudgetDimension {
	switch action {
	case providers.RecoverySwitchTransport:
		return providers.WorkflowBudgetTransportSwitches
	case providers.RecoveryRefreshAuth:
		return providers.WorkflowBudgetCredentialRefresh
	case providers.RecoveryTransformPayload:
		return providers.WorkflowBudgetPayloadTransforms
	default:
		return ""
	}
}

type InferenceCrashRecovery struct {
	OperationID   string
	AttemptID     string
	OwnerID       string
	Kind          providers.InferenceOperationKind
	Profile       providers.InferenceWorkloadProfile
	PriorPhase    string
	PriorOutcome  string
	PriorRecovery providers.RecoveryActionKind
	Outcome       providers.InferenceTerminalOutcome
	Action        providers.RecoveryActionKind
}

func recoveryCanCreateAnotherAttempt(action providers.RecoveryActionKind) bool {
	switch action {
	case providers.RecoveryReplaySame, providers.RecoveryWaitThenReplay,
		providers.RecoveryTransformPayload, providers.RecoveryRefreshAuth,
		providers.RecoverySwitchTransport, providers.RecoveryRescheduleSafe:
		return true
	default:
		return false
	}
}

type inferenceJournalUsage struct {
	input, output, cacheCreation, cacheRead int
	cacheUnknown, present                   int
}

func journalUsage(usage *providers.TokenUsage) inferenceJournalUsage {
	if usage == nil {
		return inferenceJournalUsage{}
	}
	unknown := 0
	if usage.CacheCreationUnknown {
		unknown = 1
	}
	return inferenceJournalUsage{
		input:         usage.InputTokens,
		output:        usage.OutputTokens,
		cacheCreation: usage.CacheCreationTokens,
		cacheRead:     usage.CacheReadTokens,
		cacheUnknown:  unknown,
		present:       1,
	}
}

func inferenceCostRank(state string) int {
	switch providers.InferenceCostState(state) {
	case providers.InferenceCostKnown:
		return 3
	case providers.InferenceCostEstimated:
		return 2
	default:
		return 1
	}
}

func mergeJournalUsage(left, right inferenceJournalUsage) inferenceJournalUsage {
	if left.present == 0 {
		return right
	}
	if right.present == 0 {
		return left
	}
	return inferenceJournalUsage{
		input:         maxInt(left.input, right.input),
		output:        maxInt(left.output, right.output),
		cacheCreation: maxInt(left.cacheCreation, right.cacheCreation),
		cacheRead:     maxInt(left.cacheRead, right.cacheRead),
		cacheUnknown:  maxInt(left.cacheUnknown, right.cacheUnknown),
		present:       1,
	}
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func journalTime(at time.Time) int64 {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return at.UTC().UnixMilli()
}

func optionalJournalTime(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.UTC().UnixMilli()
}

func journalText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if maxBytes > 0 && len(value) > maxBytes {
		value = value[:maxBytes]
	}
	return value
}

func validInferenceRequestHash(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func newInferenceRuntimeID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("session: generate inference runtime id: %v", err))
	}
	return "irt-" + hex.EncodeToString(raw[:])
}
