package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// Thread cleanup owns this provider's payloads only, survives retries after
// document deletion, and preserves independently copied fork artifacts.
func TestArtifactDeleteThreadIsIdempotentAndScoped(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	dir := filepath.Join(home, "workspaces", "test")
	provider := NewArtifactService(ArtifactModule, home, dir)
	if err := provider.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	client := artifactProviderClient(provider)
	artifact, err := client.Import(ctx, ArtifactScope{ThreadID: "source"}, pluginhost.ArtifactImportParams{Data: base64.StdEncoding.EncodeToString([]byte("payload")), Name: "report.txt"}, false)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := client.Fork(ctx, "source", "fork", artifact.URI)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(statepath.SessionArtifactDir(dir, "source"), "index-owned.json")
	if err := os.WriteFile(unrelated, []byte("index"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		receipt, err := client.DeleteThread(ctx, "source")
		if err != nil || !receipt.Deleted || receipt.ThreadID != "source" {
			t.Fatalf("delete receipt: %+v %v", receipt, err)
		}
	}
	if _, _, err := client.Read(ctx, "source", artifact.URI, 100); err == nil {
		t.Fatal("deleted payload still readable")
	}
	if raw, _, err := client.Read(ctx, "fork", fork.URI, 100); err != nil || string(raw) != "payload" {
		t.Fatalf("delete damaged independent fork: %q %v", raw, err)
	}
	if raw, err := os.ReadFile(unrelated); err != nil || string(raw) != "index" {
		t.Fatalf("artifact provider deleted other owner: %v", err)
	}
	if _, err := client.DeleteThread(ctx, ".."); err == nil {
		t.Fatal("delete accepted traversal")
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeleteThread(ctx, "source"); err == nil {
		t.Fatal("closed deletion fell back")
	}
}

func TestArtifactDeleteThreadUnknownOutcomeIsNotReplayed(t *testing.T) {
	calls := 0
	client := NewArtifactClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return nil, errors.New("private-backend-detail")
	})
	if _, err := client.DeleteThread(context.Background(), "thread"); err == nil || calls != 1 || strings.Contains(err.Error(), "private-backend-detail") {
		t.Fatalf("unknown deletion retried or exposed backend: calls=%d err=%v", calls, err)
	}
}
