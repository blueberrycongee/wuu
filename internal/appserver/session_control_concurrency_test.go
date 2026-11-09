package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

// A provider can serve independent sessions concurrently. A slow control read
// must preserve its own session's fence without blocking another session's
// durable send acknowledgement or active-turn cancellation.
func TestSessionControlProviderWaitIsIsolatedBySession(t *testing.T) {
	for _, method := range []string{MethodTurnQueue, MethodTurnInterrupt} {
		t.Run(method, func(t *testing.T) {
			t.Setenv("WUU_RPC_TIMING", "1")
			rt := newTestRuntime(t, &fakeClient{})
			model := newBlockingStreamClient("done")
			rt.StreamRunner.Client = model
			attachTestModelGeneration(t, rt)
			provider := session.NewService("control-isolation-session", rt.Documents)
			if err := provider.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = provider.Close(context.Background()) })
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			var gate atomic.Bool
			rt.Sessions = session.NewClient(context.Background(), func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				var params struct {
					Args []json.RawMessage `json:"args"`
				}
				_ = json.Unmarshal(raw, &params)
				if method == "ReadControl" && len(params.Args) > 0 && string(params.Args[0]) == `"blocked"` && gate.CompareAndSwap(true, false) {
					close(entered)
					<-release
				}
				return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "control-isolation", Method: method, Params: raw})
			})
			for _, id := range []string{"blocked", "independent"} {
				if _, err := rt.Sessions.CreateWithMetadata(id, rt.RootDir); err != nil {
					t.Fatal(err)
				}
			}
			initial, err := rt.Sessions.ChangeControl("blocked", "plugin:fixture", session.ControlActive, 0)
			if err != nil {
				t.Fatal(err)
			}
			out, logs := &lockedBuffer{}, &lockedBuffer{}
			s := New(rt, out)
			t.Cleanup(s.Close)
			t.Cleanup(func() { close(model.release) })
			t.Cleanup(unblock)
			s.rpcTiming.logf = func(format string, args ...any) { fmt.Fprintf(logs, format+"\n", args...) }
			dispatchPayload(t, s, "start", MethodTurnStart, TurnStartParams{ThreadID: "independent", Prompt: "synthetic active turn"})
			if response := responseByID(t, parseOutput(t, out.String()), "start"); response["error"] != nil {
				t.Fatal(response)
			}
			select {
			case <-model.started:
			case <-time.After(gatedProviderTimeout):
				t.Fatal("synthetic model did not start")
			}
			binding, err := rt.RetainState()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = binding.Close() })
			gate.Store(true)
			controlled := make(chan error, 1)
			controlDone := make(chan struct{})
			t.Cleanup(func() { unblock(); <-controlDone })
			go func() {
				defer close(controlDone)
				_, err := s.controlPluginSession(binding, context.Background(), "fixture", pluginhost.SessionControlParams{SessionID: "blocked", State: session.ControlPaused, Revision: initial.Revision})
				controlled <- err
			}()
			select {
			case <-entered:
			case <-time.After(gatedProviderTimeout):
				t.Fatal("plugin control did not enter provider")
			}
			// The stale same-session mutation must remain behind the first one;
			// after release it must lose against the newly committed revision.
			sameSession := make(chan error, 1)
			sameDone := make(chan struct{})
			t.Cleanup(func() { unblock(); <-sameDone })
			go func() {
				defer close(sameDone)
				_, err := s.controlPluginSession(binding, context.Background(), "fixture", pluginhost.SessionControlParams{SessionID: "blocked", State: session.ControlReleased, Revision: initial.Revision})
				sameSession <- err
			}()
			params := any(TurnInterruptParams{ThreadID: "independent"})
			if method == MethodTurnQueue {
				params = TurnQueueParams{ThreadID: "independent", Prompt: "synthetic queued message", ClientID: "independent-input"}
			}
			wire := string(mustJSON(Request{ID: mustJSON("independent"), Method: method, Params: mustJSON(params)})) + "\n"
			done := make(chan error, 1)
			scannerDone := make(chan struct{})
			t.Cleanup(func() { unblock(); <-scannerDone })
			started := time.Now()
			go func() {
				defer close(scannerDone)
				done <- runStdioScanner(context.Background(), s, strings.NewReader(wire))
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(gatedProviderTimeout):
				t.Fatal("independent session waited for another session's control provider")
			}
			t.Logf("%s acknowledged after %s while another session's provider remained blocked", method, time.Since(started))
			if response := responseByID(t, parseOutput(t, out.String()), "independent"); response["error"] != nil {
				t.Fatal(response)
			}
			select {
			case err := <-sameSession:
				t.Fatalf("same-session control passed an unfinished earlier mutation: %v", err)
			default:
			}
			if method == MethodTurnQueue {
				held, err := rt.Sessions.LoadHeldUserWork("independent")
				if err != nil || len(held) != 1 {
					t.Fatalf("send response did not preserve durable input: %+v, %v", held, err)
				}
			} else if !strings.Contains(logs.String(), `"phase":"interrupt_cancel_called"`) {
				t.Fatal("interrupt acknowledged without cancelling the active turn")
			}
			for _, phase := range []string{"session_control_mutex_wait", "session_control_read"} {
				if !strings.Contains(logs.String(), `"phase":"`+phase+`"`) {
					t.Fatalf("missing control timing phase %s", phase)
				}
			}
			unblock()
			if err := <-controlled; err != nil {
				t.Fatal(err)
			}
			if err := <-sameSession; !errors.Is(err, session.ErrControlChanged) {
				t.Fatalf("stale same-session mutation bypassed the revision fence: %v", err)
			}
			if _, err := s.pluginSessionControl(binding, "fixture", "blocked", initial.Revision); !errors.Is(err, session.ErrControlChanged) {
				t.Fatalf("old plugin input authority survived control change: %v", err)
			}
		})
	}
}
