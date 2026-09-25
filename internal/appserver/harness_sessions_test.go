package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestHarnessResultCarriesExecutionOutcome(t *testing.T) {
	for _, outcome := range []TurnStatus{TurnStatusCompleted, TurnStatusFailed, TurnStatusInterrupted} {
		t.Run(string(outcome), func(t *testing.T) {
			f, provider := newCollaborationFlowFixture(t)
			ctx := context.Background()
			var parent ChannelSessionResult
			f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Inspect the project", RequestID: "request"}, &parent)
			decision := provider.next(t)
			actor := harnessTestActor(t, f, parent.Session.SessionRef)
			id, _, _ := harnessTestCreate(t, f, actor)
			worker := provider.next(t)
			switch outcome {
			case TurnStatusCompleted:
				worker.response <- providers.ChatResponse{Content: "Inspection evidence is available."}
			case TurnStatusFailed:
				worker.failure <- providers.NewNonRetryableStreamError("execution could not finish")
			case TurnStatusInterrupted:
				if _, err := f.server.interruptThreadExecution(id, "", ""); err != nil {
					t.Fatal(err)
				}
			}
			waitForThreadLeaseRelease(t, f.server.rt.SessionDir, id)
			client, err := f.server.channelService.BindAgentSession(ctx, actor.AgentID, actor.SessionRef)
			if err != nil {
				t.Fatal(err)
			}
			var receipt string
			for range 2 {
				if err := f.server.reconcileHarnessSessions(ctx); err != nil {
					t.Fatal(err)
				}
				messages, err := client.ReceiveCollaboration(ctx, 32)
				if err != nil || len(messages) != 1 {
					t.Fatalf("result delivery: %+v %v", messages, err)
				}
				message := messages[0]
				if message.FromSessionRef != id || string(message.TerminalState) != string(outcome) || message.CorrelationID != actor.TurnID {
					t.Fatalf("manager received a different outcome or source: %+v", message)
				}
				if receipt != "" && receipt != message.ID {
					t.Fatal("reconciliation duplicated the result")
				}
				receipt = message.ID
			}
			if err := client.AcknowledgeCollaboration(ctx, []string{receipt}); err != nil {
				t.Fatal(err)
			}
			decision.response <- providers.ChatResponse{StopReason: "completed"}
			f.waitForCompletion(t)
		})
	}
}

func harnessTestActor(t *testing.T, f *collaborationRPCFixture, ref string) channels.HarnessSessionActor {
	t.Helper()
	b, err := f.server.channelService.LookupCollaborationSession(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return channels.HarnessSessionActor{AgentID: f.identity.ID, SessionRef: ref, TurnID: b.TurnID, RoomID: f.room.ID}
}

func harnessTestCreate(t *testing.T, f *collaborationRPCFixture, actor channels.HarnessSessionActor) (string, channels.HarnessSessionParams, string) {
	t.Helper()
	p := channels.HarnessSessionParams{Action: "create", Title: "Document refresh", Prompt: "Inspect current docs and report evidence.", WorkspaceRoot: f.server.rt.RootDir, OperationID: "create-docs"}
	workflow := providers.NewInferenceWorkflow(providers.InferenceProfileInteractive)
	result, err := f.server.HarnessSession(providers.WithInferenceWorkflow(context.Background(), workflow), actor, p)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	var decoded struct {
		Session harnessSessionView `json:"session"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Session.ID, p, workflow.ID
}

func TestHarnessSessionVisibleIdempotentAndWakesOriginalConversation(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	var parent ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Update the docs", RequestID: "request"}, &parent)
	decision := provider.next(t)
	actor := harnessTestActor(t, f, parent.Session.SessionRef)
	id, p, sourceWorkflow := harnessTestCreate(t, f, actor)
	worker := provider.next(t)
	if worker.workflowID == "" || worker.workflowID == sourceWorkflow {
		t.Fatalf("executor inherited caller inference workflow: %q", worker.workflowID)
	}
	if id == actor.SessionRef {
		t.Fatal("created another identity conversation")
	}
	m, found, err := session.Find(f.server.rt.SessionDir, id)
	if err != nil || !found {
		t.Fatal(err)
	}
	if m.Owner != "user" || m.Visibility != "user" || m.CWD != f.server.rt.RootDir || isNamedAgentSessionSource(m.Source) {
		t.Fatalf("not a normal project session: %+v", m)
	}
	if m.ParentID != actor.SessionRef {
		t.Fatalf("lost manager provenance: parent = %q", m.ParentID)
	}
	assertRootSession := func(thread Thread) {
		t.Helper()
		if thread.ParentID != "" || thread.AgentPath != "" || thread.ReadOnly || thread.Ephemeral {
			t.Fatalf("ordinary session would be hidden as an internal worker: %+v", thread)
		}
	}
	started := false
	for _, notification := range notificationsByMethod(parseOutput(t, f.out.String()), NotificationThreadStarted) {
		thread := remarshal[ThreadStartedNotification](t, notification["params"]).Thread
		if thread.ID == id {
			started = true
			assertRootSession(thread)
		}
	}
	if !started {
		t.Fatal("created session did not emit thread/started")
	}
	assertListed := func(status ThreadStatus) {
		t.Helper()
		for _, method := range []string{MethodThreadList, MethodThreadListAll} {
			var listed ThreadListResult
			f.rpc(t, method, ThreadListParams{SummaryOnly: true}, &listed)
			found := false
			for _, thread := range listed.Threads {
				if thread.ID != id {
					continue
				}
				found = true
				assertRootSession(thread)
				if thread.Status != status || thread.SessionControl == nil || thread.SessionControl.ManagerID != actor.AgentID {
					t.Fatalf("%s lost execution or management state: %+v", method, thread)
				}
			}
			if !found {
				t.Fatalf("%s omitted the managed session", method)
			}
		}
	}
	assertListed(ThreadStatusInProgress)
	if _, err := f.server.HarnessSession(context.Background(), actor, p); err != nil {
		t.Fatal(err)
	}
	all, err := session.List(f.server.rt.SessionDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, candidate := range all {
		if candidate.CreationRequestID == m.CreationRequestID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("replayed creation made %d sessions", count)
	}
	// Updating management must retain the in-flight result even though its
	// accepted input predates the current control revision.
	if _, err := f.server.HarnessSession(context.Background(), actor, channels.HarnessSessionParams{Action: "manage", SessionID: id, Prompt: "Track the installation review", OperationID: "update-running-management"}); err != nil {
		t.Fatal(err)
	}
	decision.response <- providers.ChatResponse{StopReason: "completed"}
	f.waitForCompletion(t)
	worker.response <- providers.ChatResponse{Content: "Inspected docs; stale installation instructions remain."}
	wake := provider.next(t)
	if text := collaborationRequestText(wake.request); !strings.Contains(text, id) {
		t.Fatalf("result wake lost session reference: %s", text)
	}
	inspected, err := f.server.inspectHarnessSession(context.Background(), channels.HarnessSessionParams{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(inspected)
	if !strings.Contains(string(data), "stale installation") {
		t.Fatalf("inspect omitted actual result: %s", data)
	}
	wake.response <- providers.ChatResponse{Content: "I found the outdated instructions and am checking the replacement."}
	f.waitForCompletion(t)
	assertListed(ThreadStatusIdle)
	restored, err := f.server.loadPersistedThreadState(id, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	f.server.mu.Lock()
	f.server.threads[id] = restored
	f.server.mu.Unlock()
	assertListed(ThreadStatusIdle)
}

func TestHarnessTakeoverFencesQueuedWorkAndLeavesHistory(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	var parent ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Update docs", RequestID: "request"}, &parent)
	decision := provider.next(t)
	actor := harnessTestActor(t, f, parent.Session.SessionRef)
	id, _, _ := harnessTestCreate(t, f, actor)
	worker := provider.next(t)
	if _, err := f.server.HarnessSession(context.Background(), actor, channels.HarnessSessionParams{Action: "send", SessionID: id, Prompt: "Apply the draft", OperationID: "queued-edit"}); err != nil {
		t.Fatal(err)
	}
	th := f.server.thread(id)
	th.mu.Lock()
	c, _, _ := session.ReadControl(f.server.rt.SessionDir, id)
	th.pendingSteers = append(th.pendingSteers, providers.ChatMessage{ClientID: "auto-steer", Origin: "plugin", Content: "obsolete correction"}, providers.ChatMessage{ClientID: "human-steer", Content: "human correction"})
	th.pendingSteerControls = map[string]session.Control{"auto-steer": c}
	th.mu.Unlock()
	if err := f.server.takeHarnessControl(id, session.ControlTakenOver); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.HarnessSession(context.Background(), actor, channels.HarnessSessionParams{Action: "send", SessionID: id, Prompt: "Should not run", OperationID: "stale"}); err == nil {
		t.Fatal("automatic instruction accepted after takeover")
	}
	th.mu.Lock()
	for _, msg := range th.pendingSteers {
		if msg.ClientID == "auto-steer" {
			t.Error("revoked automatic steer survived")
		}
	}
	th.pendingSteers = nil
	th.mu.Unlock()
	worker.response <- providers.ChatResponse{Content: "Draft evidence"}
	waitForTurnCompletedForThread(t, f.out, id)
	if err := f.server.reconcileHarnessSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	ops, err := f.server.channelService.PendingHarnessOperations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Params.Prompt == "Apply the draft" {
			t.Fatal("queued instruction survived control change")
		}
	}
	client, err := f.server.channelService.BindAgentSession(context.Background(), actor.AgentID, actor.SessionRef)
	if err != nil {
		t.Fatal(err)
	}
	notices, err := client.ReceiveCollaboration(context.Background(), 32)
	if err != nil {
		t.Fatal(err)
	}
	var controlNotice, failedOperation bool
	for _, notice := range notices {
		if strings.HasPrefix(notice.RequestID, "harness-control:") {
			controlNotice = true
			if notice.TerminalState != "" {
				t.Fatalf("control change claimed an execution outcome: %+v", notice)
			}
		}
		if strings.HasSuffix(notice.RequestID, ":failure") {
			failedOperation = true
			if notice.TerminalState != channels.CollaborationTerminalFailed {
				t.Fatalf("revoked operation reported success: %+v", notice)
			}
		}
	}
	if !controlNotice || !failedOperation {
		t.Fatalf("takeover lost its control or rejected-operation notice: %+v", notices)
	}
	m, found, err := session.Find(f.server.rt.SessionDir, id)
	if err != nil || !found || m.Entries == 0 {
		t.Fatalf("takeover lost session history: %+v %v", m, err)
	}
	decision.response <- providers.ChatResponse{StopReason: "completed"}
	f.waitForCompletion(t)
}

func TestHarnessManageExistingReleaseAndStop(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	ctx := context.Background()
	var parent ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Continue existing work", RequestID: "request"}, &parent)
	decision := provider.next(t)
	actor := harnessTestActor(t, f, parent.Session.SessionRef)
	th, err := f.server.createHostSessionThread("user", "", "", pluginhost.SessionCreateParams{Name: "Existing work", Visibility: "user", ContextSource: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(action, mode, prompt, key string) {
		t.Helper()
		if _, err := f.server.HarnessSession(ctx, actor, channels.HarnessSessionParams{Action: action, SessionID: th.ID, Mode: mode, Prompt: prompt, OperationID: key}); err != nil {
			t.Fatal(err)
		}
	}
	call("manage", "attach", "Verify the existing draft", "attach")
	if active, err := session.ThreadExecutionActive(f.server.rt.SessionDir, th.ID); err != nil || active {
		t.Fatalf("attach started execution: %v %v", active, err)
	}
	call("send", "queue", "Inspect the draft", "inspect")
	worker := provider.next(t)
	call("manage", "release", "", "release")
	if active, err := session.ThreadExecutionActive(f.server.rt.SessionDir, th.ID); err != nil || !active {
		t.Fatalf("release stopped execution: %v %v", active, err)
	}
	worker.response <- providers.ChatResponse{Content: "Draft inspected"}
	waitForTurnCompletedForThread(t, f.out, th.ID)
	call("manage", "attach", "Continue verification", "reattach")
	call("send", "queue", "Check the generated page", "continue")
	_ = provider.next(t)
	call("stop", "", "", "stop")
	waitForTurnCompletedForThread(t, f.out, th.ID)
	c, _, err := session.ReadControl(f.server.rt.SessionDir, th.ID)
	if err != nil || c.State != session.ControlPaused {
		t.Fatalf("stop did not persist pause: %+v %v", c, err)
	}
	call("manage", "release", "", "release-paused")
	metadata, found, err := session.Find(f.server.rt.SessionDir, th.ID)
	if err != nil || !found || metadata.ParentID != "" || metadata.Owner != "user" {
		t.Fatalf("management changed identity: %+v %v", metadata, err)
	}
	decision.response <- providers.ChatResponse{StopReason: "completed"}
	f.waitForCompletion(t)
}

func TestDeletingManagingAgentStopsTasklessHarnessExecution(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	ctx := context.Background()
	var parent ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Inspect docs", RequestID: "request"}, &parent)
	_ = provider.next(t)
	actor := harnessTestActor(t, f, parent.Session.SessionRef)
	id, _, _ := harnessTestCreate(t, f, actor)
	_ = provider.next(t)
	var deleted ChannelAgentDeleteResult
	f.rpc(t, MethodChannelAgentDelete, ChannelAgentDeleteParams{AgentID: actor.AgentID}, &deleted)
	if !deleted.Deleted {
		t.Fatal("delete RPC returned false")
	}
	for _, ref := range []string{actor.SessionRef, id} {
		waitForThreadLeaseRelease(t, f.server.rt.SessionDir, ref)
	}
	control, _, err := session.ReadControl(f.server.rt.SessionDir, id)
	if err != nil || control.State != session.ControlReleased {
		t.Fatalf("deleted identity still controls execution: %+v, %v", control, err)
	}
	if _, err := f.server.HarnessSession(ctx, actor, channels.HarnessSessionParams{Action: "send", SessionID: id, Prompt: "Late continuation", OperationID: "late-send"}); err == nil {
		t.Fatal("deleted identity accepted new execution")
	}
	if metadata, found, err := session.Find(f.server.rt.SessionDir, id); err != nil || !found || metadata.ArchivedAt == nil || metadata.ArchiveReason != "agent_deleted" {
		t.Fatalf("project execution history was not archived: %+v, %v", metadata, err)
	}
	for _, method := range []string{MethodThreadList, MethodThreadListAll, MethodThreadListArchived} {
		var result ThreadListResult
		f.rpc(t, method, ThreadListParams{}, &result)
		found := false
		for _, thread := range result.Threads {
			if thread.ID == id {
				found = true
				if !thread.Archived || thread.ArchiveReason != "agent_deleted" {
					t.Fatalf("archive classification missing: %+v", thread)
				}
			}
		}
		if found != (method == MethodThreadListArchived) {
			t.Fatalf("%s has deleted agent history: %v", method, found)
		}
	}
}

func TestDeletedAgentArchiveReconcilesOldOrphansAcrossWorkspaces(t *testing.T) {
	f, _ := newCollaborationFlowFixture(t)
	ctx := context.Background()
	states := []string{session.ControlActive, session.ControlPaused, session.ControlTakenOver, session.ControlReleased, "deleted"}
	for _, state := range states {
		if _, err := session.CreateWithMetadata(f.server.rt.SessionDir, state, "/another-workspace"); err != nil {
			t.Fatal(err)
		}
		controlState := state
		if state == "deleted" {
			controlState = session.ControlActive
		}
		control, err := session.ChangeControl(f.server.rt.SessionDir, state, f.identity.ID, controlState, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.server.channelService.PutHarnessLink(ctx, channels.HarnessSessionLink{SessionID: state, AgentID: f.identity.ID, RoomID: f.room.ID, Active: true, ControlRevision: control.Revision}); err != nil {
			t.Fatal(err)
		}
		if state == "deleted" {
			if _, err := session.Delete(f.server.rt.SessionDir, state); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Simulate an older version that deleted the identity without archiving work.
	if err := f.server.channelService.DeleteNamedAgent(ctx, f.identity.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.server.reconcileLocalHarnessSessions(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range states {
		metadata, found, err := session.Find(f.server.rt.SessionDir, state)
		if state == "deleted" {
			if err != nil || found {
				t.Fatalf("deleted history was recreated: %+v %v", metadata, err)
			}
			continue
		}
		wantArchive := state == session.ControlActive || state == session.ControlPaused
		if err != nil || !found || (metadata.ArchivedAt != nil) != wantArchive {
			t.Fatalf("%s history: %+v %v", state, metadata, err)
		}
		if wantArchive && metadata.ArchiveReason != "agent_deleted" {
			t.Fatalf("orphan mixed with ordinary archive: %+v", metadata)
		}
	}
	if _, err := session.UpdateArchived(f.server.rt.SessionDir, session.ControlPaused, false); err != nil {
		t.Fatal(err)
	}
	if err := f.server.reconcileLocalHarnessSessions(ctx); err != nil {
		t.Fatal(err)
	}
	metadata, _, err := session.Find(f.server.rt.SessionDir, session.ControlPaused)
	if err != nil || metadata.ArchivedAt != nil || metadata.ArchiveReason != "" {
		t.Fatalf("restored history was reclaimed: %+v %v", metadata, err)
	}
}

func TestHarnessTaskCancellationStopsOnlyItsExecution(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	ctx := context.Background()
	var parent ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Inspect docs and branches", RequestID: "request"}, &parent)
	decision := provider.next(t)
	actor := harnessTestActor(t, f, parent.Session.SessionRef)
	client, err := f.server.channelService.BindAgent(ctx, actor.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := client.CreateTask(ctx, channels.TaskCreateParams{RoomID: actor.RoomID, Title: "Inspect docs", OwnerID: actor.AgentID})
	if err != nil {
		t.Fatal(err)
	}
	id, _, _ := harnessTestCreate(t, f, actor)
	_ = provider.next(t)
	if _, err := f.server.HarnessSession(ctx, actor, channels.HarnessSessionParams{Action: "manage", SessionID: id, WorkID: task.ID, OperationID: "bind-task"}); err != nil {
		t.Fatal(err)
	}
	other, err := f.server.createHostSessionThread("user", "", "", pluginhost.SessionCreateParams{Name: "Other task", Visibility: "user", ContextSource: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []channels.HarnessSessionParams{
		{Action: "manage", SessionID: other.ID, OperationID: "attach-other"},
		{Action: "send", SessionID: other.ID, Prompt: "Inspect branches", OperationID: "start-other"},
	} {
		if _, err := f.server.HarnessSession(ctx, actor, p); err != nil {
			t.Fatal(err)
		}
	}
	otherCall := provider.next(t)
	if _, err := client.CancelWork(ctx, task.ID, "User cancelled documentation work"); err != nil {
		t.Fatal(err)
	}
	// The cancellation event itself must stop this worker, without a maintenance tick.
	waitForThreadLeaseRelease(t, f.server.rt.SessionDir, id)
	if err := f.server.reconcileHarnessSessions(ctx); err != nil {
		t.Fatal(err)
	}
	control, _, err := session.ReadControl(f.server.rt.SessionDir, id)
	if err != nil || control.State != session.ControlPaused {
		t.Fatalf("cancelled task still managed: %+v %v", control, err)
	}
	if active, err := session.ThreadExecutionActive(f.server.rt.SessionDir, other.ID); err != nil || !active {
		t.Fatalf("cancellation interrupted unrelated work: %v %v", active, err)
	}
	if _, err := f.server.HarnessSession(ctx, actor, channels.HarnessSessionParams{Action: "send", SessionID: id, Prompt: "Stale continuation", OperationID: "late-send"}); err == nil {
		t.Fatal("cancelled task accepted new execution")
	}
	otherCall.response <- providers.ChatResponse{Content: "Branch evidence"}
	waitForTurnCompletedForThread(t, f.out, other.ID)
	decision.response <- providers.ChatResponse{StopReason: "completed"}
	f.waitForCompletion(t)
}

func TestHarnessInspectIncludesUnsettledProgress(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	var parent ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Inspect docs", RequestID: "request"}, &parent)
	decision := provider.next(t)
	actor := harnessTestActor(t, f, parent.Session.SessionRef)
	id, _, _ := harnessTestCreate(t, f, actor)
	worker := provider.next(t)
	th := f.server.thread(id)
	th.mu.Lock()
	turn := &th.Turns[len(th.Turns)-1]
	turn.Items = append(turn.Items,
		ThreadItem{ID: "tool", Type: ThreadItemToolCall, Name: "run_shell", Status: ThreadItemStatusInProgress, Result: "Building documentation"},
		ThreadItem{ID: "reasoning", Type: ThreadItemReasoning, Text: "Private reasoning"},
	)
	th.mu.Unlock()
	result, err := f.server.inspectHarnessSession(context.Background(), channels.HarnessSessionParams{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	var view struct {
		Progress []struct{ Name, Result string } `json:"live_progress"`
	}
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range view.Progress {
		found = found || item.Name == "run_shell" && item.Result == "Building documentation"
	}
	if !found || strings.Contains(string(data), "Private reasoning") {
		t.Fatalf("live evidence unavailable or private reasoning exposed: %s", data)
	}
	worker.response <- providers.ChatResponse{Content: "Documentation built"}
	waitForTurnCompletedForThread(t, f.out, id)
	decision.response <- providers.ChatResponse{StopReason: "completed"}
	f.waitForCompletion(t)
}

func TestHarnessUnconsumedCorrectionSurvivesTerminalReconciliation(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	ctx := context.Background()
	var parent ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Inspect docs", RequestID: "request"}, &parent)
	decision := provider.next(t)
	actor := harnessTestActor(t, f, parent.Session.SessionRef)
	id, _, _ := harnessTestCreate(t, f, actor)
	worker := provider.next(t)
	// Hold reconciliation while reproducing a steer accepted immediately before
	// a terminal response, with no durable user input from consuming the steer.
	f.server.harnessMu.Lock()
	func() {
		defer f.server.harnessMu.Unlock()
		worker.response <- providers.ChatResponse{Content: "Original draft"}
		waitForTurnCompletedForThread(t, f.out, id)
		c, _, err := session.ReadControl(f.server.rt.SessionDir, id)
		if err != nil {
			t.Fatal(err)
		}
		c, err = session.ChangeControl(f.server.rt.SessionDir, id, actor.AgentID, session.ControlActive, c.Revision)
		if err != nil {
			t.Fatal(err)
		}
		link, err := f.server.channelService.HarnessLink(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		link.ControlRevision = c.Revision
		if err := f.server.channelService.PutHarnessLink(ctx, link); err != nil {
			t.Fatal(err)
		}
		op, _, err := f.server.channelService.ReserveHarnessOperation(ctx, actor, channels.HarnessSessionParams{Action: "send", SessionID: id, Prompt: "Include installation evidence", Mode: "steer", OperationID: "late-correction"}, id, c.Revision)
		if err != nil {
			t.Fatal(err)
		}
		th := f.server.thread(id)
		th.mu.Lock()
		op.TurnID = th.Turns[len(th.Turns)-1].ID
		th.mu.Unlock()
		op.State, op.Prepared = "submitted", true
		if err := f.server.channelService.PutHarnessOperation(ctx, op); err != nil {
			t.Fatal(err)
		}
	}()
	if err := f.server.reconcileHarnessSessions(ctx); err != nil {
		t.Fatal(err)
	}
	continued := provider.next(t)
	if text := collaborationRequestText(continued.request); !strings.Contains(text, "Include installation evidence") {
		t.Fatalf("correction was lost after old turn ended: %s", text)
	}
	continued.response <- providers.ChatResponse{Content: "Installation verified"}
	waitForThreadLeaseRelease(t, f.server.rt.SessionDir, id)
	decision.response <- providers.ChatResponse{StopReason: "completed"}
	f.waitForCompletion(t)
}
