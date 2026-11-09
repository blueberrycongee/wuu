package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/blueberrycongee/wuu/internal/processsandbox"
)

type policyTestSandboxProvider struct{}

func (policyTestSandboxProvider) Confine(context.Context, []string, processsandbox.Policy) (processsandbox.ConfinedCommand, error) {
	return processsandbox.ConfinedCommand{}, nil
}

func TestProcessSandboxPolicyMapsWorkspaceBoundary(t *testing.T) {
	root := t.TempDir()
	env := &Env{RootDir: root, FileScopeRoots: []string{root, os.TempDir()}}
	policy, tempDir, err := env.processSandboxPolicyWithBuiltIn(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if policy == nil || policy.Mode != processsandbox.ModeWorkspaceWrite {
		t.Fatalf("default policy = %#v, want workspace-write", policy)
	}
	wantRoot, _ := filepath.EvalSymlinks(root)
	wantTemp, _ := filepath.EvalSymlinks(tempDir)
	normalized := make(map[string]bool)
	for _, candidate := range policy.WritableRoots {
		if sameRuntimeFileScopePath(candidate, os.TempDir()) {
			t.Fatalf("writable roots must not include shared system temp %q: %#v", os.TempDir(), policy.WritableRoots)
		}
		if evaluated, err := filepath.EvalSymlinks(candidate); err == nil {
			candidate = evaluated
		}
		normalized[candidate] = true
	}
	if len(policy.WritableRoots) != 2 || len(normalized) != 2 || !normalized[wantRoot] || !normalized[wantTemp] {
		t.Fatalf("writable roots = %#v, want workspace %q and temp %q", policy.WritableRoots, wantRoot, wantTemp)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(tempDir)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("private temp mode = %v, want 700", got)
		}
	}

	env.boundaryConfigured = true
	env.AllowMutations = false
	policy, _, err = env.processSandboxPolicyWithBuiltIn(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if policy == nil || policy.Mode != processsandbox.ModeReadOnly || len(policy.WritableRoots) != 0 {
		t.Fatalf("read-only policy = %#v", policy)
	}

	env.Unconfined = true
	if policy, _, err := env.processSandboxPolicyWithBuiltIn(context.Background(), true); err != nil || policy != nil {
		t.Fatalf("unconfined policy = %#v, want nil", policy)
	}
}

func TestProcessSandboxTempRejectsSymlink(t *testing.T) {
	base := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(base, "process-tmp")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	env := &Env{RootDir: t.TempDir(), SessionDir: base}
	if _, err := env.ensureProcessSandboxTempDir(); err == nil {
		t.Fatal("symlinked process temp must be rejected")
	}
}

func TestProcessSandboxPolicyFailsClosedWithoutBackend(t *testing.T) {
	env := &Env{RootDir: t.TempDir()}
	policy, tempDir, err := env.processSandboxPolicyWithBuiltIn(context.Background(), false)
	if !errors.Is(err, processsandbox.ErrUnavailable) || policy != nil || tempDir != "" {
		t.Fatalf("missing backend = policy %#v temp %q error %v", policy, tempDir, err)
	}
	if !processsandbox.Supported() {
		policy, tempDir, err = env.processSandboxPolicy(context.Background())
		if !errors.Is(err, processsandbox.ErrUnavailable) || policy != nil || tempDir != "" {
			t.Fatalf("platform probe = policy %#v temp %q error %v, want unavailable", policy, tempDir, err)
		}
	}

	env.Unconfined = true
	if policy, _, err := env.processSandboxPolicyWithBuiltIn(context.Background(), false); err != nil || policy != nil {
		t.Fatalf("explicit unconfined mode must bypass confinement: policy %#v error %v", policy, err)
	}

	env.Unconfined = false
	env.ProcessSandboxProvider = policyTestSandboxProvider{}
	policy, _, err = env.processSandboxPolicyWithBuiltIn(context.Background(), false)
	if err != nil || policy == nil || policy.Mode != processsandbox.ModeWorkspaceWrite {
		t.Fatalf("custom provider policy = %#v error %v", policy, err)
	}
}
