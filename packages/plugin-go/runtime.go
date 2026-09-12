// Package pluginapi implements the public multiplexed JSON-lines runtime used
// by Wuu plugin helper processes. Host requests and plugin-initiated host
// service calls share one full-duplex channel and may be in flight at the same
// time. The package intentionally contains no imports from Wuu's internal
// Agent, app-server, or Desktop implementations. The host embedding facade
// lives in github.com/blueberrycongee/wuu/internal/sdk and is not a public API.
package pluginapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

const CapabilityProtocolVersion = 3
const RuntimeLifecycleVersion = 1

const (
	// HostServiceCallMethod is the plugin -> host gateway for consuming a
	// registered service. CallService emits it.
	HostServiceCallMethod = "host.service.call"
	// ServiceInvokeMethod is the host -> plugin request delivering one
	// validated call to a service provider.
	ServiceInvokeMethod = "service.invoke"
	// ServiceChangedMethod is the host -> plugin notification that a service
	// resolution changed.
	ServiceChangedMethod = "service.changed"
	// ExecutionCancelMethod is the host -> plugin fire-and-forget signal
	// translating core context cancellation of one exact execution. It is
	// handled out of band so it can preempt the running execution; any
	// acknowledgement the plugin writes is discarded by the host.
	ExecutionCancelMethod = "execution.cancel"
	// ExecutionUpdateService is the kernel service a plugin calls to report
	// progress for an execution it owns.
	ExecutionUpdateService   = "execution.update"
	ArtifactImportService    = "host.artifact.import"
	SecurityAuthorizeService = "security.authorize"
	ProcessSandboxService    = "sandbox.process"
	SecurityAuthorizeMethod  = "authorize"
	ProcessSandboxMethod     = "confine"
)

const (
	HostServiceStorageGet             = "host.storage.get"
	HostServiceStorageSet             = "host.storage.set"
	HostServiceStorageDelete          = "host.storage.delete"
	HostServiceStorageKeys            = "host.storage.keys"
	HostServiceSessionCreate          = "host.session.create"
	HostServiceSessionSend            = "host.session.send"
	HostServiceSessionList            = "host.session.list"
	HostServiceSessionCancel          = "host.session.cancel"
	HostServiceSessionInspect         = "host.session.inspect"
	HostServiceSessionHistoryRead     = "host.session.history.read"
	HostServiceSessionHistorySearch   = "host.session.history.search"
	HostServiceWorkspaceStatus        = "host.workspace.status"
	HostServiceWorkspaceApply         = "host.workspace.apply"
	HostServiceWorkspaceDiscard       = "host.workspace.discard"
	HostServiceStorageCompareExchange = "host.storage.compare_exchange"
	HostServiceSettingsGet            = "host.settings.get"
	HostServiceSettingsList           = "host.settings.list"
	CapabilityAgentTurnLifecycle      = "agent.turn.lifecycle"
	CapabilityAgentTurnInterrupted    = "agent.turn.interrupted"
	CapabilityAgentPreStep            = "agent.pre_step"
	SessionIfRunningQueue             = "queue"
	SessionIfRunningSteer             = "steer"
	SessionListScopeOwned             = "owned"
	SessionListScopeShared            = "shared"
	SessionInspectWaitNone            = "none"
	SessionInspectWaitTerminal        = "terminal"
)

const (
	StorageScopeUser      = "user"
	StorageScopeWorkspace = "workspace"
)

const (
	KernelStorageGetService             = "host.storage.get"
	KernelStorageSetService             = "host.storage.set"
	KernelStorageDeleteService          = "host.storage.delete"
	KernelStorageKeysService            = "host.storage.keys"
	KernelStorageCompareExchangeService = "host.storage.compare-exchange"
	KernelSettingsGetService            = "host.settings.get"
	KernelSettingsListService           = "host.settings.list"
	KernelSessionCreateService          = "host.session.create"
	KernelSessionSendService            = "host.session.send"
	KernelSessionListService            = "host.session.list"
	KernelSessionCancelService          = "host.session.cancel"
	KernelSessionInspectService         = "host.session.inspect"
	KernelSessionHistoryReadService     = "host.session.history.read"
	KernelSessionHistorySearchService   = "host.session.history.search"
	KernelWorkspaceStatusService        = "host.workspace.status"
	KernelWorkspaceApplyService         = "host.workspace.apply"
	KernelWorkspaceDiscardService       = "host.workspace.discard"
	KernelUserQuestionAskService        = "host.user-question.ask"
	KernelUserQuestionOfferService      = "host.user-question.offer"
	KernelArtifactImportService         = "host.artifact.import"
	KernelServiceMethod                 = "call"
)

type InitializeParams struct {
	ProtocolVersion           int      `json:"protocol_version"`
	CapabilityProtocolVersion int      `json:"capability_protocol_version,omitempty"`
	PluginID                  string   `json:"plugin_id"`
	PluginRoot                string   `json:"plugin_root"`
	ProjectRoot               string   `json:"project_root"`
	WorkspaceID               string   `json:"workspace_id,omitempty"`
	WuuHome                   string   `json:"wuu_home"`
	WorkspaceStateDir         string   `json:"workspace_state_dir,omitempty"`
	SupportedHostServices     []string `json:"supported_host_services,omitempty"`
	LifecycleVersion          int      `json:"lifecycle_version,omitempty"`
}

type Capability struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	ErrorPolicy string `json:"error_policy,omitempty"`
	Version     int    `json:"version"`
	Priority    int    `json:"priority,omitempty"`
}

type HostService struct {
	ID       string `json:"id"`
	Required bool   `json:"required,omitempty"`
}

// ServiceMethod declares one typed method of a provided service.
type ServiceMethod struct {
	Name         string `json:"name"`
	InputSchema  string `json:"input_schema"`
	OutputSchema string `json:"output_schema"`
}

// Service declares a versioned service this plugin provides. The name is
// stable across versions; consumers resolve by name and major version.
type Service struct {
	Name    string          `json:"name"`
	Version string          `json:"version"`
	Methods []ServiceMethod `json:"methods"`
}

func AuthorizationService() Service {
	return Service{Name: SecurityAuthorizeService, Version: "1.0.0", Methods: []ServiceMethod{{
		Name: SecurityAuthorizeMethod, InputSchema: "security.authorize.input.v1", OutputSchema: "security.authorize.output.v1",
	}}}
}

func ProcessSandboxProviderService() Service {
	return Service{Name: ProcessSandboxService, Version: "1.0.0", Methods: []ServiceMethod{{
		Name: ProcessSandboxMethod, InputSchema: "sandbox.process.input.v1", OutputSchema: "sandbox.process.output.v1",
	}}}
}

// ServiceRequirement declares a service this plugin consumes. Declaring it is
// the only way to gain authority to call the service.
type ServiceRequirement struct {
	Name         string `json:"name"`
	MajorVersion int    `json:"major_version"`
	Required     bool   `json:"required,omitempty"`
}

// ServiceCall is one validated call the host routes to a service provider.
// Caller carries the consumer plugin ID authenticated by the host.
type ServiceCall struct {
	Service     string          `json:"service"`
	Method      string          `json:"method"`
	Caller      string          `json:"caller"`
	ExecutionID string          `json:"execution_id,omitempty"`
	Params      json.RawMessage `json:"params,omitempty"`
}

// ServiceChangedNotice tells a consumer that a service resolution changed.
type ServiceChangedNotice struct {
	Service string `json:"service"`
	Reason  string `json:"reason,omitempty"`
}

type Tool struct {
	ID              string         `json:"id"`
	Description     string         `json:"description"`
	InputSchema     map[string]any `json:"input_schema"`
	ExecutionScopes []string       `json:"execution_scopes,omitempty"`
	Activity        *ToolActivity  `json:"activity,omitempty"`
	Display         *ToolDisplay   `json:"display,omitempty"`
}

type ToolDisplay struct {
	Kind       string `json:"kind,omitempty"`
	Text       string `json:"text,omitempty"`
	Capability string `json:"capability,omitempty"`
}

type ToolActivity struct {
	// Orchestrator delegates effects through execution.invoke-tool. It does not
	// occupy a leaf scheduling slot while its child calls execute.
	Orchestrator    bool   `json:"orchestrator,omitempty"`
	ReadOnly        bool   `json:"read_only,omitempty"`
	ConcurrencySafe bool   `json:"concurrency_safe,omitempty"`
	Destructive     bool   `json:"destructive,omitempty"`
	Risk            string `json:"risk,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

type AuthorizationRequest struct {
	SessionID      string            `json:"session_id,omitempty"`
	ActorID        string            `json:"actor_id,omitempty"`
	CWD            string            `json:"cwd"`
	PermissionMode string            `json:"permission_mode"`
	Tool           AuthorizationTool `json:"tool"`
}

type AuthorizationTool struct {
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Arguments       string `json:"arguments,omitempty"`
	ReadOnly        bool   `json:"read_only"`
	ConcurrencySafe bool   `json:"concurrency_safe"`
	Destructive     bool   `json:"destructive"`
	Risk            string `json:"risk,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

type AuthorizationDecision struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

type ProcessSandboxRequest struct {
	Argv   []string             `json:"argv"`
	Policy ProcessSandboxPolicy `json:"policy"`
}

type ProcessSandboxPolicy struct {
	Mode          string   `json:"mode"`
	WritableRoots []string `json:"writable_roots,omitempty"`
}

type ProcessSandboxResult struct {
	Argv                    []string `json:"argv"`
	Enforcement             string   `json:"enforcement"`
	DenialSignatures        []string `json:"denial_signatures,omitempty"`
	RunnerFailureSignatures []string `json:"runner_failure_signatures,omitempty"`
}

type Definition struct {
	Tools                []Tool               `json:"tools,omitempty"`
	Capabilities         []Capability         `json:"capabilities,omitempty"`
	RequiredHostServices []HostService        `json:"required_host_services,omitempty"`
	ProvidedServices     []Service            `json:"provided_services,omitempty"`
	RequiredServices     []ServiceRequirement `json:"required_services,omitempty"`
}

type ToolCall struct {
	ToolID    string `json:"tool_id"`
	SessionID string `json:"session_id,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
	// ExecutionID names this exact dispatch in the execution scope plane.
	// The runtime translates execution.cancel for it into ctx cancellation
	// and ReportExecutionUpdate references it.
	ExecutionID string          `json:"execution_id,omitempty"`
	ActorID     string          `json:"actor_id,omitempty"`
	CWD         string          `json:"cwd"`
	StepIndex   int             `json:"step_index,omitempty"`
	CallID      string          `json:"call_id"`
	Tool        string          `json:"tool"`
	Arguments   json.RawMessage `json:"arguments"`
}

type ContentPart struct {
	Type     string                `json:"type"`
	Text     string                `json:"text,omitempty"`
	Data     string                `json:"data,omitempty"`
	MIMEType string                `json:"mime_type,omitempty"`
	URI      string                `json:"uri,omitempty"`
	Name     string                `json:"name,omitempty"`
	Resource json.RawMessage       `json:"resource,omitempty"`
	Artifact *ArtifactPresentation `json:"artifact,omitempty"`
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

type ToolResult struct {
	Content           []ContentPart   `json:"content,omitempty"`
	StructuredContent json.RawMessage `json:"structured_content,omitempty"`
	Meta              json.RawMessage `json:"meta,omitempty"`
	IsError           bool            `json:"is_error,omitempty"`
	Activity          *ActivityRef    `json:"activity,omitempty"`
}

type UserQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type UserQuestion struct {
	ID          string               `json:"id"`
	Question    string               `json:"question"`
	Header      string               `json:"header,omitempty"`
	Detail      string               `json:"detail,omitempty"`
	Options     []UserQuestionOption `json:"options,omitempty"`
	MultiSelect bool                 `json:"multi_select,omitempty"`
	AllowCustom bool                 `json:"allow_custom,omitempty"`
}

type UserQuestionAnswerItem struct {
	ID       string   `json:"id"`
	Selected []string `json:"selected"`
	Custom   string   `json:"custom,omitempty"`
}

type UserQuestionAnswer struct {
	Answers []UserQuestionAnswerItem `json:"answers"`
}

type UserQuestionOfferResult struct {
	RequestID string `json:"request_id"`
	ExpiresAt string `json:"expires_at"`
}

func TextResult(text string) ToolResult {
	if text == "" {
		return ToolResult{}
	}
	return ToolResult{Content: []ContentPart{{Type: "text", Text: text}}}
}

type CapabilityCall struct {
	Capability string `json:"capability"`
	// ExecutionID names this exact dispatch in the execution scope plane,
	// identical in role to ToolCall.ExecutionID.
	ExecutionID string          `json:"execution_id,omitempty"`
	Input       json.RawMessage `json:"input"`
	Output      json.RawMessage `json:"output"`
}

type TurnContextBlock struct {
	Kind    string `json:"kind"`
	Title   string `json:"title,omitempty"`
	Source  string `json:"source,omitempty"`
	Content string `json:"content"`
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

type AgentPreStepInput struct {
	SessionID string               `json:"session_id,omitempty"`
	ThreadID  string               `json:"thread_id,omitempty"`
	CWD       string               `json:"cwd,omitempty"`
	Provider  string               `json:"provider,omitempty"`
	Model     string               `json:"model,omitempty"`
	StepIndex int                  `json:"step_index"`
	Messages  []ModelMessageViewV1 `json:"messages"`
}

type AgentPreStepMessage struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

type AgentPreStepOutput struct {
	AppendMessages []AgentPreStepMessage `json:"append_messages,omitempty"`
}

type StorageGetParams struct {
	Scope string `json:"scope"`
	Key   string `json:"key"`
}

type StorageGetResult struct {
	Value *string `json:"value"`
}

type StorageSetParams struct {
	Scope string `json:"scope"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

type StorageDeleteParams struct {
	Scope string `json:"scope"`
	Key   string `json:"key"`
}

type StorageKeysParams struct {
	Scope string `json:"scope"`
}

type StorageKeysResult struct {
	Keys []string `json:"keys"`
}

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

type SettingsGetParams struct {
	Key string `json:"key"`
}

type SettingsGetResult struct {
	Value json.RawMessage `json:"value"`
}

type SettingsListResult struct {
	Entries map[string]json.RawMessage `json:"entries"`
}

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
}

type SessionCreateResult struct {
	SessionID string `json:"session_id"`
	Created   bool   `json:"created"`
	// WorkspaceRoot is the effective directory the session runs in. It
	// differs from the project root when the session owns a worktree.
	WorkspaceRoot string `json:"workspace_root,omitempty"`
}

type SessionInput struct {
	Prompt        string             `json:"prompt"`
	ContextBlocks []TurnContextBlock `json:"context_blocks,omitempty"`
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
	// Scope defaults to owned. shared lists active user-visible sessions while
	// keeping plugin-private sessions hidden from other plugins.
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
	TurnID    string `json:"turn_id,omitempty"`
	QueueID   string `json:"queue_id,omitempty"`
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

type SessionTurnInspection struct {
	RequestID    string `json:"request_id,omitempty"`
	State        string `json:"state"`
	TurnID       string `json:"turn_id,omitempty"`
	QueueID      string `json:"queue_id,omitempty"`
	Error        string `json:"error,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	FinalOutput  string `json:"final_output,omitempty"`
}

type SessionWorkspaceSummary struct {
	Kind         string   `json:"kind"`
	Dirty        bool     `json:"dirty,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
}

type SessionInspectResult struct {
	Session   SessionSummary           `json:"session"`
	Turn      *SessionTurnInspection   `json:"turn,omitempty"`
	Workspace *SessionWorkspaceSummary `json:"workspace,omitempty"`
	TimedOut  bool                     `json:"timed_out,omitempty"`
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

type AgentTurnInterruptedInput struct {
	ThreadID string `json:"thread_id"`
	TurnID   string `json:"turn_id"`
	Cause    string `json:"cause,omitempty"`
}

type TurnLifecycleInput struct {
	RequestID    string `json:"request_id"`
	State        string `json:"state"`
	ThreadID     string `json:"thread_id"`
	TurnID       string `json:"turn_id,omitempty"`
	QueueID      string `json:"queue_id,omitempty"`
	Error        string `json:"error,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	FinalOutput  string `json:"final_output,omitempty"`
}

type Host interface {
	InitializeParams() InitializeParams
	CallHost(context.Context, string, any, any) error
}

// CallService invokes one method of a registered service through the host's
// service gateway. The plugin must declare the service in its Definition's
// RequiredServices; the host authorizes and routes the call.
func CallService(ctx context.Context, host Host, service, method string, params, result any) error {
	return host.CallHost(ctx, HostServiceCallMethod, struct {
		Service     string `json:"service"`
		Method      string `json:"method"`
		ExecutionID string `json:"execution_id,omitempty"`
		Params      any    `json:"params,omitempty"`
	}{Service: service, Method: method, ExecutionID: executionIDFromContext(ctx), Params: params}, result)
}

// CallHostService preserves the former host-call signature while routing the
// operation through its kernel Service Registry entry.
func CallHostService(ctx context.Context, host Host, service string, params, result any) error {
	if service == HostServiceStorageCompareExchange {
		service = KernelStorageCompareExchangeService
	}
	return CallService(ctx, host, service, KernelServiceMethod, params, result)
}

// ExecutionUpdate is one progress report for an execution the calling plugin
// owns. Detail carries arbitrary plugin-owned progress payload.
type ExecutionUpdate struct {
	ExecutionID string          `json:"execution_id"`
	Message     string          `json:"message,omitempty"`
	Detail      json.RawMessage `json:"detail,omitempty"`
}

type ArtifactImportParams struct {
	Path     string `json:"path,omitempty"`
	Data     string `json:"data,omitempty"`
	Name     string `json:"name,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
}

type ImportedArtifact struct {
	ID       string `json:"id"`
	URI      string `json:"uri"`
	Name     string `json:"name"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

// ReportExecutionUpdate reports progress for one live execution through the
// kernel's execution.update service. The plugin must declare the service in
// its Definition's RequiredServices; updates for executions that are not live
// or not owned by the caller fail with typed errors.
func ReportExecutionUpdate(ctx context.Context, host Host, update ExecutionUpdate) error {
	return CallService(ctx, host, ExecutionUpdateService, KernelServiceMethod, update, nil)
}

// ImportArtifact copies a generated file into storage owned by the current
// tool execution's thread and returns a durable URI for a ToolResult part.
func ImportArtifact(ctx context.Context, host Host, input ArtifactImportParams) (ImportedArtifact, error) {
	var artifact ImportedArtifact
	err := CallService(ctx, host, KernelArtifactImportService, KernelServiceMethod, input, &artifact)
	return artifact, err
}

// AskUserQuestions pauses the current Tool execution until a shell answers or
// the execution context is cancelled. The caller must declare
// KernelUserQuestionAskService in RequiredServices.
func AskUserQuestions(ctx context.Context, host Host, questions []UserQuestion) (UserQuestionAnswer, error) {
	var answer UserQuestionAnswer
	err := CallService(ctx, host, KernelUserQuestionAskService, KernelServiceMethod, struct {
		Questions []UserQuestion `json:"questions"`
	}{Questions: questions}, &answer)
	return answer, err
}

// OfferUserQuestions shows a non-blocking question to the user and returns
// immediately. An answer, if any, arrives later through the conversation,
// not as this Tool result. The caller must declare
// KernelUserQuestionOfferService in RequiredServices.
func OfferUserQuestions(ctx context.Context, host Host, questions []UserQuestion) (UserQuestionOfferResult, error) {
	var offer UserQuestionOfferResult
	err := CallService(ctx, host, KernelUserQuestionOfferService, KernelServiceMethod, struct {
		Questions []UserQuestion `json:"questions"`
	}{Questions: questions}, &offer)
	return offer, err
}

func RequireHostServices(services ...string) []ServiceRequirement {
	requirements := make([]ServiceRequirement, 0, len(services))
	for _, service := range services {
		if service == HostServiceStorageCompareExchange {
			service = KernelStorageCompareExchangeService
		}
		requirements = append(requirements, ServiceRequirement{Name: service, MajorVersion: 1, Required: true})
	}
	return requirements
}

func kernelServiceForLegacyMethod(method string) (string, bool) {
	services := map[string]string{
		HostServiceStorageGet: KernelStorageGetService, HostServiceStorageSet: KernelStorageSetService,
		HostServiceStorageDelete: KernelStorageDeleteService, HostServiceStorageKeys: KernelStorageKeysService,
		HostServiceStorageCompareExchange: KernelStorageCompareExchangeService,
		HostServiceSettingsGet:            KernelSettingsGetService, HostServiceSettingsList: KernelSettingsListService,
		HostServiceSessionCreate: KernelSessionCreateService, HostServiceSessionSend: KernelSessionSendService,
		HostServiceSessionList: KernelSessionListService, HostServiceSessionCancel: KernelSessionCancelService,
		HostServiceSessionInspect:     KernelSessionInspectService,
		HostServiceSessionHistoryRead: KernelSessionHistoryReadService, HostServiceSessionHistorySearch: KernelSessionHistorySearchService,
		HostServiceWorkspaceStatus: KernelWorkspaceStatusService, HostServiceWorkspaceApply: KernelWorkspaceApplyService,
		HostServiceWorkspaceDiscard: KernelWorkspaceDiscardService,
	}
	service, ok := services[method]
	return service, ok
}

type Handler struct {
	// ConcurrentCapabilities names capability handlers that may overlap tool
	// execution and each other. Opt in only for handlers safe under concurrent
	// access; all other requests retain ordered dispatch. Initialize completes
	// before these handlers run, and shutdown waits for them to finish.
	ConcurrentCapabilities []string

	Definition       Definition
	Initialize       func(context.Context, Host, InitializeParams) error
	Activate         func(context.Context) error
	Shutdown         func(context.Context) error
	ExecuteTool      func(context.Context, Host, ToolCall) (ToolResult, error)
	InvokeCapability func(context.Context, Host, CapabilityCall) (json.RawMessage, error)
	InvokeService    func(context.Context, Host, ServiceCall) (json.RawMessage, error)
	ServiceChanged   func(context.Context, ServiceChangedNotice) error
}

// HostCallError is the typed failure returned by a host service call. Code
// carries the registry or provider error code unchanged so consumers can
// branch on service_unavailable and other typed failures instead of parsing
// message text.
type HostCallError struct {
	Code    string
	Message string
}

func (e *HostCallError) Error() string { return e.Message }

type Client struct {
	output     io.Writer
	seq        atomic.Uint64
	initMu     sync.RWMutex
	init       InitializeParams
	writeMu    sync.Mutex
	pendingMu  sync.Mutex
	pending    map[string]chan rpcResponse
	done       chan struct{}
	doneOnce   sync.Once
	errMu      sync.Mutex
	readErr    error
	executions *executionTable
}

// executionTable maps live execution IDs to their cancellation functions so
// an out-of-band execution.cancel frame can preempt the exact running
// execution. Entries are released when the dispatch returns; a cancel for an
// unknown or already-closed ID is a no-op and can never hit a later
// execution.
type executionTable struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func newExecutionTable() *executionTable {
	return &executionTable{cancels: make(map[string]context.CancelFunc)}
}

func (t *executionTable) track(id string, cancel context.CancelFunc) {
	t.mu.Lock()
	t.cancels[id] = cancel
	t.mu.Unlock()
}

func (t *executionTable) release(id string) {
	t.mu.Lock()
	delete(t.cancels, id)
	t.mu.Unlock()
}

func (t *executionTable) cancelExecution(id string) {
	t.mu.Lock()
	cancel := t.cancels[id]
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

type executionContextKey struct{}

func executionIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(executionContextKey{}).(string)
	return strings.TrimSpace(id)
}

// trackExecution derives a cancellable context for one execution dispatch and
// registers it so a later execution.cancel frame can preempt it. The returned
// release function unregisters the execution and frees the context; it must
// run when the dispatch returns.
func (c *Client) trackExecution(ctx context.Context, executionID string) (context.Context, func()) {
	if strings.TrimSpace(executionID) == "" {
		return ctx, nil
	}
	execCtx, cancel := context.WithCancel(ctx)
	execCtx = context.WithValue(execCtx, executionContextKey{}, executionID)
	c.executions.track(executionID, cancel)
	return execCtx, func() {
		c.executions.release(executionID)
		cancel()
	}
}

// cancelExecution preempts one live execution. It answers the host's
// fire-and-forget cancel frame, so it never blocks on the execution itself.
func (c *Client) cancelExecution(executionID string) {
	c.executions.cancelExecution(executionID)
}

func (c *Client) InitializeParams() InitializeParams {
	c.initMu.RLock()
	defer c.initMu.RUnlock()
	return c.init
}

func (c *Client) CallHost(ctx context.Context, method string, params, result any) error {
	if service, ok := kernelServiceForLegacyMethod(method); ok {
		return CallService(ctx, c, service, KernelServiceMethod, params, result)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	id := fmt.Sprintf("plugin-%d", c.seq.Add(1))
	rawParams, err := json.Marshal(params)
	if err != nil {
		return err
	}
	responseCh := make(chan rpcResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = responseCh
	c.pendingMu.Unlock()
	defer c.removePending(id)
	if err := c.write(rpcRequest{ID: id, Method: strings.TrimSpace(method), Params: rawParams}); err != nil {
		return err
	}
	select {
	case response := <-responseCh:
		if response.Error != nil {
			return &HostCallError{Code: strings.TrimSpace(response.Error.Code), Message: strings.TrimSpace(response.Error.Message)}
		}
		if result == nil {
			return nil
		}
		if len(response.Result) == 0 {
			return errors.New("host service response is missing result")
		}
		return json.Unmarshal(response.Result, result)
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.transportError()
	}
}

func newClient(output io.Writer) *Client {
	return &Client{output: output, pending: make(map[string]chan rpcResponse), done: make(chan struct{}), executions: newExecutionTable()}
}

func (c *Client) write(value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeJSONLine(c.output, value)
}

func (c *Client) removePending(id string) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *Client) routeResponse(response rpcResponse) {
	c.pendingMu.Lock()
	responseCh := c.pending[response.ID]
	c.pendingMu.Unlock()
	if responseCh != nil {
		responseCh <- response
	}
}

func (c *Client) closeTransport(err error) {
	c.errMu.Lock()
	if c.readErr == nil {
		if err == nil {
			err = io.EOF
		}
		c.readErr = err
	}
	c.errMu.Unlock()
	c.doneOnce.Do(func() { close(c.done) })
}

func (c *Client) transportError() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.readErr == nil {
		return io.EOF
	}
	return c.readErr
}

type rpcRequest struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// queuedDispatch carries one host request together with its execution-scoped
// context. The execution is registered when its frame is read — in the same
// goroutine that later reads any execution.cancel frame — so a cancel can
// never arrive before the registration it targets.
type queuedDispatch struct {
	request rpcRequest
	ctx     context.Context
	release func()
}

func Serve(ctx context.Context, handler Handler) error {
	return ServeIO(ctx, os.Stdin, os.Stdout, handler)
}

func ServeIO(ctx context.Context, input io.Reader, output io.Writer, handler Handler) error {
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	scanner := bufio.NewScanner(input)
	// Capability requests may carry a complete long-session transcript. Keep a
	// finite bound, but size it for the context windows Wuu supports rather than
	// the much smaller control-plane messages used by most plugins.
	scanner.Buffer(make([]byte, 64*1024), 64<<20)
	client := newClient(output)
	incomingRequests := make(chan queuedDispatch)
	requests := make(chan queuedDispatch)
	workerDone := make(chan error, 1)
	var initialized atomic.Bool
	var concurrent sync.WaitGroup
	defer func() {
		cancel()
		client.closeTransport(context.Canceled)
		concurrent.Wait()
	}()
	go queueRequests(serveCtx, incomingRequests, requests)
	go func() {
		for envelope := range requests {
			if envelope.request.Method == "shutdown" {
				concurrent.Wait()
			}
			result, stop, err := dispatch(envelope.ctx, client, handler, envelope.request)
			if envelope.request.Method == "initialize" && err == nil {
				initialized.Store(true)
			}
			if envelope.release != nil {
				envelope.release()
			}
			response := rpcResponse{ID: envelope.request.ID, Result: result}
			if err != nil {
				response = rpcResponse{ID: envelope.request.ID, Error: &rpcError{Message: err.Error()}}
			}
			if writeErr := client.write(response); writeErr != nil {
				workerDone <- writeErr
				return
			}
			if stop {
				workerDone <- nil
				return
			}
		}
		workerDone <- nil
	}()

	acceptConcurrent := true
	for scanner.Scan() {
		if err := serveCtx.Err(); err != nil {
			client.closeTransport(err)
			close(incomingRequests)
			return err
		}
		kind, request, response, err := decodeMessage(scanner.Bytes())
		if err != nil {
			client.closeTransport(err)
			close(incomingRequests)
			return err
		}
		if kind == messageResponse {
			client.routeResponse(response)
			continue
		}
		if request.Method == ExecutionCancelMethod {
			// Cancel bypasses the serial dispatch queue so it can preempt the
			// exact execution currently running inside a handler.
			var params struct {
				ExecutionID string `json:"execution_id"`
			}
			_ = json.Unmarshal(request.Params, &params)
			client.cancelExecution(params.ExecutionID)
			if writeErr := client.write(rpcResponse{ID: request.ID, Result: json.RawMessage(`{}`)}); writeErr != nil {
				client.closeTransport(writeErr)
				close(incomingRequests)
				return writeErr
			}
			continue
		}
		envelope := queuedDispatch{request: request, ctx: serveCtx}
		if request.Method == "tool.execute" || request.Method == "capability.invoke" || request.Method == ServiceInvokeMethod {
			var probe struct {
				ExecutionID string `json:"execution_id"`
			}
			_ = json.Unmarshal(request.Params, &probe)
			envelope.ctx, envelope.release = client.trackExecution(serveCtx, probe.ExecutionID)
		}
		if request.Method == "shutdown" {
			acceptConcurrent = false
		}
		if acceptConcurrent && initialized.Load() && request.Method == "capability.invoke" {
			var call CapabilityCall
			_ = json.Unmarshal(request.Params, &call)
			if slices.Contains(handler.ConcurrentCapabilities, call.Capability) {
				concurrent.Add(1)
				go func() {
					defer concurrent.Done()
					if envelope.release != nil {
						defer envelope.release()
					}
					result, _, err := dispatch(envelope.ctx, client, handler, envelope.request)
					response := rpcResponse{ID: envelope.request.ID, Result: result}
					if err != nil {
						response = rpcResponse{ID: envelope.request.ID, Error: &rpcError{Message: err.Error()}}
					}
					if err := client.write(response); err != nil {
						client.closeTransport(err)
						cancel()
					}
				}()
				continue
			}
		}
		select {
		case incomingRequests <- envelope:
		case err := <-workerDone:
			client.closeTransport(err)
			cancel()
			close(incomingRequests)
			return err
		case <-serveCtx.Done():
			client.closeTransport(serveCtx.Err())
			close(incomingRequests)
			return serveCtx.Err()
		}
	}
	readErr := scanner.Err()
	client.closeTransport(readErr)
	close(incomingRequests)
	workerErr := <-workerDone
	if readErr != nil {
		return readErr
	}
	return workerErr
}

// queueRequests keeps host requests ordered without allowing handler backpressure
// to block the transport reader. The reader must remain available to route host
// service responses while the current handler is waiting in CallHost.
func queueRequests(ctx context.Context, input <-chan queuedDispatch, output chan<- queuedDispatch) {
	defer close(output)
	var queued []queuedDispatch
	for input != nil || len(queued) != 0 {
		var next queuedDispatch
		var ready chan<- queuedDispatch
		if len(queued) != 0 {
			next = queued[0]
			ready = output
		}
		select {
		case request, ok := <-input:
			if !ok {
				input = nil
				continue
			}
			queued = append(queued, request)
		case ready <- next:
			queued[0] = queuedDispatch{}
			queued = queued[1:]
			if len(queued) == 0 {
				queued = nil
			}
		case <-ctx.Done():
			return
		}
	}
}

type messageKind int

const (
	messageRequest messageKind = iota
	messageResponse
)

func decodeMessage(line []byte) (messageKind, rpcRequest, rpcResponse, error) {
	var envelope struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return 0, rpcRequest{}, rpcResponse{}, fmt.Errorf("decode runtime message: %w", err)
	}
	if strings.TrimSpace(envelope.ID) == "" {
		return 0, rpcRequest{}, rpcResponse{}, errors.New("runtime message id is required")
	}
	if strings.TrimSpace(envelope.Method) != "" {
		if len(envelope.Result) != 0 || envelope.Error != nil {
			return 0, rpcRequest{}, rpcResponse{}, errors.New("host request cannot contain result or error")
		}
		return messageRequest, rpcRequest{ID: envelope.ID, Method: envelope.Method, Params: envelope.Params}, rpcResponse{}, nil
	}
	if len(envelope.Params) != 0 {
		return 0, rpcRequest{}, rpcResponse{}, errors.New("host response cannot contain params")
	}
	if (len(envelope.Result) == 0) == (envelope.Error == nil) {
		return 0, rpcRequest{}, rpcResponse{}, errors.New("host response must contain exactly one of result or error")
	}
	return messageResponse, rpcRequest{}, rpcResponse{ID: envelope.ID, Result: envelope.Result, Error: envelope.Error}, nil
}

func dispatch(ctx context.Context, client *Client, handler Handler, request rpcRequest) (json.RawMessage, bool, error) {
	switch request.Method {
	case "initialize":
		var params InitializeParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, false, err
		}
		client.initMu.Lock()
		client.init = params
		client.initMu.Unlock()
		if handler.Initialize != nil {
			if err := handler.Initialize(ctx, client, params); err != nil {
				return nil, false, err
			}
		}
		definition := handler.Definition
		for _, service := range definition.RequiredHostServices {
			if name, ok := kernelServiceForLegacyMethod(service.ID); ok {
				definition.RequiredServices = append(definition.RequiredServices, ServiceRequirement{Name: name, MajorVersion: 1, Required: service.Required})
			}
		}
		definition.RequiredHostServices = nil
		return marshal(struct {
			Definition
			ProtocolVersion  int `json:"protocol_version"`
			LifecycleVersion int `json:"lifecycle_version"`
		}{Definition: definition, ProtocolVersion: CapabilityProtocolVersion, LifecycleVersion: RuntimeLifecycleVersion})
	case "activate":
		if handler.Activate != nil {
			if err := handler.Activate(ctx); err != nil {
				return nil, false, err
			}
		}
		return json.RawMessage(`{}`), false, nil
	case "tool.execute":
		if handler.ExecuteTool == nil {
			return nil, false, errors.New("tool execution is unavailable")
		}
		var call ToolCall
		if err := json.Unmarshal(request.Params, &call); err != nil {
			return nil, false, err
		}
		result, err := handler.ExecuteTool(ctx, client, call)
		if err != nil {
			return nil, false, err
		}
		return marshal(struct {
			Result ToolResult `json:"result"`
		}{Result: result})
	case "capability.invoke":
		if handler.InvokeCapability == nil {
			return nil, false, errors.New("capability invocation is unavailable")
		}
		var call CapabilityCall
		if err := json.Unmarshal(request.Params, &call); err != nil {
			return nil, false, err
		}
		value, err := handler.InvokeCapability(ctx, client, call)
		if err != nil {
			return nil, false, err
		}
		if len(value) == 0 || !json.Valid(value) {
			return nil, false, errors.New("capability returned invalid JSON")
		}
		return marshal(struct {
			Output json.RawMessage `json:"output"`
		}{Output: value})
	case "service.invoke":
		if handler.InvokeService == nil {
			return nil, false, errors.New("service invocation is unavailable")
		}
		var call ServiceCall
		if err := json.Unmarshal(request.Params, &call); err != nil {
			return nil, false, err
		}
		value, err := handler.InvokeService(ctx, client, call)
		if err != nil {
			return nil, false, err
		}
		if len(value) == 0 || !json.Valid(value) {
			return nil, false, errors.New("service returned invalid JSON")
		}
		return value, false, nil
	case "service.changed":
		if handler.ServiceChanged != nil {
			var notice ServiceChangedNotice
			if err := json.Unmarshal(request.Params, &notice); err != nil {
				return nil, false, err
			}
			if err := handler.ServiceChanged(ctx, notice); err != nil {
				return nil, false, err
			}
		}
		return json.RawMessage(`{}`), false, nil
	case "shutdown":
		if handler.Shutdown != nil {
			if err := handler.Shutdown(ctx); err != nil {
				return nil, false, err
			}
		}
		return json.RawMessage(`{}`), true, nil
	default:
		return nil, false, fmt.Errorf("method %q is not supported", request.Method)
	}
}

func marshal(value any) (json.RawMessage, bool, error) {
	raw, err := json.Marshal(value)
	return raw, false, err
}

func writeJSONLine(output io.Writer, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = output.Write(append(raw, '\n'))
	return err
}
