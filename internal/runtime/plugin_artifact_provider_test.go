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

// The range contract must preserve exact snapshot identity, reject changed bytes
// and invalid bounds, honor cancellation, and never emulate ranges by returning
// a full response from a provider that does not implement read-range.
func TestArtifactProviderRangeContract(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	provider := NewArtifactService(ArtifactModule, home, filepath.Join(home, "workspaces", "workspace"))
	if err := provider.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	client := artifactProviderClient(provider)
	artifact, err := client.Import(ctx, ArtifactScope{ThreadID: "thread", PluginID: "test"}, pluginhost.ArtifactImportParams{Data: base64.StdEncoding.EncodeToString([]byte("0123456789")), Name: "report.pdf", MIMEType: "application/pdf"}, false)
	if err != nil {
		t.Fatal(err)
	}
	request := ArtifactRangeRequest{ArtifactReadRequest: ArtifactReadRequest{ArtifactResolveRequest: ArtifactResolveRequest{ThreadID: "thread", URI: artifact.URI}, MaxBytes: 3}, Offset: 4, SHA256: artifact.SHA256}
	result, err := client.ReadRange(ctx, request)
	if err != nil || string(result.Data) != "456" || result.Offset != 4 || result.Total != 10 || result.SHA256 != artifact.SHA256 {
		t.Fatalf("range=%+v err=%v", result, err)
	}
	wrong := request
	wrong.SHA256 = strings.Repeat("0", 64)
	if _, err := client.ReadRange(ctx, wrong); err == nil {
		t.Fatal("wrong digest accepted")
	}
	wrong = request
	wrong.Offset = 11
	if _, err := client.ReadRange(ctx, wrong); err == nil {
		t.Fatal("invalid bounds accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := client.ReadRange(canceled, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	path := filepath.Join(home, "workspaces", "workspace", "sessions", "thread", "artifacts", artifact.ID, artifact.Name)
	if err := os.WriteFile(path, []byte("0123CHANGED"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReadRange(ctx, request); err == nil {
		t.Fatal("changed bytes accepted")
	}
}
