package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestPresentArtifactBoundaryAndClone(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if kit.LookupTool("present_artifact") != nil {
		t.Fatal("publisher advertised without storage")
	}
	var requests []ArtifactPublishRequest
	kit.SetArtifactPublisher(func(_ context.Context, request ArtifactPublishRequest) (toolresult.ContentPart, error) {
		requests = append(requests, request)
		return toolresult.ContentPart{Type: "image", MIMEType: "image/svg+xml", URI: "wuu-artifact://workspace/thread/id/chart.svg"}, nil
	})
	kit.SetSessionID("parent")
	clone, err := kit.CloneForRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	clone.SetSessionID("child")
	clone.SetStateDir(filepath.Join(root, "state"))
	clone.SetBoundary(ReadOnlyBoundary())
	for _, provider := range []string{"openai", "anthropic", "generic"} {
		clone.ConfigureSurfaceForProviderModel(provider, "test-model", true)
		visible := false
		for _, def := range clone.Definitions() {
			if def.Name == "present_artifact" {
				visible = true
			}
		}
		if !visible {
			t.Fatalf("present_artifact hidden for %s", provider)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "chart.svg"), []byte(`<svg/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := clone.ExecuteResult(context.Background(), providers.ToolCall{ID: "publish-call", Name: "present_artifact", Arguments: `{"path":"chart.svg"}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "image" {
		t.Fatalf("lost rich result: %+v", result)
	}
	if len(requests) != 1 || requests[0].ThreadID != "child" || requests[0].CallID != "publish-call" || requests[0].Path != filepath.Join(clone.RootDir(), "chart.svg") {
		t.Fatalf("bad execution scope: %+v", requests)
	}
	outside := filepath.Join(t.TempDir(), "outside.svg")
	if err := os.WriteFile(outside, []byte(`<svg/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.svg")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TEST_ONLY=fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(root, "hidden.svg")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", outside, "escape.svg", ".env", "hidden.svg"} {
		args, _ := json.Marshal(map[string]string{"path": path})
		if _, err := clone.ExecuteResult(context.Background(), providers.ToolCall{Name: "present_artifact", Arguments: string(args)}); err == nil {
			t.Errorf("accepted invalid/sensitive path %q", path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := clone.ExecuteResult(ctx, providers.ToolCall{Name: "present_artifact", Arguments: `{"path":"chart.svg"}`}); err == nil {
		t.Fatal("ignored cancellation")
	}
	if len(requests) != 1 {
		t.Fatalf("denied request reached publisher: %+v", requests)
	}
}

func TestPresentArtifactUsesBoundWorktree(t *testing.T) {
	kit, parent, worktree, ctx := newWorktreeExecFixture(t)
	// Real hosted checkouts live beneath WUU_HOME; its name must not be
	// mistaken for a request to read unrelated runtime metadata.
	worktree = filepath.Join(worktree, ".wuu", "checkout")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx = toolctx.WithWorktreePath(ctx, worktree)
	worktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionID("thread")
	for _, dir := range []string{parent, worktree} {
		if err := os.WriteFile(filepath.Join(dir, "chart.svg"), []byte(`<svg/>`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	kit.SetArtifactPublisher(func(_ context.Context, request ArtifactPublishRequest) (toolresult.ContentPart, error) {
		if request.Path != filepath.Join(worktree, "chart.svg") {
			t.Fatalf("wrong checkout: %s", request.Path)
		}
		return toolresult.ContentPart{Type: "image", MIMEType: "image/svg+xml", URI: "wuu-artifact://workspace/thread/id/chart.svg"}, nil
	})
	if _, err := kit.ExecuteResult(ctx, providers.ToolCall{Name: "present_artifact", Arguments: `{"path":"chart.svg"}`}); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.svg")
	if err := os.WriteFile(outside, []byte(`<svg/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(worktree, "escape.svg")); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.ExecuteResult(ctx, providers.ToolCall{Name: "present_artifact", Arguments: `{"path":"escape.svg"}`}); err == nil {
		t.Fatal("accepted a worktree symlink outside the file boundary")
	}
}
