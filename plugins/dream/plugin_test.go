package dream

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
	"github.com/blueberrycongee/wuu/plugins/memory"
)

type fakeHost struct {
	mu      sync.Mutex
	state   string
	creates []pluginapi.SessionCreateParams
	sends   []pluginapi.SessionSendParams
	// serve routes registry service calls to a provider; nil means the
	// registry has no provider for the service.
	serve func(ctx context.Context, service, method string, params json.RawMessage) (json.RawMessage, error)
}

func (h *fakeHost) InitializeParams() pluginapi.InitializeParams { return pluginapi.InitializeParams{} }
func (h *fakeHost) CallHost(_ context.Context, method string, params, result any) error {
	raw, _ := json.Marshal(params)
	if method == pluginapi.HostServiceCallMethod {
		var routed struct {
			Service string          `json:"service"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(raw, &routed)
		h.mu.Lock()
		serve := h.serve
		h.mu.Unlock()
		if serve == nil {
			return &pluginapi.HostCallError{Code: "service_unavailable", Message: "no provider for service " + routed.Service}
		}
		response, err := serve(context.Background(), routed.Service, routed.Method, routed.Params)
		if err != nil {
			return err
		}
		return json.Unmarshal(response, result)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch method {
	case pluginapi.HostServiceStorageGet:
		if h.state != "" {
			_ = json.Unmarshal([]byte(`{"value":`+quoted(h.state)+`}`), result)
		}
	case pluginapi.HostServiceStorageSet:
		var input struct {
			Value string `json:"value"`
		}
		_ = json.Unmarshal(raw, &input)
		h.state = input.Value
	case pluginapi.HostServiceSessionCreate:
		var input pluginapi.SessionCreateParams
		_ = json.Unmarshal(raw, &input)
		h.creates = append(h.creates, input)
		_ = json.Unmarshal([]byte(`{"session_id":"dream-session","created":true}`), result)
	case pluginapi.HostServiceSessionSend:
		var input pluginapi.SessionSendParams
		_ = json.Unmarshal(raw, &input)
		h.sends = append(h.sends, input)
		_ = json.Unmarshal([]byte(`{"state":"queued","session_id":"dream-session","queue_id":"q"}`), result)
	}
	return nil
}

func TestDreamUsesForkedPrivateSession(t *testing.T) {
	host := &fakeHost{serve: emptyProjectMemoryRead}
	c := &controller{now: func() time.Time { return time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC) }, tick: time.Hour}
	if err := c.prepare(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	if err := c.activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.shutdown(context.Background()) })
	c.mu.Lock()
	c.state.Settings = settings{Enabled: true, IntervalDays: 7, MinSessions: 1}
	c.mu.Unlock()
	raw, _ := json.Marshal(turnCompletedInput{ThreadID: "parent", Succeeded: true, CompletedAt: c.now()})
	if _, err := c.invokeCapability(context.Background(), pluginapi.CapabilityCall{Capability: capabilityTurnCompleted, Input: raw}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		host.mu.Lock()
		done := len(host.sends) == 1
		host.mu.Unlock()
		if done {
			break
		}
		time.Sleep(time.Millisecond)
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if len(host.creates) != 1 || host.creates[0].ParentSessionID != "parent" || host.creates[0].ContextSource != "fork" || host.creates[0].Visibility != "plugin" {
		t.Fatalf("creates=%+v", host.creates)
	}
	if len(host.sends) != 1 || host.sends[0].Cause != "dream.consolidate" {
		t.Fatalf("sends=%+v", host.sends)
	}
}

func quoted(value string) string { raw, _ := json.Marshal(value); return string(raw) }

// emptyProjectMemoryRead is the minimal memory.session provider for tests
// that exercise dream's launch path rather than the registry gate.
func emptyProjectMemoryRead(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"action":"read","target":"project_memory","exists":false,"content":""}`), nil
}

func TestDreamShutdownStopsTimerAndNextGenerationRestoresState(t *testing.T) {
	now := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	host := &fakeHost{}
	first := &controller{now: func() time.Time { return now }, tick: time.Hour}
	if err := first.prepare(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	if err := first.activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	first.mu.Lock()
	first.state.Settings = settings{Enabled: true, IntervalDays: 9, MinSessions: 3}
	first.state.Candidates["parent"] = now.Format(time.RFC3339Nano)
	first.mu.Unlock()
	if err := first.save(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := first.shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.done:
	default:
		t.Fatal("dream timer loop remained active after shutdown")
	}

	second := &controller{now: func() time.Time { return now }, tick: time.Hour}
	if err := second.prepare(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	if err := second.activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.shutdown(context.Background()) })
	restored := second.snapshot()
	if restored.Settings.IntervalDays != 9 || restored.Candidates["parent"] == "" {
		t.Fatalf("restored dream state = %+v", restored)
	}
}

func TestDreamCannotWriteMemoryWithoutTheMemoryPlugin(t *testing.T) {
	definition := Handler().Definition
	if len(definition.Tools) != 0 {
		t.Fatalf("dream must not expose a direct memory write tool: %+v", definition.Tools)
	}
	for _, service := range definition.RequiredHostServices {
		if strings.Contains(service.ID, "file") || strings.Contains(service.ID, "memory") {
			t.Fatalf("dream must not receive a host memory or file write service: %+v", service)
		}
	}
	if !strings.Contains(dreamPrompt("stable fact"), "stable fact") {
		t.Fatal("dream prompt must carry the project memory read at run start")
	}
}

func TestDreamConsumesMemorySessionServiceAcrossGenerations(t *testing.T) {
	stateDir := t.TempDir()
	memHandler := memory.Handler()
	host := &fakeHost{}
	if err := memHandler.Initialize(context.Background(), host, pluginapi.InitializeParams{WuuHome: t.TempDir(), WorkspaceStateDir: stateDir}); err != nil {
		t.Fatal(err)
	}
	host.serve = func(_ context.Context, service, method string, params json.RawMessage) (json.RawMessage, error) {
		return memHandler.InvokeService(context.Background(), host, pluginapi.ServiceCall{Service: service, Method: method, Caller: "plugin:user:dream", Params: params})
	}
	seedParams, _ := json.Marshal(map[string]string{"target": "project_memory", "content": "registry binds providers by name"})
	if _, err := host.serve(context.Background(), "memory.session", "append", seedParams); err != nil {
		t.Fatal(err)
	}

	c := &controller{now: func() time.Time { return time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC) }, tick: time.Hour}
	if err := c.prepare(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	if err := c.activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.shutdown(context.Background()) })
	c.mu.Lock()
	c.state.Settings = settings{Enabled: true, IntervalDays: 7, MinSessions: 1}
	c.mu.Unlock()
	raw, _ := json.Marshal(turnCompletedInput{ThreadID: "parent", Succeeded: true, CompletedAt: c.now()})
	if _, err := c.invokeCapability(context.Background(), pluginapi.CapabilityCall{Capability: capabilityTurnCompleted, Input: raw}); err != nil {
		t.Fatal(err)
	}
	waitForSend(t, host, 1)
	host.mu.Lock()
	prompt := host.sends[0].Input.Prompt
	host.mu.Unlock()
	if !strings.Contains(prompt, "registry binds providers by name") {
		t.Fatalf("dream prompt must carry project memory read through the service, got %q", prompt)
	}

	// Simulate a provider upgrade: a new memory generation over the same state
	// dir. The next run must re-resolve and see the new content with no
	// dream-side bookkeeping.
	if _, err := c.invokeCapability(context.Background(), pluginapi.CapabilityCall{Capability: capabilityLifecycle, Input: lifecycleInput(t, c, "completed")}); err != nil {
		t.Fatal(err)
	}
	nextHandler := memory.Handler()
	if err := nextHandler.Initialize(context.Background(), host, pluginapi.InitializeParams{WuuHome: t.TempDir(), WorkspaceStateDir: stateDir}); err != nil {
		t.Fatal(err)
	}
	host.serve = func(_ context.Context, service, method string, params json.RawMessage) (json.RawMessage, error) {
		return nextHandler.InvokeService(context.Background(), host, pluginapi.ServiceCall{Service: service, Method: method, Caller: "plugin:user:dream", Params: params})
	}
	if _, err := host.serve(context.Background(), "memory.session", "append", []byte(`{"target":"project_memory","content":"second generation lesson"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.invokeCapability(context.Background(), pluginapi.CapabilityCall{Capability: capabilityTurnCompleted, Input: raw}); err != nil {
		t.Fatal(err)
	}
	c.startIfDueForce(context.Background())
	waitForSend(t, host, 2)
	host.mu.Lock()
	second := host.sends[1].Input.Prompt
	host.mu.Unlock()
	if !strings.Contains(second, "second generation lesson") {
		t.Fatalf("upgraded provider content must reach the next dream run, got %q", second)
	}
}

func TestDreamSkipsWhenSessionMemoryServiceUnavailable(t *testing.T) {
	host := &fakeHost{} // no provider wired: resolution fails with service_unavailable
	c := &controller{now: func() time.Time { return time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC) }, tick: time.Hour}
	if err := c.prepare(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	if err := c.activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.shutdown(context.Background()) })
	c.mu.Lock()
	c.state.Settings = settings{Enabled: true, IntervalDays: 7, MinSessions: 1}
	c.mu.Unlock()
	raw, _ := json.Marshal(turnCompletedInput{ThreadID: "parent", Succeeded: true, CompletedAt: c.now()})
	if _, err := c.invokeCapability(context.Background(), pluginapi.CapabilityCall{Capability: capabilityTurnCompleted, Input: raw}); err != nil {
		t.Fatal(err)
	}
	state := c.snapshot()
	if state.LastStatus != "skipped" || !strings.Contains(state.LastError, "no provider for service memory.session") {
		t.Fatalf("state = %+v", state)
	}
	if _, err := c.readProjectMemory(context.Background()); err == nil {
		t.Fatal("readProjectMemory must fail without a provider")
	} else {
		var hostErr *pluginapi.HostCallError
		if !errors.As(err, &hostErr) || hostErr.Code != "service_unavailable" {
			t.Fatalf("typed registry error must reach the consumer, got %#v", err)
		}
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if len(host.creates) != 0 || len(host.sends) != 0 {
		t.Fatalf("no session may launch without the memory service: %+v %+v", host.creates, host.sends)
	}
}

func waitForSend(t *testing.T, host *fakeHost, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		host.mu.Lock()
		done := len(host.sends) >= count
		host.mu.Unlock()
		if done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	t.Fatalf("timed out waiting for %d sends, got %d", count, len(host.sends))
}

func lifecycleInput(t *testing.T, c *controller, state string) json.RawMessage {
	t.Helper()
	c.mu.Lock()
	requestID := c.state.ActiveRequestID
	c.mu.Unlock()
	raw, err := json.Marshal(pluginapi.TurnLifecycleInput{RequestID: requestID, State: state})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
