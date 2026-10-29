package toolctx

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInvocationSettlementOwnsCommitAndAbort(t *testing.T) {
	base := context.Background()
	require.False(t, HasSettlement(base))
	require.Error(t, RegisterSettlement(base, func(context.Context) error { return nil }, func() {}))
	ctx := WithSettlement(base)
	commits, aborts := 0, 0
	require.NoError(t, RegisterSettlement(ctx, func(context.Context) error { commits++; return nil }, func() { aborts++ }))
	attempted, err := FinishSettlement(ctx, false)
	require.NoError(t, err)
	require.False(t, attempted)
	require.Zero(t, commits)
	require.Equal(t, 1, aborts)
	ctx = WithSettlement(base)
	require.NoError(t, RegisterSettlement(ctx, func(context.Context) error { commits++; return errors.New("unknown") }, func() { aborts++ }))
	attempted, err = FinishSettlement(ctx, true)
	require.True(t, attempted)
	require.ErrorContains(t, err, "unknown")
	require.Equal(t, 1, commits)
	require.Equal(t, 2, aborts)
	attempted, err = FinishSettlement(ctx, true)
	require.NoError(t, err)
	require.False(t, attempted)
	require.Equal(t, 1, commits)
}

func TestSettlementRejectsIncompatibleOrInvalidGroups(t *testing.T) {
	ctx := WithSettlement(context.Background())
	commits := 0
	var aborts atomic.Int32
	for i := 0; i < 2; i++ {
		require.NoError(t, RegisterSettlement(ctx, func(context.Context) error { commits++; return nil }, func() { aborts.Add(1) }))
	}
	attempted, err := FinishSettlement(ctx, true)
	require.ErrorContains(t, err, "single worker and actor")
	require.False(t, attempted)
	require.Zero(t, commits)
	require.Equal(t, int32(2), aborts.Load())
	ctx = WithSettlement(context.Background())
	err = RegisterGroupedSettlement(ctx, []string{"not-comparable"}, nil, func(context.Context, []any) (*PreparedSettlement, error) { return nil, nil }, func() {})
	require.Error(t, err)
	err = RegisterGroupedSettlement(ctx, struct{ X any }{X: []int{1}}, nil, func(context.Context, []any) (*PreparedSettlement, error) { return nil, nil }, func() {})
	require.Error(t, err)
}

func TestSettlementRejectsUnfinishedAdmittedChild(t *testing.T) {
	ctx := WithSettlement(context.Background())
	release, err := ReserveSettlementChild(ctx)
	require.NoError(t, err)
	require.NoError(t, RegisterSettlement(ctx, func(context.Context) error { return nil }, func() {}))
	_, err = PrepareSettlement(ctx, true)
	require.ErrorContains(t, err, "child")
	release()
	_, err = ReserveSettlementChild(ctx)
	require.Error(t, err)
}

func TestSettlementAllowsCompletedAdmittedChild(t *testing.T) {
	ctx := WithSettlement(context.Background())
	release, err := ReserveSettlementChild(ctx)
	require.NoError(t, err)
	release()
	_, err = PrepareSettlement(ctx, true)
	require.NoError(t, err)
}

func TestSettlementAllowsUnfinishedChildWithoutParticipants(t *testing.T) {
	ctx := WithSettlement(context.Background())
	release, err := ReserveSettlementChild(ctx)
	require.NoError(t, err)
	_, err = PrepareSettlement(ctx, true)
	require.NoError(t, err)
	child := WithSettlement(ctx)
	aborted := false
	require.NoError(t, RegisterSettlement(child, func(context.Context) error { return nil }, func() { aborted = true }))
	_, err = PrepareSettlement(child, true)
	require.ErrorContains(t, err, "parent settlement")
	require.True(t, aborted)
	release()
}
