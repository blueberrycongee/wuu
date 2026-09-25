package tools

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func ptrToolResult(text string) *toolresult.Result {
	r := toolresult.FromText(text)
	return &r
}

type fakeListTool struct{ text string }

func (f fakeListTool) Name() string { return "list_files" }
func (f fakeListTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: "list_files"}
}
func (f fakeListTool) Execute(context.Context, string) (string, error) { return f.text, nil }
func (f fakeListTool) IsReadOnly() bool                                { return true }
func (f fakeListTool) IsConcurrencySafe() bool                         { return true }

type fakeRichMediaTool struct{ text string }

func (f fakeRichMediaTool) Name() string { return "mcp_rich_media" }
func (f fakeRichMediaTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: f.Name()}
}
func (f fakeRichMediaTool) Execute(context.Context, string) (string, error) {
	return f.text, nil
}
func (f fakeRichMediaTool) ExecuteResult(context.Context, string) (toolresult.Result, error) {
	return toolresult.Result{
		Content: []toolresult.ContentPart{
			{Type: toolresult.ContentTypeText, Text: f.text},
			{Type: toolresult.ContentTypeImage, Data: "aW1hZ2U=", MIMEType: "image/png", Name: "screen.png"},
		},
		StructuredContent: json.RawMessage(`{"caption":"structured metadata"}`),
		Meta:              json.RawMessage(`{"source":"mcp"}`),
	}, nil
}
func (f fakeRichMediaTool) IsReadOnly() bool        { return true }
func (f fakeRichMediaTool) IsConcurrencySafe() bool { return true }

func runFakeList(t *testing.T, mode string) (providers.ToolCall, string, []ToolExecutionRecord) {
	t.Helper()
	t.Setenv(projectionModeEnvVar, "") // isolate from any ambient override
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New toolkit: %v", err)
	}
	kit.env.SessionDir = t.TempDir()
	kit.env.ToolResultProjectionMode = mode

	call := providers.ToolCall{ID: "call-int", Name: "list_files", Arguments: "{}"}
	returned, err := kit.executeKnownToolResultWithRepeatPolicy(
		context.Background(), call, fakeListTool{text: listEnvelope(3000)}, true)
	if err != nil {
		t.Fatalf("execute (mode=%s): %v", mode, err)
	}
	return call, returned.TextProjection(), kit.ToolTelemetry()
}

func recordFor(records []ToolExecutionRecord, callID string) *ToolExecutionRecord {
	for i := range records {
		if records[i].CallID == callID {
			return &records[i]
		}
	}
	return nil
}

func assertGenericContinuation(t *testing.T, text string) {
	t.Helper()
	m := parseOut(t, text)
	if m["kind"] != "archived_tool_result" || m["artifact_ref"] == "" {
		t.Fatalf("generic budget omitted artifact identity: %s", snip(text, 200))
	}
	continuation, _ := m["continuation"].(map[string]any)
	if continuation == nil || continuation["next"] == nil {
		t.Fatalf("generic budget omitted stable continuation: %s", snip(text, 300))
	}
}

func TestChokePoint_ModeOff_UsesGenericBudgetNoDiagnostics(t *testing.T) {
	call, text, records := runFakeList(t, "off")
	assertGenericContinuation(t, text)
	rec := recordFor(records, call.ID)
	if rec == nil || rec.Projection != nil {
		t.Fatalf("off mode must not compute projection diagnostics: %+v", rec)
	}
}

func TestChokePoint_ModeShadow_MeasuresButDoesNotApply(t *testing.T) {
	call, text, records := runFakeList(t, "shadow")
	assertGenericContinuation(t, text)
	rec := recordFor(records, call.ID)
	if rec == nil || rec.Projection == nil {
		t.Fatalf("shadow mode must record projection diagnostics")
	}
	if !rec.Projection.Applied {
		t.Fatalf("shadow diagnostics should show the projection would apply: %+v", rec.Projection)
	}
	if rec.Projection.ProjectionHash == "" || rec.Projection.OriginalHash == "" {
		t.Fatalf("shadow diagnostics must carry content hashes for stability tracking")
	}
}

func TestChokePoint_ModeActive_AppliesBoundedProjection(t *testing.T) {
	call, text, records := runFakeList(t, "active")
	if !strings.HasPrefix(strings.TrimSpace(text), "{") {
		t.Fatalf("active mode must return the projected JSON envelope, got: %s", snip(text, 80))
	}
	if got := estimateResultTokens(text); got > defaultProjectionTokenBudget {
		t.Fatalf("active projection over budget: %d tokens", got)
	}
	if !strings.Contains(text, "projection") || !strings.Contains(text, "artifact_ref") {
		t.Fatalf("active projection must reference its artifact")
	}
	rec := recordFor(records, call.ID)
	if rec == nil || rec.Projection == nil || !rec.Projection.Applied {
		t.Fatalf("active mode must record an applied projection: %+v", rec)
	}
	if rec.Projection.ProjectedTokens >= rec.Projection.OriginalTokens {
		t.Fatalf("active projection must reduce tokens: %+v", rec.Projection)
	}
	if rec.ResultRef == "" {
		t.Fatalf("active projection must record a recovery ref")
	}
}

type fakeBashTool struct{ text string }

func (f fakeBashTool) Name() string { return "bash" }
func (f fakeBashTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: "bash"}
}
func (f fakeBashTool) Execute(context.Context, string) (string, error) { return f.text, nil }
func (f fakeBashTool) IsReadOnly() bool                                { return false }
func (f fakeBashTool) IsConcurrencySafe() bool                         { return false }

func TestChokePoint_OverBudgetBashUsesGenericSettlement(t *testing.T) {
	t.Setenv(projectionModeEnvVar, "")
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.env.SessionDir = t.TempDir()
	kit.env.ToolResultProjectionMode = "active"
	locations := make([]map[string]any, 8)
	for i := range locations {
		locations[i] = map[string]any{"path": "handlers_test.go", "line": 42 + i, "text": strings.Repeat("failing assertion detail ", 400)}
	}
	raw := bashEnvelope(map[string]any{
		"exit_code": 1, "stdout_tail": "ok\n", "stderr_tail": "",
		"verification": map[string]any{
			"kind": "verification", "scope": "targeted", "passed": false,
			"failure_summary": map[string]any{"failed": true, "locations": locations},
		},
	})
	call := providers.ToolCall{ID: "call-over", Name: "bash", Arguments: `{"command":"go test"}`}
	returned, err := kit.executeKnownToolResultWithRepeatPolicy(
		context.Background(), call, fakeBashTool{text: raw}, true)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	assertGenericContinuation(t, returned.TextProjection())
	rec := recordFor(kit.ToolTelemetry(), call.ID)
	if rec == nil || rec.Projection == nil || rec.Projection.Applied {
		t.Fatalf("over-budget bash must fail open into generic settlement: %+v", rec)
	}
	archived, err := os.ReadFile(parseOut(t, returned.TextProjection())["artifact_ref"].(string))
	if err != nil || string(archived) != raw {
		t.Fatalf("generic archive lost original evidence: %v", err)
	}
	// A file cannot serve as the session directory: archival must fail open
	// without settling the oversized text.
	kit.env.SessionDir = parseOut(t, returned.TextProjection())["artifact_ref"].(string)
	input := toolresult.FromText(raw)
	if got := kit.FinalizeToolResult(call, input); !reflect.DeepEqual(got, input) {
		t.Fatal("archival failure changed the original result")
	}
}

func TestBashViewModesAndEligibility(t *testing.T) {
	t.Setenv(projectionModeEnvVar, "")
	raw := toolresult.FromText(`{"action":"run","exit_code":0,"duration_ms":5,"output":"ok\nwarning\n","stdout_tail":"ok\n","stderr_tail":"warning\n","stdout_tail_truncated":false,"stderr_tail_truncated":false}`)
	for _, mode := range []string{"off", "shadow", "active"} {
		t.Run(mode, func(t *testing.T) {
			kit := &Toolkit{env: &Env{ToolResultProjectionMode: mode}}
			got, _, budgeted, diag := kit.finalizeToolResult(providers.ToolCall{Name: "bash"}, raw)
			if mode == "off" {
				if diag != nil {
					t.Fatal("off mode computed projection")
				}
			} else if diag == nil || diag.Reason != reasonRendered {
				t.Fatalf("missing view diagnostics: %+v", diag)
			}
			rendered := strings.HasPrefix(got.TextProjection(), "exit 0 · 5ms\nok\n--- stderr ---\nwarning")
			if rendered != (mode == "active") || (mode != "active" && got.TextProjection() != raw.TextProjection()) {
				t.Fatalf("projection mode %s changed the wrong model text: %q", mode, got.TextProjection())
			}
			if budgeted {
				t.Fatal("a complete view must not advertise omitted evidence")
			}
			// A later mode change must not rewrite an already settled history.
			kit.env.ToolResultProjectionMode = "active"
			if again := kit.FinalizeToolResult(providers.ToolCall{Name: "bash"}, got); !reflect.DeepEqual(again, got) {
				t.Fatal("settled history was retroactively rewritten")
			}
		})
	}
	for _, name := range []string{"mcp_server_bash", "custom_bash", "Bash", "bash"} {
		for _, rich := range []bool{false, true} {
			if name == "bash" && !rich {
				continue
			}
			input := raw.Clone()
			if rich {
				input.StructuredContent = json.RawMessage(`{"private":"metadata"}`)
			}
			got, d := finalizeBuiltInToolResult("", name, "ineligible", input, 0)
			if d.Applied || !reflect.DeepEqual(got, input) {
				t.Fatalf("ineligible %s rich=%v was rewritten", name, rich)
			}
		}
	}
}

func TestBashViewSurvivesStorageAndRequestPreparation(t *testing.T) {
	t.Setenv(projectionModeEnvVar, "active")
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	call := providers.ToolCall{ID: "bash-storage", Name: "bash", Arguments: `{}`}
	raw := `{"action":"run","exit_code":0,"duration_ms":5,"output":"ok\nwarning\n","stdout_tail":"ok\n","stderr_tail":"warning\n","stdout_tail_truncated":false,"stderr_tail_truncated":false}`
	result, err := kit.executeKnownToolResultWithRepeatPolicy(context.Background(), call, fakeBashTool{text: raw}, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.ModelText == nil || !strings.HasPrefix(result.TextProjection(), "exit 0") || result.Content[0].Text != raw {
		t.Fatal("execution did not settle the view separately from the producer payload")
	}
	record := recordFor(kit.ToolTelemetry(), call.ID)
	if record == nil || record.Projection == nil || !record.Projection.Applied || record.Projection.Reason != reasonRendered {
		t.Fatalf("execution did not record view diagnostics: %+v", record)
	}
	if record.ResultBudgeted || record.ResultRef != "" {
		t.Fatalf("complete view reported omitted evidence: %+v", record)
	}
	envelope := record.ResultEnvelope()
	if envelope.Truncated || len(envelope.Warnings) != 0 || envelope.DataRef != "" {
		t.Fatalf("complete view advertised truncation or recovery: %+v", envelope)
	}
	dir := t.TempDir()
	if _, err := session.CreateWithMetadata(dir, "bash-replay", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendHistoryRecord(dir, "bash-replay", session.HistoryRecord{Role: "tool", ToolCallID: call.ID, Content: result.TextProjection(), ToolResult: payload}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.MaintainRedundantStorage(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	records, err := session.LoadHistoryRecords(dir, "bash-replay", false)
	if err != nil || len(records) != 1 {
		t.Fatalf("load history: records=%d err=%v", len(records), err)
	}
	var restored toolresult.Result
	if err := json.Unmarshal(records[0].ToolResult, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, result) || records[0].Content != result.TextProjection() {
		t.Fatal("storage changed producer or settled model text")
	}
	for _, input := range []toolresult.Result{result, restored} {
		input = kit.FinalizeToolResult(call, input)
		messages := []providers.ChatMessage{
			{Role: "assistant", ToolCalls: []providers.ToolCall{call}},
			{Role: "tool", ToolCallID: call.ID, Content: input.TextProjection(), ToolResult: &input},
		}
		for range 2 {
			messages, err = providers.PrepareMessagesForModelRequest("gpt-5", messages)
			if err != nil || toolContent(messages, call.ID) != result.TextProjection() {
				t.Fatalf("request preparation restored the JSON envelope: %v", err)
			}
		}
	}
}

func TestChokePoint_EnvOverrideBeatsConfiguredMode(t *testing.T) {
	t.Setenv(projectionModeEnvVar, "active")
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.env.SessionDir = t.TempDir()
	kit.env.ToolResultProjectionMode = "off" // env override should win
	call := providers.ToolCall{ID: "c", Name: "list_files", Arguments: "{}"}
	returned, err := kit.executeKnownToolResultWithRepeatPolicy(
		context.Background(), call, fakeListTool{text: listEnvelope(3000)}, true)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(returned.TextProjection()), "{") {
		t.Fatalf("env override to active did not take effect: %s", snip(returned.TextProjection(), 80))
	}
}

// TestActiveProjection_IsStableThroughWireProjection proves the fix closes the
// bypass: once the result is finalized (bounded, text-only), the provider
// projection cannot restore a larger result, and repeated preparation is
// byte-identical (cache-safe within an epoch).
func TestActiveProjection_IsStableThroughWireProjection(t *testing.T) {
	call, projectedText, _ := runFakeList(t, "active")

	stable := providers.ChatMessage{
		Role:       "tool",
		Name:       "list_files",
		ToolCallID: call.ID,
		Content:    projectedText,
		ToolResult: ptrToolResult(projectedText),
	}
	msgs := []providers.ChatMessage{
		{Role: "user", Content: "search"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: call.ID, Name: "list_files"}}},
		stable,
	}

	first, err := providers.PrepareMessagesForModelRequest("gpt-5", msgs)
	if err != nil {
		t.Fatalf("prepare#1: %v", err)
	}
	second, err := providers.PrepareMessagesForModelRequest("gpt-5", first)
	if err != nil {
		t.Fatalf("prepare#2: %v", err)
	}
	c1 := toolContent(first, call.ID)
	c2 := toolContent(second, call.ID)
	if c1 != projectedText {
		t.Fatalf("wire content diverged from the finalized projection")
	}
	if c1 != c2 {
		t.Fatalf("wire content not stable across repeated preparation")
	}
	if got := estimateResultTokens(c1); got > defaultProjectionTokenBudget {
		t.Fatalf("wire content exceeded budget: %d tokens", got)
	}
}

func TestRichMediaSettlement_IsStableAndKeepsNativeObservation(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.env.SessionDir = t.TempDir()
	tool := fakeRichMediaTool{text: strings.Repeat("semantic evidence line\n", 3_000)}
	call := providers.ToolCall{ID: "call-rich", Name: tool.Name(), Arguments: "{}"}
	returned, err := kit.executeKnownToolResultWithRepeatPolicy(context.Background(), call, tool, true)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	record := recordFor(kit.ToolTelemetry(), call.ID)
	if record == nil || !record.ResultBudgeted || record.ResultRef == "" {
		t.Fatalf("rich result did not cross settlement boundary: %+v", record)
	}
	if len(returned.Content) != 2 || returned.Content[1].Type != toolresult.ContentTypeImage {
		t.Fatalf("native media was not retained: %+v", returned.Content)
	}
	if !strings.Contains(returned.TextProjection(), record.ResultRef) || strings.Contains(returned.TextProjection(), "structured metadata") {
		t.Fatalf("model projection lacks recovery index or leaks duplicate metadata: %.300q", returned.TextProjection())
	}

	messages := []providers.ChatMessage{
		{Role: "user", Content: "inspect"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: call.ID, Name: call.Name}}},
		{Role: "tool", Name: call.Name, ToolCallID: call.ID, Content: returned.TextProjection(), ToolResult: &returned},
	}
	first, err := providers.PrepareMessagesForModelRequest("gpt-5", messages)
	if err != nil {
		t.Fatalf("prepare#1: %v", err)
	}
	second, err := providers.PrepareMessagesForModelRequest("gpt-5", messages)
	if err != nil {
		t.Fatalf("prepare#2: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("rich result request projection is not byte-stable")
	}
	if got := toolContent(first, call.ID); got != returned.TextProjection() {
		t.Fatal("request preparation changed the settled page")
	}
	if archived := mustReadFile(t, record.ResultRef); !strings.Contains(archived, "structured metadata") || strings.Contains(archived, `"source":"mcp"`) {
		t.Fatal("archived text lost structured semantics or leaked private metadata")
	}
	if len(first) != 4 || len(first[3].Images) != 1 || first[3].Images[0].Data != "aW1hZ2U=" {
		t.Fatalf("native image observation missing: %+v", first)
	}
}

func toolContent(msgs []providers.ChatMessage, callID string) string {
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID == callID {
			return m.Content
		}
	}
	return ""
}
