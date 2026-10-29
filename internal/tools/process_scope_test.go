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
	"github.com/blueberrycongee/wuu/internal/executionenv"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/toolresult"
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
	t.Setenv("WUU_CLI_EXECUTABLE", "/old-host/wuu")
	root := t.TempDir()
	manager, err := proc.NewManager(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	auth := &processScopeAuthorizer{}
	scope := NewProcessScope(ProcessScopeConfig{Manager: manager, RootDir: root, ThreadID: "thread", ActorID: "actor", PermissionMode: "unconfined", Boundary: UnconfinedBoundary(), Authorizer: auth})
	process, gotManager, err := scope.Start(context.Background(), `test -z "${WUU_CLI_EXECUTABLE+x}" && printf 'host-owned-output'`, "")
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

// A selected remote environment is a binding, not a preference. Host paths,
// sandbox preparation and a later replacement cannot retarget an accepted job.
type processScopeRemote struct {
	manager  *proc.Manager
	requests []executionenv.ProcessRequest
	err      error
}

func (r *processScopeRemote) Execute(context.Context, executionenv.ToolRequest) (toolresult.Result, error) {
	panic("tool fallback")
}
func (r *processScopeRemote) Root() string                  { return "/selected/workspace" }
func (r *processScopeRemote) Backend() string               { return "ssh" }
func (r *processScopeRemote) Identity() string              { return "selected-environment" }
func (r *processScopeRemote) ProcessManager() *proc.Manager { return r.manager }
func (r *processScopeRemote) StartProcess(ctx context.Context, request executionenv.ProcessRequest) (*proc.Process, error) {
	r.requests = append(r.requests, request)
	if r.err != nil {
		return nil, r.err
	}
	return &proc.Process{ID: "remote-process", RootThreadID: "thread", OwnerID: request.Actor}, ctx.Err()
}
func TestHostProcessScopeUsesSelectedEnvironment(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{true: "denied", false: "allowed"}[denied], func(t *testing.T) {
			root := t.TempDir()
			local, err := proc.NewManager(root, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			remote := &processScopeRemote{manager: proc.NewRemoteManager("/selected/workspace", nil)}
			auth := &processScopeAuthorizer{deny: denied}
			scope := NewProcessScope(ProcessScopeConfig{Manager: local, ExecutionEnvironment: remote, RootDir: root, ThreadID: "thread", ActorID: "actor", PermissionMode: "unconfined", Boundary: UnconfinedBoundary(), Authorizer: auth, ExecutionRoot: func(context.Context) (string, error) {
				t.Fatal("remote start inspected host execution root")
				return "", nil
			}, CommandPrefix: func() (string, error) { t.Fatal("remote start built host command prefix"); return "", nil }})
			bound, err := scope.Bind("thread", "actor", root)
			if err != nil {
				t.Fatal(err)
			}
			location, err := bound.Environment()
			if err != nil || location.Root != remote.Root() || location.Backend != "ssh" || location.Identity != "selected-environment" {
				t.Fatalf("environment=%+v err=%v", location, err)
			}
			started, manager, err := bound.Start(context.Background(), "printf remote", "subdir")
			if denied {
				if err == nil || len(remote.requests) != 0 {
					t.Fatalf("denied start dispatched: %v %+v", err, remote.requests)
				}
				return
			}
			if err != nil || started.ID != "remote-process" || manager != remote.manager {
				t.Fatalf("remote start=%+v manager=%p err=%v", started, manager, err)
			}
			if len(remote.requests) != 1 || remote.requests[0].CWD != "subdir" || remote.requests[0].Actor != "actor" || remote.requests[0].PermissionMode != "unconfined" {
				t.Fatalf("request=%+v", remote.requests)
			}
			if len(auth.requests) != 1 || auth.requests[0].CWD != remote.Root() {
				t.Fatalf("authorization must inspect remote workspace: %+v", auth.requests)
			}
			remote.err = errors.New("transport disconnected; outcome unknown")
			if _, _, err = bound.Start(context.Background(), "touch must-not-fall-back", ""); err == nil || !strings.Contains(err.Error(), "outcome unknown") {
				t.Fatalf("remote failure=%v", err)
			}
			if _, err = os.Stat(filepath.Join(root, "must-not-fall-back")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("remote failure executed locally")
			}
		})
	}
}
func TestNativePluginProcessRetainsSelectedEnvironment(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionID("thread")
	kit.SetAgentIdentity("actor", "root")
	kit.SetPermissionMode("unconfined")
	kit.SetBoundary(UnconfinedBoundary())
	kit.SetAuthorizer(&processScopeAuthorizer{})
	first := &processScopeRemote{manager: proc.NewRemoteManager("/selected/workspace", nil)}
	kit.SetExecutionEnvironment(first)
	scope, err := kit.PluginProcessScope()
	if err != nil {
		t.Fatal(err)
	}
	second := &processScopeRemote{manager: proc.NewRemoteManager("/replacement/workspace", nil)}
	kit.SetExecutionEnvironment(second)
	_, manager, err := scope.Start(context.Background(), "printf original", "")
	if err != nil || manager != first.manager || len(first.requests) != 1 || len(second.requests) != 0 {
		t.Fatalf("retained binding changed: first=%+v second=%+v err=%v", first.requests, second.requests, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = scope.Start(ctx, "printf cancelled", ""); !errors.Is(err, context.Canceled) || len(first.requests) != 1 {
		t.Fatalf("cancelled start dispatched: %v", err)
	}
}
