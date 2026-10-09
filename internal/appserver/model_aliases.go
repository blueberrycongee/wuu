package appserver

import (
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
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return agentcontrol.AliasResolutionResult{}
	}
	if s == nil || s.rt == nil {
		return agentcontrol.AliasResolutionResult{Err: fmt.Errorf("runtime is not initialized")}
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return agentcontrol.AliasResolutionResult{Err: fmt.Errorf("load effective config: %w", err)}
	}
	validAliases := normalizedModelAliasNames(cfg.Agent.ModelAliases)
	configuredAlias := configuredModelAliasExists(cfg.Agent.ModelAliases, alias)
	if !configuredAlias && alias != "@verification" && alias != "@worker" {
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
		selection = s.rt.ModelRoles.Verification
	} else {
		selection = s.rt.ModelRoles.Worker
	}
	client, err := providerfactory.BuildStreamClient(selection.RuleProviderConfig, selection.Provider)
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
	if s.rt.StreamRunner != nil {
		resolved.Temperature = s.rt.StreamRunner.Temperature
		resolved.CompactThresholdPct = s.rt.StreamRunner.CompactThresholdPct
		resolved.CompactKeepRecentTokens = s.rt.StreamRunner.CompactKeepRecentTokens
		resolved.DisableAutoCompact = s.rt.StreamRunner.DisableAutoCompact
	}
	return agentcontrol.AliasResolutionResult{Found: true, Runtime: resolved}
}

func (s *Server) resolveSubagentProviderClient(providerName string) (providers.StreamClient, error) {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return nil, fmt.Errorf("provider is required")
	}
	if s == nil || s.rt == nil {
		return nil, fmt.Errorf("runtime is not initialized")
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return nil, fmt.Errorf("load effective config: %w", err)
	}
	providerCfg, ok := cfg.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("provider %q is not configured", providerName)
	}
	providerCfg = s.withCachedCodexModels(providerName, providerCfg)
	client, err := providerfactory.BuildStreamClient(providerCfg, providerName)
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
