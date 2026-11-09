package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestToolkit_EditFileMatchesCurrentContentAtExecution(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	path := filepath.Join(root, "a.txt")
	mustWriteFile(t, path, "alpha\nbeta\ngamma\n")

	if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: "read_file", Arguments: `{"path":"a.txt"}`}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	mustWriteFile(t, path, "ALPHA\nbeta\ngamma\n")
	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "edit_file",
		Arguments: `{"path":"a.txt","old_text":"beta","new_text":"BETA"}`,
	}); err != nil {
		t.Fatalf("edit current anchor after unrelated external change: %v", err)
	}
	if got := mustReadFile(t, path); got != "ALPHA\nBETA\ngamma\n" {
		t.Fatalf("edit should preserve unrelated current content: %q", got)
	}
}

func TestToolkit_EditFileRejectsMissingCurrentAnchor(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	path := filepath.Join(root, "a.txt")
	mustWriteFile(t, path, "alpha\nchanged\n")

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "edit_file",
		Arguments: `{"path":"a.txt","old_text":"beta","new_text":"BETA"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "old_text_not_found") {
		t.Fatalf("expected missing current anchor rejection, got: %v", err)
	}
	if got := mustReadFile(t, path); got != "alpha\nchanged\n" {
		t.Fatalf("failed edit must not mutate current content: %q", got)
	}
}

func TestToolkit_WriteFileUsesIntentionalCurrentOverwrite(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	path := filepath.Join(root, "a.txt")
	mustWriteFile(t, path, "alpha\n")
	if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: "read_file", Arguments: `{"path":"a.txt"}`}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	mustWriteFile(t, path, "external\n")

	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"a.txt","content":"replacement\n"}`,
	}); err != nil {
		t.Fatalf("intentional full overwrite: %v", err)
	}
	if got := mustReadFile(t, path); got != "replacement\n" {
		t.Fatalf("unexpected overwrite content: %q", got)
	}
}
