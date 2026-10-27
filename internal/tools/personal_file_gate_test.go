package tools

import (
	"context"
	"errors"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"os"
	"path/filepath"
	"testing"
)

func TestFileActionGateInheritedWithoutCodingReviewSetting(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	kit.SetFileActionGate(func(context.Context, *Toolkit, providers.ToolCall) (toolresult.Result, error) {
		calls++
		return toolresult.Result{}, errors.New("pending")
	})
	clone, err := kit.CloneForRoot(kit.RootDir())
	if err != nil {
		t.Fatal(err)
	}
	clone.SetApproveForMe(false)
	_, err = clone.Execute(context.Background(), providers.ToolCall{Name: "write_file", Arguments: `{"path":"note.txt","content":"private"}`})
	if err == nil || calls != 1 {
		t.Fatalf("gate bypass: %v %d", err, calls)
	}
}

// A lexical in-root alias must not mutate a different product's file inode.
func TestPersonalFileActionRejectsHardlinkAlias(t *testing.T) {
	root, outside := t.TempDir(), filepath.Join(t.TempDir(), "coding-config")
	if err := os.WriteFile(outside, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "note.txt")
	if err := os.Link(outside, target); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	call := providers.ToolCall{ID: "alias", Name: "write_file", Arguments: `{"path":"note.txt","content":"changed"}`}
	if _, err := kit.PrepareFileAction(context.Background(), call); err == nil {
		t.Fatal("hardlink approved")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	action, err := kit.PrepareFileAction(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, target); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.ExecuteFileAction(context.Background(), action); err == nil {
		t.Fatal("post-review alias executed")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "original" {
		t.Fatalf("outside file changed: %s %v", data, err)
	}
}
