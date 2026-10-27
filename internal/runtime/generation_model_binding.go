package runtime

import (
	"context"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type generationModelSelection struct{ provider, model string }

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
	providerName, model := s.ProviderName, s.Model
	if g.modelSelection != nil {
		providerName, model = g.modelSelection.provider, g.modelSelection.model
	}
	provider, name, err := cfg.ResolveProvider(providerName)
	if err != nil {
		return err
	}
	roles, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{ProviderName: name, ProviderConfig: provider, Model: model, Effort: cfg.Agent.Effort, Variant: cfg.Agent.Variant})
	if err != nil {
		return err
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
	g.hooks = buildHookDispatcher(g.settings, g.active, models.title, s.Model, nil)
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
func (s *Session) RefreshModelConfiguration(cfg config.Config, provider, model string) error {
	candidate, err := s.PreflightExtensions(cfg)
	if err != nil {
		return err
	}
	candidate.modelSelection = &generationModelSelection{provider: provider, model: model}
	return s.ActivatePluginGeneration(candidate, nil)
}
