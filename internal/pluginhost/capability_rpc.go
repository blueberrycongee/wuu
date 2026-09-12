package pluginhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// CapabilityRPC defines the typed, bidirectional extension protocol between
// the Wuu host and external plugin processes.
//
// ## Host → Plugin (capability calls)
//
// The host calls plugin-registered capabilities through capability.invoke.
// Each capability has a typed input/output contract.
//
// ## Plugin → Host (host service calls)
//
// Plugins call host services over the same bidirectional channel. The
// host exposes a fixed set of services for storage, settings, sub-agent
// spawning, and other controlled operations. Plugins declare required
// services during initialization; the host rejects activation if an
// unsupported service is required.
//
// ## Protocol version
//
// Protocol version 2 adds capability negotiation and is the only production
// Host-to-plugin extension seam. Protocol version 3 adds service
// provide/consume declarations routed through the generation-scoped service
// registry.
const (
	CapabilityProtocolVersion = 3
	CapabilityProtocolName    = "wuu-plugin-v2"
	// RuntimeLifecycleVersion identifies the side-effect-free prepare phase
	// followed by explicit post-commit activation.
	RuntimeLifecycleVersion = 1

	// CapabilityAgentRequestTransform lets a plugin transform the complete,
	// provider-neutral request immediately before provider validation and send.
	CapabilityAgentRequestTransform = "agent.request.transform"
	// CapabilityAgentPreStep lets a plugin append sourced, durable context at
	// the model-step boundary. Plugins own emission and cache policy.
	CapabilityAgentPreStep = "agent.pre_step"
	// CapabilityAgentSystemPromptSection contributes a generation-stable section
	// evaluated before that plugin generation can become active.
	CapabilityAgentSystemPromptSection = "agent.system_prompt.section"
	// CapabilityAgentCompaction is experimental. No distributed first-party
	// consumer has proven the plugin-owned conversation compactor contract.
	CapabilityAgentCompaction = "agent.compaction"
	// CapabilityAgentTurnCompleted observes a settled model turn after history
	// and usage have been persisted. It cannot alter host turn control flow.
	CapabilityAgentTurnCompleted = "agent.turn.completed"
	// CapabilityAgentTurnLifecycle receives state changes for ordinary turns
	// submitted by that same plugin through host.session.send. The host never
	// broadcasts these owner-scoped lifecycle events to other plugins.
	CapabilityAgentTurnLifecycle = "agent.turn.lifecycle"
	// CapabilityAgentTurnInterrupted is a best-effort, observe-only signal for
	// every active plugin generation when a turn is interrupted. The host does
	// not infer a task graph from this event; a plugin decides whether and how
	// to propagate it to work it owns.
	CapabilityAgentTurnInterrupted = "agent.turn.interrupted"
	// CapabilityPluginClientRequest handles a generation-bound opaque request
	// from a Wuu client. Method names and payload schemas belong to the plugin.
	CapabilityPluginClientRequest = "plugin.client.request"
)

// SeamKind classifies how the host dispatches one plugin capability. The
// capability RPC is the single production seam model: descriptors declare the
// kind, and the host owns ordering, short-circuiting, and error handling.
type SeamKind string

const (
	SeamObserve   SeamKind = "observe"
	SeamTransform SeamKind = "transform"
	SeamDecision  SeamKind = "decision"
)

// ErrorPolicy defines how capability dispatch handles a plugin error.
type ErrorPolicy string

const (
	ErrorPolicyPropagate ErrorPolicy = "propagate"
	ErrorPolicyIsolate   ErrorPolicy = "isolate"
	ErrorPolicyIgnore    ErrorPolicy = "ignore"
)

// HostOnlyCapabilities are safety-kernel extension points owned exclusively
// by the host. Plugins cannot declare them during capability negotiation.
var HostOnlyCapabilities = map[string]struct{}{
	"host.plugin.install":      {},
	"host.plugin.approval":     {},
	"host.plugin.enable":       {},
	"host.plugin.disable":      {},
	"host.plugin.upgrade":      {},
	"host.plugin.delete":       {},
	"host.safe_mode":           {},
	"host.crash_recovery":      {},
	"host.permission.final":    {},
	"host.window.lifecycle":    {},
	"host.appserver.lifecycle": {},
	"host.generation.isolate":  {},
	"host.escape.settings":     {},
	"host.escape.default_ui":   {},
}

// CapabilityNegotiationError marks an initialize response that cannot be
// activated safely. Runtime generation builders treat this as a hard rejection
// even when ordinary plugin startup failures are allowed in inventory.
type CapabilityNegotiationError struct{ Err error }

func (e *CapabilityNegotiationError) Error() string { return e.Err.Error() }
func (e *CapabilityNegotiationError) Unwrap() error { return e.Err }

func IsCapabilityNegotiationError(err error) bool {
	var target *CapabilityNegotiationError
	return errors.As(err, &target)
}

// CapabilityDescriptor declares one capability a plugin provides.
// Capabilities are typed, versioned, and ordered by priority.
type CapabilityDescriptor struct {
	// ID is a stable dotted identifier, e.g. "agent.request.transform".
	ID string `json:"id"`

	// Kind classifies the dispatch semantics (observe/transform/decision).
	Kind SeamKind `json:"kind"`

	// ErrorPolicy controls whether one plugin failure stops dispatch. Omitted
	// values default to propagate, except turn-completed observers which default
	// to isolate so telemetry cannot break a settled turn.
	ErrorPolicy ErrorPolicy `json:"error_policy,omitempty"`

	// Version is the capability contract version. Breaking changes increment
	// the major version.
	Version int `json:"version"`

	// Priority determines ordering when multiple plugins provide the
	// same capability. Higher values execute first.
	Priority int `json:"priority,omitempty"`

	// DependsOn lists capability IDs this capability requires from
	// other plugins or the host.
	DependsOn []string `json:"depends_on,omitempty"`

	// Conflicts lists capability IDs that must NOT be present. Used for
	// mutually exclusive capabilities (e.g., two compaction providers).
	Conflicts []string `json:"conflicts,omitempty"`
}

// HostServiceDescriptor declares a host service the plugin wants to use.
// Host services are the Plugin → Host direction of the capability RPC.
type HostServiceDescriptor struct {
	// ID is the service identifier, e.g. "host.storage.get".
	ID string `json:"id"`

	// Required indicates that the plugin cannot activate without this service.
	// Optional services are used if available; required services cause
	// activation failure if the host doesn't support them.
	Required bool `json:"required,omitempty"`
}

// ---------------------------------------------------------------------------
// Host service protocol (Plugin → Host)
// ---------------------------------------------------------------------------

// HostServiceMethod is a stable identifier for a host-exposed service method.
type HostServiceMethod string

const (
	// Storage
	HostServiceStorageGet             HostServiceMethod = "host.storage.get"
	HostServiceStorageSet             HostServiceMethod = "host.storage.set"
	HostServiceStorageDelete          HostServiceMethod = "host.storage.delete"
	HostServiceStorageKeys            HostServiceMethod = "host.storage.keys"
	HostServiceStorageCompareExchange HostServiceMethod = "host.storage.compare_exchange"

	// Settings
	HostServiceSettingsGet  HostServiceMethod = "host.settings.get"
	HostServiceSettingsList HostServiceMethod = "host.settings.list"

	// Sessions. Creation and input delivery are separate so ownership,
	// visibility, provenance, and idempotency remain explicit.
	HostServiceSessionCreate        HostServiceMethod = "host.session.create"
	HostServiceSessionSend          HostServiceMethod = "host.session.send"
	HostServiceSessionList          HostServiceMethod = "host.session.list"
	HostServiceSessionCancel        HostServiceMethod = "host.session.cancel"
	HostServiceSessionInspect       HostServiceMethod = "host.session.inspect"
	HostServiceSessionHistoryRead   HostServiceMethod = "host.session.history.read"
	HostServiceSessionHistorySearch HostServiceMethod = "host.session.history.search"

	// Workspaces. These methods operate only on an isolated workspace owned by
	// a plugin-created session; callers never supply filesystem paths.
	HostServiceWorkspaceStatus  HostServiceMethod = "host.workspace.status"
	HostServiceWorkspaceApply   HostServiceMethod = "host.workspace.apply"
	HostServiceWorkspaceDiscard HostServiceMethod = "host.workspace.discard"
)

const (
	MaxSessionSendRequestIDBytes    = 256
	MaxSessionSendContextBlocks     = 16
	MaxSessionSendContextBlockBytes = 64 * 1024
	MaxSessionSendContextTotalBytes = 256 * 1024
	MaxPreStepMessages              = 16
	MaxPreStepMessageIDBytes        = 96
	MaxPreStepMessageBytes          = 64 * 1024
	MaxPreStepTotalBytes            = 256 * 1024
)

const (
	SessionVisibilityUser          = "user"
	SessionVisibilityPlugin        = "plugin"
	SessionListScopeOwned          = "owned"
	SessionListScopeShared         = "shared"
	SessionContextFresh            = "fresh"
	SessionContextFork             = "fork"
	SessionContextSourceSeed       = "seed"
	SessionInputPlugin             = "plugin"
	SessionPresentationQueryBubble = "query_bubble"
	SessionIfRunningQueue          = "queue"
	SessionIfRunningSteer          = "steer"
	SessionInspectWaitNone         = "none"
	SessionInspectWaitTerminal     = "terminal"
)

// SessionToolPolicy can only remove tools from the child session's ordinary
// tool surface. Allow, when non-empty, is an allow-list; Deny is then applied
// on top. It never grants a tool or bypasses the host permission system.
type SessionToolPolicy struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

type SessionCreateParams struct {
	RequestID       string               `json:"request_id"`
	Name            string               `json:"name,omitempty"`
	Visibility      string               `json:"visibility"`
	ParentSessionID string               `json:"parent_session_id,omitempty"`
	ContextSource   string               `json:"context_source"`
	Workspace       string               `json:"workspace,omitempty"`
	WorkspaceID     string               `json:"workspace_id,omitempty"`
	WorkspaceRoot   string               `json:"workspace_root,omitempty"`
	ModelAlias      string               `json:"model_alias,omitempty"`
	Provider        string               `json:"provider,omitempty"`
	Model           string               `json:"model,omitempty"`
	Variant         string               `json:"variant,omitempty"`
	Effort          string               `json:"effort,omitempty"`
	PermissionMode  string               `json:"permission_mode,omitempty"`
	Instructions    string               `json:"instructions,omitempty"`
	ToolPolicy      *SessionToolPolicy   `json:"tool_policy,omitempty"`
	Seed            *SessionContextSeed  `json:"seed,omitempty"`
	Launch          *SessionLaunchParams `json:"launch,omitempty"`
}

type SessionContextSeed struct {
	Version    int                    `json:"version"`
	ID         string                 `json:"id,omitempty"`
	Body       string                 `json:"body"`
	Source     SessionHistorySnapshot `json:"source"`
	References []SessionHistoryRef    `json:"references,omitempty"`
	Artifacts  []SessionSeedArtifact  `json:"artifacts,omitempty"`
	Provenance SessionSeedProvenance  `json:"provenance"`
}

type SessionHistorySnapshot struct {
	SessionID  string `json:"session_id"`
	ThroughSeq int    `json:"through_seq"`
}

type SessionHistoryRef struct {
	ID      string              `json:"id"`
	Label   string              `json:"label,omitempty"`
	History SessionHistoryRange `json:"history"`
}

type SessionHistoryRange struct {
	Snapshot SessionHistorySnapshot `json:"snapshot"`
	StartSeq int                    `json:"start_seq"`
	EndSeq   int                    `json:"end_seq"`
}

type SessionSeedArtifact struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

type SessionSeedProvenance struct {
	Producer    string `json:"producer"`
	SourceModel string `json:"source_model,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

type SessionLaunchParams struct {
	Revision int    `json:"revision,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Intent   string `json:"intent,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
}

type SessionCreateResult struct {
	SessionID string `json:"session_id"`
	Created   bool   `json:"created"`
	// WorkspaceRoot is the effective directory the session runs in. It
	// differs from the project root when the session owns a worktree.
	WorkspaceRoot string `json:"workspace_root,omitempty"`
}

type SessionInput struct {
	Prompt        string                `json:"prompt"`
	ContextBlocks []SessionContextBlock `json:"context_blocks,omitempty"`
}

type SessionInputPresentation struct {
	Kind             string `json:"kind"`
	Text             string `json:"text"`
	Name             string `json:"name,omitempty"`
	RelatedSessionID string `json:"related_session_id,omitempty"`
}

type SessionSendParams struct {
	// ReplyToTurnID preserves the original turn scope when a plugin returns deferred work.
	ReplyToTurnID string                    `json:"reply_to_turn_id,omitempty"`
	RequestID     string                    `json:"request_id"`
	SessionID     string                    `json:"session_id"`
	Input         SessionInput              `json:"input"`
	Presentation  *SessionInputPresentation `json:"presentation,omitempty"`
	Cause         string                    `json:"cause,omitempty"`
	IfRunning     string                    `json:"if_running,omitempty"`
}

type SessionSendResult struct {
	State     string `json:"state"`
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id,omitempty"`
	QueueID   string `json:"queue_id,omitempty"`
	Steered   bool   `json:"steered,omitempty"`
}

type SessionListParams struct {
	ParentSessionID string `json:"parent_session_id,omitempty"`
	// Scope defaults to owned for compatibility. shared exposes active,
	// non-private session metadata without exposing conversation history.
	Scope string `json:"scope,omitempty"`
}

type SessionSummary struct {
	SessionID       string `json:"session_id"`
	Name            string `json:"name,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	Visibility      string `json:"visibility"`
	State           string `json:"state"`
	WorkspaceID     string `json:"workspace_id,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

type SessionListResult struct {
	Sessions []SessionSummary `json:"sessions"`
}

type SessionCancelParams struct {
	SessionID string `json:"session_id"`
	// TurnID narrows cancellation to one exact accepted turn. Empty preserves
	// the legacy session-scoped operation for callers that intentionally own
	// the whole session.
	TurnID string `json:"turn_id,omitempty"`
	// QueueID narrows cancellation to one queued turn that has not started.
	QueueID string `json:"queue_id,omitempty"`
}

type SessionCancelResult struct {
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id,omitempty"`
	QueueID   string `json:"queue_id,omitempty"`
	Cancelled bool   `json:"cancelled"`
}

type SessionInspectParams struct {
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Wait      string `json:"wait,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type SessionInspectResult struct {
	Session   SessionSummary           `json:"session"`
	Turn      *SessionTurnInspection   `json:"turn,omitempty"`
	Workspace *SessionWorkspaceSummary `json:"workspace,omitempty"`
	TimedOut  bool                     `json:"timed_out,omitempty"`
}

type SessionTurnInspection struct {
	RequestID    string     `json:"request_id,omitempty"`
	State        string     `json:"state"`
	TurnID       string     `json:"turn_id,omitempty"`
	QueueID      string     `json:"queue_id,omitempty"`
	Error        string     `json:"error,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	InputTokens  int        `json:"input_tokens,omitempty"`
	OutputTokens int        `json:"output_tokens,omitempty"`
	FinalOutput  string     `json:"final_output,omitempty"`
}

type SessionWorkspaceSummary struct {
	Kind         string   `json:"kind"`
	Dirty        bool     `json:"dirty,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
}

type WorkspaceStatusParams struct {
	SessionID string `json:"session_id"`
}

type WorkspaceStatusResult struct {
	SessionID     string   `json:"session_id"`
	Dirty         bool     `json:"dirty"`
	ChangedFiles  []string `json:"changed_files,omitempty"`
	Porcelain     []string `json:"porcelain,omitempty"`
	Diff          string   `json:"diff,omitempty"`
	CanApply      bool     `json:"can_apply"`
	ConflictFiles []string `json:"conflict_files,omitempty"`
	Error         string   `json:"error,omitempty"`
}

type WorkspaceApplyParams struct {
	SessionID string `json:"session_id"`
}

type WorkspaceApplyResult struct {
	SessionID    string   `json:"session_id"`
	Applied      bool     `json:"applied"`
	ChangedFiles []string `json:"changed_files,omitempty"`
	Discarded    bool     `json:"discarded"`
}

type WorkspaceDiscardParams struct {
	SessionID string `json:"session_id"`
}

type WorkspaceDiscardResult struct {
	SessionID string `json:"session_id"`
	Discarded bool   `json:"discarded"`
}

const (
	TurnLifecycleQueued      = "queued"
	TurnLifecycleRunning     = "running"
	TurnLifecycleCompleted   = "completed"
	TurnLifecycleFailed      = "failed"
	TurnLifecycleInterrupted = "interrupted"
	TurnLifecycleDiscarded   = "discarded"
)

// AgentTurnLifecycleInput is delivered only to the plugin that submitted the
// correlated turn. Initial running/queued state is returned synchronously by
// host.session.send; this event reports later transitions and terminal state.
type AgentTurnLifecycleInput struct {
	RequestID    string     `json:"request_id"`
	State        string     `json:"state"`
	ThreadID     string     `json:"thread_id"`
	TurnID       string     `json:"turn_id,omitempty"`
	QueueID      string     `json:"queue_id,omitempty"`
	Error        string     `json:"error,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	InputTokens  int        `json:"input_tokens,omitempty"`
	OutputTokens int        `json:"output_tokens,omitempty"`
	FinalOutput  string     `json:"final_output,omitempty"`
}

type AgentTurnLifecycleOutput struct{}

// AgentTurnInterruptedInput is delivered to every plugin that declares the
// observe-only interruption capability. It is a signal, not a cancellation
// tree edge: plugins own the policy for forwarding it to their work.
type AgentTurnInterruptedInput struct {
	ThreadID string `json:"thread_id"`
	TurnID   string `json:"turn_id"`
	Cause    string `json:"cause,omitempty"`
}

type AgentTurnInterruptedOutput struct{}

// HostServiceCall is a Plugin → Host RPC call.
type HostServiceCall struct {
	// ID is a caller-chosen correlation identifier.
	ID string `json:"id"`

	// Method is the host service being called.
	Method HostServiceMethod `json:"method"`

	// Params carries method-specific arguments.
	Params json.RawMessage `json:"params,omitempty"`
}

// HostServiceResult is the Host → Plugin response.
type HostServiceResult struct {
	// ID matches the originating HostServiceCall.ID.
	ID string `json:"id"`

	// Result carries method-specific return data.
	Result json.RawMessage `json:"result,omitempty"`

	// Error is non-nil when the call failed.
	Error *HostServiceError `json:"error,omitempty"`
}

// PluginClientRequestInput is a product-neutral client-to-plugin envelope.
type PluginClientRequestInput struct {
	Method string          `json:"method"`
	Input  json.RawMessage `json:"input,omitempty"`
}

// PluginClientRequestOutput carries one plugin-owned JSON result.
type PluginClientRequestOutput struct {
	Result json.RawMessage `json:"result,omitempty"`
}

// SessionContextBlock is request-only model context owned by the plugin.
type SessionContextBlock struct {
	Kind    string `json:"kind"`
	Title   string `json:"title,omitempty"`
	Source  string `json:"source,omitempty"`
	Content string `json:"content"`
}

// AgentTurnCompletedInput is a product-neutral settled-turn observation.
type AgentTurnCompletedInput struct {
	ThreadID     string    `json:"thread_id"`
	TurnID       string    `json:"turn_id"`
	StartedAt    time.Time `json:"started_at"`
	CompletedAt  time.Time `json:"completed_at"`
	Succeeded    bool      `json:"succeeded"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
}

type AgentTurnCompletedOutput struct{}

// HostServiceError describes a host service call failure.
type HostServiceError struct {
	// Code is a machine-readable error code.
	Code string `json:"code"`

	// Message is a human-readable description.
	Message string `json:"message"`
}

func (e *HostServiceError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// ---------------------------------------------------------------------------
// Host service parameter/result types
// ---------------------------------------------------------------------------

// StorageGetParams is the input for host.storage.get.
type StorageGetParams struct {
	Scope string `json:"scope"`
	Key   string `json:"key"`
}

// StorageGetResult is the output of host.storage.get.
type StorageGetResult struct {
	Value *string `json:"value"` // nil when key doesn't exist
}

// StorageSetParams is the input for host.storage.set.
type StorageSetParams struct {
	Scope string `json:"scope"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// StorageDeleteParams is the input for host.storage.delete.
type StorageDeleteParams struct {
	Scope string `json:"scope"`
	Key   string `json:"key"`
}

// StorageKeysParams is the input for host.storage.keys.
type StorageKeysParams struct {
	Scope string `json:"scope"`
}

// StorageKeysResult is the output of host.storage.keys.
type StorageKeysResult struct {
	Keys []string `json:"keys"`
}

// StorageCompareExchangeParams atomically replaces one plugin-owned value
// when its current value exactly matches Expected. Nil represents absence;
// a nil Value deletes the key after a successful comparison.
type StorageCompareExchangeParams struct {
	Scope    string  `json:"scope"`
	Key      string  `json:"key"`
	Expected *string `json:"expected"`
	Value    *string `json:"value"`
}

type StorageCompareExchangeResult struct {
	Swapped bool    `json:"swapped"`
	Value   *string `json:"value"`
}

// SettingsGetParams is the input for host.settings.get.
type SettingsGetParams struct {
	Key string `json:"key"`
}

// SettingsGetResult is the output of host.settings.get.
type SettingsGetResult struct {
	Value json.RawMessage `json:"value"`
}

// SettingsListResult is the output of host.settings.list.
type SettingsListResult struct {
	Entries map[string]json.RawMessage `json:"entries"`
}

// ---------------------------------------------------------------------------
// Capability negotiation
// ---------------------------------------------------------------------------

// CapabilityInitializeResult extends InitializeResult with capability
// negotiation for protocol v2+. Plugins declare which capabilities they
// provide and which host services they require.
type CapabilityInitializeResult struct {
	InitializeResult

	// Capabilities lists the capabilities this plugin provides.
	Capabilities []CapabilityDescriptor `json:"capabilities,omitempty"`

	// RequiredHostServices lists host services this plugin needs.
	// Activation fails if any required service is unavailable.
	RequiredHostServices []HostServiceDescriptor `json:"required_host_services,omitempty"`

	// ProvidedServices lists versioned services this plugin publishes into
	// the generation-scoped registry (capability protocol v3).
	ProvidedServices []ServiceDescriptor `json:"provided_services,omitempty"`

	// RequiredServices lists services this plugin consumes by name and major
	// version. An unsatisfied required service blocks activation with a
	// diagnostic; there is no dependency solver.
	RequiredServices []ServiceRequirement `json:"required_services,omitempty"`

	// ProtocolVersion is the capability protocol version the plugin
	// requests. The host may downgrade to v1 if v2 is unsupported.
	ProtocolVersion int `json:"protocol_version,omitempty"`

	// LifecycleVersion opts the runtime into prepare/activate semantics.
	LifecycleVersion int `json:"lifecycle_version,omitempty"`
}

// CapabilityInitializeParams extends InitializeParams with the host's
// capability RPC support declaration.
type CapabilityInitializeParams struct {
	InitializeParams

	// CapabilityProtocolVersion offers the capability layer independently of
	// the v1 transport version. Legacy plugins keep seeing protocol_version=1
	// and ignore this additive field.
	CapabilityProtocolVersion int `json:"capability_protocol_version,omitempty"`

	// SupportedHostServices lists the host services available to this plugin.
	// Plugins can use this to decide whether to enable optional features.
	SupportedHostServices []HostServiceMethod `json:"supported_host_services,omitempty"`

	// LifecycleVersion advertises the host's prepare/activate lifecycle.
	LifecycleVersion int `json:"lifecycle_version,omitempty"`
}

// CapabilityInvokeParams carries one typed capability invocation. Input and
// output are both present so transform capabilities can build on the value
// produced by an earlier, higher-priority plugin.
type CapabilityInvokeParams struct {
	Capability string `json:"capability"`
	// ExecutionID names this exact dispatch in the execution scope plane,
	// identical in role to ToolExecuteInput.ExecutionID.
	ExecutionID string          `json:"execution_id,omitempty"`
	Input       json.RawMessage `json:"input"`
	Output      json.RawMessage `json:"output"`
}

// CapabilityInvokeResult is the transformed value returned by a plugin.
type CapabilityInvokeResult struct {
	Output json.RawMessage `json:"output"`
}

// RequestTransformInput is immutable context for agent.request.transform.
type RequestTransformInput struct {
	SessionID string             `json:"session_id,omitempty"`
	ThreadID  string             `json:"thread_id,omitempty"`
	CWD       string             `json:"cwd,omitempty"`
	Provider  string             `json:"provider,omitempty"`
	StepIndex int                `json:"step_index"`
	Request   ModelRequestViewV1 `json:"request"`
}

// ModelRequestViewV1 is a read-only, versioned projection of the model request.
// It deliberately omits provider attempts, cache hints, media bytes, internal
// execution objects, and provider-native replay state.
type ModelRequestViewV1 struct {
	Version                     int                  `json:"version"`
	Model                       string               `json:"model"`
	Messages                    []ModelMessageViewV1 `json:"messages"`
	Tools                       []ModelToolViewV1    `json:"tools"`
	Temperature                 float64              `json:"temperature,omitempty"`
	MaxTokens                   int                  `json:"max_tokens,omitempty"`
	Effort                      string               `json:"effort,omitempty"`
	NativeDeferredToolDiscovery bool                 `json:"native_deferred_tool_discovery,omitempty"`
	ForceToolName               string               `json:"force_tool_name,omitempty"`
}

type ModelMessageViewV1 struct {
	Role            string                `json:"role"`
	Name            string                `json:"name,omitempty"`
	Content         string                `json:"content,omitempty"`
	Hidden          bool                  `json:"hidden,omitempty"`
	Origin          string                `json:"origin,omitempty"`
	OriginID        string                `json:"origin_id,omitempty"`
	Cause           string                `json:"cause,omitempty"`
	ReadOnly        bool                  `json:"read_only,omitempty"`
	HasImages       bool                  `json:"has_images,omitempty"`
	HasFiles        bool                  `json:"has_files,omitempty"`
	ToolCallID      string                `json:"tool_call_id,omitempty"`
	ToolCalls       []ModelToolCallViewV1 `json:"tool_calls,omitempty"`
	HasToolResult   bool                  `json:"has_tool_result,omitempty"`
	DiscoveredTools []string              `json:"discovered_tools,omitempty"`
}

type ModelToolCallViewV1 struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Kind      string `json:"kind,omitempty"`
}

type ModelToolViewV1 struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema"`
	DeferLoading bool           `json:"defer_loading,omitempty"`
}

// RequestTransformOutput is a deliberately narrow patch. New mutable fields
// require a real consumer and a versioned validator instead of exposing the
// host's internal ChatRequest.
type RequestTransformOutput struct {
	PrependSystemMessages []string `json:"prepend_system_messages,omitempty"`
}

// AgentPreStepInput is the immutable live transcript at a model-step boundary.
// Messages include durable plugin contributions so a plugin can own its own
// emit-on-change policy without host-managed state.
type AgentPreStepInput struct {
	SessionID string               `json:"session_id,omitempty"`
	ThreadID  string               `json:"thread_id,omitempty"`
	CWD       string               `json:"cwd,omitempty"`
	Provider  string               `json:"provider,omitempty"`
	Model     string               `json:"model,omitempty"`
	StepIndex int                  `json:"step_index"`
	Messages  []ModelMessageViewV1 `json:"messages"`
}

// AgentPreStepMessage is appended after the current transcript and persisted
// with host-stamped plugin provenance. ID is stable within the plugin.
type AgentPreStepMessage struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

type AgentPreStepOutput struct {
	AppendMessages []AgentPreStepMessage `json:"append_messages,omitempty"`
}

// SystemPromptSectionInput is immutable session context for the v1
// agent.system_prompt.section contract.
type SystemPromptSectionInput struct {
	CWD      string `json:"cwd"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// SystemPromptSectionOutput is one generation-stable prompt section.
type SystemPromptSectionOutput struct {
	Text string `json:"text"`
}

// CompactionInput is experimental immutable request context for
// agent.compaction. Operation and note fields are available in v2. Version 3
// opts into summary-free context windows with extension-owned working memory;
// it retains handoff_brief_plan without requiring note_plan inference.
type CompactionInput struct {
	Operation               string                  `json:"operation,omitempty"`
	Model                   string                  `json:"model"`
	Messages                []providers.ChatMessage `json:"messages"`
	Delta                   []providers.ChatMessage `json:"delta,omitempty"`
	PreviousNote            string                  `json:"previous_note,omitempty"`
	PreviousCoveredMessages int                     `json:"previous_covered_messages,omitempty"`
	Note                    string                  `json:"note,omitempty"`
	CoveredMessages         int                     `json:"covered_messages,omitempty"`
	Intent                  string                  `json:"intent,omitempty"`
	SourceSessionID         string                  `json:"source_session_id,omitempty"`
	SourceThroughSeq        int                     `json:"source_through_seq,omitempty"`
}

// CompactionOutput is the experimental replacement transcript returned by a compactor.
type CompactionOutput struct {
	Messages                 []providers.ChatMessage `json:"messages"`
	CoveredMessages          int                     `json:"covered_messages,omitempty"`
	NotePrompt               string                  `json:"note_prompt,omitempty"`
	CheckpointIntervalTokens int                     `json:"checkpoint_interval_tokens,omitempty"`
	MaxNoteBytes             int                     `json:"max_note_bytes,omitempty"`
	// Unavailable reports that the provider has no strategy for this
	// transcript. The host translates it into a fallback signal so the
	// default compactor can take over; Messages is ignored in that case.
	Unavailable bool `json:"unavailable,omitempty"`
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// allowedCapabilityKinds is the closed set of valid capability dispatch kinds.
var allowedCapabilityKinds = map[SeamKind]bool{
	SeamObserve:   true,
	SeamTransform: true,
	SeamDecision:  true,
}

// ValidateCapabilityDescriptor checks that a capability descriptor is well-formed.
func ValidateCapabilityDescriptor(c CapabilityDescriptor) error {
	id := strings.TrimSpace(c.ID)
	if id == "" {
		return fmt.Errorf("capability id is required")
	}
	if !strings.Contains(id, ".") {
		return fmt.Errorf("capability id %q must be a dotted identifier", id)
	}
	if _, hostOnly := HostOnlyCapabilities[id]; hostOnly {
		return fmt.Errorf("capability %s is owned exclusively by the host", id)
	}
	kind := SeamKind(strings.TrimSpace(string(c.Kind)))
	if kind == "" {
		return fmt.Errorf("capability %s: kind is required", id)
	}
	if !allowedCapabilityKinds[kind] {
		return fmt.Errorf("capability %s: unknown kind %q (valid: observe, transform, decision)", id, kind)
	}
	policy := EffectiveErrorPolicy(c)
	if policy != ErrorPolicyPropagate && policy != ErrorPolicyIsolate && policy != ErrorPolicyIgnore {
		return fmt.Errorf("capability %s: unknown error policy %q (valid: propagate, isolate, ignore)", id, policy)
	}
	if kind == SeamObserve && policy == ErrorPolicyPropagate {
		return fmt.Errorf("capability %s: observe capabilities require error policy isolate or ignore", id)
	}
	if kind != SeamObserve && policy == ErrorPolicyIgnore {
		return fmt.Errorf("capability %s: error policy ignore is only valid for observe capabilities", id)
	}
	if c.Version < 1 {
		return fmt.Errorf("capability %s: version must be >= 1, got %d", id, c.Version)
	}
	var requiredKind SeamKind
	switch c.ID {
	case CapabilityAgentRequestTransform:
		requiredKind = SeamTransform
	case CapabilityAgentPreStep:
		requiredKind = SeamTransform
	case CapabilityAgentSystemPromptSection:
		requiredKind = SeamTransform
	case CapabilityAgentCompaction:
		requiredKind = SeamDecision
	case CapabilityAgentTurnCompleted:
		requiredKind = SeamObserve
	case CapabilityAgentTurnLifecycle:
		requiredKind = SeamObserve
	case CapabilityAgentTurnInterrupted:
		requiredKind = SeamObserve
	case CapabilityPluginClientRequest:
		requiredKind = SeamDecision
	}
	if requiredKind != "" && c.Kind != requiredKind {
		return fmt.Errorf("capability %s requires kind %q", id, requiredKind)
	}
	if requiredKind != "" {
		maxVersion := 1
		if c.ID == CapabilityAgentCompaction {
			maxVersion = 3
		}
		if c.Version > maxVersion {
			return fmt.Errorf("capability %s requires a supported version between 1 and %d", id, maxVersion)
		}
	}
	if err := validateCapabilityRelations(c); err != nil {
		return err
	}
	return nil
}

// EffectiveErrorPolicy resolves descriptor defaults without mutating the
// negotiated contract stored by the host.
func EffectiveErrorPolicy(c CapabilityDescriptor) ErrorPolicy {
	if c.ErrorPolicy != "" {
		return c.ErrorPolicy
	}
	if c.ID == CapabilityAgentTurnCompleted || c.ID == CapabilityAgentTurnLifecycle || c.ID == CapabilityAgentTurnInterrupted {
		return ErrorPolicyIsolate
	}
	return ErrorPolicyPropagate
}

func validateCapabilityRelations(c CapabilityDescriptor) error {
	dependencies := make(map[string]struct{}, len(c.DependsOn))
	for _, dependency := range c.DependsOn {
		dependency = strings.TrimSpace(dependency)
		if dependency == "" || !strings.Contains(dependency, ".") {
			return fmt.Errorf("capability %s: dependency %q must be a dotted identifier", c.ID, dependency)
		}
		if dependency == c.ID {
			return fmt.Errorf("capability %s cannot depend on itself", c.ID)
		}
		if _, exists := dependencies[dependency]; exists {
			return fmt.Errorf("capability %s: duplicate dependency %q", c.ID, dependency)
		}
		dependencies[dependency] = struct{}{}
	}
	conflicts := make(map[string]struct{}, len(c.Conflicts))
	for _, conflict := range c.Conflicts {
		conflict = strings.TrimSpace(conflict)
		if conflict == "" || !strings.Contains(conflict, ".") {
			return fmt.Errorf("capability %s: conflict %q must be a dotted identifier", c.ID, conflict)
		}
		if conflict == c.ID {
			return fmt.Errorf("capability %s cannot conflict with itself", c.ID)
		}
		if _, exists := conflicts[conflict]; exists {
			return fmt.Errorf("capability %s: duplicate conflict %q", c.ID, conflict)
		}
		if _, exists := dependencies[conflict]; exists {
			return fmt.Errorf("capability %s cannot both depend on and conflict with %q", c.ID, conflict)
		}
		conflicts[conflict] = struct{}{}
	}
	return nil
}

// ValidateCapabilityNegotiation validates one initialize response against the
// services actually implemented by the host process.
func ValidateCapabilityNegotiation(result CapabilityInitializeResult, supported []HostServiceMethod) error {
	version := result.ProtocolVersion
	if version == 0 {
		version = ProtocolVersion
	}
	if version < ProtocolVersion || version > CapabilityProtocolVersion {
		return fmt.Errorf("unsupported negotiated protocol version %d", version)
	}
	if err := ValidateServiceNegotiation(result); err != nil {
		return err
	}
	if version == ProtocolVersion {
		if len(result.Capabilities) != 0 || len(result.RequiredHostServices) != 0 {
			return errors.New("protocol v1 cannot declare capabilities or host services")
		}
		return nil
	}

	seenCapabilities := make(map[string]struct{}, len(result.Capabilities))
	for _, capability := range result.Capabilities {
		if err := ValidateCapabilityDescriptor(capability); err != nil {
			return err
		}
		switch capability.ID {
		case CapabilityAgentRequestTransform, CapabilityAgentPreStep, CapabilityAgentSystemPromptSection, CapabilityAgentCompaction, CapabilityAgentTurnCompleted, CapabilityAgentTurnLifecycle, CapabilityAgentTurnInterrupted, CapabilityPluginClientRequest:
		default:
			return fmt.Errorf("capability %s is not supported by this host", capability.ID)
		}
		if _, exists := seenCapabilities[capability.ID]; exists {
			return fmt.Errorf("duplicate capability %q", capability.ID)
		}
		seenCapabilities[capability.ID] = struct{}{}
	}

	available := make(map[HostServiceMethod]struct{}, len(supported))
	for _, service := range supported {
		if err := ValidateHostServiceMethod(service); err != nil {
			return fmt.Errorf("host configured invalid service: %w", err)
		}
		available[service] = struct{}{}
	}
	seenServices := make(map[HostServiceMethod]struct{}, len(result.RequiredHostServices))
	for _, descriptor := range result.RequiredHostServices {
		service := HostServiceMethod(strings.TrimSpace(descriptor.ID))
		if service == "" {
			return errors.New("host service id is required")
		}
		if _, exists := seenServices[service]; exists {
			return fmt.Errorf("duplicate host service %q", service)
		}
		seenServices[service] = struct{}{}
		_, availableNow := available[service]
		if err := ValidateHostServiceMethod(service); err != nil {
			if descriptor.Required {
				return fmt.Errorf("required host service %q is unsupported", service)
			}
			continue
		}
		if descriptor.Required && !availableNow {
			return fmt.Errorf("required host service %q is unavailable", service)
		}
	}
	return nil
}

// ValidateHostServiceMethod checks that a host service method is recognized.
func ValidateHostServiceMethod(m HostServiceMethod) error {
	switch m {
	case ServiceCallMethod:
		return nil
	default:
		return fmt.Errorf("unknown host service method %q", m)
	}
}

func cloneCapabilityDescriptors(capabilities []CapabilityDescriptor) []CapabilityDescriptor {
	cloned := make([]CapabilityDescriptor, len(capabilities))
	for index, capability := range capabilities {
		cloned[index] = capability
		cloned[index].DependsOn = append([]string(nil), capability.DependsOn...)
		cloned[index].Conflicts = append([]string(nil), capability.Conflicts...)
	}
	return cloned
}

func cloneServiceDescriptors(services []ServiceDescriptor) []ServiceDescriptor {
	cloned := make([]ServiceDescriptor, len(services))
	for index, service := range services {
		cloned[index] = service
		cloned[index].Methods = append([]ServiceMethodDescriptor(nil), service.Methods...)
	}
	return cloned
}

func cloneServiceRequirements(requirements []ServiceRequirement) []ServiceRequirement {
	return append([]ServiceRequirement(nil), requirements...)
}
