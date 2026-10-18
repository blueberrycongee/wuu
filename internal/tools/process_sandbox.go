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

func (e *Env) processSandboxPolicyWithBuiltIn(ctx context.Context, builtInAvailable bool) (*processsandbox.Policy, string, error) {
	if e == nil || e.Unconfined && len(e.ProtectedPaths) == 0 {
		return nil, "", nil
	}
	if e.ProcessSandboxProvider == nil && !builtInAvailable {
		if len(e.ProtectedPaths) > 0 {
			return nil, "", fmt.Errorf("%w: protected read/write paths require a supporting sandbox backend", processsandbox.ErrUnavailable)
		}
		return nil, "", fmt.Errorf("%w: no built-in backend is available; configure sandbox.process@1 or explicitly use unconfined mode", processsandbox.ErrUnavailable)
	}
	if e.boundaryConfigured && !e.AllowMutations {
		return &processsandbox.Policy{Mode: processsandbox.ModeReadOnly, ProtectedPaths: append([]string(nil), e.ProtectedPaths...)}, "", nil
	}

	roots := make([]string, 0, len(e.FileScopeRoots)+2)
	for _, root := range e.FileScopeRoots {
		// File tools may use the system temp directory as a convenience scope,
		// but granting it to a process would let every command write anywhere
		// under the shared host temp tree. Commands get a private temp below.
		if sameRuntimeFileScopePath(root, os.TempDir()) {
			continue
		}
		roots = append(roots, root)
	}
	if len(roots) == 0 {
		roots = []string{e.RootDir}
	}
	if execRoot, err := e.ExecRootDir(ctx); err == nil && strings.TrimSpace(execRoot) != "" {
		replaced := false
		for i, root := range roots {
			if sameRuntimeFileScopePath(root, e.RootDir) {
				roots[i] = execRoot
				replaced = true
				break
			}
		}
		if !replaced {
			roots = append(roots, execRoot)
		}
	}
	tempDir, err := e.ensureProcessSandboxTempDir()
	if err != nil {
		return nil, "", err
	}
	roots = append(roots, tempDir)
	if e.Unconfined {
		roots = []string{string(filepath.Separator)}
	}
	return &processsandbox.Policy{
		Mode:           processsandbox.ModeWorkspaceWrite,
		WritableRoots:  roots,
		ProtectedPaths: append([]string(nil), e.ProtectedPaths...),
	}, tempDir, nil
}

func (e *Env) ensureProcessSandboxTempDir() (string, error) {
	base := strings.TrimSpace(e.SessionDir)
	if base == "" {
		base = strings.TrimSpace(e.StateDir)
	}
	// Session authority storage may be protected from the subprocess itself.
	// Give that subprocess separate private scratch instead of a write exception.
	if base != "" && e.rejectProtectedPath(filepath.Join(base, "process-tmp"), false) != nil {
		base = ""
	}
	var tempDir string
	if base != "" {
		tempDir = filepath.Join(base, "process-tmp")
	} else {
		identity := strings.TrimSpace(e.RootDir) + "\x00" + strings.TrimSpace(e.SessionID)
		sum := sha256.Sum256([]byte(identity))
		tempDir = filepath.Join(os.TempDir(), "wuu-process-sandbox", fmt.Sprintf("%x", sum[:8]))
	}
	if err := e.rejectProtectedPath(tempDir, false); err != nil {
		return "", err
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
