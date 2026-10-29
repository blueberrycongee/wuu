package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/stretchr/testify/require"
)

type failingSettlementDocuments struct {
	persistence.Documents
	fail               atomic.Bool
	applyBeforeFailure bool
}

func (d *failingSettlementDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	if d.fail.Load() {
		if d.applyBeforeFailure {
			if _, err := d.Documents.Commit(ctx, tx); err != nil {
				return persistence.Receipt{}, err
			}
		}
		return persistence.Receipt{}, errors.New("controlled ledger failure")
	}
	return d.Documents.Commit(ctx, tx)
}

type preparedSettlementExecutor struct{ docs *failingSettlementDocuments }

func (e preparedSettlementExecutor) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "test_remote"}}
}
func (e preparedSettlementExecutor) Execute(ctx context.Context, _ providers.ToolCall) (string, error) {
	err := toolctx.RegisterSettlement(ctx, func(context.Context) error { e.docs.fail.Store(true); return nil }, func() {})
	if err != nil {
		return "", err
	}
	return "provider result", nil
}
func TestRemoteCommitThenLedgerFailureRecoversUnknown(t *testing.T) {
	docs := &failingSettlementDocuments{Documents: agentTestDocuments(t, t.TempDir())}
	ledger, err := toolledger.New(docs, "thread")
	require.NoError(t, err)
	run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: preparedSettlementExecutor{docs}, Ledger: ledger, OperationID: "turn"})
	defer run.Close()
	_, err = run.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "call", Name: "test_remote", Arguments: `{}`}}, nil)
	require.ErrorContains(t, err, "outcome unknown")
	docs.fail.Store(false)
	recovered, err := toolledger.New(docs, "thread")
	require.NoError(t, err)
	require.NoError(t, recovered.Reconcile(context.Background()))
	err = recovered.ValidateContinuation(context.Background())
	var blocked *toolledger.ReplayBlockedError
	require.ErrorAs(t, err, &blocked)
	require.Equal(t, toolledger.ReplayReasonInvocationUnknown, blocked.Decision.Reason)
}

func TestRemoteCommitThenLostLedgerAcknowledgementRecoversSettled(t *testing.T) {
	docs := &failingSettlementDocuments{Documents: agentTestDocuments(t, t.TempDir()), applyBeforeFailure: true}
	ledger, err := toolledger.New(docs, "thread")
	require.NoError(t, err)
	run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: preparedSettlementExecutor{docs}, Ledger: ledger, OperationID: "turn"})
	defer run.Close()
	_, err = run.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "call", Name: "test_remote", Arguments: `{}`}}, nil)
	require.ErrorContains(t, err, "outcome unknown")
	docs.fail.Store(false)
	recovered, err := toolledger.New(docs, "thread")
	require.NoError(t, err)
	require.NoError(t, recovered.Reconcile(context.Background()))
	pending, err := recovered.PendingProjection(context.Background())
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "provider result", pending[0].Result.TextProjection())
	require.Equal(t, toolledger.InvocationSucceeded, pending[0].State)
}

type abortFailureExecutor struct{}

func (abortFailureExecutor) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "test_remote"}}
}
func (abortFailureExecutor) Execute(ctx context.Context, _ providers.ToolCall) (string, error) {
	err := toolctx.RegisterGroupedSettlement(ctx, "worker", nil, func(context.Context, []any) (*toolctx.PreparedSettlement, error) {
		return &toolctx.PreparedSettlement{
			Identity: &toolctx.SettlementIdentity{OperationID: "operation", WorkerInstance: "instance", Actor: "thread", EnvironmentIdentity: "environment"},
			Commit:   func(context.Context) error { return errors.New("commit rejected") },
			Abort:    func(context.Context) error { return errors.New("abort acknowledgement lost") },
			Release:  func(context.Context) error { return nil },
		}, nil
	}, func() {})
	if err != nil {
		return "", err
	}
	return "provider result", nil
}
func TestRemoteAbortAcknowledgementLostRemainsUnknown(t *testing.T) {
	docs := agentTestDocuments(t, t.TempDir())
	ledger, err := toolledger.New(docs, "thread")
	require.NoError(t, err)
	run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: abortFailureExecutor{}, Ledger: ledger, OperationID: "turn"})
	defer run.Close()
	_, err = run.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "call", Name: "test_remote", Arguments: `{}`}}, nil)
	require.ErrorContains(t, err, "outcome unknown")
	pending, err := ledger.PendingObservations(context.Background())
	require.NoError(t, err)
	require.Len(t, pending, 1)
	var blocked *toolledger.ReplayBlockedError
	require.ErrorAs(t, ledger.ValidateContinuation(context.Background()), &blocked)
}
