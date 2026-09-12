package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/process"
)

func TestCollaborationSessionsShareProcessManagerAndKeepExecutionIndependent(t *testing.T) {
	s, _ := collaborationTestSession(t)
	manager, err := process.NewManager(s.RootDir, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	s.ProcessManager = manager
	root := t.TempDir()
	type result struct {
		thread *ThreadRuntime
		err    error
	}
	const count = 8
	ready := make(chan result, count)
	start := make(chan struct{})
	for i := 0; i < count; i++ {
		go func(index int) {
			<-start
			thread, err := s.NewNamedAgentThreadRuntime(fmt.Sprintf("same-identity-%d", index), root, filepath.Join(root, "memory"), "shared identity", ThreadModelSelection{})
			ready <- result{thread: thread, err: err}
		}(i)
	}
	close(start)
	var threads []*ThreadRuntime
	for i := 0; i < count; i++ {
		created := <-ready
		if created.err != nil {
			t.Error(created.err)
			continue
		}
		threads = append(threads, created.thread)
		t.Cleanup(func() { created.thread.AgentControl.Close() })
	}
	if len(threads) != count {
		t.Fatal("some collaboration sessions failed to initialize")
	}
	for _, thread := range threads[1:] {
		if thread.ProcessManager != threads[0].ProcessManager {
			t.Fatal("same-identity sessions created competing process registry managers")
		}
		if thread.StreamRunner == threads[0].StreamRunner || thread.Toolkit == threads[0].Toolkit || thread.AgentControl == threads[0].AgentControl {
			t.Fatal("process sharing also shared mutable agent execution state")
		}
		if thread.Toolkit.SessionID() == threads[0].Toolkit.SessionID() {
			t.Fatal("same-identity sessions lost distinct process ownership")
		}
	}
	if threads[0].ProcessManager == s.ProcessManager {
		t.Fatal("identity process registry was merged with the ordinary workspace registry")
	}
	other := collaborationTestThread(t, s, "other-identity", ThreadModelSelection{})
	if other.ProcessManager == threads[0].ProcessManager {
		t.Fatal("different identity roots shared a process manager")
	}
	threads[0].StreamRunner.UpdateSystemPrompt("session-local change")
	threads[0].Toolkit.SetRootDir(t.TempDir())
	threads[0].AgentControl.Close()
	if threads[1].StreamRunner.SystemPrompt == "session-local change" || !sameRuntimeRoot(threads[1].Toolkit.RootDir(), root) {
		t.Fatal("changing or closing one session changed another session's context")
	}
	if _, err := threads[1].ProcessManager.List(); err != nil {
		t.Fatalf("closing one session closed the shared process manager: %v", err)
	}
}

func TestSessionCleanupOwnsCachedProcessManagers(t *testing.T) {
	s, _ := collaborationTestSession(t)
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
	s, _ := collaborationTestSession(t)
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
