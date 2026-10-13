// Package pluginprotocol holds the Go host representation of plugin wire data.
// It contains no plugin authoring or process runtime API.
package pluginprotocol

import "encoding/json"

const (
	KernelModelInvokeService        = "host.model.invoke"
	KernelModelEmitService          = "host.model.emit"
	KernelBackgroundService         = "host.background"
	CapabilityModelRequestTransform = "model.request.transform"
	CapabilityModelRequestObserve   = "model.request.observe"
	CapabilityModelAdapter          = "model.adapter"
	CapabilityModelMiddleware       = "model.middleware"
)

// LogicalModelRequestV1 is the portable model input, not a raw HTTP request.
// Host credentials, retry state and execution authority never appear here.
// Preserve provider-native history and unknown option keys when copying it.
type LogicalModelRequestV1 struct {
	Version                     int               `json:"version"`
	Adapter                     string            `json:"adapter,omitempty"`
	Provider                    string            `json:"provider,omitempty"`
	Model                       string            `json:"model"`
	Messages                    []ModelMessageV1  `json:"messages"`
	Tools                       []ModelToolV1     `json:"tools"`
	Temperature                 float64           `json:"temperature,omitempty"`
	MaxTokens                   int               `json:"max_tokens,omitempty"`
	Effort                      string            `json:"effort,omitempty"`
	CacheHint                   *ModelCacheHintV1 `json:"cache_hint,omitempty"`
	ProviderOptions             map[string]any    `json:"provider_options,omitempty"`
	ProviderStateScope          string            `json:"provider_state_scope,omitempty"`
	MediaInput                  ModelMediaInputV1 `json:"media_input,omitempty"`
	NativeDeferredToolDiscovery bool              `json:"native_deferred_tool_discovery,omitempty"`
	ForceToolName               string            `json:"force_tool_name,omitempty"`
}

type ModelRequestV1 = LogicalModelRequestV1

type ModelCacheHintV1 struct {
	PromptCacheKey       string `json:"prompt_cache_key,omitempty"`
	StableSystem         bool   `json:"stable_system,omitempty"`
	StablePrefixMessages int    `json:"stable_prefix_messages,omitempty"`
	TurnPrefixMessages   int    `json:"turn_prefix_messages,omitempty"`
	HasCompactSummary    bool   `json:"has_compact_summary,omitempty"`
}

type ModelMediaInputV1 struct {
	Video      bool `json:"video,omitempty"`
	VideoKnown bool `json:"video_known,omitempty"`
	Image      bool `json:"image,omitempty"`
	ImageKnown bool `json:"image_known,omitempty"`
	File       bool `json:"file,omitempty"`
	FileKnown  bool `json:"file_known,omitempty"`
}

// ModelImageV1 contains resolved model input; local working-copy paths are excluded.
type ModelImageV1 struct {
	MediaType      string `json:"media_type"`
	Data           string `json:"data"`
	Required       bool   `json:"required,omitempty"`
	Width          uint32 `json:"width,omitempty"`
	Height         uint32 `json:"height,omitempty"`
	ProviderItemID string `json:"provider_item_id,omitempty"`
}

type ModelFileV1 struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	Filename  string `json:"filename,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Required  bool   `json:"required,omitempty"`
}

type ModelReasoningBlockV1 struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
}

// ModelProviderItemV1 retains native JSON verbatim and its compatibility scope.
type ModelProviderItemV1 struct {
	Type     string           `json:"type"`
	Data     string           `json:"data"`
	Provider string           `json:"provider"`
	Scope    string           `json:"scope,omitempty"`
	Fallback []ModelMessageV1 `json:"fallback,omitempty"`
}

type ModelToolCallV1 struct {
	ID                   string `json:"id,omitempty"`
	Name                 string `json:"name,omitempty"`
	Arguments            string `json:"arguments,omitempty"`
	Kind                 string `json:"kind,omitempty"`
	ProviderItemID       string `json:"provider_item_id,omitempty"`
	ProviderItemProvider string `json:"provider_item_provider,omitempty"`
	ProviderItemModel    string `json:"provider_item_model,omitempty"`
}

type ModelToolV1 struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema"`
	Freeform     bool           `json:"freeform,omitempty"`
	DirectOnly   bool           `json:"direct_only,omitempty"`
	DeferLoading bool           `json:"defer_loading,omitempty"`
	CacheStable  bool           `json:"cache_stable,omitempty"`
}

type ModelLoadableToolV1 struct {
	Type         string         `json:"type"`
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema,omitempty"`
	DeferLoading bool           `json:"defer_loading,omitempty"`
}

// ModelToolResultV1 preserves the budgeted model projection as well as ordered
// content (including artifact references), structured data and native metadata.
type ModelToolResultV1 struct {
	Content           []ContentPart   `json:"content,omitempty"`
	StructuredContent json.RawMessage `json:"structured_content,omitempty"`
	Meta              json.RawMessage `json:"meta,omitempty"`
	IsError           bool            `json:"is_error,omitempty"`
	Activity          *ActivityRef    `json:"activity,omitempty"`
	ModelText         *string         `json:"model_text,omitempty"`
}

type ModelMessageV1 struct {
	Role                 string                  `json:"role"`
	Name                 string                  `json:"name,omitempty"`
	Content              string                  `json:"content,omitempty"`
	Hidden               bool                    `json:"hidden,omitempty"`
	Origin               string                  `json:"origin,omitempty"`
	OriginID             string                  `json:"origin_id,omitempty"`
	Cause                string                  `json:"cause,omitempty"`
	ReadOnly             bool                    `json:"read_only,omitempty"`
	Phase                string                  `json:"phase,omitempty"`
	ReasoningContent     string                  `json:"reasoning_content,omitempty"`
	ReasoningBlocks      []ModelReasoningBlockV1 `json:"reasoning_blocks,omitempty"`
	ProviderItems        []ModelProviderItemV1   `json:"provider_items,omitempty"`
	ProviderItemID       string                  `json:"provider_item_id,omitempty"`
	ProviderItemProvider string                  `json:"provider_item_provider,omitempty"`
	ProviderItemModel    string                  `json:"provider_item_model,omitempty"`
	Images               []ModelImageV1          `json:"images,omitempty"`
	Files                []ModelFileV1           `json:"files,omitempty"`
	ToolCallID           string                  `json:"tool_call_id,omitempty"`
	ToolResultKind       string                  `json:"tool_result_kind,omitempty"`
	ToolResult           *ModelToolResultV1      `json:"tool_result,omitempty"`
	ToolCalls            []ModelToolCallV1       `json:"tool_calls,omitempty"`
	DiscoveredTools      []ModelLoadableToolV1   `json:"discovered_tools,omitempty"`
	FinishReason         string                  `json:"finish_reason,omitempty"`
	StopReason           string                  `json:"stop_reason,omitempty"`
	Truncated            bool                    `json:"truncated,omitempty"`
}

// ModelInvocationOriginV1 is host-authenticated metadata, never plugin authority.
type ModelInvocationOriginV1 struct {
	Kind              string `json:"kind"`
	PluginID          string `json:"plugin_id,omitempty"`
	OperationID       string `json:"operation_id,omitempty"`
	ParentOperationID string `json:"parent_operation_id,omitempty"`
	SessionID         string `json:"session_id,omitempty"`
	ThreadID          string `json:"thread_id,omitempty"`
	StepIndex         int    `json:"step_index,omitempty"`
}

type ModelResponseV1 struct {
	Content           string                  `json:"content,omitempty"`
	Phase             string                  `json:"phase,omitempty"`
	ProviderItemID    string                  `json:"provider_item_id,omitempty"`
	ProviderItemModel string                  `json:"provider_item_model,omitempty"`
	ReasoningContent  string                  `json:"reasoning_content,omitempty"`
	ReasoningBlocks   []ModelReasoningBlockV1 `json:"reasoning_blocks,omitempty"`
	ProviderItems     []ModelProviderItemV1   `json:"provider_items,omitempty"`
	Images            []ModelImageV1          `json:"images,omitempty"`
	ToolCalls         []ModelToolCallV1       `json:"tool_calls,omitempty"`
	Usage             *ModelRequestUsage      `json:"usage,omitempty"`
	StopReason        string                  `json:"stop_reason,omitempty"`
	FinishReason      string                  `json:"finish_reason,omitempty"`
	Truncated         bool                    `json:"truncated,omitempty"`
}

type ModelErrorV1 struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ModelStreamEventV1 struct {
	// ResetPartial discards accumulated output from superseded attempts.
	ResetPartial bool `json:"reset_partial,omitempty"`
	// Metadata payloads are retained verbatim when forwarding provider streams.
	ProviderState     json.RawMessage        `json:"provider_state,omitempty"`
	Lifecycle         json.RawMessage        `json:"lifecycle,omitempty"`
	Type              string                 `json:"type"`
	Content           string                 `json:"content,omitempty"`
	Phase             string                 `json:"phase,omitempty"`
	ProviderItemID    string                 `json:"provider_item_id,omitempty"`
	Image             *ModelImageV1          `json:"image,omitempty"`
	Message           *ModelMessageV1        `json:"message,omitempty"`
	ReasoningBlock    *ModelReasoningBlockV1 `json:"reasoning_block,omitempty"`
	ToolCall          *ModelToolCallV1       `json:"tool_call,omitempty"`
	ProviderItem      *ModelProviderItemV1   `json:"provider_item,omitempty"`
	ProviderEventType string                 `json:"provider_event_type,omitempty"`
	Usage             *ModelRequestUsage     `json:"usage,omitempty"`
	StopReason        string                 `json:"stop_reason,omitempty"`
	FinishReason      string                 `json:"finish_reason,omitempty"`
	Truncated         bool                   `json:"truncated,omitempty"`
	Error             *ModelErrorV1          `json:"error,omitempty"`
}

type ModelRequestMiddlewareInput struct {
	Request LogicalModelRequestV1   `json:"request"`
	Origin  ModelInvocationOriginV1 `json:"origin"`
}

type ModelRequestMiddlewareOutput struct {
	Request *LogicalModelRequestV1 `json:"request,omitempty"`
}

type ModelResponseObserveInput struct {
	Phase   string                  `json:"phase"`
	Request LogicalModelRequestV1   `json:"request"`
	Origin  ModelInvocationOriginV1 `json:"origin"`
	Result  *ModelInvokeResult      `json:"result,omitempty"`
	Event   *ModelStreamEventV1     `json:"event,omitempty"`
	Error   *ModelErrorV1           `json:"error,omitempty"`
}

type ModelRequestObserveInput = ModelResponseObserveInput

type ModelMiddlewareInput struct {
	Version        int                     `json:"version"`
	Request        LogicalModelRequestV1   `json:"request"`
	Origin         ModelInvocationOriginV1 `json:"origin"`
	Stream         bool                    `json:"stream"`
	ContinuationID string                  `json:"continuation_id"`
}

type ModelMiddlewareOutput = ModelAdapterOutput

type ModelInvokeParams struct {
	ContinuationID  string                `json:"continuation_id,omitempty"`
	Request         LogicalModelRequestV1 `json:"request"`
	ModelAlias      string                `json:"model_alias,omitempty"`
	TimeoutMS       int                   `json:"timeout_ms,omitempty"`
	MaxOutputTokens int                   `json:"max_output_tokens,omitempty"`
}

type ModelInvokeResult struct {
	Response ModelResponseV1         `json:"response"`
	Origin   ModelInvocationOriginV1 `json:"origin"`
}

type ModelInvocationHandle struct {
	InvocationID string `json:"invocation_id"`
}
type ModelInvocationReadParams struct {
	InvocationID string `json:"invocation_id"`
	Cursor       int    `json:"cursor,omitempty"`
	WaitMS       int    `json:"wait_ms,omitempty"`
}
type ModelInvocationReadResult struct {
	Events []ModelStreamEventV1 `json:"events"`
	Cursor int                  `json:"cursor"`
	Done   bool                 `json:"done"`
	Result *ModelInvokeResult   `json:"result,omitempty"`
	Error  *ModelErrorV1        `json:"error,omitempty"`
}

type ModelAdapterDescriptor struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	// Mode is direct (default) or delegate. Direct callbacks are admitted as
	// potentially billable before invocation; delegate response usage is only
	// a summary of independently accounted host.model.invoke child calls.
	Mode string `json:"mode,omitempty"`
}
type ModelAdapterInput struct {
	AdapterID string                `json:"adapter_id"`
	Request   LogicalModelRequestV1 `json:"request"`
	Stream    bool                  `json:"stream"`
}

type ModelAdapterOutput struct {
	Response *ModelResponseV1 `json:"response,omitempty"`
}

type BackgroundStartParams struct {
	Key       string          `json:"key"`
	Service   string          `json:"service"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
	TimeoutMS int             `json:"timeout_ms,omitempty"`
}
type BackgroundHandle struct {
	JobID       string `json:"job_id"`
	ExecutionID string `json:"execution_id"`
}
type BackgroundJobParams struct {
	JobID string `json:"job_id"`
}
type BackgroundStatus struct {
	JobID  string          `json:"job_id"`
	State  string          `json:"state"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *ModelErrorV1   `json:"error,omitempty"`
}
type BackgroundCancelResult struct {
	Cancelled bool `json:"cancelled"`
}

type ContentPart struct {
	// RemoteRef is an output-only deferred-media reference carried by model snapshots.
	RemoteRef string                `json:"remote_ref,omitempty"`
	Type      string                `json:"type"`
	Text      string                `json:"text,omitempty"`
	Data      string                `json:"data,omitempty"`
	MIMEType  string                `json:"mime_type,omitempty"`
	URI       string                `json:"uri,omitempty"`
	Name      string                `json:"name,omitempty"`
	Resource  json.RawMessage       `json:"resource,omitempty"`
	Artifact  *ArtifactPresentation `json:"artifact,omitempty"`
}

type ArtifactPresentation struct {
	// Placement may be "inline" or "turn_end". Empty lets Wuu choose a
	// media-appropriate default.
	Placement string `json:"placement,omitempty"`
	Ref       string `json:"ref,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

type ActivityRef struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	State      string `json:"state,omitempty"`
	ThreadID   string `json:"thread_id,omitempty"`
	PreviewURI string `json:"preview_uri,omitempty"`
}

type ModelRequestUsage struct {
	InputTokens          int  `json:"input_tokens"`
	OutputTokens         int  `json:"output_tokens"`
	CacheCreationTokens  int  `json:"cache_creation_tokens"`
	CacheReadTokens      int  `json:"cache_read_tokens"`
	CacheCreationUnknown bool `json:"cache_creation_unknown"`
}
