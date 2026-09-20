package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestGenericResultPagesRecoverUnicodeAndSurviveCallIDReuse(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.env.SessionDir = t.TempDir()
	text := strings.Repeat("证据🙂\\\"", 4000)
	call := providers.ToolCall{ID: "../repeated", Name: "extension_records"}
	settled := kit.FinalizeToolResult(call, toolresult.FromText(text))
	if settled.ModelText == nil {
		t.Fatal("result did not settle")
	}
	first := parseOut(t, settled.TextProjection())
	artifact := first["artifact_ref"].(string)
	if filepath.Dir(artifact) != filepath.Join(kit.env.SessionDir, "tool-results") {
		t.Fatal("call ID escaped the artifact directory")
	}
	kit.FinalizeToolResult(call, toolresult.FromText(strings.Repeat("different", 5000)))
	if mustReadFile(t, artifact) != text {
		t.Fatal("reused call ID overwrote earlier evidence")
	}
	if again := kit.FinalizeToolResult(call, settled); again.JSONProjection() != settled.JSONProjection() {
		t.Fatal("finalization changed an already settled result")
	}
	var recovered strings.Builder
	pageText := settled.TextProjection()
	for pages := 0; ; pages++ {
		if pages > 200 || !utf8.ValidString(pageText) || estimateResultTokens(pageText) > defaultProjectionTokenBudget {
			t.Fatal("page exceeded its budget or failed to advance")
		}
		page := parseOut(t, pageText)
		content, ok := page["content"].(string)
		if !ok || content == "" || page["byte_offset"] != float64(recovered.Len()) {
			t.Fatal("Unicode recovery lost a contiguous text range")
		}
		recovered.WriteString(content)
		continuation := page["continuation"].(map[string]any)
		if continuation["has_more"] != true {
			break
		}
		args, _ := json.Marshal(continuation["next"])
		pageText, err = kit.Execute(context.Background(), providers.ToolCall{ID: fmt.Sprintf("page-%d", pages), Name: "read_file", Arguments: string(args)})
		if err != nil {
			t.Fatal(err)
		}
	}
	if recovered.String() != text {
		t.Fatal("Unicode recovery lost or duplicated bytes")
	}
	args, _ := json.Marshal(first["continuation"].(map[string]any)["next"])
	mustWriteFile(t, artifact, "changed")
	if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: "read_file", Arguments: string(args)}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("changed artifact was accepted: %v", err)
	}
}

func TestSettledPageDoesNotInvalidateMaximumProducerPayload(t *testing.T) {
	raw := toolresult.Result{}
	for i := 0; i < 3; i++ {
		raw.Content = append(raw.Content, toolresult.ContentPart{Type: toolresult.ContentTypeText, Text: strings.Repeat("x", 1024*1024)})
	}
	raw.Content[2].Text = raw.Content[2].Text[:len(raw.Content[2].Text)-(raw.SizeBytes()-toolresult.MaxResultBytes)]
	if err := raw.Validate(); err != nil {
		t.Fatal(err)
	}
	settled, _, paged := finalizeGenericToolResult(t.TempDir(), "large", raw, defaultProjectionTokenBudget)
	if !paged {
		t.Fatal("maximum-size result was not paged")
	}
	if err := settled.Validate(); err != nil {
		t.Fatalf("host projection invalidated an accepted producer result: %v", err)
	}
}

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
	if !reflect.DeepEqual(got.Content, raw.Content) {
		t.Fatalf("rich content was not preserved: %+v", got.Content)
	}
	if !strings.Contains(got.TextProjection(), ref) || !strings.Contains(got.TextProjection(), "head evidence") {
		t.Fatalf("first page lost evidence or recovery cursor: %.300q", got.TextProjection())
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
	if string(data) != providers.ProjectToolResult(raw).ToolText {
		t.Fatal("artifact does not contain the exact original model-visible context")
	}
}

func TestFinalizeGenericToolResultBoundsStructuredOnlyResult(t *testing.T) {
	sessionDir := t.TempDir()
	raw := toolresult.Result{
		StructuredContent: json.RawMessage(`{"payload":"` + strings.Repeat("x", 8_000) + `"}`),
		Meta:              json.RawMessage(`{"private":true}`),
	}

	got, ref, bounded := finalizeGenericToolResult(sessionDir, "call-structured", raw, 1_000)
	if !bounded || ref == "" || got.ModelText == nil || len(got.Content) != 0 {
		t.Fatalf("structured-only result was not bounded: bounded=%v ref=%q result=%+v", bounded, ref, got)
	}
	if !strings.Contains(got.TextProjection(), ref) || estimateResultTokens(got.TextProjection()) > 1_000 {
		t.Fatalf("structured-only provider projection is not bounded: %.300q", got.TextProjection())
	}
	var index map[string]any
	if err := json.Unmarshal([]byte(got.TextProjection()), &index); err != nil {
		t.Fatalf("structured projection must remain valid JSON: %v", err)
	}
	if !strings.HasPrefix(index["content"].(string), `{"payload":`) {
		t.Fatalf("structured projection lacks first-page evidence: %+v", index)
	}
	if string(got.StructuredContent) != string(raw.StructuredContent) || string(got.Meta) != string(raw.Meta) {
		t.Fatal("structured-only metadata was not retained")
	}
}

func TestFinalizeGenericToolResultBoundsFragmentedText(t *testing.T) {
	raw := toolresult.FromText(strings.Repeat("x\n", 10_000))
	got, ref, bounded := finalizeGenericToolResult(t.TempDir(), "call-lines", raw, defaultProjectionTokenBudget)
	if !bounded || ref == "" || got.TextProjection() == raw.TextProjection() {
		t.Fatal("line-heavy result should cross the generic settlement boundary")
	}
}

func TestFinalizeGenericToolResultFailsOpenWithoutArtifactStorage(t *testing.T) {
	raw := toolresult.Result{
		Content: []toolresult.ContentPart{
			{Type: toolresult.ContentTypeText, Text: strings.Repeat("x", 8_000)},
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
			got, ref, bounded := finalizeGenericToolResult(t.TempDir(), "structured", raw, defaultProjectionTokenBudget)
			if !bounded || ref == "" {
				t.Fatal("oversized structured result was not archived")
			}
			if size := estimateResultTokens(got.TextProjection()); size > defaultProjectionTokenBudget {
				t.Errorf("archive page exceeds budget: %d tokens", size)
			}
			if providers.ProjectToolResult(got).ToolText != got.TextProjection() {
				t.Error("provider projection changed the settled page")
			}
			if string(got.StructuredContent) != string(encoded) || string(got.Meta) != string(raw.Meta) {
				t.Fatal("projection changed durable structured data")
			}
			archived, err := os.ReadFile(ref)
			if err != nil || string(archived) != raw.TextProjection() {
				t.Fatalf("archive did not preserve the complete result: %v", err)
			}
			index := parseOut(t, got.TextProjection())
			content := index["content"].(string)
			if content == "" || !strings.HasPrefix(string(archived), content) {
				t.Fatal("archive page is not a continuous prefix")
			}
			next := index["continuation"].(map[string]any)["next"].(map[string]any)
			cursor, err := decodeReadFileContinuation(next["continuation"].(string))
			if err != nil || cursor.Path != ref || cursor.ExpectedSHA256 != sha256Hex(archived) || cursor.ByteOffset == nil || *cursor.ByteOffset != len(content) || cursor.ByteEndOffset == nil || *cursor.ByteEndOffset != len(archived) {
				t.Fatalf("archive recovery does not cover the original result: %+v, err=%v", cursor, err)
			}
		})
	}
}
