package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// Failure cases: Fusion accidentally requires project_agent/plugins; upstream
// receives a synthetic model; a correction creates another Side or loses its
// history; a wait returns another dispatch; synchronous results also wake Lead;
// asynchronous results disappear or repeat after recovery; a user correction
// cannot wake a waiting Lead; Stop leaves Side or stale queued work running;
// settings changes repin existing conversations.
func newFusionFixture(t *testing.T) (*Server, *rpcClient, *gatedProvider) {
	t.Helper()
	rt := newTestRuntime(t, &fakeClient{})
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	rt.Toolkit = kit
	pair := config.FusionConfig{Enabled: true, Lead: config.ModelRoleConfig{Provider: rt.ProviderName, Model: rt.Model}, Sidekick: config.ModelRoleConfig{Provider: rt.ProviderName, Model: rt.Model}}
	cfg := config.Config{DefaultProvider: rt.ProviderName, Providers: map[string]config.ProviderConfig{rt.ProviderName: {Type: "openai-compatible", Model: rt.Model, APIKey: "fixture", BaseURL: "http://127.0.0.1:1/v1"}}, Agent: config.AgentConfig{Fusion: &pair}}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(rt.ConfigPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	provider := newGatedProvider(16)
	rt.StreamRunner.Client = providers.AdaptStreamClient(provider)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	return srv, &rpcClient{server: srv, out: out}, provider
}

func fusionReply(text string) providers.ChatResponse {
	return providers.ChatResponse{Content: text, FinishReason: providers.FinishReasonStop, StopReason: "stop"}
}

func fusionTool(id, args string) providers.ChatResponse {
	return providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: id, Name: "fusion_delegate", Arguments: args}}, FinishReason: providers.FinishReasonToolCalls, StopReason: "tool_use"}
}

func fusionAwait(t *testing.T, srv *Server, id string, predicate func(Thread) bool) Thread {
	t.Helper()
	deadline := time.NewTimer(gatedProviderTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		th, err := srv.ensureThreadLoaded(id)
		if err != nil {
			t.Fatal(err)
		}
		th.mu.Lock()
		view := th.snapshotLocked()
		th.mu.Unlock()
		if predicate(view) {
			return view
		}
		select {
		case <-deadline.C:
			t.Fatalf("thread did not reach expected state: %+v", view)
		case <-tick.C:
		}
	}
}

func TestFusionPersistentSideAndSynchronousReview(t *testing.T) {
	srv, client, calls := newFusionFixture(t)
	var created ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
	if created.Thread.Fusion == nil || created.Thread.Model == "wuu/fusion" {
		t.Fatalf("explicit Fusion selection missing: %+v", created.Thread)
	}
	var started TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: created.Thread.ID, Prompt: "Build feature"}, &started)
	lead := calls.next(t)
	if lead.request.Model != "fake-model" {
		t.Fatalf("upstream model = %s", lead.request.Model)
	}
	lead.response <- fusionTool("first", `{"message":"Implement first pass"}`)
	side := calls.next(t)
	for _, tool := range side.request.Tools {
		if tool.Name == "fusion_delegate" {
			t.Fatal("Side can recursively delegate")
		}
	}
	side.response <- fusionReply("First pass ready; tests passed")
	review := calls.next(t)
	if !strings.Contains(review.request.Messages[len(review.request.Messages)-1].Content, "First pass ready") {
		t.Fatalf("Lead lost Side result: %+v", review.request.Messages)
	}
	var ready FusionTaskView
	if err := json.Unmarshal([]byte(review.request.Messages[len(review.request.Messages)-1].Content), &ready); err != nil {
		t.Fatal(err)
	}
	correctionRequest := tools.FusionDelegateRequest{Action: "review", TaskID: ready.ID, ReportID: ready.Report.ID, Revision: ready.Revision, Verdict: "request_changes", Message: "Fix edge case"}
	args, _ := json.Marshal(correctionRequest)
	review.response <- fusionTool("correction", string(args))
	correction := calls.next(t)
	joined := ""
	for _, message := range correction.request.Messages {
		joined += message.Content
	}
	if !strings.Contains(joined, "First pass ready") || !strings.Contains(joined, "Fix edge case") {
		t.Fatal("Side lost persistent context")
	}
	correction.response <- fusionReply("Edge case fixed")
	accept := calls.next(t)
	if err := json.Unmarshal([]byte(accept.request.Messages[len(accept.request.Messages)-1].Content), &ready); err != nil {
		t.Fatal(err)
	}
	args, _ = json.Marshal(tools.FusionDelegateRequest{Action: "review", TaskID: ready.ID, ReportID: ready.Report.ID, Revision: ready.Revision, Verdict: "accept", Message: "Verified actual implementation and checks"})
	accept.response <- fusionTool("accept", string(args))
	calls.next(t).response <- fusionReply("Reviewed and accepted")
	fusionAwait(t, srv, created.Thread.ID, func(th Thread) bool {
		return th.Status == ThreadStatusIdle && th.LatestCompletedTurnID == started.Turn.ID
	})
	for _, replay := range []struct {
		id      string
		request tools.FusionDelegateRequest
	}{
		{"accept", tools.FusionDelegateRequest{Action: "review", TaskID: ready.ID, ReportID: ready.Report.ID, Revision: ready.Revision, Verdict: "accept", Message: "Verified actual implementation and checks"}},
		{"correction", correctionRequest},
	} {
		result, err := srv.fusionDelegateHandler(created.Thread.ID)(context.Background(), replay.id, replay.request)
		if err != nil || result.(FusionTaskView).State != session.FusionTaskCompleted {
			t.Fatalf("review replay changed completed work: %+v %v", result, err)
		}
	}
	members, err := srv.fusionSides(created.Thread.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("persistent Side = %+v, %v", members, err)
	}
	disabled := false
	if failure := client.call(t, MethodConfigModelUpdate, ConfigModelUpdateParams{ThreadID: created.Thread.ID, Provider: "missing-provider", Model: "missing-model", Fusion: &disabled}, nil); failure == nil {
		t.Fatal("invalid model selection succeeded")
	}
	metadata, _, err := session.Find(srv.rt.SessionDir, created.Thread.ID)
	if err != nil || !metadata.FusionEnabled {
		t.Fatal("failed model selection disabled Fusion")
	}
	srv.recoverFusionInbox()
	select {
	case call := <-calls.calls:
		t.Fatalf("synchronous result woke Lead twice: %+v", call.request)
	default:
	}
	// A new runtime reload must retain the fixed pair and the same Side.
	reloaded := New(srv.rt, &lockedBuffer{})
	defer reloaded.Close()
	th, err := reloaded.ensureThreadLoaded(created.Thread.ID)
	if err != nil || th.Fusion == nil {
		t.Fatalf("reload lost pair: %v", err)
	}
	members, err = reloaded.fusionSides(created.Thread.ID)
	if err != nil || len(members) != 1 {
		t.Fatal("reload lost Side identity")
	}
}

func TestFusionWaitYieldsToUserAndStopFencesSide(t *testing.T) {
	srv, client, calls := newFusionFixture(t)
	var created ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
	var started TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: created.Thread.ID, Prompt: "Build feature"}, &started)
	calls.next(t).response <- fusionTool("held", `{"message":"Held implementation"}`)
	held := calls.next(t)
	var steered TurnSteerResult
	client.rpc(t, MethodTurnSteer, TurnSteerParams{ThreadID: created.Thread.ID, ExpectedTurnID: started.Turn.ID, Prompt: "Use the new requirement"}, &steered)
	awake := calls.next(t)
	joined := ""
	for _, message := range awake.request.Messages {
		joined += message.Content
	}
	if !strings.Contains(joined, "Use the new requirement") {
		t.Fatal("waiting Lead missed user steer")
	}
	client.rpc(t, MethodTurnInterrupt, TurnInterruptParams{ThreadID: created.Thread.ID}, nil)
	members, err := srv.fusionSides(created.Thread.ID)
	if err != nil || len(members) != 1 {
		t.Fatal(err)
	}
	fusionAwait(t, srv, members[0].ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	fusionAwait(t, srv, created.Thread.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	// Completion racing Stop cannot restart either conversation.
	held.response <- fusionReply("Late completion")
	srv.recoverFusionInbox()
	select {
	case call := <-calls.calls:
		t.Fatalf("stopped work restarted: %+v", call.request)
	default:
	}
}

func TestFusionSideSelectionAndPermissionBoundary(t *testing.T) {
	srv, client, calls := newFusionFixture(t)
	var created ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
	var started TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: created.Thread.ID, Prompt: "Background feature"}, &started)
	calls.next(t).response <- fusionTool("permission-boundary", `{"message":"Hold implementation","block":false}`)
	first, second := calls.next(t), calls.next(t)
	for _, call := range []*gatedCall{first, second} {
		isSide := false
		for _, message := range call.request.Messages {
			isSide = isSide || message.Cause == "fusion"
		}
		if !isSide {
			call.response <- fusionReply("Waiting")
		}
	}
	fusionAwait(t, srv, created.Thread.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	readOnly := config.PermissionModeReadOnly
	if failure := client.call(t, MethodThreadArchive, ThreadArchiveParams{ThreadID: created.Thread.ID, Archived: true}, nil); failure == nil {
		t.Fatal("archived Lead while Side was running")
	}
	if failure := client.call(t, MethodThreadDelete, ThreadDeleteParams{ThreadID: created.Thread.ID}, nil); failure == nil {
		t.Fatal("deleted Lead while Side was running")
	}
	if failure := client.call(t, MethodConfigModelUpdate, ConfigModelUpdateParams{ThreadID: created.Thread.ID, PermissionMode: &readOnly}, nil); failure == nil {
		t.Fatal("changed pair permissions while Side was running")
	}
	client.rpc(t, MethodTurnInterrupt, TurnInterruptParams{ThreadID: created.Thread.ID}, nil)
	members, err := srv.fusionSides(created.Thread.ID)
	if err != nil || len(members) != 1 {
		t.Fatal(err)
	}
	fusionAwait(t, srv, members[0].ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	enabled := true
	if failure := client.call(t, MethodConfigModelUpdate, ConfigModelUpdateParams{ThreadID: members[0].ID, Fusion: &enabled}, nil); failure == nil {
		t.Fatal("Side became a recursive Fusion Lead")
	}
	client.rpc(t, MethodConfigModelUpdate, ConfigModelUpdateParams{ThreadID: created.Thread.ID, PermissionMode: &readOnly}, nil)
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: created.Thread.ID, Prompt: "Inspect only"}, &started)
	calls.next(t).response <- fusionTool("read-only", `{"message":"Inspect without writing"}`)
	calls.next(t)
	side, _, err := session.Find(srv.rt.SessionDir, members[0].ID)
	if err != nil || side.PermissionMode != readOnly {
		t.Fatalf("Side retained old permissions: %s, %v", side.PermissionMode, err)
	}
	client.rpc(t, MethodTurnInterrupt, TurnInterruptParams{ThreadID: created.Thread.ID}, nil)
}

func TestFusionBackgroundReportDeliveredOnce(t *testing.T) {
	srv, client, calls := newFusionFixture(t)
	var created ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
	var started TurnStartResult
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: created.Thread.ID, Prompt: "Background feature"}, &started)
	calls.next(t).response <- fusionTool("background", `{"message":"Background implementation","block":false}`)
	first, second := calls.next(t), calls.next(t)
	var side *gatedCall
	for _, call := range []*gatedCall{first, second} {
		isSide := false
		for _, message := range call.request.Messages {
			isSide = isSide || (message.Role == "user" && message.Cause == "fusion")
		}
		if isSide {
			side = call
		} else {
			call.response <- fusionReply("Waiting for implementation")
		}
	}
	if side == nil {
		t.Fatal("no Side request")
	}
	fusionAwait(t, srv, created.Thread.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	side.response <- fusionReply("Background evidence")
	report := calls.next(t)
	last := report.request.Messages[len(report.request.Messages)-1]
	if last.Cause != "fusion_result" || !strings.Contains(last.Content, "Background evidence") {
		t.Fatalf("result provenance lost: %+v", last)
	}
	report.response <- fusionReply("Reviewed background evidence")
	fusionAwait(t, srv, created.Thread.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	srv.recoverFusionInbox()
	srv.recoverFusionInbox()
	pending, err := session.PendingInbox(srv.rt.SessionDir, created.Thread.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("unsettled report: %+v %v", pending, err)
	}
	select {
	case call := <-calls.calls:
		t.Fatalf("report repeated: %+v", call.request)
	default:
	}
	if _, err := srv.fusionDelegateHandler(created.Thread.ID)(context.Background(), "background", tools.FusionDelegateRequest{Message: "Background implementation", Block: new(bool)}); err != nil {
		t.Fatal(err)
	}
	select {
	case call := <-calls.calls:
		t.Fatalf("dispatch replay ran again: %+v", call.request)
	default:
	}
}

// A failed provider turn must expose its cause to the Lead in both synchronous
// delivery and inspection, including after the terminal record is reloaded.
func TestFusionFailedSideReportsProviderCause(t *testing.T) {
	srv, client, calls := newFusionFixture(t)
	var created ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: created.Thread.ID, Prompt: "Check failure reporting"}, nil)
	calls.next(t).response <- fusionTool("failed-side", `{"message":"Try implementation"}`)
	calls.next(t).failure <- &providers.HTTPError{StatusCode: 403, ProviderFamily: "grok-build", ProviderCode: "personal-team-blocked:spending-limit", Body: "spending limit reached"}
	review := calls.next(t)
	last := review.request.Messages[len(review.request.Messages)-1].Content
	if !strings.Contains(last, "spending limit reached") || !strings.Contains(last, `"status_code":403`) {
		t.Fatalf("synchronous delivery lost failure cause: %s", last)
	}
	inspection, err := srv.fusionDelegateHandler(created.Thread.ID)(context.Background(), "inspect-failed", tools.FusionDelegateRequest{Action: "inspect"})
	encoded, _ := json.Marshal(inspection)
	if err != nil || !strings.Contains(string(encoded), "spending limit reached") || !strings.Contains(string(encoded), `"status_code":403`) {
		t.Fatalf("inspection lost failure cause: %s, %v", encoded, err)
	}
	review.response <- fusionReply("Sidekick failed; explained the provider issue")
	thread := fusionAwait(t, srv, created.Thread.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	encoded, _ = json.Marshal(thread.Turns[len(thread.Turns)-1].Fusion)
	if !strings.Contains(string(encoded), "spending limit reached") {
		t.Fatalf("host projection lost failure cause: %s", encoded)
	}
}

// A persistent pair shares one workspace. Moving it must reject a running Side
// and independent Side moves, then persist both bindings when it is idle.
func TestFusionWorkspaceMoveKeepsPersistentPairTogether(t *testing.T) {
	srv, client, calls := newFusionFixture(t)
	repo := initSessionWorkspaceRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runSessionWorkspaceGit(t, repo, "worktree", "add", "-b", "fusion-workspace", linked)
	srv.rt.RootDir = repo
	var created ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: created.Thread.ID, Prompt: "Hold the Side"}, nil)
	calls.next(t).response <- fusionTool("workspace", `{"message":"Held implementation","block":false}`)
	first, second := calls.next(t), calls.next(t)
	for _, call := range []*gatedCall{first, second} {
		isSide := false
		for _, message := range call.request.Messages {
			isSide = isSide || message.Cause == "fusion"
		}
		if !isSide {
			call.response <- fusionReply("Waiting")
		}
	}
	fusionAwait(t, srv, created.Thread.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	sides, err := srv.fusionSides(created.Thread.ID)
	if err != nil || len(sides) != 1 {
		t.Fatalf("Side: %+v, %v", sides, err)
	}
	if err := srv.rebindThreadWorkspace(created.Thread.ID, linked); err == nil {
		t.Fatal("moved workspace while Side was running")
	}
	if err := srv.rebindThreadWorkspace(sides[0].ID, linked); err == nil {
		t.Fatal("Side moved independently of Lead")
	}
	client.rpc(t, MethodTurnInterrupt, TurnInterruptParams{ThreadID: created.Thread.ID}, nil)
	fusionAwait(t, srv, sides[0].ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	if err := srv.rebindThreadWorkspace(created.Thread.ID, linked); err != nil {
		t.Fatal(err)
	}
	reloaded := New(srv.rt, &lockedBuffer{})
	defer reloaded.Close()
	for _, id := range []string{created.Thread.ID, sides[0].ID} {
		th, err := reloaded.ensureThreadLoaded(id)
		if err != nil || th.CWD != linked || th.WorktreePath != linked {
			t.Fatalf("pair workspace %s: %+v, %v", id, th, err)
		}
	}
	// The next delegation must reuse the same Side in the moved workspace.
	// Existing conversations may contain a stale binding from an older host.
	if _, err := session.UpdateWorkspaceBinding(srv.rt.SessionDir, sides[0].ID, repo, "", "", ""); err != nil {
		t.Fatal(err)
	}
	client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: created.Thread.ID, Prompt: "Continue in the worktree"}, nil)
	calls.next(t).response <- fusionTool("moved", `{"message":"Continue implementation"}`)
	calls.next(t).response <- fusionReply("Worktree implementation")
	calls.next(t).response <- fusionReply("Reviewed")
	fusionAwait(t, srv, created.Thread.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	members, err := srv.fusionSides(created.Thread.ID)
	if err != nil || len(members) != 1 || members[0].ID != sides[0].ID || members[0].CWD != linked {
		t.Fatalf("workspace move replaced or diverged Side: %+v, %v", members, err)
	}
}
