package agentcontrol

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

// scriptedStreamClient serves one scripted turn per StreamChat call: an
// optional gate blocks the turn until released, then content streams.
type scriptedStreamClient struct {
	mu    sync.Mutex
	steps []scriptedStreamTurn
	calls int
}

type scriptedStreamTurn struct {
	started chan struct{}
	gate    chan struct{}
	content string
}

func (s *scriptedStreamClient) Chat(_ context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: "done"}, nil
}

func (s *scriptedStreamClient) StreamChat(ctx context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	s.mu.Lock()
	index := s.calls
	if index >= len(s.steps) {
		index = len(s.steps) - 1
	}
	step := s.steps[index]
	s.calls++
	s.mu.Unlock()
	ch := make(chan providers.StreamEvent, 2)
	go func() {
		defer close(ch)
		if step.started != nil {
			close(step.started)
		}
		if step.gate != nil {
			select {
			case <-step.gate:
			case <-ctx.Done():
				ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
				return
			}
		}
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: step.content}
		ch <- providers.StreamEvent{Type: providers.EventDone}
	}()
	return ch, nil
}

func waitForWorkerStatus(t *testing.T, c *AgentControl, id string, want subagent.Status) subagent.SubAgentSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if sa := c.manager.Get(id); sa != nil {
			snap := sa.Snapshot()
			if snap.Status == want {
				return snap
			}
		}
		if time.Now().After(deadline) {
			var got subagent.Status
			if sa := c.manager.Get(id); sa != nil {
				got = sa.Snapshot().Status
			}
			t.Fatalf("worker %s status = %q, want %q", id, got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForThreadStatus(t *testing.T, c *AgentControl, id string, want agentthread.Status) agentthread.Metadata {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if meta, ok := c.threads.Resolve(id); ok && meta.Status == want {
			return meta
		}
		if time.Now().After(deadline) {
			meta, ok := c.threads.Resolve(id)
			t.Fatalf("thread %s status = %+v (ok=%v), want %s", id, meta, ok, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A parent that finishes its final message while a direct child is still
// running parks as waiting_children (result held, no delivery), resumes when
// the child's result arrives, and only its integrated completion delivers —
// once (docs/en/integrations/app-server-protocol.md, Anonymous Worker Lifecycle States).
func TestCompletedParentParksUntilChildDelivers(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	parentTurn := scriptedStreamTurn{started: make(chan struct{}), gate: make(chan struct{}), content: "parent first pass"}
	childTurn := scriptedStreamTurn{started: make(chan struct{}), gate: make(chan struct{}), content: "child evidence"}
	integration := scriptedStreamTurn{content: "integrated result"}
	client := &scriptedStreamClient{steps: []scriptedStreamTurn{parentTurn, childTurn, integration}}
	c, err := New(Config{
		Client:        client,
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-waiting",
		HistoryDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-waiting", "workers"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })

	parent, err := c.Spawn(context.Background(), SpawnRequest{
		Type: DefaultSubagentType, TaskName: "wc_parent", Description: "parent", Prompt: "orchestrate",
	})
	if err != nil {
		t.Fatalf("spawn parent: %v", err)
	}
	<-parentTurn.started

	child, err := c.Spawn(context.Background(), SpawnRequest{
		Type: DefaultSubagentType, TaskName: "wc_child", Description: "child", Prompt: "dig",
		ParentID: parent.AgentID, ParentPath: parent.AgentPath,
	})
	if err != nil {
		t.Fatalf("spawn child: %v", err)
	}
	<-childTurn.started

	// Parent finishes while the child is still live: it must park, holding
	// its result, and its thread record must mirror the parked state.
	close(parentTurn.gate)
	waitForWorkerStatus(t, c, parent.AgentID, subagent.StatusWaitingChildren)
	waitForThreadStatus(t, c, parent.AgentID, agentthread.StatusWaitingChildren)

	// The child's delivery wakes the parent for integration; the integrated
	// completion has no live children left and becomes the one terminal.
	close(childTurn.gate)
	waitForWorkerStatus(t, c, child.AgentID, subagent.StatusCompleted)
	final := waitForWorkerStatus(t, c, parent.AgentID, subagent.StatusCompleted)
	if final.Result != "integrated result" {
		t.Fatalf("parent final result = %q, want the integrated turn's output", final.Result)
	}
	waitForThreadStatus(t, c, parent.AgentID, agentthread.StatusCompleted)
}
