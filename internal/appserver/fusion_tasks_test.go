package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// Protect the escaped cancellation bug and the accepted-update/completion race.
// Provider gates, not timing, determine when each execution may finish.
func TestFusionTaskActiveUpdateAndReview(t *testing.T) {
	srv, client, calls := newFusionFixture(t)
	var created ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: created.Thread.ID, Prompt: "Research feature"}, nil)
	calls.next(t).response <- fusionTool("task", `{"message":"Research and report","block":false}`)
	a, b := calls.next(t), calls.next(t)
	var side *gatedCall
	for _, call := range []*gatedCall{a, b} {
		if strings.Contains(call.request.Messages[len(call.request.Messages)-1].Content, "Research and report") {
			side = call
		} else {
			call.response <- fusionReply("Research is running")
		}
	}
	if side == nil {
		t.Fatal("Side did not receive brief")
	}
	fusionAwait(t, srv, created.Thread.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	handler := srv.fusionDelegateHandler(created.Thread.ID)
	inspection, err := handler(context.Background(), "inspect", tools.FusionDelegateRequest{Action: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	view := inspection.(FusionTaskView)
	if view.State != session.FusionTaskRunning {
		t.Fatalf("running projection: %+v", view)
	}
	if _, err := handler(context.Background(), "bad-stop", tools.FusionDelegateRequest{Action: "stop", Message: "wrap up and report", Reason: "done"}); err == nil {
		t.Fatal("stop silently discarded a message")
	}
	update, err := handler(context.Background(), "update", tools.FusionDelegateRequest{Action: "update", TaskID: view.ID, Message: "Use current main and wrap up"})
	if err != nil {
		t.Fatal(err)
	}
	if update.(FusionTaskView).Revision != 2 {
		t.Fatal("accepted update did not advance requirements")
	}
	side.response <- fusionReply("Initial findings")
	next := calls.next(t)
	joined := ""
	for _, message := range next.request.Messages {
		joined += message.Content
	}
	if !strings.Contains(joined, "Use current main and wrap up") {
		t.Fatalf("accepted update was lost: %s", joined)
	}
	next.response <- fusionReply("Current main verified; latest requirements addressed")
	lead := calls.next(t)
	if !strings.Contains(lead.request.Messages[len(lead.request.Messages)-1].Content, "requirements revision 2") {
		t.Fatal("Lead received obsolete result")
	}
	lead.response <- fusionReply("Reviewing evidence")
	fusionAwait(t, srv, created.Thread.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	inspection, err = handler(context.Background(), "ready", tools.FusionDelegateRequest{Action: "inspect", TaskID: view.ID})
	if err != nil {
		t.Fatal(err)
	}
	view = inspection.(FusionTaskView)
	if view.State != session.FusionTaskAwaitingReview || view.Report == nil {
		t.Fatalf("result skipped review: %+v", view)
	}
	if _, err := handler(context.Background(), "stale-review", tools.FusionDelegateRequest{Action: "review", TaskID: view.ID, ReportID: view.Report.ID, Revision: 1, Verdict: "accept", Message: "reviewed"}); err == nil {
		t.Fatal("old requirements accepted")
	}
	// A later narrowing of Lead permissions must apply to review execution too.
	readOnly := config.PermissionModeReadOnly
	client.rpc(t, MethodConfigModelUpdate, ConfigModelUpdateParams{ThreadID: created.Thread.ID, PermissionMode: &readOnly}, nil)
	if _, err := handler(context.Background(), "narrow-review", tools.FusionDelegateRequest{Action: "review", TaskID: view.ID, ReportID: view.Report.ID, Revision: 2, Verdict: "request_changes", Message: "Recheck the findings without changing files", Block: new(bool)}); err != nil {
		t.Fatal(err)
	}
	recheck := calls.next(t)
	metadata, _, err := session.Find(srv.rt.SessionDir, view.SideID)
	if err != nil || metadata.PermissionMode != readOnly {
		t.Fatalf("review widened parent permissions: %s %v", metadata.PermissionMode, err)
	}
	recheck.response <- fusionReply("Readonly recheck complete")
	calls.next(t).response <- fusionReply("Checked latest recheck")
	fusionAwait(t, srv, created.Thread.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	inspection, err = handler(context.Background(), "rechecked", tools.FusionDelegateRequest{Action: "inspect", TaskID: view.ID})
	if err != nil {
		t.Fatal(err)
	}
	view = inspection.(FusionTaskView)
	accepted, err := handler(context.Background(), "accept", tools.FusionDelegateRequest{Action: "review", TaskID: view.ID, ReportID: view.Report.ID, Revision: 3, Verdict: "accept", Message: "Checked actual source and evidence"})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.(FusionTaskView).State != session.FusionTaskCompleted {
		t.Fatal("review did not complete task")
	}
	srv.recoverFusionInbox()
	select {
	case call := <-calls.calls:
		data, _ := json.Marshal(call.request)
		t.Fatalf("report delivered twice: %s", data)
	default:
	}
}

// A background handoff can finish either before or during a later blocking
// wait. Its report must reach the Lead through exactly one delivery path.
func TestFusionWaitClaimsBackgroundReport(t *testing.T) {
	srv, client, calls := newFusionFixture(t)
	var created ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: created.Thread.ID, Prompt: "Implement feature"}, nil)
	calls.next(t).response <- fusionTool("background-wait", `{"message":"Implement and verify","block":false}`)
	var side *gatedCall
	for range 2 {
		call := calls.next(t)
		isSide := false
		for _, message := range call.request.Messages {
			isSide = isSide || message.Cause == "fusion"
		}
		if isSide {
			side = call
		} else {
			call.response <- fusionReply("Independent review complete")
		}
	}
	if side == nil {
		t.Fatal("missing Sidekick request")
	}
	fusionAwait(t, srv, created.Thread.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	handler := srv.fusionDelegateHandler(created.Thread.ID)
	value, err := handler(context.Background(), "inspect", tools.FusionDelegateRequest{Action: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	task := value.(FusionTaskView)
	ctx := &fusionWaitingContext{Context: context.Background(), waiting: make(chan struct{})}
	done := make(chan struct{})
	var result any
	var waitErr error
	go func() {
		defer close(done)
		result, waitErr = handler(ctx, "wait", tools.FusionDelegateRequest{Action: "wait", TaskID: task.ID})
	}()
	select {
	case <-ctx.waiting:
	case <-time.After(gatedProviderTimeout):
		t.Fatal("wait did not start")
	}
	side.response <- fusionReply("Implementation evidence")
	select {
	case <-done:
	case <-time.After(gatedProviderTimeout):
		t.Fatal("wait missed completion")
	}
	if waitErr != nil || result.(FusionTaskView).Report == nil || result.(FusionTaskView).Report.Output != "Implementation evidence" {
		t.Fatalf("wait lost report: %+v %v", result, waitErr)
	}
	fusionAwait(t, srv, task.SideID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	srv.recoverFusionInbox()
	delivered, err := session.InboxHas(srv.rt.SessionDir, "fusion-task-result:"+task.ID+":"+result.(FusionTaskView).Report.ID)
	if err != nil || delivered {
		t.Fatalf("blocking report also entered the background inbox: %t %v", delivered, err)
	}
}

// Signal the actual wait select, so completion never relies on a timing delay.
type fusionWaitingContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *fusionWaitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

// A read-only assignment must not reserve writes, while an implementing Side
// must keep exclusive access. Both permissions are checked at tool execution.
func TestFusionReadOnlyAssignmentDoesNotBlockLeadWrites(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(readOnly), func(t *testing.T) {
			srv, client, calls := newFusionFixture(t)
			var created ThreadStartResult
			client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
			handler := srv.fusionDelegateHandler(created.Thread.ID)
			value, err := handler(context.Background(), "research", tools.FusionDelegateRequest{Message: "Inspect independent requirements", ReadOnly: &readOnly, Block: new(bool)})
			if err != nil {
				t.Fatal(err)
			}
			calls.next(t) // Keep the actual Side execution active.
			task := value.(FusionTaskView)
			lead, _, err := session.Find(srv.rt.SessionDir, created.Thread.ID)
			if err != nil {
				t.Fatal(err)
			}
			kit, err := tools.New(srv.rt.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			call := providers.ToolCall{ID: "write", Name: "write_file", Arguments: `{"path":"lead.txt","content":"independent change"}`}
			_, err = srv.fusionToolPolicy(lead, kit).Execute(context.Background(), call)
			if readOnly {
				if err != nil {
					t.Fatalf("read-only researcher reserved writes: %v", err)
				}
				content, err := os.ReadFile(filepath.Join(srv.rt.RootDir, "lead.txt"))
				if err != nil || string(content) != "independent change" {
					t.Fatalf("Lead write did not execute: %q %v", content, err)
				}
				side, _, err := session.Find(srv.rt.SessionDir, task.SideID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := srv.fusionToolPolicy(side, kit).Execute(context.Background(), call); err == nil {
					t.Fatal("read-only Side wrote to the shared workspace")
				}
			} else if err == nil {
				t.Fatal("Lead wrote while Side owned implementation")
			}
			client.rpc(t, MethodTurnInterrupt, TurnInterruptParams{ThreadID: created.Thread.ID}, nil)
		})
	}
}
