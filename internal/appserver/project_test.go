//go:build project_agent

package appserver

import (
	"context"
	"encoding/json"
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
//   - the project lead cannot execute ordinary work, or bypasses the user-selected permission mode;
//   - a managed session's result is lost, delivered twice, or re-delivered
//     after a restart;
//   - a worktree session's changes reach the workspace before anyone delivers
//     them;
//   - turns the user runs in a member session leave no report;
//   - a message the user writes into a managed session silently takes it
//     from the project, or reaches the coordinator without the user's words;
//   - stopping a member removes membership, preserves stale admitted input,
//     or prevents a later explicit instruction without returning control;
//   - an intervention or interrupted result wakes an idle coordinator,
//     including during recovery;
//   - an ordinary conversation cannot be added to a project, or a project
//     takes a conversation of another workspace, another manager's session,
//     or a coordinator;
//   - removing a session from a project leaves the coordinator able to
//     instruct it;
//   - host-generated delegation is mistaken for user authorization or loses
//     attribution when no plugin runtime is loaded.

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

func TestProjectDelegatesAndReportsResultOnce(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	coordinator := startProject(t, client, "Catalog search")
	if coordinator.Source != projectSource || coordinator.Title != "Catalog search" || coordinator.PermissionMode != config.PermissionModeStandard {
		t.Fatalf("project coordinator = %+v", coordinator)
	}
	var turn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: coordinator.ID, Prompt: "Plan catalog pagination"}, &turn)

	plan := calls.next(t, "Plan catalog pagination")
	visible := requestToolNames(plan.request)
	if !visible["session"] {
		t.Fatalf("coordinator cannot manage sessions: %v", visible)
	}
	plan.response <- toolCallResponse("create-pagination", "session", `{"action":"create","title":"Pagination","prompt":"Implement page-size 50 in search.go"}`)

	brief := calls.next(t, "Implement page-size 50")
	if message := lastUserRequestMessage(brief.request); message.Origin != "host" || nativeReviewHumanUser(message) {
		t.Fatalf("delegation must be attributed to the host, not user authorization: %+v", message)
	}
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
		result.Origin != "host" || result.PresentationKind != "session_message" || !strings.Contains(result.Content, "Set PageSize to 50 in search.go.") || !strings.Contains(result.Content, "search.go") {
		t.Fatalf("delivered result = %+v", result)
	}
	wake.response <- providersResponse("Pagination is done.")
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.Status == ThreadStatusIdle })
	// The change stays in the session's worktree until someone delivers it.
	if _, err := os.Stat(filepath.Join(worker.Worktree.Path, "search.go")); err != nil {
		t.Fatalf("worktree change is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rt.RootDir, "search.go")); !os.IsNotExist(err) {
		t.Fatalf("worktree change reached the workspace undelivered: %v", err)
	}

	srv.Close()
	reopened := New(rt, &lockedBuffer{})
	t.Cleanup(reopened.Close)
	reopened.recoverProjectInbox()
	if ids := deliveredClientIDs(t, rt, coordinator.ID, "project-result:"+worker.ID); len(ids) != 1 {
		t.Fatalf("result deliveries after restart = %v", ids)
	}
	calls.assertIdle(t)
}

// poll returns a stashed or arriving call carrying marker within wait.
func (calls *projectCalls) poll(marker string, wait time.Duration) *gatedCall {
	for index, call := range calls.stash {
		if requestHasUserText(call.request, marker) {
			calls.stash = append(calls.stash[:index], calls.stash[index+1:]...)
			return call
		}
	}
	select {
	case call := <-calls.provider.calls:
		if requestHasUserText(call.request, marker) {
			return call
		}
		calls.stash = append(calls.stash, call)
	case <-time.After(wait):
	}
	return nil
}

// settleCoordinator answers the coordinator until it is idle with every
// wanted input delivered. Input steered into a starting turn joins its first
// request or its next step, so the number of calls is not fixed.
func settleCoordinator(t *testing.T, srv *Server, calls *projectCalls, coordinatorID, marker, answer string, clientIDs ...string) {
	t.Helper()
	deadline := time.Now().Add(gatedProviderTimeout)
	for time.Now().Before(deadline) {
		if call := calls.poll(marker, 20*time.Millisecond); call != nil {
			call.response <- providersResponse(answer)
			continue
		}
		th := srv.thread(coordinatorID)
		th.mu.Lock()
		idle := !th.running
		th.mu.Unlock()
		if idle && hasClientIDs(t, srv.rt, coordinatorID, clientIDs) {
			return
		}
	}
	t.Fatalf("coordinator did not settle with %v", clientIDs)
}

func hasClientIDs(t *testing.T, rt *runtime.Session, threadID string, clientIDs []string) bool {
	t.Helper()
	delivered := deliveredClientIDs(t, rt, threadID, "")
	for _, id := range clientIDs {
		if !slices.Contains(delivered, id) {
			return false
		}
	}
	return true
}

func projectControlClientID(sessionID string, revision int64) string {
	return "project-control:" + sessionID + ":" + jsonNumber(revision)
}

func TestProjectReportsUserTurns(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	coordinator := startProject(t, client, "Notes")
	var turn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: coordinator.ID, Prompt: "Plan the notes"}, &turn)
	calls.next(t, "Plan the notes").response <- toolCallResponse("create-notes", "session", `{"action":"create","title":"Notes","prompt":"Draft notes.md"}`)
	calls.next(t, "Draft notes.md").response <- toolCallResponse("write-notes", "write_file", `{"path":"notes.md","content":"draft\n"}`)
	calls.next(t, "Plan the notes").response <- providersResponse("Started.")
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.Status == ThreadStatusIdle })
	calls.next(t, "Draft notes.md").response <- providersResponse("Drafted.")
	calls.next(t, "Plan the notes").response <- providersResponse("Draft ready.")
	worker := projectManagedSessions(t, client, coordinator.ID)[0]
	worker = waitForThread(t, srv, worker.ID, func(thread Thread) bool {
		return thread.LatestCompletedTurnID != "" && thread.SessionControl != nil
	})

	var userTurn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: worker.ID, Prompt: "I will rewrite the notes"}, &userTurn)
	calls.next(t, "rewrite the notes").response <- toolCallResponse("rewrite-notes", "write_file", `{"path":"notes.md","content":"final\n"}`)
	calls.next(t, "rewrite the notes").response <- providersResponse("Rewritten.")
	waitForThread(t, srv, worker.ID, func(thread Thread) bool { return thread.LatestCompletedTurnID == userTurn.Turn.ID })
	settleCoordinator(t, srv, calls, coordinator.ID, "Plan the notes", "I will inspect the notes.",
		projectResultClientID(worker.ID, userTurn.Turn.ID))

	srv.Close()
	reopened := New(rt, &lockedBuffer{})
	t.Cleanup(reopened.Close)
	reopened.recoverProjectInbox()
	if ids := deliveredClientIDs(t, rt, coordinator.ID, projectResultClientID(worker.ID, userTurn.Turn.ID)); len(ids) != 1 {
		t.Fatalf("user result was lost or duplicated on restart: %v", ids)
	}
	calls.assertIdle(t)
}

func TestProjectUserMessagesSteerManagedSessions(t *testing.T) {
	srv, client, calls, _ := newProjectFixture(t)
	coordinator := startProject(t, client, "Release")
	var turn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: coordinator.ID, Prompt: "Plan the release note"}, &turn)
	calls.next(t, "Plan the release note").response <- toolCallResponse("create-notes", "session", `{"action":"create","title":"Notes","prompt":"Draft release note text","workspace":"shared"}`)
	calls.next(t, "Plan the release note").response <- providersResponse("Started.")
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.Status == ThreadStatusIdle })
	calls.next(t, "Draft release note text").response <- providersResponse("Drafted the notes.")
	calls.next(t, "Plan the release note").response <- providersResponse("The draft is ready.")
	worker := projectManagedSessions(t, client, coordinator.ID)[0]
	worker = waitForThread(t, srv, worker.ID, func(thread Thread) bool { return thread.LatestCompletedTurnID != "" })

	var userTurn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: worker.ID, Prompt: "Also mention the migration"}, &userTurn)
	held := calls.next(t, "mention the migration")
	srv.drainSessionInbox(coordinator.ID)
	pending, err := session.PendingInbox(srv.rt.SessionDir, coordinator.ID)
	if err != nil || len(pending) != 1 || pending[0].Wake || pending[0].Cause != "project_user_message" || !strings.Contains(pending[0].Content, "Also mention the migration") {
		t.Fatalf("direct user notice must persist without waking: %+v, %v", pending, err)
	}
	calls.assertIdle(t)
	held.response <- providersResponse("Mentioned the migration.")
	joined := waitForThread(t, srv, worker.ID, func(thread Thread) bool { return thread.LatestCompletedTurnID == userTurn.Turn.ID })
	if joined.SessionControl == nil || joined.SessionControl.State != session.ControlActive || joined.SessionControl.Revision != worker.SessionControl.Revision {
		t.Fatalf("a user message changed the session's control: %+v", joined.SessionControl)
	}
	result := calls.next(t, "Plan the release note")
	if last := lastUserRequestMessage(result.request); last.ClientID != projectResultClientID(worker.ID, userTurn.Turn.ID) ||
		last.Cause != projectCauseResult || !strings.Contains(last.Content, "The user wrote to this session") || !strings.Contains(last.Content, "Also mention the migration") {
		t.Fatalf("result of a turn the user joined = %+v", last)
	}
	result.response <- providersResponse("Noted.")
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.Status == ThreadStatusIdle })
	// The project still manages the session.
	handler := srv.projectSessionHandler(coordinator.ID)
	if _, err := handler(context.Background(), "send-issues", tools.ProjectSessionRequest{Action: "send", SessionID: worker.ID, Prompt: "Add the known issues"}); err != nil {
		t.Fatalf("send after the user wrote to the session: %v", err)
	}
	calls.next(t, "Add the known issues").response <- providersResponse("Added known issues.")
	calls.next(t, "Plan the release note").response <- providersResponse("Done.")
}

func TestProjectStopKeepsMembershipWithoutWakingOrRevivingOldInput(t *testing.T) {
	srv, client, calls, _ := newProjectFixture(t)
	coordinator := startProject(t, client, "Release")
	var turn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: coordinator.ID, Prompt: "Plan the release note"}, &turn)
	calls.next(t, "Plan the release note").response <- toolCallResponse("create-notes", "session", `{"action":"create","title":"Notes","prompt":"Draft release note text","workspace":"shared"}`)
	calls.next(t, "Plan the release note").response <- providersResponse("Started.")
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.Status == ThreadStatusIdle })
	calls.next(t, "Draft release note text").response <- providersResponse("Drafted the notes.")
	calls.next(t, "Plan the release note").response <- providersResponse("The draft is ready.")
	worker := projectManagedSessions(t, client, coordinator.ID)[0]
	worker = waitForThread(t, srv, worker.ID, func(thread Thread) bool { return thread.LatestCompletedTurnID != "" })
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.Status == ThreadStatusIdle })

	var userTurn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: worker.ID, Prompt: "I will finish the notes"}, &userTurn)
	calls.next(t, "I will finish the notes")
	handler := srv.projectSessionHandler(coordinator.ID)
	if _, err := handler(context.Background(), "old-advice", tools.ProjectSessionRequest{Action: "message", SessionID: worker.ID, Prompt: "Stale automatic advice", Wake: true}); err != nil {
		t.Fatal(err)
	}
	srv.drainSessionInbox(worker.ID)
	client.rpc(t, MethodTurnInterrupt, TurnInterruptParams{ThreadID: worker.ID}, nil)
	stopped := waitForThread(t, srv, worker.ID, func(thread Thread) bool {
		return thread.Status == ThreadStatusIdle && thread.LatestCompletedTurnID == userTurn.Turn.ID
	})
	if stopped.SessionControl == nil || stopped.SessionControl.State != session.ControlActive || stopped.SessionControl.Revision <= worker.SessionControl.Revision {
		t.Fatalf("stop must retain membership and advance the input fence: %+v", stopped.SessionControl)
	}
	if _, _, _, err := srv.projectActor(worker.ID); err != nil {
		t.Fatalf("stopped member cannot collaborate: %v", err)
	}
	// Recovery must neither wake the lead nor replay an old automatic input.
	srv.recoverProjectInbox()
	srv.drainSessionInbox(worker.ID)
	calls.assertIdle(t)
	if ids := deliveredClientIDs(t, srv.rt, coordinator.ID, projectResultClientID(worker.ID, userTurn.Turn.ID)); len(ids) != 0 {
		t.Fatalf("interrupted result woke idle coordinator: %v", ids)
	}
	if ids := deliveredClientIDs(t, srv.rt, worker.ID, "project-message:"+coordinator.ID+":old-advice"); len(ids) != 0 {
		t.Fatalf("stopped work consumed stale input: %v", ids)
	}
	pending, err := session.PendingInbox(srv.rt.SessionDir, coordinator.ID)
	if err != nil || len(pending) < 2 {
		t.Fatalf("intervention/result not retained: %+v, %v", pending, err)
	}
	for _, message := range pending {
		if message.Wake {
			t.Fatalf("intervention woke lead: %+v", message)
		}
	}
	if _, err := handler(context.Background(), "send-after-stop", tools.ProjectSessionRequest{Action: "send", SessionID: worker.ID, Prompt: "Add the known issues"}); err != nil {
		t.Fatalf("send after stop: %v", err)
	}
	calls.next(t, "Add the known issues").response <- providersResponse("Added known issues.")
	calls.next(t, "Plan the release note").response <- providersResponse("Done.")
}

func TestProjectAdoptsAndReleasesConversations(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	coordinator := startProject(t, client, "Docs")
	var started ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{}, &started)
	conversation := started.Thread
	var turn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: conversation.ID, Prompt: "Summarize the README"}, &turn)
	calls.next(t, "Summarize the README").response <- providersResponse("The README explains setup.")
	waitForThread(t, srv, conversation.ID, func(thread Thread) bool { return thread.LatestCompletedTurnID == turn.Turn.ID })

	adopt := func(sessionID string) *ResponseError {
		return client.call(t, MethodProjectSession, ProjectSessionParams{Action: "adopt", ProjectID: coordinator.ID, SessionID: sessionID}, nil)
	}
	if failure := adopt(coordinator.ID); failure == nil {
		t.Fatal("a project adopted its own coordinator")
	}
	var other ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{}, &other)
	if _, err := session.SetWorkspaceID(rt.SessionDir, other.Thread.ID, "workspace-two"); err != nil {
		t.Fatal(err)
	}
	if failure := adopt(other.Thread.ID); failure == nil {
		t.Fatal("a project adopted a conversation of another workspace")
	}
	var managed ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{}, &managed)
	if _, err := session.ChangeControl(rt.SessionDir, managed.Thread.ID, "plugin:reviewer", session.ControlActive, 0); err != nil {
		t.Fatal(err)
	}
	if failure := adopt(managed.Thread.ID); failure == nil {
		t.Fatal("a project took a session another manager controls")
	}

	var adopted ProjectSessionResult
	client.rpc(t, MethodProjectSession, ProjectSessionParams{Action: "adopt", ProjectID: coordinator.ID, SessionID: conversation.ID}, &adopted)
	if adopted.Thread.ProjectID != coordinator.ID || adopted.Thread.Source != projectSessionSource ||
		adopted.Thread.SessionControl == nil || adopted.Thread.SessionControl.ManagerID != coordinator.ID || adopted.Thread.SessionControl.State != session.ControlActive {
		t.Fatalf("adopted conversation = %+v", adopted.Thread)
	}
	notice := calls.next(t, "added the conversation")
	if last := lastUserRequestMessage(notice.request); last.Cause != projectCauseAdopted || last.RelatedSessionID != conversation.ID ||
		!strings.Contains(last.Content, "The README explains setup.") {
		t.Fatalf("adoption notice = %+v", last)
	}
	notice.response <- providersResponse("I will build on the summary.")
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.Status == ThreadStatusIdle })
	handler := srv.projectSessionHandler(coordinator.ID)
	if _, err := handler(context.Background(), "send-adopted", tools.ProjectSessionRequest{Action: "send", SessionID: conversation.ID, Prompt: "List the setup steps"}); err != nil {
		t.Fatalf("send to an adopted conversation: %v", err)
	}
	calls.next(t, "List the setup steps").response <- providersResponse("Install, then run make dev.")
	result := calls.next(t, "added the conversation")
	if last := lastUserRequestMessage(result.request); last.Cause != projectCauseResult || !strings.Contains(last.Content, "run make dev") {
		t.Fatalf("adopted conversation result = %+v", last)
	}
	result.response <- providersResponse("Steps listed.")
	waitForThread(t, srv, coordinator.ID, func(thread Thread) bool { return thread.Status == ThreadStatusIdle })

	var released ProjectSessionResult
	client.rpc(t, MethodProjectSession, ProjectSessionParams{Action: "release", ProjectID: coordinator.ID, SessionID: conversation.ID}, &released)
	if released.Thread.ProjectID != "" || released.Thread.Source != "" || released.Thread.SessionControl != nil {
		t.Fatalf("released conversation = %+v", released.Thread)
	}
	if _, err := handler(context.Background(), "send-released", tools.ProjectSessionRequest{Action: "send", SessionID: conversation.ID, Prompt: "Keep going"}); err == nil {
		t.Fatal("the coordinator instructed a released conversation")
	}
	srv.drainSessionInbox(coordinator.ID)
	calls.assertIdle(t)
}

func TestProjectLeadUsesOrdinarySessionPermissions(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	lead := startProject(t, client, "Direct work")
	var turn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: lead.ID, Prompt: "Write a small change"}, &turn)
	calls.next(t, "Write a small change").response <- toolCallResponse("write-direct", "write_file", `{"path":"direct.txt","content":"done"}`)
	calls.next(t, "Write a small change").response <- providersResponse("Done.")
	waitForThread(t, srv, lead.ID, func(thread Thread) bool { return thread.LatestCompletedTurnID == turn.Turn.ID })
	if data, err := os.ReadFile(filepath.Join(rt.RootDir, "direct.txt")); err != nil || string(data) != "done" {
		t.Fatalf("lead's ordinary file edit = %q, %v", data, err)
	}
	var restricted ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Project: &ThreadProjectParams{Name: "Read-only project"}, PermissionMode: config.PermissionModeReadOnly}, &restricted)
	lead = restricted.Thread
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: lead.ID, Prompt: "Try a restricted change"}, &turn)
	calls.next(t, "Try a restricted change").response <- toolCallResponse("write-restricted", "write_file", `{"path":"restricted.txt","content":"no"}`)
	calls.next(t, "Try a restricted change").response <- providersResponse("Read-only.")
	waitForThread(t, srv, lead.ID, func(thread Thread) bool { return thread.LatestCompletedTurnID == turn.Turn.ID })
	if _, err := os.Stat(filepath.Join(rt.RootDir, "restricted.txt")); !os.IsNotExist(err) {
		t.Fatalf("lead bypassed read-only permissions: %v", err)
	}
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// Team contracts: the side session survives reload and is reused, workers use
// the same ordinary session runtime, and peer messages cannot cross a project
// or bypass a human control fence (including queued messages after a return).
func TestProjectSideAndWorkersReuseOrdinarySessions(t *testing.T) {
	// Both workspace modes must preserve team roles, result routing and reload
	// identity; shared sessions must not acquire a worktree dependency.
	for _, workspace := range []string{"shared", "worktree"} {
		t.Run(workspace, func(t *testing.T) {
			srv, client, calls, rt := newProjectFixture(t)
			lead := startProject(t, client, "Team")
			var turn TurnStartResult
			client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: lead.ID, Prompt: "Build the team feature"}, &turn)
			calls.next(t, "Build the team feature").response <- toolCallResponse("create-side", "session", `{"action":"create","role":"side","title":"Implementation","prompt":"Implement the team feature","workspace":"`+workspace+`"}`)
			sideCall := calls.next(t, "Implement the team feature")
			if !requestToolNames(sideCall.request)["session"] {
				t.Fatal("side has no session communication tool")
			}
			sideCall.response <- providersResponse("Implementation ready.")
			side := projectManagedSessions(t, client, lead.ID)[0]
			if side.ProjectRole != "side" {
				t.Fatalf("listed side role = %q", side.ProjectRole)
			}
			settleCoordinator(t, srv, calls, lead.ID, "Build the team feature", "Waiting for review.", projectResultClientID(side.ID, waitForThread(t, srv, side.ID, func(th Thread) bool { return th.LatestCompletedTurnID != "" }).LatestCompletedTurnID))

			handler := srv.projectSessionHandler(lead.ID)
			reused, err := handler(context.Background(), "reuse-side", tools.ProjectSessionRequest{Action: "create", Role: "side", Prompt: "Continue the implementation"})
			if err != nil {
				t.Fatal(err)
			}
			if reused.(projectSessionView).SessionID != side.ID {
				t.Fatalf("created a second side: %+v", reused)
			}
			if len(projectManagedSessions(t, client, lead.ID)) != 1 {
				t.Fatal("side was duplicated")
			}
			calls.assertIdle(t)

			workerView, err := srv.projectSessionHandler(side.ID)(context.Background(), "create-verifier", tools.ProjectSessionRequest{Action: "create", Role: "worker", Prompt: "Verify the team feature", Workspace: workspace})
			if err != nil {
				t.Fatal(err)
			}
			workerID := workerView.(projectSessionView).SessionID
			workerCall := calls.next(t, "Verify the team feature")
			if !requestToolNames(workerCall.request)["session"] {
				t.Fatal("worker has no session communication tool")
			}
			if msg := lastUserRequestMessage(workerCall.request); msg.RelatedSessionID != side.ID {
				t.Fatalf("worker lost its delegator: %+v", msg)
			}
			workerCall.response <- providersResponse("Verified.")
			sideResult := calls.next(t, "Implement the team feature")
			if msg := lastUserRequestMessage(sideResult.request); msg.RelatedSessionID != workerID || msg.Cause != projectCauseResult {
				t.Fatalf("side did not receive worker completion: %+v", msg)
			}
			sideResult.response <- providersResponse("Worker verified implementation.")
			side = waitForThread(t, srv, side.ID, func(th Thread) bool {
				return th.LatestCompletedTurnID != side.LatestCompletedTurnID && th.Status == ThreadStatusIdle
			})
			worker := waitForThread(t, srv, workerID, func(th Thread) bool { return th.LatestCompletedTurnID != "" })
			settleCoordinator(t, srv, calls, lead.ID, "Build the team feature", "Verified.", projectResultClientID(worker.ID, worker.LatestCompletedTurnID), projectResultClientID(side.ID, side.LatestCompletedTurnID))
			if worker.ParentID != "" || worker.ProjectID != lead.ID || worker.ProjectRole != "worker" {
				t.Fatalf("worker is not an ordinary project session: %+v", worker)
			}

			srv.Close()
			reopened := New(rt, &lockedBuffer{})
			t.Cleanup(reopened.Close)
			reused, err = reopened.projectSessionHandler(lead.ID)(context.Background(), "reuse-after-restart", tools.ProjectSessionRequest{Action: "create", Role: "side", Prompt: "Continue"})
			if err != nil || reused.(projectSessionView).SessionID != side.ID {
				t.Fatalf("reloaded side = %+v, %v", reused, err)
			}
			metadata, found, err := session.Find(rt.SessionDir, side.ID)
			if err != nil || !found || metadata.ProjectRole != "side" {
				t.Fatalf("persisted side = %+v, %v", metadata, err)
			}
			calls.assertIdle(t)
		})
	}
}

func TestProjectPeerMessagesRespectMembershipAndStopFence(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	lead := startProject(t, client, "Peers")
	var turn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: lead.ID, Prompt: "Coordinate peer work"}, &turn)
	calls.next(t, "Coordinate peer work").response <- providersResponse("Ready.")
	waitForThread(t, srv, lead.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	create := func(id, prompt string) Thread {
		t.Helper()
		view, err := srv.projectSessionHandler(lead.ID)(context.Background(), id, tools.ProjectSessionRequest{Action: "create", Prompt: prompt})
		if err != nil {
			t.Fatal(err)
		}
		calls.next(t, prompt).response <- providersResponse("Ready.")
		th := waitForThread(t, srv, view.(projectSessionView).SessionID, func(th Thread) bool { return th.LatestCompletedTurnID != "" })
		settleCoordinator(t, srv, calls, lead.ID, "Coordinate peer work", "Ready.", projectResultClientID(th.ID, th.LatestCompletedTurnID))
		return th
	}
	sender := create("sender", "Inspect the API")
	receiver := create("receiver", "Implement the API")
	handler := srv.projectSessionHandler(sender.ID)
	if _, err := handler(context.Background(), "nested-create", tools.ProjectSessionRequest{Action: "create", Prompt: "Create a nested worker"}); err == nil {
		t.Fatal("worker spawned an unbounded worker hierarchy")
	}
	outsider := startProject(t, client, "Other project")
	if _, err := handler(context.Background(), "cross-project", tools.ProjectSessionRequest{Action: "message", SessionID: outsider.ID, Prompt: "Cross project", Wake: true}); err == nil {
		t.Fatal("peer message crossed project boundary")
	}
	if _, err := handler(context.Background(), "peer-question", tools.ProjectSessionRequest{Action: "message", SessionID: receiver.ID, Prompt: "Does total count filtered rows?", Wake: true}); err != nil {
		t.Fatal(err)
	}
	reply := calls.next(t, "Does total count filtered rows?")
	msg := lastUserRequestMessage(reply.request)
	if msg.RelatedSessionID != sender.ID || msg.Cause != "project_message" || msg.Origin != "host" {
		t.Fatalf("peer provenance lost: %+v", msg)
	}
	reply.response <- providersResponse("Yes, filtered rows.")
	receiver = waitForThread(t, srv, receiver.ID, func(th Thread) bool {
		return th.Status == ThreadStatusIdle && th.LatestCompletedTurnID != receiver.LatestCompletedTurnID
	})
	settleCoordinator(t, srv, calls, lead.ID, "Coordinate peer work", "Understood.", projectResultClientID(receiver.ID, receiver.LatestCompletedTurnID))
	if _, err := handler(context.Background(), "stale-peer", tools.ProjectSessionRequest{Action: "message", SessionID: receiver.ID, Prompt: "Stale queued advice"}); err != nil {
		t.Fatal(err)
	}
	client.rpc(t, MethodTurnInterrupt, TurnInterruptParams{ThreadID: receiver.ID}, nil)
	// A later instruction must not revive queued input admitted before stop.
	srv.drainSessionInbox(receiver.ID)
	if ids := deliveredClientIDs(t, rt, receiver.ID, "project-message:"+sender.ID+":stale-peer"); len(ids) != 0 {
		t.Fatalf("stale peer message delivered: %v", ids)
	}
	calls.assertIdle(t)
}

// A crash after enqueue must preserve provenance, deliver once on restart,
// and leave informational messages dormant until another turn supplies work.
func TestProjectPeerInboxRecoversWithoutDuplicateDelivery(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	lead := startProject(t, client, "Recovery")
	view, err := srv.projectSessionHandler(lead.ID)(context.Background(), "member", tools.ProjectSessionRequest{Action: "create", Prompt: "Prepare recovery work"})
	if err != nil {
		t.Fatal(err)
	}
	calls.next(t, "Prepare recovery work").response <- providersResponse("Ready.")
	member := waitForThread(t, srv, view.(projectSessionView).SessionID, func(th Thread) bool { return th.LatestCompletedTurnID != "" })
	resultID := projectResultClientID(member.ID, member.LatestCompletedTurnID)
	settleCoordinator(t, srv, calls, lead.ID, "Ready.", "Noted.", resultID)
	if _, err := srv.projectSessionHandler(member.ID)(context.Background(), "information", tools.ProjectSessionRequest{Action: "message", SessionID: lead.ID, Prompt: "Informational recovery note"}); err != nil {
		t.Fatal(err)
	}
	srv.drainSessionInbox(lead.ID)
	if ids := deliveredClientIDs(t, rt, lead.ID, "project-message:"+member.ID+":information"); len(ids) != 0 {
		t.Fatal("information woke idle lead")
	}
	control, _, err := session.ReadControl(rt.SessionDir, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	clientID := "project-message:" + member.ID + ":persisted-question"
	if err := session.EnqueueInbox(rt.SessionDir, session.InboxMessage{ClientID: clientID, SessionID: lead.ID, RelatedSessionID: member.ID, Cause: "project_message", Content: "Recovered peer question", Wake: true, Controls: []session.Control{control}}); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	reopened := New(rt, &lockedBuffer{})
	t.Cleanup(reopened.Close)
	reopened.recoverProjectInbox()
	settleCoordinator(t, reopened, calls, lead.ID, "Ready.", "Recovered.", clientID, "project-message:"+member.ID+":information")
	reopened.recoverProjectInbox()
	if ids := deliveredClientIDs(t, rt, lead.ID, clientID); len(ids) != 1 {
		t.Fatalf("recovered message occurrences: %v", ids)
	}
	calls.assertIdle(t)
}

// Legacy project pauses must not strand members or restart old work. Plugin
// control remains a separate contract and must not be normalized.
func TestProjectInterruptedSideWorkDoesNotWakeRecipients(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	lead := startProject(t, client, "Side interruption")
	view, err := srv.projectSessionHandler(lead.ID)(context.Background(), "side", tools.ProjectSessionRequest{Action: "create", Role: "side", Prompt: "Prepare side", Workspace: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	sideID := view.(projectSessionView).SessionID
	calls.next(t, "Prepare side").response <- providersResponse("Side ready.")
	side := waitForThread(t, srv, sideID, func(th Thread) bool { return th.LatestCompletedTurnID != "" })
	settleCoordinator(t, srv, calls, lead.ID, "Side ready.", "Noted.", projectResultClientID(sideID, side.LatestCompletedTurnID))
	view, err = srv.projectSessionHandler(sideID)(context.Background(), "worker", tools.ProjectSessionRequest{Action: "create", Prompt: "Hold worker", Workspace: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	workerID := view.(projectSessionView).SessionID
	calls.next(t, "Hold worker")
	client.rpc(t, MethodTurnInterrupt, TurnInterruptParams{ThreadID: workerID}, nil)
	waitForThread(t, srv, workerID, func(th Thread) bool { return th.LatestCompletedTurnID != "" && th.Status == ThreadStatusIdle })
	srv.recoverProjectInbox()
	calls.assertIdle(t)
	for _, target := range []string{lead.ID, sideID} {
		pending, err := session.PendingInbox(rt.SessionDir, target)
		if err != nil || len(pending) == 0 {
			t.Fatalf("missing stopped report for %s: %v", target, err)
		}
		for _, message := range pending {
			if message.Wake {
				t.Fatalf("stopped report wakes %s: %+v", target, message)
			}
		}
	}
}

// Retry delivery in the consumption/persistence gap must not repeat a notice.
func TestProjectNonWakingNoticeConsumptionIsIdempotent(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	lead := startProject(t, client, "Notice retry")
	var started TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: lead.ID, Prompt: "Hold current work"}, &started)
	held := calls.next(t, "Hold current work")
	if err := session.EnqueueInbox(rt.SessionDir, session.InboxMessage{ClientID: "notice-retry", SessionID: lead.ID, Cause: projectCauseUserMessage, Content: "User intervention", Wake: false}); err != nil {
		t.Fatal(err)
	}
	srv.drainSessionInbox(lead.ID)
	th, err := srv.ensureOwnedThreadLoaded(lead.ID)
	if err != nil {
		t.Fatal(err)
	}
	th.mu.Lock()
	consumed, _ := th.takePendingSteersLocked(th.currentTurn, time.Now())
	th.mu.Unlock()
	if len(consumed) != 1 {
		t.Fatalf("consumed notices = %d", len(consumed))
	}
	// The model is held, so consumed input has not yet reached durable history.
	srv.drainSessionInbox(lead.ID)
	th.mu.Lock()
	repeated, _ := th.takePendingSteersLocked(th.currentTurn, time.Now())
	th.mu.Unlock()
	if len(repeated) != 0 {
		t.Fatalf("notice delivered twice: %+v", repeated)
	}
	held.response <- providersResponse("Done.")
	waitForThread(t, srv, lead.ID, func(th Thread) bool { return th.LatestCompletedTurnID != "" })
	srv.drainSessionInbox(lead.ID)
	calls.assertIdle(t)
}

func TestProjectRecoversLegacyControlWithoutWaking(t *testing.T) {
	for _, state := range []string{session.ControlPaused, session.ControlTakenOver} {
		t.Run(state, func(t *testing.T) {
			srv, client, calls, rt := newProjectFixture(t)
			lead := startProject(t, client, "Legacy membership")
			sideView, err := srv.projectSessionHandler(lead.ID)(context.Background(), "side", tools.ProjectSessionRequest{Action: "create", Role: "side", Prompt: "Prepare side", Workspace: "shared"})
			if err != nil {
				t.Fatal(err)
			}
			sideID := sideView.(projectSessionView).SessionID
			calls.next(t, "Prepare side").response <- providersResponse("Side ready.")
			side := waitForThread(t, srv, sideID, func(th Thread) bool { return th.LatestCompletedTurnID != "" })
			settleCoordinator(t, srv, calls, lead.ID, "Side ready.", "Noted.", projectResultClientID(sideID, side.LatestCompletedTurnID))
			view, err := srv.projectSessionHandler(sideID)(context.Background(), "member", tools.ProjectSessionRequest{Action: "create", Prompt: "Prepare legacy work", Workspace: "shared"})
			if err != nil {
				t.Fatal(err)
			}
			memberID := view.(projectSessionView).SessionID
			held := calls.next(t, "Prepare legacy work")
			active, _, err := session.ReadControl(rt.SessionDir, memberID)
			if err != nil {
				t.Fatal(err)
			}
			control, err := session.ChangeControl(rt.SessionDir, memberID, lead.ID, state, active.Revision)
			if err != nil {
				t.Fatal(err)
			}
			held.response <- providersResponse("Ready.")
			member := waitForThread(t, srv, memberID, func(th Thread) bool { return th.LatestCompletedTurnID != "" })
			calls.assertIdle(t)
			plugin, err := session.ChangeControl(rt.SessionDir, "plugin-member", "plugin:example", state, 0)
			if err != nil {
				t.Fatal(err)
			}
			srv.Close()
			// A host sharing the session store must not activate another
			// workspace's legacy members before their results are settled.
			otherRuntime := newTestRuntime(t, &fakeClient{})
			otherRuntime.SessionDir = rt.SessionDir
			otherRuntime.WorkspaceID = "workspace-other"
			otherRuntime.WuuHome = t.TempDir()
			registry, err := json.Marshal(map[string]any{"projects": []map[string]string{{"id": rt.WorkspaceID, "name": "Original", "path": rt.RootDir}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(otherRuntime.WuuHome, "projects.json"), registry, 0o644); err != nil {
				t.Fatal(err)
			}
			otherHost := New(otherRuntime, &lockedBuffer{})
			otherHost.Close()
			foreign, _, err := session.ReadControl(rt.SessionDir, memberID)
			if err != nil || foreign != control {
				t.Fatalf("foreign host migrated member: %+v, %v", foreign, err)
			}
			reopened := New(rt, &lockedBuffer{})
			t.Cleanup(reopened.Close)
			reopened.recoverProjectInbox()
			for _, resultID := range []string{projectResultClientID(memberID, member.LatestCompletedTurnID), projectResultClientID(memberID, member.LatestCompletedTurnID) + ":" + sideID} {
				settled, err := session.InboxHas(rt.SessionDir, resultID)
				if err != nil || !settled {
					t.Fatalf("legacy result not settled: %s, %v", resultID, err)
				}
			}
			restored, ok, err := session.ReadControl(rt.SessionDir, memberID)
			if err != nil || !ok || restored.State != session.ControlActive || restored.Revision <= control.Revision {
				t.Fatalf("legacy member stranded: %+v, %v", restored, err)
			}
			unchanged, _, err := session.ReadControl(rt.SessionDir, plugin.SessionID)
			if err != nil || unchanged != plugin {
				t.Fatalf("plugin control changed: %+v, %v", unchanged, err)
			}
			calls.assertIdle(t)
			if _, err := reopened.projectSessionHandler(lead.ID)(context.Background(), "new-work", tools.ProjectSessionRequest{Action: "send", SessionID: memberID, Prompt: "New work after migration"}); err != nil {
				t.Fatal(err)
			}
			calls.next(t, "New work after migration").response <- providersResponse("New work done.")
			calls.next(t, "New work done.").response <- providersResponse("Received new result.")
		})
	}
}

// A message admitted while the recipient is waiting on its model is still
// pending input; stopping its sender must revoke it before consumption.
func TestProjectPeerSteerDoesNotOutliveSenderControl(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	lead := startProject(t, client, "Steering fence")
	view, err := srv.projectSessionHandler(lead.ID)(context.Background(), "sender", tools.ProjectSessionRequest{Action: "create", Prompt: "Inspect steering"})
	if err != nil {
		t.Fatal(err)
	}
	calls.next(t, "Inspect steering").response <- providersResponse("Sender ready.")
	sender := waitForThread(t, srv, view.(projectSessionView).SessionID, func(th Thread) bool { return th.LatestCompletedTurnID != "" })
	settleCoordinator(t, srv, calls, lead.ID, "Sender ready.", "Ready.", projectResultClientID(sender.ID, sender.LatestCompletedTurnID))
	var turn TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: lead.ID, Prompt: "Wait for the current model response"}, &turn)
	held := calls.next(t, "Wait for the current model response")
	if _, err := srv.projectSessionHandler(sender.ID)(context.Background(), "revoked-steer", tools.ProjectSessionRequest{Action: "message", SessionID: lead.ID, Prompt: "Revoked peer input"}); err != nil {
		t.Fatal(err)
	}
	srv.drainSessionInbox(lead.ID)
	client.rpc(t, MethodTurnInterrupt, TurnInterruptParams{ThreadID: sender.ID}, nil)
	srv.drainSessionInbox(lead.ID)
	held.response <- providersResponse("Current work done.")
	settleCoordinator(t, srv, calls, lead.ID, "Sender ready.", "Noted stop.", projectControlClientID(sender.ID, sender.SessionControl.Revision+1))
	if ids := deliveredClientIDs(t, rt, lead.ID, "project-message:"+sender.ID+":revoked-steer"); len(ids) != 0 {
		t.Fatalf("revoked peer steer reached the model: %v", ids)
	}
	calls.assertIdle(t)
}
