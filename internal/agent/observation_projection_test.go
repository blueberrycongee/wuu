package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolbatch"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/toolstate"
	"github.com/stretchr/testify/require"
)

func stagedProjection(t *testing.T) (*toolledger.Ledger, toolledger.Invocation, toolctx.SettlementIdentity, *atomic.Bool) {
	t.Helper()
	ctx := context.Background()
	ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "actor")
	require.NoError(t, err)
	batch, err := ledger.BeginBatch(ctx, "turn", 0)
	require.NoError(t, err)
	inv, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "call", Name: "run_code", Arguments: `{}`}, toolledger.ReplayAtMostOnce)
	require.NoError(t, err)
	require.NoError(t, ledger.FinalizeBatch(ctx, batch))
	require.NoError(t, ledger.Start(ctx, inv.ID))
	id := toolctx.SettlementIdentity{OperationID: inv.ID, WorkerInstance: "worker", Actor: "actor", EnvironmentIdentity: "env"}
	intent := toolledger.ObservationIntent{OperationID: id.OperationID, WorkerInstance: id.WorkerInstance, Actor: id.Actor, EnvironmentIdentity: id.EnvironmentIdentity}
	var denied atomic.Bool
	require.NoError(t, toolctx.RegisterObservationAuthorization(id, ledger, func(context.Context) error {
		if denied.Load() {
			return errors.New("revoked")
		}
		return nil
	}))
	t.Cleanup(func() { toolctx.ForgetObservationAuthorization(id, ledger) })
	require.NoError(t, ledger.StageObservation(ctx, inv.ID, intent, toolresult.FromText("SECRET")))
	require.NoError(t, ledger.ResolveObservation(ctx, inv.ID, intent, true))
	return ledger, inv, id, &denied
}

type blockingProjectionExecutor struct {
	checks  atomic.Int32
	entered chan struct{}
}

func (e *blockingProjectionExecutor) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "remote"}}
}
func (e *blockingProjectionExecutor) Execute(ctx context.Context, _ providers.ToolCall) (string, error) {
	err := toolctx.RegisterGroupedSettlement(ctx, "worker", nil, func(context.Context, []any) (*toolctx.PreparedSettlement, error) {
		return &toolctx.PreparedSettlement{Identity: &toolctx.SettlementIdentity{OperationID: "blocked-projection", WorkerInstance: "worker", Actor: "actor", EnvironmentIdentity: "env"}, Authorize: func(ctx context.Context) error {
			if e.checks.Add(1) == 2 {
				close(e.entered)
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}, Commit: func(context.Context) error { return nil }, Abort: func(context.Context) error { return nil }, Release: func(context.Context) error { return nil }}, nil
	}, func() {})
	return "SECRET", err
}
func TestSelectedBatchProjectionAuthorizationCancelsAfterPhysicalCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "actor")
	require.NoError(t, err)
	executor := &blockingProjectionExecutor{entered: make(chan struct{})}
	done := make(chan error, 1)
	var delivered atomic.Bool
	go func() {
		messages, _, err := executeSelectedToolBatch(ctx, toolbatch.V1Adapter{}, toolbatch.Request{ID: "batch", Calls: []providers.ToolCall{{ID: "call", Name: "remote", Arguments: `{}`}}, MaxParallel: 1}, ToolRuntimeConfig{Executor: executor, Ledger: ledger, OperationID: "turn", RunContext: ctx}, func(providers.ToolCall, toolresult.Result) { delivered.Store(true) })
		if len(messages) != 0 {
			done <- errors.New("result escaped after cancellation")
			return
		}
		done <- err
	}()
	select {
	case <-executor.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("post-completion authorization did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("projection cancellation did not drain")
	}
	require.False(t, delivered.Load())
	pending, err := ledger.PendingProjection(context.Background())
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Contains(t, pending[0].Result.TextProjection(), "SECRET")
}

type failedProjectionReceipt struct {
	persistence.Documents
	fired atomic.Bool
	after bool
}

func (d *failedProjectionReceipt) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	for _, w := range tx.Writes {
		var inv toolstate.Invocation
		if w.Namespace == toolstate.Invocations && json.Unmarshal(w.Value, &inv) == nil && inv.ProjectedAt > 0 && d.fired.CompareAndSwap(false, true) {
			if d.after {
				receipt, err := d.Documents.Commit(ctx, tx)
				if err != nil {
					return receipt, err
				}
				return receipt, errors.New("lost history acknowledgement")
			}
			return persistence.Receipt{}, errors.New("history unavailable")
		}
	}
	return d.Documents.Commit(ctx, tx)
}

func TestObservationBindingSurvivesUncertainHistoryAcknowledgement(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "not committed", true: "acknowledgement lost"}[after], func(t *testing.T) {
			ctx := context.Background()
			docs := &failedProjectionReceipt{Documents: agentTestDocuments(t, t.TempDir()), after: after}
			ledger, err := toolledger.New(docs, "actor")
			require.NoError(t, err)
			group, err := ledger.BeginBatch(ctx, "turn", 0)
			require.NoError(t, err)
			inv, err := ledger.Prepare(ctx, group, providers.ToolCall{ID: "call", Name: "run_code", Arguments: `{}`}, toolledger.ReplayAtMostOnce)
			require.NoError(t, err)
			require.NoError(t, ledger.FinalizeBatch(ctx, group))
			require.NoError(t, ledger.Start(ctx, inv.ID))
			id := toolctx.SettlementIdentity{OperationID: inv.ID, WorkerInstance: "worker", Actor: "actor", EnvironmentIdentity: "env"}
			intent := toolledger.ObservationIntent{OperationID: id.OperationID, WorkerInstance: id.WorkerInstance, Actor: id.Actor, EnvironmentIdentity: id.EnvironmentIdentity}
			require.NoError(t, toolctx.RegisterObservationAuthorization(id, ledger, func(context.Context) error { return nil }))
			defer toolctx.ForgetObservationAuthorization(id, ledger)
			require.NoError(t, ledger.StageObservation(ctx, inv.ID, intent, toolresult.FromText("SECRET")))
			require.NoError(t, ledger.ResolveObservation(ctx, inv.ID, intent, true))
			require.Error(t, ledger.MarkProjected(ctx, []string{inv.ID}))
			require.NoError(t, toolctx.AuthorizeObservation(ctx, id, ledger), "unknown persistence must retain authorization")
			require.NoError(t, ledger.MarkProjected(ctx, []string{inv.ID}))
			require.Error(t, toolctx.AuthorizeObservation(ctx, id, ledger))
		})
	}
}

func TestObservationPendingProjectionHonorsCurrentPermissionAndHistory(t *testing.T) {
	ledger, inv, _, denied := stagedProjection(t)
	denied.Store(true)
	runner := &StreamRunner{ToolLedger: ledger}
	messages, err := runner.pendingToolResultMessages(context.Background(), nil)
	require.Error(t, err)
	require.Empty(t, messages)
	// History is authoritative evidence the result was already projected. Its
	// receipt is repaired before checking the unprojected queue.
	history := []providers.ChatMessage{{Role: "tool", ToolInvocationID: inv.ID, ToolCallID: "call", Content: "SECRET"}}
	messages, err = runner.pendingToolResultMessages(context.Background(), history)
	require.NoError(t, err)
	require.Empty(t, messages)
}

func TestObservationLiveModelAdmissionRechecksPermission(t *testing.T) {
	ctx := context.Background()
	ledger, inv, _, denied := stagedProjection(t)
	runner := &StreamRunner{ToolLedger: ledger, Model: "test"}
	g := newStreamTurnGateway(ctx, runner, loopdriver.PersistedInput{}, nil)
	defer g.close()
	// The callback received this result while authorized, then authorization was
	// revoked before the first model request.
	g.ready = true
	g.activeHistory = []providers.ChatMessage{{Role: "tool", ToolInvocationID: inv.ID, ToolCallID: "call", Content: "SECRET"}}
	g.pendingObservationAdmission[inv.ID] = true
	denied.Store(true)
	request, err := g.ReadTurn(ctx)
	require.Error(t, err)
	require.Empty(t, request.Messages)
	denied.Store(false)
	request, err = g.ReadTurn(ctx)
	require.NoError(t, err)
	require.Contains(t, request.Messages[0].Content, "SECRET")
	// ReadTurn is the request admission boundary; an already returned request
	// cannot be recalled when permission changes afterward.
	denied.Store(true)
	retry, err := g.ReadTurn(ctx)
	require.NoError(t, err)
	require.Contains(t, retry.Messages[0].Content, "SECRET")
}

func TestObservationCustomDriverCannotSkipFirstAdmission(t *testing.T) {
	ctx := context.Background()
	ledger, inv, _, denied := stagedProjection(t)
	runner := &StreamRunner{ToolLedger: ledger, Model: "test"}
	g := newStreamTurnGateway(ctx, runner, loopdriver.PersistedInput{}, nil)
	defer g.close()
	g.ready = true
	g.pendingObservationAdmission[inv.ID] = true
	denied.Store(true)
	_, err := g.CallModel(ctx, providers.ChatRequest{Model: "test"})
	require.ErrorContains(t, err, "authorize tool results for model")
}

func TestObservationBindingReleasedOnlyAfterConfirmedProjection(t *testing.T) {
	ctx := context.Background()
	ledger, inv, id, _ := stagedProjection(t)
	require.NoError(t, ledger.ForgetProjectedObservationBindings(ctx, []string{inv.ID}))
	require.NoError(t, toolctx.AuthorizeObservation(ctx, id, ledger), "an unprojected result must retain its binding")
	require.NoError(t, ledger.MarkProjected(ctx, []string{inv.ID}))
	require.Error(t, toolctx.AuthorizeObservation(ctx, id, ledger), "confirmed history projection releases its binding")
}

type failingRemoteWithSuccessfulFinalizer struct{}

func (failingRemoteWithSuccessfulFinalizer) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "read_file"}}
}
func (failingRemoteWithSuccessfulFinalizer) Execute(ctx context.Context, _ providers.ToolCall) (string, error) {
	toolctx.MarkRemoteReadExposure(ctx)
	return "", errors.New("SECRET")
}
func (failingRemoteWithSuccessfulFinalizer) FinalizeToolOutcome(providers.ToolCall, toolresult.Result) ToolFinalization {
	return ToolFinalization{Result: toolresult.FromText("SECRET")}
}
func TestRemoteFailureFinalizerCannotExposeReadOrMarkItSuccessful(t *testing.T) {
	ctx := context.Background()
	ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "actor")
	require.NoError(t, err)
	run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: failingRemoteWithSuccessfulFinalizer{}, Ledger: ledger, OperationID: "turn"})
	defer run.Close()
	messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "read", Name: "read_file", Arguments: `{}`}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.True(t, messages[0].ToolResult.IsError)
	require.NotContains(t, messages[0].Content, "SECRET")
	pending, err := ledger.PendingProjection(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.NotContains(t, pending[0].Result.TextProjection(), "SECRET")
}
