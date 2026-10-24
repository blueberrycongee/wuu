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
)

// Published snapshots must survive reopen, retain digest/ownership on fork,
// reject changed source bytes, and stop on provider shutdown without fallback.
func TestArtifactProviderPersistenceForkAndRevocation(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	state := filepath.Join(home, "workspaces", "workspace")
	provider := NewArtifactService(ArtifactModule, home, state)
	client := artifactProviderClient(provider)
	scope := ArtifactScope{ThreadID: "source", ExecutionID: "execution", PluginID: "test"}
	input := pluginhost.ArtifactImportParams{Data: base64.StdEncoding.EncodeToString([]byte("immutable snapshot")), Name: "report.txt"}
	if _, err := client.Import(ctx, scope, input, false); err == nil {
		t.Fatal("prepared provider wrote artifact")
	}
	if err := provider.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	artifact, err := client.Import(ctx, scope, input, false)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := client.Fork(ctx, "source", "fork", artifact.URI)
	if err != nil {
		t.Fatal(err)
	}
	if fork.ID != artifact.ID || fork.SHA256 != artifact.SHA256 || !strings.Contains(fork.URI, "/fork/") {
		t.Fatalf("fork lost identity: %+v", fork)
	}
	if again, err := client.Fork(ctx, "source", "fork", artifact.URI); err != nil || again.URI != fork.URI {
		t.Fatalf("fork was not retryable: %+v %v", again, err)
	}
	if _, _, err := client.Read(ctx, "other", artifact.URI, 1024); err == nil {
		t.Fatal("cross-thread artifact read")
	}
	if err := os.RemoveAll(filepath.Join(state, "sessions", "source")); err != nil {
		t.Fatal(err)
	}
	data, _, err := client.Read(ctx, "fork", fork.URI, 1024)
	if err != nil || string(data) != "immutable snapshot" {
		t.Fatalf("fork lost bytes: %q %v", data, err)
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.Read(ctx, "fork", fork.URI, 1024); err == nil {
		t.Fatal("closed provider read")
	}
	next := NewArtifactService(ArtifactModule, home, state)
	if err := next.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	data, _, err = artifactProviderClient(next).Read(ctx, "fork", fork.URI, 1024)
	if err != nil || string(data) != "immutable snapshot" {
		t.Fatalf("reopen: %q %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(state, "sessions", "fork", "artifacts", fork.ID, fork.Name), []byte("changed snapshots!"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := artifactProviderClient(next).Read(ctx, "fork", fork.URI, 1024); err == nil {
		t.Fatal("changed digest was accepted")
	}
}
func TestArtifactClientNeverRetriesUncertainImport(t *testing.T) {
	attempts := 0
	client := NewArtifactClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		attempts++
		return nil, errors.New("unknown outcome")
	})
	if _, err := client.Import(context.Background(), ArtifactScope{ThreadID: "thread"}, pluginhost.ArtifactImportParams{Path: "/path"}, false); err == nil || attempts != 1 {
		t.Fatalf("import attempts=%d err=%v", attempts, err)
	}
}
func artifactProviderClient(provider *artifactService) *ArtifactClient {
	return NewArtifactClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ArtifactServiceName, Method: method, Params: raw})
	})
}
