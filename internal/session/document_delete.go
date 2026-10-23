package session

import (
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func (s *Store) Delete(id string) (Session, error)         { return s.deleteSession(id, false) }
func (s *Store) DeleteArchived(id string) (Session, error) { return s.deleteSession(id, true) }

func (s *Store) deleteSession(id string, onlyIfArchived bool) (Session, error) {
	id = strings.TrimSpace(id)
	var deleted Session
	err := s.transaction(func(tx *persistence.Tx) error {
		var err error
		deleted, err = requireSessionDocument(tx, id)
		if err != nil {
			return err
		}
		// Archive admission and every dependent removal share one revision.
		if onlyIfArchived && deleted.ArchivedAt == nil {
			return fmt.Errorf("%w: %q", ErrSessionNotArchived, id)
		}
		for _, table := range []string{"sessions", "session_request_context", "session_history_heads", "session_history_checkpoints", "session_history_retractions", "session_controls", "session_driver_bindings", "session_driver_checkpoints", "session_plugin_generation_snapshots"} {
			if err := tx.Delete(documentNamespace(table), id); err != nil {
				return err
			}
		}
		for _, table := range []string{"session_messages", "session_activity", "held_user_work", "session_compaction_notes", "session_model_input_receipts"} {
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
			if seed.Source.SessionID == id {
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
		work, err := readDocumentRows[ProjectWork](tx, "project_work", "")
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
			var work ProjectWork
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
	})
	return deleted, err
}
