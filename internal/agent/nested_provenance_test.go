package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type provenanceFinalizerExecutor struct{}

func (provenanceFinalizerExecutor) Definitions() []providers.ToolDefinition { return nil }
func (provenanceFinalizerExecutor) ToolMetadata(call providers.ToolCall) (ToolMetadata, bool) {
	return ToolMetadata{Orchestrator: call.Name == "outer", ReadOnly: true, ConcurrencySafe: true}, true
}
func (provenanceFinalizerExecutor) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	switch call.Name {
	case "outer":
		nested, _ := toolctx.Nested(ctx)
		_, err := nested.Invoke(ctx, providers.ToolCall{ID: "child", Name: "unverified", Arguments: `{}`})
		return "transformed output", err
	case "unverified":
		toolctx.MarkUnverified(ctx, "extension")
		return "unverified input", nil
	default:
		return "unrelated output", nil
	}
}
func (provenanceFinalizerExecutor) FinalizeToolOutcome(call providers.ToolCall, result toolresult.Result) ToolFinalization {
	// A selected finalizer is allowed to replace its returned envelope.
	if call.Name == "outer" {
		return ToolFinalization{Result: toolresult.FromText("finalizer replaced output")}
	}
	return ToolFinalization{Result: result}
}
func TestNestedProvenanceSurvivesFinalizerAndIsIsolated(t *testing.T) {
	rt := NewTurnToolRuntime(ToolRuntimeConfig{Executor: provenanceFinalizerExecutor{}, Gate: NewToolExecutionGate(2)})
	defer rt.Cancel()
	msgs, err := rt.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "parent", Name: "outer", Arguments: `{}`}, {ID: "clean", Name: "clean", Arguments: `{}`}}, nil)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("calls=%+v err=%v", msgs, err)
	}
	if !strings.Contains(msgs[0].Content, "Unverified tool content") || !strings.Contains(msgs[0].Content, "finalizer replaced output") {
		t.Fatalf("provenance lost after finalizer: %+v", msgs[0])
	}
	if strings.Contains(msgs[1].Content, "Unverified tool content") {
		t.Fatalf("unrelated sibling inherited provenance: %+v", msgs[1])
	}
}
