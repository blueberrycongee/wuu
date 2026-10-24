package runtime

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/sessiontrace"
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

// LatestContext returns one complete request projection, never a prefix of
// chronological history. Providers reject an oversized projection explicitly.
func (c *IndexClient) LatestContext(ctx context.Context, threadID string) (pluginhost.DataQueryResult, error) {
	if err := pluginhost.ValidateDataQueryParams(pluginhost.DataQueryParams{ThreadID: threadID}); err != nil {
		return pluginhost.DataQueryResult{}, err
	}
	var result pluginhost.DataQueryResult
	if err := c.invoke(ctx, pluginhost.DataLatestContextMethod, pluginhost.DataLatestContextParams{ThreadID: threadID}, &result); err != nil {
		return result, err
	}
	invalid := serviceError("invalid_response", "index returned an invalid latest context")
	if result.Version != pluginhost.DataQueryServiceVersion || result.ThreadID != threadID || result.Events == nil || len(result.Events) > 4 {
		return pluginhost.DataQueryResult{}, invalid
	}
	if len(result.Events) == 0 {
		return result, nil
	}
	if len(result.Events) < 2 || result.Events[0].Type != "turn" || result.Events[1].Type != "context_requests" {
		return pluginhost.DataQueryResult{}, invalid
	}
	turnID := result.Events[0].TurnID
	var turn *sessiontrace.TurnRecord
	if json.Unmarshal(result.Events[0].Data, &turn) != nil || turn == nil || turnID == "" || turn.ThreadID != threadID || turn.TurnID != turnID {
		return pluginhost.DataQueryResult{}, invalid
	}
	var requests []*sessiontrace.RequestContextRecord
	if json.Unmarshal(result.Events[1].Data, &requests) != nil || len(requests) != 1 || requests[0] == nil {
		return pluginhost.DataQueryResult{}, invalid
	}
	for i, event := range result.Events {
		if event.ThreadID != threadID || event.TurnID != turnID {
			return pluginhost.DataQueryResult{}, invalid
		}
		if i < 2 {
			continue
		}
		if i == 2 && event.Type == "provider_states" {
			var states []*sessiontrace.ProviderStateRecord
			if json.Unmarshal(event.Data, &states) != nil || len(states) != 1 || states[0] == nil || states[0].StepIndex != requests[0].StepIndex {
				return pluginhost.DataQueryResult{}, invalid
			}
		} else {
			var final *sessiontrace.FinalRecord
			if event.Type != "final" || (i == 3 && result.Events[2].Type != "provider_states") || json.Unmarshal(event.Data, &final) != nil || final == nil {
				return pluginhost.DataQueryResult{}, invalid
			}
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
	if method == pluginhost.DataLatestContextMethod && int64(len(result)) > pluginhost.DataLatestContextMaxBytes {
		return serviceError("limit_exceeded", "latest context exceeds the read budget")
	}
	if json.Unmarshal(result, output) != nil {
		return serviceError("invalid_response", "index provider returned an invalid response")
	}
	return nil
}
