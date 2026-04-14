package tools

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// ---------------------------------------------------------------------------
// web_search
// ---------------------------------------------------------------------------

type WebSearchTool struct{ env *Env }

func NewWebSearchTool(env *Env) *WebSearchTool { return &WebSearchTool{env: env} }

func (t *WebSearchTool) Name() string            { return "web_search" }
func (t *WebSearchTool) IsReadOnly() bool         { return true }
func (t *WebSearchTool) IsConcurrencySafe() bool  { return true }

func (t *WebSearchTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "web_search",
		Description: "Search the web using DuckDuckGo. Returns titles, URLs, and snippets.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query.",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	return webSearchExecute(ctx, argsJSON)
}

// ---------------------------------------------------------------------------
// web_fetch
// ---------------------------------------------------------------------------

type WebFetchTool struct{ env *Env }

func NewWebFetchTool(env *Env) *WebFetchTool { return &WebFetchTool{env: env} }

func (t *WebFetchTool) Name() string            { return "web_fetch" }
func (t *WebFetchTool) IsReadOnly() bool         { return true }
func (t *WebFetchTool) IsConcurrencySafe() bool  { return true }

func (t *WebFetchTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "web_fetch",
		Description: "Fetch a URL and return its content as text. HTML is converted to readable text.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "URL to fetch.",
				},
			},
			"required": []string{"url"},
		},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	return webFetchExecute(ctx, argsJSON)
}
