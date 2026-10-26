package pluginprotocol

import "encoding/json"

const EngineGatewayService = "host.engine.session"

// EngineBinding transports only data. References identify callback resources
// owned by a captured execution; possession alone does not authorize calls.
type EngineBinding struct {
	ThreadID       string            `json:"thread_id"`
	RootDir        string            `json:"root_dir"`
	Model          string            `json:"model,omitempty"`
	Effort         string            `json:"effort,omitempty"`
	Speed          string            `json:"speed,omitempty"`
	ModelOptions   map[string]string `json:"model_options,omitempty"`
	PermissionMode string            `json:"permission_mode,omitempty"`
	Instructions   string            `json:"instructions,omitempty"`
	MCPServers     []EngineMCPServer `json:"mcp_servers,omitempty"`
	ExternalRef    string            `json:"external_ref,omitempty"`
	GatewayID      string            `json:"gateway_id"`
}
type EngineMCPServer struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type EngineSessionParams struct {
	SessionID string `json:"session_id"`
}
type EngineRunParams struct {
	EngineSessionParams
	Messages []DriverMessageV1 `json:"messages"`
}
type EngineRunResult struct {
	// Result preserves the complete versioned loop receipt, including canonical
	// transcript and compaction addresses. Errors never erase a partial result.
	Result  EngineResultV1 `json:"result"`
	Failure *DriverFailure `json:"failure,omitempty"`
}
type EngineInterruptParams struct {
	EngineSessionParams
	Reason string `json:"reason"`
}
type EngineSteerParams struct {
	EngineSessionParams
	Message DriverMessageV1 `json:"message"`
}
type EngineGatewayParams struct {
	GatewayID string                    `json:"gateway_id"`
	Event     *EngineEventV1            `json:"event,omitempty"`
	Reference string                    `json:"reference,omitempty"`
	Pending   bool                      `json:"pending,omitempty"`
	Approval  *EngineApprovalRequestV1  `json:"approval,omitempty"`
	Input     *EngineUserInputRequestV1 `json:"input,omitempty"`
	Messages  []DriverMessageV1         `json:"messages,omitempty"`
}

// EngineResultV1 preserves canonical history, compaction positions and driver
// receipts. A partial result remains valid when EngineRunResult also has failure.
type EngineResultV1 struct {
	Version                int                    `json:"version"`
	Content                string                 `json:"content,omitempty"`
	NewMessages            []DriverMessageV1      `json:"new_messages"`
	DurableNewMessages     []DriverMessageV1      `json:"durable_new_messages"`
	DurableMessagesTracked bool                   `json:"durable_messages_tracked,omitempty"`
	HistoryArchiveHeadSeq  int                    `json:"history_archive_head_seq,omitempty"`
	HistoryRewritten       bool                   `json:"history_rewritten,omitempty"`
	RetainedRequestContext *DriverRetainedContext `json:"retained_request_context,omitempty"`
	InputTokens            int                    `json:"input_tokens,omitempty"`
	OutputTokens           int                    `json:"output_tokens,omitempty"`
	CacheCreationTokens    int                    `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens        int                    `json:"cache_read_tokens,omitempty"`
	ContextTokens          int                    `json:"context_tokens,omitempty"`
	FinishReason           string                 `json:"finish_reason,omitempty"`
	StopReason             string                 `json:"stop_reason,omitempty"`
	Truncated              bool                   `json:"truncated,omitempty"`
	ReceiptDiagnostic      string                 `json:"receipt_diagnostic,omitempty"`
	DriverID               string                 `json:"driver_id,omitempty"`
	DriverVersion          string                 `json:"driver_version,omitempty"`
	DriverContractVersion  int                    `json:"driver_contract_version,omitempty"`
	DriverStatus           string                 `json:"driver_status,omitempty"`
	DriverCheckpoint       json.RawMessage        `json:"driver_checkpoint,omitempty"`
}

// EngineEventV1 extends portable model events with session and tool observations.
// Message uses DriverMessageV1 so durable addresses survive stream checkpoints.
type EngineEventV1 struct {
	Version           int                    `json:"version"`
	Type              string                 `json:"type"`
	Content           string                 `json:"content,omitempty"`
	Phase             string                 `json:"phase,omitempty"`
	ProviderItemID    string                 `json:"provider_item_id,omitempty"`
	Message           *DriverMessageV1       `json:"message,omitempty"`
	Image             *ModelImageV1          `json:"image,omitempty"`
	ReasoningBlock    *ModelReasoningBlockV1 `json:"reasoning_block,omitempty"`
	ToolCall          *ModelToolCallV1       `json:"tool_call,omitempty"`
	ToolResult        string                 `json:"tool_result,omitempty"`
	ToolResultDetail  *ModelToolResultV1     `json:"tool_result_detail,omitempty"`
	CompactReason     string                 `json:"compact_reason,omitempty"`
	CompactPhase      string                 `json:"compact_phase,omitempty"`
	CompactSummary    string                 `json:"compact_summary,omitempty"`
	TodoUpdate        *EngineTodoUpdateV1    `json:"todo_update,omitempty"`
	AgentActivity     *EngineAgentActivityV1 `json:"agent_activity,omitempty"`
	RequestContext    *RequestContextSummary `json:"request_context,omitempty"`
	ProviderState     *EngineProviderStateV1 `json:"provider_state,omitempty"`
	ProviderItem      *ModelProviderItemV1   `json:"provider_item,omitempty"`
	ProviderEventType string                 `json:"provider_event_type,omitempty"`
	Lifecycle         *EngineLifecycleV1     `json:"lifecycle,omitempty"`
	Failure           *DriverFailure         `json:"failure,omitempty"`
	Usage             *ModelRequestUsage     `json:"usage,omitempty"`
	StopReason        string                 `json:"stop_reason,omitempty"`
	FinishReason      string                 `json:"finish_reason,omitempty"`
	Truncated         bool                   `json:"truncated,omitempty"`
}

type EngineProviderStateV1 struct {
	StepIndex              int    `json:"step_index"`
	Provider               string `json:"provider"`
	Protocol               string `json:"protocol"`
	Transport              string `json:"transport"`
	ConfiguredTransport    string `json:"configured_transport"`
	ReplayMode             string `json:"replay_mode"`
	PreviousResponseIDUsed bool   `json:"previous_response_id_used"`
	ConnectionReused       bool   `json:"connection_reused"`
	Diagnostic             string `json:"diagnostic"`
	TransportFailurePhase  string `json:"transport_failure_phase"`
	FailedTransport        string `json:"failed_transport"`
	FallbackTransport      string `json:"fallback_transport"`
	EventsEmitted          bool   `json:"events_emitted"`
	FallbackActive         bool   `json:"fallback_active"`
	FallbackReason         string `json:"fallback_reason"`
	FallbackPinStatus      string `json:"fallback_pin_status"`
	FallbackRetryAfterMS   int64  `json:"fallback_retry_after_ms"`
	FallbackTTLMS          int64  `json:"fallback_ttlms"`
	InputItems             int    `json:"input_items"`
	FullInputItems         int    `json:"full_input_items"`
	DeltaInputItems        int    `json:"delta_input_items"`
}

type EngineLifecycleV1 struct {
	Phase           string                 `json:"phase"`
	OperationID     string                 `json:"operation_id"`
	OperationKind   string                 `json:"operation_kind"`
	WorkloadProfile string                 `json:"workload_profile"`
	PayloadVersion  int                    `json:"payload_version"`
	AttemptID       string                 `json:"attempt_id"`
	Attempt         int                    `json:"attempt"`
	MaxAttempts     int                    `json:"max_attempts"`
	SubmissionID    string                 `json:"submission_id"`
	SubmissionCount int                    `json:"submission_count"`
	RetryCount      int                    `json:"retry_count"`
	MaxRetries      int                    `json:"max_retries"`
	RetryInNS       int64                  `json:"retry_in_ns"`
	ElapsedNS       int64                  `json:"elapsed_ns"`
	Reason          string                 `json:"reason"`
	FailureCategory string                 `json:"failure_category"`
	RecoveryAction  string                 `json:"recovery_action"`
	BudgetDimension string                 `json:"budget_dimension"`
	ReplayReason    string                 `json:"replay_reason"`
	Workflow        EngineWorkflowBudgetV1 `json:"workflow"`
	ResetPartial    bool                   `json:"reset_partial"`
}

type EngineWorkflowBudgetV1 struct {
	WorkflowID                 string             `json:"workflow_id"`
	Spec                       EngineBudgetSpecV1 `json:"spec"`
	Operations                 uint64             `json:"operations"`
	Attempts                   uint64             `json:"attempts"`
	Submissions                uint64             `json:"submissions"`
	TransportSwitches          uint64             `json:"transport_switches"`
	CredentialRefreshes        uint64             `json:"credential_refreshes"`
	PayloadTransforms          uint64             `json:"payload_transforms"`
	ChildOperations            uint64             `json:"child_operations"`
	RecoveryWaitMillis         uint64             `json:"recovery_wait_millis"`
	KnownSubmissions           uint64             `json:"known_submissions"`
	EstimatedSubmissions       uint64             `json:"estimated_submissions"`
	UnknownBillableSubmissions uint64             `json:"unknown_billable_submissions"`
	KnownUsage                 ModelRequestUsage  `json:"known_usage"`
	EstimatedUsage             ModelRequestUsage  `json:"estimated_usage"`
}

type EngineBudgetSpecV1 struct {
	MaxOperations                 EngineBudgetLimitV1 `json:"max_operations"`
	MaxAttempts                   EngineBudgetLimitV1 `json:"max_attempts"`
	MaxSubmissions                EngineBudgetLimitV1 `json:"max_submissions"`
	MaxSamePayloadReplays         EngineBudgetLimitV1 `json:"max_same_payload_replays"`
	MaxTransportSwitches          EngineBudgetLimitV1 `json:"max_transport_switches"`
	MaxCredentialRefreshes        EngineBudgetLimitV1 `json:"max_credential_refreshes"`
	MaxPayloadTransforms          EngineBudgetLimitV1 `json:"max_payload_transforms"`
	MaxChildOperations            EngineBudgetLimitV1 `json:"max_child_operations"`
	MaxRecoveryWaitMillis         EngineBudgetLimitV1 `json:"max_recovery_wait_millis"`
	MaxUnknownBillableSubmissions EngineBudgetLimitV1 `json:"max_unknown_billable_submissions"`
	MaxUsageTokens                EngineBudgetLimitV1 `json:"max_usage_tokens"`
}

type EngineBudgetLimitV1 struct {
	Set   bool   `json:"set"`
	Value uint64 `json:"value"`
}

type EngineTodoUpdateV1 struct {
	Explanation string             `json:"explanation"`
	Todos       []EngineTodoItemV1 `json:"todos"`
}

type EngineTodoItemV1 struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type EngineAgentActivityV1 struct {
	ID     string `json:"id"`
	Engine string `json:"engine"`
	Label  string `json:"label"`
	State  string `json:"state"`
}

type EngineApprovalRequestV1 struct {
	Kind        string          `json:"kind"`
	EngineID    string          `json:"engine_id"`
	ThreadID    string          `json:"thread_id"`
	TurnID      string          `json:"turn_id"`
	ItemID      string          `json:"item_id"`
	Command     string          `json:"command"`
	CWD         string          `json:"cwd"`
	FilePath    string          `json:"file_path"`
	Reason      string          `json:"reason"`
	Permissions json.RawMessage `json:"permissions"`
}

type EngineUserInputRequestV1 struct {
	EngineID  string                      `json:"engine_id"`
	ThreadID  string                      `json:"thread_id"`
	TurnID    string                      `json:"turn_id"`
	ItemID    string                      `json:"item_id"`
	Blocking  bool                        `json:"blocking"`
	Questions []EngineUserInputQuestionV1 `json:"questions"`
}

type EngineUserInputQuestionV1 struct {
	ID          string                    `json:"id"`
	Header      string                    `json:"header"`
	Question    string                    `json:"question"`
	AllowCustom bool                      `json:"allow_custom"`
	Secret      bool                      `json:"secret"`
	Options     []EngineUserInputOptionV1 `json:"options"`
}

type EngineUserInputOptionV1 struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}
