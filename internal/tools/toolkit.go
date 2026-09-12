package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/modelprofile"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/stringutil"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

const (
	browserToolName            = "wuu_browser"
	defaultShellTimeoutSeconds = 300
	maxShellTimeoutSeconds     = 3600
	defaultMaxFileBytes        = 256 * 1024
	defaultMaxEntries          = 1000
	// Per-tool output size limits (in bytes). Shell/grep produce verbose,
	// low-density output and get a tighter cap; other tools use a generous
	// default.
	maxShellOutputBytes = 30 * 1024  // 30 KB
	maxGrepOutputBytes  = 20 * 1024  // 20 KB
	maxToolOutputBytes  = 100 * 1024 // 100 KB — general cap for other tools
)

var contextWindowToolNames = []string{newContextToolName}
var historyRecoveryToolNames = []string{historyReadToolName, historySearchToolName}

// Toolkit executes local coding tools for the agent. It satisfies
// agent.ToolExecutor via Definitions() + Execute().
//
// Internally it holds an Env (shared runtime state) and a Registry
// (all registered tools). The old switch-case dispatch is replaced by
// registry lookup.
type Toolkit struct {
	env                     *Env
	registry                *Registry
	disabledTools           map[string]struct{}
	exposureMu              sync.RWMutex
	loadedDeferredTools     map[string]struct{}
	toolSearchEnabled       bool
	nativeDeferredDiscovery bool
	boundary                WorkspaceBoundary
	permissionRequestHook   func(context.Context, *Toolkit, ToolInfo, providers.ToolCall) error
	authorizer              Authorizer
	codeModeMu              sync.RWMutex
	codeMode                *codemode.Service
	codeModeOnly            bool
	codeModeAdditionalTools func() []providers.ToolDefinition
	// mcpManager, when set, exposes MCP server tools alongside built-in
	// tools. MCP tools are appended after built-ins to preserve prompt
	// cache stability (the built-in prefix stays constant).
	mcpManager           *mcp.Manager
	mcpCatalogMu         sync.RWMutex
	mcpCatalogGeneration uint64
	mcpTools             []*mcp.MCPTool
	// surfaceFreezeDepth pins the MCP catalog snapshot while model runs are
	// in flight (see FreezeToolSurface) so asynchronous server events cannot
	// reshape the provider tool list between rounds of one run. Guarded by
	// mcpCatalogMu.
	surfaceFreezeDepth  int
	activityRegistry    *activity.Registry
	mcpActivityBindings map[string]MCPActivityBinding

	// activeProfileMu guards activeProfile and activeSurface. Reads
	// from Definitions() and Execute() take the RLock; SetActiveProfile
	// takes the Lock.
	activeProfileMu sync.RWMutex
	// activeProfile is the most recently installed ModelProfile. The
	// zero value means "no profile compiled yet" — Definitions() falls
	// back to the legacy direct-tool surface in that case.
	activeProfile modelprofile.Profile
	// activeSurface is the compiled surface for activeProfile. It is
	// the authoritative source for which tool names are direct,
	// deferred, or hidden under the current profile.
	activeSurface capability.Surface
}

// SetPermissionRequestHook installs the hook invoked immediately before the
// workspace boundary makes its allow/deny decision.
func (t *Toolkit) SetPermissionRequestHook(hook func(context.Context, *Toolkit, ToolInfo, providers.ToolCall) error) {
	t.permissionRequestHook = hook
}

type permissionCheckedContextKey struct{}

func (t *Toolkit) checkPermission(ctx context.Context, info ToolInfo, call providers.ToolCall) error {
	if checked, _ := ctx.Value(permissionCheckedContextKey{}).(bool); checked {
		return nil
	}
	if t.permissionRequestHook != nil {
		if err := t.permissionRequestHook(ctx, t, info, call); err != nil {
			return err
		}
	}
	if err := t.boundary.Check(info, call); err != nil {
		return err
	}
	if t.authorizer == nil {
		return nil
	}
	decision, err := t.authorizer.Authorize(ctx, AuthorizationRequest{
		SessionID: t.env.SessionID, ActorID: t.env.AgentID, CWD: t.env.RootDir,
		PermissionMode: t.env.PermissionMode, Tool: info, Arguments: call.Arguments,
	})
	if err != nil {
		return authorizationDenied(info.Name, "authorization provider unavailable")
	}
	if strings.TrimSpace(decision.Outcome) != "allow" {
		return authorizationDenied(info.Name, decision.Reason)
	}
	return nil
}

func (t *Toolkit) SetAuthorizer(authorizer Authorizer) { t.authorizer = authorizer }

func (t *Toolkit) SetProcessSandboxProvider(provider processsandbox.Provider) {
	t.env.ProcessSandboxProvider = provider
}

func (t *Toolkit) SetPermissionMode(mode string) { t.env.PermissionMode = strings.TrimSpace(mode) }

func (t *Toolkit) AuthorizeTool(ctx context.Context, call providers.ToolCall, metadata agent.ToolMetadata) error {
	return t.checkPermission(ctx, ToolInfo{
		Name: call.Name, Kind: ToolKindPlugin, Exposure: ToolExposureDirect,
		Risk: ToolRisk(metadata.Risk), ReadOnly: metadata.ReadOnly,
		ConcurrencySafe: metadata.ConcurrencySafe, Destructive: metadata.Destructive, Reason: metadata.Reason,
	}, call)
}

func markPermissionChecked(ctx context.Context) context.Context {
	return context.WithValue(ctx, permissionCheckedContextKey{}, true)
}

// ExecutionActor exposes the generic identity bound to this tool surface so
// external plugin tools can preserve parent/child execution relationships.
func (t *Toolkit) ExecutionActor() (string, string) {
	if t == nil || t.env == nil {
		return "", ""
	}
	return strings.TrimSpace(t.env.AgentID), currentExecutionPath(t.env)
}

// New creates a tool executor rooted in a workspace.
func New(rootDir string) (*Toolkit, error) {
	if strings.TrimSpace(rootDir) == "" {
		return nil, errors.New("root directory is required")
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve root directory: %w", err)
	}
	if ev, err := filepath.EvalSymlinks(abs); err == nil {
		abs = ev
	}
	wuuHome, err := statepath.Home("")
	if err != nil {
		return nil, fmt.Errorf("resolve wuu home: %w", err)
	}
	stateDir, err := statepath.WorkspaceDir(wuuHome, abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace state directory: %w", err)
	}

	env := &Env{
		RootDir:            abs,
		StateDir:           stateDir,
		AllowMutations:     true,
		boundaryConfigured: true,
		ToolSearchEnabled:  true,
	}
	t := &Toolkit{
		env: env,
		disabledTools: map[string]struct{}{
			browserToolName:    {},
			newContextToolName: {},
		},
		toolSearchEnabled: true,
		boundary:          StandardBoundary(),
	}
	t.rebuildRegistry()
	t.SetEditToolMode(EditToolModeText)
	return t, nil
}

// CloneForRoot returns a toolkit with the same configured dependencies but a
// fresh per-session Env state. It is used by desktop app-server threads so
// concurrent conversations do not share mutable tool state such as SessionID,
// read tracking, plans, or telemetry.
func (t *Toolkit) CloneForRoot(rootDir string) (*Toolkit, error) {
	if t == nil || t.env == nil {
		return nil, errors.New("toolkit is not initialized")
	}
	cloneRoot := strings.TrimSpace(rootDir)
	if cloneRoot == "" {
		cloneRoot = t.env.RootDir
	}
	abs, err := filepath.Abs(cloneRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve root directory: %w", err)
	}
	if ev, err := filepath.EvalSymlinks(abs); err == nil {
		abs = ev
	}
	// Build a fresh Env from the source toolkit's configured dependencies.
	// We must NOT do `env := *t.env`: Env embeds testRunState, which holds a
	// sync.RWMutex, and the sync package contract forbids copying a Mutex or
	// RWMutex after first use (go vet's copylocks analyzer enforces this).
	// The lock-bearing per-session state fields (readState, testState,
	// webState, toolTelemetry, gitAttributionShell) stay zero so each cloned session
	// owns independent mutable state, matching the original intent.
	env := Env{
		RootDir:                     abs,
		WorkspaceID:                 t.env.WorkspaceID,
		StateDir:                    t.env.StateDir,
		Unconfined:                  t.env.Unconfined,
		PermissionMode:              t.env.PermissionMode,
		AllowMutations:              t.env.AllowMutations,
		boundaryConfigured:          t.env.boundaryConfigured,
		SessionID:                   t.env.SessionID,
		SessionDir:                  t.env.SessionDir,
		SessionsDir:                 t.env.SessionsDir,
		AgentID:                     t.env.AgentID,
		AgentPath:                   t.env.AgentPath,
		ToolSearchEnabled:           t.env.ToolSearchEnabled,
		NativeDeferredToolDiscovery: t.env.NativeDeferredToolDiscovery,
		GitAttributionDisabled:      t.env.GitAttributionDisabled,
		GitWrapperExecutable:        t.env.GitWrapperExecutable,
		ProcessMgr:                  t.env.ProcessMgr,
		ProcessSandboxProvider:      t.env.ProcessSandboxProvider,
		AgentControl:                t.env.AgentControl,
		// Browser dependencies must be copied explicitly: every desktop thread
		// runs through CloneForRoot, so omitting these here silently strips the
		// bridge/tab store from every cloned session (the mcpActivityBindings
		// precedent below is the same hazard).
		BrowserBridge:             t.env.BrowserBridge,
		BrowserTabs:               t.env.BrowserTabs,
		FileScopeRoots:            append([]string(nil), t.env.FileScopeRoots...),
		Skills:                    t.env.Skills,
		OnFileChanged:             t.env.OnFileChanged,
		OnSessionWorkspaceChanged: t.env.OnSessionWorkspaceChanged,
	}

	clone := &Toolkit{
		env:                 &env,
		boundary:            t.boundary,
		authorizer:          t.authorizer,
		mcpManager:          t.mcpManager,
		activityRegistry:    t.activityRegistry,
		mcpActivityBindings: cloneMCPActivityBindings(t.mcpActivityBindings),
	}
	t.exposureMu.RLock()
	clone.toolSearchEnabled = t.toolSearchEnabled
	clone.nativeDeferredDiscovery = t.nativeDeferredDiscovery
	t.exposureMu.RUnlock()
	// The code-mode service owns one host connection per Wuu session. Thread
	// clones share the pointer so every conversation in the workspace keeps
	// using the same runtime; per-thread mutable state stays independent.
	t.codeModeMu.RLock()
	clone.codeMode = t.codeMode
	clone.codeModeOnly = t.codeModeOnly
	t.codeModeMu.RUnlock()
	t.activeProfileMu.RLock()
	clone.activeProfile = t.activeProfile
	clone.activeSurface = cloneSurface(t.activeSurface)
	t.activeProfileMu.RUnlock()
	if len(t.disabledTools) > 0 {
		clone.disabledTools = make(map[string]struct{}, len(t.disabledTools))
		for name := range t.disabledTools {
			clone.disabledTools[name] = struct{}{}
		}
	}
	clone.activeProfileMu.Lock()
	clone.publishActiveSurfaceLocked()
	clone.activeProfileMu.Unlock()
	// Deferred tool loads are model-visible, per-conversation state created by
	// tool_search. A cloned toolkit must not inherit them unless the clone's
	// own model context has seen the loadable schema.
	clone.rebuildRegistry()
	clone.refreshMCPToolSnapshot(true)
	return clone, nil
}

// rebuildRegistry constructs the tool registry from the current Env.
// Called at construction and whenever dependencies change.
func (t *Toolkit) rebuildRegistry() {
	e := t.env
	registered := []Tool{
		// File operations
		NewReadFileTool(e),
		NewWriteFileTool(e),
		NewListFilesTool(e),
		NewEditFileTool(e),
		NewApplyPatchTool(e),
		// Search
		NewGrepTool(e),
		NewGlobTool(e),
		// Bash is the unified command entry point emitted by the model
		// profile compiler. It covers foreground commands, local
		// verification, and the full background-process lifecycle.
		NewBashTool(e),
		// Git
		NewGitTool(e),
		// Web
		NewWebSearchTool(e),
		NewWebFetchTool(e),
		// Skills
		NewLoadSkillTool(e),
		// Session/thread lookup (used after right-click "copy ID" on the
		// desktop session tree — agents receive a thread ID and resolve it
		// back to the full conversation via this tool).
		NewThreadGetTool(e),
		NewSetSessionWorkspaceTool(e),
		NewNewContextTool(),
		NewHistoryReadTool(e),
		NewHistorySearchTool(e),
		// Recurring agent profiles
		NewListAgentProfilesTool(e),
		NewCreateAgentProfileTool(e),
		// Embedded browser automation (default-disabled in New(); enabled per
		// session by SetBrowserEnabled off WUU_ENABLE_BROWSER).
		NewBrowserTool(e),
		// Deferred tool discovery
		NewToolSearchTool(t),
	}
	if e.ChatAgent != nil {
		registered = append(registered, NewYieldTurnTool())
		registered = append(registered, NewChatCheckTool(e), NewChatReadTool(e), NewChatSessionTool(e), NewCollaborationSendTool(e), NewChatDraftTool(e), NewChatTaskTool(e), NewChatWorkTool(e), NewChatRemindTool(e), NewChatWakeTool(e), NewChatMemoryTool(e))
		registered = append(registered, NewChatSendTool(e), NewChatVerifyTool(e), NewChatRosterTool(e))
	}
	// Code-mode entry tools appear only when a host service is attached to the
	// session. They stay out of the registry otherwise, so Direct mode never
	// advertises a runtime it cannot reach.
	t.codeModeMu.RLock()
	hasCodeMode := t.codeMode != nil
	t.codeModeMu.RUnlock()
	if hasCodeMode {
		registered = append(registered, NewCodeModeExecTool(t), NewCodeModeWaitTool(t))
	}
	t.registry = NewRegistry(registered...)
}

// SetCodeModeService attaches the session-scoped code-mode runtime and makes
// its exec/wait entry tools visible to the model. Setting nil removes them.
func (t *Toolkit) SetCodeModeService(service *codemode.Service) {
	t.codeModeMu.Lock()
	t.codeMode = service
	t.codeModeMu.Unlock()
	t.rebuildRegistry()
	t.activeProfileMu.Lock()
	t.publishActiveSurfaceLocked()
	t.activeProfileMu.Unlock()
}

// CodeModeService returns the session's code-mode runtime, if enabled.
func (t *Toolkit) CodeModeService() *codemode.Service {
	t.codeModeMu.RLock()
	defer t.codeModeMu.RUnlock()
	return t.codeMode
}

// SetCodeModeOnly switches the model-visible surface between the full tool
// list and only the code-mode exec/wait entries. Underlying tools are hidden,
// not disabled: they stay executable because live cells invoke them through
// the nested execution pipeline.
func (t *Toolkit) SetCodeModeOnly(only bool) {
	t.codeModeMu.Lock()
	t.codeModeOnly = only
	t.codeModeMu.Unlock()
}

// CodeModeOnly reports whether only the code-mode entry tools are visible.
func (t *Toolkit) CodeModeOnly() bool {
	t.codeModeMu.RLock()
	defer t.codeModeMu.RUnlock()
	return t.codeModeOnly
}

// ── Dependency setters ─────────────────────────────────────────────

// SetAgentControl attaches the shared agent control runtime.
func (t *Toolkit) SetAgentControl(c *agentcontrol.AgentControl) {
	t.env.AgentControl = c
}

func (t *Toolkit) SetChatAgent(client *channels.AgentClient) {
	if t == nil || t.env == nil {
		return
	}
	t.env.ChatAgent = client
	t.rebuildRegistry()
	kind := modelprofile.SurfaceNamedAgent
	if client != nil && client.IsRoomRuntime() {
		kind = modelprofile.SurfaceRoomAgent
	}
	t.setActiveProfileForSurface(t.ActiveProfile(), kind)
}

// SetImageInputSupported installs the active model's resolved image-input
// capability for rich tool-result projection.
func (t *Toolkit) SetImageInputSupported(supported bool) {
	if t == nil || t.env == nil {
		return
	}
	t.env.ImageInputSupported = &supported
}

// SetNativeDeferredToolDiscovery configures whether ordinary tool results may
// attach provider-native discovered schemas. When false, tool_search remains
// the only path that loads deferred schemas into the current tool surface.
func (t *Toolkit) SetNativeDeferredToolDiscovery(enabled bool) {
	if t == nil {
		return
	}
	t.exposureMu.Lock()
	t.nativeDeferredDiscovery = enabled
	t.exposureMu.Unlock()
	if t.env != nil {
		t.env.NativeDeferredToolDiscovery = enabled
	}
}

func (t *Toolkit) NativeDeferredToolDiscovery() bool {
	return t.nativeDeferredToolDiscoveryEnabled()
}

func (t *Toolkit) SetToolSearchEnabled(enabled bool) {
	if t == nil {
		return
	}
	t.exposureMu.Lock()
	t.toolSearchEnabled = enabled
	t.exposureMu.Unlock()
	if t.env != nil {
		t.env.ToolSearchEnabled = enabled
	}
	t.activeProfileMu.Lock()
	t.publishActiveSurfaceLocked()
	t.activeProfileMu.Unlock()
}

func (t *Toolkit) ToolSearchEnabled() bool {
	if t == nil {
		return false
	}
	t.exposureMu.RLock()
	defer t.exposureMu.RUnlock()
	return t.toolSearchEnabled
}

func (t *Toolkit) nativeDeferredToolDiscoveryEnabled() bool {
	if t == nil {
		return false
	}
	t.exposureMu.RLock()
	defer t.exposureMu.RUnlock()
	return t.nativeDeferredDiscovery
}

func (t *Toolkit) toolSearchCanLoadDeferredTool(string) bool { return true }

// SetProcessManager attaches the process manager.
func (t *Toolkit) SetProcessManager(m *proc.Manager) {
	t.env.ProcessMgr = m
}

// SetStateDir sets the workspace-scoped runtime state directory.
func (t *Toolkit) SetStateDir(dir string) {
	t.env.StateDir = strings.TrimSpace(dir)
}

// SetSkills attaches the discovered skills.
func (t *Toolkit) SetSkills(s []skills.Skill) {
	t.env.Skills = s
}

// Skills returns the currently registered skills (read-only).
func (t *Toolkit) Skills() []skills.Skill {
	return t.env.Skills
}

// SetSessionID sets the current session ID.
func (t *Toolkit) SetSessionID(id string) {
	t.env.SessionID = id
}

// SessionID returns the session currently bound to this toolkit.
func (t *Toolkit) SessionID() string {
	if t == nil || t.env == nil {
		return ""
	}
	return t.env.SessionID
}

// SetSessionDir sets the session directory for result budgeting.
func (t *Toolkit) SetSessionDir(dir string) {
	t.env.SessionDir = dir
}

// SetSessionsDir binds session-history tools to a specific SQLite store.
func (t *Toolkit) SetSessionsDir(dir string) {
	if t == nil || t.env == nil {
		return
	}
	t.env.SessionsDir = strings.TrimSpace(dir)
}

// SessionDir returns the session artifact directory currently bound to this toolkit.
func (t *Toolkit) SessionDir() string {
	if t == nil || t.env == nil {
		return ""
	}
	return t.env.SessionDir
}

// SetAgentIdentity sets the current agent identity for relative agent-path
// resolution inside orchestration tools.
func (t *Toolkit) SetAgentIdentity(id, path string) {
	t.env.AgentID = strings.TrimSpace(id)
	t.env.AgentPath = strings.TrimSpace(path)
}

// SetOnFileChanged sets the callback fired after write_file/edit_file
// successfully modifies a file. Used to dispatch FileChanged hooks.
func (t *Toolkit) SetOnFileChanged(fn func(absPath string)) {
	t.env.OnFileChanged = fn
}

// SetOnSessionWorkspaceChanged attaches the app-server persistence and
// notification hook used by set_session_workspace.
func (t *Toolkit) SetOnSessionWorkspaceChanged(fn func(root string) error) {
	t.env.OnSessionWorkspaceChanged = fn
}

// SetMCPManager attaches the MCP manager so its tools are exposed to the agent.
func (t *Toolkit) SetMCPManager(m *mcp.Manager) {
	t.mcpManager = m
	t.refreshMCPToolSnapshot(true)
}

func (t *Toolkit) MCPManager() *mcp.Manager {
	if t == nil {
		return nil
	}
	return t.mcpManager
}

// SetBoundary installs the single authority gate for this runtime. It also
// wires Env.Unconfined (path confinement) and Env.AllowMutations (per-path
// mutation exemptions) so they both track the boundary.
func (t *Toolkit) SetBoundary(boundary WorkspaceBoundary) {
	if t == nil {
		return
	}
	t.boundary = boundary
	if t.env != nil {
		t.env.Unconfined = !boundary.Enforce
		t.env.AllowMutations = boundary.AllowMutations
		t.env.boundaryConfigured = true
	}
}

// Boundary returns the currently installed authority gate. Worker wake
// paths use it to re-copy the parent runtime's live boundary onto a woken
// worker's toolkit, the same inheritance CloneForRoot performs at spawn.
func (t *Toolkit) Boundary() WorkspaceBoundary {
	if t == nil {
		return WorkspaceBoundary{}
	}
	return t.boundary
}

// AgentControl returns the attached agent control runtime, or nil.
func (t *Toolkit) AgentControl() *agentcontrol.AgentControl {
	return t.env.AgentControl
}

// ── Tool disabling ─────────────────────────────────────────────────

// DisableTools removes specific tools from this toolkit instance.
func (t *Toolkit) DisableTools(names ...string) {
	if t.disabledTools == nil {
		t.disabledTools = make(map[string]struct{}, len(names))
	}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		t.disabledTools[n] = struct{}{}
	}
}

// EnableTools re-enables specific tools that were previously disabled.
func (t *Toolkit) EnableTools(names ...string) {
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		delete(t.disabledTools, n)
	}
}

// SetBrowserBridge attaches the desktop transport for the embedded browser
// backend. Nil leaves the tool dependency-less so it returns a clear
// execute-time error instead of panicking.
func (t *Toolkit) SetBrowserBridge(bridge BrowserBridge) {
	if t == nil || t.env == nil {
		return
	}
	t.env.BrowserBridge = bridge
}

// SetBrowserTabs attaches the durable per-thread tab store. Nil keeps tab
// addressing in-memory only.
func (t *Toolkit) SetBrowserTabs(store BrowserTabStore) {
	if t == nil || t.env == nil {
		return
	}
	t.env.BrowserTabs = store
}

// SetBrowserEnabled gates the embedded browser tool. New toolkits keep it
// disabled; the runtime opts in per session (WUU_ENABLE_BROWSER). It toggles
// disabledTools then republishes the surface so the catalog reflects availability.
func (t *Toolkit) SetBrowserEnabled(enabled bool) {
	if t == nil {
		return
	}
	if enabled {
		t.EnableTools(browserToolName)
	} else {
		t.DisableTools(browserToolName)
	}
	t.activeProfileMu.Lock()
	t.publishActiveSurfaceLocked()
	t.activeProfileMu.Unlock()
}

// SetContextWindowToolsEnabled exposes host-owned context switching only when
// the runtime can install a continuation checkpoint. History recovery tools are
// independent of note compaction.
func (t *Toolkit) SetContextWindowToolsEnabled(enabled bool) {
	if t == nil {
		return
	}
	if enabled {
		t.EnableTools(contextWindowToolNames...)
	} else {
		t.DisableTools(contextWindowToolNames...)
	}
	t.activeProfileMu.Lock()
	t.publishActiveSurfaceLocked()
	t.activeProfileMu.Unlock()
}

// SetGitAttributionEnabled controls automatic WUU Agent co-author trailers
// for commits created through both the structured git tool and bash.
func (t *Toolkit) SetGitAttributionEnabled(enabled bool) {
	if t == nil || t.env == nil {
		return
	}
	t.env.GitAttributionDisabled = !enabled
}

// GitAttributionEnabled reports the effective commit-attribution state carried
// by this toolkit. Runtime owners use it to verify that long-lived thread
// toolkits have caught up with the persisted general setting.
func (t *Toolkit) GitAttributionEnabled() bool {
	return t != nil && t.env != nil && t.env.gitAttributionEnabled()
}

func (t *Toolkit) isToolDisabled(name string) bool {
	if len(t.disabledTools) == 0 {
		return false
	}
	_, ok := t.disabledTools[name]
	return ok
}

// ── ToolExecutor interface ─────────────────────────────────────────

// Definitions returns JSON-schema tool definitions for the current request.
// In native deferred-loading mode, deferred tool declarations are sent with
// DeferLoading=true so the provider can keep them out of the initial prompt
// prefix. In Wuu tool_search mode, deferred tools stay hidden until
// tool_search loads their schemas; once loaded, they are appended after the
// stable direct-tool prefix so generic tool-calling providers can call them on
// the next turn.
//
// When an active profile has been installed via SetActiveProfile,
// the visible set is the per-profile compiled surface. Without an
// active profile the toolkit falls back to the legacy direct-tool
// surface: every direct tool the registry knows about, in the same
// order the legacy code returned them. The legacy fallback exists so
// callers that have not migrated yet (replay, debugging, internal
// admin tools) keep working.
func (t *Toolkit) Definitions() []providers.ToolDefinition {
	if t.CodeModeOnly() {
		return t.codeModeEntryDefinitions()
	}
	t.refreshMCPToolSnapshot(false)
	all := t.registry.Definitions()
	surface := t.activeCompiledSurface()
	hasSurface := surface.ProfileName != ""
	stable := make([]providers.ToolDefinition, 0, len(all))
	dynamic := make([]providers.ToolDefinition, 0)
	nativeDeferred := t.nativeDeferredToolDiscoveryEnabled()
	for _, d := range all {
		if t.isToolDisabled(d.Name) {
			continue
		}
		exposure := t.toolExposure(d.Name)
		if exposure == ToolExposureDirect {
			if classifyToolKind(d.Name) == ToolKindMCP {
				d.CacheStable = false
				dynamic = append(dynamic, d)
				continue
			}
			d.CacheStable = true
			stable = append(stable, d)
			continue
		}
		if exposure == ToolExposureDeferred && (nativeDeferred || t.isDeferredToolLoaded(d.Name)) {
			d.CacheStable = false
			if nativeDeferred {
				d.DeferLoading = true
			}
			dynamic = append(dynamic, d)
		}
	}
	out := make([]providers.ToolDefinition, 0, len(stable)+len(dynamic))
	out = append(out, stable...)
	out = append(out, dynamic...)
	// Append direct MCP tools after built-ins to preserve prompt cache stability.
	for _, tool := range t.mcpToolsSnapshot() {
		name := tool.Name()
		if hasSurface && !activeSurfaceAllowsKnownTool(surface, tool) {
			continue
		}
		exposure := t.toolExposure(name)
		if exposure == ToolExposureDirect || (exposure == ToolExposureDeferred && (nativeDeferred || t.isDeferredToolLoaded(name))) {
			d := tool.Definition()
			d.CacheStable = false
			if nativeDeferred && exposure == ToolExposureDeferred {
				d.DeferLoading = true
			}
			out = append(out, d)
		}
	}
	return out
}

// SupportsTool reports whether the active model surface can use the named
// tool after any required progressive loading. Deferred tools return true here
// even before tool_search loads their schema; Execute still enforces the
// loaded-before-use rule.
// FreezeToolSurface pins the MCP catalog snapshot so Definitions() keeps
// returning the same provider tool list while a model run is in flight —
// asynchronous server events (list_changed, connect, disconnect) would
// otherwise reshape req.Tools between rounds and invalidate the prompt-cache
// prefix for the whole request. Freezes nest; the deferred catalog change is
// applied by the first Definitions() call after the last Unfreeze. Only
// externally-driven changes are pinned — model-initiated deferred tool loads
// still surface immediately.
func (t *Toolkit) FreezeToolSurface() {
	if t == nil {
		return
	}
	t.mcpCatalogMu.Lock()
	t.surfaceFreezeDepth++
	t.mcpCatalogMu.Unlock()
}

// UnfreezeToolSurface releases one FreezeToolSurface pin.
func (t *Toolkit) UnfreezeToolSurface() {
	if t == nil {
		return
	}
	t.mcpCatalogMu.Lock()
	if t.surfaceFreezeDepth > 0 {
		t.surfaceFreezeDepth--
	}
	t.mcpCatalogMu.Unlock()
}

func (t *Toolkit) SupportsTool(name string) bool {
	name = strings.TrimSpace(name)
	if t == nil || name == "" || t.isToolDisabled(name) {
		return false
	}
	if t.LookupTool(name) == nil {
		return false
	}
	switch t.toolExposure(name) {
	case ToolExposureDirect, ToolExposureDeferred:
		return true
	default:
		return false
	}
}

// SetRootDir re-roots the toolkit's execution environment (bash cwd,
// relative-path resolution, search root, display paths) without rebuilding
// the toolkit or touching other per-session state. The file-scope whitelist
// (SetFileScopeRoots) is intentionally independent: changing the root changes
// where tools work by default, not what they may touch. Callers
// must only invoke this between turns, never while tools are executing.
func (t *Toolkit) SetRootDir(rootDir string) {
	if t == nil || t.env == nil {
		return
	}
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return
	}
	if ev, err := filepath.EvalSymlinks(abs); err == nil {
		abs = ev
	}
	t.env.RootDir = abs
}

// RootDir returns the workspace root the toolkit currently executes in.
func (t *Toolkit) RootDir() string {
	if t == nil || t.env == nil {
		return ""
	}
	return t.env.RootDir
}

// SetWorkspaceID binds tools to the stable identity of their workspace.
func (t *Toolkit) SetWorkspaceID(workspaceID string) {
	if t == nil || t.env == nil {
		return
	}
	t.env.WorkspaceID = strings.TrimSpace(workspaceID)
}

// SetFileScopeRoots installs (or clears, with an empty slice) the file-tool
// whitelist for ordinary turns and scoped participant runs: agent home,
// registered workspace roots, and the system temp directory. Empty roots
// keep the ordinary single-RootDir confinement.
func (t *Toolkit) SetFileScopeRoots(roots []string) {
	if t == nil || t.env == nil {
		return
	}
	cleaned := make([]string, 0, len(roots))
	for _, root := range roots {
		if trimmed := strings.TrimSpace(root); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 0 {
		t.env.FileScopeRoots = nil
		return
	}
	t.env.FileScopeRoots = cleaned
}

// SurfaceToolNames returns the registered built-in tools available to the
// active model surface, including deferred tools before they are loaded.
func (t *Toolkit) SurfaceToolNames() []string {
	if t == nil || t.registry == nil {
		return nil
	}
	surface := t.activeCompiledSurface()
	out := make([]string, 0)
	for _, tool := range t.registry.All() {
		if tool == nil {
			continue
		}
		name := tool.Name()
		if t.isToolDisabled(name) {
			continue
		}
		if surface.ProfileName != "" {
			if !activeSurfaceAllowsKnownTool(surface, tool) {
				continue
			}
		} else if t.toolExposure(name) == ToolExposureHidden {
			continue
		}
		out = append(out, name)
	}
	return out
}

// SetActiveProfile installs the model profile that drives
// Definitions(). The toolkit compiles the profile into a Surface and
// uses it as the whitelist for visible tool names.
//
// Passing the zero value clears the active profile and restores the
// legacy direct-tool surface. This is the right behavior for the
// CLI's `wuu debug tools` path and for any admin caller that wants
// to inspect every tool the registry knows about, regardless of
// model context.
//
// forMainAgent must be true when configuring a main-agent kit so the surface
// can include or hide main-agent-only recovery tools consistently. Worker kits
// built via CloneForRoot pass false so the compiled surface omits them; the
// same boundary is enforced at runtime by worker tool filtering and
// tool-specific path checks.
func (t *Toolkit) SetActiveProfile(p modelprofile.Profile, forMainAgent bool) {
	if t.IsRoomAgent() {
		t.setActiveProfileForSurface(p, modelprofile.SurfaceRoomAgent)
		return
	}
	kind := modelprofile.SurfaceWorker
	if forMainAgent {
		kind = modelprofile.SurfaceMain
	}
	t.setActiveProfileForSurface(p, kind)
}

func (t *Toolkit) setActiveProfileForSurface(p modelprofile.Profile, kind modelprofile.SurfaceKind) {
	if t == nil {
		return
	}
	if (p == modelprofile.Profile{}) {
		t.SetEditToolMode(EditToolModeText)
	} else {
		t.SetEditToolMode(EditToolModeForProfile(p))
	}

	t.activeProfileMu.Lock()
	defer t.activeProfileMu.Unlock()
	t.activeProfile = p
	if (p == modelprofile.Profile{}) && kind != modelprofile.SurfaceNamedAgent && kind != modelprofile.SurfaceRoomAgent {
		t.activeSurface = capability.Surface{}
		t.publishActiveSurfaceLocked()
		return
	}
	compiledProfile := p
	if (compiledProfile == modelprofile.Profile{}) {
		compiledProfile = modelprofile.Resolve("wuu", "named-agent-chat")
	}
	t.activeSurface = modelprofile.DefaultCompiler{}.Compile(compiledProfile, kind)
	t.publishActiveSurfaceLocked()
}

// ActiveProfile returns the currently installed model profile, or the
// zero value if none is installed.
func (t *Toolkit) ActiveProfile() modelprofile.Profile {
	if t == nil {
		return modelprofile.Profile{}
	}
	t.activeProfileMu.RLock()
	defer t.activeProfileMu.RUnlock()
	return t.activeProfile
}

// ActiveSurface returns the compiled surface for the active profile.
// The returned value is a copy; callers can inspect it freely.
func (t *Toolkit) ActiveSurface() capability.Surface {
	if t == nil {
		return capability.Surface{}
	}
	t.activeProfileMu.RLock()
	defer t.activeProfileMu.RUnlock()
	return t.exposedSurfaceLocked()
}

// exposedSurfaceLocked returns a defensive copy of the active surface as the
// model actually sees it: the compiled surface projected for the current
// tool-loading mode with disabled tools removed. Callers must hold
// activeProfileMu (read or write).
func (t *Toolkit) exposedSurfaceLocked() capability.Surface {
	return t.withDisabledToolsRemoved(t.withCodeModeSurface(cloneSurface(t.surfaceForToolLoadingMode(t.activeSurface))))
}

// publishActiveSurfaceLocked is the single write path for env.ActiveSurface.
// Routing every update through it guarantees that surface consumers outside
// the toolkit (skill filtering, prompt assembly, surface snapshots) never see
// tools that Definitions() hides and Execute() rejects. Callers must hold
// activeProfileMu for writing.
func (t *Toolkit) publishActiveSurfaceLocked() {
	if t.env == nil {
		return
	}
	t.env.ActiveSurface = t.exposedSurfaceLocked()
}

// activeCompiledSurface is the internal helper Definitions() uses
// to read the active surface under the read lock. Kept package-private
// so the public ActiveSurface() getter can hand callers a copy
// without contention on the hot Definitions() path.
func (t *Toolkit) activeCompiledSurface() capability.Surface {
	if t == nil {
		return capability.Surface{}
	}
	t.activeProfileMu.RLock()
	defer t.activeProfileMu.RUnlock()
	return t.withDisabledToolsRemoved(t.withCodeModeSurface(t.surfaceForToolLoadingMode(t.activeSurface)))
}

func (t *Toolkit) withDisabledToolsRemoved(surface capability.Surface) capability.Surface {
	if len(t.disabledTools) == 0 || surface.ProfileName == "" {
		return surface
	}
	out := cloneSurface(surface)
	for name := range t.disabledTools {
		delete(out.Tools, name)
		delete(out.DeferredTools, name)
		delete(out.HiddenTools, name)
	}
	return out
}

func (t *Toolkit) surfaceForToolLoadingMode(surface capability.Surface) capability.Surface {
	if surface.ProfileName == "" || t.toolSearchEnabledForSurface() {
		return surface
	}
	out := cloneSurface(surface)
	delete(out.Tools, "tool_search")
	for name, capName := range out.DeferredTools {
		out.Tools[name] = capName
	}
	out.DeferredTools = map[string]capability.Capability{}
	for _, capName := range out.DeferredCapabilities {
		if !surfaceHasCapability(out.Capabilities, capName) {
			out.Capabilities = append(out.Capabilities, capName)
		}
	}
	out.DeferredCapabilities = nil
	return out
}

func (t *Toolkit) toolSearchEnabledForSurface() bool {
	if t == nil {
		return false
	}
	t.exposureMu.RLock()
	defer t.exposureMu.RUnlock()
	return t.toolSearchEnabled
}

func surfaceHasCapability(caps []capability.Capability, capName capability.Capability) bool {
	for _, existing := range caps {
		if existing == capName {
			return true
		}
	}
	return false
}

func cloneSurface(surface capability.Surface) capability.Surface {
	out := surface
	if len(surface.Tools) > 0 {
		out.Tools = make(map[string]capability.Capability, len(surface.Tools))
		for name, cap := range surface.Tools {
			out.Tools[name] = cap
		}
	}
	if len(surface.DeferredTools) > 0 {
		out.DeferredTools = make(map[string]capability.Capability, len(surface.DeferredTools))
		for name, cap := range surface.DeferredTools {
			out.DeferredTools[name] = cap
		}
	}
	if len(surface.HiddenTools) > 0 {
		out.HiddenTools = make(map[string]capability.Capability, len(surface.HiddenTools))
		for name, cap := range surface.HiddenTools {
			out.HiddenTools[name] = cap
		}
	}
	out.Capabilities = append([]capability.Capability(nil), surface.Capabilities...)
	out.DeferredCapabilities = append([]capability.Capability(nil), surface.DeferredCapabilities...)
	out.HiddenCapabilities = append([]capability.Capability(nil), surface.HiddenCapabilities...)
	return out
}

// Execute runs one tool call and returns JSON result. This is the
// registry-based dispatch that replaces the old switch-case.
//
// Large results are automatically persisted to disk when a SessionDir
// is configured, so the model receives a compact reference instead of a
// truncated blob.
func (t *Toolkit) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	result, err := t.ExecuteResult(ctx, call)
	return result.TextProjection(), err
}

// ExecuteResult runs one tool call without flattening images, structured
// content, metadata, or Activity references. Legacy tools are wrapped as one
// text content part until they migrate to RichTool.
func (t *Toolkit) ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	if t.isToolDisabled(call.Name) {
		return toolresult.Result{}, fmt.Errorf("tool %q is disabled in this session", call.Name)
	}
	if err := t.ensureToolAvailableForExecution(call.Name); err != nil {
		return toolresult.Result{}, err
	}
	tool := t.registry.Lookup(call.Name)
	if tool == nil {
		for _, mcpTool := range t.mcpToolsSnapshot() {
			if mcpTool.Name() == call.Name {
				return t.executeActivityBoundToolResult(ctx, call, mcpTool, mcpTool.ServerName())
			}
		}
		return toolresult.Result{}, fmt.Errorf("unknown tool %q", call.Name)
	}
	// The built-in browser tool binds its tab-addressed actions to a KindBrowser
	// activity lease, the same discipline MCP CUA tools get, via the shared spine.
	if call.Name == browserToolName {
		return t.executeBrowserToolResult(ctx, call, tool)
	}
	return t.executeKnownToolResult(ctx, call, tool)
}

func (t *Toolkit) ensureToolAvailableForExecution(name string) error {
	surface := t.activeCompiledSurface()
	if surface.ProfileName != "" {
		if !activeSurfaceAllowsKnownTool(surface, t.LookupTool(name)) {
			return fmt.Errorf("tool %q is not available in the active model surface", name)
		}
		if activeSurfaceToolExposure(surface, name) == ToolExposureDeferred && !t.isDeferredToolLoaded(name) {
			return fmt.Errorf("tool %q is deferred; call tool_search first to load it", name)
		}
		return nil
	}
	if t.toolExposure(name) == ToolExposureDeferred && !t.isDeferredToolLoaded(name) {
		return fmt.Errorf("tool %q is deferred; call tool_search first to load it", name)
	}
	return nil
}

func activeSurfaceAllowsDynamicTool(surface capability.Surface, name string) bool {
	if surface.ProfileName == "" {
		return true
	}
	if _, ok := surface.Tools[name]; ok {
		return true
	}
	if _, ok := surface.DeferredTools[name]; ok {
		return true
	}
	if classifyToolKind(name) == ToolKindMCP && surfaceHasAvailableCapability(surface, capability.CapabilityMCP) {
		return true
	}
	return false
}

func activeSurfaceAllowsKnownTool(surface capability.Surface, tool Tool) bool {
	if tool == nil {
		return false
	}
	if !activeSurfaceAllowsDynamicTool(surface, tool.Name()) {
		return false
	}
	if classifyToolKind(tool.Name()) == ToolKindMCP &&
		surfaceLacksTerminalExecution(surface) {
		return mcpToolAllowedWithoutTerminalExecution(surface, tool)
	}
	return true
}

func mcpToolAllowedWithoutTerminalExecution(surface capability.Surface, tool Tool) bool {
	if mcpToolMentionsTerminalOnlyPath(tool) {
		return false
	}
	capName, ok := mcpToolCapability(tool)
	if !ok {
		return false
	}
	if !surfaceHasAvailableCapability(surface, capName) {
		return false
	}
	if !isReadOnlyMCPProfileCapability(capName) {
		return false
	}
	return tool.IsReadOnly()
}

func isReadOnlyMCPProfileCapability(capName capability.Capability) bool {
	switch capName {
	case capability.CapabilityFileRead,
		capability.CapabilityFileList,
		capability.CapabilitySearchGrep,
		capability.CapabilitySearchGlob,
		capability.CapabilitySearchAST,
		capability.CapabilitySearchSemantic,
		capability.CapabilityWebFetch,
		capability.CapabilityWebSearch:
		return true
	default:
		return false
	}
}

func surfaceHasVisibleCapability(surface capability.Surface, capName capability.Capability) bool {
	for _, existing := range surface.Capabilities {
		if existing == capName {
			return true
		}
	}
	return false
}

func surfaceHasAvailableCapability(surface capability.Surface, capName capability.Capability) bool {
	for _, existing := range surface.Capabilities {
		if existing == capName {
			return true
		}
	}
	for _, existing := range surface.DeferredCapabilities {
		if existing == capName {
			return true
		}
	}
	return false
}

func activeSurfaceToolExposure(surface capability.Surface, name string) ToolExposure {
	if surface.ProfileName == "" {
		return ToolExposureDirect
	}
	if _, ok := surface.Tools[name]; ok {
		return ToolExposureDirect
	}
	if _, ok := surface.DeferredTools[name]; ok {
		return ToolExposureDeferred
	}
	if classifyToolKind(name) == ToolKindMCP {
		if surfaceHasVisibleCapability(surface, capability.CapabilityMCP) {
			return ToolExposureDirect
		}
		if surfaceHasAvailableCapability(surface, capability.CapabilityMCP) {
			return ToolExposureDeferred
		}
	}
	return ToolExposureHidden
}

// LookupTool returns the Tool with the given name, or nil. This
// allows callers (e.g. the agent loop) to inspect tool metadata
// like IsReadOnly() and IsConcurrencySafe() for scheduling.
func (t *Toolkit) LookupTool(name string) Tool {
	if tool := t.registry.Lookup(name); tool != nil {
		return tool
	}
	for _, tool := range t.mcpToolsSnapshot() {
		if tool.Name() == name {
			return tool
		}
	}
	return nil
}

func (t *Toolkit) refreshMCPToolSnapshot(force bool) {
	if t == nil {
		return
	}
	manager := t.mcpManager
	if manager == nil {
		t.mcpCatalogMu.Lock()
		t.mcpCatalogGeneration = 0
		t.mcpTools = nil
		t.mcpCatalogMu.Unlock()
		return
	}
	generation := manager.Generation()
	t.mcpCatalogMu.RLock()
	current := t.mcpCatalogGeneration
	frozen := t.surfaceFreezeDepth > 0
	t.mcpCatalogMu.RUnlock()
	// While the surface is frozen (a model run is in flight), asynchronous
	// catalog generations are deliberately ignored: changing the tool list
	// between rounds would invalidate the prompt-cache prefix mid-run. The
	// pending generation is picked up on the first refresh after unfreeze.
	if !force && (frozen || current == generation) {
		return
	}
	native := manager.NativeTools()
	tools := make([]*mcp.MCPTool, 0, len(native))
	for _, item := range native {
		tools = append(tools, mcp.NewMCPTool(item.Client, item.Definition))
	}
	t.mcpCatalogMu.Lock()
	t.mcpCatalogGeneration = generation
	t.mcpTools = tools
	t.mcpCatalogMu.Unlock()
}

func (t *Toolkit) mcpToolsSnapshot() []*mcp.MCPTool {
	if t == nil {
		return nil
	}
	t.mcpCatalogMu.RLock()
	defer t.mcpCatalogMu.RUnlock()
	return append([]*mcp.MCPTool(nil), t.mcpTools...)
}

// ToolMetadata implements agent.ToolMetadataProvider so the loop can
// partition tool calls into concurrent (read-only) and serial (write)
// batches without importing the tools package.
func (t *Toolkit) ToolMetadata(call providers.ToolCall) (agent.ToolMetadata, bool) {
	tool := t.LookupTool(call.Name)
	if tool == nil {
		return agent.ToolMetadata{}, false
	}
	info := buildToolInfoForArgs(tool, t.toolExposure(call.Name), call.Arguments)
	orchestrator := false
	if marker, ok := tool.(OrchestratorTool); ok {
		orchestrator = marker.IsOrchestrator()
	}
	return agent.ToolMetadata{
		Orchestrator:    orchestrator,
		ReadOnly:        info.ReadOnly,
		ConcurrencySafe: info.ConcurrencySafe,
		Destructive:     info.Destructive,
		Risk:            string(info.Risk),
		Reason:          info.Reason,
	}, true
}

// ── Shared utilities (used by individual tool files) ───────────────

func nonInteractiveShellEnv() map[string]string {
	return map[string]string{
		"EDITOR":              "true",
		"GIT_EDITOR":          "true",
		"GIT_PAGER":           "cat",
		"GIT_SEQUENCE_EDITOR": "true",
		"GIT_TERMINAL_PROMPT": "0",
		"GH_PAGER":            "cat",
		"NO_COLOR":            "1",
		"PAGER":               "cat",
		"VISUAL":              "true",
	}
}

func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	// Windows env names are case-insensitive (Path vs PATH); merging by
	// exact key there would emit duplicates with undefined child behavior.
	canon := func(key string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(key)
		}
		return key
	}
	merged := make(map[string]string, len(base)+len(overrides))
	display := make(map[string]string, len(base)+len(overrides))
	order := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		ck := canon(key)
		if _, exists := merged[ck]; !exists {
			order = append(order, ck)
			display[ck] = key
		}
		merged[ck] = value
	}
	for key, value := range overrides {
		ck := canon(key)
		if _, exists := merged[ck]; !exists {
			order = append(order, ck)
			display[ck] = key
		}
		merged[ck] = value
	}
	out := make([]string, 0, len(order))
	for _, ck := range order {
		out = append(out, display[ck]+"="+merged[ck])
	}
	return out
}

func decodeArgs(raw string, target any) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = "{}"
	}
	if err := json.Unmarshal([]byte(trimmed), target); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}

func mustJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func truncate(value string, maxBytes int) (string, bool) {
	if len(value) <= maxBytes {
		return value, false
	}
	return stringutil.Truncate(value, maxBytes, ""), true
}

func normalizeDisplayPath(rootDir, absPath string) string {
	rel, err := filepath.Rel(rootDir, absPath)
	if err != nil {
		return absPath
	}
	if rel == "." {
		return "."
	}
	return rel
}

func isSkippedDir(name string) bool {
	switch name {
	case ".git", ".wuu", ".hg", ".svn", "node_modules", "vendor", "__pycache__", ".tox", ".venv":
		return true
	}
	return false
}

func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	const binaryCheckSize = 8192
	buf := make([]byte, binaryCheckSize)
	n, _ := f.Read(buf)
	checkBuf := buf[:n]

	nonPrintable := 0
	for _, b := range checkBuf {
		if b == 0 {
			return true
		}
		if b < 32 && b != 9 && b != 10 && b != 13 {
			nonPrintable++
		}
	}

	if len(checkBuf) > 0 && float64(nonPrintable)/float64(len(checkBuf)) > 0.1 {
		return true
	}
	return false
}

// levenshtein returns the edit distance between two strings.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr := make([]int, lb+1)
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev = curr
	}
	return prev[lb]
}

// suggestSimilarFile looks in the same directory for a filename within
// Levenshtein distance 3 of the requested file. Returns "" if none found.
func suggestSimilarFile(absPath string) string {
	dir := filepath.Dir(absPath)
	target := filepath.Base(absPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	bestDist := 4 // threshold + 1
	bestName := ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		d := levenshtein(strings.ToLower(target), strings.ToLower(e.Name()))
		if d < bestDist {
			bestDist = d
			bestName = e.Name()
		}
	}
	return bestName
}

func matchGlob(pattern, path string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	path = filepath.ToSlash(path)
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "/") {
		matched, _ := filepath.Match(pattern, filepath.Base(path))
		return matched
	}
	re, err := regexp.Compile(globToRegexp(pattern))
	if err != nil {
		return false
	}
	return re.MatchString(path)
}

func globToRegexp(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
					continue
				}
				if i+2 >= len(pattern) && i > 0 && pattern[i-1] == '/' {
					b.WriteString(".*")
					i++
					continue
				}
				b.WriteString("[^/]*")
				i++
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(pattern[i])
		default:
			b.WriteByte(pattern[i])
		}
	}
	b.WriteString("$")
	return b.String()
}

// ── Ripgrep helpers ────────────────────────────────────────────────

type grepMatch struct {
	File             string `json:"file"`
	Line             int    `json:"line"`
	Content          string `json:"content"`
	ContentTruncated bool   `json:"content_truncated,omitempty"`
}

type grepOptions struct {
	outputMode string
	context    int
	before     int
	after      int
	ignoreCase bool
}

type grepCountResult struct {
	File  string `json:"file"`
	Count int    `json:"count"`
}

type rgJSONEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber int `json:"line_number"`
	} `json:"data"`
}

var (
	rgLookupPath = exec.LookPath
	rgCommand    = exec.CommandContext
	rgPathOnce   sync.Once
	rgPath       string
)

func lookupRG() string {
	rgPathOnce.Do(func() {
		path, err := rgLookupPath("rg")
		if err == nil {
			rgPath = path
		}
	})
	return rgPath
}

func resetRGForTests() {
	rgPathOnce = sync.Once{}
	rgPath = ""
}

func buildRGFilesCommand(ctx context.Context, pattern string) *exec.Cmd {
	name := lookupRG()
	if name == "" {
		return nil
	}
	args := []string{"--no-config", "--files", "--hidden", "-0", "--glob", pattern, "."}
	return rgCommand(ctx, name, args...)
}

func buildRGGrepCommand(ctx context.Context, pattern, searchRoot, include string, opts grepOptions) *exec.Cmd {
	name := lookupRG()
	if name == "" {
		return nil
	}
	args := []string{"--no-config", "--json", "--hidden", "-H", "-n"}
	if opts.ignoreCase {
		args = append(args, "-i")
	}
	if opts.context > 0 {
		args = append(args, "-C", fmt.Sprintf("%d", opts.context))
	}
	if opts.before > 0 {
		args = append(args, "-B", fmt.Sprintf("%d", opts.before))
	}
	if opts.after > 0 {
		args = append(args, "-A", fmt.Sprintf("%d", opts.after))
	}
	if include != "" {
		args = append(args, "--glob", include)
	}
	args = append(args, "--", pattern)
	if strings.TrimSpace(searchRoot) != "" {
		args = append(args, searchRoot)
	} else {
		args = append(args, ".")
	}
	return rgCommand(ctx, name, args...)
}

func (t *Toolkit) IsRoomAgent() bool {
	return t != nil && t.env != nil && t.env.ChatAgent != nil && t.env.ChatAgent.IsRoomRuntime()
}
