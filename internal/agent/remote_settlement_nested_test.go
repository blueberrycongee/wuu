package agent

import (
	"context"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/stretchr/testify/require"
)

func TestSettlementRejectsParentWithUnfinishedNestedCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	childDone := make(chan struct{})
	executor := orchestrationTestTools{run: func(ctx context.Context, call providers.ToolCall) (string, error) {
		if call.Name != "orchestrate" {
			close(entered)
			<-release
			return "child", nil
		}
		if err := toolctx.RegisterSettlement(ctx, func(context.Context) error { return nil }, func() {}); err != nil {
			return "", err
		}
		scope, ok := toolctx.Nested(ctx)
		if !ok {
			return "", context.Canceled
		}
		go func() {
			defer close(childDone)
			_, _ = scope.Invoke(ctx, providers.ToolCall{ID: "child", Name: "read", Arguments: `{}`})
		}()
		<-entered
		return "premature parent success", nil
	}}
	ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "owner")
	require.NoError(t, err)
	run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Ledger: ledger, OperationID: "turn"})
	defer run.Close()
	defer func() { close(release); <-childDone }()
	messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "parent", Name: "orchestrate", Arguments: `{}`}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.True(t, messages[0].ToolResult.IsError)
	require.Contains(t, messages[0].ToolResult.TextProjection(), "pending remote read output was discarded")
	require.NotContains(t, messages[0].ToolResult.TextProjection(), "premature parent success")
}

func TestSettlementLateChildAbortsWithoutChangingParentSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered, release, aborted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	committed := make(chan struct{}, 1)
	executor := orchestrationTestTools{run: func(ctx context.Context, call providers.ToolCall) (string, error) {
		if call.Name != "orchestrate" {
			close(entered)
			<-release
			err := toolctx.RegisterSettlement(ctx, func(context.Context) error { committed <- struct{}{}; return nil }, func() { close(aborted) })
			return "child", err
		}
		scope, ok := toolctx.Nested(ctx)
		if !ok {
			return "", context.Canceled
		}
		go func() { _, _ = scope.Invoke(ctx, providers.ToolCall{ID: "child", Name: "read", Arguments: `{}`}) }()
		<-entered
		return "parent success", nil
	}}
	ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "owner")
	require.NoError(t, err)
	run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Ledger: ledger, OperationID: "turn"})
	defer run.Close()
	messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "parent", Name: "orchestrate", Arguments: `{}`}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.False(t, messages[0].ToolResult.IsError)
	require.Contains(t, messages[0].ToolResult.TextProjection(), "parent success")
	close(release)
	select {
	case <-aborted:
	case <-ctx.Done():
		t.Fatal("late child did not abort")
	}
	select {
	case <-committed:
		t.Fatal("late child committed")
	default:
	}
}
