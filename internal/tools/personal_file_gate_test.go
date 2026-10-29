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

type fencedFileAuthorizer struct {
	mode  string
	calls int
}

func (a *fencedFileAuthorizer) SupportsAtomicAuthorization() bool { return a.mode != "unsupported" }
func (a *fencedFileAuthorizer) Authorize(context.Context, AuthorizationRequest) (AuthorizationDecision, error) {
	return AuthorizationDecision{}, errors.New("snapshot policy must not replace effect fence")
}
func (a *fencedFileAuthorizer) WithAuthorization(ctx context.Context, r AuthorizationRequest, use func(context.Context) error) error {
	a.calls++
	if a.mode == "deny" {
		return errors.New("selected policy denied")
	}
	if a.mode == "noop" {
		return nil
	}
	if a.mode == "cancel" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		cancel()
	}
	return use(ctx)
}
func TestFileActionRequiresSelectedEffectFenceAndPreservesLocalBoundary(t *testing.T) {
	for _, mode := range []string{"allow", "deny", "noop", "cancel", "read-only", "snapshot", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			kit, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			a := &fencedFileAuthorizer{mode: mode}
			kit.SetAuthorizer(a)
			if mode == "read-only" {
				kit.SetBoundary(ReadOnlyBoundary())
			}
			if mode == "snapshot" {
				kit.SetAuthorizer(&recordingAuthorizer{decision: AuthorizationDecision{Outcome: "allow"}})
			}
			frozen, err := kit.PrepareFileAction(context.Background(), providers.ToolCall{Name: "write_file", Arguments: `{"path":"file.txt","content":"exact"}`})
			if mode == "snapshot" || mode == "unsupported" {
				if err == nil {
					t.Fatal("snapshot-only provider admitted reviewed effect")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = kit.ExecuteFileAction(context.Background(), frozen)
			data, readErr := os.ReadFile(filepath.Join(root, "file.txt"))
			if mode == "allow" {
				if err != nil || readErr != nil || string(data) != "exact" || a.calls != 1 {
					t.Fatalf("fenced write: %s %v %v calls=%d", data, err, readErr, a.calls)
				}
			} else if err == nil || !os.IsNotExist(readErr) {
				t.Fatalf("unfenced write: %s %v %v", data, err, readErr)
			}
		})
	}
}
