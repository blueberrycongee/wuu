package modelcatalog

import (
	_ "embed"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/provideroptions"
)

//go:generate go run ../../cmd/wuu-modelcatalog-gen -input https://models.dev/api.json -output models_dev_catalog.json

//go:embed models_dev_catalog.json
var catalogJSON []byte

type Provider struct {
	ID           string            `json:"id"`
	Name         string            `json:"name,omitempty"`
	API          string            `json:"api,omitempty"`
	NPM          string            `json:"npm,omitempty"`
	Env          []string          `json:"env,omitempty"`
	ModelOptions map[string]any    `json:"model_options,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Models       []Model           `json:"models"`
}

type Model struct {
	ID               string                    `json:"id"`
	APIID            string                    `json:"api_id,omitempty"`
	Name             string                    `json:"name,omitempty"`
	Family           string                    `json:"family,omitempty"`
	Status           string                    `json:"status,omitempty"`
	ReleaseDate      string                    `json:"release_date,omitempty"`
	Reasoning        bool                      `json:"reasoning"`
	ReasoningOptions []map[string]any          `json:"reasoning_options,omitempty"`
	Attachment       *bool                     `json:"attachment,omitempty"`
	ToolCall         *bool                     `json:"tool_call,omitempty"`
	StructuredOutput *bool                     `json:"structured_output,omitempty"`
	Temperature      *bool                     `json:"temperature,omitempty"`
	Interleaved      any                       `json:"interleaved,omitempty"`
	Modalities       *Modalities               `json:"modalities,omitempty"`
	Cost             map[string]any            `json:"cost,omitempty"`
	Provider         *ModelProvider            `json:"provider,omitempty"`
	Limit            *Limit                    `json:"limit,omitempty"`
	Options          map[string]any            `json:"options,omitempty"`
	Headers          map[string]string         `json:"headers,omitempty"`
	SupportedEfforts []string                  `json:"supported_efforts,omitempty"`
	DefaultVariant   string                    `json:"default_variant,omitempty"`
	Variants         map[string]map[string]any `json:"variants,omitempty"`
}

type ModelProvider struct {
	API string `json:"api,omitempty"`
	NPM string `json:"npm,omitempty"`
}

type Modalities struct {
	Input  []string `json:"input,omitempty"`
	Output []string `json:"output,omitempty"`
}

type Limit struct {
	Context int `json:"context,omitempty"`
	Input   int `json:"input,omitempty"`
	Output  int `json:"output,omitempty"`
}

type catalogData struct {
	Providers []Provider `json:"providers"`
}

func Providers() ([]Provider, error) {
	return catalogProviders()
}

func ProviderByID(id string) (Provider, bool) {
	id = normalizeID(id)
	if id == "" {
		return Provider{}, false
	}
	providers, err := Providers()
	if err != nil {
		return Provider{}, false
	}
	for _, provider := range providers {
		if normalizeID(provider.ID) == id {
			return provider, true
		}
	}
	return Provider{}, false
}

// CodexSubscriptionCatalogProvider returns the OpenAI catalog metadata that is
// safe to reuse for ChatGPT/Codex subscription models. It intentionally does
// not make MatchProvider treat the Codex backend as the public OpenAI API or as
// an OpenAI-compatible gateway; callers opt into this narrow model-alias layer.
func CodexSubscriptionCatalogProvider(modelIDs ...string) (Provider, bool) {
	openai, ok := ProviderByID("openai")
	if !ok {
		return Provider{}, false
	}
	requested := make(map[string]bool, len(modelIDs))
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID != "" {
			requested[modelID] = true
		}
	}
	if len(requested) == 0 {
		return Provider{}, false
	}
	filtered := openai
	filtered.Models = nil
	for _, model := range openai.Models {
		id := strings.TrimSpace(model.ID)
		apiID := strings.TrimSpace(model.APIID)
		if requested[id] || (apiID != "" && requested[apiID]) {
			filtered.Models = append(filtered.Models, model)
		}
	}
	if len(filtered.Models) == 0 {
		return Provider{}, false
	}
	return filtered, true
}

func CodexSubscriptionModelAliases(modelID string) []Model {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil
	}
	provider, ok := CodexSubscriptionCatalogProvider(modelID)
	if !ok {
		return nil
	}
	out := make([]Model, 0, len(provider.Models))
	for _, model := range provider.Models {
		if strings.TrimSpace(model.ID) != modelID && strings.TrimSpace(model.APIID) == modelID {
			out = append(out, model)
		}
	}
	return out
}

func MatchProvider(providerName string, provider config.ProviderConfig) (Provider, bool) {
	providers, err := Providers()
	if err != nil {
		return Provider{}, false
	}

	endpoints := endpointCandidates(provider)
	for _, endpoint := range endpoints {
		var endpointMatches []Provider
		for _, item := range providers {
			if normalizeEndpoint(item.API) == endpoint {
				endpointMatches = append(endpointMatches, item)
			}
		}
		if len(endpointMatches) > 0 {
			return bestProviderMatch(endpointMatches, providerName, provider), true
		}
	}

	if !allowProviderIdentityCatalogMatch(provider) {
		return Provider{}, false
	}
	for _, candidate := range providerIDCandidates(providerName, provider, true) {
		for _, item := range providers {
			if normalizeID(item.ID) == candidate {
				return item, true
			}
		}
	}

	return Provider{}, false
}

func bestProviderMatch(providers []Provider, providerName string, provider config.ProviderConfig) Provider {
	candidates := providerIDCandidates(providerName, provider, true)
	for _, candidate := range candidates {
		for _, item := range providers {
			if normalizeID(item.ID) == candidate {
				return item
			}
		}
	}
	return providers[0]
}

func EnrichProvider(providerName string, provider config.ProviderConfig, modelIDs ...string) (string, config.ProviderConfig) {
	catalogProvider, ok := MatchProvider(providerName, provider)
	if !ok {
		if isCodexSubscriptionProviderType(provider.Type) {
			if catalogProvider, ok := CodexSubscriptionCatalogProvider(modelIDs...); ok {
				return providerName, MergeProvider(provider, catalogProvider)
			}
		}
		if config.IsGrokBuildProvider(provider.Type) {
			if catalogProvider, ok := catalogProviderModelsByID("xai", modelIDs...); ok {
				return "xai", mergeProviderModelMetadata(provider, catalogProvider, modelIDs...)
			}
		}
		if isOpenAICompatibleProviderType(provider.Type) {
			if catalogProvider, ok := catalogProviderModelsByID("openai", modelIDs...); ok {
				return providerName, mergeProviderModelMetadata(provider, catalogProvider, modelIDs...)
			}
		}
		return providerName, provider
	}
	return catalogProvider.ID, applyProviderCompatibilityDefaults(
		catalogProvider.ID,
		MergeProvider(provider, catalogProvider, modelIDs...),
		modelIDs...,
	)
}

func catalogProviderModelsByID(providerID string, modelIDs ...string) (Provider, bool) {
	provider, ok := ProviderByID(providerID)
	if !ok {
		return Provider{}, false
	}
	requested := make(map[string]bool, len(modelIDs))
	for _, modelID := range modelIDs {
		if modelID = strings.TrimSpace(modelID); modelID != "" {
			requested[modelID] = true
		}
	}
	if len(requested) == 0 {
		return Provider{}, false
	}
	models := make([]Model, 0, len(requested))
	for _, model := range provider.Models {
		if requested[strings.TrimSpace(model.ID)] {
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		return Provider{}, false
	}
	provider.Models = models
	return provider, true
}

func mergeProviderModelMetadata(provider config.ProviderConfig, catalogProvider Provider, modelIDs ...string) config.ProviderConfig {
	out := provider
	models := make(map[string]config.ProviderModelConfig, len(provider.Models)+len(modelIDs))
	for id, model := range provider.Models {
		models[id] = model
	}
	requested := make(map[string]bool, len(modelIDs))
	for _, modelID := range modelIDs {
		if modelID = strings.TrimSpace(modelID); modelID != "" {
			requested[modelID] = true
		}
	}
	for _, model := range catalogProvider.Models {
		id := strings.TrimSpace(model.ID)
		if id == "" || !requested[id] {
			continue
		}
		models[id] = MergeModelConfig(models[id], ModelConfig(model))
	}
	if len(models) > 0 {
		out.Models = models
	}
	return out
}

func MergeProvider(provider config.ProviderConfig, catalogProvider Provider, modelIDs ...string) config.ProviderConfig {
	out := provider
	if strings.TrimSpace(out.API) == "" {
		out.API = catalogProvider.API
	}
	if strings.TrimSpace(out.NPM) == "" {
		out.NPM = catalogProvider.NPM
	}
	if strings.TrimSpace(out.BaseURL) == "" && strings.TrimSpace(out.API) != "" {
		out.BaseURL = out.API
	}
	if strings.TrimSpace(out.APIKeyEnv) == "" &&
		strings.TrimSpace(out.APIKey) == "" &&
		!isCodexSubscriptionProviderType(out.Type) {
		out.APIKeyEnv = firstCatalogEnv(catalogProvider.Env)
	}
	out.Headers = mergeHeaders(catalogProvider.Headers, out.Headers)

	modelSet := make(map[string]bool, len(modelIDs))
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID != "" {
			modelSet[modelID] = true
		}
	}
	includeAll := len(modelSet) == 0

	providerModelDefaults := config.ProviderModelConfig{
		Options: cloneOptions(catalogProvider.ModelOptions),
	}
	models := make(map[string]config.ProviderModelConfig, len(out.Models)+len(catalogProvider.Models))
	for id, model := range out.Models {
		models[id] = MergeModelConfig(model, providerModelDefaults)
	}
	for _, model := range catalogProvider.Models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		if !includeAll && !modelSet[id] {
			continue
		}
		catalogModel := MergeModelConfig(ModelConfig(model), providerModelDefaults)
		models[id] = MergeModelConfig(models[id], catalogModel)
	}
	if len(models) > 0 {
		out.Models = models
	}
	if len(modelSet) == 1 {
		for id := range modelSet {
			if model := models[id]; len(model.Headers) > 0 {
				out.Headers = mergeHeaders(out.Headers, model.Headers)
			}
		}
	}
	if len(modelSet) == 1 {
		for id := range modelSet {
			applyModelProviderOverride(&out, models[id])
		}
	}
	return out
}

func firstCatalogEnv(values []string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func applyModelProviderOverride(provider *config.ProviderConfig, model config.ProviderModelConfig) {
	if provider == nil || model.Provider == nil {
		return
	}
	if api := strings.TrimSpace(model.Provider.API); api != "" {
		provider.API = api
		provider.BaseURL = api
	}
	if npm := strings.TrimSpace(model.Provider.NPM); npm != "" {
		provider.NPM = npm
		if providerType := nativeProviderTypeForNPM(npm); providerType != "" {
			provider.Type = providerType
		}
	}
}

func nativeProviderTypeForNPM(npm string) string {
	switch normalizeNPM(npm) {
	case "@ai-sdk/anthropic":
		return "anthropic"
	case "@ai-sdk/openai":
		return "openai"
	case "@ai-sdk/openai-compatible", "@openrouter/ai-sdk-provider", "@ai-sdk/xai":
		return "openai-compatible"
	default:
		return ""
	}
}

func ModelConfig(model Model) config.ProviderModelConfig {
	apiID := strings.TrimSpace(model.APIID)
	if apiID == "" {
		apiID = strings.TrimSpace(model.ID)
	}
	reasoning := model.Reasoning
	out := config.ProviderModelConfig{
		ID:               apiID,
		Name:             strings.TrimSpace(model.Name),
		Family:           strings.TrimSpace(model.Family),
		Status:           strings.TrimSpace(model.Status),
		ReleaseDate:      strings.TrimSpace(model.ReleaseDate),
		Reasoning:        &reasoning,
		ReasoningOptions: cloneOptionList(model.ReasoningOptions),
		Attachment:       cloneBool(model.Attachment),
		ToolCall:         cloneBool(model.ToolCall),
		StructuredOutput: cloneBool(model.StructuredOutput),
		Temperature:      cloneBool(model.Temperature),
		Interleaved:      cloneAny(model.Interleaved),
		Cost:             cloneOptions(model.Cost),
		SupportedEfforts: append([]string(nil), model.SupportedEfforts...),
		DefaultVariant:   strings.TrimSpace(model.DefaultVariant),
		Variants:         cloneVariants(model.Variants),
	}
	if model.Modalities != nil {
		out.Modalities = &config.ProviderModelModalitiesConfig{
			Input:  append([]string(nil), model.Modalities.Input...),
			Output: append([]string(nil), model.Modalities.Output...),
		}
	}
	if model.Provider != nil && (strings.TrimSpace(model.Provider.API) != "" || strings.TrimSpace(model.Provider.NPM) != "") {
		out.Provider = &config.ProviderModelProviderConfig{
			API: strings.TrimSpace(model.Provider.API),
			NPM: strings.TrimSpace(model.Provider.NPM),
		}
	}
	if model.Limit != nil {
		out.Limit = &config.ProviderModelLimitConfig{
			Context: model.Limit.Context,
			Input:   model.Limit.Input,
			Output:  model.Limit.Output,
		}
		if model.Limit.Context > 0 {
			out.ContextWindow = model.Limit.Context
		}
	}
	out.Options = cloneOptions(model.Options)
	out.Headers = cloneHeaders(model.Headers)
	return out
}

func APIModel(provider config.ProviderConfig, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if modelCfg, ok := provider.Models[model]; ok {
		if apiID := strings.TrimSpace(modelCfg.ID); apiID != "" {
			return apiID
		}
	}
	return model
}

func MergeModelConfig(primary, fallback config.ProviderModelConfig) config.ProviderModelConfig {
	out := primary
	if strings.TrimSpace(out.ID) == "" {
		out.ID = fallback.ID
	}
	if strings.TrimSpace(out.Name) == "" {
		out.Name = fallback.Name
	}
	if strings.TrimSpace(out.Family) == "" {
		out.Family = fallback.Family
	}
	if strings.TrimSpace(out.Status) == "" {
		out.Status = fallback.Status
	}
	if strings.TrimSpace(out.ReleaseDate) == "" {
		out.ReleaseDate = fallback.ReleaseDate
	}
	if out.Reasoning == nil {
		out.Reasoning = fallback.Reasoning
	}
	if len(out.ReasoningOptions) == 0 {
		out.ReasoningOptions = cloneOptionList(fallback.ReasoningOptions)
	}
	if out.Attachment == nil {
		out.Attachment = cloneBool(fallback.Attachment)
	}
	if out.ToolCall == nil {
		out.ToolCall = cloneBool(fallback.ToolCall)
	}
	if out.StructuredOutput == nil {
		out.StructuredOutput = cloneBool(fallback.StructuredOutput)
	}
	if out.Temperature == nil {
		out.Temperature = cloneBool(fallback.Temperature)
	}
	if out.Interleaved == nil {
		out.Interleaved = cloneAny(fallback.Interleaved)
	}
	if out.Modalities == nil && fallback.Modalities != nil {
		out.Modalities = &config.ProviderModelModalitiesConfig{
			Input:  append([]string(nil), fallback.Modalities.Input...),
			Output: append([]string(nil), fallback.Modalities.Output...),
		}
	}
	if len(out.Cost) == 0 {
		out.Cost = cloneOptions(fallback.Cost)
	}
	if out.Provider == nil {
		out.Provider = fallback.Provider
	} else if fallback.Provider != nil {
		provider := *out.Provider
		if strings.TrimSpace(provider.API) == "" {
			provider.API = fallback.Provider.API
		}
		if strings.TrimSpace(provider.NPM) == "" {
			provider.NPM = fallback.Provider.NPM
		}
		out.Provider = &provider
	}
	// ContextWindow and Limit.Context are two config spellings of the same
	// fact. Guard them jointly: when the user wrote either one, the catalog
	// must not fill the other spelling, or budget lookups that prefer one
	// field would silently shadow the user's value with catalog data.
	userSetContext := out.ContextWindow > 0 || (out.Limit != nil && out.Limit.Context > 0)
	if out.Limit == nil {
		if fallback.Limit != nil {
			limit := *fallback.Limit
			if userSetContext {
				limit.Context = 0
			}
			out.Limit = &limit
		}
	} else if fallback.Limit != nil {
		limit := *out.Limit
		if limit.Context == 0 && !userSetContext {
			limit.Context = fallback.Limit.Context
		}
		if limit.Input == 0 {
			limit.Input = fallback.Limit.Input
		}
		if limit.Output == 0 {
			limit.Output = fallback.Limit.Output
		}
		out.Limit = &limit
	}
	if out.ContextWindow == 0 && !userSetContext {
		out.ContextWindow = fallback.ContextWindow
	}
	out.Options = mergeModelOptions(fallback.Options, out.Options)
	out.Headers = mergeHeaders(fallback.Headers, out.Headers)
	if len(out.SupportedEfforts) == 0 {
		out.SupportedEfforts = append([]string(nil), fallback.SupportedEfforts...)
	}
	if strings.TrimSpace(out.DefaultVariant) == "" {
		out.DefaultVariant = fallback.DefaultVariant
	}
	out.Variants = mergeVariants(fallback.Variants, out.Variants)
	return out
}

func cloneBool(input *bool) *bool {
	if input == nil {
		return nil
	}
	value := *input
	return &value
}

func cloneAny(input any) any {
	if input == nil {
		return nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return input
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return input
	}
	return out
}

func cloneOptionList(input []map[string]any) []map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(input))
	for _, item := range input {
		out = append(out, cloneOptions(item))
	}
	return out
}

func cloneVariants(input map[string]map[string]any) map[string]map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]map[string]any, len(input))
	for key, options := range input {
		out[key] = cloneOptions(options)
	}
	return out
}

func mergeVariants(base, override map[string]map[string]any) map[string]map[string]any {
	out := cloneVariants(base)
	if len(override) == 0 {
		return out
	}
	if out == nil {
		out = make(map[string]map[string]any, len(override))
	}
	for key, options := range override {
		out[key] = mergeModelOptions(out[key], options)
	}
	return out
}

func cloneOptions(input map[string]any) map[string]any {
	return provideroptions.Clone(input)
}

func mergeModelOptions(base, override map[string]any) map[string]any {
	out := provideroptions.Clone(base)
	if len(override) == 0 {
		return out
	}
	if out == nil {
		out = make(map[string]any, len(override))
	}
	for key, value := range override {
		provideroptions.MergeValue(out, key, value)
	}
	return out
}

func cloneHeaders(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func mergeHeaders(base, override map[string]string) map[string]string {
	out := cloneHeaders(base)
	if len(override) == 0 {
		return out
	}
	if out == nil {
		out = make(map[string]string, len(override))
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}

func providerIDCandidates(providerName string, provider config.ProviderConfig, includeType bool) []string {
	raw := []string{providerName}
	if includeType {
		raw = append(raw, provider.Type)
	}
	aliases := map[string]string{
		"anthropic-official":  "anthropic",
		"bedrock":             "amazon-bedrock",
		"claude":              "anthropic",
		"gemini":              "google",
		"zhipuai":             "zai",
		"zhipuai-coding-plan": "zai-coding-plan",
		"xai-subscription":    "xai",
		"xai-oauth":           "xai",
		"grok-subscription":   "xai",
		"supergrok":           "xai",
		"kimi-for-coding":     "kimi-code-plan-cn",
	}
	var out []string
	seen := map[string]bool{}
	for _, value := range raw {
		normalized := normalizeID(value)
		if normalized == "" || normalized == "openai-compatible" {
			continue
		}
		if alias := aliases[normalized]; alias != "" {
			normalized = alias
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	return out
}

func allowProviderIdentityCatalogMatch(provider config.ProviderConfig) bool {
	endpoints := endpointCandidates(provider)
	if len(endpoints) == 0 {
		return true
	}
	providerType := normalizeID(provider.Type)
	for _, endpoint := range endpoints {
		if isOfficialEndpointForType(providerType, endpoint) {
			return true
		}
	}
	return false
}

func isCodexSubscriptionProviderType(providerType string) bool {
	switch normalizeID(providerType) {
	case "openai-codex", "codex-subscription", "chatgpt-codex":
		return true
	default:
		return false
	}
}

func isOpenAICompatibleProviderType(providerType string) bool {
	switch normalizeID(providerType) {
	case "openai", "openai-compatible", "codex":
		return true
	default:
		return false
	}
}

func isOfficialEndpointForType(providerType, endpoint string) bool {
	host := endpointHost(endpoint)
	path := endpointPath(endpoint)
	switch providerType {
	case "openai", "codex":
		return host == "api.openai.com"
	case "xai", "xai-subscription", "xai-oauth", "grok-subscription", "supergrok":
		return host == "api.x.ai"
	case "openai-compatible":
		return host == "api.z.ai" && path == "/api/v1"
	case "anthropic", "claude", "anthropic-official":
		return host == "api.anthropic.com" ||
			(host == "api.deepseek.com" && path == "/anthropic") ||
			(host == "api.z.ai" && path == "/api/anthropic")
	case "gemini", "google":
		return strings.HasSuffix(host, "generativelanguage.googleapis.com")
	default:
		return false
	}
}

func endpointCandidates(provider config.ProviderConfig) []string {
	raw := []string{provider.API, provider.BaseURL}
	var out []string
	seen := map[string]bool{}
	for _, value := range raw {
		normalized := normalizeEndpoint(value)
		for _, candidate := range endpointVariants(normalized) {
			if candidate == "" || seen[candidate] {
				continue
			}
			seen[candidate] = true
			out = append(out, candidate)
		}
	}
	return out
}

func endpointVariants(endpoint string) []string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil
	}
	out := []string{endpoint}
	if strings.HasSuffix(endpoint, "/v1") {
		base := strings.TrimSuffix(endpoint, "/v1")
		if base != "" {
			out = append(out, base)
		}
	} else {
		out = append(out, endpoint+"/v1")
	}
	return out
}

func normalizeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

func normalizeNPM(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "npm:")
	return value
}

func normalizeEndpoint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		parsed.RawQuery = ""
		parsed.Fragment = ""
		value = parsed.String()
	}
	return strings.TrimRight(strings.ToLower(value), "/")
}

func endpointHost(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Host)
}

func endpointPath(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return strings.TrimRight(strings.ToLower(parsed.Path), "/")
}
