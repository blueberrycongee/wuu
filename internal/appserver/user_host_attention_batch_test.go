package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/blueberrycongee/wuu/internal/personalhost"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// These contracts protect notification delivery, revocation, and durable result
// preservation. Fake time makes deadline and shutdown behavior deterministic.
type batchAttentionPolicyClient struct {
	pluginClientRequestTestRuntime
	allowed atomic.Bool
	window  atomic.Int64
	calls   chan pluginhost.SessionAttentionInput
}

func (c *batchAttentionPolicyClient) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilitySessionAttention, Kind: "decision", Version: 1}}
}

func (c *batchAttentionPolicyClient) InvokeCapability(ctx context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	var input pluginhost.SessionAttentionInput
	if err := json.Unmarshal(params.Input, &input); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	select {
	case c.calls <- input:
	case <-ctx.Done():
		return pluginhost.CapabilityInvokeResult{}, ctx.Err()
	}
	raw, _ := json.Marshal(map[string]any{"allowed": c.allowed.Load(), "batch_window_seconds": c.window.Load()})
	return pluginhost.CapabilityInvokeResult{Output: raw}, nil
}

func newBatchAttentionHost(t *testing.T, seconds int64) (*UserHost, *runtime.Session, *batchAttentionPolicyClient) {
	t.Helper()
	policy := &batchAttentionPolicyClient{pluginClientRequestTestRuntime: pluginClientRequestTestRuntime{id: "fixture"}, calls: make(chan pluginhost.SessionAttentionInput, 1024)}
	policy.allowed.Store(true)
	policy.window.Store(seconds)
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("preserved result")})
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
	attachTestModelGeneration(t, rt, policy)
	host, err := NewUserHost(rt, personalhost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	return host, rt, policy
}

func sendBatchAttentionTurn(t *testing.T, rt *runtime.Session, policy *batchAttentionPolicyClient, agent, request string) pluginhost.SessionAttentionInput {
	t.Helper()
	main, err := rt.PluginSessionRouter.Create(pluginInvocationContext(t, rt, context.Background()), "fixture", pluginhost.SessionCreateParams{RequestID: "main-" + agent, Visibility: "plugin", ContextSource: "fresh", AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	sent, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, context.Background()), "fixture", pluginhost.SessionSendParams{RequestID: request, SessionID: main.SessionID, Input: pluginhost.SessionInput{Prompt: "finish " + request}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case input := <-policy.calls:
		if input.TurnID != sent.TurnID {
			t.Fatalf("wrong admission: got %s, want %s", input.TurnID, sent.TurnID)
		}
		synctest.Wait()
		return input
	case <-time.After(5 * time.Second):
		t.Fatal("completion did not consult notification policy")
		return pluginhost.SessionAttentionInput{}
	}
}

func requireNoBatchAttention(t *testing.T, host *UserHost) {
	t.Helper()
	synctest.Wait()
	select {
	case event := <-host.attentionEvents:
		t.Fatalf("unexpected notification: %+v", event.notification)
	default:
	}
}

func requireBatchAttention(t *testing.T, host *UserHost, turn string, count int) userAttentionEvent {
	t.Helper()
	synctest.Wait()
	select {
	case event := <-host.attentionEvents:
		raw, _ := json.Marshal(event.notification)
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		if event.notification.TurnID != turn || fields["item_count"] != float64(count) {
			t.Fatalf("wrong latest locator or aggregate count: %s", raw)
		}
		return event
	default:
		t.Fatal("notification did not arrive at first-item deadline")
		return userAttentionEvent{}
	}
}

func TestUserHostAttentionBatchUsesFirstDeadlineAndPreservesResults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host, rt, policy := newBatchAttentionHost(t, 10)
		first := sendBatchAttentionTurn(t, rt, policy, "alpha", "first")
		requireNoBatchAttention(t, host)
		time.Sleep(6 * time.Second)
		second := sendBatchAttentionTurn(t, rt, policy, "alpha", "second")
		// Replaying the same terminal transition cannot inflate a digest.
		host.enqueueAttention(userAttentionJob{owner: "fixture", input: second, location: UserLocation{Version: 1, UserSpaceID: "local", ProductDomain: "personal", AgentID: "alpha", MessageSeq: second.MessageSeq}})
		requireNoBatchAttention(t, host)
		time.Sleep(4 * time.Second)
		event := requireBatchAttention(t, host, second.TurnID, 2)
		changed := event
		changed.input.RequestID = "another-request"
		if host.revalidateAttention(context.Background(), changed) {
			t.Fatal("attention retargeted a committed turn to another request")
		}
		if event.notification.UserLocation.AgentID != "alpha" || event.notification.UserLocation.MessageSeq != second.MessageSeq || first.MessageSeq >= second.MessageSeq {
			t.Fatalf("digest did not retain the latest responsible-agent result: %+v", event.notification)
		}
		if len(policy.calls) != 1 {
			t.Fatalf("flush must freshly consult policy exactly once; calls = %d", len(policy.calls))
		}
		history, err := rt.Sessions.LoadHistoryRecords(first.SessionID, false)
		if err != nil {
			t.Fatal(err)
		}
		results := 0
		for _, message := range history {
			if message.Role == "assistant" && message.Content == "preserved result" {
				results++
			}
		}
		if results != 2 {
			t.Fatalf("batching changed individual conversation results: %d", results)
		}
		time.Sleep(10 * time.Second)
		requireNoBatchAttention(t, host)
	})
}

func TestUserHostAttentionBatchKeepsAgentsSeparateAndRevalidatesReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host, rt, policy := newBatchAttentionHost(t, 10)
		alpha := sendBatchAttentionTurn(t, rt, policy, "alpha", "first")
		beta := sendBatchAttentionTurn(t, rt, policy, "beta", "second")
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if len(host.attentionEvents) != 2 {
			t.Fatalf("independent agents were combined or lost: %d", len(host.attentionEvents))
		}
		seen := map[string]bool{}
		for range 2 {
			event := <-host.attentionEvents
			seen[event.notification.TurnID] = true
			policy.allowed.Store(false)
			if host.revalidateAttention(context.Background(), event) {
				t.Fatal("buffered digest bypassed a newly disabled policy")
			}
		}
		if !seen[alpha.TurnID] || !seen[beta.TurnID] {
			t.Fatalf("wrong responsible-agent notifications: %v", seen)
		}
	})
}

func TestUserHostAttentionBatchRevokesPendingDelivery(t *testing.T) {
	for _, cause := range []string{"policy disabled", "owner removed", "generation replaced", "session removed", "input removed", "input retracted", "result retracted", "host closed"} {
		t.Run(cause, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				host, rt, policy := newBatchAttentionHost(t, 10)
				input := sendBatchAttentionTurn(t, rt, policy, "alpha", "first")
				requireNoBatchAttention(t, host)
				switch cause {
				case "policy disabled":
					policy.allowed.Store(false)
				case "owner removed":
					rt.PluginHost.RetirePlugin(context.Background(), "fixture", fmt.Errorf("test disable"))
				case "generation replaced":
					_, replacement, _ := newBatchAttentionHost(t, 10)
					_, generation := replacement.RetainPluginHost(pluginhost.CapabilitySessionAttention)
					defer replacement.ReleasePluginGeneration(generation)
					if err := rt.ActivatePluginGeneration(generation, nil); err != nil {
						t.Fatal(err)
					}
				case "session removed":
					if _, err := rt.Sessions.Delete(input.SessionID); err != nil {
						t.Fatal(err)
					}
				case "input removed":
					if err := rt.Sessions.RewriteHistoryRecords(input.SessionID, nil); err != nil {
						t.Fatal(err)
					}
				case "input retracted":
					page, err := rt.Sessions.ReadHistoryPage(input.SessionID, 1, 1)
					if err != nil || len(page.Records) != 1 {
						t.Fatalf("read committed source: %+v %v", page, err)
					}
					if err := rt.Sessions.RewriteHistoryRecordsForEdit(input.SessionID, nil, 1, page.HeadSeq); err != nil {
						t.Fatal(err)
					}
					if retained, err := rt.Sessions.ReadHistoryPage(input.SessionID, 1, 1); err != nil || len(retained.Records) != 1 {
						t.Fatalf("edit must retain the raw admission while revoking its active projection: %+v %v", retained, err)
					}
				case "result retracted":
					page, err := rt.Sessions.ReadHistoryPage(input.SessionID, 1, 1)
					if err != nil || len(page.Records) != 1 || input.MessageSeq <= page.Records[0].Seq {
						t.Fatalf("read input before result: %+v %v", page, err)
					}
					if err := rt.Sessions.RewriteHistoryRecordsForEdit(input.SessionID, page.Records, input.MessageSeq, page.HeadSeq); err != nil {
						t.Fatal(err)
					}
					if retained, err := rt.Sessions.ReadActiveHistoryQuery(session.HistoryReadQuery{SessionID: input.SessionID, StartSeq: 1, EndSeq: 1, Limit: 1}); err != nil || len(retained.Records) != 1 {
						t.Fatalf("result-only edit must retain the active source: %+v %v", retained, err)
					}
				case "host closed":
					host.Close()
				}
				time.Sleep(10 * time.Second)
				requireNoBatchAttention(t, host)
			})
		})
	}
}

func TestUserHostAttentionBatchWindowBoundAndImmediateOptOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host, rt, policy := newBatchAttentionHost(t, 9999)
		first := sendBatchAttentionTurn(t, rt, policy, "alpha", "first")
		time.Sleep(899 * time.Second)
		requireNoBatchAttention(t, host)
		time.Sleep(time.Second)
		requireBatchAttention(t, host, first.TurnID, 1)
		<-policy.calls // flush authorization
		policy.window.Store(10)
		sendBatchAttentionTurn(t, rt, policy, "alpha", "second")
		policy.window.Store(0)
		third := sendBatchAttentionTurn(t, rt, policy, "alpha", "third")
		requireBatchAttention(t, host, third.TurnID, 2)
		time.Sleep(10 * time.Second)
		requireNoBatchAttention(t, host)
	})
}

func TestUserHostAttentionBatchQueueIsBoundedWithoutBlockingCompletions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host, rt, policy := newBatchAttentionHost(t, 10)
		// Exercise the terminal-publication boundary at capacity without running
		// 129 unrelated model-turn lifecycles. The fixed-deadline test above
		// covers real turn completion and individual history preservation.
		for index := range 129 {
			id := fmt.Sprintf("bounded-session-%d", index)
			_, err := rt.Sessions.CreateInitialized(session.Session{
				ID: id, CreatedAt: time.Now(), UserSpaceID: "local", ProductDomain: "personal",
				Owner: "plugin:fixture", Visibility: "plugin", AgentID: fmt.Sprintf("agent-%d", index),
			}, []session.HistoryRecord{{Role: "user", ClientID: pluginSessionRequestClientID("fixture", id), Origin: "plugin", OriginID: "fixture", ReadOnly: true, Content: "finish"}, {Role: "assistant", Content: "persisted result"}})
			if err != nil {
				t.Fatal(err)
			}
			thread, err := host.server.ensureThreadLoaded(testRuntimeState(t, rt), id)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{"method": NotificationTurnCompleted, "params": TurnCompletedNotification{
				ThreadID: id, Turn: thread.Turns[0],
			}})
			if _, err := host.Write(raw); err != nil {
				t.Fatal(err)
			}
			<-policy.calls
			synctest.Wait()
		}
		requireNoBatchAttention(t, host)
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if got := len(policy.calls); got != 128 {
			t.Fatalf("pending batches must bound flush policy work to 128 agents: %d", got)
		}
		if got := len(host.attentionEvents); got != 128 {
			t.Fatalf("bounded notification queue lost admitted agents: %d", got)
		}
	})
}

func TestUserHostAttentionBatchDeniedTurnDoesNotDiscardAdmittedResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host, rt, policy := newBatchAttentionHost(t, 10)
		first := sendBatchAttentionTurn(t, rt, policy, "alpha", "allowed")
		policy.allowed.Store(false)
		sendBatchAttentionTurn(t, rt, policy, "alpha", "cancelled-result")
		policy.allowed.Store(true)
		time.Sleep(10 * time.Second)
		requireBatchAttention(t, host, first.TurnID, 1)
	})
}
