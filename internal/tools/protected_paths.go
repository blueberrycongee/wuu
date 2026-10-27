package tools

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/processsandbox"
)

// SetProtectedPaths adds immutable read/write exclusions. They are inherited by
// clones and cannot be removed by widening a workspace or selecting unconfined
// mode. Replacing a domain requires constructing a fresh toolkit.
func (t *Toolkit) SetProtectedPaths(roots []string) {
	if t == nil || t.env == nil {
		return
	}
	seen := make(map[string]bool, len(t.env.ProtectedPaths))
	for _, root := range t.env.ProtectedPaths {
		seen[root] = true
	}
	add := func(root string) {
		if !seen[root] {
			t.env.ProtectedPaths = append(t.env.ProtectedPaths, root)
			seen[root] = true
		}
	}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		add(root)
		if resolved, err := processsandbox.CanonicalPath(root); err == nil {
			add(resolved)
		}
	}
}

func (e *Env) rejectProtectedPath(path string, recursive bool) error {
	if e == nil || len(e.ProtectedPaths) == 0 {
		return nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve protected path boundary: %w", err)
	}
	resolved, err := processsandbox.CanonicalPath(absolute)
	if err != nil {
		return fmt.Errorf("resolve protected path boundary: %w", err)
	}
	for _, root := range e.ProtectedPaths {
		if strings.TrimSpace(root) == "" {
			return fmt.Errorf("invalid protected path boundary")
		}
		lexical, err := filepath.Abs(root)
		if err != nil {
			return fmt.Errorf("resolve protected path boundary: %w", err)
		}
		canonical, err := processsandbox.CanonicalPath(lexical)
		if err != nil {
			return fmt.Errorf("resolve protected path boundary: %w", err)
		}
		for _, candidate := range []string{absolute, resolved} {
			for _, boundary := range []string{lexical, canonical} {
				_, inside := relInsideRoot(boundary, candidate)
				_, ancestor := relInsideRoot(candidate, boundary)
				if inside || recursive && ancestor {
					return fmt.Errorf("path %q is denied by the protected read/write boundary in every permission mode", path)
				}
			}
		}
	}
	return nil
}

type protectedPathContextKey struct{}

func (e *Env) withProtectedPaths(ctx context.Context) context.Context {
	if e == nil || len(e.ProtectedPaths) == 0 {
		return ctx
	}
	return context.WithValue(ctx, protectedPathContextKey{}, e)
}

func protectedPathEnv(ctx context.Context) *Env {
	env, _ := ctx.Value(protectedPathContextKey{}).(*Env)
	return env
}

// Auxiliary commands (Git and similar helpers) need the same domain boundary
// as interactive shells; without protected paths their historical behavior is
// unchanged.
func (e *Env) confineProtectedCommand(ctx context.Context, cmd *exec.Cmd) error {
	if e == nil || len(e.ProtectedPaths) == 0 {
		return nil
	}
	policy, _, err := e.processSandboxPolicy(ctx)
	if err != nil {
		return err
	}
	if policy == nil {
		return fmt.Errorf("protected process boundary is unavailable")
	}
	_, err = processsandbox.ApplyWithProvider(ctx, cmd, *policy, e.ProcessSandboxProvider)
	return err
}

func (e *Env) workspaceRevision(ctx context.Context, root string) string {
	if e == nil {
		return workspaceRevision(ctx, root, "")
	}
	if len(e.ProtectedPaths) == 0 {
		return workspaceRevision(ctx, root, e.RuntimeHome)
	}
	if e.rejectProtectedPath(root, false) != nil {
		return ""
	}
	// Git can invoke external helpers and read global configuration. A guarded
	// in-process digest avoids both subprocess escape and cross-domain telemetry.
	return filesystemRevisionFallback(e.withProtectedPaths(ctx), root, e.RuntimeHome)
}
