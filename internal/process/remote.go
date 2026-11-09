package process

import (
	"context"
	"time"
)

// RemoteTransport carries process control to the environment that owns the PID.
// Remote process identifiers must never be resolved against the host OS.
type RemoteTransport interface {
	Request(context.Context, string, any, any) error
}

func NewRemoteManager(root string, transport RemoteTransport) *Manager {
	return &Manager{rootDir: root, remote: transport}
}

func (m *Manager) remoteRequest(method string, params, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	return m.remote.Request(ctx, method, params, result)
}

func (m *Manager) PublishRemoteEvent(event Event) { m.publish(event) }
