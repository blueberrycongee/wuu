package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/sessiontrace"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

const DataQueryModule = "wuu.index.trace"

// dataQueryService owns the default trace index for one generation. External
// implementations provide host.data.query through the same service registry.
type dataQueryService struct {
	mu           sync.RWMutex
	id, stateDir string
	state        pluginhost.State
}

func NewDataQueryService(id, stateDir string) *dataQueryService {
	return &dataQueryService{id: id, stateDir: stateDir, state: pluginhost.StatePrepared}
}
func (s *dataQueryService) ID() string { return s.id }
func (s *dataQueryService) Status() pluginhost.Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return pluginhost.Status{ID: s.id, State: s.state}
}
func (*dataQueryService) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{pluginhost.KernelDataQueryDescriptor()}
}
func (*dataQueryService) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (s *dataQueryService) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != pluginhost.StatePrepared || s.stateDir == "" {
		return serviceError("service_unavailable", "trace index cannot activate")
	}
	s.state = pluginhost.StateActive
	return nil
}
func (s *dataQueryService) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pluginhost.StateStopped
	return nil
}
func (s *dataQueryService) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state != pluginhost.StateActive || params.Service != pluginhost.KernelDataQueryService {
		return nil, serviceError("service_unavailable", "trace index is unavailable")
	}
	if params.Method == pluginhost.DataAppendTurnMethod {
		var batch pluginhost.DataAppendTurnParams
		if err := decodeServiceParams(params.Params, &batch); err != nil {
			return nil, err
		}
		if err := pluginhost.ValidateDataAppendTurnParams(batch); err != nil {
			return nil, err
		}
		receipt, err := appendNativeIndexTurn(ctx, s.stateDir, batch)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(receipt)
	}
	if params.Method == pluginhost.DataLatestContextMethod {
		var input pluginhost.DataLatestContextParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		if err := pluginhost.ValidateDataQueryParams(pluginhost.DataQueryParams{ThreadID: input.ThreadID}); err != nil {
			return nil, err
		}
		return queryLatestNativeContext(ctx, s.stateDir, input.ThreadID)
	}
	if params.Method != pluginhost.KernelServiceMethod {
		return nil, serviceError("method_not_found", "trace query method is unavailable")
	}
	var query pluginhost.DataQueryParams
	if err := decodeServiceParams(params.Params, &query); err != nil {
		return nil, err
	}
	if err := pluginhost.ValidateDataQueryParams(query); err != nil {
		return nil, err
	}
	return queryNativeIndex(ctx, s.stateDir, query)
}

func querySessionTrace(ctx context.Context, stateDir string, query pluginhost.DataQueryParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := pluginhost.ValidateDataQueryParams(query); err != nil {
		return nil, err
	}
	result := pluginhost.DataQueryResult{Version: pluginhost.DataQueryServiceVersion, ThreadID: query.ThreadID, Events: []pluginhost.DataEvent{}}
	file, err := os.Open(sessiontrace.Path(statepath.SessionArtifactDir(stateDir, query.ThreadID)))
	if errors.Is(err, os.ErrNotExist) {
		return marshalServiceResult(result)
	}
	if err != nil {
		return nil, serviceError("data_unavailable", "session data is unavailable")
	}
	defer file.Close()
	result.DiagnosticPath = sessiontrace.Path(statepath.SessionArtifactDir(stateDir, query.ThreadID))
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var event pluginhost.DataEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			return nil, serviceError("data_unavailable", "session data is invalid")
		}
		if len(pluginhost.FilterDataEvents([]pluginhost.DataEvent{event}, query)) == 0 {
			continue
		}
		result.Events = append(result.Events, event)
		if query.Limit > 0 && len(result.Events) >= query.Limit {
			break
		}
	}
	if scanner.Err() != nil {
		return nil, serviceError("data_unavailable", "session data cannot be read")
	}
	return marshalServiceResult(result)
}
