package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const fileScopeGuidance = "该路径不在工作区内"

// newFileScopeKit builds a toolkit whose whitelist covers home and one
// workspace root only. The "outside" dir sits under the real os.TempDir(),
// so tests that must observe rejections deliberately leave the temp root
// out of the whitelist (in production it is included; see
// TestFileScopeAllowsHomeWorkspaceAndTemp for the temp-allowed case).
func newFileScopeKit(t *testing.T) (kit *Toolkit, home, workspace, outside string) {
	t.Helper()
	home = t.TempDir()
	workspace = t.TempDir()
	outside = t.TempDir()
	kit, err := New(home)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	kit.SetFileScopeRoots([]string{home, workspace})
	return kit, home, workspace, outside
}

func executeFileTool(t *testing.T, kit *Toolkit, name, args string) (string, error) {
	t.Helper()
	return kit.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: args})
}

func TestFileScopeAllowsHomeWorkspaceAndTemp(t *testing.T) {
	kit, home, workspace, _ := newFileScopeKit(t)
	kit.SetFileScopeRoots([]string{home, workspace, os.TempDir()})

	homeFile := filepath.Join(home, "notes.txt")
	if _, err := executeFileTool(t, kit, "write_file", fmt.Sprintf(`{"path":%q,"content":"home"}`, homeFile)); err != nil {
		t.Fatalf("write inside home: %v", err)
	}

	// Workspace roots are outside the toolkit RootDir: without the scope
	// whitelist this write would be rejected as escaping the workspace.
	workspaceFile := filepath.Join(workspace, "plan.md")
	if _, err := executeFileTool(t, kit, "write_file", fmt.Sprintf(`{"path":%q,"content":"ws"}`, workspaceFile)); err != nil {
		t.Fatalf("write inside registered workspace: %v", err)
	}
	if _, err := executeFileTool(t, kit, "read_file", fmt.Sprintf(`{"path":%q}`, workspaceFile)); err != nil {
		t.Fatalf("read inside registered workspace: %v", err)
	}

	tempFile := filepath.Join(os.TempDir(), fmt.Sprintf("wuu-scope-%d.txt", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(tempFile) })
	if _, err := executeFileTool(t, kit, "write_file", fmt.Sprintf(`{"path":%q,"content":"tmp"}`, tempFile)); err != nil {
		t.Fatalf("write inside system temp: %v", err)
	}
}

func TestFileScopeRejectsOutsidePathsForReadAndWrite(t *testing.T) {
	kit, _, _, outside := newFileScopeKit(t)

	outsideFile := filepath.Join(outside, "private.txt")
	if err := os.WriteFile(outsideFile, []byte("private"), 0o644); err != nil {
		t.Fatalf("prepare outside file: %v", err)
	}

	if _, err := executeFileTool(t, kit, "write_file", fmt.Sprintf(`{"path":%q,"content":"x"}`, outsideFile)); err == nil || !strings.Contains(err.Error(), fileScopeGuidance) {
		t.Fatalf("write outside scope should fail with guidance, got err=%v", err)
	}
	// Reads are rejected the same as writes (design doc §5.2).
	if _, err := executeFileTool(t, kit, "read_file", fmt.Sprintf(`{"path":%q}`, outsideFile)); err == nil || !strings.Contains(err.Error(), fileScopeGuidance) {
		t.Fatalf("read outside scope should fail with guidance, got err=%v", err)
	}
	if _, err := executeFileTool(t, kit, "glob", fmt.Sprintf(`{"path":%q,"pattern":"*.txt"}`, outside)); err == nil || !strings.Contains(err.Error(), fileScopeGuidance) {
		t.Fatalf("glob outside scope should fail with guidance, got err=%v", err)
	}
	if _, err := executeFileTool(t, kit, "grep", fmt.Sprintf(`{"path":%q,"pattern":"secret"}`, outside)); err == nil || !strings.Contains(err.Error(), fileScopeGuidance) {
		t.Fatalf("grep outside scope should fail with guidance, got err=%v", err)
	}
	if _, err := executeFileTool(t, kit, "edit_file", fmt.Sprintf(`{"path":%q,"old_text":"secret","new_text":"x"}`, outsideFile)); err == nil || !strings.Contains(err.Error(), fileScopeGuidance) {
		t.Fatalf("edit outside scope should fail with guidance, got err=%v", err)
	}
}

func TestFileScopeRejectsSymlinkEscape(t *testing.T) {
	kit, home, _, outside := newFileScopeKit(t)

	outsideFile := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(outsideFile, []byte("out"), 0o644); err != nil {
		t.Fatalf("prepare outside file: %v", err)
	}
	link := filepath.Join(home, "escape-link")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := executeFileTool(t, kit, "read_file", fmt.Sprintf(`{"path":%q}`, link)); err == nil || !strings.Contains(err.Error(), fileScopeGuidance) {
		t.Fatalf("read through escaping symlink should fail, got err=%v", err)
	}
	if _, err := executeFileTool(t, kit, "write_file", fmt.Sprintf(`{"path":%q,"content":"x"}`, link)); err == nil || !strings.Contains(err.Error(), fileScopeGuidance) {
		t.Fatalf("write through escaping symlink should fail, got err=%v", err)
	}
}

func TestFileScopeUnsetKeepsLegacyWorkspaceConfinement(t *testing.T) {
	home := t.TempDir()
	elsewhere := t.TempDir()
	kit, err := New(home)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	// No SetFileScopeRoots: work sessions keep the single-RootDir boundary
	// and the legacy error message.
	target := filepath.Join(elsewhere, "file.txt")
	_, err = executeFileTool(t, kit, "write_file", fmt.Sprintf(`{"path":%q,"content":"x"}`, target))
	if err == nil || !strings.Contains(err.Error(), "escapes workspace") || strings.Contains(err.Error(), fileScopeGuidance) {
		t.Fatalf("legacy confinement should be unchanged, got err=%v", err)
	}
}
