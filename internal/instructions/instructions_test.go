package instructions

import (
	"os"
	"path/filepath"
	"testing"
)

// testOpts returns Options that scan only the given dirs, avoiding host
// defaults that could leak into tests.
func testOpts(userDirs []string) Options {
	o := DefaultOptions()
	o.UserDirs = userDirs
	return o
}

func legacyTestOpts(userDirs []string) Options {
	o := testOpts(userDirs)
	o.IncludeLegacyInstructions = boolPtr(true)
	return o
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
	if len(files) != 1 {
		t.Fatalf("expected AGENTS.md to suppress same-dir CLAUDE.md, got %d files", len(files))
	}
	if files[0].Name != "AGENTS.md" {
		t.Errorf("unexpected user instruction file: %s", files[0].Name)
	}
	for _, f := range files {
		if f.Source != "user" {
			t.Errorf("expected source=user, got %q", f.Source)
		}
	}
}

func TestDiscover_ProjectHierarchyWithGitMarker(t *testing.T) {
	// Create a project root with shared instructions plus a legacy file below.
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

func TestDiscover_ProjectAgentsSuppressesSameDirLegacyInstruction(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		"AGENTS.md":          "agents",
		"AGENTS.override.md": "override",
		"CLAUDE.md":          "claude",
	} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files := Discover(root, "", testOpts(nil))
	if len(files) != 2 {
		t.Fatalf("expected AGENTS.md and AGENTS.override.md only, got %d: %+v", len(files), files)
	}
	if files[0].Content != "agents" || files[1].Content != "override" {
		t.Fatalf("unexpected instruction files: %+v", files)
	}
	for _, f := range files {
		if f.Name == "CLAUDE.md" {
			t.Fatalf("same-directory CLAUDE.md should not be loaded when AGENTS.md exists: %+v", files)
		}
	}
}

func TestDiscover_ProjectLegacyBeforeAgentsOverrideWithoutAgents(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		"CLAUDE.md":          "claude",
		"AGENTS.override.md": "override",
	} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files := Discover(root, "", testOpts(nil))
	if len(files) != 2 {
		t.Fatalf("expected CLAUDE.md and AGENTS.override.md, got %d: %+v", len(files), files)
	}
	if files[0].Name != "CLAUDE.md" || files[1].Name != "AGENTS.override.md" {
		t.Fatalf("override should be loaded after base project instructions, got %+v", files)
	}
}

func TestDiscover_CustomFilenamesLegacyNotSuppressedByUnwantedAgents(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		"AGENTS.md": "agents",
		"CLAUDE.md": "claude",
	} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opts := testOpts(nil)
	opts.Filenames = []string{"CLAUDE.md"}

	files := Discover(root, "", opts)
	if len(files) != 1 {
		t.Fatalf("expected only CLAUDE.md, got %d: %+v", len(files), files)
	}
	if files[0].Name != "CLAUDE.md" || files[0].Content != "claude" {
		t.Fatalf("custom Filenames should be respected, got %+v", files)
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

func TestDiscover_LegacyProjectMemoryLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude", "rules", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	filesToWrite := map[string]string{
		"CLAUDE.md":                     "project claude",
		".claude/CLAUDE.md":             "dot claude",
		".claude/rules/style.md":        "style rule",
		".claude/rules/nested/tests.md": "test rule",
		"CLAUDE.local.md":               "local private",
		".claude/rules/ignored.txt":     "ignored",
	}
	for rel, content := range filesToWrite {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files := Discover(root, "", legacyTestOpts(nil))
	byContent := map[string]File{}
	for _, f := range files {
		byContent[f.Content] = f
	}
	for _, want := range []string{"project claude", "dot claude", "style rule", "test rule", "local private"} {
		if _, ok := byContent[want]; !ok {
			t.Fatalf("missing %q in discovered files: %+v", want, files)
		}
	}
	if byContent["local private"].Source != "local" {
		t.Fatalf("CLAUDE.local.md source = %q, want local", byContent["local private"].Source)
	}
	if _, ok := byContent["ignored"]; ok {
		t.Fatalf("non-md rule should not be loaded: %+v", files)
	}
}

func TestDiscover_LegacyUserRules(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(claudeDir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "rules", "go.md"), []byte("user go rule"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := Discover("", home, legacyTestOpts([]string{"~/.claude"}))
	if len(files) != 1 {
		t.Fatalf("expected one user rule, got %d: %+v", len(files), files)
	}
	if files[0].Content != "user go rule" || files[0].Source != "user" {
		t.Fatalf("unexpected user rule file: %+v", files[0])
	}
}

func TestDiscover_LegacyAutoMemoryEntrypoint(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectKeyRoot := root
	if ev, err := filepath.EvalSymlinks(projectKeyRoot); err == nil {
		projectKeyRoot = ev
	}
	projectKey := claudeCodeSanitizePath(filepath.Clean(projectKeyRoot))
	memoryPath := filepath.Join(home, ".claude", "projects", projectKey, "memory", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(memoryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memoryPath, []byte("cc auto memory"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := Discover(root, home, legacyTestOpts(nil))
	if len(files) != 1 {
		t.Fatalf("expected one auto memory file, got %d: %+v", len(files), files)
	}
	if files[0].Content != "cc auto memory" || files[0].Source != "claude_auto" || files[0].Name != "MEMORY.md" {
		t.Fatalf("unexpected auto memory file: %+v", files[0])
	}
}

func TestDiscover_DefaultUserDirsScanUnifiedHome(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".wuu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".wuu", "AGENTS.md"), []byte("unified"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := Discover("", home, DefaultOptions())
	if len(files) != 1 {
		t.Fatalf("expected 1 file from unified home, got %d: %+v", len(files), files)
	}
	if files[0].Content != "unified" || files[0].Source != "user" {
		t.Fatalf("unexpected unified-home memory file: %+v", files[0])
	}
}

func TestDiscover_DefaultUserDirsStillReadLegacy(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "wuu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "wuu", "AGENTS.md"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := Discover("", home, DefaultOptions())
	if len(files) != 1 {
		t.Fatalf("expected 1 file from legacy dir, got %d: %+v", len(files), files)
	}
	if files[0].Content != "legacy" {
		t.Fatalf("unexpected legacy memory file: %+v", files[0])
	}
}

func TestDiscover_DefaultSkipsLegacyMemoryLayouts(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "CLAUDE.md"), []byte("dot claude"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "rules", "style.md"), []byte("style"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectKeyRoot := root
	if ev, err := filepath.EvalSymlinks(projectKeyRoot); err == nil {
		projectKeyRoot = ev
	}
	autoPath := filepath.Join(home, ".claude", "projects", claudeCodeSanitizePath(filepath.Clean(projectKeyRoot)), "memory", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(autoPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(autoPath, []byte("auto"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := Discover(root, home, testOpts(nil))
	if len(files) != 0 {
		t.Fatalf("default discovery should skip legacy layouts, got %+v", files)
	}
}
