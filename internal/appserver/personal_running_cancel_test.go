//go:build !windows

package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

func waitForCancellationEvidence(t *testing.T, label string, check func() bool) {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if check() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timeout: %s", label)
		case <-tick.C:
		}
	}
}

func TestPersonalRunningWorkerCancellationStopsNativeEffect(t *testing.T) {
	for _, mode := range []string{"worker-context", "exact-scope", "foreign-session", "stale-revision"} {
		t.Run(mode, func(t *testing.T) { testPersonalRunningCancellation(t, mode) })
	}
}

func testPersonalRunningCancellation(t *testing.T, mode string) {
	f := newPersonalBoundaryFixture(t, nil)
	f.setReviewMode(false)
	worker := "diagnostic-worker"
	_, err := f.rt.Sessions.CreateInitialized(session.Session{ID: worker, ParentID: f.id, AgentID: "main", UserSpaceID: f.rt.UserSpaceID, ProductDomain: "personal", Owner: "plugin:personal", Visibility: "plugin", CWD: f.rt.RootDir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pluginsettings.UpdateState(f.rt.DataRoot, f.rt.RootDir, "plugin:bundled:personal", pluginsettings.ScopeUser, func(values map[string]string) error {
		facts := personalExecutionFacts{SessionID: worker, AgentID: "main", MainSessionID: f.id, Active: true, Task: &personalTaskFacts{ID: "diagnostic-task", Revision: 2, RunID: "diagnostic-turn", Active: true, Goal: "Run bounded marker command", SourceSessionID: f.id, SourceMessageSeq: f.sourceSeq}}
		raw, _ := json.Marshal(facts)
		values["fixture.facts."+worker] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	kit, err := f.rt.Toolkit.CloneForRoot(f.rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionID(worker)
	ctx, cancel := context.WithCancel(f.owner)
	defer cancel()
	gate := filepath.Join(f.rt.RootDir, "release")
	defer os.WriteFile(gate, []byte("release"), 0600)
	command := `sh -c 'printf "%s" $$ > child-pid; while [ ! -f release ]; do sleep 0.05; done; printf child_after_cancel >> effects' & printf "%s" $$ > pid; printf started > started; wait`
	args, _ := json.Marshal(map[string]any{"command": command})
	done := make(chan error, 1)
	go func() {
		_, err := kit.Execute(ctx, providers.ToolCall{ID: "cancel-running", Name: "bash", Arguments: string(args)})
		done <- err
	}()
	var r personalreview.Record
	waitForCancellationEvidence(t, "pending approval", func() bool {
		select {
		case e := <-done:
			t.Fatalf("worker ended before approval: %v", e)
		default:
		}
		rs, e := f.host.actionsForTest(t).engine.Records(context.Background(), f.rt.UserSpaceID)
		if e != nil {
			return false
		}
		for _, v := range rs {
			if v.Status == personalreview.AwaitingConfirmation {
				r = v
				return true
			}
		}
		return false
	})
	if _, err = f.host.personal.Call(f.owner, "review.resolve", f.command(r), ""); err != nil {
		t.Fatal(err)
	}
	waitForCancellationEvidence(t, "native shell started", func() bool { _, e := os.Stat(filepath.Join(f.rt.RootDir, "started")); return e == nil })
	pid, _ := os.ReadFile(filepath.Join(f.rt.RootDir, "pid"))
	t.Logf("native process started pid=%s action=%s", pid, r.Action.ID)
	waitForCancellationEvidence(t, "child started", func() bool {
		b, e := os.ReadFile(filepath.Join(f.rt.RootDir, "child-pid"))
		return e == nil && len(b) > 0
	})
	childBytes, _ := os.ReadFile(filepath.Join(f.rt.RootDir, "child-pid"))
	childPID, _ := strconv.Atoi(string(childBytes))
	if mode == "foreign-session" || mode == "stale-revision" {
		wrong := r.Action.Scope
		if mode == "foreign-session" {
			wrong.SessionID = "another-worker"
		} else {
			wrong.RequirementsRevision--
		}
		if err := f.host.actionsForTest(t).cancelExecution(f.owner, wrong.SessionID, wrong); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Kill(childPID, 0); err != nil {
			t.Fatalf("unrelated cancellation stopped child: %v", err)
		}
		t.Logf("%s cancellation left child %d running", mode, childPID)
	}
	if mode == "worker-context" {
		cancel()
	} else {
		if err := f.host.actionsForTest(t).cancelExecution(f.owner, worker, r.Action.Scope); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("cancelled exact execution while shell and child were blocked on release marker")
	// A bounded observation window distinguishes prompt cancellation from a task
	// that can still write after cancellation; release always lets cleanup finish.
	returned := false
	select {
	case err := <-done:
		t.Logf("worker returned before release: %v", err)
		returned = true
	case <-time.After(time.Second):
		t.Log("worker still blocked one second after cancellation")
	}
	waitForCancellationEvidence(t, "parent exit", func() bool { n, _ := strconv.Atoi(string(pid)); return syscall.Kill(n, 0) == syscall.ESRCH })
	waitForCancellationEvidence(t, "child exit", func() bool { return syscall.Kill(childPID, 0) == syscall.ESRCH })
	PID, parseErr := strconv.Atoi(string(pid))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	aliveErr := syscall.Kill(PID, 0)
	t.Logf("process check after cancellation pid=%d signal0=%v", PID, aliveErr)
	if err = os.WriteFile(gate, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	if !returned {
		select {
		case err := <-done:
			t.Logf("worker returned after release: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("worker failed to drain")
		}
	}
	if err = f.host.actionsForTest(t).drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, readErr := os.ReadFile(filepath.Join(f.rt.RootDir, "effects"))
	record := f.current(r.Action.Key())
	t.Logf("final action status=%s post_cancel_content=%q read_error=%v", record.Status, data, readErr)
	if record.Status == personalreview.Succeeded {
		t.Error("cancelled command was recorded as succeeded")
	}
	if len(data) > 0 {
		t.Error("native command wrote after original worker cancellation")
	}
}
