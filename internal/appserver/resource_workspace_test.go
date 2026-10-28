package appserver

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/principal"
)

func TestWorkspaceResourceDefaultAndSymlinkCannotBorrowGlobalGrant(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	caller, _ := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "test", Tenant: "test", Subject: "agent"})
	root, err := resolvedWorkspaceResource(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	var allowWorkspace atomic.Bool
	out := &lockedBuffer{}
	s := New(rt, out, WithPrincipal(caller), WithAccessPolicy(access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		for _, resource := range r.Resources {
			if resource.Kind == "workspace" && (!allowWorkspace.Load() || resource.ID != root.ID) {
				return access.Decision{}, nil
			}
		}
		return access.Decision{Allow: true}, nil
	})))
	defer s.Close()
	dispatchPayload(t, s, "create-denied", MethodThreadStart, ThreadStartParams{})
	if responseByID(t, parseOutput(t, out.String()), "create-denied")["error"] == nil {
		t.Fatal("global create granted omitted workspace")
	}
	dispatchPayload(t, s, "read-denied", MethodWorkspaceDirectoryList, map[string]string{})
	if responseByID(t, parseOutput(t, out.String()), "read-denied")["error"] == nil {
		t.Fatal("global read granted omitted workspace")
	}
	allowWorkspace.Store(true)
	dispatchPayload(t, s, "create", MethodThreadStart, ThreadStartParams{})
	if responseByID(t, parseOutput(t, out.String()), "create")["error"] != nil {
		t.Fatal("named workspace creation denied")
	}
	other := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(other, alias); err != nil {
		t.Fatal(err)
	}
	dispatchPayload(t, s, "alias", MethodThreadStart, ThreadStartParams{CWD: alias})
	if responseByID(t, parseOutput(t, out.String()), "alias")["error"] == nil {
		t.Fatal("symlink alias escaped workspace grant")
	}
	sessions, err := rt.Sessions.List(0)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("denied creation persisted: %d %v", len(sessions), err)
	}
}

func TestWorkspaceListFiltersCurrentMetadataAndRechecksReplay(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	root, err := resolvedWorkspaceResource(a)
	if err != nil {
		t.Fatal(err)
	}
	caller, _ := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "test", Tenant: "test", Subject: "agent"})
	var revoked atomic.Bool
	s := &Server{access: connectionAccess{principal: caller, policy: access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		for _, resource := range r.Resources {
			if resource.Kind != "global" && (resource.ID != root.ID || revoked.Load()) {
				return access.Decision{}, nil
			}
		}
		return access.Decision{Allow: true}, nil
	})}}
	req := Request{ID: mustJSON("list"), Method: "workspace/list"}
	if err := s.trackAuthorizedRequest(principal.WithContext(context.Background(), caller), req); err != nil {
		t.Fatal(err)
	}
	value, guard, ok := s.authorizeOutbound(context.Background(), Response{ID: req.ID, Result: WorkspaceListResult{Current: b, CurrentID: "hidden", Workspaces: []WorkspaceInfo{{ID: "visible", Path: a}, {ID: "hidden", Path: b}}}})
	if !ok {
		t.Fatal("list response dropped")
	}
	result := value.(Response).Result.(WorkspaceListResult)
	if len(result.Workspaces) != 1 || result.Workspaces[0].ID != "visible" || result.Current != "" || result.CurrentID != "" {
		t.Fatalf("workspace metadata leaked: %+v", result)
	}
	revoked.Store(true)
	if guard() == nil {
		t.Fatal("revoked workspace list row replayed")
	}
}
