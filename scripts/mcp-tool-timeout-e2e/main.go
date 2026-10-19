// Run with: go run ./scripts/mcp-tool-timeout-e2e
// Uses a real Wuu runtime, HTTP/SSE loopback MCP servers, and the real PTC
// interpreter. No model request or external credential is used. The full run
// includes a 31-second first-byte wait and the actual 30-second default expiry.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/mcpconfig"
	"github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	wuurt "github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type check struct {
	Name      string `json:"name"`
	ElapsedMS int64  `json:"elapsed_ms"`
	Error     string `json:"error,omitempty"`
}
type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Arguments struct {
			Tag     string `json:"tag"`
			DelayMS int    `json:"delay_ms"`
		} `json:"arguments"`
	} `json:"params"`
}
type fixture struct {
	mu            sync.Mutex
	calls         map[string]int
	streams       map[string]chan any
	next          atomic.Int64
	cancelStarted chan struct{}
	cancelEnded   chan struct{}
}

func (f *fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sse") {
		id := fmt.Sprint(f.next.Add(1))
		ch := make(chan any, 8)
		f.mu.Lock()
		f.streams[id] = ch
		f.mu.Unlock()
		defer func() { f.mu.Lock(); delete(f.streams, id); f.mu.Unlock() }()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: endpoint\ndata: /messages/%s\n\n", id)
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case value := <-ch:
				data, _ := json.Marshal(value)
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
				w.(http.Flusher).Flush()
			}
		}
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if len(req.ID) == 0 {
		if r.URL.Path == "/stalled" && req.Method == "notifications/cancelled" {
			close(f.cancelStarted)
			<-r.Context().Done()
			close(f.cancelEnded)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result any
	switch req.Method {
	case "server/discover":
		result = map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "legacy fixture"}}
	case "initialize":
		if strings.HasSuffix(r.URL.Path, "/tiny") {
			time.Sleep(60 * time.Millisecond)
		}
		result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "budget-fixture", "version": "1"}}
	case "tools/list":
		if strings.HasSuffix(r.URL.Path, "/tiny") {
			time.Sleep(60 * time.Millisecond)
		}
		result = map[string]any{"tools": []any{map[string]any{"name": "wait", "description": "Synthetic bounded wait", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"tag": map[string]string{"type": "string"}, "delay_ms": map[string]string{"type": "integer"}}}, "annotations": map[string]bool{"readOnlyHint": true}}}}
	case "tools/call":
		args := req.Params.Arguments
		f.mu.Lock()
		f.calls[args.Tag]++
		f.mu.Unlock()
		if r.URL.Path == "/stalled" && args.Tag != "cancel-neighbor" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		timer := time.NewTimer(time.Duration(args.DelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		result = map[string]any{"content": []any{map[string]string{"type": "text", "text": "finished:" + args.Tag}}}
	default:
		http.Error(w, "unexpected method", 400)
		return
	}
	if req.Method != "server/discover" {
		result = map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}
	}
	if strings.HasPrefix(r.URL.Path, "/messages/") {
		f.mu.Lock()
		ch := f.streams[strings.TrimPrefix(r.URL.Path, "/messages/")]
		f.mu.Unlock()
		if ch != nil {
			select {
			case ch <- result:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func require(ok bool, msg string, args ...any) {
	if !ok {
		panic(fmt.Sprintf(msg, args...))
	}
}
func writeJSON(path string, value any) {
	must(os.MkdirAll(filepath.Dir(path), 0700))
	data, err := json.MarshalIndent(value, "", "  ")
	must(err)
	must(os.WriteFile(path, data, 0600))
}
func waitTools(manager *mcp.Manager, count int) []mcp.NativeTool {
	require(manager != nil, "missing MCP manager")
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if native := manager.NativeTools(); len(native) == count {
			return native
		}
		time.Sleep(5 * time.Millisecond)
	}
	panic(fmt.Sprintf("catalog not ready: %+v", manager.Status()))
}
func main() {
	out := os.Getenv("WUU_MCP_TIMEOUT_E2E_OUTPUT")
	if out == "" {
		out = ".tmp/e2e/mcp-tool-timeout"
	}
	must(os.MkdirAll(out, 0700))
	scratch, err := os.MkdirTemp(out, "run-")
	must(err)
	scratch, err = filepath.Abs(scratch)
	must(err)
	home := filepath.Join(scratch, "home")
	root := filepath.Join(scratch, "project")
	state := filepath.Join(home, ".wuu")
	must(os.MkdirAll(root, 0700))
	must(os.MkdirAll(state, 0700))
	must(os.Setenv("HOME", home))
	must(os.Setenv("WUU_HOME", state))
	f := &fixture{calls: map[string]int{}, streams: map[string]chan any{}, cancelStarted: make(chan struct{}), cancelEnded: make(chan struct{})}
	server := httptest.NewServer(f)
	defer server.Close()
	var checks []check
	defer func() {
		failure := recover()
		status := "passed"
		if failure != nil {
			status = "failed"
		}
		f.mu.Lock()
		calls := f.calls
		f.mu.Unlock()
		writeJSON(filepath.Join(scratch, "results.json"), map[string]any{"status": status, "checks": checks, "calls": calls, "failure": fmt.Sprint(failure), "scope": "real runtime and loopback protocols; synthetic data; no model or desktop acceptance"})
		fmt.Println(filepath.Join(scratch, "results.json"))
		if failure != nil {
			panic(failure)
		}
	}()
	measure := func(name string, fn func() error) {
		start := time.Now()
		err := fn()
		entry := check{Name: name, ElapsedMS: time.Since(start).Milliseconds()}
		if err != nil {
			entry.Error = err.Error()
		}
		checks = append(checks, entry)
		fmt.Printf("%s: %dms %v\n", name, entry.ElapsedMS, err)
		must(err)
	}
	pluginPath := filepath.Join(state, "plugins", "budget-fixture", "plugin.json")
	writeJSON(pluginPath, map[string]any{"id": "budget-fixture", "mcpServers": map[string]any{"docs": map[string]any{"url": server.URL + "/plugin", "transport": "http", "tool_timeout_ms": 400}}})
	item, err := plugin.LoadManifest(pluginPath, "user")
	must(err)
	settings := &extensions.Settings{}
	must(settings.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint, Scope: extensions.GrantScopeUser, Permissions: item.EffectivePermissions, ApprovedAt: time.Now()}))
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: server.URL + "/never-infer", APIKey: "synthetic", Model: "gpt-5"}}, Agent: config.AgentConfig{PermissionMode: "unconfined", ToolLoading: config.ToolLoadingFlat}, Extensions: settings, MCPJson: &config.MCPJsonTrust{EnableAll: true}, MCPServers: map[string]config.MCPServerConfig{
		"default": {URL: server.URL + "/default", Transport: "http"},
		"native":  {URL: server.URL + "/native", Transport: "http", ToolTimeoutMS: 700},
		"tiny":    {URL: server.URL + "/tiny", Transport: "http", ToolTimeoutMS: 20},
		"long":    {URL: server.URL + "/long", Transport: "http", ToolTimeoutMS: 35000},
		"legacy":  {URL: server.URL + "/sse", Transport: "sse", ToolTimeoutMS: 35000},
		"project": {ToolTimeoutMS: 500},
	}}
	configPath := filepath.Join(state, "config.json")
	writeJSON(configPath, cfg)
	writeJSON(filepath.Join(root, ".mcp.json"), map[string]any{"mcpServers": map[string]any{"project": map[string]any{"type": "http", "url": server.URL + "/project", "tool_timeout_ms": 100}}})
	cfg, _, err = config.LoadFrom(root, home)
	must(err)
	rt, err := wuurt.NewSession(wuurt.Options{RootDir: root, HomeDir: home, ConfigPath: configPath, Config: cfg, NonInteractive: true})
	must(err)
	defer func() { _, err := rt.Cleanup(); must(err) }()
	native := waitTools(rt.Toolkit.MCPManager(), 7)
	byName := map[string]mcp.NativeTool{}
	for _, n := range native {
		byName[n.Client.Name()] = n
	}
	require(byName["default"].Timeout == 30*time.Second, "default budget was changed")
	require(byName["project"].Timeout == 500*time.Millisecond, "project preference budget lost")
	require(byName["plugin.budget-fixture.docs"].Timeout == 400*time.Millisecond, "plugin budget lost")
	require(byName["tiny"].Timeout == 20*time.Millisecond, "tiny budget lost")
	checks = append(checks, check{Name: "user, project preference, plugin, default metadata and independent startup/discovery budgets"})
	rt.Toolkit.SetBoundary(tools.UnconfinedBoundary())
	rt.Toolkit.ConfigurePTC(rt.CodeMode, config.PTCConfig{Enabled: false})
	rt.Toolkit.Definitions()
	invoke := func(ctx context.Context, serverName, tag string, delay int) error {
		n := byName[serverName]
		name := mcp.NewMCPTool(n.Client, n.Definition).Name()
		args, _ := json.Marshal(map[string]any{"tag": tag, "delay_ms": delay})
		result, err := rt.Toolkit.ExecuteResult(ctx, providers.ToolCall{ID: tag, Name: name, Arguments: string(args)})
		if err == nil && !strings.Contains(result.TextProjection(), "finished:"+tag) {
			return fmt.Errorf("unexpected result %s", result.TextProjection())
		}
		return err
	}
	measure("native configured success", func() error { return invoke(context.Background(), "native", "native-success", 250) })
	measure("project configured success", func() error { return invoke(context.Background(), "project", "project-success", 200) })
	measure("plugin configured success", func() error { return invoke(context.Background(), "plugin.budget-fixture.docs", "plugin-success", 180) })
	measure("tool budget expiry", func() error {
		err := invoke(context.Background(), "tiny", "tiny-expiry", 200)
		if !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("expected deadline, got %v", err)
		}
		return nil
	})
	measure("earlier parent deadline", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		err := invoke(ctx, "native", "parent-deadline", 200)
		if !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("expected caller deadline, got %v", err)
		}
		return nil
	})
	measure("caller cancellation", func() error {
		ctx, cancel := context.WithCancel(context.Background())
		timer := time.AfterFunc(40*time.Millisecond, cancel)
		defer timer.Stop()
		defer cancel()
		err := invoke(ctx, "native", "parent-cancel", 200)
		if !errors.Is(err, context.Canceled) {
			return fmt.Errorf("expected caller cancel, got %v", err)
		}
		return nil
	})
	measure("already canceled never dispatches", func() error {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := invoke(ctx, "native", "never-dispatch", 1)
		if !errors.Is(err, context.Canceled) {
			return fmt.Errorf("expected cancel, got %v", err)
		}
		return nil
	})
	measure("stalled cancel notification cannot extend tool budget", func() error {
		client, err := mcp.ConnectStreamableHTTP(context.Background(), mcp.ServerConfig{Name: "stalled", URL: server.URL + "/stalled", ToolTimeoutMS: 20})
		if err != nil {
			return err
		}
		defer client.Close()
		start := time.Now()
		_, err = client.CallTool(context.Background(), "wait", json.RawMessage(`{"tag":"stalled-cancel"}`))
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 250*time.Millisecond {
			return fmt.Errorf("budget overrun: %s %v", time.Since(start), err)
		}
		select {
		case <-f.cancelStarted:
		case <-time.After(time.Second):
			return errors.New("cancel notification not dispatched")
		}
		if _, err := client.CallTool(context.Background(), "wait", json.RawMessage(`{"tag":"cancel-neighbor","delay_ms":1}`)); err != nil {
			return fmt.Errorf("cancel blocked healthy call: %w", err)
		}
		if err := client.Close(); err != nil {
			return err
		}
		select {
		case <-f.cancelEnded:
		case <-time.After(250 * time.Millisecond):
			return errors.New("retirement did not cancel pending notification")
		}
		return nil
	})
	measure("positive config bounds and invalid declarations", func() error {
		for _, value := range []int{1, 3600000} {
			var declaration config.MCPServerConfig
			if err := json.Unmarshal([]byte(fmt.Sprintf(`{"tool_timeout_ms":%d}`, value)), &declaration); err != nil {
				return err
			}
			if duration, err := declaration.ToolTimeoutMS.Duration(); err != nil || duration != time.Duration(value)*time.Millisecond {
				return fmt.Errorf("budget %d: %v %v", value, duration, err)
			}
		}
		for _, value := range []string{"0", "-1", "3600001", "1.5", "null", `"infinity"`, "9223372036854775808"} {
			var declaration config.MCPServerConfig
			if err := json.Unmarshal([]byte(`{"tool_timeout_ms":`+value+`}`), &declaration); err == nil {
				return fmt.Errorf("accepted invalid budget %s", value)
			}
		}
		return nil
	})
	measure("invalid programmatic budget rejected before network", func() error {
		_, err := mcp.ConnectStreamableHTTP(context.Background(), mcp.ServerConfig{Name: "bad", URL: server.URL, ToolTimeoutMS: mcpconfig.ToolTimeoutMS(-1)})
		if err == nil || !strings.Contains(err.Error(), "tool_timeout_ms") {
			return fmt.Errorf("unexpected rejection: %v", err)
		}
		return nil
	})
	rt.Toolkit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	rt.Toolkit.ConfigurePTC(rt.CodeMode, config.PTCConfig{Enabled: true})
	measure("PTC configured success and shorter nested budget", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		tr := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: rt.Toolkit, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
		defer tr.Cancel()
		code := `const a = await tools.mcp_native_wait({tag:"ptc-success",delay_ms:220}); console.log(JSON.stringify(a)); try { await tools.mcp_tiny_wait({tag:"ptc-expiry",delay_ms:200}); } catch (e) { console.log("expected-budget-error:"+e.message); }`
		args, _ := json.Marshal(map[string]string{"input": code})
		messages, err := tr.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "ptc", Name: "run_code", Arguments: string(args)}}, nil)
		if err != nil {
			return err
		}
		if len(messages) != 1 || !strings.Contains(messages[0].Content, "finished:ptc-success") || !strings.Contains(messages[0].Content, "expected-budget-error:") {
			return fmt.Errorf("unexpected PTC result: %+v", messages)
		}
		return nil
	})
	// Capture tools before the reload. Their pinned client policy must not be
	// overwritten by the replacement runtime generation.
	pinned := rt.RetainPluginGeneration()
	defer rt.ReleasePluginGeneration(pinned)
	old := byName["native"]
	cfg.MCPServers["native"] = config.MCPServerConfig{URL: server.URL + "/native", Transport: "http", ToolTimeoutMS: 80}
	cfg.MCPServers["plugin.budget-fixture.docs"] = config.MCPServerConfig{ToolTimeoutMS: 150}
	writeJSON(configPath, cfg)
	cfg, _, err = config.LoadFrom(root, home)
	must(err)
	measure("config reload preserves retained generation budget", func() error {
		if err := rt.RefreshExtensions(cfg); err != nil {
			return err
		}
		reloaded := waitTools(rt.Toolkit.MCPManager(), 7)
		for _, n := range reloaded {
			if n.Client.Name() == "native" {
				require(n.Timeout == 80*time.Millisecond, "new native budget lost")
			}
			if n.Client.Name() == "plugin.budget-fixture.docs" {
				require(n.Timeout == 150*time.Millisecond, "plugin preference override lost")
			}
		}
		require(old.Client.ToolTimeout() == 700*time.Millisecond, "retained client budget changed")
		_, err := old.Client.CallTool(context.Background(), "wait", json.RawMessage(`{"tag":"retained-success","delay_ms":180}`))
		return err
	})
	if os.Getenv("WUU_MCP_TIMEOUT_E2E_SHORT") != "1" {
		// These real deadlines run concurrently and use no artificial clock or
		// rewritten production constants. SSE waits before acknowledging POST.
		type outcome struct {
			name    string
			elapsed time.Duration
			err     error
		}
		done := make(chan outcome, 3)
		for _, name := range []string{"default", "long", "legacy"} {
			go func(name string) {
				start := time.Now()
				n := byName[name]
				delay := 31000
				if name == "default" {
					delay = 45000
				}
				_, err := mcp.NewMCPTool(n.Client, n.Definition).Execute(context.Background(), fmt.Sprintf(`{"tag":%q,"delay_ms":%d}`, "full-"+name, delay))
				done <- outcome{name, time.Since(start), err}
			}(name)
		}
		for range 3 {
			o := <-done
			if o.name == "default" {
				require(errors.Is(o.err, context.DeadlineExceeded), "default expiry: %v", o.err)
				require(o.elapsed >= 29*time.Second && o.elapsed < 34*time.Second, "default elapsed %v", o.elapsed)
			} else {
				require(o.err == nil, "long %s first byte: %v", o.name, o.err)
				require(o.elapsed >= 31*time.Second, "long call did not wait")
			}
			checks = append(checks, check{Name: "full wall-clock " + o.name, ElapsedMS: o.elapsed.Milliseconds()})
			fmt.Printf("full %s: %v\n", o.name, o.elapsed)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for tag, count := range f.calls {
		require(count == 1, "tool %s replayed %d times", tag, count)
	}
	require(f.calls["never-dispatch"] == 0, "already canceled tool was dispatched")
	checks = append(checks, check{Name: "one dispatch per invocation; canceled request not replayed"})
}
