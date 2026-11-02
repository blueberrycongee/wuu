package personalconnectors

import (
	"context"
	"encoding/json"
	"strings"
)

// SharedConnections uses the selected MCP connection and credential services.
// The broker stores only metadata needed to bind receipts, never shared tokens.
// Call must revalidate the exact revision and report any ambiguous write effect.
type SharedConnections interface {
	List(context.Context, Actor) ([]Connection, error)
	Call(context.Context, Actor, Connection, string, json.RawMessage) (json.RawMessage, bool, error)
}

func (b *Broker) SetSharedConnections(shared SharedConnections) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.shared = shared
}

// This is deliberately not a credential migration. Existing REST grants,
// pending actions, and polling cursors retain their original identities.
func (b *Broker) syncShared(ctx context.Context, a Actor) error {
	if err := validateActor(a); err != nil {
		return err
	}
	if b.shared == nil {
		return nil
	}
	connections, err := b.shared.List(ctx, a)
	if err != nil {
		return err
	}
	return b.update(ctx, a, func(s *stored) error {
		for id, grant := range s.Grants {
			if grant.Connection.Provider == "mcp" {
				grant.Connection.State = "disconnected"
				s.Grants[id] = grant
			}
		}
		for _, connection := range connections {
			if !strings.HasPrefix(connection.ID, "mcp:") || connection.Provider != "mcp" || connection.Revision == "" {
				return fault("invalid_shared_connection")
			}
			s.Grants[connection.ID] = Grant{Connection: connection, NonExpiring: true}
		}
		return nil
	})
}
