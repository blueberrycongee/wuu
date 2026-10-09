package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
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
