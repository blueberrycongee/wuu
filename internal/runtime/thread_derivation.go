package runtime

import (
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelbudget"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// ThreadModelDerivation is the derived, non-persistent model state for one
// conversation's selection: the model/worker budgets and the resolved worker
// role plus its stream client.
//
// Per the selection / derivation / override model (issue #81) these are a pure
// function of (selection, config): they are NEVER independent state to be
// copied from the workspace runtime. Any code that needs a conversation's
// budget or worker default must recompute it here from that conversation's own
// selection, so a workspace-default change (advanced settings, another session
// switching model) can never leak the workspace model's budget or worker
// provider onto a pinned conversation.
type ThreadModelDerivation struct {
	Provider    string
	Model       string
	APIModel    string
	ModelBudget modelbudget.Budget

	WorkerProvider    string
	WorkerAPIModel    string
	WorkerEffort      string
	WorkerOptions     map[string]any
	WorkerModelBudget modelbudget.Budget
	WorkerClient      providers.StreamClient
}

// DeriveThreadModel resolves the derived model state for a conversation's
// selection against cfg. Empty selection fields fall back to the workspace
// runtime's provider/model (a conversation that follows the workspace default),
// so the result is the workspace derivation in that case and the conversation's
// own derivation when it is pinned. It mirrors the model resolution in
// NewThreadRuntimeForRootModel; the two must stay in lockstep.
func (s *Session) DeriveThreadModel(cfg config.Config, selected ThreadModelSelection) (ThreadModelDerivation, error) {
	providerName := strings.TrimSpace(selected.Provider)
	if providerName == "" {
		providerName = s.ProviderName
	}
	model := strings.TrimSpace(selected.Model)
	if model == "" {
		model = s.Model
	}
	providerCfg, resolvedName, err := cfg.ResolveProvider(providerName)
	if err != nil {
		return ThreadModelDerivation{}, err
	}
	ruleProviderName, ruleProviderCfg := modelcatalog.EnrichProvider(resolvedName, providerCfg, model)
	apiModel := modelcatalog.APIModel(ruleProviderCfg, model)
	selection := modelvariant.ResolveForProvider(ruleProviderName, ruleProviderCfg, model, strings.TrimSpace(selected.Variant), strings.TrimSpace(selected.Effort))
	if err := modelvariant.ApplySpeed(ruleProviderCfg, model, selected.Speed, &selection); err != nil {
		return ThreadModelDerivation{}, err
	}
	roles, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{
		ProviderName:   resolvedName,
		ProviderConfig: providerCfg,
		Model:          model,
		Effort:         selection.LegacyEffort,
		Variant:        selection.Variant,
	})
	if err != nil {
		return ThreadModelDerivation{}, err
	}
	workerClient, err := providerfactory.BuildStreamClient(roles.Worker.RuleProviderConfig, roles.Worker.Provider)
	if err != nil {
		return ThreadModelDerivation{}, err
	}
	return ThreadModelDerivation{
		Provider:          resolvedName,
		Model:             model,
		APIModel:          apiModel,
		ModelBudget:       ResolveModelBudget(model, ruleProviderCfg, cfg.Agent.MaxContextTokens),
		WorkerProvider:    roles.Worker.Provider,
		WorkerAPIModel:    roles.Worker.APIModel,
		WorkerEffort:      roles.Worker.LegacyEffort,
		WorkerOptions:     modelvariant.CloneOptions(roles.Worker.ProviderOptions),
		WorkerModelBudget: ResolveModelBudget(roles.Worker.Model, roles.Worker.RuleProviderConfig, cfg.Agent.MaxContextTokens),
		WorkerClient:      workerClient,
	}, nil
}
