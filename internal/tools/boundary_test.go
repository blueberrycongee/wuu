package tools

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type testGuard struct {
	err error
}

func (g testGuard) Check(ToolInfo, providers.ToolCall) error {
	return g.err
}

func TestReadOnlyBoundaryRejectsMutationAndAllowsRead(t *testing.T) {
	b := ReadOnlyBoundary()
	err := b.Check(ToolInfo{Name: "write_file", Kind: ToolKindFile}, providers.ToolCall{Name: "write_file"})
	if err == nil || !strings.Contains(err.Error(), "error_kind=boundary_denied") {
		t.Fatalf("read-only boundary should deny file mutation, got %v", err)
	}

	err = b.Check(ToolInfo{Name: "read_file", Kind: ToolKindFile, ReadOnly: true}, providers.ToolCall{Name: "read_file"})
	if err != nil {
		t.Fatalf("read-only boundary should allow reads: %v", err)
	}
}

func TestBoundaryGuardsAreHardDenyOnly(t *testing.T) {
	want := errors.New("denied")
	b := WorkspaceBoundary{Enforce: true, AllowMutations: true, Guards: []Guard{nil, testGuard{err: want}}}
	err := b.Check(ToolInfo{Name: "bash", Kind: ToolKindShell}, providers.ToolCall{Name: "bash"})
	if !errors.Is(err, want) {
		t.Fatalf("boundary should return guard denial, got %v", err)
	}
}

func TestToolkitPermissionRequestHookRunsOnceBeforeBoundary(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetBoundary(ReadOnlyBoundary())
	calls := 0
	kit.SetPermissionRequestHook(func(_ context.Context, active *Toolkit, info ToolInfo, call providers.ToolCall) error {
		calls++
		if active != kit {
			t.Fatal("permission hook received the wrong toolkit")
		}
		if info.Name != "write_file" || call.Name != "write_file" {
			t.Fatalf("unexpected permission payload: info=%+v call=%+v", info, call)
		}
		return nil
	})
	err = kit.checkPermission(context.Background(), ToolInfo{Name: "write_file", Kind: ToolKindFile}, providers.ToolCall{Name: "write_file"})
	if err == nil {
		t.Fatal("read-only boundary should still deny the request")
	}
	if calls != 1 {
		t.Fatalf("permission hook calls = %d, want 1", calls)
	}
}

func TestSetBoundaryControlsPathConfinement(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetFileScopeRoots([]string{root})

	kit.SetBoundary(StandardBoundary())
	if _, err := kit.env.ResolvePath(outside); err == nil || !strings.Contains(err.Error(), "outside the allowed file scope") {
		t.Fatalf("standard boundary should reject outside path, got %v", err)
	}

	kit.SetBoundary(UnconfinedBoundary())
	resolved, err := kit.env.ResolvePath(outside)
	if err != nil {
		t.Fatalf("unconfined boundary should allow outside path: %v", err)
	}
	if resolved != outside {
		t.Fatalf("resolved path = %q, want %q", resolved, outside)
	}
}
