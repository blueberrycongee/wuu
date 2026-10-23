package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/userspace"
	"strings"
	"testing"
)

// Persisted attachment reads are owner/scoped, digest checked and bounded; the
// history projection must never expose local paths or inline attachment bytes.
func TestPluginHistoryAttachmentReadIsScopedImmutableAndChunked(t *testing.T) {
	home := t.TempDir()
	dir := statepath.SessionsDir(home)
	_, err := session.CreateManagedWithMetadata(dir, "private", t.TempDir(), session.ManagedMetadata{Owner: "plugin:personal", Visibility: pluginhost.SessionVisibilityPlugin})
	if err != nil {
		t.Fatal(err)
	}
	data := strings.Repeat("x", 150000)
	digest := sha256.Sum256([]byte(data))
	sum := hex.EncodeToString(digest[:])
	files, _ := json.Marshal([]providers.InputFile{{Filename: "record.txt", MediaType: "text/plain", Data: base64.StdEncoding.EncodeToString([]byte(data)), LocalPath: "/private/never-expose"}})
	if err := session.AppendHistoryRecord(dir, "private", session.HistoryRecord{Role: "user", Content: "read", Files: files}); err != nil {
		t.Fatal(err)
	}
	page, err := readPluginSessionHistory(context.Background(), home, "personal", userspace.Binding{}, pluginhost.SessionHistoryReadParams{SessionID: "private", StartSeq: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 1 || len(page.Records[0].Attachments) != 1 {
		t.Fatalf("metadata missing: %+v", page)
	}
	meta := page.Records[0].Attachments[0]
	if meta.SHA256 != sum || meta.Name != "record.txt" || meta.Size != 150000 {
		t.Fatalf("metadata: %+v", meta)
	}
	projected, _ := json.Marshal(page)
	if strings.Contains(string(projected), "never-expose") || strings.Contains(string(projected), "eHh4eHh4") {
		t.Fatal("history leaked file path or bytes")
	}
	params := pluginhost.SessionAttachmentReadParams{SessionID: "private", Seq: 1, Kind: "file", Index: 0, SHA256: sum}
	chunk, err := readPluginSessionAttachment(context.Background(), home, "personal", params)
	if err != nil || len(chunk.Data) > 128*1024 || chunk.Total != len(base64.StdEncoding.EncodeToString([]byte(data))) {
		t.Fatalf("chunk: %d %+v %v", len(chunk.Data), chunk, err)
	}
	params.Offset = len(chunk.Data)
	last, err := readPluginSessionAttachment(context.Background(), home, "personal", params)
	if err != nil || chunk.Data+last.Data != base64.StdEncoding.EncodeToString([]byte(data)) {
		t.Fatal("chunk recovery failed", err)
	}
	if _, err := readPluginSessionAttachment(context.Background(), home, "other", params); err == nil {
		t.Fatal("foreign plugin read private attachment")
	}
	if _, err := readPluginSessionAttachment(context.Background(), t.TempDir(), "personal", params); err == nil {
		t.Fatal("foreign domain root read attachment")
	}
	params.SHA256 = strings.Repeat("0", 64)
	if _, err := readPluginSessionAttachment(context.Background(), home, "personal", params); err == nil {
		t.Fatal("digest mismatch accepted")
	}
}

func TestPluginAttachmentSearchDoesNotExposeOrMatchWorkingCopyPaths(t *testing.T) {
	home := t.TempDir()
	dir := statepath.SessionsDir(home)
	if _, err := session.CreateWithMetadata(dir, "source", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	files, _ := json.Marshal([]providers.InputFile{{Filename: "record.txt", MediaType: "text/plain", Data: "eA==", LocalPath: "/private/.input-files/session/file.txt"}})
	if err := session.AppendHistoryRecord(dir, "source", session.HistoryRecord{Role: "user", Origin: "user", Content: "\n\nAttached files: record.txt at /private/.input-files/session/file.txt", Files: files}); err != nil {
		t.Fatal(err)
	}
	hidden, err := searchPluginSessionHistory(context.Background(), home, "demo", pluginhost.SessionHistorySearchParams{SessionID: "source", Query: ".input-files"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden.Matches) != 0 {
		t.Fatalf("hidden working-copy text matched visible history: %+v", hidden.Matches)
	}
	visible, err := searchPluginSessionHistory(context.Background(), home, "demo", pluginhost.SessionHistorySearchParams{SessionID: "source", Query: "record.txt"})
	if err != nil || len(visible.Matches) != 1 {
		t.Fatalf("filename lookup failed: %+v %v", visible, err)
	}
	if strings.Contains(visible.Matches[0].Excerpt, "/private/") {
		t.Fatal("filename result leaked local path")
	}
}
