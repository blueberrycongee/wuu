package runtime

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

func (store sessionModelInputReceiptStore) ReadModelUsageReceipts(ctx context.Context, driverID, requestID string) (pluginprotocol.DriverModelReceipts, error) {
	return store.sessions.WithContext(ctx).ReadModelUsageReceipts(store.sessionID, driverID, requestID)
}
