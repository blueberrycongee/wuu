package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestFinalizeGenericToolResultBoundsTextAndPreservesRichMedia(t *testing.T) {
	sessionDir := t.TempDir()
	text := strings.Repeat("head evidence\n", 600) + strings.Repeat("tail evidence\n", 600)
	raw := toolresult.Result{
		Content: []toolresult.ContentPart{
			{Type: toolresult.ContentTypeText, Text: text},
			{Type: toolresult.ContentTypeImage, Data: "aW1hZ2U=", MIMEType: "image/png", Name: "screen.png"},
		},
		StructuredContent: json.RawMessage(`{"caption":"screen","private_payload":"kept"}`),
		Meta:              json.RawMessage(`{"source":"mcp"}`),
		Activity:          &toolresult.ActivityRef{ID: "activity-1", Kind: "computer"},
	}

	got, ref, bounded := finalizeGenericToolResult(sessionDir, "call-rich", raw, 1_000)
	if !bounded || ref == "" {
		t.Fatalf("expected bounded rich result, bounded=%v ref=%q", bounded, ref)
	}
	if len(got.Content) != 2 || got.Content[0].Type != toolresult.ContentTypeText || !reflect.DeepEqual(got.Content[1], raw.Content[1]) {
		t.Fatalf("rich content was not preserved: %+v", got.Content)
	}
	if !strings.Contains(got.Content[0].Text, ref) || !strings.Contains(got.Content[0].Text, "head evidence") || !strings.Contains(got.Content[0].Text, "tail evidence") {
		t.Fatalf("bounded preview lost evidence or recovery index: %.300q", got.Content[0].Text)
	}
	if string(got.StructuredContent) != string(raw.StructuredContent) || string(got.Meta) != string(raw.Meta) || got.Activity == nil || got.Activity.ID != raw.Activity.ID {
		t.Fatalf("rich metadata changed: %+v", got)
	}
	if strings.Contains(got.TextProjection(), "private_payload") {
		t.Fatalf("structured metadata was duplicated into model text: %q", got.TextProjection())
	}
	data, err := os.ReadFile(ref)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if string(data) != raw.TextProjection() {
		t.Fatal("artifact does not contain the exact original model-visible context")
	}
}

func TestFinalizeGenericToolResultBoundsStructuredOnlyResult(t *testing.T) {
	sessionDir := t.TempDir()
	raw := toolresult.Result{
		StructuredContent: json.RawMessage(`{"payload":"` + strings.Repeat("x", 4_000) + `"}`),
		Meta:              json.RawMessage(`{"private":true}`),
	}

	got, ref, bounded := finalizeGenericToolResult(sessionDir, "call-structured", raw, 1_000)
	if !bounded || ref == "" || len(got.Content) != 1 || got.Content[0].Type != toolresult.ContentTypeText {
		t.Fatalf("structured-only result was not bounded: bounded=%v ref=%q result=%+v", bounded, ref, got)
	}
	if !strings.Contains(got.TextProjection(), ref) || strings.Contains(got.TextProjection(), strings.Repeat("x", 2_000)) {
		t.Fatalf("structured-only provider projection is not bounded: %.300q", got.TextProjection())
	}
	var index map[string]any
	if err := json.Unmarshal([]byte(got.TextProjection()), &index); err != nil {
		t.Fatalf("structured projection must remain valid JSON: %v", err)
	}
	if index["kind"] != "archived_structured_tool_result" || index["shape"] != "object" {
		t.Fatalf("structured projection lacks a meaningful index: %+v", index)
	}
	if string(got.StructuredContent) != string(raw.StructuredContent) || string(got.Meta) != string(raw.Meta) {
		t.Fatal("structured-only metadata was not retained")
	}
}

func TestFinalizeGenericToolResultUsesLineLimit(t *testing.T) {
	raw := toolresult.FromText(strings.Repeat("x\n", defaultResultMaxLines+1))
	got, ref, bounded := finalizeGenericToolResult(t.TempDir(), "call-lines", raw, defaultResultBudget)
	if !bounded || ref == "" || got.TextProjection() == raw.TextProjection() {
		t.Fatal("line-heavy result should cross the generic settlement boundary")
	}
}

func TestFinalizeGenericToolResultFailsOpenWithoutArtifactStorage(t *testing.T) {
	raw := toolresult.Result{
		Content: []toolresult.ContentPart{
			{Type: toolresult.ContentTypeText, Text: strings.Repeat("x", 2_000)},
			{Type: toolresult.ContentTypeImage, Data: "aW1hZ2U=", MIMEType: "image/png"},
		},
	}
	got, ref, bounded := finalizeGenericToolResult("", "call-no-store", raw, 1_000)
	if bounded || ref != "" || got.JSONProjection() != raw.JSONProjection() {
		t.Fatal("settlement without recoverable storage must fail open")
	}
}

func TestStructuredResultProjectionBoundsOversizedScalars(t *testing.T) {
	longKey := strings.Repeat("key", 20000)
	wide := map[string]any{"status": "ready"}
	for i := 0; i < 64; i++ {
		wide[fmt.Sprintf("key-%02d-%s", i, strings.Repeat("<&\"", 200))] = i
	}
	for _, tc := range []struct {
		name  string
		value map[string]any
	}{
		{name: "long key", value: map[string]any{longKey: "value", "status": "ready"}},
		{name: "escaped keys", value: wide},
		{name: "large number", value: map[string]any{"count": json.Number(strings.Repeat("9", 60000)), "status": "ready"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			raw := toolresult.Result{StructuredContent: encoded, Meta: json.RawMessage(`{"source":"tool"}`)}
			if err := raw.Validate(); err != nil {
				t.Fatalf("fixture must be a valid tool result: %v", err)
			}
			got, ref, bounded := finalizeGenericToolResult(t.TempDir(), "structured", raw, defaultResultBudget)
			if !bounded || ref == "" {
				t.Fatal("oversized structured result was not archived")
			}
			if size := len(got.TextProjection()); size > projectionPreviewBytes {
				t.Errorf("archive index exceeds preview budget: %d bytes", size)
			}
			// Provider lowering adds a semantic index of the retained structured
			// data. That second projection must not reintroduce oversized scalars.
			if size := len(providers.ProjectToolResult(got).ToolText); size > defaultResultBudget {
				t.Errorf("provider projection reintroduced oversized data: %d bytes", size)
			}
			if string(got.StructuredContent) != string(encoded) || string(got.Meta) != string(raw.Meta) {
				t.Fatal("projection changed durable structured data")
			}
			archived, err := os.ReadFile(ref)
			if err != nil || string(archived) != raw.TextProjection() {
				t.Fatalf("archive did not preserve the complete result: %v", err)
			}
			index := parseOut(t, got.TextProjection())
			if index["shape"] != "object" || index["key_count"] != float64(len(tc.value)) {
				t.Fatalf("archive index lost the original shape: %+v", index)
			}
			next := index["continuation"].(map[string]any)["next"].(map[string]any)
			cursor, err := decodeReadFileContinuation(next["continuation"].(string))
			if err != nil || cursor.Path != ref || cursor.ExpectedSHA256 != sha256Hex(archived) || cursor.ByteOffset == nil || *cursor.ByteOffset != 0 || cursor.ByteEndOffset == nil || *cursor.ByteEndOffset != len(archived) {
				t.Fatalf("archive recovery does not cover the original result: %+v, err=%v", cursor, err)
			}
		})
	}
}
