package persistence

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
)

// Tx buffers domain writes against one store revision. It owns no backend lock
// and must not escape its Update callback. Reads see earlier buffered writes.
type Tx struct {
	ctx      context.Context
	store    Documents
	revision *Revision
	writes   map[documentKey]bufferedWrite
	err      error
}

type bufferedWrite struct {
	Write
	prepared bool
}

// PreparedWrite owns a normalized JSON snapshot for reuse across Update attempts.
// Its value is shared with commit intents, which must remain unchanged until an
// uncertain result is resolved, just like any other Transaction.
type PreparedWrite struct {
	write Write
}

// PrepareWrite copies and normalizes value exactly as Tx.Put does. Later changes
// to the caller's value cannot alter the prepared write.
func PrepareWrite(namespace, key string, value any) (PreparedWrite, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return PreparedWrite{}, err
	}
	return PreparedWrite{write: Write{Namespace: namespace, Key: key, Value: raw}}, nil
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
		tx := &Tx{ctx: ctx, store: store, writes: make(map[documentKey]bufferedWrite)}
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
			request.Writes = append(request.Writes, tx.writes[documentKey{key.Namespace, key.Key}].Write)
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
	view, err := tx.ReadView(query)
	return view.Documents, err
}

// ReadView applies buffered writes before key ordering and pagination. Keep the
// same buffered writes while walking pages. Every read is pinned to the first
// revision observed by this transaction, including reads of other namespaces.
func (tx *Tx) ReadView(query Query) (View, error) {
	if tx.err != nil {
		return View{}, tx.err
	}
	if err := validateQuery(query); err != nil {
		tx.err = err
		return View{}, err
	}
	if tx.revision != nil && query.AtRevision != nil && *tx.revision != *query.AtRevision {
		tx.err = ErrConflict
		return View{}, tx.err
	}
	request := query
	if tx.revision != nil {
		request.AtRevision = tx.revision
	}
	documents := make(map[documentKey]Document)
	surviving := 0
	for {
		if query.Limit > 0 {
			request.Limit = min(query.Limit+1-surviving, MaxReadLimit)
		}
		view, err := tx.store.Read(tx.ctx, request)
		if err != nil {
			tx.err = err
			return View{}, err
		}
		if tx.revision == nil {
			tx.revision = &view.Revision
		} else if *tx.revision != view.Revision {
			tx.err = ErrConflict
			return View{}, tx.err
		}
		for _, d := range view.Documents {
			key := documentKey{d.Namespace, d.Key}
			if write, ok := tx.writes[key]; ok && write.Delete {
				continue
			}
			documents[key] = d
			surviving++
		}
		if query.Limit == 0 || surviving > query.Limit || view.NextCursor == "" {
			break
		}
		request.Cursor = view.NextCursor
		request.AtRevision = tx.revision
	}
	for key, write := range tx.writes {
		d := Document{Namespace: write.Namespace, Key: write.Key, Value: write.Value}
		if !write.Delete && matchesQuery(d, query) {
			// A read result must not expose the immutable value reused by another
			// attempt or transaction. Ordinary Put retains its existing behavior.
			if write.prepared {
				d.Value = bytes.Clone(write.Value)
			}
			documents[key] = d
		}
	}
	view := View{Revision: *tx.revision, Documents: make([]Document, 0, len(documents))}
	for _, d := range documents {
		view.Documents = append(view.Documents, d)
	}
	sortDocuments(view.Documents)
	if query.Reverse {
		slices.Reverse(view.Documents)
	}
	finishPage(&view, query)
	return view, nil
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
	tx.writes[documentKey{namespace, key}] = bufferedWrite{Write: Write{Namespace: namespace, Key: key, Value: raw}}
	return nil
}

// PutPrepared borrows a prepared value without normalizing or copying it again.
// It participates in the same read-your-writes and atomic commit as Put.
func (tx *Tx) PutPrepared(prepared PreparedWrite) error {
	if tx.err != nil {
		return tx.err
	}
	write := prepared.write
	tx.writes[documentKey{write.Namespace, write.Key}] = bufferedWrite{Write: write, prepared: true}
	return nil
}

func (tx *Tx) Delete(namespace, key string) error {
	if tx.err != nil {
		return tx.err
	}
	tx.writes[documentKey{namespace, key}] = bufferedWrite{Write: Write{Namespace: namespace, Key: key, Delete: true}}
	return nil
}
