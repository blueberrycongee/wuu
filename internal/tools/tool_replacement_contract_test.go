package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestReplacementResultDoesNotUseNativeEnvelopeProjection(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := kit.SetNativeToolReplacements(map[string]func(Tool) Tool{"bash": func(original Tool) Tool { return original }}); err != nil {
		t.Fatal(err)
	}
	raw := bashEnvelope(map[string]any{"stdout_tail": "hello\n", "stderr_tail": ""})
	result := toolresult.FromText(raw)
	result.StructuredContent = json.RawMessage(`{"value":"kept"}`)
	output := kit.FinalizeToolResult(providers.ToolCall{Name: "bash", ID: "one"}, result)
	if !strings.Contains(output.TextProjection(), raw) {
		t.Fatalf("replacement projected as native: %s", output.TextProjection())
	}
}

type availableReplacement struct {
	Tool
	available *bool
}

func (r availableReplacement) ToolAvailable() bool { return *r.available }

func TestReplacementSurfacePinsAvailabilityDuringFreeze(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	available := true
	if err := kit.SetNativeToolReplacements(map[string]func(Tool) Tool{"read_file": func(original Tool) Tool { return availableReplacement{Tool: original, available: &available} }}); err != nil {
		t.Fatal(err)
	}
	has := func() bool {
		for _, d := range kit.Definitions() {
			if d.Name == "read_file" {
				return true
			}
		}
		return false
	}
	if !has() {
		t.Fatal("initial tool missing")
	}
	kit.FreezeToolSurface()
	available = false
	if !has() {
		t.Fatal("frozen surface changed")
	}
	kit.UnfreezeToolSurface()
	if has() {
		t.Fatal("retired tool still advertised after unfreeze")
	}
}

type incompatibleReplacement struct{ Tool }

func (r incompatibleReplacement) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: r.Name(), InputSchema: map[string]any{"type": "object", "required": []string{"new_field"}}}
}
func TestReplacementRejectsIncompatibleWireSchema(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = kit.SetNativeToolReplacements(map[string]func(Tool) Tool{"read_file": func(original Tool) Tool { return incompatibleReplacement{original} }})
	if err == nil {
		t.Fatal("incompatible replacement schema accepted")
	}
}

func TestReplacementAvailabilityUpdatesPromptSurface(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	available := true
	if err := kit.SetNativeToolReplacements(map[string]func(Tool) Tool{"web_search": func(original Tool) Tool { return availableReplacement{Tool: original, available: &available} }}); err != nil {
		t.Fatal(err)
	}
	if _, found := kit.ActiveSurface().Tools["web_search"]; !found {
		t.Fatal("available tool missing from prompt surface")
	}
	available = false
	if _, found := kit.ActiveSurface().Tools["web_search"]; found {
		t.Fatal("retired tool remains in prompt surface")
	}
}
