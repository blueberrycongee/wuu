package persistence

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

// The index is disposable. Only complete, checksummed log records are durable;
// every opener rebuilds the index and every operation reads another writer's tail
// while holding the shared authority lock.
type jsonlBackend struct {
	file        *os.File
	offset      int64
	unsynced    bool
	initialized bool
	revision    Revision
	documents   map[documentKey]Document
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
	b := &jsonlBackend{file: file, documents: make(map[documentKey]Document), receipts: make(map[string]CommitRecord), ordered: []CommitRecord{}}
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
	if info.Size() < b.offset {
		return fmt.Errorf("%w: journal was truncated behind its reader", ErrCorrupt)
	}
	reader := bufio.NewReader(io.NewSectionReader(b.file, b.offset, info.Size()-b.offset))
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			if len(line) != 0 {
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
		if err := b.apply(record); err != nil {
			return err
		}
		b.offset += int64(len(line))
		b.unsynced = true
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

func (b *jsonlBackend) apply(record journalRecord) error {
	switch record.Kind {
	case "snapshot":
		if b.initialized || record.Snapshot == nil || record.Transaction != nil {
			return fmt.Errorf("%w: misplaced snapshot", ErrCorrupt)
		}
		s := *record.Snapshot
		if err := validateSnapshot(s); err != nil {
			return err
		}
		b.revision = s.Revision
		for _, d := range s.Documents {
			b.documents[documentKey{d.Namespace, d.Key}] = d
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
		for _, w := range tx.Writes {
			key := documentKey{w.Namespace, w.Key}
			if w.Delete {
				delete(b.documents, key)
			} else {
				b.documents[key] = Document{Namespace: w.Namespace, Key: w.Key, Value: append(json.RawMessage(nil), w.Value...)}
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

func (b *jsonlBackend) append(ctx context.Context, record journalRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	line, err := json.Marshal(journalFrame{Payload: payload, Digest: hex.EncodeToString(sum[:])})
	if err != nil {
		return err
	}
	line = append(line, '\n')
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
	if err := b.apply(persisted); err != nil {
		return err
	}
	b.offset += int64(len(line))
	return nil
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
	for _, d := range b.documents {
		if matchesQuery(d, q) {
			d.Value = append(json.RawMessage(nil), d.Value...)
			view.Documents = append(view.Documents, d)
		}
	}
	sortDocuments(view.Documents)
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
	for _, d := range b.documents {
		d.Value = append(json.RawMessage(nil), d.Value...)
		s.Documents = append(s.Documents, d)
	}
	sortDocuments(s.Documents)
	return s, nil
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
