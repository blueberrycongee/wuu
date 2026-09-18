package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestSessionCreationRoutesExplicitWorkspaceInsteadOfParentHome(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("done")})
	rt.WuuHome = t.TempDir()
	project := t.TempDir()
	if err := os.MkdirAll(rt.WuuHome, 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"projects": []map[string]string{{"id": "target", "path": project, "name": "Target"}}})
	if err := os.WriteFile(filepath.Join(rt.WuuHome, "projects.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	parent, err := srv.createPluginSession(context.Background(), "test", pluginhost.SessionCreateParams{RequestID: "parent", Visibility: "user", ContextSource: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := srv.createPluginSession(context.Background(), "test", pluginhost.SessionCreateParams{RequestID: "child", Visibility: "user", ContextSource: "fresh", ParentSessionID: parent.SessionID, WorkspaceID: "target", WorkspaceRoot: project})
	if err != nil {
		t.Fatal(err)
	}
	meta, ok, err := session.Find(rt.SessionDir, created.SessionID)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if meta.CWD != project || meta.WorkspaceID != "target" || meta.ParentID != parent.SessionID {
		t.Fatalf("wrong workspace or ancestry: %+v", meta)
	}
	if _, _, err := srv.resolveSessionWorkspace("target", rt.RootDir); err == nil {
		t.Fatal("mismatched root accepted")
	}
}

func TestLocalForkExecutionUsesPersistedProjectWithDifferentCWD(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WorkspaceID = "project"
	rt.WuuHome = t.TempDir()
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	// Same-directory forks carry the project ID without owning the source's
	// worktree. Their execution directory must not become a project-root check.
	fork, err := session.CreateForkWithMetadata(rt.SessionDir, "local-fork", t.TempDir(), session.ForkMetadata{ForkedFromID: "source"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.SetWorkspaceID(rt.SessionDir, fork.ID, rt.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	th, err := srv.ensureThreadLoaded(fork.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.ensureThreadRuntime(th); err != nil {
		t.Fatalf("local fork could not execute: %v", err)
	}
	// A cached runtime must still reject execution after a project rebind.
	th.mu.Lock()
	th.WorkspaceID = "unregistered-project"
	th.mu.Unlock()
	if _, err := srv.ensureThreadRuntime(th); err == nil {
		t.Fatal("foreign project reused the cached runtime")
	}
}
