package runtime

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/promptservice"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// PrepareNextStepModel validates a candidate against this runtime's retained
// generation and existing tool authority. It does not persist or publish it.
func (r *ThreadRuntime) PrepareNextStepModel(ctx context.Context, selected session.RuntimeSelection) error {
	_, err := r.prepareNextStepModelBinding(ctx, selected)
	return err
}

// Intent writers cannot attest engine or driver support. Admission and durable
// consumption share this gate against the runtime's retained generation.
func (r *ThreadRuntime) prepareNextStepModelBinding(ctx context.Context, selected session.RuntimeSelection) (*agent.ModelStepBinding, error) {
	if r == nil || r.prepareStepModel == nil || r.StreamRunner == nil {
		return nil, fmt.Errorf("runtime does not support next-step model selection")
	}
	if r.PluginHost == nil {
		return nil, fmt.Errorf("selected engine is unavailable for next-step model selection")
	}
	var engine selectedEngineDescriptor
	engineID := agentengine.NormalizeEngineID(string(r.EngineID))
	if err := callSelectedEngine(ctx, r.PluginHost, engineID, "descriptor", struct{}{}, &engine); err != nil {
		return nil, err
	}
	if engine.ID != engineID || !engine.Enabled || !slices.Contains(engine.Capabilities, "host-managed-continuation") || !slices.Contains(engine.Capabilities, "step-model-selection") {
		return nil, fmt.Errorf("selected engine does not support next-step model selection")
	}
	driver := r.StreamRunner.LoopDriver
	if r.StreamRunner.AcquireLoopDriver != nil {
		var expected *loopdriver.Binding
		if r.StreamRunner.DriverBindingStore != nil {
			binding, found, err := r.StreamRunner.DriverBindingStore.LoadBinding(ctx)
			if err != nil {
				return nil, err
			}
			if found {
				expected = &binding
			}
		}
		resolved, _, release, err := r.StreamRunner.AcquireLoopDriver(ctx, expected)
		if err != nil {
			return nil, err
		}
		defer release()
		driver = resolved
	}
	if driver == nil {
		return nil, fmt.Errorf("next-step model selection requires an available loop driver")
	}
	descriptor, err := driver.Descriptor(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(descriptor.Capabilities, "step_model_selection") {
		return nil, fmt.Errorf("selected loop driver does not support next-step model selection")
	}
	return r.prepareStepModel(ctx, selected)
}

func (s *Session) prepareStepModelBinding(ctx context.Context, cfg config.Config, runner *agent.StreamRunner, host *pluginhost.Host, id, root string, promptInput *promptservice.Request, selected session.RuntimeSelection) (*agent.ModelStepBinding, error) {
	if selected.PermissionMode != "" && config.NormalizePermissionMode(selected.PermissionMode) != s.Permissions.Mode {
		return nil, fmt.Errorf("next-step model selection cannot change execution permissions")
	}
	provider, name, err := cfg.ResolveProvider(strings.TrimSpace(selected.Provider))
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(selected.Model)
	configured, known := provider.Models[model]
	if model == "" || (model != provider.Model && !known) {
		return nil, fmt.Errorf("model %q is not a configured choice for provider %q", model, name)
	}
	if configured.Disabled {
		return nil, fmt.Errorf("model %q is disabled", model)
	}
	ruleName, rule := modelcatalog.EnrichProvider(name, provider, model)
	if selected.Variant != "" {
		if _, ok := modelvariant.OptionsForProvider(ruleName, rule, model, selected.Variant); !ok {
			return nil, fmt.Errorf("model %s does not support variant %s", model, selected.Variant)
		}
	}
	selection := modelvariant.ResolveForProvider(ruleName, rule, model, selected.Variant, selected.Effort)
	if err := modelvariant.ApplySpeed(rule, model, selected.Speed, &selection); err != nil {
		return nil, err
	}
	caps, _ := modelroles.BuildFacts(ruleName, rule, model)
	var definitions []providers.ToolDefinition
	if executor, ok := runner.Tools.(agent.ContextualToolDefinitions); ok {
		definitions, err = executor.DefinitionsContext(ctx)
	} else if runner.Tools != nil {
		definitions = runner.Tools.Definitions()
	}
	if err != nil {
		return nil, err
	}
	if len(definitions) > 0 && !caps.Tools {
		return nil, fmt.Errorf("model %q does not support the active tool surface", model)
	}
	apiModel := modelcatalog.APIModel(rule, model)
	if err := providers.ValidateToolDefinitionsForProvider(providers.ToolSurfaceValidationTarget{ProviderKind: rule.Type, ProviderName: name, Model: apiModel}, definitions); err != nil {
		return nil, err
	}
	rule.Model = model
	client, err := selectedRouteClient(host, rule, name, providerfactory.Bindings{Credentials: s.credentials, ChatGPT: s.chatGPTManager})
	if err != nil {
		return nil, err
	}
	surface, err := selectedToolSurface(ctx, runner.Tools)
	if err != nil {
		return nil, err
	}
	instructions, skills := s.guidanceForRoot(root, s.pluginGeneration)
	input := promptservice.Request{Role: "main", CWD: root, Date: s.SessionDate, Provider: name, Model: apiModel, Surface: surface, Instructions: instructions, Skills: skills}
	if promptInput != nil {
		input = *promptInput
		input.Provider = name
		input.Model = apiModel
		input.Surface = surface
	}
	// Tool authority and execution-environment contributions remain those of the
	// admitted runtime; changing a model never reconstructs an executor.
	prompt, err := assembleSelectedPrompt(ctx, host, input, s.systemPrompts)
	if err != nil {
		return nil, err
	}
	budget := ResolveModelBudget(model, rule, cfg.Agent.MaxContextTokens)
	_, _, nativeDeferred := resolveToolLoadingForProvider(cfg.Agent, rule, apiModel, selection.ProviderOptions)
	return &agent.ModelStepBinding{
		Client: client, Provider: name, Model: model, APIModel: apiModel,
		ProviderObservationKey: agent.BuildProviderObservationKey(name, rule.BaseURL, rule.Type, rule.API, rule.WireAPI, rule.StreamTransport, agent.ProviderUsageNormalizationKey(rule.CacheCreationInputTokensOmitted, rule.InputTokensIncludeCacheRead)),
		Variant:                selection.Variant, Effort: selection.LegacyEffort, ProviderOptions: modelvariant.CloneOptions(selection.ProviderOptions), MediaInput: mediaInputPolicyFromCapabilities(caps),
		NativeDeferredToolDiscovery: runner.NativeDeferredToolDiscovery && nativeDeferred,
		SystemPrompt:                prompt.Content, SystemPromptSections: agentPromptSections(prompt.Sections),
		ContextWindowTokens: budget.ContextWindowTokens, InputTokens: budget.InputLimitTokens, OutputReserveTokens: budget.OutputReserveTokens, CompactThresholdTokens: budget.CompactThresholdTokens,
		BeforeModelStep: pluginPreStepInjector(host, name, model, id, root), StepPolicy: pluginStepPolicy(host, name, model, id, root), BeforeRequest: pluginRequestInterceptor(host, name, id, root),
	}, nil
}
