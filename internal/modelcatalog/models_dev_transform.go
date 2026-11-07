package modelcatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const ModelsDevURL = "https://models.dev/api.json"

type modelsDevCatalog map[string]modelsDevProvider
type modelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	API    string                    `json:"api"`
	NPM    string                    `json:"npm"`
	Env    []string                  `json:"env"`
	Models map[string]modelsDevModel `json:"models"`
}
type modelsDevModel struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name"`
	Family           string                 `json:"family"`
	Status           string                 `json:"status"`
	ReleaseDate      string                 `json:"release_date"`
	Reasoning        bool                   `json:"reasoning"`
	ReasoningOptions []map[string]any       `json:"reasoning_options"`
	Attachment       *bool                  `json:"attachment"`
	ToolCall         *bool                  `json:"tool_call"`
	StructuredOutput *bool                  `json:"structured_output"`
	Temperature      *bool                  `json:"temperature"`
	Interleaved      any                    `json:"interleaved"`
	Modalities       *Modalities            `json:"modalities"`
	Cost             map[string]any         `json:"cost"`
	Provider         *ModelProvider         `json:"provider"`
	Limit            *Limit                 `json:"limit"`
	Options          map[string]any         `json:"options"`
	Headers          map[string]string      `json:"headers"`
	Experimental     *modelsDevExperimental `json:"experimental"`
}
type modelsDevExperimental struct {
	Modes map[string]modelsDevMode `json:"modes"`
}
type modelsDevMode struct {
	Cost     map[string]any         `json:"cost"`
	Provider *modelsDevModeProvider `json:"provider"`
}
type modelsDevModeProvider struct {
	Body    map[string]any    `json:"body"`
	Headers map[string]string `json:"headers"`
}

// NormalizeModelsDev converts the upstream API document into the exact
// deterministic format shared by Wuu's embedded snapshot and runtime cache.
func NormalizeModelsDev(data []byte) ([]byte, CatalogCounts, error) {
	var raw modelsDevCatalog
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, CatalogCounts{}, fmt.Errorf("decode models.dev catalog: %w", err)
	}
	if len(raw) == 0 {
		return nil, CatalogCounts{}, fmt.Errorf("models.dev catalog has no providers")
	}
	catalog := buildModelsDevCatalog(raw)
	counts := countCatalog(catalog)
	if counts.Providers == 0 || counts.Models == 0 {
		return nil, CatalogCounts{}, fmt.Errorf("models.dev catalog normalized to an empty catalog")
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		return nil, CatalogCounts{}, err
	}
	return append(encoded, '\n'), counts, nil
}

func buildModelsDevCatalog(raw modelsDevCatalog) catalogData {
	providerIDs := make([]string, 0, len(raw))
	for id := range raw {
		providerIDs = append(providerIDs, id)
	}
	sort.Strings(providerIDs)
	out := catalogData{Providers: make([]Provider, 0, len(providerIDs))}
	for _, providerID := range providerIDs {
		provider := raw[providerID]
		if provider.ID == "" {
			provider.ID = providerID
		}
		models := buildModelsDevModels(provider)
		if len(models) == 0 {
			continue
		}
		out.Providers = append(out.Providers, Provider{ID: provider.ID, Name: provider.Name, API: provider.API, NPM: provider.NPM, Env: append([]string(nil), provider.Env...), Models: models})
	}
	return out
}

func buildModelsDevModels(provider modelsDevProvider) []Model {
	modelIDs := make([]string, 0, len(provider.Models))
	explicitIDs := make(map[string]bool, len(provider.Models))
	for key, model := range provider.Models {
		modelIDs = append(modelIDs, key)
		id := model.ID
		if id == "" {
			id = key
		}
		explicitIDs[id] = true
	}
	sort.Strings(modelIDs)
	models := make([]Model, 0, len(modelIDs))
	for _, key := range modelIDs {
		model := provider.Models[key]
		if model.ID == "" {
			model.ID = key
		}
		base := convertModelsDevModel(provider, model, model.ID, model.ID, model.Name)
		if filteredModelsDevModel(provider.ID, base.ID, base.Status) {
			continue
		}
		models = append(models, base)
		for _, mode := range sortedModelsDevModes(model.Experimental) {
			candidate := modelsDevModeModel(base, mode, model.Experimental.Modes[mode])
			// An explicit upstream entry owns its ID, including retirement status.
			// Experimental modes must not duplicate or resurrect that entry.
			if !explicitIDs[candidate.ID] && !filteredModelsDevModel(provider.ID, candidate.ID, candidate.Status) {
				models = append(models, candidate)
			}
		}
	}
	return models
}

func sortedModelsDevModes(experimental *modelsDevExperimental) []string {
	if experimental == nil || len(experimental.Modes) == 0 {
		return nil
	}
	out := make([]string, 0, len(experimental.Modes))
	for mode := range experimental.Modes {
		out = append(out, mode)
	}
	sort.Strings(out)
	return out
}

func filteredModelsDevModel(providerID, modelID, status string) bool {
	switch strings.TrimSpace(status) {
	case "deprecated", "alpha":
		return true
	}
	if modelID == "gpt-5-chat-latest" {
		switch providerID {
		case "openai", "github-copilot", "openrouter":
			return true
		}
	}
	return providerID == "openrouter" && modelID == "openai/gpt-5-chat"
}

func convertModelsDevModel(provider modelsDevProvider, model modelsDevModel, id, apiID, name string) Model {
	status := strings.TrimSpace(model.Status)
	if status == "active" {
		status = ""
	}
	return Model{
		ID: id, APIID: modelsDevAPIID(id, apiID), Name: name, Family: model.Family, Status: status, ReleaseDate: model.ReleaseDate,
		Reasoning: model.Reasoning, ReasoningOptions: cloneModelsDevOptionList(model.ReasoningOptions), Attachment: cloneModelsDevBool(model.Attachment),
		ToolCall: cloneModelsDevBool(model.ToolCall), StructuredOutput: cloneModelsDevBool(model.StructuredOutput), Temperature: cloneModelsDevBool(model.Temperature),
		Interleaved: cloneModelsDevAny(model.Interleaved), Modalities: cloneModelsDevModalities(model.Modalities), Cost: cloneModelsDevOptions(model.Cost),
		Provider: modelsDevModelProvider(provider, model), Limit: cloneModelsDevLimit(model.Limit), Options: cloneModelsDevOptions(model.Options), Headers: cloneModelsDevHeaders(model.Headers),
	}
}

func modelsDevAPIID(id, apiID string) string {
	if strings.TrimSpace(apiID) == "" || apiID == id {
		return ""
	}
	return apiID
}

func modelsDevModelProvider(provider modelsDevProvider, model modelsDevModel) *ModelProvider {
	if model.Provider == nil {
		return nil
	}
	out := *model.Provider
	if out.API == provider.API {
		out.API = ""
	}
	if out.NPM == provider.NPM {
		out.NPM = ""
	}
	if strings.TrimSpace(out.API) == "" && strings.TrimSpace(out.NPM) == "" {
		return nil
	}
	return &out
}

func modelsDevModeModel(base Model, mode string, opts modelsDevMode) Model {
	out := base
	out.ID = base.ID + "-" + mode
	out.APIID = base.ID
	if base.Name != "" {
		out.Name = base.Name + " " + strings.ToUpper(mode[:1]) + mode[1:]
	}
	if len(opts.Cost) > 0 {
		out.Cost = mergeModelsDevOptions(base.Cost, opts.Cost)
	}
	if opts.Provider != nil {
		if len(opts.Provider.Body) > 0 {
			out.Options = camelCaseModelsDevOptions(opts.Provider.Body)
		}
		if len(opts.Provider.Headers) > 0 {
			out.Headers = cloneModelsDevHeaders(opts.Provider.Headers)
		}
	}
	return out
}

func camelCaseModelsDevOptions(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[modelsDevSnakeToLowerCamel(key)] = cloneModelsDevAny(value)
	}
	return out
}
func modelsDevSnakeToLowerCamel(input string) string {
	var b strings.Builder
	upperNext := false
	for _, r := range input {
		if r == '_' {
			upperNext = true
			continue
		}
		if upperNext && r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		upperNext = false
		b.WriteRune(r)
	}
	return b.String()
}
func mergeModelsDevOptions(base, override map[string]any) map[string]any {
	out := cloneModelsDevOptions(base)
	if out == nil {
		out = map[string]any{}
	}
	for key, value := range override {
		out[key] = cloneModelsDevAny(value)
	}
	return out
}
func cloneModelsDevBool(input *bool) *bool {
	if input == nil {
		return nil
	}
	value := *input
	return &value
}
func cloneModelsDevLimit(input *Limit) *Limit {
	if input == nil {
		return nil
	}
	out := *input
	return &out
}
func cloneModelsDevModalities(input *Modalities) *Modalities {
	if input == nil {
		return nil
	}
	return &Modalities{Input: append([]string(nil), input.Input...), Output: append([]string(nil), input.Output...)}
}
func cloneModelsDevHeaders(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
func cloneModelsDevOptions(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneModelsDevAny(value)
	}
	return out
}
func cloneModelsDevOptionList(input []map[string]any) []map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(input))
	for _, item := range input {
		out = append(out, cloneModelsDevOptions(item))
	}
	return out
}
func cloneModelsDevAny(input any) any {
	if input == nil {
		return nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return input
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var out any
	if err := decoder.Decode(&out); err != nil {
		return input
	}
	return out
}
