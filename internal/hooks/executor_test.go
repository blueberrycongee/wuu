package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// stubExecutor is a fake ToolExecutor for testing.
type stubExecutor struct {
	result string
	err    error
	calls  []providers.ToolCall
	defs   []providers.ToolDefinition
}

func (s *stubExecutor) Definitions() []providers.ToolDefinition { return s.defs }
func (s *stubExecutor) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	s.calls = append(s.calls, call)
	return s.result, s.err
}

type supportStubExecutor struct {
	stubExecutor
	supported map[string]bool
}

func (s *supportStubExecutor) SupportsTool(name string) bool {
	return s.supported[name]
}

type discoveryStubExecutor struct {
	stubExecutor
	discovered     []providers.LoadableToolDefinition
	discoveryCalls []providers.ToolCall
}

type richStubExecutor struct {
	stubExecutor
	result toolresult.Result
}

type displayStubExecutor struct{ stubExecutor }

func (s *displayStubExecutor) ToolDisplay(call providers.ToolCall) (providers.ToolCallDisplay, bool) {
	return providers.ToolCallDisplay{Text: "display " + call.Name}, true
}

func (s *richStubExecutor) ExecuteResult(_ context.Context, call providers.ToolCall) (toolresult.Result, error) {
	s.calls = append(s.calls, call)
	return s.result.Clone(), s.err
}

func (s *discoveryStubExecutor) DiscoveredTools(call providers.ToolCall) []providers.LoadableToolDefinition {
	s.discoveryCalls = append(s.discoveryCalls, call)
	return s.discovered
}

func TestHookedExecutorForwardsToolDisplay(t *testing.T) {
	exec := NewHookedExecutor(&displayStubExecutor{}, NewDispatcher(NewRegistry(nil)), "sess-1", "/tmp")
	display, ok := exec.ToolDisplay(providers.ToolCall{Name: "plugin_tool"})
	if !ok || display.Text != "display plugin_tool" {
		t.Fatalf("display = %+v ok = %v", display, ok)
	}
}

func TestHookedExecutorPreservesRichResultAndSendsStableProjectionToHooks(t *testing.T) {
	inputPath := t.TempDir() + "/hook-input.json"
	result := toolresult.Result{
		Content: []toolresult.ContentPart{
			{Type: "text", Text: "visible"},
			{Type: "image", Data: "aW1hZ2U=", MIMEType: "image/png"},
		},
		StructuredContent: json.RawMessage(`{"answer":42}`),
		Meta:              json.RawMessage(`{"private":"kept"}`),
		Activity:          &toolresult.ActivityRef{ID: "activity-1", Kind: "browser"},
	}
	inner := &richStubExecutor{result: result}
	r := NewRegistry(map[Event][]HookConfig{
		PostToolUse: {{Matcher: "*", Command: fmt.Sprintf("cat > %q", inputPath)}},
	})
	exec := NewHookedExecutor(inner, NewDispatcher(r), "sess-1", "/tmp")

	got, err := exec.ExecuteResult(context.Background(), providers.ToolCall{Name: "browser_observe", Arguments: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if got.JSONProjection() != result.JSONProjection() {
		t.Fatalf("rich result changed across hooks:\ngot  %s\nwant %s", got.JSONProjection(), result.JSONProjection())
	}
	data, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read hook input: %v", err)
	}
	var input Input
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatalf("parse hook input: %v", err)
	}
	if input.ToolResponse != result.HookProjection() {
		t.Fatalf("hook projection = %q, want %q", input.ToolResponse, result.HookProjection())
	}
}

func TestHookedExecutor_SupportsToolDelegates(t *testing.T) {
	inner := &supportStubExecutor{supported: map[string]bool{"deferred_tool": true}}
	exec := NewHookedExecutor(inner, NewDispatcher(nil), "", "")

	if !exec.SupportsTool("deferred_tool") {
		t.Fatal("expected hooked executor to preserve inner deferred tool support")
	}
	if exec.SupportsTool("missing") {
		t.Fatal("missing tool should not be supported")
	}
}

func TestHookedExecutor_SupportsToolFallsBackToDefinitions(t *testing.T) {
	inner := &stubExecutor{defs: []providers.ToolDefinition{{Name: "read_file"}}}
	exec := NewHookedExecutor(inner, NewDispatcher(nil), "", "")

	if !exec.SupportsTool("READ_FILE") {
		t.Fatal("expected hooked executor to support direct definition names")
	}
	if exec.SupportsTool("deferred_tool") {
		t.Fatal("unlisted tool should not be supported without inner support provider")
	}
}

func TestHookedExecutor_DiscoveredToolsDelegates(t *testing.T) {
	inner := &discoveryStubExecutor{
		discovered: []providers.LoadableToolDefinition{{
			Name:        "await_agents",
			Description: "Wait for running agents.",
			InputSchema: map[string]any{"type": "object"},
		}},
	}
	exec := NewHookedExecutor(inner, NewDispatcher(nil), "", "")

	call := providers.ToolCall{ID: "call-1", Name: "spawn_agent"}
	discovered := exec.DiscoveredTools(call)
	if len(discovered) != 1 || discovered[0].Name != "await_agents" {
		t.Fatalf("expected discovered await_agents to be forwarded, got %#v", discovered)
	}
	if len(inner.discoveryCalls) != 1 || inner.discoveryCalls[0].ID != "call-1" {
		t.Fatalf("expected discovery call to be forwarded, got %#v", inner.discoveryCalls)
	}

	discovered[0].Name = "mutated"
	if inner.discovered[0].Name != "await_agents" {
		t.Fatal("expected forwarded discovered tools to be cloned")
	}
}
