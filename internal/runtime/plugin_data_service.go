package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/sessiontrace"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// dataQueryInvoker answers host.data.query from the workspace's persisted
// session trace. It is read-only and resolved from the thread_id validated in
// the request; the caller cannot address arbitrary filesystem paths.
type dataQueryInvoker struct {
	parent *kernelHostServices
}

func (k *dataQueryInvoker) ID() string                { return k.parent.ID() }
func (k *dataQueryInvoker) Status() pluginhost.Status { return k.parent.Status() }

func (k *dataQueryInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if params.Method != pluginhost.KernelServiceMethod {
		return nil, serviceError("method_not_found", "kernel service method is unavailable")
	}
	var query pluginhost.DataQueryParams
	if err := decodeServiceParams(params.Params, &query); err != nil {
		return nil, err
	}
	if err := pluginhost.ValidateDataQueryParams(query); err != nil {
		return nil, err
	}

	k.parent.mu.RLock()
	stateDir := k.parent.workspaceStateDir
	k.parent.mu.RUnlock()
	if strings.TrimSpace(stateDir) == "" {
		return nil, serviceError("service_unavailable", "host data service is unavailable")
	}

	return querySessionTrace(ctx, stateDir, query)
}

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
	if params.Method != pluginhost.KernelServiceMethod {
		return nil, serviceError("method_not_found", "trace query method is unavailable")
	}
	var query pluginhost.DataQueryParams
	if err := decodeServiceParams(params.Params, &query); err != nil {
		return nil, err
	}
	return querySessionTrace(ctx, s.stateDir, query)
}

func querySessionTrace(ctx context.Context, stateDir string, query pluginhost.DataQueryParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := pluginhost.ValidateDataQueryParams(query); err != nil {
		return nil, err
	}
	if query.ThreadID == "." || query.ThreadID == ".." {
		return nil, serviceError("invalid_params", "thread id cannot address a directory")
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
