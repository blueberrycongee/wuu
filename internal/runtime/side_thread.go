package runtime

import (
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/workspaces"
)

const sideThreadSystemPrompt = `You are a read-only side agent attached to a coding task. Answer the user's questions from the supplied snapshot of the main task and the side-conversation history.

The snapshot may have been captured while the main task was still running. Describe only what the snapshot proves, distinguish completed work from in-progress work, and say when the current outcome is not yet known. Do not claim to have inspected state newer than the snapshot.

Use your read-only tools when current workspace evidence is needed. You may inspect files, search code, and perform other non-mutating investigation. You must not modify files, run mutating commands, start agents, or steer the main task. Keep the answer direct and useful.`

// NewSideThreadRunner clones the attached conversation's model configuration
// into an isolated runner with the same tool surface as a main agent in
// read-only mode. selected is the main conversation's
// pinned model selection, so a side chat answers with the same model the
// conversation is locked to rather than the workspace default (which drifts
// whenever another session switches model). Its toolkit is separately cloned,
// rooted at the main thread's workspace, and protected by ReadOnlyBoundary.
// It shares no mutable callbacks, tool ledger, or usage tracker with the main
// thread.
func (s *Session) NewSideThreadRunner(sideThreadID, rootDir string, selected ThreadModelSelection) (*agent.StreamRunner, error) {
	if s == nil || s.StreamRunner == nil {
		return nil, errors.New("stream runner is required")
	}
	id := strings.TrimSpace(sideThreadID)
	if id == "" {
		return nil, errors.New("side thread id is required")
	}

	if selected.Model == config.AutoModelID {
		cfg, _, err := s.LoadEffectiveConfig()
		if err != nil {
			return nil, err
		}
		initial, err := cfg.AutoModelSelection()
		if err != nil {
			return nil, err
		}
		selected.Provider, selected.Model, selected.Variant, selected.Effort = initial.Provider, initial.Model, initial.Variant, initial.Effort
	}
	runner := s.newSideThreadBaseRunner(selected)
	root := strings.TrimSpace(rootDir)
	if root == "" {
		root = s.RootDir
	}
	if s.Toolkit != nil {
		kit, err := s.Toolkit.CloneForRoot(root)
		if err != nil {
			return nil, err
		}
		model := strings.TrimSpace(runner.APIModel)
		if model == "" {
			model = strings.TrimSpace(runner.Model)
		}
		kit.ConfigureSurfaceForProviderModel(runner.ProviderName, model, true)
		ConfigureToolkitPermissions(kit, config.ResolvedPermissions{Mode: config.PermissionModeReadOnly})
		kit.SetSessionID(id)
		kit.SetAgentIdentity(id, agentthread.RootPath)
		kit.SetFileScopeRoots(workspaces.BoundaryRoots(kit.RootDir(), s.WuuHome))

		var toolExecutor agent.ToolExecutor = newPluginAwareToolExecutor(kit, s.PluginHost, s.HookDispatcher, id, "", root)
		runner.Tools = toolExecutor

		surface := kit.ActiveSurface()
		promptParts := []string{sideThreadSystemPrompt, strings.TrimSpace(surface.SystemFragment)}
		if catalog, err := deferredToolCatalogPromptForToolkit(kit); err != nil {
			return nil, err
		} else {
			promptParts = append(promptParts, strings.TrimSpace(catalog))
		}
		runner.SystemPrompt = joinNonEmptyPromptParts(promptParts...)
	} else {
		runner.Tools = nil
		runner.SystemPrompt = sideThreadSystemPrompt
	}
	runner.ToolLedger = nil
	runner.SystemPromptSections = nil
	runner.OnEvent = nil
	runner.OnUsage = nil
	runner.OnTokenUsage = nil
	runner.StreamingToolExecution = false
	runner.BeforeStep = nil
	runner.BeforeModelStep = nil
	runner.BeforeRequestContext = nil
	runner.BeforeRequest = nil
	runner.OnRequestContext = nil
	runner.OnCompactAttempt = nil
	runner.OnToolBatchRejected = nil
	runner.AfterTurn = nil
	runner.ForceToolFirstStep = ""
	runner.ForceInitialCompact = false
	runner.CompactOnly = false
	runner.PromptCacheKey = "side-thread:" + id
	runner.InferenceJournal = s.InferenceJournalForOwner("side-thread:" + id)
	return runner, nil
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

// newSideThreadBaseRunner clones the workspace stream runner and repins it to
// the main conversation's model selection. It is best-effort: an empty
// selection, a selection that already equals the workspace defaults, or any
// failure to resolve the pinned model (no loadable config, a removed provider,
// a client that will not build) all fall back to the plain workspace clone.
// A read-only side chat must never fail to open just because the conversation's
// pinned model can no longer be resolved — the workspace runner is always a
// usable answer, and the main thread self-heals to those same defaults on its
// next turn. This mirrors the model pinning in NewThreadRuntimeForRootModel,
// minus the toolkit/worker/permission wiring a side chat has no use for.
func (s *Session) newSideThreadBaseRunner(selected ThreadModelSelection) *agent.StreamRunner {
	providerName := strings.TrimSpace(selected.Provider)
	model := strings.TrimSpace(selected.Model)
	currentVariant := strings.TrimSpace(s.StreamRunner.Variant)
	currentEffort := strings.TrimSpace(s.StreamRunner.Effort)
	if providerName == "" || model == "" ||
		(providerName == s.ProviderName && model == s.Model &&
			strings.TrimSpace(selected.Variant) == currentVariant &&
			strings.TrimSpace(selected.Effort) == currentEffort) {
		return cloneStreamRunnerForThread(s.StreamRunner, nil)
	}

	cfg, _, err := s.LoadEffectiveConfig()
	if err != nil {
		providers.DebugLogf("side thread base runner falling back to workspace model (config unavailable): %v", err)
		return cloneStreamRunnerForThread(s.StreamRunner, nil)
	}
	providerCfg, resolvedName, err := cfg.ResolveProvider(providerName)
	if err != nil {
		providers.DebugLogf("side thread base runner falling back to workspace model (%q unavailable): %v", providerName, err)
		return cloneStreamRunnerForThread(s.StreamRunner, nil)
	}
	ruleProviderName, ruleProviderCfg := modelcatalog.EnrichProvider(resolvedName, providerCfg, model)
	selection := modelvariant.ResolveForProvider(ruleProviderName, ruleProviderCfg, model, strings.TrimSpace(selected.Variant), strings.TrimSpace(selected.Effort))
	client, err := providerfactory.BuildStreamClient(ruleProviderCfg, resolvedName)
	if err != nil {
		providers.DebugLogf("side thread base runner falling back to workspace model (build client for %q failed): %v", resolvedName, err)
		return cloneStreamRunnerForThread(s.StreamRunner, nil)
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
	return runner
}
