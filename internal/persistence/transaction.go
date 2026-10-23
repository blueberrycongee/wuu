package persistence

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// Tx buffers domain writes against one store revision. It owns no backend lock
// and must not escape its Update callback. Reads see earlier buffered writes.
type Tx struct {
	ctx      context.Context
	store    Documents
	revision *Revision
	writes   map[documentKey]Write
	err      error
}

// CommitError retains the exact attempted transaction when commit fails. The
// write may have completed before the response was lost; callers can retry this
// transaction unchanged to obtain its durable receipt. They must not repeat an
// external effect or reconstruct the write against a newer revision.
type CommitError struct {
	Transaction Transaction
	Err         error
}

func (e *CommitError) Error() string { return fmt.Sprintf("commit %s: %v", e.Transaction.ID, e.Err) }
func (e *CommitError) Unwrap() error { return e.Err }

// Update repeats only an explicitly rejected revision conflict. The callback
// must be free of external effects, create identities/timestamps outside the
// callback, and replace (rather than accumulate) any returned domain result on
// each attempt. All other errors, including uncertain transport failures, stop
// immediately. Cancellation is checked before every attempt and commit; once a
// provider admits commit its durability barrier may finish after cancellation.
func Update(ctx context.Context, store Documents, body func(*Tx) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tx := &Tx{ctx: ctx, store: store, writes: make(map[documentKey]Write)}
		err := body(tx)
		if err == nil {
			err = tx.err
		}
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil || len(tx.writes) == 0 {
			return err
		}
		// A write-only transaction still needs a revision. Query no keys in
		// the first namespace, preserving the same contract for remote stores.
		if tx.revision == nil {
			for key := range tx.writes {
				_, err = tx.Read(Query{Namespace: key.namespace, Keys: []string{}})
				break
			}
			if err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return err
		}
		request := Transaction{ID: hex.EncodeToString(id[:]), ExpectedRevision: *tx.revision, Writes: make([]Write, 0, len(tx.writes))}
		// Stable ordering also keeps the retry intent readable in diagnostics.
		keys := make([]Document, 0, len(tx.writes))
		for key := range tx.writes {
			keys = append(keys, Document{Namespace: key.namespace, Key: key.key})
		}
		sortDocuments(keys)
		for _, key := range keys {
			request.Writes = append(request.Writes, tx.writes[documentKey{key.Namespace, key.Key}])
		}
		_, err = store.Commit(ctx, request)
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return &CommitError{Transaction: request, Err: err}
		}
		return nil
	}
}

func (tx *Tx) Read(query Query) ([]Document, error) {
	if tx.err != nil {
		return nil, tx.err
	}
	view, err := tx.store.Read(tx.ctx, query)
	if err != nil {
		tx.err = err
		return nil, err
	}
	if tx.revision == nil {
		tx.revision = &view.Revision
	} else if *tx.revision != view.Revision {
		tx.err = ErrConflict
		return nil, tx.err
	}
	documents := make(map[documentKey]Document, len(view.Documents))
	for _, document := range view.Documents {
		documents[documentKey{document.Namespace, document.Key}] = document
	}
	for key, write := range tx.writes {
		document := Document{Namespace: write.Namespace, Key: write.Key, Value: write.Value}
		if !matchesQuery(document, query) {
			continue
		}
		if write.Delete {
			delete(documents, key)
		} else {
			documents[key] = document
		}
	}
	result := make([]Document, 0, len(documents))
	for _, document := range documents {
		result = append(result, document)
	}
	sortDocuments(result)
	return result, nil
}

func (tx *Tx) Get(namespace, key string, target any) (bool, error) {
	documents, err := tx.Read(Query{Namespace: namespace, Keys: []string{key}})
	if err != nil || len(documents) == 0 {
		return false, err
	}
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		tx.err = fmt.Errorf("%w: document target must be a non-nil pointer", ErrInvalid)
		return false, tx.err
	}
	// JSON decoding normally merges maps and leaves omitted fields intact.
	// Each read must replace the previous state, including on a CAS retry.
	decoded := reflect.New(value.Elem().Type())
	if err := json.Unmarshal(documents[0].Value, decoded.Interface()); err != nil {
		tx.err = fmt.Errorf("%w: decode document %s/%s: %v", ErrCorrupt, namespace, key, err)
		return false, tx.err
	}
	value.Elem().Set(decoded.Elem())
	return true, nil
}

func (tx *Tx) Put(namespace, key string, value any) error {
	if tx.err != nil {
		return tx.err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		tx.err = err
		return err
	}
	tx.writes[documentKey{namespace, key}] = Write{Namespace: namespace, Key: key, Value: raw}
	return nil
}

func (tx *Tx) Delete(namespace, key string) error {
	if tx.err != nil {
		return tx.err
	}
	tx.writes[documentKey{namespace, key}] = Write{Namespace: namespace, Key: key, Delete: true}
	return nil
}
