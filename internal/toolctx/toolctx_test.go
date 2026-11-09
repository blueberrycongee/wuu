package toolctx

import (
	"context"
	"testing"
)

func TestWorktreeBindingRoundTrip(t *testing.T) {
	ctx := WithWorktreeBinding(context.Background(), "  /tmp/parent  ", "  /tmp/worktrees/session/fork  ")
	root, checkout, ok := WorktreeBinding(ctx)
	if !ok || root != "/tmp/parent" || checkout != "/tmp/worktrees/session/fork" {
		t.Fatalf("WorktreeBinding = %q, %q, %v; want trimmed root and checkout", root, checkout, ok)
	}
}

func TestWorktreeBindingIncompleteBindingIsNoop(t *testing.T) {
	base := context.Background()
	for _, ctx := range []context.Context{
		WithWorktreeBinding(base, "/tmp/parent", "   "),
		WithWorktreeBinding(base, "   ", "/tmp/wt"),
	} {
		if ctx != base {
			t.Fatalf("incomplete worktree binding should return the original context")
		}
	}
	if root, checkout, ok := WorktreeBinding(base); ok || root != "" || checkout != "" {
		t.Fatalf("WorktreeBinding on unbound ctx = %q, %q, %v; want none", root, checkout, ok)
	}
}

func TestWorktreeBindingNilContext(t *testing.T) {
	if _, _, ok := WorktreeBinding(nil); ok {
		t.Fatal("WorktreeBinding(nil) should report no binding")
	}
	ctx := WithWorktreeBinding(nil, "/tmp/parent", "/tmp/wt")
	if _, checkout, ok := WorktreeBinding(ctx); !ok || checkout != "/tmp/wt" {
		t.Fatalf("WithWorktreeBinding(nil, ...) should still bind, got %q, %v", checkout, ok)
	}
}

func TestWaitInterruptRoundTrip(t *testing.T) {
	interrupt := make(chan struct{})
	ctx := WithWaitInterrupt(context.Background(), interrupt)
	if got := WaitInterrupt(ctx); got != interrupt {
		t.Fatal("WaitInterrupt did not return the bound signal")
	}
	close(interrupt)
	select {
	case <-WaitInterrupt(ctx):
	default:
		t.Fatal("bound wait interrupt did not preserve closure")
	}
}

func TestWaitInterruptNilBindingIsNoop(t *testing.T) {
	base := context.Background()
	if ctx := WithWaitInterrupt(base, nil); ctx != base {
		t.Fatal("nil wait interrupt should return the original context")
	}
	if got := WaitInterrupt(nil); got != nil {
		t.Fatal("WaitInterrupt(nil) should return nil")
	}
}
