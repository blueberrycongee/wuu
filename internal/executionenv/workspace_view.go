package executionenv

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/workspaceview"
)

type WorkspaceViewRequest struct {
	Session  string               `json:"session"`
	Identity string               `json:"identity"`
	Method   string               `json:"method"`
	Params   workspaceview.Params `json:"params"`
}

// ReadWorkspace requires a live connection established by execution. It must
// not call Open or connect: merely browsing cannot provision or restart a worker.
func (m *Manager) ReadWorkspace(ctx context.Context, store, session, identity, method string, params workspaceview.Params) (json.RawMessage, error) {
	m.mu.Lock()
	environment := m.environments[filepath.Join(store, session)]
	m.mu.Unlock()
	if environment == nil || environment.identity != identity {
		return nil, errors.New("session execution workspace is not connected")
	}
	return environment.readWorkspace(ctx, method, params)
}

func (e *Environment) readWorkspace(ctx context.Context, method string, params workspaceview.Params) (json.RawMessage, error) {
	e.statusMu.RLock()
	client, state, connection := e.statusClient, e.status, e.viewConnection
	e.statusMu.RUnlock()
	if state != "ready" || client == nil || client.Failed() {
		return nil, errors.New("session execution workspace is not connected")
	}
	if params.Target != nil && params.Target.ConnectionID != "" && params.Target.ConnectionID != connection {
		return nil, errors.New("execution connection changed; reload the file view")
	}
	params.Target = &workspaceview.Target{ThreadID: e.session, EnvironmentID: e.identity, ConnectionID: connection}
	data, err := client.Call(ctx, "workspace/view", WorkspaceViewRequest{Session: e.session, Identity: e.identity, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	e.statusMu.RLock()
	current := e.status == "ready" && e.statusClient == client && e.viewConnection == connection
	e.statusMu.RUnlock()
	if !current || client.Failed() {
		return nil, errors.New("execution connection changed; reload the file view")
	}
	return data, nil
}
