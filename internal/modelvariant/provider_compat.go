package modelvariant

import (
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
)

// Provider compatibility rules are aligned with
// sst/opencode@671d1937867fc85c0d300a47faba371b5eab5e54
// packages/opencode/src/provider/transform.ts.
//
// Keep runtime support separate from catalog/UI parity: some option bundles
// here are exposed so copied models.dev-style configs keep the same shape,
// even when wuu's current Go clients do not consume that provider package.
const (
	compatNPMOpenAI           = "@ai-sdk/openai"
	compatNPMOpenAICompatible = "@ai-sdk/openai-compatible"
	compatNPMOpenRouter       = "@openrouter/ai-sdk-provider"
	compatNPMLLMGateway       = "@llmgateway/ai-sdk-provider"
	compatNPMAnthropic        = "@ai-sdk/anthropic"
	compatNPMVertexAnthropic  = "@ai-sdk/google-vertex/anthropic"
	compatNPMGithubCopilot    = "@ai-sdk/github-copilot"
	compatNPMAzure            = "@ai-sdk/azure"
	compatNPMGateway          = "@ai-sdk/gateway"
	compatNPMAIGateway        = "ai-gateway-provider"
	compatNPMGoogle           = "@ai-sdk/google"
	compatNPMGoogleVertex     = "@ai-sdk/google-vertex"
	compatNPMAmazonBedrock    = "@ai-sdk/amazon-bedrock"
	compatNPMBedrockMantle    = "@ai-sdk/amazon-bedrock/mantle"
	compatNPMCerebras         = "@ai-sdk/cerebras"
	compatNPMTogetherAI       = "@ai-sdk/togetherai"
	compatNPMXAI              = "@ai-sdk/xai"
	compatNPMDeepInfra        = "@ai-sdk/deepinfra"
	compatNPMVenice           = "venice-ai-sdk-provider"
	compatNPMMistral          = "@ai-sdk/mistral"
	compatNPMCohere           = "@ai-sdk/cohere"
	compatNPMGroq             = "@ai-sdk/groq"
	compatNPMPerplexity       = "@ai-sdk/perplexity"
	compatNPMSAP              = "@jerome-benoit/sap-ai-provider-v2"
	compatNoneEffortRelease   = "2025-11-13"
	compatXHighEffortRelease  = "2025-12-04"
)

func BaseOptionsForProvider(providerName string, provider config.ProviderConfig, model string) map[string]any {
	desc := compatDescriptor(providerName, provider, model)
	modelCfg := provider.Models[strings.TrimSpace(model)]
	result := CloneOptions(modelCfg.Options)
	if result == nil {
		result = map[string]any{}
	}

	// Per-model "temperature": false is the user-facing switch for models
	// that reject the sampling field (OpenAI o/gpt-5 series, Claude Opus
	// 4.7+). It travels to the wire clients as a provider option; explicit
	// user options keep precedence.
	if _, exists := result["temperatureSupported"]; !exists {
		if modelCfg.Temperature != nil && !*modelCfg.Temperature {
			result["temperatureSupported"] = false
		}
	}

	applyCompatSamplingDefaults(result, desc)
	if (desc.APINPM == compatNPMAnthropic || desc.APINPM == compatNPMVertexAnthropic) && AnthropicRequiresBoundThinking(desc.APIID) {
		result["temperatureSupported"] = false
		setOptionDefault(result, "thinking", map[string]any{"type": "adaptive", "display": "summarized"})
	}
	if desc.APINPM == compatNPMVertexAnthropic || (desc.APINPM == compatNPMAnthropic && !strings.Contains(desc.APIID, "claude")) {
		setOptionDefault(result, "toolStreaming", false)
	}
	if desc.ProviderID == "openai" || desc.APINPM == compatNPMOpenAI || desc.APINPM == compatNPMGithubCopilot || desc.APINPM == compatNPMBedrockMantle {
		setOptionDefault(result, "store", false)
	}
	if desc.ProviderID == "openai" || desc.APINPM == compatNPMOpenAI || desc.APINPM == compatNPMOpenRouter {
		setOptionDefault(result, "promptCacheKeySupported", true)
	}
	if desc.APINPM == compatNPMAzure {
		setOptionDefault(result, "store", false)
	}
	if desc.APINPM == compatNPMOpenRouter || desc.APINPM == compatNPMLLMGateway {
		setOptionDefault(result, "usage", map[string]any{"include": true})
		if strings.Contains(desc.APIID, "gemini-3") {
			setOptionDefault(result, "reasoning", map[string]any{"effort": "high"})
		}
	}
	if desc.ProviderID == "baseten" || (desc.ProviderID == "opencode" && (desc.APIID == "kimi-k2-thinking" || desc.APIID == "glm-4.6")) {
		setOptionDefault(result, "chat_template_args", map[string]any{"enable_thinking": true})
	}
	if (strings.Contains(desc.ProviderID, "zai") || strings.Contains(desc.ProviderID, "zhipuai")) && desc.APINPM == compatNPMOpenAICompatible {
		thinking := map[string]any{
			"type":           "enabled",
			"clear_thinking": false,
		}
		if isGLM53(desc.APIID) {
			// GLM-5.3 rejects disabled thinking, including stale explicit
			// settings left behind by earlier GLM versions.
			result["thinking"] = thinking
			setOptionDefault(result, "toolChoiceAutoOnly", true)
			if !isResponsesWire(provider) {
				setOptionDefault(result, "tool_stream", true)
			}
		} else {
			setOptionDefault(result, "thinking", thinking)
		}
	}
	if (strings.Contains(desc.ProviderID, "zai") || strings.Contains(desc.ProviderID, "zhipuai")) &&
		desc.APINPM == compatNPMAnthropic && isGLM53(desc.APIID) {
		result["thinking"] = map[string]any{"type": "enabled"}
	}
	if isDirectXAI(desc) && isLatestGrokResponsesModel(desc.APIID) && isResponsesWire(provider) {
		setOptionDefault(result, "store", false)
		setOptionDefault(result, "include", []any{"reasoning.encrypted_content"})
		setOptionDefault(result, "maxOutputTokens", 128_000)
	}
	if isDirectDeepSeek(desc) && isDeepSeekV4Family(desc) && isResponsesWire(provider) {
		setOptionDefault(result, "omitStore", true)
		setOptionDefault(result, "omitPromptCacheKey", true)
	}
	if desc.APINPM == compatNPMGoogle || desc.APINPM == compatNPMGoogleVertex {
		if desc.Reasoning {
			thinking := map[string]any{"includeThoughts": true}
			if strings.Contains(desc.APIID, "gemini-3") {
				thinking["thinkingLevel"] = "high"
			}
			setOptionDefault(result, "thinkingConfig", thinking)
		}
	}
	if strings.Contains(desc.APIID, "minimax-m3") && desc.APINPM == compatNPMAnthropic {
		setOptionDefault(result, "thinking", map[string]any{"type": "adaptive"})
	}
	if (desc.APINPM == compatNPMAnthropic || desc.APINPM == compatNPMVertexAnthropic) &&
		(strings.Contains(desc.APIID, "k2p") || strings.Contains(desc.APIID, "kimi-k2.") || strings.Contains(desc.APIID, "kimi-k2p")) {
		setOptionDefault(result, "thinking", map[string]any{
			"type":         "enabled",
			"budgetTokens": compatAnthropicHighBudget(desc.OutputLimit),
		})
	}
	if strings.HasPrefix(desc.ProviderID, "alibaba") && desc.Reasoning && desc.APINPM == compatNPMOpenAICompatible && !strings.Contains(desc.APIID, "kimi-k2-thinking") {
		setOptionDefault(result, "enable_thinking", true)
	}
	if desc.APINPM == compatNPMAzure && strings.Contains(desc.APIID, "gpt-5.5") {
		setOptionDefault(result, "reasoningSummary", "auto")
		return nilIfEmpty(result)
	}
	if gptFamily := compatOpenAIGPTFamily(desc.APIID); gptFamily != 0 && !strings.Contains(desc.APIID, "gpt-5-chat") && !strings.Contains(desc.APIID, "gpt-6-chat") {
		defaultEffort := "medium"
		if gptFamily >= 6 && !strings.Contains(desc.APIID, "gpt-6-sol") && !strings.Contains(desc.APIID, "gpt-6-luna") {
			defaultEffort = "low"
		}
		if !compatOpenAIGPTProModel(desc.APIID) {
			setOptionDefault(result, "reasoningEffort", defaultEffort)
			if desc.APINPM == compatNPMOpenAI || desc.APINPM == compatNPMAzure || desc.APINPM == compatNPMGithubCopilot || desc.APINPM == compatNPMBedrockMantle {
				setOptionDefault(result, "reasoningSummary", "auto")
			}
			if desc.APINPM == compatNPMOpenAI || desc.APINPM == compatNPMBedrockMantle {
				setOptionDefault(result, "include", []any{"reasoning.encrypted_content"})
			}
		}
		if !strings.Contains(desc.APIID, "codex") &&
			!strings.Contains(desc.APIID, "-chat") &&
			desc.ProviderID != "azure" &&
			(gptFamily >= 6 || strings.Contains(desc.APIID, "gpt-5.")) {
			setOptionDefault(result, "textVerbosity", "low")
		}
		if strings.HasPrefix(desc.ProviderID, "opencode") {
			setOptionDefault(result, "include", []any{"reasoning.encrypted_content"})
			setOptionDefault(result, "reasoningSummary", "auto")
		}
	}
	if desc.APINPM == compatNPMGateway {
		setOptionDefault(result, "gateway", map[string]any{"caching": "auto"})
	}
	return nilIfEmpty(result)
}

func applyCompatSamplingDefaults(result map[string]any, desc compatModelDescriptor) {
	id := desc.ModelID
	apiID := desc.APIID
	if strings.Contains(id, "qwen") {
		setOptionDefault(result, "temperature", 0.55)
		setOptionDefault(result, "topP", 1.0)
	}
	if strings.Contains(id, "gemini") {
		setOptionDefault(result, "temperature", 1.0)
		setOptionDefault(result, "topP", 0.95)
		setOptionDefault(result, "topK", 64)
	}
	if strings.Contains(id, "glm-4.6") || strings.Contains(id, "glm-4.7") {
		setOptionDefault(result, "temperature", 1.0)
	}
	if strings.Contains(id, "minimax-m2") {
		setOptionDefault(result, "temperature", 1.0)
		setOptionDefault(result, "topP", 0.95)
		if strings.Contains(id, "m2.") || strings.Contains(id, "m25") || strings.Contains(id, "m21") {
			setOptionDefault(result, "topK", 40)
		} else {
			setOptionDefault(result, "topK", 20)
		}
	}
	if strings.Contains(id, "kimi-k2") {
		if strings.Contains(id, "thinking") || strings.Contains(id, "k2.") || strings.Contains(id, "k2p") || strings.Contains(id, "k2-5") {
			setOptionDefault(result, "temperature", 1.0)
		} else {
			setOptionDefault(result, "temperature", 0.6)
		}
	}
	if strings.Contains(apiID, "kimi-k2.5") || strings.Contains(apiID, "kimi-k2p5") || strings.Contains(apiID, "kimi-k2-5") {
		setOptionDefault(result, "topP", 0.95)
	}
}

func inferredOptionsForProvider(providerName string, provider config.ProviderConfig, model string) map[string]map[string]any {
	desc := compatDescriptor(providerName, provider, model)
	if !desc.Reasoning {
		return nil
	}
	id := desc.ModelID
	apiID := desc.APIID
	adaptiveEfforts := compatAnthropicAdaptiveEfforts(apiID)
	if forceAdaptiveThinking(provider.Models[model].Options) &&
		(desc.APINPM == compatNPMAnthropic || desc.APINPM == compatNPMVertexAnthropic || desc.APINPM == compatNPMOpenAICompatible) {
		efforts := modelReasoningEfforts(provider.Models[model])
		if len(efforts) == 0 {
			efforts = compatWidelySupportedEfforts()
		}
		effortKey := "effort"
		if desc.APINPM == compatNPMOpenAICompatible {
			effortKey = "reasoningEffort"
		}
		return compatVariantsFromEfforts(efforts, func(effort string) map[string]any {
			return map[string]any{
				"thinking": map[string]any{"type": "adaptive", "display": "summarized"},
				effortKey:  effort,
			}
		})
	}
	if strings.Contains(desc.APIID, "minimax-m3") &&
		(desc.APINPM == compatNPMAnthropic || desc.APINPM == compatNPMOpenAICompatible) {
		return map[string]map[string]any{
			"none":     {"thinking": map[string]any{"type": "disabled"}},
			"thinking": {"thinking": map[string]any{"type": "adaptive"}},
		}
	}
	if isGLM53(id) && desc.APINPM == compatNPMOpenAICompatible {
		return compatGLM53Variants()
	}
	if isGLM53(id) && desc.APINPM == compatNPMAnthropic {
		return compatGLM53AnthropicVariants()
	}
	if isGLM52(id) && desc.APINPM == compatNPMOpenAICompatible {
		return compatGLM52Variants()
	}
	if compatExcludedReasoningModel(id) {
		return nil
	}
	if strings.Contains(id, "grok") && strings.Contains(id, "grok-3-mini") {
		if desc.APINPM == compatNPMOpenRouter {
			return compatVariantsFromEfforts([]string{"low", "high"}, func(effort string) map[string]any {
				return map[string]any{"reasoning": map[string]any{"effort": effort}}
			})
		}
		return compatReasoningEffortVariants([]string{"low", "high"})
	}
	if strings.Contains(id, "grok") {
		efforts := modelReasoningEfforts(provider.Models[model])
		if len(efforts) == 0 {
			efforts = compatGrokFallbackEfforts(id)
		}
		return compatReasoningEffortVariants(efforts)
	}
	if isDeepSeekV4Family(desc) && desc.APINPM == compatNPMAnthropic {
		return compatDeepSeekV4AnthropicVariants(modelReasoningEfforts(provider.Models[model]))
	}

	switch desc.APINPM {
	case compatNPMOpenRouter:
		efforts := compatWidelySupportedEfforts()
		if strings.HasPrefix(apiID, "openai/") || strings.Contains(id, "gpt") {
			efforts = compatCompatibleReasoningEfforts(id)
		}
		return compatVariantsFromEfforts(efforts, func(effort string) map[string]any {
			return map[string]any{"reasoning": map[string]any{"effort": effort}}
		})
	case compatNPMAIGateway:
		if strings.HasPrefix(apiID, "openai/") {
			return compatReasoningEffortVariants(compatReasoningEfforts(apiID, desc.ReleaseDate))
		}
		return compatReasoningEffortVariants(compatWidelySupportedEfforts())
	case compatNPMGateway:
		if strings.Contains(id, "anthropic") {
			return compatAnthropicVariants(desc, adaptiveEfforts, false)
		}
		if strings.Contains(id, "google") {
			return compatGatewayGoogleVariants(id)
		}
		return compatReasoningEffortVariants(compatCompatibleReasoningEfforts(apiID))
	case compatNPMGithubCopilot:
		if strings.Contains(id, "gemini") {
			return nil
		}
		if strings.Contains(id, "claude") {
			return compatReasoningEffortVariants(compatWidelySupportedEfforts())
		}
		efforts := append([]string{}, compatWidelySupportedEfforts()...)
		if strings.Contains(id, "5.1-codex-max") || strings.Contains(id, "5.2") || strings.Contains(id, "5.3") {
			efforts = append(efforts, "xhigh")
		} else if (strings.Contains(id, "gpt-5") || strings.Contains(id, "gpt-6")) && desc.ReleaseDate >= compatXHighEffortRelease {
			efforts = append(efforts, "xhigh")
		}
		return compatVariantsFromEfforts(efforts, compatOpenAIProviderVariantOptions)
	case compatNPMCerebras, compatNPMTogetherAI, compatNPMXAI, compatNPMDeepInfra, compatNPMVenice, compatNPMOpenAICompatible:
		if isDeepSeekV4Family(desc) {
			return compatDeepSeekV4Variants(provider.WireAPI, modelReasoningEfforts(provider.Models[model]))
		}
		efforts := modelReasoningEfforts(provider.Models[model])
		if len(efforts) == 0 {
			efforts = append([]string{}, compatWidelySupportedEfforts()...)
		}
		return compatReasoningEffortVariants(efforts)
	case compatNPMAzure:
		if id == "o1-mini" {
			return nil
		}
		efforts := compatWidelySupportedEfforts()
		if compatGPT5FamilyRE.MatchString(id) && compatGPT5Version(id) == 0 {
			efforts = append([]string{"minimal"}, efforts...)
		}
		return compatVariantsFromEfforts(efforts, compatOpenAIProviderVariantOptions)
	case compatNPMOpenAI, compatNPMBedrockMantle:
		if efforts := modelReasoningEfforts(provider.Models[model]); len(efforts) > 0 {
			return compatVariantsFromEfforts(efforts, compatOpenAIProviderVariantOptions)
		}
		return compatVariantsFromEfforts(compatReasoningEfforts(apiID, desc.ReleaseDate), compatOpenAIProviderVariantOptions)
	case compatNPMAnthropic, compatNPMVertexAnthropic:
		return compatAnthropicVariants(desc, adaptiveEfforts, true)
	case compatNPMAmazonBedrock:
		return compatBedrockVariants(apiID, adaptiveEfforts)
	case compatNPMGoogleVertex, compatNPMGoogle:
		return compatGoogleVariants(id)
	case compatNPMMistral:
		mistralID := strings.ToLower(apiID)
		if strings.Contains(mistralID, "mistral-small-2603") ||
			strings.Contains(mistralID, "mistral-small-latest") ||
			strings.Contains(mistralID, "mistral-medium-3.5") ||
			strings.Contains(mistralID, "mistral-medium-2604") {
			return map[string]map[string]any{"high": {"reasoningEffort": "high"}}
		}
		return nil
	case compatNPMCohere, compatNPMPerplexity:
		return nil
	case compatNPMGroq:
		return compatReasoningEffortVariants(append([]string{"none"}, compatWidelySupportedEfforts()...))
	case compatNPMSAP:
		return compatSAPVariants(desc, adaptiveEfforts)
	default:
		return nil
	}
}

func forceAdaptiveThinking(options map[string]any) bool {
	for _, key := range []string{"force_adaptive_thinking", "forceAdaptiveThinking"} {
		if enabled, ok := options[key].(bool); ok {
			return enabled
		}
	}
	return false
}

func modelReasoningEfforts(model config.ProviderModelConfig) []string {
	if efforts := normalizedEffortList(model.SupportedEfforts); len(efforts) > 0 {
		return efforts
	}
	for _, option := range model.ReasoningOptions {
		if strings.ToLower(strings.TrimSpace(stringOption(option["type"]))) != "effort" {
			continue
		}
		var efforts []string
		switch values := option["values"].(type) {
		case []string:
			efforts = append(efforts, values...)
		case []any:
			for _, value := range values {
				if effort, ok := value.(string); ok {
					efforts = append(efforts, effort)
				}
			}
		}
		return normalizedEffortList(efforts)
	}
	return nil
}

func stringOption(value any) string {
	text, _ := value.(string)
	return text
}

func isGLM52(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	return strings.Contains(id, "glm-5.2") || strings.Contains(id, "glm5.2")
}

func isGLM53(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	return strings.Contains(id, "glm-5.3") || strings.Contains(id, "glm5.3")
}

func isLatestGrokResponsesModel(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	return strings.Contains(id, "grok-4.6") || strings.Contains(id, "grok-4.7")
}

func isResponsesWire(provider config.ProviderConfig) bool {
	return strings.EqualFold(strings.TrimSpace(provider.WireAPI), "responses")
}

func isDirectXAI(desc compatModelDescriptor) bool {
	return desc.ProviderID == "xai" || strings.Contains(desc.BaseURL, "api.x.ai")
}

func isDirectDeepSeek(desc compatModelDescriptor) bool {
	return desc.ProviderID == "deepseek" || strings.Contains(desc.BaseURL, "api.deepseek.com")
}

// compatGrokFallbackEfforts fills the reasoning tiers for Grok models that are
// not yet in the embedded catalog. Grok 4.6 and 4.7 follow xAI's documented
// effort vocabulary (low/medium/high plus xhigh); earlier Grok 4.x fall back
// to low/medium/high.
func compatGrokFallbackEfforts(id string) []string {
	if strings.Contains(id, "grok-4.6") || strings.Contains(id, "grok-4.7") {
		return []string{"low", "medium", "high", "xhigh"}
	}
	return []string{"low", "medium", "high"}
}
