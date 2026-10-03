package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestPluginTurnWaitHubNotifiesAllAndCleansUp(t *testing.T) {
	var hub pluginTurnWaitHub
	first, unsubscribeFirst := hub.subscribe(" subagent ", " request-1 ")
	second, unsubscribeSecond := hub.subscribe("subagent", "request-1")
	defer unsubscribeFirst()
	defer unsubscribeSecond()

	if got := pluginTurnWaiterCount(&hub, "subagent", "request-1"); got != 2 {
		t.Fatalf("waiter count = %d, want 2", got)
	}
	hub.notify("subagent", "request-1")
	for index, ready := range []<-chan struct{}{first, second} {
		select {
		case <-ready:
		case <-time.After(time.Second):
			t.Fatalf("waiter %d was not notified", index)
		}
	}
	if got := pluginTurnWaiterCount(&hub, "subagent", "request-1"); got != 0 {
		t.Fatalf("waiter count after notify = %d, want 0", got)
	}

	third, unsubscribeThird := hub.subscribe("subagent", "request-2")
	unsubscribeThird()
	if got := pluginTurnWaiterCount(&hub, "subagent", "request-2"); got != 0 {
		t.Fatalf("waiter count after unsubscribe = %d, want 0", got)
	}
	select {
	case <-third:
		t.Fatal("unsubscribe unexpectedly notified waiter")
	default:
	}
}

func TestPluginSessionInspectWaitsForPersistedTerminalLifecycle(t *testing.T) {
	for _, retry := range []bool{false, true} {
		name := "new"
		if retry {
			name = "retry"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			observerRelease := make(chan struct{})
			var startOnce sync.Once
			var releaseOnce sync.Once
			var observerReleaseOnce sync.Once
			client := &fakeClient{
				response: providersResponse("done"),
				onChat: func(_ int, _ providers.ChatRequest) {
					startOnce.Do(func() { close(started) })
					<-release
				},
			}
			rt := newTestRuntime(t, client)
			rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
			observer := &blockingPluginTurnLifecycleClient{started: make(chan struct{}), release: observerRelease}
			rt.PluginHost = pluginhost.New(observer)
			out := newTerminalBlockingWriter()
			srv := New(rt, out)
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(release) })
				observerReleaseOnce.Do(func() { close(observerRelease) })
				out.unblock()
				srv.Close()
			})

			created, err := rt.PluginSessionRouter.Create(context.Background(), observer.ID(), pluginhost.SessionCreateParams{
				RequestID: "create", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh,
			})
			if err != nil {
				t.Fatal(err)
			}
			// A shutdown discard can remain retained while the same request retries.
			var discarded pluginhost.AgentTurnLifecycleInput
			if retry {
				discarded = pluginhost.AgentTurnLifecycleInput{
					RequestID: "run", ThreadID: created.SessionID,
					State: pluginhost.TurnLifecycleDiscarded, Retryable: true,
				}
				payload, err := json.Marshal(discarded)
				if err != nil {
					t.Fatal(err)
				}
				if err := session.PutPluginTurnLifecycleOutbox(rt.SessionDir, observer.ID(), discarded.RequestID, payload); err != nil {
					t.Fatal(err)
				}
			}

			sent, err := rt.PluginSessionRouter.Send(context.Background(), observer.ID(), pluginhost.SessionSendParams{
				RequestID: "run", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "work"},
			})
			if err != nil || sent.State != pluginhost.TurnLifecycleRunning {
				t.Fatalf("send = %+v, %v", sent, err)
			}
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("turn did not reach provider")
			}

			// Hold the real terminal notification after execution settles but before the
			// plugin lifecycle is persisted. Inspect must not mistake this live snapshot
			// for the durable result that terminal callers are waiting for.
			releaseOnce.Do(func() { close(release) })
			select {
			case <-out.started:
			case <-time.After(3 * time.Second):
				t.Fatal("turn did not reach its terminal notification")
			}
			snapshot, err := rt.PluginSessionRouter.Inspect(context.Background(), observer.ID(), pluginhost.SessionInspectParams{
				SessionID: created.SessionID, RequestID: "run",
			})
			if err != nil || snapshot.TimedOut || snapshot.Turn == nil || snapshot.Turn.State != pluginhost.TurnLifecycleCompleted {
				t.Fatalf("nonblocking inspect = %+v, %v", snapshot, err)
			}

			timedOut, err := rt.PluginSessionRouter.Inspect(context.Background(), observer.ID(), pluginhost.SessionInspectParams{
				SessionID: created.SessionID, RequestID: "run", Wait: pluginhost.SessionInspectWaitTerminal, TimeoutMS: 20,
			})
			if err != nil || !timedOut.TimedOut || timedOut.Turn == nil || timedOut.Turn.CompletedAt != nil {
				t.Fatalf("timed out inspect = %+v, %v; turn = %+v", timedOut, err, timedOut.Turn)
			}
			if got := pluginTurnWaiterCount(&srv.pluginTurnWaiters, observer.ID(), "run"); got != 0 {
				t.Fatalf("waiters after timeout = %d, want 0", got)
			}

			cancelCtx, cancel := context.WithCancel(context.Background())
			cancelled := make(chan error, 1)
			go func() {
				_, err := rt.PluginSessionRouter.Inspect(cancelCtx, observer.ID(), pluginhost.SessionInspectParams{
					SessionID: created.SessionID, RequestID: "run", Wait: pluginhost.SessionInspectWaitTerminal, TimeoutMS: 2000,
				})
				cancelled <- err
			}()
			waitForPluginTurnWaiters(t, srv, observer.ID(), "run", 1)
			cancel()
			select {
			case err := <-cancelled:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled inspect error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancelled inspect did not return")
			}
			waitForPluginTurnWaiters(t, srv, observer.ID(), "run", 0)

			type inspectResponse struct {
				result pluginhost.SessionInspectResult
				err    error
			}
			completed := make(chan inspectResponse, 1)
			go func() {
				result, err := rt.PluginSessionRouter.Inspect(context.Background(), observer.ID(), pluginhost.SessionInspectParams{
					SessionID: created.SessionID, RequestID: "run", Wait: pluginhost.SessionInspectWaitTerminal, TimeoutMS: 2000,
				})
				completed <- inspectResponse{result: result, err: err}
			}()
			waitForPluginTurnWaiters(t, srv, observer.ID(), "run", 1)
			if retry {
				// Replaying the old shutdown receipt must not settle the live retry.
				srv.notifyPluginTurnLifecycleAsync(observer.ID(), discarded)
				waitForPluginTurnWaiters(t, srv, observer.ID(), "run", 1)
			}
			out.unblock()

			select {
			case response := <-completed:
				if response.err != nil || response.result.TimedOut || response.result.Turn == nil || response.result.Turn.State != pluginhost.TurnLifecycleCompleted || response.result.Turn.FinalOutput != "done" || response.result.Turn.StartedAt == nil || response.result.Turn.CompletedAt == nil {
					t.Fatalf("completed inspect = %+v, %v", response.result, response.err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("terminal lifecycle did not wake inspect")
			}
			select {
			case <-observer.started:
			case <-time.After(time.Second):
				t.Fatal("terminal lifecycle was not delivered to observer")
			}
			observerReleaseOnce.Do(func() { close(observerRelease) })
			waitForPluginTurnWaiters(t, srv, observer.ID(), "run", 0)
		})
	}
}

func TestPluginSessionInspectTimeoutCapAdmitsForegroundBudget(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("done")})
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)

	if maxPluginSessionInspectTimeout < 10*time.Minute {
		t.Fatalf("host inspect cap = %s, must admit the ten-minute foreground budget", maxPluginSessionInspectTimeout)
	}
	_, err := rt.PluginSessionRouter.Inspect(context.Background(), "subagent", pluginhost.SessionInspectParams{
		SessionID: "missing", Wait: pluginhost.SessionInspectWaitTerminal,
		TimeoutMS: int(maxPluginSessionInspectTimeout/time.Millisecond) + 1,
	})
	if err == nil || !strings.Contains(err.Error(), "timeout_ms") {
		t.Fatalf("timeout above host cap returned %v", err)
	}
	_, err = rt.PluginSessionRouter.Inspect(context.Background(), "subagent", pluginhost.SessionInspectParams{
		SessionID: "missing", Wait: pluginhost.SessionInspectWaitTerminal, TimeoutMS: 10 * 60 * 1000,
	})
	if err != nil && strings.Contains(err.Error(), "timeout_ms") {
		t.Fatalf("ten-minute foreground budget was rejected: %v", err)
	}
}

func waitForPluginTurnWaiters(t *testing.T, srv *Server, pluginID, requestID string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := pluginTurnWaiterCount(&srv.pluginTurnWaiters, pluginID, requestID); got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waiter count = %d, want %d", pluginTurnWaiterCount(&srv.pluginTurnWaiters, pluginID, requestID), want)
}

func pluginTurnWaiterCount(hub *pluginTurnWaitHub, pluginID, requestID string) int {
	key := pluginTurnWaitKey{pluginID: strings.TrimSpace(pluginID), requestID: strings.TrimSpace(requestID)}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return len(hub.waiters[key])
}
