package runtime

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

func invokeDriverCompaction(ctx context.Context, turn loopdriver.TurnGateway, method string, raw json.RawMessage) (json.RawMessage, error) {
	compactions, ok := turn.(loopdriver.CompactionGateway)
	if !ok {
		return nil, serviceError("service_unavailable", "execution does not expose durable compaction proposals")
	}
	var input pluginprotocol.DriverCompactionParams
	if err := decodeServiceParams(raw, &input); err != nil {
		return nil, err
	}
	var result pluginprotocol.DriverCompactionResult
	var err error
	switch method {
	case "start_compaction":
		result, err = compactions.StartCompaction(ctx, input)
	case "read_compaction":
		result, err = compactions.ReadCompaction(ctx, input.ID, input.Wait)
	case "apply_compaction":
		result, err = compactions.ApplyCompaction(ctx, input.ID)
	case "cancel_compaction":
		result, err = compactions.CancelCompaction(ctx, input.ID)
	}
	if err != nil {
		return nil, err
	}
	return marshalServiceResult(result)
}
