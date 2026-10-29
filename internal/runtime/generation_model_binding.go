package runtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
)

var ErrStaleModelPublication = errors.New("model runtime changed before publication")

type ResolvedModelPublication struct {
	RuleProviderName   string
	RuleProviderConfig config.ProviderConfig
	Selection          modelvariant.Selection
	Roles              modelroles.Set
	Explicit           bool
}

type generationModelSelection struct {
	basisVersion    uint64
	provider, model string
	permissions     *config.ResolvedPermissions
	resolved        *ResolvedModelPublication
	expectedConfig  *config.Config
}

type generationToolLoading struct {
	preference, mode config.ToolLoadingMode
	search, native   bool
	catalog          string
}

type generationModelBindings struct {
	selection    *generationModelSelection
	main, worker providers.StreamClient
	title        providers.Client
	roles        modelroles.Set
	config       config.Config
	issues       []ReadinessIssue
	err          error
}

// Authentication and transport clients belong to the graph that supplied their
// configuration. Build outside the publication lock; old runners keep their
// captured clients until their retained generation finishes cleanup.
func (s *Session) bindGenerationModels(g *PluginGeneration) (err error) {
	if s.StreamRunner == nil || g.configClient == nil || s.SafeMode {
		return nil // A graph without configuration cannot resolve model roles.
	}
	models := &generationModelBindings{selection: g.modelSelection}
	defer func() {
		if err != nil {
			unavailable := &providers.UnavailableClient{Reason: err.Error()}
			models.main, models.worker, models.title = unavailable, unavailable, unavailable
			models.err = err
		}
		g.revokedMu.Lock()
		g.models = models
		g.revokedMu.Unlock()
	}()
	view, err := g.configClient.Read(context.Background())
	if err != nil {
		return fmt.Errorf("read generation model configuration: %w", err)
	}
	cfg, err := s.applyRuntimeConfig(view.Config)
	if err != nil {
		return err
	}
	if !cfg.RequiresModelConfiguration() && len(cfg.Providers) == 0 && g.modelSelection == nil {
		// Keep the same graph route, but never fabricate a native connection.
		// Calls to an unselected model service fail closed at dispatch.
		client := &modelRouteClient{host: g.host}
		models.main, models.worker, models.title, models.config = client, client, client, cfg
		return nil
	}
	var providerName, model string
	if g.modelSelection != nil {
		providerName, model = g.modelSelection.provider, g.modelSelection.model
	} else {
		basis, captureErr := s.CaptureAdvancedRuntime()
		if captureErr != nil {
			return captureErr
		}
		providerName, model = basis.ProviderName, basis.Model
	}
	provider, name, err := cfg.ResolveProvider(providerName)
	if err != nil {
		return err
	}
	roles, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{ProviderName: name, ProviderConfig: provider, Model: model, Effort: cfg.Agent.Effort, Variant: cfg.Agent.Variant})
	if err != nil {
		return err
	}
	if selection := g.modelSelection; selection != nil && selection.resolved != nil {
		expected, compareErr := s.applyRuntimeConfig(*selection.expectedConfig)
		if compareErr != nil {
			return compareErr
		}
		if !reflect.DeepEqual(cfg, expected) {
			return ErrStaleModelPublication
		}
		resolved := selection.resolved
		roles = cloneStateModels(StateModels{Roles: resolved.Roles}).Roles
		if resolved.Explicit {
			roles.Main.APIModel = modelcatalog.APIModel(resolved.RuleProviderConfig, model)
			roles.Main.RuleProvider = resolved.RuleProviderName
			roles.Main.RuleProviderConfig = resolved.RuleProviderConfig
			roles.Main.LegacyEffort, roles.Main.Variant = resolved.Selection.LegacyEffort, resolved.Selection.Variant
			roles.Main.ProviderOptions = modelvariant.CloneOptions(resolved.Selection.ProviderOptions)
		}
	}
	build := func(role modelroles.Selection) (providers.StreamClient, error) {
		client, err := selectedRouteClient(g.host, role.RuleProviderConfig, role.Provider, providerfactory.Bindings{Credentials: g.credentials, ChatGPT: g.chatGPTManager})
		if client != nil && err != nil {
			models.issues = append(models.issues, ReadinessIssue{Code: "credential_missing", Provider: role.Provider, Message: err.Error()})
			return client, nil
		}
		return client, err
	}
	if models.main, err = build(roles.Main); err != nil {
		return err
	}
	models.title = models.main
	if !roles.Title.Inherited {
		if models.title, err = build(roles.Title); err != nil {
			return err
		}
	}
	if models.worker, err = build(roles.Worker); err != nil {
		return err
	}
	models.roles, models.config = roles, cfg
	if g.modelSelection != nil {
		preference := cfg.Agent.ToolLoadingPreference()
		mode, search, native := resolveToolLoadingModeForProvider(preference, roles.Main.RuleProviderConfig, roles.Main.APIModel, roles.Main.ProviderOptions)
		g.modelToolLoading = &generationToolLoading{preference: preference, mode: mode, search: search, native: native}
	}
	g.hooks = buildHookDispatcher(g.settings, g.active, models.title, model, nil)
	return nil
}

func (s *Session) applyGenerationModels(models *generationModelBindings) {
	if models == nil {
		return
	}
	s.TitleClient, s.WorkerClient = models.title, models.worker
	s.ReadinessIssues = models.issues
	if s.StreamRunner != nil {
		s.StreamRunner.Client = models.main
	}
	if models.err != nil {
		s.ReadinessIssues = []ReadinessIssue{{Code: "configuration_unavailable", Message: models.err.Error()}}
		return
	}
	if models.selection != nil {
		s.ProviderName, s.Model = models.roles.Main.Provider, models.roles.Main.Model
		if s.StreamRunner != nil {
			s.StreamRunner.ProviderName, s.StreamRunner.Model = s.ProviderName, s.Model
		}
	}
	s.ModelRoles = models.roles
	s.ProjectModels = models.config.Agent.ProjectModels
	s.ModelBudget = ResolveModelBudget(s.Model, models.roles.Main.RuleProviderConfig, models.config.Agent.MaxContextTokens)
	s.WorkerModelBudget = ResolveModelBudget(models.roles.Worker.Model, models.roles.Worker.RuleProviderConfig, models.config.Agent.MaxContextTokens)
	if runner := s.StreamRunner; runner != nil {
		main := models.roles.Main
		provider := main.RuleProviderConfig
		runner.ProviderObservationKey = agent.BuildProviderObservationKey(main.Provider, provider.BaseURL, provider.Type, provider.API, provider.WireAPI, provider.StreamTransport, agent.ProviderUsageNormalizationKey(provider.CacheCreationInputTokensOmitted, provider.InputTokensIncludeCacheRead))
		runner.APIModel = main.APIModel
		runner.MediaInput = mediaInputPolicyFromCapabilities(main.Capabilities)
		runner.Effort, runner.Variant = main.LegacyEffort, main.Variant
		runner.ProviderOptions = modelvariant.CloneOptions(main.ProviderOptions)
		runner.ContextWindowOverride = s.ModelBudget.ContextWindowTokens
		runner.MaxInputTokens = s.ModelBudget.InputLimitTokens
		runner.OutputReserveTokens = s.ModelBudget.OutputReserveTokens
		runner.CompactThresholdTokens = s.ModelBudget.CompactThresholdTokens
	}
}

// RefreshModelConfiguration prepares the saved provider configuration without
// changing live selection. Selection and clients publish with the generation;
// existing consumers continue using their captured configuration and clients.
func (s *Session) RefreshModelConfiguration(cfg config.Config, provider, model string, permissions ...config.ResolvedPermissions) error {
	return s.refreshResolvedModelConfiguration(cfg, provider, model, nil, permissions...)
}

// RefreshResolvedModelConfiguration includes host-resolved dynamic model data
// while preserving the selected configuration provider as the authority.
func (s *Session) RefreshResolvedModelConfiguration(cfg config.Config, provider, model string, resolved ResolvedModelPublication, permissions ...config.ResolvedPermissions) error {
	resolved.Roles = cloneStateModels(StateModels{Roles: resolved.Roles}).Roles
	resolved.Selection.ProviderOptions = modelvariant.CloneOptions(resolved.Selection.ProviderOptions)
	return s.refreshResolvedModelConfiguration(cfg, provider, model, &resolved, permissions...)
}

func (s *Session) refreshResolvedModelConfiguration(cfg config.Config, provider, model string, resolved *ResolvedModelPublication, permissions ...config.ResolvedPermissions) error {
	basisVersion := s.runtimePublicationVersion.Load()
	preparation, err := s.preparePluginCatalog(cfg, discoverPlugins(s.RootDir, s.WuuHome), nil, nil, startPluginClient)
	if err != nil {
		return err
	}
	selection := &generationModelSelection{provider: provider, model: model, basisVersion: basisVersion, resolved: resolved}
	if resolved != nil {
		selection.expectedConfig = &cfg
	}
	if len(permissions) != 0 {
		value := permissions[0]
		selection.permissions = &value
	}
	preparation.modelSelection = selection
	candidate, err := preparation.Build(context.Background())
	if err != nil {
		return err
	}
	return s.ActivatePluginGeneration(candidate, nil)
}
