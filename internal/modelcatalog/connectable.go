package modelcatalog

import (
	"sort"
	"strings"
	"time"
)

// Connectable is a catalog provider Wuu can create from a name, an endpoint,
// and a key: its SDK maps to a native provider type and its endpoint needs no
// account-specific template.
type Connectable struct {
	Provider
	Type         string
	BaseURL      string
	DefaultModel string
}

// The catalog omits endpoints for providers whose SDK already knows them.
// These are the official endpoints the matching provider types expect.
var sdkDefaultEndpoints = map[string]string{
	"openai":    "https://api.openai.com/v1",
	"anthropic": "https://api.anthropic.com",
}

// ConnectableProviders lists connectable catalog providers ordered by name.
func ConnectableProviders() []Connectable {
	providers, err := Providers()
	if err != nil {
		return nil
	}
	out := make([]Connectable, 0, len(providers))
	for _, provider := range providers {
		if item, ok := connectable(provider); ok {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if left != right {
			return left < right
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ConnectableProvider returns one connectable catalog provider by ID.
func ConnectableProvider(id string) (Connectable, bool) {
	provider, ok := ProviderByID(id)
	if !ok {
		return Connectable{}, false
	}
	return connectable(provider)
}

func connectable(provider Provider) (Connectable, bool) {
	providerType := nativeProviderTypeForNPM(provider.NPM)
	if providerType == "" {
		return Connectable{}, false
	}
	baseURL := strings.TrimSpace(provider.API)
	if baseURL == "" {
		baseURL = sdkDefaultEndpoints[normalizeID(provider.ID)]
	}
	if strings.Contains(baseURL, "${") ||
		!(strings.HasPrefix(baseURL, "https://") || strings.HasPrefix(baseURL, "http://")) {
		return Connectable{}, false
	}
	defaultModel := suggestedModel(provider.Models)
	if defaultModel == "" {
		return Connectable{}, false
	}
	if strings.TrimSpace(provider.Name) == "" {
		provider.Name = provider.ID
	}
	return Connectable{Provider: provider, Type: providerType, BaseURL: baseURL, DefaultModel: defaultModel}, true
}

// Family names that mark a cheaper or faster tier of a line rather than its
// main model. A main line released within a couple of months of a newer small
// tier is the better first choice for an agent.
var smallTierTokens = map[string]bool{
	"flash": true, "flashx": true, "mini": true, "lite": true, "nano": true,
	"air": true, "turbo": true, "haiku": true, "fast": true, "highspeed": true,
}

const (
	mainLineWindow = 60 * 24 * time.Hour
	releaseWindow  = 14 * 24 * time.Hour
)

// suggestedModel proposes the model a new connection starts with: the newest
// tool-capable main line, then within that line the base model of its latest
// release (the shortest ID, so "gpt-6-astra" wins over "-pro" and "-fast").
func suggestedModel(models []Model) string {
	type family struct {
		name   string
		newest time.Time
		models []Model
		small  bool
	}
	families := map[string]*family{}
	for _, model := range models {
		if model.ToolCall == nil || !*model.ToolCall || strings.TrimSpace(model.ID) == "" || model.Status == "deprecated" {
			continue
		}
		name := strings.TrimSpace(model.Family)
		if name == "" {
			name = model.ID
		}
		entry := families[name]
		if entry == nil {
			entry = &family{name: name}
			for _, token := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return r == '-' || r == '_' || r == '.' }) {
				entry.small = entry.small || smallTierTokens[token]
			}
			families[name] = entry
		}
		entry.models = append(entry.models, model)
		if released := releaseTime(model); released.After(entry.newest) {
			entry.newest = released
		}
	}
	ranked := make([]*family, 0, len(families))
	for _, entry := range families {
		ranked = append(ranked, entry)
	}
	if len(ranked) == 0 {
		return ""
	}
	sort.Slice(ranked, func(i, j int) bool {
		if !ranked[i].newest.Equal(ranked[j].newest) {
			return ranked[i].newest.After(ranked[j].newest)
		}
		return ranked[i].name < ranked[j].name
	})
	chosen := ranked[0]
	for _, entry := range ranked {
		if !entry.small && chosen.newest.Sub(entry.newest) <= mainLineWindow {
			chosen = entry
			break
		}
	}
	best := ""
	for _, model := range chosen.models {
		if chosen.newest.Sub(releaseTime(model)) > releaseWindow {
			continue
		}
		if best == "" || len(model.ID) < len(best) || (len(model.ID) == len(best) && model.ID < best) {
			best = model.ID
		}
	}
	return best
}

func releaseTime(model Model) time.Time {
	value := strings.TrimSpace(model.ReleaseDate)
	for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
