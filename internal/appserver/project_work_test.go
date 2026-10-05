//go:build project_agent

package appserver

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// The complete work contract must prevent root blocking, executor reports
// bypassing review, stale acceptance, duplicate dispatch on recovery, and
// stopped work resurrecting. Related executions retain the same session/CWD.
func TestProjectWorkDeliveryLifecycle(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	root := startProject(t, client, "Responsive project")
	act := func(actor, id string, r tools.ProjectWorkRequest) projectWorkView {
		t.Helper()
		v, err := srv.projectWork(context.Background(), actor, id, r)
		if err != nil {
			t.Fatal(err)
		}
		return v.(projectWorkView)
	}
	w := act(root.ID, "new-work", tools.ProjectWorkRequest{Operation: "create", Title: "Pagination", Brief: "Keep the API compatible", Acceptance: "Existing callers work", Authority: "Local edits and tests only"})
	leadCall := calls.next(t, "Keep the API compatible")
	if _, err := srv.projectSessionHandler(root.ID)(context.Background(), "no-wait", tools.ProjectSessionRequest{Action: "wait", SessionID: w.LeadID}); err == nil {
		t.Fatal("coordinator can block")
	}
	// Root remains independently usable while a technical model call is held.
	var turn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: root.ID, Prompt: "What is running?"}, &turn)
	rootCall := calls.next(t, "What is running?")
	if requestToolNames(rootCall.request)["bash"] || requestToolNames(rootCall.request)["write_file"] {
		t.Fatal("coordinator exposes implementation tools")
	}
	rootCall.response <- providersResponse("Pagination is under investigation.")
	w = act(w.LeadID, "execute", tools.ProjectWorkRequest{Operation: "execute", WorkID: w.ID, Revision: 1, Brief: "Implement compatible pagination; preserve existing interfaces"})
	executorCall := calls.next(t, "Implement compatible pagination")
	executorID := w.ExecutorID
	lead, _, _ := session.Find(rt.SessionDir, w.LeadID)
	executor, _, _ := session.Find(rt.SessionDir, executorID)
	if lead.CWD != executor.CWD || lead.CWD == rt.RootDir {
		t.Fatalf("workstream is not isolated and shared: %s %s", lead.CWD, executor.CWD)
	}
	leadCall.response <- providersResponse("Execution dispatched.")
	waitForThread(t, srv, w.LeadID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	w = act(executorID, "submit", tools.ProjectWorkRequest{Operation: "submit", WorkID: w.ID, Revision: 1, Summary: "Implemented pagination", Evidence: "go test ./... passed; artifact: test.log", CodeRef: "working-tree:pagination-v1"})
	if _, err := srv.projectWork(context.Background(), executorID, "self-review", tools.ProjectWorkRequest{Operation: "review", WorkID: w.ID, Revision: 1, Evidence: "self checked", CodeRef: w.CodeRef}); err == nil {
		t.Fatal("executor accepted itself")
	}
	executorCall.response <- providersResponse("Ready for technical review.")
	reviewCall := calls.next(t, "Ready for technical review")
	if pending, err := session.PendingInbox(rt.SessionDir, root.ID); err != nil || len(pending) != 0 {
		t.Fatalf("executor leaked to coordinator: %+v %v", pending, err)
	}
	w = act(w.LeadID, "accept", tools.ProjectWorkRequest{Operation: "review", WorkID: w.ID, Revision: 1, Summary: "Compatibility inspected", Evidence: "Reviewed API call sites and test.log", CodeRef: "working-tree:pagination-v1"})
	if w.Phase != "accepted" {
		t.Fatalf("acceptance = %+v", w)
	}
	reviewCall.response <- providersResponse("Technical review complete.")
	resultCall := calls.next(t, "Compatibility inspected")
	resultCall.response <- providersResponse("Reviewed changes are ready for local delivery.")
	w = act(root.ID, "delivered", tools.ProjectWorkRequest{Operation: "deliver", WorkID: w.ID, Revision: 1, Delivery: "Local isolated worktree; no commit or push"})
	if w.Phase != "delivered" {
		t.Fatalf("delivery not recorded: %+v", w)
	}

	w = act(root.ID, "correct", tools.ProjectWorkRequest{Operation: "update", WorkID: w.ID, Revision: 1, Brief: "Keep the API compatible; leave database indexes alone", Acceptance: "Existing callers work; indexes unchanged", Authority: "Local edits and tests only"})
	if w.Revision != 2 || w.Review != "" || w.Phase != "planning" {
		t.Fatalf("correction retained acceptance: %+v", w)
	}
	if _, err := srv.projectWork(context.Background(), root.ID, "stale-delivery", tools.ProjectWorkRequest{Operation: "deliver", WorkID: w.ID, Revision: 1, Delivery: "local worktree"}); err == nil {
		t.Fatal("stale delivery accepted")
	}
	w = act(root.ID, "stop-work", tools.ProjectWorkRequest{Operation: "stop", WorkID: w.ID, Revision: 2})
	srv.recoverProjectInbox()
	reopened, err := session.ReadProjectWork(rt.SessionDir, w.ID)
	if err != nil || reopened.Phase != "stopped" {
		t.Fatalf("stopped work revived: %+v %v", reopened, err)
	}
	if reopened.ExecutorID != executorID {
		t.Fatal("persistent executor lost")
	}
}

func TestProjectWorkStopsFromMemberAndKeepsWriteLease(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	root := startProject(t, client, "Stop control")
	value, err := srv.projectWork(context.Background(), root.ID, "new", tools.ProjectWorkRequest{Operation: "create", Brief: "Review stop behavior", Acceptance: "No resurrection", Authority: "Local only"})
	if err != nil {
		t.Fatal(err)
	}
	w := value.(projectWorkView)
	held := calls.next(t, "Review stop behavior")
	member, _, _ := session.Find(rt.SessionDir, w.LeadID)
	release, err := srv.acquireProjectWorkTool(member, providers.ToolCall{Name: "write_file"}, rt.Toolkit)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := srv.acquireProjectWorkTool(member, providers.ToolCall{Name: "write_file"}, rt.Toolkit); err == nil {
		t.Fatal("concurrent writer acquired lease")
	}
	var result OKResult
	client.rpc(t, MethodTurnInterrupt, TurnInterruptParams{ThreadID: w.LeadID}, &result)
	current, err := session.ReadProjectWork(rt.SessionDir, w.ID)
	if err != nil || current.Phase != "stopped" {
		t.Fatalf("member stop did not stop work: %+v %v", current, err)
	}
	lease, acquired, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, "project-work-write:"+w.ID)
	if lease != nil {
		defer lease.Release()
	}
	if err != nil || acquired {
		t.Fatalf("stop released live writer: %v %v", acquired, err)
	}
	held.response <- providersResponse("Late completion")
	srv.recoverProjectInbox()
	current, err = session.ReadProjectWork(rt.SessionDir, w.ID)
	if err != nil || current.Phase != "stopped" {
		t.Fatalf("late result revived work: %+v %v", current, err)
	}
}

func TestProjectWorkRecoveryDoesNotInventSuccess(t *testing.T) {
	srv, client, _, rt := newProjectFixture(t)
	root := startProject(t, client, "Interrupted work")
	leadID := session.NewID()
	_, err := session.CreateInitialized(rt.SessionDir, session.Session{ID: leadID, Source: projectSessionSource, ParentID: root.ID, ProjectRole: "technical_lead", CWD: rt.RootDir, WorkspaceID: rt.WorkspaceID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.ChangeControl(rt.SessionDir, leadID, root.ID, session.ControlActive, 0); err != nil {
		t.Fatal(err)
	}
	_, err = session.SaveProjectWork(rt.SessionDir, session.ProjectWork{ID: "interrupted", ProjectID: root.ID, LeadID: leadID, Revision: 1, Phase: "planning", LeadInput: &session.ProjectWorkInput{ClientID: "crash-input", Revision: 1}}, 0, "prepare-crash")
	if err != nil {
		t.Fatal(err)
	}
	if err = session.AppendHistoryRecord(rt.SessionDir, leadID, session.HistoryRecord{Role: "user", ClientID: "crash-input", Content: "Accepted before crash"}); err != nil {
		t.Fatal(err)
	}
	if err = srv.recoverProjectWork(root.ID); err != nil {
		t.Fatal(err)
	}
	w, err := session.ReadProjectWork(rt.SessionDir, "interrupted")
	if err != nil || w.Phase != "blocked" || w.Review != "" || w.Delivery != "" {
		t.Fatalf("recovered unfinished work: %+v %v", w, err)
	}
}

func TestProjectWorkDirectUserCorrectionInvalidatesAcceptanceOnce(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	root := startProject(t, client, "Direct correction")
	value, err := srv.projectWork(context.Background(), root.ID, "create-direct", tools.ProjectWorkRequest{Operation: "create", Brief: "Preserve API", Acceptance: "Callers still work", Authority: "Local only"})
	if err != nil {
		t.Fatal(err)
	}
	w := value.(projectWorkView)
	calls.next(t, "Preserve API")
	message := providers.ChatMessage{Role: "user", Origin: "user", ClientID: "direct-correction", Content: "Leave the database index alone"}
	srv.noticeProjectUserMessage(srv.thread(w.LeadID), message)
	srv.noticeProjectUserMessage(srv.thread(w.LeadID), message)
	current, err := session.ReadProjectWork(rt.SessionDir, w.ID)
	if err != nil || current.Revision != 2 || current.Review != "" || current.LeadInput == nil {
		t.Fatalf("direct intervention: %+v %v", current, err)
	}
	member, _, _ := session.Find(rt.SessionDir, w.LeadID)
	if _, err := srv.acquireProjectWorkTool(member, providers.ToolCall{Name: "write_file"}, rt.Toolkit); err == nil {
		t.Fatal("old turn can write before consuming the correction")
	}
	oldTurn, err := srv.currentProjectWorkTurn(w.LeadID)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []TurnStatus{TurnStatusCompleted, TurnStatusFailed, TurnStatusInterrupted} {
		if _, err := srv.recordWorkTurn(srv.thread(w.LeadID), Turn{ID: oldTurn, Status: status}); err != nil {
			t.Fatal(err)
		}
		after, err := session.ReadProjectWork(rt.SessionDir, w.ID)
		if err != nil || after.Version != current.Version || after.Phase != "planning" {
			t.Fatalf("late %s changed the new assignment: %+v %v", status, after, err)
		}
	}
}

func TestProjectWorkDelegatesAuthorizedDelivery(t *testing.T) {
	srv, client, calls, _ := newProjectFixture(t)
	root := startProject(t, client, "Deliver outcome")
	act := func(actor, id string, r tools.ProjectWorkRequest) projectWorkView {
		t.Helper()
		v, err := srv.projectWork(context.Background(), actor, id, r)
		if err != nil {
			t.Fatal(err)
		}
		return v.(projectWorkView)
	}
	w := act(root.ID, "create", tools.ProjectWorkRequest{Operation: "create", Brief: "Prepare a local artifact", Acceptance: "Artifact is readable", Authority: "Write the artifact in this worktree"})
	lead := calls.next(t, "Prepare a local artifact")
	w = act(w.LeadID, "review", tools.ProjectWorkRequest{Operation: "review", WorkID: w.ID, Revision: 1, Summary: "Artifact verified", Evidence: "Read the generated artifact", CodeRef: "artifact:sha256-example"})
	lead.response <- providersResponse("Reviewed.")
	rootReport := calls.next(t, "Artifact verified")
	rootReport.response <- providersResponse("Delivering the requested artifact.")
	waitForThread(t, srv, w.LeadID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	w = act(root.ID, "deliver-request", tools.ProjectWorkRequest{Operation: "dispatch_delivery", WorkID: w.ID, Revision: 1, Brief: "Place the verified artifact at the requested local destination; no external publication"})
	delivery := calls.next(t, "Place the verified artifact")
	if w.Phase != "delivering" || w.Review == "" {
		t.Fatalf("delivery lost its acceptance: %+v", w)
	}
	w = act(w.LeadID, "complete-delivery", tools.ProjectWorkRequest{Operation: "complete_delivery", WorkID: w.ID, Revision: 1, Delivery: "Local artifact path", CodeRef: "artifact:sha256-example"})
	if w.Phase != "delivered" {
		t.Fatalf("delivery incomplete: %+v", w)
	}
	delivery.response <- providersResponse("Local artifact delivered.")
}

func TestProjectWorkConcurrentRecoveryKeepsOneDispatch(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	root := startProject(t, client, "Recover outbox")
	project, _ := srv.projectCoordinator(root.ID)
	w := session.ProjectWork{ID: session.NewID(), ProjectID: root.ID, LeadID: session.NewID(), Revision: 1, Phase: "planning", Workspace: "worktree", Title: "Recovered work", Brief: "Replay-safe work", Acceptance: "One dispatch", Authority: "Local only", LeadModel: runtimeSelectionFromSession(project)}
	w.LeadInput = srv.workInput(w, "recoverable-input", root.ID, workContract(w))
	var err error
	w, err = session.SaveProjectWork(rt.SessionDir, w, 0, "saved-before-creation")
	if err != nil {
		t.Fatal(err)
	}
	lease, acquired, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, "project-work-reconcile:"+w.ID)
	if err != nil || !acquired {
		t.Fatalf("hold reconciliation: %v %v", acquired, err)
	}
	defer lease.Release()

	observer := New(rt, &lockedBuffer{})
	t.Cleanup(observer.Close)
	results := make(chan error, 2)
	for _, host := range []*Server{srv, observer} {
		go func(host *Server) { results <- host.reconcileProjectWork(w) }(host)
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	srv.kickProjectInboxDrain(root.ID)

	calls.next(t, "Replay-safe work")
	members, err := srv.projectSessions(root.ID)
	if err != nil || len(members) != 1 || members[0].ID != w.LeadID {
		t.Fatalf("duplicate/replaced work member: %+v %v", members, err)
	}
	history, err := loadChatMessages(rt.SessionDir, w.LeadID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range history {
		if message.ClientID == w.LeadInput.ClientID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("dispatch consumed %d times", count)
	}
	calls.assertIdle(t)
}

func TestProjectWorkOutboxStaysWithOwningWorkspace(t *testing.T) {
	srv, client, _, rt := newProjectFixture(t)
	root := startProject(t, client, "Owned workspace")
	w := session.ProjectWork{ID: "owned-work", ProjectID: root.ID, LeadID: session.NewID(), Revision: 1, Phase: "planning", Workspace: "shared"}
	if _, err := session.SaveProjectWork(rt.SessionDir, w, 0, "owned-create"); err != nil {
		t.Fatal(err)
	}
	otherRuntime := *rt
	otherRuntime.RootDir = t.TempDir()
	otherRuntime.WorkspaceID = "other-workspace"
	other := New(&otherRuntime, &lockedBuffer{})
	t.Cleanup(other.Close)
	if other.drainProjectInboxPass(root.ID) {
		t.Fatal("another workspace scheduled retries for this work")
	}
	pending, err := session.PendingProjectWork(rt.SessionDir, root.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("another workspace settled the outbox: %+v %v", pending, err)
	}
	if _, found, err := session.Find(rt.SessionDir, w.LeadID); err != nil || found {
		t.Fatalf("another workspace created the member: %v %v", found, err)
	}
	_ = srv
}
