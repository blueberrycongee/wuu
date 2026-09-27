package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestServerActivityLifecycleRequestsAndNotifications(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.ActivityRegistry = activity.NewRegistry()
	out := &lockedBuffer{}
	srv := New(rt, out)

	defer srv.Close()
	srv.threads["thread-1"] = newThreadState("thread-1", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	started, _, err := rt.ActivityRegistry.Start(activity.StartOptions{
		ID:       "activity-1",
		Kind:     activity.KindBrowser,
		ThreadID: "thread-1",
		Workdir:  rt.RootDir,
		PluginID: "browser-use",
		Target:   "https://example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	interaction := &activity.Interaction{Kind: "click", X: 0.25, Y: 0.75, Revision: 42}
	if _, err := rt.ActivityRegistry.Update("thread-1", started.ID, activity.UpdateOptions{State: activity.StateActive, Preview: "preview://activity-1", Interaction: interaction}); err != nil {
		t.Fatal(err)
	}

	requests := []string{
		`{"id":"list","method":"activity/list","params":{"thread_id":"thread-1"}}`,
		`{"id":"takeover","method":"activity/takeover","params":{"thread_id":"thread-1","activity_id":"activity-1"}}`,
		`{"id":"release","method":"activity/release","params":{"thread_id":"thread-1","activity_id":"activity-1"}}`,
		`{"id":"stop","method":"activity/stop","params":{"thread_id":"thread-1","activity_id":"activity-1"}}`,
	}
	for _, raw := range requests {
		if err := srv.handleLine(context.Background(), []byte(raw)); err != nil {
			t.Fatalf("handleLine %s: %v", raw, err)
		}
	}

	messages := parseOutput(t, out.String())
	listed := remarshal[ActivityListResult](t, responseByID(t, messages, "list")["result"])
	if len(listed.Activities) != 1 || listed.Activities[0].ID != started.ID || listed.Activities[0].Workdir != rt.RootDir {
		t.Fatalf("activity/list = %+v", listed)
	}
	if listed.Activities[0].Interaction == nil || listed.Activities[0].Interaction.Revision != 42 {
		t.Fatalf("activity/list interaction = %+v", listed.Activities[0].Interaction)
	}
	takeover := remarshal[ActivityActionResult](t, responseByID(t, messages, "takeover")["result"])
	if takeover.Activity.Controller != string(activity.ControllerUser) || takeover.Activity.State != string(activity.StateUserControlled) {
		t.Fatalf("takeover = %+v", takeover)
	}
	release := remarshal[ActivityReleaseResult](t, responseByID(t, messages, "release")["result"])
	if release.Activity.Controller != string(activity.ControllerAgent) || strings.TrimSpace(release.LeaseToken) == "" {
		t.Fatalf("release = %+v", release)
	}
	stopped := remarshal[ActivityActionResult](t, responseByID(t, messages, "stop")["result"])
	if stopped.Activity.State != string(activity.StateStopped) || stopped.Activity.Controller != string(activity.ControllerNone) {
		t.Fatalf("stop = %+v", stopped)
	}

	for _, method := range []string{
		NotificationActivityStarted,
		NotificationActivityUpdated,
		NotificationActivityControlChanged,
		NotificationActivityStopped,
	} {
		notification := notificationByMethod(t, messages, method)
		payload := remarshal[ActivitySession](t, notification["params"])
		if payload.Workdir != rt.RootDir || payload.ThreadID != "thread-1" || payload.ID != started.ID || payload.Kind != string(activity.KindBrowser) {
			t.Fatalf("%s payload = %+v", method, payload)
		}
		if method == NotificationActivityUpdated && (payload.Interaction == nil || payload.Interaction.Kind != "click") {
			t.Fatalf("%s interaction = %+v", method, payload.Interaction)
		}
	}
}

func TestServerBrowserInputPausesUntilUserContinuation(t *testing.T) {
	for _, method := range []string{MethodTurnStart, MethodTurnQueue, MethodTurnSteer} {
		t.Run(method, func(t *testing.T) {
			client := newBlockingStreamClient("done")
			rt := newTestRuntime(t, &fakeClient{})
			rt.StreamRunner.Client = client
			rt.ActivityRegistry = activity.NewRegistry()
			out := &lockedBuffer{}
			srv := New(rt, out)
			defer srv.Close()
			if err := srv.handleLine(context.Background(), []byte(`{"id":"thread","method":"thread/start"}`)); err != nil {
				t.Fatal(err)
			}
			threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "thread")["result"]).Thread.ID
			request := func(id, method, fields string) {
				t.Helper()
				raw := fmt.Sprintf(`{"id":%q,"method":%q,"params":{"thread_id":%q,%s}}`, id, method, threadID, fields)
				if err := srv.handleLine(context.Background(), []byte(raw)); err != nil {
					t.Fatal(err)
				}
				if response := responseByID(t, parseOutput(t, out.String()), id); response["error"] != nil {
					t.Fatalf("%s: %+v", method, response)
				}
			}
			request("start", MethodTurnStart, `"prompt":"browse"`)
			select {
			case <-client.started:
			case <-time.After(3 * time.Second):
				t.Fatal("turn did not start")
			}
			options := activity.StartOptions{ThreadID: threadID, Workdir: rt.RootDir, PluginID: "browser", Kind: activity.KindBrowser, Target: "tab-live"}
			current, oldLease, err := rt.ActivityRegistry.Acquire(options)
			if err != nil {
				t.Fatal(err)
			}
			if method == MethodTurnSteer {
				request("queue", MethodTurnQueue, `"prompt":"continue","client_id":"held"`)
			}
			request("input", MethodActivityTakeover, fmt.Sprintf(`"activity_id":%q`, current.ID))
			waitForMethod(t, out, NotificationTurnError)
			if _, _, err := rt.ActivityRegistry.Acquire(options); !errors.Is(err, activity.ErrControlRevoked) {
				t.Fatalf("browser after input = %v", err)
			}
			close(client.release)
			// A queued extension turn can run, but must not regain browser input.
			if _, err := srv.startQueuedTurn(context.Background(), threadID, queuedTurn{
				id: "background", msg: providers.ChatMessage{Role: "user", Content: "background update", Origin: "plugin"},
			}); err != nil {
				t.Fatal(err)
			}
			waitForTurnCompletedCountForThread(t, out, threadID, 1)
			if _, _, err := rt.ActivityRegistry.Acquire(options); !errors.Is(err, activity.ErrControlRevoked) {
				t.Fatalf("background turn resumed browser: %v", err)
			}
			fields := `"prompt":"continue"`
			if method != MethodTurnStart {
				fields += `,"client_id":"held"`
			}
			request("continue", method, fields)
			waitForTurnCompletedCountForThread(t, out, threadID, 2)
			resumed, nextLease, err := rt.ActivityRegistry.Acquire(options)
			if err != nil || resumed.ID != current.ID || nextLease.Token == oldLease.Token {
				t.Fatalf("browser after continuation = %+v / %+v, %v", resumed, nextLease, err)
			}
			if err := rt.ActivityRegistry.CheckControl(threadID, current.ID, oldLease.Token); !errors.Is(err, activity.ErrControlRevoked) {
				t.Fatalf("old browser action regained control: %v", err)
			}
		})
	}
}

func TestServerActivityRejectsCrossThreadControl(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.ActivityRegistry = activity.NewRegistry()
	if _, _, err := rt.ActivityRegistry.Start(activity.StartOptions{ID: "activity-1", Kind: activity.KindCUA, ThreadID: "thread-1", Workdir: rt.RootDir}); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"takeover","method":"activity/takeover","params":{"thread_id":"thread-2","activity_id":"activity-1"}}`)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "takeover")
	if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), activity.ErrThreadMismatch.Error()) {
		t.Fatalf("cross-thread response = %+v", response)
	}
}
