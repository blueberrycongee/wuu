package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestSessionArtifactRootPinsDirectory(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "session")
	root, err := newSessionArtifactRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(path, filepath.Join(parent, "original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := root.writeArchive("abc.txt", []byte("contents")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, "tool-results", "abc.txt")); !os.IsNotExist(err) {
		t.Fatalf("wrote into replacement root: %v", err)
	}
	f, err := root.open("tool-results/abc.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b := make([]byte, 8)
	if _, err := f.Read(b); err != nil {
		t.Fatal(err)
	}
	if string(b) != "contents" {
		t.Fatalf("got %q", b)
	}
}

func TestSessionArtifactRootRejectsArchiveDirectorySymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session")
	root, err := newSessionArtifactRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Mkdir(filepath.Join(path, "other"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "other", "result.txt"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other", filepath.Join(path, "tool-results")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if f, err := root.open("tool-results/result.txt"); err == nil {
		f.Close()
		t.Fatal("read through archive directory symlink")
	}
	if err := root.writeArchive("another.txt", []byte("output")); err == nil {
		t.Fatal("wrote through archive directory symlink")
	}
}

func TestSharedSessionLeaseKeepsRecoveryAvailableAfterOwnerCloses(t *testing.T) {
	manager := NewSessionArtifactManager()
	dir := filepath.Join(t.TempDir(), "session")
	first, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	releaseFirst, err := first.BindSessionArtifacts(dir, manager)
	if err != nil {
		t.Fatal(err)
	}
	releaseSecond, err := second.BindSessionArtifacts(dir, manager)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecond()
	settled := first.FinalizeToolResult(providers.ToolCall{ID: "large", Name: "fixture"}, toolresult.FromText(strings.Repeat("long result ", 20000)))
	page := parseOut(t, settled.TextProjection())
	token := page["continuation"].(map[string]any)["next"].(map[string]any)["continuation"].(string)
	if err := releaseFirst(); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"continuation": token})
	if _, err := second.ExecuteResult(context.Background(), providers.ToolCall{Name: "recover_tool_result", Arguments: string(args)}); err != nil {
		t.Fatalf("remaining lease could not recover result: %v", err)
	}
}

func TestSessionArtifactLeasesConcurrentRelease(t *testing.T) {
	manager := NewSessionArtifactManager()
	path := filepath.Join(t.TempDir(), "session")
	keeper, err := manager.acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := manager.acquire(path)
			if err != nil {
				t.Error(err)
				return
			}
			if lease.root != keeper.root {
				t.Error("different root")
			}
			if err := lease.Close(); err != nil {
				t.Error(err)
			}
			if err := lease.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := keeper.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionArtifactLeaseAcquisitionReusesPinnedRootAfterReplacement(t *testing.T) {
	manager := NewSessionArtifactManager()
	parent := t.TempDir()
	path := filepath.Join(parent, "session")
	first, err := manager.acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := os.Rename(path, filepath.Join(parent, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	second, err := manager.acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.root != first.root {
		t.Fatal("rebound an active session to a replacement directory")
	}
}

func TestSessionArtifactLeasesKeepRootUntilLastRelease(t *testing.T) {
	manager := NewSessionArtifactManager()
	path := filepath.Join(t.TempDir(), "session")
	first, err := manager.acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.root != second.root {
		t.Fatal("session leases did not share root")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.root.writeArchive("one.txt", []byte("valid")); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.root.writeArchive("two.txt", []byte("invalid")); err == nil {
		t.Fatal("root remained open after last release")
	}
}
