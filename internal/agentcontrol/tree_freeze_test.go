package agentcontrol

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

// turn/interrupt semantics: the freeze cancels the whole tree preserving
// partial state, gates wakes so racing terminals cannot restart cancelled
// parents, and ResolveFrozenWorkerTree hands the held results to the next
// user turn exactly once (docs/en/integrations/app-server-protocol.md, Turn Interrupt and
// Tree Freeze).
func TestFreezeWorkerTreeCancelsAndHoldsResults(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	parentTurn := scriptedStreamTurn{started: make(chan struct{}), gate: make(chan struct{}), content: "parent done"}
	childTurn := scriptedStreamTurn{started: make(chan struct{}), gate: make(chan struct{}), content: "child done"}
	client := &scriptedStreamClient{steps: []scriptedStreamTurn{parentTurn, childTurn}}
	c, err := New(Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),

		Client:        client,
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-freeze",
		HistoryDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-freeze", "workers"),
		HarnessDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-freeze", "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })

	parent, err := c.Spawn(context.Background(), SpawnRequest{
		Type: DefaultSubagentType, TaskName: "fz_parent", Description: "parent", Prompt: "orchestrate",
	})
	if err != nil {
		t.Fatalf("spawn parent: %v", err)
	}
	<-parentTurn.started
	child, err := c.Spawn(context.Background(), SpawnRequest{
		Type: DefaultSubagentType, TaskName: "fz_child", Description: "child", Prompt: "dig",
		ParentID: parent.AgentID, ParentPath: parent.AgentPath,
	})
	if err != nil {
		t.Fatalf("spawn child: %v", err)
	}
	<-childTurn.started

	c.FreezeWorkerTree()
	if !c.WorkerTreeFrozen() {
		t.Fatal("tree must report frozen after FreezeWorkerTree")
	}
	waitForWorkerStatus(t, c, parent.AgentID, subagent.StatusCancelled)
	waitForWorkerStatus(t, c, child.AgentID, subagent.StatusCancelled)

	// The freeze must hold: no worker may return to running while frozen
	// even though terminal deliveries fired during the cancellation wave.
	time.Sleep(150 * time.Millisecond)
	for _, id := range []string{parent.AgentID, child.AgentID} {
		if snap := c.manager.Get(id).Snapshot(); snap.Status != subagent.StatusCancelled {
			t.Fatalf("worker %s status = %q while frozen, want cancelled", id, snap.Status)
		}
	}

	c.ResolveFrozenWorkerTree()
	if c.WorkerTreeFrozen() {
		t.Fatal("ResolveFrozenWorkerTree must lift the freeze")
	}
	// Lifting the freeze must not auto-restart anything: the root resumes
	// selected workers explicitly.
	time.Sleep(150 * time.Millisecond)
	for _, id := range []string{parent.AgentID, child.AgentID} {
		if snap := c.manager.Get(id).Snapshot(); snap.Status != subagent.StatusCancelled {
			t.Fatalf("worker %s status = %q after unfreeze, want cancelled (no auto-restart)", id, snap.Status)
		}
	}
}
