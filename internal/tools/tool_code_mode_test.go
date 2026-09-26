package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func newCodeModeTestToolkit(t *testing.T) *Toolkit {
	t.Helper()
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The portable execution fixture opts out of OS confinement; sandbox tests cover confinement separately.
	kit.SetBoundary(UnconfinedBoundary())
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	service := codemode.NewService(codemode.ServiceConfig{})
	t.Cleanup(func() { _ = service.Close() })
	kit.ConfigurePTC(service, config.PTCConfig{Enabled: true})
	return kit
}
func codeModeDefsToProviderDefs(defs []codemode.ToolDefinition) []providers.ToolDefinition {
	var out []providers.ToolDefinition
	for _, d := range defs {
		out = append(out, providers.ToolDefinition{Name: d.Name})
	}
	return out
}
func TestPTCFamilySwitchAndExecutionBoundary(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	kit.ConfigurePTC(kit.CodeModeService(), config.PTCConfig{Enabled: true, Families: map[string]bool{"claude": false}})
	if !contains("run_code", kit.Definitions()) || contains("read_file", kit.Definitions()) {
		t.Fatal("PTC surface is not collapsed")
	}
	if _, err := kit.ExecuteResult(context.Background(), providers.ToolCall{Name: "read_file", Arguments: `{"path":"README.md"}`}); err == nil {
		t.Fatal("model-direct leaf call bypassed PTC")
	}
	kit.ConfigureSurfaceForProviderModel("anthropic", "claude-sonnet-4", true)
	if contains("run_code", kit.Definitions()) || !contains("read_file", kit.Definitions()) {
		t.Fatal("family disabled override ignored")
	}
	clone, err := kit.CloneForRoot(kit.RootDir())
	if err != nil {
		t.Fatal(err)
	}
	clone.ConfigureSurfaceForProviderModel("deepseek", "deepseek-v4", true)
	if !contains("run_code", clone.Definitions()) || contains("run_code", kit.Definitions()) {
		t.Fatal("thread family selection leaked")
	}
}
func TestPTCNestedReadThroughRealNode(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	if err := os.WriteFile(filepath.Join(kit.RootDir(), "fixture.txt"), []byte("PTC_READ_OK"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: kit, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer runtime.Cancel()
	args, _ := json.Marshal(map[string]any{"code": `const result = await tools.read_file({path:"fixture.txt"}); console.log(result.content[0].text);`, "description": "Read directory"})
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "outer", Name: "run_code", Arguments: string(args)}}, nil)
	if err != nil || len(messages) != 1 || !strings.Contains(messages[0].Content, "PTC_READ_OK") {
		t.Fatalf("nested execution: %+v %v", messages, err)
	}
}

func contains(name string, defs []providers.ToolDefinition) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}
