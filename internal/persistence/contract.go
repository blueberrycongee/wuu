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

// MaxReadLimit bounds an explicitly paged read; zero retains legacy full scans.
const MaxReadLimit = 10000

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

// Query intersects namespace, prefix, explicit keys, and the [Start, End) range.
// Keys nil selects all keys; an empty slice selects none. Keys use UTF-8 byte
// order. Limit zero is unbounded; positive limits cannot exceed MaxReadLimit.
// Cursor is an exclusive key in the selected direction and requires AtRevision.
// Continue with the same selector and View.Revision; any intervening commit
// returns ErrConflict, so callers must restart a pure read from its first page.
type Query struct {
	Namespace  string    `json:"namespace"`
	Prefix     string    `json:"prefix,omitempty"`
	Keys       []string  `json:"keys,omitzero"`
	Start      string    `json:"start,omitempty"`
	End        string    `json:"end,omitempty"`
	Reverse    bool      `json:"reverse,omitempty"`
	Limit      int       `json:"limit,omitempty"`
	Cursor     string    `json:"cursor,omitempty"`
	AtRevision *Revision `json:"at_revision,string,omitempty"`
}

// View is one consistent read, ordered by key (descending with Reverse).
// NextCursor is the last returned key only when more matching keys exist.
// Revisions compare the entire store, including changes in other namespaces.
type View struct {
	Revision   Revision   `json:"revision,string"`
	Documents  []Document `json:"documents"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

func validateQuery(q Query) error {
	if strings.TrimSpace(q.Namespace) == "" || q.Limit < 0 || q.Limit > MaxReadLimit {
		return fmt.Errorf("%w: namespace and a limit between zero and %d are required", ErrInvalid, MaxReadLimit)
	}
	if q.End != "" && q.Start > q.End {
		return fmt.Errorf("%w: start must not exceed end", ErrInvalid)
	}
	if q.Cursor != "" && q.AtRevision == nil {
		return fmt.Errorf("%w: cursor requires at_revision", ErrInvalid)
	}
	return nil
}

// keyBounds combines prefix and range without turning prefix characters into
// SQL patterns. The upper bound may be non-UTF-8 internally; ordering is bytes.
func keyBounds(q Query) (string, string) {
	start, end := q.Start, q.End
	if q.Prefix > start {
		start = q.Prefix
	}
	if q.Prefix != "" {
		upper := []byte(q.Prefix)
		for i := len(upper) - 1; i >= 0; i-- {
			if upper[i] != 255 {
				upper[i]++
				if end == "" || string(upper[:i+1]) < end {
					end = string(upper[:i+1])
				}
				break
			}
		}
	}
	return start, end
}

func finishPage(view *View, q Query) {
	if q.Limit > 0 && len(view.Documents) > q.Limit {
		view.Documents = view.Documents[:q.Limit]
		view.NextCursor = view.Documents[len(view.Documents)-1].Key
	}
}

// Compaction reports physical file sizes without changing documents, receipts,
// or the logical revision. It is optional provider maintenance, not a write.
type Compaction struct {
	Revision    Revision `json:"revision,string"`
	BytesBefore int64    `json:"bytes_before"`
	BytesAfter  int64    `json:"bytes_after"`
}

// ImportResult acknowledges installation; callers independently verify the full
// exported checkpoint before publishing a new host authority.
type ImportResult struct {
	Revision Revision `json:"revision,string"`
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
	digest := sha256.New()
	if err := WriteTransactionJSON(digest, tx); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
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
	if d.Namespace != q.Namespace || !strings.HasPrefix(d.Key, q.Prefix) || d.Key < q.Start || (q.End != "" && d.Key >= q.End) || (q.Cursor != "" && ((!q.Reverse && d.Key <= q.Cursor) || (q.Reverse && d.Key >= q.Cursor))) {
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
