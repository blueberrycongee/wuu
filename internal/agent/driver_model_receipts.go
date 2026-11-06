package agent

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

type ModelUsageReceiptStore interface {
	ReadModelUsageReceipts(context.Context, string, string) (pluginprotocol.DriverModelReceipts, error)
}

func (g *streamTurnGateway) ReadModelReceipts(ctx context.Context, requestID string) (pluginprotocol.DriverModelReceipts, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return pluginprotocol.DriverModelReceipts{}, err
	}
	defer leave()
	store, ok := g.runner.ModelInputReceiptStore.(ModelUsageReceiptStore)
	if !ok {
		return pluginprotocol.DriverModelReceipts{Status: "unavailable", Receipts: []pluginprotocol.DriverModelUsageReceipt{}}, nil
	}
	return store.ReadModelUsageReceipts(ctx, g.descriptor.ID, requestID)
}
