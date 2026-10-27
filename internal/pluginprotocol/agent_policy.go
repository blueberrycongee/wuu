package pluginprotocol

const AgentPolicyService = "agent.policy"

// AgentPolicyInput contains observations, not authority to replay external
// effects. Attempt counts retries for the current request, starting at zero.
type AgentPolicyInput struct {
	// CompletedMessages counts durable protocol-complete items, including reasoning-only items.
	CompletedMessages int               `json:"completed_messages,omitempty"`
	MaxAttempts       int               `json:"max_attempts,omitempty"`
	InitialDelayMS    int64             `json:"initial_delay_ms,omitempty"`
	MaxDelayMS        int64             `json:"max_delay_ms,omitempty"`
	RecoveryHint      string            `json:"recovery_hint,omitempty"`
	Phase             string            `json:"phase"`
	ExecutionID       string            `json:"execution_id,omitempty"`
	StepIndex         int               `json:"step_index"`
	Attempt           int               `json:"attempt"`
	Failure           *DriverFailure    `json:"failure,omitempty"`
	PartialOutput     bool              `json:"partial_output,omitempty"`
	ToolWorkStarted   bool              `json:"tool_work_started,omitempty"`
	FinishReason      string            `json:"finish_reason,omitempty"`
	Truncated         bool              `json:"truncated,omitempty"`
	ToolCalls         []ModelToolCallV1 `json:"tool_calls,omitempty"`
}

// AgentPolicyDecision is terminal for one evaluation. Composition providers
// invoke ordered strategies until one decides; errors and cancellation stop
// evaluation. No decision is an error, never permission for a host fallback.
type AgentPolicyDecision struct {
	MaxParallel    int    `json:"max_parallel,omitempty"`
	RecoveryAction string `json:"recovery_action,omitempty"`
	Action         string `json:"action"`
	MaxAttempts    int    `json:"max_attempts,omitempty"`
	DelayMS        int    `json:"delay_ms,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

type DriverTurnPolicyParams struct {
	DriverTurnScope
	Input AgentPolicyInput `json:"input"`
}
