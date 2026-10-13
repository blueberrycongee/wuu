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
	for _, mode := range []string{"child-completes", "stop-and-resume", "stop-all-and-resume"} {
		t.Run(mode, func(t *testing.T) {
			stop := mode != "child-completes"
			dir := t.TempDir()
			initRepo(t, dir)
			parentTurn := scriptedStreamTurn{started: make(chan struct{}), gate: make(chan struct{}), content: "parent first pass"}
			childTurn := scriptedStreamTurn{started: make(chan struct{}), gate: make(chan struct{}), content: "child evidence"}
			integration := scriptedStreamTurn{content: "integrated result"}
			client := &scriptedStreamClient{steps: []scriptedStreamTurn{parentTurn, childTurn, integration}}
			threadDir := ""
			if stop {
				threadDir = filepath.Join(dir, ".wuu-state", "sessions", "sess-waiting", "threads")
			}
			c, err := New(Config{
				Client:        client,
				DefaultModel:  "fake-model",
				ParentRepo:    dir,
				WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
				SessionID:     "sess-waiting",
				HistoryDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-waiting", "workers"),
				ThreadDir:     threadDir,
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

			if stop {
				parked := c.manager.Get(parent.AgentID).Snapshot()
				parked.Status = subagent.StatusCompleted
				oldTerminal := newWorkerTerminalFinalizationRecord(subagent.Notification{AgentID: parent.AgentID, Status: subagent.StatusCompleted, Snapshot: parked})
				cancelled := make(chan struct{})
				release := make(chan struct{})
				var cancelOnce, releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				t.Cleanup(unblock)
				c.SetWorkerTerminalTransitionHookForTest(func(id string) {
					if id == parent.AgentID {
						cancelOnce.Do(func() { close(cancelled); <-release })
					}
				})
				if mode == "stop-all-and-resume" {
					c.StopAll()
				} else if !c.Stop(parent.AgentID) {
					t.Fatal("stop parked subtree failed")
				}
				select {
				case <-cancelled:
				case <-time.After(5 * time.Second):
					t.Fatal("parked cancellation did not reach terminal delivery")
				}
				if !c.workerExecutionOwned(parent.AgentID) {
					t.Fatal("parked cancellation lost execution ownership before terminal delivery")
				}
				// A scanner holding the old completion must not acknowledge or
				// replay it over the new cancellation while its observer waits.
				c.startPendingWorkerTerminalRecovery(oldTerminal)
				c.workerTerminalRecoveryWG.Wait()
				if pending, err := c.workerTerminalFinalizationPending(parent.AgentID); err != nil || !pending {
					t.Fatalf("recovery consumed in-flight cancellation: pending=%v err=%v", pending, err)
				}
				unblock()
				for _, id := range []string{parent.AgentID, child.AgentID} {
					waitForWorkerStatus(t, c, id, subagent.StatusCancelled)
					if _, err := c.Wait(context.Background(), id); err != nil {
						t.Fatal(err)
					}
					meta := waitForThreadStatus(t, c, id, agentthread.StatusCancelled)
					if meta.Source.EdgeStatus != agentthread.EdgeClosed {
						t.Fatalf("stopped edge still open: %+v", meta)
					}
					if pending, err := c.workerTerminalFinalizationPending(id); err != nil || pending {
						t.Fatalf("stopped worker did not settle: pending=%v err=%v", pending, err)
					}
					run, err := subagent.LoadPersistedRun(filepath.Join(c.historyDir, id+".json"))
					if err != nil || run.Status != subagent.StatusCancelled {
						t.Fatalf("cancelled state not persisted: %+v, %v", run, err)
					}
				}
				if snap := c.manager.Get(parent.AgentID).Snapshot(); snap.Result != "parent first pass" {
					t.Fatalf("stop discarded held parent output: %+v", snap)
				}
				cancelledChild := c.manager.Get(child.AgentID).Snapshot()
				if delivery, err := c.ensureAgentResultDelivery(cancelledChild); err != nil || delivery.ConsumedBy != "" {
					t.Fatalf("stopped child result must remain available: %+v, %v", delivery, err)
				}
				if _, err := c.FollowupTask(context.Background(), parent.AgentID, " "); err == nil {
					t.Fatal("empty resume accepted")
				}
				if meta, _ := c.threads.Resolve(parent.AgentID); meta.Source.EdgeStatus != agentthread.EdgeClosed {
					t.Fatal("failed followup reopened stopped edge")
				}

				// Explicit follow-up starts a new generation on both closed edges.
				// The next child result must be allowed to wake the resumed parent.
				parentTurn = scriptedStreamTurn{started: make(chan struct{}), gate: make(chan struct{}), content: "resumed parent"}
				childTurn = scriptedStreamTurn{started: make(chan struct{}), gate: make(chan struct{}), content: "resumed child"}
				client.mu.Lock()
				client.steps = append(client.steps[:2], parentTurn, childTurn, integration)
				client.mu.Unlock()
				if _, err := c.FollowupTask(context.Background(), parent.AgentID, "resume parent"); err != nil {
					t.Fatal(err)
				}
				<-parentTurn.started
				if err := c.SendMessage(context.Background(), child.AgentID, "resume child"); err != nil {
					t.Fatal(err)
				}
				<-childTurn.started
				for _, id := range []string{parent.AgentID, child.AgentID} {
					if meta, _ := c.threads.Resolve(id); meta.Source.EdgeStatus != agentthread.EdgeOpen {
						t.Fatalf("explicit resume did not reopen %s", id)
					}
				}
			}

			// The child's delivery wakes the parent for integration; the integrated
			// completion has no live children left and becomes the one terminal.
			close(childTurn.gate)
			waitForWorkerStatus(t, c, child.AgentID, subagent.StatusCompleted)
			if stop {
				// The cancellation result was retained; a later child's normal
				// result may now queue to the explicitly resumed parent.
				if _, err := c.Wait(context.Background(), child.AgentID); err != nil {
					t.Fatal(err)
				}
				close(parentTurn.gate)
			}
			final := waitForWorkerStatus(t, c, parent.AgentID, subagent.StatusCompleted)
			if final.Result != "integrated result" {
				t.Fatalf("parent final result = %q, want the integrated turn's output", final.Result)
			}
			waitForThreadStatus(t, c, parent.AgentID, agentthread.StatusCompleted)
		})
	}
}
