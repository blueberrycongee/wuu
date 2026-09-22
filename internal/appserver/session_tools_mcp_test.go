package appserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/externalengine"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
	"github.com/blueberrycongee/wuu/plugins/peers"
)

// Run the real plugin handler against the real app-server session services.
// Only the storage transport and model responses are replaced.
type peerIntegrationClient struct {
	t        *testing.T
	srv      *Server
	handler  pluginapi.Handler
	disabled atomic.Bool
	mu       sync.Mutex
	stored   *string
	sends    []pluginhost.SessionSendParams
}

func (c *peerIntegrationClient) ID() string { return "peers" }
func (c *peerIntegrationClient) Status() pluginhost.Status {
	state := pluginhost.StateActive
	if c.disabled.Load() {
		state = pluginhost.StateStopped
	}
	return pluginhost.Status{ID: c.ID(), State: state}
}
func (c *peerIntegrationClient) Close(context.Context) error { c.disabled.Store(true); return nil }
func (c *peerIntegrationClient) ProtocolVersion() int        { return pluginhost.CapabilityProtocolVersion }
func (c *peerIntegrationClient) Tools() []pluginhost.ToolRegistration {
	return remarshal[[]pluginhost.ToolRegistration](c.t, c.handler.Definition.Tools)
}
func (c *peerIntegrationClient) Capabilities() []pluginhost.CapabilityDescriptor {
	return remarshal[[]pluginhost.CapabilityDescriptor](c.t, c.handler.Definition.Capabilities)
}
func (c *peerIntegrationClient) ExecuteTool(ctx context.Context, params pluginhost.ToolExecuteParams) (pluginhost.ToolExecuteResult, error) {
	result, err := c.handler.ExecuteTool(ctx, c, remarshal[pluginapi.ToolCall](c.t, params))
	return remarshal[pluginhost.ToolExecuteResult](c.t, map[string]any{"result": result}), err
}
func (c *peerIntegrationClient) InvokeCapability(ctx context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	result, err := c.handler.InvokeCapability(ctx, c, remarshal[pluginapi.CapabilityCall](c.t, params))
	return pluginhost.CapabilityInvokeResult{Output: result}, err
}
func (c *peerIntegrationClient) InitializeParams() pluginapi.InitializeParams {
	return pluginapi.InitializeParams{WorkspaceID: c.srv.rt.WorkspaceID}
}
func (c *peerIntegrationClient) CallHost(ctx context.Context, method string, params, out any) error {
	var result any
	var err error
	switch method {
	case pluginapi.HostServiceSessionList:
		result, err = c.srv.listPluginSessions(ctx, c.ID(), remarshal[pluginhost.SessionListParams](c.t, params))
	case pluginapi.HostServiceSessionSend:
		p := remarshal[pluginhost.SessionSendParams](c.t, params)
		c.mu.Lock()
		c.sends = append(c.sends, p)
		c.mu.Unlock()
		result, err = c.srv.sendPluginSession(ctx, c.ID(), p)
	case pluginapi.HostServiceSessionInspect:
		result, err = c.srv.inspectPluginSession(ctx, c.ID(), remarshal[pluginhost.SessionInspectParams](c.t, params))
	case pluginapi.HostServiceStorageGet:
		c.mu.Lock()
		var value *string
		if c.stored != nil {
			copied := *c.stored
			value = &copied
		}
		c.mu.Unlock()
		result = pluginapi.StorageGetResult{Value: value}
	case pluginapi.HostServiceStorageCompareExchange:
		p := params.(pluginapi.StorageCompareExchangeParams)
		c.mu.Lock()
		swapped := p.Expected == nil && c.stored == nil || p.Expected != nil && c.stored != nil && *p.Expected == *c.stored
		if swapped {
			c.stored = p.Value
		}
		c.mu.Unlock()
		result = pluginapi.StorageCompareExchangeResult{Swapped: swapped}
	default:
		return fmt.Errorf("unexpected host service %s", method)
	}
	if err != nil {
		return err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

type peerTestOutput struct {
	lockedBuffer
	completed chan TurnCompletedNotification
}

func (o *peerTestOutput) Write(data []byte) (int, error) {
	n, err := o.lockedBuffer.Write(data)
	for _, line := range bytes.Split(data, []byte("\n")) {
		var notification struct {
			Method string                    `json:"method"`
			Params TurnCompletedNotification `json:"params"`
		}
		if json.Unmarshal(line, &notification) == nil && notification.Method == NotificationTurnCompleted {
			o.completed <- notification.Params
		}
	}
	return n, err
}

func newPeerIntegrationServer(t *testing.T) (*Server, *peerIntegrationClient, *peerTestOutput) {
	t.Helper()
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("native output")})
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	rt.Toolkit = kit
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	client := &peerIntegrationClient{t: t, handler: peers.Handler()}
	rt.PluginHost = pluginhost.New(client)
	registry := agentengine.NewRegistry()
	if err := registry.Register(rt.WuuEngine()); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"grok", "cursor"} {
		engine := externalengine.New(enginecatalog.Entry{ID: id, Name: id, Protocol: "acp",
			Args: []string{"-test.run=^TestPeerACPProcess$", "--", "wuu-peer-acp"}}, binary, rt.RootDir)
		if err := registry.Register(engine); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Register(externalengine.New(enginecatalog.Entry{ID: "opencode", Name: "OpenCode", Protocol: "opencode", Args: []string{"-test.run=^TestPeerOpenCodeProcess$", "--", "wuu-peer-opencode"}}, binary, rt.RootDir)); err != nil {
		t.Fatal(err)
	}
	rt.SetEnginesForTest(registry)
	out := &peerTestOutput{completed: make(chan TurnCompletedNotification, 32)}
	srv := New(rt, out)
	client.srv = srv
	t.Cleanup(srv.Close)
	return srv, client, out
}

func startPeerTestThread(t *testing.T, srv *Server, out *peerTestOutput, id, engine, mode string) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"id": id, "method": MethodThreadStart, "params": map[string]any{"engine": engine, "permission_mode": mode}})
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), id)
	if response["error"] != nil {
		t.Fatalf("start thread: %v", response)
	}
	return remarshal[ThreadStartResult](t, response["result"]).Thread.ID
}

func peerToolName(t *testing.T, host *pluginhost.Host, local string) string {
	t.Helper()
	for _, definition := range host.ToolDefinitions() {
		tool, _ := host.Tool(definition.Name)
		if tool.Registration.ID == local {
			return definition.Name
		}
	}
	t.Fatalf("tool %s unavailable", local)
	return ""
}

func TestPeersRoundTripAcrossEngines(t *testing.T) {
	for _, engines := range [][2]string{{"wuu", "wuu"}, {"wuu", "grok"}, {"grok", "wuu"}, {"grok", "cursor"}, {"opencode", "wuu"}, {"wuu", "opencode"}, {"opencode", "cursor"}, {"grok", "opencode"}} {
		t.Run(engines[0]+"-to-"+engines[1], func(t *testing.T) {
			srv, client, out := newPeerIntegrationServer(t)
			source := startPeerTestThread(t, srv, out, "source", engines[0], "unconfined")
			targetMode := "read_only"
			if engines[1] == "opencode" {
				targetMode = "standard"
			}
			target := startPeerTestThread(t, srv, out, "target", engines[1], targetMode)
			if engines[0] == "wuu" {
				rt, err := srv.ensureThreadRuntime(srv.thread(source))
				if err != nil {
					t.Fatal(err)
				}
				args, _ := json.Marshal(map[string]string{"target_session_id": target, "message": "/peer refuse\nrequest payload"})
				_, err = rt.StreamRunner.Tools.Execute(context.Background(), providers.ToolCall{ID: "source-call", Name: peerToolName(t, srv.rt.PluginHost, "send_message"), Arguments: string(args)})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				raw, _ := json.Marshal(map[string]any{"id": "send", "method": MethodTurnStart, "params": TurnStartParams{ThreadID: source, Prompt: "coordinate:" + target}})
				if err := srv.handleLine(context.Background(), raw); err != nil {
					t.Fatal(err)
				}
			}
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
			want := "receipt observed"
			if engines[0] == "wuu" {
				want = "native output"
			}
		wait:
			for {
				select {
				case done := <-out.completed:
					if done.ThreadID == source && done.Content == want {
						break wait
					}
				case <-timer.C:
					t.Fatalf("no reply reached source; output=%s", out.String())
				}
			}
			client.mu.Lock()
			sends := append([]pluginhost.SessionSendParams(nil), client.sends...)
			client.mu.Unlock()
			if len(sends) != 2 {
				t.Fatalf("wanted one request and one reply: %+v", sends)
			}
			var request, reply pluginhost.SessionSendParams
			for _, sent := range sends {
				if strings.HasPrefix(sent.RequestID, "peer:req:") {
					request = sent
				} else {
					reply = sent
				}
			}
			if request.SessionID != target || request.IfRunning != "queue" || request.Presentation.Text != "/peer refuse\nrequest payload" || request.Presentation.RelatedSessionID != source {
				t.Fatalf("request=%+v", request)
			}
			if reply.SessionID != source || reply.Presentation.RelatedSessionID != target {
				t.Fatalf("reply=%+v", reply)
			}
			wantReply := "native output"
			if engines[1] != "wuu" {
				wantReply = "peer output:plan"
			}
			if engines[1] == "opencode" {
				wantReply = "peer output:standard"
			}
			if reply.Presentation.Text != wantReply {
				t.Fatalf("target permission or output lost: %+v", reply)
			}
			metadata, _, err := session.Find(srv.rt.SessionDir, target)
			if err != nil || metadata.PermissionMode != targetMode {
				t.Fatalf("target permission=%q err=%v", metadata.PermissionMode, err)
			}
		})
	}
}

func peerMCPRequest(endpoint, method string, id int, params any) (map[string]any, error) {
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	response, err := http.Post(endpoint, "application/json", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var result map[string]any
	err = json.NewDecoder(response.Body).Decode(&result)
	if result["error"] != nil {
		return nil, fmt.Errorf("MCP error: %v", result["error"])
	}
	if value, ok := result["result"].(map[string]any); ok && value["isError"] == true {
		return nil, fmt.Errorf("tool error: %v", value)
	}
	return result, err
}

func TestSessionToolsCapabilityEnforcesIdentityPolicyAndRevocation(t *testing.T) {
	srv, client, out := newPeerIntegrationServer(t)
	source := startPeerTestThread(t, srv, out, "source", "grok", "standard")
	target := startPeerTestThread(t, srv, out, "target", "cursor", "standard")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := func(id string) (agentengine.MCPServer, func()) {
		th := srv.thread(id)
		th.mu.Lock()
		th.running = true
		th.currentTurn = "test-turn"
		th.mu.Unlock()
		t.Cleanup(func() { th.mu.Lock(); th.running = false; th.currentTurn = ""; th.mu.Unlock() })
		binding, _, stop, err := srv.startSessionToolsMCP(ctx, th, "test-turn")
		if err != nil || binding.URL == "" || binding.Stdio == nil {
			t.Fatalf("binding=%+v err=%v", binding, err)
		}
		t.Cleanup(stop)
		return binding, stop
	}
	from, stopFrom := start(source)
	to, _ := start(target)
	policy := peerToolName(t, srv.rt.PluginHost, "peer_policy")
	send := peerToolName(t, srv.rt.PluginHost, "send_message")
	call := func(endpoint, name string, id int, args any) (map[string]any, error) {
		return peerMCPRequest(endpoint, "tools/call", id, map[string]any{"name": name, "arguments": args})
	}
	// A caller cannot change its identity by putting another session in JSON.
	if _, err := call(to.URL, policy, 1, map[string]string{"inbound": "refuse", "session_id": source}); err != nil {
		t.Fatal(err)
	}
	result, err := call(from.URL, send, 2, map[string]string{"target_session_id": target, "message": "request"})
	if err != nil {
		t.Fatal(err)
	}
	text := result["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	var receipt struct {
		State string `json:"state"`
	}
	if json.Unmarshal([]byte(text), &receipt) != nil || receipt.State != "refused" {
		t.Fatalf("receipt=%s", text)
	}
	client.mu.Lock()
	sent := len(client.sends)
	client.mu.Unlock()
	if sent != 0 {
		t.Fatal("refused request created a target turn")
	}
	th := srv.thread(source)
	th.mu.Lock()
	th.PermissionMode = "read_only"
	th.mu.Unlock()
	if _, err := call(from.URL, policy, 3, map[string]string{"inbound": "refuse"}); err == nil {
		t.Fatal("read-only mode allowed a policy mutation")
	}
	if _, err := call(from.URL, "read_file", 4, map[string]string{"path": "secret"}); err == nil {
		t.Fatal("native tool leaked through MCP")
	}
	list := peerToolName(t, srv.rt.PluginHost, "list_peers")
	for _, allow := range [][]string{{list}, {"unavailable_tool"}} {
		encoded, _ := json.Marshal(pluginhost.SessionToolPolicy{Allow: allow})
		metadata, err := session.CreateInitialized(srv.rt.SessionDir, session.Session{
			CWD: srv.rt.RootDir, EngineID: "grok", PermissionMode: "standard", ToolPolicyJSON: string(encoded),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		restricted := &threadState{ID: metadata.ID, CWD: metadata.CWD, EngineID: "grok", PermissionMode: "standard", running: true, currentTurn: "restricted-turn"}
		binding, _, stop, err := srv.startSessionToolsMCP(ctx, restricted, restricted.currentTurn)
		t.Cleanup(stop)
		if err != nil {
			t.Fatal(err)
		}
		if allow[0] != list {
			if binding.URL != "" {
				t.Fatal("session with no allowed tools received a capability")
			}
			continue
		}
		listed, err := peerMCPRequest(binding.URL, "tools/list", 1, nil)
		if err != nil {
			t.Fatal(err)
		}
		definitions := listed["result"].(map[string]any)["tools"].([]any)
		if len(definitions) != 1 || definitions[0].(map[string]any)["name"] != list {
			t.Fatalf("session policy did not filter discovery: %v", definitions)
		}
		if _, err := call(binding.URL, policy, 2, map[string]string{"inbound": "refuse"}); err == nil {
			t.Fatal("guessed tool bypassed session policy")
		}
	}
	for _, origin := range []string{"https://example.com", "null"} {
		req, _ := http.NewRequest(http.MethodPost, from.URL, strings.NewReader(`{"jsonrpc":"2.0","id":5,"method":"tools/list"}`))
		req.Header.Set("Origin", origin)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("origin %q accepted", origin)
		}
	}
	// Cached native catalogs cannot keep a disabled plugin executable.
	client.disabled.Store(true)
	listed, err := peerMCPRequest(from.URL, "tools/list", 6, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed["result"].(map[string]any)["tools"].([]any)) != 0 {
		t.Fatal("disabled plugin still listed")
	}
	if _, err := call(from.URL, send, 7, map[string]string{"target_session_id": target, "message": "again"}); err == nil {
		t.Fatal("disabled plugin executed")
	}
	noTools, _, stop, err := srv.startSessionToolsMCP(ctx, th, "test-turn")
	stop()
	if err != nil || noTools.URL != "" {
		t.Fatalf("disabled turn got capability: %+v %v", noTools, err)
	}
	client.disabled.Store(false)
	if _, err := session.UpdateArchived(srv.rt.SessionDir, source, true); err != nil {
		t.Fatal(err)
	}
	if _, err := peerMCPRequest(from.URL, "tools/list", 8, nil); err == nil {
		t.Fatal("archived session retained access")
	}
	stopFrom()
	if _, err := peerMCPRequest(from.URL, "tools/list", 9, nil); err == nil {
		t.Fatal("ended turn retained its capability")
	}
}

func peerEngineReply(endpoint, prompt, mode string) (string, error) {
	if strings.Contains(prompt, "Peer response:") {
		return "receipt observed", nil
	}
	if !strings.HasPrefix(prompt, "coordinate:") {
		return "peer output:" + mode, nil
	}
	list, err := peerMCPRequest(endpoint, "tools/list", 1, nil)
	if err != nil {
		return "", err
	}
	sendTool, listTool := "", ""
	for _, raw := range list["result"].(map[string]any)["tools"].([]any) {
		name := raw.(map[string]any)["name"].(string)
		if strings.Contains(name, "send_message") {
			sendTool = name
		}
		if strings.Contains(name, "list_peers") {
			listTool = name
		}
	}
	target := strings.TrimPrefix(prompt, "coordinate:")
	listed, err := peerMCPRequest(endpoint, "tools/call", 2, map[string]any{"name": listTool, "arguments": map[string]any{}})
	if err != nil {
		return "", err
	}
	encoded, _ := json.Marshal(listed)
	if !strings.Contains(string(encoded), target) {
		return "", fmt.Errorf("target not discovered")
	}
	_, err = peerMCPRequest(endpoint, "tools/call", 3, map[string]any{"name": sendTool, "arguments": map[string]string{"target_session_id": target, "message": "/peer refuse\nrequest payload"}})
	return "request accepted", err
}

// The child speaks ACP over real pipes and calls the injected host MCP over
// HTTP. It never starts a model or reads installed-agent credentials.
func TestPeerACPProcess(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "wuu-peer-acp" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	encoder := json.NewEncoder(os.Stdout)
	endpoint, mode := "", ""
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		var result any = map[string]any{}
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true, "mcpCapabilities": map[string]any{"http": true}}}
		case "session/new", "session/load":
			var params struct {
				Servers []struct {
					Name string `json:"name"`
					URL  string `json:"url"`
				} `json:"mcpServers"`
			}
			_ = json.Unmarshal(request.Params, &params)
			for _, server := range params.Servers {
				if server.Name == "wuu_session_tools" {
					endpoint = server.URL
				}
			}
			result = map[string]any{"sessionId": "native-peer", "configOptions": []map[string]any{{"id": "mode", "category": "mode", "type": "select", "currentValue": "code", "options": []map[string]string{{"value": "code", "name": "Code"}, {"value": "plan", "name": "Plan"}, {"value": "yolo", "name": "YOLO"}}}}}
		case "session/set_config_option":
			var params struct {
				Mode string `json:"value"`
			}
			_ = json.Unmarshal(request.Params, &params)
			mode = params.Mode
		case "session/prompt":
			var params struct {
				Prompt []struct {
					Text string `json:"text"`
				} `json:"prompt"`
			}
			_ = json.Unmarshal(request.Params, &params)
			prompt := params.Prompt[len(params.Prompt)-1].Text
			text, err := peerEngineReply(endpoint, prompt, mode)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "native-peer", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": text}}}})
			result = map[string]string{"stopReason": "end_turn"}
		}
		_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}
	os.Exit(0)
}

var _ io.Writer = (*peerTestOutput)(nil)
