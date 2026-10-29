package appserver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// Security failures: queued admission drops attenuation, fabricated calls bypass
// the catalog, policy widens session authority, or restrictions leak to next turn.
func TestPluginTurnToolPolicyQueueAndRestore(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	releaseFirst := sync.OnceFunc(func() { close(release) })
	client := &fakeClient{response: providersResponse("done"), onChat: func(call int, _ providers.ChatRequest) {
		if call == 1 {
			close(started)
			<-release
		}
	}}
	rt := newTestRuntime(t, client)
	rt.StreamRunner.Tools = newBlockingToolExecutor()
	owner := &pluginTurnLifecycleClient{id: "observer", calls: make(chan pluginhost.AgentTurnLifecycleInput, 16)}
	attachTestModelGeneration(t, rt, owner)
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(func() { releaseFirst(); srv.Close() })
	created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, pluginhost.SessionCreateParams{RequestID: "create", Visibility: "user", ContextSource: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	send := func(id string, disable bool) pluginhost.SessionSendResult {
		t.Helper()
		result, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, pluginhost.SessionSendParams{RequestID: id, SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: id}, DisableTools: disable})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	send("ordinary-before", false)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	restricted := send("feedback", true)
	if restricted.State != "queued" {
		t.Fatalf("not queued: %+v", restricted)
	}
	releaseFirst()
	wait := func(id string) {
		t.Helper()
		for {
			select {
			case state := <-owner.calls:
				if state.RequestID == id && state.State == "completed" {
					return
				}
			case <-time.After(5 * time.Second):
				t.Fatal("turn did not complete: " + id)
			}
		}
	}
	wait("feedback")
	send("ordinary-after", false)
	wait("ordinary-after")
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.requests) != 3 {
		t.Fatalf("requests: %d", len(client.requests))
	}
	if len(client.requests[0].Tools) == 0 || len(client.requests[1].Tools) != 0 || len(client.requests[2].Tools) != len(client.requests[0].Tools) {
		t.Fatalf("tool counts: %d %d %d", len(client.requests[0].Tools), len(client.requests[1].Tools), len(client.requests[2].Tools))
	}
}

func TestPluginTurnToolPolicyDeniesFabricatedCallsAndCannotWiden(t *testing.T) {
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionGuard := newSessionToolPolicyExecutor(kit, pluginhost.SessionToolPolicy{Deny: []string{"write_file"}})
	widened := newSessionToolPolicyExecutor(sessionGuard, pluginhost.SessionToolPolicy{Allow: []string{"write_file"}})
	if _, err := widened.Execute(context.Background(), providers.ToolCall{Name: "write_file", Arguments: `{}`}); err == nil || !strings.Contains(err.Error(), "disabled by the session tool policy") {
		t.Fatal("turn widened session policy")
	}
	guard := newSessionToolPolicyExecutor(sessionGuard, pluginhost.SessionToolPolicy{Deny: []string{"*"}})
	for _, name := range []string{"bash", "exec", "write_file", "browser", "tool_search", "unknown_extension"} {
		call := providers.ToolCall{Name: name, Arguments: `{}`}
		if _, err := guard.Execute(context.Background(), call); err == nil || !strings.Contains(err.Error(), "disabled by the session tool policy") {
			t.Fatal("execute allowed " + name)
		}
		if _, err := guard.(agent.RichToolExecutor).ExecuteResult(context.Background(), call); err == nil || !strings.Contains(err.Error(), "disabled by the session tool policy") {
			t.Fatal("rich execute allowed " + name)
		}
		if err := guard.(agent.ToolAuthorizationGate).AuthorizeTool(context.Background(), call, agent.ToolMetadata{}); err == nil || !strings.Contains(err.Error(), "disabled by the session tool policy") {
			t.Fatal("authorize allowed " + name)
		}
	}
}

func TestPluginTurnToolPolicyRejectsUnsupportedAdmission(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "observer", pluginhost.SessionCreateParams{RequestID: "create", Visibility: "user", ContextSource: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	params := pluginhost.SessionSendParams{RequestID: "restricted", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "feedback"}, DisableTools: true, IfRunning: "steer"}
	if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "observer", params); err == nil || !strings.Contains(err.Error(), "steer") {
		t.Fatalf("steer: %v", err)
	}
	params.IfRunning = "queue"
	if _, err := rt.Sessions.SetEngine(created.SessionID, "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "observer", params); err == nil || !strings.Contains(err.Error(), "disable_tools") {
		t.Fatalf("external: %v", err)
	}
	if srv.hasQueuedUserTurns(created.SessionID) {
		t.Fatal("unsupported work queued")
	}
	records, err := rt.Sessions.LoadHistoryRecords(created.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("unsupported work admitted: %+v", records)
	}
}

// Cancellation and provider failure must release the cached runner restriction.
func TestPluginTurnToolPolicyRestoresAfterUnsuccessfulTurn(t *testing.T) {
	for _, outcome := range []string{"failure", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			client := &restrictedTurnClient{outcome: outcome, started: make(chan struct{})}
			rt := newTestRuntime(t, &fakeClient{})
			rt.StreamRunner.Client = providers.AdaptStreamClient(client)
			rt.StreamRunner.Tools = newBlockingToolExecutor()
			owner := &pluginTurnLifecycleClient{id: "observer", calls: make(chan pluginhost.AgentTurnLifecycleInput, 16)}
			attachTestModelGeneration(t, rt, owner)
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, pluginhost.SessionCreateParams{RequestID: "create", Visibility: "user", ContextSource: "fresh"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, pluginhost.SessionSendParams{RequestID: "restricted", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "feedback"}, DisableTools: true})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.started:
			case <-time.After(5 * time.Second):
				t.Fatal("restricted turn did not start")
			}
			if outcome == "cancel" {
				if _, err := srv.cancelPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, pluginhost.SessionCancelParams{SessionID: created.SessionID, TurnID: result.TurnID}); err != nil {
					t.Fatal(err)
				}
			}
			for {
				select {
				case state := <-owner.calls:
					if state.RequestID == "restricted" && pluginTurnLifecycleTerminal(state.State) {
						goto settled
					}
				case <-time.After(5 * time.Second):
					t.Fatal("restricted turn did not settle")
				}
			}
		settled:
			client.mu.Lock()
			client.outcome = ""
			client.mu.Unlock()
			if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, pluginhost.SessionSendParams{RequestID: "ordinary", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "ordinary"}}); err != nil {
				t.Fatal(err)
			}
			for {
				select {
				case state := <-owner.calls:
					if state.RequestID == "ordinary" && state.State == "completed" {
						goto complete
					}
				case <-time.After(5 * time.Second):
					t.Fatal("ordinary turn did not settle")
				}
			}
		complete:
			client.mu.Lock()
			defer client.mu.Unlock()
			if len(client.requests) < 2 || len(client.requests[0].Tools) != 0 || len(client.requests[len(client.requests)-1].Tools) == 0 {
				t.Fatal("tool restriction leaked or was absent")
			}
		})
	}
}

type restrictedTurnClient struct {
	mu       sync.Mutex
	outcome  string
	started  chan struct{}
	once     sync.Once
	requests []providers.ChatRequest
}

func (c *restrictedTurnClient) Chat(ctx context.Context, request providers.ChatRequest) (providers.ChatResponse, error) {
	c.mu.Lock()
	c.requests = append(c.requests, request)
	outcome := c.outcome
	c.mu.Unlock()
	c.once.Do(func() { close(c.started) })
	if outcome == "cancel" {
		<-ctx.Done()
		return providers.ChatResponse{}, ctx.Err()
	}
	if outcome == "failure" {
		return providers.ChatResponse{}, errors.New("invalid request: deterministic provider failure")
	}
	return providersResponse("done"), nil
}

func TestPluginTurnToolPolicyRejectsChangedEngineBinding(t *testing.T) {
	client := &fakeClient{response: providersResponse("must not execute")}
	rt := newTestRuntime(t, client)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "observer", pluginhost.SessionCreateParams{RequestID: "create", Visibility: "user", ContextSource: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	th := srv.thread(created.SessionID)
	started, admitted, err := srv.startThreadUserTurn(context.Background(), th, providers.ChatMessage{Role: "user", Content: "feedback"}, turnRuntimeSnapshot{DisableTools: true}, false, turnReadOnlyFail)
	if err != nil || !admitted {
		t.Fatalf("admission: %t %v", admitted, err)
	}
	th.mu.Lock()
	th.EngineID = "codex"
	th.mu.Unlock()
	srv.runTurn(testRuntimeState(t, srv.rt), started.ctx, th, &runtime.ThreadRuntime{EngineID: "codex"}, started.turnID, started.runtime, started.history)
	messages := parseOutput(t, out.String())
	failed := remarshal[TurnErrorNotification](t, notificationByMethod(t, messages, NotificationTurnError)["params"])
	if failed.Turn.Error == nil || !strings.Contains(failed.Turn.Error.Message, "disable_tools") {
		t.Fatalf("missing binding rejection: %+v", failed)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.requests) != 0 {
		t.Fatal("unsupported binding ran native provider fallback")
	}
}
