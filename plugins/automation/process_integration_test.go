package automation_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
	"github.com/blueberrycongee/wuu/plugins/automation"
)

func TestAutomationPluginProcessHelper(t *testing.T) {
	if os.Getenv("WUU_AUTOMATION_PLUGIN_TEST_HELPER") != "1" {
		return
	}
	if err := pluginapi.Serve(context.Background(), automation.Handler()); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestAutomationPluginNegotiatesAcrossRealProcessProtocol(t *testing.T) {
	services := &automationTestHostServices{}
	client, err := pluginhost.Start(context.Background(), pluginhost.ProcessConfig{
		ID:            "automation",
		Command:       os.Args[0],
		Args:          []string{"-test.run=^TestAutomationPluginProcessHelper$"},
		Env:           map[string]string{"WUU_AUTOMATION_PLUGIN_TEST_HELPER": "1"},
		Timeout:       5 * time.Second,
		ServiceRouter: services,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	created, err := client.ExecuteTool(context.Background(), pluginhost.ToolExecuteParams{
		ToolID: "cron",
		ToolExecuteInput: pluginhost.ToolExecuteInput{
			ThreadID:  "thread-1",
			Arguments: json.RawMessage(`{"action":"add","title":"Daily review","prompt":"Review open work","cron":"0 9 * * *","timezone":"UTC","mode":"thread_heartbeat","recurring":true,"durable":true}`),
		},
	})
	if err != nil || len(created.Result.Content) != 1 {
		t.Fatalf("create = %+v, err = %v", created, err)
	}
	services.mu.Lock()
	state := services.state
	services.mu.Unlock()
	if state == "" {
		t.Fatal("automation process did not persist its task through host storage")
	}
}

type automationTestHostServices struct {
	mu                 sync.Mutex
	state              string
	sessionUnavailable bool
	sends              int
	saved              chan string
}

func (s *automationTestHostServices) SupportedHostServices() []pluginhost.HostServiceMethod {
	return []pluginhost.HostServiceMethod{
		pluginhost.HostServiceStorageGet,
		pluginhost.HostServiceStorageSet,
		pluginhost.HostServiceSessionCreate,
		pluginhost.HostServiceSessionSend,
	}
}

func (s *automationTestHostServices) HandleHostService(_ context.Context, method pluginhost.HostServiceMethod, raw json.RawMessage) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch method {
	case pluginhost.HostServiceStorageGet:
		if s.state == "" {
			return json.Marshal(pluginhost.StorageGetResult{})
		}
		return json.Marshal(pluginhost.StorageGetResult{Value: &s.state})
	case pluginhost.HostServiceStorageSet:
		var params pluginhost.StorageSetParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		s.state = params.Value
		if s.saved != nil {
			s.saved <- s.state
		}
		return json.RawMessage(`{}`), nil
	case pluginhost.HostServiceSessionCreate:
		if s.sessionUnavailable {
			return nil, errors.New("session service is unavailable")
		}
		return json.Marshal(pluginhost.SessionCreateResult{SessionID: "session-1", Created: true})
	case pluginhost.HostServiceSessionSend:
		if s.sessionUnavailable {
			return nil, errors.New("session service is unavailable")
		}
		s.sends++
		return json.Marshal(pluginhost.SessionSendResult{State: pluginhost.TurnLifecycleQueued, SessionID: "session-1", QueueID: "queue-1"})
	default:
		return nil, errors.New("unsupported host service")
	}
}

func TestAutomationPluginProcessRecoversOverdueTasksAfterServiceReadiness(t *testing.T) {
	for _, mode := range []string{"new_thread", "thread_heartbeat"} {
		for _, recurring := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/recurring=%t", mode, recurring), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				due := time.Now().UTC().Add(-time.Hour)
				task := automation.Task{ID: "overdue", Title: "Review", Prompt: "Review", Cron: "* * * * *", Timezone: "UTC", Mode: mode, HeartbeatThreadID: "session-1", Durable: true, Recurring: recurring, NextRunAt: due}
				encoded, err := json.Marshal(map[string]any{"tasks": []automation.Task{task}})
				if err != nil {
					t.Fatal(err)
				}
				services := &automationTestHostServices{state: string(encoded), sessionUnavailable: true, saved: make(chan string, 16)}
				start := func() *pluginhost.ProcessClient {
					client, err := pluginhost.Start(ctx, pluginhost.ProcessConfig{
						ID: "automation", Command: os.Args[0],
						Args:    []string{"-test.run=^TestAutomationPluginProcessHelper$"},
						Env:     map[string]string{"WUU_AUTOMATION_PLUGIN_TEST_HELPER": "1"},
						Timeout: 5 * time.Second, ServiceRouter: services,
					})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = client.Close(context.Background()) })
					return client
				}
				first := start()
				// Observe the persisted retry state after the unavailable response.
				for {
					select {
					case raw := <-services.saved:
						var state struct {
							Tasks []automation.Task
							Runs  []automation.Run
						}
						if err := json.Unmarshal([]byte(raw), &state); err != nil {
							t.Fatal(err)
						}
						if len(state.Tasks) != 1 || !state.Tasks[0].NextRunAt.Equal(due) {
							t.Fatalf("unavailable host consumed the overdue occurrence: %s", raw)
						}
						if len(state.Runs) != 0 {
							continue
						}
					case <-ctx.Done():
						t.Fatal("plugin did not retain the task after the unavailable response")
					}
					break
				}
				if err := first.Close(ctx); err != nil {
					t.Fatal(err)
				}
				services.mu.Lock()
				services.sessionUnavailable = false
				services.mu.Unlock()
				start()
				for {
					select {
					case raw := <-services.saved:
						var state struct {
							Tasks []automation.Task
							Runs  []automation.Run
						}
						if err := json.Unmarshal([]byte(raw), &state); err != nil {
							t.Fatal(err)
						}
						consumed := len(state.Tasks) == 0
						if recurring {
							consumed = len(state.Tasks) == 1 && len(state.Runs) == 1 && state.Tasks[0].NextRunAt.After(state.Runs[0].TriggeredAt)
						}
						if !consumed {
							continue
						}
						if len(state.Runs) != 1 || state.Runs[0].Status != "queued" || state.Runs[0].SessionID != "session-1" {
							t.Fatalf("recovered run = %s", raw)
						}
					case <-ctx.Done():
						t.Fatal("plugin did not dispatch once the session service became available")
					}
					break
				}
				services.mu.Lock()
				sends := services.sends
				services.mu.Unlock()
				if sends != 1 {
					t.Fatalf("overdue occurrence dispatched %d times", sends)
				}
			})
		}
	}
}

func (s *automationTestHostServices) RouteServiceCall(ctx context.Context, _ string, params pluginhost.ServiceCallParams) (json.RawMessage, *pluginhost.HostServiceError) {
	result, err := s.HandleHostService(ctx, pluginhost.HostServiceMethod(params.Service), params.Params)
	if err != nil {
		return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: err.Error()}
	}
	return result, nil
}
