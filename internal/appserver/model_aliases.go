package appserver

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

func (s *Server) resolveSubagentModelAlias(alias string) agentcontrol.AliasResolutionResult {
	if strings.TrimSpace(alias) == "" {
		return agentcontrol.AliasResolutionResult{}
	}
	if s == nil || s.rt == nil {
		return agentcontrol.AliasResolutionResult{Err: fmt.Errorf("runtime is not initialized")}
	}
	binding, err := s.rt.RetainState()
	if err != nil {
		return agentcontrol.AliasResolutionResult{Err: err}
	}
	defer binding.Close()
	services, err := binding.Services()
	if err != nil {
		return agentcontrol.AliasResolutionResult{Err: err}
	}
	models, err := binding.Models()
	if err != nil {
		return agentcontrol.AliasResolutionResult{Err: err}
	}
	return s.resolveSubagentModelAliasWithServices(services, models, alias)
}

func (s *Server) resolveSubagentModelAliasWithServices(services *runtime.StateServices, models runtime.StateModels, alias string) agentcontrol.AliasResolutionResult {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return agentcontrol.AliasResolutionResult{}
	}
	view, err := services.Config.Read(context.Background())
	cfg := view.Config
	if err != nil {
		return agentcontrol.AliasResolutionResult{Err: fmt.Errorf("load effective config: %w", err)}
	}
	validAliases := normalizedModelAliasNames(cfg.Agent.ModelAliases)
	configuredAlias := configuredModelAliasExists(cfg.Agent.ModelAliases, alias)
	if !configuredAlias && alias != "@verification" {
		return agentcontrol.AliasResolutionResult{
			Unknown:      true,
			ValidAliases: validAliases,
		}
	}
	var selection modelroles.Selection
	if configuredAlias {
		selection, err = modelroles.ResolveAlias(cfg, alias)
		if err != nil {
			return agentcontrol.AliasResolutionResult{Err: err}
		}
	} else if alias == "@verification" {
		selection = models.Roles.Verification
	}
	client, err := providerfactory.BuildStreamClient(selection.RuleProviderConfig, selection.Provider, providerfactory.Bindings{Credentials: services.Credentials, ChatGPT: services.ChatGPT})
	if err != nil {
		return agentcontrol.AliasResolutionResult{
			Err: fmt.Errorf("build provider %q client: %w", selection.Provider, err),
		}
	}
	budget := runtime.ResolveModelBudget(selection.Model, selection.RuleProviderConfig, cfg.Agent.MaxContextTokens)
	resolved := subagent.WorkerRuntime{
		Provider:               selection.Provider,
		Model:                  selection.Model,
		APIModel:               selection.APIModel,
		Effort:                 selection.LegacyEffort,
		Variant:                selection.Variant,
		ProviderOptions:        selection.ProviderOptions,
		ContextWindow:          budget.ContextWindowTokens,
		MaxInputTokens:         budget.InputLimitTokens,
		OutputReserveTokens:    budget.OutputReserveTokens,
		CompactThresholdTokens: budget.CompactThresholdTokens,
		Client:                 client,
	}
	resolved.Temperature = models.Temperature
	resolved.CompactThresholdPct = models.CompactThresholdPct
	resolved.CompactKeepRecentTokens = models.CompactKeepRecentTokens
	resolved.DisableAutoCompact = models.DisableAutoCompact
	return agentcontrol.AliasResolutionResult{Found: true, Runtime: resolved}
}

func (s *Server) resolveSubagentProviderClientWithServices(services *runtime.StateServices, providerName string) (providers.StreamClient, error) {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return nil, fmt.Errorf("provider is required")
	}
	if s == nil || s.rt == nil {
		return nil, fmt.Errorf("runtime is not initialized")
	}
	view, err := services.Config.Read(context.Background())
	cfg := view.Config
	if err != nil {
		return nil, fmt.Errorf("load effective config: %w", err)
	}
	providerCfg, ok := cfg.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("provider %q is not configured", providerName)
	}
	providerCfg = s.withCachedCodexModels(providerName, providerCfg)
	client, err := providerfactory.BuildStreamClient(providerCfg, providerName, providerfactory.Bindings{Credentials: services.Credentials, ChatGPT: services.ChatGPT})
	if err != nil {
		return nil, fmt.Errorf("build provider %q client: %w", providerName, err)
	}
	return client, nil
}

func normalizedModelAliasNames(aliases map[string]config.ModelRoleConfig) []string {
	if len(aliases) == 0 {
		return nil
	}
	names := make([]string, 0, len(aliases))
	for name := range aliases {
		name = strings.TrimSpace(name)
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func configuredModelAliasExists(aliases map[string]config.ModelRoleConfig, requested string) bool {
	for name := range aliases {
		if strings.TrimSpace(name) == requested {
			return true
		}
	}
	return false
}
