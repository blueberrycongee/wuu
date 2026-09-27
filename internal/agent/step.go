// Package agent: Step is the transport-agnostic abstraction the
// shared tool-use loop drives. Both Runner and StreamRunner execute
// through Step (Runner adapts providers.Client to StreamClient first),
// so the actual loop logic — step counting, tool execution,
// finish handling, context-overflow auto-compact — lives in
// exactly one place. See loop.go.
package agent

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// StepResult is the outcome of one model round-trip, normalized so
// the loop doesn't care whether the response came from a one-shot
// Chat or a fully-consumed SSE stream.
type StepResult struct {
	// Content is the assistant's text for this round (concatenation
	// of all content deltas in the streaming case).
	Content string
	Images  []providers.InputImage
	// Phase is the provider-supplied assistant message phase when
	// available. Empty means unknown and callers may infer from tool use.
	Phase providers.MessagePhase
	// ProviderItemID preserves provider-native assistant output item identity
	// for same-model replay on APIs such as OpenAI Responses.
	ProviderItemID       string
	ProviderItemProvider string
	ProviderItemModel    string
	// ReasoningContent is provider-emitted hidden reasoning for this
	// round. Some OpenAI-compatible providers require replaying it
	// verbatim on follow-up assistant tool-call messages.
	ReasoningContent string
	// ReasoningBlocks preserves provider-native thinking payloads that
	// cannot safely be reconstructed from ReasoningContent text alone.
	ReasoningBlocks []providers.ReasoningBlock
	// ToolCalls is the ordered list of tool invocations the model
	// requested in this round, fully assembled (arguments included).
	ToolCalls []providers.ToolCall
	// Truncated is true when the provider signalled an output-token
	// cap (Anthropic stop_reason=max_tokens, OpenAI finish_reason=length).
	// It is a completed model response whose canonical FinishReason is length.
	Truncated bool
	// FinishReason is the provider-neutral reason this model response ended.
	FinishReason providers.FinishReason
	// StopReason is the lowercase normalized stop signal. Surfaced
	// for diagnostics; FinishReason carries the cross-provider semantic.
	StopReason string
	// Usage is the per-round token consumption when the provider
	// reports it. nil is allowed.
	Usage *providers.TokenUsage
	// ToolRuntime owns any tool calls that were started while the provider
	// was still streaming. The loop uses it to wait for those same runs
	// instead of issuing duplicate executions.
	ToolRuntime *TurnToolRuntime
}

// Step performs exactly one model round-trip and returns the result
// in normalized form. Implementations encapsulate the transport
// (one-shot vs streaming) and any per-round live-rendering side
// effects; the loop above doesn't observe those.
type Step interface {
	Execute(ctx context.Context, req providers.ChatRequest) (StepResult, error)
}

// CompactFn compresses a conversation that has overflowed the model's
// context window. The loop calls it once when the underlying step
// returns a context-overflow error, then re-issues the same step.
//
// Implementations are expected to wrap whatever provider-side
// summarization they need; the loop is intentionally agnostic.
type CompactFn func(ctx context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error)

// CompactReason classifies why the loop ran a compact pass.
type CompactReason string

const (
	// CompactReasonProactive means the loop hit its proactive usable-window
	// threshold and ran a compact preemptively to avoid overflow.
	CompactReasonProactive CompactReason = "proactive"
	// CompactReasonOverflow means a step.Execute returned a
	// context-overflow error and the loop ran compact reactively as
	// the recovery path.
	CompactReasonOverflow CompactReason = "overflow"
	// CompactReasonNewContext means the model requested a fresh note-backed
	// context window. The host applies it after the complete tool batch.
	CompactReasonNewContext CompactReason = "new_context"
	// CompactReasonManual means the user explicitly requested a compact
	// pass (the /compact slash command); it runs before the first model
	// request of the turn regardless of the fill-rate threshold.
	CompactReasonManual CompactReason = "manual"
)

// CompactAttemptStatus records the outcome of a compact attempt for telemetry.
type CompactAttemptStatus string

const (
	CompactAttemptSucceeded CompactAttemptStatus = "succeeded"
	CompactAttemptFailed    CompactAttemptStatus = "failed"
	CompactAttemptUnchanged CompactAttemptStatus = "unchanged"
)

// CompactInfo describes a compact pass that just ran. Surfaced via
// LoopConfig.OnCompact so interactive clients can let the user know
// what just happened.
type CompactInfo struct {
	Reason       CompactReason
	TokensBefore int
	TokensAfter  int
	// Message counts use user-facing conversation units: preserved system
	// scaffolding and hidden bookkeeping are excluded, and the replacement
	// continuation summary counts as one message.
	MessagesBefore int
	MessagesAfter  int
	// Summary is the body of the replacement context produced by this pass
	// (the "[Conversation summary]" handoff content without its wrapper).
	// Empty when the pass did not replace history. This is durable
	// conversation state, not raw prompt text, so it is safe to surface in
	// the UI; it is intentionally kept out of CompactAttemptInfo, whose
	// records stay metadata-only for traces.
	Summary string
}

// CompactAttemptInfo describes every compact attempt, including failures and
// no-op results. It is metadata-only and must not include raw prompt text.
type CompactAttemptInfo struct {
	Reason            CompactReason
	Status            CompactAttemptStatus
	TokensBefore      int
	LastResponseTotal int
	PendingDelta      int
	UsageAdjustment   UsageAdjustment
	MessagesBefore    int
	MessagesAfter     int
	Error             string
	OutputLimit       bool
}

// ToolBatchRejectionInfo describes a whole assistant tool-call batch that was
// rejected before execution because a history-rewriting barrier tool appeared
// with sibling tool calls. It is metadata-only and excludes raw arguments.
type ToolBatchRejectionInfo struct {
	StepIndex     int
	BarrierTool   string
	SiblingTools  []string
	ToolCallCount int
}

// RequestContextInfo summarizes request-only model context assembled before a
// provider call. It intentionally excludes raw prompt text.
type RequestContextInfo struct {
	StepIndex                int
	TransientMessages        int
	ContentBytes             int
	BlockKinds               []string
	BlockKindCounts          map[string]int
	BlockKindBytes           map[string]int
	SegmentLifecycleCounts   map[string]int
	SegmentPlacementCounts   map[string]int
	SegmentCachePolicyCounts map[string]int
	MessageCount             int
	SystemMessages           int
	HiddenMessages           int
	ToolCount                int
	StablePrefix             int
	TurnPrefix               int
	DynamicBytes             int
	SystemBytes              int
	StablePrefixBytes        int
	TurnPrefixBytes          int
	MessageBytes             int
	ToolSchemaBytes          int
	LoadableToolCount        int
	LoadableToolSchemaBytes  int
	LoadableToolSurfaceHash  string
	SystemHash               string
	StablePrefixHash         string
	TurnPrefixHash           string
	ToolSurfaceHash          string
	PromptCacheKey           string
	InputTokens              int
	OutputTokens             int
	CacheCreationTokens      int
	CacheReadTokens          int
	SystemSections           []SystemPromptSectionInfo
}

// SystemPromptSectionInfo describes one section of the assembled system prompt
// without exposing the raw prompt text.
type SystemPromptSectionInfo struct {
	Key    string
	Static bool
	Bytes  int
	Hash   string
}

// LoopConfig bundles every knob the shared loop needs. All callbacks
// are optional. Tools is required if the model is allowed to call any.
type LoopConfig struct {
	// Tools is the executor used to run model-requested tool calls.
	// May be nil if the caller knows the model has no tools available.
	Tools ToolExecutor
	// Model is the model identifier passed through to the provider.
	Model string
	// ProviderName is Wuu's configured provider identity. It never goes on the
	// wire; provider adapters use it to filter provider-native replay state.
	ProviderName string
	// ProviderObservationKey is an opaque identity for the configured provider
	// instance plus its endpoint/protocol. Reactive compaction only reuses a
	// successful request observation when this key, the API model, variant, and
	// provider options all match. Empty falls back to ProviderName isolation.
	ProviderObservationKey string
	// ModelVariant is the selected model-scoped option bundle identity.
	ModelVariant string
	// InferenceOperationKind and InferenceWorkloadProfile classify each
	// provider request for the shared retry lifecycle.
	InferenceOperationKind   providers.InferenceOperationKind
	InferenceWorkloadProfile providers.InferenceWorkloadProfile
	SessionID                string
	ExecutionID              string
	DriverID                 string
	DriverVersion            string
	ModelInputReceiptStore   ModelInputReceiptStore
	// Temperature is the sampling temperature; 0 means provider default.
	Temperature float64
	// MediaInput is the admission policy for user-supplied media on every
	// request this loop builds. Unprobed (auto) kinds are refined from the
	// process-local probe cache at request build time.
	MediaInput providers.MediaInputPolicy
	// MaxSteps caps the number of model round-trips per Run. Zero means
	// unlimited; positive values act as a runaway safety net.
	MaxSteps int
	// Compact is invoked when the loop wants to summarize the older
	// conversation (proactive fill-rate trigger or reactive
	// context-overflow recovery). nil disables both auto-compact
	// paths; the overflow error is propagated to the caller as-is.
	Compact CompactFn
	// MaxContextTokens is the model's context window. When non-zero,
	// the loop tracks usage from response.usage plus local estimates
	// and proactively triggers a compact pass once the conversation
	// exceeds the configured fraction or the output-reserved usable
	// input window. Zero disables proactive compact (the reactive
	// overflow path still works).
	MaxContextTokens int
	// MaxInputTokens is the provider's prompt/input limit when it is
	// lower than the full context window. Some APIs advertise a large
	// total context window but reserve a large output budget server-side;
	// proactive compact must respect the smaller input side.
	MaxInputTokens int
	// OutputReserveTokens is the model output limit used for context
	// budgeting. It does not force the request's max_tokens; DefaultMaxTokens
	// remains the request cap.
	OutputReserveTokens int
	// CompactThresholdTokens is the modelbudget-derived absolute trigger for
	// proactive compaction. When set it takes precedence over the loop's own
	// window-based derivation so the trigger always matches what trace/UI
	// report. Zero falls back to the legacy derivation.
	CompactThresholdTokens int
	// CompactThresholdPct overrides the default usable-window trigger with a
	// fraction of the configured input/context window. Zero means use the
	// default usable-window calculation.
	CompactThresholdPct float64
	// CompactKeepRecentTokens overrides the default recent raw-history budget
	// kept after compaction. Zero means use the default.
	CompactKeepRecentTokens int
	// ForceInitialCompact runs one compact pass at run entry, bypassing the
	// fill-rate threshold. CompactOnly turns this into a control-plane compact
	// turn; without CompactOnly the run continues to the provider request.
	// Requires Compact to be set; a failed or no-op forced pass does not
	// suppress later proactive passes.
	ForceInitialCompact bool
	// CompactOnly returns after the forced compact pass instead of sending a
	// normal provider request. Used for control-plane /compact turns.
	CompactOnly bool
	// FreshContext installs a bounded note-backed context without a model call.
	// ArchiveHistory must commit original in-run messages before that context is
	// released. Both must be set before new_context or hard rollover is enabled.
	FreshContext       FreshContextBuilder
	ArchiveHistory     HistoryArchiveFunc
	FreshContextTokens int
	AcceptFreshContext func(context.Context, []providers.ChatMessage, int) ([]providers.ChatMessage, int, error)
	// ContextWindowStatus supplies a read-only checkpoint snapshot without
	// waiting for generation or asking the main model to manage background work.
	ContextWindowStatus func(context.Context, []providers.ChatMessage) string
	// OnHistoryAdvanced must return immediately. StreamRunner uses it to launch
	// best-effort background note refreshes while the main loop continues.
	OnHistoryAdvanced func([]providers.ChatMessage)
	// ToolWaitInterrupt supplies a turn-scoped signal to wait-only tools that
	// can safely return while leaving their underlying work alive.
	ToolWaitInterrupt func() <-chan struct{}
	// RetainedRequestContext, when set, is the previous run's retained
	// request-only context (LoopResult.RetainedRequestContext). If it still
	// matches the incoming durable history it is spliced back into the
	// provider transcript so this run's first request byte-extends the
	// previous run's last request for prompt-cache continuity. Invalid or
	// stale state is ignored.
	RetainedRequestContext *RetainedRequestContextState
	// BeforeStep, when set, is called at the start of each model
	// round. Any returned messages are appended to the live history
	// before the next provider request is built. This is used by
	// sub-agent follow-up messaging: send_message queues
	// user-role messages that are injected on the next round.
	BeforeStep func() []providers.ChatMessage
	// BeforeModelStep is the context-aware extension boundary for durable,
	// append-only messages. The input is an immutable snapshot of live history.
	BeforeModelStep func(context.Context, int, []providers.ChatMessage) ([]providers.ChatMessage, error)
	// BeforeRequestContext, when set, is called after live-history updates
	// and before each provider request. Returned segments are assembled into
	// the provider request without being appended to live or durable history.
	BeforeRequestContext func() []ContextSegment
	// BeforeRequest runs after Wuu has assembled the complete provider-neutral
	// request and before cache telemetry or provider serialization. Plugin hosts
	// use it to transform messages, model parameters, and tool definitions.
	// Returning an error aborts the current model round.
	//
	// Prefer RequestTransforms for new code; it supports multi-plugin chains.
	BeforeRequest func(context.Context, *providers.ChatRequest) error
	// RequestTransforms is the pluggable request transform chain. When set,
	// all registered transforms execute in priority order after BeforeRequest
	// (if any). This replaces ad-hoc single-callback transforms with a
	// registry-based chain that supports dependency ordering and priority.
	RequestTransforms *RequestTransformChain
	// CompactionRegistry, when set, resolves the active compaction strategy
	// for this run. The highest-priority registered provider wins. When nil,
	// the legacy Compact field is used.
	CompactionRegistry *CompactionRegistry
	// CompactionNoteStore persists hidden Markdown checkpoints for fork-capable
	// compaction providers. ForkCompactionNote performs the provider request
	// while preserving the main request's model and tool surface.
	CompactionNoteStore CompactionNoteStore
	ForkCompactionNote  CompactionNoteFork
	// OnCompactionNote reports whether a compact pass used an existing note,
	// forced a fresh fork, or failed to produce one.
	OnCompactionNote func(status string, err error)
	// SystemPromptSections is metadata for the stable system prompt in the
	// live history. It is emitted as telemetry only and never sent to providers.
	SystemPromptSections []SystemPromptSectionInfo
	// OnRequestContext receives a metadata-only summary of request-only model
	// context assembled before a request.
	OnRequestContext func(info RequestContextInfo)
	// OnUsage is invoked once per LLM round-trip with the per-call
	// token counts when the provider reports them. The loop also
	// accumulates totals into LoopResult.
	OnUsage func(input, output int)
	// OnTokenUsage is invoked once per LLM round-trip with the full
	// provider token usage, including prompt cache read/write counts.
	OnTokenUsage func(usage providers.TokenUsage)
	// OnMessage is invoked whenever the loop appends a visible semantic chat
	// message to its live history. Request-only context is never emitted as a
	// message event.
	OnMessage func(msg providers.ChatMessage)
	// OnToolResult is invoked after each tool execution with the
	// (call, JSON result) pair. Used by streaming callers to feed
	// live tool-result rendering into clients.
	OnToolResult func(call providers.ToolCall, result string)
	// OnToolResultDetail receives the lossless canonical result. New callers
	// should prefer it; OnToolResult remains as a compatibility projection.
	OnToolResultDetail func(call providers.ToolCall, result toolresult.Result)
	// OnCompactStart is invoked immediately before a potentially slow compact
	// pass begins so interactive clients can render real progress.
	OnCompactStart func(reason CompactReason)
	// BeforeCompact runs immediately before the compaction implementation.
	// Returning an error rejects that compact attempt.
	BeforeCompact func(context.Context, CompactReason) error
	// AfterCompact runs after the compaction implementation returns and before
	// a replacement transcript is accepted. Returning an error rejects it.
	AfterCompact func(context.Context, CompactReason, error) error
	// OnCompact is invoked once per compact pass (proactive or
	// reactive). Optional; clients can use it to render a status line.
	OnCompact func(info CompactInfo)
	// OnCompactAttempt is invoked for every compact attempt, including
	// failed and no-op attempts. It is intended for diagnostics and traces.
	OnCompactAttempt func(info CompactAttemptInfo)
	// OnToolBatchRejected is invoked when the loop rejects an entire
	// assistant tool-call batch before execution because a barrier tool was
	// called with siblings.
	OnToolBatchRejected func(info ToolBatchRejectionInfo)
	// UsageTracker, when non-nil, is the caller-owned conversation
	// usage state to reuse across runs. This lets the loop make the
	// same compact decision before the first request of a new turn
	// that it would make mid-run after receiving fresh usage.
	UsageTracker *UsageTracker

	// DefaultMaxTokens is the output token cap sent on every request.
	// Zero means the provider's default (e.g. 16 384 for Anthropic).
	DefaultMaxTokens int
	// EscalatedMaxTokens is retained for config compatibility. Output
	// truncation now completes the turn with FinishReason=length instead
	// of issuing an automatic continuation request.
	EscalatedMaxTokens int
	// Effort controls reasoning depth. Empty = API default. Valid:
	//   Anthropic: "low", "medium", "high", "max"
	//   OpenAI:    "low", "medium", "high"
	// This maps to each provider's reasoning-depth control.
	Effort string
	// ProviderOptions are provider-specific model options selected by the
	// active model variant. They are forwarded to ChatRequest.
	ProviderOptions map[string]any
	// NativeDeferredToolDiscovery lets provider adapters use native
	// deferred-tool declarations for tools marked DeferLoading.
	NativeDeferredToolDiscovery bool
	// PromptCacheKey, when set, overrides the content-derived fallback
	// key in CacheHint for providers with explicit prompt-cache routing.
	PromptCacheKey string
	// ForceToolFirstStep, when non-empty, pins the FIRST request of this
	// loop to the named tool via the provider's forced tool_choice. Later
	// steps run unforced so the model can finish normally after the tool
	// result returns. Used for mechanical closing turns (agent_report).
	ForceToolFirstStep string
}

// LoopResult is what RunToolLoop returns on success.
type LoopResult struct {
	// Content is the model's final assistant message for this run.
	Content string
	// NewMessages is the slice of messages produced during this run
	// (assistant turns + tool result turns) in order. When a compact
	// pass rewrote the live history mid-run, this becomes the full
	// replacement history snapshot and HistoryRewritten is true.
	NewMessages []providers.ChatMessage
	// DurableNewMessages contains original messages not already archived during
	// this run. It differs from NewMessages after a context rewrite, whose value
	// is the active replacement snapshot.
	DurableNewMessages     []providers.ChatMessage
	DurableMessagesTracked bool
	// HistoryArchiveHeadSeq is the physical transcript head after the most recent
	// in-run archive required by a fresh-context transition.
	HistoryArchiveHeadSeq int
	// HistoryRewritten reports whether a compact pass replaced the
	// live history slice mid-run. Callers that persist conversations
	// should replace stored history instead of append-only extending
	// it when this is true.
	HistoryRewritten bool
	// RetainedRequestContext is the request-only context retained in this
	// run's provider transcript, fingerprinted against the durable history
	// the run ended with. Long-lived callers hand it back via
	// LoopConfig.RetainedRequestContext on the next run of the same
	// conversation for cross-run prompt-cache continuity.
	RetainedRequestContext *RetainedRequestContextState
	// InputTokens / OutputTokens are the cumulative usage across
	// every round in this run, including compact requests. Zero when
	// the provider doesn't report usage.
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
	// ContextTokens is the post-run provider-context estimate used by
	// auto-compact. It includes retained request-only context because those
	// messages remain in subsequent provider requests and occupy the window.
	ContextTokens int
	FinishReason  providers.FinishReason
	StopReason    string
	Truncated     bool
	// Driver metadata is the versioned policy identity and terminal checkpoint
	// associated with this result. The checkpoint is opaque to callers.
	DriverID              string
	DriverVersion         string
	DriverContractVersion int
	DriverStatus          string
	DriverCheckpoint      json.RawMessage
}
