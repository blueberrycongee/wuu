package durablewait

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"reflect"
)

const GateNamespace = "wuu.durable_wait_batches.v1"

type batchGate struct {
	WaitID  string `json:"wait_id"`
	Digest  string `json:"digest"`
	Started bool   `json:"started"`
}

func batchKey(owner Owner, batch string) string {
	raw, _ := json.Marshal([]string{owner.PluginID, owner.SessionID, batch})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ReserveBatch atomically reserves a batch identity and its expiring wait. A
// new attempt may replace an old one only after a positive pre-effect fence.
func (s *Store) ReserveBatch(ctx context.Context, owner Owner, intent Intent, fresh bool) (Record, error) {
	intent.ExpiresAt = intent.ExpiresAt.UTC().Round(0)
	if intent.Kind != "approval" || !validOwner(owner) || intent.Version != 1 || intent.ID == "" || len(intent.ID) > 1024 || intent.Question == "" || len(intent.Question) > 65536 || intent.BatchID == "" || intent.CallsDigest == "" || !s.now().Before(intent.ExpiresAt) {
		return Record{}, ErrInvalid
	}
	for n := 0; n < 16; n++ {
		view, err := s.documents.Read(ctx, persistence.Query{Namespace: GateNamespace, Keys: []string{batchKey(owner, intent.BatchID)}})
		if err != nil {
			return Record{}, err
		}
		if len(view.Documents) > 0 {
			var gate batchGate
			if err := json.Unmarshal(view.Documents[0].Value, &gate); err != nil {
				return Record{}, err
			}
			if gate.Started {
				return Record{}, ErrUnknown
			}
			if gate.Digest != intent.CallsDigest {
				return Record{}, ErrConflict
			}
			old, err := s.Lookup(ctx, gate.WaitID)
			if err != nil {
				return Record{}, err
			}
			if gate.WaitID == intent.ID {
				if !reflect.DeepEqual(old.Intent, intent) || old.Owner.PluginID != owner.PluginID || old.Owner.SessionID != owner.SessionID {
					return Record{}, ErrConflict
				}
				return old, nil
			}
			if old.Status != "fenced" || old.EffectState != "not_started" {
				return Record{}, ErrConflict
			}
		}
		if len(view.Documents) == 0 && !fresh {
			return Record{}, ErrUnknown
		}
		existing, err := s.documents.Read(ctx, persistence.Query{Namespace: Namespace, Keys: []string{intent.ID}})
		if err != nil {
			return Record{}, err
		}
		if existing.Revision != view.Revision {
			continue
		}
		if len(existing.Documents) > 0 {
			return Record{}, ErrConflict
		}
		record := Record{Intent: intent, Owner: owner, Status: "pending", EffectState: "not_started", CreatedAt: s.now()}
		raw, _ := json.Marshal(record)
		gateRaw, _ := json.Marshal(batchGate{WaitID: intent.ID, Digest: intent.CallsDigest})
		_, err = s.documents.Commit(ctx, persistence.Transaction{ID: fmt.Sprintf("wait-reserve/%s/%d", batchKey(owner, intent.BatchID), view.Revision), ExpectedRevision: view.Revision, Writes: []persistence.Write{{Namespace: Namespace, Key: intent.ID, Value: raw}, {Namespace: GateNamespace, Key: batchKey(owner, intent.BatchID), Value: gateRaw}}})
		if errors.Is(err, persistence.ErrConflict) {
			continue
		}
		return record, err
	}
	return Record{}, ErrConflict
}

// DispatchBatch records an irreversible admission before calling the executor.
// Missing wait IDs cannot bypass a reserved gate. Ungated batches also leave a
// marker, preventing a later registration from claiming they never started.
func (s *Store) DispatchBatch(ctx context.Context, owner Owner, batch, digest, waitID string) error {
	if !validOwner(owner) || batch == "" || digest == "" {
		return ErrInvalid
	}
	for n := 0; n < 16; n++ {
		key := batchKey(owner, batch)
		view, err := s.documents.Read(ctx, persistence.Query{Namespace: GateNamespace, Keys: []string{key}})
		if err != nil {
			return err
		}
		gate := batchGate{Digest: digest}
		var writes []persistence.Write
		if len(view.Documents) > 0 {
			if err := json.Unmarshal(view.Documents[0].Value, &gate); err != nil {
				return err
			}
			if gate.Started {
				return ErrUnknown
			}
			if gate.Digest != digest || gate.WaitID != waitID || waitID == "" {
				return ErrConflict
			}
			record, err := s.Lookup(ctx, waitID)
			if err != nil {
				return err
			}
			if record.Owner.SessionID != owner.SessionID || record.Owner.PluginID != owner.PluginID {
				return ErrOwner
			}
			if record.Status != "approved" {
				return ErrConflict
			}
			if !s.now().Before(record.ExpiresAt) {
				return ErrExpired
			}
			if record.EffectState != "not_started" {
				return ErrUnknown
			}
			record.EffectState = "unknown"
			raw, _ := json.Marshal(record)
			writes = append(writes, persistence.Write{Namespace: Namespace, Key: waitID, Value: raw})
		} else if waitID != "" {
			return ErrNotFound
		}
		gate.Started = true
		raw, _ := json.Marshal(gate)
		writes = append(writes, persistence.Write{Namespace: GateNamespace, Key: key, Value: raw})
		_, err = s.documents.Commit(ctx, persistence.Transaction{ID: fmt.Sprintf("wait-dispatch/%s/%d", key, view.Revision), ExpectedRevision: view.Revision, Writes: writes})
		if errors.Is(err, persistence.ErrConflict) {
			continue
		}
		return err
	}
	return ErrConflict
}
