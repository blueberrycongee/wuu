package agent

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/stretchr/testify/require"
)

type invalidFinalizerSettlementTools struct{ transaction, nested bool }

func (e invalidFinalizerSettlementTools) Definitions() []providers.ToolDefinition { return nil }
func (e invalidFinalizerSettlementTools) ToolMetadata(call providers.ToolCall) (ToolMetadata, bool) {
	return ToolMetadata{Orchestrator: call.Name == "parent"}, true
}
func (e invalidFinalizerSettlementTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	if call.Name == "parent" {
		scope, ok := toolctx.Nested(ctx)
		if !ok {
			return "", context.Canceled
		}
		child, err := scope.Invoke(ctx, providers.ToolCall{ID: "child", Name: "child", Arguments: `{}`})
		if err != nil {
			return "", err
		}
		return child.TextProjection(), nil
	}
	if e.transaction {
		if err := toolctx.RegisterSettlement(ctx, func(context.Context) error { return nil }, func() {}); err != nil {
			return "", err
		}
	}
	return "valid producer", nil
}
func (e invalidFinalizerSettlementTools) FinalizeToolOutcome(call providers.ToolCall, result toolresult.Result) ToolFinalization {
	if call.Name == "parent" {
		return ToolFinalization{Result: result}
	}
	return ToolFinalization{Result: toolresult.Result{Content: []toolresult.ContentPart{{Type: "invalid"}}}, AdditionalContext: "invalid-metadata", DiscoveredTools: []providers.LoadableToolDefinition{{Name: "invalid-discovery"}}}
}
func TestInvalidFinalizerDoesNotLeakTransactionMetadata(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "nested"}[nested], func(t *testing.T) {
			ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "owner")
			require.NoError(t, err)
			run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: invalidFinalizerSettlementTools{transaction: true, nested: nested}, Ledger: ledger, OperationID: "turn"})
			defer run.Close()
			name := "child"
			if nested {
				name = "parent"
			}
			_, err = run.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "call", Name: name, Arguments: `{}`}}, nil)
			require.NoError(t, err)
			for _, item := range run.runs {
				if item.call.Name == "child" {
					require.Empty(t, item.discoveredTools)
					require.Nil(t, item.requestContext)
				}
			}
			require.Empty(t, run.TakeRequestContextSegments())
		})
	}
}
func TestInvalidFinalizerWithoutTransactionPreservesLegacy(t *testing.T) {
	for _, withLedger := range []bool{false, true} {
		t.Run(map[bool]string{false: "without_ledger", true: "with_ledger"}[withLedger], func(t *testing.T) {
			var ledger *toolledger.Ledger
			if withLedger {
				var err error
				ledger, err = toolledger.New(agentTestDocuments(t, t.TempDir()), "owner")
				require.NoError(t, err)
			}
			run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: invalidFinalizerSettlementTools{}, Ledger: ledger, OperationID: "turn"})
			defer run.Close()
			messages, err := run.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "call", Name: "child", Arguments: `{}`}}, nil)
			if withLedger {
				require.ErrorContains(t, err, "settle tool invocation")
			} else {
				require.NoError(t, err)
				require.Len(t, messages, 1)
				require.Equal(t, "invalid", messages[0].ToolResult.Content[0].Type)
			}
		})
	}
}

func TestNestedInvalidFinalizerWithoutTransactionPreservesLegacy(t *testing.T) {
	for _, withLedger := range []bool{false, true} {
		t.Run(map[bool]string{false: "without_ledger", true: "with_ledger"}[withLedger], func(t *testing.T) {
			var ledger *toolledger.Ledger
			if withLedger {
				var err error
				ledger, err = toolledger.New(agentTestDocuments(t, t.TempDir()), "owner")
				require.NoError(t, err)
			}
			run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: invalidFinalizerSettlementTools{}, Ledger: ledger, OperationID: "turn"})
			defer run.Close()
			_, err := run.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "call", Name: "parent", Arguments: `{}`}}, nil)
			require.NoError(t, err)
			var child *toolRun
			for _, item := range run.runs {
				if item.call.Name == "child" {
					child = item
				}
			}
			require.NotNil(t, child)
			if withLedger {
				require.ErrorContains(t, child.err, "settle tool invocation")
			} else {
				require.NoError(t, child.err)
				require.Equal(t, "invalid", child.result.Content[0].Type)
			}
			require.NotEmpty(t, child.discoveredTools)
			require.NotNil(t, child.requestContext)
		})
	}
}
