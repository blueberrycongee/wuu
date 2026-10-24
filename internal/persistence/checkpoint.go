package persistence

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Checkpoint commits to all documents and the complete ordered receipt history.
// It is portable across providers. Equality verifies a stopped copy, not shared
// live storage; active generations also require VerifySharedAuthority.
type Checkpoint struct {
	Version         int      `json:"version"`
	Revision        Revision `json:"revision,string"`
	DocumentsDigest string   `json:"documents_digest"`
	ReceiptsDigest  string   `json:"receipts_digest"`
}

type SnapshotReader interface {
	Export(context.Context) (Snapshot, error)
}

// SnapshotCheckpoint validates the snapshot before hashing its contents. Each
// document hash includes its namespace and key. XOR makes that unique-key set
// independent of export ordering and permits updates using only changed keys.
// Receipts use an ordered hash chain so deletions and reordering are detectable.
func SnapshotCheckpoint(snapshot Snapshot) (Checkpoint, error) {
	if err := validateSnapshot(snapshot); err != nil {
		return Checkpoint{}, err
	}
	var documents [sha256.Size]byte
	receipts := sha256.Sum256([]byte("wuu.state.receipts.v1"))
	for _, document := range snapshot.Documents {
		digest, err := checkpointDocumentHash(document)
		if err != nil {
			return Checkpoint{}, err
		}
		xorDigest(&documents, digest)
	}
	for _, receipt := range snapshot.Receipts {
		receipts = checkpointReceiptHash(receipts, receipt)
	}
	return Checkpoint{Version: 1, Revision: snapshot.Revision, DocumentsDigest: hex.EncodeToString(documents[:]), ReceiptsDigest: hex.EncodeToString(receipts[:])}, nil
}

// CaptureCheckpoint performs a full export for admission, explicit transfer or
// recovery. It must not be placed on an ordinary read/write path.
func CaptureCheckpoint(ctx context.Context, source SnapshotReader) (Checkpoint, error) {
	if client, ok := source.(*Client); ok {
		return client.captureCheckpoint(ctx)
	}
	snapshot, err := source.Export(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	return SnapshotCheckpoint(snapshot)
}

func VerifyCheckpoint(ctx context.Context, source SnapshotReader, expected Checkpoint) error {
	if _, _, err := decodeCheckpoint(expected); err != nil {
		return err
	}
	actual, err := CaptureCheckpoint(ctx, source)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("%w: document or receipt checkpoint differs (expected revision %d, received %d)", ErrMigrationRequired, expected.Revision, actual.Revision)
	}
	return nil
}

// NextCheckpoint predicts a new commit without mutating the provider. Its reads
// are pinned to before.Revision and restricted to keys changed by tx. The caller
// must hold the shared writer gate and durably record the exact transaction and
// before/after checkpoints before committing. This function is not a writer lock
// or an admission check. Idempotent receipt retries do not advance a checkpoint.
func NextCheckpoint(ctx context.Context, current Documents, before Checkpoint, tx Transaction) (Checkpoint, error) {
	documents, receipts, err := decodeCheckpoint(before)
	if err != nil {
		return Checkpoint{}, err
	}
	intent, err := transactionDigest(tx)
	if err != nil {
		return Checkpoint{}, err
	}
	if tx.ExpectedRevision != before.Revision {
		return Checkpoint{}, ErrConflict
	}
	namespaces := make(map[string][]string)
	for _, write := range tx.Writes {
		namespaces[write.Namespace] = append(namespaces[write.Namespace], write.Key)
	}
	names := make([]string, 0, len(namespaces))
	for name := range namespaces {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, namespace := range names {
		keys := namespaces[namespace]
		for len(keys) > 0 {
			count := min(len(keys), 256)
			batch := keys[:count]
			keys = keys[count:]
			query := Query{Namespace: namespace, Keys: batch, Limit: count, AtRevision: &before.Revision}
			view, err := current.Read(ctx, query)
			if err != nil {
				return Checkpoint{}, err
			}
			if view.Revision != before.Revision {
				return Checkpoint{}, ErrConflict
			}
			seen := make(map[string]bool, len(view.Documents))
			for _, document := range view.Documents {
				if document.Namespace != namespace || !matchesQuery(document, query) || seen[document.Key] {
					return Checkpoint{}, fmt.Errorf("%w: checkpoint read returned an unexpected document", ErrCorrupt)
				}
				seen[document.Key] = true
				digest, err := checkpointDocumentHash(document)
				if err != nil {
					return Checkpoint{}, err
				}
				xorDigest(&documents, digest)
			}
			if view.NextCursor != "" {
				return Checkpoint{}, fmt.Errorf("%w: checkpoint read exceeded requested keys", ErrCorrupt)
			}
		}
	}
	for _, write := range tx.Writes {
		if write.Delete {
			continue
		}
		digest, err := checkpointDocumentHash(Document{Namespace: write.Namespace, Key: write.Key, Value: write.Value})
		if err != nil {
			return Checkpoint{}, err
		}
		xorDigest(&documents, digest)
	}
	receipts = checkpointReceiptHash(receipts, CommitRecord{Receipt: Receipt{ID: tx.ID, Revision: before.Revision + 1}, Digest: intent})
	return Checkpoint{Version: 1, Revision: before.Revision + 1, DocumentsDigest: hex.EncodeToString(documents[:]), ReceiptsDigest: hex.EncodeToString(receipts[:])}, nil
}

// VerifySharedAuthority rejects a separate copy, even with identical content,
// by requiring it to observe a fresh write through the retained current binding.
// The caller must exclude all other writers and first verify full checkpoint
// equality. current must be the caller's durable guarded client: both probe and
// cleanup are real commits with receipts and must advance its host checkpoint.
// A provider must obey the single-authority Read/Commit contract after admission;
// the host's ordinary operation gate must still reject later divergent heads.
func VerifySharedAuthority(ctx context.Context, current, candidate Documents) (err error) {
	const namespace = "wuu.state.authority.v1"
	view, err := current.Read(ctx, Query{Namespace: namespace, Keys: []string{}})
	if err != nil {
		return err
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	nonce := hex.EncodeToString(random[:])
	value, _ := json.Marshal(nonce)
	probe := Transaction{ID: "state-authority-probe:" + nonce, ExpectedRevision: view.Revision, Writes: []Write{{Namespace: namespace, Key: nonce, Value: value}}}
	receipt, err := current.Commit(ctx, probe)
	if err != nil {
		return err
	}
	if receipt.ID != probe.ID || receipt.Revision != probe.ExpectedRevision+1 {
		return fmt.Errorf("%w: authority probe returned an invalid receipt", ErrCorrupt)
	}
	defer func() {
		_, cleanupErr := current.Commit(ctx, Transaction{ID: "state-authority-cleanup:" + nonce, ExpectedRevision: receipt.Revision, Writes: []Write{{Namespace: namespace, Key: nonce, Delete: true}}})
		err = errors.Join(err, cleanupErr)
	}()
	observed, err := candidate.Read(ctx, Query{Namespace: namespace, Keys: []string{nonce}, AtRevision: &receipt.Revision})
	if err != nil {
		return fmt.Errorf("%w: candidate did not observe current authority probe: %w", ErrMigrationRequired, err)
	}
	if observed.Revision != receipt.Revision || len(observed.Documents) != 1 || observed.Documents[0].Namespace != namespace || observed.Documents[0].Key != nonce || !bytes.Equal(observed.Documents[0].Value, value) {
		return fmt.Errorf("%w: candidate is not observing the retained writer", ErrMigrationRequired)
	}
	return nil
}

func decodeCheckpoint(checkpoint Checkpoint) (documents, receipts [sha256.Size]byte, err error) {
	documentBytes, documentErr := hex.DecodeString(checkpoint.DocumentsDigest)
	receiptBytes, receiptErr := hex.DecodeString(checkpoint.ReceiptsDigest)
	if checkpoint.Version != 1 || documentErr != nil || receiptErr != nil || len(documentBytes) != sha256.Size || len(receiptBytes) != sha256.Size {
		return documents, receipts, fmt.Errorf("%w: invalid authority checkpoint", ErrCorrupt)
	}
	copy(documents[:], documentBytes)
	copy(receipts[:], receiptBytes)
	return documents, receipts, nil
}

func checkpointDocumentHash(document Document) ([sha256.Size]byte, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("%w: checkpoint document: %v", ErrCorrupt, err)
	}
	return sha256.Sum256(raw), nil
}

func checkpointReceiptHash(previous [sha256.Size]byte, receipt CommitRecord) [sha256.Size]byte {
	raw, _ := json.Marshal(receipt)
	hash := sha256.New()
	hash.Write(previous[:])
	hash.Write(raw)
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func xorDigest(target *[sha256.Size]byte, value [sha256.Size]byte) {
	for i := range target {
		target[i] ^= value[i]
	}
}
