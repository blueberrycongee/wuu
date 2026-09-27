package toolctx

import (
	"context"
	"strings"
)

type stepIndexKey struct{}

type worktreeBindingKey struct{}

type worktreeBinding struct {
	root     string
	checkout string
}

type waitInterruptKey struct{}

type workspaceRevisionKey struct{}

type workspaceRevisionValue struct {
	Root     string
	Revision string
}

// WithWorkspaceRevision annotates tool execution context with the workspace
// revision the toolkit computed just before the tool ran. Read-only tools may
// reuse it instead of re-running git, but only when it was computed for the
// same root the tool would use — a bound worktree checkout has a different
// revision and must compute its own.
func WithWorkspaceRevision(ctx context.Context, root, revision string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, workspaceRevisionKey{}, workspaceRevisionValue{
		Root:     strings.TrimSpace(root),
		Revision: revision,
	})
}

// WorkspaceRevision returns the precomputed workspace revision stashed by the
// toolkit for this tool execution, if any.
func WorkspaceRevision(ctx context.Context) (root, revision string, ok bool) {
	if ctx == nil {
		return "", "", false
	}
	value, ok := ctx.Value(workspaceRevisionKey{}).(workspaceRevisionValue)
	if !ok || value.Root == "" {
		return "", "", false
	}
	return value.Root, value.Revision, true
}

// WithStepIndex annotates tool execution context with the model step that
// requested the tool. The value is telemetry-only; tools must not branch on it.
func WithStepIndex(ctx context.Context, stepIndex int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, stepIndexKey{}, stepIndex)
}

func StepIndex(ctx context.Context) (int, bool) {
	if ctx == nil {
		return 0, false
	}
	stepIndex, ok := ctx.Value(stepIndexKey{}).(int)
	return stepIndex, ok
}

// WithWorktreeBinding binds the isolated git-worktree checkout a
// worktree-bound thread must execute in (fork-to-worktree step 5). The turn
// entry injects the session's persisted worktree path here together with the
// root of the toolkit that runs the turn. File and shell tools switch their
// execution CWD to this checkout only AFTER their ordinary sandbox /
// whitelist checks pass, so the binding never widens what a tool may touch —
// it only relocates approved workspace paths into the isolated copy.
//
// The binding describes that one toolkit root. A toolkit with another root
// (a worker cloned for its own directory, or a root moved by an explicit
// workspace rebind) executes in its own root even though it inherits this
// context.
func WithWorktreeBinding(ctx context.Context, root, checkout string) context.Context {
	root = strings.TrimSpace(root)
	checkout = strings.TrimSpace(checkout)
	if root == "" || checkout == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, worktreeBindingKey{}, worktreeBinding{root: root, checkout: checkout})
}

// WorktreeBinding reports the toolkit root and worktree checkout bound to
// this tool execution, if any. Tools that do not touch the filesystem can
// ignore it.
func WorktreeBinding(ctx context.Context) (root, checkout string, ok bool) {
	if ctx == nil {
		return "", "", false
	}
	binding, ok := ctx.Value(worktreeBindingKey{}).(worktreeBinding)
	if !ok {
		return "", "", false
	}
	return binding.root, binding.checkout, true
}

// WithWaitInterrupt exposes a turn-scoped signal that wait-only tools may use
// to return control to the agent without canceling the work they started.
// Ordinary tools must ignore it and continue to their normal safe boundary.
func WithWaitInterrupt(ctx context.Context, interrupt <-chan struct{}) context.Context {
	if interrupt == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, waitInterruptKey{}, interrupt)
}

// WaitInterrupt returns the optional signal for safely detachable tool waits.
func WaitInterrupt(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	interrupt, _ := ctx.Value(waitInterruptKey{}).(<-chan struct{})
	return interrupt
}
