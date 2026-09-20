package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

func TestSundayPublicCreate(t *testing.T) {
	for _, entry := range []struct{ method, field string }{
		{"cron", "cron"}, {"automation.create", "cron"}, {"automation.create", "schedule"},
	} {
		for _, tc := range []struct {
			dow  string
			days []time.Weekday
		}{
			{"0", []time.Weekday{time.Sunday}},
			{"7", []time.Weekday{time.Sunday}},
			{"6-7", []time.Weekday{time.Saturday, time.Sunday}},
			{"6,7", []time.Weekday{time.Saturday, time.Sunday}},
			{"5-7/2", []time.Weekday{time.Friday, time.Sunday}},
			{"6-7/2", []time.Weekday{time.Saturday}},
		} {
			for _, recurring := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%s/recurring=%t", entry.method, entry.field, tc.dow, recurring), func(t *testing.T) {
					host := &scheduleStorageHost{}
					params := pluginapi.InitializeParams{WorkspaceID: "synthetic", ProjectRoot: t.TempDir()}
					h := Handler()
					if err := h.Initialize(context.Background(), host, params); err != nil {
						t.Fatal(err)
					}
					cron := "0 9 * * " + tc.dow
					before := time.Now()
					task, err := publicScheduleMutation(t, h, host, entry.method, map[string]any{
						"action": "add", entry.field: cron, "timezone": "UTC",
						"prompt": "Synthetic schedule test", "recurring": recurring, "durable": true,
					})
					after := time.Now()
					if err != nil {
						t.Fatal(err)
					}
					assertPublicNextRun(t, task, before, after, tc.days)
					if task.Cron != cron || task.Recurring != recurring {
						t.Fatalf("created task = %+v", task)
					}
					// Initialize only: no timer, real task, or execution service is started.
					reloaded := Handler()
					if err := reloaded.Initialize(context.Background(), host, params); err != nil {
						t.Fatal(err)
					}
					if tasks := publicScheduleTasks(t, reloaded, host); len(tasks) != 1 || tasks[0] != task {
						t.Fatalf("reloaded tasks = %+v; want %+v", tasks, task)
					}
				})
			}
		}
	}
}

func TestSundayPublicUpdateAndInvalidMutations(t *testing.T) {
	host := &scheduleStorageHost{}
	h := Handler()
	if err := h.Initialize(context.Background(), host, pluginapi.InitializeParams{ProjectRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	task, err := publicScheduleMutation(t, h, host, "automation.create", map[string]any{
		"schedule": "0 9 * * 0", "timezone": "UTC", "prompt": "Synthetic update", "durable": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("update", func(t *testing.T) {
		before := time.Now()
		updated, err := publicScheduleMutation(t, h, host, "automation.update", map[string]any{"id": task.ID, "schedule": "0 9 * * 7"})
		after := time.Now()
		if err != nil {
			t.Fatal(err)
		}
		assertPublicNextRun(t, updated, before, after, []time.Weekday{time.Sunday})
		if updated.ID != task.ID || updated.Cron != "0 9 * * 7" {
			t.Fatalf("updated task = %+v", updated)
		}
		task = updated
	})
	for _, method := range []string{"cron", "automation.create", "automation.update"} {
		for _, invalid := range []struct{ cron, zone string }{
			{"0 9 * * 8", "UTC"}, {"0 9 * * 6-8", "UTC"}, {"0 9 * * 7/0", "UTC"}, {"0 9 * * 7", "Invalid/Timezone"},
		} {
			t.Run(method+"/"+invalid.cron+"/"+invalid.zone, func(t *testing.T) {
				state, writes := host.state, host.writes
				_, err := publicScheduleMutation(t, h, host, method, map[string]any{
					"action": "add", "id": task.ID, "cron": invalid.cron, "timezone": invalid.zone, "prompt": "Invalid fixture",
				})
				if err == nil {
					t.Fatal("invalid mutation accepted")
				}
				if host.state != state || host.writes != writes || !reflect.DeepEqual(publicScheduleTasks(t, h, host), []Task{task}) {
					t.Fatal("invalid mutation changed tasks or persisted state")
				}
			})
		}
	}
}

// Handler uses the wall clock. Bracket the call with independent calendar
// expectations so crossing 09:00 during the call does not make the test flaky.
func assertPublicNextRun(t *testing.T, task Task, before, after time.Time, days []time.Weekday) {
	t.Helper()
	next := func(anchor time.Time) time.Time {
		anchor = anchor.UTC()
		candidate := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 9, 0, 0, 0, time.UTC)
		for {
			for _, day := range days {
				if candidate.After(anchor) && candidate.Weekday() == day {
					return candidate
				}
			}
			candidate = candidate.AddDate(0, 0, 1)
		}
	}
	if lo, hi := next(before), next(after); task.NextRunAt.Before(lo) || task.NextRunAt.After(hi) {
		t.Fatalf("next run = %s; want within [%s, %s]", task.NextRunAt, lo, hi)
	}
}

type scheduleStorageHost struct {
	testHost
	writes int
}

func (h *scheduleStorageHost) CallHost(ctx context.Context, method string, params, result any) error {
	switch method {
	case pluginapi.HostServiceStorageGet:
	case pluginapi.HostServiceStorageSet:
		h.writes++
	default:
		return fmt.Errorf("unexpected host service: %s", method)
	}
	return h.testHost.CallHost(ctx, method, params, result)
}

func publicScheduleMutation(t *testing.T, h pluginapi.Handler, host pluginapi.Host, method string, input map[string]any) (Task, error) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if method == "cron" {
		result, err := h.ExecuteTool(context.Background(), host, pluginapi.ToolCall{ToolID: "cron", Arguments: raw})
		if err != nil {
			return Task{}, err
		}
		var response struct{ Task Task }
		if len(result.Content) != 1 {
			t.Fatalf("unexpected tool response: %+v", result)
		}
		err = json.Unmarshal([]byte(result.Content[0].Text), &response)
		return response.Task, err
	}
	envelope, err := json.Marshal(map[string]any{"method": method, "input": json.RawMessage(raw)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.InvokeCapability(context.Background(), host, pluginapi.CapabilityCall{Capability: capabilityClient, Input: envelope})
	if err != nil {
		return Task{}, err
	}
	var response struct{ Result Task }
	err = json.Unmarshal(result, &response)
	return response.Result, err
}

func publicScheduleTasks(t *testing.T, h pluginapi.Handler, host pluginapi.Host) []Task {
	t.Helper()
	raw, err := h.InvokeCapability(context.Background(), host, pluginapi.CapabilityCall{
		Capability: capabilityClient, Input: json.RawMessage(`{"method":"automation.list"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var response struct{ Result struct{ Tasks []Task } }
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	return response.Result.Tasks
}

func TestSundayPersistedTaskRecovery(t *testing.T) {
	for _, repair := range []string{"save", "due"} {
		t.Run(repair, func(t *testing.T) {
			now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
			// A pre-fix weekend task skipped Sunday and persisted next Saturday.
			stale := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
			task := Task{ID: "legacy-weekend", Cron: "0 9 * * 6-7", Timezone: "UTC", Prompt: "Synthetic recovery", Mode: "new_thread", Recurring: true, Durable: true, NextRunAt: stale}
			encoded, err := json.Marshal(persistedState{Tasks: []Task{task}})
			if err != nil {
				t.Fatal(err)
			}
			host := &testHost{state: string(encoded)}
			c := &controller{now: func() time.Time { return now }}
			if err := c.prepare(context.Background(), host, pluginapi.InitializeParams{ProjectRoot: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			if got := c.snapshotTasks(); len(got) != 1 || !got[0].NextRunAt.Equal(stale) {
				t.Fatalf("load must preserve the stored deadline: %+v", got)
			}
			if repair == "save" {
				if _, err := c.invokeCapability(context.Background(), pluginapi.CapabilityCall{
					Capability: capabilityClient, Input: json.RawMessage(`{"method":"automation.update","input":{"id":"legacy-weekend","schedule":"0 9 * * 6-7"}}`),
				}); err != nil {
					t.Fatal(err)
				}
				if got := c.snapshotTasks()[0].NextRunAt; !got.Equal(time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)) {
					t.Fatalf("save did not restore the upcoming Sunday: %s", got)
				}
			}
			// Drive the synchronous dispatcher with a fake clock and in-memory host.
			// No background timer or live session is activated.
			for i := 0; i < 4; i++ {
				task := c.snapshotTasks()[0]
				now = task.NextRunAt
				c.fireDue(context.Background())
				want := now.AddDate(0, 0, 1)
				if now.Weekday() == time.Sunday {
					want = now.AddDate(0, 0, 6)
				}
				got := c.snapshotTasks()[0]
				if got.Paused || !got.NextRunAt.Equal(want) {
					t.Fatalf("recurring next = %+v; want %s", got, want)
				}
				c.fireDue(context.Background())
				if len(host.sends) != i+1 {
					t.Fatalf("duplicate dispatch: sends=%d, want %d", len(host.sends), i+1)
				}
				var saved persistedState
				if err := json.Unmarshal([]byte(host.state), &saved); err != nil {
					t.Fatal(err)
				}
				if len(saved.Tasks) != 1 || !saved.Tasks[0].NextRunAt.Equal(want) {
					t.Fatalf("recomputed deadline was not persisted: %+v", saved.Tasks)
				}
			}
		})
	}
}
