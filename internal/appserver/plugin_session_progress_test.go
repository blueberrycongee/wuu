package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Public observation failures: a pending steer borrowing another request's
// turn, a mixed-input turn, a foreign owner, secret fields escaping a display
// projection, and large/escaped text exceeding its explicit wire bound.
func TestPluginSessionProgressScopesAndBounds(t *testing.T) {
	input := ThreadItem{ID: "input", Type: ThreadItemUserMessage, SourceID: pluginSessionRequestClientID("owner", "a"), Text: "private prompt"}
	th := &threadState{ID: "session", Owner: "plugin:owner", Turns: []Turn{{ID: "turn-a", Items: []ThreadItem{
		input,
		{ID: "reasoning", Type: ThreadItemReasoning, Text: "private reasoning"},
		{ID: "message", Type: ThreadItemAgentMessage, Status: ThreadItemStatusInProgress, Text: "working"},
		{ID: "tool", Type: ThreadItemToolCall, Status: ThreadItemStatusInProgress, Name: "read_file", Arguments: "private arguments", Result: "private result"},
	}}}}
	progress := readPluginSessionProgress(th, "owner", "a", "turn-a")
	if !progress.Available || progress.RequestID != "a" || progress.TurnID != "turn-a" || len(progress.Items) != 2 || progress.Items[0].Text != "working" || progress.Items[1].Name != "read_file" {
		t.Fatalf("progress = %+v", progress)
	}
	raw, err := json.Marshal(progress)
	if err != nil || strings.Contains(string(raw), "private") {
		t.Fatalf("private content in progress: %s, %v", raw, err)
	}
	for _, test := range []struct{ plugin, request, turn, reason string }{
		{"other", "a", "turn-a", "session_not_owned"},
		{"owner", "b", "turn-a", "request_not_exclusive"},
		{"owner", "a", "missing", "turn_unavailable"},
	} {
		got := readPluginSessionProgress(th, test.plugin, test.request, test.turn)
		if got.Available || got.Reason != test.reason || len(got.Items) != 0 {
			t.Fatalf("%+v: progress = %+v", test, got)
		}
	}
	th.Turns[0].Items = append(th.Turns[0].Items, ThreadItem{ID: "steer", Type: ThreadItemUserMessage, SourceID: pluginSessionRequestClientID("owner", "b")})
	for _, request := range []string{"a", "b"} {
		got := readPluginSessionProgress(th, "owner", request, "turn-a")
		if got.Available || got.Reason != "request_not_exclusive" {
			t.Fatalf("mixed request %s: %+v", request, got)
		}
	}
	th.Turns[0].Items = []ThreadItem{input}
	for i := 0; i < 80; i++ {
		th.Turns[0].Items = append(th.Turns[0].Items, ThreadItem{ID: strings.Repeat("i", 20), Type: ThreadItemAgentMessage, Status: ThreadItemStatusCompleted, Text: strings.Repeat("\x00界", 2000)})
	}
	progress = readPluginSessionProgress(th, "owner", "a", "turn-a")
	raw, err = json.Marshal(progress)
	if err != nil || !progress.Available || !progress.Truncated || len(raw) > 32768 || len(progress.Items) > 32 || len(progress.Items) == 0 {
		t.Fatalf("unbounded progress: items=%d bytes=%d value=%+v err=%v", len(progress.Items), len(raw), progress, err)
	}
	for _, item := range progress.Items {
		if !utf8.ValidString(item.Text) || len(item.Text) > 4096 || !item.Truncated {
			t.Fatalf("invalid item text bound: bytes=%d truncated=%v", len(item.Text), item.Truncated)
		}
	}
	// Item identity is never shortened into a different identity.
	th.Turns[0].Items = []ThreadItem{input, {ID: strings.Repeat("x", 40000), Type: ThreadItemAgentMessage, Text: "oversized identity"}}
	progress = readPluginSessionProgress(th, "owner", "a", "turn-a")
	if !progress.Available || !progress.Truncated || len(progress.Items) != 0 {
		t.Fatalf("oversized item identity = %+v", progress)
	}
}

func TestPluginSessionInspectLiveProgressDoesNotLeakQueuedOrSteeredWork(t *testing.T) {
	client := newPartialBlockingStreamClient("in-progress answer")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	owner := &pluginTurnLifecycleClient{id: "progress-owner", calls: make(chan pluginhost.AgentTurnLifecycleInput, 16)}
	other := &pluginTurnLifecycleClient{id: "other", calls: make(chan pluginhost.AgentTurnLifecycleInput, 16)}
	attachTestModelGeneration(t, rt, owner, other)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	ctx := pluginInvocationContext(t, rt, context.Background())
	created, err := rt.PluginSessionRouter.Create(ctx, owner.id, pluginhost.SessionCreateParams{RequestID: "create", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh})
	if err != nil {
		t.Fatal(err)
	}
	sent, err := rt.PluginSessionRouter.Send(ctx, owner.id, pluginhost.SessionSendParams{SessionID: created.SessionID, RequestID: "a", Input: pluginhost.SessionInput{Prompt: "private input"}})
	if err != nil {
		t.Fatal(err)
	}
	waitForNotificationCount(t, out, NotificationAgentMessageDelta, 1)
	inspect := func(request string, include bool) pluginhost.SessionInspectResult {
		t.Helper()
		result, err := rt.PluginSessionRouter.Inspect(ctx, owner.id, pluginhost.SessionInspectParams{SessionID: created.SessionID, RequestID: request, IncludeProgress: include})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if got := inspect("a", false); got.Progress != nil {
		t.Fatalf("default inspect exposed progress: %+v", got)
	}
	got := inspect("a", true)
	if got.Turn == nil || got.Turn.State != "running" || got.Progress == nil || !got.Progress.Available || len(got.Progress.Items) != 1 || got.Progress.Items[0].Text != "in-progress answer" {
		t.Fatalf("live inspect = %+v; progress=%+v", got, got.Progress)
	}
	// Observe actual host waiter registration, not merely a submitted client
	// request, before cancelling the inspector while its target is still live.
	waitCtx, cancelWait := context.WithCancel(context.Background())
	defer cancelWait()
	waited := make(chan error, 1)
	go func() {
		_, err := rt.PluginSessionRouter.Inspect(pluginInvocationContext(t, rt, waitCtx), owner.id, pluginhost.SessionInspectParams{
			SessionID: created.SessionID, RequestID: "a", IncludeProgress: true,
			Wait: pluginhost.SessionInspectWaitTerminal, TimeoutMS: 2000,
		})
		waited <- err
	}()
	waitForPluginTurnWaiters(t, srv, owner.id, "a", 1)
	cancelWait()
	select {
	case err := <-waited:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled progress inspection = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled progress inspection did not return")
	}
	waitForPluginTurnWaiters(t, srv, owner.id, "a", 0)
	for _, mode := range []string{"queue", "steer"} {
		_, err := rt.PluginSessionRouter.Send(ctx, owner.id, pluginhost.SessionSendParams{SessionID: created.SessionID, RequestID: mode, IfRunning: mode, Input: pluginhost.SessionInput{Prompt: "other input"}})
		if err != nil {
			t.Fatal(err)
		}
		got := inspect(mode, true)
		if got.Progress == nil || got.Progress.Available || len(got.Progress.Items) != 0 {
			t.Fatalf("%s borrowed running progress: %+v", mode, got.Progress)
		}
	}
	if _, err := rt.PluginSessionRouter.Inspect(ctx, other.id, pluginhost.SessionInspectParams{SessionID: created.SessionID, RequestID: "a", IncludeProgress: true}); err == nil {
		t.Fatal("foreign owner inspected private progress")
	}
	if _, err := rt.PluginSessionRouter.Inspect(ctx, owner.id, pluginhost.SessionInspectParams{SessionID: created.SessionID, IncludeProgress: true}); err == nil {
		t.Fatal("progress accepted an implicit latest request")
	}
	if got := inspect("unknown", true); got.Progress == nil || got.Progress.Available {
		t.Fatalf("unknown request borrowed progress: %+v", got.Progress)
	}
	// Inspection registers no observer and cannot cancel the target execution.
	if got := inspect("a", true); got.Turn.TurnID != sent.TurnID || got.Turn.State != "running" {
		t.Fatalf("inspection changed execution: %+v", got)
	}
}
