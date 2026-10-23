package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Storage is synthetic; send/inspect and registry error serialization are real.
// The admitted test client proof does not establish an independent human actor.
type genuiRefusalBridge struct {
	server   *Server
	registry *pluginhost.ServiceRegistry
	t        *testing.T
	mu       sync.Mutex
	storage  map[string]*string
	unknown  bool
	sends    []pluginhost.SessionSendParams
	accepted int
}

func (b *genuiRefusalBridge) ID() string { return "genui-test-services" }
func (b *genuiRefusalBridge) Status() pluginhost.Status {
	return pluginhost.Status{ID: b.ID(), State: pluginhost.StateActive}
}
func (b *genuiRefusalBridge) Close(context.Context) error { return nil }
func (b *genuiRefusalBridge) ProvidedServices() []pluginhost.ServiceDescriptor {
	return pluginhost.KernelServiceDescriptors()
}
func (b *genuiRefusalBridge) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (b *genuiRefusalBridge) RouteServiceCall(ctx context.Context, id string, p pluginhost.ServiceCallParams) (json.RawMessage, *pluginhost.HostServiceError) {
	raw, err := b.registry.Call(ctx, id, p)
	if err != nil {
		b.t.Logf("Go registry -> shipped SDK: service=%s code=%s message=%q", p.Service, err.Code, err.Message)
	}
	return raw, err
}
func (b *genuiRefusalBridge) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var result any
	switch p.Service {
	case "host.storage.get":
		var q pluginhost.StorageGetParams
		if err := json.Unmarshal(p.Params, &q); err != nil {
			return nil, err
		}
		result = pluginhost.StorageGetResult{Value: b.storage[q.Key]}
	case "host.storage.keys":
		keys := []string{}
		for key := range b.storage {
			keys = append(keys, key)
		}
		result = pluginhost.StorageKeysResult{Keys: keys}
	case "host.storage.compare-exchange":
		var q pluginhost.StorageCompareExchangeParams
		if err := json.Unmarshal(p.Params, &q); err != nil {
			return nil, err
		}
		old := b.storage[q.Key]
		swapped := old == nil && q.Expected == nil || old != nil && q.Expected != nil && *old == *q.Expected
		if swapped {
			b.storage[q.Key] = q.Value
		}
		result = pluginhost.StorageCompareExchangeResult{Swapped: swapped, Value: b.storage[q.Key]}
	case "host.session.create":
		var q pluginhost.SessionCreateParams
		if err := json.Unmarshal(p.Params, &q); err != nil {
			return nil, err
		}
		created, err := b.server.createPluginSession(ctx, p.Caller, q)
		if err != nil {
			return nil, err
		}
		result = created
	case "host.session.context":
		var q struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(p.Params, &q); err != nil {
			return nil, err
		}
		metadata, found, err := session.Find(b.server.rt.SessionDir, q.SessionID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, session.ErrSessionNotFound
		}
		result = map[string]any{"session_id": q.SessionID, "user_space_id": metadata.UserSpaceID, "product_domain": metadata.ProductDomain, "agent_id": metadata.AgentID}
	case "host.session.inspect":
		var q pluginhost.SessionInspectParams
		if err := json.Unmarshal(p.Params, &q); err != nil {
			return nil, err
		}
		inspected, err := b.server.inspectPluginSession(ctx, p.Caller, q)
		if err != nil {
			return nil, err
		}
		result = inspected
	case "host.session.send":
		var q pluginhost.SessionSendParams
		if err := json.Unmarshal(p.Params, &q); err != nil {
			return nil, err
		}
		b.sends = append(b.sends, q)
		if b.unknown {
			return nil, errors.New("synthetic uncertain transport outcome")
		}
		proof := runtime.ClientUserInput{RequestID: q.UserInputRequestID, Prompt: q.Input.Prompt}
		ack, err := b.server.sendPluginSession(runtime.WithClientUserInput(ctx, p.Caller, proof), p.Caller, q)
		if err != nil {
			return nil, err
		}
		b.accepted++
		result = ack
	case "host.client.invalidate":
		result = map[string]any{"delivered": true}
	default:
		return nil, errors.New("unexpected test service: " + p.Service)
	}
	return json.Marshal(result)
}

func TestPersonalGenUIRegistryRefusalThroughShippedSDK(t *testing.T) {
	provider := &fakeClient{response: providersResponse("not invoked")}
	rt := newTestRuntime(t, provider)
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "personal"}}}
	rt.PluginHost = pluginhost.New()
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	bridge := &genuiRefusalBridge{server: srv, t: t, storage: make(map[string]*string)}
	bridge.registry, _ = pluginhost.BuildServiceRegistry(bridge)
	bridge.registry.Activate()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", "plugin", "bundled", "personal"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := pluginhost.Start(context.Background(), pluginhost.ProcessConfig{ID: "personal", Command: node, Args: []string{filepath.Join(root, "runtime.mjs")}, PluginRoot: root, ProjectRoot: t.TempDir(), WuuHome: t.TempDir(), WorkspaceStateDir: t.TempDir(), UserSpaceID: "local", ProductDomain: "personal", ServiceRouter: bridge, PrepareOnly: true, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()); bridge.registry.Close(context.Background()) })
	if conflicts := bridge.registry.RegisterClients(client); len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	if err := client.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	invoke := func(method string, input map[string]any) (map[string]any, error) {
		raw, _ := json.Marshal(map[string]any{"method": method, "input": input})
		output, err := client.InvokeCapability(context.Background(), pluginhost.CapabilityInvokeParams{Capability: "plugin.client.request", Input: raw, ExecutionID: "test-owner-client"})
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Result map[string]any `json:"result"`
		}
		if err := json.Unmarshal(output.Output, &envelope); err != nil {
			return nil, err
		}
		return envelope.Result, nil
	}
	mustInvoke := func(method string, input map[string]any) map[string]any {
		result, err := invoke(method, input)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	created := mustInvoke("agents.ensure_default", map[string]any{})["agent"].(map[string]any)
	agentID, sessionID := created["agent_id"].(string), created["main_session_id"].(string)
	th := srv.thread(sessionID)
	th.mu.Lock()
	th.running = true
	th.mu.Unlock()
	t.Cleanup(func() { th.mu.Lock(); th.running = false; th.mu.Unlock() })
	setEngine := func(engine string) {
		t.Helper()
		if _, err := session.SetEngine(rt.SessionDir, sessionID, engine); err != nil {
			t.Fatal(err)
		}
	}
	publish := func(id string) string {
		result, err := client.ExecuteTool(context.Background(), pluginhost.ToolExecuteParams{ToolID: "present_interactive_answer", ToolExecuteInput: pluginhost.ToolExecuteInput{SessionID: sessionID, ThreadID: sessionID, CallID: id, TurnID: "test-turn", Tool: "test-genui-tool", Arguments: json.RawMessage(`{"spec":{"schemaVersion":1,"title":"Synthetic","fallbackText":"Synthetic choices","fields":{"count":{"type":"number","scope":"persistent","initial":4,"min":1,"max":20}},"actions":[{"id":"submit","kind":"submit","label":"Explain"}],"tree":{"id":"submit","type":"button","label":"Explain","action":"submit"}}}`)}})
		if err != nil {
			t.Fatal(err)
		}
		var reference struct {
			SurfaceID string `json:"surfaceId"`
		}
		if err := json.Unmarshal(result.Result.StructuredContent, &reference); err != nil {
			t.Fatal(err)
		}
		if reference.SurfaceID == "" {
			t.Fatalf("tool result: %+v", result)
		}
		return reference.SurfaceID
	}
	submit := func(surface, event string, revision, count int) (map[string]any, error) {
		return invoke("genui.action", map[string]any{"agent_id": agentID, "request": map[string]any{"surfaceId": surface, "actionId": "submit", "expectedRevision": revision, "eventId": event, "state": map[string]any{"count": count}}})
	}
	retry := func(surface string) map[string]any {
		return mustInvoke("genui.retry", map[string]any{"agent_id": agentID, "surface_id": surface})
	}
	a := publish("definite-refusal")
	setEngine("codex")
	if _, err := submit(a, "refused-event", 0, 4); err != nil {
		t.Fatal(err)
	}
	rejected := retry(a)
	state := rejected["surface"].(map[string]any)
	if rejected["delivery"] != "rejected" || state["submitted"] != false || state["revision"] != float64(2) || bridge.accepted != 0 {
		t.Fatalf("pre-admission refusal did not restore editing: result=%+v hostAcceptedCount=%d", rejected, bridge.accepted)
	}
	setEngine("wuu")
	if _, err := submit(a, "corrected-event", 2, 7); err != nil {
		t.Fatal(err)
	}
	if accepted := retry(a); accepted["delivery"] != "accepted" || bridge.accepted != 1 {
		t.Fatalf("corrected state was not admitted: %+v count=%d", accepted, bridge.accepted)
	}
	if !strings.Contains(bridge.sends[1].Input.ContextBlocks[0].Content, `"count":7`) {
		t.Fatal("corrected actual state was lost")
	}
	b := publish("unknown-refusal")
	if _, err := submit(b, "unknown-event", 0, 4); err != nil {
		t.Fatal(err)
	}
	bridge.mu.Lock()
	bridge.unknown = true
	bridge.mu.Unlock()
	unknown := retry(b)
	bridge.mu.Lock()
	bridge.unknown = false
	bridge.mu.Unlock()
	setEngine("codex")
	stillUnknown := retry(b)
	for _, result := range []map[string]any{unknown, stillUnknown} {
		if result["delivery"] != "pending" || result["surface"].(map[string]any)["submitted"] != true {
			t.Fatalf("uncertain attempt was unlocked: %+v", result)
		}
	}
	if bridge.sends[2].RequestID != bridge.sends[3].RequestID || bridge.accepted != 1 {
		t.Fatal("uncertain attempt changed identity or was admitted")
	}
	if _, err := submit(b, "replacement-event", 1, 9); err == nil || !strings.Contains(err.Error(), "already_submitted") {
		t.Fatalf("uncertain request could be replaced: %v", err)
	}
	assertFakeClientRequestCount(t, provider, 0)
	t.Log("real appserver -> registry -> JSONL -> shipped SDK: definite refusal reopened; corrected count=7 admitted once; generic service_unavailable and later refusal stayed fenced; zero provider calls")
}
