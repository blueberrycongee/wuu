package pluginprotocol

const (
	DriverTurnService = "driver.turn"
	DriverTurnMajor   = 4
)

// DriverTurnScope identifies an active driver execution. The service also checks
// transport caller identity; knowing an execution ID does not grant access.
type DriverTurnScope struct {
	ExecutionID string `json:"execution_id"`
}

// DriverMessageV1 adds durable execution addresses to the portable model message.
// These addresses are correlation metadata, never call authority.
type DriverMessageV1 struct {
	// Host-owned references preserve UI retry/fork metadata, never model API input or file authority.
	WorkingCopies     *DriverWorkingCopiesV1       `json:"working_copies,omitempty"`
	DisplayContent    string                       `json:"display_content,omitempty"`
	PresentationKind  string                       `json:"presentation_kind,omitempty"`
	RelatedSessionID  string                       `json:"related_session_id,omitempty"`
	EngineTurnID      string                       `json:"engine_turn_id,omitempty"`
	EngineTurnPending bool                         `json:"engine_turn_pending,omitempty"`
	ContentParts      []DriverMessageContentPartV1 `json:"content_parts,omitempty"`
	ModelMessageV1
	ToolInvocationID string `json:"tool_invocation_id,omitempty"`
	ClientID         string `json:"client_id,omitempty"`
	Seq              int    `json:"seq,omitempty"`
	Steered          bool   `json:"steered,omitempty"`
}

type DriverWorkingCopiesV1 struct {
	Images []string `json:"images,omitempty"`
	Files  []string `json:"files,omitempty"`
}

type DriverTurnReadResult struct {
	ModelSelectionRevision uint64 `json:"model_selection_revision,omitempty"`
	LogicalModelRequestV1
	Messages []DriverMessageV1 `json:"messages"`
}

type DriverTurnContextResult struct {
	Messages    []DriverMessageV1 `json:"messages"`
	Revision    uint64            `json:"revision"`
	ArchiveHead int               `json:"archive_head"`
	CanReplace  bool              `json:"can_replace"`
}

// DriverStepDecision is the default loop admission policy result. Replacement
// algorithms receive the same decision and explicitly choose how to apply it.
// Source is stamped by the host; stopping does not undo completed tool effects.
type DriverStepDecision struct {
	Stop    bool   `json:"stop"`
	Reason  string `json:"reason,omitempty"`
	Content string `json:"content,omitempty"`
	Source  string `json:"source,omitempty"`
}

type DriverTurnAdmissionResult struct {
	Decision DriverStepDecision      `json:"decision"`
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
	Volatile bool              `json:"volatile,omitempty"`
}

type DriverTurnArchiveResult struct {
	Messages []DriverMessageV1 `json:"messages"`
	Head     int               `json:"head"`
}

type DriverTurnModelParams struct {
	DriverTurnScope
	Request DriverTurnReadResult `json:"request"`
	BatchID string               `json:"batch_id,omitempty"`
}

// DriverFailure describes a failed operation or terminal driver result. It does
// not authorize another attempt: the driver must account for partial output,
// accepted effects, and the recovery executor's stopping decision.
type DriverFailure struct {
	Message         string               `json:"message"`
	Code            string               `json:"code,omitempty"`
	StopReason      string               `json:"stop_reason,omitempty"`
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
	CompletedMessages []DriverMessageV1  `json:"completed_messages,omitempty"`
	Messages          []DriverMessageV1  `json:"messages"`
	Usage             *ModelRequestUsage `json:"usage,omitempty"`
	Failure           *DriverFailure     `json:"failure,omitempty"`
	ToolBatchID       string             `json:"tool_batch_id,omitempty"`
	ToolWorkStarted   bool               `json:"tool_work_started,omitempty"`
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
	Content      string            `json:"content"`
	Messages     []DriverMessageV1 `json:"messages,omitempty"`
	FinishReason string            `json:"finish_reason,omitempty"`
	StopReason   string            `json:"stop_reason,omitempty"`
	Truncated    bool              `json:"truncated,omitempty"`
}

type DriverTurnCompleteResult struct {
	ReceiptID string `json:"receipt_id"`
}

// DriverMessageContentPartV1 preserves canonical presentation and host references.
// Model request projection never includes these host-only fields.
type DriverMessageContentPartV1 struct {
	LocalPath string                       `json:"local_path,omitempty"`
	Type      string                       `json:"type"`
	Text      string                       `json:"text,omitempty"`
	Title     string                       `json:"title,omitempty"`
	Source    *DriverFileSelectionSourceV1 `json:"source,omitempty"`
	Intent    string                       `json:"intent,omitempty"`
	Comment   string                       `json:"comment,omitempty"`
	ID        string                       `json:"id,omitempty"`
	Selection *DriverResponseSelectionV1   `json:"selection,omitempty"`
}

// DriverFileSelectionSourceV1 preserves authored history metadata independently of model projection.
type DriverFileSelectionSourceV1 struct {
	Workspace   string                         `json:"workspace"`
	Path        string                         `json:"path"`
	StartLine   int                            `json:"start_line"`
	StartColumn int                            `json:"start_column"`
	EndLine     int                            `json:"end_line"`
	EndColumn   int                            `json:"end_column"`
	Quote       string                         `json:"quote"`
	Revision    string                         `json:"revision"`
	PDF         *DriverPDFSelectionReferenceV1 `json:"pdf,omitempty"`
}

// DriverPDFSelectionReferenceV1 preserves authored history metadata independently of model projection.
type DriverPDFSelectionReferenceV1 struct {
	StartPage        int    `json:"start_page"`
	EndPage          int    `json:"end_page"`
	ArtifactURI      string `json:"artifact_uri,omitempty"`
	ArtifactSHA256   string `json:"artifact_sha256,omitempty"`
	ArtifactThreadID string `json:"artifact_thread_id,omitempty"`
}

// DriverResponseSelectionV1 preserves authored history metadata independently of model projection.
type DriverResponseSelectionV1 struct {
	ID      string                          `json:"id"`
	Text    string                          `json:"text"`
	Comment string                          `json:"comment,omitempty"`
	Source  DriverResponseSelectionSourceV1 `json:"source"`
}

// DriverResponseSelectionSourceV1 preserves authored history metadata independently of model projection.
type DriverResponseSelectionSourceV1 struct {
	ThreadID    string `json:"thread_id"`
	TurnID      string `json:"turn_id"`
	ItemID      string `json:"item_id"`
	StartOffset int    `json:"start_offset"`
	EndOffset   int    `json:"end_offset"`
	// RangeText is the exact DOM textContent slice when readable selection text differs.
	RangeText string `json:"range_text,omitempty"`
}
