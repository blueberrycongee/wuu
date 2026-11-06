package loopdriver

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

func (g serviceGateway) StartCompaction(ctx context.Context, input pluginprotocol.DriverCompactionParams) (pluginprotocol.DriverCompactionResult, error) {
	input.ExecutionID = g.executionID
	var result pluginprotocol.DriverCompactionResult
	err := g.callTurn(ctx, "start_compaction", input, &result)
	return result, err
}

func (g serviceGateway) ReadCompaction(ctx context.Context, id string, wait bool) (pluginprotocol.DriverCompactionResult, error) {
	var result pluginprotocol.DriverCompactionResult
	err := g.callTurn(ctx, "read_compaction", pluginprotocol.DriverCompactionParams{DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, ID: id, Wait: wait}, &result)
	return result, err
}

func (g serviceGateway) ApplyCompaction(ctx context.Context, id string) (pluginprotocol.DriverCompactionResult, error) {
	var result pluginprotocol.DriverCompactionResult
	err := g.callTurn(ctx, "apply_compaction", pluginprotocol.DriverCompactionParams{DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, ID: id}, &result)
	return result, err
}

func (g serviceGateway) CancelCompaction(ctx context.Context, id string) (pluginprotocol.DriverCompactionResult, error) {
	var result pluginprotocol.DriverCompactionResult
	err := g.callTurn(ctx, "cancel_compaction", pluginprotocol.DriverCompactionParams{DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, ID: id}, &result)
	return result, err
}
