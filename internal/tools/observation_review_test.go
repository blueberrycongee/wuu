package tools

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestObservationReauthorizationRunsCurrentHooksAndReview(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	kit, err := New(t.TempDir())
	require.NoError(t, err)
	kit.SetPermissionMode("standard")
	kit.SetApproveForMe(true)
	reviewer := &recordingReviewer{decision: approvefor.Decision{Outcome: approvefor.OutcomeAllow}}
	kit.SetReviewer(reviewer)
	hookCalls := 0
	kit.SetPermissionRequestHook(func(context.Context, *Toolkit, ToolInfo, providers.ToolCall) error { hookCalls++; return nil })
	call := providers.ToolCall{Name: "plugin_read", ID: "read", Arguments: `{"path":"file.txt"}`}
	low := agent.ToolMetadata{ReadOnly: true, Risk: "low"}
	high := agent.ToolMetadata{ReadOnly: true, Risk: "high"}
	id := toolctx.SettlementIdentity{OperationID: "review-test", WorkerInstance: "worker", Actor: "actor", EnvironmentIdentity: "env"}
	meta := low
	require.NoError(t, toolctx.RegisterObservationAuthorization(id, kit, func(ctx context.Context) error { return kit.AuthorizeTool(ctx, call, meta) }))
	defer toolctx.ForgetObservationAuthorization(id, kit)
	require.NoError(t, toolctx.AuthorizeObservation(context.Background(), id, kit))
	require.NoError(t, toolctx.AuthorizeObservation(context.Background(), id, kit))
	require.Equal(t, 2, hookCalls)
	require.Empty(t, reviewer.requests)
	meta = high
	require.NoError(t, toolctx.AuthorizeObservation(context.Background(), id, kit))
	require.Len(t, reviewer.requests, 1)
	reviewer.decision = approvefor.Decision{Outcome: approvefor.OutcomeDeny}
	require.Error(t, toolctx.AuthorizeObservation(context.Background(), id, kit))
	require.Len(t, reviewer.requests, 2)
	require.Equal(t, 4, hookCalls)
}
