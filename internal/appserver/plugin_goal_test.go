package appserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Keep the existing app-server behavioral tests connected to the shipped
// TypeScript executable, including the same JSONL service boundary as production.
func startBundledPluginTestProcess(t *testing.T, id string, router pluginhost.ServiceRouter) *pluginhost.ProcessClient {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("bundled TypeScript plugins require Node.js: ", err)
	}
	root, err := filepath.Abs(filepath.Join("..", "plugin", "bundled", id))
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "runtime.mjs")
	if _, err := os.Stat(entry); err != nil {
		t.Fatal("run node scripts/build-bundled-plugins.mjs before testing: ", err)
	}
	client, err := pluginhost.Start(context.Background(), pluginhost.ProcessConfig{
		ID: id, Command: node, Args: []string{entry}, PluginRoot: root,
		ProjectRoot: t.TempDir(), WuuHome: t.TempDir(), WorkspaceStateDir: t.TempDir(),
		ServiceRouter: router, PrepareOnly: true, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	return client
}

// Exercise the extension's public messages against the real session router and
// settled-turn broadcast; storage writes signal completion without polling.
type goalBridge struct {
	server  *Server
	mu      sync.Mutex
	value   *string
	settled chan struct{}
	once    sync.Once
}

type goalSnapshot struct {
	Status     string `json:"status"`
	TokensUsed int64  `json:"tokens_used"`
}

func (b *goalBridge) RouteServiceCall(ctx context.Context, id string, call pluginhost.ServiceCallParams) (json.RawMessage, *pluginhost.HostServiceError) {
	var result any
	var err error
	switch call.Service {
	case string(pluginhost.HostServiceStorageGet):
		b.mu.Lock()
		result = pluginhost.StorageGetResult{Value: b.value}
		b.mu.Unlock()
	case string(pluginhost.HostServiceStorageSet):
		var p pluginhost.StorageSetParams
		_ = json.Unmarshal(call.Params, &p)
		b.mu.Lock()
		b.value = &p.Value
		b.mu.Unlock()
		result = struct{}{}
		var state map[string]goalSnapshot
		_ = json.Unmarshal([]byte(p.Value), &state)
		for _, g := range state {
			if g.Status == "complete" && g.TokensUsed == 20 {
				b.once.Do(func() { close(b.settled) })
			}
		}
	case string(pluginhost.HostServiceSessionSend):
		var p pluginhost.SessionSendParams
		_ = json.Unmarshal(call.Params, &p)
		result, err = b.server.sendPluginSession(ctx, id, p)
	case string(pluginhost.HostServiceSessionInspect):
		var p pluginhost.SessionInspectParams
		_ = json.Unmarshal(call.Params, &p)
		result, err = b.server.inspectPluginSession(ctx, id, p)
	case string(pluginhost.HostServiceSessionCancel):
		var p pluginhost.SessionCancelParams
		_ = json.Unmarshal(call.Params, &p)
		result, err = b.server.cancelPluginSession(ctx, id, p)
	default:
		return nil, &pluginhost.HostServiceError{Code: "service_not_found", Message: "unexpected service " + call.Service}
	}
	if err != nil {
		return nil, &pluginhost.HostServiceError{Code: "host_error", Message: err.Error()}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, &pluginhost.HostServiceError{Code: "host_error", Message: err.Error()}
	}
	return raw, nil
}

func TestGoalExtensionContinuesUserSessionThroughRealTurnLifecycle(t *testing.T) {
	response := providersResponse("working")
	response.Usage = &providers.TokenUsage{InputTokens: 5, OutputTokens: 5}
	bridge := &goalBridge{settled: make(chan struct{})}
	client := startBundledPluginTestProcess(t, "goal", bridge)
	var threadID string
	rt := newTestRuntime(t, &fakeClient{response: response, onChat: func(n int, _ providers.ChatRequest) {
		if n == 2 {
			_, err := client.ExecuteTool(context.Background(), pluginhost.ToolExecuteParams{ToolID: "update_goal", ToolExecuteInput: pluginhost.ToolExecuteInput{SessionID: threadID, Arguments: json.RawMessage(`{"status":"complete"}`)}})
			if err != nil {
				t.Errorf("complete goal: %v", err)
			}
		}
	}})
	for _, capability := range client.Capabilities() {
		if err := pluginhost.ValidateCapabilityDescriptor(capability); err != nil {
			t.Fatal(err)
		}
	}
	rt.PluginHost = pluginhost.New(client)
	out := &lockedBuffer{}
	srv := New(rt, out)
	bridge.server = srv
	t.Cleanup(srv.Close)
	if err := client.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"goal-thread","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	threadID = remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "goal-thread")["result"]).Thread.ID
	input, _ := json.Marshal(map[string]any{"method": "create_goal", "input": map[string]any{"thread_id": threadID, "objective": "Finish the work"}})
	if _, err := client.InvokeCapability(context.Background(), pluginhost.CapabilityInvokeParams{Capability: "plugin.client.request", Input: input}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-bridge.settled:
	case <-time.After(5 * time.Second):
		t.Fatalf("goal did not settle after completion; output=%s", out.String())
	}
	result, err := client.ExecuteTool(context.Background(), pluginhost.ToolExecuteParams{ToolID: "get_goal", ToolExecuteInput: pluginhost.ToolExecuteInput{SessionID: threadID, Arguments: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct{ Goal goalSnapshot }
	if err := json.Unmarshal(result.Result.StructuredContent, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Goal.TokensUsed != 20 || snapshot.Goal.Status != "complete" {
		t.Fatalf("goal = %+v", snapshot.Goal)
	}
}

func TestSharedSessionPluginCanInspectAndCancelOnlyItsOwnWork(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("done"), onChat: func(_ int, _ providers.ChatRequest) { close(started); <-release }})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(func() { close(release); srv.Close() })
	if err := srv.handleLine(context.Background(), []byte(`{"id":"shared","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	id := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "shared")["result"]).Thread.ID
	send := func(plugin, request string) pluginhost.SessionSendResult {
		t.Helper()
		r, err := srv.sendPluginSession(context.Background(), plugin, pluginhost.SessionSendParams{RequestID: request, SessionID: id, Input: pluginhost.SessionInput{Prompt: "work"}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	running := send("scheduler", "first")
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	queued := send("workflow", "queued")
	inspected, err := srv.inspectPluginSession(context.Background(), "workflow", pluginhost.SessionInspectParams{SessionID: id, RequestID: "queued"})
	if err != nil || inspected.Turn == nil || inspected.Turn.QueueID != queued.QueueID {
		t.Fatalf("inspect = %+v %v", inspected, err)
	}
	other, err := srv.inspectPluginSession(context.Background(), "other", pluginhost.SessionInspectParams{SessionID: id, RequestID: "queued"})
	if err != nil || other.Turn != nil {
		t.Fatalf("leaked other plugin request: %+v %v", other, err)
	}
	if _, err := srv.cancelPluginSession(context.Background(), "workflow", pluginhost.SessionCancelParams{SessionID: id}); err == nil {
		t.Fatal("unscoped cancellation accepted")
	}
	if _, err := srv.cancelPluginSession(context.Background(), "workflow", pluginhost.SessionCancelParams{SessionID: id, TurnID: running.TurnID}); err == nil {
		t.Fatal("other plugin turn cancelled")
	}
	cancelled, err := srv.cancelPluginSession(context.Background(), "other", pluginhost.SessionCancelParams{SessionID: id, QueueID: queued.QueueID})
	if err != nil || cancelled.Cancelled {
		t.Fatal("other plugin queue cancelled")
	}
	cancelled, err = srv.cancelPluginSession(context.Background(), "workflow", pluginhost.SessionCancelParams{SessionID: id, QueueID: queued.QueueID})
	if err != nil || !cancelled.Cancelled {
		t.Fatalf("owned queue cancellation: %+v %v", cancelled, err)
	}
	cancelled, err = srv.cancelPluginSession(context.Background(), "scheduler", pluginhost.SessionCancelParams{SessionID: id, TurnID: running.TurnID})
	if err != nil || !cancelled.Cancelled {
		t.Fatalf("owned running cancellation: %+v %v", cancelled, err)
	}
}
