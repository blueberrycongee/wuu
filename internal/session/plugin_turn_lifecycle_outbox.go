package session

import (
	"encoding/json"
	"time"
)

// PluginTurnLifecycleOutboxEntry is one owner-scoped terminal turn event that
// remains pending until the target plugin acknowledges delivery.
type PluginTurnLifecycleOutboxEntry struct {
	PluginID  string
	RequestID string
	Payload   json.RawMessage
	UpdatedAt time.Time
}

// PluginTurnLifecycleEntry is durable latest state retained after delivery
// acknowledgement for host.session.inspect and restart recovery.
type PluginTurnLifecycleEntry struct {
	PluginID  string
	RequestID string
	SessionID string
	TurnID    string
	QueueID   string
	Payload   json.RawMessage
	UpdatedAt time.Time
}
