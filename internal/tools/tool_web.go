package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// ---------------------------------------------------------------------------
// web_search
// ---------------------------------------------------------------------------

type WebSearchTool struct{ env *Env }

func NewWebSearchTool(env *Env) *WebSearchTool { return &WebSearchTool{env: env} }

func (t *WebSearchTool) Name() string            { return "web_search" }
func (t *WebSearchTool) IsReadOnly() bool        { return true }
func (t *WebSearchTool) IsConcurrencySafe() bool { return true }

func (t *WebSearchTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "web_search",
		Description: "Search the web using DuckDuckGo. Returns evidence metadata plus titles, URLs, and snippets.\n\n" +
			"Usage:\n" +
			"- Use for finding documentation, examples, or researching APIs\n" +
			"- Treat results as web evidence, not repository truth; match claims against the repo's dependency versions before editing\n" +
			"- Prefer official documentation or primary sources when choosing a URL to fetch\n" +
			"- Returns up to 10 results with title, URL, and snippet\n" +
			"- For fetching a specific URL's content, use web_fetch instead",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query.",
				},
				"version_hint": map[string]any{
					"type":        "string",
					"description": "Optional repo dependency or API version this search should be matched against, for example next@15.2.1.",
				},
				"package_context": webPackageContextSchema(),
			},
			"required": []string{"query"},
		},
	}
}

func (t *WebSearchTool) ValidateInput(argsJSON string) error {
	var args struct {
		Query string `json:"query"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.Query) == "" {
		return errors.New("web_search requires query")
	}
	return nil
}

func (t *WebSearchTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	result, err := webSearchExecute(ctx, argsJSON)
	if err == nil {
		recordWebEvidenceResult(t.env, t.Name(), result)
	}
	return result, err
}

// ---------------------------------------------------------------------------
// web_fetch
// ---------------------------------------------------------------------------

type WebFetchTool struct{ env *Env }

func NewWebFetchTool(env *Env) *WebFetchTool { return &WebFetchTool{env: env} }

func (t *WebFetchTool) Name() string            { return "web_fetch" }
func (t *WebFetchTool) IsReadOnly() bool        { return true }
func (t *WebFetchTool) IsConcurrencySafe() bool { return true }

func (t *WebFetchTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "web_fetch",
		Description: "Fetch a URL and return evidence metadata plus readable text.\n\n" +
			"Usage:\n" +
			"- Treat fetched content as web evidence; match claims against the repo's dependency versions before editing\n" +
			"- Prefer official documentation, changelogs, or primary sources for implementation decisions\n" +
			"- HTML is automatically converted to readable text (scripts, nav, footer stripped)\n" +
			"- JSON responses are pretty-printed\n" +
			"- Reads up to 1 MiB; returned text also fits the 1 MiB serialized result limit. truncated=true reports either limit\n" +
			"- Local/private network targets are blocked unless full access is active\n" +
			"- 30 second timeout",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "URL to fetch.",
				},
				"version_hint": map[string]any{
					"type":        "string",
					"description": "Optional repo dependency or API version this fetch should be matched against, for example next@15.2.1.",
				},
				"package_context": webPackageContextSchema(),
			},
			"required": []string{"url"},
		},
	}
}

func (t *WebFetchTool) ValidateInput(argsJSON string) error {
	var args struct {
		URL string `json:"url"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.URL) == "" {
		return errors.New("web_fetch requires url")
	}
	return nil
}

func (t *WebFetchTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	result, err := webFetchExecute(ctx, argsJSON, t.env.BypassToolHardProtections())
	if err == nil {
		recordWebEvidenceResult(t.env, t.Name(), result)
	}
	return result, err
}

func webPackageContextSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "Optional package coordinates from the repo so web evidence can be checked against the installed dependency version.",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Package or library name from the repo.",
			},
			"version": map[string]any{
				"type":        "string",
				"description": "Version found in the repo manifest or lockfile.",
			},
			"ecosystem": map[string]any{
				"type":        "string",
				"description": "Package ecosystem, for example npm, pypi, go, cargo, or maven.",
			},
		},
	}
}

func recordWebEvidenceResult(env *Env, toolName, result string) {
	if env == nil || result == "" {
		return
	}
	var payload struct {
		Evidence    webEvidence       `json:"evidence"`
		Error       string            `json:"error"`
		Results     []json.RawMessage `json:"results"`
		StatusCode  int               `json:"status_code"`
		ContentType string            `json:"content_type"`
		Size        int               `json:"size"`
		Truncated   bool              `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil || payload.Evidence.ID == "" {
		return
	}
	env.RecordWebEvidence(webEvidenceEntry{
		ToolName:    toolName,
		Evidence:    payload.Evidence,
		Error:       payload.Error,
		ResultCount: len(payload.Results),
		StatusCode:  payload.StatusCode,
		ContentType: payload.ContentType,
		Size:        payload.Size,
		Truncated:   payload.Truncated,
	})
}

// RecordWebSearchEvidence preserves the native evidence context for a selected
// replacement after its result has passed the host's contract validation.
func (t *Toolkit) RecordWebSearchEvidence(result string) {
	recordWebEvidenceResult(t.env, "web_search", result)
}

// WebSearchEvidenceVersion returns the same version context the native search
// records, without trusting a replacement to invent a repository match.
func WebSearchEvidenceVersion(argsJSON string) (string, error) {
	var args struct {
		VersionHint    string            `json:"version_hint"`
		PackageContext webPackageContext `json:"package_context"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	version := webVersionMatchedToRepo(args.VersionHint, args.PackageContext)
	if version == "" {
		return "unknown", nil
	}
	return version, nil
}

// ContinueNativeWebSearch performs an admitted native search without publishing
// an intermediate evidence entry. The replacement commits its final evidence.
func (t *Toolkit) ContinueNativeWebSearch(ctx context.Context, original Tool, call providers.ToolCall) (toolresult.Result, error) {
	return t.continueNativeWebSearch(ctx, original, call, duckDuckGoSearch)
}

func (t *Toolkit) continueNativeWebSearch(ctx context.Context, original Tool, call providers.ToolCall, search func(context.Context, string, int) ([]searchResult, error)) (toolresult.Result, error) {
	if err := ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	native, ok := original.(*WebSearchTool)
	if !ok || native.env != t.env || call.Name != "web_search" {
		return toolresult.Result{}, errors.New("invalid native search continuation")
	}
	if t.IsToolDisabled(call.Name) {
		return toolresult.Result{}, errors.New("native search is disabled")
	}
	if err := native.ValidateInput(call.Arguments); err != nil {
		return toolresult.Result{}, err
	}
	if err := t.checkPermission(ctx, buildToolInfoForArgs(native, t.toolExposure(call.Name), call.Arguments), call); err != nil {
		return toolresult.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	value, err := webSearchExecuteWith(ctx, call.Arguments, search)
	if err := ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	if err != nil {
		return toolresult.Result{}, err
	}
	return toolresult.FromText(value), nil
}
