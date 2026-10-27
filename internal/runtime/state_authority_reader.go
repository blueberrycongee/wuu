package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func (a *StateAuthority) readRecord() (*authorityRecord, error) {
	raw, err := os.ReadFile(filepath.Join(a.dir, "head.json"))
	if errors.Is(err, os.ErrNotExist) {
		if _, markerErr := os.Stat(filepath.Join(a.dir, "initialized")); markerErr == nil {
			return nil, fmt.Errorf("%w: authority checkpoint was removed", persistence.ErrCorrupt)
		} else if !errors.Is(markerErr, os.ErrNotExist) {
			return nil, markerErr
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var envelope authorityEnvelope
	if err := decodeAuthorityHeadJSON(raw, &envelope); err != nil {
		return nil, persistence.ErrCorrupt
	}
	hash := sha256.Sum256(envelope.Payload)
	if hex.EncodeToString(hash[:]) != envelope.Digest {
		return nil, fmt.Errorf("%w: authority head checksum", persistence.ErrCorrupt)
	}
	var record authorityRecord
	if err := decodeAuthorityHeadJSON(envelope.Payload, &record); err != nil || record.Version != 1 {
		return nil, persistence.ErrCorrupt
	}
	for _, digest := range []string{record.Checkpoint.DocumentsDigest, record.Checkpoint.ReceiptsDigest} {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size {
			return nil, persistence.ErrCorrupt
		}
	}
	if record.Checkpoint.Version != 1 {
		return nil, persistence.ErrCorrupt
	}
	if pending := record.Pending; pending != nil && (pending.Before != record.Checkpoint || pending.After.Revision != pending.Before.Revision+1 || pending.Transaction.ExpectedRevision != pending.Before.Revision) {
		return nil, persistence.ErrCorrupt
	}
	return &record, nil
}

// Head bytes belong to readRecord. Borrow their exact ranges so the payload
// checksum and retained pending intent share one immutable file buffer. Unlike
// service requests, existing heads permit unknown members under v1 semantics.
func decodeAuthorityHeadJSON(raw []byte, target any) error {
	values := jsonv2.UnmarshalFromFunc(func(dec *jsontext.Decoder, value *json.RawMessage) error {
		encoded, err := dec.ReadValue()
		if err != nil {
			return err
		}
		end := dec.InputOffset()
		// ReadValue expires on the next read; retain the owned input, not its view.
		*value = raw[end-int64(len(encoded)) : end : end]
		return nil
	})
	return jsonv2.Unmarshal(raw, target, json.DefaultOptionsV1(), jsonv2.WithUnmarshalers(values))
}
