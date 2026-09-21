package modelvariant

import (
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
)

type compatModelDescriptor struct {
	ProviderID  string
	ModelID     string
	APIID       string
	APINPM      string
	ReleaseDate string
	Reasoning   bool
	OutputLimit int
	BaseURL     string
}

func compatDescriptor(providerName string, provider config.ProviderConfig, model string) compatModelDescriptor {
	modelID := strings.TrimSpace(model)
	modelCfg := provider.Models[modelID]
	apiID := strings.TrimSpace(modelCfg.ID)
	if apiID == "" {
		apiID = modelID
	}
	apiNPM := ""
	if modelCfg.Provider != nil {
		apiNPM = strings.TrimSpace(modelCfg.Provider.NPM)
	}
	if apiNPM == "" {
		apiNPM = strings.TrimSpace(provider.NPM)
	}
	if apiNPM == "" {
		apiNPM = compatInferNPM(providerName, provider)
	}
	outputLimit := 0
	if modelCfg.Limit != nil {
		outputLimit = modelCfg.Limit.Output
	}
	desc := compatModelDescriptor{
		ProviderID:  compatProviderID(providerName, provider),
		ModelID:     strings.ToLower(modelID),
		APIID:       strings.ToLower(apiID),
		APINPM:      apiNPM,
		ReleaseDate: strings.TrimSpace(modelCfg.ReleaseDate),
		OutputLimit: outputLimit,
		BaseURL:     strings.ToLower(strings.TrimSpace(provider.BaseURL)),
	}
	desc.Reasoning = compatReasoningEnabled(desc, modelCfg.Reasoning)
	return desc
}

func compatProviderID(providerName string, provider config.ProviderConfig) string {
	if value := strings.ToLower(strings.TrimSpace(providerName)); value != "" {
		return value
	}
	providerType := normalizedProviderType(provider.Type)
	baseURL := strings.ToLower(strings.TrimSpace(provider.BaseURL))
	switch {
	case strings.Contains(baseURL, "openrouter.ai"):
		return "openrouter"
	case isCodexProviderType(provider.Type):
		return "opencode"
	case providerType == "openai":
		return "openai"
	case providerType == "anthropic" || providerType == "claude" || providerType == "anthropic-official":
		return "anthropic"
	default:
		return providerType
	}
}

func compatInferNPM(providerName string, provider config.ProviderConfig) string {
	providerType := normalizedProviderType(provider.Type)
	baseURL := strings.ToLower(strings.TrimSpace(provider.BaseURL))
	if strings.Contains(baseURL, "openrouter.ai") || strings.ToLower(strings.TrimSpace(providerName)) == "openrouter" {
		return compatNPMOpenRouter
	}
	if isCodexProviderType(provider.Type) || providerType == "openai" {
		return compatNPMOpenAI
	}
	if providerType == "anthropic" || providerType == "claude" || providerType == "anthropic-official" {
		return compatNPMAnthropic
	}
	return compatNPMOpenAICompatible
}

func normalizedProviderType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.ReplaceAll(value, "_", "-")
}

func isDeepSeekV4Family(desc compatModelDescriptor) bool {
	for _, value := range []string{desc.ModelID, desc.APIID} {
		id := strings.ToLower(strings.TrimSpace(value))
		if idx := strings.LastIndex(id, "/"); idx >= 0 {
			id = id[idx+1:]
		}
		id = strings.TrimPrefix(id, "~")
		if strings.Contains(id, "deepseek-v4") || id == "deepseek-flash" || strings.HasPrefix(id, "deepseek-flash-") {
			return true
		}
	}
	return false
}

func compatReasoningEnabled(desc compatModelDescriptor, configured *bool) bool {
	if configured != nil {
		return *configured
	}
	if compatExcludedReasoningModel(desc.ModelID) {
		return false
	}
	if strings.Contains(desc.BaseURL, "xiaomimimo.com") {
		return true
	}
	id := desc.ModelID
	apiID := desc.APIID
	if strings.Contains(id, "claude") || strings.Contains(apiID, "claude") || strings.Contains(apiID, "anthropic") {
		return true
	}
	if strings.Contains(id, "gemini") || strings.Contains(apiID, "gemini") {
		return true
	}
	if strings.Contains(id, "grok-3-mini") || strings.Contains(apiID, "grok-3-mini") {
		return true
	}
	if strings.Contains(id, "grok-4.6") || strings.Contains(apiID, "grok-4.6") ||
		strings.Contains(id, "grok-4.7") || strings.Contains(apiID, "grok-4.7") ||
		strings.Contains(id, "glm-5.3") || strings.Contains(apiID, "glm-5.3") {
		return true
	}
	if isDeepSeekV4Family(desc) {
		return true
	}
	if compatGPT5FamilyRE.MatchString(apiID) || compatGPT5FamilyRE.MatchString(id) ||
		strings.Contains(apiID, "gpt-6") || strings.Contains(id, "gpt-6") ||
		strings.Contains(apiID, "o1") || strings.Contains(apiID, "o3") || strings.Contains(apiID, "o4") ||
		strings.Contains(id, "o1") || strings.Contains(id, "o3") || strings.Contains(id, "o4") {
		return true
	}
	if desc.APINPM == compatNPMAmazonBedrock && (strings.Contains(apiID, "nova") || strings.Contains(id, "nova")) {
		return true
	}
	if desc.APINPM == compatNPMMistral && strings.Contains(apiID, "mistral") {
		return true
	}
	return false
}
