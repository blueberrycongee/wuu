package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestExternalReadPreservesStructuredTextWithoutNativeObservation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("native file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := kit.PrepareExternalRead(context.Background(), `{"path":"note.txt","offset":1,"limit":2}`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Render(toolresult.Result{StructuredContent: json.RawMessage(`{"text":"external\r\nsecond\n","total_lines":3}`)})
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(result.StructuredContent, &data); err != nil {
		t.Fatal(err)
	}
	if data["text"] != "external\r\nsecond\n" || data["source"] != "extension" || data["content_verified"] != false {
		t.Fatalf("unexpected structured data: %+v", data)
	}
	var view map[string]any
	if err := json.Unmarshal([]byte(result.TextProjection()), &view); err != nil {
		t.Fatal(err)
	}
	if view["source"] != "extension" || view["content_verified"] != false || !strings.Contains(view["content"].(string), "1|external\n|second\n") {
		t.Fatalf("unexpected model view: %+v", view)
	}
	if len(kit.env.ReadEntries()) != 0 {
		t.Fatal("unverified content recorded as native read")
	}
}
func TestExternalReadRejectsInvalidScopeAndResult(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("real"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{`{"path":"../outside"}`, `{"path":".env"}`, `{"continuation":"plugin-cursor"}`} {
		if _, err := kit.PrepareExternalRead(context.Background(), args); err == nil {
			t.Fatalf("accepted unsafe args %s", args)
		}
	}
	scope, err := kit.PrepareExternalRead(context.Background(), `{"path":"file.txt","limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{}`, `{"text":"a\nb","total_lines":1}`, `{"text":"a","total_lines":0}`, `{"text":"a","total_lines":1,"continuation":"invented"}`} {
		if _, err := scope.Render(toolresult.Result{StructuredContent: json.RawMessage(data)}); err == nil {
			t.Fatalf("accepted invalid result %s", data)
		}
	}
	image := toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: "aW1hZ2U="}}}
	if _, err := scope.Render(image); err == nil {
		t.Fatal("accepted unsupported image")
	}
}

func TestExternalReadPreservesCRWithoutTrailingNewline(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := kit.PrepareExternalRead(context.Background(), `{"path":"file.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Render(toolresult.Result{StructuredContent: json.RawMessage(`{"text":"last\r","total_lines":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	var view map[string]any
	if err := json.Unmarshal([]byte(result.TextProjection()), &view); err != nil {
		t.Fatal(err)
	}
	if view["content"] != "1|last\r\n" {
		t.Fatalf("standalone CR lost: %q", view["content"])
	}
}

func TestExternalReadRejectsImagesRemoteAndCanceledCalls(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "picture"), readImagePNG(t, 2, 2), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("text"), 0600); err != nil {
		t.Fatal(err)
	}
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kit.PrepareExternalRead(context.Background(), `{"path":"picture"}`); err == nil {
		t.Fatal("image accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := kit.PrepareExternalRead(canceled, `{"path":"file.txt"}`); err == nil {
		t.Fatal("canceled call accepted")
	}
	kit.SetExecutionEnvironment(&externalReadRemote{})
	if _, err := kit.PrepareExternalRead(context.Background(), `{"path":"file.txt"}`); err == nil {
		t.Fatal("remote environment accepted")
	}
}

type externalReadRemote struct{}

func (*externalReadRemote) Execute(context.Context, executionenv.ToolRequest) (toolresult.Result, error) {
	panic("must not execute")
}

func TestExternalReadDoesNotClaimPriorNativeObservation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kit.ExecuteResult(context.Background(), providers.ToolCall{Name: "read_file", Arguments: `{"path":"file.txt"}`}); err != nil {
		t.Fatal(err)
	}
	if len(kit.env.ReadEntries()) != 1 {
		t.Fatal("native observation missing")
	}
	scope, err := kit.PrepareExternalRead(context.Background(), `{"path":"file.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scope.Render(toolresult.Result{StructuredContent: json.RawMessage(`{"text":"bad","total_lines":0}`)}); err == nil {
		t.Fatal("bad result accepted")
	}
	if len(kit.env.ReadEntries()) != 1 {
		t.Fatal("failed extension removed native observation")
	}
	if _, err := scope.Render(toolresult.Result{StructuredContent: json.RawMessage(`{"text":"independent\n","total_lines":1}`)}); err != nil {
		t.Fatal(err)
	}
	if len(kit.env.ReadEntries()) != 0 {
		t.Fatal("previous native observation misattributed to extension result")
	}
}

func TestExternalReadPreservesIdenticalNewNativeObservation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	call := providers.ToolCall{Name: "read_file", Arguments: `{"path":"file.txt"}`}
	if _, err := kit.ExecuteResult(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	scope, err := kit.PrepareExternalRead(context.Background(), call.Arguments)
	if err != nil {
		t.Fatal(err)
	}
	// This read represents a newer observation even though all its public
	// fields are identical to the one captured by the extension call.
	if _, err := kit.ExecuteResult(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	if _, err := scope.Render(toolresult.Result{StructuredContent: json.RawMessage(`{"text":"extension\n","total_lines":1}`)}); err != nil {
		t.Fatal(err)
	}
	if len(kit.env.ReadEntries()) != 1 {
		t.Fatal("newer identical native observation was removed")
	}
}

func TestReadObservationRevisionSurvivesDeletionAndSaturation(t *testing.T) {
	var state readFileState
	entry := ReadFileEntry{Size: 1}
	state.record("file", entry)
	previous := state.observationSnapshot()["file"]
	state.delete("file")
	state.record("file", entry)
	state.deleteIfUnchanged("file", previous)
	if !state.hasBeenRead("file") {
		t.Fatal("a deleted and re-recorded observation was removed")
	}
	state.revision = ^uint64(0)
	state.record("file", entry)
	saturated := state.observationSnapshot()["file"]
	state.record("file", entry)
	state.deleteIfUnchanged("file", saturated)
	if !state.hasBeenRead("file") {
		t.Fatal("a saturated revision must not delete a newer observation")
	}
}

func TestNativeRecoverySurvivesUnavailableExternalReader(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionDir(t.TempDir())
	settled := kit.FinalizeToolResult(providers.ToolCall{Name: "fixture", ID: "archive"}, toolresult.FromText(strings.Repeat("evidence\n", 20000)))
	var first map[string]any
	if err := json.Unmarshal([]byte(settled.TextProjection()), &first); err != nil {
		t.Fatal(err)
	}
	cursor := first["continuation"].(map[string]any)["next"].(map[string]any)["continuation"].(string)
	available := false
	if err := kit.SetNativeToolReplacements(map[string]func(Tool) Tool{"read_file": func(original Tool) Tool { return availableReplacement{Tool: original, available: &available} }}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"continuation": cursor})
	if _, err := kit.ExecuteResult(context.Background(), providers.ToolCall{Name: "recover_tool_result", ID: "recover", Arguments: string(raw)}); err != nil {
		t.Fatalf("native recovery was coupled to unavailable reader: %v", err)
	}
}

type retiredExternalReader struct{ Tool }

func (r retiredExternalReader) ToolAvailable() bool { return false }
func (r retiredExternalReader) NativeToolForCall(raw string) (Tool, bool) {
	var args struct {
		Continuation string `json:"continuation"`
	}
	if json.Unmarshal([]byte(raw), &args) == nil && args.Continuation != "" {
		return r.Tool, true
	}
	return nil, false
}
func TestRetiredExternalReaderExposesOnlyLegacyContinuationToPTC(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	if err := kit.SetNativeToolReplacements(map[string]func(Tool) Tool{"read_file": func(native Tool) Tool { return retiredExternalReader{native} }}); err != nil {
		t.Fatal(err)
	}
	nested, err := kit.CodeModeNestedSurface()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range nested {
		if definition.Name == "read_file" {
			var schema map[string]any
			if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			props := schema["properties"].(map[string]any)
			if len(props) != 1 || props["continuation"] == nil {
				t.Fatalf("unexpected recovery schema: %s", definition.InputSchema)
			}
			if !strings.Contains(definition.Description, "continuation") {
				t.Fatal("missing recovery-only description")
			}
			return
		}
	}
	t.Fatal("legacy continuation binding was not exposed")
}
