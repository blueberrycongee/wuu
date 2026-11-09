package sidethread

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

var ErrNotFound = errors.New("side thread not found")

const (
	recordNamespace   = "wuu.sidethread.record.v1"
	deletingNamespace = "wuu.sidethread.deleting.v1"
	legacyNamespace   = "wuu.sidethread.legacy.v1"
)

// Store captures the selected Documents authority. Its caller retains the
// owning generation until all turns and deletion stages have finished.
type Store struct {
	ctx       context.Context
	documents persistence.Documents
}

type DeleteStage struct {
	store  *Store
	key    string
	exists bool
}

func NewStore(ctx context.Context, documents persistence.Documents) *Store {
	return &Store{ctx: ctx, documents: documents}
}

// update retries an uncertain commit only with its original durable intent.
// It never reruns a domain mutation after a possibly successful commit.
func (s *Store) update(body func(*persistence.Tx) error) error {
	if s == nil || s.documents == nil {
		return errors.New("sidethread.Store: selected Documents is required")
	}
	err := persistence.Update(s.ctx, s.documents, body)
	var pending *persistence.CommitError
	if !errors.As(err, &pending) {
		return err
	}
	for attempt := 0; attempt < 2 && s.ctx.Err() == nil; attempt++ {
		if _, retryErr := s.documents.Commit(s.ctx, pending.Transaction); retryErr == nil {
			return nil
		} else {
			pending = &persistence.CommitError{Transaction: pending.Transaction, Err: retryErr}
		}
	}
	return pending
}

// record reads live and staged values at one revision. A staged value remains
// visible until main-thread deletion succeeds, including after a crash.
func record(tx *persistence.Tx, key string) (json.RawMessage, string, error) {
	var live, staged json.RawMessage
	a, err := tx.Get(recordNamespace, key, &live)
	if err != nil {
		return nil, "", err
	}
	b, err := tx.Get(deletingNamespace, key, &staged)
	if err != nil {
		return nil, "", err
	}
	if a && b {
		return nil, "", fmt.Errorf("side thread %s has both live and staged records", key)
	}
	if b {
		return staged, deletingNamespace, nil
	}
	if a {
		return live, recordNamespace, nil
	}
	return nil, recordNamespace, nil
}

func (s *Store) Load(mainThreadID string) (*SideThread, error) {
	key := normalizeKey(mainThreadID)
	if key == "" {
		return nil, ErrNotFound
	}
	var result *SideThread
	err := s.update(func(tx *persistence.Tx) error {
		raw, _, err := record(tx, key)
		if err != nil {
			return err
		}
		if raw == nil {
			return ErrNotFound
		}
		var st SideThread
		if err := json.Unmarshal(raw, &st); err != nil {
			return err
		}
		result = &st
		return nil
	})
	return result, err
}

func (s *Store) Exists(mainThreadID string) (bool, error) {
	_, err := s.Load(mainThreadID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) Save(st *SideThread) error {
	if st == nil {
		return errors.New("sidethread.Store: nil side thread")
	}
	key := normalizeKey(st.MainThreadID)
	if key == "" {
		return errors.New("sidethread.Store: main_thread_id is required")
	}
	input := *st
	now := time.Now().UTC()
	var saved SideThread
	err := s.update(func(tx *persistence.Tx) error {
		raw, namespace, err := record(tx, key)
		if err != nil {
			return err
		}
		var previous SideThread
		if raw != nil {
			if err := json.Unmarshal(raw, &previous); err != nil {
				return err
			}
		}
		saved = input
		if saved.CreatedAt.IsZero() {
			saved.CreatedAt = now
		}
		saved.UpdatedAt = now
		saved.Revision = max(input.Revision, previous.Revision) + 1
		return tx.Put(namespace, key, &saved)
	})
	if err == nil {
		*st = saved
	}
	return err
}

// Mutate may repeat fn on a rejected revision conflict; fn must have no
// external effects. An uncertain commit never repeats fn.
func (s *Store) Mutate(mainThreadID string, fn func(*SideThread) error) error {
	_, err := s.mutateRecord(mainThreadID, fn)
	return err
}
func (s *Store) mutateRecord(mainThreadID string, fn func(*SideThread) error) (*SideThread, error) {
	key := normalizeKey(mainThreadID)
	if key == "" {
		return nil, ErrNotFound
	}
	now := time.Now().UTC()
	var result *SideThread
	err := s.update(func(tx *persistence.Tx) error {
		raw, namespace, err := record(tx, key)
		if err != nil {
			return err
		}
		if raw == nil {
			return ErrNotFound
		}
		var st SideThread
		if err := json.Unmarshal(raw, &st); err != nil {
			return err
		}
		if fn != nil {
			if err := fn(&st); err != nil {
				return err
			}
		}
		st.UpdatedAt = now
		st.Revision++
		result = cloneSideThread(&st)
		return tx.Put(namespace, key, &st)
	})
	return result, err
}

func (s *Store) BeginTurn(mainThreadID, prompt, userMessageID, assistantMessageID string) (*SideThread, error) {
	return s.BeginTurnWithSideThreadID(mainThreadID, "", prompt, userMessageID, assistantMessageID)
}
func (s *Store) BeginTurnWithSideThreadID(mainThreadID, sideThreadID, prompt, userMessageID, assistantMessageID string) (*SideThread, error) {
	return s.BeginTurnWithSelection(mainThreadID, sideThreadID, prompt, userMessageID, assistantMessageID, nil)
}

// BeginTurnWithSelection requires the caller's durable side-thread execution
// lease. A previous running placeholder is therefore stale and is interrupted.
func (s *Store) BeginTurnWithSelection(mainThreadID, sideThreadID, prompt, userMessageID, assistantMessageID string, selection *SelectionReference) (*SideThread, error) {
	key := normalizeKey(mainThreadID)
	if key == "" {
		return nil, errors.New("sidethread.Store: main_thread_id is required")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, errors.New("sidethread.Store: prompt is required")
	}
	if strings.TrimSpace(userMessageID) == "" || strings.TrimSpace(assistantMessageID) == "" {
		return nil, errors.New("sidethread.Store: message ids are required")
	}
	sideThreadID = strings.TrimSpace(sideThreadID)
	if sideThreadID == "" {
		var err error
		sideThreadID, err = NewSideThreadID()
		if err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	var result *SideThread
	err := s.update(func(tx *persistence.Tx) error {
		raw, namespace, err := record(tx, key)
		if err != nil {
			return err
		}
		st := SideThread{SideThreadID: sideThreadID, MainThreadID: key}
		if raw != nil {
			if err := json.Unmarshal(raw, &st); err != nil {
				return err
			}
		}
		if st.SideThreadID == "" {
			st.SideThreadID = sideThreadID
		}
		if st.Status == StatusRunning {
			settleInterrupted(&st)
		}
		if st.CreatedAt.IsZero() {
			st.CreatedAt = now
		}
		st.MainThreadID = key
		st.Status = StatusRunning
		st.UpdatedAt = now
		st.Revision++
		st.Messages = append(st.Messages, Message{ID: userMessageID, SideThreadID: st.SideThreadID, Role: RoleUser, Text: prompt, Selection: selection, CreatedAt: now}, Message{ID: assistantMessageID, SideThreadID: st.SideThreadID, Role: RoleAssistant, Status: AssistantStreaming, CreatedAt: now})
		result = cloneSideThread(&st)
		return tx.Put(namespace, key, &st)
	})
	return result, err
}

// RollbackTurn removes an accepted turn whose execution never started.
func (s *Store) RollbackTurn(mainThreadID, userMessageID, assistantMessageID string) error {
	key := normalizeKey(mainThreadID)
	if key == "" {
		return nil
	}
	now := time.Now().UTC()
	return s.update(func(tx *persistence.Tx) error {
		raw, namespace, err := record(tx, key)
		if err != nil || raw == nil {
			return err
		}
		var st SideThread
		if err := json.Unmarshal(raw, &st); err != nil {
			return err
		}
		kept := make([]Message, 0, len(st.Messages))
		removed := false
		for _, message := range st.Messages {
			if message.ID == userMessageID || message.ID == assistantMessageID {
				removed = true
				continue
			}
			kept = append(kept, message)
		}
		if !removed {
			return nil
		}
		if len(kept) == 0 {
			return tx.Delete(namespace, key)
		}
		st.Messages = kept
		st.Status = threadStatusForMessages(kept)
		st.UpdatedAt = now
		st.Revision++
		return tx.Put(namespace, key, &st)
	})
}

// FinishTurn commits the terminal assistant message. An interrupt already
// persisted by another app-server wins over a late successful provider reply.
func (s *Store) FinishTurn(mainThreadID, assistantMessageID, text string, status Status, errorText string) (*SideThread, Message, error) {
	return s.FinishTurnWithItems(mainThreadID, assistantMessageID, text, nil, status, errorText)
}

// FinishTurnWithItems commits the terminal assistant message and the agent
// process items collected while it ran.
func (s *Store) FinishTurnWithItems(mainThreadID, assistantMessageID, text string, items []Item, status Status, errorText string) (*SideThread, Message, error) {
	var finished Message
	st, err := s.mutateRecord(mainThreadID, func(st *SideThread) error {
		persistedInterrupt := st.Status == StatusInterrupted
		effectiveStatus := status
		if persistedInterrupt {
			effectiveStatus = StatusInterrupted
		}
		finalText := text
		finalErrorText := errorText
		if persistedInterrupt && status != StatusInterrupted {
			// A peer interrupted before this owner observed the durable state.
			// Discard the owner's late final payload; it was produced after the
			// user-visible interrupt boundary.
			finalText = ""
		}
		if effectiveStatus == StatusInterrupted {
			finalErrorText = ""
		}
		assistantStatus, err := assistantStatusForThreadStatus(effectiveStatus)
		if err != nil {
			return err
		}
		for i := range st.Messages {
			if st.Messages[i].ID != assistantMessageID || st.Messages[i].Role != RoleAssistant {
				continue
			}
			st.Messages[i].Text = finalText
			st.Messages[i].Items = append([]Item(nil), items...)
			st.Messages[i].Status = assistantStatus
			st.Messages[i].ErrorText = finalErrorText
			finished = st.Messages[i]
			st.Status = effectiveStatus
			return nil
		}
		return fmt.Errorf("assistant message %q not found", assistantMessageID)
	})
	if err != nil {
		return nil, Message{}, err
	}
	return st, finished, nil
}

// Interrupt marks the active assistant placeholder and side thread terminal.
// It is safe to call from an app-server that does not own the provider request;
// FinishTurn preserves this terminal state when the owner eventually returns.
func (s *Store) Interrupt(mainThreadID string) (*SideThread, bool, error) {
	changed := false
	st, err := s.mutateRecord(mainThreadID, func(st *SideThread) error {
		changed = false
		if st.Status != StatusRunning {
			return nil
		}
		settleInterrupted(st)
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return st, changed, nil
}

// RecoverRunning requires exclusive workspace boot recovery ownership.
func (s *Store) RecoverRunning() (int, error) {
	now := time.Now().UTC()
	recovered := 0
	err := s.update(func(tx *persistence.Tx) error {
		recovered = 0
		seen := map[string]bool{}
		for _, namespace := range []string{recordNamespace, deletingNamespace} {
			docs, err := tx.Read(persistence.Query{Namespace: namespace})
			if err != nil {
				return err
			}
			for _, doc := range docs {
				if seen[doc.Key] {
					return fmt.Errorf("side thread %s has both live and staged records", doc.Key)
				}
				seen[doc.Key] = true
				var st SideThread
				if err := json.Unmarshal(doc.Value, &st); err != nil {
					return err
				}
				if st.Status != StatusRunning {
					continue
				}
				settleInterrupted(&st)
				st.UpdatedAt = now
				st.Revision++
				if err := tx.Put(namespace, doc.Key, &st); err != nil {
					return err
				}
				recovered++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return recovered, nil
}

func (s *Store) Delete(mainThreadID string) error {
	key := normalizeKey(mainThreadID)
	if key == "" {
		return nil
	}
	return s.update(func(tx *persistence.Tx) error {
		if err := tx.Delete(recordNamespace, key); err != nil {
			return err
		}
		return tx.Delete(deletingNamespace, key)
	})
}

// BeginDelete stages a reversible deletion at the same authority as the turn.
func (s *Store) BeginDelete(mainThreadID string) (*DeleteStage, error) {
	key := normalizeKey(mainThreadID)
	stage := &DeleteStage{store: s, key: key}
	if key == "" {
		return stage, nil
	}
	err := s.update(func(tx *persistence.Tx) error {
		raw, namespace, err := record(tx, key)
		if err != nil {
			return err
		}
		stage.exists = raw != nil
		if raw == nil || namespace == deletingNamespace {
			return nil
		}
		if err := tx.Put(deletingNamespace, key, raw); err != nil {
			return err
		}
		return tx.Delete(recordNamespace, key)
	})
	return stage, err
}
func (d *DeleteStage) Rollback() error {
	if d == nil || !d.exists {
		return nil
	}
	return d.store.update(func(tx *persistence.Tx) error {
		raw, namespace, err := record(tx, d.key)
		if err != nil {
			return err
		}
		if raw == nil {
			return fmt.Errorf("cannot roll back side thread %s delete: missing staged state", d.key)
		}
		if namespace == recordNamespace {
			return nil
		}
		if err := tx.Put(recordNamespace, d.key, raw); err != nil {
			return err
		}
		return tx.Delete(deletingNamespace, d.key)
	})
}
func (d *DeleteStage) Commit() error {
	if d == nil || !d.exists {
		return nil
	}
	return d.store.update(func(tx *persistence.Tx) error {
		raw, namespace, err := record(tx, d.key)
		if err != nil {
			return err
		}
		if raw == nil {
			return nil
		}
		if namespace == recordNamespace {
			return fmt.Errorf("cannot commit side thread %s delete while a live record exists", d.key)
		}
		return tx.Delete(deletingNamespace, d.key)
	})
}

// NewSideThreadID generates a fresh hex-encoded side thread id (16 hex
// chars / 8 random bytes). Enough entropy to make collisions negligible
// across the lifetime of a single session dir while staying short
// enough for human-readable logs.
func NewSideThreadID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate side thread id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// NewMessageID returns a durable side-thread message id.
func NewMessageID() (string, error) {
	id, err := NewSideThreadID()
	if err != nil {
		return "", err
	}
	return "sm_" + id, nil
}

func settleInterrupted(st *SideThread) {
	if st == nil {
		return
	}
	st.Status = StatusInterrupted
	for i := range st.Messages {
		if st.Messages[i].Role == RoleAssistant && st.Messages[i].Status == AssistantStreaming {
			st.Messages[i].Status = AssistantInterrupted
		}
	}
}

// threadStatusForMessages restores the thread status implied by the most
// recent assistant reply. A leftover streaming placeholder maps to
// interrupted, mirroring settleInterrupted.
func threadStatusForMessages(messages []Message) Status {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != RoleAssistant {
			continue
		}
		switch messages[i].Status {
		case AssistantCompleted:
			return StatusCompleted
		case AssistantFailed:
			return StatusFailed
		}
		return StatusInterrupted
	}
	return StatusInterrupted
}

func assistantStatusForThreadStatus(status Status) (AssistantMessageStatus, error) {
	switch status {
	case StatusCompleted:
		return AssistantCompleted, nil
	case StatusFailed:
		return AssistantFailed, nil
	case StatusInterrupted:
		return AssistantInterrupted, nil
	default:
		return "", fmt.Errorf("side thread status %q is not terminal", status)
	}
}

func cloneSideThread(st *SideThread) *SideThread {
	if st == nil {
		return nil
	}
	cloned := *st
	cloned.Messages = append([]Message(nil), st.Messages...)
	return &cloned
}

func normalizeKey(mainThreadID string) string {
	trimmed := strings.TrimSpace(mainThreadID)
	if trimmed == "" || strings.ContainsAny(trimmed, `/\`) {
		return ""
	}
	return trimmed
}
