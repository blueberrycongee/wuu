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

type generationModelBindings struct {
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
	if s.StreamRunner == nil || s.configClient == nil || s.SafeMode {
		return nil // Explicitly embedded runtimes own their injected clients.
	}
	models := &generationModelBindings{}
	g.models = models
	defer func() {
		if err != nil {
			unavailable := &providers.UnavailableClient{Reason: err.Error()}
			models.main, models.worker, models.title = unavailable, unavailable, unavailable
			models.err = err
		}
	}()
	view, err := g.configClient.Read(context.Background())
	if err != nil {
		return fmt.Errorf("read generation model configuration: %w", err)
	}
	cfg := withDesktopExecutionDefaults(view.Config, s.DesktopExecutionDefaults)
	provider, name, err := cfg.ResolveProvider(s.ProviderName)
	if err != nil {
		return err
	}
	roles, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{ProviderName: name, ProviderConfig: provider, Model: s.Model, Effort: cfg.Agent.Effort, Variant: cfg.Agent.Variant})
	if err != nil {
		return err
	}
	build := func(role modelroles.Selection) (providers.StreamClient, error) {
		client, err := providerfactory.BuildRuntimeStreamClient(role.RuleProviderConfig, role.Provider, g.chatGPTManager)
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
