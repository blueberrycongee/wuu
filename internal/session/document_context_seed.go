package session

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func prepareContextSeed(seed ContextSeed, now time.Time) ContextSeed {
	if strings.TrimSpace(seed.ID) == "" {
		seed.ID = NewID()
	}
	if strings.TrimSpace(seed.Provenance.CreatedAt) == "" {
		seed.Provenance.CreatedAt = now.Format(time.RFC3339Nano)
	}
	return seed
}

func putContextSeedDocument(tx *persistence.Tx, seed ContextSeed) (ContextSeed, error) {
	if err := ValidateContextSeed(seed); err != nil {
		return ContextSeed{}, err
	}
	if _, err := requireSessionDocument(tx, seed.Source.SessionID); err != nil {
		return ContextSeed{}, err
	}
	head, err := historyHeadDocument(tx, seed.Source.SessionID)
	if err != nil {
		return ContextSeed{}, err
	}
	if seed.Source.ThroughSeq > head {
		return ContextSeed{}, fmt.Errorf("%w: source cutoff %d is beyond head %d", ErrHistorySnapshotGone, seed.Source.ThroughSeq, head)
	}
	var existing ContextSeed
	found, err := tx.Get(documentNamespace("context_seeds"), seed.ID, &existing)
	if err != nil {
		return ContextSeed{}, err
	}
	if found {
		if existing.Body != seed.Body || existing.Source != seed.Source {
			return ContextSeed{}, fmt.Errorf("%w: seed %q already exists with different content", ErrSessionLaunchConflict, seed.ID)
		}
		return existing, nil
	}
	return seed, tx.Put(documentNamespace("context_seeds"), seed.ID, seed)
}

func (s *documentStore) PutContextSeed(seed ContextSeed) (ContextSeed, error) {
	seed = prepareContextSeed(seed, time.Now().UTC())
	var result ContextSeed
	err := s.transaction(func(tx *persistence.Tx) error {
		var err error
		result, err = putContextSeedDocument(tx, seed)
		return err
	})
	return result, err
}

func (s *documentStore) LoadContextSeed(id string) (ContextSeed, bool, error) {
	var result ContextSeed
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		var err error
		found, err = tx.Get(documentNamespace("context_seeds"), id, &result)
		return err
	})
	return result, found, err
}

func prepareSessionLaunch(record SessionLaunchRecord, now time.Time) SessionLaunchRecord {
	record.RequestID = strings.TrimSpace(record.RequestID)
	if record.Revision < 1 {
		record.Revision = 1
	}
	if strings.TrimSpace(record.Kind) == "" {
		record.Kind = SessionLaunchKindHandoff
	}
	if strings.TrimSpace(record.Status) == "" {
		record.Status = SessionLaunchDraft
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	if record.CommittedAt == nil && record.Status == SessionLaunchCommitted {
		record.CommittedAt = &now
	}
	return record
}

func putSessionLaunchDocument(tx *persistence.Tx, record SessionLaunchRecord) (SessionLaunchRecord, error) {
	if record.RequestID == "" {
		return SessionLaunchRecord{}, errors.New("launch request_id is required")
	}
	var existing SessionLaunchRecord
	found, err := tx.Get(documentNamespace("session_launches"), record.RequestID, &existing)
	if err != nil {
		return SessionLaunchRecord{}, err
	}
	if found {
		if existing.Revision != record.Revision || !existing.Runtime.equal(record.Runtime) || existing.SourceSession != record.SourceSession || existing.SourceCutoff != record.SourceCutoff {
			return SessionLaunchRecord{}, fmt.Errorf("%w: request %q", ErrSessionLaunchConflict, record.RequestID)
		}
		if existing.Status == SessionLaunchCommitted && record.Status != SessionLaunchCommitted {
			return existing, nil
		}
		existing.Status, existing.SeedID, existing.TargetSession = record.Status, record.SeedID, record.TargetSession
		existing.InitialTurnID, existing.Error, existing.UpdatedAt = record.InitialTurnID, record.Error, record.UpdatedAt
		if existing.CommittedAt == nil {
			existing.CommittedAt = record.CommittedAt
		}
		record = existing
	}
	return record, tx.Put(documentNamespace("session_launches"), record.RequestID, record)
}

func (s *documentStore) PutSessionLaunch(record SessionLaunchRecord) (SessionLaunchRecord, error) {
	record = prepareSessionLaunch(record, time.Now().UTC())
	var result SessionLaunchRecord
	err := s.transaction(func(tx *persistence.Tx) error {
		var err error
		result, err = putSessionLaunchDocument(tx, record)
		return err
	})
	return result, err
}

func (s *documentStore) LoadSessionLaunch(requestID string) (SessionLaunchRecord, bool, error) {
	var result SessionLaunchRecord
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		var err error
		found, err = tx.Get(documentNamespace("session_launches"), requestID, &result)
		return err
	})
	return result, found, err
}
