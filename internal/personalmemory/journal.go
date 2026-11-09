// Package personalmemory owns authoritative candidate records and their durable
// publication receipts through a selected document provider. Legacy records stay
// with their original provider until a candidate with that ID is first admitted;
// from that point the journal is the sole authority, including forgotten rows.
package personalmemory

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/persistence"
)

const recordsNamespace = "wuu.personal_memory_records.v1"
const operationsNamespace = "wuu.personal_memory_operations.v1"

var ErrIntentConflict = errors.New("memory operation identity has different intent")
var ErrRevisionConflict = errors.New("memory revision_conflict: reread the current record")
var ErrAborted = errors.New("memory operation was reconciled as not committed")
var ErrNotFound = errors.New("memory operation not found")

// Operation is written before acquiring any durable guard. Bindings identify
// selected providers, never host paths. Recovery must use these same bindings.
// State and Result are changed only with the authoritative record transaction.
type Operation struct {
	ID             string                       `json:"id"`
	Digest         string                       `json:"digest"`
	Key            string                       `json:"key"`
	Expected       *string                      `json:"expected"`
	Value          string                       `json:"value"`
	SessionID      string                       `json:"session_id"`
	AgentID        string                       `json:"agent_id"`
	TaskID         string                       `json:"task_id,omitempty"`
	RunID          string                       `json:"run_id,omitempty"`
	Revision       int64                        `json:"requirements_revision,omitempty"`
	SourceRef      string                       `json:"source_ref,omitempty"`
	Bindings       map[string]string            `json:"bindings"`
	TaskGuardID    string                       `json:"task_guard_id"`
	SessionGuardID string                       `json:"session_guard_id"`
	Authorization  *executionauth.CommitRequest `json:"authorization,omitempty"`
	State          string                       `json:"state"`
	Result         string                       `json:"result,omitempty"`
	ReceiptDigest  string                       `json:"receipt_digest,omitempty"`
	Redacted       bool                         `json:"redacted,omitempty"`
	RecoveryReason string                       `json:"recovery_reason,omitempty"`
}
type Journal struct {
	documents persistence.Documents
	prefix    string
}

func NewJournal(documents persistence.Documents, space, subject string) *Journal {
	part := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return &Journal{documents: documents, prefix: part(space) + "/" + part(subject) + "/"}
}
func (j *Journal) update(ctx context.Context, body func(*persistence.Tx) error) error {
	if j.documents == nil {
		return errors.New("selected memory journal is unavailable")
	}
	if err := persistence.ClaimNamespaces(ctx, j.documents, []string{recordsNamespace, operationsNamespace}); err != nil {
		return err
	}
	return persistence.Update(ctx, j.documents, body)
}
func (j *Journal) Prepare(ctx context.Context, intent Operation) (Operation, error) {
	intent.State = "pending"
	intent.Result = ""
	result := intent
	err := j.update(ctx, func(tx *persistence.Tx) error {
		result = intent
		found, err := tx.Get(operationsNamespace, j.prefix+intent.ID, &result)
		if err != nil {
			return err
		}
		if found {
			if result.Digest != intent.Digest {
				return ErrIntentConflict
			}
			return nil
		}
		result = intent
		return tx.Put(operationsNamespace, j.prefix+intent.ID, intent)
	})
	return result, err
}
func (j *Journal) Operation(ctx context.Context, id string) (Operation, error) {
	var result Operation
	err := j.update(ctx, func(tx *persistence.Tx) error {
		found, err := tx.Get(operationsNamespace, j.prefix+id, &result)
		if err == nil && !found {
			return ErrNotFound
		}
		return err
	})
	return result, err
}
func (j *Journal) Operations(ctx context.Context) ([]Operation, error) {
	result := []Operation{}
	err := j.update(ctx, func(tx *persistence.Tx) error {
		result = []Operation{}
		docs, err := tx.Read(persistence.Query{Namespace: operationsNamespace, Prefix: j.prefix})
		if err != nil {
			return err
		}
		for _, doc := range docs {
			var op Operation
			if _, err = tx.Get(operationsNamespace, doc.Key, &op); err != nil {
				return err
			}
			result = append(result, op)
		}
		return nil
	})
	return result, err
}
func (j *Journal) records(tx *persistence.Tx) (map[string]string, error) {
	result := map[string]string{}
	docs, err := tx.Read(persistence.Query{Namespace: recordsNamespace, Prefix: j.prefix})
	if err != nil {
		return nil, err
	}
	for _, doc := range docs {
		var value string
		if _, err = tx.Get(recordsNamespace, doc.Key, &value); err != nil {
			return nil, err
		}
		result[strings.TrimPrefix(doc.Key, j.prefix)] = value
	}
	return result, nil
}
func (j *Journal) Records(ctx context.Context) (map[string]string, error) {
	var result map[string]string
	err := j.update(ctx, func(tx *persistence.Tx) error { var err error; result, err = j.records(tx); return err })
	return result, err
}

// Publish's validator is pure. Task/session/grant guards are held by the host
// across this transaction. A terminal abort invalidates delayed CAS attempts;
// retries reread the operation, never publish over a terminal disposition.
func (j *Journal) Publish(ctx context.Context, id, digest string, legacy map[string]string, validate func(Operation, map[string]string) error) (Operation, error) {
	var result Operation
	err := j.update(ctx, func(tx *persistence.Tx) error {
		found, err := tx.Get(operationsNamespace, j.prefix+id, &result)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if result.Digest != digest {
			return ErrIntentConflict
		}
		if result.State == "committed" {
			return nil
		}
		if result.State != "pending" {
			return ErrAborted
		}
		records, err := j.records(tx)
		if err != nil {
			return err
		}
		for key, value := range legacy {
			if _, exists := records[key]; !exists {
				records[key] = value
			}
		}
		current, exists := records[result.Key]
		if result.Expected == nil && exists || result.Expected != nil && (!exists || *result.Expected != current) {
			return ErrRevisionConflict
		}
		if validate != nil {
			if err = validate(result, records); err != nil {
				return err
			}
		}
		result.State = "committed"
		result.Result = result.Value
		receipt := sha256.Sum256([]byte(result.Digest + "\x00" + result.Result))
		result.ReceiptDigest = hex.EncodeToString(receipt[:])
		var record struct {
			Status string `json:"status"`
		}
		if json.Unmarshal([]byte(result.Value), &record) == nil && record.Status == "forgotten" {
			result.Expected = nil
			result.Redacted = true
			// Forget removes prior content from this logical authority, including
			// retry receipts. Digests retain cleanup proof without plaintext.
			operations, err := tx.Read(persistence.Query{Namespace: operationsNamespace, Prefix: j.prefix})
			if err != nil {
				return err
			}
			for _, doc := range operations {
				var old Operation
				if _, err = tx.Get(operationsNamespace, doc.Key, &old); err != nil {
					return err
				}
				if old.Key != result.Key || old.ID == result.ID {
					continue
				}
				old.Expected = nil
				old.Value = ""
				old.Result = ""
				old.Redacted = true
				if old.State == "pending" {
					old.State = "not-committed"
				}
				if err = tx.Put(operationsNamespace, doc.Key, old); err != nil {
					return err
				}
			}
		}
		if err = tx.Put(recordsNamespace, j.prefix+result.Key, result.Value); err != nil {
			return err
		}
		return tx.Put(operationsNamespace, j.prefix+id, result)
	})
	if err != nil {
		return Operation{}, err
	}
	return result, nil
}

// Abort establishes a terminal outcome without guessing transport results. If a
// publication won, its receipt is returned; otherwise this CAS fences every
// earlier in-flight publication before reporting not-committed.
func (j *Journal) Abort(ctx context.Context, id, digest string) (Operation, error) {
	var result Operation
	err := j.update(ctx, func(tx *persistence.Tx) error {
		found, err := tx.Get(operationsNamespace, j.prefix+id, &result)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if result.Digest != digest {
			return ErrIntentConflict
		}
		if result.State != "pending" {
			return nil
		}
		result.State = "not-committed"
		return tx.Put(operationsNamespace, j.prefix+id, result)
	})
	return result, err
}

// Reconcile records the owner's stopped-writer evidence with the terminal
// decision. The record CAS is also the barrier against delayed publication.
func (j *Journal) Reconcile(ctx context.Context, id, digest, reason string) (Operation, error) {
	var result Operation
	if strings.TrimSpace(reason) == "" {
		return result, errors.New("memory recovery reason required")
	}
	err := j.update(ctx, func(tx *persistence.Tx) error {
		found, err := tx.Get(operationsNamespace, j.prefix+id, &result)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if result.Digest != digest {
			return ErrIntentConflict
		}
		if result.State == "pending" {
			result.State = "not-committed"
		}
		result.RecoveryReason = reason
		return tx.Put(operationsNamespace, j.prefix+id, result)
	})
	return result, err
}
