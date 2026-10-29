package runtime

import (
	"context"
	"errors"
	"sync"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/blueberrycongee/wuu/internal/workspaces"
)

// A candidate allocates only a scope. The selected provider decides whether the
// native backend exists, so replacing agent.tools also removes MCP/PTC effects.
func (s *Session) candidateToolScope(generation *PluginGeneration, required map[string]bool) (*toolservice.Scope, func() error) {
	var admissionMu sync.Mutex
	var closed, inFlight bool
	scope := toolservice.NewScope(func(context.Context) (toolservice.Backend, error) {
		admissionMu.Lock()
		if closed {
			admissionMu.Unlock()
			return toolservice.Backend{}, errors.New("native tool backend was not initialized during workspace admission")
		}
		inFlight = true
		admissionMu.Unlock()
		defer func() { admissionMu.Lock(); inFlight = false; admissionMu.Unlock() }()
		var kit *tools.Toolkit
		var err error
		if s.Toolkit != nil {
			kit, err = s.Toolkit.CloneForRoot(s.RootDir)
		} else {
			kit, err = tools.New(s.RootDir)
		}
		if err != nil {
			return toolservice.Backend{}, err
		}
		cfg := generation.settings
		kit.SetStateDir(s.StateDir)
		kit.SetRuntimeHome(s.WuuHome)
		kit.SetWorkingNotesHome(s.WuuHome)
		kit.SetWorkspaceID(s.WorkspaceID)
		kit.SetProcessManager(s.ProcessManager)
		kit.SetActivityRegistry(s.ActivityRegistry)
		if generation.state != nil {
			kit.SetSessionStore(generation.state.sessions)
		}
		kit.SetArtifactPublisher(NewArtifactPublisher(generation.artifactClient))
		kit.SetSkillsWithAvailability(generation.skills, generation.skillAvailable)
		ConfigureToolkitPermissions(kit, s.Permissions)
		configureToolkitSecurityExtensions(kit, generation.host)
		kit.ConfigureSurfaceForProviderModel(s.ProviderName, s.Model, true)
		kit.SetBrowserEnabled(browserEnabledFromEnv())
		kit.SetToolSearchEnabled(s.ToolSearchEnabled)
		kit.SetNativeDeferredToolDiscovery(s.NativeDeferredToolDiscovery)
		kit.SetGitAttributionEnabled(cfg.Agent.GitAttributionEnabledValue())
		bindToolkitHooks(kit, generation.hooks)
		kit.SetFileScopeRoots(workspaces.BoundaryRoots(kit.RootDir(), s.WuuHome))
		manager, err := startMCPManager(cfg, generation.active, required, generation.mcpOAuth)
		if err != nil {
			return toolservice.Backend{}, err
		}
		kit.SetMCPManager(manager)
		codeMode := codemode.NewService(codemode.ServiceConfig{NodeExecutable: cfg.PTC.NodeExecutable})
		kit.ConfigurePTC(codeMode, cfg.PTC)
		executor := newPluginToolExecutor(kit, generation.host, "", s.RootDir)
		if err := configureToolReplacements(executor, kit, cfg.Agent.ToolReplacements); err != nil {
			if manager != nil {
				_ = manager.Close()
			}
			_ = codeMode.Close()
			return toolservice.Backend{}, err
		}
		generation.toolkit, generation.codeMode, generation.mcp = kit, codeMode, manager
		return toolservice.Backend{Executor: executor, Toolkit: kit, Close: func(context.Context) error {
			var err error
			if manager != nil {
				err = manager.Close()
			}
			return errors.Join(err, codeMode.Close())
		}}, nil
	})
	seal := func() error {
		admissionMu.Lock()
		defer admissionMu.Unlock()
		closed = true
		if inFlight {
			return errors.New("native tool backend initialization did not finish during workspace admission")
		}
		return nil
	}
	return scope, seal
}

// newThreadNativeToolkit creates a thread toolkit using dependencies shared by
// its pinned generation. Call it only when the selected provider delegates.
func (s *Session) newThreadNativeToolkit(generation *PluginGeneration, root string) (*tools.Toolkit, func(context.Context) error, error) {
	if generation == nil {
		return nil, nil, errors.New("native toolkit requires a captured plugin generation")
	}
	kit, err := tools.New(root)
	if err != nil {
		return nil, nil, err
	}
	cfg := generation.settings
	kit.SetStateDir(s.StateDir)
	kit.SetRuntimeHome(s.WuuHome)
	kit.SetWorkingNotesHome(s.WuuHome)
	kit.SetWorkspaceID(s.WorkspaceID)
	kit.SetActivityRegistry(s.ActivityRegistry)
	if generation.state != nil {
		kit.SetSessionStore(generation.state.sessions)
	}
	kit.SetArtifactPublisher(NewArtifactPublisher(generation.artifactClient))
	kit.SetSkillsWithAvailability(generation.skills, generation.skillAvailable)
	ConfigureToolkitPermissions(kit, s.Permissions)
	configureToolkitSecurityExtensions(kit, generation.host)
	ruleProvider := s.ModelRoles.Main.RuleProvider
	if ruleProvider == "" {
		ruleProvider = s.ProviderName
	}
	apiModel := s.ModelRoles.Main.APIModel
	if apiModel == "" {
		apiModel = s.Model
	}
	kit.ConfigureSurfaceForProviderModel(ruleProvider, apiModel, true)
	kit.SetBrowserEnabled(browserEnabledFromEnv())
	kit.SetToolSearchEnabled(s.ToolSearchEnabled)
	kit.SetNativeDeferredToolDiscovery(s.NativeDeferredToolDiscovery)
	kit.SetGitAttributionEnabled(cfg.Agent.GitAttributionEnabledValue())
	bindToolkitHooks(kit, generation.hooks)
	kit.SetFileScopeRoots(workspaces.BoundaryRoots(kit.RootDir(), s.WuuHome))
	manager, codeMode, err := generation.nativeDependencies.get(generation)
	if err != nil {
		return nil, nil, err
	}
	kit.SetMCPManager(manager)
	kit.SetMCPActivityBindings(generation.mcpBinding)
	kit.ConfigurePTC(codeMode, cfg.PTC)
	// Thread scope closes its own executor. Shared dependencies stay with the
	// pinned generation until its last admitted scope and call have drained.
	return kit, func(context.Context) error { return nil }, nil
}
