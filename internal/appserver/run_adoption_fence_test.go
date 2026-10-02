//go:build project_agent

package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Real cross-Server control must distinguish harmless adoption from a stop,
// including ordinary Runs that retained a released control row.
func TestRunSchemaRetryAdoptionFence(t *testing.T) {
	for _, duringHook := range []bool{false, true} {
		for _, released := range []bool{false, true} {
			for _, stopped := range []bool{false, true} {
				t.Run(fmt.Sprintf("hook_%v_released_%v_stop_%v", duringHook, released, stopped), func(t *testing.T) {
					srv, client, calls, rt := newProjectFixture(t)
					lead := startProject(t, client, "Run adoption stop")
					lease, ok, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, lead.ID)
					if err != nil || !ok {
						t.Fatalf("lead lease: %v %v", ok, err)
					}
					defer lease.Release()
					var worker ThreadStartResult
					client.rpc(t, MethodThreadStart, ThreadStartParams{}, &worker)
					id := worker.Thread.ID
					if released {
						old, err := session.ChangeControl(rt.SessionDir, id, "prior-manager", session.ControlActive, 0)
						if err != nil {
							t.Fatal(err)
						}
						if _, err = session.ChangeControl(rt.SessionDir, id, "prior-manager", session.ControlReleased, old.Revision); err != nil {
							t.Fatal(err)
						}
					}
					journal, err := session.NewInferenceJournalRuntime(rt.SessionDir, "run-adoption")
					if err != nil {
						t.Fatal(err)
					}
					defer journal.Close()
					rt.InferenceJournalRuntime = journal
					rt.ActivityRegistry = activity.NewRegistry()
					peer := New(rt, &lockedBuffer{})
					defer peer.Close()
					start := func(prompt string) RunStartResult {
						var result RunStartResult
						client.rpc(t, MethodRunStart, RunStartParams{ThreadID: id, Prompt: prompt, OutputSchema: json.RawMessage(`{"type":"object","required":["ok"]}`), Request: execution.Request{Mode: execution.ModeStart}}, &result)
						return result
					}
					started := start("run-adoption-original")
					first := calls.next(t, "run-adoption-original")
					var release func()
					if duringHook {
						entered, releasedHook := make(chan struct{}, 1), make(chan struct{})
						var once sync.Once
						release = func() { once.Do(func() { close(releasedHook) }) }
						gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							select {
							case entered <- struct{}{}:
							default:
							}
							select {
							case <-releasedHook:
								_, _ = w.Write([]byte("{}"))
							case <-r.Context().Done():
							}
						}))
						defer gate.Close()
						defer release()
						rt.HookDispatcher.Replace(hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
							hooks.UserPromptSubmit: {{Command: fmt.Sprintf(`node -e "fetch('%s').then(r=>r.text()).then(t=>process.stdout.write(t))"`, gate.URL)}},
						})))
						first.response <- providersResponse(`{"wrong":true}`)
						select {
						case <-entered:
						case <-time.After(gatedProviderTimeout):
							t.Fatal("correction did not enter prompt hook")
						}
					}
					if _, err := peer.adoptProjectSession(lead.ID, id); err != nil {
						t.Fatal(err)
					}
					if stopped {
						if err := peer.takeSessionControl(id, session.ControlPaused); err != nil {
							t.Fatal(err)
						}
					}
					if duringHook {
						release()
					} else {
						first.response <- providersResponse(`{"wrong":true}`)
					}
					if !stopped {
						calls.next(t, "run-adoption-original").response <- providersResponse(`{"wrong_again":true}`)
						calls.next(t, "run-adoption-original").response <- providersResponse(`{"ok":true}`)
					}
					waitRun := func(runID string, denyProvider bool) execution.Run {
						deadline := time.Now().Add(gatedProviderTimeout)
						var run execution.Run
						for time.Now().Before(deadline) {
							run, err = srv.runStore.Get(context.Background(), runID)
							if err != nil {
								t.Fatal(err)
							}
							if run.Status.Terminal() {
								break
							}
							if denyProvider {
								select {
								case call := <-calls.provider.calls:
									t.Fatalf("stopped adopted Run invoked correction: %+v", lastUserRequestMessage(call.request))
								default:
								}
							}
							time.Sleep(5 * time.Millisecond)
						}
						return run
					}
					run := waitRun(started.Run.ID, stopped)
					want := execution.StatusCompleted
					turns := 3
					if stopped {
						want = execution.StatusInterrupted
						turns = 1
					}
					if run.Status != want || len(run.Turns) != turns {
						t.Fatalf("Run status=%s turns=%d want %s/%d", run.Status, len(run.Turns), want, turns)
					}
					assertSchemaUserTurns(t, srv, id, turns)
					if stopped {
						fresh := start("run-adoption-fresh")
						calls.next(t, "run-adoption-fresh").response <- providersResponse(`{"wrong":true}`)
						calls.next(t, "run-adoption-fresh").response <- providersResponse(`{"ok":true}`)
						run = waitRun(fresh.Run.ID, false)
						if run.Status != execution.StatusCompleted || len(run.Turns) != 2 {
							t.Fatalf("fresh Run after stop: %+v", run)
						}
					}
					srv.Close()
					calls.assertIdle(t)
				})
			}
		}
	}
}
