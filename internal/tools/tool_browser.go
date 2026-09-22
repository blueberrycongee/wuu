package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// BrowserTool is the single model-facing surface for the embedded browser
// backend. One tool multiplexed by an action enum (matching bash)
// drives navigation, DOM observation, input, screenshots, and tab lifecycle
// against a desktop-hosted hidden WebContentsView reached through env.BrowserBridge.
//
// The activity binding (lease acquire, before/after CheckControl, state/preview
// publish) is owned by the toolkit orchestration in executeBrowserToolResult;
// this tool executes the leaf bridge round-trips for one action and reconciles
// the durable tab store. It reads the resolved activity/tab facts from the
// browser action context the orchestration injects.
type BrowserTool struct{ env *Env }

func NewBrowserTool(env *Env) *BrowserTool { return &BrowserTool{env: env} }

func (t *BrowserTool) Name() string { return browserToolName }

// IsReadOnly reports the tool's default (mutating) posture. Per-call read-only
// status is refined by Classify so observe/screenshot/tabs/wait_for stay usable
// in read-only sessions while navigate/click/type/etc. are blocked.
func (t *BrowserTool) IsReadOnly() bool        { return false }
func (t *BrowserTool) IsConcurrencySafe() bool { return false }

// browserReadOnlyActions are the actions that only inspect page state. They are
// permitted under a read-only workspace boundary; everything else mutates.
var browserReadOnlyActions = map[string]struct{}{
	"observe":    {},
	"screenshot": {},
	"tabs":       {},
	"wait_for":   {},
}

// browserKnownActions is the closed action set the tool accepts.
var browserKnownActions = map[string]struct{}{
	"navigate": {}, "observe": {}, "click": {}, "type": {}, "scroll": {}, "key": {},
	"screenshot": {}, "tabs": {}, "finalize": {}, "sequence": {}, "set_visibility": {}, "wait_for": {},
}

func (t *BrowserTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: browserToolName,
		Description: "Drive an embedded browser: navigate pages, observe the DOM, click, type, scroll, screenshot, and manage tabs through a single action parameter. " +
			"Tabs stay hidden in the background by default; visiting a page does not show it to the user. " +
			"Call set_visibility with visible=true only when the user's main goal is to watch the page, then set_visibility false or finalize when that goal ends. " +
			"Read-only actions (observe, screenshot, tabs, wait_for) inspect page state; the others mutate it. " +
			"Screenshot saves a UI preview and returns its path and dimensions, not image content to the model. " +
			"observe returns readable content (headings, paragraphs, lists, and tables) and the interactive nodes from the same page view. Content that can be clicked includes its node_id. " +
			"A long page sets content_next_offset; pass that value as content_offset to read the next slice. Node ids expire on the next observe. " +
			"finalize keeps only tabs named in keep. A tab_id without keep retains that one tab as handoff. " +
			"Prefer node ids from observe over raw coordinates, and re-observe after an input to confirm the outcome before continuing.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{
						"navigate", "observe", "click", "type", "scroll", "key",
						"screenshot", "tabs", "finalize", "sequence", "set_visibility", "wait_for",
					},
					"description": "Required. Selects the browser operation to perform. Use set_visibility only to show or hide the page; navigate and observe stay in the hidden host.",
				},
				"tab_id": map[string]any{
					"type":        "string",
					"description": "Target tab. Omit on navigate to open a new tab; the tool mints and returns a tab_id. On finalize, a tab_id without keep retains that tab as handoff.",
				},
				"url": map[string]any{
					"type":        "string",
					"description": "Used by action=navigate: the destination URL. Opening this URL does not make the tab visible.",
				},
				"visible": map[string]any{
					"type":        "boolean",
					"description": "Used by action=set_visibility. true overlays the tab in the current session's right-side browser; false returns it to the hidden host.",
				},
				"node_id": map[string]any{
					"type":        "integer",
					"description": "Node from the latest observe. Used by click, type, and scroll. Ids are invalid after the next observe.",
				},
				"x":    map[string]any{"type": "number", "description": "CSS pixel x. Used by click or scroll when node_id is omitted."},
				"y":    map[string]any{"type": "number", "description": "CSS pixel y. Used by click or scroll when node_id is omitted."},
				"text": map[string]any{"type": "string", "description": "Used by action=type. Inserted into the focused node or the node_id target."},
				"keys": map[string]any{
					"description": "Used by action=key. One key name, such as Enter, or a list of key names.",
					"oneOf": []any{
						map[string]any{"type": "string"},
						map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
				},
				"dx":         map[string]any{"type": "number", "description": "Horizontal wheel delta for action=scroll. Omit both dx and dy to scroll down one viewport."},
				"dy":         map[string]any{"type": "number", "description": "Vertical wheel delta for action=scroll. Positive scrolls down."},
				"timeout_ms": map[string]any{"type": "integer", "description": "Used by action=wait_for. Bounded to 60000 milliseconds."},
				"content_offset": map[string]any{
					"type":        "integer",
					"description": "Used by action=observe. Zero-based block offset into the page's readable content. Pass the previous observe's content_next_offset to continue. Interactive node ids always come from this observe.",
				},
				"keep": map[string]any{
					"type":        "array",
					"description": "Used by action=finalize. Tabs to retain. Each item names tab_id and status (deliverable or handoff). Omitted tabs, including popups opened by the page, are closed. An explicit keep list wins over tab_id.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"tab_id": map[string]any{"type": "string"},
							"status": map[string]any{
								"type": "string",
								"enum": []string{"deliverable", "handoff"},
							},
						},
						"required": []string{"tab_id"},
					},
				},
				"steps": map[string]any{
					"type":        "array",
					"description": "Used by action=sequence. One to 64 actions on this tab. Each step must set risk to safe, external_side_effect, or destructive; a non-safe step also requires confirmed=true.",
					"items":       map[string]any{"type": "object"},
				},
			},
			"required": []string{"action"},
		},
	}
}

// Classify refines read-only status and risk per call. boundary.Check consults
// the resulting ReadOnly, so a read-only session blocks any mutating action here.
func (t *BrowserTool) Classify(argsJSON string) ToolClassification {
	action := decodeActionField(argsJSON)
	_, readOnly := browserReadOnlyActions[action]
	risk := ToolRiskHigh
	if readOnly {
		risk = ToolRiskMedium
	}
	return ToolClassification{
		ReadOnly:        readOnly,
		ConcurrencySafe: false,
		Risk:            risk,
	}
}

// ValidateInput rejects malformed calls before any lease is acquired.
func (t *BrowserTool) ValidateInput(argsJSON string) error {
	action := decodeActionField(argsJSON)
	if action == "" {
		return errors.New("browser tool requires an action: error_kind=missing_action")
	}
	if _, ok := browserKnownActions[action]; !ok {
		return fmt.Errorf("browser tool: unsupported action %q: error_kind=unknown_action", action)
	}
	if action == "navigate" {
		var probe struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal([]byte(argsJSON), &probe)
		if strings.TrimSpace(probe.URL) == "" {
			return errors.New("browser navigate requires url: error_kind=missing_url")
		}
	}
	return nil
}

// errBrowserUnavailable is the clear, non-retryable error returned when no
// desktop-hosted browser backend is wired (CLI/headless runtime).
var errBrowserUnavailable = errors.New("embedded browser requires the wuu desktop client: error_kind=browser_unavailable model_next_action=\"do not retry; this environment has no embedded browser backend\"")

// browserCDPMethod is the single server->desktop wire method for semantic
// browser actions; the semantic action rides in BrowserCDPParams.Method.
const browserCDPMethod = "browser/cdp"

// browserCDPRequest mirrors appserver.BrowserCDPParams (snake_case) so the tool
// never imports the appserver wire types. The bridge marshals it as-is.
type browserCDPRequest struct {
	Workdir string          `json:"workdir"`
	TabID   string          `json:"tab_id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// browserCDPEnvelope mirrors appserver.BrowserCDPResult: the semantic result is
// inline in Result, or spilled to Path (with Size) when the desktop size gate
// tripped on a large payload.
type browserCDPEnvelope struct {
	Result json.RawMessage `json:"result,omitempty"`
	Path   string          `json:"path,omitempty"`
	Size   int             `json:"size,omitempty"`
}

type browserObservation struct {
	URL               string                `json:"url"`
	Title             string                `json:"title"`
	Nodes             []browserNode         `json:"nodes"`
	Content           []browserContentBlock `json:"content"`
	ContentOffset     int                   `json:"content_offset"`
	ContentTotal      int                   `json:"content_total"`
	ContentNextOffset *int                  `json:"content_next_offset"`
	ScreenshotPath    string                `json:"screenshot_path"`
}

type browserContentBlock struct {
	Kind    string                  `json:"kind"`
	Level   int                     `json:"level,omitempty"`
	Text    string                  `json:"text,omitempty"`
	NodeID  int                     `json:"node_id,omitempty"`
	Ordered bool                    `json:"ordered,omitempty"`
	Links   []browserContentPiece   `json:"links,omitempty"`
	Items   []browserContentPiece   `json:"items,omitempty"`
	Header  []browserContentPiece   `json:"header,omitempty"`
	Rows    [][]browserContentPiece `json:"rows,omitempty"`
}

type browserContentPiece struct {
	Text   string `json:"text,omitempty"`
	NodeID int    `json:"node_id,omitempty"`
}

type browserNode struct {
	NodeID int       `json:"node_id"`
	Role   string    `json:"role"`
	Name   string    `json:"name"`
	Value  string    `json:"value,omitempty"`
	Bounds []float64 `json:"bounds,omitempty"`
}

// ExecuteResult runs the leaf bridge work for one browser action. It expects the
// browser action context (activity id + resolved tab id + preview sink) that
// executeBrowserToolResult injects for tab-addressed actions; tabs/finalize run
// without it.
func (t *BrowserTool) ExecuteResult(ctx context.Context, argsJSON string) (toolresult.Result, error) {
	if t.env == nil || t.env.BrowserBridge == nil {
		return toolresult.Result{}, errBrowserUnavailable
	}
	action := decodeActionField(argsJSON)
	bctx, _ := browserActionContextFrom(ctx)
	switch action {
	case "navigate":
		return t.doNavigate(ctx, argsJSON, bctx)
	case "observe":
		return t.doObserve(ctx, argsJSON, bctx)
	case "click", "type", "scroll", "key":
		return t.doInput(ctx, action, argsJSON, bctx)
	case "screenshot":
		return t.doScreenshot(ctx, argsJSON, bctx)
	case "set_visibility":
		return t.doSetVisibility(ctx, argsJSON, bctx)
	case "wait_for":
		return t.doWaitFor(ctx, argsJSON, bctx)
	case "tabs":
		return t.doTabs(ctx)
	case "finalize":
		return t.doFinalize(ctx, argsJSON)
	default:
		return toolresult.Result{}, fmt.Errorf("browser tool: unsupported action %q: error_kind=unknown_action", action)
	}
}

// Execute is retained for the plain Tool interface; the toolkit always prefers
// ExecuteResult (RichTool) for the browser tool.
func (t *BrowserTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	result, err := t.ExecuteResult(ctx, argsJSON)
	return result.TextProjection(), err
}

// redactURLForModel strips credentials the model must never receive from a page
// URL — userinfo and query values whose key names a token/secret/code — while
// leaving the URL otherwise intact. The durable tab store keeps the real URL for
// rebuild-by-navigation; only the model-facing copy is redacted.
func redactURLForModel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return redactToolOutput(raw)
	}
	if u.User != nil {
		u.User = url.User("redacted")
	}
	if q := u.Query(); len(q) > 0 {
		changed := false
		for k, vs := range q {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "token") || strings.Contains(lk, "secret") ||
				strings.Contains(lk, "password") || strings.Contains(lk, "passwd") ||
				strings.Contains(lk, "auth") || strings.Contains(lk, "session") ||
				strings.Contains(lk, "apikey") || lk == "code" || lk == "key" {
				for i := range vs {
					vs[i] = "redacted"
				}
				q[k] = vs
				changed = true
			}
		}
		if changed {
			u.RawQuery = q.Encode()
		}
	}
	return u.String()
}

func (t *BrowserTool) doNavigate(ctx context.Context, argsJSON string, bctx browserActionContext) (toolresult.Result, error) {
	var args struct {
		URL string `json:"url"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return toolresult.Result{}, err
	}
	dest := strings.TrimSpace(args.URL)
	if dest == "" {
		return toolresult.Result{}, errors.New("browser navigate requires url: error_kind=missing_url")
	}
	tabID := t.resolveTabID(argsJSON, bctx)
	if tabID == "" {
		return toolresult.Result{}, errBrowserMissingTab("navigate")
	}
	if bctx.newTab {
		// A fresh tab: create the hidden view and land it on the URL in one step.
		if err := t.env.BrowserBridge.OpenTab(ctx, tabID, dest); err != nil {
			return toolresult.Result{}, browserBridgeError("navigate", tabID, err)
		}
		t.rememberTab(tabID, dest, "", false)
		return browserResult("navigate", map[string]any{"tab_id": tabID, "url": dest}), nil
	}
	// Existing tab: navigate in place. A lost view rebuilds at the destination.
	raw, err := t.cdpWithRebuild(ctx, tabID, "navigate", map[string]any{"url": dest}, dest, true)
	if err != nil {
		return toolresult.Result{}, err
	}
	var nav struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	_ = json.Unmarshal(raw, &nav)
	if strings.TrimSpace(nav.URL) == "" {
		nav.URL = dest
	}
	t.rememberTab(tabID, nav.URL, nav.Title, false)
	return browserResult("navigate", map[string]any{"tab_id": tabID, "url": redactURLForModel(nav.URL), "title": nav.Title}), nil
}

func (t *BrowserTool) doObserve(ctx context.Context, argsJSON string, bctx browserActionContext) (toolresult.Result, error) {
	tabID := t.resolveTabID(argsJSON, bctx)
	if tabID == "" {
		return toolresult.Result{}, errBrowserMissingTab("observe")
	}
	destPath := t.previewPath(bctx)
	params := map[string]any{"screenshot": destPath != "", "dest_path": destPath}
	var observeArgs struct {
		ContentOffset int `json:"content_offset"`
	}
	_ = decodeArgs(argsJSON, &observeArgs)
	if observeArgs.ContentOffset > 0 {
		params["content_offset"] = observeArgs.ContentOffset
	}
	raw, err := t.cdpWithRebuild(ctx, tabID, "observe", params, t.tabURL(tabID), true)
	if err != nil {
		return toolresult.Result{}, err
	}
	var obs browserObservation
	_ = json.Unmarshal(raw, &obs)
	realURL := obs.URL
	// Page-derived text passes through credential redaction before the model ever
	// sees it: node names/values, readable content, the title, and the URL
	// (which can carry OAuth codes or session/reset tokens in userinfo or query).
	for i := range obs.Nodes {
		obs.Nodes[i].Name = redactToolOutput(obs.Nodes[i].Name)
		obs.Nodes[i].Value = redactToolOutput(obs.Nodes[i].Value)
	}
	redactBrowserContent(obs.Content)
	obs.Title = redactToolOutput(obs.Title)
	obs.URL = redactURLForModel(obs.URL)
	payload := map[string]any{
		"action": "observe", "tab_id": tabID, "url": obs.URL, "title": obs.Title, "nodes": obs.Nodes,
	}
	if obs.Content != nil {
		payload["content"] = obs.Content
		payload["content_offset"] = obs.ContentOffset
		payload["content_total"] = obs.ContentTotal
		if obs.ContentNextOffset != nil {
			payload["content_next_offset"] = *obs.ContentNextOffset
		}
	}
	structured, _ := json.Marshal(payload)
	// The screenshot is a UI preview only: reference it as a file:// URI on the
	// activity, never as a base64 image in the model result.
	if bctx.setPreview != nil {
		if sp := strings.TrimSpace(obs.ScreenshotPath); sp != "" {
			bctx.setPreview(fileURI(sp))
		} else if destPath != "" {
			bctx.setPreview(fileURI(destPath))
		}
	}
	t.rememberTab(tabID, realURL, obs.Title, false)
	return toolresult.Result{
		Content:           []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: renderObserveText(tabID, obs)}},
		StructuredContent: structured,
	}, nil
}

func (t *BrowserTool) doInput(ctx context.Context, action, argsJSON string, bctx browserActionContext) (toolresult.Result, error) {
	tabID := t.resolveTabID(argsJSON, bctx)
	if tabID == "" {
		return toolresult.Result{}, errBrowserMissingTab(action)
	}
	// Input actions can have side effects, so a lost tab is rebuilt but the input
	// is not replayed (that would risk a double submit); the model must observe.
	if _, err := t.cdpWithRebuild(ctx, tabID, action, browserForwardParams(argsJSON), t.tabURL(tabID), false); err != nil {
		return toolresult.Result{}, err
	}
	return browserResult(action, map[string]any{"tab_id": tabID, "ok": true}), nil
}

func (t *BrowserTool) doScreenshot(ctx context.Context, argsJSON string, bctx browserActionContext) (toolresult.Result, error) {
	tabID := t.resolveTabID(argsJSON, bctx)
	if tabID == "" {
		return toolresult.Result{}, errBrowserMissingTab("screenshot")
	}
	destPath := t.previewPath(bctx)
	if destPath == "" {
		return toolresult.Result{}, errors.New("browser screenshot requires a session artifact directory: error_kind=no_artifact_dir")
	}
	var args struct {
		Format string `json:"format"`
	}
	_ = decodeArgs(argsJSON, &args)
	format := strings.TrimSpace(args.Format)
	if format == "" {
		format = "png"
	}
	res, err := t.env.BrowserBridge.Screenshot(ctx, tabID, destPath, format)
	if err != nil && isTabNotFound(err) {
		if url := t.tabURL(tabID); url != "" {
			if oerr := t.env.BrowserBridge.OpenTab(ctx, tabID, url); oerr == nil {
				t.rememberTab(tabID, url, "", false)
				res, err = t.env.BrowserBridge.Screenshot(ctx, tabID, destPath, format)
			}
		}
		if err != nil {
			// Genuinely lost view that could not be rebuilt: drop the durable
			// record so a later action does not chase a dead tab.
			t.forgetTab(tabID)
			return toolresult.Result{}, browserBridgeError("screenshot", tabID, err)
		}
	} else if err != nil {
		// A generic bridge error (e.g. a transient false timeout while the view
		// is still alive — see client_calls.go) must NOT discard the tab's
		// recovery URL; mirror cdpWithRebuild / doSetVisibility and keep it.
		return toolresult.Result{}, browserBridgeError("screenshot", tabID, err)
	}
	previewTarget := strings.TrimSpace(res.Path)
	if previewTarget == "" {
		previewTarget = destPath
	}
	if bctx.setPreview != nil {
		bctx.setPreview(fileURI(previewTarget))
	}
	return browserResult("screenshot", map[string]any{"tab_id": tabID, "width": res.Width, "height": res.Height, "path": previewTarget}), nil
}

func (t *BrowserTool) doSetVisibility(ctx context.Context, argsJSON string, bctx browserActionContext) (toolresult.Result, error) {
	tabID := t.resolveTabID(argsJSON, bctx)
	if tabID == "" {
		return toolresult.Result{}, errBrowserMissingTab("set_visibility")
	}
	var args struct {
		Visible bool `json:"visible"`
	}
	_ = decodeArgs(argsJSON, &args)
	err := t.env.BrowserBridge.SetVisibility(ctx, tabID, args.Visible)
	if err != nil && isTabNotFound(err) {
		if url := t.tabURL(tabID); url != "" {
			if oerr := t.env.BrowserBridge.OpenTab(ctx, tabID, url); oerr == nil {
				t.rememberTab(tabID, url, "", false)
				err = t.env.BrowserBridge.SetVisibility(ctx, tabID, args.Visible)
			}
		}
	}
	if err != nil {
		return toolresult.Result{}, browserBridgeError("set_visibility", tabID, err)
	}
	return browserResult("set_visibility", map[string]any{"tab_id": tabID, "visible": args.Visible}), nil
}

func (t *BrowserTool) doWaitFor(ctx context.Context, argsJSON string, bctx browserActionContext) (toolresult.Result, error) {
	tabID := t.resolveTabID(argsJSON, bctx)
	if tabID == "" {
		return toolresult.Result{}, errBrowserMissingTab("wait_for")
	}
	var args struct {
		TimeoutMS int    `json:"timeout_ms"`
		Until     string `json:"until"`
	}
	_ = decodeArgs(argsJSON, &args)
	total := args.TimeoutMS
	if total <= 0 {
		total = 10000
	}
	if total > 60000 {
		total = 60000
	}
	// Slice the wait into <=10s bridge calls: a single long call would collide
	// with the bridge's 30s per-call timeout and read as a false failure.
	const slice = 10000
	remaining := total
	waited := 0
	changed := false
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return toolresult.Result{}, err
		}
		chunk := remaining
		if chunk > slice {
			chunk = slice
		}
		params := map[string]any{"timeout_ms": chunk}
		if u := strings.TrimSpace(args.Until); u != "" {
			params["until"] = u
		}
		raw, err := t.cdpWithRebuild(ctx, tabID, "wait", params, t.tabURL(tabID), true)
		if err != nil {
			return toolresult.Result{}, err
		}
		var w struct {
			OK      bool `json:"ok"`
			Changed bool `json:"changed"`
		}
		_ = json.Unmarshal(raw, &w)
		waited += chunk
		remaining -= chunk
		if w.Changed {
			changed = true
			break
		}
	}
	return browserResult("wait_for", map[string]any{"tab_id": tabID, "changed": changed, "waited_ms": waited}), nil
}

func (t *BrowserTool) doTabs(ctx context.Context) (toolresult.Result, error) {
	live, records, err := t.syncLiveTabs(ctx)
	if err != nil {
		return toolresult.Result{}, err
	}
	ids := browserLiveIDs(live)
	// The registry keeps the real URL so a lost view can be rebuilt. The model
	// sees the same redaction observe applies, including a popup it did not open.
	visible := modelTabRecords(records)
	structured, _ := json.Marshal(map[string]any{"action": "tabs", "live_tab_ids": ids, "tabs": visible})
	return toolresult.Result{
		Content:           []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: renderTabsText(ids, visible)}},
		StructuredContent: structured,
	}, nil
}

func modelTabRecords(records []BrowserTabRecord) []BrowserTabRecord {
	if len(records) == 0 {
		return records
	}
	out := make([]BrowserTabRecord, len(records))
	for i, rec := range records {
		rec.URL = redactURLForModel(rec.URL)
		rec.Title = redactToolOutput(rec.Title)
		out[i] = rec
	}
	return out
}

func (t *BrowserTool) doFinalize(ctx context.Context, argsJSON string) (toolresult.Result, error) {
	keep := browserKeepSet(argsJSON)
	live, liveKnown := t.listLiveTabs(ctx)
	liveByID := make(map[string]BrowserLiveTab, len(live))
	for _, tab := range live {
		liveByID[tab.ID] = tab
	}
	var records []BrowserTabRecord
	if t.env.BrowserTabs != nil {
		records, _ = t.env.BrowserTabs.List()
	}
	var closed, kept []string
	seen := make(map[string]bool, len(records))
	if t.env.BrowserTabs != nil {
		for _, rec := range records {
			seen[rec.TabID] = true
			if status, ok := keep[rec.TabID]; ok {
				rec.Status = status
				rec.UpdatedAt = time.Time{}
				if strings.TrimSpace(rec.URL) == "" {
					if tab, ok := liveByID[rec.TabID]; ok {
						rec.URL = tab.URL
						if strings.TrimSpace(rec.Title) == "" {
							rec.Title = tab.Title
						}
					}
				}
				_ = t.env.BrowserTabs.Put(rec)
				kept = append(kept, rec.TabID)
				delete(keep, rec.TabID)
				continue
			}
			// Not kept: tear down the hidden view (best effort) and drop the record.
			_ = t.env.BrowserBridge.CloseTab(ctx, rec.TabID)
			_ = t.env.BrowserTabs.Delete(rec.TabID)
			closed = append(closed, rec.TabID)
		}
	}
	// A page-opened window is a live view before it has a durable record.
	// Close it unless the model kept it. When the host list is unavailable,
	// leave those views alone rather than guessing.
	if liveKnown {
		for _, tab := range live {
			if seen[tab.ID] {
				continue
			}
			if status, ok := keep[tab.ID]; ok {
				if t.env.BrowserTabs != nil {
					_ = t.env.BrowserTabs.Put(BrowserTabRecord{TabID: tab.ID, URL: tab.URL, Title: tab.Title, Status: status})
				}
				kept = append(kept, tab.ID)
				delete(keep, tab.ID)
				continue
			}
			_ = t.env.BrowserBridge.CloseTab(ctx, tab.ID)
			closed = append(closed, tab.ID)
		}
	}
	// Kept tabs the model minted this turn may not be in the store yet.
	if t.env.BrowserTabs != nil {
		for id, status := range keep {
			_ = t.env.BrowserTabs.Put(BrowserTabRecord{TabID: id, Status: status})
			kept = append(kept, id)
		}
	}
	structured, _ := json.Marshal(map[string]any{"action": "finalize", "kept": kept, "closed": closed})
	return toolresult.Result{
		Content:           []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: renderFinalizeText(kept, closed)}},
		StructuredContent: structured,
	}, nil
}

// syncLiveTabs reconciles the durable registry with the host. Views the host
// no longer has are marked dead so the next use rebuilds by URL. Views the
// host has but the registry does not — a popup adopted on the desktop — are
// recorded so the model can address them and finalize can close them.
func (t *BrowserTool) syncLiveTabs(ctx context.Context) ([]BrowserLiveTab, []BrowserTabRecord, error) {
	live, err := t.env.BrowserBridge.ListTabs(ctx)
	if err != nil {
		return nil, nil, browserBridgeError("tabs", "", err)
	}
	cleaned := normalizeLiveTabs(live)
	liveByID := make(map[string]BrowserLiveTab, len(cleaned))
	for _, tab := range cleaned {
		liveByID[tab.ID] = tab
	}
	if t.env.BrowserTabs == nil {
		return cleaned, nil, nil
	}
	stored, listErr := t.env.BrowserTabs.List()
	if listErr != nil {
		return cleaned, nil, nil
	}
	seen := make(map[string]bool, len(stored))
	for _, rec := range stored {
		seen[rec.TabID] = true
		tab, liveOK := liveByID[rec.TabID]
		changed := false
		if liveOK {
			if rec.Dead {
				rec.Dead = false
				changed = true
			}
			if strings.TrimSpace(rec.URL) == "" && tab.URL != "" {
				rec.URL = tab.URL
				changed = true
			}
			if strings.TrimSpace(rec.Title) == "" && tab.Title != "" {
				rec.Title = tab.Title
				changed = true
			}
		} else if !rec.Dead {
			rec.Dead = true
			changed = true
		}
		if changed {
			rec.UpdatedAt = time.Time{}
			_ = t.env.BrowserTabs.Put(rec)
		}
	}
	for _, tab := range cleaned {
		if seen[tab.ID] {
			continue
		}
		_ = t.env.BrowserTabs.Put(BrowserTabRecord{TabID: tab.ID, URL: tab.URL, Title: tab.Title})
	}
	records, listErr := t.env.BrowserTabs.List()
	if listErr != nil {
		return cleaned, nil, nil
	}
	return cleaned, records, nil
}

func (t *BrowserTool) listLiveTabs(ctx context.Context) ([]BrowserLiveTab, bool) {
	if t.env == nil || t.env.BrowserBridge == nil {
		return nil, false
	}
	live, err := t.env.BrowserBridge.ListTabs(ctx)
	if err != nil {
		return nil, false
	}
	return normalizeLiveTabs(live), true
}

func normalizeLiveTabs(live []BrowserLiveTab) []BrowserLiveTab {
	out := make([]BrowserLiveTab, 0, len(live))
	seen := make(map[string]bool, len(live))
	for _, tab := range live {
		tab.ID = strings.TrimSpace(tab.ID)
		tab.URL = strings.TrimSpace(tab.URL)
		tab.Title = strings.TrimSpace(tab.Title)
		if tab.ID == "" || seen[tab.ID] {
			continue
		}
		seen[tab.ID] = true
		out = append(out, tab)
	}
	return out
}

func browserLiveIDs(live []BrowserLiveTab) []string {
	ids := make([]string, 0, len(live))
	for _, tab := range live {
		ids = append(ids, tab.ID)
	}
	return ids
}

// browserKeepSet is the set of tabs finalize retains. An explicit keep array,
// including an empty one, is the whole decision. When keep is absent, a
// tab_id retains that single tab as handoff — models that only have the
// generic tab_id field still keep the page they named.
func browserKeepSet(argsJSON string) map[string]string {
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(argsJSON), &raw)
	_, explicit := raw["keep"]
	var args struct {
		Keep []struct {
			TabID  string `json:"tab_id"`
			Status string `json:"status"`
		} `json:"keep"`
		TabID string `json:"tab_id"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)
	keep := make(map[string]string, len(args.Keep)+1)
	for _, item := range args.Keep {
		id := strings.TrimSpace(item.TabID)
		if id == "" {
			continue
		}
		status := strings.TrimSpace(item.Status)
		if status == "" {
			status = "handoff"
		}
		keep[id] = status
	}
	if !explicit {
		if id := strings.TrimSpace(args.TabID); id != "" {
			keep[id] = "handoff"
		}
	}
	return keep
}

// cdpCall issues one semantic browser action over the CDP wire method and
// unwraps the (possibly spilled-to-disk) envelope.
func (t *BrowserTool) cdpCall(ctx context.Context, tabID, method string, params any) (json.RawMessage, error) {
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = data
	}
	resp, err := t.env.BrowserBridge.Call(ctx, browserCDPMethod, browserCDPRequest{
		Workdir: t.rootDir(),
		TabID:   tabID,
		Method:  method,
		Params:  raw,
	})
	if err != nil {
		return nil, err
	}
	return unwrapCDPResult(resp)
}

// cdpWithRebuild runs a CDP action and, on a tab_not_found signal, rebuilds the
// hidden view once from the tab's last known URL. replay controls whether the
// original action is re-issued after the rebuild: safe for reads/navigation,
// unsafe for side-effecting input (which returns a rebuilt notice instead).
func (t *BrowserTool) cdpWithRebuild(ctx context.Context, tabID, method string, params any, fallbackURL string, replay bool) (json.RawMessage, error) {
	raw, err := t.cdpCall(ctx, tabID, method, params)
	if err == nil {
		return raw, nil
	}
	if !isTabNotFound(err) {
		return nil, browserBridgeError(method, tabID, err)
	}
	rebuildURL := strings.TrimSpace(fallbackURL)
	if rebuildURL == "" {
		rebuildURL = t.tabURL(tabID)
	}
	if rebuildURL == "" {
		t.forgetTab(tabID)
		return nil, browserTabLostError(tabID, false)
	}
	if openErr := t.env.BrowserBridge.OpenTab(ctx, tabID, rebuildURL); openErr != nil {
		t.forgetTab(tabID)
		return nil, browserTabLostError(tabID, false)
	}
	t.rememberTab(tabID, rebuildURL, "", false)
	if !replay {
		return nil, browserTabLostError(tabID, true)
	}
	raw, err = t.cdpCall(ctx, tabID, method, params)
	if err != nil {
		t.forgetTab(tabID)
		return nil, browserBridgeError(method, tabID, err)
	}
	return raw, nil
}

func unwrapCDPResult(raw json.RawMessage) (json.RawMessage, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var env browserCDPEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		// Not the envelope shape; treat the payload as the semantic result.
		return raw, nil
	}
	if p := strings.TrimSpace(env.Path); p != "" {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read spilled browser result: %w", err)
		}
		return json.RawMessage(data), nil
	}
	if len(env.Result) > 0 {
		return env.Result, nil
	}
	// Envelope keys absent: the desktop inlined the semantic result directly.
	return raw, nil
}

// ── tab store + context helpers ─────────────────────────────────────

func (t *BrowserTool) rootDir() string {
	if t.env == nil {
		return ""
	}
	return t.env.RootDir
}

func (t *BrowserTool) resolveTabID(argsJSON string, bctx browserActionContext) string {
	if id := strings.TrimSpace(bctx.tabID); id != "" {
		return id
	}
	return browserTabIDFromArgs(argsJSON)
}

func (t *BrowserTool) previewPath(bctx browserActionContext) string {
	if t.env == nil {
		return ""
	}
	dir := strings.TrimSpace(t.env.SessionDir)
	activityID := strings.TrimSpace(bctx.activityID)
	if dir == "" || activityID == "" {
		return ""
	}
	full := filepath.Join(dir, "activities", activityID, "preview.png")
	_ = os.MkdirAll(filepath.Dir(full), 0o755)
	return full
}

func (t *BrowserTool) getTab(tabID string) (BrowserTabRecord, bool) {
	if t.env == nil || t.env.BrowserTabs == nil {
		return BrowserTabRecord{}, false
	}
	rec, ok, err := t.env.BrowserTabs.Get(tabID)
	if err != nil {
		return BrowserTabRecord{}, false
	}
	return rec, ok
}

func (t *BrowserTool) tabURL(tabID string) string {
	rec, ok := t.getTab(tabID)
	if !ok {
		return ""
	}
	return strings.TrimSpace(rec.URL)
}

func (t *BrowserTool) rememberTab(tabID, pageURL, title string, dead bool) {
	if t.env == nil || t.env.BrowserTabs == nil {
		return
	}
	rec, _ := t.getTab(tabID)
	rec.TabID = tabID
	if u := strings.TrimSpace(pageURL); u != "" {
		rec.URL = u
	}
	if ti := strings.TrimSpace(title); ti != "" {
		rec.Title = ti
	}
	rec.Dead = dead
	rec.UpdatedAt = time.Time{} // store restamps
	_ = t.env.BrowserTabs.Put(rec)
}

func (t *BrowserTool) forgetTab(tabID string) {
	if t.env == nil || t.env.BrowserTabs == nil {
		return
	}
	_ = t.env.BrowserTabs.Delete(tabID)
}

// ── free helpers ────────────────────────────────────────────────────

func newBrowserTabID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "tab-" + hex.EncodeToString(b[:])
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func isTabNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "tab_not_found")
}

func browserForwardParams(argsJSON string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil || m == nil {
		return map[string]any{}
	}
	delete(m, "action")
	delete(m, "tab_id")
	return m
}

func browserResult(action string, payload map[string]any) toolresult.Result {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["action"] = action
	structured, _ := json.Marshal(payload)
	return toolresult.Result{
		Content:           []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: string(structured)}},
		StructuredContent: structured,
	}
}

func browserBridgeError(action, tabID string, err error) error {
	return fmt.Errorf("browser %s failed: error_kind=browser_bridge tab_id=%q detail=%q model_next_action=%q",
		action, tabID, err.Error(), "observe the page or retry; if it persists the desktop host may be unavailable")
}

func browserTabLostError(tabID string, rebuilt bool) error {
	if rebuilt {
		return fmt.Errorf("browser tab was lost and rebuilt at its last url: error_kind=tab_rebuilt tab_id=%q model_next_action=%q",
			tabID, "observe before retrying the input to avoid a double submit")
	}
	return fmt.Errorf("browser tab is gone and could not be rebuilt: error_kind=tab_unrecoverable tab_id=%q model_next_action=%q",
		tabID, "call navigate to open a fresh tab")
}

func errBrowserMissingTab(action string) error {
	return fmt.Errorf("browser %s requires tab_id: error_kind=missing_tab_id model_next_action=%q", action, "pass the tab_id returned by navigate")
}

func redactBrowserContent(blocks []browserContentBlock) {
	for i := range blocks {
		blocks[i].Text = redactToolOutput(blocks[i].Text)
		redactContentPieces(blocks[i].Links)
		redactContentPieces(blocks[i].Items)
		redactContentPieces(blocks[i].Header)
		for r := range blocks[i].Rows {
			redactContentPieces(blocks[i].Rows[r])
		}
	}
}

func redactContentPieces(pieces []browserContentPiece) {
	for i := range pieces {
		pieces[i].Text = redactToolOutput(pieces[i].Text)
	}
}

func renderObserveText(tabID string, obs browserObservation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tab %s\n", tabID)
	if u := strings.TrimSpace(obs.URL); u != "" {
		fmt.Fprintf(&b, "url: %s\n", u)
	}
	if ti := strings.TrimSpace(obs.Title); ti != "" {
		fmt.Fprintf(&b, "title: %s\n", ti)
	}
	if obs.Content != nil {
		writeObserveContent(&b, obs)
	}
	fmt.Fprintf(&b, "%d interactive node(s):", len(obs.Nodes))
	for _, n := range obs.Nodes {
		b.WriteByte('\n')
		fmt.Fprintf(&b, "  [%d] %s", n.NodeID, strings.TrimSpace(n.Role))
		if name := strings.TrimSpace(n.Name); name != "" {
			b.WriteByte(' ')
			b.WriteString(name)
		}
		if v := strings.TrimSpace(n.Value); v != "" {
			b.WriteString(" = ")
			b.WriteString(v)
		}
	}
	return b.String()
}

func writeObserveContent(b *strings.Builder, obs browserObservation) {
	total := obs.ContentTotal
	if total == 0 {
		total = len(obs.Content)
	}
	if total == 0 {
		b.WriteString("content: none\n")
		return
	}
	end := obs.ContentOffset + len(obs.Content)
	switch {
	case obs.ContentNextOffset != nil:
		fmt.Fprintf(b, "content (blocks %d-%d of %d; content_offset=%d continues):\n", obs.ContentOffset+1, end, total, *obs.ContentNextOffset)
	case obs.ContentOffset > 0:
		fmt.Fprintf(b, "content (blocks %d-%d of %d):\n", obs.ContentOffset+1, end, total)
	default:
		b.WriteString("content:\n")
	}
	for _, block := range obs.Content {
		writeContentBlock(b, block)
	}
}

func writeContentBlock(b *strings.Builder, block browserContentBlock) {
	switch block.Kind {
	case "heading":
		level := block.Level
		if level < 1 {
			level = 1
		}
		if level > 6 {
			level = 6
		}
		fmt.Fprintf(b, "  %s %s\n", strings.Repeat("#", level), annotateNode(block.Text, block.NodeID))
	case "list":
		for i, item := range block.Items {
			marker := "-"
			if block.Ordered {
				marker = fmt.Sprintf("%d.", i+1)
			}
			fmt.Fprintf(b, "  %s %s\n", marker, annotateNode(item.Text, item.NodeID))
		}
	case "table":
		writeContentTable(b, block)
	default:
		fmt.Fprintf(b, "  %s\n", annotateNode(block.Text, block.NodeID))
		for _, link := range block.Links {
			if link.NodeID <= 0 {
				continue
			}
			label := strings.TrimSpace(link.Text)
			if label == "" {
				fmt.Fprintf(b, "  [node %d]\n", link.NodeID)
				continue
			}
			fmt.Fprintf(b, "  [node %d: %s]\n", link.NodeID, label)
		}
	}
}

func writeContentTable(b *strings.Builder, block browserContentBlock) {
	header := block.Header
	rows := block.Rows
	if len(header) == 0 && len(rows) > 0 {
		header = rows[0]
		rows = rows[1:]
	}
	writeContentRow(b, header)
	if len(header) > 0 {
		b.WriteString("  |")
		for range header {
			b.WriteString(" --- |")
		}
		b.WriteByte('\n')
	}
	for _, row := range rows {
		writeContentRow(b, row)
	}
}

func writeContentRow(b *strings.Builder, cells []browserContentPiece) {
	if len(cells) == 0 {
		return
	}
	b.WriteString("  |")
	for _, cell := range cells {
		b.WriteString(" ")
		b.WriteString(strings.ReplaceAll(annotateNode(cell.Text, cell.NodeID), "|", "/"))
		b.WriteString(" |")
	}
	b.WriteByte('\n')
}

func annotateNode(text string, nodeID int) string {
	text = strings.TrimSpace(text)
	if nodeID <= 0 {
		return text
	}
	if text == "" {
		return fmt.Sprintf("[node %d]", nodeID)
	}
	return fmt.Sprintf("%s [node %d]", text, nodeID)
}

func renderTabsText(live []string, records []BrowserTabRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d live tab(s), %d tracked", len(live), len(records))
	for _, rec := range records {
		state := "live"
		if rec.Dead {
			state = "dead"
		}
		b.WriteByte('\n')
		fmt.Fprintf(&b, "  %s (%s)", rec.TabID, state)
		if u := strings.TrimSpace(rec.URL); u != "" {
			fmt.Fprintf(&b, " %s", u)
		}
	}
	return b.String()
}

func renderFinalizeText(kept, closed []string) string {
	return fmt.Sprintf("finalize: kept %d tab(s), closed %d tab(s)", len(kept), len(closed))
}

// decodeActionField extracts only the action field so Classify never fails on
// otherwise-incomplete arguments (full validation happens at execution time).
func decodeActionField(argsJSON string) string {
	var probe struct {
		Action string `json:"action"`
	}
	if err := decodeArgs(argsJSON, &probe); err != nil {
		return ""
	}
	return strings.TrimSpace(probe.Action)
}
