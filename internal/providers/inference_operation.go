package providers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// InferenceOperationKind identifies the product operation that owns one model
// response. It is deliberately provider-neutral: callers use it for
// completion, visibility, and recovery policy without teaching provider
// clients about app-server or agent types.
type InferenceOperationKind string

const (
	InferenceOperationAgentRound InferenceOperationKind = "agent_round"
	InferenceOperationTitle      InferenceOperationKind = "title"
	InferenceOperationCompaction InferenceOperationKind = "compaction"
	InferenceOperationReview     InferenceOperationKind = "review"
	InferenceOperationMemory     InferenceOperationKind = "memory"
	InferenceOperationHookPrompt InferenceOperationKind = "hook_prompt"
	InferenceOperationAuxiliary  InferenceOperationKind = "auxiliary"
)

// InferenceWorkloadProfile is the centrally named user-experience class for an
// operation. It is not a retry implementation: the retry engine resolves the
// profile into limits and scheduling policy.
type InferenceWorkloadProfile string

const (
	InferenceProfileInteractive          InferenceWorkloadProfile = "interactive"
	InferenceProfileContinuationCritical InferenceWorkloadProfile = "continuation_critical"
	InferenceProfileBackgroundAgent      InferenceWorkloadProfile = "background_agent"
	InferenceProfileBestEffort           InferenceWorkloadProfile = "best_effort"
)

// InferenceOperation is metadata-only and must never be serialized into a
// provider request body. One operation represents one reasoning round or one
// auxiliary model job. Retries retain the ID; a payload mutation increments
// PayloadVersion.
type InferenceOperation struct {
	ID                string
	WorkflowID        string
	ParentOperationID string
	Kind              InferenceOperationKind
	WorkloadProfile   InferenceWorkloadProfile
	PayloadVersion    int
	// AttemptLimit is frozen with the operation so later configuration changes
	// cannot expand an in-flight operation's local recovery allowance.
	AttemptLimit int
}

// NewInferenceOperation creates a normalized operation with a stable random
// identifier. Failure to obtain system randomness is fatal because silently
// reusing an ID would corrupt retry correlation and future durable journals.
func NewInferenceOperation(kind InferenceOperationKind, profile InferenceWorkloadProfile) InferenceOperation {
	return EnsureInferenceOperation(InferenceOperation{Kind: kind, WorkloadProfile: profile}, kind, profile)
}

// EnsureInferenceOperation fills missing metadata while preserving an
// existing operation ID and payload version across retries and fallbacks.
func EnsureInferenceOperation(op InferenceOperation, fallbackKind InferenceOperationKind, fallbackProfile InferenceWorkloadProfile) InferenceOperation {
	if strings.TrimSpace(op.ID) == "" {
		op.ID = newInferenceID("iop")
	}
	if op.Kind == "" {
		op.Kind = fallbackKind
	}
	if op.Kind == "" {
		op.Kind = InferenceOperationAuxiliary
	}
	if op.WorkloadProfile == "" {
		op.WorkloadProfile = fallbackProfile
	}
	if op.WorkloadProfile == "" {
		op.WorkloadProfile = InferenceProfileInteractive
	}
	if op.PayloadVersion < 1 {
		op.PayloadVersion = 1
	}
	if op.AttemptLimit < 1 {
		op.AttemptLimit = NormalizeRetryConfig(RetryConfigForProfile(op.WorkloadProfile)).MaxRetries + 1
	}
	return op
}

// AttemptID returns the stable attempt identifier for one 1-based execution
// attempt inside the operation. The operation ID provides global uniqueness;
// the ordinal stays readable in diagnostics.
func (op InferenceOperation) AttemptID(ordinal int) string {
	if ordinal < 1 {
		panic("providers: inference attempt ordinal must be positive")
	}
	op = EnsureInferenceOperation(op, InferenceOperationAuxiliary, InferenceProfileInteractive)
	return fmt.Sprintf("%s-a%d", op.ID, ordinal)
}

func newInferenceID(prefix string) string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("providers: generate inference id: %v", err))
	}
	return prefix + "-" + hex.EncodeToString(raw[:])
}
