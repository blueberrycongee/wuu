package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/workspaces"
)

// NewThreadCapabilityRuntime binds host browser and CUA capabilities to an
// external-engine conversation. It does not resolve model providers, initialize
// native workers, or create a StreamRunner. The caller releases the returned
// PluginGeneration with the conversation's ordinary runtime lifecycle.
func (s *Session) NewThreadCapabilityRuntime(sessionID, rootDir string, selected ThreadModelSelection) (*ThreadRuntime, error) {
	if s == nil {
		return nil, fmt.Errorf("runtime session is required")
	}
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return nil, fmt.Errorf("session id is required")
	}
	root := strings.TrimSpace(rootDir)
	if root == "" {
		root = s.RootDir
	}
	selection := ThreadModelSelection{
		Provider: strings.TrimSpace(selected.Provider), Model: strings.TrimSpace(selected.Model),
		Variant: strings.TrimSpace(selected.Variant), Effort: strings.TrimSpace(selected.Effort),
		Speed: selected.Speed, PermissionMode: strings.TrimSpace(selected.PermissionMode),
	}
	if selection.PermissionMode == "" || s.PermissionModeExplicit {
		selection.PermissionMode = s.Permissions.Mode
	}
	selection.PermissionMode = config.NormalizePermissionMode(selection.PermissionMode)

	// Publication changes the root toolkit in place. Clone and retain under the
	// same lock so a conversation never combines tools and hooks from different
	// generations; the clone owns every mutable per-thread environment field.
	s.pluginGenerationMu.Lock()
	var kit *tools.Toolkit
	if s.Toolkit != nil {
		var err error
		kit, err = s.Toolkit.CloneForRoot(root)
		if err != nil {
			s.pluginGenerationMu.Unlock()
			return nil, err
		}
	}
	documents, sessions, host := s.Documents, s.Sessions, s.PluginHost
	artifacts := s.artifactClient
	generation := s.pluginGeneration
	generation.retain()
	dispatcher := s.HookDispatcher
	if generation != nil {
		dispatcher = generation.hooks
	}
	s.pluginGenerationMu.Unlock()
	retained := false
	defer func() {
		if !retained {
			s.releasePluginGeneration(generation)
		}
	}()
	result := &ThreadRuntime{SessionID: id,
		Toolkit: kit, ActivityRegistry: s.ActivityRegistry,
		Selection: selection, PluginGeneration: generation,
		Documents: documents, Sessions: sessions, PluginHost: host,
	}

	stateDir, err := s.executionStateDir()
	if err != nil {
		return nil, fmt.Errorf("resolve workspace state directory: %w", err)
	}
	wuuHome := strings.TrimSpace(s.WuuHome)
	if wuuHome == "" {
		wuuHome, err = statepath.Home("")
		if err != nil {
			return nil, fmt.Errorf("resolve wuu home: %w", err)
		}
	}
	artifactDir := statepath.SessionArtifactDir(stateDir, id)
	if kit == nil {
		manager, err := s.processManagerForThread(root, stateDir)
		if err != nil {
			return nil, err
		}
		environment, err := s.selectedExecutionEnvironment(id, artifactDir)
		if err != nil {
			return nil, err
		}
		if remote, ok := environment.(*environmentToolExecutor); ok {
			manager = remote.ProcessManager()
		}
		result.ProcessManager = manager
		result.processScopeConfig = tools.ProcessScopeConfig{Manager: manager, ExecutionEnvironment: environment, GitAttributionEnabled: processGitAttribution(generation), RootDir: root, StateDir: stateDir, SessionDir: artifactDir, ThreadID: id, ActorID: id, ActorPath: agentthread.RootPath, FileScopeRoots: workspaces.BoundaryRoots(root, wuuHome, artifactDir)}
		result.processHooks = dispatcher
		if host != nil && selectsRuntimeModule(host, toolExecutionConsumer) {
			result.CapabilityTools = newPluginAwareToolExecutor(nil, host, dispatcher, id, id, root)
		}
		result.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: selection.PermissionMode}, false, nil)
		retained = true
		return result, nil
	}
	kit.ConfigureExternalCapabilities()
	ConfigureToolkitPermissions(kit, config.ResolvedPermissions{Mode: selection.PermissionMode})
	kit.SetApproveForMe(false)
	kit.SetStateDir(stateDir)
	kit.SetArtifactPublisher(NewArtifactPublisher(artifacts))
	kit.SetSessionID(id)
	kit.SetSessionDir(artifactDir)
	kit.SetSessionStore(sessions)
	kit.SetWorkingNotesHome(wuuHome)
	browserTabs, err := selectedBrowserTabStore(documents, wuuHome, s.SessionDir, stateDir, id)
	if err != nil {
		return nil, err
	}
	kit.SetBrowserTabs(browserTabs)
	kit.SetImageInputSupported(true)
	kit.SetAgentIdentity(id, agentthread.RootPath)
	kit.SetFileScopeRoots(workspaces.BoundaryRoots(kit.RootDir(), wuuHome, artifactDir))
	if dispatcher != nil {
		kit.SetPermissionRequestHook(func(ctx context.Context, active *tools.Toolkit, _ tools.ToolInfo, call providers.ToolCall) error {
			_, err := dispatcher.Dispatch(ctx, hooks.PermissionRequest, &hooks.Input{
				SessionID: active.SessionID(), CWD: active.RootDir(), ToolName: call.Name,
				ToolInput: json.RawMessage(call.Arguments),
			})
			return err
		})
	}
	// The selected registry owns the capability surface for external engines too.
	result.CapabilityTools = newPluginAwareToolExecutor(kit, host, dispatcher, id, id, kit.RootDir())
	retained = true
	return result, nil
}
