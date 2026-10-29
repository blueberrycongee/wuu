package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolstate"
	"github.com/stretchr/testify/require"
)

type denyReadOnlyProjection struct{}

type lostProjectionResolution struct {
	persistence.Documents
	fired atomic.Bool
}

func (d *lostProjectionResolution) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	receipt, err := d.Documents.Commit(ctx, tx)
	if err != nil {
		return receipt, err
	}
	for _, w := range tx.Writes {
		var inv toolstate.Invocation
		if w.Namespace == toolstate.Invocations && json.Unmarshal(w.Value, &inv) == nil && inv.Observation != nil && inv.Observation.Outcome == "committed" && d.fired.CompareAndSwap(false, true) {
			return receipt, errors.New("lost final acknowledgement")
		}
	}
	return receipt, nil
}

func (denyReadOnlyProjection) Authorize(_ context.Context, req tools.AuthorizationRequest) (tools.AuthorizationDecision, error) {
	if req.PermissionMode == config.PermissionModeReadOnly {
		return tools.AuthorizationDecision{Outcome: "deny", Reason: "current turn denied"}, nil
	}
	return tools.AuthorizationDecision{Outcome: "allow"}, nil
}
func TestTurnProjectionUsesNewTurnPermissions(t *testing.T) {
	client := &fakeClient{response: providersResponse("should not run")}
	rt := newTestRuntime(t, client)
	kit, err := tools.New(rt.RootDir)
	require.NoError(t, err)
	rt.Toolkit, rt.StreamRunner.Tools = kit, kit
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	dispatchPayload(t, srv, "start", MethodThreadStart, ThreadStartParams{})
	start := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"])
	th := srv.thread(start.Thread.ID)
	thread, err := srv.ensureThreadRuntime(th)
	require.NoError(t, err)
	thread.Toolkit.SetPermissionMode(config.PermissionModeUnconfined)
	thread.Toolkit.SetAuthorizer(denyReadOnlyProjection{})
	ctx := context.Background()
	docs := &lostProjectionResolution{Documents: rt.Documents}
	ledger, err := toolledger.New(docs, start.Thread.ID)
	require.NoError(t, err)
	thread.StreamRunner.ToolLedger = ledger
	batch, err := ledger.BeginBatch(ctx, "earlier", 0)
	require.NoError(t, err)
	inv, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "call", Name: "read_file", Arguments: `{"path":"secret.txt"}`}, toolledger.ReplayAtMostOnce)
	require.NoError(t, err)
	require.NoError(t, ledger.FinalizeBatch(ctx, batch))
	require.NoError(t, ledger.Start(ctx, inv.ID))
	id := toolctx.SettlementIdentity{OperationID: inv.ID, WorkerInstance: "worker", Actor: start.Thread.ID, EnvironmentIdentity: "env"}
	intent := toolledger.ObservationIntent{OperationID: id.OperationID, WorkerInstance: id.WorkerInstance, Actor: id.Actor, EnvironmentIdentity: id.EnvironmentIdentity}
	original := providers.ToolCall{Name: "read_file", Arguments: `{"path":"secret.txt"}`}
	check := func(ctx context.Context) error {
		return thread.Toolkit.AuthorizeTool(ctx, original, agent.ToolMetadata{ReadOnly: true, Risk: "low"})
	}
	require.NoError(t, check(ctx))
	require.NoError(t, toolctx.RegisterObservationAuthorization(id, ledger, check))
	t.Cleanup(func() { toolctx.ForgetObservationAuthorization(id, ledger) })
	require.NoError(t, ledger.StageObservation(ctx, inv.ID, intent, toolresult.FromText("SECRET")))
	require.ErrorContains(t, ledger.ResolveObservation(ctx, inv.ID, intent, true), "lost final acknowledgement")
	require.True(t, docs.fired.Load())
	stored, found, err := rt.Sessions.Find(start.Thread.ID)
	require.NoError(t, err)
	require.True(t, found)
	_, err = rt.Sessions.SetRuntimeSelection(start.Thread.ID, session.RuntimeSelection{Provider: stored.Provider, Model: stored.Model, Variant: stored.Variant, Effort: stored.Effort, Speed: stored.Speed, ModelOptions: stored.ModelOptions, PermissionMode: config.PermissionModeReadOnly, ApproveForMe: stored.ApproveForMe})
	require.NoError(t, err)
	dispatchPayload(t, srv, "turn", MethodTurnStart, TurnStartParams{ThreadID: start.Thread.ID, Prompt: "continue"})
	waitForMethod(t, out, NotificationTurnError)
	require.Contains(t, out.String(), "observation result unavailable")
	require.NotContains(t, out.String(), "SECRET")
	client.mu.Lock()
	defer client.mu.Unlock()
	require.Empty(t, client.requests, "revoked prior result must not reach the model")
}
