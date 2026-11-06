// Package durablewait implements optional expiring wait metadata. A decision is
// not a permission grant. Ordinary execution authorization remains mandatory.
package durablewait

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

const Module = "wuu.durable-wait"
const ServiceName = "host.durable-wait"
const Namespace = "wuu.durable_waits.v1"

var (
	ErrInvalid  = errors.New("invalid wait intent")
	ErrOwner    = errors.New("wait owner mismatch")
	ErrConflict = errors.New("wait state conflicts with operation")
	ErrExpired  = errors.New("wait expired")
	ErrUnknown  = errors.New("effect may have started")
	ErrNotFound = errors.New("wait not found")
)

type Owner struct {
	PluginID    string `json:"plugin_id"`
	SessionID   string `json:"session_id"`
	ExecutionID string `json:"execution_id"`
	CallID      string `json:"call_id"`
}
type InputRef struct {
	SessionID string `json:"session_id"`
	Seq       int    `json:"seq"`
}

type Intent struct {
	InputBoundary *InputRef `json:"input_boundary,omitempty"`
	Version       int       `json:"version"`
	ID            string    `json:"id"`
	ExpiresAt     time.Time `json:"expires_at"`
	Question      string    `json:"question"`
	Kind          string    `json:"kind,omitempty"`
	BatchID       string    `json:"batch_id,omitempty"`
	CallsDigest   string    `json:"calls_digest,omitempty"`
}
type Record struct {
	ResponseRef *InputRef `json:"response_ref,omitempty"`
	Intent
	Owner       Owner      `json:"owner"`
	Status      string     `json:"status"`
	EffectState string     `json:"effect_state"`
	CreatedAt   time.Time  `json:"created_at"`
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
	Decision    string     `json:"decision,omitempty"`
}
type Store struct {
	documents persistence.Documents
	now       func() time.Time
}

func New(documents persistence.Documents, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{documents: documents, now: now}
}
func validOwner(owner Owner) bool {
	return owner.PluginID != "" && owner.SessionID != "" && owner.ExecutionID != "" && owner.CallID != ""
}

// Register records an ordinary wait without asserting anything about effects.
func (s *Store) Register(ctx context.Context, owner Owner, intent Intent) (Record, error) {
	return s.register(ctx, owner, intent, "unknown")
}

// RegisterBeforeDispatch is exclusively for the host's pre-effect gate. Every
// path capable of dispatching that effect must call BeginDispatch on this record.
// Do not expose this method to callers that can merely assert an effect state.
func (s *Store) RegisterBeforeDispatch(ctx context.Context, owner Owner, intent Intent) (Record, error) {
	return s.register(ctx, owner, intent, "not_started")
}
func (s *Store) register(ctx context.Context, owner Owner, intent Intent, effect string) (Record, error) {
	intent.ExpiresAt = intent.ExpiresAt.UTC().Round(0)
	if (intent.Kind != "" && intent.Kind != "approval" && intent.Kind != "input" && intent.Kind != "event") || !validOwner(owner) || intent.Version != 1 || intent.ID == "" || len(intent.ID) > 1024 || intent.Question == "" || len(intent.Question) > 65536 || intent.ExpiresAt.IsZero() {
		return Record{}, ErrInvalid
	}
	return s.update(ctx, owner, intent.ID, func(record *Record, exists bool) error {
		if exists {
			if !reflect.DeepEqual(record.Intent, intent) {
				return ErrConflict
			}
			return nil
		}
		now := s.now()
		if !now.Before(intent.ExpiresAt) {
			return ErrExpired
		}
		*record = Record{Intent: intent, Owner: owner, Status: "pending", EffectState: effect, CreatedAt: now}
		return nil
	})
}
func (s *Store) Inspect(ctx context.Context, owner Owner, id string) (Record, error) {
	record, _, err := s.read(ctx, owner, id)
	if err != nil {
		return Record{}, err
	}
	if record.ID == "" {
		return Record{}, ErrNotFound
	}
	if (record.Status == "pending" || record.Status == "approved") && !s.now().Before(record.ExpiresAt) {
		record.Status = "expired"
	}
	return record, nil
}
func (s *Store) Decide(ctx context.Context, owner Owner, id, decision string) (Record, error) {
	if decision != "approve" && decision != "deny" {
		return Record{}, ErrInvalid
	}
	return s.update(ctx, owner, id, func(record *Record, exists bool) error {
		if !exists {
			return ErrNotFound
		}
		if !s.now().Before(record.ExpiresAt) {
			return ErrExpired
		}
		status := "approved"
		if decision == "deny" {
			status = "denied"
		}
		if record.Status == status {
			return nil
		}
		if record.Status != "pending" {
			return ErrConflict
		}
		now := s.now()
		record.Status = status
		record.DecidedAt = &now
		record.Decision = decision
		return nil
	})
}

// Fence revokes this attempt atomically with dispatch admission. Expiry and a
// disconnected caller alone never constitute a fence or evidence of nonexecution.
func (s *Store) Fence(ctx context.Context, owner Owner, id string) (Record, error) {
	return s.update(ctx, owner, id, func(record *Record, exists bool) error {
		if !exists {
			return ErrNotFound
		}
		if record.EffectState != "not_started" {
			return ErrUnknown
		}
		if record.Status == "denied" {
			return ErrConflict
		}
		record.Status = "fenced"
		return nil
	})
}

// BeginDispatch consumes the gate once. Persisting unknown BEFORE dispatch
// deliberately sacrifices automatic retry if the host crashes at this boundary.
func (s *Store) BeginDispatch(ctx context.Context, owner Owner, id string) (Record, error) {
	return s.update(ctx, owner, id, func(record *Record, exists bool) error {
		if !exists {
			return ErrNotFound
		}
		if record.EffectState != "not_started" {
			return ErrUnknown
		}
		if record.Status != "approved" {
			return ErrConflict
		}
		if !s.now().Before(record.ExpiresAt) {
			return ErrExpired
		}
		record.EffectState = "unknown"
		return nil
	})
}
func (s *Store) read(ctx context.Context, owner Owner, id string) (Record, persistence.Revision, error) {
	if !validOwner(owner) || id == "" {
		return Record{}, 0, ErrInvalid
	}
	view, err := s.documents.Read(ctx, persistence.Query{Namespace: Namespace, Keys: []string{id}})
	if err != nil {
		return Record{}, 0, err
	}
	if len(view.Documents) == 0 {
		return Record{}, view.Revision, nil
	}
	var record Record
	if err := json.Unmarshal(view.Documents[0].Value, &record); err != nil {
		return Record{}, 0, err
	}
	if record.Version != 1 || record.ID != id {
		return Record{}, 0, ErrInvalid
	}
	if record.Owner != owner {
		return Record{}, 0, ErrOwner
	}
	return record, view.Revision, nil
}
func (s *Store) update(ctx context.Context, owner Owner, id string, change func(*Record, bool) error) (Record, error) {
	for attempt := 0; attempt < 16; attempt++ {
		record, revision, err := s.read(ctx, owner, id)
		if err != nil {
			return Record{}, err
		}
		before := record
		if err := change(&record, record.ID != ""); err != nil {
			return Record{}, err
		}
		if reflect.DeepEqual(before, record) {
			return record, nil
		}
		raw, err := json.Marshal(record)
		if err != nil {
			return Record{}, err
		}
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return Record{}, err
		}
		_, err = s.documents.Commit(ctx, persistence.Transaction{ID: "durable-wait/" + hex.EncodeToString(nonce[:]), ExpectedRevision: revision, Writes: []persistence.Write{{Namespace: Namespace, Key: id, Value: raw}}})
		if errors.Is(err, persistence.ErrConflict) {
			continue
		}
		if err != nil {
			return Record{}, err
		}
		return record, nil
	}
	return Record{}, fmt.Errorf("%w: state contention", ErrConflict)
}

// Lookup is for host-scoped adapters. Plugin-facing callers must validate the
// retained session/provider before exposing the record.
func (s *Store) Lookup(ctx context.Context, id string) (Record, error) {
	view, err := s.documents.Read(ctx, persistence.Query{Namespace: Namespace, Keys: []string{id}})
	if err != nil {
		return Record{}, err
	}
	if len(view.Documents) != 1 {
		return Record{}, ErrNotFound
	}
	var record Record
	if err := json.Unmarshal(view.Documents[0].Value, &record); err != nil {
		return Record{}, err
	}
	return s.Inspect(ctx, record.Owner, id)
}
func (s *Store) List(ctx context.Context, sessionID string) ([]Record, error) {
	view, err := s.documents.Read(ctx, persistence.Query{Namespace: Namespace})
	if err != nil {
		return nil, err
	}
	records := []Record{}
	for _, doc := range view.Documents {
		var record Record
		if err := json.Unmarshal(doc.Value, &record); err != nil {
			return nil, err
		}
		if record.Owner.SessionID == sessionID {
			if (record.Status == "pending" || record.Status == "approved") && !s.now().Before(record.ExpiresAt) {
				record.Status = "expired"
			}
			records = append(records, record)
		}
	}
	return records, nil
}

// ResolveInput consumes only the host-authenticated source of a later physical
// turn. It stores a reference to ordinary host history, never a second body.
func (s *Store) ResolveInput(ctx context.Context, current Owner, id string, source InputRef) (Record, error) {
	record, err := s.Lookup(ctx, id)
	if err != nil {
		return Record{}, err
	}
	if current.PluginID != record.Owner.PluginID || current.SessionID != record.Owner.SessionID || source.SessionID != record.Owner.SessionID {
		return Record{}, ErrOwner
	}
	return s.update(ctx, record.Owner, id, func(record *Record, exists bool) error {
		if !exists {
			return ErrNotFound
		}
		if record.Kind != "input" && record.Kind != "event" {
			return ErrConflict
		}
		if record.Status == "resolved" {
			if record.ResponseRef == nil || *record.ResponseRef != source {
				return ErrConflict
			}
			return nil
		}
		if !s.now().Before(record.ExpiresAt) {
			return ErrExpired
		}
		if record.Status != "pending" {
			return ErrConflict
		}
		if record.InputBoundary == nil || record.InputBoundary.SessionID != source.SessionID {
			return ErrInvalid
		}
		if current.ExecutionID == record.Owner.ExecutionID || source.Seq <= record.InputBoundary.Seq {
			return nil
		}
		now := s.now()
		record.Status = "resolved"
		record.DecidedAt = &now
		record.ResponseRef = &source
		return nil
	})
}
