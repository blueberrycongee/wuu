package memory

import (
	"os"
	"path/filepath"
	"testing"
)

// testOpts returns Options that scan only the given dirs (no defaults
// like ~/.claude that could leak from the host).
func testOpts(userDirs []string) Options {
	o := DefaultOptions()
	o.UserDirs = userDirs
	return o
}

func TestDiscover_EmptyDirs(t *testing.T) {
	files := Discover("", "", testOpts(nil))
	if len(files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(files))
	}
}

func TestDiscover_UserDirOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agents"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("claude"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := Discover("", "", testOpts([]string{dir}))
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	// AGENTS.md comes first per default Filenames priority.
	if files[0].Name != "AGENTS.md" || files[1].Name != "CLAUDE.md" {
		t.Errorf("unexpected order: %s, %s", files[0].Name, files[1].Name)
	}
	for _, f := range files {
		if f.Source != "user" {
			t.Errorf("expected source=user, got %q", f.Source)
		}
	}
}

func TestDiscover_ProjectHierarchyWithGitMarker(t *testing.T) {
	// Create: tmp/repo/.git, tmp/repo/AGENTS.md, tmp/repo/sub/CLAUDE.md
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("repo-agents"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "CLAUDE.md"), []byte("sub-claude"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := Discover(sub, "", testOpts(nil))
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %+v", len(files), files)
	}
	// Walk goes from project root → workspace, so repo/AGENTS.md first.
	if files[0].Content != "repo-agents" {
		t.Errorf("expected first = repo-agents, got %q", files[0].Content)
	}
	if files[1].Content != "sub-claude" {
		t.Errorf("expected second = sub-claude, got %q", files[1].Content)
	}
}

func TestDiscover_ProjectRootMarkerStopsWalk(t *testing.T) {
	// Create AGENTS.md ABOVE the .git marker — should be ignored.
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	repo := filepath.Join(parent, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("above-marker"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("inside-repo"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := Discover(repo, "", testOpts(nil))
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %+v", len(files), files)
	}
	if files[0].Content != "inside-repo" {
		t.Errorf("got %q", files[0].Content)
	}
}

func TestDiscover_NoMarkerOnlyCwd(t *testing.T) {
	// No .git anywhere — only the workspace dir itself contributes.
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	cwd := filepath.Join(parent, "cwd")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("parent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("cwd"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := Discover(cwd, "", testOpts(nil))
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %+v", len(files), files)
	}
	if files[0].Content != "cwd" {
		t.Errorf("got %q", files[0].Content)
	}
}

func TestDiscover_AgentsOverrideTakesPrecedence(t *testing.T) {
	// Both AGENTS.md and AGENTS.override.md exist — both are loaded
	// but the override comes second in the file list (more specific).
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.override.md"), []byte("override"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := Discover(root, "", testOpts(nil))
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[0].Name != "AGENTS.md" || files[1].Name != "AGENTS.override.md" {
		t.Errorf("unexpected order: %s, %s", files[0].Name, files[1].Name)
	}
}

func TestDiscover_MultipleUserDirs(t *testing.T) {
	wuuDir := t.TempDir()
	claudeDir := t.TempDir()
	codexDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wuuDir, "AGENTS.md"), []byte("wuu"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("claude"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "AGENTS.md"), []byte("codex"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := Discover("", "", testOpts([]string{wuuDir, claudeDir, codexDir}))
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	if files[0].Content != "wuu" || files[1].Content != "claude" || files[2].Content != "codex" {
		t.Errorf("unexpected order: %v", files)
	}
}

func TestDiscover_TildeExpansion(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "wuu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "wuu", "AGENTS.md"), []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := Discover("", home, testOpts([]string{"~/.config/wuu"}))
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Content != "user" {
		t.Errorf("got %q", files[0].Content)
	}
}

func TestDiscover_DefaultOptionsIncludesAllUserDirs(t *testing.T) {
	opts := DefaultOptions()
	if len(opts.UserDirs) != 3 {
		t.Errorf("expected 3 default user dirs, got %d: %v", len(opts.UserDirs), opts.UserDirs)
	}
	if len(opts.Filenames) != 3 {
		t.Errorf("expected 3 default filenames, got %d", len(opts.Filenames))
	}
	if opts.Filenames[0] != "AGENTS.md" {
		t.Errorf("expected AGENTS.md as highest-priority filename, got %q", opts.Filenames[0])
	}
}
