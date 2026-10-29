package appserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// A worker admitted between an idle check and retirement must not be canceled
// merely because the runtime is being released.
func TestRuntimeRetirementLetsAdmittedWorkerFinish(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	worker := newBlockingStreamClient("finished naturally")
	control, err := agentcontrol.New(agentcontrol.Config{AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}), Client: worker, DefaultModel: "fake-model", ParentRepo: rt.RootDir, WorktreeRoot: filepath.Join(rt.RootDir, "worktrees"), SessionID: "retirement-race", WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
		return noopToolExecutor{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { control.StopAll(); control.Close() })
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	rt.Toolkit = kit
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	dispatchPayload(t, srv, "start", MethodThreadStart, ThreadStartParams{})
	start := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"])
	th := srv.thread(start.Thread.ID)
	thread, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	if thread.AgentControl != nil {
		thread.AgentControl.Close()
	}
	thread.AgentControl = control
	settled := thread.Toolkit.FinalizeToolResult(providers.ToolCall{ID: "archive", Name: "synthetic"}, toolresult.FromText(strings.Repeat("evidence", 10000)))
	var first map[string]any
	if err := json.Unmarshal([]byte(settled.TextProjection()), &first); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(first["continuation"].(map[string]any)["next"])
	if err != nil {
		t.Fatal(err)
	}
	call := providers.ToolCall{ID: "recover", Name: "recover_tool_result", Arguments: string(args)}
	if _, err := thread.Toolkit.Execute(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	// Exercise the same initial predicate used by settings/model/reload callers.
	if threadRuntimeHasOutstandingWork("retirement-race", thread) {
		t.Fatal("unexpected initial work")
	}
	spawned, err := control.Spawn(context.Background(), agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "race", Description: "admitted during retirement", Prompt: "wait", Isolation: string(agentcontrol.IsolationInplace)})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-worker.started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	// This is the production cleanup called after the caller detaches its runtime.
	th.mu.Lock()
	detached := detachThreadRuntimeLocked(th)
	th.mu.Unlock()
	srv.releaseDetachedThreadRuntime(detached)
	if page, err := thread.Toolkit.Execute(context.Background(), call); err != nil || !strings.Contains(page, "evidence") {
		t.Fatalf("archive lost while worker active: %q %v", page, err)
	}
	close(worker.release)
	waitForAgentStatus(t, control, spawned.AgentID, subagent.StatusCompleted)
	deadline := time.After(5 * time.Second)
	for {
		_, err = thread.Toolkit.Execute(context.Background(), call)
		if err != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("archive retained after worker finished")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
