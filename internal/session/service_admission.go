package session

import (
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// The unused admission arguments preserve the session.domain@1 wire contract.
// Capacity policy is no longer provided by the session store.
func (s *documentStore) AdmitThreadExecution(id string, limit int, activeIDs, knownIDs []string) (int64, error) {
	var generation int64
	ownerID := NewID()
	err := s.transaction(func(tx *persistence.Tx) error {
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
