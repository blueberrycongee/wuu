// Command mcp-auth-reload-e2e exercises the runtime and app-server protocol with
// disposable state, persisted synthetic OAuth tokens, and a loopback MCP peer.
// Run: go run ./scripts/mcp-auth-reload-e2e
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	wuuexec "github.com/blueberrycongee/wuu/internal/exec"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type receipt struct {
	StartedAt        string      `json:"started_at"`
	ExecutableSHA256 string      `json:"executable_sha256"`
	Status           string      `json:"status"`
	Checks           []string    `json:"checks"`
	Wire             []wireEvent `json:"wire"`
	Error            string      `json:"error,omitempty"`
}

type wireEvent struct {
	Method        string `json:"method"`
	Session       string `json:"session,omitempty"`
	Authenticated bool   `json:"authenticated"`
}

type fixture struct {
	server      *httptest.Server
	mu          sync.Mutex
	nextSession int
	wire        []wireEvent
	held        chan string
	release     chan struct{}
	releaseOnce sync.Once
}

func (f *fixture) unblock() { f.releaseOnce.Do(func() { close(f.release) }) }
func (f *fixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	endpoint := f.server.URL
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp":
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": endpoint + "/mcp", "authorization_servers": []string{endpoint}})
		return
	case "/.well-known/oauth-authorization-server":
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": endpoint, "authorization_endpoint": endpoint + "/authorize", "token_endpoint": endpoint + "/token"})
		return
	case "/token":
		_ = r.ParseForm()
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-" + r.Form.Get("code"), "refresh_token": "synthetic-refresh", "expires_in": 3600})
		return
	}
	if r.Method != http.MethodPost {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Arguments struct {
				Hold bool `json:"hold"`
			} `json:"arguments"`
		} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	authorization := r.Header.Get("Authorization")
	authorized := authorization == "Bearer synthetic-saved" || authorization == "Bearer synthetic-replacement"
	sessionID := r.Header.Get("Mcp-Session-Id")
	f.mu.Lock()
	f.wire = append(f.wire, wireEvent{request.Method, sessionID, authorized})
	f.mu.Unlock()
	if !authorized {
		w.WriteHeader(401)
		return
	}
	reply := func(result any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}
	switch request.Method {
	case "server/discover":
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32601, "message": "legacy fixture"}})
	case "initialize":
		f.mu.Lock()
		f.nextSession++
		id := fmt.Sprintf("connection-%d", f.nextSession)
		f.mu.Unlock()
		w.Header().Set("Mcp-Session-Id", id)
		reply(map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "loopback", "version": "1"}})
	case "tools/list":
		reply(map[string]any{"tools": []any{map[string]any{"name": "identity", "description": "Return this connection identity", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"hold": map[string]any{"type": "boolean"}}}}}})
	case "tools/call":
		if request.Params.Arguments.Hold {
			select {
			case f.held <- sessionID:
			case <-r.Context().Done():
				return
			}
			select {
			case <-f.release:
			case <-r.Context().Done():
				return
			}
		}
		reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": sessionID}}})
	default:
		w.WriteHeader(202)
	}
}

type protocol struct {
	client     *wuuexec.ProtocolClient
	input      *io.PipeWriter
	output     *io.PipeReader
	done       chan error
	transcript bytes.Buffer
}

func serve(rt *runtime.Session) *protocol {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	p := &protocol{input: inW, output: outR, done: make(chan error, 1)}
	p.client = wuuexec.NewProtocolClient(outR, inW)
	go func() {
		defer outW.Close()
		defer inR.Close()
		p.done <- appserver.RunStdio(context.Background(), rt, inR, io.MultiWriter(&p.transcript, outW))
	}()
	go func() {
		for range p.client.Notifications() {
		}
	}()
	return p
}
func (p *protocol) close() error {
	_ = p.input.Close()
	select {
	case err := <-p.done:
		_ = p.output.Close()
		return err
	case <-time.After(10 * time.Second):
		_ = p.output.Close()
		return errors.New("app-server shutdown timed out")
	}
}
func (p *protocol) auth(ctx context.Context) error {
	var status appserver.MCPAuthStatusResult
	if err := p.client.Call(ctx, "mcp/auth/status", map[string]string{"name": "docs"}, &status); err != nil {
		return err
	}
	if !status.Authenticated {
		return fmt.Errorf("auth status = %+v", status)
	}
	return nil
}
func waitConnected(ctx context.Context, manager *mcp.Manager) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		status := manager.Status()["docs"]
		if status.Connected && status.ToolCount == 1 {
			return nil
		}
		if status.State == mcp.MCPServerStateFailed || status.State == mcp.MCPServerStateAuthRequired {
			return fmt.Errorf("connection failed: %+v", status)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for MCP: %+v: %w", status, ctx.Err())
		case <-ticker.C:
		}
	}
}
func replace(rt *runtime.Session, cfg config.Config) error {
	candidate, err := rt.PreflightExtensions(cfg)
	if err != nil {
		return err
	}
	return rt.ActivatePluginGeneration(candidate, nil)
}
func callTool(ctx context.Context, manager *mcp.Manager, args string) (string, error) {
	tools := manager.AllTools()
	if len(tools) != 1 {
		return "", fmt.Errorf("expected one tool, got %d", len(tools))
	}
	return tools[0].Execute(ctx, args)
}

func run(report *receipt, output string) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	scratch, err := os.MkdirTemp(output, "state-")
	if err != nil {
		return err
	}
	// Keep all process discovery, persistence and model requests inside the fixture.
	if err = os.Setenv("WUU_HOME", filepath.Join(scratch, "home")); err != nil {
		return err
	}
	if err = os.Setenv("PATH", scratch); err != nil {
		return err
	}
	f := &fixture{held: make(chan string, 1), release: make(chan struct{})}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	defer func() {
		f.unblock()
		f.server.Close()
		f.mu.Lock()
		report.Wire = append([]wireEvent(nil), f.wire...)
		f.mu.Unlock()
	}()
	path := filepath.Join(scratch, "synthetic-oauth.json")
	oauth := mcp.NewOAuthManager(credentialstore.NewFileStore(path), f.server.Client())
	start, err := oauth.Start(ctx, mcp.OAuthStartOptions{ServerID: "docs", ResourceURL: f.server.URL + "/mcp", RedirectURI: "http://127.0.0.1/callback", ClientID: "fixture"})
	if err != nil {
		return err
	}
	if _, err = oauth.Finish(ctx, mcp.OAuthFinishOptions{ServerID: "docs", ResourceURL: f.server.URL + "/mcp", State: start.State, Code: "saved"}); err != nil {
		return err
	}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: f.server.URL, Model: "fixture", APIKey: "synthetic-unused"}}, MCPServers: map[string]config.MCPServerConfig{"docs": {URL: f.server.URL + "/mcp", Transport: "http", OAuth: &config.MCPOAuthConfig{ClientID: "fixture", RedirectURI: "http://127.0.0.1/callback"}}}}
	opts := runtime.Options{RootDir: scratch, HomeDir: scratch, Config: cfg, SafeMode: true, MCPOAuth: mcp.NewOAuthManager(credentialstore.NewFileStore(path), f.server.Client())}
	rt, err := runtime.NewSession(opts)
	if err != nil {
		return err
	}
	defer func() {
		if rt != nil {
			_, _ = rt.Cleanup()
		}
	}()
	initial := rt.Toolkit.MCPManager()
	if err = waitConnected(ctx, initial); err != nil {
		return fmt.Errorf("saved-credential startup: %w", err)
	}
	report.Checks = append(report.Checks, "Saved credentials connect before app-server construction")
	p := serve(rt)
	defer func() {
		if p != nil {
			_ = p.close()
		}
	}()
	if err = p.auth(ctx); err != nil {
		return err
	}
	var pending appserver.MCPAuthStartResult
	if err = p.client.Call(ctx, "mcp/auth/start", map[string]string{"name": "docs"}, &pending); err != nil {
		return err
	}
	thread, err := rt.NewThreadRuntime("retained-oauth-turn")
	if err != nil {
		return err
	}
	defer func() {
		if thread != nil {
			thread.AgentControl.Close()
			rt.ReleasePluginGeneration(thread.PluginGeneration)
		}
	}()
	old := thread.Toolkit.MCPManager()
	type callResult struct {
		text string
		err  error
	}
	complete := make(chan callResult, 1)
	go func() { text, err := callTool(ctx, old, `{"hold":true}`); complete <- callResult{text, err} }()
	var oldID string
	select {
	case oldID = <-f.held:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err = replace(rt, cfg); err != nil {
		return err
	}
	current := rt.Toolkit.MCPManager()
	if current == old {
		return errors.New("replacement reused the old connection manager")
	}
	if err = waitConnected(ctx, current); err != nil {
		return fmt.Errorf("replacement: %w", err)
	}
	if err = p.auth(ctx); err != nil {
		return fmt.Errorf("replacement auth RPC: %w", err)
	}
	var finished appserver.MCPAuthFinishResult
	if err = p.client.Call(ctx, "mcp/auth/finish", appserver.MCPAuthFinishParams{Name: "docs", State: pending.State, Code: "replacement"}, &finished); err != nil {
		return fmt.Errorf("login spanning replacement: %w", err)
	}
	if !finished.Auth.Authenticated {
		return errors.New("replacement lost the pending OAuth flow")
	}
	report.Checks = append(report.Checks, "Distinct replacement manager inherits credentials and pending login state")
	f.unblock()
	select {
	case result := <-complete:
		if result.err != nil || result.text != oldID {
			return fmt.Errorf("retained call: %+v, want %s", result, oldID)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	if err = old.Connect(ctx, "docs"); err != nil {
		return fmt.Errorf("retained manager authentication: %w", err)
	}
	if err = current.Connect(ctx, "docs"); err != nil {
		return err
	}
	newID, err := callTool(ctx, current, `{}`)
	if err != nil {
		return err
	}
	if newID == oldID {
		return errors.New("replacement used old transport session")
	}
	thread.AgentControl.Close()
	rt.ReleasePluginGeneration(thread.PluginGeneration)
	thread = nil
	if err = old.Connect(ctx, "docs"); !errors.Is(err, mcp.ErrManagerClosed) {
		return fmt.Errorf("released old manager should close: %v", err)
	}
	if _, err = callTool(ctx, current, `{}`); err != nil {
		return fmt.Errorf("old cleanup affected replacement: %w", err)
	}
	report.Checks = append(report.Checks, "In-flight old-generation tool survives replacement; final lease closes only old manager")
	disabled := false
	serverCfg := cfg.MCPServers["docs"]
	serverCfg.Enabled = &disabled
	cfg.MCPServers["docs"] = serverCfg
	if err = replace(rt, cfg); err != nil {
		return err
	}
	if rt.Toolkit.MCPManager().Status()["docs"].Connected {
		return errors.New("disabled MCP connected")
	}
	if err = p.auth(ctx); err != nil {
		return fmt.Errorf("disabled manager lost credentials: %w", err)
	}
	serverCfg.Enabled = nil
	cfg.MCPServers["docs"] = serverCfg
	if err = replace(rt, cfg); err != nil {
		return err
	}
	if err = waitConnected(ctx, rt.Toolkit.MCPManager()); err != nil {
		return err
	}
	report.Checks = append(report.Checks, "Disable and re-enable preserve authentication without reusing managers")
	if err = p.close(); err != nil {
		p = nil
		return err
	}
	if strings.Contains(p.transcript.String(), "synthetic-saved") || strings.Contains(p.transcript.String(), "synthetic-replacement") || strings.Contains(p.transcript.String(), "synthetic-refresh") {
		return errors.New("app-server RPC exposed synthetic credentials")
	}
	if err = os.WriteFile(filepath.Join(output, "rpc.jsonl"), p.transcript.Bytes(), 0600); err != nil {
		return err
	}
	p = nil
	_, err = rt.Cleanup()
	rt = nil
	if err != nil {
		return err
	}
	opts.Config.MCPServers = nil
	opts.MCPOAuth = mcp.NewOAuthManager(credentialstore.NewFileStore(path), f.server.Client())
	rt, err = runtime.NewSession(opts)
	if err != nil {
		return err
	}
	if rt.Toolkit.MCPManager() != nil {
		return errors.New("zero-MCP startup created a connection manager")
	}
	p = serve(rt)
	if err = replace(rt, cfg); err != nil {
		return err
	}
	if err = waitConnected(ctx, rt.Toolkit.MCPManager()); err != nil {
		return fmt.Errorf("first MCP after zero startup: %w", err)
	}
	if err = p.auth(ctx); err != nil {
		return err
	}
	if _, err = callTool(ctx, rt.Toolkit.MCPManager(), `{}`); err != nil {
		return err
	}
	report.Checks = append(report.Checks, "First MCP added after zero-MCP startup uses persisted credentials and auth RPC")
	if err = p.close(); err != nil {
		p = nil
		return err
	}
	if strings.Contains(p.transcript.String(), "synthetic-") {
		return errors.New("first-MCP RPC exposed synthetic credentials")
	}
	if err = os.WriteFile(filepath.Join(output, "first-mcp-rpc.jsonl"), p.transcript.Bytes(), 0600); err != nil {
		return err
	}
	p = nil
	_, err = rt.Cleanup()
	rt = nil
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, event := range f.wire {
		if !event.Authenticated {
			return fmt.Errorf("unauthenticated MCP wire request: %+v", event)
		}
	}
	report.Checks = append(report.Checks, "Every observed MCP POST was authenticated; protocol responses omit credentials")
	return nil
}

func main() {
	output := os.Getenv("WUU_MCP_AUTH_E2E_OUTPUT")
	if output == "" {
		output = ".tmp/e2e/mcp-auth-reload"
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		panic(err)
	}
	binary, _ := os.Executable()
	data, err := os.ReadFile(binary)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(data)
	report := receipt{StartedAt: time.Now().UTC().Format(time.RFC3339Nano), ExecutableSHA256: hex.EncodeToString(sum[:]), Status: "failed"}
	err = run(&report, output)
	if err != nil {
		report.Error = err.Error()
	} else {
		report.Status = "passed"
	}
	data, _ = json.MarshalIndent(report, "", "  ")
	if writeErr := os.WriteFile(filepath.Join(output, "results.json"), append(data, '\n'), 0600); writeErr != nil {
		panic(writeErr)
	}
	fmt.Println(string(data))
	if err != nil {
		os.Exit(1)
	}
}
