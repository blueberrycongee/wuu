package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func processTestSession(t *testing.T) *Session {
	t.Helper()
	home := t.TempDir()
	t.Setenv("WUU_HOME", home)
	root := t.TempDir()
	kit, err := tools.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return &Session{
		ProviderName: "primary", Model: "gpt-test", RootDir: root,
		WuuHome: home, SessionDir: statepath.SessionsDir(home), Toolkit: kit,
		StreamRunner: &agent.StreamRunner{Client: &sessionRecordingClient{}, ProviderName: "primary", Model: "gpt-test"},
	}
}

func TestSessionCleanupOwnsCachedProcessManagers(t *testing.T) {
	s := processTestSession(t)
	manager, err := process.NewManager(s.RootDir, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	s.ProcessManager = manager
	cached, err := s.processManagerForThread(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	job, err := cached.Start(context.Background(), process.StartOptions{Command: "cat", OwnerKind: process.OwnerMainAgent, OwnerID: "session-owner", RootThreadID: "session-owner", Lifecycle: process.LifecycleSession})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = cached.Stop(job.ID) })
	cleaned, err := s.Cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned.Cleaned) != 1 || cleaned.Cleaned[0].ID != job.ID {
		t.Fatalf("runtime cleanup omitted cached process: %+v", cleaned)
	}
	settled, err := cached.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Status != process.StatusStopped || settled.RootThreadID != "session-owner" {
		t.Fatalf("cached process was not stopped with ownership intact: %+v", settled)
	}
}

func TestThreadModelClonesReuseProcessManagerPool(t *testing.T) {
	s := processTestSession(t)
	manager, err := process.NewManager(s.RootDir, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	s.ProcessManager = manager
	first := s.cloneForThreadModel()
	second := s.cloneForThreadModel()
	root, state := t.TempDir(), t.TempDir()
	a, err := first.processManagerForThread(root, state)
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.processManagerForThread(root, state)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("thread model clones created separate managers for one registry")
	}
	other, err := second.processManagerForThread(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if other == a {
		t.Fatal("different process registries reused one manager")
	}
}
