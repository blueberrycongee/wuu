package acceptance_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/stretchr/testify/require"
)

// The ordinary child-state algorithms route through the exact same selected
// graph for native and SDK-only external providers, including after restart.
func TestAcceptanceChildStateProviderParity(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl", "external"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			host, documents, _, unused := startAcceptanceProvider(t, kind, dir)
			threads := agentthread.NewDocumentStore(documents, "root", "")
			tasks := harness.NewDocumentStore(documents, "root", "")
			require.NoError(t, threads.UpsertThread(agentthread.Metadata{ID: "worker", Path: "root/worker"}))
			require.NoError(t, threads.AppendEvent(agentthread.Event{Type: agentthread.EventResultReady, ResultID: "result"}))
			claimed, _, err := threads.ClaimResultDelivery(agentthread.Event{ResultID: "result", Consumer: "owner"})
			require.NoError(t, err)
			require.True(t, claimed)
			require.NoError(t, tasks.UpsertTask(harness.Task{ID: "worker", Status: harness.TaskStatusRunning}))
			require.NoError(t, tasks.UpsertQueueItem(harness.QueueItem{ID: "queue", Kind: "agent_spawn"}))
			marked, err := tasks.MarkQueueItemCancelling("queue")
			require.NoError(t, err)
			require.True(t, marked)
			legacy := filepath.Join(t.TempDir(), "worker.json")
			require.NoError(t, os.WriteFile(legacy, []byte(`{"version":1,"id":"worker","status":"completed","result":"durable"}`), 0600))
			run, err := subagent.LoadPersistedRunFromDocuments(documents, "root", "worker", legacy)
			require.NoError(t, err)
			require.Equal(t, "durable", run.Result)
			require.NoError(t, os.Remove(legacy))
			outcome, found := host.RetirePlugin(context.Background(), "acceptance.selected", errors.New("disabled"))
			require.True(t, found)
			require.NoError(t, outcome.Err)
			require.Equal(t, pluginhost.StateActive, unused.Status().State)
			_, err = threads.ListThreads()
			require.Error(t, err)
			_, err = tasks.ListTasks()
			require.Error(t, err)
			_, err = subagent.LoadPersistedRunFromDocuments(documents, "root", "worker", legacy)
			require.Error(t, err)
			require.NoError(t, host.Close(context.Background()))
			_, reopened, _, _ := startAcceptanceProvider(t, kind, dir)
			recovered := agentthread.NewDocumentStore(reopened, "root", "")
			entries, err := recovered.ListThreads()
			require.NoError(t, err)
			require.Len(t, entries, 1)
			claimed, owner, err := recovered.ClaimResultDelivery(agentthread.Event{ResultID: "result", Consumer: "another"})
			require.NoError(t, err)
			require.False(t, claimed)
			require.Equal(t, "owner", owner)
			queue, found, err := harness.NewDocumentStore(reopened, "root", "").GetQueueItem("queue")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, harness.QueueItemStateCancelling, queue.State)
			run, err = subagent.LoadPersistedRunFromDocuments(reopened, "root", "worker", legacy)
			require.NoError(t, err)
			require.Equal(t, "durable", run.Result)
		})
	}
}
