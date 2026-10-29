package tools

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
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

type slowObservationReviewer struct{ calls int }

func (r *slowObservationReviewer) Review(ctx context.Context, _ approvefor.Request) (approvefor.Decision, error) {
	r.calls++
	timer := time.NewTimer(5100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return approvefor.Decision{Outcome: approvefor.OutcomeAllow}, nil
	case <-ctx.Done():
		return approvefor.Decision{}, ctx.Err()
	}
}
func TestObservationReauthorizationAllowsSlowReviewer(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	kit, err := New(t.TempDir())
	require.NoError(t, err)
	kit.SetPermissionMode("standard")
	kit.SetApproveForMe(true)
	reviewer := &slowObservationReviewer{}
	kit.SetReviewer(reviewer)
	id := toolctx.SettlementIdentity{OperationID: "slow-review", WorkerInstance: "worker", Actor: "actor", EnvironmentIdentity: "env"}
	call := providers.ToolCall{Name: "plugin_read", Arguments: `{"path":"file.txt"}`}
	require.NoError(t, toolctx.RegisterObservationAuthorization(id, kit, func(ctx context.Context) error {
		return kit.AuthorizeTool(ctx, call, agent.ToolMetadata{ReadOnly: true, Risk: "high"})
	}))
	defer toolctx.ForgetObservationAuthorization(id, kit)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	require.NoError(t, toolctx.AuthorizeObservation(ctx, id, kit))
	require.Equal(t, 1, reviewer.calls)
}
