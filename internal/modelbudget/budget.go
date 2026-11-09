package modelbudget

import (
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
)

type Source string

const (
	SourceUnknown               Source = "unknown"
	SourceProviderContextWindow Source = "provider_context_window"
	SourceProviderModelLimit    Source = "provider_model_limit"
	SourceProviderInputLimit    Source = "provider_input_limit"
	SourceAgentOverride         Source = "agent_max_context_tokens"
)

const CodexSubscriptionGPT5InputCap = 272_000

// CodexSubscriptionGPT6InputCap is the ChatGPT/Codex prompt cap for GPT-6
// family models. Codex currently advertises the same 272k input ceiling as GPT-5.
const CodexSubscriptionGPT6InputCap = 272_000

// CodexSubscriptionGPT5ContextWindow is the ChatGPT/Codex advertised window for
// GPT-5 family models that are not in the 1.05M GPT-5.6 generation.
const CodexSubscriptionGPT5ContextWindow = 400_000

// CodexSubscriptionGPT6ContextWindow is the ChatGPT/Codex advertised window for
// GPT-6 and GPT-5.6 family models.
const CodexSubscriptionGPT6ContextWindow = 1_050_000

// compactOutputReserveCapTokens caps how much of the prompt ceiling is held
// back for the response when deriving the compact threshold. Real answers and
// compact summaries rarely exceed ~20k even when the model's output limit is
// far larger, so reserving the full output limit (e.g. 128k of a 1M window)
// would waste usable context. Mirrors Claude Code's effective-window rule
// (window - min(max_output, 20k)).
const compactOutputReserveCapTokens = 20_000

// compactInputBufferTokens is the safety margin below the prompt ceiling at
// which proactive compaction fires, so the summarize request itself (history +
// summary output) still fits comfortably. Mirrors Claude Code's 13k autocompact
// buffer. Scaled down to ceiling/8 for small windows so tiny local models are
// not compacted constantly.
const compactInputBufferTokens = 13_000

type Budget struct {
	Model                  string
	APIModel               string
	ContextWindowTokens    int
	InputLimitTokens       int
	OutputLimitTokens      int
	OutputReserveTokens    int
	UsableInputTokens      int
	CompactThresholdTokens int
	ContextWindowSource    Source
	ContextWindowKnown     bool
}

func Resolve(model string, provider config.ProviderConfig, agentOverride int) Budget {
	model = strings.TrimSpace(model)
	apiModel := modelcatalog.APIModel(provider, model)
	if apiModel == "" {
		apiModel = model
	}

	context, source := resolveContextWindow(model, apiModel, provider, agentOverride)
	input := resolveInputWindow(model, apiModel, provider)
	output := configuredModelOutputLimit(model, provider)
	outputReserve := output
	// The prompt ceiling is the context window, clamped by a published
	// input cap when the channel has one (e.g. Codex subscription 272k).
	// The input cap is a clamp, never an independent anchor: budget and
	// display must derive from the same ceiling or they drift apart.
	ceiling := context
	if input > 0 && (ceiling <= 0 || input < ceiling) {
		ceiling = input
	}
	usable := 0
	if ceiling > 0 {
		reserve := outputReserve
		if reserve > compactOutputReserveCapTokens {
			reserve = compactOutputReserveCapTokens
		}
		buffer := compactInputBufferTokens
		if buffer > ceiling/8 {
			buffer = ceiling / 8
		}
		usable = max(0, ceiling-reserve-buffer)
	}

	return Budget{
		Model:                  model,
		APIModel:               apiModel,
		ContextWindowTokens:    context,
		InputLimitTokens:       input,
		OutputLimitTokens:      output,
		OutputReserveTokens:    outputReserve,
		UsableInputTokens:      usable,
		CompactThresholdTokens: usable,
		ContextWindowSource:    source,
		ContextWindowKnown:     source != SourceUnknown && context > 0,
	}
}

// EffectiveContextWindow resolves the user-visible context ceiling for the
// current provider channel. Some channels publish a prompt/input cap that is
// lower than, or available without, the model's full context window. The UI
// should show the smaller effective cap because that is the limit users can
// actually fill with retained context.
func (b Budget) EffectiveContextWindow() (int, Source) {
	if b.InputLimitTokens > 0 && (b.ContextWindowTokens <= 0 || b.InputLimitTokens < b.ContextWindowTokens) {
		return b.InputLimitTokens, SourceProviderInputLimit
	}
	if b.ContextWindowTokens > 0 {
		return b.ContextWindowTokens, b.ContextWindowSource
	}
	return 0, SourceUnknown
}

func resolveContextWindow(model, apiModel string, provider config.ProviderConfig, agentOverride int) (int, Source) {
	// Provider.ContextWindow is an explicit user override. It is allowed to
	// exceed discovered model metadata; the upstream provider remains the
	// authority and will reject an unsupported request naturally.
	if provider.ContextWindow > 0 {
		return provider.ContextWindow, SourceProviderContextWindow
	}
	if limit := configuredModelContextLimit(model, provider); limit > 0 {
		return limit, SourceProviderModelLimit
	}
	if agentOverride > 0 {
		return agentOverride, SourceAgentOverride
	}
	return 0, SourceUnknown
}

func resolveInputWindow(model, apiModel string, provider config.ProviderConfig) int {
	// An explicit provider context override replaces the discovered effective
	// ceiling as a whole. Keeping a catalog or subscription input cap here would
	// silently force the UI/runtime back to the automatic value.
	if provider.ContextWindow > 0 {
		return 0
	}
	if limit := configuredModelInputLimit(model, provider); limit > 0 {
		if cap := codexSubscriptionInputCapForModels(model, apiModel, provider.Type); cap > 0 && cap < limit {
			return cap
		}
		return limit
	}
	if cap := codexSubscriptionInputCapForModels(model, apiModel, provider.Type); cap > 0 {
		return cap
	}
	return 0
}

func configuredModelContextLimit(model string, provider config.ProviderConfig) int {
	for _, cfg := range configuredModelCandidates(model, provider) {
		if cfg.ContextWindow > 0 {
			return cfg.ContextWindow
		}
		if cfg.Limit != nil && cfg.Limit.Context > 0 {
			return cfg.Limit.Context
		}
	}
	return 0
}

func configuredModelInputLimit(model string, provider config.ProviderConfig) int {
	for _, cfg := range configuredModelCandidates(model, provider) {
		if cfg.Limit != nil && cfg.Limit.Input > 0 {
			return cfg.Limit.Input
		}
	}
	return 0
}

func configuredModelOutputLimit(model string, provider config.ProviderConfig) int {
	for _, cfg := range configuredModelCandidates(model, provider) {
		if cfg.Limit != nil && cfg.Limit.Output > 0 {
			return cfg.Limit.Output
		}
	}
	return 0
}

func configuredModelCandidates(model string, provider config.ProviderConfig) []config.ProviderModelConfig {
	model = strings.TrimSpace(model)
	if model == "" || len(provider.Models) == 0 {
		return nil
	}
	out := make([]config.ProviderModelConfig, 0, 2)
	if cfg, ok := provider.Models[model]; ok {
		out = append(out, cfg)
	}
	apiModel := modelcatalog.APIModel(provider, model)
	if apiModel != "" && apiModel != model {
		if cfg, ok := provider.Models[apiModel]; ok {
			out = append(out, cfg)
		}
	}
	return out
}

func CodexSubscriptionInputCap(model, providerType string) int {
	if !isCodexSubscriptionProviderType(providerType) {
		return 0
	}
	switch {
	case isCodexSubscriptionGPT6Model(model):
		return CodexSubscriptionGPT6InputCap
	case isCodexSubscriptionGPT5Model(model):
		return CodexSubscriptionGPT5InputCap
	default:
		return 0
	}
}

func CodexSubscriptionContextWindow(model, providerType string) int {
	if !isCodexSubscriptionProviderType(providerType) {
		return 0
	}
	switch {
	case isCodexSubscriptionGPT6Model(model):
		return CodexSubscriptionGPT6ContextWindow
	case isCodexSubscriptionGPT5Model(model):
		return CodexSubscriptionGPT5ContextWindow
	default:
		return 0
	}
}

func isCodexSubscriptionGPT6Model(model string) bool {
	id := codexSubscriptionModelID(model)
	return strings.Contains(id, "gpt-6") || strings.Contains(id, "gpt-5.6")
}

func isCodexSubscriptionGPT5Model(model string) bool {
	id := codexSubscriptionModelID(model)
	return strings.Contains(id, "gpt-5")
}

func codexSubscriptionModelID(model string) string {
	id := strings.ToLower(strings.TrimSpace(model))
	if idx := strings.LastIndex(id, "/"); idx >= 0 {
		id = id[idx+1:]
	}
	return id
}

func codexSubscriptionInputCapForModels(model, apiModel, providerType string) int {
	if cap := CodexSubscriptionInputCap(model, providerType); cap > 0 {
		return cap
	}
	if apiModel != "" && apiModel != model {
		return CodexSubscriptionInputCap(apiModel, providerType)
	}
	return 0
}

func isCodexSubscriptionProviderType(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "openai-codex", "codex-subscription", "chatgpt-codex":
		return true
	default:
		return false
	}
}
