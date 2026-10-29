package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	kit.SetSessionDir(session)
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
