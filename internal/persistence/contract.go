// Package persistence implements the state.documents v1 provider contract.
// A commit atomically compares the store revision and applies all writes. Its
// operation ID survives restarts and migrations, making an uncertain response
// retryable without replaying the write. It does not make external effects
// (including tool execution) retryable.
package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

const ServiceName = "state.documents"

var (
	ErrConflict          = errors.New("store revision conflict")
	ErrOperationConflict = errors.New("operation ID already has different intent")
	ErrMigrationRequired = errors.New("store backend change requires migration")
	ErrBindingChanged    = errors.New("store binding changed; reopen the selected provider")
	ErrCorrupt           = errors.New("persistent store is corrupt")
	ErrClosed            = errors.New("persistent store is closed")
	ErrInvalid           = errors.New("invalid persistence request")
)

// Revision is encoded as a decimal string to avoid JavaScript integer loss.
type Revision uint64

type Document struct {
	Namespace string          `json:"namespace"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
}

type Query struct {
	Namespace string   `json:"namespace"`
	Prefix    string   `json:"prefix,omitempty"`
	Keys      []string `json:"keys,omitzero"`
}

// View is one consistent read, ordered by key. A transaction using its revision
// fails if any namespace changed since the read, including newly inserted keys.
type View struct {
	Revision  Revision   `json:"revision,string"`
	Documents []Document `json:"documents"`
}

type Write struct {
	Namespace string          `json:"namespace"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value,omitempty"`
	Delete    bool            `json:"delete,omitempty"`
}

type Transaction struct {
	ID               string   `json:"id"`
	ExpectedRevision Revision `json:"expected_revision,string"`
	Writes           []Write  `json:"writes"`
}

type Receipt struct {
	ID       string   `json:"id"`
	Revision Revision `json:"revision,string"`
}

type CommitRecord struct {
	Receipt
	Digest string `json:"digest"`
}

// Snapshot includes idempotency receipts: copying documents alone would permit
// previously committed operations to run again after a backend change.
type Snapshot struct {
	Version   int            `json:"version"`
	Revision  Revision       `json:"revision,string"`
	Documents []Document     `json:"documents"`
	Receipts  []CommitRecord `json:"receipts"`
}

func transactionDigest(tx Transaction) (string, error) {
	if strings.TrimSpace(tx.ID) == "" || len(tx.Writes) == 0 || tx.ExpectedRevision >= math.MaxInt64 {
		return "", fmt.Errorf("%w: an operation ID, writes, and an incrementable revision are required", ErrInvalid)
	}
	seen := make(map[documentKey]bool, len(tx.Writes))
	for _, w := range tx.Writes {
		key := documentKey{w.Namespace, w.Key}
		if strings.TrimSpace(w.Namespace) == "" || w.Key == "" || seen[key] {
			return "", fmt.Errorf("%w: namespaces and keys must be nonempty and writes unique", ErrInvalid)
		}
		seen[key] = true
		if (w.Delete && len(w.Value) != 0) || (!w.Delete && !json.Valid(w.Value)) {
			return "", fmt.Errorf("%w: write requires a JSON value or an explicit deletion", ErrInvalid)
		}
	}
	raw, err := json.Marshal(tx)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func validateSnapshot(s Snapshot) error {
	if s.Version != 1 || uint64(len(s.Receipts)) != uint64(s.Revision) {
		return fmt.Errorf("%w: unsupported snapshot or missing receipts", ErrCorrupt)
	}
	keys := make(map[documentKey]bool, len(s.Documents))
	for _, d := range s.Documents {
		key := documentKey{d.Namespace, d.Key}
		if strings.TrimSpace(d.Namespace) == "" || d.Key == "" || !json.Valid(d.Value) || keys[key] {
			return fmt.Errorf("%w: invalid or duplicate document", ErrCorrupt)
		}
		keys[key] = true
	}
	ids := make(map[string]bool, len(s.Receipts))
	for i, r := range s.Receipts {
		digest, err := hex.DecodeString(r.Digest)
		if r.ID == "" || ids[r.ID] || r.Revision != Revision(i+1) || err != nil || len(digest) != sha256.Size {
			return fmt.Errorf("%w: invalid receipt sequence", ErrCorrupt)
		}
		ids[r.ID] = true
	}
	return nil
}

type documentKey struct{ namespace, key string }

func sortDocuments(docs []Document) {
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].Namespace != docs[j].Namespace {
			return docs[i].Namespace < docs[j].Namespace
		}
		return docs[i].Key < docs[j].Key
	})
}

func matchesQuery(d Document, q Query) bool {
	if d.Namespace != q.Namespace || !strings.HasPrefix(d.Key, q.Prefix) {
		return false
	}
	if q.Keys == nil {
		return true
	}
	for _, key := range q.Keys {
		if key == d.Key {
			return true
		}
	}
	return false
}
