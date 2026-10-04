package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
)

func (s *Server) handleConfigModelCatalogRefresh(ctx context.Context, req Request) error {
	if s == nil || s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("runtime session is required"))
	}
	cachePath := strings.TrimSpace(s.modelCatalogCachePath)
	if cachePath == "" {
		return s.writeResponse(req.ID, nil, errors.New("model catalog cache path is unavailable"))
	}
	counts, err := modelcatalog.Refresh(ctx, modelcatalog.RefreshOptions{
		URL:       s.modelCatalogURL,
		CachePath: cachePath,
		Client:    s.modelCatalogHTTPClient,
	})
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	providers := s.providerSummaries()
	if providers == nil {
		providers = []ProviderSummary{}
	}
	return s.writeResponse(req.ID, ConfigModelCatalogRefreshResult{
		ProviderCount: counts.Providers,
		ModelCount:    counts.Models,
		Providers:     providers,
	}, nil)
}

func (s *Server) handleConfigModelCatalogProviders(req Request) error {
	var params ConfigModelCatalogProvidersParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if id := strings.TrimSpace(params.Provider); id != "" {
		provider, ok := modelcatalog.ConnectableProvider(id)
		if !ok {
			return s.writeResponse(req.ID, nil, fmt.Errorf("catalog provider %q cannot be connected", id))
		}
		summary := catalogProviderSummary(provider)
		summary.Models = make([]CatalogModelSummary, 0, len(provider.Models))
		for _, model := range provider.Models {
			item := CatalogModelSummary{
				ID:          model.ID,
				Name:        strings.TrimSpace(model.Name),
				ReleaseDate: strings.TrimSpace(model.ReleaseDate),
				ToolCall:    model.ToolCall != nil && *model.ToolCall,
			}
			if model.Limit != nil {
				item.ContextWindow = model.Limit.Context
			}
			summary.Models = append(summary.Models, item)
		}
		return s.writeResponse(req.ID, ConfigModelCatalogProvidersResult{Providers: []CatalogProviderSummary{summary}}, nil)
	}
	connectable := modelcatalog.ConnectableProviders()
	out := make([]CatalogProviderSummary, 0, len(connectable))
	for _, provider := range connectable {
		out = append(out, catalogProviderSummary(provider))
	}
	return s.writeResponse(req.ID, ConfigModelCatalogProvidersResult{Providers: out}, nil)
}

func catalogProviderSummary(provider modelcatalog.Connectable) CatalogProviderSummary {
	summary := CatalogProviderSummary{
		ID:           provider.ID,
		Name:         provider.Name,
		Type:         provider.Type,
		BaseURL:      provider.BaseURL,
		ModelCount:   len(provider.Models),
		DefaultModel: provider.DefaultModel,
	}
	if len(provider.Env) > 0 {
		summary.APIKeyEnv = provider.Env[0]
	}
	summary.Connections = []CatalogConnectionSummary{{
		ID: "api-key", Name: "API key", Auth: "api_key",
		Type: provider.Type, BaseURL: provider.BaseURL, DefaultModel: provider.DefaultModel,
	}}
	// Subscription adapters keep their own transport, model defaults and
	// credential ownership. The directory only advertises how to connect them.
	var subscriptions []CatalogConnectionSummary
	switch provider.ID {
	case "openai":
		subscriptions = []CatalogConnectionSummary{{ID: "codex-cli", Name: "ChatGPT / Codex CLI", Type: "openai-codex", Auth: "local", ReuseCodexCredentials: true}}
	case "xai":
		subscriptions = []CatalogConnectionSummary{
			{ID: "xai-oauth", Name: "Grok / X", Type: "xai-subscription", Auth: "oauth", Login: "xai"},
			{ID: "grok-cli", Name: "Grok Build CLI", Type: "grok-build", Auth: "local"},
		}
	}
	for _, connection := range subscriptions {
		providerDefaults := config.Default().Providers[connection.Type]
		connection.BaseURL = providerDefaults.BaseURL
		connection.DefaultModel = providerDefaults.Model
		connection.DesktopOnly = true
		summary.Connections = append(summary.Connections, connection)
	}
	return summary
}
