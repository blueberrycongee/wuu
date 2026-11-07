package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

// Snapshot publication must survive lost replies and partial commits without
// exposing incomplete bytes, leaking across owners, or re-reading deleted history.
type interruptedSnapshotDocuments struct {
	persistence.Documents
	commits int
	failAt  int
}

func (d *interruptedSnapshotDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	d.commits++
	r, err := d.Documents.Commit(ctx, tx)
	if err == nil && d.commits == d.failAt {
		return r, errors.New("lost commit reply")
	}
	return r, err
}

func TestAttachmentSnapshotDurableChunksAndIsolation(t *testing.T) {
	ctx := context.Background()
	documents := testSessionDocuments(t, t.TempDir())
	store := session.NewStore(ctx, documents)
	_, err := store.CreateManagedWithMetadata("source", t.TempDir(), session.ManagedMetadata{Owner: "plugin:personal", Visibility: "plugin"})
	if err != nil {
		t.Fatal(err)
	}
	data := strings.Repeat("x", pluginhost.MaxSessionHandoffBytes)
	digest := sha256.Sum256([]byte(data))
	encoded := base64.StdEncoding.EncodeToString([]byte(data))
	files, _ := json.Marshal([]map[string]string{{"filename": "report.pdf", "media_type": "application/pdf", "data": encoded}})
	if err := store.AppendHistoryRecord("source", session.HistoryRecord{Role: "user", Origin: "user", Files: files}); err != nil {
		t.Fatal(err)
	}
	p := pluginhost.SessionAttachmentReadParams{SessionID: "source", Seq: 1, Kind: "file", SHA256: hex.EncodeToString(digest[:])}
	flaky := &interruptedSnapshotDocuments{Documents: documents, failAt: 2}
	if _, err := snapshotPluginSessionAttachment(ctx, store, nil, flaky, "personal", userspace.Binding{}, p); err == nil {
		t.Fatal("lost commit was hidden")
	}
	first, err := snapshotPluginSessionAttachment(ctx, store, nil, flaky, "personal", userspace.Binding{}, p)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("20 MiB snapshot: %d decoded bytes, %d base64 bytes, %d data chunks", len(data), len(encoded), (len(encoded)+attachmentSnapshotChunk-1)/attachmentSnapshotChunk)
	commits := flaky.commits
	second, err := snapshotPluginSessionAttachment(ctx, store, nil, flaky, "personal", userspace.Binding{}, p)
	if err != nil || first.SnapshotID != second.SnapshotID || flaky.commits != commits {
		t.Fatalf("snapshot duplicated: %+v %v", second, err)
	}
	// A fresh session-domain client with metadata but no source row models history
	// removal after a restart. The authorized immutable snapshot remains readable.
	restored := testSessionStore(t, t.TempDir())
	_, _ = restored.CreateManagedWithMetadata("source", t.TempDir(), session.ManagedMetadata{Owner: "plugin:personal", Visibility: "plugin"})
	p.SnapshotID = first.SnapshotID
	var received strings.Builder
	for received.Len() < len(encoded) {
		p.Offset = received.Len()
		part, err := readPluginAttachmentSnapshot(ctx, restored, documents, "personal", userspace.Binding{}, p)
		if err != nil {
			t.Fatal(err)
		}
		if len(part.Data) > 128*1024 || part.Total != len(encoded) || part.ContentType != "application/pdf" {
			t.Fatalf("bad chunk: %d", len(part.Data))
		}
		received.WriteString(part.Data)
	}
	if received.String() != encoded {
		t.Fatal("snapshot bytes changed")
	}
	for _, change := range []string{"plugin", "source", "digest", "domain"} {
		t.Run(change, func(t *testing.T) {
			q, plugin, binding := p, "personal", userspace.Binding{}
			switch change {
			case "plugin":
				plugin = "other"
			case "source":
				q.Seq++
			case "digest":
				q.SHA256 = strings.Repeat("0", 64)
			case "domain":
				binding = userspace.Binding{UserSpaceID: "other", ProductDomain: "personal"}
			}
			if _, err := readPluginAttachmentSnapshot(ctx, restored, documents, plugin, binding, q); err == nil {
				t.Fatal("snapshot crossed scope")
			}
		})
	}
	// Corrupting one committed chunk must fail, never silently deliver it.
	p.Offset = 0
	if err := persistence.Update(ctx, documents, func(tx *persistence.Tx) error {
		return tx.Put(attachmentSnapshotNamespace, first.SnapshotID+"/0", "eA==")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := readPluginAttachmentSnapshot(ctx, restored, documents, "personal", userspace.Binding{}, p); err == nil {
		t.Fatal("corrupt chunk accepted")
	}
}
