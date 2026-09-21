package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

type harnessRouteWriter struct {
	output *lockedBuffer
	routes chan Request
}

func (w *harnessRouteWriter) Write(data []byte) (int, error) {
	var request Request
	if json.Unmarshal(data, &request) == nil && request.Method == MethodWorkspaceHarnessDispatch {
		w.routes <- request
	}
	return w.output.Write(data)
}

func TestHarnessWorkspaceDispatchUsesTargetRuntimeAndRecoversOnTargetOnly(t *testing.T) {
	f, managerProvider := newCollaborationFlowFixture(t)
	f.server.channelService.SetWakeSink(nil)
	handoffTestRoom(t, f)
	source := handoffTestSource(t, f)
	ctx := context.Background()
	var parent ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Update target docs", RequestID: "request"}, &parent)
	_ = managerProvider.next(t)
	actor := harnessTestActor(t, f, parent.Session.SessionRef)

	targetRT := newTestRuntime(t, &fakeClient{})
	targetRT.WuuHome, targetRT.SessionDir = f.server.rt.WuuHome, f.server.rt.SessionDir
	targetRT.WorkspaceID, targetRT.StateDir = "target", t.TempDir()
	targetRT.StreamRunner.SystemPrompt = "Target project configuration"
	targetProvider := &collaborationFlowProvider{calls: make(chan *collaborationFlowCall, 8)}
	targetRT.StreamRunner.Client = providers.AdaptStreamClient(targetProvider)
	attachNamedAgentTestToolkit(t, targetRT)
	projects, _ := json.Marshal(map[string]any{"projects": []map[string]string{{"id": "target", "path": targetRT.RootDir, "name": "Target"}}})
	if err := os.WriteFile(filepath.Join(targetRT.WuuHome, "projects.json"), projects, 0600); err != nil {
		t.Fatal(err)
	}
	targetOut := &lockedBuffer{}
	target := New(targetRT, targetOut)
	t.Cleanup(target.Close)
	target.channelService.SetWakeSink(nil)

	p := channels.HarnessSessionParams{Action: "create", WorkspaceID: "target", WorkspaceRoot: targetRT.RootDir, Prompt: "Inspect documentation", OperationID: "cross-project", Media: []channels.HarnessMediaRef{{MessageID: source.ID, Kind: "image", Index: 2}}}
	if _, err := f.server.HarnessSession(ctx, actor, p); err == nil {
		t.Fatal("a host without workspace dispatch accepted foreign execution")
	}
	if pending, err := f.server.channelService.PendingHarnessOperations(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("unavailable routing left executable work: %v, %v", pending, err)
	}

	writer := &harnessRouteWriter{output: f.out, routes: make(chan Request, 8)}
	f.server.writeMu.Lock()
	f.server.out = writer
	f.server.writeMu.Unlock()
	f.server.setClientMethods([]string{MethodWorkspaceHarnessDispatch})
	done := make(chan error, 1)
	go func() { _, err := f.server.HarnessSession(ctx, actor, p); done <- err }()
	var route Request
	select {
	case route = <-writer.routes:
	case <-time.After(collaborationTestWaitTimeout):
		t.Fatal("no workspace dispatch")
	}
	var dispatch harnessWorkspaceRequest
	if err := json.Unmarshal(route.Params, &dispatch); err != nil {
		t.Fatal(err)
	}
	op, err := f.server.channelService.HarnessOperation(ctx, dispatch.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.reconcileLocalHarnessSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := session.Find(targetRT.SessionDir, op.Params.SessionID); err != nil || exists {
		t.Fatalf("foreign maintenance created the session: exists=%v err=%v", exists, err)
	}
	if err := f.server.applyHarnessDispatch(ctx, dispatch); err == nil {
		t.Fatal("wrong executor accepted dispatch")
	}
	request, _ := json.Marshal(Request{ID: json.RawMessage(`"target-dispatch"`), Method: MethodHarnessDispatch, Params: route.Params})
	if err := target.handleLine(ctx, request); err != nil {
		t.Fatal(err)
	}
	if response := waitForResponseByID(t, targetOut, "target-dispatch"); response["error"] != nil {
		t.Fatalf("target dispatch failed: %v", response)
	}
	reply, _ := json.Marshal(Response{ID: route.ID, Result: struct{}{}})
	f.server.deliverClientResponse(reply)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	worker := targetProvider.next(t)
	var handedOff []providers.InputImage
	for _, msg := range worker.request.Messages {
		handedOff = append(handedOff, msg.Images...)
	}
	if len(handedOff) != 1 || handedOff[0].Data != source.Images[1].Data {
		t.Fatal("cross-workspace dispatch lost the selected source image")
	}
	if !strings.Contains(collaborationRequestText(worker.request), "Target project configuration") {
		t.Fatal("worker did not use the target runtime's configuration")
	}
	id := op.Params.SessionID
	th := target.thread(id)
	th.mu.Lock()
	kit := th.execRuntime.Toolkit
	liveID := th.WorkspaceID
	th.mu.Unlock()
	if liveID != "target" {
		t.Fatalf("target live thread lost workspace id: %q", liveID)
	}
	started := false
	for _, notification := range notificationsByMethod(parseOutput(t, targetOut.String()), NotificationThreadStarted) {
		thread := remarshal[ThreadStartedNotification](t, notification["params"]).Thread
		if thread.ID != id {
			continue
		}
		started = true
		if thread.WorkspaceID != "target" || thread.CWD != targetRT.RootDir {
			t.Fatalf("thread/started lost target workspace identity: %+v", thread)
		}
	}
	if !started {
		t.Fatal("target create did not emit thread/started")
	}
	canonicalRoot, _ := filepath.EvalSymlinks(targetRT.RootDir)
	if kit.RootDir() != canonicalRoot || kit.SessionDir() != statepath.SessionArtifactDir(targetRT.StateDir, id) {
		t.Fatalf("wrong execution scope: cwd=%s artifacts=%s", kit.RootDir(), kit.SessionDir())
	}
	if f.server.thread(id) != nil {
		t.Fatal("manager loaded an execution runtime for the target session")
	}
	loaded, err := f.server.ensureThreadLoaded(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.ensureThreadRuntime(loaded); err == nil {
		t.Fatal("ordinary admission bypassed the session's project binding")
	}
	if _, err := f.server.HarnessSession(ctx, actor, channels.HarnessSessionParams{Action: "send", SessionID: id, WorkspaceRoot: f.server.rt.RootDir, Prompt: "Wrong project", OperationID: "retarget"}); err == nil {
		t.Fatal("send retargeted the existing session")
	}
	// Replayed dispatch is idempotent while the target is executing.
	if err := target.applyHarnessDispatch(ctx, dispatch); err != nil {
		t.Fatal(err)
	}
	select {
	case <-targetProvider.calls:
		t.Fatal("replayed dispatch started a duplicate turn")
	default:
	}
	// Simulate an accepted follow-up awaiting recovery after a host disconnect.
	control, _, err := session.ReadControl(targetRT.SessionDir, id)
	if err != nil {
		t.Fatal(err)
	}
	queued, _, err := f.server.channelService.ReserveHarnessOperation(ctx, actor, channels.HarnessSessionParams{Action: "send", SessionID: id, Prompt: "Also check navigation", OperationID: "recover-followup", Media: []channels.HarnessMediaRef{{MessageID: source.ID, Kind: "image", Index: 1}}}, id, control.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.reconcileLocalHarnessSessions(ctx); err != nil {
		t.Fatal(err)
	}
	pending, _ := f.server.channelService.HarnessOperation(ctx, queued.ID)
	if pending.State != "pending" {
		t.Fatalf("foreign runtime consumed pending input: %s", pending.State)
	}
	worker.response <- providers.ChatResponse{Content: "Documentation inspected"}
	waitForThreadLeaseRelease(t, targetRT.SessionDir, id)
	if err := target.reconcileLocalHarnessSessions(ctx); err != nil {
		t.Fatal(err)
	}
	followup := targetProvider.next(t)
	handedOff = nil
	for _, msg := range followup.request.Messages {
		handedOff = append(handedOff, msg.Images...)
	}
	if len(handedOff) != 2 || handedOff[1].Data != source.Images[0].Data {
		t.Fatal("target recovery lost queued image evidence")
	}
	if !strings.Contains(collaborationRequestText(followup.request), "Also check navigation") {
		t.Fatal("target did not recover the same session's follow-up")
	}
}

func TestHarnessWorkspaceBindingRejectsMismatchAndMissingProject(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	project := t.TempDir()
	data, _ := json.Marshal(map[string]any{"projects": []map[string]string{{"id": "target", "path": project}}})
	if err := os.WriteFile(filepath.Join(rt.WuuHome, "projects.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	if _, _, err := srv.resolveSessionWorkspace("target", rt.RootDir); err == nil {
		t.Fatal("conflicting project ID and path accepted")
	}
	linked := session.Session{WorkspaceID: "target", CWD: filepath.Join(t.TempDir(), "worktree"), WorktreeBaseRepo: project}
	root, id, err := srv.sessionWorkspace(linked)
	if err != nil || root != project || id != "target" {
		t.Fatalf("linked worktree lost its base project: %q %q %v", root, id, err)
	}
	if err := os.Remove(project); err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.resolveSessionWorkspace("target", ""); err == nil {
		t.Fatal("missing project silently fell back to the host workspace")
	}
}
