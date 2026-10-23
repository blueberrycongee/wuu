package pluginprotocol

const (
	DriverTurnService = "driver.turn"
	DriverTurnMajor   = 3
)

// DriverTurnScope identifies an active driver execution. The service also checks
// transport caller identity; knowing an execution ID does not grant access.
type DriverTurnScope struct {
	ExecutionID string `json:"execution_id"`
}

// DriverMessageV1 adds durable execution addresses to the portable model message.
// These addresses are correlation metadata, never call authority.
type DriverMessageV1 struct {
	ModelMessageV1
	ToolInvocationID string `json:"tool_invocation_id,omitempty"`
	ClientID         string `json:"client_id,omitempty"`
	Seq              int    `json:"seq,omitempty"`
	Steered          bool   `json:"steered,omitempty"`
}

type DriverTurnReadResult struct {
	LogicalModelRequestV1
	Messages []DriverMessageV1 `json:"messages"`
}

type DriverTurnContextResult struct {
	Messages    []DriverMessageV1 `json:"messages"`
	Revision    uint64            `json:"revision"`
	ArchiveHead int               `json:"archive_head"`
	CanReplace  bool              `json:"can_replace"`
}

type DriverTurnAdmissionResult struct {
	Messages []DriverMessageV1       `json:"messages"`
	Context  DriverTurnContextResult `json:"context"`
}

type DriverContextBlock struct {
	Kind        string `json:"kind"`
	Title       string `json:"title,omitempty"`
	Source      string `json:"source,omitempty"`
	Content     string `json:"content"`
	TokenBudget int    `json:"token_budget,omitempty"`
}

type DriverContextSegment struct {
	Messages    []DriverMessageV1    `json:"messages"`
	Blocks      []DriverContextBlock `json:"blocks,omitempty"`
	Lifecycle   string               `json:"lifecycle"`
	Placement   string               `json:"placement"`
	CachePolicy string               `json:"cache_policy"`
	Durable     bool                 `json:"durable"`
	VisibleInUI bool                 `json:"visible_in_ui"`
}

type DriverTurnRequestContextResult struct {
	Segments []DriverContextSegment `json:"segments"`
}

type DriverTurnReplaceContextParams struct {
	DriverTurnScope
	Revision uint64            `json:"revision"`
	Messages []DriverMessageV1 `json:"messages"`
}

type DriverTurnModelParams struct {
	DriverTurnScope
	Request DriverTurnReadResult `json:"request"`
}

// DriverModelFailure describes a completed model operation that failed. It does
// not authorize another attempt: the driver must account for partial output,
// accepted effects, and the recovery executor's stopping decision.
type DriverModelFailure struct {
	Message         string               `json:"message"`
	Origin          string               `json:"origin"`
	Category        string               `json:"category"`
	ProviderCode    string               `json:"provider_code,omitempty"`
	HTTPStatus      int                  `json:"http_status,omitempty"`
	RetryAfterMS    int64                `json:"retry_after_ms,omitempty"`
	ContextOverflow bool                 `json:"context_overflow"`
	Recovery        *DriverModelRecovery `json:"recovery,omitempty"`
}

type DriverModelRecovery struct {
	OperationID     string `json:"operation_id,omitempty"`
	AttemptCount    int    `json:"attempt_count"`
	RetryCount      int    `json:"retry_count"`
	MaxAttempts     int    `json:"max_attempts"`
	SubmissionCount int    `json:"submission_count"`
	StopReason      string `json:"stop_reason"`
	BudgetDimension string `json:"budget_dimension,omitempty"`
}

type DriverTurnModelResult struct {
	Messages []DriverMessageV1   `json:"messages"`
	Usage    *ModelRequestUsage  `json:"usage,omitempty"`
	Failure  *DriverModelFailure `json:"failure,omitempty"`
}

type DriverTurnToolsParams struct {
	DriverTurnScope
	BatchID  string            `json:"batch_id"`
	Messages []DriverMessageV1 `json:"messages"`
	Calls    []ModelToolCallV1 `json:"calls"`
}

type DriverTurnToolsResult struct {
	Messages []DriverMessageV1 `json:"messages"`
}

type DriverTurnCompleteParams struct {
	DriverTurnScope
	Content  string            `json:"content"`
	Messages []DriverMessageV1 `json:"messages,omitempty"`
}

type DriverTurnCompleteResult struct {
	ReceiptID string `json:"receipt_id"`
}
