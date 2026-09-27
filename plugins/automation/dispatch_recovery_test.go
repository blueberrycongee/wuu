package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

type dispatchRecoveryHost struct {
	testHost
	beforeCall         func(string, any)
	sendError          error
	sessionUnavailable bool
}

func (h *dispatchRecoveryHost) CallHost(ctx context.Context, method string, params, result any) error {
	if h.beforeCall != nil {
		h.beforeCall(method, params)
	}
	if h.sessionUnavailable && (method == pluginapi.HostServiceSessionCreate || method == pluginapi.HostServiceSessionSend) {
		return errors.New("session service is unavailable")
	}
	if method == pluginapi.HostServiceSessionSend && h.sendError != nil {
		return h.sendError
	}
	return h.testHost.CallHost(ctx, method, params, result)
}

func TestAutomationRestartDoesNotRetryRecordedFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	init := pluginapi.InitializeParams{WorkspaceID: "workspace-one", ProjectRoot: "/workspace/one"}
	host := &dispatchRecoveryHost{sendError: errors.New("session does not exist")}
	first := &controller{now: func() time.Time { return now }}
	if err := first.prepare(ctx, host, init); err != nil {
		t.Fatal(err)
	}
	task, err := first.add(ctx, mutationInput{Prompt: "Review", Schedule: "1 9 * * *", Timezone: "UTC", Mode: "thread_heartbeat", HeartbeatThreadID: "removed-session", Durable: boolPointer(true)})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	first.fire(ctx, task, now)
	// Restart after fire returns, before fireDue consumes the occurrence.
	restored := &controller{now: func() time.Time { return now }}
	if err := restored.prepare(ctx, host, init); err != nil {
		t.Fatal(err)
	}
	host.sendError = nil
	restored.fireDue(ctx)
	if len(host.sends) != 0 {
		t.Fatalf("a failed one-shot was retried after restart: %+v", host.sends)
	}
	runs := restored.snapshotRuns()
	if len(runs) != 1 || runs[0].Status != "failed" || runs[0].CompletedAt == nil || runs[0].Error != "session does not exist" {
		t.Fatalf("failure did not survive restart: %+v", runs)
	}
	if tasks := restored.snapshotTasks(); len(tasks) != 0 {
		t.Fatalf("failed one-shot remains scheduled: %+v", tasks)
	}
}

func TestAutomationDispatchPreservesAnEditedNextOccurrence(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	host := &dispatchRecoveryHost{}
	c := &controller{now: func() time.Time { return now }}
	if err := c.prepare(ctx, host, pluginapi.InitializeParams{WorkspaceID: "workspace-one", ProjectRoot: "/workspace/one"}); err != nil {
		t.Fatal(err)
	}
	task, err := c.add(ctx, mutationInput{Prompt: "Review", Schedule: "1 9 * * *", Timezone: "UTC", Mode: "new_thread", Durable: boolPointer(true)})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	var edited Task
	host.beforeCall = func(method string, _ any) {
		if method == pluginapi.HostServiceSessionSend {
			var err error
			edited, err = c.update(ctx, mutationInput{ID: task.ID, Schedule: "0 12 * * *"})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	c.fireDue(ctx)
	tasks := c.snapshotTasks()
	if len(tasks) != 1 || !tasks[0].NextRunAt.Equal(edited.NextRunAt) {
		t.Fatalf("dispatch consumed the newly edited occurrence: %+v", tasks)
	}
}

func TestAutomationRestartsAnUnconsumedOccurrenceWithoutDuplicatingRuns(t *testing.T) {
	for _, mode := range []string{"new_thread", "thread_heartbeat"} {
		for _, recurring := range []bool{false, true} {
			for _, boundary := range []string{"before_send", "after_send", "before_consumption_save"} {
				t.Run(fmt.Sprintf("%s/recurring=%t/%s", mode, recurring, boundary), func(t *testing.T) {
					ctx := context.Background()
					now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
					init := pluginapi.InitializeParams{WorkspaceID: "workspace-one", ProjectRoot: "/workspace/one"}
					host := &dispatchRecoveryHost{}
					first := &controller{now: func() time.Time { return now }}
					if err := first.prepare(ctx, host, init); err != nil {
						t.Fatal(err)
					}
					task, err := first.add(ctx, mutationInput{Prompt: "Review", Schedule: "1 9 * * *", Timezone: "UTC", Mode: mode, HeartbeatThreadID: "parent-session", Recurring: boolPointer(recurring), Durable: boolPointer(true)})
					if err != nil {
						t.Fatal(err)
					}
					now = now.Add(time.Minute)
					crashed := false
					host.beforeCall = func(method string, params any) {
						shouldCrash := boundary == "before_send" && method == pluginapi.HostServiceSessionSend
						if method == pluginapi.HostServiceStorageSet {
							encoded, _ := json.Marshal(params)
							var input struct{ Value string }
							if err := json.Unmarshal(encoded, &input); err != nil {
								t.Fatal(err)
							}
							var state persistedState
							if err := json.Unmarshal([]byte(input.Value), &state); err != nil {
								t.Fatal(err)
							}
							if boundary == "after_send" && len(state.Runs) > 0 && state.Runs[0].Status == "running" {
								shouldCrash = true
							}
							if boundary == "before_consumption_save" && (len(state.Tasks) == 0 || state.Tasks[0].NextRunAt.After(now)) {
								shouldCrash = true
							}
						}
						if shouldCrash {
							panic("simulated process exit")
						}
					}
					func() {
						defer func() {
							if value := recover(); value != nil {
								if value != "simulated process exit" {
									panic(value)
								}
								crashed = true
							}
						}()
						first.fireDue(ctx)
					}()
					if !crashed {
						t.Fatal("dispatch did not reach the crash boundary")
					}
					host.beforeCall = nil
					restored := &controller{now: func() time.Time { return now }}
					if err := restored.prepare(ctx, host, init); err != nil {
						t.Fatal(err)
					}
					host.sessionUnavailable = boundary == "before_consumption_save"
					restored.fireDue(ctx)
					host.sessionUnavailable = false
					restored.fireDue(ctx)
					runs := restored.snapshotRuns()
					if len(runs) != 1 || runs[0].Status != "running" || runs[0].SessionID == "" || runs[0].TurnID == "" {
						t.Fatalf("one occurrence must recover as one running record: %+v", runs)
					}
					if len(host.sends) == 0 {
						t.Fatal("the durable occurrence was lost without dispatch")
					}
					for _, send := range host.sends {
						if send.RequestID != runs[0].RequestID {
							t.Fatalf("recovery changed the dispatch identity: %+v", host.sends)
						}
					}
					tasks := restored.snapshotTasks()
					if recurring {
						if len(tasks) != 1 || tasks[0].ID != task.ID || !tasks[0].NextRunAt.After(now) {
							t.Fatalf("recurring occurrence was not consumed: %+v", tasks)
						}
					} else if len(tasks) != 0 {
						t.Fatalf("one-shot occurrence was not consumed: %+v", tasks)
					}
				})
			}
		}
	}
}
