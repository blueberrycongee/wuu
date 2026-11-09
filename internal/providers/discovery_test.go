package providers

import "testing"

func TestLoadableToolsFromToolSearchResultReadsCanonicalLoadableTools(t *testing.T) {
	tools := LoadableToolsFromToolSearchResult(`{
		"loadable_tools": [{
			"type": "function",
			"name": "mcp_docs_search",
			"description": "Search docs",
			"input_schema": {"type": "object"},
			"defer_loading": true
		}]
	}`)
	if len(tools) != 1 || tools[0].Name != "mcp_docs_search" || !tools[0].DeferLoading {
		t.Fatalf("unexpected loadable tools: %+v", tools)
	}
}

func TestLoadableToolsFromToolSearchResultReadsNativeToolsFallback(t *testing.T) {
	tools := LoadableToolsFromToolSearchResult(`{
		"tools": [{
			"type": "function",
			"name": "mcp_docs_search",
			"description": "Search docs",
			"input_schema": {"type": "object"},
			"defer_loading": true
		}]
	}`)
	if len(tools) != 1 || tools[0].Name != "mcp_docs_search" || !tools[0].DeferLoading {
		t.Fatalf("unexpected native fallback tools: %+v", tools)
	}
}

func TestLoadableToolsFromToolSearchResultPrefersCanonicalLoadableTools(t *testing.T) {
	tools := LoadableToolsFromToolSearchResult(`{
		"loadable_tools": [{"type": "function", "name": "canonical"}],
		"tools": [{"type": "function", "name": "native"}]
	}`)
	if len(tools) != 1 || tools[0].Name != "canonical" {
		t.Fatalf("expected canonical loadable_tools to win, got %+v", tools)
	}
}
