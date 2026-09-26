package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// The failure cases these tests guard:
//   - the coordinator can edit files or widen its permission mode;
//   - a managed session's result is lost, delivered twice, or re-delivered
//     after a restart;
//   - a candidate is applied over conflicting workspace changes, or applied
//     twice;
//   - two turns of one session leave overlapping candidates that can each be
//     decided, so applying them in turn conflicts or records a change twice;
//   - an applied or published change is offered again;
//   - turns the user runs while holding control leave no candidate, or are
//     reported to the coordinator, including after a return and a restart;
//   - clients are not told when a candidate is frozen or decided;
//   - the coordinator keeps instructing a session the user took over, or is
//     not told when the user takes it over or returns it.

func newProjectFixture(t *testing.T) (*Server, *rpcClient, *projectCalls, *runtime.Session) {
	t.Helper()
	rt := newTestRuntime(t, &fakeClient{})
	initAppserverGitRepo(t, rt.RootDir)
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	rt.Toolkit = kit
	rt.WorkspaceID = "workspace-one"
	provider := newGatedProvider(16)
	rt.StreamRunner.Client = providers.AdaptStreamClient(provider)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	return srv, &rpcClient{server: srv, out: out}, &projectCalls{provider: provider}, rt
}

// projectCalls routes concurrent model calls to the conversation whose user
// messages contain a marker, so each conversation is answered in order.
type projectCalls struct {
	provider *gatedProvider
	stash    []*gatedCall
}

func (calls *projectCalls) next(t *testing.T, marker string) *gatedCall {
	t.Helper()
	for index, call := range calls.stash {
		if requestHasUserText(call.request, marker) {
			calls.stash = append(calls.stash[:index], calls.stash[index+1:]...)
			return call
		}
	}
	for {
		call := calls.provider.next(t)
		if requestHasUserText(call.request, marker) {
			return call
		}
		calls.stash = append(calls.stash, call)
	}
}

func (calls *projectCalls) assertIdle(t *testing.T) {
	t.Helper()
	if len(calls.stash) > 0 {
		t.Fatalf("unanswered model calls: %d", len(calls.stash))
	}
	select {
	case call := <-calls.provider.calls:
		t.Fatalf("unexpected model call: %+v", lastUserRequestMessage(call.request))
	default:
	}
}

func requestHasUserText(request providers.ChatRequest, marker string) bool {
	for _, message := range request.Messages {
		if message.Role == "user" && strings.Contains(message.Content, marker) {
			return true
		}
	}
	return false
}

func lastUserRequestMessage(request providers.ChatRequest) providers.ChatMessage {
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role == "user" {
			return request.Messages[index]
		}
	}
	return providers.ChatMessage{}
}

func requestToolNames(request providers.ChatRequest) map[string]bool {
	names := make(map[string]bool, len(request.Tools))
	for _, tool := range request.Tools {
		names[tool.Name] = true
	}
	return names
}

func toolCallResponse(id, name, arguments string) providers.ChatResponse {
	return providers.ChatResponse{
		ToolCalls:    []providers.ToolCall{{ID: id, Name: name, Arguments: arguments}},
		StopReason:   "tool_use",
		FinishReason: providers.FinishReasonToolCalls,
	}
}

func startProject(t *testing.T, client *rpcClient, name string) Thread {
	t.Helper()
	var started ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Project: &ThreadProjectParams{Name: name}}, &started)
	return started.Thread
}

func waitForThread(t *testing.T, srv *Server, id string, ready func(Thread) bool) Thread {
	t.Helper()
	deadline := time.Now().Add(gatedProviderTimeout)
	for time.Now().Before(deadline) {
		if th := srv.thread(id); th != nil {
			th.mu.Lock()
			snapshot := th.snapshotLocked()
			th.mu.Unlock()
			if ready(snapshot) {
				return snapshot
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("thread %s did not reach the expected state", id)
	return Thread{}
}

func projectManagedSessions(t *testing.T, client *rpcClient, projectID string) []Thread {
	t.Helper()
	var listed ThreadListResult
	client.rpc(t, MethodThreadList, ThreadListParams{SummaryOnly: true}, &listed)
	var managed []Thread
	for _, thread := range listed.Threads {
		if thread.ProjectID == projectID {
			managed = append(managed, thread)
		}
	}
	return managed
}

func deliveredClientIDs(t *testing.T, rt *runtime.Session, threadID, prefix string) []string {
	t.Helper()
	history, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, message := range history {
		if strings.HasPrefix(message.ClientID, prefix) {
			ids = append(ids, message.ClientID)
		}
	}
	return ids
}

func TestProjectDelegatesAndDeliversCandidateOnce(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	coordinator := startProject(t, client, "Catalog search")
	if coordinator.Source != projectSource || coordinator.Title != "Catalog search" || coordinator.PermissionMode != config.PermissionModeReadOnly {
		t.Fatalf("project coordinator = %+v", coordinator)
	}
	var turn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: coordinator.ID, Prompt: "Plan catalog pagination"}, &turn)

	plan := calls.next(t, "Plan catalog pagination")
	visible := requestToolNames(plan.request)
	for _, writer := range []string{"write_file", "edit_file", "apply_patch", "wuu_browser"} {
		if visible[writer] {
			t.Fatalf("coordinator can call %s: %v", writer, visible)
		}
	}
	if !visible["session"] {
		t.Fatalf("coordinator cannot manage sessions: %v", visible)
	}
	plan.response <- toolCallResponse("create-pagination", "session", `{"action":"create","title":"Pagination","prompt":"Implement page-size 50 in search.go"}`)

	brief := calls.next(t, "Implement page-size 50")
	brief.response <- toolCallResponse("write-search", "write_file", `{"path":"search.go","content":"package search\n\nconst PageSize = 50\n"}`)
	calls.next(t, "Plan catalog pagination").response <- providersResponse("Started a session for pagination.")
	calls.next(t, "Implement page-size 50").response <- providersResponse("Set PageSize to 50 in search.go.")

	wake := calls.next(t, "Plan catalog pagination")
	managed := projectManagedSessions(t, client, coordinator.ID)
	if len(managed) != 1 || managed[0].Source != projectSessionSource || managed[0].Worktree == nil {
		t.Fatalf("managed sessions = %+v", managed)
	}
	worker := waitForThread(t, srv, managed[0].ID, func(thread Thread) bool { return thread.LatestCompletedTurnID != "" })
	if worker.SessionControl == nil || worker.SessionControl.ManagerID != coordinator.ID || worker.SessionControl.State != session.ControlActive {
		t.Fatalf("worker control = %+v", worker.SessionControl)
	}
	result := lastUserRequestMessage(wake.request)
	if result.ClientID != "project-result:"+worker.ID+":"+worker.LatestCompletedTurnID || result.RelatedSessionID != worker.ID ||
		result.PresentationKind != "session_message" || !strings.Contains(result.Content, "Set PageSize to 50 in search.go.") || !strings.Contains(result.Content, "search.go") {
		t.Fatalf("delivered result = %+v", result)
	}
	wake.response <- providersResponse("Pagination is ready for your review.")

	var listed ProjectCandidateResult
	client.rpc(t, MethodProjectCandidate, ProjectCandidateParams{ProjectID: coordinator.ID, Action: "list"}, &listed)
	if len(listed.Candidates) != 1 || listed.Candidates[0].SessionID != worker.ID || !slices.Equal(listed.Candidates[0].ChangedFiles, []string{"search.go"}) || listed.Candidates[0].Disposition != "" {
		t.Fatalf("candidates = %+v", listed.Candidates)
	}
	candidate := listed.Candidates[0]
	apply := ProjectCandidateParams{SessionID: candidate.SessionID, TurnID: candidate.TurnID, Action: "apply"}

	// A conflicting workspace change leaves the workspace and the decision untouched.
	target := filepath.Join(rt.RootDir, "search.go")
	conflict := []byte("package search\n\nconst PageSize = 10\n")
	if err := os.WriteFile(target, conflict, 0o600); err != nil {
		t.Fatal(err)
	}
	if failure := client.call(t, MethodProjectCandidate, apply, nil); failure == nil {
		t.Fatal("candidate applied over a conflicting change")
	}
	if data, _ := os.ReadFile(target); string(data) != string(conflict) {
		t.Fatalf("conflicting apply changed the workspace: %q", data)
	}
	client.rpc(t, MethodProjectCandidate, ProjectCandidateParams{ProjectID: coordinator.ID, Action: "list"}, &listed)
	if listed.Candidates[0].Disposition != "" {
		t.Fatalf("failed apply recorded a decision: %+v", listed.Candidates[0])
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}

	var applied ProjectCandidateResult
	client.rpc(t, MethodProjectCandidate, apply, &applied)
	if applied.Candidate == nil || applied.Candidate.Disposition != "applied" {
		t.Fatalf("applied candidate = %+v", applied.Candidate)
	}
	if data, _ := os.ReadFile(target); string(data) != "package search\n\nconst PageSize = 50\n" {
		t.Fatalf("workspace after apply = %q", data)
	}
	if failure := client.call(t, MethodProjectCandidate, apply, nil); failure == nil {
		t.Fatal("candidate was applied twice")
	}
	decided := calls.next(t, "Plan catalog pagination")
	if notice := lastUserRequestMessage(decided.request); notice.ClientID != "project-candidate:"+worker.ID+":"+candidate.TurnID+":applied" {
		t.Fatalf("decision notice = %+v", notice)
	}
	decided.response <- providersResponse("Applied.")
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.Status == ThreadStatusIdle })

	srv.Close()
	reopened := New(rt, &lockedBuffer{})
	t.Cleanup(reopened.Close)
	reopened.recoverProjectInbox()
	if ids := deliveredClientIDs(t, rt, coordinator.ID, "project-result:"+worker.ID); len(ids) != 1 {
		t.Fatalf("result deliveries after restart = %v", ids)
	}
	calls.assertIdle(t)
}

func projectCandidates(t *testing.T, client *rpcClient, projectID string) []ProjectCandidate {
	t.Helper()
	var listed ProjectCandidateResult
	client.rpc(t, MethodProjectCandidate, ProjectCandidateParams{ProjectID: projectID, Action: "list"}, &listed)
	return listed.Candidates
}

func TestProjectProposalsSupersedeAndStartFromDelivery(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	coordinator := startProject(t, client, "Search")
	var turn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: coordinator.ID, Prompt: "Plan search paging"}, &turn)
	calls.next(t, "Plan search paging").response <- toolCallResponse("create-paging", "session", `{"action":"create","title":"Paging","prompt":"Implement paging in search.go"}`)
	calls.next(t, "Implement paging").response <- toolCallResponse("write-search", "write_file", `{"path":"search.go","content":"package search\n\nconst PageSize = 50\n"}`)
	calls.next(t, "Plan search paging").response <- providersResponse("Started.")
	calls.next(t, "Implement paging").response <- providersResponse("Paged search.")
	worker := projectManagedSessions(t, client, coordinator.ID)[0]

	// The coordinator corrects the session before the user reviews its first turn.
	calls.next(t, "Plan search paging").response <- toolCallResponse("send-docs", "session", `{"action":"send","session_id":"`+worker.ID+`","prompt":"Also document paging in api.md"}`)
	calls.next(t, "document paging").response <- toolCallResponse("write-api", "write_file", `{"path":"api.md","content":"Results are paged.\n"}`)
	calls.next(t, "Plan search paging").response <- providersResponse("Sent.")
	calls.next(t, "document paging").response <- providersResponse("Documented.")
	calls.next(t, "Plan search paging").response <- providersResponse("Ready for review.")
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.PendingCandidates == 1 && thread.Status == ThreadStatusIdle })
	waitForThread(t, srv, worker.ID, func(thread Thread) bool { return thread.PendingCandidates == 1 })

	candidates := projectCandidates(t, client, coordinator.ID)
	if len(candidates) != 2 || candidates[0].Disposition != session.CandidateSuperseded || candidates[1].Disposition != "" ||
		!slices.Equal(candidates[1].ChangedFiles, []string{"api.md", "search.go"}) {
		t.Fatalf("candidates after two turns = %+v", candidates)
	}
	older, latest := candidates[0], candidates[1]
	if failure := client.call(t, MethodProjectCandidate, ProjectCandidateParams{SessionID: worker.ID, TurnID: older.TurnID, Action: "apply"}, nil); failure == nil {
		t.Fatal("a superseded candidate was applied")
	}
	var published ProjectCandidateResult
	client.rpc(t, MethodProjectCandidate, ProjectCandidateParams{SessionID: worker.ID, TurnID: latest.TurnID, Action: "publish", URL: "https://example.test/pull/1"}, &published)
	if published.Candidate == nil || published.Candidate.Disposition != session.CandidatePublished || published.Candidate.URL != "https://example.test/pull/1" {
		t.Fatalf("published candidate = %+v", published.Candidate)
	}
	if failure := client.call(t, MethodProjectCandidate, ProjectCandidateParams{SessionID: worker.ID, TurnID: latest.TurnID, Action: "apply"}, nil); failure == nil {
		t.Fatal("a published candidate was applied as well")
	}
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.PendingCandidates == 0 })

	// The next proposal holds only what was not published.
	notice := calls.next(t, "Plan search paging")
	if last := lastUserRequestMessage(notice.request); last.ClientID != "project-candidate:"+worker.ID+":"+latest.TurnID+":published" {
		t.Fatalf("publish notice = %+v", last)
	}
	notice.response <- toolCallResponse("send-test", "session", `{"action":"send","session_id":"`+worker.ID+`","prompt":"Add a paging test"}`)
	calls.next(t, "Add a paging test").response <- toolCallResponse("write-test", "write_file", `{"path":"search_test.go","content":"package search\n"}`)
	calls.next(t, "Plan search paging").response <- providersResponse("Sent.")
	calls.next(t, "Add a paging test").response <- providersResponse("Tested.")
	calls.next(t, "Plan search paging").response <- providersResponse("Test ready.")
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.PendingCandidates == 1 && thread.Status == ThreadStatusIdle })
	candidates = projectCandidates(t, client, coordinator.ID)
	third := candidates[len(candidates)-1]
	if len(candidates) != 3 || third.BaseRevision != latest.Revision || !slices.Equal(third.ChangedFiles, []string{"search_test.go"}) {
		t.Fatalf("candidates after publishing = %+v", candidates)
	}
	client.rpc(t, MethodProjectCandidate, ProjectCandidateParams{SessionID: worker.ID, TurnID: third.TurnID, Action: "apply"}, nil)
	if _, err := os.Stat(filepath.Join(rt.RootDir, "search_test.go")); err != nil {
		t.Fatalf("applied candidate is missing from the workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rt.RootDir, "search.go")); !os.IsNotExist(err) {
		t.Fatalf("apply re-delivered the published change: %v", err)
	}
	calls.next(t, "Plan search paging").response <- providersResponse("Applied.")
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.Status == ThreadStatusIdle })
	calls.assertIdle(t)
}

func TestProjectFreezesUserTurnsWithoutReportingThem(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	coordinator := startProject(t, client, "Notes")
	var turn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: coordinator.ID, Prompt: "Plan the notes"}, &turn)
	calls.next(t, "Plan the notes").response <- toolCallResponse("create-notes", "session", `{"action":"create","title":"Notes","prompt":"Draft notes.md"}`)
	calls.next(t, "Draft notes.md").response <- toolCallResponse("write-notes", "write_file", `{"path":"notes.md","content":"draft\n"}`)
	calls.next(t, "Plan the notes").response <- providersResponse("Started.")
	calls.next(t, "Draft notes.md").response <- providersResponse("Drafted.")
	calls.next(t, "Plan the notes").response <- providersResponse("Draft ready.")
	worker := projectManagedSessions(t, client, coordinator.ID)[0]
	worker = waitForThread(t, srv, worker.ID, func(thread Thread) bool { return thread.PendingCandidates == 1 })

	var userTurn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: worker.ID, Prompt: "I will rewrite the notes"}, &userTurn)
	taken := waitForThread(t, srv, worker.ID, func(thread Thread) bool {
		return thread.SessionControl != nil && thread.SessionControl.State == session.ControlTakenOver
	})
	calls.next(t, "Plan the notes").response <- providersResponse("Understood.")
	calls.next(t, "rewrite the notes").response <- toolCallResponse("rewrite-notes", "write_file", `{"path":"notes.md","content":"final\n"}`)
	calls.next(t, "rewrite the notes").response <- providersResponse("Rewritten.")
	waitForThread(t, srv, worker.ID, func(thread Thread) bool { return thread.LatestCompletedTurnID == userTurn.Turn.ID })

	// The user's turn is frozen for review but belongs to the user.
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool {
		candidates := projectCandidates(t, client, coordinator.ID)
		return thread.PendingCandidates == 1 && len(candidates) == 2 && candidates[1].TurnID == userTurn.Turn.ID
	})
	client.rpc(t, "thread/control/return", map[string]any{"thread_id": worker.ID, "revision": taken.SessionControl.Revision}, nil)
	calls.next(t, "Plan the notes").response <- providersResponse("I will inspect the notes.")
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.Status == ThreadStatusIdle })

	srv.Close()
	reopened := New(rt, &lockedBuffer{})
	t.Cleanup(reopened.Close)
	reopened.recoverProjectInbox()
	if ids := deliveredClientIDs(t, rt, coordinator.ID, projectResultClientID(worker.ID, userTurn.Turn.ID)); len(ids) != 0 {
		t.Fatalf("a user-controlled turn was reported after return and restart: %v", ids)
	}
	calls.assertIdle(t)
}

func TestProjectTakeoverPausesDelegationUntilReturned(t *testing.T) {
	srv, client, calls, _ := newProjectFixture(t)
	coordinator := startProject(t, client, "Release")
	var turn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: coordinator.ID, Prompt: "Plan the release note"}, &turn)
	calls.next(t, "Plan the release note").response <- toolCallResponse("create-notes", "session", `{"action":"create","title":"Notes","prompt":"Draft release note text","workspace":"shared"}`)
	calls.next(t, "Draft release note text").response <- providersResponse("Drafted the notes.")
	calls.next(t, "Plan the release note").response <- providersResponse("Started.")
	calls.next(t, "Plan the release note").response <- providersResponse("The draft is ready.")
	worker := projectManagedSessions(t, client, coordinator.ID)[0]
	worker = waitForThread(t, srv, worker.ID, func(thread Thread) bool { return thread.LatestCompletedTurnID != "" })

	var userTurn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: worker.ID, Prompt: "I will finish the notes"}, &userTurn)
	taken := waitForThread(t, srv, worker.ID, func(thread Thread) bool {
		return thread.SessionControl != nil && thread.SessionControl.State == session.ControlTakenOver
	})
	notified := calls.next(t, "Plan the release note")
	if notice := lastUserRequestMessage(notified.request); notice.ClientID != "project-control:"+worker.ID+":"+jsonNumber(taken.SessionControl.Revision) || notice.RelatedSessionID != worker.ID {
		t.Fatalf("takeover notice = %+v", notice)
	}
	notified.response <- providersResponse("Understood.")
	calls.next(t, "Draft release note text").response <- providersResponse("Finished by the user.")
	waitForThread(t, srv, worker.ID, func(thread Thread) bool { return thread.LatestCompletedTurnID == userTurn.Turn.ID })

	handler := srv.projectSessionHandler(coordinator.ID)
	if _, err := handler(context.Background(), "send-after-takeover", tools.ProjectSessionRequest{Action: "send", SessionID: worker.ID, Prompt: "Keep going"}); !errors.Is(err, session.ErrControlChanged) {
		t.Fatalf("send to a taken-over session = %v", err)
	}
	srv.recoverProjectInbox()
	if ids := deliveredClientIDs(t, srv.rt, coordinator.ID, "project-result:"+worker.ID+":"+userTurn.Turn.ID); len(ids) != 0 {
		t.Fatalf("user-controlled turn was reported to the coordinator: %v", ids)
	}

	var returned struct {
		Control *ThreadSessionControl `json:"control"`
	}
	client.rpc(t, "thread/control/return", map[string]any{"thread_id": worker.ID, "revision": taken.SessionControl.Revision}, &returned)
	if returned.Control == nil || returned.Control.State != session.ControlActive || returned.Control.ManagerName != "Release" {
		t.Fatalf("returned control = %+v", returned.Control)
	}
	back := calls.next(t, "Plan the release note")
	if notice := lastUserRequestMessage(back.request); notice.ClientID != "project-control:"+worker.ID+":"+jsonNumber(returned.Control.Revision) {
		t.Fatalf("return notice = %+v", notice)
	}
	back.response <- providersResponse("I will continue.")
	if _, err := handler(context.Background(), "send-after-return", tools.ProjectSessionRequest{Action: "send", SessionID: worker.ID, Prompt: "Add the known issues"}); err != nil {
		t.Fatalf("send after return: %v", err)
	}
	calls.next(t, "Add the known issues").response <- providersResponse("Added known issues.")
	calls.next(t, "Plan the release note").response <- providersResponse("Done.")
}

func TestProjectCoordinatorStaysReadOnly(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	coordinator := startProject(t, client, "Guarded")
	standard := config.PermissionModeStandard
	if failure := client.call(t, MethodConfigModelUpdate, ConfigModelUpdateParams{ThreadID: coordinator.ID, Model: coordinator.Model, PermissionMode: &standard}, nil); failure == nil {
		t.Fatal("coordinator permission mode was widened")
	}
	var turn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: coordinator.ID, Prompt: "Write the file yourself"}, &turn)
	calls.next(t, "Write the file yourself").response <- toolCallResponse("write-direct", "write_file", `{"path":"direct.txt","content":"x"}`)
	denied := calls.next(t, "Write the file yourself")
	last := denied.request.Messages[len(denied.request.Messages)-1]
	if last.Role != "tool" || last.ToolCallID != "write-direct" {
		t.Fatalf("coordinator write was not rejected as a tool result: %+v", last)
	}
	denied.response <- providersResponse("I cannot edit files here.")
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.LatestCompletedTurnID == turn.Turn.ID })
	if _, err := os.Stat(filepath.Join(rt.RootDir, "direct.txt")); !os.IsNotExist(err) {
		t.Fatalf("coordinator wrote a file: %v", err)
	}
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
