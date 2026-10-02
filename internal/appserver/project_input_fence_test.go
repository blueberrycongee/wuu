//go:build project_agent

package appserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// A prompt hook is allowed to finish after another session has been stopped.
// Its delayed admission must recheck both sender and recipient fences at the
// durable append, while an unchanged fence must still admit the message once.
func TestProjectPeerAdmissionRechecksControlsAfterPromptHook(t *testing.T) {
	for _, tc := range []struct {
		name         string
		workerTarget bool
		stopSender   bool
		stopTarget   bool
	}{
		{name: "sender_stopped_before_lead_admission", stopSender: true},
		{name: "sender_stopped_before_worker_admission", workerTarget: true, stopSender: true},
		{name: "target_stopped_before_worker_admission", workerTarget: true, stopTarget: true},
		{name: "unchanged_controls_admit_once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, client, calls, rt := newProjectFixture(t)
			lead := startProject(t, client, "Peer admission fence")
			create := func(id string) Thread {
				t.Helper()
				brief := "Prepare " + id
				result, err := srv.projectSessionHandler(lead.ID)(context.Background(), id, tools.ProjectSessionRequest{
					Action: "create", Prompt: brief, Workspace: "shared",
				})
				if err != nil {
					t.Fatal(err)
				}
				calls.next(t, brief).response <- providersResponse("Member ready.")
				member := waitForThread(t, srv, result.(projectSessionView).SessionID, func(th Thread) bool {
					return th.Status == ThreadStatusIdle && th.LatestCompletedTurnID != ""
				})
				settleCoordinator(t, srv, calls, lead.ID, "Member ready.", "Ready.", projectResultClientID(member.ID, member.LatestCompletedTurnID))
				return member
			}
			sender := create("sender")
			target := lead
			if tc.workerTarget {
				target = create("receiver")
			}

			entered, released := make(chan struct{}, 1), make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(released) }) }
			gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case entered <- struct{}{}:
				default:
				}
				select {
				case <-released:
					_, _ = w.Write([]byte("{}"))
				case <-r.Context().Done():
				}
			}))
			defer gate.Close()
			defer release()
			rt.HookDispatcher.Replace(hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
				hooks.UserPromptSubmit: {{Command: fmt.Sprintf(`node -e "fetch('%s').then(r=>r.text()).then(t=>process.stdout.write(t))"`, gate.URL)}},
			})))
			request := tools.ProjectSessionRequest{Action: "message", SessionID: target.ID, Prompt: "Delayed peer instruction", Wake: true}
			handler := srv.projectSessionHandler(sender.ID)
			if _, err := handler(context.Background(), "delayed-peer", request); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(gatedProviderTimeout):
				t.Fatal("prompt hook did not reach the admission gate")
			}
			if tc.stopSender || tc.stopTarget {
				stopped := sender
				if tc.stopTarget {
					stopped = target
				}
				client.rpc(t, MethodTurnInterrupt, TurnInterruptParams{ThreadID: stopped.ID}, nil)
				control, ok, err := session.ReadControl(rt.SessionDir, stopped.ID)
				if err != nil || !ok || control.Revision <= stopped.SessionControl.Revision {
					t.Fatalf("stop did not commit its fence: %+v, %v", control, err)
				}
			}
			release()
			// The inbox mutex joins the already-started admission without relying
			// on elapsed time. A second drain also settles any revoked dispatch.
			srv.drainSessionInbox(target.ID)
			clientID := "project-message:" + sender.ID + ":delayed-peer"
			ids := deliveredClientIDs(t, rt, target.ID, clientID)
			if tc.stopSender || tc.stopTarget {
				if len(ids) != 0 {
					t.Fatalf("stopped control was bypassed after the prompt hook: %v", ids)
				}
				calls.assertIdle(t)
				return
			}
			if len(ids) != 1 {
				t.Fatalf("unchanged control deliveries = %v", ids)
			}
			if _, err := handler(context.Background(), "delayed-peer", request); err != nil {
				t.Fatal(err)
			}
			srv.drainSessionInbox(target.ID)
			calls.next(t, request.Prompt).response <- providersResponse("Peer instruction handled.")
			waitForThread(t, srv, target.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
			if ids := deliveredClientIDs(t, rt, target.ID, clientID); len(ids) != 1 {
				t.Fatalf("replayed unchanged input was duplicated: %v", ids)
			}
			calls.assertIdle(t)
		})
	}
}
