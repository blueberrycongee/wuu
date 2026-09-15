package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestCUAActivityControlStateMapsMechanism(t *testing.T) {
	cases := []struct {
		name              string
		mechanism         string
		foregroundChanged bool
		policy            string
		wantState         activity.State
	}{
		{name: "background_ax", mechanism: "background_ax", wantState: activity.StateBackgroundControlled},
		{name: "background_directed", mechanism: "background_directed", wantState: activity.StateBackgroundControlled},
		{name: "foreground_native", mechanism: "foreground_native", wantState: activity.StateForegroundControlled},
		{name: "allow input action reports foreground mechanism", mechanism: "foreground_native", policy: "allow", wantState: activity.StateForegroundControlled},
		{name: "allow without mechanism stays background", policy: "allow", wantState: activity.StateBackgroundControlled},
		{name: "policy require overrides ax mechanism", mechanism: "background_ax", policy: "require", wantState: activity.StateForegroundControlled},
		{name: "foreground_changed overrides directed mechanism", mechanism: "background_directed", foregroundChanged: true, wantState: activity.StateForegroundControlled},
		{name: "default background", wantState: activity.StateBackgroundControlled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{}
			if tc.mechanism != "" {
				payload["mechanism"] = tc.mechanism
			}
			if tc.foregroundChanged {
				payload["foreground_changed"] = true
			}
			structured, _ := json.Marshal(payload)
			arguments := `{"action":"type_text","app":"com.apple.TextEdit"}`
			if tc.policy != "" {
				arguments = `{"action":"type_text","app":"com.apple.TextEdit","foreground_policy":"` + tc.policy + `"}`
			}
			got := cuaActivityControlState(arguments, toolresult.Result{StructuredContent: structured})
			if got != tc.wantState {
				t.Fatalf("state = %q, want %q", got, tc.wantState)
			}
		})
	}
}

func TestCanonicalCUATargetFromRunningApps(t *testing.T) {
	apps := json.RawMessage(`{"apps":[{
		"id":"com.apple.Calculator",
		"displayName":"Calculator",
		"bundleIdentifier":"com.apple.Calculator",
		"path":"/System/Applications/Calculator.app"
	}]}`)
	for _, target := range []string{"Calculator", "/System/Applications/Calculator.app", "com.apple.Calculator"} {
		if got := canonicalCUATargetFromApps(target, apps); got != "com.apple.Calculator" {
			t.Fatalf("canonical target for %q = %q", target, got)
		}
	}
	if got := canonicalCUATargetFromApps("Unknown", apps); got != "Unknown" {
		t.Fatalf("unknown target changed to %q", got)
	}
}

func TestCanonicalCUATargetPrefersResolvedInstalledApp(t *testing.T) {
	payload := json.RawMessage(`{"resolved_app":{
		"id":"com.apple.Calculator",
		"bundleIdentifier":"com.apple.Calculator",
		"path":"/System/Applications/Calculator.app"
	},"apps":[]}`)
	if got := canonicalCUATargetFromApps("Calculator", payload); got != "com.apple.Calculator" {
		t.Fatalf("resolved installed target = %q", got)
	}
}

func TestCanonicalCUATargetKeepsResolvingRepeatedLocalizedAlias(t *testing.T) {
	root := t.TempDir()
	tool := &activityTestTool{structuredContent: json.RawMessage(`{"resolved_app":{
		"id":"com.apple.calculator",
		"displayName":"计算器",
		"bundleIdentifier":"com.apple.calculator",
		"path":"/System/Applications/Calculator.app"
	},"apps":[]}`)}
	kit := &Toolkit{
		env:      &Env{RootDir: root},
		registry: NewRegistry(tool),
		boundary: StandardBoundary(),
	}
	for attempt := 1; attempt <= 4; attempt++ {
		if got := kit.canonicalCUATarget(context.Background(), tool, "计算器"); got != "com.apple.calculator" {
			t.Fatalf("canonical target attempt %d = %q", attempt, got)
		}
	}
}

func TestSequenceControlAggregatesHighestMechanism(t *testing.T) {
	stepResult := func(mechanism string, foregroundChanged bool) toolresult.Result {
		payload := map[string]any{"mechanism": mechanism}
		if foregroundChanged {
			payload["foreground_changed"] = true
		}
		structured, _ := json.Marshal(payload)
		return toolresult.Result{StructuredContent: structured}
	}

	var background sequenceControl
	background.observe(`{"action":"click","foreground_policy":"avoid"}`, stepResult("background_ax", false))
	background.observe(`{"action":"type_text","foreground_policy":"avoid"}`, stepResult("background_directed", false))
	if got := cuaActivityControlState("", cuaSequenceResult("completed", nil, 2, nil, background)); got != activity.StateBackgroundControlled {
		t.Fatalf("background sequence state = %q, want %q", got, activity.StateBackgroundControlled)
	}

	var escalated sequenceControl
	escalated.observe(`{"action":"click","foreground_policy":"avoid"}`, stepResult("background_directed", false))
	escalated.observe(`{"action":"type_text","foreground_policy":"allow"}`, stepResult("foreground_native", true))
	if got := cuaActivityControlState("", cuaSequenceResult("completed", nil, 2, nil, escalated)); got != activity.StateForegroundControlled {
		t.Fatalf("escalated sequence state = %q, want %q", got, activity.StateForegroundControlled)
	}

	// A require step force-activates the app even if its own mechanism reads background.
	var required sequenceControl
	required.observe(`{"action":"click","foreground_policy":"require"}`, stepResult("background_ax", false))
	if !required.foregroundChanged {
		t.Fatalf("require step should mark the sequence foreground-changed")
	}
}

func TestCropTransparentPNGRemovesWindowShadowPadding(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 8, 10))
	for y := 2; y < 9; y++ {
		for x := 1; x < 8; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: 48})
		}
	}
	for y := 3; y < 8; y++ {
		for x := 2; x < 7; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 20, G: 30, B: 40, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	cropped, err := png.Decode(bytes.NewReader(cropTransparentPNG(encoded.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	if got := cropped.Bounds().Size(); got.X != 5 || got.Y != 5 {
		t.Fatalf("cropped size = %v, want 5x5", got)
	}
}

type activityTestTool struct {
	calls             int
	structuredContent json.RawMessage
	onExecute         func()
}

func (t *activityTestTool) Name() string { return "mcp_plugin_cua_mac_computer_computer" }
func (t *activityTestTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: t.Name(), InputSchema: map[string]any{"type": "object"}}
}
func (t *activityTestTool) Execute(context.Context, string) (string, error) {
	panic("rich path required")
}
func (t *activityTestTool) ExecuteResult(_ context.Context, arguments string) (toolresult.Result, error) {
	var input struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal([]byte(arguments), &input)
	if input.Action == "list_apps" {
		return toolresult.Result{StructuredContent: t.structuredContent}, nil
	}
	t.calls++
	if t.onExecute != nil {
		t.onExecute()
	}
	return toolresult.Result{Content: []toolresult.ContentPart{
		{Type: toolresult.ContentTypeText, Text: "observed"},
		{Type: toolresult.ContentTypeImage, Data: "cG5n", MIMEType: "image/png"},
	}, StructuredContent: t.structuredContent}, nil
}

func TestCUASequenceDoesNotPublishInteractionAfterTakeover(t *testing.T) {
	registry := activity.NewRegistry()
	tool := &activityTestTool{structuredContent: json.RawMessage(`{"interaction":{"kind":"click","x":0.2,"y":0.4}}`)}
	kit := &Toolkit{
		env:      &Env{RootDir: "/repo", SessionID: "thread-1", SessionDir: t.TempDir()},
		registry: NewRegistry(tool), boundary: StandardBoundary(), activityRegistry: registry,
		mcpActivityBindings: map[string]MCPActivityBinding{
			"plugin.cua-mac.computer": {Kind: activity.KindCUA, PluginID: "cua-mac"},
		},
	}
	tool.onExecute = func() {
		sessions := registry.List("thread-1")
		if len(sessions) == 1 {
			_, _ = registry.Takeover("thread-1", sessions[0].ID)
		}
	}
	call := providers.ToolCall{ID: "sequence-takeover", Name: tool.Name(), Arguments: `{
		"action":"sequence","app":"com.apple.calculator","steps":[
			{"action":"click","element_id":1,"risk":"safe"}
		]}`}
	if _, err := kit.executeActivityBoundToolResult(context.Background(), call, tool, "plugin.cua-mac.computer"); !errors.Is(err, activity.ErrControlRevoked) {
		t.Fatalf("sequence error = %v, want control revoked", err)
	}
	sessions := registry.List("thread-1")
	if len(sessions) != 1 || sessions[0].Interaction != nil || sessions[0].Controller != activity.ControllerUser {
		t.Fatalf("activity after takeover = %+v", sessions)
	}
}
func (t *activityTestTool) IsReadOnly() bool        { return false }
func (t *activityTestTool) IsConcurrencySafe() bool { return false }

func TestExecuteActivityBoundToolCreatesRefAndHonorsTakeover(t *testing.T) {
	registry := activity.NewRegistry()
	tool := &activityTestTool{}
	artifacts := t.TempDir()
	kit := &Toolkit{
		env:              &Env{RootDir: "/repo", SessionID: "thread-1", SessionDir: artifacts},
		registry:         NewRegistry(tool),
		boundary:         StandardBoundary(),
		activityRegistry: registry,
		mcpActivityBindings: map[string]MCPActivityBinding{
			"plugin.cua-mac.computer": {Kind: activity.KindCUA, PluginID: "cua-mac"},
		},
	}
	call := providers.ToolCall{ID: "call-1", Name: tool.Name(), Arguments: `{"action":"observe","app":"com.apple.TextEdit"}`}

	result, err := kit.executeActivityBoundToolResult(context.Background(), call, tool, "plugin.cua-mac.computer")
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	if result.Activity == nil || result.Activity.Kind != string(activity.KindCUA) || result.Activity.ThreadID != "thread-1" {
		t.Fatalf("activity result = %+v", result.Activity)
	}
	if result.Activity.PreviewURI == "" {
		t.Fatalf("activity preview missing: %+v", result.Activity)
	}
	previewPath := filepath.Join(artifacts, "activities", result.Activity.ID, "preview.png")
	if data, readErr := os.ReadFile(previewPath); readErr != nil || string(data) != "png" {
		t.Fatalf("preview artifact = %q, %v", data, readErr)
	}
	sessions := registry.List("thread-1")
	if len(sessions) != 1 || sessions[0].PluginID != "cua-mac" || sessions[0].Target != "com.apple.TextEdit" || sessions[0].State != activity.StateBackgroundControlled || sessions[0].Preview != result.Activity.PreviewURI {
		t.Fatalf("activity sessions = %+v", sessions)
	}
	if tool.calls != 1 {
		t.Fatalf("tool calls = %d", tool.calls)
	}

	if _, err := registry.Takeover("thread-1", sessions[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.executeActivityBoundToolResult(context.Background(), call, tool, "plugin.cua-mac.computer"); !errors.Is(err, activity.ErrControlRevoked) {
		t.Fatalf("execute after takeover = %v, want ErrControlRevoked", err)
	}
	if tool.calls != 1 {
		t.Fatalf("revoked call reached helper: calls=%d", tool.calls)
	}

	if _, _, err := registry.Release("thread-1", sessions[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.executeActivityBoundToolResult(context.Background(), call, tool, "plugin.cua-mac.computer"); err != nil {
		t.Fatalf("execute after release: %v", err)
	}
	if tool.calls != 2 {
		t.Fatalf("released call count = %d", tool.calls)
	}

	if _, err := registry.Stop("thread-1", sessions[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.executeActivityBoundToolResult(context.Background(), call, tool, "plugin.cua-mac.computer"); !errors.Is(err, activity.ErrStopped) {
		t.Fatalf("execute after stop = %v, want ErrStopped", err)
	}
	if tool.calls != 2 {
		t.Fatalf("stopped call reached helper: calls=%d", tool.calls)
	}
}

func TestCUAInputActionPreservesEvidenceAndPublishesActivity(t *testing.T) {
	registry := activity.NewRegistry()
	tool := &activityTestTool{structuredContent: json.RawMessage(`{
		"delivery":"delivered",
		"verification":"not_requested",
		"mechanism":"background_directed",
		"snapshot_id":"snapshot-1",
		"interaction":{"kind":"click","x":0.25,"y":0.75}
	}`)}
	kit := &Toolkit{
		env:      &Env{RootDir: "/repo", SessionID: "thread-1", SessionDir: t.TempDir()},
		registry: NewRegistry(tool), boundary: StandardBoundary(), activityRegistry: registry,
		mcpActivityBindings: map[string]MCPActivityBinding{
			"plugin.cua-mac.computer": {Kind: activity.KindCUA, PluginID: "cua-mac"},
		},
	}
	call := providers.ToolCall{ID: "click-1", Name: tool.Name(), Arguments: `{"action":"click","app":"com.apple.TextEdit","x":500,"y":500,"coordinate_space":"normalized"}`}

	result, err := kit.executeActivityBoundToolResult(context.Background(), call, tool, "plugin.cua-mac.computer")
	if err != nil {
		t.Fatalf("execute click: %v", err)
	}
	if string(result.StructuredContent) != string(tool.structuredContent) {
		t.Fatalf("input evidence lost: %+v", result)
	}
	if result.Activity == nil || result.Activity.Kind != string(activity.KindCUA) {
		t.Fatalf("activity reference missing: %+v", result.Activity)
	}
	sessions := registry.List("thread-1")
	if len(sessions) != 1 || sessions[0].Interaction == nil || sessions[0].Interaction.X != 0.25 || sessions[0].State != activity.StateBackgroundControlled {
		t.Fatalf("internal activity metadata was not preserved: %+v", sessions)
	}
}

func TestCUAGlobalActionsBypassRevokedActivityControl(t *testing.T) {
	registry := activity.NewRegistry()
	session, _, err := registry.Start(activity.StartOptions{
		Kind: activity.KindCUA, ThreadID: "thread-1", Workdir: "/repo", PluginID: "cua-mac", Target: "com.apple.TextEdit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.Takeover("thread-1", session.ID); err != nil {
		t.Fatal(err)
	}
	tool := &activityTestTool{structuredContent: json.RawMessage(`{"apps":[]}`)}
	kit := &Toolkit{
		env:      &Env{RootDir: "/repo", SessionID: "thread-1", SessionDir: t.TempDir()},
		registry: NewRegistry(tool), boundary: StandardBoundary(), activityRegistry: registry,
		mcpActivityBindings: map[string]MCPActivityBinding{
			"plugin.cua-mac.computer": {Kind: activity.KindCUA, PluginID: "cua-mac"},
		},
	}

	result, err := kit.executeActivityBoundToolResult(
		context.Background(),
		providers.ToolCall{ID: "list-1", Name: tool.Name(), Arguments: `{"action":"list_apps"}`},
		tool,
		"plugin.cua-mac.computer",
	)
	if err != nil {
		t.Fatalf("list_apps was blocked by unrelated activity lease: %v", err)
	}
	if string(result.StructuredContent) != `{"apps":[]}` {
		t.Fatalf("list_apps result = %s", result.StructuredContent)
	}
	_, err = kit.executeActivityBoundToolResult(
		context.Background(),
		providers.ToolCall{ID: "resolve-1", Name: tool.Name(), Arguments: `{"action":"list_apps","app":"Calculator"}`},
		tool,
		"plugin.cua-mac.computer",
	)
	if !errors.Is(err, activity.ErrControlRevoked) {
		t.Fatalf("target-resolving list_apps bypassed revoked activity control: %v", err)
	}
}

func TestCUAActivityInteractionValidatesAndAssignsRevision(t *testing.T) {
	result := toolresult.Result{StructuredContent: json.RawMessage(`{"interaction":{"kind":"click","x":0.25,"y":0.75}}`)}
	interaction := cuaActivityInteraction(result)
	if interaction == nil || interaction.Kind != "click" || interaction.X != 0.25 || interaction.Y != 0.75 || interaction.Revision == 0 {
		t.Fatalf("interaction = %+v", interaction)
	}
	invalid := toolresult.Result{StructuredContent: json.RawMessage(`{"interaction":{"kind":"click","x":1.25,"y":0.75}}`)}
	if got := cuaActivityInteraction(invalid); got != nil {
		t.Fatalf("out-of-range interaction = %+v", got)
	}
}

func TestExecuteActivityBoundCUASequenceChecksRiskPerStep(t *testing.T) {
	registry := activity.NewRegistry()
	tool := &activityTestTool{structuredContent: json.RawMessage(`{"interaction":{"kind":"click","x":0.2,"y":0.4}}`)}
	kit := &Toolkit{
		env:      &Env{RootDir: "/repo", SessionID: "thread-1", SessionDir: t.TempDir()},
		registry: NewRegistry(tool), boundary: StandardBoundary(), activityRegistry: registry,
		mcpActivityBindings: map[string]MCPActivityBinding{
			"plugin.cua-mac.computer": {Kind: activity.KindCUA, PluginID: "cua-mac"},
		},
	}
	call := providers.ToolCall{ID: "sequence-1", Name: tool.Name(), Arguments: `{
		"action":"sequence","app":"com.apple.calculator","steps":[
			{"action":"click","element_id":1,"risk":"safe"},
			{"action":"click","element_id":2,"risk":"external_side_effect"}
		]}`}
	result, err := kit.executeActivityBoundToolResult(context.Background(), call, tool, "plugin.cua-mac.computer")
	if err != nil {
		t.Fatalf("sequence execute: %v", err)
	}
	if tool.calls != 1 {
		t.Fatalf("executed steps = %d, want 1 before policy pause", tool.calls)
	}
	if result.Activity == nil || result.Activity.State != string(activity.StateWaitingConfirmation) {
		t.Fatalf("activity state = %+v, want waiting_confirmation", result.Activity)
	}
	sessions := registry.List("thread-1")
	if len(sessions) != 1 || sessions[0].Interaction == nil || sessions[0].Interaction.X != 0.2 {
		t.Fatalf("sequence step interaction = %+v", sessions)
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

func TestExecuteActivityBoundToolRequiresThreadContext(t *testing.T) {
	tool := &activityTestTool{}
	kit := &Toolkit{
		env:              &Env{RootDir: "/repo"},
		registry:         NewRegistry(tool),
		boundary:         StandardBoundary(),
		activityRegistry: activity.NewRegistry(),
		mcpActivityBindings: map[string]MCPActivityBinding{
			"plugin.cua-mac.computer": {Kind: activity.KindCUA, PluginID: "cua-mac"},
		},
	}
	call := providers.ToolCall{ID: "call-1", Name: tool.Name(), Arguments: `{"action":"observe"}`}
	if _, err := kit.executeActivityBoundToolResult(context.Background(), call, tool, "plugin.cua-mac.computer"); err == nil {
		t.Fatal("expected missing thread context error")
	}
	if tool.calls != 0 {
		t.Fatalf("missing-thread call reached helper: calls=%d", tool.calls)
	}
}
