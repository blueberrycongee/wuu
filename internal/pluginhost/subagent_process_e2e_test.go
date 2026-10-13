package pluginhost_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

// Keep real child waits blocked until the status query has returned. This
// reproduces the desktop polling a plugin while a foreground tool is in flight.
type foregroundRouter struct {
	mu          sync.Mutex
	stored      *string
	next        int
	completions int
	waiting     chan string
	release     chan struct{}
}

func (r *foregroundRouter) RouteServiceCall(ctx context.Context, _ string, call pluginhost.ServiceCallParams) (json.RawMessage, *pluginhost.HostServiceError) {
	var result any = struct{}{}
	switch call.Service {
	case pluginapi.KernelStorageGetService:
		r.mu.Lock()
		result = pluginapi.StorageGetResult{Value: r.stored}
		r.mu.Unlock()
	case pluginapi.KernelStorageSetService:
		var p pluginapi.StorageSetParams
		_ = json.Unmarshal(call.Params, &p)
		r.mu.Lock()
		r.stored = &p.Value
		r.mu.Unlock()
	case pluginapi.KernelStorageKeysService:
		result = map[string]any{"keys": []string{}}
	case pluginapi.KernelSessionCreateService:
		r.mu.Lock()
		r.next++
		id := fmt.Sprintf("child-%d", r.next)
		r.mu.Unlock()
		result = pluginapi.SessionCreateResult{SessionID: id, Created: true}
	case pluginapi.KernelSessionSendService:
		var p pluginapi.SessionSendParams
		_ = json.Unmarshal(call.Params, &p)
		if p.Cause == "subagent.completion" {
			r.mu.Lock()
			r.completions++
			r.mu.Unlock()
		}
		result = pluginapi.SessionSendResult{SessionID: p.SessionID, TurnID: p.SessionID + "-turn", State: "running"}
	case pluginapi.KernelSessionInspectService:
		var p pluginapi.SessionInspectParams
		_ = json.Unmarshal(call.Params, &p)
		r.waiting <- p.SessionID
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, &pluginhost.HostServiceError{Code: "cancelled", Message: ctx.Err().Error()}
		}
		result = pluginapi.SessionInspectResult{Turn: &pluginapi.SessionTurnInspection{RequestID: p.RequestID, TurnID: p.SessionID + "-turn", State: "completed", FinalOutput: "report " + p.SessionID}}
	case pluginapi.KernelSessionListService:
		result = pluginapi.SessionListResult{Sessions: []pluginapi.SessionSummary{{SessionID: "child-1", State: "running"}}}
	default:
		return nil, &pluginhost.HostServiceError{Code: "unexpected", Message: call.Service}
	}
	raw, _ := json.Marshal(result)
	return raw, nil
}

func TestForegroundSpawnsSurviveConcurrentStatusQueries(t *testing.T) {
	router := &foregroundRouter{waiting: make(chan string, 4), release: make(chan struct{}, 4)}
	root := t.TempDir()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for the TypeScript plugin process")
	}
	runtime, err := filepath.Abs("../../internal/plugin/bundled/subagent/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	client, err := pluginhost.Start(context.Background(), pluginhost.ProcessConfig{
		ID: "subagent", Command: node, Args: []string{runtime},
		PluginRoot: root, ProjectRoot: root, WuuHome: t.TempDir(),
		Timeout: time.Second, ServiceRouter: router,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer client.Close(context.Background())
	defer cancel()
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func(i int) {
			result, err := client.ExecuteTool(ctx, pluginhost.ToolExecuteParams{ToolID: "spawn_agent", ToolExecuteInput: pluginhost.ToolExecuteInput{
				ExecutionID: fmt.Sprintf("spawn-%d", i), SessionID: "parent", Arguments: json.RawMessage(`{"description":"review","prompt":"inspect","run_in_background":false}`),
			}})
			if err == nil && (len(result.Result.Content) != 1 || !strings.Contains(result.Result.Content[0].Text, "report child-")) {
				err = fmt.Errorf("missing foreground report: %+v", result)
			}
			results <- err
		}(i)
	}
	for i := 0; i < 4; i++ {
		select {
		case <-router.waiting:
		case <-time.After(5 * time.Second):
			t.Fatal("foreground spawn did not reach child wait")
		}
		_, err := client.InvokeCapability(context.Background(), pluginhost.CapabilityInvokeParams{Capability: "plugin.client.request", Input: json.RawMessage(`{"method":"status.list","input":{"parent_session_id":"parent"}}`)})
		if err != nil {
			t.Fatalf("status query during foreground wait: %v; plugin=%+v", err, client.Status())
		}
		router.release <- struct{}{}
	}
	for i := 0; i < 4; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("foreground result missing")
		}
	}
	// Terminal observations remain ordered behind foreground execution and
	// must not deliver a second copy of results already returned by the tool.
	router.mu.Lock()
	stored := *router.stored
	router.mu.Unlock()
	var index struct {
		Records []struct {
			SessionID string `json:"session_id"`
			RequestID string `json:"request_id"`
		} `json:"records"`
	}
	if err := json.Unmarshal([]byte(stored), &index); err != nil {
		t.Fatal(err)
	}
	for _, record := range index.Records {
		input, _ := json.Marshal(pluginapi.TurnLifecycleInput{ThreadID: record.SessionID, RequestID: record.RequestID, State: "completed", FinalOutput: "report"})
		if _, err := client.InvokeCapability(context.Background(), pluginhost.CapabilityInvokeParams{Capability: "agent.turn.lifecycle", Input: input}); err != nil {
			t.Fatal(err)
		}
	}
	router.mu.Lock()
	completions := router.completions
	router.mu.Unlock()
	if completions != 0 {
		t.Fatalf("foreground reports delivered again: %d", completions)
	}
	if client.Status().State != pluginhost.StateActive {
		t.Fatalf("plugin failed: %+v", client.Status())
	}
}
