package runtime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// CollaborationRuntimeVersion is persisted by collaboration session owners.
// Changing the execution contract requires a new version; ordinary extension
// generations never replace an already constructed collaboration environment.
const CollaborationRuntimeVersion = "collaboration/v1"

// newCollaborationSession deliberately constructs a new execution environment.
// Cloning an interactive runner or toolkit and clearing selected callbacks is
// unsafe: new extension seams would silently become inherited dependencies.
func (s *Session) newCollaborationSession(rootDir, orientation string, selected ThreadModelSelection) (*Session, error) {
	if s == nil || s.StreamRunner == nil {
		return nil, errors.New("runtime stream runner is required")
	}
	stateDir, err := resolveWorkspaceStateDir(s.WuuHome, "", rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve collaboration state directory: %w", err)
	}
	processManager, err := s.processManagerForThread(rootDir, stateDir)
	if err != nil {
		return nil, fmt.Errorf("collaboration process manager: %w", err)
	}
	model, client, err := s.collaborationModel(selected)
	if err != nil {
		return nil, err
	}
	permissionMode := strings.TrimSpace(selected.PermissionMode)
	if permissionMode == "" || s.PermissionModeExplicit {
		permissionMode = s.Permissions.Mode
	}
	permissions := config.ResolvedPermissions{Mode: config.NormalizePermissionMode(permissionMode)}
	// Model limits come from the selected provider, while execution and
	// compaction behavior use the built-in contract, never interactive settings.
	budget := ResolveModelBudget(model.Model, model.RuleProviderConfig, 0)
	preference := config.Default().Agent.ToolLoadingPreference()
	loadingMode, searchEnabled, deferredDiscovery := resolveToolLoadingModeForProvider(preference, model.RuleProviderConfig, model.APIModel, model.ProviderOptions)
	kit, err := tools.New(rootDir)
	if err != nil {
		return nil, err
	}
	kit.ConfigureSurfaceForProviderModel(model.RuleProvider, model.APIModel, true)
	kit.SetToolSearchEnabled(searchEnabled)
	kit.SetNativeDeferredToolDiscovery(deferredDiscovery)
	kit.SetActivityRegistry(s.ActivityRegistry)
	kit.SetBrowserEnabled(browserEnabledFromEnv())
	ConfigureToolkitPermissions(kit, permissions)
	worker := model
	worker.Role = modelroles.RoleWorker
	worker.Inherited = true
	base := &Session{
		ProviderName:                model.Provider,
		Model:                       model.Model,
		RootDir:                     rootDir,
		Host:                        s.Host,
		StateDir:                    stateDir,
		SessionDir:                  s.SessionDir,
		WuuHome:                     s.WuuHome,
		SessionDate:                 s.SessionDate,
		Toolkit:                     kit,
		ProcessManager:              processManager,
		threadProcesses:             s.threadProcessManagerPool(),
		ActivityRegistry:            s.ActivityRegistry,
		InferenceJournalRuntime:     s.InferenceJournalRuntime,
		HookDispatcher:              hooks.NewDispatcher(nil),
		WorkerClient:                client,
		ModelRoles:                  modelroles.Set{Main: model, Worker: worker},
		ModelBudget:                 budget,
		WorkerModelBudget:           budget,
		Permissions:                 permissions,
		PermissionModeExplicit:      s.PermissionModeExplicit,
		maxParallel:                 s.MaxParallel(),
		ToolLoadingPreference:       preference,
		ToolLoadingMode:             loadingMode,
		ToolSearchEnabled:           searchEnabled,
		NativeDeferredToolDiscovery: deferredDiscovery,
		workerOrientation:           strings.TrimSpace(orientation),
		StreamRunner: &agent.StreamRunner{
			Client:                      client,
			ProviderName:                model.Provider,
			ProviderObservationKey:      agent.BuildProviderObservationKey(model.Provider, model.RuleProviderConfig.BaseURL, model.RuleProviderConfig.Type, model.RuleProviderConfig.API, model.RuleProviderConfig.WireAPI, model.RuleProviderConfig.StreamTransport, agent.ProviderUsageNormalizationKey(model.RuleProviderConfig.CacheCreationInputTokensOmitted, model.RuleProviderConfig.InputTokensIncludeCacheRead)),
			Model:                       model.Model,
			APIModel:                    model.APIModel,
			MediaInput:                  mediaInputPolicyFromCapabilities(model.Capabilities),
			Effort:                      model.LegacyEffort,
			Variant:                     model.Variant,
			ProviderOptions:             provideroptions.Clone(model.ProviderOptions),
			NativeDeferredToolDiscovery: deferredDiscovery,
			ContextWindowOverride:       budget.ContextWindowTokens,
			MaxInputTokens:              budget.InputLimitTokens,
			OutputReserveTokens:         budget.OutputReserveTokens,
			CompactThresholdTokens:      budget.CompactThresholdTokens,
			InferenceOperationKind:      providers.InferenceOperationAgentRound,
			InferenceWorkloadProfile:    providers.InferenceProfileBackgroundAgent,
		},
	}
	return base, nil
}

func (s *Session) collaborationModel(selected ThreadModelSelection) (modelroles.Selection, providers.StreamClient, error) {
	provider := strings.TrimSpace(selected.Provider)
	model := strings.TrimSpace(selected.Model)
	variant := strings.TrimSpace(selected.Variant)
	effort := strings.TrimSpace(selected.Effort)
	if provider == "" {
		provider = s.ProviderName
	}
	if model == "" {
		model = s.Model
	}
	// Reusing a transport is safe: it carries credentials and wire behavior,
	// not prompts, tools, hooks, drivers, or mutable conversation history.
	if provider == s.ProviderName && model == s.StreamRunner.Model && (s.ModelRoles.Empty() || s.ModelRoles.Main.Model == model) && variant == strings.TrimSpace(s.StreamRunner.Variant) && effort == strings.TrimSpace(s.StreamRunner.Effort) {
		selection := s.ModelRoles.Main
		if s.ModelRoles.Empty() {
			// Embedded callers may supply a transport without a model catalog.
			// Preserve that transport's explicit identity and modality contract.
			selection = modelroles.Selection{
				Role: modelroles.RoleMain, Provider: provider, Model: model,
				APIModel: strings.TrimSpace(s.StreamRunner.APIModel), RuleProvider: provider,
				Variant: variant, LegacyEffort: effort, ProviderOptions: s.StreamRunner.ProviderOptions,
				Capabilities: modelroles.Capabilities{
					ImageInput:      s.StreamRunner.MediaInput.Image,
					FileInput:       s.StreamRunner.MediaInput.File,
					ImageInputKnown: s.StreamRunner.MediaInput.ImageKnown,
					FileInputKnown:  s.StreamRunner.MediaInput.FileKnown,
				},
			}
			if selection.APIModel == "" {
				selection.APIModel = model
			}
		}
		selection.ProviderOptions = modelvariant.CloneOptions(selection.ProviderOptions)
		return selection, s.StreamRunner.Client, nil
	}
	cfg, _, err := s.LoadEffectiveConfig()
	if err != nil {
		return modelroles.Selection{}, nil, err
	}
	providerCfg, provider, err := cfg.ResolveProvider(provider)
	if err != nil {
		return modelroles.Selection{}, nil, fmt.Errorf("%w: %v", ErrThreadProviderUnavailable, err)
	}
	// Resolve only this session's model. Ordinary worker role
	// overrides must not silently replace the identity's selected BYOK model.
	cfg.Agent = config.Default().Agent
	roles, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{
		ProviderName: provider, ProviderConfig: providerCfg, Model: model,
		Variant: variant, Effort: effort,
	})
	if err != nil {
		return modelroles.Selection{}, nil, err
	}
	client, err := providerfactory.BuildStreamClient(roles.Main.RuleProviderConfig, roles.Main.Provider)
	if err != nil {
		return modelroles.Selection{}, nil, fmt.Errorf("build collaboration model client: %w", err)
	}
	return roles.Main, client, nil
}
