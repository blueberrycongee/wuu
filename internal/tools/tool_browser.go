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
			"Screenshot saves a UI preview and returns its path and dimensions, not image content to the model; use observe to read page text. " +
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
					"description": "Target tab. Omit on navigate to open a new tab; the tool mints and returns a tab_id.",
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
	URL            string        `json:"url"`
	Title          string        `json:"title"`
	Nodes          []browserNode `json:"nodes"`
	ScreenshotPath string        `json:"screenshot_path"`
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
	raw, err := t.cdpWithRebuild(ctx, tabID, "observe", params, t.tabURL(tabID), true)
	if err != nil {
		return toolresult.Result{}, err
	}
	var obs browserObservation
	_ = json.Unmarshal(raw, &obs)
	realURL := obs.URL
	// Page-derived text passes through credential redaction before the model ever
	// sees it: node names/values, the title, and the URL (which can carry OAuth
	// codes or session/reset tokens in userinfo or query params).
	for i := range obs.Nodes {
		obs.Nodes[i].Name = redactToolOutput(obs.Nodes[i].Name)
		obs.Nodes[i].Value = redactToolOutput(obs.Nodes[i].Value)
	}
	obs.Title = redactToolOutput(obs.Title)
	obs.URL = redactURLForModel(obs.URL)
	structured, _ := json.Marshal(map[string]any{
		"action": "observe", "tab_id": tabID, "url": obs.URL, "title": obs.Title, "nodes": obs.Nodes,
	})
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
	live, err := t.env.BrowserBridge.ListTabs(ctx)
	if err != nil {
		return toolresult.Result{}, browserBridgeError("tabs", "", err)
	}
	liveSet := make(map[string]bool, len(live))
	for _, id := range live {
		liveSet[strings.TrimSpace(id)] = true
	}
	// Reconcile the durable store against the live host set: entries the host no
	// longer knows have lost their view (core restart) and are marked dead so the
	// next use rebuilds by URL instead of addressing a gone tab.
	var records []BrowserTabRecord
	if t.env.BrowserTabs != nil {
		stored, listErr := t.env.BrowserTabs.List()
		if listErr == nil {
			for _, rec := range stored {
				if !liveSet[rec.TabID] && !rec.Dead {
					rec.Dead = true
					rec.UpdatedAt = time.Time{}
					_ = t.env.BrowserTabs.Put(rec)
				}
				records = append(records, rec)
			}
		}
	}
	structured, _ := json.Marshal(map[string]any{"action": "tabs", "live_tab_ids": live, "tabs": records})
	return toolresult.Result{
		Content:           []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: renderTabsText(live, records)}},
		StructuredContent: structured,
	}, nil
}

func (t *BrowserTool) doFinalize(ctx context.Context, argsJSON string) (toolresult.Result, error) {
	var args struct {
		Keep []struct {
			TabID  string `json:"tab_id"`
			Status string `json:"status"`
		} `json:"keep"`
	}
	_ = decodeArgs(argsJSON, &args)
	keep := make(map[string]string, len(args.Keep))
	for _, k := range args.Keep {
		if id := strings.TrimSpace(k.TabID); id != "" {
			keep[id] = strings.TrimSpace(k.Status)
		}
	}
	var closed, kept []string
	if t.env.BrowserTabs != nil {
		stored, _ := t.env.BrowserTabs.List()
		for _, rec := range stored {
			if status, ok := keep[rec.TabID]; ok {
				rec.Status = status
				rec.UpdatedAt = time.Time{}
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
		// Kept tabs the model minted this turn may not be in the store yet.
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

func renderObserveText(tabID string, obs browserObservation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tab %s\n", tabID)
	if u := strings.TrimSpace(obs.URL); u != "" {
		fmt.Fprintf(&b, "url: %s\n", u)
	}
	if ti := strings.TrimSpace(obs.Title); ti != "" {
		fmt.Fprintf(&b, "title: %s\n", ti)
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
