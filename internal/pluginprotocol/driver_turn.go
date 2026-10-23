package pluginprotocol

const DriverTurnService = "driver.turn"

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

type DriverTurnReplaceContextParams struct {
	DriverTurnScope
	Revision uint64            `json:"revision"`
	Messages []DriverMessageV1 `json:"messages"`
}

type DriverTurnModelParams struct {
	DriverTurnScope
	Request DriverTurnReadResult `json:"request"`
}

type DriverTurnModelResult struct {
	Messages []DriverMessageV1  `json:"messages"`
	Usage    *ModelRequestUsage `json:"usage,omitempty"`
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
