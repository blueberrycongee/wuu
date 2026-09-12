package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"charm.land/catwalk/pkg/catwalk"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/claudeengine"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/codexengine"
	"github.com/blueberrycongee/wuu/internal/config"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/instructions"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/memdir"
	"github.com/blueberrycongee/wuu/internal/modelbudget"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
	"github.com/blueberrycongee/wuu/internal/modelprofile"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/participant"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/prompt"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/version"
	"github.com/blueberrycongee/wuu/internal/workspaces"
)

// Options describes the shared agent runtime needed by interactive clients.
// The shape is intentionally UI-neutral so desktop and protocol clients can
// attach without rebuilding the agent.
type Options struct {
	RootDir string
	// Host is immutable process metadata supplied by the shell or cloud
	// control plane. Its zero value resolves to a local host.
	Host Host
	// WorkspaceID is the stable, location-independent identity of the
	// workspace (the desktop's registered-project id). When set, the workspace
	// state directory is keyed by this id so it survives the project being
	// moved or renamed on disk. Empty for location-anchored roots (the shared
	// 对话 scratch dir, agent homes), which stay keyed by their path.
	WorkspaceID string
	HomeDir     string
	ConfigPath  string
	// ConfigLoadMode records the source model that produced Config so later
	// app-server reloads use the same inputs. The zero value is normal user
	// config plus restricted project overlays.
	ConfigLoadMode ConfigLoadMode
	Config         config.Config
	ProviderName   string
	ModelOverride  string
	// PermissionModeExplicit marks Config's permission mode as an explicit
	// process-scoped execution override (a CLI --permission-mode flag). The
	// override wins over per-thread pinned modes and persisted session
	// metadata for every turn this process runs, and is never written back
	// into sessions.
	PermissionModeExplicit bool
	NoTools                bool
	// DriverProfile selects the loop driver for new turns: empty keeps the
	// in-process default; a profile name binds the plugin that provides the
	// versioned "driver.<profile>" service. Unknown profiles fail closed.
	DriverProfile string
	// SafeMode discovers plugin manifests for management but activates no
	// runtime, tool, skill, hook, or desktop contribution from a plugin.
	SafeMode bool
}

// ConfigLoadMode identifies the three supported config source models. It is
// intentionally not a boolean: an explicitly trusted project config includes
// its settings layers, while an explicit file is loaded alone.
type ConfigLoadMode uint8

const (
	ConfigLoadNormal ConfigLoadMode = iota
	ConfigLoadFile
	ConfigLoadProject
)

// Session owns one initialized local agent runtime: provider client, tool
// executor, hooks, MCP, skills, execution control, process manager, and the
// stream runner. UI surfaces should depend on this instead of reassembling the
// pieces themselves.
type Session struct {
	ProviderName      string
	Model             string
	RootDir           string
	Host              Host
	WorkspaceID       string
	StateDir          string
	ConfigPath        string
	HomeDir           string
	ConfigLoadMode    ConfigLoadMode
	SessionDir        string
	StreamRunner      *agent.StreamRunner
	TitleClient       providers.Client
	HookDispatcher    *hooks.Dispatcher
	Skills            []skills.Skill
	Plugins           []pluginpkg.Plugin
	ActivePlugins     []pluginpkg.Plugin
	ExtensionSettings *extensions.Settings
	PluginHost        *pluginhost.Host
	UserQuestions     *pluginhost.UserQuestionBroker
	// DriverProfile records the driver profile bound at construction, for
	// diagnostics; the driver itself lives on the stream runner template.
	DriverProfile       string
	PluginSessionRouter *PluginSessionRouter
	systemPrompts       *agent.SystemPromptAssembler
	InstructionFiles    []instructions.File
	AgentControl        *agentcontrol.AgentControl
	ProcessManager      *process.Manager
	Toolkit             *tools.Toolkit
	ActivityRegistry    *activity.Registry
	// CodeMode is the session-scoped code-mode runtime. One host connection
	// serves every thread of this session; cells are owned per turn and
	// terminate when their owning turn ends.
	CodeMode                 *codemode.Service
	codeModeStop             context.CancelFunc
	WorkerClient             providers.StreamClient
	ModelRoles               modelroles.Set
	ModelBudget              modelbudget.Budget
	WorkerModelBudget        modelbudget.Budget
	BaseSystemPrompt         string
	BaseSystemPromptSections []prompt.SectionInfo
	// SessionDate is the session-start frozen calendar date (YYYY-MM-DD)
	// stamped into the "# Environment" system section. Freezing it here keeps
	// the cached system prefix byte-stable across turns and thread rebuilds:
	// a long-lived session that crosses a day boundary keeps its start date
	// instead of churning the prompt cache. Real-time clock reads belong in the
	// per-turn message stream, never in this cached prefix.
	SessionDate string
	WuuHome     string
	SafeMode    bool
	pluginEpoch uint64
	Permissions config.ResolvedPermissions
	// PermissionModeExplicit reports that Permissions carries an explicit
	// process-scoped override (see Options.PermissionModeExplicit): it beats
	// thread pins and session metadata and is never persisted into sessions.
	PermissionModeExplicit      bool
	maxParallel                 int
	ExperimentalCoordinatorMode bool
	ToolLoadingPreference       config.ToolLoadingMode
	ToolLoadingMode             config.ToolLoadingMode
	ToolSearchEnabled           bool
	NativeDeferredToolDiscovery bool
	DeferredToolCatalogPrompt   string
	ReadinessIssues             []ReadinessIssue
	InferenceJournalRuntime     *session.InferenceJournalRuntime
	// engines is the registry of agent engines this runtime can host. The
	// built-in wuu engine is registered at construction; external engines
	// (Claude, Codex) register as they are added.
	engines *agentengine.Registry
	// codexHost is the host behind the registered codex engine, kept so
	// settings updates can rebuild the engine around the same process.
	codexHost *codexengine.Host
	// DefaultEngine is the engine id used for new threads when the caller
	// does not request one explicitly (settings default; empty = wuu).
	DefaultEngine      agentengine.EngineID
	pluginGenerationMu sync.Mutex
	pluginGeneration   *PluginGeneration
	workerOrientation  string
	threadProcessMu    sync.Mutex
	threadProcesses    *threadProcessManagers
}

// MaxParallel returns the worker concurrency configured for this session.
func (s *Session) MaxParallel() int {
	if s == nil || s.maxParallel <= 0 {
		return config.DefaultAgentMaxParallel
	}
	return s.maxParallel
}

// cloneForThreadModel copies the shared, immutable session dependencies used
// to build a thread runtime. Thread-specific mutable dependencies are replaced
// by the caller below.
func (s *Session) cloneForThreadModel() *Session {
	if s == nil {
		return nil
	}
	clone := &Session{
		ProviderName:                s.ProviderName,
		Model:                       s.Model,
		RootDir:                     s.RootDir,
		Host:                        s.Host,
		WorkspaceID:                 s.WorkspaceID,
		StateDir:                    s.StateDir,
		ConfigPath:                  s.ConfigPath,
		HomeDir:                     s.HomeDir,
		ConfigLoadMode:              s.ConfigLoadMode,
		SessionDir:                  s.SessionDir,
		StreamRunner:                s.StreamRunner,
		TitleClient:                 s.TitleClient,
		HookDispatcher:              s.HookDispatcher,
		Skills:                      s.Skills,
		Plugins:                     s.Plugins,
		ActivePlugins:               s.ActivePlugins,
		ExtensionSettings:           s.ExtensionSettings,
		PluginHost:                  s.PluginHost,
		UserQuestions:               s.UserQuestions,
		DriverProfile:               s.DriverProfile,
		PluginSessionRouter:         s.PluginSessionRouter,
		systemPrompts:               s.systemPrompts,
		InstructionFiles:            s.InstructionFiles,
		AgentControl:                s.AgentControl,
		ProcessManager:              s.ProcessManager,
		threadProcesses:             s.threadProcessManagerPool(),
		Toolkit:                     s.Toolkit,
		CodeMode:                    s.CodeMode,
		ActivityRegistry:            s.ActivityRegistry,
		WorkerClient:                s.WorkerClient,
		ModelRoles:                  s.ModelRoles,
		ModelBudget:                 s.ModelBudget,
		WorkerModelBudget:           s.WorkerModelBudget,
		BaseSystemPrompt:            s.BaseSystemPrompt,
		BaseSystemPromptSections:    s.BaseSystemPromptSections,
		SessionDate:                 s.SessionDate,
		WuuHome:                     s.WuuHome,
		SafeMode:                    s.SafeMode,
		Permissions:                 s.Permissions,
		PermissionModeExplicit:      s.PermissionModeExplicit,
		maxParallel:                 s.maxParallel,
		ExperimentalCoordinatorMode: s.ExperimentalCoordinatorMode,
		ToolLoadingPreference:       s.ToolLoadingPreference,
		ToolLoadingMode:             s.ToolLoadingMode,
		ToolSearchEnabled:           s.ToolSearchEnabled,
		NativeDeferredToolDiscovery: s.NativeDeferredToolDiscovery,
		DeferredToolCatalogPrompt:   s.DeferredToolCatalogPrompt,
		ReadinessIssues:             s.ReadinessIssues,
		InferenceJournalRuntime:     s.InferenceJournalRuntime,
		DefaultEngine:               s.DefaultEngine,
		engines:                     s.engines,
	}
	return clone
}

type ReadinessIssue struct {
	Code     string
	Provider string
	Message  string
}

// ThreadRuntime owns the mutable execution state for one app-server
// conversation. The desktop app can run multiple ThreadRuntimes at once; each
// one has its own StreamRunner, Toolkit Env, usage tracker, and AgentControl.
type ThreadRuntime struct {
	StreamRunner      *agent.StreamRunner
	Toolkit           *tools.Toolkit
	AgentControl      *agentcontrol.AgentControl
	ProcessManager    *process.Manager
	ActivityRegistry  *activity.Registry
	ModelBudget       modelbudget.Budget
	WorkerModelBudget modelbudget.Budget
	// Selection is the (trimmed, unresolved) thread model selection this
	// runtime was built for, with PermissionMode always carrying the
	// effective normalized mode so a constructor-built stamp is never the
	// zero value. It is the reuse invariant's comparison key: a cached
	// runtime must never run a turn after the thread's selection changed
	// behind its back (e.g. another app-server process repinned the
	// session), so callers compare this stamp before reusing an idle runtime.
	Selection ThreadModelSelection
	// EngineID is the agent engine this runtime executes for. It is stamped
	// from the thread's persisted binding; the built-in engine is "wuu".
	EngineID agentengine.EngineID
	// ExecutionProfile identifies the execution contract used to construct the
	// runtime. A profile change requires a new runtime rather than hook mutation.
	ExecutionProfile string
}

// ThreadModelSelection is the model choice persisted with one conversation.
// Empty fields mean the workspace runtime defaults should be used.
type ThreadModelSelection struct {
	Provider       string
	Model          string
	Variant        string
	Effort         string
	PermissionMode string
}

// resolveWorkspaceStateDir returns the workspace state directory, keyed by the
// stable workspace id when present (so it survives the project moving on disk)
// and otherwise by the filesystem path (location-anchored roots that never
// move: the shared 对话 scratch dir, agent homes, worktree checkouts).
func resolveWorkspaceStateDir(wuuHome, workspaceID, rootDir string) (string, error) {
	if id := strings.TrimSpace(workspaceID); id != "" {
		return statepath.WorkspaceDirByID(wuuHome, id)
	}
	return statepath.WorkspaceDir(wuuHome, rootDir)
}

// NewSession builds the shared runtime for an interactive agent surface.
// browserEnabledFromEnv reports whether the embedded browser tool is switched
// on for this process. It follows the bundled-plugin gate convention (see
// plugin.EnableCUAMacEnv): enabled only when WUU_ENABLE_BROWSER trims to
// exactly "1", so an unset or any other value keeps the tool off.
func browserEnabledFromEnv() bool {
	return strings.TrimSpace(os.Getenv("WUU_ENABLE_BROWSER")) == "1"
}

func NewSession(opts Options) (*Session, error) {
	rootDir := strings.TrimSpace(opts.RootDir)
	if rootDir == "" {
		return nil, fmt.Errorf("root dir is required")
	}
	host, err := ResolveHost(opts.Host)
	if err != nil {
		return nil, err
	}
	cfg := opts.Config

	wuuHome, err := statepath.Home(opts.HomeDir)
	if err != nil {
		return nil, fmt.Errorf("resolve wuu home: %w", err)
	}
	startupPluginLease, acquired, err := session.TryAcquirePluginGenerationExecutionLease(wuuHome)
	if err != nil {
		return nil, fmt.Errorf("acquire initial plugin generation: %w", err)
	}
	if !acquired {
		return nil, errors.New("plugin packages are being changed by another app-server")
	}
	defer func() {
		if err := startupPluginLease.Release(); err != nil {
			providers.DebugLogf("release initial plugin generation: %v", err)
		}
	}()
	initialPluginGenerationEpoch := startupPluginLease.Epoch()
	workspaceID := strings.TrimSpace(opts.WorkspaceID)
	workspaceStateDir, err := resolveWorkspaceStateDir(wuuHome, workspaceID, rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace state directory: %w", err)
	}
	sessionDir := statepath.SessionsDir(wuuHome)
	journalRuntime, err := session.NewInferenceJournalRuntime(sessionDir, inferenceJournalWorkspaceScope(workspaceID, rootDir))
	if err != nil {
		return nil, fmt.Errorf("initialize inference journal: %w", err)
	}
	journalOwned := true
	defer func() {
		if journalOwned {
			_ = journalRuntime.Close()
		}
	}()
	workspaceJournal := journalRuntime.ForOwner("workspace-runtime")
	permissions := config.ResolveAgentPermissions(cfg.Agent)

	providerCfg, resolvedName, err := cfg.ResolveProvider(opts.ProviderName)
	if err != nil {
		return nil, err
	}
	if opts.ModelOverride != "" {
		providerCfg.Model = opts.ModelOverride
	}
	roleSelections, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{
		ProviderName:   resolvedName,
		ProviderConfig: providerCfg,
		Model:          providerCfg.Model,
		Effort:         cfg.Agent.Effort,
		Variant:        cfg.Agent.Variant,
	})
	if err != nil {
		return nil, err
	}
	mainRole := roleSelections.Main
	ruleProviderName, ruleProviderCfg := mainRole.RuleProvider, mainRole.RuleProviderConfig
	toolModeModel := mainRole.APIModel

	client, clientErr := providerfactory.BuildRuntimeStreamClient(ruleProviderCfg, resolvedName)
	if client == nil {
		return nil, clientErr
	}
	var readinessIssues []ReadinessIssue
	if clientErr != nil {
		readinessIssues = append(readinessIssues, ReadinessIssue{Code: "credential_missing", Provider: resolvedName, Message: clientErr.Error()})
	}
	titleClient := providers.Client(client)
	if !roleSelections.Title.Inherited {
		roleClient, roleErr := providerfactory.BuildRuntimeStreamClient(roleSelections.Title.RuleProviderConfig, roleSelections.Title.Provider)
		if roleClient == nil {
			return nil, fmt.Errorf("build title client: %w", roleErr)
		}
		if roleErr != nil {
			readinessIssues = append(readinessIssues, ReadinessIssue{Code: "credential_missing", Provider: roleSelections.Title.Provider, Message: roleErr.Error()})
		}
		titleClient = roleClient
	}

	providers.InitDebugLog(statepath.LogDir(wuuHome))
	setupCatwalk(cfg)

	discoveredPlugins := discoverPlugins(rootDir, wuuHome)
	safeMode := opts.SafeMode || strings.TrimSpace(os.Getenv("WUU_SAFE_MODE")) == "1"
	var activePlugins []pluginpkg.Plugin
	if !safeMode {
		activationPlan, activationErr := ResolvePluginActivationPlan(cfg, discoveredPlugins)
		if activationErr != nil {
			return nil, activationErr
		}
		activePlugins = activationPlan.Plugins
	}
	var agentControl *agentcontrol.AgentControl
	pluginTurnRouter := NewPluginSessionRouter()
	userQuestions := pluginhost.NewUserQuestionBroker()
	pluginHost, pluginKernel := startPluginHost(activePlugins, rootDir, workspaceID, wuuHome, workspaceStateDir, pluginTurnRouter, userQuestions)
	systemPrompts, compactions, capabilityErr := buildPluginAgentCapabilities(context.Background(), pluginHost, resolvedName, providerCfg.Model, rootDir)
	if capabilityErr != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		closeErr := pluginHost.Close(ctx)
		cancel()
		return nil, errors.Join(capabilityErr, closeErr)
	}
	hookDispatcher := buildHookDispatcher(cfg, activePlugins, providers.Client(client), toolModeModel, workspaceJournal)
	discoveredSkills := discoverSkills(rootDir, opts.HomeDir, wuuHome, activePlugins)

	processMgr, err := process.NewManager(rootDir, statepath.RuntimeDir(workspaceStateDir))
	if err != nil {
		return nil, err
	}
	activityRegistry := activity.NewRegistry()
	var toolExecutor agent.ToolExecutor
	var toolkit *tools.Toolkit
	toolLoadingPreference := cfg.Agent.ToolLoadingPreference()
	toolLoadingMode, toolSearchEnabled, nativeDeferredDiscovery := resolveToolLoadingModeForProvider(toolLoadingPreference, ruleProviderCfg, toolModeModel, mainRole.ProviderOptions)
	if !opts.NoTools {
		kit, newErr := tools.New(rootDir)
		if newErr != nil {
			return nil, newErr
		}
		kit.SetStateDir(workspaceStateDir)
		kit.SetWorkspaceID(workspaceID)
		kit.SetProcessManager(processMgr)
		kit.SetSkills(discoveredSkills)
		ConfigureToolkitPermissions(kit, permissions)
		var securityRegistry *pluginhost.ServiceRegistry
		if pluginKernel != nil {
			securityRegistry = pluginKernel.registry
		}
		configureToolkitSecurityExtensions(kit, securityRegistry)
		kit.ConfigureSurfaceForProviderModel(ruleProviderName, toolModeModel, true)
		kit.SetBrowserEnabled(browserEnabledFromEnv())
		kit.SetToolSearchEnabled(toolSearchEnabled)
		kit.SetNativeDeferredToolDiscovery(nativeDeferredDiscovery)
		kit.SetGitAttributionEnabled(cfg.Agent.GitAttributionEnabledValue())
		kit.SetActivityRegistry(activityRegistry)
		kit.SetPermissionRequestHook(func(ctx context.Context, active *tools.Toolkit, info tools.ToolInfo, call providers.ToolCall) error {
			_, err := hookDispatcher.Dispatch(ctx, hooks.PermissionRequest, &hooks.Input{
				SessionID: active.SessionID(), CWD: active.RootDir(), ToolName: call.Name,
				ToolInput: json.RawMessage(call.Arguments),
			})
			return err
		})
		kit.SetFileScopeRoots(workspaces.BoundaryRoots(kit.RootDir(), wuuHome))
		kit.SetOnFileChanged(func(absPath string) {
			_, _ = hookDispatcher.Dispatch(context.Background(), hooks.FileChanged, &hooks.Input{
				CWD:      rootDir,
				FilePath: absPath,
			})
		})
		toolkit = kit
		toolExecutor = newPluginAwareToolExecutor(kit, pluginHost, hookDispatcher, "", "", rootDir)
		connectMCPServers(cfg, activePlugins, toolkit)
	}

	// Code Mode is on by default. The service owns one host connection for the
	// whole workspace session; every thread toolkit shares it through clones.
	// Direct mode or a missing host path leaves the entry tools unregistered,
	// so the model falls back to the ordinary tool surface.
	var codeModeService *codemode.Service
	codeModeLife, codeModeStop := context.WithCancel(context.Background())
	// The lifetime context must not leak when the constructor bails out before
	// the session takes ownership of it.
	defer func() {
		if codeModeService == nil {
			codeModeStop()
		}
	}()
	if !opts.NoTools && toolkit != nil && cfg.CodeMode.InvocationMode() != config.CodeModeDirect {
		executable := strings.TrimSpace(cfg.CodeMode.Host)
		if executable == "" {
			executable = strings.TrimSpace(os.Getenv("WUU_CODE_MODE_HOST"))
		}
		if executable == "" {
			// Packaged desktop layouts keep the runtime next to wuu-core in
			// the app's bin directory. This is an explicit, absolute,
			// pinned-binary lookup — never a PATH search.
			if self, err := os.Executable(); err == nil {
				candidate := filepath.Join(filepath.Dir(self), "wuu-code-mode-host")
				if runtime.GOOS == "windows" {
					candidate += ".exe"
				}
				if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
					executable = candidate
				}
			}
		}
		if executable != "" {
			codeModeSessionID := workspaceID
			if codeModeSessionID == "" {
				// SDK/CLI sessions can identify their workspace by path only.
				codeModeSessionID = workspaceStateDir
			}
			var heapLimit *uint64
			if cfg.CodeMode.MaxHeapSizeBytes > 0 {
				value := cfg.CodeMode.MaxHeapSizeBytes
				heapLimit = &value
			}
			service, serviceErr := codemode.NewService(codemode.ServiceConfig{
				Executable:     executable,
				SessionID:      codeModeSessionID,
				Limits:         codemode.CellLimits{MaxHeapSizeBytes: heapLimit},
				DefaultYieldMS: cfg.CodeMode.DefaultYieldMS,
				Stderr:         os.Stderr,
				Notify: func(ctx context.Context, callID, cellID, text string) error {
					fmt.Fprintf(os.Stderr, "code-mode notification (cell %s): %s\n", cellID, text)
					return nil
				},
				Life: codeModeLife,
			})
			if serviceErr == nil {
				codeModeService = service
				toolkit.SetCodeModeService(service)
				if cfg.CodeMode.InvocationMode() == config.CodeModeOnly {
					toolkit.SetCodeModeOnly(true)
				}
			}
		}
	}

	instructionFiles := discoverInstructions(rootDir, opts.HomeDir, cfg.Instructions)
	// Freeze the environment date once at session start. Every system-prompt
	// build in this session (base, worker, per-thread rebuild) reuses this
	// frozen value so the cached system prefix does not drift when threads
	// rebuild or the wall clock crosses midnight during a long session.
	sessionDate := wuucontext.Snapshot(rootDir).Date
	mainSurface := activeSurface(toolkit)
	if toolkit != nil {
		if err := toolkit.ValidateActiveToolSurfaceForProvider(providers.ToolSurfaceValidationTarget{
			ProviderKind: ruleProviderCfg.Type,
			ProviderName: resolvedName,
			Model:        toolModeModel,
		}); err != nil {
			return nil, err
		}
	}
	deferredToolCatalogPrompt, err := deferredToolCatalogPromptForToolkit(toolkit)
	if err != nil {
		return nil, err
	}
	mainSurface.DeferredToolCatalog = deferredToolCatalogPrompt
	baseSystemPromptResult := buildBaseSystemPromptResult(rootDir, sessionDate, config.DefaultSystemPrompt(), "", resolvedName, toolModeModel, mainSurface, instructionFiles, "", "", discoveredSkills)
	baseSystemPrompt, pluginPromptSections := assemblePluginSystemPrompt(baseSystemPromptResult.Content, systemPrompts)
	baseSystemPromptSections := append(agentPromptSections(baseSystemPromptResult.Sections), pluginPromptSections...)

	if toolkit != nil {
		if err := agentcontrol.EnsureSharedDir(workspaceStateDir); err != nil {
			return nil, fmt.Errorf("ensure shared dir: %w", err)
		}
	}

	var workerClient providers.StreamClient
	workerModelBudget := ResolveModelBudget(
		roleSelections.Worker.Model,
		roleSelections.Worker.RuleProviderConfig,
		cfg.Agent.MaxContextTokens,
	)
	if toolkit != nil {
		workerToolProviderName := roleSelections.Worker.RuleProvider
		workerToolModeModel := roleSelections.Worker.APIModel
		_, workerToolSearchEnabled, workerNativeDeferredDiscovery := resolveToolLoadingForProvider(cfg.Agent, roleSelections.Worker.RuleProviderConfig, workerToolModeModel, roleSelections.Worker.ProviderOptions)
		workerToolSurface := compiledSurfaceForProviderModel(workerToolProviderName, workerToolModeModel)
		// Fill the worker deferred-tool catalog the same way mainSurface is
		// filled above (consistency-repair #13: this was left empty while the
		// worker prompt taught catalog lookups through tool_search).
		workerDeferredCatalog, catErr := workerDeferredToolCatalogPromptForToolkit(toolkit, workerToolProviderName, workerToolModeModel, workerToolSearchEnabled)
		if catErr != nil {
			return nil, catErr
		}
		workerToolSurface.DeferredToolCatalog = workerDeferredCatalog
		workerBaseSystemPrompt := buildBaseSystemPromptContent(rootDir, sessionDate, config.WorkerSystemPrompt(), "", workerToolProviderName, workerToolModeModel, workerToolSurface, instructionFiles, "", "", discoveredSkills)
		var werr error
		workerClient, werr = providerfactory.BuildStreamClient(roleSelections.Worker.RuleProviderConfig, roleSelections.Worker.Provider)
		if werr != nil {
			if providerfactory.IsCredentialError(werr) {
				workerClient = &providers.UnavailableClient{Reason: werr.Error()}
				readinessIssues = append(readinessIssues, ReadinessIssue{Code: "credential_missing", Provider: roleSelections.Worker.Provider, Message: werr.Error()})
			} else {
				return nil, fmt.Errorf("build worker client: %w", werr)
			}
		}

		c, cerr := agentcontrol.New(agentcontrol.Config{
			Client:                         workerClient,
			ProviderName:                   roleSelections.Worker.Provider,
			DefaultModel:                   roleSelections.Worker.APIModel,
			DefaultEffort:                  roleSelections.Worker.LegacyEffort,
			DefaultOptions:                 modelvariant.CloneOptions(roleSelections.Worker.ProviderOptions),
			DefaultContextWindow:           workerModelBudget.ContextWindowTokens,
			DefaultMaxInputTokens:          workerModelBudget.InputLimitTokens,
			DefaultOutputReserveTokens:     workerModelBudget.OutputReserveTokens,
			DefaultCompactThresholdTokens:  workerModelBudget.CompactThresholdTokens,
			DefaultTemperature:             cfg.Agent.Temperature,
			DefaultCompactThresholdPct:     cfg.Agent.CompactThresholdPct,
			DefaultCompactKeepRecentTokens: cfg.Agent.CompactKeepRecentTokens,
			DefaultDisableAutoCompact:      cfg.Agent.DisableAutoCompact,
			ParentRepo:                     rootDir,
			WorktreeRoot:                   statepath.WorktreeRoot(workspaceStateDir),
			SessionID:                      "session-pending",
			HistoryDir:                     "",
			WorkerSysPrompt:                workerBaseSystemPrompt,
			WorkerPrompt: func(workerRoot string, wt agentcontrol.WorkerType, meta agentthread.Metadata, isolation agentcontrol.IsolationMode) (string, error) {
				return buildWorkerBasePrompt(workerRoot, sessionDate, "", workerToolProviderName, workerToolModeModel, workerToolSurface, instructionFiles, discoveredSkills), nil
			},
			WorkerFactory: func(workerRoot string, wt agentcontrol.WorkerType, meta agentthread.Metadata) (agent.ToolExecutor, error) {
				wkit, werr := toolkit.CloneForRoot(workerRoot)
				if werr != nil {
					return nil, werr
				}
				// Reset the inherited file-scope whitelist to the standard
				// workspace, registered-project, and temporary roots.
				wkit.SetFileScopeRoots(workspaces.BoundaryRoots(workerRoot, wuuHome))
				workerStateDir := workspaceStateDir
				if workerRoot != rootDir {
					if dir, err := statepath.WorkspaceDir(wuuHome, workerRoot); err == nil {
						workerStateDir = dir
					}
				}
				wkit.SetStateDir(workerStateDir)
				wkit.SetProcessManager(processMgr)
				wkit.SetSkills(discoveredSkills)
				wkit.SetAgentControl(agentControl)
				wkit.ConfigureSurfaceForProviderModel(workerToolProviderName, workerToolModeModel, false)
				wkit.SetToolSearchEnabled(workerToolSearchEnabled)
				wkit.SetNativeDeferredToolDiscovery(workerNativeDeferredDiscovery)
				wkit.SetAgentIdentity(meta.ID, meta.Path)
				applyWorkerToolFilter(wkit, wt)
				return wkit, nil
			},
			WorkerWakeAuthority: workerWakeAuthority(toolkit),
			OnSubagentStart: func(ctx context.Context, agentID string) error {
				_, err := hookDispatcher.Dispatch(ctx, hooks.SubagentStart, &hooks.Input{CWD: rootDir, AgentID: agentID})
				return err
			},
			OnSubagentStop: func(ctx context.Context, agentID string) error {
				_, err := hookDispatcher.Dispatch(ctx, hooks.SubagentStop, &hooks.Input{CWD: rootDir, AgentID: agentID})
				return err
			},
			ParticipantStore: sessionParticipantStore{sessDir: statepath.SessionsDir(wuuHome)},
			MaxParallel:      cfg.Agent.MaxParallelValue(),
			InferenceJournal: workspaceJournal,
			ToolLedgerFactory: func(ownerID string) (*toolledger.Ledger, error) {
				return toolledger.New(sessionDir, ownerID)
			},
		})
		if cerr == nil {
			agentControl = c
			toolkit.SetAgentControl(agentControl)
		}
	}

	modelBudget := ResolveModelBudget(
		providerCfg.Model,
		ruleProviderCfg,
		cfg.Agent.MaxContextTokens,
	)
	streamRunner := &agent.StreamRunner{
		Client:                      client,
		ProviderName:                resolvedName,
		ProviderObservationKey:      agent.BuildProviderObservationKey(resolvedName, ruleProviderCfg.BaseURL, ruleProviderCfg.Type, ruleProviderCfg.API, ruleProviderCfg.WireAPI, ruleProviderCfg.StreamTransport, agent.ProviderUsageNormalizationKey(ruleProviderCfg.CacheCreationInputTokensOmitted, ruleProviderCfg.InputTokensIncludeCacheRead)),
		Tools:                       toolExecutor,
		Model:                       providerCfg.Model,
		APIModel:                    modelcatalog.APIModel(ruleProviderCfg, providerCfg.Model),
		SystemPrompt:                baseSystemPrompt,
		SystemPromptSections:        baseSystemPromptSections,
		CompactionRegistry:          compactions,
		MaxSteps:                    cfg.Agent.MaxSteps,
		Temperature:                 cfg.Agent.Temperature,
		MediaInput:                  mediaInputPolicyFromCapabilities(mainRole.Capabilities),
		Effort:                      mainRole.LegacyEffort,
		Variant:                     mainRole.Variant,
		ProviderOptions:             modelvariant.CloneOptions(mainRole.ProviderOptions),
		NativeDeferredToolDiscovery: nativeDeferredDiscovery,
		ContextWindowOverride:       modelBudget.ContextWindowTokens,
		MaxInputTokens:              modelBudget.InputLimitTokens,
		OutputReserveTokens:         modelBudget.OutputReserveTokens,
		CompactThresholdTokens:      modelBudget.CompactThresholdTokens,
		CompactThresholdPct:         cfg.Agent.CompactThresholdPct,
		CompactKeepRecentTokens:     cfg.Agent.CompactKeepRecentTokens,
		DisableAutoCompact:          cfg.Agent.DisableAutoCompact,
		BeforeCompact: func(ctx context.Context, reason agent.CompactReason) error {
			_, err := hookDispatcher.Dispatch(ctx, hooks.PreCompact, &hooks.Input{CWD: rootDir, CompactReason: string(reason)})
			return err
		},
		AfterCompact: func(ctx context.Context, reason agent.CompactReason, compactErr error) error {
			input := &hooks.Input{CWD: rootDir, CompactReason: string(reason)}
			if compactErr != nil {
				input.Error = compactErr.Error()
			}
			_, err := hookDispatcher.Dispatch(ctx, hooks.PostCompact, input)
			return err
		},
		BeforeRequestContext:     RuntimeContextInjector(agentControl, agentthread.RootPath, toolkitContextBlockProvider(toolkit)),
		BeforeModelStep:          pluginPreStepInjector(pluginHost, resolvedName, providerCfg.Model, "", rootDir),
		BeforeRequest:            pluginRequestInterceptor(pluginHost, resolvedName, "", rootDir),
		InferenceOperationKind:   providers.InferenceOperationAgentRound,
		InferenceWorkloadProfile: providers.InferenceProfileInteractive,
		InferenceJournal:         workspaceJournal,
	}

	configLoadMode := opts.ConfigLoadMode
	// Keep direct runtime callers that pass a non-user ConfigPath pinned to
	// that file. Normal product paths set the mode explicitly when they trust
	// a complete project config rather than one standalone file.
	if configLoadMode == ConfigLoadNormal && strings.TrimSpace(opts.ConfigPath) != "" && !isUserConfigPath(opts.ConfigPath, opts.HomeDir) {
		configLoadMode = ConfigLoadFile
	}

	runtimeSession := &Session{
		ProviderName:                resolvedName,
		Model:                       providerCfg.Model,
		RootDir:                     rootDir,
		Host:                        host,
		WorkspaceID:                 workspaceID,
		StateDir:                    workspaceStateDir,
		ConfigPath:                  opts.ConfigPath,
		HomeDir:                     opts.HomeDir,
		ConfigLoadMode:              configLoadMode,
		SessionDir:                  sessionDir,
		StreamRunner:                streamRunner,
		TitleClient:                 titleClient,
		HookDispatcher:              hookDispatcher,
		Skills:                      discoveredSkills,
		Plugins:                     discoveredPlugins,
		ActivePlugins:               activePlugins,
		ExtensionSettings:           cfg.Extensions,
		PluginHost:                  pluginHost,
		UserQuestions:               userQuestions,
		PluginSessionRouter:         pluginTurnRouter,
		systemPrompts:               systemPrompts,
		InstructionFiles:            instructionFiles,
		AgentControl:                agentControl,
		ProcessManager:              processMgr,
		Toolkit:                     toolkit,
		CodeMode:                    codeModeService,
		codeModeStop:                codeModeStop,
		ActivityRegistry:            activityRegistry,
		WorkerClient:                workerClient,
		ModelRoles:                  roleSelections,
		ModelBudget:                 modelBudget,
		WorkerModelBudget:           workerModelBudget,
		BaseSystemPrompt:            baseSystemPrompt,
		BaseSystemPromptSections:    baseSystemPromptResult.Sections,
		SessionDate:                 sessionDate,
		WuuHome:                     wuuHome,
		SafeMode:                    safeMode,
		pluginEpoch:                 initialPluginGenerationEpoch,
		Permissions:                 permissions,
		PermissionModeExplicit:      opts.PermissionModeExplicit,
		maxParallel:                 cfg.Agent.MaxParallelValue(),
		ExperimentalCoordinatorMode: cfg.Agent.ExperimentalCoordinatorMode,
		ToolLoadingPreference:       toolLoadingPreference,
		ToolLoadingMode:             toolLoadingMode,
		ToolSearchEnabled:           toolSearchEnabled,
		NativeDeferredToolDiscovery: nativeDeferredDiscovery,
		DeferredToolCatalogPrompt:   deferredToolCatalogPrompt,
		ReadinessIssues:             readinessIssues,
		InferenceJournalRuntime:     journalRuntime,
	}
	// The built-in wuu engine is the native StreamRunner loop. Registering it
	// here makes the seam explicit: thread runtimes are always created by an
	// engine factory, starting with this one.
	runtimeSession.engines = agentengine.NewRegistry()
	if err := runtimeSession.engines.Register(&WuuEngine{session: runtimeSession}); err != nil {
		journalOwned = false
		_ = journalRuntime.Close()
		return nil, fmt.Errorf("register built-in agent engine: %w", err)
	}
	// The codex engine hosts the codex CLI app-server as an external agent.
	// Settings can disable it or override the binary path; auto mode
	// (unset) enables it when the CLI is found, and a missing binary fails
	// with a clear error at first use.
	codexBinary, codexResolveErr := codexengine.ResolveBinary()
	codexEnabled := codexResolveErr == nil
	if engineCfg := cfg.Engines; engineCfg != nil {
		if strings.TrimSpace(engineCfg.DefaultEngine) != "" {
			runtimeSession.DefaultEngine = agentengine.NormalizeEngineID(engineCfg.DefaultEngine)
		}
		if codexCfg := engineCfg.Codex; codexCfg != nil {
			enabled, explicit := codexCfg.EngineEnabled()
			if explicit {
				codexEnabled = enabled
			}
			if path := strings.TrimSpace(codexCfg.BinaryPath); path != "" {
				codexBinary = path
				if !explicit {
					codexEnabled = true
				}
			}
		}
	}
	if codexEnabled {
		codexHost := codexengine.NewHost(codexBinary, rootDir)
		if err := runtimeSession.engines.Register(codexengine.NewEngine(codexHost)); err == nil {
			runtimeSession.codexHost = codexHost
		}
	}
	// The claude engine follows the same settings shape: auto when the CLI
	// is found, explicit disable or binary override from settings.
	claudeBinary, claudeResolveErr := claudeengine.ResolveBinary()
	claudeEnabled := claudeResolveErr == nil
	if engineCfg := cfg.Engines; engineCfg != nil && engineCfg.Claude != nil {
		enabled, explicit := engineCfg.Claude.EngineEnabled()
		if explicit {
			claudeEnabled = enabled
		}
		if path := strings.TrimSpace(engineCfg.Claude.BinaryPath); path != "" {
			claudeBinary = path
			if !explicit {
				claudeEnabled = true
			}
		}
	}
	if claudeEnabled {
		runtimeSession.engines.Register(claudeengine.NewEngine(claudeBinary, rootDir))
	}
	if runtimeSession.DefaultEngine != "" && !runtimeSession.EngineAvailable(runtimeSession.DefaultEngine) {
		runtimeSession.DefaultEngine = agentengine.EngineWuu
	}
	initialHooks := hooks.NewDispatcher(nil)
	initialHooks.Replace(hookDispatcher)
	runtimeSession.pluginGeneration = &PluginGeneration{
		settings:      cfg,
		plugins:       append([]pluginpkg.Plugin(nil), discoveredPlugins...),
		active:        append([]pluginpkg.Plugin(nil), activePlugins...),
		host:          pluginHost,
		hooks:         initialHooks,
		skills:        append([]skills.Skill(nil), discoveredSkills...),
		mcpBinding:    mcpActivityBindingsFromPlugins(activePlugins),
		systemPrompts: systemPrompts,
		compactions:   compactions,
	}
	if pluginKernel != nil {
		runtimeSession.pluginGeneration.driverGateways = pluginKernel.driverGateways
	}
	// Bind the session-selected loop driver: an installed profile resolves to
	// a remote driver behind the registry; a missing one fails closed.
	if driver := resolveLoopDriver(opts.DriverProfile, pluginHost, runtimeSession.currentDriverGatewayTable); driver != nil {
		streamRunner.LoopDriver = driver
		runtimeSession.DriverProfile = strings.TrimSpace(opts.DriverProfile)
	}
	if toolkit != nil {
		runtimeSession.pluginGeneration.mcp = toolkit.MCPManager()
	}
	// The legacy/root control remains dormant until SetSessionID binds its real
	// artifact directories. Per-thread controls created by NewThreadRuntime are
	// likewise started only after app-server installs their terminal finalizer.
	journalOwned = false
	return runtimeSession, nil
}

// InitialPluginGenerationEpoch is the generation observed while NewSession
// held a shared execution lease across plugin discovery and process startup.
func (s *Session) InitialPluginGenerationEpoch() uint64 {
	if s == nil {
		return 0
	}
	return s.pluginEpoch
}

func inferenceJournalWorkspaceScope(workspaceID, rootDir string) string {
	if id := strings.TrimSpace(workspaceID); id != "" {
		return "workspace-id:" + id
	}
	root := cleanRuntimeRoot(rootDir)
	sum := sha256.Sum256([]byte(root))
	return "workspace-path:" + hex.EncodeToString(sum[:16])
}

func (s *Session) InferenceJournalForOwner(ownerID string) providers.InferenceJournal {
	if s == nil || s.InferenceJournalRuntime == nil {
		return nil
	}
	return s.InferenceJournalRuntime.ForOwner(ownerID)
}

// LoadEffectiveConfig reloads the same source model that created the session.
// Normal sessions rebuild the user base plus restricted project overlays,
// explicit files remain pinned to one file, and explicitly trusted project
// configs continue to include their settings layers.
func (s *Session) LoadEffectiveConfig() (config.Config, string, error) {
	if s == nil {
		return config.Config{}, "", errors.New("runtime session is nil")
	}
	switch s.ConfigLoadMode {
	case ConfigLoadNormal:
		return config.LoadFrom(s.RootDir, s.HomeDir)
	case ConfigLoadFile:
		return config.LoadPath(s.ConfigPath)
	case ConfigLoadProject:
		return config.LoadProjectConfig(s.RootDir)
	default:
		return config.Config{}, "", fmt.Errorf("unknown config load mode %d", s.ConfigLoadMode)
	}
}

func isUserConfigPath(path, home string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	want := []string{statepath.LegacyConfigPath(home)}
	if canonical, err := statepath.ConfigPath(home); err == nil {
		want = append(want, canonical)
	}
	cleanPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, candidate := range want {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		cleanCandidate, err := filepath.Abs(candidate)
		if err == nil && cleanPath == cleanCandidate {
			return true
		}
	}
	return false
}

func resolveToolLoadingForProvider(agentCfg config.AgentConfig, providerCfg config.ProviderConfig, model string, providerOptions map[string]any) (config.ToolLoadingMode, bool, bool) {
	return resolveToolLoadingModeForProvider(agentCfg.ToolLoadingPreference(), providerCfg, model, providerOptions)
}

func resolveToolLoadingModeForProvider(mode config.ToolLoadingMode, providerCfg config.ProviderConfig, model string, providerOptions map[string]any) (config.ToolLoadingMode, bool, bool) {
	switch mode {
	case config.ToolLoadingFlat:
		return mode, false, false
	case config.ToolLoadingNative:
		if providerfactory.SupportsNativeToolDiscovery(providerCfg, model, providerOptions) {
			return mode, true, true
		}
		// Explicit native on a path that cannot carry the provider's own
		// deferred-discovery protocol degrades to flat rather than silently
		// selecting a different loading strategy. Say so: the user asked for
		// deferred tools and is not getting them.
		warnUnsupportedNativeToolLoadingOnce(providerCfg, model)
		return config.ToolLoadingFlat, false, false
	default:
		if providerfactory.SupportsNativeToolDiscoveryByDefault(providerCfg, model, providerOptions) {
			return config.ToolLoadingNative, true, true
		}
		// Everything else is flat. Paying the fixed schema cost once keeps the
		// provider prompt-cache prefix stable, which progressive loading could
		// not do: appending to the top-level tools array invalidated the cached
		// prefix past the insertion point on every load.
		return config.ToolLoadingFlat, false, false
	}
}

// ReconfigureToolLoading reapplies every mutable tool-loading field after the
// workspace provider or model changes. Provider switching updates a live
// Session in place, so leaving any of these fields behind can send one
// provider's native discovery protocol to a different compatible endpoint.
func (s *Session) ReconfigureToolLoading(agentCfg config.AgentConfig, providerCfg config.ProviderConfig, model string, providerOptions map[string]any) error {
	if s == nil {
		return nil
	}
	preference := agentCfg.ToolLoadingPreference()
	mode, toolSearchEnabled, nativeDeferredDiscovery := resolveToolLoadingModeForProvider(preference, providerCfg, model, providerOptions)
	s.ToolLoadingPreference = preference
	s.ToolLoadingMode = mode
	s.ToolSearchEnabled = toolSearchEnabled
	s.NativeDeferredToolDiscovery = nativeDeferredDiscovery
	if s.Toolkit != nil {
		s.Toolkit.SetToolSearchEnabled(toolSearchEnabled)
		s.Toolkit.SetNativeDeferredToolDiscovery(nativeDeferredDiscovery)
		catalog, err := deferredToolCatalogPromptForToolkit(s.Toolkit)
		if err != nil {
			return err
		}
		s.DeferredToolCatalogPrompt = catalog
	} else {
		s.DeferredToolCatalogPrompt = ""
	}
	if s.StreamRunner != nil {
		s.StreamRunner.NativeDeferredToolDiscovery = nativeDeferredDiscovery
	}
	return nil
}

// PrewarmThreadProvider moves provider-owned auth and transport setup ahead of
// the first turn without constructing the thread's Toolkit, AgentControl, tool
// ledger, or worker recovery state. It only runs when runtime construction will
// reuse the workspace StreamRunner client; shadow provider/model selections own
// different clients and must warm themselves on their normal request path.
func (s *Session) PrewarmThreadProvider(ctx context.Context, sessionID string, engineID agentengine.EngineID, selected ThreadModelSelection) error {
	if s == nil || agentengine.NormalizeEngineID(string(engineID)) != agentengine.EngineWuu || s.StreamRunner == nil || s.StreamRunner.Client == nil {
		return nil
	}
	providerName := strings.TrimSpace(selected.Provider)
	model := strings.TrimSpace(selected.Model)
	permissionMode := strings.TrimSpace(selected.PermissionMode)
	if permissionMode == "" || s.PermissionModeExplicit {
		permissionMode = s.Permissions.Mode
	}
	usesSharedClient := providerName == "" || model == "" ||
		(providerName == s.ProviderName &&
			model == s.Model &&
			strings.TrimSpace(selected.Variant) == strings.TrimSpace(s.StreamRunner.Variant) &&
			strings.TrimSpace(selected.Effort) == strings.TrimSpace(s.StreamRunner.Effort) &&
			config.NormalizePermissionMode(permissionMode) == config.NormalizePermissionMode(s.Permissions.Mode))
	if !usesSharedClient {
		return nil
	}
	prewarmer, ok := s.StreamRunner.Client.(providers.SessionPrewarmer)
	if !ok {
		return nil
	}
	return prewarmer.PrewarmSession(ctx, sessionID)
}

// NewThreadRuntime creates a per-conversation execution runtime from the
// shared workspace runtime. It intentionally does not mutate Session.Toolkit or
// Session.AgentControl; those remain the legacy single-session runtime used by
// CLI and older call sites.
func (s *Session) NewThreadRuntime(sessionID string) (*ThreadRuntime, error) {
	return s.NewThreadRuntimeForRoot(sessionID, s.RootDir)
}

// ErrThreadProviderUnavailable marks a thread-runtime build that failed
// because the thread's pinned provider is no longer configured. Callers can
// self-heal by rebuilding on the workspace defaults instead of failing every
// turn on the dead pin.
var ErrThreadProviderUnavailable = errors.New("thread provider unavailable")

// NewThreadRuntimeForRootModel creates a thread runtime from a conversation's
// persisted model selection without mutating the workspace-wide defaults.
func (s *Session) NewThreadRuntimeForRootModel(sessionID, rootDir string, selected ThreadModelSelection) (*ThreadRuntime, error) {
	if s == nil {
		return nil, fmt.Errorf("runtime session is required")
	}
	providerName := strings.TrimSpace(selected.Provider)
	model := strings.TrimSpace(selected.Model)
	requested := ThreadModelSelection{
		Provider:       providerName,
		Model:          model,
		Variant:        strings.TrimSpace(selected.Variant),
		Effort:         strings.TrimSpace(selected.Effort),
		PermissionMode: strings.TrimSpace(selected.PermissionMode),
	}
	permissionMode := requested.PermissionMode
	// An explicit process-scoped override (exec --permission-mode) wins over
	// the thread's pinned mode; it also keeps the fast path viable when only
	// the pinned mode differs, so the override never forces a shadow rebuild.
	if permissionMode == "" || s.PermissionModeExplicit {
		permissionMode = s.Permissions.Mode
	}
	permissions := config.ResolvedPermissions{Mode: config.NormalizePermissionMode(permissionMode)}
	requested.PermissionMode = permissions.Mode
	currentVariant := ""
	currentEffort := ""
	if s.StreamRunner != nil {
		currentVariant = strings.TrimSpace(s.StreamRunner.Variant)
		currentEffort = strings.TrimSpace(s.StreamRunner.Effort)
	}
	if providerName == "" || model == "" || (providerName == s.ProviderName && model == s.Model && requested.Variant == currentVariant && requested.Effort == currentEffort) {
		// Permission changes do not require rebuilding an unchanged model client.
		shadow := s.cloneForThreadModel()
		shadow.Permissions = permissions
		threadRuntime, err := shadow.NewThreadRuntimeForRoot(sessionID, rootDir)
		if err != nil {
			return nil, err
		}
		threadRuntime.Selection = requested
		return threadRuntime, nil
	}
	cfg, _, err := s.LoadEffectiveConfig()
	if err != nil {
		return nil, err
	}
	providerCfg, resolvedName, err := cfg.ResolveProvider(providerName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrThreadProviderUnavailable, err)
	}
	ruleProviderName, ruleProviderCfg := modelcatalog.EnrichProvider(resolvedName, providerCfg, model)
	variant := strings.TrimSpace(selected.Variant)
	effort := strings.TrimSpace(selected.Effort)
	selection := modelvariant.ResolveForProvider(ruleProviderName, ruleProviderCfg, model, variant, effort)
	client, err := providerfactory.BuildStreamClient(ruleProviderCfg, resolvedName)
	if err != nil {
		return nil, fmt.Errorf("build thread model client: %w", err)
	}
	roles, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{
		ProviderName: resolvedName, ProviderConfig: providerCfg, Model: model,
		Effort: selection.LegacyEffort, Variant: selection.Variant,
	})
	if err != nil {
		return nil, err
	}

	shadow := s.cloneForThreadModel()
	shadow.Permissions = permissions
	shadow.ProviderName = resolvedName
	shadow.Model = model
	shadow.ModelRoles = roles
	shadow.ModelBudget = ResolveModelBudget(model, ruleProviderCfg, cfg.Agent.MaxContextTokens)
	shadow.WorkerModelBudget = ResolveModelBudget(roles.Worker.Model, roles.Worker.RuleProviderConfig, cfg.Agent.MaxContextTokens)
	shadow.StreamRunner = cloneStreamRunnerForThread(s.StreamRunner, nil)
	if shadow.StreamRunner == nil {
		return nil, fmt.Errorf("stream runner is required")
	}
	apiModel := modelcatalog.APIModel(ruleProviderCfg, model)
	shadow.StreamRunner.Client = client
	shadow.StreamRunner.ProviderName = resolvedName
	shadow.StreamRunner.ProviderObservationKey = agent.BuildProviderObservationKey(resolvedName, ruleProviderCfg.BaseURL, ruleProviderCfg.Type, ruleProviderCfg.API, ruleProviderCfg.WireAPI, ruleProviderCfg.StreamTransport, agent.ProviderUsageNormalizationKey(ruleProviderCfg.CacheCreationInputTokensOmitted, ruleProviderCfg.InputTokensIncludeCacheRead))
	shadow.StreamRunner.Model = model
	shadow.StreamRunner.APIModel = apiModel
	// The cloned runner inherits the workspace model's media admission
	// policy. The thread pins a different model, so re-derive the policy
	// from the thread model's own capabilities; otherwise a text-only model
	// keeps the base model's policy and unsupported images reach the wire.
	shadow.StreamRunner.MediaInput = mediaInputPolicyFromCapabilities(roles.Main.Capabilities)
	shadow.StreamRunner.Effort = selection.LegacyEffort
	shadow.StreamRunner.Variant = selection.Variant
	shadow.StreamRunner.ProviderOptions = modelvariant.CloneOptions(selection.ProviderOptions)
	shadow.StreamRunner.ContextWindowOverride = shadow.ModelBudget.ContextWindowTokens
	shadow.StreamRunner.MaxInputTokens = shadow.ModelBudget.InputLimitTokens
	shadow.StreamRunner.OutputReserveTokens = shadow.ModelBudget.OutputReserveTokens
	shadow.StreamRunner.CompactThresholdTokens = shadow.ModelBudget.CompactThresholdTokens
	shadow.ToolLoadingMode, shadow.ToolSearchEnabled, shadow.NativeDeferredToolDiscovery = resolveToolLoadingForProvider(cfg.Agent, ruleProviderCfg, apiModel, selection.ProviderOptions)
	shadow.StreamRunner.NativeDeferredToolDiscovery = shadow.NativeDeferredToolDiscovery

	threadRoot := strings.TrimSpace(rootDir)
	if threadRoot == "" {
		threadRoot = s.RootDir
	}
	if s.Toolkit != nil {
		kit, cloneErr := s.Toolkit.CloneForRoot(threadRoot)
		if cloneErr != nil {
			return nil, cloneErr
		}
		kit.ConfigureSurfaceForProviderModel(ruleProviderName, apiModel, true)
		kit.SetToolSearchEnabled(shadow.ToolSearchEnabled)
		kit.SetNativeDeferredToolDiscovery(shadow.NativeDeferredToolDiscovery)
		shadow.Toolkit = kit
		catalog, catalogErr := deferredToolCatalogPromptForToolkit(kit)
		if catalogErr != nil {
			return nil, catalogErr
		}
		shadow.DeferredToolCatalogPrompt = catalog
	}
	workerClient, err := providerfactory.BuildStreamClient(roles.Worker.RuleProviderConfig, roles.Worker.Provider)
	if err != nil {
		return nil, fmt.Errorf("build thread worker client: %w", err)
	}
	shadow.WorkerClient = workerClient

	promptResult := buildBaseSystemPromptResult(
		threadRoot, shadow.SessionDate, config.DefaultSystemPrompt(), "",
		resolvedName, apiModel, activeSurfaceWithDeferredToolCatalog(shadow.Toolkit, shadow.DeferredToolCatalogPrompt),
		shadow.InstructionFiles, "", "", shadow.Skills,
	)
	shadow.BaseSystemPrompt = promptResult.Content
	shadow.BaseSystemPromptSections = promptResult.Sections
	shadow.StreamRunner.UpdateSystemPromptWithSections(promptResult.Content, agentPromptSections(promptResult.Sections))
	threadRuntime, err := shadow.NewThreadRuntimeForRoot(sessionID, threadRoot)
	if err != nil {
		return nil, err
	}
	threadRuntime.Selection = requested
	return threadRuntime, nil
}

// ConfigureNamedAgentThreadRuntime adds one collaboration named agent's
// durable identity prompt and notebook scope to a thread runtime.
func (s *Session) ConfigureNamedAgentThreadRuntime(threadRuntime *ThreadRuntime, rootDir, memoryDir, orientation string) error {
	if s == nil || threadRuntime == nil || threadRuntime.StreamRunner == nil {
		return errors.New("named agent thread runtime is required")
	}
	if threadRuntime.ExecutionProfile != CollaborationRuntimeVersion {
		return errors.New("named agent requires the collaboration execution profile")
	}
	rootDir = strings.TrimSpace(rootDir)
	memoryDir = strings.TrimSpace(memoryDir)
	if rootDir == "" || memoryDir == "" {
		return errors.New("named agent root and memory directory are required")
	}
	if err := memdir.EnsureDir(memoryDir); err != nil {
		return fmt.Errorf("ensure named agent memory: %w", err)
	}
	teaching := memdir.IdentityTeaching(memoryDir)
	index := ""
	toolkit := threadRuntime.Toolkit
	if toolkit != nil && toolkit.IsRoomAgent() {
		teaching = "Use chat_memory with scope=room for durable shared knowledge. Keep MEMORY.md as a compact index; read relevant topics as needed."
	}
	if toolkit != nil {
		toolkit.SetFileScopeRoots(workspaces.BoundaryRoots(rootDir, s.WuuHome, memoryDir))
	}
	catalog := ""
	if toolkit != nil {
		var err error
		catalog, err = deferredToolCatalogPromptForToolkit(toolkit)
		if err != nil {
			return err
		}
	}
	runner := threadRuntime.StreamRunner
	runner.Tools = toolkit
	runner.BeforeRequestContext = RuntimeContextInjector(
		threadRuntime.AgentControl,
		rootDir,
		toolkitContextBlockProvider(toolkit),
		namedAgentWorkspaceContextProvider(s.WuuHome, rootDir, memoryDir, toolkit),
		namedAgentNotebookContextProvider(memoryDir),
	)
	userPrompt := strings.TrimSpace(orientation)
	promptResult := buildBaseSystemPromptResult(
		rootDir, s.SessionDate, config.DefaultSystemPrompt(), userPrompt,
		runner.ProviderName, runner.APIModel, activeSurfaceWithDeferredToolCatalog(toolkit, catalog),
		nil, teaching, index, nil,
	)
	runner.UpdateSystemPromptWithSections(promptResult.Content, agentPromptSections(promptResult.Sections))
	return nil
}

func (s *Session) NewNamedAgentThreadRuntime(sessionID, rootDir, memoryDir, orientation string, selected ThreadModelSelection) (*ThreadRuntime, error) {
	if s == nil {
		return nil, errors.New("runtime session is required")
	}
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return nil, errors.New("named agent root is required")
	}
	base, err := s.newCollaborationSession(rootDir, orientation, selected)
	if err != nil {
		return nil, err
	}
	threadRuntime, err := base.NewThreadRuntimeForRoot(sessionID, rootDir)
	if err != nil {
		return nil, err
	}
	threadRuntime.ExecutionProfile = CollaborationRuntimeVersion
	if err := base.ConfigureNamedAgentThreadRuntime(threadRuntime, rootDir, memoryDir, orientation); err != nil {
		return nil, err
	}
	return threadRuntime, nil
}

// NewThreadRuntimeForRoot creates a per-conversation execution runtime whose
// tools are rooted at rootDir while durable artifacts stay in the parent
// workspace state directory.
func (s *Session) NewThreadRuntimeForRoot(sessionID, rootDir string) (*ThreadRuntime, error) {
	if s == nil {
		return nil, fmt.Errorf("runtime session is required")
	}
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if s.StreamRunner == nil {
		return nil, fmt.Errorf("stream runner is required")
	}
	threadRoot := strings.TrimSpace(rootDir)
	if threadRoot == "" {
		threadRoot = s.RootDir
	}
	if abs, err := filepath.Abs(threadRoot); err == nil {
		threadRoot = abs
	}
	if ev, err := filepath.EvalSymlinks(threadRoot); err == nil {
		threadRoot = ev
	}

	stateDir := strings.TrimSpace(s.StateDir)
	if stateDir == "" {
		home, err := statepath.Home("")
		if err != nil {
			return nil, fmt.Errorf("resolve wuu home: %w", err)
		}
		stateDir, err = resolveWorkspaceStateDir(home, s.WorkspaceID, s.RootDir)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace state directory: %w", err)
		}
	}
	artifactDir := statepath.SessionArtifactDir(stateDir, id)
	// The embedded-browser tab registry is durable per-thread state, created with
	// the thread runtime and reclaimed with the thread's artifact
	// directory on delete. Recovery after a core restart is driven by the desktop
	// host's tab_not_found signal (the tool rebuilds by URL), and by the tabs-list
	// reconciliation against the live host set.
	browserTabs := tools.NewBrowserTabStore(statepath.ThreadBrowserTabsPath(stateDir, id))
	wuuHome := strings.TrimSpace(s.WuuHome)
	if wuuHome == "" {
		if home, err := statepath.Home(""); err == nil {
			wuuHome = home
		}
	}

	var (
		kit          *tools.Toolkit
		agentControl *agentcontrol.AgentControl
		toolExecutor = s.StreamRunner.Tools
	)
	threadProcessManager, err := s.processManagerForThread(threadRoot, stateDir)
	if err != nil {
		return nil, fmt.Errorf("thread process manager: %w", err)
	}

	// Prepare every fallible thread-local dependency before AgentControl. The
	// control restores queue metadata here but remains dormant until app-server
	// installs its model resolver and reliable terminal finalizer, then calls
	// StartQueuedWork.
	if s.Toolkit != nil {
		var err error
		kit, err = s.Toolkit.CloneForRoot(threadRoot)
		if err != nil {
			return nil, err
		}
		kit.SetStateDir(stateDir)
		kit.SetProcessManager(threadProcessManager)
		kit.SetSkills(s.Skills)
		ConfigureToolkitPermissions(kit, s.Permissions)
		kit.SetSessionID(id)
		kit.SetSessionDir(artifactDir)
		kit.SetSessionsDir(s.SessionDir)
		kit.SetBrowserTabs(browserTabs)
		kit.SetImageInputSupported(s.ModelRoles.Main.Capabilities.ImageInput)
		kit.SetAgentIdentity(id, agentthread.RootPath)
		fileScopeExtras := []string{artifactDir}
		// Rebase the file-scope whitelist on the thread root (the clone
		// inherited the parent session's roots): thread root + registered
		// workspaces + temp + artifact extras.
		kit.SetFileScopeRoots(workspaces.BoundaryRoots(kit.RootDir(), wuuHome, fileScopeExtras...))
	}

	toolLedger, err := toolledger.New(s.SessionDir, id)
	if err != nil {
		return nil, fmt.Errorf("open tool ledger: %w", err)
	}

	if s.Toolkit != nil {
		workerClient := s.WorkerClient
		workerClientProvider := strings.TrimSpace(s.ModelRoles.Worker.Provider)
		if workerClient == nil {
			// Falling back to the main-thread client means the worker's
			// provider identity is the main provider, not the worker role's.
			workerClient = s.StreamRunner.Client
			workerClientProvider = strings.TrimSpace(s.ProviderName)
		}
		if workerClient != nil {
			var control *agentcontrol.AgentControl
			workerModel := s.Model
			if roleModel := strings.TrimSpace(s.ModelRoles.Worker.APIModel); roleModel != "" {
				workerModel = roleModel
			}
			workerModelBudget := s.WorkerModelBudget
			workerToolProviderName := s.ModelRoles.Worker.RuleProvider
			workerToolModeModel := workerModel
			_, workerToolSearchEnabled, workerNativeDeferredDiscovery := resolveToolLoadingModeForProvider(s.ToolLoadingPreference, s.ModelRoles.Worker.RuleProviderConfig, workerToolModeModel, s.ModelRoles.Worker.ProviderOptions)
			workerToolSurface := compiledSurfaceForProviderModel(workerToolProviderName, workerToolModeModel)
			// Fill the worker deferred-tool catalog like the session build
			// path does (consistency-repair #13).
			workerDeferredCatalog, catErr := workerDeferredToolCatalogPromptForToolkit(kit, workerToolProviderName, workerToolModeModel, workerToolSearchEnabled)
			if catErr != nil {
				return nil, catErr
			}
			workerToolSurface.DeferredToolCatalog = workerDeferredCatalog
			workerBaseSystemPrompt := buildWorkerBasePrompt(
				threadRoot,
				s.SessionDate,
				s.workerOrientation,
				workerToolProviderName,
				workerToolModeModel,
				workerToolSurface,
				s.InstructionFiles,
				s.Skills,
			)
			control, controlErr := agentcontrol.New(agentcontrol.Config{
				Client:                         workerClient,
				ProviderName:                   workerClientProvider,
				DefaultModel:                   workerModel,
				DefaultEffort:                  s.ModelRoles.Worker.LegacyEffort,
				DefaultOptions:                 modelvariant.CloneOptions(s.ModelRoles.Worker.ProviderOptions),
				DefaultContextWindow:           workerModelBudget.ContextWindowTokens,
				DefaultMaxInputTokens:          workerModelBudget.InputLimitTokens,
				DefaultOutputReserveTokens:     workerModelBudget.OutputReserveTokens,
				DefaultCompactThresholdTokens:  workerModelBudget.CompactThresholdTokens,
				DefaultCompactThresholdPct:     s.StreamRunner.CompactThresholdPct,
				DefaultCompactKeepRecentTokens: s.StreamRunner.CompactKeepRecentTokens,
				DefaultDisableAutoCompact:      s.StreamRunner.DisableAutoCompact,
				ParentRepo:                     threadRoot,
				WorktreeRoot:                   statepath.WorktreeRoot(stateDir),
				SessionID:                      id,
				HistoryDir:                     filepath.Join(artifactDir, "workers"),
				ThreadDir:                      filepath.Join(artifactDir, "threads"),
				HarnessDir:                     filepath.Join(artifactDir, "harness"),
				WorkerSysPrompt:                workerBaseSystemPrompt,
				WorkerPrompt: func(workerRoot string, wt agentcontrol.WorkerType, meta agentthread.Metadata, isolation agentcontrol.IsolationMode) (string, error) {
					return buildWorkerBasePrompt(workerRoot, s.SessionDate, s.workerOrientation, workerToolProviderName, workerToolModeModel, workerToolSurface, s.InstructionFiles, s.Skills), nil
				},
				WorkerFactory: func(workerRoot string, wt agentcontrol.WorkerType, meta agentthread.Metadata) (agent.ToolExecutor, error) {
					workerKit, err := kit.CloneForRoot(workerRoot)
					if err != nil {
						return nil, err
					}
					// Reset the inherited file-scope whitelist: a worker gets
					// the standard workspace boundary roots.
					workerKit.SetFileScopeRoots(workspaces.BoundaryRoots(workerRoot, wuuHome))
					workerKit.ConfigureSurfaceForProviderModel(workerToolProviderName, workerToolModeModel, false)
					workerStateDir := stateDir
					if !sameRuntimeRoot(workerRoot, threadRoot) {
						if home, err := statepath.Home(""); err == nil {
							if dir, err := statepath.WorkspaceDir(home, workerRoot); err == nil {
								workerStateDir = dir
							}
						}
					}
					workerKit.SetStateDir(workerStateDir)
					workerKit.SetProcessManager(threadProcessManager)
					workerKit.SetSkills(s.Skills)
					workerKit.SetAgentControl(control)
					workerKit.SetSessionID(id)
					workerKit.SetSessionDir(artifactDir)
					workerKit.SetToolSearchEnabled(workerToolSearchEnabled)
					workerKit.SetNativeDeferredToolDiscovery(workerNativeDeferredDiscovery)
					workerKit.SetAgentIdentity(meta.ID, meta.Path)
					applyWorkerToolFilter(workerKit, wt)
					return workerKit, nil
				},
				WorkerWakeAuthority: workerWakeAuthority(kit),
				OnSubagentStart: func(ctx context.Context, agentID string) error {
					_, err := s.HookDispatcher.Dispatch(ctx, hooks.SubagentStart, &hooks.Input{SessionID: id, CWD: threadRoot, AgentID: agentID})
					return err
				},
				OnSubagentStop: func(ctx context.Context, agentID string) error {
					_, err := s.HookDispatcher.Dispatch(ctx, hooks.SubagentStop, &hooks.Input{SessionID: id, CWD: threadRoot, AgentID: agentID})
					return err
				},
				ParticipantStore: sessionParticipantStore{sessDir: statepath.SessionsDir(wuuHome)},
				MaxParallel:      s.MaxParallel(),
				InferenceJournal: s.InferenceJournalForOwner(id),
				ToolLedgerFactory: func(ownerID string) (*toolledger.Ledger, error) {
					return toolledger.New(s.SessionDir, ownerID)
				},
			})
			if controlErr != nil {
				return nil, fmt.Errorf("create thread agent control: %w", controlErr)
			}
			agentControl = control
		}
		kit.SetAgentControl(agentControl)
		toolExecutor = newPluginAwareToolExecutor(kit, s.PluginHost, s.HookDispatcher, id, "", threadRoot)
	}

	runner := cloneStreamRunnerForThread(s.StreamRunner, toolExecutor)
	runner.ToolLedger = toolLedger
	runner.SystemPrompt, runner.SystemPromptSections = systemPromptForThreadRoot(runner.SystemPrompt, runner.SystemPromptSections, threadRoot, s.SessionDate)
	runner.PromptCacheKey = strings.TrimSpace(id)
	runner.InferenceJournal = s.InferenceJournalForOwner(id)
	runner.DriverCheckpointStore = sessionDriverCheckpointStore{sessDir: s.SessionDir, sessionID: id}
	runner.CompactionNoteStore = sessionCompactionNoteStore{sessDir: s.SessionDir, sessionID: id}
	if kit != nil {
		kit.SetContextWindowToolsEnabled(runner.ContextWindowsAvailable())
	}
	// Exact model-input receipts have no production consumer and duplicate the
	// cumulative conversation on every step. Keep the optional runner contract
	// available for explicit diagnostics, but do not persist receipts by default.
	runner.BeforeRequestContext = RuntimeContextInjector(agentControl, agentthread.RootPath, toolkitContextBlockProvider(kit))
	runner.BeforeModelStep = pluginPreStepInjector(s.PluginHost, s.ProviderName, s.Model, id, threadRoot)
	runner.BeforeRequest = pluginRequestInterceptor(s.PluginHost, s.ProviderName, id, threadRoot)
	return &ThreadRuntime{
		StreamRunner:      runner,
		Toolkit:           kit,
		AgentControl:      agentControl,
		ProcessManager:    threadProcessManager,
		ActivityRegistry:  s.ActivityRegistry,
		ModelBudget:       s.ModelBudget,
		WorkerModelBudget: s.WorkerModelBudget,
		// Direct callers get the session's own identity as the stamp;
		// NewThreadRuntimeForRootModel overwrites it with the thread's
		// requested selection so reuse comparisons stay in thread terms.
		Selection: ThreadModelSelection{
			Provider:       s.ProviderName,
			Model:          s.Model,
			Variant:        strings.TrimSpace(runner.Variant),
			Effort:         strings.TrimSpace(runner.Effort),
			PermissionMode: config.NormalizePermissionMode(s.Permissions.Mode),
		},
	}, nil
}

type sessionDriverCheckpointStore struct {
	sessDir   string
	sessionID string
}

type sessionCompactionNoteStore struct {
	sessDir   string
	sessionID string
}

func (store sessionCompactionNoteStore) RecordContextNoteMetric(ctx context.Context, metric agent.ContextNoteMetric) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(metric)
	if err != nil {
		return err
	}
	// Separate from token_usage: turn totals already include drained background
	// usage. This audit record also survives a fork finishing after the last turn.
	return session.AppendHistoryRecord(store.sessDir, store.sessionID, session.HistoryRecord{
		Role: "meta", Name: "context_note_attempt", Content: string(data),
	})
}

func (store sessionCompactionNoteStore) LoadCompactionNote(ctx context.Context, providerKey string) (agent.CompactionNote, bool, error) {
	if err := ctx.Err(); err != nil {
		return agent.CompactionNote{}, false, err
	}
	record, ok, err := session.LoadCompactionNote(store.sessDir, store.sessionID, providerKey)
	if errors.Is(err, session.ErrSessionNotFound) {
		return agent.CompactionNote{}, false, nil
	}
	if err != nil || !ok {
		return agent.CompactionNote{}, ok, err
	}
	return agent.CompactionNote{
		Markdown: record.Markdown, CoveredMessages: record.CoveredMessages, CoveredHash: record.CoveredHash,
	}, true, nil
}

func (store sessionCompactionNoteStore) StoreCompactionNote(ctx context.Context, providerKey string, note agent.CompactionNote) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := session.StoreCompactionNote(store.sessDir, session.CompactionNote{
		SessionID: store.sessionID, ProviderKey: providerKey, Markdown: note.Markdown,
		CoveredMessages: note.CoveredMessages, CoveredHash: note.CoveredHash,
	})
	if errors.Is(err, session.ErrSessionNotFound) {
		return nil
	}
	return err
}

func (store sessionCompactionNoteStore) CompareAndSwapCompactionNote(ctx context.Context, providerKey string, expected agent.CompactionNote, expectedExists bool, replacement agent.CompactionNote) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	stored, err := session.CompareAndSwapCompactionNote(store.sessDir, session.CompactionNote{
		SessionID: store.sessionID, ProviderKey: providerKey, Markdown: expected.Markdown,
		CoveredMessages: expected.CoveredMessages, CoveredHash: expected.CoveredHash,
	}, expectedExists, session.CompactionNote{
		SessionID: store.sessionID, ProviderKey: providerKey, Markdown: replacement.Markdown,
		CoveredMessages: replacement.CoveredMessages, CoveredHash: replacement.CoveredHash,
	})
	if errors.Is(err, session.ErrSessionNotFound) {
		return false, nil
	}
	return stored, err
}

type sessionModelInputReceiptStore struct {
	sessDir   string
	sessionID string
}

func (store sessionModelInputReceiptStore) SaveModelInputReceipt(ctx context.Context, receipt agent.ModelInputReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if receipt.SessionID != "" && receipt.SessionID != store.sessionID {
		return fmt.Errorf("model input receipt session %q does not match runtime %q", receipt.SessionID, store.sessionID)
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode model input receipt: %w", err)
	}
	err = session.SaveModelInputReceipt(store.sessDir, store.sessionID, session.ModelInputReceiptRecord{
		OperationID:     receipt.OperationID,
		ContractVersion: receipt.ContractVersion,
		DriverID:        receipt.DriverID,
		DriverVersion:   receipt.DriverVersion,
		Payload:         payload,
		CreatedAt:       receipt.CreatedAt,
	})
	if errors.Is(err, session.ErrSessionNotFound) {
		return nil
	}
	return err
}

func (store sessionDriverCheckpointStore) Load(ctx context.Context) (loopdriver.Checkpoint, bool, error) {
	if err := ctx.Err(); err != nil {
		return loopdriver.Checkpoint{}, false, err
	}
	record, ok, err := session.LoadDriverCheckpoint(store.sessDir, store.sessionID)
	if errors.Is(err, session.ErrSessionNotFound) {
		return loopdriver.Checkpoint{}, false, nil
	}
	if err != nil || !ok {
		return loopdriver.Checkpoint{}, ok, err
	}
	return loopdriver.Checkpoint{
		ContractVersion: record.ContractVersion,
		DriverID:        record.DriverID,
		DriverVersion:   record.DriverVersion,
		State:           append(json.RawMessage(nil), record.State...),
	}, true, nil
}

func (store sessionDriverCheckpointStore) Save(ctx context.Context, checkpoint loopdriver.Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := session.SaveDriverCheckpoint(store.sessDir, store.sessionID, session.DriverCheckpointRecord{
		ContractVersion: checkpoint.ContractVersion,
		DriverID:        checkpoint.DriverID,
		DriverVersion:   checkpoint.DriverVersion,
		State:           append(json.RawMessage(nil), checkpoint.State...),
	})
	if errors.Is(err, session.ErrSessionNotFound) {
		return nil
	}
	return err
}

func cloneStreamRunnerForThread(base *agent.StreamRunner, toolExecutor agent.ToolExecutor) *agent.StreamRunner {
	if base == nil {
		return nil
	}
	return &agent.StreamRunner{
		Client:                      base.Client,
		ProviderName:                base.ProviderName,
		ProviderObservationKey:      base.ProviderObservationKey,
		Tools:                       toolExecutor,
		ToolLedger:                  base.ToolLedger,
		Model:                       base.Model,
		APIModel:                    base.APIModel,
		SystemPrompt:                base.SystemPrompt,
		SystemPromptSections:        append([]agent.SystemPromptSectionInfo(nil), base.SystemPromptSections...),
		MaxSteps:                    base.MaxSteps,
		Temperature:                 base.Temperature,
		MediaInput:                  base.MediaInput,
		OnEvent:                     base.OnEvent,
		OnUsage:                     base.OnUsage,
		OnTokenUsage:                base.OnTokenUsage,
		BeforeCompact:               base.BeforeCompact,
		AfterCompact:                base.AfterCompact,
		ContextWindowOverride:       base.ContextWindowOverride,
		MaxInputTokens:              base.MaxInputTokens,
		OutputReserveTokens:         base.OutputReserveTokens,
		CompactThresholdTokens:      base.CompactThresholdTokens,
		CompactThresholdPct:         base.CompactThresholdPct,
		CompactKeepRecentTokens:     base.CompactKeepRecentTokens,
		DisableAutoCompact:          base.DisableAutoCompact,
		StreamingToolExecution:      base.StreamingToolExecution,
		BeforeStep:                  base.BeforeStep,
		BeforeModelStep:             base.BeforeModelStep,
		BeforeRequestContext:        base.BeforeRequestContext,
		BeforeRequest:               base.BeforeRequest,
		AfterTurn:                   base.AfterTurn,
		Effort:                      base.Effort,
		Variant:                     base.Variant,
		ProviderOptions:             provideroptions.Clone(base.ProviderOptions),
		NativeDeferredToolDiscovery: base.NativeDeferredToolDiscovery,
		PromptCacheKey:              base.PromptCacheKey,
		InferenceOperationKind:      base.InferenceOperationKind,
		InferenceWorkloadProfile:    base.InferenceWorkloadProfile,
		InferenceJournal:            base.InferenceJournal,
		LoopDriver:                  base.LoopDriver,
		DriverCheckpointStore:       base.DriverCheckpointStore,
		ModelInputReceiptStore:      base.ModelInputReceiptStore,
		CompactionRegistry:          base.CompactionRegistry,
		CompactionNoteStore:         base.CompactionNoteStore,
		ArchiveHistory:              base.ArchiveHistory,
	}
}

func sameRuntimeRoot(left, right string) bool {
	left = cleanRuntimeRoot(left)
	right = cleanRuntimeRoot(right)
	return left != "" && left == right
}

func (s *Session) processManagerForThread(threadRoot, stateDir string) (*process.Manager, error) {
	if s == nil || s.ProcessManager == nil || sameRuntimeRoot(threadRoot, s.RootDir) {
		if s == nil {
			return nil, nil
		}
		return s.ProcessManager, nil
	}
	pool := s.threadProcessManagerPool()
	pool.mu.Lock()
	defer pool.mu.Unlock()
	key := threadProcessManagerKey{root: cleanRuntimeRoot(threadRoot), state: cleanRuntimeRoot(stateDir)}
	if manager := pool.managers[key]; manager != nil {
		return manager, nil
	}
	manager, err := process.NewManagerWithHostGeneration(
		threadRoot,
		s.ProcessManager.HostGenerationID(),
		statepath.RuntimeDir(stateDir),
	)
	if err != nil {
		return nil, err
	}
	pool.managers[key] = manager
	return manager, nil
}

type threadProcessManagerKey struct{ root, state string }

// A process registry needs one in-process authority for handles, writes and
// recheck timers. Model runtimes remain per-session; process ownership is
// recorded independently on every launch through RootThreadID.
type threadProcessManagers struct {
	mu       sync.Mutex
	managers map[threadProcessManagerKey]*process.Manager
}

func (s *Session) threadProcessManagerPool() *threadProcessManagers {
	s.threadProcessMu.Lock()
	defer s.threadProcessMu.Unlock()
	if s.threadProcesses == nil {
		s.threadProcesses = &threadProcessManagers{managers: make(map[threadProcessManagerKey]*process.Manager)}
	}
	return s.threadProcesses
}

// sessionParticipantStore adapts the session store to
// agentcontrol.ParticipantStore so spawned workers get durable
// participant identities.
type sessionParticipantStore struct {
	sessDir string
}

func (s sessionParticipantStore) Upsert(p participant.Participant) error {
	return session.UpsertParticipant(s.sessDir, p)
}

func cleanRuntimeRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	// State directories may not exist before the first manager is created.
	// Resolve the nearest existing ancestor so their cache key stays stable
	// after creation (including macOS /var -> /private/var).
	for ancestor, suffix := root, ""; ; {
		if resolved, err := filepath.EvalSymlinks(ancestor); err == nil {
			return filepath.Clean(filepath.Join(resolved, suffix))
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			break
		}
		suffix = filepath.Join(filepath.Base(ancestor), suffix)
		ancestor = parent
	}
	return filepath.Clean(root)
}

func systemPromptForThreadRoot(promptText string, sections []agent.SystemPromptSectionInfo, rootDir, sessionDate string) (string, []agent.SystemPromptSectionInfo) {
	envSection := environmentSystemPromptSection(rootDir, sessionDate)
	if strings.TrimSpace(promptText) == "" || strings.TrimSpace(envSection) == "" {
		return promptText, append([]agent.SystemPromptSectionInfo(nil), sections...)
	}
	const marker = "# Environment"
	start := strings.Index(promptText, marker)
	if start < 0 {
		return promptText, append([]agent.SystemPromptSectionInfo(nil), sections...)
	}
	end := len(promptText)
	if next := strings.Index(promptText[start+len(marker):], "\n\n# "); next >= 0 {
		end = start + len(marker) + next
	}
	updated := promptText[:start] + envSection + promptText[end:]
	return updated, updateEnvironmentSectionInfo(sections, envSection)
}

func updateEnvironmentSectionInfo(sections []agent.SystemPromptSectionInfo, envSection string) []agent.SystemPromptSectionInfo {
	return updateSectionInfo(sections, "environment", envSection)
}

func updateSectionInfo(sections []agent.SystemPromptSectionInfo, key, content string) []agent.SystemPromptSectionInfo {
	out := append([]agent.SystemPromptSectionInfo(nil), sections...)
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:16])
	for i := range out {
		if out[i].Key == key {
			out[i].Bytes = len([]byte(content))
			out[i].Hash = hash
			return out
		}
	}
	return out
}

// mediaInputPolicyFromCapabilities maps resolved model capabilities onto the
// request media admission policy. Missing modality evidence stays unknown so
// explicit user media reaches the provider instead of being silently dropped.
func mediaInputPolicyFromCapabilities(caps modelroles.Capabilities) providers.MediaInputPolicy {
	return providers.MediaInputPolicy{
		Image:      caps.ImageInput,
		File:       caps.FileInput,
		ImageKnown: caps.ImageInputKnown,
		FileKnown:  caps.FileInputKnown,
	}
}

func applyWorkerToolFilter(kit *tools.Toolkit, wt agentcontrol.WorkerType) {
	if kit == nil {
		return
	}
	fullNames := kit.SurfaceToolNames()

	allowed := agentcontrol.FilterToolsForWorker(wt, fullNames)
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}

	disabled := make([]string, 0, len(fullNames)-len(allowedSet))
	for _, name := range fullNames {
		if _, ok := allowedSet[name]; !ok {
			disabled = append(disabled, name)
		}
	}
	kit.DisableTools(disabled...)
}

// SetSessionID binds user-level runtime artifact paths after the UI has
// created or resumed a session. Conversation logs live in SessionDir.
func (s *Session) SetSessionID(id string) error {
	if s == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	id = strings.TrimSpace(id)
	if s.StreamRunner != nil {
		s.StreamRunner.PromptCacheKey = id
	}
	stateDir := strings.TrimSpace(s.StateDir)
	if stateDir == "" {
		if home, err := statepath.Home(""); err == nil {
			if dir, err := resolveWorkspaceStateDir(home, s.WorkspaceID, s.RootDir); err == nil {
				stateDir = dir
			}
		}
	}
	if stateDir == "" {
		return errors.New("workspace state directory is required to bind session")
	}
	artifactDir := statepath.SessionArtifactDir(stateDir, id)
	if s.Toolkit != nil {
		s.Toolkit.SetSessionID(id)
		s.Toolkit.SetAgentIdentity(id, agentthread.RootPath)
		s.Toolkit.SetSessionDir(artifactDir)
	}
	if s.AgentControl != nil {
		if err := s.AgentControl.SetSessionInfo(
			id,
			filepath.Join(artifactDir, "workers"),
			filepath.Join(artifactDir, "threads"),
		); err != nil {
			return err
		}
		s.AgentControl.StartWorkerTerminalRecovery()
		s.AgentControl.StartQueuedWork()
	}
	if s.HookDispatcher != nil {
		if _, err := s.HookDispatcher.Dispatch(context.Background(), hooks.SessionStart, &hooks.Input{SessionID: id, CWD: s.RootDir}); err != nil {
			return fmt.Errorf("session start hook: %w", err)
		}
	}
	return nil
}

// ownedWorkerExecutionDrainTimeout bounds how long session cleanup waits for
// worker execution leases to release. A wedged worker must not stall the MCP,
// inference-journal, and process-manager cleanup that follows.
const ownedWorkerExecutionDrainTimeout = 5 * time.Second

func shutdownAndCleanupAgentControl(control *agentcontrol.AgentControl) error {
	if control == nil {
		return nil
	}
	control.BeginShutdown()
	control.StopAll()
	control.YieldWorkerTerminalFinalizations()
	deadline := time.Now().Add(ownedWorkerExecutionDrainTimeout)
	for control.HasOwnedWorkerExecutions() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if remaining := control.OwnedWorkerExecutionCount(); remaining > 0 {
		providers.DebugLogf("session cleanup: %d worker execution lease(s) still owned after %s; proceeding with cleanup", remaining, ownedWorkerExecutionDrainTimeout)
	}
	pending, pendingErr := control.PendingWorkerTerminalFinalizations()
	control.Close()
	if pendingErr != nil {
		return fmt.Errorf("inspect pending worker terminal finalizations; preserving session worktrees: %w", pendingErr)
	}
	if pending {
		return errors.New("worker terminal finalizations remain pending; preserving session worktrees for recovery")
	}
	return control.CleanupSession()
}

// Cleanup stops session-scoped background work owned by the runtime.
func (s *Session) Cleanup() (process.CleanupResult, error) {
	if s == nil {
		return process.CleanupResult{}, nil
	}
	var cleanupErr error
	if s.HookDispatcher != nil {
		sessionID := ""
		if s.Toolkit != nil {
			sessionID = s.Toolkit.SessionID()
		}
		_, hookErr := s.HookDispatcher.Dispatch(context.Background(), hooks.SessionEnd, &hooks.Input{SessionID: sessionID, CWD: s.RootDir})
		cleanupErr = errors.Join(cleanupErr, hookErr)
	}
	if s.AgentControl != nil {
		// Terminal intents are the durable recovery authority. Yield local retry
		// loops, wait for physical execution leases to release, and only remove
		// worktrees when no unacknowledged terminal record remains.
		cleanupErr = errors.Join(cleanupErr, shutdownAndCleanupAgentControl(s.AgentControl))
		s.AgentControl = nil
	}
	s.pluginGenerationMu.Lock()
	if s.pluginGeneration != nil {
		cleanupErr = errors.Join(cleanupErr, s.pluginGeneration.close())
		s.pluginGeneration = nil
		s.PluginHost = nil
	} else if s.Toolkit != nil {
		if manager := s.Toolkit.MCPManager(); manager != nil {
			cleanupErr = errors.Join(cleanupErr, manager.Close())
		}
	}
	s.pluginGenerationMu.Unlock()
	if s.InferenceJournalRuntime != nil {
		cleanupErr = errors.Join(cleanupErr, s.InferenceJournalRuntime.Close())
		s.InferenceJournalRuntime = nil
	}
	if s.UserQuestions != nil {
		s.UserQuestions.Close()
		s.UserQuestions = nil
	}
	if s.codeModeStop != nil {
		// Killing the process is the termination authority for stuck cells.
		s.codeModeStop()
		s.codeModeStop = nil
	}
	if s.CodeMode != nil {
		cleanupErr = errors.Join(cleanupErr, s.CodeMode.Close())
		s.CodeMode = nil
	}
	if s.PluginHost != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = s.PluginHost.Close(ctx)
		cancel()
		s.PluginHost = nil
	}
	managers := map[*process.Manager]struct{}{}
	if s.ProcessManager != nil {
		managers[s.ProcessManager] = struct{}{}
	}
	pool := s.threadProcessManagerPool()
	pool.mu.Lock()
	for _, manager := range pool.managers {
		managers[manager] = struct{}{}
	}
	pool.mu.Unlock()
	result := process.CleanupResult{}
	for manager := range managers {
		cleaned, err := manager.CleanupSessionWithResult()
		result.Cleaned = append(result.Cleaned, cleaned.Cleaned...)
		cleanupErr = errors.Join(cleanupErr, err)
	}
	return result, cleanupErr
}

func ResolveModelBudget(model string, provider config.ProviderConfig, agentOverride int) modelbudget.Budget {
	return modelbudget.Resolve(model, provider, agentOverride)
}

// ResolveContextWindow resolves the trusted model context size used for
// proactive auto-compact. A zero return means the model limit is unknown; the
// runtime should skip proactive compaction and rely on provider overflow errors.
func ResolveContextWindow(model string, provider config.ProviderConfig, agentOverride int) int {
	return ResolveModelBudget(model, provider, agentOverride).ContextWindowTokens
}

// ResolveInputWindow resolves the effective prompt/input budget when the
// provider publishes a separate input cap. It intentionally does not synthesize
// an input cap from context-output; proactive compaction handles output reserve
// separately.
func ResolveInputWindow(model string, provider config.ProviderConfig) int {
	return ResolveModelBudget(model, provider, 0).InputLimitTokens
}

const codexSubscriptionGPT5InputCap = modelbudget.CodexSubscriptionGPT5InputCap

// RuntimeContextInjector returns volatile request-only runtime context injected
// into model requests without appending it to live or durable history. Stable
// session environment belongs in the system prompt, not here.
func RuntimeContextInjector(control *agentcontrol.AgentControl, currentPath string, blockProviders ...func() []wuucontext.Block) func() []agent.ContextSegment {
	return func() []agent.ContextSegment {
		var blocks []wuucontext.Block
		for _, provider := range blockProviders {
			if provider == nil {
				continue
			}
			blocks = append(blocks, provider()...)
		}
		if control != nil {
			if agentReminder := control.ActiveTaskReminder(currentPath); agentReminder != "" {
				blocks = append(blocks, wuucontext.Block{
					Kind:    wuucontext.BlockTaskState,
					Title:   "Active child-agent status",
					Source:  "runtime.subagent_status",
					Content: agentReminder,
				})
			}
		}
		return agent.RequestOnlyContextBlocks(blocks)
	}
}

func toolkitContextBlockProvider(toolkit *tools.Toolkit) func() []wuucontext.Block {
	if toolkit == nil {
		return nil
	}
	return toolkit.ContextBlocks
}

func namedAgentNotebookContextProvider(memoryDir string) func() []wuucontext.Block {
	return func() []wuucontext.Block {
		snapshot, err := memdir.ReadIndex(memoryDir)
		if err != nil {
			providers.DebugLogf("refresh collaboration memory: %v", err)
			return nil
		}
		return []wuucontext.Block{{Kind: wuucontext.BlockMemory, Title: "Current collaboration memory index", Source: "runtime.collaboration_memory", Content: snapshot.Content}}
	}
}

func namedAgentWorkspaceContextProvider(wuuHome, agentHome, memoryDir string, toolkit *tools.Toolkit) func() []wuucontext.Block {
	return func() []wuucontext.Block {
		if toolkit != nil {
			toolkit.SetFileScopeRoots(workspaces.BoundaryRoots(agentHome, wuuHome, memoryDir))
		}
		registered, err := workspaces.List(wuuHome)
		if err != nil {
			providers.DebugLogf("read named agent registered workspaces: %v", err)
			return nil
		}
		var content strings.Builder
		fmt.Fprintf(&content, "Agent home (identity/state anchor, not project scope): %s\n", agentHome)
		if len(registered) == 0 {
			content.WriteString("Registered project workspaces: none. Projectless conversation sessions are excluded.")
		} else {
			content.WriteString("Registered project workspaces available for activity:\n")
			for _, workspace := range registered {
				name := strings.TrimSpace(workspace.Name)
				root := strings.TrimSpace(workspace.Root)
				if name == "" {
					name = root
				}
				fmt.Fprintf(&content, "- %s — %s\n", name, root)
			}
			content.WriteString("Use absolute paths or a command-specific cwd to work in any listed project.")
		}
		return []wuucontext.Block{{
			Kind:    wuucontext.BlockEnvironment,
			Title:   "Named agent project activity scope",
			Source:  "runtime.named_agent_workspaces",
			Content: strings.TrimSpace(content.String()),
		}}
	}
}

func setupCatwalk(cfg config.Config) {
	catwalkCfg := providers.CatwalkSyncConfig{
		CachePath: providers.DefaultCatwalkCachePath(),
	}
	if cfg.Agent.CatwalkAutoupdate {
		catwalkCfg.Client = catwalk.NewWithURL(providers.DefaultCatwalkURL)
	}
	catwalkSync := providers.NewCatwalkSync(catwalkCfg)
	providers.SetCatwalkSync(catwalkSync)
	if cfg.Agent.CatwalkAutoupdate {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = providers.RefreshCatwalkIndex(ctx)
		}()
	}
}

func buildHookDispatcher(cfg config.Config, plugins []pluginpkg.Plugin, client providers.Client, defaultModel string, defaultJournal providers.InferenceJournal) *hooks.Dispatcher {
	hookEntries := make(map[hooks.Event][]hooks.HookConfig)
	for evName, entries := range cfg.Hooks {
		ev := hooks.Event(evName)
		for _, e := range entries {
			hookEntries[ev] = append(hookEntries[ev], hooks.HookConfig{
				Matcher: e.Matcher,
				Type:    e.Type,
				Command: e.Command,
				Prompt:  e.Prompt,
				Model:   e.Model,
				Timeout: e.Timeout,
			})
		}
	}
	for _, item := range plugins {
		for evName, entries := range item.Hooks {
			ev := hooks.Event(evName)
			for _, e := range entries {
				hookEntries[ev] = append(hookEntries[ev], hooks.HookConfig{
					Matcher: e.Matcher,
					Type:    e.Type,
					Command: e.Command,
					Prompt:  e.Prompt,
					Model:   e.Model,
					Timeout: e.Timeout,
				})
			}
		}
	}
	hookRegistry := hooks.NewRegistry(hookEntries)
	if client != nil {
		// Wire the prompt-hook model client so type=prompt hooks actually
		// run. Without this, PromptHook.Execute short-circuits with a
		// nil client and the hook silently passes through. Pass the
		// configured tool-mode model as the default; individual hook
		// entries can still override via their own `model` field.
		hookRegistry.SetModelClient(hooks.NewProviderModelClient(client, defaultModel, defaultJournal))
	}
	return hooks.NewDispatcher(hookRegistry)
}

func discoverPlugins(rootDir, wuuHome string) []pluginpkg.Plugin {
	return pluginpkg.Discover(rootDir, wuuHome)
}

// currentWuuVersion resolves the running host version for the
// minimum_wuu_version compatibility gate. Overridable in tests.
var currentWuuVersion = func() string { return version.Info().Version }

// activatedPlugins separates inert discovery from executable activation.
// Community packages require an exact user-owned grant. Official bundled
// packages and authenticated local development generations are trusted by
// provenance. Every tier follows its package default unless the user has made
// an explicit choice, and every tier
// must satisfy its declared minimum_wuu_version against the running host:
// the compatibility contract fails closed regardless of trust tier.
func activatedPlugins(cfg config.Config, discovered []pluginpkg.Plugin) []pluginpkg.Plugin {
	settings := cfg.Extensions
	hostVersion := currentWuuVersion()
	out := make([]pluginpkg.Plugin, 0, len(discovered))
	for _, item := range discovered {
		enabled := item.EnabledByDefault()
		if settings != nil {
			enabled = settings.IsEnabled(item.SubjectID, enabled)
		}
		if !enabled {
			continue
		}
		if err := pluginpkg.CheckMinimumWuuVersion(item.MinimumWuuVersion, hostVersion); err != nil {
			continue
		}
		if item.Official || item.AuthorizedDev {
			out = append(out, item)
			continue
		}
		if settings == nil || settings.IsRejected(item.SubjectID, item.Fingerprint) {
			continue
		}
		grant, ok := settings.FindGrant(item.SubjectID, item.Fingerprint)
		if !ok || !permissionSetContains(grant.Permissions, item.EffectivePermissions) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// ResolvePluginActivationPlan applies package relationships after trust,
// explicit disablement, and host compatibility have selected candidates.
func ResolvePluginActivationPlan(cfg config.Config, discovered []pluginpkg.Plugin) (pluginpkg.ActivationPlan, error) {
	return pluginpkg.BuildActivationPlan(activatedPlugins(cfg, discovered))
}

func permissionSetContains(granted, required []string) bool {
	if len(required) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(granted))
	for _, permission := range granted {
		set[strings.TrimSpace(permission)] = struct{}{}
	}
	for _, permission := range required {
		if _, ok := set[strings.TrimSpace(permission)]; !ok {
			return false
		}
	}
	return true
}

// SetExtensionSettings keeps the live policy and generation rollback snapshot
// aligned after a durable settings transaction.
func (s *Session) SetExtensionSettings(settings *extensions.Settings) {
	if s == nil {
		return
	}
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	s.ExtensionSettings = settings
	if s.pluginGeneration != nil {
		s.pluginGeneration.settings.Extensions = settings
	}
}

// RefreshExtensions rediscovers package manifests before rebuilding the active
// extension surfaces. It is used by the catalog's explicit Refresh action.
func (s *Session) RefreshExtensions(cfg config.Config) error {
	if s == nil {
		return errors.New("runtime is not initialized")
	}
	candidate, err := s.PreflightExtensions(cfg)
	if err != nil {
		return err
	}
	return s.ActivatePluginGeneration(candidate, nil)
}

// RefreshPluginCatalog updates the installed-package inventory only. It never
// rebuilds or swaps the active plugin generation, so installation and
// validation can proceed while Sessions and Turns are running. Newly
// installed packages become eligible for later compositions but add no
// model-facing surfaces to the current one.
func (s *Session) RefreshPluginCatalog() error {
	if s == nil {
		return errors.New("runtime is not initialized")
	}
	discovered := discoverPlugins(s.RootDir, s.WuuHome)
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	s.Plugins = discovered
	return nil
}

func discoverSkills(rootDir, homeDir, wuuHome string, plugins []pluginpkg.Plugin) []skills.Skill {
	var projectDirs []skills.SourceDir
	var userDirs []skills.SourceDir
	for _, item := range plugins {
		source := item.SourceLabel()
		for _, dir := range item.SkillDirs() {
			switch item.Source {
			case "project":
				projectDirs = append(projectDirs, skills.SourceDir{Path: dir, Source: source})
			default:
				userDirs = append(userDirs, skills.SourceDir{Path: dir, Source: source})
			}
		}
	}
	if home := skillUserHome(homeDir); home != "" {
		userDirs = append(userDirs,
			skills.SourceDir{Path: filepath.Join(home, ".codex", "skills"), Source: "user"},
			skills.SourceDir{Path: filepath.Join(home, ".claude", "skills"), Source: "user"},
			skills.SourceDir{Path: filepath.Join(home, ".agents", "skills"), Source: "user"},
			skills.SourceDir{Path: filepath.Join(home, ".config", "opencode", "skills"), Source: "user"},
		)
	}
	if strings.TrimSpace(wuuHome) != "" {
		userDirs = append(userDirs, skills.SourceDir{Path: filepath.Join(wuuHome, "skills"), Source: "user"})
	}
	projectDirs = append(projectDirs, skillProjectDirs(rootDir)...)
	discovered := skills.DiscoverSourceDirs(projectDirs, userDirs)
	// Claude Code-style command templates (.claude/commands/*.md) are read as
	// pure content and adapted into lightweight skill entries. Native skills
	// take precedence over commands with the same name.
	commands := skills.DiscoverCommandsSourceDirs(commandProjectDirs(rootDir), commandUserDirs(wuuHome))
	return skills.MergeWithBundled(skills.MergeCommands(discovered, commands), wuuHome)
}

// commandProjectDirs returns the project-chain command directories, mirroring
// skillProjectDirs but scoped to Claude Code / wuu command template folders.
func commandProjectDirs(rootDir string) []skills.SourceDir {
	if strings.TrimSpace(rootDir) == "" {
		return nil
	}
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil
	}
	projectRoot := findSkillProjectRoot(absRoot)
	chain := skillDirChain(projectRoot, absRoot)
	out := make([]skills.SourceDir, 0, len(chain)*2)
	for _, dir := range chain {
		out = append(out,
			skills.SourceDir{Path: filepath.Join(dir, ".claude", "commands"), Source: "project"},
			skills.SourceDir{Path: filepath.Join(dir, ".wuu", "commands"), Source: "project"},
		)
	}
	return out
}

// commandUserDirs returns the user-level command directory (~/.wuu/commands),
// resolved from wuuHome exactly like the ~/.wuu/skills user directory.
func commandUserDirs(wuuHome string) []skills.SourceDir {
	if strings.TrimSpace(wuuHome) == "" {
		return nil
	}
	return []skills.SourceDir{{Path: filepath.Join(wuuHome, "commands"), Source: "user"}}
}

func skillUserHome(homeDir string) string {
	home := strings.TrimSpace(homeDir)
	if home != "" {
		return home
	}
	if resolved, err := os.UserHomeDir(); err == nil {
		return resolved
	}
	return ""
}

func skillProjectDirs(rootDir string) []skills.SourceDir {
	if strings.TrimSpace(rootDir) == "" {
		return nil
	}
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil
	}
	projectRoot := findSkillProjectRoot(absRoot)
	chain := skillDirChain(projectRoot, absRoot)
	out := make([]skills.SourceDir, 0, len(chain)*5)
	for _, dir := range chain {
		// External ecosystem directories are lower precedence than native wuu
		// skills at the same level. More specific child directories still override
		// ancestors because the chain is ordered root -> current directory.
		out = append(out,
			skills.SourceDir{Path: filepath.Join(dir, ".claude", "skills"), Source: "project"},
			skills.SourceDir{Path: filepath.Join(dir, ".agents", "skills"), Source: "project"},
			skills.SourceDir{Path: filepath.Join(dir, ".opencode", "skill"), Source: "project"},
			skills.SourceDir{Path: filepath.Join(dir, ".opencode", "skills"), Source: "project"},
			skills.SourceDir{Path: filepath.Join(dir, ".wuu", "skills"), Source: "project"},
		)
	}
	return out
}

func findSkillProjectRoot(start string) string {
	cur := start
	for {
		for _, marker := range []string{".git", ".hg", ".jj", ".svn"} {
			if _, err := os.Lstat(filepath.Join(cur, marker)); err == nil {
				return cur
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func skillDirChain(root, leaf string) []string {
	if root == "" {
		return []string{leaf}
	}
	rel, err := filepath.Rel(root, leaf)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return []string{leaf}
	}
	chain := []string{root}
	if rel == "." {
		return chain
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		chain = append(chain, cur)
	}
	return chain
}

func connectMCPServers(cfg config.Config, plugins []pluginpkg.Plugin, toolkit *tools.Toolkit) {
	if toolkit == nil {
		return
	}
	toolkit.SetMCPActivityBindings(mcpActivityBindingsFromPlugins(plugins))
	manager, _ := startMCPManager(cfg, plugins, nil)
	toolkit.SetMCPManager(manager)
}

func startMCPManager(cfg config.Config, plugins []pluginpkg.Plugin, requiredPlugins map[string]bool) (*mcp.Manager, error) {
	servers := mcpServersFromConfigAndPlugins(cfg, plugins)
	if len(servers) == 0 {
		return nil, nil
	}
	mcpMgr := mcp.NewManager()
	serverConfigs := make(map[string]mcp.ServerConfig, len(servers))
	requiredServers := make(map[string]bool)
	for _, item := range plugins {
		if !requiredPlugins[item.ID] {
			continue
		}
		for name := range item.MCPServers {
			requiredServers[PluginMCPServerName(item.ID, name)] = true
		}
	}
	for name, mcpCfg := range servers {
		serverConfigs[name] = mcp.ServerConfig{
			Name:          name,
			Command:       mcpCfg.Command,
			Args:          mcpCfg.Args,
			URL:           mcpCfg.URL,
			Transport:     mcpCfg.Transport,
			Env:           mcpCfg.Env,
			Headers:       mcpCfg.Headers,
			OAuth:         mcpOAuthConfig(mcpCfg.OAuth),
			Enabled:       mcpCfg.Enabled,
			ToolOverrides: mcpToolOverrides(mcpCfg.ToolOverrides),
		}
	}
	mcpMgr.Configure(serverConfigs)
	for name, serverCfg := range serverConfigs {
		if !requiredServers[name] || !serverCfg.IsEnabled() {
			continue
		}
		if err := mcpMgr.Add(context.Background(), serverCfg); err != nil {
			_ = mcpMgr.Close()
			return nil, fmt.Errorf("start required MCP server %q: %w", name, err)
		}
	}
	go func() {
		ctx := context.Background()
		for name, serverCfg := range serverConfigs {
			if requiredServers[name] {
				continue
			}
			if !serverCfg.IsEnabled() {
				providers.DebugLogf("mcp server %q disabled", name)
				continue
			}
			if err := mcpMgr.Add(ctx, serverCfg); err != nil {
				providers.DebugLogf("mcp server %q failed to connect: %v", name, err)
			} else {
				providers.DebugLogf("mcp server %q connected (%d tools)", name, mcpMgr.Status()[name].ToolCount)
			}
		}
	}()
	return mcpMgr, nil
}

func mcpActivityBindingsFromPlugins(plugins []pluginpkg.Plugin) map[string]tools.MCPActivityBinding {
	out := make(map[string]tools.MCPActivityBinding)
	for _, item := range plugins {
		if len(item.ActivityKinds) != 1 {
			continue
		}
		kind := activity.Kind(strings.TrimSpace(item.ActivityKinds[0]))
		if kind != activity.KindBrowser && kind != activity.KindCUA {
			continue
		}
		pluginID := strings.TrimSpace(item.ID)
		if pluginID == "" {
			continue
		}
		for name := range item.MCPServers {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			out[PluginMCPServerName(pluginID, name)] = tools.MCPActivityBinding{Kind: kind, PluginID: pluginID}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mcpServersFromConfigAndPlugins(cfg config.Config, plugins []pluginpkg.Plugin) map[string]config.MCPServerConfig {
	out := make(map[string]config.MCPServerConfig)
	for _, item := range plugins {
		for name, server := range item.MCPServers {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			out[PluginMCPServerName(item.ID, name)] = server
		}
	}
	for name, server := range cfg.MCPServers {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if IsPluginMCPServerName(name) {
			pluginServer, ok := out[name]
			if !ok {
				continue
			}
			if server.Enabled != nil {
				pluginServer.Enabled = server.Enabled
			}
			out[name] = pluginServer
			continue
		}
		out[name] = server
	}
	return out
}

func PluginMCPServerName(pluginID, serverName string) string {
	return "plugin." + strings.TrimSpace(pluginID) + "." + strings.TrimSpace(serverName)
}

func IsPluginMCPServerName(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), "plugin.")
}

func mcpToolOverrides(in map[string]config.MCPToolOverride) map[string]mcp.ToolOverride {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]mcp.ToolOverride, len(in))
	for name, override := range in {
		out[name] = mcp.ToolOverride{
			ReadOnly:        override.ReadOnly,
			ConcurrencySafe: override.ConcurrencySafe,
			Capability:      override.Capability,
		}
	}
	return out
}

func mcpOAuthConfig(in *config.MCPOAuthConfig) *mcp.OAuthConfig {
	if in == nil {
		return nil
	}
	return &mcp.OAuthConfig{
		ClientID:     in.ClientID,
		ClientSecret: in.ClientSecret,
		Scopes:       append([]string(nil), in.Scopes...),
		RedirectURI:  in.RedirectURI,
	}
}

func ConfigureToolkitPermissions(kit *tools.Toolkit, permissions config.ResolvedPermissions) {
	if kit == nil {
		return
	}
	kit.SetBoundary(BoundaryForMode(permissions.Mode))
	kit.SetPermissionMode(config.NormalizePermissionMode(permissions.Mode))
}

// workerWakeAuthority builds the wake-time authority refresher for workers
// cloned from the given parent toolkit. Waking a dormant worker is a new
// execution admission, so the woken worker re-copies the parent's CURRENT
// workspace boundary — the same inheritance a fresh spawn performs via
// CloneForRoot — instead of keeping the boundary captured when it was
// spawned. The parent toolkit is the permission anchor kept fresh by
// ConfigureToolkitPermissions at turn starts and permission updates.
func workerWakeAuthority(parent *tools.Toolkit) func(agent.ToolExecutor) {
	return func(executor agent.ToolExecutor) {
		workerKit, ok := executor.(*tools.Toolkit)
		if !ok || workerKit == nil || parent == nil {
			return
		}
		workerKit.SetBoundary(parent.Boundary())
	}
}

func BoundaryForMode(mode string) tools.WorkspaceBoundary {
	switch config.NormalizePermissionMode(mode) {
	case config.PermissionModeReadOnly:
		return tools.ReadOnlyBoundary()
	case config.PermissionModeUnconfined:
		return tools.UnconfinedBoundary()
	default:
		return tools.StandardBoundary()
	}
}

func discoverInstructions(rootDir, homeDir string, cfg config.InstructionFilesConfig) []instructions.File {
	instructionOptions := instructions.DefaultOptions()
	if len(cfg.Filenames) > 0 {
		instructionOptions.Filenames = cfg.Filenames
	}
	if len(cfg.ProjectRootMarkers) > 0 {
		instructionOptions.ProjectRootMarkers = cfg.ProjectRootMarkers
	}
	if len(cfg.UserDirs) > 0 {
		instructionOptions.UserDirs = cfg.UserDirs
	} else if dirs := statepath.UserInstructionDirs(homeDir); len(dirs) > 0 {
		// Resolve the canonical user instruction dirs (unified wuu home +
		// legacy ~/.config/wuu) through statepath so WUU_HOME relocates the
		// user AGENTS.md scan along with the rest of the directory.
		instructionOptions.UserDirs = dirs
	}
	if cfg.IncludeLegacyInstructions != nil {
		instructionOptions.IncludeLegacyInstructions = cfg.IncludeLegacyInstructions
	}
	return instructions.Discover(rootDir, homeDir, instructionOptions)
}

// buildWorkerBasePrompt assembles a worker subagent's base system prompt.
// Workers get the READ-ONLY user-notebook variant (memory-redesign §3:
// 临时 worker 只读注入): the user index is injected as orientation — read
// fresh here because worker/thread creation is a prompt-prefix creation
// moment under the cache red lines — while the notebook directory stays
// outside the worker's writable file scope.
func buildWorkerBasePrompt(rootDir, sessionDate, userPrompt, providerName, model string, toolSurface capability.Surface, instructionFiles []instructions.File, discoveredSkills []skills.Skill) string {
	return buildBaseSystemPromptContent(rootDir, sessionDate, config.WorkerSystemPrompt(), userPrompt, providerName, model, toolSurface, instructionFiles, "", "", discoveredSkills)
}

func (s *Session) RefreshSystemPrompt(providerName, model string) string {
	if s == nil {
		return ""
	}
	baseSystemPromptResult := buildBaseSystemPromptResult(
		s.RootDir,
		s.SessionDate,
		config.DefaultSystemPrompt(),
		"",
		providerName,
		model,
		activeSurfaceWithDeferredToolCatalog(s.Toolkit, s.DeferredToolCatalogPrompt),
		s.InstructionFiles,
		"",
		"",
		s.Skills,
	)
	baseSystemPrompt, pluginSections := assemblePluginSystemPrompt(baseSystemPromptResult.Content, s.systemPrompts)
	s.BaseSystemPrompt = baseSystemPrompt
	s.BaseSystemPromptSections = baseSystemPromptResult.Sections
	if s.StreamRunner != nil {
		sections := append(agentPromptSections(baseSystemPromptResult.Sections), pluginSections...)
		s.StreamRunner.UpdateSystemPromptWithSections(baseSystemPrompt, sections)
	}
	return baseSystemPrompt
}

func assemblePluginSystemPrompt(base string, assembler *agent.SystemPromptAssembler) (string, []agent.SystemPromptSectionInfo) {
	if assembler == nil {
		return base, nil
	}
	pluginText, sections := assembler.Assemble("")
	if strings.TrimSpace(pluginText) == "" {
		return base, sections
	}
	if strings.TrimSpace(base) == "" {
		return pluginText, sections
	}
	return base + "\n\n" + pluginText, sections
}

// ApplyGeneralConfig refreshes instruction and git-attribution settings on the
// shared session runtime without changing provider or model selection.
func (s *Session) ApplyGeneralConfig(cfg config.Config, homeDir string) string {
	if s == nil {
		return ""
	}
	if strings.TrimSpace(homeDir) == "" {
		homeDir = os.Getenv("HOME")
	}
	s.InstructionFiles = discoverInstructions(s.RootDir, homeDir, cfg.Instructions)
	if s.Toolkit != nil {
		s.Toolkit.SetGitAttributionEnabled(cfg.Agent.GitAttributionEnabledValue())
		s.Toolkit.SetFileScopeRoots(workspaces.BoundaryRoots(s.Toolkit.RootDir(), s.WuuHome))
	}
	apiModel := s.Model
	if s.StreamRunner != nil && strings.TrimSpace(s.StreamRunner.APIModel) != "" {
		apiModel = s.StreamRunner.APIModel
	}
	return s.RefreshSystemPrompt(s.ProviderName, apiModel)
}

// buildBaseSystemPrompt keeps the frozen-date-agnostic signature used by tests
// and standalone callers; it passes an empty sessionDate so the environment
// section falls back to the current date.
func buildBaseSystemPrompt(rootDir, basePrompt, userPrompt, providerName, model string, toolSurface capability.Surface, instructionFiles []instructions.File, memdirTeaching, memdirIndex string, discoveredSkills []skills.Skill) string {
	return buildBaseSystemPromptContent(rootDir, "", basePrompt, userPrompt, providerName, model, toolSurface, instructionFiles, memdirTeaching, memdirIndex, discoveredSkills)
}

func buildBaseSystemPromptContent(rootDir, sessionDate, basePrompt, userPrompt, providerName, model string, toolSurface capability.Surface, instructionFiles []instructions.File, memdirTeaching, memdirIndex string, discoveredSkills []skills.Skill) string {
	return buildBaseSystemPromptResult(rootDir, sessionDate, basePrompt, userPrompt, providerName, model, toolSurface, instructionFiles, memdirTeaching, memdirIndex, discoveredSkills).Content
}

func buildBaseSystemPromptResult(rootDir, sessionDate, basePrompt, userPrompt, providerName, model string, toolSurface capability.Surface, instructionFiles []instructions.File, memdirTeaching, memdirIndex string, discoveredSkills []skills.Skill) prompt.BuildResult {
	var pb prompt.Builder
	pb.AddSection("base", basePrompt, true)
	pb.AddSection("tool_surface", toolSurface.SystemFragment, true)
	if _, ok := toolSurface.Tools["tool_search"]; ok {
		pb.AddSection("deferred_tool_catalog", toolSurface.DeferredToolCatalog, true)
	}
	pb.AddSection("environment", environmentSystemPromptSection(rootDir, sessionDate), true)
	if strings.TrimSpace(userPrompt) != "" {
		pb.AddSection("user_custom_prompt", "# User Custom Instructions\n\nFollow these user-defined instructions unless they conflict with wuu's built-in behavior, safety, or tool-use discipline above.\n\n"+userPrompt, true)
	}
	pb.AddInstructions(instructionFiles)
	if strings.TrimSpace(memdirTeaching) != "" {
		pb.AddMemdir(memdirTeaching, memdirIndex)
	}
	if toolSurface.ProfileName != "" {
		pb.AddSkills(tools.FilterSkillsForSurface(discoveredSkills, toolSurface))
	}
	return pb.BuildWithInfo()
}

// environmentSystemPromptSection renders the static "# Environment" system
// section. The date is the session-start frozen value (sessionDate) so the
// cached system prefix stays byte-stable across turns and thread rebuilds; a
// long-lived session that crosses a day boundary keeps its start date instead
// of busting the prompt cache. Real-time clock reads belong in the per-turn
// message stream, not this cached prefix. When sessionDate is empty (callers
// without a frozen session, e.g. standalone worker-prompt helpers) it falls
// back to the current date.
func environmentSystemPromptSection(rootDir, sessionDate string) string {
	env := wuucontext.Snapshot(rootDir)
	if frozen := strings.TrimSpace(sessionDate); frozen != "" {
		env.Date = frozen
	}
	var b strings.Builder
	b.WriteString("# Environment\n\n")
	if cwd := strings.TrimSpace(env.CWD); cwd != "" {
		fmt.Fprintf(&b, "- Current working directory: %s\n", cwd)
	}
	if date := strings.TrimSpace(env.Date); date != "" {
		fmt.Fprintf(&b, "- Current date: %s\n", date)
	}
	return strings.TrimRight(b.String(), "\n")
}

func agentPromptSections(sections []prompt.SectionInfo) []agent.SystemPromptSectionInfo {
	if len(sections) == 0 {
		return nil
	}
	out := make([]agent.SystemPromptSectionInfo, 0, len(sections))
	for _, section := range sections {
		out = append(out, agent.SystemPromptSectionInfo{
			Key:    section.Key,
			Static: section.Static,
			Bytes:  section.Bytes,
			Hash:   section.Hash,
		})
	}
	return out
}

func activeSurface(kit *tools.Toolkit) capability.Surface {
	if kit == nil {
		return capability.Surface{}
	}
	return kit.ActiveSurface()
}

func activeSurfaceWithDeferredToolCatalog(kit *tools.Toolkit, catalogPrompt string) capability.Surface {
	surface := activeSurface(kit)
	surface.DeferredToolCatalog = catalogPrompt
	return surface
}

func deferredToolCatalogPromptForToolkit(kit *tools.Toolkit) (string, error) {
	if kit == nil {
		return "", nil
	}
	return kit.DeferredToolCatalogSystemSection()
}

// workerDeferredToolCatalogPromptForToolkit computes the deferred-tool
// catalog section for the worker surface (consistency-repair #13: worker
// prompts taught tool_search catalog lookups while their catalog stayed
// empty). It reuses the exact generator that fills
// mainSurface.DeferredToolCatalog, but on a throwaway in-memory clone of the
// session toolkit configured with the worker-compiled surface, so entries are
// filtered by the worker's own exposure buckets (no orchestration suite,
// worker tool-search setting).
func workerDeferredToolCatalogPromptForToolkit(kit *tools.Toolkit, providerName, model string, toolSearchEnabled bool) (string, error) {
	if kit == nil {
		return "", nil
	}
	wkit, err := kit.CloneForRoot("")
	if err != nil {
		return "", err
	}
	wkit.ConfigureSurfaceForProviderModel(providerName, model, false)
	wkit.SetToolSearchEnabled(toolSearchEnabled)
	return wkit.DeferredToolCatalogSystemSection()
}

// compiledSurfaceForProviderModel is the worker-only entry point in
// production: every caller in internal/runtime/session.go that uses
// it is configuring a worker's tool surface, not the main agent's.
// The main agent's surface is installed through
// internal/tools/edit_mode.go::ConfigureSurfaceForProviderModel on
// the toolkit itself. Worker surfaces intentionally omit the
// main-agent orchestration tools.
func compiledSurfaceForProviderModel(providerName, model string) capability.Surface {
	return modelprofile.DefaultCompiler{}.Compile(modelprofile.Resolve(providerName, model), modelprofile.SurfaceWorker)
}
