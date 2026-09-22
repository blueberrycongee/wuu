package runtime

import (
	"fmt"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

type workerModelSurface struct {
	provider, model string
	search, native  bool
	surface         capability.Surface
}

// ApplyAutoModel changes only the next root execution and future worker
// defaults. Existing workers, processes, leases and tool ledgers stay alive.
// Call exclusively between root executions while holding the session lease.
func (s *Session) ApplyAutoModel(rt *ThreadRuntime, cfg config.Config, selected config.ModelRoleConfig) error {
	if rt == nil || rt.StreamRunner == nil {
		return fmt.Errorf("Auto requires the Wuu engine")
	}
	if rt.ExecutionProfile == CollaborationRuntimeVersion {
		cfg.Agent = config.Default().Agent
	}
	p, name, err := cfg.ResolveProvider(selected.Provider)
	if err != nil {
		return err
	}
	if model, ok := p.Models[selected.Model]; ok && model.Disabled {
		return fmt.Errorf("Auto execution model %q is disabled", selected.Model)
	}
	roles, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{ProviderName: name, ProviderConfig: p, Model: selected.Model, Effort: selected.Effort, Variant: selected.Variant})
	if err != nil {
		return err
	}
	main := roles.Main
	client, err := providerfactory.BuildStreamClient(main.RuleProviderConfig, name)
	if err != nil {
		return err
	}
	worker := roles.Worker
	workerClient, err := providerfactory.BuildStreamClient(worker.RuleProviderConfig, worker.Provider)
	if err != nil {
		return err
	}
	if rt.refreshWorkerModel != nil {
		if err := rt.refreshWorkerModel(worker); err != nil {
			return err
		}
	}
	budget := ResolveModelBudget(main.Model, main.RuleProviderConfig, cfg.Agent.MaxContextTokens)
	workerBudget := ResolveModelBudget(worker.Model, worker.RuleProviderConfig, cfg.Agent.MaxContextTokens)
	_, search, native := resolveToolLoadingForProvider(cfg.Agent, main.RuleProviderConfig, main.APIModel, main.ProviderOptions)
	if rt.Toolkit != nil {
		rt.Toolkit.SetImageInputSupported(main.Capabilities.ImageInput)
		rt.Toolkit.ConfigureSurfaceForProviderModel(main.RuleProvider, main.APIModel, true)
		rt.Toolkit.SetToolSearchEnabled(search)
		rt.Toolkit.SetNativeDeferredToolDiscovery(native)
	}
	r := rt.StreamRunner
	r.UpdateProviderConnection(client, agent.BuildProviderObservationKey(name, p.BaseURL, p.Type, p.API, p.WireAPI, p.StreamTransport, agent.ProviderUsageNormalizationKey(p.CacheCreationInputTokensOmitted, p.InputTokensIncludeCacheRead)))
	r.ProviderName, r.Model, r.APIModel = name, main.Model, main.APIModel
	r.Effort, r.Variant, r.ProviderOptions = main.LegacyEffort, main.Variant, main.ProviderOptions
	r.MediaInput = mediaInputPolicyFromCapabilities(main.Capabilities)
	r.NativeDeferredToolDiscovery = native
	r.ContextWindowOverride, r.MaxInputTokens = budget.ContextWindowTokens, budget.InputLimitTokens
	r.OutputReserveTokens, r.CompactThresholdTokens = budget.OutputReserveTokens, budget.CompactThresholdTokens
	rt.ModelBudget, rt.WorkerModelBudget = budget, workerBudget
	if rt.AgentControl != nil {
		rt.AgentControl.UpdateWorkerDefaults(workerClient, worker.APIModel, subagent.ManagerOptions{
			DefaultProviderName: worker.Provider, DefaultEffort: worker.LegacyEffort, DefaultProviderOptions: worker.ProviderOptions,
			ContextWindowOverride: workerBudget.ContextWindowTokens, MaxInputTokens: workerBudget.InputLimitTokens,
			OutputReserveTokens: workerBudget.OutputReserveTokens, CompactThresholdTokens: workerBudget.CompactThresholdTokens,
			Temperature: r.Temperature, CompactThresholdPct: r.CompactThresholdPct, CompactKeepRecentTokens: r.CompactKeepRecentTokens, DisableAutoCompact: r.DisableAutoCompact,
		})
	}
	if rt.refreshModelPrompt != nil {
		return rt.refreshModelPrompt()
	}
	return nil
}
