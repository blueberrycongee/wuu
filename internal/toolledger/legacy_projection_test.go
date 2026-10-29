package toolledger

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestLegacyReadProjectionFailsClosedAndOrdinaryResultsRemainCompatible(t *testing.T) {
	for _, test := range []struct {
		name     string
		root     string
		nested   bool
		failed   bool
		ordinary bool
		blocked  bool
	}{
		{name: "legacy direct success", root: "read_file", blocked: true},
		{name: "legacy direct failure", root: "read_file", failed: true, blocked: true},
		{name: "legacy nested success", root: "run_code", nested: true, blocked: true},
		{name: "legacy nested failure", root: "run_code", nested: true, failed: true, blocked: true},
		{name: "ordinary unmarked", root: "web_search"},
		{name: "ordinary current read", root: "read_file", ordinary: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			forEachLedgerStore(t, func(t *testing.T, docs *ledgerTestDocuments) {
				ctx := context.Background()
				l, err := New(docs, "owner")
				require.NoError(t, err)
				batch, err := l.BeginBatch(ctx, "turn", 0)
				require.NoError(t, err)
				root, err := l.Prepare(ctx, batch, providers.ToolCall{ID: "root", Name: test.root, Arguments: `{}`}, ReplayAtMostOnce)
				require.NoError(t, err)
				require.NoError(t, l.FinalizeBatch(ctx, batch))
				require.NoError(t, l.Start(ctx, root.ID))
				if test.nested {
					middleBatch, err := l.BeginBatch(ctx, "turn", 1)
					require.NoError(t, err)
					middle, err := l.PrepareNested(ctx, middleBatch, root.ID, providers.ToolCall{ID: "middle", Name: "orchestrator", Arguments: `{}`}, ReplayAtMostOnce)
					require.NoError(t, err)
					require.NoError(t, l.FinalizeBatch(ctx, middleBatch))
					require.NoError(t, l.Start(ctx, middle.ID))
					childBatch, err := l.BeginBatch(ctx, "turn", 2)
					require.NoError(t, err)
					child, err := l.PrepareNested(ctx, childBatch, middle.ID, providers.ToolCall{ID: "child", Name: "read_file", Arguments: `{}`}, ReplayAtMostOnce)
					require.NoError(t, err)
					require.NoError(t, l.FinalizeBatch(ctx, childBatch))
					require.NoError(t, l.Start(ctx, child.ID))
					require.NoError(t, l.Settle(ctx, child.ID, toolresult.FromText("secret")))
					require.NoError(t, l.Settle(ctx, middle.ID, toolresult.FromText("secret")))
				}
				result := toolresult.FromText("result")
				if test.failed {
					result = toolresult.FromErrorText("secret error")
				}
				if test.ordinary {
					require.NoError(t, l.SettleOrdinary(ctx, root.ID, result))
				} else {
					require.NoError(t, l.Settle(ctx, root.ID, result))
				}
				pending, err := l.PendingProjection(ctx)
				if test.blocked {
					require.ErrorContains(t, err, "legacy read result")
					require.Empty(t, pending)
				} else {
					require.NoError(t, err)
					require.Len(t, pending, 1)
				}
			})
		})
	}
}
