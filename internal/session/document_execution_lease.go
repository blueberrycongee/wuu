package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/securefs"
)

// DocumentThreadExecutionLease retains a captured authority and an OS lease.
// The descriptor, never a heartbeat timeout, determines execution ownership.
type DocumentThreadExecutionLease struct {
	mu              sync.Mutex
	file            *os.File
	store           *Store
	threadID        string
	resetGeneration int64
}

type threadExecutionDocument struct {
	ThreadID                string
	Generation, RequestedAt int64
	OwnerID                 string
}

// Admission and reset share a local process lock independently of the selected
// document backend. Store revision CAS also fences concurrent membership edits.
// Lock acquisition and probes stay outside retried document callbacks.
func acquireThreadAdmission(ctx context.Context, dir string) (*os.File, error) {
	file, err := securefs.OpenFile(filepath.Join(dir, threadExecutionLeaseDirName, "admission.lock"), os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		acquired, err := tryLockThreadExecutionFile(file)
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		if acquired {
			return file, nil
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Store) TryAcquireThreadExecutionLease(sessDir, threadID string) (*DocumentThreadExecutionLease, bool, error) {
	return s.TryAcquireThreadExecutionLeaseWithProjectLimit(sessDir, threadID, 0)
}

func (s *Store) TryAcquireThreadExecutionLeaseWithProjectLimit(sessDir, threadID string, limit int) (*DocumentThreadExecutionLease, bool, error) {
	sessDir, threadID = strings.TrimSpace(sessDir), strings.TrimSpace(threadID)
	if sessDir == "" || threadID == "" {
		return nil, false, errors.New("session directory and thread id are required")
	}
	gate, err := acquireThreadAdmission(s.ctx, sessDir)
	if err != nil {
		return nil, false, err
	}
	defer gate.Close()
	defer unlockThreadExecutionFile(gate)
	file, err := securefs.OpenFile(threadExecutionLeasePath(sessDir, threadID), os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, false, err
	}
	acquired, err := tryLockThreadExecutionFile(file)
	if err != nil || !acquired {
		_ = file.Close()
		return nil, false, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = unlockThreadExecutionFile(file)
			_ = file.Close()
		}
	}()
	ownerID, operationID := NewID(), NewID()

attempt:
	for {
		resetView, err := s.documents.Read(s.ctx, persistence.Query{Namespace: documentNamespace("thread_execution_resets"), Keys: []string{threadID}})
		if err != nil {
			return nil, false, err
		}
		record := threadExecutionDocument{ThreadID: threadID}
		if len(resetView.Documents) != 0 {
			if err := json.Unmarshal(resetView.Documents[0].Value, &record); err != nil {
				return nil, false, err
			}
		}
		if limit > 0 {
			query := persistence.Query{Namespace: documentNamespace("sessions"), Limit: 256, AtRevision: &resetView.Revision}
			var members []persistence.Document
			for {
				view, err := s.documents.Read(s.ctx, query)
				if errors.Is(err, persistence.ErrConflict) {
					continue attempt
				}
				if err != nil {
					return nil, false, err
				}
				members = append(members, view.Documents...)
				if view.NextCursor == "" {
					break
				}
				query.Cursor = view.NextCursor
			}

			if err := checkDocumentProjectCapacity(members, sessDir, threadID, limit); err != nil {
				return nil, false, err
			}
		}
		record.OwnerID = ownerID
		value, err := json.Marshal(record)
		if err != nil {
			return nil, false, err
		}
		request := persistence.Transaction{ID: operationID, ExpectedRevision: resetView.Revision, Writes: []persistence.Write{{Namespace: documentNamespace("thread_execution_resets"), Key: threadID, Value: value}}}
		if _, err := s.documents.Commit(s.ctx, request); err != nil {
			if errors.Is(err, persistence.ErrConflict) {
				continue
			}
			return nil, false, &persistence.CommitError{Transaction: request, Err: err}
		}
		keep = true
		return &DocumentThreadExecutionLease{file: file, store: s.WithContext(context.Background()), threadID: threadID, resetGeneration: record.Generation}, true, nil
	}
}

func checkDocumentProjectCapacity(rows []persistence.Document, dir, threadID string, limit int) error {
	members := make([]Session, 0, len(rows))
	projectID := ""
	for _, row := range rows {
		var member Session
		if err := json.Unmarshal(row.Value, &member); err != nil {
			return err
		}
		if member.Source != "project-session" || member.ParentID == "" || member.ArchivedAt != nil {
			continue
		}
		switch member.ProjectRole {
		case "", "worker", "executor", "technical_lead":
		default:
			continue
		}
		members = append(members, member)
		if member.ID == threadID {
			projectID = member.ParentID
		}
	}
	if projectID == "" {
		return nil
	}
	occupied := 0
	for _, member := range members {
		if member.ID == threadID || member.ParentID != projectID {
			continue
		}
		active, err := ThreadExecutionActive(dir, member.ID)
		if err != nil {
			return err
		}
		if active {
			occupied++
		}
		if occupied >= limit {
			return fmt.Errorf("project %q worker limit %d reached: %w", projectID, limit, ErrProjectWorkerCapacity)
		}
	}
	return nil
}

func (s *Store) RequestThreadExecutionReset(sessDir, threadID string) (bool, error) {
	sessDir, threadID = strings.TrimSpace(sessDir), strings.TrimSpace(threadID)
	if sessDir == "" || threadID == "" {
		return false, errors.New("session directory and thread id are required")
	}
	gate, err := acquireThreadAdmission(s.ctx, sessDir)
	if err != nil {
		return false, err
	}
	defer gate.Close()
	defer unlockThreadExecutionFile(gate)
	active, err := ThreadExecutionActive(sessDir, threadID)
	if err != nil || !active {
		return false, err
	}
	stamp := time.Now().UTC().UnixMilli()
	err = s.transaction(func(tx *persistence.Tx) error {
		var record threadExecutionDocument
		_, err := tx.Get(documentNamespace("thread_execution_resets"), threadID, &record)
		if err != nil {
			return err
		}
		record.ThreadID, record.Generation, record.RequestedAt = threadID, record.Generation+1, stamp
		return tx.Put(documentNamespace("thread_execution_resets"), threadID, record)
	})
	return err == nil, err
}

func (l *DocumentThreadExecutionLease) ResetRequested() (bool, error) {
	if l == nil {
		return false, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return false, nil
	}
	var record threadExecutionDocument
	err := l.store.transaction(func(tx *persistence.Tx) error {
		record = threadExecutionDocument{}
		_, err := tx.Get(documentNamespace("thread_execution_resets"), l.threadID, &record)
		return err
	})
	return record.Generation > l.resetGeneration, err
}

func (l *DocumentThreadExecutionLease) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	file := l.file
	l.file, l.store = nil, nil
	return errors.Join(unlockThreadExecutionFile(file), file.Close())
}
