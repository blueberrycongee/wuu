package pluginprotocol

// RequestContextSummary describes request shape without exposing raw prompts.
type RequestContextSummary struct {
	StepIndex                int                          `json:"step_index"`
	TransientMessages        int                          `json:"transient_messages,omitempty"`
	ContentBytes             int                          `json:"content_bytes,omitempty"`
	BlockKinds               []string                     `json:"block_kinds,omitempty"`
	BlockKindCounts          map[string]int               `json:"block_kind_counts,omitempty"`
	BlockKindBytes           map[string]int               `json:"block_kind_bytes,omitempty"`
	SegmentLifecycleCounts   map[string]int               `json:"segment_lifecycle_counts,omitempty"`
	SegmentPlacementCounts   map[string]int               `json:"segment_placement_counts,omitempty"`
	SegmentCachePolicyCounts map[string]int               `json:"segment_cache_policy_counts,omitempty"`
	MessageCount             int                          `json:"message_count,omitempty"`
	SystemMessages           int                          `json:"system_messages,omitempty"`
	HiddenMessages           int                          `json:"hidden_messages,omitempty"`
	ToolCount                int                          `json:"tool_count,omitempty"`
	StablePrefix             int                          `json:"stable_prefix,omitempty"`
	TurnPrefix               int                          `json:"turn_prefix,omitempty"`
	DynamicBytes             int                          `json:"dynamic_context_bytes,omitempty"`
	SystemBytes              int                          `json:"system_bytes,omitempty"`
	StablePrefixBytes        int                          `json:"stable_prefix_bytes,omitempty"`
	TurnPrefixBytes          int                          `json:"turn_prefix_bytes,omitempty"`
	MessageBytes             int                          `json:"message_bytes,omitempty"`
	ToolSchemaBytes          int                          `json:"tool_schema_bytes,omitempty"`
	LoadableToolCount        int                          `json:"loadable_tool_count,omitempty"`
	LoadableToolSchemaBytes  int                          `json:"loadable_tool_schema_bytes,omitempty"`
	LoadableToolSurfaceHash  string                       `json:"loadable_tool_surface_hash,omitempty"`
	SystemHash               string                       `json:"system_hash,omitempty"`
	StablePrefixHash         string                       `json:"stable_prefix_hash,omitempty"`
	TurnPrefixHash           string                       `json:"turn_prefix_hash,omitempty"`
	ToolSurfaceHash          string                       `json:"tool_surface_hash,omitempty"`
	PromptCacheKey           string                       `json:"prompt_cache_key,omitempty"`
	InputTokens              int                          `json:"input_tokens,omitempty"`
	OutputTokens             int                          `json:"output_tokens,omitempty"`
	CacheCreationTokens      int                          `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens          int                          `json:"cache_read_tokens,omitempty"`
	SystemSections           []SystemPromptSectionSummary `json:"system_sections,omitempty"`
}

// SystemPromptSectionSummary describes one assembled system prompt section
// without exposing the raw prompt text.
type SystemPromptSectionSummary struct {
	Key    string `json:"key"`
	Static bool   `json:"static"`
	Bytes  int    `json:"bytes"`
	Hash   string `json:"hash,omitempty"`
}
