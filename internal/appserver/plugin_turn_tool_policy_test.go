package appserver

import (
	"context"
	"encoding/json"
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
	if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "observer", params); err == nil || err.Error() != "disable_tools cannot be used when if_running is steer" {
		t.Fatalf("steer: %v", err)
	}
	params.IfRunning = "queue"
	if _, err := rt.Sessions.SetEngine(created.SessionID, "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "observer", params); err == nil || err.Error() != "the selected agent engine cannot enforce disable_tools" {
		t.Fatalf("external: %v", err)
	}
	params.DisableTools = false
	params.ToolPolicy = &pluginhost.SessionTurnToolPolicy{Allow: []string{"read_file"}}
	if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "observer", params); err == nil || err.Error() != "the selected agent engine cannot enforce tool_policy" {
		t.Fatalf("external engine policy refusal: %v", err)
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
	for _, outcome := range []string{"failure", "cancel", "failure-policy", "cancel-policy"} {
		t.Run(outcome, func(t *testing.T) {
			usePolicy := strings.HasSuffix(outcome, "-policy")
			behavior := strings.TrimSuffix(outcome, "-policy")
			client := &restrictedTurnClient{outcome: behavior, started: make(chan struct{})}
			rt := newTestRuntime(t, &fakeClient{})
			rt.StreamRunner.Client = providers.AdaptStreamClient(client)
			rt.StreamRunner.Tools = newBlockingToolExecutor()
			owner := &pluginTurnLifecycleClient{id: "observer", calls: make(chan pluginhost.AgentTurnLifecycleInput, 16)}
			attachTestModelGeneration(t, rt, owner)
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, pluginhost.SessionCreateParams{RequestID: "create", Visibility: "user", ContextSource: "fresh"})
			if err != nil {
				t.Fatal(err)
			}
			params := pluginhost.SessionSendParams{RequestID: "restricted", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "feedback"}, DisableTools: !usePolicy}
			if usePolicy {
				params.ToolPolicy = &pluginhost.SessionTurnToolPolicy{Allow: []string{}}
			}
			result, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, params)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.started:
			case <-time.After(5 * time.Second):
				t.Fatal("restricted turn did not start")
			}
			if behavior == "cancel" {
				if usePolicy {
					request := Request{ID: json.RawMessage(`"interrupt"`), Method: MethodTurnInterrupt, Params: mustJSON(TurnInterruptParams{ThreadID: created.SessionID})}
					if err := srv.handleLine(context.Background(), mustJSON(request)); err != nil {
						t.Fatal(err)
					}
					if response := responseByID(t, parseOutput(t, out.String()), "interrupt"); response["error"] != nil {
						t.Fatalf("owner cannot interrupt: %+v", response)
					}
				} else if _, err := srv.cancelPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, pluginhost.SessionCancelParams{SessionID: created.SessionID, TurnID: result.TurnID}); err != nil {
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

// Failure cases: explicit empty allow becomes unrestricted after persistence;
// allowed tools disappear, deny does not intersect allow, or turn-local policy
// leaks into the next ordinary request.
func TestPluginTurnToolPolicySelectiveQueueAndRestore(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy pluginhost.SessionTurnToolPolicy
		want   int
	}{
		{"allow", pluginhost.SessionTurnToolPolicy{Allow: []string{"wait_for_steer"}}, 1},
		{"empty", pluginhost.SessionTurnToolPolicy{Allow: []string{}}, 0},
		{"deny-only", pluginhost.SessionTurnToolPolicy{Deny: []string{"other"}}, 1},
		{"deny", pluginhost.SessionTurnToolPolicy{Allow: []string{"wait_for_steer"}, Deny: []string{"wait_for_steer"}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			releaseFirst := sync.OnceFunc(func() { close(release) })
			client := &fakeClient{response: providersResponse("done"), onChat: func(n int, _ providers.ChatRequest) {
				if n == 1 {
					close(started)
					<-release
				}
			}}
			rt := newTestRuntime(t, client)
			rt.StreamRunner.Tools = newBlockingToolExecutor()
			owner := &pluginTurnLifecycleClient{id: "observer", calls: make(chan pluginhost.AgentTurnLifecycleInput, 32)}
			attachTestModelGeneration(t, rt, owner)
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(func() { releaseFirst(); srv.Close() })
			created, err := srv.createPluginSession(pluginInvocationContext(t, rt, context.Background()), owner.id, pluginhost.SessionCreateParams{RequestID: "create", Visibility: "user", ContextSource: "fresh"})
			if err != nil {
				t.Fatal(err)
			}
			send := func(id string, policy *pluginhost.SessionTurnToolPolicy) pluginhost.SessionSendResult {
				t.Helper()
				params := pluginhost.SessionSendParams{SessionID: created.SessionID, RequestID: id, Input: pluginhost.SessionInput{Prompt: id}, ToolPolicy: policy}
				raw, err := json.Marshal(params)
				if err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(raw, &params); err != nil {
					t.Fatal(err)
				}
				result, err := srv.sendPluginSession(pluginInvocationContext(t, rt, context.Background()), owner.id, params)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			send("before", nil)
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("not started")
			}
			result := send("restricted", &tc.policy)
			if result.State != "queued" {
				t.Fatalf("%+v", result)
			}
			// Round-trip queued policy with the same snapshot codec used by durable input.
			srv.queuedTurnMu.Lock()
			entry := srv.pendingQueuedTurns[created.SessionID][0]
			raw, err := json.Marshal(entry.snapshot)
			if err == nil {
				err = json.Unmarshal(raw, &entry.snapshot)
			}
			srv.pendingQueuedTurns[created.SessionID][0] = entry
			srv.queuedTurnMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			wait := func(id string) {
				t.Helper()
				for {
					select {
					case event := <-owner.calls:
						if event.RequestID == id && event.State == "completed" {
							return
						}
					case <-time.After(5 * time.Second):
						t.Fatal("not completed: " + id)
					}
				}
			}
			releaseFirst()
			wait("restricted")
			send("after", nil)
			wait("after")
			client.mu.Lock()
			defer client.mu.Unlock()
			if len(client.requests) != 3 || len(client.requests[1].Tools) != tc.want || len(client.requests[2].Tools) == 0 {
				t.Fatalf("policy did not survive queue or restore: %+v", client.requests)
			}
		})
	}
}

// An isolated tool-bearing turn must not absorb plugin/peer input, completion
// input or human steering. Refused human input remains resubmittable as a queue;
// the owner retains the normal interrupt operation.
func TestPluginTurnToolPolicyIsolatedAdmission(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	releaseFirst := sync.OnceFunc(func() { close(release) })
	client := &fakeClient{response: providersResponse("done"), onChat: func(n int, _ providers.ChatRequest) {
		if n == 1 {
			close(started)
			<-release
		}
	}}
	rt := newTestRuntime(t, client)
	out := &lockedBuffer{}
	owner := &pluginTurnLifecycleClient{id: "observer", calls: make(chan pluginhost.AgentTurnLifecycleInput, 32)}
	attachTestModelGeneration(t, rt, owner)
	srv := New(rt, out)
	t.Cleanup(func() { releaseFirst(); srv.Close() })
	ctx := pluginInvocationContext(t, rt, context.Background())
	created, err := srv.createPluginSession(ctx, owner.id, pluginhost.SessionCreateParams{RequestID: "create", Visibility: "user", ContextSource: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	review, err := srv.sendPluginSession(ctx, owner.id, pluginhost.SessionSendParams{SessionID: created.SessionID, RequestID: "review", Input: pluginhost.SessionInput{Prompt: "review"}, ToolPolicy: &pluginhost.SessionTurnToolPolicy{Allow: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("not started")
	}
	peer, err := srv.sendPluginSession(ctx, "peer", pluginhost.SessionSendParams{SessionID: created.SessionID, RequestID: "peer", Input: pluginhost.SessionInput{Prompt: "peer data"}, IfRunning: "steer"})
	if err != nil || peer.State != "queued" || peer.Steered {
		t.Fatalf("peer contaminated turn: %+v %v", peer, err)
	}
	if srv.steerAgentCompletion(testRuntimeState(t, rt), created.SessionID, "completion", providers.ChatMessage{Role: "user", Content: "completion data"}) {
		t.Fatal("completion contaminated isolated turn")
	}
	request := Request{ID: json.RawMessage(`"steer"`), Method: MethodTurnSteer, Params: mustJSON(TurnSteerParams{ThreadID: created.SessionID, ExpectedTurnID: review.TurnID, ClientID: "human", Prompt: "human input"})}
	if err := srv.handleLine(context.Background(), mustJSON(request)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "steer")
	if response["error"] == nil || remarshal[ResponseError](t, response["error"]).Code != "isolated_turn_requires_queue" {
		t.Fatalf("human steer missing definite refusal: %+v", response)
	}
	th := srv.thread(created.SessionID)
	th.mu.Lock()
	if len(th.pendingSteers) != 0 {
		t.Error("isolated turn absorbed input")
	}
	th.mu.Unlock()
	request.ID = json.RawMessage(`"queue"`)
	request.Method = MethodTurnQueue
	request.Params = mustJSON(TurnQueueParams{ThreadID: created.SessionID, ClientID: "human", Prompt: "human input"})
	if err := srv.handleLine(context.Background(), mustJSON(request)); err != nil {
		t.Fatal(err)
	}
	if response := responseByID(t, parseOutput(t, out.String()), "queue"); response["error"] != nil {
		t.Fatalf("queue lost refused input: %+v", response)
	}
	request.ID = json.RawMessage(`"held-steer"`)
	request.Method = MethodTurnSteer
	request.Params = mustJSON(TurnSteerParams{ThreadID: created.SessionID, ExpectedTurnID: review.TurnID, ClientID: "human", Prompt: "changed held input"})
	if err := srv.handleLine(context.Background(), mustJSON(request)); err != nil {
		t.Fatal(err)
	}
	heldResponse := responseByID(t, parseOutput(t, out.String()), "held-steer")
	if heldResponse["error"] == nil || remarshal[ResponseError](t, heldResponse["error"]).Code == "isolated_turn_requires_queue" {
		t.Fatalf("held input permitted unsafe automatic fallback: %+v", heldResponse)
	}
	releaseFirst()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-owner.calls:
			if event.RequestID == "review" && event.State == "completed" {
				goto reviewComplete
			}
		case <-deadline:
			t.Fatal("review did not settle")
		}
	}
reviewComplete:
	observed, err := srv.inspectPluginSession(pluginInvocationContext(t, rt, context.Background()), "peer", pluginhost.SessionInspectParams{SessionID: created.SessionID, RequestID: "peer", Wait: "terminal", TimeoutMS: 5000})
	if err != nil || observed.TimedOut || observed.Turn == nil || observed.Turn.State != "completed" {
		t.Fatalf("queued peer did not finish separately: %+v %v", observed, err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.requests) != 3 {
		t.Fatalf("input lost or duplicated: %d turns", len(client.requests))
	}
	for i, expected := range []string{"review", "human input", "peer data"} {
		messages := client.requests[i].Messages
		found := false
		for _, message := range messages {
			if message.Content == expected {
				found = true
			}
			if i == 0 && (message.Content == "human input" || message.Content == "peer data") || i == 1 && message.Content == "peer data" || message.Content == "changed held input" {
				t.Fatalf("input contaminated turn %d: %+v", i, messages)
			}
		}
		if !found {
			t.Fatalf("input not delivered in its own turn: %s", expected)
		}
	}
}
