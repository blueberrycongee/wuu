package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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

func TestServerActivityInputPausesUntilUserContinuation(t *testing.T) {
	for _, kind := range []activity.Kind{activity.KindBrowser, activity.KindCUA} {
		for _, controlMethod := range []string{MethodActivityTakeover, MethodActivityStop, MethodTurnInterrupt} {
			for _, method := range []string{MethodTurnStart, MethodTurnQueue, MethodTurnSteer} {
				t.Run(string(kind)+"/"+controlMethod+"/"+method, func(t *testing.T) {
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
						if fields != "" {
							fields = "," + fields
						}
						raw := fmt.Sprintf(`{"id":%q,"method":%q,"params":{"thread_id":%q%s}}`, id, method, threadID, fields)
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
					options := activity.StartOptions{ThreadID: threadID, Workdir: rt.RootDir, PluginID: string(kind), Kind: kind, Target: "tab-live"}
					current, oldLease, err := rt.ActivityRegistry.Acquire(options)
					if err != nil {
						t.Fatal(err)
					}
					siblingKind := activity.KindCUA
					if kind == activity.KindCUA {
						siblingKind = activity.KindBrowser
					}
					siblingOptions := activity.StartOptions{ThreadID: threadID, Workdir: rt.RootDir, PluginID: string(siblingKind), Kind: siblingKind, Target: "sibling"}
					sibling, siblingLease, err := rt.ActivityRegistry.Acquire(siblingOptions)
					if err != nil {
						t.Fatal(err)
					}
					for _, lease := range []activity.Lease{oldLease, siblingLease} {
						if _, err := rt.ActivityRegistry.UpdateWithLease(lease, activity.UpdateOptions{Preview: "file:///previous-preview.png", ProcessID: 41, WindowID: 42}); err != nil {
							t.Fatal(err)
						}
					}
					expectedPauseError := activity.ErrControlRevoked
					if controlMethod == MethodActivityStop {
						expectedPauseError = activity.ErrStopped
					}
					if method == MethodTurnSteer {
						request("queue", MethodTurnQueue, `"prompt":"continue","client_id":"held"`)
					}
					controlFields := fmt.Sprintf(`"activity_id":%q,"execution_id":%q`, current.ID, current.ExecutionID)
					if controlMethod == MethodTurnInterrupt {
						controlFields = ""
					}
					request("input", controlMethod, controlFields)
					waitForMethod(t, out, NotificationTurnError)
					if _, _, err := rt.ActivityRegistry.Acquire(options); !errors.Is(err, expectedPauseError) {
						t.Fatalf("activity after input = %v", err)
					}
					if controlMethod == MethodTurnInterrupt {
						for _, interrupted := range rt.ActivityRegistry.List(threadID) {
							if interrupted.Controller != activity.ControllerNone || interrupted.State != activity.StateActive {
								t.Fatalf("ordinary interruption invented manual ownership: %+v", interrupted)
							}
						}
					}
					if err := rt.ActivityRegistry.CheckControl(threadID, sibling.ID, siblingLease.Token); !errors.Is(err, activity.ErrControlRevoked) {
						t.Fatalf("sibling activity retained control: %v", err)
					}
					if _, _, err := rt.ActivityRegistry.Acquire(siblingOptions); !errors.Is(err, expectedPauseError) {
						t.Fatalf("sibling backend resumed task: %v", err)
					}
					close(client.release)
					// A queued extension turn can run, but must not regain local UI input.
					if _, err := srv.startQueuedTurn(context.Background(), threadID, queuedTurn{
						id: "background", msg: providers.ChatMessage{Role: "user", Content: "background update", Origin: "plugin"},
					}); err != nil {
						t.Fatal(err)
					}
					waitForTurnCompletedCountForThread(t, out, threadID, 1)
					if _, _, err := rt.ActivityRegistry.Acquire(options); !errors.Is(err, expectedPauseError) {
						t.Fatalf("background turn resumed activity: %v", err)
					}
					fields := `"prompt":"continue"`
					if method != MethodTurnStart {
						fields += `,"client_id":"held"`
					}
					client.started = make(chan struct{})
					client.release = make(chan struct{})
					client.once = sync.Once{}
					request("continue", method, fields)
					select {
					case <-client.started:
					case <-time.After(3 * time.Second):
						t.Fatal("continuation did not start")
					}
					for _, pending := range rt.ActivityRegistry.List(threadID) {
						if pending.Controller != activity.ControllerNone {
							t.Fatalf("text-only admission resumed UI control: %+v", pending)
						}
					}
					if _, err := rt.ActivityRegistry.Stop(threadID, current.ID, current.ExecutionID); !errors.Is(err, activity.ErrControlRevoked) {
						t.Fatalf("old idle activity stopped a newer text-only turn: %v", err)
					}
					for _, freshOptions := range []activity.StartOptions{options, siblingOptions} {
						freshOptions.Target = "next-" + freshOptions.Target
						var activation activity.Session
						unsubscribe := rt.ActivityRegistry.Subscribe(func(event activity.Event) {
							if event.Type == activity.EventControlChanged && event.Activity.Kind == freshOptions.Kind && event.Activity.Controller == activity.ControllerAgent {
								activation = event.Activity
							}
						})
						_, _, err := rt.ActivityRegistry.Acquire(freshOptions)
						unsubscribe()
						if err != nil {
							t.Fatalf("new execution could not acquire UI control: %v", err)
						}
						if activation.Target != freshOptions.Target || activation.Preview != "" || activation.ProcessID != 0 || activation.WindowID != 0 {
							t.Fatalf("first activation published the previous target or preview identity: %+v", activation)
						}
					}
					close(client.release)
					waitForTurnCompletedCountForThread(t, out, threadID, 2)
					if _, _, err := rt.ActivityRegistry.Acquire(options); !errors.Is(err, activity.ErrControlRevoked) {
						t.Fatalf("completed execution retained input authority: %v", err)
					}
					for _, resumed := range rt.ActivityRegistry.List(threadID) {
						if resumed.Controller != activity.ControllerNone || resumed.State != activity.StateActive || resumed.ExecutionID == "" || resumed.ExecutionID == current.ExecutionID {
							t.Fatalf("continuation did not finish a fresh execution: %+v", resumed)
						}
					}
					if _, err := rt.ActivityRegistry.Stop(threadID, current.ID, current.ExecutionID); !errors.Is(err, activity.ErrControlRevoked) {
						t.Fatalf("stale desktop action reached newer execution: %v", err)
					}
					if err := rt.ActivityRegistry.CheckControl(threadID, current.ID, oldLease.Token); !errors.Is(err, activity.ErrControlRevoked) {
						t.Fatalf("old activity action regained control: %v", err)
					}
				})
			}
		}
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
