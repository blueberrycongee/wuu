package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

const sessionStateConsumer = "wuu.session-state"

// runtimeState is a native consumer, not a storage implementation. The captured
// registry owns its only authority, just as it does for process consumers.
// Its required edge keeps persistence alive through the journal's final flush.
type runtimeState struct {
	mu        sync.Mutex
	status    pluginhost.State
	scope     string
	documents persistence.Documents
	sessions  *session.Store
	journal   *session.DocumentInferenceRuntime
}

func newRuntimeState(registry *pluginhost.ServiceRegistry, workspaceScope string) *runtimeState {
	documents := persistence.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		result, err := registry.Call(ctx, sessionStateConsumer, pluginhost.ServiceCallParams{
			Service: persistence.ServiceName, Method: method, Params: params,
		})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	return &runtimeState{status: pluginhost.StatePrepared, scope: workspaceScope,
		documents: documents, sessions: session.NewStore(context.Background(), documents)}
}

func (s *runtimeState) ID() string { return sessionStateConsumer }

func (s *runtimeState) Status() pluginhost.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pluginhost.Status{ID: s.ID(), State: s.status}
}

func (s *runtimeState) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }

func (s *runtimeState) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}}
}

func (s *runtimeState) Activate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != pluginhost.StatePrepared {
		return errors.New("session state consumer is not prepared")
	}
	journal, err := s.sessions.WithContext(ctx).NewInferenceJournalRuntime(s.scope)
	if err != nil {
		s.status = pluginhost.StateFailed
		return err
	}
	s.journal = journal
	s.status = pluginhost.StateActive
	return nil
}

func (s *runtimeState) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = pluginhost.StateStopped
	if s.journal == nil {
		return nil
	}
	return s.journal.Close()
}
