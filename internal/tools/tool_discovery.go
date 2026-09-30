package tools

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type ToolSearchTool struct {
	toolkit *Toolkit
}

func NewToolSearchTool(toolkit *Toolkit) *ToolSearchTool {
	return &ToolSearchTool{toolkit: toolkit}
}

func (t *ToolSearchTool) Name() string            { return "tool_search" }
func (t *ToolSearchTool) IsReadOnly() bool        { return false }
func (t *ToolSearchTool) IsConcurrencySafe() bool { return false }

func (t *ToolSearchTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "tool_search",
		Description: "Search deferred tools and load matching tool schemas.\n\n" +
			"A bounded preview of deferred tools appears under <available-deferred-tools>; more tools may be available through search. Deferred tools are not callable until their schemas are loaded. " +
			"Use this when you need a listed deferred tool or a capability that is not currently visible, especially MCP tools, scheduling, memory, or specialized helpers. Search by capability words, or use select:<tool_name> when you already know the exact tool. Load related tools in one call when you expect to need several, for example select:tool_a tool_b. Loaded schemas are callable on the next tool step; if a tool is already visible or reported in already_loaded, call it directly. Do not call MCP list/resource tools just to discover what tools exist; this search is the discovery path.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search terms describing the tool you need, or select:<tool_name> for exact loading.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of tool schemas to load. Default 8, maximum 20.",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (t *ToolSearchTool) Execute(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return "", errors.New("tool_search requires query")
	}
	if t.toolkit == nil {
		return "", errors.New("tool_search is not attached to a toolkit")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}

	matches := t.toolkit.searchDeferredTools(query, limit)
	names := make([]string, 0, len(matches))
	alreadyLoaded := make([]string, 0)
	newlyLoaded := make([]string, 0)
	loadableTools := make([]providers.LoadableToolDefinition, 0, len(matches))
	for _, match := range matches {
		names = append(names, match.Name)
		if t.toolkit.isDeferredToolLoaded(match.Name) {
			alreadyLoaded = append(alreadyLoaded, match.Name)
		} else {
			newlyLoaded = append(newlyLoaded, match.Name)
		}
		loadableTools = append(loadableTools, match.LoadableTool)
	}
	t.toolkit.markDeferredToolsLoaded(names...)

	return mustJSON(map[string]any{
		"action":          "tool_search",
		"query":           query,
		"matched":         len(matches),
		"loaded_tools":    names,
		"already_loaded":  alreadyLoaded,
		"newly_loaded":    newlyLoaded,
		"loadable_tools":  loadableTools,
		"tools":           matches,
		"schemas_loaded":  len(matches) > 0,
		"surface_changed": len(newlyLoaded) > 0,
	})
}

type toolSearchMatch struct {
	Name            string                           `json:"name"`
	Kind            ToolKind                         `json:"kind"`
	Summary         string                           `json:"summary"`
	Tags            []string                         `json:"tags,omitempty"`
	Description     string                           `json:"description"`
	ReadOnly        bool                             `json:"read_only"`
	ConcurrencySafe bool                             `json:"concurrency_safe"`
	Score           int                              `json:"score"`
	LoadableTool    providers.LoadableToolDefinition `json:"-"`
}

func (t *Toolkit) searchDeferredTools(query string, limit int) []toolSearchMatch {
	if names, ok := toolSearchSelectNames(query); ok {
		return t.selectDeferredTools(names, limit)
	}
	tokens := searchTokens(query)
	if len(tokens) == 0 || limit <= 0 {
		return nil
	}
	var matches []toolSearchMatch
	surface := t.activeCompiledSurface()
	for _, tool := range t.allKnownTools() {
		if !activeSurfaceAllowsKnownTool(surface, tool) {
			continue
		}
		if t.toolExposure(tool.Name()) != ToolExposureDeferred {
			continue
		}
		def := tool.Definition()
		score := scoreToolSearchMatch(tool, def, tokens)
		if score == 0 {
			continue
		}
		desc, _ := truncate(def.Description, 600)
		entry := deferredToolCatalogEntry(tool)
		matches = append(matches, toolSearchMatch{
			Name:            tool.Name(),
			Kind:            classifyToolKind(tool.Name()),
			Summary:         entry.Summary,
			Tags:            entry.Tags,
			Description:     desc,
			ReadOnly:        tool.IsReadOnly(),
			ConcurrencySafe: tool.IsConcurrencySafe(),
			Score:           score,
			LoadableTool:    providers.LoadableToolFromDefinition(def),
		})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Name < matches[j].Name
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func (t *Toolkit) selectDeferredTools(names []string, limit int) []toolSearchMatch {
	if len(names) == 0 || limit <= 0 {
		return nil
	}
	surface := t.activeCompiledSurface()
	toolsByName := make(map[string]Tool)
	for _, tool := range t.allKnownTools() {
		if !activeSurfaceAllowsKnownTool(surface, tool) {
			continue
		}
		if t.toolExposure(tool.Name()) != ToolExposureDeferred {
			continue
		}
		toolsByName[tool.Name()] = tool
	}
	matches := make([]toolSearchMatch, 0, min(limit, len(names)))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		// Keep discovery prompts written before the provider-safe rename working
		// without exposing the reserved name itself to model providers.
		if name == "browser" {
			name = browserToolName
		}
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		tool := toolsByName[name]
		if tool == nil {
			continue
		}
		matches = append(matches, buildToolSearchMatch(tool, 1000-len(matches)))
		if len(matches) >= limit {
			break
		}
	}
	return matches
}

func toolSearchSelectNames(query string) ([]string, bool) {
	query = strings.TrimSpace(query)
	if !strings.HasPrefix(strings.ToLower(query), "select:") {
		return nil, false
	}
	raw := strings.TrimSpace(query[len("select:"):])
	if raw == "" {
		return nil, true
	}
	raw = strings.ReplaceAll(raw, ",", " ")
	return strings.Fields(raw), true
}

func (t *Toolkit) allKnownTools() []Tool {
	all := t.registry.All()
	for _, tool := range t.mcpToolsSnapshot() {
		all = append(all, tool)
	}
	return all
}

func buildToolSearchMatch(tool Tool, score int) toolSearchMatch {
	def := tool.Definition()
	desc, _ := truncate(def.Description, 600)
	entry := deferredToolCatalogEntry(tool)
	return toolSearchMatch{
		Name:            tool.Name(),
		Kind:            classifyToolKind(tool.Name()),
		Summary:         entry.Summary,
		Tags:            entry.Tags,
		Description:     desc,
		ReadOnly:        tool.IsReadOnly(),
		ConcurrencySafe: tool.IsConcurrencySafe(),
		Score:           score,
		LoadableTool:    providers.LoadableToolFromDefinition(def),
	}
}

func (t *Toolkit) markDeferredToolsLoaded(names ...string) {
	if len(names) == 0 {
		return
	}
	toLoad := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || t.isToolDisabled(name) || t.toolExposure(name) != ToolExposureDeferred {
			continue
		}
		toLoad = append(toLoad, name)
	}
	if len(toLoad) == 0 {
		return
	}
	t.exposureMu.Lock()
	defer t.exposureMu.Unlock()
	if t.loadedDeferredTools == nil {
		t.loadedDeferredTools = make(map[string]struct{}, len(toLoad))
	}
	for _, name := range toLoad {
		t.loadedDeferredTools[name] = struct{}{}
	}
}

func (t *Toolkit) isDeferredToolLoaded(name string) bool {
	t.exposureMu.RLock()
	defer t.exposureMu.RUnlock()
	_, ok := t.loadedDeferredTools[name]
	return ok
}

func searchTokens(query string) []string {
	fields := strings.Fields(strings.ToLower(query))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, " \t\n\r.,:;()[]{}\"'")
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func scoreToolSearchMatch(tool Tool, def providers.ToolDefinition, tokens []string) int {
	name := strings.ToLower(def.Name)
	haystack := toolSearchText(tool, def)
	score := 0
	for _, token := range tokens {
		if token == "" {
			continue
		}
		if strings.Contains(name, token) {
			score += 20
		}
		if strings.Contains(haystack, token) {
			score += 10
		}
	}
	return score
}

func toolSearchText(tool Tool, def providers.ToolDefinition) string {
	entry := deferredToolCatalogEntry(tool)
	parts := []string{
		strings.ToLower(def.Name),
		strings.ToLower(strings.ReplaceAll(def.Name, "_", " ")),
		strings.ToLower(entry.Summary),
		strings.ToLower(strings.Join(entry.Tags, " ")),
		strings.ToLower(def.Description),
	}
	if len(def.InputSchema) > 0 {
		if raw, err := json.Marshal(def.InputSchema); err == nil {
			parts = append(parts, strings.ToLower(string(raw)))
		}
	}
	return strings.Join(parts, " ")
}
