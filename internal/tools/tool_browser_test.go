package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// fakeBrowserBridge is an injectable BrowserBridge that records calls and lets a
// test drive per-method behavior (including error injection). Semantic CDP
// results are wrapped in the BrowserCDPResult envelope the tool expects.
type fakeBrowserBridge struct {
	mu sync.Mutex

	cdpCalls    []fakeCDPCall
	openCalls   []fakeOpenCall
	closeCalls  []string
	setVisCalls []fakeSetVisCall
	shotCalls   int
	listCalls   int

	onCDP   func(method string, params map[string]any) (any, error)
	onOpen  func(tabID, initialURL string) error
	onShot  func(tabID, destPath, format string) (BrowserScreenshotResult, error)
	onList  func() ([]BrowserLiveTab, error)
	onClose func(tabID string) error
}

type fakeCDPCall struct {
	Method string
	TabID  string
	Params map[string]any
}

type fakeOpenCall struct {
	TabID      string
	InitialURL string
}

type fakeSetVisCall struct {
	TabID   string
	Visible bool
}

func (b *fakeBrowserBridge) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	req, _ := params.(browserCDPRequest)
	var decoded map[string]any
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &decoded)
	}
	b.mu.Lock()
	b.cdpCalls = append(b.cdpCalls, fakeCDPCall{Method: req.Method, TabID: req.TabID, Params: decoded})
	b.mu.Unlock()
	if method != browserCDPMethod {
		return nil, errors.New("unexpected bridge method " + method)
	}
	var semantic any = map[string]any{"ok": true}
	if b.onCDP != nil {
		result, err := b.onCDP(req.Method, decoded)
		if err != nil {
			return nil, err
		}
		semantic = result
	}
	inner, _ := json.Marshal(semantic)
	env, _ := json.Marshal(map[string]json.RawMessage{"result": inner})
	return env, nil
}

func (b *fakeBrowserBridge) Screenshot(_ context.Context, tabID, destPath, format string) (BrowserScreenshotResult, error) {
	b.mu.Lock()
	b.shotCalls++
	b.mu.Unlock()
	if b.onShot != nil {
		return b.onShot(tabID, destPath, format)
	}
	_ = os.WriteFile(destPath, []byte("png"), 0o600)
	return BrowserScreenshotResult{Width: 800, Height: 600, Path: destPath}, nil
}

func (b *fakeBrowserBridge) OpenTab(_ context.Context, tabID, initialURL string) error {
	b.mu.Lock()
	b.openCalls = append(b.openCalls, fakeOpenCall{TabID: tabID, InitialURL: initialURL})
	b.mu.Unlock()
	if b.onOpen != nil {
		return b.onOpen(tabID, initialURL)
	}
	return nil
}

func (b *fakeBrowserBridge) CloseTab(_ context.Context, tabID string) error {
	b.mu.Lock()
	b.closeCalls = append(b.closeCalls, tabID)
	b.mu.Unlock()
	if b.onClose != nil {
		return b.onClose(tabID)
	}
	return nil
}

func (b *fakeBrowserBridge) SetVisibility(_ context.Context, tabID string, visible bool) error {
	b.mu.Lock()
	b.setVisCalls = append(b.setVisCalls, fakeSetVisCall{TabID: tabID, Visible: visible})
	b.mu.Unlock()
	return nil
}

func (b *fakeBrowserBridge) ListTabs(_ context.Context) ([]BrowserLiveTab, error) {
	b.mu.Lock()
	b.listCalls++
	b.mu.Unlock()
	if b.onList != nil {
		return b.onList()
	}
	return nil, nil
}

func (b *fakeBrowserBridge) cdpCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.cdpCalls)
}

// newBrowserKit assembles a Toolkit wired to a browser tool + fake bridge + real
// activity registry + durable tab store, mirroring the CUA test idiom.
func newBrowserKit(t *testing.T, bridge BrowserBridge) (*Toolkit, *activity.Registry, *BrowserTabFileStore) {
	t.Helper()
	registry := activity.NewRegistry()
	store := NewBrowserTabStore(filepath.Join(t.TempDir(), "browser_tabs.json"))
	env := &Env{
		RootDir:       "/repo",
		SessionID:     "thread-1",
		SessionDir:    t.TempDir(),
		BrowserBridge: bridge,
		BrowserTabs:   store,
	}
	kit := &Toolkit{
		env:              env,
		registry:         NewRegistry(NewBrowserTool(env)),
		boundary:         StandardBoundary(),
		activityRegistry: registry,
	}
	return kit, registry, store
}

func browserCall(action string, fields map[string]any) providers.ToolCall {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["action"] = action
	args, _ := json.Marshal(fields)
	return providers.ToolCall{ID: action + "-1", Name: browserToolName, Arguments: string(args)}
}

func TestBrowserDefinitionPublishesInputFields(t *testing.T) {
	tool := NewBrowserTool(&Env{})
	props, _ := tool.Definition().InputSchema["properties"].(map[string]any)
	for _, name := range []string{"node_id", "text", "keys", "dx", "dy", "steps", "keep", "content_offset"} {
		if _, ok := props[name]; !ok {
			t.Fatalf("browser schema omits %s", name)
		}
	}
}

func TestBrowserToolIsDirectOnDefaultSurface(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5-codex", true)
	if _, ok := kit.ActiveSurface().Tools[browserToolName]; !ok {
		t.Fatalf("wuu_browser must be a direct tool, got tools=%v deferred=%v", kit.ActiveSurface().Tools, kit.ActiveSurface().DeferredTools)
	}
	if !containsProfileDef(kit.Definitions(), browserToolName) {
		t.Fatal("wuu_browser must appear in Definitions without tool_search")
	}
}

func TestSetBrowserEnabledHidesDirectTool(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5-codex", true)
	kit.SetBrowserEnabled(false)
	if _, ok := kit.ActiveSurface().Tools[browserToolName]; ok {
		t.Fatal("disabled wuu_browser leaked into the compiled surface")
	}
	if containsProfileDef(kit.Definitions(), browserToolName) {
		t.Fatal("disabled wuu_browser leaked into Definitions")
	}
}

func TestBrowserObserveBuildsActivityRefAndPreview(t *testing.T) {
	bridge := &fakeBrowserBridge{
		onCDP: func(method string, params map[string]any) (any, error) {
			if method != "observe" {
				t.Fatalf("unexpected method %q", method)
			}
			dest, _ := params["dest_path"].(string)
			if dest != "" {
				_ = os.WriteFile(dest, []byte("png"), 0o600)
			}
			return map[string]any{
				"url":   "https://example.com",
				"title": "Example",
				"nodes": []map[string]any{
					{"node_id": 1, "role": "button", "name": "Sign in"},
				},
				"screenshot_path": dest,
			}, nil
		},
	}
	kit, registry, _ := newBrowserKit(t, bridge)
	tool := kit.registry.Lookup(browserToolName)

	result, err := kit.executeBrowserToolResult(context.Background(), browserCall("observe", map[string]any{"tab_id": "tab-1"}), tool)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if result.Activity == nil || result.Activity.Kind != string(activity.KindBrowser) || result.Activity.ThreadID != "thread-1" {
		t.Fatalf("activity ref = %+v", result.Activity)
	}
	if result.Activity.State != string(activity.StateBackgroundControlled) {
		t.Fatalf("state = %q", result.Activity.State)
	}
	if !strings.Contains(result.TextProjection(), "Sign in") {
		t.Fatalf("observe text lost node name: %q", result.TextProjection())
	}
	sessions := registry.List("thread-1")
	if len(sessions) != 1 || sessions[0].Target != "tab-1" || sessions[0].Preview == "" {
		t.Fatalf("session = %+v", sessions)
	}
	expectedPreview := fileURI(filepath.Join(kit.env.SessionDir, "activities", sessions[0].ID, "preview.png"))
	if result.Activity.PreviewURI != expectedPreview || sessions[0].Preview != expectedPreview {
		t.Fatalf("preview = %q want %q", result.Activity.PreviewURI, expectedPreview)
	}
	if data, readErr := os.ReadFile(strings.TrimPrefix(expectedPreview, "file://")); readErr != nil || string(data) != "png" {
		t.Fatalf("preview artifact = %q, %v", data, readErr)
	}
}

func TestBrowserActionDiscardedAfterTakeover(t *testing.T) {
	var registry *activity.Registry
	bridge := &fakeBrowserBridge{
		onCDP: func(method string, _ map[string]any) (any, error) {
			// A user grabs control while the CDP round-trip is in flight.
			if sessions := registry.List("thread-1"); len(sessions) == 1 {
				_, _ = registry.Takeover("thread-1", sessions[0].ID)
			}
			return map[string]any{"url": "https://example.com", "title": "Example", "nodes": []any{}}, nil
		},
	}
	kit, reg, _ := newBrowserKit(t, bridge)
	registry = reg
	tool := kit.registry.Lookup(browserToolName)

	_, err := kit.executeBrowserToolResult(context.Background(), browserCall("observe", map[string]any{"tab_id": "tab-1"}), tool)
	if !errors.Is(err, activity.ErrControlRevoked) {
		t.Fatalf("observe after takeover = %v, want ErrControlRevoked", err)
	}
	sessions := registry.List("thread-1")
	if len(sessions) != 1 || sessions[0].Controller != activity.ControllerUser {
		t.Fatalf("session after takeover = %+v", sessions)
	}
}

func TestBrowserSequencePausesOnUnconfirmedRisk(t *testing.T) {
	bridge := &fakeBrowserBridge{}
	kit, _, _ := newBrowserKit(t, bridge)
	tool := kit.registry.Lookup(browserToolName)

	call := browserCall("sequence", map[string]any{
		"tab_id": "tab-1",
		"steps": []map[string]any{
			{"action": "click", "node_id": 1, "risk": "safe"},
			{"action": "click", "node_id": 2, "risk": "external_side_effect"},
		},
	})
	result, err := kit.executeBrowserToolResult(context.Background(), call, tool)
	if err != nil {
		t.Fatalf("sequence: %v", err)
	}
	if got := bridge.cdpCount(); got != 1 {
		t.Fatalf("executed cdp steps = %d, want 1 before the pause", got)
	}
	if result.Activity == nil || result.Activity.State != string(activity.StateWaitingConfirmation) {
		t.Fatalf("activity = %+v, want waiting_confirmation", result.Activity)
	}
	var structured struct {
		Status   string `json:"status"`
		NextStep int    `json:"next_step"`
	}
	if err := json.Unmarshal(result.StructuredContent, &structured); err != nil {
		t.Fatal(err)
	}
	if structured.Status != "policy_paused" || structured.NextStep != 1 {
		t.Fatalf("sequence result = %+v", structured)
	}
}

func TestBrowserStoppedActivityReturnedVerbatim(t *testing.T) {
	bridge := &fakeBrowserBridge{}
	kit, registry, _ := newBrowserKit(t, bridge)
	tool := kit.registry.Lookup(browserToolName)

	// Pre-create and stop the thread's browser activity (a user Stop).
	session, _, err := registry.Start(activity.StartOptions{
		Kind: activity.KindBrowser, ThreadID: "thread-1", Workdir: "/repo", PluginID: embeddedBrowserPluginID, Target: "tab-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Stop("thread-1", session.ID); err != nil {
		t.Fatal(err)
	}

	_, err = kit.executeBrowserToolResult(context.Background(), browserCall("observe", map[string]any{"tab_id": "tab-1"}), tool)
	if !errors.Is(err, activity.ErrStopped) {
		t.Fatalf("observe after stop = %v, want ErrStopped (no silent rebuild)", err)
	}
	if bridge.cdpCount() != 0 {
		t.Fatalf("stopped activity reached the bridge: %d calls", bridge.cdpCount())
	}
}

func TestBrowserTabNotFoundRebuildsFromStoreURL(t *testing.T) {
	var failFirst = true
	bridge := &fakeBrowserBridge{
		onCDP: func(method string, _ map[string]any) (any, error) {
			if method == "observe" && failFirst {
				failFirst = false
				return nil, errors.New("cdp error: tab_not_found")
			}
			return map[string]any{"url": "https://example.com", "title": "Example", "nodes": []any{}}, nil
		},
	}
	kit, _, store := newBrowserKit(t, bridge)
	tool := kit.registry.Lookup(browserToolName)
	// The store remembers the tab's last URL from a prior turn.
	if err := store.Put(BrowserTabRecord{TabID: "tab-1", URL: "https://example.com"}); err != nil {
		t.Fatal(err)
	}

	result, err := kit.executeBrowserToolResult(context.Background(), browserCall("observe", map[string]any{"tab_id": "tab-1"}), tool)
	if err != nil {
		t.Fatalf("observe with rebuild: %v", err)
	}
	if len(bridge.openCalls) != 1 || bridge.openCalls[0].TabID != "tab-1" || bridge.openCalls[0].InitialURL != "https://example.com" {
		t.Fatalf("rebuild open calls = %+v", bridge.openCalls)
	}
	if bridge.cdpCount() != 2 {
		t.Fatalf("expected observe retry after rebuild, cdp calls = %d", bridge.cdpCount())
	}
	if result.Activity == nil {
		t.Fatalf("result missing activity ref: %+v", result)
	}
}

func TestBrowserReadOnlyBoundaryBlocksMutatingAction(t *testing.T) {
	bridge := &fakeBrowserBridge{}
	kit, registry, _ := newBrowserKit(t, bridge)
	kit.boundary = ReadOnlyBoundary()
	tool := kit.registry.Lookup(browserToolName)

	_, err := kit.executeBrowserToolResult(context.Background(), browserCall("navigate", map[string]any{"url": "https://example.com"}), tool)
	if err == nil || !strings.Contains(err.Error(), "boundary_denied") {
		t.Fatalf("navigate under read-only boundary = %v, want boundary denial", err)
	}
	if bridge.cdpCount() != 0 || len(bridge.openCalls) != 0 {
		t.Fatalf("blocked navigate still reached the bridge: cdp=%d open=%d", bridge.cdpCount(), len(bridge.openCalls))
	}
	if len(registry.List("thread-1")) != 0 {
		t.Fatalf("blocked navigate created an activity: %+v", registry.List("thread-1"))
	}

	// A read-only action stays allowed under the same boundary.
	if _, err := kit.executeBrowserToolResult(context.Background(), browserCall("observe", map[string]any{"tab_id": "tab-1"}), tool); err != nil {
		t.Fatalf("observe under read-only boundary: %v", err)
	}
}

func TestBrowserUnavailableBridgeReturnsClearError(t *testing.T) {
	env := &Env{RootDir: "/repo", SessionID: "thread-1", SessionDir: t.TempDir()}
	tool := NewBrowserTool(env)
	_, err := tool.ExecuteResult(context.Background(), `{"action":"observe","tab_id":"tab-1"}`)
	if err == nil || !strings.Contains(err.Error(), "browser_unavailable") {
		t.Fatalf("nil bridge error = %v, want browser_unavailable", err)
	}
}

func TestRedactURLForModel(t *testing.T) {
	// Ordinary URLs and query params survive untouched.
	if got := redactURLForModel("https://example.com/path?page=2"); got != "https://example.com/path?page=2" {
		t.Errorf("ordinary URL mutated: %q", got)
	}
	if redactURLForModel("") != "" {
		t.Error("empty URL should stay empty")
	}
	// Userinfo credentials are stripped.
	if got := redactURLForModel("https://user:secretpw@example.com/x"); got != "https://redacted@example.com/x" {
		t.Errorf("userinfo not redacted: %q", got)
	}
	// Token/code/secret-named query values are redacted; ordinary ones survive.
	got := redactURLForModel("https://example.com/cb?code=AUTH123&access_token=T0k3n&page=2")
	if strings.Contains(got, "AUTH123") || strings.Contains(got, "T0k3n") {
		t.Errorf("token/code query not redacted: %q", got)
	}
	if !strings.Contains(got, "page=2") {
		t.Errorf("ordinary query dropped: %q", got)
	}
}

func TestBrowserObserveRedactsURLButStoresReal(t *testing.T) {
	bridge := &fakeBrowserBridge{
		onCDP: func(_ string, _ map[string]any) (any, error) {
			return map[string]any{"url": "https://example.com/cb?code=SECRET123", "title": "T", "nodes": []any{}}, nil
		},
	}
	kit, _, store := newBrowserKit(t, bridge)
	tool := kit.registry.Lookup(browserToolName)
	result, err := kit.executeBrowserToolResult(context.Background(), browserCall("observe", map[string]any{"tab_id": "tab-1"}), tool)
	if err != nil {
		t.Fatal(err)
	}
	// The model-facing URL is redacted...
	if strings.Contains(string(result.StructuredContent), "SECRET123") {
		t.Fatalf("observe leaked a URL token to the model: %s", result.StructuredContent)
	}
	// ...but the durable store keeps the real URL so rebuild-by-URL can navigate.
	rec, ok, _ := store.Get("tab-1")
	if !ok || !strings.Contains(rec.URL, "SECRET123") {
		t.Fatalf("store must keep the real URL for rebuild, got %+v", rec)
	}
}

func TestBrowserFinalizeEmptyKeepDoesNotTombstone(t *testing.T) {
	bridge := &fakeBrowserBridge{
		onCDP: func(_ string, _ map[string]any) (any, error) {
			return map[string]any{"url": "https://example.com", "title": "Example", "nodes": []any{}}, nil
		},
	}
	kit, registry, _ := newBrowserKit(t, bridge)
	tool := kit.registry.Lookup(browserToolName)

	if _, err := kit.executeBrowserToolResult(context.Background(), browserCall("observe", map[string]any{"tab_id": "tab-1"}), tool); err != nil {
		t.Fatalf("initial observe: %v", err)
	}
	// finalize with an empty keep list must NOT hard-tombstone the session.
	if _, err := kit.executeBrowserToolResult(context.Background(), browserCall("finalize", map[string]any{"keep": []any{}}), tool); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	for _, s := range registry.List("thread-1") {
		if s.Kind == activity.KindBrowser && s.State == activity.StateStopped {
			t.Fatalf("finalize-empty stopped the browser activity, breaking later turns: %+v", s)
		}
	}
	// A later turn can browse again without ErrStopped.
	if _, err := kit.executeBrowserToolResult(context.Background(), browserCall("observe", map[string]any{"tab_id": "tab-2"}), tool); err != nil {
		t.Fatalf("observe after finalize-empty = %v, want success (session reusable)", err)
	}
}

func TestBrowserObserveReadsContentAndContinues(t *testing.T) {
	var gotOffset any
	bridge := &fakeBrowserBridge{
		onCDP: func(method string, params map[string]any) (any, error) {
			if method != "observe" {
				t.Fatalf("unexpected method %q", method)
			}
			gotOffset = params["content_offset"]
			next := 2
			return map[string]any{
				"url":   "https://mimo.xiaomi.com/mimo-v2-6",
				"title": "MiMo",
				"nodes": []any{map[string]any{"node_id": 3, "role": "link", "name": "collection"}},
				"content": []any{
					map[string]any{"kind": "heading", "level": 1, "text": "MiMo V2.6"},
					map[string]any{
						"kind":  "paragraph",
						"text":  "token: supersecretvalue",
						"links": []any{map[string]any{"text": "collection", "node_id": 3}},
					},
					map[string]any{
						"kind":   "table",
						"header": []any{map[string]any{"text": "Benchmark"}, map[string]any{"text": "Score"}},
						"rows": []any{[]any{
							map[string]any{"text": "MMLU"},
							map[string]any{"text": "85.2", "node_id": 3},
						}},
					},
				},
				"content_offset":      0,
				"content_total":       4,
				"content_next_offset": next,
			}, nil
		},
	}
	kit, _, _ := newBrowserKit(t, bridge)
	tool := kit.registry.Lookup(browserToolName)
	result, err := kit.executeBrowserToolResult(context.Background(), browserCall("observe", map[string]any{
		"tab_id":         "tab-1",
		"content_offset": 2,
	}), tool)
	if err != nil {
		t.Fatal(err)
	}
	if gotOffset != float64(2) {
		t.Fatalf("content_offset forwarded as %#v", gotOffset)
	}
	text := result.TextProjection()
	for _, want := range []string{"# MiMo V2.6", "content_offset=2 continues", "MMLU", "85.2 [node 3]", "[node 3: collection]", "[REDACTED]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("observe text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "supersecretvalue") {
		t.Fatalf("observe leaked credential text:\n%s", text)
	}
}

func TestBrowserTabsRecordsUntrackedPopup(t *testing.T) {
	bridge := &fakeBrowserBridge{
		onList: func() ([]BrowserLiveTab, error) {
			return []BrowserLiveTab{
				{ID: "tab-1", URL: "https://news.ycombinator.com/", Title: "HN"},
				{ID: "popup-tab-1-0", URL: "https://huggingface.co/collections/XiaomiMiMo/mimo-v26?code=SECRET123", Title: "MiMo"},
			}, nil
		},
	}
	kit, _, store := newBrowserKit(t, bridge)
	if err := store.Put(BrowserTabRecord{TabID: "tab-1", URL: "https://news.ycombinator.com/"}); err != nil {
		t.Fatal(err)
	}
	tool := kit.registry.Lookup(browserToolName)
	result, err := kit.executeBrowserToolResult(context.Background(), browserCall("tabs", nil), tool)
	if err != nil {
		t.Fatal(err)
	}
	popup, ok, err := store.Get("popup-tab-1-0")
	if err != nil || !ok || popup.Dead || !strings.Contains(popup.URL, "SECRET123") {
		t.Fatalf("popup record = %+v ok=%v err=%v", popup, ok, err)
	}
	text := result.TextProjection()
	if !strings.Contains(text, "popup-tab-1-0") || !strings.Contains(text, "huggingface.co/collections/XiaomiMiMo/mimo-v26") {
		t.Fatalf("tabs text hid the popup:\n%s", text)
	}
	if strings.Contains(text, "SECRET123") || strings.Contains(string(result.StructuredContent), "SECRET123") {
		t.Fatalf("tabs leaked the popup credential:\n%s", text)
	}
}

func TestBrowserFinalizeClosesUntrackedPopup(t *testing.T) {
	bridge := &fakeBrowserBridge{
		onList: func() ([]BrowserLiveTab, error) {
			return []BrowserLiveTab{
				{ID: "tab-1", URL: "https://news.ycombinator.com/", Title: "HN"},
				{ID: "popup-tab-1-0", URL: "https://huggingface.co/collections/XiaomiMiMo/mimo-v26", Title: "MiMo"},
			}, nil
		},
	}
	kit, _, store := newBrowserKit(t, bridge)
	if err := store.Put(BrowserTabRecord{TabID: "tab-1", URL: "https://news.ycombinator.com/"}); err != nil {
		t.Fatal(err)
	}
	tool := kit.registry.Lookup(browserToolName)
	if _, err := kit.executeBrowserToolResult(context.Background(), browserCall("finalize", map[string]any{
		"keep": []any{map[string]any{"tab_id": "tab-1", "status": "handoff"}},
	}), tool); err != nil {
		t.Fatal(err)
	}
	kept, ok, err := store.Get("tab-1")
	if err != nil || !ok || kept.Status != "handoff" {
		t.Fatalf("kept tab = %+v ok=%v err=%v", kept, ok, err)
	}
	if _, ok, _ := store.Get("popup-tab-1-0"); ok {
		t.Fatal("unkept popup stayed in the registry")
	}
	bridge.mu.Lock()
	closed := append([]string(nil), bridge.closeCalls...)
	bridge.mu.Unlock()
	if len(closed) != 1 || closed[0] != "popup-tab-1-0" {
		t.Fatalf("closed = %v, want the popup only", closed)
	}
}

func TestBrowserFinalizeTabIDKeepsNamedTab(t *testing.T) {
	bridge := &fakeBrowserBridge{}
	kit, _, store := newBrowserKit(t, bridge)
	for _, id := range []string{"tab-1", "tab-2"} {
		if err := store.Put(BrowserTabRecord{TabID: id, URL: "https://example.com/" + id}); err != nil {
			t.Fatal(err)
		}
	}
	tool := kit.registry.Lookup(browserToolName)
	if _, err := kit.executeBrowserToolResult(context.Background(), browserCall("finalize", map[string]any{
		"tab_id": "tab-1",
	}), tool); err != nil {
		t.Fatal(err)
	}
	kept, ok, err := store.Get("tab-1")
	if err != nil || !ok || kept.Status != "handoff" {
		t.Fatalf("tab_id did not retain tab-1: %+v ok=%v err=%v", kept, ok, err)
	}
	if _, ok, _ := store.Get("tab-2"); ok {
		t.Fatal("unnamed tab stayed open")
	}
}

func TestBrowserFinalizeExplicitEmptyKeepClosesNamedTab(t *testing.T) {
	bridge := &fakeBrowserBridge{}
	kit, _, store := newBrowserKit(t, bridge)
	if err := store.Put(BrowserTabRecord{TabID: "tab-1", URL: "https://example.com"}); err != nil {
		t.Fatal(err)
	}
	tool := kit.registry.Lookup(browserToolName)
	if _, err := kit.executeBrowserToolResult(context.Background(), browserCall("finalize", map[string]any{
		"keep":   []any{},
		"tab_id": "tab-1",
	}), tool); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get("tab-1"); ok {
		t.Fatal("explicit empty keep retained the tab named by tab_id")
	}
}

func TestBrowserScreenshotTransientErrorKeepsTab(t *testing.T) {
	bridge := &fakeBrowserBridge{
		onShot: func(_, _, _ string) (BrowserScreenshotResult, error) {
			// A false timeout (not tab_not_found) while the view is still alive.
			return BrowserScreenshotResult{}, errors.New("screenshot request timed out after 30s (desktop unresponsive or protocol congestion)")
		},
	}
	kit, _, store := newBrowserKit(t, bridge)
	tool := kit.registry.Lookup(browserToolName)
	if err := store.Put(BrowserTabRecord{TabID: "tab-1", URL: "https://example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.executeBrowserToolResult(context.Background(), browserCall("screenshot", map[string]any{"tab_id": "tab-1"}), tool); err == nil {
		t.Fatal("expected a screenshot error")
	}
	// A transient (non-tab_not_found) error must NOT discard the recovery URL.
	rec, ok, gerr := store.Get("tab-1")
	if gerr != nil || !ok || rec.URL != "https://example.com" {
		t.Fatalf("transient screenshot error forgot the tab: ok=%v rec=%+v err=%v", ok, rec, gerr)
	}
}
