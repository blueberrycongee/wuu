package appserver

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

func TestGeneralSettingsResetKeepsActiveRuntimeUntilWorkerFinalization(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	workerClient := newBlockingStreamClient("worker finished")
	control, err := agentcontrol.New(agentcontrol.Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),

		Client:       workerClient,
		DefaultModel: "fake-model",
		ParentRepo:   rt.RootDir,
		WorktreeRoot: filepath.Join(rt.RootDir, ".wuu", "worktrees"),
		SessionID:    "settings-reset-active",
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	if err != nil {
		t.Fatalf("agentcontrol.New: %v", err)
	}
	t.Cleanup(func() {
		control.StopAll()
		control.Close()
	})

	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	now := time.Now().UTC()
	th := newThreadState("settings-reset-active", nil, rt.ProviderName, rt.Model, rt.RootDir, false, now)
	th.state = testRuntimeState(t, rt)
	threadRuntime := &runtime.ThreadRuntime{AgentControl: control}
	subscription := srv.subscribeThreadRuntime(testRuntimeState(t, srv.rt), th.ID, threadRuntime)
	th.execRuntime = threadRuntime
	th.runtimeSubscription = subscription
	th.startTurnLocked("root-turn", providers.ChatMessage{Role: "user", Content: "spawn after settings update"}, now)
	srv.mu.Lock()
	srv.threads[th.ID] = th
	srv.mu.Unlock()

	srv.resetThreadRuntimesForGeneralSettings("updated system prompt")
	th.mu.Lock()
	if th.execRuntime != threadRuntime || !th.pendingRuntimeReset {
		t.Fatalf("active runtime was not deferred: runtime=%p pending=%t", th.execRuntime, th.pendingRuntimeReset)
	}
	th.mu.Unlock()
	select {
	case <-subscription.done:
		t.Fatal("settings reset stopped active runtime forwarding")
	default:
	}

	spawned, err := control.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "spawn_after_settings_reset",
		Description: "prove the active runtime remains admitted",
		Prompt:      "finish after release",
		Isolation:   string(agentcontrol.IsolationInplace),
	})
	if err != nil {
		t.Fatalf("spawn after deferred reset: %v", err)
	}
	select {
	case <-workerClient.started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start after deferred reset")
	}
	close(workerClient.release)
	waitForAgentStatus(t, control, spawned.AgentID, subagent.StatusCompleted)
	waitForWorkerFinalization(t, control)
	waitForAgentUpdateStatus(t, out, spawned.AgentID, string(subagent.StatusCompleted))

	select {
	case <-subscription.done:
		t.Fatal("runtime forwarding stopped before the active root turn ended")
	default:
	}
	th.mu.Lock()
	th.completeTurnLocked("root-turn", TurnStatusCompleted, nil, time.Now().UTC(), "", "", false)
	th.mu.Unlock()
	srv.resetThreadRuntimesForGeneralSettings("updated system prompt")

	th.mu.Lock()
	if th.execRuntime != nil || th.pendingRuntimeReset {
		t.Fatalf("idle settled runtime was not reset: runtime=%p pending=%t", th.execRuntime, th.pendingRuntimeReset)
	}
	th.mu.Unlock()
	select {
	case <-subscription.done:
	case <-time.After(2 * time.Second):
		t.Fatal("settled runtime subscription was not released")
	}
}

func TestQueuedTurnRejectedAfterDurableAdmissionPersistsFailure(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "must not run"}}
	rt := newTestRuntime(t, client)
	sess, err := rt.Sessions.CreateWithMetadata("queued-prelaunch-close", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	if _, err := srv.ensureThreadLoaded(testRuntimeState(t, srv.rt), sess.ID); err != nil {
		t.Fatalf("load queued thread: %v", err)
	}
	srv.beforeQueuedTurnBackgroundForTest = func() {
		srv.closed.Store(true)
	}

	started, err := srv.startQueuedTurn(testRuntimeState(t, srv.rt), context.Background(), sess.ID, queuedTurn{
		id:  "queued-before-close",
		msg: providers.ChatMessage{Role: "user", Content: "persist me before shutdown"},
	})
	if started || !errors.Is(err, errServerClosed) {
		t.Fatalf("queued turn started=%t err=%v, want server closed", started, err)
	}
	assertFakeClientRequestCount(t, client, 0)
	assertPersistedPrelaunchTurnFailed(t, srv, sess.ID, errServerClosed.Error())
	assertThreadLeaseAvailable(t, rt.Sessions, rt.SessionDir, sess.ID)

	loaded, err := srv.loadPersistedThreadSnapshot(testRuntimeState(t, srv.rt), sess.ID)
	if err != nil {
		t.Fatalf("reload queued thread: %v", err)
	}
	turns := turnsFromPersistedHistory(sess.ID, loaded.displayHistory, time.Now().UTC(), srv.participantResolver(testRuntimeState(t, srv.rt)))
	if len(turns) != 1 || turns[0].Status != TurnStatusFailed {
		t.Fatalf("reloaded queued turn = %+v, want one failed turn", turns)
	}
}

func waitForWorkerFinalization(t *testing.T, control *agentcontrol.AgentControl) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !control.HasOwnedWorkerExecutions() && !control.HasPendingWorkerTerminalFinalizations() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("worker finalization did not settle: leases=%d pending=%t", control.OwnedWorkerExecutionCount(), control.HasPendingWorkerTerminalFinalizations())
}

func waitForAgentUpdateStatus(t *testing.T, out *lockedBuffer, agentID, status string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, message := range parseOutput(t, out.String()) {
			if message["method"] != NotificationAgentUpdated {
				continue
			}
			update := remarshal[AgentUpdatedNotification](t, message["params"])
			if update.Agent.ID == agentID && update.Agent.Status == status {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("agent %q did not publish status %q; output:\n%s", agentID, status, out.String())
}
