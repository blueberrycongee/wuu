package appserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func TestAgentThreadReadDoesNotPublishPendingTerminal(t *testing.T) {
	const rootID = "pending-visibility-root"
	rt := newTestRuntime(t, &fakeClient{})
	rt.StateDir = filepath.Join(rt.RootDir, ".wuu-state")
	if _, err := rt.Sessions.CreateWithMetadata(rootID, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	artifact := statepath.SessionArtifactDir(rt.StateDir, rootID)
	client := &fakeClient{response: providersResponse("pending secret")}
	c, err := agentcontrol.New(agentcontrol.Config{AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}), Client: providers.AdaptStreamClient(client), DefaultModel: "fake-model", ParentRepo: rt.RootDir, SessionID: rootID, HistoryDir: filepath.Join(artifact, "workers"), ThreadDir: filepath.Join(artifact, "threads"), HarnessDir: filepath.Join(artifact, "harness"), Documents: rt.Documents, WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
		return noopToolExecutor{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	s := New(rt, out)
	binding := testRuntimeState(t, rt)
	th := newThreadState(rootID, nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	th.state = binding
	th.execRuntime = &runtime.ThreadRuntime{AgentControl: c}
	s.mu.Lock()
	s.threads[rootID] = th
	s.mu.Unlock()
	entered := make(chan string, 1)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() {
		once.Do(func() { close(release) })
		c.BeginShutdown()
		c.StopAll()
		c.YieldWorkerTerminalFinalizations()
		c.Close()
		s.Close()
	})
	c.SetWorkerTerminalTransitionHookForTest(func(id string) { entered <- id; <-release })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := c.Spawn(ctx, agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "pending_visibility", Prompt: "finish"})
	if err != nil {
		t.Fatal(err)
	}
	id := <-entered
	thread, found, err := s.agentSessionThread(binding, id)
	if err != nil || !found {
		t.Fatalf("read thread: found=%v err=%v", found, err)
	}
	raw, _ := json.Marshal(thread)
	if thread.Status != ThreadStatusInProgress || len(thread.Turns) == 0 || thread.Turns[len(thread.Turns)-1].Status != TurnStatusInProgress {
		t.Fatalf("unconfirmed worker shown as terminal: %s", raw)
	}
	if result.Status == "completed" {
		t.Fatalf("async spawn status=%q", result.Status)
	}
	once.Do(func() { close(release) })
	if _, err := c.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
}
