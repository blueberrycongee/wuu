package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

// NewSideThreadRunner clones the attached conversation's model configuration
// into an isolated runner with the same tool surface as a main agent in
// read-only mode. selected is the main conversation's
// pinned model selection, so a side chat answers with the same model the
// conversation is locked to rather than the workspace default (which drifts
// whenever another session switches model). Its toolkit is separately cloned,
// rooted at the main thread's workspace, and protected by ReadOnlyBoundary.
// It shares no mutable callbacks, tool ledger, or usage tracker with the main
// thread.
func (s *Session) NewSideThreadRunner(sideThreadID, rootDir string, selected ThreadModelSelection, systemPrompt string, parentSession ...string) (*agent.StreamRunner, error) {
	shadow, err := s.cloneForThreadModel()
	if err != nil {
		return nil, err
	}
	defer s.releasePluginGeneration(shadow.pluginGeneration)
	return shadow.newSideThreadRunner(sideThreadID, rootDir, selected, systemPrompt, parentSession...)
}

func (s *Session) newSideThreadRunner(sideThreadID, rootDir string, selected ThreadModelSelection, systemPrompt string, parentSession ...string) (*agent.StreamRunner, error) {
	return s.newSideThreadRunnerWithArtifacts(sideThreadID, rootDir, selected, systemPrompt, nil, parentSession...)
}

func (s *Session) newSideThreadRunnerWithArtifacts(sideThreadID, rootDir string, selected ThreadModelSelection, systemPrompt string, release *func() error, parentSession ...string) (*agent.StreamRunner, error) {
	var runnerOwned bool
	var constructed *agent.StreamRunner
	var acquired func() error
	var openLate func()
	defer func() {
		if !runnerOwned && (constructed != nil || acquired != nil) {
			generation := s.pluginGeneration
			if generation != nil {
				generation.retain()
			}
			s.runNativeCleanup(func() {
				_ = CloseSideThreadTools(context.Background(), constructed)
				if acquired != nil {
					_ = acquired()
				}
				if generation != nil {
					_ = s.releasePluginGeneration(generation)
				}
			})
		}
	}()
	if s == nil || s.StreamRunner == nil {
		return nil, errors.New("stream runner is required")
	}
	id := strings.TrimSpace(sideThreadID)
	if id == "" {
		return nil, errors.New("side thread id is required")
	}

	if strings.TrimSpace(systemPrompt) == "" {
		return nil, errors.New("side conversation prompt is required")
	}
	runner, err := s.newSideThreadBaseRunner(selected)
	if err != nil {
		return nil, err
	}
	constructed = runner
	root := strings.TrimSpace(rootDir)
	if root == "" {
		root = s.RootDir
	}
	if s.Toolkit != nil {
		kit, err := s.Toolkit.CloneForRoot(root)
		if err != nil {
			return nil, err
		}
		_, threadSkills, generation := s.skillSnapshotForRoot(kit.RootDir(), nil)
		kit.SetSkillsWithAvailability(threadSkills, generation.skillAvailable)
		s.bindLocalSkillRefresh(kit, kit.RootDir(), generation)
		model := strings.TrimSpace(runner.APIModel)
		if model == "" {
			model = strings.TrimSpace(runner.Model)
		}
		kit.ConfigureSurfaceForProviderModel(runner.ProviderName, model, true)
		ConfigureToolkitPermissions(kit, config.ResolvedPermissions{Mode: config.PermissionModeReadOnly})
		kit.SetSessionID(id)
		// Side conversations do not resume from managed-process completion.
		// Keep their requested foreground wait instead of yielding early.
		kit.SetShellEarlyHandoffEnabled(false)
		if len(parentSession) > 0 {
			kit.SetCodeModeStateOwner(parentSession[0])
		}
		kit.SetAgentIdentity(id, agentthread.RootPath)
		kit.SetFileScopeRoots(runtimeFileScopeRoots(kit.RootDir(), s.WuuHome, s.UserBinding()))
		if len(parentSession) > 0 {
			stateDir, err := s.executionStateDir()
			if err != nil {
				return nil, err
			}
			artifactDir := statepath.SessionArtifactDir(stateDir, parentSession[0])
			if release != nil {
				acquired, err = kit.BindSessionArtifacts(artifactDir, s.sessionArtifactManager())
				if err != nil {
					return nil, err
				}
			}
			if err := s.configureExecutionEnvironment(kit, parentSession[0], artifactDir); err != nil {
				return nil, err
			}
		}

		var toolExecutor agent.ToolExecutor = newPluginAwareToolExecutor(kit, s.PluginHost, s.HookDispatcher, id, "", root)
		runner.Tools = toolExecutor

		surface, surfaceErr := selectedToolSurface(context.Background(), toolExecutor)
		if surfaceErr != nil {
			return nil, surfaceErr
		}
		promptParts := []string{systemPrompt, strings.TrimSpace(surface.SystemFragment)}
		promptParts = append(promptParts, strings.TrimSpace(surface.DeferredToolCatalog))
		runner.SystemPrompt = joinNonEmptyPromptParts(promptParts...)
	} else if s.PluginHost != nil && s.StreamRunner.Tools != nil {
		generation := s.pluginGeneration
		var admissionMu sync.Mutex
		var inFlight, attempted, succeeded, admissionClosed, lateOpen bool
		sideArtifacts := &artifactLeaseOwner{}
		if release != nil {
			acquired = sideArtifacts.close
		}
		openLate = func() {
			admissionMu.Lock()
			lateOpen = release != nil
			admissionMu.Unlock()
		}
		scope := toolservice.NewScope(func(ctx context.Context) (toolservice.Backend, error) {
			completed := false
			admissionMu.Lock()
			if admissionClosed && !lateOpen {
				admissionMu.Unlock()
				return toolservice.Backend{}, errors.New("native tool backend was not initialized during side thread admission")
			}
			attempted, inFlight = true, true
			admissionMu.Unlock()
			defer func() {
				admissionMu.Lock()
				inFlight = false
				succeeded = completed
				admissionMu.Unlock()
			}()
			kit, closeResources, err := s.newThreadNativeToolkit(ctx, generation, root)
			if err != nil {
				return toolservice.Backend{}, err
			}
			ok := false
			defer func() {
				if !ok {
					_ = sideArtifacts.close()
					_ = closeResources(context.Background())
				}
			}()
			_, threadSkills, skillGeneration := s.skillSnapshotForRoot(kit.RootDir(), generation)
			kit.SetSkillsWithAvailability(threadSkills, skillGeneration.skillAvailable)
			if skillGeneration != nil && skillGeneration.managedSkillDiscovery {
				s.bindLocalSkillRefresh(kit, kit.RootDir(), skillGeneration)
			}
			model := strings.TrimSpace(runner.APIModel)
			if model == "" {
				model = strings.TrimSpace(runner.Model)
			}
			kit.ConfigureSurfaceForProviderModel(runner.ProviderName, model, true)
			ConfigureToolkitPermissions(kit, config.ResolvedPermissions{Mode: config.PermissionModeReadOnly})
			kit.SetSessionID(id)
			kit.SetShellEarlyHandoffEnabled(false)
			kit.SetAgentIdentity(id, agentthread.RootPath)
			kit.SetFileScopeRoots(runtimeFileScopeRoots(kit.RootDir(), s.WuuHome, s.UserBinding()))
			if len(parentSession) > 0 {
				kit.SetCodeModeStateOwner(parentSession[0])
				stateDir, err := s.executionStateDir()
				if err != nil {
					return toolservice.Backend{}, err
				}
				artifactDir := statepath.SessionArtifactDir(stateDir, parentSession[0])
				if release != nil {
					sideArtifacts.mu.Lock()
					if sideArtifacts.closed {
						sideArtifacts.mu.Unlock()
						return toolservice.Backend{}, errors.New("side thread artifact owner is closed")
					}
					sideArtifacts.release, err = kit.BindSessionArtifacts(artifactDir, s.sessionArtifactManager())
					sideArtifacts.mu.Unlock()
					if err != nil {
						return toolservice.Backend{}, err
					}
				}
				if err := s.configureExecutionEnvironment(kit, parentSession[0], artifactDir); err != nil {
					return toolservice.Backend{}, err
				}
			}
			executor := newPluginToolExecutor(kit, s.PluginHost, id, root)
			if err := configureToolReplacements(executor, kit, generation.settings.Agent.ToolReplacements); err != nil {
				return toolservice.Backend{}, err
			}
			ok = true
			completed = true
			return toolservice.Backend{Executor: executor, Toolkit: kit, Close: closeResources}, nil
		})
		runner.Tools = newSelectedToolExecutor(s.PluginHost, scope, selectedToolIdentity{threadID: id, cwd: root, actorID: id})
		if s.HookDispatcher != nil {
			runner.Tools = hooks.NewHookedExecutor(runner.Tools, s.HookDispatcher, "", root)
		}
		surface, err := selectedToolSurface(context.Background(), runner.Tools)
		admissionMu.Lock()
		admissionClosed = true
		initializing := inFlight || (attempted && !succeeded)
		admissionMu.Unlock()
		if err != nil {
			return nil, err
		}
		if initializing {
			return nil, errors.New("native tool backend initialization did not finish during side thread admission")
		}
		runner.SystemPrompt = joinNonEmptyPromptParts(systemPrompt, surface.SystemFragment, surface.DeferredToolCatalog)
	} else {
		runner.Tools = nil
		runner.SystemPrompt = systemPrompt
	}
	runner.ToolLedger = nil
	runner.SystemPromptSections = nil
	runner.OnEvent = nil
	runner.OnUsage = nil
	runner.OnTokenUsage = nil
	runner.StreamingToolExecution = false
	runner.BeforeStep = nil
	runner.BeforeModelStep = pluginPreStepInjector(s.PluginHost, runner.ProviderName, runner.Model, id, root)
	runner.StepPolicy = pluginStepPolicy(s.PluginHost, runner.ProviderName, runner.Model, id, root)
	runner.BeforeRequestContext = nil
	runner.BeforeRequest = pluginRequestInterceptor(s.PluginHost, runner.ProviderName, id, root)
	runner.OnRequestContext = nil
	runner.OnCompactAttempt = nil
	runner.OnToolBatchRejected = nil
	runner.AfterTurn = nil
	runner.ForceToolFirstStep = ""
	runner.ForceInitialCompact = false
	runner.CompactOnly = false
	runner.PromptCacheKey = "side-thread:" + id
	runner.InferenceJournal = s.InferenceJournalForOwner("side-thread:" + id)
	if openLate != nil {
		openLate()
	}
	runnerOwned = true
	if release != nil {
		*release = acquired
		acquired = nil
	}
	return runner, nil
}

// CloseSideThreadTools releases the side runner's native scope after its turn
// and finalizers finish. It does not close a provider shared with other scopes.
func CloseSideThreadTools(ctx context.Context, runner *agent.StreamRunner) error {
	if runner == nil {
		return nil
	}
	scope := selectedNativeToolScope(runner.Tools)
	if scope == nil {
		return nil
	}
	return scope.Close(ctx)
}

func joinNonEmptyPromptParts(parts ...string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			nonEmpty = append(nonEmpty, trimmed)
		}
	}
	return strings.Join(nonEmpty, "\n\n")
}

// newSideThreadBaseRunner pins the captured runner to the conversation's model.
// An explicit selection that cannot be resolved fails before any inference.
func (s *Session) newSideThreadBaseRunner(selected ThreadModelSelection) (*agent.StreamRunner, error) {
	providerName := strings.TrimSpace(selected.Provider)
	model := strings.TrimSpace(selected.Model)
	currentVariant := strings.TrimSpace(s.StreamRunner.Variant)
	currentEffort := strings.TrimSpace(s.StreamRunner.Effort)
	if (providerName == "" && model == "") ||
		(selected.Speed == "" && providerName == s.ProviderName && model == s.Model &&
			strings.TrimSpace(selected.Variant) == currentVariant &&
			strings.TrimSpace(selected.Effort) == currentEffort) {
		return cloneStreamRunnerForThread(s.StreamRunner, nil), nil
	}
	if providerName == "" || model == "" {
		return nil, errors.New("side conversation model selection requires provider and model")
	}

	cfg, _, err := s.LoadEffectiveConfig()
	if err != nil {
		return nil, fmt.Errorf("load side conversation model configuration: %w", err)
	}
	providerCfg, resolvedName, err := cfg.ResolveProvider(providerName)
	if err != nil {
		return nil, fmt.Errorf("resolve side conversation provider %q: %w", providerName, err)
	}
	ruleProviderName, ruleProviderCfg := modelcatalog.EnrichProvider(resolvedName, providerCfg, model)
	selection := modelvariant.ResolveForProvider(ruleProviderName, ruleProviderCfg, model, strings.TrimSpace(selected.Variant), strings.TrimSpace(selected.Effort))
	if err := modelvariant.ApplySpeed(ruleProviderCfg, model, selected.Speed, &selection); err != nil {
		return nil, fmt.Errorf("resolve side conversation speed: %w", err)
	}
	client, err := selectedRouteClient(s.PluginHost, ruleProviderCfg, resolvedName, providerfactory.Bindings{Credentials: s.credentials, ChatGPT: s.chatGPTManager})
	if err != nil {
		return nil, fmt.Errorf("build side conversation client %q: %w", resolvedName, err)
	}
	budget := ResolveModelBudget(model, ruleProviderCfg, cfg.Agent.MaxContextTokens)
	apiModel := modelcatalog.APIModel(ruleProviderCfg, model)

	runner := cloneStreamRunnerForThread(s.StreamRunner, nil)
	runner.Client = client
	runner.ProviderName = resolvedName
	runner.ProviderObservationKey = agent.BuildProviderObservationKey(resolvedName, ruleProviderCfg.BaseURL, ruleProviderCfg.Type, ruleProviderCfg.API, ruleProviderCfg.WireAPI, ruleProviderCfg.StreamTransport, agent.ProviderUsageNormalizationKey(ruleProviderCfg.CacheCreationInputTokensOmitted, ruleProviderCfg.InputTokensIncludeCacheRead))
	runner.Model = model
	runner.APIModel = apiModel
	// The cloned runner inherits the workspace model's media admission
	// policy; re-derive it for the pinned model so unsupported images do
	// not inherit a permissive base-model policy (same trap as
	// NewThreadRuntimeForRootModel's shadow path).
	capabilities, _ := modelroles.BuildFacts(resolvedName, providerCfg, model)
	runner.MediaInput = mediaInputPolicyFromCapabilities(capabilities)
	runner.Effort = selection.LegacyEffort
	runner.Variant = selection.Variant
	runner.ProviderOptions = modelvariant.CloneOptions(selection.ProviderOptions)
	runner.ContextWindowOverride = budget.ContextWindowTokens
	runner.MaxInputTokens = budget.InputLimitTokens
	runner.OutputReserveTokens = budget.OutputReserveTokens
	runner.CompactThresholdTokens = budget.CompactThresholdTokens
	return runner, nil
}
