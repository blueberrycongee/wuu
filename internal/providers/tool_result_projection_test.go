package providers

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestBudgetedToolResultRequestCloneIsolation(t *testing.T) {
	for _, text := range []string{"bounded", ""} {
		t.Run(fmt.Sprintf("bytes_%d", len(text)), func(t *testing.T) {
			result := toolresult.Result{
				Content:           []toolresult.ContentPart{{Type: "text", Text: "original recovery text"}, {Type: "image", MIMEType: "image/png", Data: "aW1hZ2U="}},
				StructuredContent: json.RawMessage(`{"continuation":{"next":"recovery-cursor"}}`),
				Meta:              json.RawMessage(`{"private":"metadata"}`), IsError: true, ModelText: &text,
			}
			history := []ChatMessage{
				{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "lookup", Arguments: `{}`}}},
				{Role: "tool", ToolCallID: "call_1", Content: text, ToolResult: &result},
			}
			original := CloneChatMessages(history)
			for range 2 {
				prepared, err := PrepareMessagesForModelRequest("gpt-4o", history)
				if err != nil {
					t.Fatal(err)
				}
				if prepared[1].Content != text || !prepared[1].ToolResult.IsError || len(prepared) != 3 || len(prepared[2].Images) != 1 {
					t.Fatal("settled result or observation lost")
				}
				*prepared[1].ToolResult.ModelText = "request-only mutation"
				prepared[1].ToolResult.Content[0].Text = "request-only mutation"
				prepared[1].ToolResult.StructuredContent[0] = ' '
				prepared[1].ToolResult.Meta[0] = ' '
				prepared[2].Images[0].Data = "changed"
				if !reflect.DeepEqual(history, original) {
					t.Fatal("prepared result aliases stored history")
				}
			}
		})
	}
}

func TestProjectToolResultPreservesOrderAndHidesPrivateMetadata(t *testing.T) {
	result := toolresult.Result{
		Content: []toolresult.ContentPart{
			{Type: "text", Text: "first"},
			{Type: "image", Data: "aW1hZ2U=", MIMEType: "image/png", Name: "screenshot"},
			{Type: "resource_link", URI: "https://example.test/docs", Name: "docs"},
			{Type: "resource", Resource: json.RawMessage(`{"uri":"file:///tmp/a","text":"body"}`), Name: "embedded"},
			{Type: "audio", Data: "YXVkaW8=", MIMEType: "audio/wav", Name: "clip.wav"},
		},
		StructuredContent: json.RawMessage(`{"b":2,"a":1}`),
		Meta:              json.RawMessage(`{"secret":"must-not-reach-model"}`),
		Activity:          &toolresult.ActivityRef{ID: "activity-1", Kind: "browser"},
	}
	projected := ProjectToolResult(result)
	parts := strings.Split(projected.ToolText, "\n")
	if len(parts) != 4 || strings.Join(parts[:3], "\n") != "first\n[resource_link: docs (https://example.test/docs)]\n[resource: embedded] {\"text\":\"body\",\"uri\":\"file:///tmp/a\"}" {
		t.Fatalf("ToolText prefix = %q", projected.ToolText)
	}
	var index map[string]any
	if err := json.Unmarshal([]byte(parts[3]), &index); err != nil {
		t.Fatalf("structured index is not JSON: %v", err)
	}
	preview, _ := index["value_preview"].(map[string]any)
	if index["shape"] != "object" || len(index["sha256"].(string)) != 64 || preview["a"] != float64(1) || preview["b"] != float64(2) {
		t.Fatalf("structured semantic index = %+v", index)
	}
	if len(projected.ObservationImages) != 1 || projected.ObservationImages[0].MediaType != "image/png" {
		t.Fatalf("images = %+v", projected.ObservationImages)
	}
	if len(projected.ObservationFiles) != 1 || projected.ObservationFiles[0].Filename != "clip.wav" {
		t.Fatalf("files = %+v", projected.ObservationFiles)
	}
	if strings.Contains(projected.ToolText, "must-not-reach-model") || strings.Contains(projected.ToolText, "activity-1") {
		t.Fatalf("private metadata leaked: %q", projected.ToolText)
	}
}

func TestProjectToolResultUsesStructuredContentWhenNoContentExists(t *testing.T) {
	projected := ProjectToolResult(toolresult.Result{StructuredContent: json.RawMessage(`{"b":2,"a":1}`)})
	if got, want := projected.ToolText, `{"a":1,"b":2}`; got != want {
		t.Fatalf("ToolText = %q, want %q", got, want)
	}
}

func TestProjectToolResultBoundsMixedStructuredContentToSemanticIndex(t *testing.T) {
	keys := make([]string, 0, structuredResultIndexMaxKeys+5)
	structured := map[string]any{}
	for index := 0; index < structuredResultIndexMaxKeys+5; index++ {
		key := strings.Repeat("k", structuredResultIndexMaxKeyRunes+10) + string(rune('a'+index))
		keys = append(keys, key)
		structured[key] = strings.Repeat("private-value", 1_000)
	}
	raw, err := json.Marshal(structured)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	projected := ProjectToolResult(toolresult.Result{
		Content:           []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: "human-readable summary"}},
		StructuredContent: raw,
	})
	parts := strings.Split(projected.ToolText, "\n")
	if len(parts) != 2 || parts[0] != "human-readable summary" {
		t.Fatalf("mixed projection lost producer text: %.300q", projected.ToolText)
	}
	var index map[string]any
	if err := json.Unmarshal([]byte(parts[1]), &index); err != nil {
		t.Fatalf("structured index is not JSON: %v", err)
	}
	if index["kind"] != "structured_tool_result_index" || index["shape"] != "object" || index["key_count"] != float64(len(keys)) {
		t.Fatalf("structured index lacks shape metadata: %+v", index)
	}
	if got := index["keys"].([]any); len(got) != structuredResultIndexMaxKeys {
		t.Fatalf("visible keys = %d, want %d", len(got), structuredResultIndexMaxKeys)
	}
	preview, ok := index["value_preview"].(map[string]any)
	if !ok || len(preview) == 0 || index["preview_truncated"] != true {
		t.Fatalf("structured index lacks bounded value evidence: %+v", index)
	}
	if index["keys_omitted"] != float64(5) || strings.Contains(projected.ToolText, strings.Repeat("private-value", 1_000)) {
		t.Fatalf("structured index leaked an unbounded value or lost omission count: %.300q", projected.ToolText)
	}
}

func TestProjectToolResultProvidesTextForEmptySuccess(t *testing.T) {
	projected := ProjectToolResult(toolresult.Result{})
	if projected.ToolText != emptyToolResultText {
		t.Fatalf("empty result projection = %q, want %q", projected.ToolText, emptyToolResultText)
	}
}

func TestPrepareMessagesProvidesTextForLegacyEmptyToolMessage(t *testing.T) {
	prepared, err := PrepareMessagesForModelRequest("gpt-5", []ChatMessage{
		{Role: "user", Content: "act"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "computer"}}},
		{Role: "tool", ToolCallID: "call-1"},
	})
	if err != nil {
		t.Fatalf("PrepareMessagesForModelRequest: %v", err)
	}
	if prepared[2].Content != emptyToolResultText {
		t.Fatalf("legacy empty tool content = %q, want %q", prepared[2].Content, emptyToolResultText)
	}
}

func TestPrepareMessagesPlacesObservationsAfterContiguousToolResults(t *testing.T) {
	imageResult := toolresult.Result{Content: []toolresult.ContentPart{{Type: "image", Data: "aW1hZ2U=", MIMEType: "image/png"}}}
	fileResult := toolresult.Result{Content: []toolresult.ContentPart{{Type: "file", Data: "ZmlsZQ==", MIMEType: "application/pdf", Name: "report.pdf"}}}
	messages := []ChatMessage{
		{Role: "user", Content: "inspect"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "observe"}, {ID: "call-2", Name: "download"}}},
		{Role: "tool", ToolCallID: "call-1", Name: "observe", ToolResult: &imageResult},
		{Role: "tool", ToolCallID: "call-2", Name: "download", ToolResult: &fileResult},
	}

	prepared, err := PrepareMessagesForModelRequest("gpt-5", messages)
	if err != nil {
		t.Fatalf("PrepareMessagesForModelRequest: %v", err)
	}
	if len(prepared) != 5 || prepared[2].Role != "tool" || prepared[3].Role != "tool" || prepared[4].Role != "user" {
		t.Fatalf("tool results/observation order = %+v", prepared)
	}
	if len(prepared[4].Images) != 1 || len(prepared[4].Files) != 1 || !prepared[4].Hidden {
		t.Fatalf("observation message = %+v", prepared[4])
	}
	if prepared[2].Content == "" || prepared[3].Content == "" {
		t.Fatalf("unsupported attachment notes missing: %+v", prepared[2:4])
	}
	if err := ValidateToolCallHistory(prepared); err != nil {
		t.Fatalf("projected history invalid: %v", err)
	}
}
