package session

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func (s *documentStore) Delete(id string) (Session, error)         { return s.deleteSession(id, false) }
func (s *documentStore) DeleteArchived(id string) (Session, error) { return s.deleteSession(id, true) }

func (s *documentStore) deleteSession(id string, onlyIfArchived bool) (Session, error) {
	id = strings.TrimSpace(id)
	var deleted Session
	err := s.transaction(func(tx *persistence.Tx) error {
		var err error
		deleted, err = requireSessionDocument(tx, id)
		if err != nil {
			return err
		}
		if onlyIfArchived && deleted.ArchivedAt == nil {
			return fmt.Errorf("%w: %q", ErrSessionNotArchived, id)
		}
		return deleteSessionDocuments(tx, deleted)
	})
	return deleted, err
}

func (s *documentStore) DeleteWithSideChats(id string, childIDs []string, onlyIfArchived bool) ([]Session, error) {
	var deleted []Session
	err := s.transaction(func(tx *persistence.Tx) error {
		parent, err := requireSessionDocument(tx, id)
		if err != nil {
			return err
		}
		if onlyIfArchived && parent.ArchivedAt == nil {
			return fmt.Errorf("%w: %q", ErrSessionNotArchived, id)
		}
		rows, err := readDocumentRows[Session](tx, "sessions", "")
		if err != nil {
			return err
		}
		approved := make(map[string]bool, len(childIDs))
		for _, child := range childIDs {
			approved[child] = true
		}
		deleted = []Session{parent}
		for _, row := range rows {
			if row.ParentID == id && row.Placement == PlacementSide {
				if !approved[row.ID] {
					return errors.New("attached conversations changed during deletion")
				}
				deleted = append(deleted, row)
			}
		}
		if len(deleted) != len(approved)+1 {
			return errors.New("attached conversations changed during deletion")
		}
		for _, row := range deleted {
			if err := deleteSessionDocuments(tx, row); err != nil {
				return err
			}
		}
		return nil
	})
	return deleted, err
}

func deleteSessionDocuments(tx *persistence.Tx, deleted Session) error {
	id := deleted.ID
	if err := tx.Delete(documentNamespace("session_order"), sessionOrderKey(deleted)); err != nil {
		return err
	}
	for _, table := range []string{"sessions", "session_request_context", "session_history_heads", "session_history_checkpoints", "session_history_retractions", "session_controls", "session_driver_bindings", "session_driver_checkpoints", "session_plugin_generation_snapshots"} {
		if err := tx.Delete(documentNamespace(table), id); err != nil {
			return err
		}
	}
	for _, table := range []string{"session_messages", "session_activity", "held_user_work", "session_compaction_notes", "session_model_input_receipts", "client_input_receipts", "client_input_payloads"} {
		if err := deleteDocumentPrefix(tx, table, documentPrefix(id)); err != nil {
			return err
		}
	}
	inbox, err := readDocumentRows[inboxDocument](tx, "session_inbox", "")
	if err != nil {
		return err
	}
	for _, row := range inbox {
		if row.SessionID == id {
			if err := tx.Delete(documentNamespace("session_inbox"), row.ClientID); err != nil {
				return err
			}
		}
	}
	seeds, err := readDocumentRows[ContextSeed](tx, "context_seeds", "")
	if err != nil {
		return err
	}
	for _, seed := range seeds {
		if seed.Source.SessionID != id && seed.ID != deleted.SeedID {
			continue
		}
		rows, err := readDocumentRows[Session](tx, "sessions", "")
		if err != nil {
			return err
		}
		inUse := slices.ContainsFunc(rows, func(row Session) bool { return row.InheritedThroughSeq > 0 && row.SeedID == seed.ID })
		if !inUse {
			if err := tx.Delete(documentNamespace("context_seeds"), seed.ID); err != nil {
				return err
			}
		}
	}
	lifecycle, err := readDocumentRows[PluginTurnLifecycleEntry](tx, "plugin_turn_lifecycle", "")
	if err != nil {
		return err
	}
	for _, row := range lifecycle {
		if row.SessionID != id {
			continue
		}
		key := documentPrefix(row.PluginID) + row.RequestID
		for _, table := range []string{"plugin_turn_lifecycle", "plugin_turn_lifecycle_outbox"} {
			if err := tx.Delete(documentNamespace(table), key); err != nil {
				return err
			}
		}
	}
	// Keep deletion of historical records without the retired work API.
	type retiredWork struct {
		ID        string `json:"id"`
		ProjectID string `json:"project_id"`
	}
	work, err := readDocumentRows[retiredWork](tx, "project_work", "")
	if err != nil {
		return err
	}
	removedWork := make(map[string]bool)
	for _, row := range work {
		if row.ProjectID != id {
			continue
		}
		removedWork[row.ID] = true
		if err := tx.Delete(documentNamespace("project_work"), row.ID); err != nil {
			return err
		}
	}
	operations, err := tx.Read(persistence.Query{Namespace: documentNamespace("project_work_operations")})
	if err != nil {
		return err
	}
	for _, document := range operations {
		var work retiredWork
		if _, err := tx.Get(document.Namespace, document.Key, &work); err != nil {
			return err
		}
		if removedWork[work.ID] {
			if err := tx.Delete(document.Namespace, document.Key); err != nil {
				return err
			}
		}
	}
	return nil
}
