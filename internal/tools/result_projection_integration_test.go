package tools

import (
	"bytes"
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

func TestFileMutationProjectionPreservesRecoveryAndWarnings(t *testing.T) {
	for _, name := range []string{"edit_file", "write_file"} {
		kit, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		kit.SetSessionDir(t.TempDir())
		payload := `{"action":"edit","path":"worker.go","contract_warning":"retain warning","diff":{"lines":["literal source"]}}`
		raw := toolresult.FromText(payload)
		call := providers.ToolCall{ID: "mutation", Name: name, Arguments: `{}`}
		got := kit.FinalizeToolResult(call, raw)
		if got.TextProjection() != payload || !reflect.DeepEqual(got.Content, raw.Content) {
			t.Fatal("mutation evidence changed")
		}
		if again := kit.FinalizeToolResult(call, got); !reflect.DeepEqual(again, got) {
			t.Fatal("settled mutation changed on replay")
		}
		raw.IsError = true
		if failed := kit.FinalizeToolResult(call, raw); !failed.IsError || failed.TextProjection() != payload {
			t.Fatal("failure evidence lost")
		}
	}
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
	returned, err := kit.executeKnownToolResult(
		context.Background(), call, fakeListTool{text: listEnvelope(3000)})
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
		"exit_code": 1, "stdout_tail": "... 900 bytes omitted ...\nok\n", "stdout_tail_truncated": true, "stderr_tail": "",
		"verification": map[string]any{
			"kind": "verification", "scope": "targeted", "passed": false,
			"failure_summary": map[string]any{"failed": true, "locations": locations},
		},
	})
	call := providers.ToolCall{ID: "call-over", Name: "bash", Arguments: `{"command":"go test"}`}
	returned, err := kit.executeKnownToolResult(
		context.Background(), call, fakeBashTool{text: raw})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	assertGenericContinuation(t, returned.TextProjection())
	rec := recordFor(kit.ToolTelemetry(), call.ID)
	if rec == nil || rec.Projection != nil || !rec.ResultBudgeted {
		t.Fatalf("over-budget bash must fail open into generic settlement: %+v", rec)
	}
	archived, err := os.ReadFile(parseOut(t, returned.TextProjection())["artifact_ref"].(string))
	fullView, _, ok := renderBashModelView(raw, 0)
	if err != nil || !ok || string(archived) != fullView || returned.Content[0].Text != raw {
		t.Fatalf("generic archive lost original evidence: %v", err)
	}
	// A file cannot serve as the session directory: archival must fail open
	// without discarding any of the terminal output.
	kit.env.SessionDir = parseOut(t, returned.TextProjection())["artifact_ref"].(string)
	input := toolresult.FromText(raw)
	if got := kit.FinalizeToolResult(call, input); got.TextProjection() != fullView || !reflect.DeepEqual(got.Content, input.Content) {
		t.Fatal("archival failure lost terminal output or the audit payload")
	}
}

func TestBashViewModesAndEligibility(t *testing.T) {
	t.Setenv(projectionModeEnvVar, "")
	raw := toolresult.FromText(`{"action":"run","exit_code":0,"duration_ms":5,"output":"ok\nwarning\n","stdout_tail":"ok\n","stderr_tail":"warning\n","stdout_tail_truncated":false,"stderr_tail_truncated":false}`)
	for _, mode := range []string{"off", "shadow", "active"} {
		t.Run(mode, func(t *testing.T) {
			raw := raw.Clone()
			raw.StructuredContent = json.RawMessage(`{"stdout":"private program data"}`)
			kit := &Toolkit{env: &Env{ToolResultProjectionMode: mode}}
			got, _, budgeted, diag := kit.finalizeToolResult(providers.ToolCall{Name: "bash"}, raw)
			if diag != nil || got.TextProjection() != "ok\nwarning" || !reflect.DeepEqual(got.Content, raw.Content) {
				t.Fatalf("legacy mode %s changed canonical output: %q", mode, got.TextProjection())
			}
			if budgeted {
				t.Fatal("a complete view must not advertise omitted evidence")
			}
			if !bytes.Equal(got.StructuredContent, raw.StructuredContent) || strings.Contains(got.TextProjection(), "private program data") {
				t.Fatal("structured data was lost or leaked into the display")
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
				input.Content = append(input.Content, toolresult.ContentPart{Type: toolresult.ContentTypeResource, Resource: json.RawMessage(`{"private":"metadata"}`)})
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
	result, err := kit.executeKnownToolResult(context.Background(), call, fakeBashTool{text: raw})
	if err != nil {
		t.Fatal(err)
	}
	if result.ModelText == nil || result.TextProjection() != "ok\nwarning" || result.Content[0].Text != raw {
		t.Fatal("execution did not settle the view separately from the producer payload")
	}
	record := recordFor(kit.ToolTelemetry(), call.ID)
	if record == nil || record.Projection != nil {
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

func TestCommandReceiptPreservesExecutionAndProgramData(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionDir(t.TempDir())
	call := providers.ToolCall{ID: "receipt", Name: "bash", Arguments: `{"command":"printf 'receipt-out\\n'; printf 'receipt-err\\n' >&2; exit 7"}`}
	result, err := kit.ExecuteResult(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	view := result.TextProjection()
	if strings.Count(view, "receipt-out") != 1 || strings.Count(view, "receipt-err") != 1 || !strings.Contains(view, "Exit code 7") || strings.Contains(view, "printf") {
		t.Fatalf("terminal receipt repeats command/output or loses exit status: %q", view)
	}
	var data struct {
		Stdout     string `json:"stdout"`
		Stderr     string `json:"stderr"`
		ExitCode   int    `json:"exit_code"`
		FullLogRef string `json:"full_log_ref"`
	}
	if err := json.Unmarshal(result.StructuredContent, &data); err != nil {
		t.Fatal(err)
	}
	if data.Stdout != "receipt-out\n" || data.Stderr != "receipt-err\n" || data.ExitCode != 7 || data.FullLogRef == "" {
		t.Fatalf("program data lost: %+v", data)
	}
	log, err := os.ReadFile(data.FullLogRef)
	if err != nil || !strings.Contains(string(log), "receipt-out") || !strings.Contains(string(log), "receipt-err") {
		t.Fatalf("full log missing: %v", err)
	}
	if !strings.Contains(result.Content[0].Text, "classification") {
		t.Fatal("audit envelope lost")
	}
	if again := kit.FinalizeToolResult(call, result); !reflect.DeepEqual(again, result) {
		t.Fatal("settled history changed")
	}
}

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
	returned, err := kit.executeKnownToolResult(context.Background(), call, tool)
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

// Readable source must remain literal while the canonical programmatic result
// and range/continuation facts survive settlement and replay.
func TestReadFileModelViewPreservesLiteralSourceAndRecovery(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.env.SessionDir = t.TempDir()
	t.Setenv(projectionModeEnvVar, "active")
	source := "7|const path = \"C:\\\\tmp\";\n|const greeting = \"你好\";\n"
	for _, continuation := range []any{nil, map[string]any{"has_more": true, "next": map[string]any{"continuation": "opaque-cursor"}}} {
		envelope := map[string]any{"action": "read", "path": "quoted.js", "content": source, "start_line": 7, "num_lines": 2, "total_lines": 30, "truncated": true, "contract_warning": "source warning", "continuation": continuation}
		payload, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		raw := toolresult.FromText(string(payload))
		raw.StructuredContent = json.RawMessage(`{"text":"canonical source"}`)
		call := providers.ToolCall{ID: "read-view", Name: "read_file", Arguments: `{}`}
		got := kit.FinalizeToolResult(call, raw)
		var metadata map[string]any
		if err := json.Unmarshal([]byte(got.TextProjection()), &metadata); err != nil {
			t.Fatal(err)
		}
		if metadata["content"] != source {
			t.Fatal("literal source lost")
		}
		delete(metadata, "content")
		delete(envelope, "content")
		expected, _ := json.Marshal(envelope)
		actual, _ := json.Marshal(metadata)
		if !bytes.Equal(actual, expected) {
			t.Fatalf("range, warning or recovery metadata lost: %s", actual)
		}
		if !reflect.DeepEqual(got.Content, raw.Content) || !bytes.Equal(got.StructuredContent, raw.StructuredContent) {
			t.Fatal("canonical result was mutated")
		}
		if again := kit.FinalizeToolResult(call, got); !reflect.DeepEqual(again, got) {
			t.Fatal("settled read changed on replay")
		}
	}
}
