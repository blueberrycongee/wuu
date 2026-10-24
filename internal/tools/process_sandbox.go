package tools

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/processsandbox"
)

// processSandboxPolicy translates the existing workspace boundary into an OS
// filesystem policy. It never asks for per-call approval: policy changes remain
// a user-owned session/workspace setting. Platforms without Wuu's built-in
// backend may still confine processes through a configured provider.
func (e *Env) processSandboxPolicy(ctx context.Context) (*processsandbox.Policy, string, error) {
	return e.processSandboxPolicyWithBuiltIn(ctx, processsandbox.Supported())
}

type processSandboxScope struct {
	root, stateDir, sessionDir, sessionID, execRoot string
	roots                                           []string
	unconfined, allowMutations, boundaryConfigured  bool
	provider                                        processsandbox.Provider
}

func (e *Env) processSandboxPolicyWithBuiltIn(ctx context.Context, builtInAvailable bool) (*processsandbox.Policy, string, error) {
	if e == nil || e.Unconfined {
		return nil, "", nil
	}
	execRoot, _ := e.ExecRootDir(ctx)
	return (processSandboxScope{root: e.RootDir, stateDir: e.StateDir, sessionDir: e.SessionDir, sessionID: e.SessionID, execRoot: execRoot, roots: e.FileScopeRoots, unconfined: e.Unconfined, allowMutations: e.AllowMutations, boundaryConfigured: e.boundaryConfigured, provider: e.ProcessSandboxProvider}).policy(builtInAvailable)
}
func (p processSandboxScope) policy(builtInAvailable bool) (*processsandbox.Policy, string, error) {
	if p.unconfined {
		return nil, "", nil
	}
	if p.provider == nil && !builtInAvailable {
		return nil, "", fmt.Errorf("%w: no built-in backend is available; configure sandbox.process@1 or explicitly use unconfined mode", processsandbox.ErrUnavailable)
	}
	if p.boundaryConfigured && !p.allowMutations {
		return &processsandbox.Policy{Mode: processsandbox.ModeReadOnly}, "", nil
	}

	roots := make([]string, 0, len(p.roots)+2)
	for _, root := range p.roots {
		// File tools may use the system temp directory as a convenience scope,
		// but granting it to a process would let every command write anywhere
		// under the shared host temp tree. Commands get a private temp below.
		if sameRuntimeFileScopePath(root, os.TempDir()) {
			continue
		}
		roots = append(roots, root)
	}
	if len(roots) == 0 {
		roots = []string{p.root}
	}
	if execRoot := p.execRoot; strings.TrimSpace(execRoot) != "" {
		replaced := false
		for i, root := range roots {
			if sameRuntimeFileScopePath(root, p.root) {
				roots[i] = execRoot
				replaced = true
				break
			}
		}
		if !replaced {
			roots = append(roots, execRoot)
		}
	}
	tempDir, err := p.tempDir()
	if err != nil {
		return nil, "", err
	}
	roots = append(roots, tempDir)
	return &processsandbox.Policy{
		Mode:          processsandbox.ModeWorkspaceWrite,
		WritableRoots: roots,
	}, tempDir, nil
}

func (e *Env) ensureProcessSandboxTempDir() (string, error) {
	return (processSandboxScope{root: e.RootDir, stateDir: e.StateDir, sessionDir: e.SessionDir, sessionID: e.SessionID}).tempDir()
}
func (p processSandboxScope) tempDir() (string, error) {
	base := strings.TrimSpace(p.sessionDir)
	if base == "" {
		base = strings.TrimSpace(p.stateDir)
	}
	var tempDir string
	if base != "" {
		tempDir = filepath.Join(base, "process-tmp")
	} else {
		identity := strings.TrimSpace(p.root) + "\x00" + strings.TrimSpace(p.sessionID)
		sum := sha256.Sum256([]byte(identity))
		tempDir = filepath.Join(os.TempDir(), "wuu-process-sandbox", fmt.Sprintf("%x", sum[:8]))
	}
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return "", fmt.Errorf("create private process temp directory: %w", err)
	}
	info, err := os.Lstat(tempDir)
	if err != nil {
		return "", fmt.Errorf("inspect private process temp directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("private process temp path is not a real directory: %s", tempDir)
	}
	if err := os.Chmod(tempDir, 0o700); err != nil {
		return "", fmt.Errorf("secure private process temp directory: %w", err)
	}
	return tempDir, nil
}
