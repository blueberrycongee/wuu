package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// InferenceJournalRuntime holds one generation's authority for its lifetime.
// Its owner releases the provider only after Close drains admitted journal work.
type InferenceJournalRuntime struct {
	store                             *Store
	workspaceScope, runtimeID         string
	stop, heartbeatDone, progressDone chan struct{}
	closeOnce                         sync.Once
	lifecycleMu                       sync.Mutex
	closing                           bool
	activeWG                          sync.WaitGroup
	closeErr                          error
	progressMu                        sync.Mutex
	progressFlushMu                   sync.Mutex
	progress                          map[string]documentSubmissionProgress
	progressErr                       error
}

// InferenceRuntimeRecord is the durable process lease shared by inference and execution recovery.
type InferenceRuntimeRecord struct {
	ID, WorkspaceScope               string
	PID                              int
	StartedAt, HeartbeatAt, ClosedAt int64
}

func ReadInferenceRuntime(tx *persistence.Tx, id string) (InferenceRuntimeRecord, error) {
	return requireInferenceDocument[InferenceRuntimeRecord](tx, "inference_journal_runtimes", id)
}

type documentSubmissionProgress struct {
	journal *documentInferenceJournal
	record  providers.InferenceSubmissionJournalRecord
}

type documentInferenceJournal struct {
	runtime *InferenceJournalRuntime
	ownerID string
}

func (s *Store) NewInferenceJournalRuntime(workspaceScope string) (*InferenceJournalRuntime, error) {
	workspaceScope = journalText(workspaceScope, 512)
	if workspaceScope == "" {
		return nil, errors.New("inference journal workspace scope is required")
	}
	id, now := newInferenceRuntimeID(), time.Now().UTC().UnixMilli()
	if err := s.transaction(func(tx *persistence.Tx) error {
		return tx.Put(documentNamespace("inference_journal_runtimes"), id, InferenceRuntimeRecord{
			ID: id, WorkspaceScope: workspaceScope, PID: os.Getpid(), StartedAt: now, HeartbeatAt: now,
		})
	}); err != nil {
		return nil, fmt.Errorf("register inference journal runtime: %w", err)
	}
	r := &InferenceJournalRuntime{store: s.WithContext(context.Background()), workspaceScope: workspaceScope, runtimeID: id,
		stop: make(chan struct{}), heartbeatDone: make(chan struct{}), progressDone: make(chan struct{})}
	go func() {
		defer close(r.heartbeatDone)
		ticker := time.NewTicker(inferenceJournalHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case at := <-ticker.C:
				if err := r.store.transaction(func(tx *persistence.Tx) error { return r.heartbeat(tx, at.UnixMilli(), false) }); err != nil {
					providers.DebugLogf("inference journal heartbeat update: %v", err)
				}
			case <-r.stop:
				return
			}
		}
	}()
	go func() {
		defer close(r.progressDone)
		ticker := time.NewTicker(inferenceJournalProgressFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.flushSubmissionProgress(false)
			case <-r.stop:
				r.flushSubmissionProgress(true)
				return
			}
		}
	}()
	return r, nil
}

func (r *InferenceJournalRuntime) RuntimeID() string {
	if r == nil {
		return ""
	}
	return r.runtimeID
}

func (r *InferenceJournalRuntime) beginUse() (func(), error) {
	if r == nil {
		return nil, errors.New("inference journal runtime is not initialized")
	}
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.closing {
		return nil, errors.New("inference journal runtime is closed")
	}
	r.activeWG.Add(1)
	return r.activeWG.Done, nil
}

func (r *InferenceJournalRuntime) heartbeat(tx *persistence.Tx, stamp int64, closing bool) error {
	var record InferenceRuntimeRecord
	found, err := tx.Get(documentNamespace("inference_journal_runtimes"), r.runtimeID, &record)
	if err != nil {
		return err
	}
	if !found || record.ClosedAt != 0 {
		return errors.New("inference journal runtime lease is closed")
	}
	record.HeartbeatAt = stamp
	if closing {
		record.ClosedAt = stamp
	}
	return tx.Put(documentNamespace("inference_journal_runtimes"), r.runtimeID, record)
}

func (r *InferenceJournalRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.lifecycleMu.Lock()
		r.closing = true
		r.lifecycleMu.Unlock()
		r.activeWG.Wait()
		close(r.stop)
		<-r.heartbeatDone
		<-r.progressDone
		stamp := time.Now().UTC().UnixMilli()
		r.closeErr = r.store.transaction(func(tx *persistence.Tx) error { return r.heartbeat(tx, stamp, true) })
	})
	return r.closeErr
}

func (r *InferenceJournalRuntime) ForOwner(ownerID string) providers.InferenceJournal {
	if r == nil {
		return nil
	}
	ownerID = journalText(ownerID, 512)
	if ownerID == "" {
		ownerID = "workspace-runtime"
	}
	return &documentInferenceJournal{runtime: r, ownerID: ownerID}
}

func (j *documentInferenceJournal) write(action string, body func(*persistence.Tx) error) error {
	return j.writeContext(context.Background(), action, body)
}

func (j *documentInferenceJournal) writeContext(ctx context.Context, action string, body func(*persistence.Tx) error) error {
	if j == nil || j.runtime == nil {
		return fmt.Errorf("%s: inference journal is not initialized", action)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	done, err := j.runtime.beginUse()
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	defer done()
	stamp := time.Now().UTC().UnixMilli()
	err = j.runtime.store.WithContext(ctx).transaction(func(tx *persistence.Tx) error {
		if err := j.runtime.heartbeat(tx, stamp, false); err != nil {
			return err
		}
		return body(tx)
	})
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}

func (j *documentInferenceJournal) RecordSubmissionProgress(record providers.InferenceSubmissionJournalRecord) {
	if j == nil || j.runtime == nil || strings.TrimSpace(record.ID) == "" {
		return
	}
	r := j.runtime
	done, err := r.beginUse()
	if err != nil {
		return
	}
	defer done()
	normalizeSubmissionDocument(&record)
	r.progressMu.Lock()
	if r.progress == nil {
		r.progress = make(map[string]documentSubmissionProgress)
	}
	r.progress[record.ID] = documentSubmissionProgress{journal: j, record: record}
	r.progressMu.Unlock()
}

func (r *InferenceJournalRuntime) flushSubmissionProgress(allowClosing bool) {
	if r == nil {
		return
	}
	if !allowClosing {
		done, err := r.beginUse()
		if err != nil {
			return
		}
		defer done()
	}
	// A terminal barrier also waits for a batch already claimed by the
	// background writer. An empty pending map alone does not mean durable.
	r.progressFlushMu.Lock()
	defer r.progressFlushMu.Unlock()
	r.progressMu.Lock()
	batch := r.progress
	r.progress = nil
	r.progressMu.Unlock()
	if len(batch) == 0 {
		return
	}
	stamp := time.Now().UTC().UnixMilli()
	err := r.store.transaction(func(tx *persistence.Tx) error {
		if err := r.heartbeat(tx, stamp, false); err != nil {
			return err
		}
		for _, pending := range batch {
			if err := pending.journal.upsertSubmission(tx, pending.record); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		r.progressMu.Lock()
		r.progressErr = err
		r.progressMu.Unlock()
		providers.DebugLogf("inference journal: submission progress flush degraded (%d records): %v", len(batch), err)
	}
}

func (r *InferenceJournalRuntime) pendingProgressErr() error {
	r.progressMu.Lock()
	defer r.progressMu.Unlock()
	return r.progressErr
}
