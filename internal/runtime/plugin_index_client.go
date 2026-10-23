package runtime

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// IndexClient captures one admitted provider; it never resolves native paths.
type IndexClient struct {
	call func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func NewIndexClient(call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) *IndexClient {
	return &IndexClient{call: call}
}
func (c *IndexClient) Query(ctx context.Context, query pluginhost.DataQueryParams) (pluginhost.DataQueryResult, error) {
	if err := pluginhost.ValidateDataQueryParams(query); err != nil {
		return pluginhost.DataQueryResult{}, err
	}
	var result pluginhost.DataQueryResult
	if err := c.invoke(ctx, pluginhost.KernelServiceMethod, query, &result); err != nil {
		return result, err
	}
	if result.Version != pluginhost.DataQueryServiceVersion || result.ThreadID != query.ThreadID {
		return pluginhost.DataQueryResult{}, serviceError("invalid_response", "index provider returned an invalid query scope")
	}
	for _, event := range result.Events {
		if event.ThreadID != query.ThreadID {
			return pluginhost.DataQueryResult{}, serviceError("invalid_response", "index provider returned data outside the query scope")
		}
	}
	return result, nil
}
func (c *IndexClient) AppendTurn(ctx context.Context, batch pluginhost.DataAppendTurnParams) (pluginhost.DataAppendTurnResult, error) {
	if err := pluginhost.ValidateDataAppendTurnParams(batch); err != nil {
		return pluginhost.DataAppendTurnResult{}, err
	}
	var result pluginhost.DataAppendTurnResult
	if err := c.invoke(ctx, pluginhost.DataAppendTurnMethod, batch, &result); err != nil {
		return result, err
	}
	if result.Version != pluginhost.DataQueryServiceVersion || result.ThreadID != batch.ThreadID || result.TurnID != batch.TurnID || !result.Committed {
		return pluginhost.DataAppendTurnResult{}, serviceError("invalid_response", "index append has an unconfirmed outcome")
	}
	return result, nil
}
func (c *IndexClient) invoke(ctx context.Context, method string, input, output any) error {
	if c == nil || c.call == nil {
		return serviceError("service_unavailable", "selected index binding is unavailable")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return serviceError("invalid_params", "index request is invalid")
	}
	defer clear(raw)
	result, err := c.call(ctx, method, raw)
	defer clear(result)
	if err != nil {
		var serviceErr *pluginhost.HostServiceError
		if errors.As(err, &serviceErr) {
			return serviceError(serviceErr.Code, "selected index provider failed")
		}
		return serviceError("index_unavailable", "selected index provider failed; an append may have been applied")
	}
	if json.Unmarshal(result, output) != nil {
		return serviceError("invalid_response", "index provider returned an invalid response")
	}
	return nil
}
