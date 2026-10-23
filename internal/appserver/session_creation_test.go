package appserver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func TestFailedForkCreationUsesSelectedArtifactCleanup(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	provider := &threadDeleteArtifactProvider{err: errors.New("selected rollback unavailable")}
	bindThreadDeleteArtifacts(t, rt, provider)
	srv := &Server{rt: rt}
	binding := testRuntimeState(t, rt)
	if _, err := rt.Sessions.CreateWithMetadata("parent", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	stateDir, err := srv.workspaceStateDir()
	if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(statepath.SessionArtifactDir(stateDir, "failed-fork"), "artifacts", "unselected")
	if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("unselected payload"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = srv.createHostSessionThreadWithStore(binding, binding.Sessions, "test", "test", "failed-fork", hostSessionCreateParams{
		ContextSource: sessionContextFork, ParentSessionID: "parent", InitialInput: &session.InboxMessage{},
	})
	if err == nil || !strings.Contains(err.Error(), "client ID") || !strings.Contains(err.Error(), "cleanup") || provider.calls != 1 {
		t.Fatalf("failed commit did not retain selected rollback failure: calls=%d err=%v", provider.calls, err)
	}
	if _, found, err := rt.Sessions.Find("failed-fork"); err != nil || found {
		t.Fatalf("failed fork persisted: found=%v err=%v", found, err)
	}
	if _, found, err := rt.Sessions.Find("parent"); err != nil || !found {
		t.Fatalf("rollback changed parent: found=%v err=%v", found, err)
	}
	if data, err := os.ReadFile(local); err != nil || string(data) != "unselected payload" {
		t.Fatalf("rollback fell back to native deletion: %q %v", data, err)
	}
}
