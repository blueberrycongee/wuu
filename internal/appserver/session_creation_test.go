package appserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// A child must not become discoverable before its inherited composition is
// durable. Otherwise retry after a failed write can execute workspace defaults.
func TestSessionCreationRequiresDurableCompositionInheritance(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if _, err := rt.Sessions.CreateWithMetadata("parent", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	binding := testRuntimeState(t, rt)
	failure := errors.New("composition storage unavailable")
	binding.Documents = &rejectedCompositionDocuments{Documents: binding.Documents, err: failure}
	srv := &Server{rt: rt}
	_, err := srv.createHostSessionThreadWithStore(binding, binding.Sessions, "test", "test", "child", hostSessionCreateParams{
		ContextSource: sessionContextFresh, ParentSessionID: "parent",
	})
	if !errors.Is(err, failure) {
		t.Fatalf("creation did not report inheritance failure: %v", err)
	}
	if _, found, err := rt.Sessions.Find("child"); err != nil || found {
		t.Fatalf("child exposed without durable composition: found=%v err=%v", found, err)
	}
}

type rejectedCompositionDocuments struct {
	persistence.Documents
	err error
}

func (d *rejectedCompositionDocuments) Commit(context.Context, persistence.Transaction) (persistence.Receipt, error) {
	return persistence.Receipt{}, d.err
}

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
