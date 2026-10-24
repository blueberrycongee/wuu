package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/toolstate"
	"os"
	"path/filepath"
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
	sessDir   string
	host      *pluginhost.Host
	documents persistence.Documents
	sessions  *session.Store
	journal   *session.InferenceJournalRuntime
	registry  *pluginhost.ServiceRegistry
}

func newRuntimeState(registry *pluginhost.ServiceRegistry, workspaceScope string) *runtimeState {
	var state *runtimeState
	call := func(service string) func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
			if state != nil && state.host != nil {
				state.mu.Lock()
				active := state.status == pluginhost.StateActive
				state.mu.Unlock()
				if active {
					result, err := state.host.CallService(ctx, sessionStateConsumer, service, method, params)
					if err != nil {
						return nil, err
					}
					return result, nil
				}
			}
			result, err := registry.Call(ctx, sessionStateConsumer, pluginhost.ServiceCallParams{Service: service, Method: method, Params: params})
			if err != nil {
				return nil, err
			}
			return result, nil
		}
	}
	state = &runtimeState{status: pluginhost.StatePrepared, scope: workspaceScope, registry: registry,
		documents: persistence.NewClient(call(persistence.ServiceName)), sessions: session.NewClient(context.Background(), call(session.ServiceName))}

	return state
}

func (s *runtimeState) ID() string { return sessionStateConsumer }

func (s *runtimeState) Status() pluginhost.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pluginhost.Status{ID: s.ID(), State: s.status}
}

func (s *runtimeState) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }

func (s *runtimeState) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}, {Name: session.ServiceName, MajorVersion: 1, Required: true}}
}

func (s *runtimeState) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.registry.CheckSatisfaction(s.RequiredServices()); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != pluginhost.StatePrepared {
		return errors.New("session state consumer is not prepared")
	}
	s.journal = session.NewLazyInferenceJournalRuntime(func(ctx context.Context) (*session.InferenceJournalRuntime, error) {
		return s.sessions.WithContext(ctx).NewInferenceJournalRuntime(s.scope)
	})
	s.status = pluginhost.StateActive
	return nil
}

func (s *runtimeState) Close(ctx context.Context) error {
	s.mu.Lock()
	s.status = pluginhost.StateStopped
	journal := s.journal
	s.mu.Unlock()
	var err error
	if journal != nil {
		err = journal.CloseContext(ctx)
	}
	return err
}

func initializeSessionState(ctx context.Context, dir string, documents persistence.Documents, store *session.Store) error {
	lease, first, err := session.AcquireAppServerPresence(dir)
	if err != nil {
		return err
	}
	defer lease.Release()
	migrated, err := session.LegacySQLiteMigrationRecorded(ctx, documents)
	if err != nil {
		return err
	}
	if !migrated {
		if _, err := os.Stat(filepath.Join(dir, "sessions.sqlite3")); err == nil {
			if !first {
				return fmt.Errorf("%w: stop live app-servers before importing legacy sessions", persistence.ErrMigrationRequired)
			}
			if _, err := session.MigrateLegacySQLite(ctx, dir, documents, session.NewLegacyInferenceConverter(), toolstate.NewLegacyConverter(), execution.NewLegacyConverter()); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// The completed import is authoritative. Auditing a retired legacy file
	// belongs to explicit migration/check commands, not every workspace startup.

	return store.WithContext(ctx).RebuildSessionIndexes()
}
