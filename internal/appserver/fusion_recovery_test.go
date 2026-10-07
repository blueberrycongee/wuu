package appserver

import (
	"context"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// Recovery must settle consumed work after both orderly shutdown and a crash,
// preserve execution owned by another host, deliver one report, and free the
// task slot. Side progress must reach the Lead while the Lead is still running.
func TestFusionConsumedTaskRecovery(t *testing.T) {
	srv, client, calls := newFusionFixture(t)
	var created ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
	lead, _, err := session.Find(srv.rt.SessionDir, created.Thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	side, control, err := srv.ensureFusionSide(context.Background(), lead, false)
	if err != nil {
		t.Fatal(err)
	}
	task := session.FusionTask{ID: "crashed-task", LeadID: lead.ID, SideID: side.ID, LeadTurnID: "lead-turn"}
	dispatch := session.FusionDispatch{ClientID: "crashed-input", LeadID: lead.ID, SideID: side.ID, LeadTurnID: task.LeadTurnID, TaskID: task.ID, Kind: "delegate", Delivery: "waiting"}
	task, err = session.AdmitFusionTask(srv.rt.SessionDir, task, dispatch, session.InboxMessage{ClientID: dispatch.ClientID, SessionID: side.ID, RelatedSessionID: lead.ID, Cause: "fusion", Content: "Run the task", Wake: true, Controls: []session.Control{control}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendHistoryRecord(srv.rt.SessionDir, side.ID, session.HistoryRecord{Role: "user", Content: "Run the task", ClientID: dispatch.ClientID, Origin: "host", Cause: "fusion", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := session.MarkFusionTaskRunning(srv.rt.SessionDir, task.ID, 1); err != nil {
		t.Fatal(err)
	}
	// A different live host owns execution: recovery must leave its task alone.
	lease, acquired, err := session.TryAcquireThreadExecutionLease(srv.rt.SessionDir, side.ID)
	if err != nil || !acquired {
		t.Fatalf("acquire execution: %t %v", acquired, err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	srv.recoverFusionInbox()
	before, _, err := session.ReadFusionTask(srv.rt.SessionDir, task.ID)
	if err != nil || before.State != session.FusionTaskRunning || before.ReportID != "" {
		t.Fatalf("live owner was settled: %+v %v", before, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	srv.recoverFusionInbox()
	view, err := srv.fusionTaskLiveView(task)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := session.PendingInbox(srv.rt.SessionDir, side.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != session.FusionTaskFailed || view.Report == nil || view.Report.Status != TurnStatusInterrupted || view.Report.Error == nil {
		t.Fatalf("orphan was not reported: %+v", view)
	}
	report := calls.next(t)
	report.response <- fusionReply("Interruption acknowledged")
	fusionAwait(t, srv, lead.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	srv.recoverFusionInbox()
	select {
	case <-calls.calls:
		t.Fatal("recovery replayed work or delivered a duplicate report")
	default:
	}
	t.Logf("after recovery: state=%s execution=%s report=%v pending=%d", view.State, view.ExecutionState, view.Report, len(pending))
	waited, err := srv.fusionDelegateHandler(lead.ID)(context.Background(), "wait", tools.FusionDelegateRequest{Action: "wait", TaskID: task.ID, TimeoutMS: 1})
	if err != nil {
		t.Fatal(err)
	}
	if waited.(FusionTaskView).TimedOut || waited.(FusionTaskView).Report == nil {
		t.Fatalf("wait lost recovery report: %+v", waited)
	}
	_, err = srv.fusionDelegateHandler(lead.ID)(context.Background(), "new-task", tools.FusionDelegateRequest{Message: "New task", Block: new(bool)})
	if err != nil {
		t.Fatalf("recovered task blocks new delegation: %v", err)
	}
	calls.next(t)
	if len(pending) != 0 {
		t.Fatalf("consumed input was requeued: %+v", pending)
	}
}

func TestFusionOrderlyShutdownRecovery(t *testing.T) {
	srv, client, calls := newFusionFixture(t)
	var created ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: created.Thread.ID, Prompt: "Investigate"}, nil)
	calls.next(t)
	delegated, err := srv.fusionDelegateHandler(created.Thread.ID)(context.Background(), "task", tools.FusionDelegateRequest{Message: "Investigate repository", Block: new(bool)})
	if err != nil {
		t.Fatal(err)
	}
	calls.next(t)
	task := delegated.(FusionTaskView).FusionTask
	srv.Close()
	recovered := New(srv.rt, &lockedBuffer{})
	defer recovered.Close()
	recovered.recoverFusionInbox()
	view, err := recovered.fusionTaskLiveView(task)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("after orderly shutdown and restart: state=%s execution=%s report=%v", view.State, view.ExecutionState, view.Report)
	waited, err := recovered.fusionDelegateHandler(created.Thread.ID)(context.Background(), "wait", tools.FusionDelegateRequest{Action: "wait", TaskID: task.ID, TimeoutMS: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("wait state=%s wait_status=%s report=%v", waited.(FusionTaskView).State, waited.(FusionTaskView).WaitStatus, waited.(FusionTaskView).Report)
	if view.State != session.FusionTaskFailed || view.Report == nil || view.Report.Status != TurnStatusInterrupted {
		t.Fatalf("interrupted task was not settled: %+v", view)
	}
	if waited.(FusionTaskView).TimedOut || waited.(FusionTaskView).Report == nil {
		t.Fatalf("wait did not return interrupted report: %+v", waited)
	}
}

func TestFusionSideProgressPublication(t *testing.T) {
	srv, client, calls := newFusionFixture(t)
	var created ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: created.Thread.ID, Prompt: "Investigate"}, nil)
	calls.next(t) // Hold Lead so its finalization cannot publish Side progress.
	_, err := srv.fusionDelegateHandler(created.Thread.ID)(context.Background(), "task", tools.FusionDelegateRequest{Message: "Investigate repository", Block: new(bool)})
	if err != nil {
		t.Fatal(err)
	}
	side := calls.next(t)
	side.response <- providers.ChatResponse{Content: "Side progress marker", ToolCalls: []providers.ToolCall{{ID: "read", Name: "list_files", Arguments: `{}`}}, FinishReason: providers.FinishReasonToolCalls, StopReason: "tool_use"}
	calls.next(t) // Side reached the next provider request after publishing its events.
	tasks, err := session.ListFusionTasks(srv.rt.SessionDir, created.Thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	live, err := srv.fusionTaskLiveView(tasks[0])
	if err != nil {
		t.Fatal(err)
	}
	th := srv.thread(created.Thread.ID)
	th.mu.Lock()
	view := th.snapshotLocked()
	th.mu.Unlock()
	cached := view.Turns[0].Fusion.Tasks[0].Progress
	t.Logf("direct inspection progress=%+v; published Lead progress=%+v; side ParentID=%q", live.Progress, cached, srv.thread(tasks[0].SideID).ParentID)
	if live.Progress == nil || live.Progress.Summary == "" || cached == nil || cached.Summary != live.Progress.Summary {
		t.Error("Side progress never reaches Lead projection")
	}
	client.rpc(t, MethodTurnInterrupt, TurnInterruptParams{ThreadID: created.Thread.ID}, nil)
}
