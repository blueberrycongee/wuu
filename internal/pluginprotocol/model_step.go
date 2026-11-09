package pluginprotocol

// ModelStepSummaryV1 identifies the model actually dispatched, independently of
// a conversation's pending selection. OperationID correlates durable inference.
type ModelStepSummaryV1 struct {
	Provider            string `json:"provider"`
	Model               string `json:"model"`
	APIModel            string `json:"api_model,omitempty"`
	Revision            uint64 `json:"revision,omitempty"`
	StepIndex           int    `json:"step_index"`
	OperationID         string `json:"operation_id"`
	ContextWindowTokens int    `json:"context_window_tokens,omitempty"`
}

// ModelStepUsageV1 is one logical model operation's reported usage. Turn totals
// remain separate and must not be attributed to one provider on a mixed turn.
type ModelStepUsageV1 struct {
	ModelStepSummaryV1
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`
	ContextTokens       int `json:"context_tokens,omitempty"`
}
