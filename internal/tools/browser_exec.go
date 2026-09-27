package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// embeddedBrowserPluginID is the fixed activity plugin identity for the built-in
// browser tool. It is intentionally shared by every tab in a thread: Acquire
// keys on (thread, plugin, kind), so the thread has one browser activity whose
// Target tracks the currently addressed tab (the CUA app-switch model).
const embeddedBrowserPluginID = "embedded-browser"

// browserActionContextKey carries the resolved per-action lease facts (activity
// id, tab id, preview sink) from the toolkit orchestration down into
// BrowserTool.ExecuteResult without changing the RichTool interface. The tool
// needs the activity id to name the screenshot path and the resolved tab id
// because a navigate that opens a new tab mints the id at the toolkit layer.
type browserActionContextKey struct{}

type browserActionContext struct {
	activityID string
	tabID      string
	// newTab is set when the toolkit minted a fresh tab id for a navigate that
	// omitted tab_id, so the tool creates the hidden view instead of navigating
	// an existing one.
	newTab bool
	// setPreview publishes the file:// screenshot URI for the activity's live
	// preview. Captured by the orchestration so the value survives result
	// projection/budgeting.
	setPreview func(uri string)
}

func withBrowserActionContext(ctx context.Context, bctx browserActionContext) context.Context {
	return context.WithValue(ctx, browserActionContextKey{}, bctx)
}

func browserActionContextFrom(ctx context.Context) (browserActionContext, bool) {
	bctx, ok := ctx.Value(browserActionContextKey{}).(browserActionContext)
	return bctx, ok
}

// executeBrowserToolResult is the built-in-tool analogue of
// executeActivityBoundToolResult: it binds each tab-addressed browser action to
// a KindBrowser activity lease via the shared runActivityBoundAction spine, and
// routes the thread-global actions (tabs list, finalize) around it.
func (t *Toolkit) executeBrowserToolResult(ctx context.Context, call providers.ToolCall, tool Tool) (toolresult.Result, error) {
	// Gate the top-level action on the workspace boundary before acquiring any
	// lease: a read-only session must block navigate/click/type/sequence/finalize
	// (mutating) while still allowing observe/screenshot/tabs/wait_for, and a
	// denial must not leave a spurious errored activity behind.
	info := buildToolInfoForArgs(tool, t.toolExposure(call.Name), call.Arguments)
	if err := t.checkPermission(ctx, info, call); err != nil {
		return toolresult.Result{}, err
	}
	ctx = markPermissionChecked(ctx)

	action := strings.TrimSpace(decodeActionField(call.Arguments))
	switch action {
	case "tabs":
		// Listing is thread-global reconciliation; it creates no lease.
		return t.executeKnownToolResult(ctx, call, tool)
	case "finalize":
		return t.executeBrowserFinalize(ctx, call, tool)
	}

	threadID := ""
	workdir := ""
	if t.env != nil {
		threadID = strings.TrimSpace(t.env.SessionID)
		workdir = strings.TrimSpace(t.env.RootDir)
	}
	if threadID == "" {
		return toolresult.Result{}, errors.New("browser tool requires a thread context: error_kind=missing_thread_context model_next_action=\"run the browser tool inside a conversation thread\"")
	}

	tabID := strings.TrimSpace(browserTabIDFromArgs(call.Arguments))
	minted := false
	if action == "navigate" && tabID == "" {
		// A navigate without tab_id opens a fresh tab; mint the id up front so the
		// activity target is stable and the tool echoes it back to the model.
		tabID = newBrowserTabID()
		minted = true
	}
	if tabID == "" {
		return toolresult.Result{}, fmt.Errorf("browser action %q requires tab_id: error_kind=missing_tab_id model_next_action=%q", action, "pass the tab_id returned by navigate, or call navigate to open a tab")
	}

	spec := activitySpec{Kind: activity.KindBrowser, ThreadID: threadID, Workdir: workdir, PluginID: embeddedBrowserPluginID, Target: tabID}
	var previewURI string
	setPreview := func(uri string) { previewURI = strings.TrimSpace(uri) }
	hooks := activityHooks{
		run: func(ctx context.Context, session activity.Session, lease activity.Lease) (toolresult.Result, string, error) {
			bctx := browserActionContext{activityID: session.ID, tabID: tabID, newTab: minted, setPreview: setPreview}
			stepCtx := withBrowserActionContext(ctx, bctx)
			if action == "sequence" {
				steps, perr := parseBrowserSequenceSteps(call.Arguments)
				if perr != nil {
					return toolresult.Result{}, "failed", perr
				}
				return t.runRiskSequence(stepCtx, tool, session.ThreadID, session.ID, lease.Token, "browser sequence", steps, browserSequenceHooks())
			}
			result, err := t.executeKnownToolResult(stepCtx, call, tool)
			return result, "", err
		},
		controlState: func(_ toolresult.Result) activity.State {
			return browserControlState(action, call.Arguments)
		},
		preview: func(_ activity.Session, _ toolresult.Result) (string, error) {
			return previewURI, nil
		},
	}
	result, err := t.runActivityBoundAction(ctx, spec, hooks)
	if err == nil && browserActionIsDeliveryOnly(action) {
		// Delivering input is not proof the page accepted it; keep the receipt
		// neutral and non-empty (the SKILL teaches "delivered != succeeded").
		result.Content = []toolresult.ContentPart{{
			Type: toolresult.ContentTypeText,
			Text: "Input delivered. Call observe when the outcome matters.",
		}}
		result.StructuredContent = nil
		result.Meta = nil
	}
	return result, err
}

// executeBrowserFinalize closes or preserves tabs through the ordinary pipeline
// without ending the thread's browser activity.
func (t *Toolkit) executeBrowserFinalize(ctx context.Context, call providers.ToolCall, tool Tool) (toolresult.Result, error) {
	// finalize closes the tabs the model did not keep (handled by the tool's own
	// Execute) but deliberately does NOT stop the browser activity, even with an
	// empty keep list. A hard registry.Stop tombstones the (thread, plugin, kind)
	// session for the process lifetime — Acquire then returns ErrStopped forever
	// with no in-thread recovery, so a later turn's navigate would break browsing
	// for good. The activity's lifetime is the thread's: Server.Close stops it on
	// teardown, and activity/stop remains an explicit mid-thread hard stop.
	return t.executeKnownToolResult(ctx, call, tool)
}

// browserControlState maps a browser action onto an activity control state.
// Tabs run hidden in the background by default; only an explicit set_visibility
// promotion places the activity in the foreground, mirroring how CUA derives its
// state per call from the mechanism actually used.
func browserControlState(action, arguments string) activity.State {
	if action == "set_visibility" && browserRequestedVisibility(arguments) {
		return activity.StateForegroundControlled
	}
	return activity.StateBackgroundControlled
}

// browserActionIsDeliveryOnly reports the input actions whose success cannot be
// confirmed from the call alone (the page must be re-observed).
func browserActionIsDeliveryOnly(action string) bool {
	switch action {
	case "click", "type", "scroll", "key":
		return true
	default:
		return false
	}
}

// browserSequenceHooks builds the risk-sequence hooks for a browser batch. Every
// step targets the envelope tab (carried in the browser action context), so
// there is no per-step argument inheritance or live-preview interaction to
// publish; only the aggregate result shape differs from CUA.
func browserSequenceHooks() riskSequenceHooks {
	return riskSequenceHooks{
		build: func(status string, completed []map[string]any, nextStep int, lastImage *toolresult.ContentPart) toolresult.Result {
			payload := map[string]any{"action": "sequence", "status": status, "completed_steps": completed, "next_step": nextStep}
			structured, _ := json.Marshal(payload)
			content := []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: fmt.Sprintf("browser sequence %s after %d step(s).", status, len(completed))}}
			if lastImage != nil {
				content = append(content, *lastImage)
			}
			return toolresult.Result{Content: content, StructuredContent: structured, IsError: status == "failed" || status == "partial"}
		},
	}
}

func browserTabIDFromArgs(arguments string) string {
	var probe struct {
		TabID string `json:"tab_id"`
	}
	_ = json.Unmarshal([]byte(arguments), &probe)
	return strings.TrimSpace(probe.TabID)
}

func browserRequestedVisibility(arguments string) bool {
	var probe struct {
		Visible bool `json:"visible"`
	}
	_ = json.Unmarshal([]byte(arguments), &probe)
	return probe.Visible
}

func parseBrowserSequenceSteps(arguments string) ([]map[string]any, error) {
	var probe struct {
		Steps []map[string]any `json:"steps"`
	}
	if err := json.Unmarshal([]byte(arguments), &probe); err != nil {
		return nil, fmt.Errorf("browser sequence has invalid steps: %w", err)
	}
	return probe.Steps, nil
}

func browserFinalizeKeepEmpty(arguments string) bool {
	var probe struct {
		Keep []struct {
			TabID string `json:"tab_id"`
		} `json:"keep"`
	}
	_ = json.Unmarshal([]byte(arguments), &probe)
	for _, k := range probe.Keep {
		if strings.TrimSpace(k.TabID) != "" {
			return false
		}
	}
	return true
}
