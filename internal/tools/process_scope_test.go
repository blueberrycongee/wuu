package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/approvefor"
	proc "github.com/blueberrycongee/wuu/internal/process"
)

type processScopeAuthorizer struct {
	deny     bool
	requests []AuthorizationRequest
}

func (a *processScopeAuthorizer) Authorize(_ context.Context, r AuthorizationRequest) (AuthorizationDecision, error) {
	a.requests = append(a.requests, r)
	if a.deny {
		return AuthorizationDecision{Outcome: "deny", Reason: "test denial"}, nil
	}
	return AuthorizationDecision{Outcome: "allow"}, nil
}

func TestHostProcessScopeRunsWithoutToolkit(t *testing.T) {
	root := t.TempDir()
	manager, err := proc.NewManager(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	auth := &processScopeAuthorizer{}
	scope := NewProcessScope(ProcessScopeConfig{Manager: manager, RootDir: root, ThreadID: "thread", ActorID: "actor", PermissionMode: "unconfined", Boundary: UnconfinedBoundary(), Authorizer: auth})
	process, gotManager, err := scope.Start(context.Background(), "printf 'host-owned-output'", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = manager.Stop(process.ID) })
	if gotManager != manager || process.RootThreadID != "thread" || process.OwnerID != "actor" {
		t.Fatalf("wrong ownership: %+v", process)
	}
	offset := int64(0)
	out, err := manager.ReadOutputSnapshot(context.Background(), process.ID, proc.OutputReadOptions{MaxBytes: 1024, OffsetBytes: &offset, Wait: time.Second})
	if err != nil || !strings.Contains(out.Output, "host-owned-output") {
		t.Fatalf("output: %+v %v", out, err)
	}
	if len(auth.requests) != 1 || auth.requests[0].SessionID != "thread" || auth.requests[0].ActorID != "actor" || auth.requests[0].Tool.Name != "bash" {
		t.Fatalf("authorization: %+v", auth.requests)
	}
}

func TestHostProcessScopeDeniesBeforeSpawn(t *testing.T) {
	for _, mode := range []string{"authorization", "read-only", "missing-authorizer", "cancelled", "identity", "review", "workspace"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			manager, err := proc.NewManager(root, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			auth := &processScopeAuthorizer{deny: mode == "authorization"}
			config := ProcessScopeConfig{Manager: manager, RootDir: root, ThreadID: "thread", ActorID: "actor", PermissionMode: "unconfined", Boundary: UnconfinedBoundary(), Authorizer: auth}
			if mode == "read-only" {
				config.Boundary = ReadOnlyBoundary()
				config.PermissionMode = "read-only"
			}
			if mode == "missing-authorizer" {
				config.Authorizer = nil
			}
			if mode == "review" {
				config.PermissionMode = "standard"
				config.Boundary = StandardBoundary()
				config.ApproveForMe = true
				config.Reviewer = &recordingReviewer{decision: approvefor.Decision{Outcome: approvefor.OutcomeDeny, Reason: "fixture review denial"}}
			}
			if mode == "workspace" {
				config.PermissionMode = "standard"
				config.Boundary = StandardBoundary()
			}
			scope := NewProcessScope(config)
			ctx := context.Background()
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if mode == "identity" {
				if _, err := scope.Bind("other-thread", "actor", root); err == nil {
					t.Fatal("forged thread accepted")
				}
				return
			}
			cwd := ""
			if mode == "workspace" {
				cwd = t.TempDir()
			}
			_, _, err = scope.Start(ctx, "touch blocked", cwd)
			if err == nil {
				t.Fatal("forbidden process started")
			}
			if _, err := os.Stat(filepath.Join(root, "blocked")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("side effect occurred: %v", err)
			}
		})
	}
}

func TestNativePluginProcessUsesHostScope(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := proc.NewManager(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	auth := &processScopeAuthorizer{}
	kit.SetProcessManager(manager)
	kit.SetSessionID("thread")
	kit.SetAgentIdentity("actor", "root")
	kit.SetBoundary(UnconfinedBoundary())
	kit.SetPermissionMode("unconfined")
	kit.SetAuthorizer(auth)
	kit.SetGitAttributionEnabled(false)
	started, got, err := kit.StartPluginProcess(context.Background(), "printf native-host-process", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = manager.Stop(started.ID) })
	if got != manager || started.RootThreadID != "thread" || started.OwnerID != "actor" || len(auth.requests) != 1 {
		t.Fatalf("native gateway ownership: %+v auth=%+v", started, auth.requests)
	}
}
