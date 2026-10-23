package session

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func (s *Store) PutPluginTurnLifecycleOutbox(pluginID, requestID string, payload json.RawMessage) error {
	pluginID, requestID = strings.TrimSpace(pluginID), strings.TrimSpace(requestID)
	if pluginID == "" || requestID == "" {
		return errors.New("plugin lifecycle outbox requires plugin_id and request_id")
	}
	if !json.Valid(payload) {
		return errors.New("plugin lifecycle outbox payload is invalid JSON")
	}
	var identity struct {
		ThreadID string `json:"thread_id"`
		TurnID   string `json:"turn_id"`
		QueueID  string `json:"queue_id"`
	}
	if err := json.Unmarshal(payload, &identity); err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.transaction(func(tx *persistence.Tx) error {
		key := documentPrefix(pluginID) + requestID
		if err := tx.Put(documentNamespace("plugin_turn_lifecycle_outbox"), key, PluginTurnLifecycleOutboxEntry{PluginID: pluginID, RequestID: requestID, Payload: payload, UpdatedAt: now}); err != nil {
			return err
		}
		return tx.Put(documentNamespace("plugin_turn_lifecycle"), key, PluginTurnLifecycleEntry{PluginID: pluginID, RequestID: requestID, SessionID: strings.TrimSpace(identity.ThreadID), TurnID: strings.TrimSpace(identity.TurnID), QueueID: strings.TrimSpace(identity.QueueID), Payload: payload, UpdatedAt: now})
	})
}

func (s *Store) DeletePluginTurnLifecycleOutbox(pluginID, requestID string) error {
	pluginID, requestID = strings.TrimSpace(pluginID), strings.TrimSpace(requestID)
	if pluginID == "" || requestID == "" {
		return errors.New("plugin lifecycle outbox requires plugin_id and request_id")
	}
	return s.transaction(func(tx *persistence.Tx) error {
		return tx.Delete(documentNamespace("plugin_turn_lifecycle_outbox"), documentPrefix(pluginID)+requestID)
	})
}

func (s *Store) DeletePluginTurnLifecycleOutboxForPlugin(pluginID string) error {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return errors.New("plugin lifecycle outbox requires plugin_id")
	}
	return s.transaction(func(tx *persistence.Tx) error {
		for _, table := range []string{"plugin_turn_lifecycle_outbox", "plugin_turn_lifecycle"} {
			if err := deleteDocumentPrefix(tx, table, documentPrefix(pluginID)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ListPluginTurnLifecycleOutbox() ([]PluginTurnLifecycleOutboxEntry, error) {
	var result []PluginTurnLifecycleOutboxEntry
	err := s.transaction(func(tx *persistence.Tx) error {
		var err error
		result, err = readDocumentRows[PluginTurnLifecycleOutboxEntry](tx, "plugin_turn_lifecycle_outbox", "")
		return err
	})
	sort.Slice(result, func(i, j int) bool {
		if !result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].UpdatedAt.Before(result[j].UpdatedAt)
		}
		if result[i].PluginID != result[j].PluginID {
			return result[i].PluginID < result[j].PluginID
		}
		return result[i].RequestID < result[j].RequestID
	})
	return result, err
}

func (s *Store) FindPluginTurnLifecycle(pluginID, requestID, sessionID, turnID string) (PluginTurnLifecycleEntry, bool, error) {
	pluginID, requestID = strings.TrimSpace(pluginID), strings.TrimSpace(requestID)
	sessionID, turnID = strings.TrimSpace(sessionID), strings.TrimSpace(turnID)
	var result PluginTurnLifecycleEntry
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		result, found = PluginTurnLifecycleEntry{}, false
		rows, err := readDocumentRows[PluginTurnLifecycleEntry](tx, "plugin_turn_lifecycle", documentPrefix(pluginID))
		if err != nil {
			return err
		}
		for _, row := range rows {
			if requestID != "" && requestID != row.RequestID || sessionID != "" && sessionID != row.SessionID || turnID != "" && turnID != row.TurnID {
				continue
			}
			if !found || row.UpdatedAt.After(result.UpdatedAt) {
				result, found = row, true
			}
		}
		return nil
	})
	return result, found, err
}

func (s *Store) WritePluginGenerationSnapshot(sessionID string, snapshot PluginGenerationSnapshot) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session_id is required")
	}
	return s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, sessionID); err != nil {
			return err
		}
		return tx.Put(documentNamespace("session_plugin_generation_snapshots"), sessionID, snapshot.Normalize())
	})
}

func (s *Store) ReadPluginGenerationSnapshot(sessionID string) (PluginGenerationSnapshot, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return PluginGenerationSnapshot{}, false, errors.New("session_id is required")
	}
	var result PluginGenerationSnapshot
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		var err error
		found, err = tx.Get(documentNamespace("session_plugin_generation_snapshots"), sessionID, &result)
		return err
	})
	return result.Normalize(), found, err
}

func (s *Store) LoadRequestContextCheckpoint(sessionID string) (json.RawMessage, error) {
	sessionID = strings.TrimSpace(sessionID)
	var result json.RawMessage
	err := s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, sessionID); err != nil {
			return err
		}
		result = nil
		_, err := tx.Get(documentNamespace("session_request_context"), sessionID, &result)
		return err
	})
	return result, err
}

func (s *Store) SaveRequestContextCheckpoint(sessionID string, payload json.RawMessage) error {
	if !json.Valid(payload) {
		return errors.New("request context checkpoint must be valid JSON")
	}
	sessionID = strings.TrimSpace(sessionID)
	return s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, sessionID); err != nil {
			return err
		}
		return tx.Put(documentNamespace("session_request_context"), sessionID, payload)
	})
}
