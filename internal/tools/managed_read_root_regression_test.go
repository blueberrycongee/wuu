package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedReadRejectsReplacedSessionDirectory(t *testing.T) {
	workspace := t.TempDir()
	parent := t.TempDir()
	session := filepath.Join(parent, "session")
	if err := os.Mkdir(session, 0700); err != nil {
		t.Fatal(err)
	}
	kit, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := kit.SetSessionDir(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = kit.CloseSessionArtifacts() })
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(session, filepath.Join(parent, "old-session")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, session); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	args, _ := json.Marshal(map[string]any{"path": filepath.Join(session, "secret.txt")})
	if _, err := NewReadFileTool(kit.env).ExecuteResult(context.Background(), string(args)); err == nil {
		t.Fatal("read_file followed a replacement session directory outside its original root")
	}
}

func TestManagedReadKeepsSessionInternalSymlinks(t *testing.T) {
	session := t.TempDir()
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := kit.SetSessionDir(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = kit.CloseSessionArtifacts() })
	if err := os.WriteFile(filepath.Join(session, "target.txt"), []byte("expected content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(session, "link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	args, _ := json.Marshal(map[string]any{"path": filepath.Join(session, "link.txt")})
	result, err := NewReadFileTool(kit.env).Execute(context.Background(), string(args))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "expected content") {
		t.Fatalf("missing internal target: %s", result)
	}
}

func TestManagedOpenRejectsSymlinkEscape(t *testing.T) {
	session, outside := t.TempDir(), t.TempDir()
	root, err := newSessionArtifactRoot(session)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(session, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if f, err := root.openManaged("escape"); err == nil {
		f.Close()
		t.Fatal("opened symlink outside session root")
	}
}

func TestManagedReadUsesOpenedFileAfterReplacement(t *testing.T) {
	session, outside := t.TempDir(), t.TempDir()
	root, err := newSessionArtifactRoot(session)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	path := filepath.Join(session, "data.txt")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := root.openManaged("data.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Rename(path, filepath.Join(session, "moved.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	read, err := readFileLineRangeFromOpen(f, 1, 0, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if read.Text != "original" {
		t.Fatalf("read replaced path: %q", read.Text)
	}
}

func TestManagedActiveFileStatusUsesPinnedSession(t *testing.T) {
	parent, outside := t.TempDir(), t.TempDir()
	session := filepath.Join(parent, "session")
	if err := os.Mkdir(session, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(session, "file.txt")
	if err := os.WriteFile(path, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := kit.SetSessionDir(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = kit.CloseSessionArtifacts() })
	args, _ := json.Marshal(map[string]any{"path": path})
	if _, err := NewReadFileTool(kit.env).Execute(context.Background(), string(args)); err != nil {
		t.Fatal(err)
	}
	pinned := filepath.Join(parent, "pinned")
	if err := os.Rename(session, pinned); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pinned, "file.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	changedTime := info.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(pinned, "file.txt"), changedTime, changedTime); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "file.txt"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(outside, "file.txt"), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, session); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	block, ok := kit.ActiveFilesContextBlock()
	if !ok || !strings.Contains(block.Content, "possibly_stale") {
		t.Fatalf("expected stale pinned file: %q", block.Content)
	}
}
