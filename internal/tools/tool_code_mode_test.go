package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
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

func runPTCProgram(t *testing.T, kit *Toolkit, code string) toolresult.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: kit, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer runtime.Cancel()
	args, err := json.Marshal(map[string]any{"code": code, "description": "Exercise PTC execution"})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "program", Name: "run_code", Arguments: string(args)}}, nil)
	if err != nil || len(messages) != 1 || messages[0].ToolResult == nil {
		t.Fatalf("program execution: %+v %v", messages, err)
	}
	return *messages[0].ToolResult
}

func TestPTCProgramReviewBeforeNestedEffects(t *testing.T) {
	// Denial and missing-reviewer cases must stop before execution. An allow
	// must execute the program without exempting nested commands from review.
	for _, tc := range []struct {
		name, outcome, code string
		wantCalls           int
		wantError           bool
	}{
		{"denied", approvefor.OutcomeDeny, `await tools.bash({command: 'printf done > marker.txt'})`, 1, true},
		{"missing reviewer", "", `await tools.bash({command: 'printf done > marker.txt'})`, 0, true},
		{"allowed nested", approvefor.OutcomeAllow, `await tools.bash({command: 'printf done > marker.txt'})`, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.wantError && !processsandbox.Supported() {
				t.Skip("successful standard-mode execution requires a filesystem sandbox")
			}
			kit := newCodeModeTestToolkit(t)
			kit.SetBoundary(StandardBoundary())
			kit.SetPermissionMode("standard")
			kit.SetApproveForMe(true)
			reviewer := &recordingReviewer{decision: approvefor.Decision{Outcome: tc.outcome, Reason: "PTC review fixture"}}
			if tc.outcome != "" {
				kit.SetReviewer(reviewer)
			}
			result := runPTCProgram(t, kit, tc.code)
			if result.IsError != tc.wantError || len(reviewer.requests) != tc.wantCalls {
				t.Fatalf("review calls=%d result=%+v", len(reviewer.requests), result)
			}
			if tc.wantError {
				if !strings.Contains(result.TextProjection(), "blocked by approve for me") {
					t.Fatalf("program failed outside the review gate: %s", result.TextProjection())
				}
				if _, err := os.Stat(filepath.Join(kit.RootDir(), "marker.txt")); !os.IsNotExist(err) {
					t.Fatalf("unapproved program had filesystem effects: %v", err)
				}
				return
			}
			var reviewed struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal([]byte(reviewer.requests[0].Arguments), &reviewed); err != nil {
				t.Fatal(err)
			}
			if reviewer.requests[0].Tool.Name != "run_code" || reviewed.Code != tc.code {
				t.Fatalf("reviewer did not receive the program: %+v", reviewer.requests[0])
			}
			data, err := os.ReadFile(filepath.Join(kit.RootDir(), "marker.txt"))
			if err != nil || string(data) != "done" {
				t.Fatalf("approved program did not complete: %q %v", data, err)
			}
		})
	}
}

func TestPTCReadOnlyProgramKeepsSandbox(t *testing.T) {
	if !processsandbox.Supported() {
		t.Skip("read-only execution requires a filesystem sandbox")
	}
	kit := newCodeModeTestToolkit(t)
	kit.SetBoundary(ReadOnlyBoundary())
	kit.SetPermissionMode("read_only")
	kit.SetApproveForMe(true)
	if err := os.WriteFile(filepath.Join(kit.RootDir(), "input.txt"), []byte("PTC_READ_ONLY"), 0600); err != nil {
		t.Fatal(err)
	}
	result := runPTCProgram(t, kit, `console.log((await tools.read_file({path:'input.txt'})).content[0].text);
await tools.bash({command:'printf forbidden > marker.txt'});`)
	if !result.IsError || !strings.Contains(result.TextProjection(), "PTC_READ_ONLY") {
		t.Fatalf("read-only program failed to read or was allowed to write: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(kit.RootDir(), "marker.txt")); !os.IsNotExist(err) {
		t.Fatalf("read-only program wrote a file: %v", err)
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

func TestPTCDiscoveryThenExactInvocation(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	if err := os.WriteFile(filepath.Join(kit.RootDir(), "fixture.txt"), []byte("DISCOVERED_READ_OK"), 0600); err != nil {
		t.Fatal(err)
	}
	result := runPTCProgram(t, kit, `const page = await searchTools('read_file');
const name = page.tools.find(t => t.name === 'read_file').name;
const detail = await describeTool(name);
if (!detail.input_schema.properties.path) throw new Error('missing schema');
return (await tools[name]({path:'fixture.txt'})).content[0].text;`)
	if result.IsError || !strings.Contains(result.TextProjection(), "DISCOVERED_READ_OK") {
		t.Fatalf("discovery and invocation failed: %+v", result)
	}
}
