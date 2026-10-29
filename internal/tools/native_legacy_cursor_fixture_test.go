package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// Generates a legacy line cursor from the native reader and its historical
// projector, for cross-generation process acceptance. The current normal
// settlement uses archive byte cursors instead.
func TestNativeLegacyCursorFixture(t *testing.T) {
	fixture := os.Getenv("WUU_EXTENSION_READ_FIXTURE")
	if fixture == "" {
		t.Skip("run extension-read e2e")
	}
	root := filepath.Join(fixture, "legacy-workspace")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "legacy.txt"), []byte(strings.Repeat("LEGACY_LINE evidence and content\n", 3000)), 0600); err != nil {
		t.Fatal(err)
	}
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{ID: "legacy-source", Name: "read_file", Arguments: `{"path":"legacy.txt"}`})
	if err != nil {
		t.Fatal(err)
	}
	projected, _, ok := projectReadFileResult(result.Content[0].Text, projectorContext{CallID: "legacy", BudgetTokens: 1000, ArtifactRef: "legacy-evidence"})
	if !ok {
		t.Fatal("legacy native projector declined")
	}
	var page struct {
		Continuation struct {
			Next json.RawMessage `json:"next"`
		} `json:"continuation"`
	}
	if err := json.Unmarshal([]byte(projected), &page); err != nil || len(page.Continuation.Next) == 0 {
		t.Fatalf("native legacy cursor missing: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "legacy_cursor.json"), page.Continuation.Next, 0600); err != nil {
		t.Fatal(err)
	}
}
