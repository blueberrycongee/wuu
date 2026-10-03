package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestPorcelainV2DirtyPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		record string
		path   string
		dir    bool
	}{
		{"? scratch/", "scratch/", true},
		{"? scratch/a.go", "scratch/a.go", false},
		{"1 .M N... 100644 100644 100644 abc def VERSION", "VERSION", false},
		{"u UU N... 100644 100644 100644 100644 a b c conflict.txt", "conflict.txt", false},
		{"2 R. N... 100644 100644 100644 abc def R100 new.txt\told.txt", "new.txt", false},
		{"! ignored.txt", "", false},
		{"# branch.oid abc", "", false},
	}
	for _, tt := range tests {
		path, dir := porcelainV2DirtyPath([]byte(tt.record))
		if path != tt.path || dir != tt.dir {
			t.Errorf("%q -> path=%q dir=%v, want %q %v", tt.record, path, dir, tt.path, tt.dir)
		}
	}
}

func TestWorkspaceRevisionChangesOnEqualLengthDirtyEdit(t *testing.T) {
	kit, root := setupGitRepo(t)
	path := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(path, []byte("needle alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := workspaceRevision(ctx, kit.env.RootDir)
	if first == "" || !strings.HasPrefix(first, "git:") {
		t.Fatalf("dirty revision %q", first)
	}
	status1 := gitPorcelain(t, root)
	if err := os.WriteFile(path, []byte("nothing here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status2 := gitPorcelain(t, root)
	if status1 != status2 {
		t.Fatalf("expected identical porcelain, got\n%s\nvs\n%s", status1, status2)
	}
	second := workspaceRevision(ctx, kit.env.RootDir)
	if second == first {
		t.Fatalf("revision stayed %q after equal-length dirty edit", first)
	}
}

func TestGrepCacheInvalidatesAfterEqualLengthDirtyEdit(t *testing.T) {
	kit, root := setupGitRepo(t)
	kit.SetSessionDir(t.TempDir())
	kit.SetToolSearchEnabled(false)
	path := filepath.Join(root, "hello.txt")
	ctx := context.Background()

	if err := os.WriteFile(path, []byte("needle alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := grepTotal(t, kit, ctx, "needle"); got != 1 {
		t.Fatalf("after first edit total=%d want 1", got)
	}

	if err := os.WriteFile(path, []byte("nothing here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := grepTotal(t, kit, ctx, "needle"); got != 0 {
		t.Fatalf("after second edit total=%d want 0 (stale cache)", got)
	}
}

func TestGlobCacheSeesFileAddedInUntrackedDir(t *testing.T) {
	kit, root := setupGitRepo(t)
	kit.SetSessionDir(t.TempDir())
	kit.SetToolSearchEnabled(false)
	dir := filepath.Join(root, "scratch")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := globFiles(t, kit, ctx, "scratch/*.go")
	if len(first) != 1 || first[0] != "scratch/a.go" {
		t.Fatalf("first glob=%v", first)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := globFiles(t, kit, ctx, "scratch/*.go")
	if len(second) != 2 {
		t.Fatalf("second glob=%v want a.go and b.go", second)
	}
}

func gitPorcelain(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain=v2", "-z", "--branch")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	return string(out)
}

func grepTotal(t *testing.T, kit *Toolkit, ctx context.Context, pattern string) int {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"pattern": pattern, "path": "hello.txt"})
	out, err := kit.Execute(ctx, providers.ToolCall{Name: "grep", Arguments: string(args)})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	var parsed struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("parse grep %s: %v", out, err)
	}
	return parsed.Total
}

func globFiles(t *testing.T, kit *Toolkit, ctx context.Context, pattern string) []string {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"pattern": pattern})
	out, err := kit.Execute(ctx, providers.ToolCall{Name: "glob", Arguments: string(args)})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var parsed struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("parse glob %s: %v", out, err)
	}
	return parsed.Files
}
