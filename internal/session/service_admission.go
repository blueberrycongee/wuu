package session

import (
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"time"
)

var errAdmissionChanged = errors.New("session admission candidates changed")

func (s *documentStore) AdmitThreadExecution(id string, limit int, activeIDs, knownIDs []string) (int64, error) {
	var generation int64
	ownerID := NewID()
	err := s.transaction(func(tx *persistence.Tx) error {
		if limit > 0 {
			members, err := readDocumentRows[Session](tx, "sessions", "")
			if err != nil {
				return err
			}
			known := make(map[string]bool, len(knownIDs))
			for _, id := range knownIDs {
				known[id] = true
			}
			for _, member := range members {
				if !known[member.ID] {
					return errAdmissionChanged
				}
			}
			active := make(map[string]bool, len(activeIDs))
			for _, id := range activeIDs {
				active[id] = true
			}
			projectID := ""
			for _, member := range members {
				if member.ID == id && executionCapacityMember(member) {
					projectID = member.ParentID
				}
			}
			if projectID != "" {
				occupied := 0
				for _, member := range members {
					if member.ID != id && member.ParentID == projectID && executionCapacityMember(member) && active[member.ID] {
						occupied++
					}
				}
				if occupied >= limit {
					return fmt.Errorf("project %q worker limit %d reached: %w", projectID, limit, ErrProjectWorkerCapacity)
				}
			}
		}
		var record threadExecutionDocument
		if _, err := tx.Get(documentNamespace("thread_execution_resets"), id, &record); err != nil {
			return err
		}
		record.ThreadID = id
		record.OwnerID = ownerID
		generation = record.Generation
		return tx.Put(documentNamespace("thread_execution_resets"), id, record)
	})
	return generation, err
}
func executionCapacityMember(member Session) bool {
	if member.Source != "project-session" || member.ParentID == "" || member.ArchivedAt != nil {
		return false
	}
	switch member.ProjectRole {
	case "", "worker", "executor", "technical_lead":
		return true
	}
	return false
}
func (s *documentStore) RequestThreadExecutionResetRecord(id string) error {
	stamp := time.Now().UTC().UnixMilli()
	return s.transaction(func(tx *persistence.Tx) error {
		var record threadExecutionDocument
		if _, err := tx.Get(documentNamespace("thread_execution_resets"), id, &record); err != nil {
			return err
		}
		record.ThreadID = id
		record.Generation++
		record.RequestedAt = stamp
		return tx.Put(documentNamespace("thread_execution_resets"), id, record)
	})
}
func (s *documentStore) ThreadExecutionResetRequested(id string, generation int64) (bool, error) {
	var record threadExecutionDocument
	err := s.transaction(func(tx *persistence.Tx) error {
		_, err := tx.Get(documentNamespace("thread_execution_resets"), id, &record)
		return err
	})
	return record.Generation > generation, err
}
