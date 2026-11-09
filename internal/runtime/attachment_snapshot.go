package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

const attachmentSnapshotNamespace = "session.attachment.snapshots.v1"
const attachmentSnapshotChunk = 512 * 1024 // Base64 characters; independently persisted.

type attachmentSnapshotManifest struct {
	Attachment pluginhost.SessionHistoryAttachment `json:"attachment"`
	Chunks     []string                            `json:"chunks"`
}

func attachmentSnapshotID(pluginID string, binding userspace.Binding, p pluginhost.SessionAttachmentReadParams) string {
	raw, _ := json.Marshal([]any{pluginID, binding, p.SessionID, p.Seq, p.Kind, p.Index, p.SHA256})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func snapshotPluginSessionAttachment(ctx context.Context, store *session.Store, artifacts *ArtifactClient, documents persistence.Documents, pluginID string, binding userspace.Binding, p pluginhost.SessionAttachmentReadParams) (pluginhost.SessionAttachmentSnapshotResult, error) {
	var result pluginhost.SessionAttachmentSnapshotResult
	if documents == nil || p.SnapshotID != "" || p.Offset != 0 || p.Seq < 1 || p.Index < 0 || len(p.SHA256) != 64 {
		return result, errors.New("invalid attachment snapshot source")
	}
	if _, err := loadVisibleHistorySession(ctx, store, pluginID, p.SessionID, binding); err != nil {
		return result, err
	}
	id := attachmentSnapshotID(pluginID, binding, p)
	view, err := documents.Read(ctx, persistence.Query{Namespace: attachmentSnapshotNamespace, Keys: []string{id}})
	if err != nil {
		return result, err
	}
	var manifest attachmentSnapshotManifest
	if len(view.Documents) > 0 {
		if err = json.Unmarshal(view.Documents[0].Value, &manifest); err != nil {
			return result, err
		}
		return pluginhost.SessionAttachmentSnapshotResult{SnapshotID: id, Attachment: manifest.Attachment}, nil
	}
	page, err := store.WithContext(ctx).ReadHistoryQuery(session.HistoryReadQuery{SessionID: p.SessionID, StartSeq: p.Seq, EndSeq: p.Seq, Limit: 1})
	if err != nil {
		return result, err
	}
	if len(page.Records) != 1 || page.Records[0].Seq != p.Seq || page.Records[0].Hidden || page.Records[0].Truncated {
		return result, errors.New("attachment snapshot source is unavailable")
	}
	for _, a := range recordAttachments(page.Records[0]) {
		meta := a.metadata
		if meta.Kind != p.Kind || meta.Index != p.Index || meta.SHA256 != p.SHA256 {
			continue
		}
		if meta.Size <= 0 || meta.Size > pluginhost.MaxSessionHandoffBytes {
			return result, errors.New("attachment snapshot exceeds 20 MiB")
		}
		data := a.data
		if a.uri != "" {
			bytes, media, err := artifacts.Read(ctx, p.SessionID, a.uri, pluginhost.MaxSessionHandoffBytes)
			if err != nil {
				return result, err
			}
			sum := sha256.Sum256(bytes)
			if media != meta.MIMEType || int64(len(bytes)) != meta.Size || hex.EncodeToString(sum[:]) != meta.SHA256 {
				return result, errors.New("attachment changed")
			}
			data = base64.StdEncoding.EncodeToString(bytes)
		}
		manifest.Attachment = meta
		// Commit immutable chunks first, then publish the manifest. A lost reply
		// or interrupted upload retries identical keys; incomplete data is unreadable.
		for start := 0; start < len(data); start += attachmentSnapshotChunk {
			chunk := data[start:min(start+attachmentSnapshotChunk, len(data))]
			sum := sha256.Sum256([]byte(chunk))
			key := fmt.Sprintf("%s/%d", id, len(manifest.Chunks))
			manifest.Chunks = append(manifest.Chunks, hex.EncodeToString(sum[:]))
			if err = persistence.Update(ctx, documents, func(tx *persistence.Tx) error {
				var existing string
				found, err := tx.Get(attachmentSnapshotNamespace, key, &existing)
				if err != nil {
					return err
				}
				if found {
					if existing != chunk {
						return errors.New("attachment snapshot chunk conflict")
					}
					return nil
				}
				return tx.Put(attachmentSnapshotNamespace, key, chunk)
			}); err != nil {
				return result, err
			}
		}
		if err = persistence.Update(ctx, documents, func(tx *persistence.Tx) error {
			var existing attachmentSnapshotManifest
			found, err := tx.Get(attachmentSnapshotNamespace, id, &existing)
			if err != nil {
				return err
			}
			if found {
				return nil
			}
			return tx.Put(attachmentSnapshotNamespace, id, manifest)
		}); err != nil {
			return result, err
		}
		return pluginhost.SessionAttachmentSnapshotResult{SnapshotID: id, Attachment: meta}, nil
	}
	return result, errors.New("attachment changed or was removed")
}

func readPluginAttachmentSnapshot(ctx context.Context, store *session.Store, documents persistence.Documents, pluginID string, binding userspace.Binding, p pluginhost.SessionAttachmentReadParams) (pluginhost.SessionAttachmentReadResult, error) {
	var result pluginhost.SessionAttachmentReadResult
	if documents == nil || p.SnapshotID == "" || p.SnapshotID != attachmentSnapshotID(pluginID, binding, p) || p.Offset < 0 {
		return result, errors.New("attachment snapshot scope mismatch")
	}
	if _, err := loadVisibleHistorySession(ctx, store, pluginID, p.SessionID, binding); err != nil {
		return result, err
	}
	view, err := documents.Read(ctx, persistence.Query{Namespace: attachmentSnapshotNamespace, Keys: []string{p.SnapshotID}})
	if err != nil {
		return result, err
	}
	if len(view.Documents) != 1 {
		return result, errors.New("attachment snapshot is incomplete or unavailable")
	}
	var manifest attachmentSnapshotManifest
	if err = json.Unmarshal(view.Documents[0].Value, &manifest); err != nil {
		return result, err
	}
	meta := manifest.Attachment
	if meta.SHA256 != p.SHA256 || meta.Size <= 0 || meta.Size > pluginhost.MaxSessionHandoffBytes {
		return result, errors.New("invalid attachment snapshot manifest")
	}
	total := base64.StdEncoding.EncodedLen(int(meta.Size))
	if p.Offset >= total || len(manifest.Chunks) != (total+attachmentSnapshotChunk-1)/attachmentSnapshotChunk {
		return result, errors.New("invalid attachment snapshot offset or chunks")
	}
	index := p.Offset / attachmentSnapshotChunk
	view, err = documents.Read(ctx, persistence.Query{Namespace: attachmentSnapshotNamespace, Keys: []string{fmt.Sprintf("%s/%d", p.SnapshotID, index)}})
	if err != nil {
		return result, err
	}
	if len(view.Documents) != 1 {
		return result, errors.New("attachment snapshot chunk is missing")
	}
	var chunk string
	if err = json.Unmarshal(view.Documents[0].Value, &chunk); err != nil {
		return result, err
	}
	sum := sha256.Sum256([]byte(chunk))
	if hex.EncodeToString(sum[:]) != manifest.Chunks[index] || len(chunk) != min(attachmentSnapshotChunk, total-index*attachmentSnapshotChunk) {
		return result, errors.New("attachment snapshot chunk is corrupt")
	}
	start := p.Offset % attachmentSnapshotChunk
	return pluginhost.SessionAttachmentReadResult{Data: chunk[start:min(len(chunk), start+128*1024)], Offset: p.Offset, Total: total, ContentType: meta.MIMEType, Name: meta.Name, SHA256: meta.SHA256}, nil
}
