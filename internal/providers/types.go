package providers

import (
	"context"
	"errors"
	"maps"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// ToolDefinition describes a callable tool exposed to the model.
type ToolDefinition struct {
	Name        string
	Description string
	InputSchema map[string]any
	// DeferLoading marks a provider-native loadable tool declaration. It is a
	// wire-format hint, not an execution policy; runtimes still decide whether a
	// tool is hidden, deferred, or directly callable.
	DeferLoading bool
	// CacheStable marks tools that belong to the stable prompt-cache
	// prefix. Providers that support tool-level cache markers can place
	// a breakpoint at the end of the contiguous stable prefix.
	CacheStable bool
}

// LoadableToolDefinition is the provider-neutral shape for a tool schema that
// can be returned by a discovery flow before it is part of the ordinary tool
// list. Providers can adapt it into native shapes such as Responses
// tool_search_output.tools or Anthropic tool_reference expansion.
type LoadableToolDefinition struct {
	Type         string         `json:"type"`
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema,omitempty"`
	DeferLoading bool           `json:"defer_loading,omitempty"`
}

func LoadableToolFromDefinition(def ToolDefinition) LoadableToolDefinition {
	return LoadableToolDefinition{
		Type:         "function",
		Name:         def.Name,
		Description:  def.Description,
		InputSchema:  cloneLoadableToolSchema(def.InputSchema),
		DeferLoading: true,
	}
}

// ToolCallDisplay carries a user-facing summary for a tool invocation.
type ToolCallDisplay struct {
	Kind  string `json:"kind,omitempty"`
	Label string `json:"label,omitempty"`
	// LabelTranslations overrides Label by locale without changing tool identity.
	LabelTranslations map[string]string `json:"label_translations,omitempty"`
	Text              string            `json:"text,omitempty"`
	Capability        string            `json:"capability,omitempty"`
}

// Clone returns an independent display snapshot, including its translations.
func (d *ToolCallDisplay) Clone() *ToolCallDisplay {
	if d == nil {
		return nil
	}
	clone := *d
	clone.LabelTranslations = maps.Clone(d.LabelTranslations)
	return &clone
}

// ToolCallKind distinguishes ordinary function calls from provider-native
// tool discovery calls that use different wire-format input/output items.
type ToolCallKind string

const (
	ToolCallKindFunction   ToolCallKind = "function"
	ToolCallKindToolSearch ToolCallKind = "tool_search"
)

func NormalizeToolCallKind(kind string) ToolCallKind {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case string(ToolCallKindToolSearch):
		return ToolCallKindToolSearch
	case string(ToolCallKindFunction):
		return ToolCallKindFunction
	default:
		return ""
	}
}

// FinishReason is Wuu's provider-neutral reason for why a model response ended.
// StopReason keeps the raw provider string for diagnostics; FinishReason is the
// stable semantic value used by the agent loop and app-server.
type FinishReason string

const (
	FinishReasonStop          FinishReason = "stop"
	FinishReasonLength        FinishReason = "length"
	FinishReasonToolCalls     FinishReason = "tool_calls"
	FinishReasonContentFilter FinishReason = "content_filter"
	FinishReasonError         FinishReason = "error"
	FinishReasonUnknown       FinishReason = "unknown"
)

func NormalizeFinishReason(stopReason string, truncated bool, hasToolCalls bool) FinishReason {
	reason := strings.ToLower(strings.TrimSpace(stopReason))
	if truncated {
		return FinishReasonLength
	}
	switch reason {
	case "stop", "end_turn", "stop_sequence", "pause_turn", "completed":
		if hasToolCalls {
			return FinishReasonToolCalls
		}
		return FinishReasonStop
	case "length", "max_tokens", "max_output_tokens":
		return FinishReasonLength
	case "tool_calls", "tool_use", "function_call":
		return FinishReasonToolCalls
	case "content_filter", "refusal":
		return FinishReasonContentFilter
	case "error", "failed":
		return FinishReasonError
	case "":
		if hasToolCalls {
			return FinishReasonToolCalls
		}
		return ""
	default:
		if hasToolCalls {
			return FinishReasonToolCalls
		}
		return FinishReasonUnknown
	}
}

// ToolCall is a model requested tool execution.
type ToolCall struct {
	ID                   string           `json:"id,omitempty"`
	ProviderItemID       string           `json:"provider_item_id,omitempty"`
	ProviderItemProvider string           `json:"provider_item_provider,omitempty"`
	ProviderItemModel    string           `json:"provider_item_model,omitempty"`
	Name                 string           `json:"name,omitempty"`
	Arguments            string           `json:"arguments,omitempty"`
	Kind                 ToolCallKind     `json:"kind,omitempty"`
	Display              *ToolCallDisplay `json:"display,omitempty"`
}

// InputImage carries one user-provided image in base64 form.
type InputImage struct {
	MediaType string
	Data      string
	Width     uint32
	Height    uint32
}

// InputFile carries one user-provided file attachment in base64 form.
// Images are still represented by InputImage for backward compatibility;
// non-image media such as PDFs use InputFile.
type InputFile struct {
	MediaType string
	Data      string
	Filename  string
}

// ReasoningBlock preserves provider-native thinking payloads that must be
// replayed verbatim on follow-up requests for some APIs.
type ReasoningBlock struct {
	Type      string
	Thinking  string
	Signature string
	Data      string
}

// ProviderItem preserves an opaque provider-native history item that must be
// replayed verbatim to the same provider state scope. Data is the complete JSON
// wire item; Type is copied separately for validation and diagnostics.
type ProviderItem struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	Provider string `json:"provider"`
	Scope    string `json:"scope,omitempty"`
	// Fallback retains the portable pre-compaction history for a future route
	// whose provider, credential scope, or model cannot replay Data.
	Fallback []ChatMessage `json:"fallback,omitempty"`
}

// MessagePhase classifies assistant text when a provider exposes the same
// structured phase signal Codex uses. Empty means unknown.
type MessagePhase string

const (
	MessagePhaseCommentary  MessagePhase = "commentary"
	MessagePhaseFinalAnswer MessagePhase = "final_answer"
)

// NormalizeMessagePhase returns a known phase value or empty for unsupported
// provider strings.
func NormalizeMessagePhase(phase string) MessagePhase {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case string(MessagePhaseCommentary):
		return MessagePhaseCommentary
	case string(MessagePhaseFinalAnswer):
		return MessagePhaseFinalAnswer
	default:
		return ""
	}
}

// MessageContentPart preserves the authored structure of one user message.
// Providers still consume ChatMessage.Content as flattened text.
type MessageContentPart struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Title string `json:"title,omitempty"`
}

// ChatMessage is a generic multi-provider chat message.
type ChatMessage struct {
	Role           string
	Name           string
	ClientID       string
	Content        string
	DisplayContent string `json:"display_content,omitempty"`
	// Origin is product provenance, independent from provider Role. A plugin
	// generated query still projects to provider role=user while retaining an
	// auditable non-user author in durable history and UI state.
	Origin           string
	OriginID         string
	Cause            string
	PresentationKind string
	RelatedSessionID string
	ReadOnly         bool
	Phase            MessagePhase
	// Hidden marks model-visible context that should be omitted from
	// user-facing conversation items and previews. It does not imply durable
	// history; request-only context is owned by the agent request assembler.
	Hidden bool
	// ProviderItemID preserves provider-native assistant output item identity,
	// such as OpenAI Responses msg_* ids, for same-model replay.
	ProviderItemID       string
	ProviderItemProvider string
	ProviderItemModel    string
	// Steered marks user input that was injected into an already-running turn.
	// Providers ignore this; app-server history uses it to restore turn items.
	Steered bool
	// ReasoningContent stores provider-emitted hidden reasoning that
	// must be replayed in follow-up assistant tool-call messages for
	// providers like Kimi when thinking mode is enabled.
	ReasoningContent string
	ReasoningBlocks  []ReasoningBlock
	ProviderItems    []ProviderItem
	Images           []InputImage
	Files            []InputFile
	// ContentParts is durable presentation metadata; provider adapters ignore it.
	ContentParts []MessageContentPart `json:"content_parts,omitempty"`
	ToolCallID   string
	// ToolInvocationID is Wuu's durable identity for the execution. Provider
	// adapters must continue using ToolCallID for protocol correlation.
	ToolInvocationID string
	ToolResultKind   ToolCallKind
	ToolResult       *toolresult.Result
	ToolCalls        []ToolCall
	FinishReason     FinishReason
	StopReason       string
	Truncated        bool
	// DiscoveredTools carries provider-native deferred tool state across
	// compact/resume/fork boundaries without adding full schemas to prompt text.
	DiscoveredTools []LoadableToolDefinition
	// Seq is the message's stable per-thread address (session_messages.seq),
	// populated when history is loaded from the store. App-level only; provider
	// adapters ignore it. 0 = unknown/not persisted.
	Seq int `json:"seq,omitempty"`
}

// CacheHint carries provider-agnostic prompt-cache guidance.
//
// The goal is not to model every provider's caching surface area.
// wuu only needs a small set of cross-provider signals:
//   - PromptCacheKey: a stable conversation-scoped cache key for
//     OpenAI-compatible APIs that expose one.
//   - StableSystem/StablePrefixMessages: which request prefix is
//     stable enough to mark as cache-eligible on providers like
//     Anthropic.
//   - TurnPrefixMessages: which request prefix is stable within a
//     single user turn for multi-step tool loops.
//   - HasCompactSummary: whether the stable prefix starts with a
//     compacted conversation summary, so providers can bias cache
//     anchors toward that rewritten history root.
//
// Providers are free to ignore hints they don't support.
//
// Provider-specific caching strategy (Anthropic):
//   - StablePrefixMessages and TurnPrefixMessages are hints used to derive
//     message-level cache markers. On Anthropic, the actual marker placement
//     ignores the message counts and instead walks the final merged payload
//     backward to find the last cacheable block, placing exactly one sliding
//     tail marker there. This strategy optimizes intra-turn tool loops by
//     marking the request tail, which moves every round without requiring
//     per-tool or per-message stable prefixes.
type CacheHint struct {
	// PromptCacheKey is a stable key for providers exposing an explicit
	// prompt cache key (for example promptCacheKey / prompt_cache_key).
	PromptCacheKey string
	// StableSystem marks the system prompt as part of the cacheable
	// stable prefix. Ignored when the request has no system message.
	StableSystem bool
	// StablePrefixMessages is the number of leading non-system entries
	// in ChatRequest.Messages that belong to the stable prefix.
	// Providers that merge, split, or lift messages must map this
	// source-message count onto their final wire-format block boundary.
	// On Anthropic, this is informational only; the actual marker is derived
	// from the payload tail instead.
	StablePrefixMessages int
	// TurnPrefixMessages is the number of leading non-system entries
	// that are stable within the current turn, typically through the
	// latest visible user message. It can be larger than
	// StablePrefixMessages without making the latest user request part
	// of the cross-turn stable prefix.
	// On Anthropic, this is informational only; the actual marker is derived
	// from the payload tail instead.
	TurnPrefixMessages int
	// HasCompactSummary reports that the leading system prompt contains
	// a compacted conversation summary. This lets providers prefer a
	// cache anchor close to the rewritten history root without needing
	// a heavier session-parts model.
	HasCompactSummary bool
}

// ChatRequest is the normalized request payload for providers.
type ChatRequest struct {
	// Provider is Wuu's configured provider identity. It is request metadata
	// used to prevent provider-native state from crossing endpoints and is
	// never serialized by provider adapters.
	Provider    string
	Model       string
	Messages    []ChatMessage
	Tools       []ToolDefinition
	Temperature float64
	CacheHint   *CacheHint
	// Operation correlates one logical model operation across execution
	// attempts and transport fallbacks. Provider clients must not send it on
	// the wire.
	Operation InferenceOperation
	// Execution and Attempt are process-local retry bookkeeping. They are
	// shared across copied requests and must never be serialized to providers.
	Execution *InferenceExecution
	Attempt   InferenceAttempt
	// StepIndex is agent-loop metadata for correlating request-shape,
	// provider-state, and usage telemetry. Provider clients must not send it
	// on the wire.
	StepIndex int
	// MaxTokens caps the model's output length. Zero means the provider
	// should use its own default. If the provider hits this cap, the
	// response completes with FinishReason=length.
	MaxTokens int
	// Effort controls how much reasoning the model performs before
	// responding. Empty string means "use API default". Valid values
	// are provider-specific:
	//   Anthropic: "low", "medium", "high", "max"
	//   OpenAI:    "low", "medium", "high"
	// This maps to each provider's reasoning-depth control.
	Effort string
	// ProviderOptions carries model/provider-specific options selected through
	// model variants. Keys follow OpenCode's AI SDK option names where
	// possible; provider clients translate them to wire-format fields.
	ProviderOptions map[string]any
	// ProviderStateScope is an opaque, non-secret compatibility key for
	// replaying provider-native history. Provider adapters must not serialize it.
	ProviderStateScope string
	// NativeDeferredToolDiscovery allows provider adapters to use their native
	// deferred-tool protocol for tools marked DeferLoading. Compatible
	// endpoints leave this false unless the session explicitly opts in.
	NativeDeferredToolDiscovery bool
	// ForceToolName, when non-empty, pins this request's tool choice to the
	// named tool: providers translate it to their forced tool_choice wire
	// form. Used for mechanical closing turns (e.g. forcing agent_report on
	// requires_report workers); the tool must be present in Tools.
	ForceToolName string
	// MediaInput is the resolved admission policy for user-supplied media on
	// this request. Provider adapters apply it at the shared request
	// boundary (see ProjectMediaForPolicy); it is request metadata and must
	// never be serialized on the wire.
	MediaInput MediaInputPolicy
}

// ChatResponse is the normalized response from providers.
type ChatResponse struct {
	Content           string
	Phase             MessagePhase
	ProviderItemID    string
	ProviderItemModel string
	ReasoningContent  string
	ReasoningBlocks   []ReasoningBlock
	ToolCalls         []ToolCall
	Usage             *TokenUsage // optional; nil when the provider didn't return usage
	// StopReason is the raw provider stop signal, normalized to lowercase.
	// Common values: "stop" / "end_turn" (natural finish), "length" /
	// "max_tokens" (output truncation), "tool_calls" / "tool_use".
	StopReason string
	// FinishReason is Wuu's provider-neutral stop semantic. It preserves
	// OpenCode-style behavior where max_tokens/max_output_tokens are a
	// completed response with finish_reason=length, not an interrupted turn.
	FinishReason FinishReason
	// Truncated is true when the model hit its output token cap mid-response.
	// Callers can surface this as "output reached the limit" without treating
	// the turn as a user interruption or transport failure.
	Truncated bool
}

// Client sends chat requests to an LLM provider.
type Client interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// StreamEventType classifies events emitted during streaming.
type StreamEventType string

const (
	EventContentDelta    StreamEventType = "content_delta"
	EventContentReplace  StreamEventType = "content_replace"
	EventThinkingDelta   StreamEventType = "thinking_delta"
	EventThinkingReplace StreamEventType = "thinking_replace"
	EventThinkingDone    StreamEventType = "thinking_done"
	EventToolUseStart    StreamEventType = "tool_use_start"
	EventToolUseDelta    StreamEventType = "tool_use_delta"
	EventToolUseEnd      StreamEventType = "tool_use_end"
	EventTodoUpdate      StreamEventType = "todo_update"
	EventAgentActivity   StreamEventType = "agent_activity"
	EventRequestContext  StreamEventType = "request_context"
	EventProviderState   StreamEventType = "provider_state"
	EventProviderItem    StreamEventType = "provider_item"
	EventUsage           StreamEventType = "usage"
	EventMessage         StreamEventType = "message"
	EventLifecycle       StreamEventType = "lifecycle"
	EventReconnect       StreamEventType = "reconnect"
	EventCompact         StreamEventType = "compact"
	EventDone            StreamEventType = "done"
	EventError           StreamEventType = "error"
)

// AgentActivityState is the provider-neutral lifecycle reported for an
// external engine's native child agent. These observations do not imply that
// Wuu owns, can resume, or can control the child session.
type AgentActivityState string

const (
	AgentActivityQueued    AgentActivityState = "queued"
	AgentActivityRunning   AgentActivityState = "running"
	AgentActivityWaiting   AgentActivityState = "waiting"
	AgentActivityFailed    AgentActivityState = "failed"
	AgentActivityCompleted AgentActivityState = "completed"
)

// AgentActivity is a transient observation of one native child agent owned by
// an external engine such as Codex or Claude.
type AgentActivity struct {
	ID     string             `json:"id"`
	Engine string             `json:"engine"`
	Label  string             `json:"label"`
	State  AgentActivityState `json:"state"`
}

// CompactPhase identifies whether a compact event starts or finishes a
// compaction pass. Empty is treated as completed for compatibility with
// stream producers that predate explicit progress events.
type CompactPhase string

const (
	CompactPhaseStarted   CompactPhase = "started"
	CompactPhaseCompleted CompactPhase = "completed"
	CompactPhaseFailed    CompactPhase = "failed"
)

// StreamLifecyclePhase is the structured state of the live response transport.
type StreamLifecyclePhase string

const (
	StreamPhaseConnecting   StreamLifecyclePhase = "connecting"
	StreamPhaseConnected    StreamLifecyclePhase = "connected"
	StreamPhaseReconnecting StreamLifecyclePhase = "reconnecting"
	StreamPhaseFailed       StreamLifecyclePhase = "failed"
)

// ProviderStateSummary carries metadata-only provider replay/request-state
// details. It intentionally avoids raw provider ids and prompt text.
type ProviderStateSummary struct {
	StepIndex              int    `json:"step_index"`
	Provider               string `json:"provider,omitempty"`
	Protocol               string `json:"protocol,omitempty"`
	Transport              string `json:"transport,omitempty"`
	ConfiguredTransport    string `json:"configured_transport,omitempty"`
	ReplayMode             string `json:"replay_mode,omitempty"`
	PreviousResponseIDUsed bool   `json:"previous_response_id_used,omitempty"`
	ConnectionReused       bool   `json:"connection_reused,omitempty"`
	Diagnostic             string `json:"diagnostic,omitempty"`
	TransportFailurePhase  string `json:"transport_failure_phase,omitempty"`
	FailedTransport        string `json:"failed_transport,omitempty"`
	FallbackTransport      string `json:"fallback_transport,omitempty"`
	EventsEmitted          bool   `json:"events_emitted,omitempty"`
	FallbackActive         bool   `json:"fallback_active,omitempty"`
	FallbackReason         string `json:"fallback_reason,omitempty"`
	FallbackPinStatus      string `json:"fallback_pin_status,omitempty"`
	FallbackRetryAfterMS   int64  `json:"fallback_retry_after_ms,omitempty"`
	FallbackTTLMS          int64  `json:"fallback_ttl_ms,omitempty"`
	InputItems             int    `json:"input_items,omitempty"`
	FullInputItems         int    `json:"full_input_items,omitempty"`
	DeltaInputItems        int    `json:"delta_input_items,omitempty"`
}

// StreamLifecycle carries retry metadata for one streaming connection attempt.
// Attempt is 1-based and includes the initial connect.
// RetryCount is the number of retries so far; MaxRetries is the configured cap.
type StreamLifecycle struct {
	Phase           StreamLifecyclePhase
	OperationID     string
	OperationKind   InferenceOperationKind
	WorkloadProfile InferenceWorkloadProfile
	PayloadVersion  int
	AttemptID       string
	Attempt         int
	MaxAttempts     int
	SubmissionID    string
	SubmissionCount int
	RetryCount      int
	MaxRetries      int
	RetryIn         time.Duration
	Elapsed         time.Duration
	Reason          string
	FailureCategory string
	RecoveryAction  string
	BudgetDimension string
	ReplayReason    string
	Workflow        WorkflowBudgetSnapshot
	// ResetPartial tells aggregators that events from the previous attempt
	// must be superseded before any replacement output is accepted.
	ResetPartial bool
}

// TokenUsage reports token consumption for a single API call. Cache
// fields are populated when the provider supports prompt caching:
// Anthropic returns them on every messages response; OpenAI returns
// `cached_tokens` under prompt_tokens_details on supporting models.
//
// IMPORTANT: cached tokens still occupy the model's context window —
// they are read out of cache and packed into the prompt, the only
// difference is the per-token price. Auto-compact's fill-rate
// calculation must include them, otherwise providers using prompt
// caching look like they're using almost no context and the trigger
// fires far too late.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	// CacheCreationTokens are the tokens that were written into the
	// provider's prompt cache as a side effect of this call (Anthropic
	// only; OpenAI doesn't expose this separately).
	CacheCreationTokens int
	// CacheReadTokens are the tokens served out of the prompt cache
	// for this call. Reported by both Anthropic
	// (cache_read_input_tokens) and OpenAI
	// (prompt_tokens_details.cached_tokens) on models that support
	// prompt caching.
	CacheReadTokens int
	// CacheCreationUnknown is true when the provider's response did not
	// meaningfully report CacheCreationTokens. Some anthropic-compatible
	// backends (notably minimax's api.minimaxi.com) omit
	// cache_creation_input_tokens from their response entirely while
	// still serving cache_read_input_tokens, so the CacheCreationTokens
	// value above stays at zero and reads as "wuu didn't create any
	// cache" in UI even when the provider caches implicitly. Downstream
	// code should render this as N/A instead of a literal zero so users
	// don't chase a non-existent regression. The flag is stamped by the
	// provider client based on endpoint detection; the default value
	// (false) is correct for native Anthropic endpoints where a zero
	// CacheCreationTokens means "no cache written this turn".
	CacheCreationUnknown bool
}

// TodoItem is one item in a semantic TODO snapshot.
type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

// TodoUpdate carries the latest semantic TODO snapshot as a stream event.
type TodoUpdate struct {
	Explanation string     `json:"explanation,omitempty"`
	Todos       []TodoItem `json:"todos"`
}

// RequestContextSummary carries metadata-only context compilation details for
// one provider request. It intentionally excludes raw prompt text.
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

// TotalContextTokens returns the number of tokens this call actually
// consumed against the model's context window. On the Anthropic wire format
// input_tokens EXCLUDES both cache_read and cache_creation tokens (the full
// prompt is input + cache_creation + cache_read), so all three input parts
// must be summed. Omitting cache_creation made a cold-cache first request —
// where cache_creation is roughly the entire history — look nearly empty and
// suppressed proactive auto-compact exactly when it was most needed.
// Endpoints whose input_tokens are inclusive (e.g. MiniMax) are normalized to
// this exclusive form at the client layer before this method sees them.
func (u TokenUsage) TotalContextTokens() int {
	if u == (TokenUsage{}) {
		return 0
	}
	return u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens + u.OutputTokens
}

// StreamEvent is a single event from a streaming chat response.
type StreamEvent struct {
	Type             StreamEventType
	Content          string
	Phase            MessagePhase
	ProviderItemID   string
	Message          *ChatMessage
	ReasoningBlock   *ReasoningBlock
	ToolCall         *ToolCall
	ToolResult       string
	ToolResultDetail *toolresult.Result
	CompactReason    string
	CompactPhase     CompactPhase
	// CompactSummary carries the replacement-context summary body produced
	// by a completed compact pass, for client surfaces that let the user
	// inspect what the new context looks like. Only populated on
	// EventCompact with CompactPhaseCompleted after a pass that replaced
	// history; intentionally absent from the metadata-only attempt records.
	CompactSummary string
	TodoUpdate     *TodoUpdate
	AgentActivity  *AgentActivity
	RequestContext *RequestContextSummary
	ProviderState  *ProviderStateSummary
	ProviderItem   *ProviderItem
	// ProviderEventType records the exact provider terminal event when a
	// protocol-specific operation needs stricter completion validation.
	ProviderEventType string
	Lifecycle         *StreamLifecycle
	Error             error
	Usage             *TokenUsage
	// StopReason / FinishReason / Truncated are populated on the terminal
	// EventDone when the provider reports them. StopReason is raw-ish provider
	// detail; FinishReason is the normalized semantic.
	StopReason   string
	FinishReason FinishReason
	Truncated    bool
}

// StreamClient extends Client with streaming support.
type StreamClient interface {
	Client
	StreamChat(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error)
}

var ErrNativeCompactionUnavailable = errors.New("native compaction unavailable")

// NativeCompactionResult is a provider-generated replacement history. Opaque
// provider items in Replacement remain bound to ProviderStateScope.
type NativeCompactionResult struct {
	Replacement        []ChatMessage
	Usage              *TokenUsage
	ProviderStateScope string
}

// NativeCompactor is an optional provider capability. NativeCompact receives a
// provider-normalized request with a valid inference attempt. Implementations
// must not replace caller history until they return a successful result.
type NativeCompactor interface {
	NativeCompactionAvailable() bool
	NativeCompact(ctx context.Context, req ChatRequest) (NativeCompactionResult, error)
}

// SessionPrewarmer is an optional provider capability for moving cold
// authentication and transport setup ahead of a conversation's first request.
// Implementations must not generate model output or mutate conversation state.
type SessionPrewarmer interface {
	PrewarmSession(ctx context.Context, sessionID string) error
}
