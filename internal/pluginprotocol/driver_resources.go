package pluginprotocol

// DriverTurnSettings describes the selected execution configuration. Drivers
// decide when to use these resources; reading settings performs no inference,
// compaction or tool execution.
type DriverTurnSettings struct {
	HasPolicy               bool                  `json:"has_policy"`
	ResumeSampling          bool                  `json:"resume_sampling,omitempty"`
	MaxSteps                int                   `json:"max_steps"`
	StreamingTools          bool                  `json:"streaming_tools"`
	ProviderObservationKey  string                `json:"provider_observation_key"`
	ModelVariant            string                `json:"model_variant"`
	ContextTokens           int                   `json:"context_tokens"`
	InputTokens             int                   `json:"input_tokens"`
	OutputReserveTokens     int                   `json:"output_reserve_tokens"`
	CompactThresholdTokens  int                   `json:"compact_threshold_tokens"`
	CompactThresholdPercent float64               `json:"compact_threshold_percent"`
	CompactKeepRecentTokens int                   `json:"compact_keep_recent_tokens"`
	DisableAutoCompact      bool                  `json:"disable_auto_compact"`
	ForceInitialCompact     bool                  `json:"force_initial_compact"`
	CompactOnly             bool                  `json:"compact_only"`
	FreshContextTokens      int                   `json:"fresh_context_tokens"`
	CanArchive              bool                  `json:"can_archive"`
	CanCompact              bool                  `json:"can_compact"`
	SystemSections          []DriverSystemSection `json:"system_sections,omitempty"`
}

type DriverSystemSection struct {
	Key    string `json:"key"`
	Static bool   `json:"static"`
	Bytes  int    `json:"bytes"`
	Hash   string `json:"hash,omitempty"`
}

// DriverContextState retains request-only insertions and context estimates. It
// does not replace durable history or the authoritative model usage counters.
type DriverContextState struct {
	Retained *DriverRetainedContext `json:"retained,omitempty"`
	Usage    *DriverContextUsage    `json:"usage,omitempty"`
}

type DriverRetainedContext struct {
	Messages    []DriverRetainedMessage `json:"messages"`
	DurableLen  int                     `json:"durable_len"`
	DurableHash string                  `json:"durable_hash"`
}

type DriverRetainedMessage struct {
	AfterDurable int             `json:"after_durable"`
	Message      DriverMessageV1 `json:"message"`
}

type DriverContextUsage struct {
	LastResponseTotal           int    `json:"last_response_total"`
	LastSuccessfulRequestTokens int    `json:"last_successful_request_tokens"`
	LastResponseContract        string `json:"last_response_contract"`
	PendingDelta                int    `json:"pending_delta"`
	Adjustment                  string `json:"adjustment"`
}

type DriverTurnStateParams struct {
	DriverTurnScope
	State DriverContextState `json:"state"`
}

// DriverCompactBudget carries the driver's recovery observation explicitly;
// context-local Go values never form part of the process contract.
type DriverCompactBudget struct {
	Reason               string               `json:"reason"`
	LastSuccessfulTokens int                  `json:"last_successful_tokens"`
	FailedRequestTokens  int                  `json:"failed_request_tokens"`
	TargetTotalTokens    int                  `json:"target_total_tokens"`
	Window               *DriverContextWindow `json:"window,omitempty"`
}

// DriverContextWindow requests a selected policy's fresh-context proposal.
// The driver must validate and explicitly commit the returned messages.
type DriverContextWindow struct {
	HistoryHead  int `json:"history_head"`
	FixedTokens  int `json:"fixed_tokens"`
	TargetTokens int `json:"target_tokens"`
}

type DriverTurnCompactParams struct {
	DriverTurnScope
	Messages []DriverMessageV1   `json:"messages"`
	Budget   DriverCompactBudget `json:"budget"`
}

// DriverTurnObservation reports algorithm decisions without submitting effects.
// Kind is request_context, compact_start, compact_result, compact_attempt,
// before_compact or after_compact. The last two invoke configured hooks and may
// reject the proposed transition before it is accepted.
type DriverTurnObservation struct {
	Kind           string                    `json:"kind"`
	RequestContext *RequestContextSummary    `json:"request_context,omitempty"`
	Compact        *DriverCompactObservation `json:"compact,omitempty"`
}

type DriverCompactObservation struct {
	Reason            string         `json:"reason"`
	Status            string         `json:"status,omitempty"`
	TokensBefore      int            `json:"tokens_before,omitempty"`
	TokensAfter       int            `json:"tokens_after,omitempty"`
	MessagesBefore    int            `json:"messages_before,omitempty"`
	MessagesAfter     int            `json:"messages_after,omitempty"`
	Summary           string         `json:"summary,omitempty"`
	LastResponseTotal int            `json:"last_response_total,omitempty"`
	PendingDelta      int            `json:"pending_delta,omitempty"`
	UsageAdjustment   string         `json:"usage_adjustment,omitempty"`
	Error             string         `json:"error,omitempty"`
	Failure           *DriverFailure `json:"failure,omitempty"`
	OutputLimit       bool           `json:"output_limit,omitempty"`
}

type DriverTurnObserveParams struct {
	DriverTurnScope
	Observation DriverTurnObservation `json:"observation"`
}

type DriverTurnCompactResult struct {
	Messages []DriverMessageV1 `json:"messages"`
	Failure  *DriverFailure    `json:"failure,omitempty"`
}
