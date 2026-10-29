package appserver

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/stretchr/testify/require"
)

func TestReleaseThreadSubscriptionUnsubscribesItsOriginalControl(t *testing.T) {
	first, err := agentcontrol.New(agentcontrol.Config{ParentRepo: t.TempDir(), SessionID: "first", Client: newBlockingStreamClient("unused"), DefaultModel: "fake", WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
		return noopToolExecutor{}, nil
	}})
	require.NoError(t, err)
	defer first.Close()
	status := make(chan subagent.Notification, 2)
	stream := make(chan subagent.StreamNotification, 2)
	first.Subscribe(status)
	first.SubscribeStream(stream)
	sub := &threadRuntimeSubscription{control: first, statusCh: status, streamCh: stream, done: make(chan struct{})}
	sample := &subagent.SubAgent{}
	first.Manager().BroadcastSnapshot(sample)
	require.Len(t, status, 1)
	<-status

	// Cleanup must detach subscriptions from the control on which they were
	// installed.
	releaseThreadRuntimeSubscription(sub)
	first.Manager().BroadcastSnapshot(sample)
	require.Empty(t, status)
}

func TestRetiringOldSubscriptionKeepsNewControlQueueUsable(t *testing.T) {
	root := t.TempDir()
	client := newBlockingStreamClient("finished")
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(client.release) }) })
	newControl, err := agentcontrol.New(agentcontrol.Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),
		ParentRepo:        root, SessionID: "new", Client: client, DefaultModel: "fake",
		HistoryDir: filepath.Join(root, "workers"), ThreadDir: filepath.Join(root, "threads"), HarnessDir: filepath.Join(root, "harness"), MaxParallel: 1,
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { release.Do(func() { close(client.release) }); newControl.StopAll(); newControl.Close() })
	oldControl, err := agentcontrol.New(agentcontrol.Config{
		ParentRepo: root, SessionID: "old", Client: newBlockingStreamClient("unused"), DefaultModel: "fake",
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(oldControl.Close)
	occupied, err := newControl.Spawn(context.Background(), agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "occupier", Prompt: "wait"})
	require.NoError(t, err)
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	queued, err := newControl.Spawn(context.Background(), agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "queued", Prompt: "finish"})
	require.NoError(t, err)
	require.Equal(t, "queued", queued.Status)
	sub := &threadRuntimeSubscription{control: oldControl, statusCh: make(chan subagent.Notification, 1), streamCh: make(chan subagent.StreamNotification, 1), done: make(chan struct{})}
	releaseThreadRuntimeSubscription(sub)
	newControl.StartQueuedWork()
	release.Do(func() { close(client.release) })
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = newControl.Manager().Wait(waitCtx, occupied.AgentID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		worker := newControl.Manager().Get(queued.AgentID)
		return worker != nil && worker.Snapshot().Status == subagent.StatusCompleted
	}, 5*time.Second, 10*time.Millisecond, "new control's queued worker must still run")
}
