package persistence

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

// The disposable index retains ordered keys and value offsets, not payloads.
// Only complete, checksummed log records are durable. Opening replays the journal
// and operations replay another writer's tail under the authority lock. Replay
// memory is proportional to the largest frame; key/receipt metadata remains in
// RAM. Bounded reads seek the index and load only the selected values from disk.
type jsonlBackend struct {
	file        *os.File
	readOnly    bool
	offset      int64
	unsynced    bool
	initialized bool
	revision    Revision
	documents   map[documentKey]valueLocation
	keys        map[string][]string
	namespaces  []string
	receipts    map[string]CommitRecord
	ordered     []CommitRecord
}

type journalRecord struct {
	Kind        string       `json:"kind"`
	Snapshot    *Snapshot    `json:"snapshot,omitempty"`
	Transaction *Transaction `json:"transaction,omitempty"`
}

type journalFrame struct {
	Payload json.RawMessage `json:"payload"`
	Digest  string          `json:"digest"`
}

func openJSONL(path string) (*jsonlBackend, error) {
	file, err := securefs.OpenFile(path, os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, err
	}
	return readJSONLFile(file, false)
}

func readJSONLFile(file *os.File, readOnly bool) (*jsonlBackend, error) {
	b := &jsonlBackend{file: file, readOnly: readOnly, keys: make(map[string][]string), documents: make(map[documentKey]valueLocation), receipts: make(map[string]CommitRecord), ordered: []CommitRecord{}}
	if err := b.refresh(); err != nil {
		file.Close()
		return nil, err
	}
	return b, nil
}

func (b *jsonlBackend) close() error { return b.file.Close() }

func (b *jsonlBackend) refresh() error {
	info, err := b.file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Stat(b.file.Name())
	if err != nil {
		return err
	}
	if !os.SameFile(info, current) {
		// Compaction replaces the journal atomically under the authority lock.
		// Rebuild before appending so no writer can extend a retired inode.
		var next *jsonlBackend
		if b.readOnly {
			var file *os.File
			file, err = os.Open(b.file.Name())
			if err == nil {
				next, err = readJSONLFile(file, true)
			}
		} else {
			next, err = openJSONL(b.file.Name())
		}
		if err != nil {
			return err
		}
		closeErr := b.file.Close()
		*b = *next
		return closeErr
	}
	if info.Size() < b.offset {
		return fmt.Errorf("%w: journal was truncated behind its reader", ErrCorrupt)
	}
	reader := bufio.NewReader(io.NewSectionReader(b.file, b.offset, info.Size()-b.offset))
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			if len(line) != 0 && !b.readOnly {
				// No newline means the last append never completed. Only this
				// unacknowledged suffix may be discarded; complete bad frames fail.
				if err := b.file.Truncate(b.offset); err != nil {
					return err
				}
				b.unsynced = true
			}
			break
		}
		if err != nil {
			return err
		}
		var frame journalFrame
		if err := json.Unmarshal(line, &frame); err != nil {
			return fmt.Errorf("%w: invalid journal frame at %d", ErrCorrupt, b.offset)
		}
		sum := sha256.Sum256(frame.Payload)
		if hex.EncodeToString(sum[:]) != frame.Digest {
			return fmt.Errorf("%w: journal checksum at %d", ErrCorrupt, b.offset)
		}
		var record journalRecord
		if err := json.Unmarshal(frame.Payload, &record); err != nil {
			return fmt.Errorf("%w: invalid journal record", ErrCorrupt)
		}
		if err := b.apply(record, line); err != nil {
			return err
		}
		b.offset += int64(len(line))
		b.unsynced = !b.readOnly
	}
	// A previous writer may have lost its acknowledgement at fsync. Reading
	// its complete receipt must finish that barrier before returning success.
	if b.unsynced {
		if err := b.file.Sync(); err != nil {
			return err
		}
		b.unsynced = false
	}
	return nil
}

func (b *jsonlBackend) apply(record journalRecord, line []byte) error {
	switch record.Kind {
	case "snapshot":
		if b.initialized || record.Snapshot == nil || record.Transaction != nil {
			return fmt.Errorf("%w: misplaced snapshot", ErrCorrupt)
		}
		s := *record.Snapshot
		if err := validateSnapshot(s); err != nil {
			return err
		}
		locations, err := journalLocations(line, "snapshot", "documents", b.offset)
		if err != nil {
			return err
		}
		b.revision = s.Revision
		for i, d := range s.Documents {
			b.putDocument(d.Namespace, d.Key, locations[i])
		}
		for _, r := range s.Receipts {
			b.receipts[r.ID] = r
			b.ordered = append(b.ordered, r)
		}
		b.initialized = true
	case "commit":
		if !b.initialized || record.Transaction == nil || record.Snapshot != nil {
			return fmt.Errorf("%w: commit without snapshot", ErrCorrupt)
		}
		tx := *record.Transaction
		digest, err := transactionDigest(tx)
		_, exists := b.receipts[tx.ID]
		if err != nil || tx.ExpectedRevision != b.revision || exists {
			return fmt.Errorf("%w: invalid commit sequence", ErrCorrupt)
		}
		locations, err := journalLocations(line, "transaction", "writes", b.offset)
		if err != nil {
			return err
		}
		for i, w := range tx.Writes {
			key := documentKey{w.Namespace, w.Key}
			if w.Delete {
				delete(b.documents, key)
				keys := b.keys[w.Namespace]
				i := sort.SearchStrings(keys, w.Key)
				if i < len(keys) && keys[i] == w.Key {
					copy(keys[i:], keys[i+1:])
					keys[len(keys)-1] = ""
					b.keys[w.Namespace] = keys[:len(keys)-1]
				}
			} else {
				b.putDocument(w.Namespace, w.Key, locations[i])
			}
		}
		b.revision++
		r := CommitRecord{Receipt: Receipt{ID: tx.ID, Revision: b.revision}, Digest: digest}
		b.receipts[tx.ID] = r
		b.ordered = append(b.ordered, r)
	default:
		return fmt.Errorf("%w: unknown journal record", ErrCorrupt)
	}
	return nil
}

func encodeJournalRecord(record journalRecord) ([]byte, []byte, error) {
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(payload)
	line, err := json.Marshal(journalFrame{Payload: payload, Digest: hex.EncodeToString(sum[:])})
	if err != nil {
		return nil, nil, err
	}
	line = append(line, '\n')
	return payload, line, nil
}

func (b *jsonlBackend) append(ctx context.Context, record journalRecord) error {
	payload, line, err := encodeJournalRecord(record)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.unsynced = true
	if _, err := b.file.WriteAt(line, b.offset); err != nil {
		return err
	}
	if err := b.file.Sync(); err != nil {
		return err
	}
	b.unsynced = false
	// Decode the stored bytes so the in-memory projection is identical to a
	// restart (including JSON normalization and ownership of raw value bytes).
	var persisted journalRecord
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return err
	}
	if err := b.apply(persisted, line); err != nil {
		return err
	}
	b.offset += int64(len(line))
	return nil
}

type valueLocation struct {
	offset int64
	length int
	digest [sha256.Size]byte
}

// jsonMember locates a member in validated JSON. Decoder offsets refer to the
// original bytes, so repeated values, escaped strings and nested fields cannot
// accidentally select a different occurrence of the same value.
func jsonMember(raw []byte, member string) ([]byte, int64, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, 0, err
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, 0, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, 0, err
		}
		if key == member {
			end := decoder.InputOffset()
			start := end - int64(len(value))
			return raw[start:end], start, nil
		}
	}
	return nil, 0, nil
}

func journalLocations(line []byte, kind, collection string, base int64) ([]valueLocation, error) {
	raw, offset, err := jsonMember(line, "payload")
	if err != nil {
		return nil, err
	}
	base += offset
	raw, offset, err = jsonMember(raw, kind)
	if err != nil {
		return nil, err
	}
	base += offset
	raw, offset, err = jsonMember(raw, collection)
	if err != nil {
		return nil, err
	}
	base += offset
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	locations := []valueLocation{}
	for decoder.More() {
		var entry json.RawMessage
		if err := decoder.Decode(&entry); err != nil {
			return nil, err
		}
		entryOffset := decoder.InputOffset() - int64(len(entry))
		value, valueOffset, err := jsonMember(entry, "value")
		if err != nil {
			return nil, err
		}
		locations = append(locations, valueLocation{offset: base + entryOffset + valueOffset, length: len(value), digest: sha256.Sum256(value)})
	}
	return locations, nil
}

func (b *jsonlBackend) putDocument(namespace, key string, location valueLocation) {
	id := documentKey{namespace, key}
	if _, exists := b.documents[id]; !exists {
		if _, known := b.keys[namespace]; !known {
			i := sort.SearchStrings(b.namespaces, namespace)
			b.namespaces = append(b.namespaces, "")
			copy(b.namespaces[i+1:], b.namespaces[i:])
			b.namespaces[i] = namespace
		}
		keys := b.keys[namespace]
		i := sort.SearchStrings(keys, key)
		keys = append(keys, "")
		copy(keys[i+1:], keys[i:])
		keys[i] = key
		b.keys[namespace] = keys
	}
	b.documents[id] = location
}

func (b *jsonlBackend) readDocument(namespace, key string, location valueLocation) (Document, error) {
	d := Document{Namespace: namespace, Key: key, Value: make(json.RawMessage, location.length)}
	if _, err := b.file.ReadAt(d.Value, location.offset); err != nil {
		return Document{}, err
	}
	if sha256.Sum256(d.Value) != location.digest {
		return Document{}, fmt.Errorf("%w: indexed document checksum", ErrCorrupt)
	}
	return d, nil
}

func (b *jsonlBackend) read(ctx context.Context, q Query) (View, error) {
	if err := ctx.Err(); err != nil {
		return View{}, err
	}
	if err := b.refresh(); err != nil {
		return View{}, err
	}
	if !b.initialized {
		return View{}, ErrCorrupt
	}
	view := View{Revision: b.revision, Documents: []Document{}}
	if q.AtRevision != nil && *q.AtRevision != b.revision {
		return View{}, ErrConflict
	}
	keys := b.keys[q.Namespace]
	if q.Keys != nil {
		keys = append([]string{}, q.Keys...)
		sort.Strings(keys)
	}
	start, end := keyBounds(q)
	lo := sort.SearchStrings(keys, start)
	hi := len(keys)
	if end != "" {
		hi = sort.SearchStrings(keys, end)
	}
	if q.Cursor != "" {
		if q.Reverse {
			cursor := sort.SearchStrings(keys, q.Cursor)
			if cursor < hi {
				hi = cursor
			}
		} else {
			cursor := sort.Search(len(keys), func(i int) bool { return keys[i] > q.Cursor })
			if cursor > lo {
				lo = cursor
			}
		}
	}
	for i := 0; i < hi-lo; i++ {
		if err := ctx.Err(); err != nil {
			return View{}, err
		}
		at := lo + i
		if q.Reverse {
			at = hi - 1 - i
		}
		key := keys[at]
		if i > 0 {
			previous := at - 1
			if q.Reverse {
				previous = at + 1
			}
			if keys[previous] == key {
				continue
			}
		}
		location, exists := b.documents[documentKey{q.Namespace, key}]
		if !exists {
			continue
		}
		if q.Limit > 0 && len(view.Documents) == q.Limit {
			view.NextCursor = view.Documents[len(view.Documents)-1].Key
			break
		}
		d, err := b.readDocument(q.Namespace, key, location)
		if err != nil {
			return View{}, err
		}
		view.Documents = append(view.Documents, d)
	}
	return view, nil
}

func (b *jsonlBackend) commit(ctx context.Context, tx Transaction, digest string) (Receipt, error) {
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if err := b.refresh(); err != nil {
		return Receipt{}, err
	}
	if !b.initialized {
		return Receipt{}, ErrCorrupt
	}
	if prior, exists := b.receipts[tx.ID]; exists {
		if prior.Digest != digest {
			return Receipt{}, ErrOperationConflict
		}
		return prior.Receipt, nil
	}
	if b.revision != tx.ExpectedRevision {
		return Receipt{}, ErrConflict
	}
	if err := b.append(ctx, journalRecord{Kind: "commit", Transaction: &tx}); err != nil {
		return Receipt{}, err
	}
	return b.receipts[tx.ID].Receipt, nil
}

func (b *jsonlBackend) export(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if err := b.refresh(); err != nil {
		return Snapshot{}, err
	}
	if !b.initialized {
		return Snapshot{}, ErrCorrupt
	}
	s := Snapshot{Version: 1, Revision: b.revision, Documents: []Document{}, Receipts: append([]CommitRecord{}, b.ordered...)}
	for key, location := range b.documents {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		d, err := b.readDocument(key.namespace, key.key, location)
		if err != nil {
			return Snapshot{}, err
		}
		s.Documents = append(s.Documents, d)
	}
	sortDocuments(s.Documents)
	return s, nil
}

func (b *jsonlBackend) exportPage(ctx context.Context, q ExportQuery, generation string) (ExportPage, error) {
	if err := ctx.Err(); err != nil {
		return ExportPage{}, err
	}
	if err := b.refresh(); err != nil {
		return ExportPage{}, err
	}
	if !b.initialized {
		return ExportPage{}, ErrCorrupt
	}
	builder, err := newExportPage(q, b.revision, generation)
	if err != nil {
		return ExportPage{}, err
	}
	if q.Section == "receipts" {
		var start Revision
		if q.After != nil {
			start = *q.After.Revision
		}
		for i := start; i < Revision(len(b.ordered)); i++ {
			if err := ctx.Err(); err != nil {
				return ExportPage{}, err
			}
			fits, err := builder.addReceipt(b.ordered[i])
			if err != nil {
				return ExportPage{}, err
			}
			if !fits {
				return builder.page, nil
			}
		}
	} else {
		start := 0
		if q.After != nil {
			start = sort.SearchStrings(b.namespaces, q.After.Namespace)
		}
		for _, namespace := range b.namespaces[start:] {
			keys := b.keys[namespace]
			at := 0
			if q.After != nil && namespace == q.After.Namespace {
				at = sort.Search(len(keys), func(i int) bool { return keys[i] > q.After.Key })
			}
			for _, key := range keys[at:] {
				if err := ctx.Err(); err != nil {
					return ExportPage{}, err
				}
				if builder.items == q.MaxItems || builder.items > 0 && builder.size >= q.TargetBytes {
					return builder.page, nil
				}
				d, err := b.readDocument(namespace, key, b.documents[documentKey{namespace, key}])
				if err != nil {
					return ExportPage{}, err
				}
				fits, err := builder.addDocument(d)
				if err != nil {
					return ExportPage{}, err
				}
				if !fits {
					return builder.page, nil
				}
			}
		}
	}
	builder.page.Done = true
	return builder.page, nil
}

func (b *jsonlBackend) restore(ctx context.Context, s Snapshot) error {
	if b.initialized {
		return fmt.Errorf("%w: import target is not empty", ErrConflict)
	}
	if err := validateSnapshot(s); err != nil {
		return err
	}
	return b.append(ctx, journalRecord{Kind: "snapshot", Snapshot: &s})
}

func (b *jsonlBackend) compact(ctx context.Context) (Compaction, error) {
	snapshot, err := b.export(ctx)
	if err != nil {
		return Compaction{}, err
	}
	info, err := b.file.Stat()
	if err != nil {
		return Compaction{}, err
	}
	result := Compaction{Revision: snapshot.Revision, BytesBefore: info.Size()}
	_, line, err := encodeJournalRecord(journalRecord{Kind: "snapshot", Snapshot: &snapshot})
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	path := b.file.Name()
	if err := securefs.WriteFileAtomic(path, line); err != nil {
		return result, err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return result, err
	}
	if err := b.refresh(); err != nil {
		return result, err
	}
	result.BytesAfter = int64(len(line))
	return result, nil
}
