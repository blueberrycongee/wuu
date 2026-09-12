package appserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/config"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/modelbudget"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/codex"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/sessiontrace"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/coder/websocket"
)

func TestServerMCPAuthLifecycleDoesNotExposeCredentials(t *testing.T) {
	var baseURL string
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": baseURL, "authorization_servers": []string{baseURL}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": baseURL, "authorization_endpoint": baseURL + "/authorize", "token_endpoint": baseURL + "/token",
			})
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "app-server-secret", "refresh_token": "refresh-secret", "expires_in": 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()
	baseURL = authServer.URL

	rt := newTestRuntime(t, &fakeClient{})
	manager := mcp.NewManager()
	manager.Configure(map[string]mcp.ServerConfig{
		"docs": {
			Name: "docs",
			URL:  baseURL,
			OAuth: &mcp.OAuthConfig{
				ClientID:    "desktop-client",
				RedirectURI: "http://127.0.0.1/callback",
				Scopes:      []string{"tools"},
			},
		},
	})
	credentialStore := credentialstore.NewFileStore(filepath.Join(t.TempDir(), "oauth.json"))
	toolkit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	rt.Toolkit = toolkit
	rt.Toolkit.SetMCPManager(manager)
	out := &lockedBuffer{}
	srv := NewWithCredentialStore(rt, out, credentialStore, authServer.Client())

	requests := []string{
		`{"id":"start","method":"mcp/auth/start","params":{"name":"docs"}}`,
		`{"id":"status-before","method":"mcp/auth/status","params":{"name":"docs"}}`,
	}
	for _, raw := range requests {
		if err := srv.handleLine(context.Background(), []byte(raw)); err != nil {
			t.Fatalf("handleLine %s: %v", raw, err)
		}
	}
	start := remarshal[MCPAuthStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"])
	if start.AuthorizationURL == "" || start.State == "" {
		t.Fatalf("start result = %+v", start)
	}
	before := remarshal[MCPAuthStatusResult](t, responseByID(t, parseOutput(t, out.String()), "status-before")["result"])
	if before.Authenticated {
		t.Fatalf("status before finish = %+v", before)
	}

	finishRaw, err := json.Marshal(Request{ID: json.RawMessage(`"finish"`), Method: "mcp/auth/finish", Params: mustJSON(MCPAuthFinishParams{Name: "docs", State: start.State, Code: "code-1"})})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), finishRaw); err != nil {
		t.Fatalf("mcp/auth/finish: %v", err)
	}
	finish := remarshal[MCPAuthFinishResult](t, responseByID(t, parseOutput(t, out.String()), "finish")["result"])
	if !finish.Auth.Authenticated || finish.Server.AuthStatus != string(mcp.MCPAuthStatusOAuth) {
		t.Fatalf("finish result = %+v", finish)
	}
	if strings.Contains(out.String(), "app-server-secret") || strings.Contains(out.String(), "refresh-secret") {
		t.Fatalf("OAuth RPC leaked credentials: %s", out.String())
	}

	if err := srv.handleLine(context.Background(), []byte(`{"id":"remove","method":"mcp/auth/remove","params":{"name":"docs"}}`)); err != nil {
		t.Fatalf("mcp/auth/remove: %v", err)
	}
	removed := remarshal[MCPAuthRemoveResult](t, responseByID(t, parseOutput(t, out.String()), "remove")["result"])
	if removed.Auth.Authenticated || removed.Server.State != string(mcp.MCPServerStateAuthRequired) {
		t.Fatalf("remove result = %+v", removed)
	}
}

type fakeClient struct {
	mu        sync.Mutex
	requests  []providers.ChatRequest
	errs      []error
	err       error
	responses []providers.ChatResponse
	response  providers.ChatResponse
	onChat    func(call int, req providers.ChatRequest)
}

type prewarmRecordingClient struct {
	*fakeClient
	prewarmed chan string
}

func (c *prewarmRecordingClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	return providers.AdaptStreamClient(c.fakeClient).StreamChat(ctx, req)
}

func (c *prewarmRecordingClient) PrewarmSession(_ context.Context, sessionID string) error {
	c.prewarmed <- sessionID
	return nil
}

func providersResponse(content string) providers.ChatResponse {
	return providers.ChatResponse{
		Content:      content,
		StopReason:   "stop",
		FinishReason: providers.FinishReasonStop,
	}
}

func waitForOwnedWorkerExecutions(t *testing.T, control *agentcontrol.AgentControl, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := control.OwnedWorkerExecutionCount(); got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("owned worker executions = %d, want %d", control.OwnedWorkerExecutionCount(), want)
}

func visibleMessagesForTest(msgs []providers.ChatMessage) []providers.ChatMessage {
	out := make([]providers.ChatMessage, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Hidden {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func (f *fakeClient) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	call := len(f.requests)
	onChat := f.onChat
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		f.mu.Unlock()
		if onChat != nil {
			onChat(call, req)
		}
		return providers.ChatResponse{}, err
	}
	if f.err != nil {
		err := f.err
		f.mu.Unlock()
		if onChat != nil {
			onChat(call, req)
		}
		return providers.ChatResponse{}, err
	}
	if len(f.responses) > 0 {
		res := f.responses[0]
		f.responses = f.responses[1:]
		f.mu.Unlock()
		if onChat != nil {
			onChat(call, req)
		}
		return res, nil
	}
	res := f.response
	f.mu.Unlock()
	if onChat != nil {
		onChat(call, req)
	}
	return res, nil
}

type blockingStreamClient struct {
	started chan struct{}
	release chan struct{}
	content string
	once    sync.Once
}

type partialBlockingStreamClient struct {
	started chan struct{}
	content string
	once    sync.Once
}

type usageStreamClient struct {
	events []providers.StreamEvent
}

func (c usageStreamClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, nil
}

func (c usageStreamClient) StreamChat(context.Context, providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, len(c.events))
	for _, event := range c.events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func newBlockingStreamClient(content string) *blockingStreamClient {
	return &blockingStreamClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
		content: content,
	}
}

func newPartialBlockingStreamClient(content string) *partialBlockingStreamClient {
	return &partialBlockingStreamClient{
		started: make(chan struct{}),
		content: content,
	}
}

func (c *partialBlockingStreamClient) Chat(ctx context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	<-ctx.Done()
	return providers.ChatResponse{}, ctx.Err()
}

func (c *partialBlockingStreamClient) StreamChat(ctx context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, 2)
	go func() {
		defer close(ch)
		ch <- providers.StreamEvent{
			Type:    providers.EventContentDelta,
			Content: c.content,
			Phase:   providers.MessagePhaseFinalAnswer,
		}
		c.once.Do(func() { close(c.started) })
		<-ctx.Done()
		ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
	}()
	return ch, nil
}

func (c *blockingStreamClient) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	<-c.started
	select {
	case <-c.release:
	case <-ctx.Done():
		return providers.ChatResponse{}, ctx.Err()
	}
	return providers.ChatResponse{Content: c.content}, nil
}

func (c *blockingStreamClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, 4)
	c.once.Do(func() { close(c.started) })
	go func() {
		defer close(ch)
		select {
		case <-c.release:
		case <-ctx.Done():
			ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
			return
		}
		if c.content != "" {
			ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: c.content}
		}
		ch <- providers.StreamEvent{Type: providers.EventDone}
	}()
	return ch, nil
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

type noopToolExecutor struct{}

func (noopToolExecutor) Definitions() []providers.ToolDefinition { return nil }
func (noopToolExecutor) Execute(_ context.Context, _ providers.ToolCall) (string, error) {
	return "", nil
}

type blockingToolExecutor struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingToolExecutor() *blockingToolExecutor {
	return &blockingToolExecutor{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *blockingToolExecutor) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{
		Name:        "wait_for_steer",
		Description: "wait for a steer test signal",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}}
}

func (b *blockingToolExecutor) Execute(ctx context.Context, _ providers.ToolCall) (string, error) {
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.release:
		return `{"ok":true}`, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type detachableWaitToolExecutor struct {
	started chan struct{}
	once    sync.Once
}

func newDetachableWaitToolExecutor() *detachableWaitToolExecutor {
	return &detachableWaitToolExecutor{started: make(chan struct{})}
}

func (d *detachableWaitToolExecutor) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{
		Name:        "wait_in_background",
		Description: "wait until a steer safely backgrounds this work",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}}
}

func (d *detachableWaitToolExecutor) Execute(ctx context.Context, _ providers.ToolCall) (string, error) {
	d.once.Do(func() { close(d.started) })
	interrupt := toolctx.WaitInterrupt(ctx)
	if interrupt == nil {
		return "", errors.New("wait interrupt not configured")
	}
	select {
	case <-interrupt:
		return `{"status":"running","backgrounded":true}`, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestServerInitializeAndConfigRead(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"initialize"}`)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"2","method":"config/read"}`)); err != nil {
		t.Fatalf("config/read: %v", err)
	}

	msgs := parseOutput(t, out.String())
	initMsg := responseByID(t, msgs, "1")
	initResult := remarshal[InitializeResult](t, initMsg["result"])
	if initResult.ProtocolVersion != ProtocolVersion {
		t.Fatalf("unexpected protocol version: %+v", initResult)
	}
	if initResult.Core.Version == "" {
		t.Fatalf("expected core.version in initialize result, got %+v", initResult)
	}
	if initResult.Core.Commit == "" {
		t.Fatalf("expected core.commit in initialize result, got %+v", initResult)
	}
	if initResult.Model != "fake-model" || initResult.Provider != "fake-provider" {
		t.Fatalf("unexpected initialize result: %+v", initResult)
	}
	if initResult.MaxParallel != config.DefaultAgentMaxParallel {
		t.Fatalf("initialize missing max_parallel runtime state: %+v", initResult)
	}
	if initResult.RuntimeHost.Kind != string(runtime.HostLocal) || initResult.RuntimeHost.InstanceID != "" {
		t.Fatalf("initialize missing default local host: %+v", initResult.RuntimeHost)
	}
	configMsg := responseByID(t, msgs, "2")
	configResult := remarshal[ConfigReadResult](t, configMsg["result"])
	if configResult.ConfigPath == "" || configResult.SessionDir == "" {
		t.Fatalf("expected config paths, got %+v", configResult)
	}
	if configResult.MaxParallel != config.DefaultAgentMaxParallel {
		t.Fatalf("config/read missing max_parallel runtime state: %+v", configResult)
	}
}

func TestServerInitializeReportsCloudRuntimeHost(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Host = runtime.Host{Kind: runtime.HostCloud, InstanceID: "run-123"}
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"initialize"}`)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	result := remarshal[InitializeResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if result.RuntimeHost.Kind != "cloud" || result.RuntimeHost.InstanceID != "run-123" {
		t.Fatalf("unexpected runtime host: %+v", result.RuntimeHost)
	}
}

func TestServerInitializeNegotiatesBrowserClient(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	request := `{"id":"1","method":"initialize","params":{"protocol_version":"wuu-app-server/v0.1","capabilities":{"reverse_rpc":{"methods":["browser/cdp","browser/screenshot","browser/open_tab","browser/close_tab","browser/set_visibility","browser/list_tabs"]}}}}`

	if err := srv.handleLine(context.Background(), []byte(request)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	result := remarshal[InitializeResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if !result.Features.Browser {
		t.Fatalf("browser feature not negotiated: %+v", result.Features)
	}
}

func TestServerInitializeRejectsIncompatibleProtocol(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"initialize","params":{"protocol_version":"wuu-app-server/v9"}}`)); err != nil {
		t.Fatalf("initialize write: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "1")
	errorPayload := remarshal[ResponseError](t, response["error"])
	if !strings.Contains(errorPayload.Message, "unsupported protocol version") {
		t.Fatalf("unexpected initialize error: %+v", errorPayload)
	}
}

func TestServerInitializeReportsCredentialSetupWithoutExiting(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.ReadinessIssues = []runtime.ReadinessIssue{{Code: "credential_missing", Provider: "fake-provider", Message: "missing credential"}}
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"initialize"}`)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	result := remarshal[InitializeResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if result.Status != "needs_setup" || len(result.Issues) != 1 || result.Issues[0].Code != "credential_missing" {
		t.Fatalf("unexpected readiness result: %+v", result)
	}
}

func TestServerInitializeDoesNotExposeToolPolicySummary(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"initialize"}`)); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	msgs := parseOutput(t, out.String())
	raw, err := json.Marshal(responseByID(t, msgs, "1")["result"])
	if err != nil {
		t.Fatalf("marshal initialize result: %v", err)
	}
	if strings.Contains(string(raw), `"tool_policy"`) {
		t.Fatalf("initialize result should not expose tool_policy: %s", raw)
	}
}

func TestServerInitializeExposesExtensionTrustSummary(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("New toolkit: %v", err)
	}
	rt.Toolkit = kit
	rt.Skills = []skills.Skill{{Name: "docs", Description: "Docs"}}
	rt.Plugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "compose-kit"}}}
	rt.ActivePlugins = append([]pluginpkg.Plugin(nil), rt.Plugins...)
	rt.HookDispatcher = hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
		hooks.PreToolUse: {{Command: "true"}},
	}))
	kit.SetSkills(rt.Skills)

	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"initialize"}`)); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[InitializeResult](t, responseByID(t, msgs, "1")["result"])
	main := result.ExtensionTrust.MainSession
	if !main.Skills.Allowed || !main.Skills.Active || main.Skills.Count != 1 || main.Skills.KnownTools == 0 {
		t.Fatalf("unexpected skills trust summary: %+v", main.Skills)
	}
	if main.Workflows.Allowed || main.Workflows.Active || main.Workflows.Count != 0 || main.Workflows.KnownTools != 0 {
		t.Fatalf("unexpected workflow trust summary: %+v", main.Workflows)
	}
	if !main.Hooks.Allowed || !main.Hooks.Active {
		t.Fatalf("unexpected hooks trust summary: %+v", main.Hooks)
	}
	if !main.Plugins.Allowed || !main.Plugins.Active || main.Plugins.Count != 1 {
		t.Fatalf("unexpected plugins trust summary: %+v", main.Plugins)
	}
	reviewer := result.ExtensionTrust.ReviewerSession
	if reviewer.MCP.Allowed || reviewer.Hooks.Allowed || reviewer.Plugins.Allowed || reviewer.Skills.Allowed || reviewer.Workflows.Allowed || reviewer.ExternalTools.Allowed {
		t.Fatalf("reviewer extension surfaces should be denied by default: %+v", reviewer)
	}
}

func TestServerInitializeExposesExtensionInventoryWithoutSecrets(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	t.Setenv("TEST_WUU_KEY", "test")
	rt.Skills = []skills.Skill{{
		Name:   "docs",
		Source: "project",
		Path:   filepath.Join(rt.RootDir, ".wuu", "skills", "docs", "SKILL.md"),
	}}
	rt.Plugins = []pluginpkg.Plugin{{
		Manifest: pluginpkg.Manifest{
			ID:                   "compose-kit",
			RequestedPermissions: []string{"network.connect"},
			UnsupportedFields:    []string{"commands"},
			MCPServers: map[string]config.MCPServerConfig{
				"docs": {
					Command: "plugin-docs",
					Args:    []string{"--stdio"},
					Env:     map[string]string{"API_TOKEN": "top-secret-value"},
				},
				"search": {URL: "https://example.test/mcp"},
			},
			Hooks: map[string][]config.HookEntry{
				"PreToolUse": {{Matcher: "read_file", Command: "plugin-hook"}},
			},
		},
		Source:       "project",
		Root:         filepath.Join(rt.RootDir, ".wuu", "plugins", "compose-kit"),
		ManifestPath: filepath.Join(rt.RootDir, ".wuu", "plugins", "compose-kit", ".codex-plugin", "plugin.json"),
	}}

	mcpFingerprint, err := extensions.Fingerprint(extensions.ExecutableSpec{
		Command:     "plugin-docs",
		Args:        []string{"--stdio"},
		Env:         map[string]string{"API_TOKEN": "different-secret"},
		Permissions: []string{"network.connect", "process.spawn"},
	})
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	cfg := config.Config{
		DefaultProvider: "fake",
		Providers: map[string]config.ProviderConfig{
			"fake": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKeyEnv: "TEST_WUU_KEY", Model: "fake-model"},
		},
		MCPServers: map[string]config.MCPServerConfig{
			"disabled": {Command: "disabled-server", Enabled: &disabled},
		},
		Extensions: &extensions.Settings{Grants: map[string]extensions.Grant{
			"mcp:plugin:compose-kit:docs": {
				SubjectID:   "mcp:plugin:compose-kit:docs",
				Fingerprint: mcpFingerprint,
				Scope:       extensions.GrantScopeProject,
				ApprovedAt:  time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC),
			},
			"hook:plugin:compose-kit:PreToolUse:0": {
				SubjectID:   "hook:plugin:compose-kit:PreToolUse:0",
				Fingerprint: "stale-fingerprint",
				Scope:       extensions.GrantScopeProject,
				ApprovedAt:  time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC),
			},
		}},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"initialize"}`)); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[InitializeResult](t, responseByID(t, msgs, "1")["result"])
	byID := make(map[string]ExtensionInventoryRecord, len(result.ExtensionInventory))
	for _, record := range result.ExtensionInventory {
		byID[record.ID] = record
	}
	if got := byID["skill:project:docs"]; got.State != ExtensionStateActive || got.Provenance.Kind != extensions.KindSkill {
		t.Fatalf("skill inventory = %+v", got)
	}
	pluginRecord := byID["plugin:project:compose-kit"]
	if pluginRecord.State != ExtensionStatePending || pluginRecord.ApprovalState != ExtensionApprovalPending || len(pluginRecord.UnsupportedFields) != 1 || pluginRecord.Provenance.Source != "codex" {
		t.Fatalf("plugin inventory = %+v", pluginRecord)
	}
	mcpRecord := byID["mcp:plugin:compose-kit:docs"]
	if mcpRecord.State != ExtensionStateGranted || mcpRecord.Fingerprint != mcpFingerprint || mcpRecord.GrantScope != extensions.GrantScopeProject {
		t.Fatalf("MCP inventory = %+v", mcpRecord)
	}
	if !containsTestString(mcpRecord.RequestedPermissions, "network.connect") || !containsTestString(mcpRecord.RequestedPermissions, "process.spawn") {
		t.Fatalf("MCP permissions = %+v", mcpRecord.RequestedPermissions)
	}
	if got := byID["mcp:plugin:compose-kit:search"]; got.State != ExtensionStatePending {
		t.Fatalf("pending MCP inventory = %+v", got)
	}
	if got := byID["mcp:config:disabled"]; got.State != ExtensionStateRejected {
		t.Fatalf("disabled MCP inventory = %+v", got)
	}
	if got := byID["hook:plugin:compose-kit:PreToolUse:0"]; got.State != ExtensionStateChanged {
		t.Fatalf("hook inventory = %+v", got)
	}
	raw, err := json.Marshal(result.ExtensionInventory)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "top-secret-value") || strings.Contains(string(raw), "different-secret") {
		t.Fatalf("extension inventory leaked a secret: %s", raw)
	}
}

func TestServerInitializeHidesInactivePluginMCPOverrides(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	t.Setenv("TEST_WUU_KEY", "test")
	cfg := config.Config{
		DefaultProvider: "fake",
		Providers: map[string]config.ProviderConfig{
			"fake": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKeyEnv: "TEST_WUU_KEY", Model: "fake-model"},
		},
		MCPServers: map[string]config.MCPServerConfig{
			"docs":                    {Command: "docs-server"},
			"plugin.cua-mac.computer": {},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"initialize"}`)); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[InitializeResult](t, responseByID(t, msgs, "1")["result"])
	if _, ok := result.GeneralSettings.MCPServerEnabled["plugin.cua-mac.computer"]; ok {
		t.Fatalf("inactive plugin MCP override leaked into general settings: %+v", result.GeneralSettings.MCPServerEnabled)
	}
	if !result.GeneralSettings.MCPServerEnabled["docs"] {
		t.Fatalf("ordinary MCP server missing from general settings: %+v", result.GeneralSettings.MCPServerEnabled)
	}
	for _, record := range result.ExtensionInventory {
		if record.Name == "plugin.cua-mac.computer" {
			t.Fatalf("inactive plugin MCP override leaked into extension inventory: %+v", record)
		}
	}
}

func TestServerInitializeExposesModelSurfaceSummary(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.ProviderName = "openai"
	rt.Model = "gpt-5-codex"
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("New toolkit: %v", err)
	}
	kit.ConfigureSurfaceForProviderModel(rt.ProviderName, rt.Model, true)
	rt.Toolkit = kit

	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"initialize"}`)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"2","method":"config/read"}`)); err != nil {
		t.Fatalf("config/read: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[InitializeResult](t, responseByID(t, msgs, "1")["result"])
	if result.ModelProfile == nil {
		t.Fatalf("initialize missing model profile summary: %+v", result)
	}
	if result.ModelProfile.ProfileName != "openai_codex" || result.ModelProfile.Provider != "openai" || result.ModelProfile.Model != "gpt-5-codex" {
		t.Fatalf("unexpected model profile summary: %+v", result.ModelProfile)
	}
	if result.ModelProfile.EditPrimitive != "apply_patch" || !result.ModelProfile.BashFirst {
		t.Fatalf("unexpected model profile capabilities: %+v", result.ModelProfile)
	}
	if result.ToolSurface == nil {
		t.Fatalf("initialize missing tool surface summary: %+v", result)
	}
	if result.ToolSurface.ToolCapabilityMap["apply_patch"] != "file.edit" {
		t.Fatalf("tool surface missing apply_patch capability: %+v", result.ToolSurface.ToolCapabilityMap)
	}
	if result.ToolSurface.ToolCapabilityMap["bash"] != "command.bash" {
		t.Fatalf("tool surface missing bash capability: %+v", result.ToolSurface.ToolCapabilityMap)
	}
	configResult := remarshal[ConfigReadResult](t, responseByID(t, msgs, "2")["result"])
	if configResult.ModelProfile == nil || configResult.ModelProfile.ProfileName != "openai_codex" {
		t.Fatalf("config/read missing model profile summary: %+v", configResult.ModelProfile)
	}
	if configResult.ToolSurface == nil || configResult.ToolSurface.ToolCapabilityMap["bash"] != "command.bash" {
		t.Fatalf("config/read missing tool surface summary: %+v", configResult.ToolSurface)
	}
}

func TestServerInitializeExposesModelRoles(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	cfg := config.Config{
		DefaultProvider: "fake-provider",
		Providers: map[string]config.ProviderConfig{
			"fake-provider": {
				Type:    "openai-compatible",
				BaseURL: "https://example.test/v1",
				Model:   "fake-model",
			},
		},
		Agent: config.AgentConfig{
			ModelRoles: config.ModelRolesConfig{
				Title: config.ModelRoleConfig{Model: "gpt-4.1-mini"},
			},
		},
	}
	roles, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{})
	if err != nil {
		t.Fatalf("modelroles.Resolve: %v", err)
	}
	rt.ModelRoles = roles
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"initialize"}`)); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	result := remarshal[InitializeResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if len(result.ModelRoles) != 7 {
		t.Fatalf("expected all role summaries, got %+v", result.ModelRoles)
	}
	title := modelRoleByName(t, result.ModelRoles, "title")
	if title.Inherited || title.Model != "gpt-4.1-mini" || title.Behavior.Family != "gpt" {
		t.Fatalf("unexpected title role summary: %+v", title)
	}
	review := modelRoleByName(t, result.ModelRoles, "review")
	if !review.Inherited || review.Model != "fake-model" {
		t.Fatalf("review should inherit main model: %+v", review)
	}
	if !review.Capabilities.Tools || review.Capabilities.ProtocolFamily == "" {
		t.Fatalf("review capabilities missing: %+v", review.Capabilities)
	}
	verification := modelRoleByName(t, result.ModelRoles, "verification")
	if !verification.Inherited || verification.Model != "fake-model" {
		t.Fatalf("verification should inherit main model: %+v", verification)
	}
}

func TestServerInitializeExposesModelAliases(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "model": "fake-model"
    }
  },
  "agent": {
    "model_aliases": {
      "frontend": {
        "provider": "fake-provider",
        "model": "ui-model:latest",
        "effort": "high"
      }
    }
  }
}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"initialize"}`)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	result := remarshal[InitializeResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	alias, ok := result.ModelAliases["frontend"]
	if !ok || alias.Provider != "fake-provider" || alias.Model != "ui-model:latest" || alias.Effort != "high" {
		t.Fatalf("unexpected frontend alias: %+v", result.ModelAliases)
	}
}

func TestProviderSummariesExposeOpenCodeStyleVariants(t *testing.T) {
	cfg := config.Config{
		DefaultProvider: "xiaomi",
		Providers: map[string]config.ProviderConfig{
			"xiaomi": {
				Type:    "openai-compatible",
				BaseURL: "https://token-plan-cn.xiaomimimo.com/v1",
				Model:   "mimo-v2.5-pro",
			},
			"anthropic": {
				Type:    "anthropic",
				BaseURL: "https://anthropic.example.test",
				Model:   "claude-opus-4-6",
			},
			"openai": {
				Type:  "openai",
				Model: "gpt-5.5",
			},
			"openrouter": {
				Type:    "openai-compatible",
				BaseURL: "https://openrouter.ai/api/v1",
				Model:   "openai/gpt-5.5",
			},
		},
	}

	summaries := providerSummariesFromConfig(cfg, t.TempDir())
	var xiaomi, anthropic, openai, openrouter ProviderSummary
	for _, summary := range summaries {
		switch summary.Name {
		case "xiaomi":
			xiaomi = summary
		case "anthropic":
			anthropic = summary
		case "openai":
			openai = summary
		case "openrouter":
			openrouter = summary
		}
	}
	if len(xiaomi.Models) < 2 {
		t.Fatalf("expected xiaomi catalog models, got %+v", xiaomi)
	}
	xiaomiModel := providerModelByID(t, xiaomi, "mimo-v2.5-pro")
	if xiaomiModel.DisplayName != "MiMo-V2.5-Pro" || xiaomiModel.Source != "models.dev" {
		t.Fatalf("unexpected xiaomi model summary: %+v", xiaomiModel)
	}
	if xiaomiModel.Capabilities.ContextWindow != 1048576 || !xiaomiModel.Capabilities.Reasoning {
		t.Fatalf("xiaomi capabilities = %+v", xiaomiModel.Capabilities)
	}
	if xiaomiModel.Behavior.Family != "portable" || xiaomiModel.Behavior.JSONReliability == 0 {
		t.Fatalf("xiaomi behavior = %+v", xiaomiModel.Behavior)
	}
	if got := variantIDs(xiaomiModel.Variants); strings.Join(got, ",") != "low,medium,high" {
		t.Fatalf("xiaomi variants = %+v", got)
	}
	if got := xiaomiModel.Variants[0].Options["reasoningEffort"]; got != "low" {
		t.Fatalf("xiaomi low variant options = %#v", xiaomiModel.Variants[0].Options)
	}
	anthropicModel := providerModelByID(t, anthropic, "claude-opus-4-6")
	if got := variantIDs(anthropicModel.Variants); strings.Join(got, ",") != "low,medium,high,max" {
		t.Fatalf("anthropic variants = %+v", got)
	}
	openaiModel := providerModelByID(t, openai, "gpt-5.5")
	if got := variantIDs(openaiModel.Variants); strings.Join(got, ",") != "none,low,medium,high,xhigh" {
		t.Fatalf("openai variants = %+v", got)
	}
	if got := openaiModel.Variants[0].Options["reasoningSummary"]; got != "auto" {
		t.Fatalf("openai variant options = %#v", openaiModel.Variants[0].Options)
	}
	openrouterModel := providerModelByID(t, openrouter, "openai/gpt-5.5")
	if got := variantIDs(openrouterModel.Variants); strings.Join(got, ",") != "none,low,medium,high,xhigh" {
		t.Fatalf("openrouter variants = %+v", got)
	}
	reasoning, ok := openrouterModel.Variants[0].Options["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "none" {
		t.Fatalf("openrouter variant options = %#v", openrouterModel.Variants[0].Options)
	}
}

func TestProviderHasAuthRequiresAvailableCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WUU_TEST_MISSING_API_KEY", "")
	t.Setenv("WUU_TEST_AVAILABLE_API_KEY", "  available-key  ")
	t.Setenv("WUU_TEST_MISSING_AUTH_TOKEN", "")
	t.Setenv("WUU_TEST_AVAILABLE_AUTH_TOKEN", "  available-token  ")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")

	tests := []struct {
		name     string
		provider config.ProviderConfig
		want     bool
	}{
		{
			name: "configured api key env is missing",
			provider: config.ProviderConfig{
				Type:      "openai-compatible",
				APIKeyEnv: "WUU_TEST_MISSING_API_KEY",
			},
		},
		{
			name: "configured api key env has a value",
			provider: config.ProviderConfig{
				Type:      "openai-compatible",
				APIKeyEnv: "WUU_TEST_AVAILABLE_API_KEY",
			},
			want: true,
		},
		{
			name: "configured auth token env is missing",
			provider: config.ProviderConfig{
				Type:         "anthropic",
				AuthTokenEnv: "WUU_TEST_MISSING_AUTH_TOKEN",
			},
		},
		{
			name: "configured auth token env has a value",
			provider: config.ProviderConfig{
				Type:         "anthropic",
				AuthTokenEnv: "WUU_TEST_AVAILABLE_AUTH_TOKEN",
			},
			want: true,
		},
		{
			name: "direct api key is available",
			provider: config.ProviderConfig{
				Type:   "openai-compatible",
				APIKey: "  direct-key  ",
			},
			want: true,
		},
		{
			name: "unrelated auth token is not accepted by openai wire",
			provider: config.ProviderConfig{
				Type:      "openai-compatible",
				AuthToken: "token",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := providerHasAuth("provider", tt.provider, home); got != tt.want {
				t.Fatalf("providerHasAuth() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProviderHasAuthUsesDefaultEnvironmentAndStoredCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "default-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")

	if !providerHasAuth("openai", config.ProviderConfig{Type: "openai"}, home) {
		t.Fatal("expected the default OpenAI environment variable to be detected")
	}

	store, err := authstorage.ForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("anthropic", authstorage.Credentials{AuthToken: "stored-token"}); err != nil {
		t.Fatal(err)
	}
	if !providerHasAuth("anthropic", config.ProviderConfig{Type: "anthropic"}, home) {
		t.Fatal("expected the stored Anthropic auth token to be detected")
	}
}

func TestProviderHasAuthChecksCodexOAuthAvailability(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WUU_TEST_MISSING_CODEX_KEY", "")
	provider := config.ProviderConfig{
		Type:                  "openai-codex",
		APIKeyEnv:             "WUU_TEST_MISSING_CODEX_KEY",
		ReuseCodexCredentials: true,
	}
	if providerHasAuth("codex-provider", provider, home) {
		t.Fatal("credential reuse and an env name must not report unavailable Codex credentials as configured")
	}

	store, err := authstorage.ForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai-codex", authstorage.Credentials{AccessToken: "stored-access-token"}); err != nil {
		t.Fatal(err)
	}
	provider.ReuseCodexCredentials = false
	if !providerHasAuth("codex-provider", provider, home) {
		t.Fatal("expected Wuu Codex OAuth credentials to be detected without CLI credential reuse")
	}
}

func TestProviderSummariesExposeDiscoveredCodexCLILogin(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	home := os.Getenv("HOME")
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"exp": time.Now().Add(time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_summary",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(claims)
	token := header + "." + payload + ".sig"
	auth := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"` + token + `","refresh_token":"refresh-token"}}`)
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), auth, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", "")
	data := []byte(`{
  "default_provider": "openai-codex",
  "providers": {
    "openai-codex": {
      "type": "openai-codex",
      "model": "gpt-6-astra",
      "reuse_codex_credentials": false
    }
  }
}`)
	if err := os.WriteFile(rt.ConfigPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	summaries := New(rt, &lockedBuffer{}).providerSummaries()
	var discovered *ProviderSummary
	for index := range summaries {
		if summaries[index].Name == "openai-codex" {
			discovered = &summaries[index]
			break
		}
	}
	if discovered == nil {
		t.Fatalf("openai-codex summary missing from %+v", summaries)
	}
	if discovered.ReuseCodexCredentials {
		t.Fatal("discovered Codex login must not imply reuse is already enabled")
	}
	if discovered.CodexCredentialSource != "codex-cli" {
		t.Fatalf("codex credential source = %q, want codex-cli", discovered.CodexCredentialSource)
	}
	if !discovered.ConnectionLocked {
		t.Fatalf("expected locked Codex connection: %+v", discovered)
	}
}

func TestProviderSummariesExposeGrokBuildLoginAndModelDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", "")
	grokHome := filepath.Join(home, ".grok")
	if err := os.MkdirAll(grokHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(grokHome, "auth.json"), []byte(`{"https://accounts.x.ai/sign-in":{"key":"test-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"grok-build": {
			Type: "grok-build", Model: "grok-4.5", ReuseGrokCredentials: true,
		},
	}}
	summaries := providerSummariesFromConfig(cfg, home)
	if len(summaries) != 1 || !summaries[0].APIKeyConfigured || !summaries[0].ConnectionLocked {
		t.Fatalf("summary = %+v", summaries)
	}
	for id, wantEfforts := range map[string]string{
		"grok-4.5": "low,medium,high",
		"grok-4.6": "low,medium,high,xhigh",
	} {
		model := providerModelByID(t, summaries[0], id)
		if got := strings.Join(model.SupportedEfforts, ","); got != wantEfforts {
			t.Fatalf("%s efforts = %q", id, got)
		}
		if model.DefaultEffort != "high" || model.DefaultVariant != "high" || model.Capabilities.ContextWindow != 500_000 {
			t.Fatalf("%s summary = %+v", id, model)
		}
	}
}

func TestProviderSummariesAutoDiscoverLocalGrokBuildLogin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROK_HOME", "")
	grokHome := filepath.Join(home, ".grok")
	t.Setenv("GROK_HOME", grokHome)
	if err := os.MkdirAll(grokHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(grokHome, "auth.json"), []byte(`{"https://auth.x.ai::b1a00492-073a-47ea-816f-4c329264a828":{"key":"local-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	rt := newTestRuntime(t, &fakeClient{})
	cfg := config.Default()
	delete(cfg.Providers, "grok-build")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	summaries := New(rt, &lockedBuffer{}).providerSummaries()
	var discovered *ProviderSummary
	for index := range summaries {
		if summaries[index].Name == "grok-build" {
			discovered = &summaries[index]
			break
		}
	}
	if discovered == nil {
		t.Fatalf("auto-discovered Grok provider missing from %+v", summaries)
	}
	if !discovered.AutoDiscovered || !discovered.APIKeyConfigured || !discovered.ConnectionLocked {
		t.Fatalf("auto-discovered Grok summary = %+v", discovered)
	}
	model := providerModelByID(t, *discovered, "grok-4.6")
	if got := strings.Join(model.SupportedEfforts, ","); got != "low,medium,high,xhigh" {
		t.Fatalf("grok-4.6 efforts = %q", got)
	}
}

func TestProviderSummariesDoNotInventGrokBuildWithoutLocalLogin(t *testing.T) {
	t.Setenv("GROK_HOME", filepath.Join(t.TempDir(), "missing"))
	rt := newTestRuntime(t, &fakeClient{})
	cfg := config.Default()
	delete(cfg.Providers, "grok-build")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, summary := range New(rt, &lockedBuffer{}).providerSummaries() {
		if summary.Name == "grok-build" {
			t.Fatalf("unexpected Grok provider without local login: %+v", summary)
		}
	}
}

func TestSelectingAutoDiscoveredGrokBuildPersistsProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROK_HOME", "")
	grokHome := filepath.Join(home, ".grok")
	t.Setenv("GROK_HOME", grokHome)
	if err := os.MkdirAll(grokHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(grokHome, "auth.json"), []byte(`{"https://auth.x.ai::b1a00492-073a-47ea-816f-4c329264a828":{"key":"local-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	rt := newTestRuntime(t, &fakeClient{})
	cfg := config.Default()
	delete(cfg.Providers, "grok-build")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	request := `{"id":"grok","method":"config/model/update","params":{"provider":"grok-build","model":"grok-4.6","variant":"xhigh"}}`
	if err := srv.handleLine(context.Background(), []byte(request)); err != nil {
		t.Fatalf("select auto-discovered Grok provider: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "grok")
	if response["error"] != nil {
		t.Fatalf("select response = %+v", response)
	}

	persisted, _, err := config.LoadPath(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := persisted.Providers["grok-build"]
	if !ok {
		t.Fatalf("persisted providers = %+v", persisted.Providers)
	}
	if provider.Model != "grok-4.6" || !provider.ReuseGrokCredentials || provider.WireAPI != "chat" {
		t.Fatalf("persisted Grok provider = %+v", provider)
	}
}

func TestProviderSummariesExposeGPT56CatalogForCompatibleGateway(t *testing.T) {
	cfg := config.Config{
		DefaultProvider: "gateway",
		Providers: map[string]config.ProviderConfig{
			"gateway": {
				Type:    "openai-compatible",
				BaseURL: "https://gateway.example.test/codex/v1",
				WireAPI: "responses",
				Model:   "gpt-5.6-sol",
			},
		},
	}

	summaries := providerSummariesFromConfig(cfg, t.TempDir())
	if len(summaries) != 1 {
		t.Fatalf("unexpected summaries: %+v", summaries)
	}
	model := providerModelByID(t, summaries[0], "gpt-5.6-sol")
	if model.Source != "selected" || model.DisplayName != "GPT-5.6 Sol" {
		t.Fatalf("unexpected selected model summary: %+v", model)
	}
	if got := strings.Join(variantIDs(model.Variants), ","); got != "none,low,medium,high,xhigh,max" {
		t.Fatalf("variants = %q", got)
	}
	if got := strings.Join(model.SupportedEfforts, ","); got != "none,low,medium,high,xhigh,max" {
		t.Fatalf("supported efforts = %q", got)
	}
	if model.Capabilities.ContextWindow != 1_050_000 || !model.Capabilities.Reasoning {
		t.Fatalf("capabilities = %+v", model.Capabilities)
	}
}

func TestProviderSummariesEnrichPartialGPT56ConfigForCompatibleGateway(t *testing.T) {
	cfg := config.Config{
		DefaultProvider: "gateway",
		Providers: map[string]config.ProviderConfig{
			"gateway": {
				Type:    "openai-compatible",
				BaseURL: "https://gateway.example.test/codex/v1",
				WireAPI: "responses",
				Model:   "gpt-5.6-sol",
				Models: map[string]config.ProviderModelConfig{
					"gpt-5.6-sol": {Name: "Gateway GPT-5.6 Sol"},
				},
			},
		},
	}

	summaries := providerSummariesFromConfig(cfg, t.TempDir())
	if len(summaries) != 1 {
		t.Fatalf("unexpected summaries: %+v", summaries)
	}
	model := providerModelByID(t, summaries[0], "gpt-5.6-sol")
	if model.Source != "config" || model.DisplayName != "Gateway GPT-5.6 Sol" {
		t.Fatalf("unexpected configured model summary: %+v", model)
	}
	if got := strings.Join(variantIDs(model.Variants), ","); got != "none,low,medium,high,xhigh,max" {
		t.Fatalf("variants = %q", got)
	}
	if got := strings.Join(model.SupportedEfforts, ","); got != "none,low,medium,high,xhigh,max" {
		t.Fatalf("supported efforts = %q", got)
	}
	if model.Capabilities.ContextWindow != 1_050_000 || model.Capabilities.InputLimit != 922_000 || model.Capabilities.OutputLimit != 128_000 {
		t.Fatalf("capabilities = %+v", model.Capabilities)
	}
}

func TestProviderSummariesMergeConfiguredVariants(t *testing.T) {
	cfg := config.Config{
		DefaultProvider: "custom",
		Providers: map[string]config.ProviderConfig{
			"custom": {
				Type:    "openai-compatible",
				BaseURL: "https://example.test/v1",
				Model:   "custom-model",
				Models: map[string]config.ProviderModelConfig{
					"custom-model": {
						DefaultVariant: "deep",
						Variants: map[string]map[string]any{
							"deep":     {"reasoningEffort": "high"},
							"disabled": {"disabled": true, "reasoningEffort": "low"},
						},
					},
				},
			},
		},
	}

	summaries := providerSummariesFromConfig(cfg, t.TempDir())
	if len(summaries) != 1 || len(summaries[0].Models) != 1 {
		t.Fatalf("unexpected summaries: %+v", summaries)
	}
	model := summaries[0].Models[0]
	if model.DefaultVariant != "deep" {
		t.Fatalf("DefaultVariant = %q, want deep", model.DefaultVariant)
	}
	if got := variantIDs(model.Variants); strings.Join(got, ",") != "deep" {
		t.Fatalf("variants = %+v", got)
	}
}

func TestProviderSummariesPreferConfiguredVariantsOverCatalog(t *testing.T) {
	cfg := config.Config{
		DefaultProvider: "openai",
		Providers: map[string]config.ProviderConfig{
			"openai": {
				Type:  "openai",
				Model: "gpt-5.5",
				Models: map[string]config.ProviderModelConfig{
					"gpt-5.5": {
						DefaultVariant: "deep",
						Variants: map[string]map[string]any{
							"deep": {"reasoningEffort": "high"},
						},
					},
				},
			},
		},
	}

	summaries := providerSummariesFromConfig(cfg, t.TempDir())
	model := providerModelByID(t, summaries[0], "gpt-5.5")
	if model.Source != "config" || model.DisplayName != "GPT-5.5" {
		t.Fatalf("unexpected configured model summary: %+v", model)
	}
	if got := variantIDs(model.Variants); strings.Join(got, ",") != "deep" {
		t.Fatalf("variants = %+v", got)
	}
}

func TestProviderSummariesDoNotShowOfficialModelsForCustomAnthropicEndpoint(t *testing.T) {
	cfg := config.Config{
		DefaultProvider: "zhipu2",
		Providers: map[string]config.ProviderConfig{
			"zhipu2": {
				Type:    "anthropic",
				BaseURL: "https://open.bigmodel.cn/api/anthropic",
				Model:   "glm-5.1",
			},
		},
	}

	summaries := providerSummariesFromConfig(cfg, t.TempDir())
	if len(summaries) != 1 {
		t.Fatalf("unexpected summaries: %+v", summaries)
	}
	if len(summaries[0].Models) != 1 || summaries[0].Models[0].ID != "glm-5.1" {
		t.Fatalf("custom endpoint should only expose configured model, got %+v", summaries[0].Models)
	}
}

func TestProviderSummariesExposeCodexSubscriptionAliasesOnly(t *testing.T) {
	cfg := config.Config{
		DefaultProvider: "openai-codex",
		Providers: map[string]config.ProviderConfig{
			"openai-codex": {
				Type:    "openai-codex",
				BaseURL: "https://chatgpt.com/backend-api/codex",
				Model:   "gpt-5.5",
			},
		},
	}

	summaries := providerSummariesFromConfig(cfg, t.TempDir())
	if len(summaries) != 1 {
		t.Fatalf("unexpected summaries: %+v", summaries)
	}
	if len(summaries[0].Models) != 2 {
		t.Fatalf("Codex subscription should expose selected model and safe aliases before live load, got %+v", summaries[0].Models)
	}
	if providerModelByID(t, summaries[0], "gpt-5.5").ID != "gpt-5.5" {
		t.Fatalf("selected Codex model missing: %+v", summaries[0].Models)
	}
	fast := providerModelByID(t, summaries[0], "gpt-5.5-fast")
	if fast.DisplayName != "GPT-5.5 Fast" || fast.Source != "models.dev" {
		t.Fatalf("unexpected Codex fast alias summary: %+v", fast)
	}
}

func TestProviderSummariesClampCodexCatalogInputLimit(t *testing.T) {
	cfg := config.Config{
		DefaultProvider: "openai-codex",
		Providers: map[string]config.ProviderConfig{
			"openai-codex": {
				Type:    "openai-codex",
				BaseURL: "https://chatgpt.com/backend-api/codex",
				Model:   "gpt-5.6-sol",
				Models: map[string]config.ProviderModelConfig{
					"gpt-5.6-sol": {
						ContextWindow: 1_050_000,
						Limit: &config.ProviderModelLimitConfig{
							Context: 1_050_000,
							Input:   922_000,
							Output:  128_000,
						},
					},
				},
			},
		},
	}

	summaries := providerSummariesFromConfig(cfg, t.TempDir())
	if len(summaries) != 1 {
		t.Fatalf("unexpected summaries: %+v", summaries)
	}
	model := providerModelByID(t, summaries[0], "gpt-5.6-sol")
	if model.Capabilities.ContextWindow != 1_050_000 {
		t.Fatalf("ContextWindow = %d, want catalog model window 1050000", model.Capabilities.ContextWindow)
	}
	if model.Capabilities.InputLimit != 272_000 {
		t.Fatalf("InputLimit = %d, want Codex subscription clamp 272000; capabilities=%+v", model.Capabilities.InputLimit, model.Capabilities)
	}
}

func providerModelByID(t *testing.T, provider ProviderSummary, id string) ProviderModelSummary {
	t.Helper()
	for _, model := range provider.Models {
		if model.ID == id {
			return model
		}
	}
	t.Fatalf("model %s not found in provider %+v", id, provider)
	return ProviderModelSummary{}
}

func modelRoleByName(t *testing.T, roles []ModelRoleSummary, name string) ModelRoleSummary {
	t.Helper()
	for _, role := range roles {
		if role.Role == name {
			return role
		}
	}
	t.Fatalf("role %s not found in %+v", name, roles)
	return ModelRoleSummary{}
}

func variantIDs(variants []ProviderModelVariantSummary) []string {
	out := make([]string, 0, len(variants))
	for _, variant := range variants {
		out = append(out, variant.ID)
	}
	return out
}

func TestServerConfigModelUpdate(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "model": "fake-model"
    }
	  }
}
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"model":"new-model"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[ConfigModelUpdateResult](t, responseByID(t, msgs, "1")["result"])
	if result.Provider != "fake-provider" || result.Model != "new-model" {
		t.Fatalf("unexpected update result: %+v", result)
	}
	if len(result.Providers) != 1 || result.Providers[0].Name != "fake-provider" || result.Providers[0].Model != "new-model" {
		t.Fatalf("unexpected provider summaries: %+v", result.Providers)
	}
	if rt.Model != "new-model" || rt.StreamRunner.Model != "new-model" {
		t.Fatalf("runtime model not updated: runtime=%q stream_runner=%q", rt.Model, rt.StreamRunner.Model)
	}
	if rt.StreamRunner.ContextWindowOverride != 0 {
		t.Fatalf("context window override not updated: got %d", rt.StreamRunner.ContextWindowOverride)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"model": "new-model"`) {
		t.Fatalf("config model was not persisted: %s", data)
	}
}

func TestProviderClientConfigChangedDetectsAuthAndEndpointChanges(t *testing.T) {
	base := config.ProviderConfig{
		Type:      "openai-compatible",
		BaseURL:   "https://api.deepseek.example/v1",
		APIKeyEnv: "DEEPSEEK_API_KEY",
		Headers:   map[string]string{"X-Test": "same"},
	}
	next := base
	next.Type = "anthropic"
	next.BaseURL = "https://api.minimax.example/anthropic/v1"
	next.APIKeyEnv = "MINIMAX_API_KEY"
	if !providerClientConfigChanged(base, next) {
		t.Fatal("expected provider client config change for endpoint/wire/auth env switch")
	}
	if providerClientConfigChanged(base, base) {
		t.Fatal("identical provider client config should not require a rebuild")
	}
}

func TestCachedThreadRestoresPersistedModelSelection(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "api_key": "test-key",
      "model": "fake-model"
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateWithMetadata(rt.SessionDir, "persisted-model-thread", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if _, err := session.SetModelSelection(rt.SessionDir, "persisted-model-thread", rt.ProviderName, "persisted-model", ""); err != nil {
		t.Fatal(err)
	}
	srv := New(rt, &lockedBuffer{})
	th, err := srv.ensureThreadLoaded("persisted-model-thread")
	if err != nil {
		t.Fatal(err)
	}
	if th.ModelProvider != rt.ProviderName || th.Model != "persisted-model" {
		t.Fatalf("restored thread model = %s/%s", th.ModelProvider, th.Model)
	}
	threadRuntime, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	if threadRuntime.StreamRunner.Model != "persisted-model" {
		t.Fatalf("restored runtime model = %q", threadRuntime.StreamRunner.Model)
	}
}

func TestServerConfigModelUpdateRejectsRunningTargetThread(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "agent": {"permission_mode": "read_only"},
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "api_key": "test-key",
      "model": "fake-model"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	now := time.Now().UTC()

	running := newThreadState("running-thread", nil, rt.ProviderName, rt.Model, rt.RootDir, true, now)
	running.PermissionMode = config.PermissionModeReadOnly
	running.startTurnLocked("running-turn", providers.ChatMessage{Role: "user", Content: "keep running"}, now)
	srv.threads[running.ID] = running

	if _, err := session.CreateWithMetadata(rt.SessionDir, running.ID, rt.RootDir); err != nil {
		t.Fatalf("create running session: %v", err)
	}
	if _, err := session.SetRuntimeSelection(rt.SessionDir, running.ID, session.RuntimeSelection{
		Provider:       rt.ProviderName,
		Model:          rt.Model,
		PermissionMode: config.PermissionModeReadOnly,
	}); err != nil {
		t.Fatalf("pin running selection: %v", err)
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"thread_id":"running-thread","model":"new-model","permission_mode":"unconfined"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "cannot change model or permission mode") {
		t.Fatalf("expected running-thread rejection, got %+v", response["error"])
	}
	if rt.Model != "fake-model" || rt.Permissions.Mode != config.PermissionModeReadOnly {
		t.Fatalf("failed update changed workspace defaults: model=%q permission=%q", rt.Model, rt.Permissions.Mode)
	}
	metadata, ok, err := session.Find(rt.SessionDir, running.ID)
	if err != nil || !ok {
		t.Fatalf("find running session: ok=%v err=%v", ok, err)
	}
	if metadata.Model != "fake-model" || metadata.PermissionMode != config.PermissionModeReadOnly {
		t.Fatalf("failed update changed running session: %+v", metadata)
	}
}

func TestServerConfigModelUpdateWorkspaceDefaultsPreserveRunningSession(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "agent": {"permission_mode": "read_only"},
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "api_key": "test-key",
      "model": "fake-model"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	now := time.Now().UTC()

	running := newThreadState("running-thread", nil, rt.ProviderName, rt.Model, rt.RootDir, true, now)
	running.PermissionMode = config.PermissionModeReadOnly
	running.startTurnLocked("running-turn", providers.ChatMessage{Role: "user", Content: "keep running"}, now)
	srv.threads[running.ID] = running

	if _, err := session.CreateWithMetadata(rt.SessionDir, running.ID, rt.RootDir); err != nil {
		t.Fatalf("create running session: %v", err)
	}
	if _, err := session.SetRuntimeSelection(rt.SessionDir, running.ID, session.RuntimeSelection{
		Provider:       rt.ProviderName,
		Model:          rt.Model,
		PermissionMode: config.PermissionModeReadOnly,
	}); err != nil {
		t.Fatalf("pin running selection: %v", err)
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"model":"new-model"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] != nil {
		t.Fatalf("workspace update failed: %+v", response["error"])
	}
	if rt.Model != "new-model" || running.Model != "fake-model" {
		t.Fatalf("workspace/session selections were not isolated: workspace=%q session=%q", rt.Model, running.Model)
	}
	metadata, ok, err := session.Find(rt.SessionDir, running.ID)
	if err != nil || !ok {
		t.Fatalf("find running session: ok=%v err=%v", ok, err)
	}
	if metadata.Model != "fake-model" || metadata.PermissionMode != config.PermissionModeReadOnly {
		t.Fatalf("failed update changed running session: %+v", metadata)
	}
}

func TestServerConfigModelUpdateRejectsTargetOwnedByAnotherServer(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "model": "fake-model"
    }
  }
}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := session.CreateWithMetadata(rt.SessionDir, "leased-thread", rt.RootDir); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := session.SetRuntimeSelection(rt.SessionDir, "leased-thread", session.RuntimeSelection{
		Provider:       rt.ProviderName,
		Model:          rt.Model,
		PermissionMode: config.PermissionModeStandard,
	}); err != nil {
		t.Fatalf("pin selection: %v", err)
	}
	lease, acquired, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, "leased-thread")
	if err != nil || !acquired {
		t.Fatalf("acquire external lease: acquired=%v err=%v", acquired, err)
	}
	defer func() {
		if err := lease.Release(); err != nil {
			t.Fatalf("release external lease: %v", err)
		}
	}()

	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"thread_id":"leased-thread","model":"new-model"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "cannot change model or permission mode") {
		t.Fatalf("expected external running-thread rejection, got %+v", response["error"])
	}
	if rt.Model != "fake-model" {
		t.Fatalf("failed external update changed workspace default to %q", rt.Model)
	}
}

func TestServerConfigModelUpdateScopesSelectionToTargetOnly(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "agent": {"permission_mode": "read_only"},
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "api_key": "test-key",
      "model": "fake-model"
    }
  }
}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	now := time.Now().UTC()
	target := newThreadState("target-thread", nil, rt.ProviderName, rt.Model, rt.RootDir, true, now)
	other := newThreadState("other-thread", nil, rt.ProviderName, rt.Model, rt.RootDir, true, now)
	for _, th := range []*threadState{target, other} {
		th.PermissionMode = config.PermissionModeReadOnly
		srv.threads[th.ID] = th
		if _, err := session.CreateWithMetadata(rt.SessionDir, th.ID, rt.RootDir); err != nil {
			t.Fatalf("create %s: %v", th.ID, err)
		}
		if _, err := session.SetRuntimeSelection(rt.SessionDir, th.ID, session.RuntimeSelection{
			Provider:       rt.ProviderName,
			Model:          rt.Model,
			PermissionMode: config.PermissionModeReadOnly,
		}); err != nil {
			t.Fatalf("pin %s: %v", th.ID, err)
		}
	}

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"thread_id":"target-thread","model":"new-model","effort":"xhigh","permission_mode":"unconfined"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] != nil {
		t.Fatalf("unexpected update error: %+v", response["error"])
	}
	if rt.Model != "fake-model" || rt.Permissions.Mode != config.PermissionModeReadOnly {
		t.Fatalf("conversation selection changed workspace defaults: model=%q permission=%q", rt.Model, rt.Permissions.Mode)
	}
	target.mu.Lock()
	if target.Model != "new-model" || target.ModelEffort != "" || target.PermissionMode != config.PermissionModeUnconfined {
		t.Fatalf("target selection not updated: model=%q effort=%q permission=%q", target.Model, target.ModelEffort, target.PermissionMode)
	}
	target.mu.Unlock()
	other.mu.Lock()
	if other.Model != "fake-model" || other.ModelEffort != "" || other.PermissionMode != config.PermissionModeReadOnly {
		t.Fatalf("non-target selection changed: model=%q effort=%q permission=%q", other.Model, other.ModelEffort, other.PermissionMode)
	}
	other.mu.Unlock()
	metadata, ok, err := session.Find(rt.SessionDir, other.ID)
	if err != nil || !ok {
		t.Fatalf("find non-target session: ok=%v err=%v", ok, err)
	}
	if metadata.Model != "fake-model" || metadata.PermissionMode != config.PermissionModeReadOnly {
		t.Fatalf("non-target persisted selection changed: %+v", metadata)
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"2","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start after default update: %v", err)
	}
	created := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "2")["result"]).Thread
	if created.Model != "fake-model" || created.ModelEffort != "" || created.PermissionMode != config.PermissionModeReadOnly {
		t.Fatalf("future thread inherited another conversation's selection: %+v", created)
	}
}

func newTargetedUpdateFixture(t *testing.T, rt *runtime.Session, srv *Server) *threadState {
	t.Helper()
	target := newThreadState("target-thread", nil, rt.ProviderName, "thread-model", rt.RootDir, true, time.Now().UTC())
	target.ModelEffort = "low"
	target.PermissionMode = config.PermissionModeReadOnly
	srv.threads[target.ID] = target
	if _, err := session.CreateWithMetadata(rt.SessionDir, target.ID, rt.RootDir); err != nil {
		t.Fatalf("create target session: %v", err)
	}
	if _, err := session.SetRuntimeSelection(rt.SessionDir, target.ID, session.RuntimeSelection{
		Provider:       rt.ProviderName,
		Model:          "thread-model",
		Effort:         "low",
		PermissionMode: config.PermissionModeReadOnly,
	}); err != nil {
		t.Fatalf("pin target selection: %v", err)
	}
	return target
}

func writeTargetedUpdateConfig(t *testing.T, rt *runtime.Session) {
	t.Helper()
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "agent": {"permission_mode": "read_only", "effort": "high"},
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "api_key": "test-key",
      "model": "fake-model"
    }
  }
}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestServerConfigModelUpdatePermissionOnlyTargetKeepsModelSelection(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	rt.StreamRunner.Effort = "high"
	writeTargetedUpdateConfig(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	target := newTargetedUpdateFixture(t, rt, srv)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"thread_id":"target-thread","permission_mode":"unconfined"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] != nil {
		t.Fatalf("unexpected update error: %+v", response["error"])
	}
	if rt.Permissions.Mode != config.PermissionModeReadOnly {
		t.Fatalf("workspace permission changed: %q", rt.Permissions.Mode)
	}
	if rt.Model != "fake-model" || rt.StreamRunner.Model != "fake-model" ||
		rt.StreamRunner.Effort != "high" || rt.StreamRunner.Variant != "" {
		t.Fatalf("permission-only update changed workspace selection: model=%q runner=%q effort=%q variant=%q",
			rt.Model, rt.StreamRunner.Model, rt.StreamRunner.Effort, rt.StreamRunner.Variant)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"permission_mode": "read_only"`) ||
		!strings.Contains(string(data), `"model": "fake-model"`) ||
		!strings.Contains(string(data), `"effort": "high"`) {
		t.Fatalf("persisted config drifted beyond permission mode: %s", data)
	}
	target.mu.Lock()
	if target.Model != "thread-model" || target.ModelEffort != "low" || target.ModelVariant != "" ||
		target.PermissionMode != config.PermissionModeUnconfined {
		t.Fatalf("target selection wrong: model=%q effort=%q variant=%q permission=%q",
			target.Model, target.ModelEffort, target.ModelVariant, target.PermissionMode)
	}
	target.mu.Unlock()
	metadata, ok, err := session.Find(rt.SessionDir, target.ID)
	if err != nil || !ok {
		t.Fatalf("find target session: ok=%v err=%v", ok, err)
	}
	if metadata.Model != "thread-model" || metadata.Effort != "low" || metadata.PermissionMode != config.PermissionModeUnconfined {
		t.Fatalf("persisted target selection wrong: %+v", metadata)
	}
}

func TestServerConfigModelUpdateModelOnlyTargetResetsUnsupportedEffortAndKeepsPermission(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	rt.StreamRunner.Effort = "high"
	writeTargetedUpdateConfig(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	target := newTargetedUpdateFixture(t, rt, srv)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"thread_id":"target-thread","model":"new-model"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] != nil {
		t.Fatalf("unexpected update error: %+v", response["error"])
	}
	if rt.Model != "fake-model" || rt.StreamRunner.Model != "fake-model" {
		t.Fatalf("workspace model changed: model=%q runner=%q", rt.Model, rt.StreamRunner.Model)
	}
	if rt.StreamRunner.Effort != "high" || rt.StreamRunner.Variant != "" || rt.Permissions.Mode != config.PermissionModeReadOnly {
		t.Fatalf("model-only update changed workspace effort/variant/permission: effort=%q variant=%q permission=%q",
			rt.StreamRunner.Effort, rt.StreamRunner.Variant, rt.Permissions.Mode)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"model": "fake-model"`) ||
		!strings.Contains(string(data), `"effort": "high"`) ||
		!strings.Contains(string(data), `"permission_mode": "read_only"`) {
		t.Fatalf("persisted config drifted beyond model: %s", data)
	}
	target.mu.Lock()
	if target.Model != "new-model" || target.ModelEffort != "" || target.PermissionMode != config.PermissionModeReadOnly {
		t.Fatalf("target selection wrong: model=%q effort=%q permission=%q",
			target.Model, target.ModelEffort, target.PermissionMode)
	}
	target.mu.Unlock()
	metadata, ok, err := session.Find(rt.SessionDir, target.ID)
	if err != nil || !ok {
		t.Fatalf("find target session: ok=%v err=%v", ok, err)
	}
	if metadata.Model != "new-model" || metadata.Effort != "" || metadata.PermissionMode != config.PermissionModeReadOnly {
		t.Fatalf("persisted target selection wrong: %+v", metadata)
	}
}

func TestServerConfigModelUpdateExplicitEmptyVariantClearsSelection(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	rt.StreamRunner.Effort = "high"
	writeTargetedUpdateConfig(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	target := newTargetedUpdateFixture(t, rt, srv)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"thread_id":"target-thread","variant":""}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] != nil {
		t.Fatalf("unexpected update error: %+v", response["error"])
	}
	if rt.StreamRunner.Effort != "high" || rt.StreamRunner.Variant != "" {
		t.Fatalf("explicit empty variant changed workspace selection: effort=%q variant=%q",
			rt.StreamRunner.Effort, rt.StreamRunner.Variant)
	}
	if rt.Model != "fake-model" {
		t.Fatalf("variant-only update changed workspace model: %q", rt.Model)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"effort": "high"`) || strings.Contains(string(data), `"variant"`) {
		t.Fatalf("workspace selection changed: %s", data)
	}
	target.mu.Lock()
	if target.Model != "thread-model" || target.ModelEffort != "" || target.ModelVariant != "" {
		t.Fatalf("target selection wrong: model=%q effort=%q variant=%q",
			target.Model, target.ModelEffort, target.ModelVariant)
	}
	target.mu.Unlock()
	metadata, ok, err := session.Find(rt.SessionDir, target.ID)
	if err != nil || !ok {
		t.Fatalf("find target session: ok=%v err=%v", ok, err)
	}
	if metadata.Model != "thread-model" || metadata.Effort != "" || metadata.Variant != "" {
		t.Fatalf("persisted target selection wrong: %+v", metadata)
	}
}

func writeForeignProviderUpdateConfig(t *testing.T, rt *runtime.Session) {
	t.Helper()
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "agent": {"permission_mode": "read_only", "effort": "high"},
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "api_key": "test-key",
      "model": "fake-model"
    },
    "xiaomi": {
      "type": "openai-compatible",
      "base_url": "https://token-plan-cn.xiaomimimo.com/v1",
      "api_key": "test-key",
      "model": "mimo-v2.5-pro"
    }
  }
}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func newForeignProviderTargetFixture(t *testing.T, rt *runtime.Session, srv *Server) *threadState {
	t.Helper()
	target := newThreadState("foreign-thread", nil, "xiaomi", "mimo-v2.5-pro", rt.RootDir, true, time.Now().UTC())
	target.PermissionMode = config.PermissionModeReadOnly
	srv.threads[target.ID] = target
	if _, err := session.CreateWithMetadata(rt.SessionDir, target.ID, rt.RootDir); err != nil {
		t.Fatalf("create target session: %v", err)
	}
	if _, err := session.SetRuntimeSelection(rt.SessionDir, target.ID, session.RuntimeSelection{
		Provider:       "xiaomi",
		Model:          "mimo-v2.5-pro",
		PermissionMode: config.PermissionModeReadOnly,
	}); err != nil {
		t.Fatalf("pin target selection: %v", err)
	}
	return target
}

// A targeted model-only update on a thread pinned to a different provider is
// thread-scoped: grafting the new model onto the workspace provider would
// persist an incoherent (provider, model) pair the workspace provider cannot
// serve.
func TestServerConfigModelUpdateTargetedModelOnlyForeignProviderStaysThreadScoped(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	rt.StreamRunner.Effort = "high"
	writeForeignProviderUpdateConfig(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	target := newForeignProviderTargetFixture(t, rt, srv)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"thread_id":"foreign-thread","model":"mimo-v2.5"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] != nil {
		t.Fatalf("unexpected update error: %+v", response["error"])
	}
	result := remarshal[ConfigModelUpdateResult](t, response["result"])
	if result.Provider != "fake-provider" || result.Model != "fake-model" {
		t.Fatalf("result should stay workspace-effective: %+v", result)
	}
	if rt.ProviderName != "fake-provider" || rt.Model != "fake-model" || rt.StreamRunner.Model != "fake-model" {
		t.Fatalf("workspace runtime drifted: provider=%q model=%q runner=%q",
			rt.ProviderName, rt.Model, rt.StreamRunner.Model)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"model": "fake-model"`) ||
		!strings.Contains(string(data), `"model": "mimo-v2.5-pro"`) ||
		!strings.Contains(string(data), `"default_provider": "fake-provider"`) {
		t.Fatalf("workspace config drifted: %s", data)
	}
	target.mu.Lock()
	if target.ModelProvider != "xiaomi" || target.Model != "mimo-v2.5" {
		t.Fatalf("target pin wrong: provider=%q model=%q", target.ModelProvider, target.Model)
	}
	target.mu.Unlock()
	metadata, ok, err := session.Find(rt.SessionDir, target.ID)
	if err != nil || !ok {
		t.Fatalf("find target session: ok=%v err=%v", ok, err)
	}
	if metadata.Provider != "xiaomi" || metadata.Model != "mimo-v2.5" {
		t.Fatalf("persisted target selection wrong: %+v", metadata)
	}
}

// A targeted variant/effort-only update validates against the thread's pinned
// model, not the workspace model, and on a foreign-provider thread the
// variant stays out of the workspace config entirely.
func TestServerConfigModelUpdateTargetedVariantOnlyValidatesAgainstThreadModel(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	rt.StreamRunner.Effort = "high"
	writeForeignProviderUpdateConfig(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	target := newForeignProviderTargetFixture(t, rt, srv)

	// "high" is a mimo-v2.5-pro variant; the workspace fake-model has no
	// variants, so validating against the workspace model would hard-reject
	// this update.
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"thread_id":"foreign-thread","variant":"high"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] != nil {
		t.Fatalf("variant must validate against the thread model, got %+v", response["error"])
	}
	if rt.StreamRunner.Variant != "" || rt.StreamRunner.Effort != "high" || rt.Model != "fake-model" {
		t.Fatalf("thread-scoped variant leaked into the workspace: variant=%q effort=%q model=%q",
			rt.StreamRunner.Variant, rt.StreamRunner.Effort, rt.Model)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), `"variant"`) || !strings.Contains(string(data), `"effort": "high"`) {
		t.Fatalf("workspace selection drifted: %s", data)
	}
	target.mu.Lock()
	if target.ModelProvider != "xiaomi" || target.Model != "mimo-v2.5-pro" || target.ModelVariant != "high" {
		t.Fatalf("target pin wrong: provider=%q model=%q variant=%q",
			target.ModelProvider, target.Model, target.ModelVariant)
	}
	target.mu.Unlock()
	metadata, ok, err := session.Find(rt.SessionDir, target.ID)
	if err != nil || !ok {
		t.Fatalf("find target session: ok=%v err=%v", ok, err)
	}
	if metadata.Model != "mimo-v2.5-pro" || metadata.Variant != "high" {
		t.Fatalf("persisted target selection wrong: %+v", metadata)
	}
}

func TestServerConfigModelUpdateConnectionOnlyTargetAllowedWhileRunning(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	rt.StreamRunner.Effort = "high"
	writeTargetedUpdateConfig(t, rt)
	oldClient := rt.StreamRunner.Client
	out := &lockedBuffer{}
	srv := New(rt, out)
	target := newTargetedUpdateFixture(t, rt, srv)
	target.mu.Lock()
	target.startTurnLocked("running-turn", providers.ChatMessage{Role: "user", Content: "keep running"}, time.Now().UTC())
	target.mu.Unlock()

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"thread_id":"target-thread","base_url":"https://custom.example.test/v1","api_key":"new-key"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] != nil {
		t.Fatalf("connection-only targeted update should succeed while running, got %+v", response["error"])
	}
	if rt.StreamRunner.Client == oldClient {
		t.Fatal("expected stream runner client to be rebuilt")
	}
	if rt.Model != "fake-model" || rt.StreamRunner.Model != "fake-model" ||
		rt.StreamRunner.Effort != "high" || rt.Permissions.Mode != config.PermissionModeReadOnly {
		t.Fatalf("connection-only update changed workspace selection: model=%q runner=%q effort=%q permission=%q",
			rt.Model, rt.StreamRunner.Model, rt.StreamRunner.Effort, rt.Permissions.Mode)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"base_url": "https://custom.example.test/v1"`) ||
		!strings.Contains(string(data), `"model": "fake-model"`) ||
		!strings.Contains(string(data), `"effort": "high"`) {
		t.Fatalf("connection change not persisted or selection drifted: %s", data)
	}
	target.mu.Lock()
	if target.Model != "thread-model" || target.ModelEffort != "low" || target.PermissionMode != config.PermissionModeReadOnly {
		t.Fatalf("connection-only update touched target selection: model=%q effort=%q permission=%q",
			target.Model, target.ModelEffort, target.PermissionMode)
	}
	target.mu.Unlock()
	metadata, ok, err := session.Find(rt.SessionDir, target.ID)
	if err != nil || !ok {
		t.Fatalf("find target session: ok=%v err=%v", ok, err)
	}
	if metadata.Model != "thread-model" || metadata.Effort != "low" || metadata.PermissionMode != config.PermissionModeReadOnly {
		t.Fatalf("connection-only update touched persisted target selection: %+v", metadata)
	}
}

func TestRunningTurnUsesModelSnapshotAfterConfigUpdate(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	streamClient := usageStreamClient{events: []providers.StreamEvent{
		{Type: providers.EventUsage, Usage: &providers.TokenUsage{InputTokens: 7, OutputTokens: 3}},
		{Type: providers.EventDone, Usage: &providers.TokenUsage{InputTokens: 7, OutputTokens: 3}},
	}}
	rt.StreamRunner.Client = streamClient
	rt.StreamRunner.Model = "fake-model"
	rt.StreamRunner.APIModel = "fake-model"
	out := &lockedBuffer{}
	srv := New(rt, out)
	if _, err := session.CreateWithMetadata(rt.SessionDir, "running-thread", rt.RootDir); err != nil {
		t.Fatalf("create session: %v", err)
	}
	threadRuntime := &runtime.ThreadRuntime{
		StreamRunner: &agent.StreamRunner{
			Client:   streamClient,
			Model:    "fake-model",
			APIModel: "fake-model",
		},
	}
	th := newThreadState("running-thread", nil, rt.ProviderName, rt.Model, rt.RootDir, true, time.Now().UTC())
	th.execRuntime = threadRuntime
	userMsg := providers.ChatMessage{Role: "user", Content: "hello"}
	turnID := "turn-snapshot"
	now := time.Now().UTC()
	th.mu.Lock()
	th.History = append(th.History, userMsg)
	th.startTurnLocked(turnID, userMsg, now)
	turnRuntime := turnRuntimeSnapshotLocked(th)
	th.mu.Unlock()
	srv.threads[th.ID] = th

	if err := srv.handleLine(context.Background(), []byte(`{"id":"default-update","method":"config/model/update","params":{"model":"new-model"}}`)); err != nil {
		t.Fatalf("update workspace default: %v", err)
	}

	srv.runTurnWithRequestContext(context.Background(), th, threadRuntime, turnID, turnRuntime, []providers.ChatMessage{userMsg}, nil)

	th.mu.Lock()
	if len(th.Turns) != 1 || th.Turns[0].UsageModel != "fake-model" {
		t.Fatalf("turn should keep original usage model: %+v", th.Turns)
	}
	if th.Model != "fake-model" || th.execRuntime.StreamRunner.Model != "fake-model" || th.pendingRuntimeReset {
		t.Fatalf("workspace default update changed running thread: thread=%q runner=%q pending=%v", th.Model, th.execRuntime.StreamRunner.Model, th.pendingRuntimeReset)
	}
	th.mu.Unlock()
	metas, err := loadMetaMessages(rt.SessionDir, th.ID)
	if err != nil {
		t.Fatalf("load meta messages: %v", err)
	}
	var usageMetas []persistedMessage
	for _, meta := range metas {
		if meta.Content == "token_usage" {
			usageMetas = append(usageMetas, meta)
		}
	}
	if len(usageMetas) != 1 || usageMetas[0].Provider != "fake-provider" || usageMetas[0].Model != "fake-model" {
		t.Fatalf("token usage should use turn snapshot, got %+v", usageMetas)
	}
}

func TestServerConfigAdvancedUpdatePersistsAndRefreshesRuntime(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "model": "fake-model"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	req := `{"id":"1","method":"config/advanced/update","params":{"max_steps":12,"max_context_tokens":256000,"temperature":0.4,"compact_threshold_pct":0.5,"compact_keep_recent_tokens":20000,"disable_auto_compact":true,"provider_context_window":512000,"model_aliases":{"cheap":{"provider":"fake-provider","model":"cheap-model:latest","effort":"low"}},"verification_model":{"provider":"fake-provider","model":"verification-model"}}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/advanced/update: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[ConfigAdvancedUpdateResult](t, responseByID(t, msgs, "1")["result"])
	if result.AdvancedSettings.MaxSteps != 12 ||
		result.AdvancedSettings.MaxContextTokens != 256000 ||
		result.AdvancedSettings.Temperature != 0.4 ||
		result.AdvancedSettings.CompactThresholdPct != 0.5 ||
		result.AdvancedSettings.CompactKeepRecentTokens != 20000 ||
		!result.AdvancedSettings.DisableAutoCompact ||
		result.AdvancedSettings.ProviderContextWindow != 512000 {
		t.Fatalf("unexpected advanced settings result: %+v", result.AdvancedSettings)
	}
	if alias, ok := result.ModelAliases["cheap"]; !ok || alias.Provider != "fake-provider" || alias.Model != "cheap-model:latest" || alias.Effort != "low" {
		t.Fatalf("unexpected model aliases result: %+v", result.ModelAliases)
	}
	if role := modelRoleByName(t, result.ModelRoles, "verification"); role.Inherited || role.Model != "verification-model" {
		t.Fatalf("unexpected verification model: %+v", role)
	}
	if rt.StreamRunner.MaxSteps != 12 ||
		rt.StreamRunner.Temperature != 0.4 ||
		rt.StreamRunner.CompactThresholdPct != 0.5 ||
		rt.StreamRunner.CompactKeepRecentTokens != 20000 ||
		!rt.StreamRunner.DisableAutoCompact ||
		rt.StreamRunner.ContextWindowOverride != 512000 {
		t.Fatalf("runtime advanced settings not updated: %+v", rt.StreamRunner)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	for _, want := range []string{
		`"max_steps": 12`,
		`"max_context_tokens": 256000`,
		`"compact_threshold_pct": 0.5`,
		`"compact_keep_recent_tokens": 20000`,
		`"disable_auto_compact": true`,
		`"context_window": 512000`,
		`"cheap"`,
		`"model": "cheap-model:latest"`,
		`"verification"`,
		`"model": "verification-model"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("config missing %s: %s", want, data)
		}
	}
}

func TestServerConfigGeneralUpdatePersistsAndRefreshesRuntime(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "model": "fake-model"
    }
  },
  "agent": {
    "git_attribution_enabled": true
  },
  "mcp_servers": {
    "docs": {
      "command": "docs-mcp"
    },
    "search": {
      "command": "search-mcp",
      "enabled": false
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	oldPrompt := rt.StreamRunner.SystemPrompt
	th := newThreadState("thread-1", []providers.ChatMessage{{Role: "system", Content: oldPrompt}}, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	th.execRuntime = &runtime.ThreadRuntime{StreamRunner: &agent.StreamRunner{SystemPrompt: oldPrompt}}
	out := &lockedBuffer{}
	srv := New(rt, out)
	srv.threads[th.ID] = th

	req := `{"id":"1","method":"config/general/update","params":{"git_attribution_enabled":false,"mcp_enabled_toggles":{"docs":false,"search":true}}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/general/update: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[ConfigGeneralUpdateResult](t, responseByID(t, msgs, "1")["result"])
	if result.GeneralSettings.GitAttributionEnabled ||
		result.GeneralSettings.MCPServerEnabled["docs"] ||
		!result.GeneralSettings.MCPServerEnabled["search"] {
		t.Fatalf("unexpected general settings result: %+v", result.GeneralSettings)
	}
	cfg, _, err := config.LoadPath(rt.ConfigPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if cfg.Agent.GitAttributionEnabled == nil ||
		*cfg.Agent.GitAttributionEnabled {
		t.Fatalf("config general settings not persisted: %+v", cfg)
	}
	if cfg.MCPServers["docs"].Enabled == nil || *cfg.MCPServers["docs"].Enabled {
		t.Fatalf("docs MCP server should be disabled: %+v", cfg.MCPServers["docs"])
	}
	if cfg.MCPServers["search"].Enabled != nil {
		t.Fatalf("enabled MCP server should omit explicit enabled flag: %+v", cfg.MCPServers["search"])
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	if th.execRuntime != nil {
		t.Fatalf("thread runtime should be reset after general settings change")
	}
}

func TestCurrentAdvancedSettingsUsesEffectiveInputLimit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		budget modelbudget.Budget
	}{
		{
			name: "lower than known context window",
			budget: modelbudget.Budget{
				ContextWindowTokens: 400000,
				InputLimitTokens:    272000,
				OutputReserveTokens: 128000,
				ContextWindowSource: modelbudget.SourceProviderModelLimit,
			},
		},
		{
			name: "model window unknown",
			budget: modelbudget.Budget{
				InputLimitTokens:    272000,
				OutputReserveTokens: 128000,
				ContextWindowSource: modelbudget.SourceUnknown,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			rt.ModelBudget = tc.budget
			srv := New(rt, &lockedBuffer{})

			summary := srv.currentAdvancedSettingsSummary()
			if summary.ContextWindowTokens != 272000 {
				t.Fatalf("ContextWindowTokens = %d, want effective input limit", summary.ContextWindowTokens)
			}
			if summary.ContextWindowSource != string(modelbudget.SourceProviderInputLimit) {
				t.Fatalf("ContextWindowSource = %q, want input limit source", summary.ContextWindowSource)
			}
		})
	}
}

func TestServerConfigModelUpdateRejectsToolPolicyProfile(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("new runtime toolkit: %v", err)
	}
	rt.Toolkit = kit
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "agent": {
    "tool_policy": {
      "tools": {
        "run_shell": "allow"
      }
    }
  },
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "model": "fake-model"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	req := `{"id":"1","method":"config/model/update","params":{"model":"fake-model","tool_policy_profile":"auto"}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/model/update should return a JSON-RPC error response, got transport error: %v", err)
	}
	msg := responseByID(t, parseOutput(t, out.String()), "1")
	if msg["error"] == nil || !strings.Contains(fmt.Sprint(msg["error"]), `unknown field "tool_policy_profile"`) {
		t.Fatalf("expected tool_policy_profile rejection, got %+v", msg)
	}
}

func TestServerConfigModelUpdatePersistsPermissionMode(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("new runtime toolkit: %v", err)
	}
	rt.Toolkit = kit
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "agent": {
    "tool_policy": {
      "tools": {
        "run_shell": "allow"
      }
    }
  },
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "model": "fake-model"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	req := `{"id":"1","method":"config/model/update","params":{"model":"fake-model","permission_mode":"unconfined"}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}

	result := remarshal[ConfigModelUpdateResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if result.Permissions.Mode != config.PermissionModeUnconfined {
		t.Fatalf("unexpected permissions result: %+v", result.Permissions)
	}
	if rt.Permissions.Mode != config.PermissionModeUnconfined {
		t.Fatalf("runtime permissions not updated: %+v", rt.Permissions)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	for _, want := range []string{
		`"permission_mode": "unconfined"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("config missing %s: %s", want, data)
		}
	}
	for _, legacyKey := range []string{`"tool_policy"`, `"permission_profile"`, `"approval_policy"`, `"approvals_reviewer"`, `"permission_rules"`} {
		if strings.Contains(string(data), legacyKey) {
			t.Fatalf("permission mode update should remove legacy key %s: %s", legacyKey, data)
		}
	}
}

func TestServerConfigModelUpdateAppliesPermissionBoundary(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("new runtime toolkit: %v", err)
	}
	rt.Toolkit = kit
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "model": "fake-model"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	req := `{"id":"1","method":"config/model/update","params":{"model":"fake-model","permission_mode":"read_only"}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}

	result := remarshal[ConfigModelUpdateResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if result.Permissions.Mode != config.PermissionModeReadOnly {
		t.Fatalf("unexpected permissions: %+v", result.Permissions)
	}
	_, err = rt.Toolkit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"blocked.txt","content":"nope"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "error_kind=boundary_denied") {
		t.Fatalf("expected read-only runtime boundary, got %v", err)
	}
}

func TestServerConfigModelUpdateReconfiguresEditTools(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	rt := newTestRuntime(t, &fakeClient{})
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("new runtime toolkit: %v", err)
	}
	threadKit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("new thread toolkit: %v", err)
	}
	rt.Toolkit = kit
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "model": "fake-model"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	if _, err := session.CreateWithMetadata(rt.SessionDir, "thread-1", rt.RootDir); err != nil {
		t.Fatalf("create session: %v", err)
	}
	thread := newThreadState("thread-1", nil, rt.ProviderName, rt.Model, rt.RootDir, true, time.Now().UTC())
	thread.History = []providers.ChatMessage{
		{Role: "system", Content: "old fake-model system prompt"},
		{Role: "user", Content: "hello"},
	}
	if err := rewriteChatHistory(rt.SessionDir, thread.ID, thread.History); err != nil {
		t.Fatalf("write thread history: %v", err)
	}
	thread.execRuntime = &runtime.ThreadRuntime{
		StreamRunner: &agent.StreamRunner{Model: "fake-model", APIModel: "fake-model"},
		Toolkit:      threadKit,
	}
	srv.threads[thread.ID] = thread

	if defs := toolDefinitionNames(rt.Toolkit.Definitions()); defs["apply_patch"] || !defs["edit_file"] {
		t.Fatalf("fixture should start in text edit mode: %+v", defs)
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"thread_id":"thread-1","model":"gpt-5.5"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}
	result := remarshal[ConfigModelUpdateResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if result.Model != "fake-model" || rt.Model != "fake-model" {
		t.Fatalf("thread switch changed workspace model: %+v", result)
	}
	if defs := toolDefinitionNames(rt.Toolkit.Definitions()); defs["apply_patch"] || !defs["edit_file"] {
		t.Fatalf("thread switch changed workspace tools: %+v", defs)
	}
	if _, err := srv.ensureThreadRuntime(thread); err != nil {
		t.Fatalf("rebuild target thread runtime: %v", err)
	}
	thread.mu.Lock()
	defer thread.mu.Unlock()
	if thread.ModelProvider != "fake-provider" || thread.Model != "gpt-5.5" {
		t.Fatalf("idle thread model metadata not updated: provider=%q model=%q", thread.ModelProvider, thread.Model)
	}
	if thread.execRuntime.StreamRunner.APIModel != "gpt-5.5" {
		t.Fatalf("idle thread APIModel not updated: %q", thread.execRuntime.StreamRunner.APIModel)
	}
	if len(thread.History) < 2 ||
		thread.History[0].Role != "system" ||
		!strings.Contains(thread.History[0].Content, "[Tool surface: openai_gpt]") ||
		!strings.Contains(thread.History[0].Content, "Use apply_patch for file changes and bash for command execution") ||
		strings.Contains(thread.History[0].Content, "old fake-model system prompt") {
		t.Fatalf("idle thread system prompt not replaced: %+v", thread.History)
	}
	if defs := toolDefinitionNames(thread.execRuntime.Toolkit.Definitions()); !defs["apply_patch"] || defs["edit_file"] || defs["write_file"] {
		t.Fatalf("idle thread toolkit should switch to patch edit mode: %+v", defs)
	}
	if thread.execRuntime.Toolkit.ActiveSurface().ProfileName == "" {
		t.Fatal("idle thread toolkit should install active model surface")
	}
	persisted, err := loadChatMessages(rt.SessionDir, thread.ID)
	if err != nil {
		t.Fatalf("load thread history: %v", err)
	}
	if len(persisted) != 1 || persisted[0].Role != "user" || persisted[0].Content != "hello" {
		t.Fatalf("persisted thread history should keep only persistable messages: %+v", persisted)
	}
}

func TestServerConfigModelUpdateSwitchesProvider(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "model": "fake-model"
    },
    "codex-provider": {
      "type": "openai-codex",
      "base_url": "https://chatgpt.example.test/backend-api/codex",
      "model": "old-codex-model"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	oldClient := rt.StreamRunner.Client
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"provider":"codex-provider","model":"new-codex-model"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}

	result := remarshal[ConfigModelUpdateResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if result.Provider != "codex-provider" || result.Model != "new-codex-model" {
		t.Fatalf("unexpected update result: %+v", result)
	}
	if len(result.Providers) != 2 {
		t.Fatalf("expected two provider summaries, got %+v", result.Providers)
	}
	var codexSummary ProviderSummary
	for _, summary := range result.Providers {
		if summary.Name == "codex-provider" {
			codexSummary = summary
			break
		}
	}
	if !codexSummary.ConnectionLocked {
		t.Fatalf("expected codex provider connection to be locked: %+v", result.Providers)
	}
	if rt.ProviderName != "codex-provider" || rt.Model != "new-codex-model" || rt.StreamRunner.Model != "new-codex-model" {
		t.Fatalf("runtime provider/model not updated: provider=%q runtime=%q runner=%q", rt.ProviderName, rt.Model, rt.StreamRunner.Model)
	}
	if rt.StreamRunner.Client == oldClient {
		t.Fatal("expected stream runner client to be rebuilt")
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"default_provider": "codex-provider"`) ||
		!strings.Contains(string(data), `"model": "new-codex-model"`) {
		t.Fatalf("provider selection was not persisted: %s", data)
	}
}

func TestServerConfigModelUpdateRecomputesToolLoadingForSessionProviderSelection(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://api.openai.com/v1",
      "api_key": "test-key",
      "wire_api": "responses",
      "model": "gpt-5.4"
    },
    "kimi-code": {
      "type": "anthropic",
      "base_url": "https://api.kimi.com/coding",
      "api_key": "test-key",
      "model": "k3"
    }
  }
}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	// Model the live state left by the first-party OpenAI provider before the
	// workspace selection changes to a compatible Anthropic endpoint.
	rt.ToolLoadingMode = config.ToolLoadingNative
	rt.ToolSearchEnabled = true
	rt.NativeDeferredToolDiscovery = true
	rt.Toolkit.SetToolSearchEnabled(true)
	rt.Toolkit.SetNativeDeferredToolDiscovery(true)
	rt.StreamRunner.NativeDeferredToolDiscovery = true

	out := &lockedBuffer{}
	srv := New(rt, out)
	target := newThreadState("target-thread", nil, rt.ProviderName, rt.Model, rt.RootDir, true, time.Now().UTC())
	srv.threads[target.ID] = target
	if _, err := session.CreateWithMetadata(rt.SessionDir, target.ID, rt.RootDir); err != nil {
		t.Fatalf("create target session: %v", err)
	}
	if _, err := session.SetRuntimeSelection(rt.SessionDir, target.ID, session.RuntimeSelection{
		Provider: rt.ProviderName,
		Model:    rt.Model,
	}); err != nil {
		t.Fatalf("pin target selection: %v", err)
	}

	// This is the payload emitted by the desktop model picker for an existing
	// session: the provider/model selection is always scoped by thread_id.
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"thread_id":"target-thread","provider":"kimi-code","model":"k3"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}

	if rt.ProviderName != "fake-provider" || rt.Model != "fake-model" || rt.ToolLoadingMode != config.ToolLoadingNative {
		t.Fatalf("conversation switch changed workspace runtime: provider=%q model=%q", rt.ProviderName, rt.Model)
	}
	exec, err := srv.ensureThreadRuntime(target)
	if err != nil {
		t.Fatal(err)
	}
	if exec.Toolkit.NativeDeferredToolDiscovery() || exec.StreamRunner.NativeDeferredToolDiscovery {
		t.Fatal("conversation retained native discovery from the workspace provider")
	}
	target.mu.Lock()
	if target.ModelProvider != "kimi-code" || target.Model != "k3" {
		t.Fatalf("target session pin not updated: provider=%q model=%q", target.ModelProvider, target.Model)
	}
	target.mu.Unlock()
	metadata, ok, err := session.Find(rt.SessionDir, target.ID)
	if err != nil || !ok {
		t.Fatalf("find target session: ok=%v err=%v", ok, err)
	}
	if metadata.Provider != "kimi-code" || metadata.Model != "k3" {
		t.Fatalf("persisted target selection not updated: %+v", metadata)
	}
}

func TestServerConfigModelUpdatePersistsProviderConnection(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "api_key": "old-key",
      "model": "fake-model"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	oldClient := rt.StreamRunner.Client
	out := &lockedBuffer{}
	srv := New(rt, out)
	idle := newThreadState("idle-thread", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	idle.execRuntime = &runtime.ThreadRuntime{StreamRunner: &agent.StreamRunner{Model: rt.Model}}
	srv.threads[idle.ID] = idle

	req := `{"id":"1","method":"config/model/update","params":{"provider":"fake-provider","model":"new-model","base_url":"https://custom.example.test/v1","api_key":"new-key"}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}

	result := remarshal[ConfigModelUpdateResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if result.Provider != "fake-provider" || result.Model != "new-model" {
		t.Fatalf("unexpected update result: %+v", result)
	}
	if len(result.Providers) != 1 ||
		result.Providers[0].BaseURL != "https://custom.example.test/v1" ||
		!result.Providers[0].APIKeyConfigured {
		t.Fatalf("unexpected provider summaries: %+v", result.Providers)
	}
	if rt.StreamRunner.Client == oldClient {
		t.Fatal("expected stream runner client to be rebuilt")
	}
	idle.mu.Lock()
	if idle.execRuntime != nil {
		idle.mu.Unlock()
		t.Fatal("idle thread runtime should be released after provider connection changes")
	}
	idle.mu.Unlock()
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"base_url": "https://custom.example.test/v1"`) ||
		strings.Contains(string(data), `"api_key": "new-key"`) ||
		!strings.Contains(string(data), `"model": "new-model"`) {
		t.Fatalf("provider connection was not persisted: %s", data)
	}
	store, err := authstorage.ForHome(os.Getenv("HOME"))
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := store.Get("fake-provider")
	if err != nil || credentials.APIKey != "new-key" {
		t.Fatalf("provider key was not saved to auth store: credentials=%+v err=%v", credentials, err)
	}
}

func TestServerConfigModelUpdateCreatesProvider(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "api_key": "old-key",
      "model": "fake-model"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	oldClient := rt.StreamRunner.Client
	out := &lockedBuffer{}
	srv := New(rt, out)

	req := `{"id":"1","method":"config/model/update","params":{"provider":"custom-1","model":"custom-model","base_url":"https://custom.example.test/v1","api_key":"new-key","create_provider":true}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}

	result := remarshal[ConfigModelUpdateResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if result.Provider != "custom-1" || result.Model != "custom-model" {
		t.Fatalf("unexpected update result: %+v", result)
	}
	if len(result.Providers) != 2 {
		t.Fatalf("expected two provider summaries, got %+v", result.Providers)
	}
	var customSummary ProviderSummary
	for _, summary := range result.Providers {
		if summary.Name == "custom-1" {
			customSummary = summary
			break
		}
	}
	if customSummary.BaseURL != "https://custom.example.test/v1" || !customSummary.APIKeyConfigured {
		t.Fatalf("unexpected custom provider summary: %+v", result.Providers)
	}
	if rt.ProviderName != "custom-1" || rt.Model != "custom-model" || rt.StreamRunner.Model != "custom-model" {
		t.Fatalf("runtime provider/model not updated: provider=%q runtime=%q runner=%q", rt.ProviderName, rt.Model, rt.StreamRunner.Model)
	}
	if rt.StreamRunner.Client == oldClient {
		t.Fatal("expected stream runner client to be rebuilt")
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"custom-1"`) ||
		!strings.Contains(string(data), `"base_url": "https://custom.example.test/v1"`) ||
		strings.Contains(string(data), `"api_key": "new-key"`) ||
		!strings.Contains(string(data), `"default_provider": "custom-1"`) {
		t.Fatalf("new provider was not persisted: %s", data)
	}
	store, err := authstorage.ForHome(os.Getenv("HOME"))
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := store.Get("custom-1")
	if err != nil || credentials.APIKey != "new-key" {
		t.Fatalf("provider key was not saved to auth store: credentials=%+v err=%v", credentials, err)
	}
}

func TestServerConfigModelUpdateCreatesAnthropicProviderWithAuthToken(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "api_key": "old-key",
      "model": "fake-model"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	req := `{"id":"1","method":"config/model/update","params":{"provider":"anthropic-gateway","model":"claude-sonnet-4-6[1M]","base_url":"https://anthropic-gateway.example.test/","auth_token":"sk-token","type":"anthropic","create_provider":true}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}

	result := remarshal[ConfigModelUpdateResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if result.Provider != "anthropic-gateway" || result.Model != "claude-sonnet-4-6[1M]" {
		t.Fatalf("unexpected update result: %+v", result)
	}
	var customSummary ProviderSummary
	for _, summary := range result.Providers {
		if summary.Name == "anthropic-gateway" {
			customSummary = summary
			break
		}
	}
	if customSummary.Type != "anthropic" ||
		customSummary.BaseURL != "https://anthropic-gateway.example.test/" ||
		!customSummary.APIKeyConfigured {
		t.Fatalf("unexpected anthropic provider summary: %+v", result.Providers)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"type": "anthropic"`) ||
		!strings.Contains(string(data), `"base_url": "https://anthropic-gateway.example.test/"`) ||
		strings.Contains(string(data), `"auth_token": "sk-token"`) ||
		strings.Contains(string(data), `"api_key": "sk-token"`) {
		t.Fatalf("anthropic provider was not persisted safely: %s", data)
	}
	store, err := authstorage.ForHome(os.Getenv("HOME"))
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := store.Get("anthropic-gateway")
	if err != nil || credentials.AuthToken != "sk-token" {
		t.Fatalf("provider auth token was not saved to auth store: credentials=%+v err=%v", credentials, err)
	}
}

func TestServerConfigModelUpdateCreatesXAISubscriptionWithoutAPIKey(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "api_key": "old-key",
      "model": "fake-model"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	req := `{"id":"1","method":"config/model/update","params":{"provider":"xai-subscription","model":"grok-4.6","type":"xai-subscription","create_provider":true}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}

	result := remarshal[ConfigModelUpdateResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if result.Provider != "xai-subscription" || result.Model != "grok-4.6" {
		t.Fatalf("unexpected update result: %+v", result)
	}
	var summary ProviderSummary
	for _, item := range result.Providers {
		if item.Name == "xai-subscription" {
			summary = item
			break
		}
	}
	if summary.Type != "xai-subscription" || summary.BaseURL != "https://api.x.ai/v1" || summary.APIKeyConfigured || !summary.ConnectionLocked {
		t.Fatalf("unexpected xAI provider summary: %+v", result.Providers)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"type": "xai-subscription"`) ||
		!strings.Contains(string(data), `"https://api.x.ai/v1"`) ||
		!strings.Contains(string(data), `"wire_api": "responses"`) {
		t.Fatalf("xAI subscription provider was not persisted: %s", data)
	}
}

func TestServerXAILoginStartAndPoll(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	pending := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/device/code":
			_, _ = w.Write([]byte(`{"device_code":"dev","user_code":"WXYZ-9999","verification_uri":"https://auth.x.ai/device","expires_in":600,"interval":1}`))
		case "/oauth2/token":
			if pending {
				pending = false
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"live","refresh_token":"rt","expires_in":3600}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("WUU_XAI_DEVICE_CODE_URL", server.URL+"/oauth2/device/code")
	t.Setenv("WUU_XAI_TOKEN_URL", server.URL+"/oauth2/token")
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"start","method":"auth/xai/login/start"}`)); err != nil {
		t.Fatalf("login start: %v", err)
	}
	start := remarshal[AuthXAILoginStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"])
	if start.UserCode != "WXYZ-9999" || start.LoginID == "" {
		t.Fatalf("start = %+v", start)
	}

	out.Reset()
	pollReq, err := json.Marshal(Request{ID: json.RawMessage(`"poll1"`), Method: MethodAuthXAILoginPoll, Params: mustJSON(AuthXAILoginPollParams{LoginID: start.LoginID})})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), pollReq); err != nil {
		t.Fatalf("poll1: %v", err)
	}
	first := remarshal[AuthXAILoginPollResult](t, responseByID(t, parseOutput(t, out.String()), "poll1")["result"])
	if first.Status != "pending" {
		t.Fatalf("first poll = %+v", first)
	}

	out.Reset()
	pollReq, err = json.Marshal(Request{ID: json.RawMessage(`"poll2"`), Method: MethodAuthXAILoginPoll, Params: mustJSON(AuthXAILoginPollParams{LoginID: start.LoginID})})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), pollReq); err != nil {
		t.Fatalf("poll2: %v", err)
	}
	second := remarshal[AuthXAILoginPollResult](t, responseByID(t, parseOutput(t, out.String()), "poll2")["result"])
	if second.Status != "success" {
		t.Fatalf("second poll = %+v", second)
	}
}

func TestServerConfigModelUpdateRejectsOAuthConnectionChanges(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "codex-provider",
  "providers": {
    "codex-provider": {
      "type": "openai-codex",
      "base_url": "https://chatgpt.example.test/backend-api/codex",
      "model": "old-codex-model"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	req := `{"id":"1","method":"config/model/update","params":{"provider":"codex-provider","model":"new-codex-model","base_url":"https://custom.example.test/v1","api_key":"new-key"}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}

	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "OpenAI OAuth") {
		t.Fatalf("expected OAuth connection error, got %+v", response)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), `"api_key": "new-key"`) ||
		strings.Contains(string(data), "https://custom.example.test/v1") ||
		strings.Contains(string(data), `"model": "new-codex-model"`) {
		t.Fatalf("OAuth provider connection should not be persisted: %s", data)
	}
}

func TestServerConfigModelUpdatePersistsEffort(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Effort = "medium"
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "agent": {
    "effort": "medium"
  },
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "model": "fake-model"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"model":"fake-model","effort":"xhigh"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}

	result := remarshal[ConfigModelUpdateResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if result.Model != "fake-model" || result.Effort != "xhigh" {
		t.Fatalf("unexpected update result: %+v", result)
	}
	if rt.StreamRunner.Effort != "xhigh" {
		t.Fatalf("runtime effort not updated: %q", rt.StreamRunner.Effort)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"effort": "xhigh"`) {
		t.Fatalf("effort was not persisted: %s", data)
	}
}

func TestServerConfigModelUpdatePersistsVariantOptions(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.ProviderName = "xiaomi"
	rt.Model = "mimo-v2.5-pro"
	rt.StreamRunner.Model = "mimo-v2.5-pro"
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "agent": {
    "effort": "low"
  },
  "default_provider": "xiaomi",
  "providers": {
    "xiaomi": {
      "type": "openai-compatible",
      "base_url": "https://token-plan-cn.xiaomimimo.com/v1",
      "model": "mimo-v2.5-pro"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"provider":"xiaomi","model":"mimo-v2.5-pro","variant":"high"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}

	result := remarshal[ConfigModelUpdateResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if result.Model != "mimo-v2.5-pro" || result.Variant != "high" || result.Effort != "high" {
		t.Fatalf("unexpected update result: %+v", result)
	}
	if rt.StreamRunner.Variant != "high" {
		t.Fatalf("runtime variant not updated: %q", rt.StreamRunner.Variant)
	}
	if rt.StreamRunner.Effort != "" {
		t.Fatalf("legacy effort should be empty when variant options are active, got %q", rt.StreamRunner.Effort)
	}
	if got := rt.StreamRunner.ProviderOptions["reasoningEffort"]; got != "high" {
		t.Fatalf("runtime provider options not updated: %#v", rt.StreamRunner.ProviderOptions)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"variant": "high"`) {
		t.Fatalf("variant was not persisted: %s", text)
	}
	if strings.Contains(text, `"effort"`) {
		t.Fatalf("legacy effort should be removed after variant migration: %s", text)
	}
}

func TestServerConfigModelUpdateUsesCatalogFastModelRuntime(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.ProviderName = "openai"
	rt.Model = "gpt-5.5"
	rt.StreamRunner.Model = "gpt-5.5"
	t.Setenv("OPENAI_API_KEY", "abc")
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "openai",
  "providers": {
    "openai": {
      "type": "openai",
      "base_url": "https://api.openai.com/v1",
      "model": "gpt-5.5"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/model/update","params":{"provider":"openai","model":"gpt-5.5-fast"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}

	result := remarshal[ConfigModelUpdateResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if result.Model != "gpt-5.5-fast" {
		t.Fatalf("unexpected update result: %+v", result)
	}
	if rt.StreamRunner.Model != "gpt-5.5-fast" || rt.StreamRunner.APIModel != "gpt-5.5" {
		t.Fatalf("runtime model mismatch: model=%q api=%q", rt.StreamRunner.Model, rt.StreamRunner.APIModel)
	}
	if got := rt.StreamRunner.ProviderOptions["serviceTier"]; got != "priority" {
		t.Fatalf("runtime provider options not updated: %#v", rt.StreamRunner.ProviderOptions)
	}
}

func TestServerConfigCodexModels(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.ProviderName = "openai-codex"
	rt.Model = "gpt-5.5"
	rt.StreamRunner.Model = "gpt-5.5"
	rt.StreamRunner.Effort = "xhigh"

	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	defer func() {
		select {
		case <-releaseRequest:
		default:
			close(releaseRequest)
		}
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %q, want /models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		close(requestStarted)
		<-releaseRequest
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "models": [
		    {"slug":"gpt-hidden","visibility":"hide","supported_in_api":true},
		    {"slug":"spark","display_name":"Spark","supported_in_api":false},
		    {"slug":"gpt-5.4","display_name":"GPT-5.4","priority":20,"supported_in_api":true},
		    {"slug":"gpt-5.5","display_name":"GPT-5.5","priority":9,"default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"low"},{"effort":"xhigh"},{"effort":"ultra"}],"supported_in_api":true}
		  ]
		}`))
	}))
	defer server.Close()

	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "agent": {
    "effort": "xhigh"
  },
  "default_provider": "openai-codex",
  "providers": {
    "openai-codex": {
      "type": "openai-codex",
      "base_url": "`+server.URL+`",
      "api_key": "test-token",
      "model": "gpt-5.5"
    }
  }
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"config/codex/models"}`)); err != nil {
		t.Fatalf("config/codex/models: %v", err)
	}
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Codex model request did not start")
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"update-while-loading","method":"config/model/update","params":{"provider":"openai-codex","model":"gpt-5.5","variant":"xhigh"}}`)); err != nil {
		t.Fatalf("config/model/update while models load: %v", err)
	}
	if responseByID(t, parseOutput(t, out.String()), "update-while-loading")["result"] == nil {
		t.Fatal("model update was blocked behind Codex model discovery")
	}
	close(releaseRequest)
	srv.backgroundWG.Wait()

	result := remarshal[ConfigCodexModelsResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	var discovered *ProviderModelSummary
	for _, provider := range result.Providers {
		if provider.Name != "openai-codex" {
			continue
		}
		for i := range provider.Models {
			if provider.Models[i].ID == "spark" {
				discovered = &provider.Models[i]
			}
		}
	}
	if discovered == nil {
		t.Fatal("live model missing from shared settings/composer inventory")
	}
	if result.Provider != "openai-codex" || result.Model != "gpt-5.5" || result.Effort != "xhigh" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Models) != 5 || result.Models[0].Slug != "gpt-5.5" || result.Models[1].Slug != "gpt-5.5-fast" || result.Models[2].Slug != "gpt-5.4" || result.Models[3].Slug != "gpt-5.4-fast" || result.Models[4].Slug != "spark" {
		t.Fatalf("unexpected models: %+v", result.Models)
	}
	if got := result.Models[0].SupportedReasoning; len(got) != 2 || got[0] != "low" || got[1] != "xhigh" {
		t.Fatalf("unexpected reasoning levels: %+v", got)
	}
	if got := result.Models[1].SupportedReasoning; len(got) != 2 || got[0] != "low" || got[1] != "xhigh" {
		t.Fatalf("unexpected fast alias reasoning levels: %+v", got)
	}

	if err := srv.handleLine(context.Background(), []byte(`{"id":"2","method":"config/model/update","params":{"provider":"openai-codex","model":"gpt-5.5","variant":"xhigh"}}`)); err != nil {
		t.Fatalf("config/model/update: %v", err)
	}
	update := remarshal[ConfigModelUpdateResult](t, responseByID(t, parseOutput(t, out.String()), "2")["result"])
	if update.Provider != "openai-codex" || update.Model != "gpt-5.5" || update.Variant != "xhigh" {
		t.Fatalf("unexpected update: %+v", update)
	}
	if got := rt.StreamRunner.ProviderOptions["reasoningEffort"]; got != "xhigh" {
		t.Fatalf("runtime provider options = %#v", rt.StreamRunner.ProviderOptions)
	}
	if rt.StreamRunner.ContextWindowOverride != 400000 ||
		rt.StreamRunner.MaxInputTokens != 272000 ||
		rt.StreamRunner.OutputReserveTokens != 128000 {
		t.Fatalf("runtime budget not updated from live Codex model: context=%d input=%d output=%d",
			rt.StreamRunner.ContextWindowOverride,
			rt.StreamRunner.MaxInputTokens,
			rt.StreamRunner.OutputReserveTokens)
	}

	if err := srv.handleLine(context.Background(), []byte(`{"id":"3","method":"config/model/update","params":{"provider":"openai-codex","model":"gpt-5.5-fast","variant":"xhigh"}}`)); err != nil {
		t.Fatalf("config/model/update fast: %v", err)
	}
	fastUpdate := remarshal[ConfigModelUpdateResult](t, responseByID(t, parseOutput(t, out.String()), "3")["result"])
	if fastUpdate.Provider != "openai-codex" || fastUpdate.Model != "gpt-5.5-fast" || fastUpdate.Variant != "xhigh" {
		t.Fatalf("unexpected fast update: %+v", fastUpdate)
	}
	if rt.StreamRunner.Model != "gpt-5.5-fast" || rt.StreamRunner.APIModel != "gpt-5.5" {
		t.Fatalf("fast runtime model mismatch: model=%q api=%q", rt.StreamRunner.Model, rt.StreamRunner.APIModel)
	}
	if got := rt.StreamRunner.ProviderOptions["serviceTier"]; got != "priority" {
		t.Fatalf("fast runtime service tier = %#v in %#v", got, rt.StreamRunner.ProviderOptions)
	}
	if got := rt.StreamRunner.ProviderOptions["reasoningEffort"]; got != "xhigh" {
		t.Fatalf("fast runtime reasoning effort = %#v in %#v", got, rt.StreamRunner.ProviderOptions)
	}
}

func TestCachedCodexModelsReplaceCatalogOnlyReasoningLevels(t *testing.T) {
	srv := &Server{}
	srv.cacheCodexModels("openai-codex", []codex.ModelInfo{{
		Slug:               "gpt-5.6-sol",
		SupportedReasoning: []string{"low", "medium", "high", "xhigh", "max", "ultra"},
	}})
	provider := config.ProviderConfig{
		Type: "openai-codex",
		Models: map[string]config.ProviderModelConfig{
			"gpt-5.6-sol": {
				SupportedEfforts: []string{"none", "low", "medium", "high", "xhigh", "max"},
				Variants: map[string]map[string]any{
					"none": {"reasoningEffort": "none"},
				},
			},
		},
	}

	merged := srv.withCachedCodexModels("openai-codex", provider)
	model := merged.Models["gpt-5.6-sol"]
	if got := strings.Join(model.SupportedEfforts, ","); got != "low,medium,high,xhigh,max" {
		t.Fatalf("supported efforts = %q", got)
	}
	if _, ok := model.Variants["none"]; ok {
		t.Fatalf("catalog-only none variant survived live model merge: %#v", model.Variants)
	}
	if _, ok := model.Variants["ultra"]; ok {
		t.Fatalf("client-only ultra mode leaked into reasoning variants: %#v", model.Variants)
	}
	if model.ContextWindow != 1_050_000 || model.Limit == nil || model.Limit.Context != 1_050_000 || model.Limit.Input != 272_000 || model.Limit.Output != 128_000 {
		t.Fatalf("live Codex model did not receive subscription limits: %+v", model)
	}
	summaries := providerSummariesFromConfig(config.Config{
		DefaultProvider: "openai-codex",
		Providers: map[string]config.ProviderConfig{
			"openai-codex": merged,
		},
	}, t.TempDir())
	summary := providerModelByID(t, summaries[0], "gpt-5.6-sol")
	if summary.Capabilities.ContextWindow != 1_050_000 || summary.Capabilities.InputLimit != 272_000 {
		t.Fatalf("Codex model summary exposed API limits instead of subscription limits: %+v", summary.Capabilities)
	}
	merged.ContextWindow = 922_000
	overriddenSummaries := providerSummariesFromConfig(config.Config{
		DefaultProvider: "openai-codex",
		Providers: map[string]config.ProviderConfig{
			"openai-codex": merged,
		},
	}, t.TempDir())
	overridden := providerModelByID(t, overriddenSummaries[0], "gpt-5.6-sol")
	if overridden.Capabilities.ContextWindow != 922_000 || overridden.Capabilities.InputLimit != 0 {
		t.Fatalf("explicit Codex context override was clamped in model summary: %+v", overridden.Capabilities)
	}
}

func TestCachedCodexModelsKeepGPT5SubscriptionWindow(t *testing.T) {
	srv := &Server{}
	srv.cacheCodexModels("openai-codex", []codex.ModelInfo{{
		Slug:               "gpt-5.5",
		DisplayName:        "GPT-5.5",
		SupportedReasoning: []string{"low", "medium", "high", "xhigh"},
	}})
	merged := srv.withCachedCodexModels("openai-codex", config.ProviderConfig{Type: "openai-codex"})
	model := merged.Models["gpt-5.5"]
	if model.ContextWindow != 400_000 || model.Limit == nil || model.Limit.Context != 400_000 || model.Limit.Input != 272_000 || model.Limit.Output != 128_000 {
		t.Fatalf("live GPT-5.5 model should keep the GPT-5 subscription window: %+v", model)
	}
}

func TestCachedCodexModelsKeepAstraCatalogWindow(t *testing.T) {
	srv := &Server{}
	srv.cacheCodexModels("openai-codex", []codex.ModelInfo{{
		Slug:               "gpt-6-astra",
		DisplayName:        "GPT-6 Astra",
		SupportedReasoning: []string{"low", "medium", "high", "xhigh", "max"},
	}})
	merged := srv.withCachedCodexModels("openai-codex", config.ProviderConfig{Type: "openai-codex"})
	model := merged.Models["gpt-6-astra"]
	if model.ContextWindow != 1_050_000 || model.Limit == nil || model.Limit.Context != 1_050_000 || model.Limit.Input != 272_000 || model.Limit.Output != 128_000 {
		t.Fatalf("live Astra model did not keep the GPT-6 subscription window: %+v", model)
	}
	fast := merged.Models["gpt-6-astra-fast"]
	if fast.ID != "gpt-6-astra" || fast.Name != "GPT-6 Astra Fast" || fast.ContextWindow != 1_050_000 {
		t.Fatalf("live Astra Fast alias missing GPT-6 subscription metadata: %+v", fast)
	}
}

func TestServerConfigProviderRemoveInactive(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	store, err := authstorage.ForHome(os.Getenv("HOME"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("drop", authstorage.Credentials{Type: "api_key", APIKey: "drop-key"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "keep",
  "providers": {
    "keep": {
      "type": "openai-compatible",
      "base_url": "https://keep.example.test/v1",
      "api_key": "keep-key",
      "model": "keep-model"
    },
    "drop": {
      "type": "openai-compatible",
      "base_url": "https://drop.example.test/v1",
      "api_key": "drop-key",
      "model": "drop-model"
    }
  }
}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	req := `{"id":"1","method":"config/provider/remove","params":{"provider":"drop"}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/provider/remove: %v", err)
	}

	if got := responseByID(t, parseOutput(t, out.String()), "1")["error"]; got != nil {
		t.Fatalf("unexpected error response: %+v", got)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), `"drop"`) {
		t.Fatalf("drop provider was not removed: %s", data)
	}
	if !strings.Contains(string(data), `"keep"`) {
		t.Fatalf("keep provider was unexpectedly removed: %s", data)
	}
	if !strings.Contains(string(data), `"default_provider": "keep"`) {
		t.Fatalf("default provider changed unexpectedly: %s", data)
	}
	if rt.ProviderName != "fake-provider" || rt.Model != "fake-model" {
		t.Fatalf("runtime selection changed for inactive removal: provider=%q model=%q", rt.ProviderName, rt.Model)
	}
	if _, err := store.Get("drop"); err == nil {
		t.Fatal("removed provider credential still exists")
	}
}

func TestServerConfigProviderRemoveActiveSwapsDefault(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "drop",
  "providers": {
    "drop": {
      "type": "openai-compatible",
      "base_url": "https://drop.example.test/v1",
      "api_key": "drop-key",
      "model": "drop-model"
    },
    "keep": {
      "type": "openai-compatible",
      "base_url": "https://keep.example.test/v1",
      "api_key": "keep-key",
      "model": "keep-model"
    }
  }
}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rt.ProviderName = "drop"
	rt.Model = "drop-model"
	out := &lockedBuffer{}
	srv := New(rt, out)

	req := `{"id":"1","method":"config/provider/remove","params":{"provider":"drop","fallback_provider":"keep","fallback_model":"keep-model"}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/provider/remove: %v", err)
	}

	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] != nil {
		t.Fatalf("unexpected error response: %+v", response["error"])
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), `"drop"`) {
		t.Fatalf("drop provider was not removed: %s", data)
	}
	if !strings.Contains(string(data), `"default_provider": "keep"`) {
		t.Fatalf("default provider not swapped to keep: %s", data)
	}
	if rt.ProviderName != "keep" || rt.Model != "keep-model" {
		t.Fatalf("runtime selection not updated: provider=%q model=%q", rt.ProviderName, rt.Model)
	}
}

func TestServerConfigProviderRemoveRejectsProviderUsedByRunningTurn(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "drop",
  "providers": {
    "drop": {
      "type": "openai-compatible",
      "base_url": "https://drop.example.test/v1",
      "api_key": "drop-key",
      "model": "drop-model"
    },
    "keep": {
      "type": "openai-compatible",
      "base_url": "https://keep.example.test/v1",
      "api_key": "keep-key",
      "model": "keep-model"
    }
  }
}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rt.ProviderName = "drop"
	rt.Model = "drop-model"
	rt.StreamRunner.Model = "drop-model"
	out := &lockedBuffer{}
	srv := New(rt, out)
	now := time.Now().UTC()
	running := newThreadState("running-thread", nil, rt.ProviderName, rt.Model, rt.RootDir, true, now)
	running.execRuntime = &runtime.ThreadRuntime{
		StreamRunner: &agent.StreamRunner{Model: "drop-model", APIModel: "drop-model"},
	}
	running.startTurnLocked("running-turn", providers.ChatMessage{Role: "user", Content: "keep running"}, now)
	srv.threads[running.ID] = running

	req := `{"id":"1","method":"config/provider/remove","params":{"provider":"drop","fallback_provider":"keep","fallback_model":"keep-model"}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/provider/remove: %v", err)
	}

	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] == nil {
		t.Fatal("expected provider-in-use removal to fail, got success")
	}
	if !strings.Contains(fmt.Sprint(response["error"]), "running turn") {
		t.Fatalf("expected running-turn provider error, got %+v", response["error"])
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"drop"`) || !strings.Contains(string(data), `"default_provider": "drop"`) {
		t.Fatalf("provider in use was removed despite rejection: %s", data)
	}
	running.mu.Lock()
	defer running.mu.Unlock()
	if running.execRuntime.StreamRunner.Model != "drop-model" || running.execRuntime.StreamRunner.APIModel != "drop-model" {
		t.Fatalf("running turn runtime changed after rejected removal: model=%q api=%q",
			running.execRuntime.StreamRunner.Model, running.execRuntime.StreamRunner.APIModel)
	}
}

// Removing a provider must not be blocked by idle sessions that still pin it:
// those pins heal lazily to the workspace defaults on their next turn
// (TestEnsureThreadRuntimeHealsRemovedProviderPin), so an archived thread in a
// drawer somewhere can never hold the provider list hostage.
func TestServerConfigProviderRemoveAllowsProviderPinnedByIdleArchivedSession(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "keep",
  "providers": {
    "keep": {
      "type": "openai-compatible",
      "base_url": "https://keep.example.test/v1",
      "api_key": "keep-key",
      "model": "keep-model"
    },
    "drop": {
      "type": "openai-compatible",
      "base_url": "https://drop.example.test/v1",
      "api_key": "drop-key",
      "model": "drop-model"
    }
  }
}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := session.CreateWithMetadata(rt.SessionDir, "pinned-thread", rt.RootDir); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := session.SetRuntimeSelection(rt.SessionDir, "pinned-thread", session.RuntimeSelection{
		Provider: "drop",
		Model:    "drop-model",
	}); err != nil {
		t.Fatalf("pin session to provider: %v", err)
	}
	if _, err := session.UpdateArchived(rt.SessionDir, "pinned-thread", true); err != nil {
		t.Fatalf("archive session: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	req := `{"id":"1","method":"config/provider/remove","params":{"provider":"drop"}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/provider/remove: %v", err)
	}

	if got := responseByID(t, parseOutput(t, out.String()), "1")["error"]; got != nil {
		t.Fatalf("idle pinned session must not block removal: %+v", got)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), `"drop"`) {
		t.Fatalf("pinned-but-idle provider was not removed: %s", data)
	}
}

func TestServerConfigProviderRemoveAllowsUnusedProviderWithRunningThread(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "keep",
  "providers": {
    "keep": {
      "type": "openai-compatible",
      "base_url": "https://keep.example.test/v1",
      "api_key": "keep-key",
      "model": "keep-model"
    },
    "drop": {
      "type": "openai-compatible",
      "base_url": "https://drop.example.test/v1",
      "api_key": "drop-key",
      "model": "drop-model"
    }
  }
}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rt.ProviderName = "keep"
	rt.Model = "keep-model"
	rt.StreamRunner.Model = "keep-model"
	out := &lockedBuffer{}
	srv := New(rt, out)
	now := time.Now().UTC()
	running := newThreadState("running-thread", nil, rt.ProviderName, rt.Model, rt.RootDir, true, now)
	running.execRuntime = &runtime.ThreadRuntime{
		StreamRunner: &agent.StreamRunner{Model: "keep-model", APIModel: "keep-model"},
	}
	running.startTurnLocked("running-turn", providers.ChatMessage{Role: "user", Content: "keep running"}, now)
	srv.threads[running.ID] = running

	req := `{"id":"1","method":"config/provider/remove","params":{"provider":"drop"}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/provider/remove: %v", err)
	}

	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] != nil {
		t.Fatalf("unexpected error response: %+v", response["error"])
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), `"drop"`) {
		t.Fatalf("unused provider was not removed: %s", data)
	}
}

func TestServerConfigProviderRemoveRejectsOAuth(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "real",
  "providers": {
    "real": {
      "type": "openai-compatible",
      "base_url": "https://real.example.test/v1",
      "model": "real-model"
    },
    "codex": {
      "type": "openai-codex",
      "base_url": "https://chatgpt.example.test/backend-api/codex",
      "model": "codex-model"
    }
  }
}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	req := `{"id":"1","method":"config/provider/remove","params":{"provider":"codex"}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/provider/remove: %v", err)
	}

	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] == nil {
		t.Fatal("expected OAuth removal to fail, got success")
	}
	if !strings.Contains(fmt.Sprint(response["error"]), "OAuth") {
		t.Fatalf("expected OAuth error, got %+v", response["error"])
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"codex"`) {
		t.Fatalf("OAuth provider was removed despite rejection: %s", data)
	}
}

func TestServerConfigProviderRemoveRejectsLastProvider(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "only",
  "providers": {
    "only": {
      "type": "openai-compatible",
      "base_url": "https://only.example.test/v1",
      "model": "only-model"
    }
  }
}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	req := `{"id":"1","method":"config/provider/remove","params":{"provider":"only"}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("config/provider/remove: %v", err)
	}

	response := responseByID(t, parseOutput(t, out.String()), "1")
	if response["error"] == nil {
		t.Fatal("expected last-provider removal to fail, got success")
	}
	if !strings.Contains(fmt.Sprint(response["error"]), "last") {
		t.Fatalf("expected last-provider error, got %+v", response["error"])
	}
}

func TestServerSkillList(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Skills = []skills.Skill{{
		Name:          "slides",
		Description:   "Create slide decks",
		WhenToUse:     "When the user asks for a presentation",
		Source:        "bundled",
		ArgumentHint:  "topic",
		UserInvocable: true,
		AllowedTools:  []string{"read_file"},
		Paths:         []string{"**/*.pptx"},
	}}
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"skill/list"}`)); err != nil {
		t.Fatalf("skill/list: %v", err)
	}

	result := remarshal[SkillListResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if len(result.Skills) != 1 {
		t.Fatalf("expected one skill, got %+v", result)
	}
	got := result.Skills[0]
	if got.Name != "slides" || got.Description != "Create slide decks" || got.Source != "bundled" || !got.UserInvocable {
		t.Fatalf("unexpected skill summary: %+v", got)
	}
	if len(got.AllowedTools) != 1 || got.AllowedTools[0] != "read_file" || len(got.Paths) != 1 || got.Paths[0] != "**/*.pptx" {
		t.Fatalf("skill metadata missing: %+v", got)
	}
}

func TestServerProcessListAndStop(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	manager := attachTestProcessManager(t, rt)
	started, err := manager.Start(context.Background(), process.StartOptions{
		Command:   "sleep 30",
		OwnerKind: process.OwnerMainAgent,
		OwnerID:   "test",
		Lifecycle: process.LifecycleManaged,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _, _ = manager.Stop(started.ID) }()

	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"process/list","params":{"thread_id":"test"}}`)); err != nil {
		t.Fatalf("process/list: %v", err)
	}
	listed := remarshal[ProcessListResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if len(listed.Processes) != 1 || listed.Processes[0].ID != started.ID || listed.Processes[0].Status != string(process.StatusRunning) {
		t.Fatalf("unexpected process list: %+v", listed)
	}
	rawListed := responseByID(t, parseOutput(t, out.String()), "1")
	rawJSON, err := json.Marshal(rawListed["result"])
	if err != nil {
		t.Fatalf("marshal process/list result: %v", err)
	}
	if strings.Contains(string(rawJSON), "log_path") || strings.Contains(string(rawJSON), "pgid") {
		t.Fatalf("process/list leaked internal process fields: %s", string(rawJSON))
	}

	stopPayload := fmt.Sprintf(`{"id":"2","method":"process/stop","params":{"thread_id":"test","process_id":%q}}`, started.ID)
	if err := srv.handleLine(context.Background(), []byte(stopPayload)); err != nil {
		t.Fatalf("process/stop: %v", err)
	}
	stopped := remarshal[ProcessStopResult](t, responseByID(t, parseOutput(t, out.String()), "2")["result"])
	if stopped.Process.ID != started.ID || stopped.Process.Status != string(process.StatusStopped) {
		t.Fatalf("unexpected stopped process: %+v", stopped)
	}
}

func TestServerProcessTTYReadWriteResizeAndOwnership(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("managed tty is not supported on windows")
	}
	rt := newTestRuntime(t, &fakeClient{})
	manager := attachTestProcessManager(t, rt)
	started, err := manager.Start(context.Background(), process.StartOptions{
		Command:   `printf 'ready\n'; read line; printf 'got:%s\n' "$line"; sleep 30`,
		OwnerKind: process.OwnerMainAgent,
		OwnerID:   "thread-live",
		Lifecycle: process.LifecycleManaged,
		TTY:       true,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _, _ = manager.Stop(started.ID) }()

	out := &lockedBuffer{}
	srv := New(rt, out)
	readPayload := fmt.Sprintf(`{"id":"1","method":"process/read","params":{"thread_id":"thread-live","process_id":%q,"offset_bytes":0,"wait_ms":2000}}`, started.ID)
	if err := srv.handleLine(context.Background(), []byte(readPayload)); err != nil {
		t.Fatalf("process/read: %v", err)
	}
	first := remarshal[ProcessReadResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if !strings.Contains(first.Output, "ready") || !first.Process.InputAvailable {
		t.Fatalf("unexpected first read: %+v", first)
	}

	wrongThreadPayload := fmt.Sprintf(`{"id":"2","method":"process/read","params":{"thread_id":"another-thread","process_id":%q,"offset_bytes":0}}`, started.ID)
	if err := srv.handleLine(context.Background(), []byte(wrongThreadPayload)); err != nil {
		t.Fatalf("cross-thread process/read: %v", err)
	}
	if got := responseByID(t, parseOutput(t, out.String()), "2")["error"]; got == nil || !strings.Contains(fmt.Sprint(got), "does not belong") {
		t.Fatalf("cross-thread read should fail: %+v", got)
	}

	writePayload := fmt.Sprintf(`{"id":"3","method":"process/write","params":{"thread_id":"thread-live","process_id":%q,"input":"hello\n"}}`, started.ID)
	if err := srv.handleLine(context.Background(), []byte(writePayload)); err != nil {
		t.Fatalf("process/write: %v", err)
	}
	resizePayload := fmt.Sprintf(`{"id":"4","method":"process/resize","params":{"thread_id":"thread-live","process_id":%q,"cols":100,"rows":30}}`, started.ID)
	if err := srv.handleLine(context.Background(), []byte(resizePayload)); err != nil {
		t.Fatalf("process/resize: %v", err)
	}

	offset := first.EndOffset
	var incremental strings.Builder
	deadline := time.Now().Add(3 * time.Second)
	for attempt := 0; !strings.Contains(incremental.String(), "got:hello") && time.Now().Before(deadline); attempt++ {
		requestID := fmt.Sprintf("read-%d", attempt)
		secondReadPayload := fmt.Sprintf(`{"id":%q,"method":"process/read","params":{"thread_id":"thread-live","process_id":%q,"offset_bytes":%d,"wait_ms":500}}`, requestID, started.ID, offset)
		if err := srv.handleLine(context.Background(), []byte(secondReadPayload)); err != nil {
			t.Fatalf("incremental process/read: %v", err)
		}
		second := remarshal[ProcessReadResult](t, responseByID(t, parseOutput(t, out.String()), requestID)["result"])
		if second.StartOffset != offset {
			t.Fatalf("incremental read started at %d, want %d: %+v", second.StartOffset, offset, second)
		}
		incremental.WriteString(second.Output)
		offset = second.EndOffset
	}
	if got := incremental.String(); !strings.Contains(got, "got:hello") {
		t.Fatalf("incremental reads never observed command response: %q", got)
	}
}

func TestServerProcessTTYUsesThreadLocalWorktreeManager(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("managed tty is not supported on windows")
	}
	rt := newTestRuntime(t, &fakeClient{})
	globalManager := attachTestProcessManager(t, rt)
	threadManager, err := process.NewManager(t.TempDir(), filepath.Join(rt.RootDir, "runtime"))
	if err != nil {
		t.Fatalf("thread process.NewManager: %v", err)
	}
	started, err := threadManager.Start(context.Background(), process.StartOptions{
		Command:   `printf 'ready\n'; read line; printf 'got:%s\n' "$line"; sleep 30`,
		OwnerKind: process.OwnerMainAgent,
		OwnerID:   "thread-worktree",
		Lifecycle: process.LifecycleManaged,
		TTY:       true,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _, _ = threadManager.Stop(started.ID) }()
	if globalManager.InputAvailable(started.ID) {
		t.Fatal("global manager unexpectedly owns the worktree tty handle")
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	th := newThreadState("thread-worktree", nil, rt.ProviderName, rt.Model, t.TempDir(), false, time.Now().UTC())
	threadRuntime := &runtime.ThreadRuntime{ProcessManager: threadManager}
	th.execRuntime = threadRuntime
	srv.mu.Lock()
	srv.threads[th.ID] = th
	srv.mu.Unlock()

	srv.resetThreadRuntimesForGeneralSettings("")
	th.mu.Lock()
	if th.execRuntime != threadRuntime || !th.pendingRuntimeReset {
		t.Fatalf("live worktree tty runtime was not deferred: runtime=%p pending=%t", th.execRuntime, th.pendingRuntimeReset)
	}
	th.mu.Unlock()
	ensured, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatalf("ensureThreadRuntime with live tty: %v", err)
	}
	if ensured != threadRuntime {
		t.Fatal("pending runtime reset replaced the live worktree tty manager")
	}
	if _, release, err := srv.beginThreadRuntimeSelectionMutation(th.ID); err == nil {
		release()
		t.Fatal("runtime selection mutation should be rejected while a managed tty is live")
	}

	listPayload := `{"id":"1","method":"process/list","params":{"thread_id":"thread-worktree"}}`
	if err := srv.handleLine(context.Background(), []byte(listPayload)); err != nil {
		t.Fatalf("process/list: %v", err)
	}
	listed := remarshal[ProcessListResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if len(listed.Processes) != 1 || !listed.Processes[0].InputAvailable {
		t.Fatalf("worktree tty should expose input through its thread manager: %+v", listed)
	}

	writePayload := fmt.Sprintf(`{"id":"2","method":"process/write","params":{"thread_id":"thread-worktree","process_id":%q,"input":"hello\n"}}`, started.ID)
	if err := srv.handleLine(context.Background(), []byte(writePayload)); err != nil {
		t.Fatalf("process/write: %v", err)
	}
	if got := responseByID(t, parseOutput(t, out.String()), "2")["error"]; got != nil {
		t.Fatalf("worktree process/write failed: %+v", got)
	}

	resizePayload := fmt.Sprintf(`{"id":"3","method":"process/resize","params":{"thread_id":"thread-worktree","process_id":%q,"cols":100,"rows":30}}`, started.ID)
	if err := srv.handleLine(context.Background(), []byte(resizePayload)); err != nil {
		t.Fatalf("process/resize: %v", err)
	}
	if got := responseByID(t, parseOutput(t, out.String()), "3")["error"]; got != nil {
		t.Fatalf("worktree process/resize failed: %+v", got)
	}
}

func TestServerProcessEndpointsValidateErrors(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	requests := []string{
		`{"id":"1","method":"process/list","params":{"thread_id":"test"}}`,
		`{"id":"2","method":"process/stop","params":{"thread_id":"test","process_id":"proc-missing"}}`,
	}
	for _, raw := range requests {
		if err := srv.handleLine(context.Background(), []byte(raw)); err != nil {
			t.Fatalf("handleLine %s: %v", raw, err)
		}
	}
	msgs := parseOutput(t, out.String())
	if got := responseByID(t, msgs, "1")["error"]; got == nil || !strings.Contains(fmt.Sprint(got), "process manager is not available") {
		t.Fatalf("process/list missing-manager error mismatch: %+v", got)
	}
	if got := responseByID(t, msgs, "2")["error"]; got == nil || !strings.Contains(fmt.Sprint(got), "process manager is not available") {
		t.Fatalf("process/stop missing-manager error mismatch: %+v", got)
	}
}

func TestServerProcessStopValidatesProcessID(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	attachTestProcessManager(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"process/stop","params":{"thread_id":"test","process_id":"   "}}`)); err != nil {
		t.Fatalf("process/stop blank id: %v", err)
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"2","method":"process/stop","params":{"thread_id":"test","process_id":"proc-does-not-exist"}}`)); err != nil {
		t.Fatalf("process/stop missing id: %v", err)
	}
	msgs := parseOutput(t, out.String())
	if got := responseByID(t, msgs, "1")["error"]; got == nil || !strings.Contains(fmt.Sprint(got), "process_id is required") {
		t.Fatalf("blank process_id error mismatch: %+v", got)
	}
	if got := responseByID(t, msgs, "2")["error"]; got == nil || !strings.Contains(fmt.Sprint(got), "proc-does-not-exist") {
		t.Fatalf("missing process error mismatch: %+v", got)
	}
}

func TestServerProcessListRejectsUnknownParams(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	attachTestProcessManager(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"process/list","params":{"extra":true}}`)); err != nil {
		t.Fatalf("process/list unknown params: %v", err)
	}
	got := responseByID(t, parseOutput(t, out.String()), "1")["error"]
	if got == nil || !strings.Contains(fmt.Sprint(got), "unknown field") {
		t.Fatalf("unknown params error mismatch: %+v", got)
	}
}

func TestServerProcessListRedactsSensitiveCommandAndError(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	manager := attachTestProcessManager(t, rt)
	started, err := manager.Start(context.Background(), process.StartOptions{
		Command:   "sleep 30 # api_key=super-secret-token",
		OwnerKind: process.OwnerMainAgent,
		OwnerID:   "test",
		Lifecycle: process.LifecycleManaged,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _, _ = manager.Stop(started.ID) }()

	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"process/list","params":{"thread_id":"test"}}`)); err != nil {
		t.Fatalf("process/list: %v", err)
	}
	listed := remarshal[ProcessListResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if len(listed.Processes) != 1 {
		t.Fatalf("expected one process, got %+v", listed)
	}
	if strings.Contains(listed.Processes[0].Command, "super-secret-token") || !strings.Contains(listed.Processes[0].Command, "[REDACTED]") {
		t.Fatalf("process command was not redacted: %+v", listed.Processes[0])
	}
	summary := managedProcessSummary(process.Process{
		ID:        "proc-error",
		Command:   "echo ok",
		LastError: "password=super-secret-token",
	})
	if strings.Contains(summary.LastError, "super-secret-token") || !strings.Contains(summary.LastError, "[REDACTED]") {
		t.Fatalf("process error was not redacted: %+v", summary)
	}
}

func TestServerThreadResumeSchedulesProviderPrewarm(t *testing.T) {
	baseClient := &fakeClient{}
	rt := newTestRuntime(t, baseClient)
	prewarmer := &prewarmRecordingClient{
		fakeClient: baseClient,
		prewarmed:  make(chan string, 4),
	}
	rt.StreamRunner.Client = prewarmer
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()

	if err := srv.handleLine(context.Background(), []byte(`{"id":"start","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"]).Thread.ID
	waitForPrewarm := func(stage string) {
		t.Helper()
		select {
		case got := <-prewarmer.prewarmed:
			if got != threadID {
				t.Fatalf("%s prewarm session = %q, want %q", stage, got, threadID)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not schedule provider prewarm", stage)
		}
	}
	waitForPrewarm("start")

	resume := fmt.Sprintf(`{"id":"resume","method":"thread/resume","params":{"session_id":%q}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(resume)); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}
	waitForPrewarm("resume")
}

func TestServerThreadStartEphemeralDoesNotPersistSession(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start","params":{"ephemeral":true}}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	result := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if result.Thread.ID == "" || !result.Thread.Ephemeral {
		t.Fatalf("expected ephemeral thread: %+v", result.Thread)
	}
	if _, ok, err := session.Find(rt.SessionDir, result.Thread.ID); err != nil || ok {
		t.Fatalf("ephemeral thread should not create a session, ok=%v err=%v", ok, err)
	}
	sessions, err := session.List(rt.SessionDir, 0)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("ephemeral thread should not appear in session store: %+v", sessions)
	}

	if err := srv.handleLine(context.Background(), []byte(`{"id":"2","method":"thread/list"}`)); err != nil {
		t.Fatalf("thread/list: %v", err)
	}
	listed := remarshal[ThreadListResult](t, responseByID(t, parseOutput(t, out.String()), "2")["result"])
	if len(listed.Threads) != 0 {
		t.Fatalf("ephemeral thread should not appear in thread list: %+v", listed.Threads)
	}
}

func TestServerThreadStartPersistsInitialModelSelection(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Variant = "high"
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	thread := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread
	stored, ok, err := session.Find(rt.SessionDir, thread.ID)
	if err != nil || !ok {
		t.Fatalf("find session: ok=%v err=%v", ok, err)
	}
	if stored.Provider != rt.ProviderName || stored.Model != rt.Model || stored.Variant != "high" {
		t.Fatalf("initial model selection not persisted: %+v", stored)
	}
	if thread.ModelVariant != "high" {
		t.Fatalf("thread variant = %q, want high", thread.ModelVariant)
	}
}

func TestServerTurnStartRunsAgentLoop(t *testing.T) {
	client := &fakeClient{
		response: providers.ChatResponse{
			Content: "done",
			Usage:   &providers.TokenUsage{InputTokens: 10, OutputTokens: 3, CacheCreationTokens: 6, CacheReadTokens: 4},
		},
	}
	rt := newTestRuntime(t, client)
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	rt.Toolkit = kit
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	if strings.TrimSpace(threadID) == "" {
		t.Fatal("expected thread id")
	}

	payload := map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "hello"},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	msgs := waitForMethod(t, out, NotificationTurnCompleted)
	completed := notificationByMethod(t, msgs, NotificationTurnCompleted)
	params := remarshal[TurnCompletedNotification](t, completed["params"])
	if params.ThreadID != threadID || params.Turn.ID == "" || params.Turn.Status != TurnStatusCompleted || params.Content != "done" {
		t.Fatalf("unexpected completion: %+v", params)
	}
	if params.Turn.StartedAt == nil || params.Turn.CompletedAt == nil || params.Turn.DurationMS == nil {
		t.Fatalf("completed turn should include timing: %+v", params.Turn)
	}
	if params.InputTokens != 10 || params.OutputTokens != 3 || params.CacheCreationTokens != 6 || params.CacheReadTokens != 4 {
		t.Fatalf("unexpected usage: %+v", params)
	}
	if params.ContextTokens == 0 || params.Turn.ContextTokens == 0 {
		t.Fatalf("completed turn should carry retained durable context estimate: %+v", params)
	}
	if params.TracePath == "" {
		t.Fatalf("completed turn should include trace path: %+v", params)
	}
	if _, err := os.Stat(params.TracePath); err != nil {
		t.Fatalf("turn trace path should exist: %v", err)
	}
	traceSummary, err := sessiontrace.ReplayTrace(params.TracePath)
	if err != nil {
		t.Fatalf("replay turn trace: %v", err)
	}
	if len(traceSummary.ContextRequests) != 1 {
		t.Fatalf("expected one context request in trace, got %+v", traceSummary.ContextRequests)
	}
	requestRecord := traceSummary.ContextRequests[0]
	if requestRecord.InputTokens != 10 ||
		requestRecord.OutputTokens != 3 ||
		requestRecord.CacheCreationTokens != 6 ||
		requestRecord.CacheReadTokens != 4 {
		t.Fatalf("turn trace context request missing per-call usage: %+v", requestRecord)
	}
	if requestRecord.DynamicBytes != 0 || requestRecord.BlockKindBytes[string(wuucontext.BlockAvailableDeferred)] != 0 {
		t.Fatalf("turn trace should not include deferred tool index as request-only context: %+v", requestRecord)
	}

	event := turnEventByType(t, msgs, providers.EventContentDelta)
	eventParams := remarshal[TurnEventNotification](t, event["params"])
	if eventParams.Event.Type != providers.EventContentDelta || eventParams.Event.Content != "done" {
		t.Fatalf("unexpected turn event: %+v", eventParams)
	}
	contextEvent := turnEventByType(t, msgs, providers.EventRequestContext)
	contextParams := remarshal[TurnEventNotification](t, contextEvent["params"])
	if contextParams.Event.RequestContext == nil {
		t.Fatalf("request context missing from turn event: %+v", contextParams.Event)
	}
	if contextParams.Event.RequestContext.MessageCount == 0 ||
		contextParams.Event.RequestContext.DynamicBytes != 0 ||
		contextParams.Event.RequestContext.SystemBytes == 0 ||
		contextParams.Event.RequestContext.StablePrefixBytes == 0 ||
		contextParams.Event.RequestContext.TurnPrefixBytes == 0 ||
		contextParams.Event.RequestContext.MessageBytes == 0 ||
		contextParams.Event.RequestContext.ToolSchemaBytes == 0 ||
		contextParams.Event.RequestContext.SystemHash == "" ||
		contextParams.Event.RequestContext.StablePrefixHash == "" ||
		contextParams.Event.RequestContext.TurnPrefixHash == "" ||
		contextParams.Event.RequestContext.ToolSurfaceHash == "" {
		t.Fatalf("request context missing request shape metadata: %+v", contextParams.Event.RequestContext)
	}
	if contextParams.Event.RequestContext.BlockKindBytes[string(wuucontext.BlockAvailableDeferred)] != 0 {
		t.Fatalf("request context should not include deferred tool index metadata: %+v", contextParams.Event.RequestContext)
	}
	hasEnvironmentSection := false
	for _, section := range contextParams.Event.RequestContext.SystemSections {
		if section.Key == "environment" && section.Static && section.Bytes > 0 && section.Hash != "" {
			hasEnvironmentSection = true
			break
		}
	}
	if !hasEnvironmentSection {
		t.Fatalf("request context should report stable environment system section: %+v", contextParams.Event.RequestContext.SystemSections)
	}
	hasToolPolicySection := false
	for _, section := range contextParams.Event.RequestContext.SystemSections {
		if section.Key == "tool_policy" {
			hasToolPolicySection = true
			break
		}
	}
	if hasToolPolicySection {
		t.Fatalf("request context should keep runtime tool policy out of stable system sections: %+v", contextParams.Event.RequestContext.SystemSections)
	}
	for _, unwanted := range []string{"ENVIRONMENT", "TOOL_POLICY", "TASK", "CONSTRAINT_LEDGER"} {
		if testStringSliceContains(contextParams.Event.RequestContext.BlockKinds, unwanted) {
			t.Fatalf("single-turn request should not include block kind %s: %+v", unwanted, contextParams.Event.RequestContext)
		}
	}
	delta := notificationByMethod(t, msgs, NotificationAgentMessageDelta)
	deltaParams := remarshal[AgentMessageDeltaNotification](t, delta["params"])
	if deltaParams.ThreadID != threadID || deltaParams.Delta != "done" {
		t.Fatalf("unexpected agent delta: %+v", deltaParams)
	}
	itemCompleted := notificationByMethod(t, msgs, NotificationItemCompleted)
	itemParams := remarshal[ItemCompletedNotification](t, itemCompleted["params"])
	if itemParams.Item.Type != ThreadItemAgentMessage || itemParams.Item.Text != "done" {
		t.Fatalf("unexpected completed item: %+v", itemParams)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.requests) != 1 {
		t.Fatalf("expected one provider request, got %d", len(client.requests))
	}
	messages := client.requests[0].Messages
	if len(messages) < 2 || messages[0].Role != "system" || messages[1].Role != "user" || messages[1].Content != "hello" {
		t.Fatalf("unexpected agent-loop messages: %+v", messages)
	}

	persisted, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("load persisted history: %v", err)
	}
	visiblePersisted := visibleMessagesForTest(persisted)
	if len(visiblePersisted) != 2 || visiblePersisted[0].Role != "user" || visiblePersisted[0].Content != "hello" || visiblePersisted[1].Role != "assistant" || visiblePersisted[1].Content != "done" {
		t.Fatalf("unexpected persisted history: %+v", persisted)
	}
	sessions, err := session.List(rt.SessionDir, 1)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != threadID || sessions[0].Entries != 2 || sessions[0].Summary != "hello" {
		t.Fatalf("unexpected session index: %+v", sessions)
	}
}

func TestServerTurnDispatchesPromptAndStopHooksOnce(t *testing.T) {
	client := &fakeClient{response: providersResponse("done")}
	rt := newTestRuntime(t, client)
	logPath := filepath.Join(t.TempDir(), "turn-hooks.log")
	command := func(event string) string { return fmt.Sprintf("printf '%s\\n' >> %q", event, logPath) }
	rt.HookDispatcher = hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
		hooks.UserPromptSubmit: {{Command: command("prompt")}},
		hooks.Stop:             {{Command: command("stop")}},
	}))
	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	payload, err := json.Marshal(map[string]any{
		"id": "2", "method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "hello"},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	waitForMethod(t, out, NotificationTurnCompleted)
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got, want := strings.Fields(string(raw)), []string{"prompt", "stop"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("turn hook events = %v, want %v", got, want)
	}
}

func TestServerUserPromptHookBlocksBeforeHistory(t *testing.T) {
	client := &fakeClient{response: providersResponse("should not run")}
	rt := newTestRuntime(t, client)
	rt.HookDispatcher = hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
		hooks.UserPromptSubmit: {{Command: "exit 2"}},
	}))
	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	payload, err := json.Marshal(map[string]any{
		"id": "2", "method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "blocked"},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatalf("turn/start response: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "2")
	if response["error"] == nil {
		t.Fatalf("blocked prompt should return an RPC error: %+v", response)
	}
	th := srv.thread(threadID)
	th.mu.Lock()
	defer th.mu.Unlock()
	for _, msg := range th.History {
		if msg.Role == "user" && msg.Content == "blocked" {
			t.Fatal("blocked user prompt was appended to history")
		}
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.requests) != 0 {
		t.Fatalf("provider requests = %d, want 0", len(client.requests))
	}
}

func TestServerThreadContextCompositionReturnsLatestRequest(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{
		Content: "done",
		Usage: &providers.TokenUsage{
			InputTokens:         10,
			OutputTokens:        3,
			CacheCreationTokens: 6,
			CacheReadTokens:     4,
		},
	}}
	rt := newTestRuntime(t, client)
	rt.ModelBudget = modelbudget.Budget{
		ContextWindowTokens:    1_000_000,
		InputLimitTokens:       512_000,
		UsableInputTokens:      384_000,
		CompactThresholdTokens: 384_000,
		ContextWindowSource:    modelbudget.SourceProviderModelLimit,
		ContextWindowKnown:     true,
	}
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("create toolkit: %v", err)
	}
	rt.Toolkit = kit
	out := &lockedBuffer{}
	srv := New(rt, out)

	raw, err := json.Marshal(map[string]any{
		"id":     "start",
		"method": MethodThreadStart,
		"params": ThreadStartParams{},
	})
	if err != nil {
		t.Fatalf("marshal start request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	start := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"])
	threadID := start.Thread.ID

	raw, err = json.Marshal(map[string]any{
		"id":     "turn",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "hello"},
	})
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	_ = waitForMethod(t, out, NotificationTurnCompleted)

	raw, err = json.Marshal(map[string]any{
		"id":     "context",
		"method": MethodThreadContextComposition,
		"params": ThreadContextCompositionParams{ThreadID: threadID},
	})
	if err != nil {
		t.Fatalf("marshal context request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("thread/context-composition: %v", err)
	}
	result := remarshal[ThreadContextCompositionResult](t, responseByID(t, parseOutput(t, out.String()), "context")["result"])
	if !result.Available || result.Mode != contextCompositionModeLatestRequest || result.ThreadID != threadID {
		t.Fatalf("unexpected availability: %+v", result)
	}
	if result.TurnID == "" || result.TracePath == "" {
		t.Fatalf("expected latest turn and trace path: %+v", result)
	}
	if result.PromptTokens != 20 || result.TotalContextTokens != 23 || result.RetainedTokens != 23 {
		t.Fatalf("unexpected token totals: %+v", result)
	}
	if result.InputTokens != 10 || result.OutputTokens != 3 || result.CacheCreationTokens != 6 || result.CacheReadTokens != 4 {
		t.Fatalf("unexpected provider usage: %+v", result)
	}
	if result.ContextWindowTokens != 512_000 {
		t.Fatalf("context composition should expose the unified context ceiling: %+v", result)
	}
	if result.InputLimitTokens != 512_000 || result.UsableInputTokens != 384_000 || result.CompactThresholdTokens != 384_000 {
		t.Fatalf("unexpected runtime context limits: %+v", result)
	}
	if result.TokenEstimateSource != "provider_usage" {
		t.Fatalf("expected provider usage allocation, got %q", result.TokenEstimateSource)
	}
	if result.MessageCount == 0 || result.SystemMessages == 0 || result.ToolCount == 0 {
		t.Fatalf("expected request shape counts: %+v", result)
	}
	if result.SystemHash == "" || result.StablePrefixHash == "" || result.TurnPrefixHash == "" || result.ToolSurfaceHash == "" || result.PromptCacheKey == "" {
		t.Fatalf("expected cache shape hashes: %+v", result)
	}
	categories := map[string]ContextCompositionCategory{}
	for _, category := range result.Categories {
		categories[category.ID] = category
	}
	for _, id := range []string{"system", "turn_prefix", "tool_schema"} {
		category, ok := categories[id]
		if !ok || !category.Contributes || category.Tokens <= 0 || category.Bytes <= 0 {
			t.Fatalf("expected contributing %s category, got %+v", id, category)
		}
	}
	if result.BlockKindBytes[string(wuucontext.BlockAvailableDeferred)] != 0 {
		t.Fatalf("context composition should not include deferred tool index bytes: %+v", result.BlockKindBytes)
	}
	if len(result.SystemSections) == 0 {
		t.Fatalf("expected system sections: %+v", result)
	}
}

func TestContextCompositionFromTraceExposesUnifiedContextCeiling(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	srv := New(rt, &lockedBuffer{})
	summary := sessiontrace.ReplaySummary{
		LatestTurn: &sessiontrace.TurnRecord{
			TurnID:       "turn-1",
			ProviderName: "custom-provider",
			Model:        "bring-your-own-model",
			ModelProfile: &sessiontrace.ModelProfileRecord{
				ContextWindowTokens:    1_000_000,
				InputLimitTokens:       512_000,
				UsableInputTokens:      384_000,
				CompactThresholdTokens: 384_000,
			},
		},
		ContextRequests: []sessiontrace.RequestContextRecord{{
			StepIndex:         0,
			MessageBytes:      2_000,
			SystemBytes:       100,
			StablePrefixBytes: 400,
			TurnPrefixBytes:   1_600,
			InputTokens:       508_000,
			MessageCount:      4,
		}},
		RequestSteps: []sessiontrace.RequestStepSummary{{
			TurnID:    "turn-1",
			StepIndex: 0,
			Provider:  "custom-provider",
		}},
	}

	result := srv.contextCompositionFromTrace("thread-1", "trace.jsonl", summary)
	if !result.Available {
		t.Fatalf("expected context composition to be available: %+v", result)
	}
	if result.ContextWindowTokens != 512_000 {
		t.Fatalf("context composition should expose the unified context ceiling: %+v", result)
	}
	if result.InputLimitTokens != 512_000 || result.UsableInputTokens != 384_000 || result.CompactThresholdTokens != 384_000 {
		t.Fatalf("unexpected runtime context limits: %+v", result)
	}
	if result.Provider != "custom-provider" || result.Model != "bring-your-own-model" {
		t.Fatalf("unexpected BYOK runtime identity: %+v", result)
	}
}

func TestServerCodexWebSocketReplayAcrossThreadTurns(t *testing.T) {
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		for i := 0; i < 2; i++ {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				t.Errorf("read request %d: %v", i+1, err)
				return
			}
			if typ != websocket.MessageText {
				t.Errorf("request %d type = %v", i+1, typ)
				return
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				t.Errorf("decode request %d: %v", i+1, err)
				return
			}
			requests <- body

			if i == 0 {
				writeAppServerWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`)
				writeAppServerWSEvent(t, ctx, conn, `{"type":"response.output_item.added","item":{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","status":"in_progress"},"output_index":0}`)
				writeAppServerWSEvent(t, ctx, conn, `{"type":"response.output_text.delta","delta":"first answer","item_id":"msg_1","output_index":0}`)
				writeAppServerWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"first answer"}]},"output_index":0}`)
				writeAppServerWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":12,"output_tokens":2}}}`)
			} else {
				writeAppServerWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_2","status":"in_progress"}}`)
				writeAppServerWSEvent(t, ctx, conn, `{"type":"response.output_item.added","item":{"id":"msg_2","type":"message","role":"assistant","phase":"final_answer","status":"in_progress"},"output_index":0}`)
				writeAppServerWSEvent(t, ctx, conn, `{"type":"response.output_text.delta","delta":"second answer","item_id":"msg_2","output_index":0}`)
				writeAppServerWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_2","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"second answer"}]},"output_index":0}`)
				writeAppServerWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_2","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":2,"input_tokens_details":{"cached_tokens":2}}}}`)
			}
		}
	}))
	defer server.Close()

	client, err := codex.New(codex.ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-token",
		StreamConfig: &providers.StreamTransportConfig{
			ConnectTimeout: 2 * time.Second,
			IdleTimeout:    5 * time.Second,
		},
	})
	if err != nil {
		t.Fatalf("codex.New: %v", err)
	}
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	runtimeState := "alpha"
	rt := &runtime.Session{
		ProviderName: "openai-codex",
		Model:        "gpt-5.5",
		RootDir:      root,
		ConfigPath:   filepath.Join(root, ".wuu.json"),
		SessionDir:   filepath.Join(root, ".wuu-home", "sessions"),
		StreamRunner: &agent.StreamRunner{
			Client:       providers.AdaptStreamClient(client),
			Model:        "gpt-5.5",
			SystemPrompt: "stable system prompt",
			SystemPromptSections: []agent.SystemPromptSectionInfo{{
				Key:    "base",
				Static: true,
				Bytes:  len("stable system prompt"),
				Hash:   "base-hash",
			}},
			MaxSteps: 1,
		},
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	threadRuntime, err := srv.ensureThreadRuntime(srv.thread(threadID))
	if err != nil {
		t.Fatal(err)
	}
	threadRuntime.StreamRunner.BeforeRequestContext = func() []agent.ContextSegment {
		return agent.RequestOnlyContextBlocks([]wuucontext.Block{{
			Kind: wuucontext.BlockToolResultSummary, Title: "Tool state", Source: "tool_telemetry", Content: "State: " + runtimeState,
		}})
	}
	startTurn := func(id, prompt string) {
		t.Helper()
		payload := map[string]any{
			"id":     id,
			"method": MethodTurnStart,
			"params": TurnStartParams{ThreadID: threadID, Prompt: prompt},
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal turn request: %v", err)
		}
		if err := srv.handleLine(context.Background(), raw); err != nil {
			t.Fatalf("turn/start %s: %v", id, err)
		}
	}

	startTurn("2", "say first answer")
	waitForTurnCompletedCountForThread(t, out, threadID, 1)
	runtimeState = "beta"
	startTurn("3", "say second answer")
	msgs := waitForTurnCompletedCountForThread(t, out, threadID, 2)

	providerStates := turnEventsByTypeForThread(t, msgs, threadID, providers.EventProviderState)
	if len(providerStates) != 2 {
		t.Fatalf("provider state events = %d, want 2", len(providerStates))
	}
	firstState := providerStates[0].Event.ProviderState
	secondState := providerStates[1].Event.ProviderState
	if firstState == nil || firstState.ReplayMode != "full_request" || firstState.PreviousResponseIDUsed {
		t.Fatalf("unexpected first provider state: %+v", firstState)
	}
	if secondState == nil || secondState.ReplayMode != "previous_response_id" || !secondState.PreviousResponseIDUsed {
		t.Fatalf("same app-server thread did not use previous_response_id on second turn: first=%+v second=%+v", firstState, secondState)
	}
	if firstState.StepIndex != 0 || secondState.StepIndex != 0 {
		t.Fatalf("single-step turns should annotate provider state with step_index=0: first=%+v second=%+v", firstState, secondState)
	}
	if secondState.FullInputItems <= secondState.InputItems || secondState.DeltaInputItems != secondState.InputItems {
		t.Fatalf("second provider state should report a smaller delta input: %+v", secondState)
	}

	contexts := turnEventsByTypeForThread(t, msgs, threadID, providers.EventRequestContext)
	if len(contexts) != 2 {
		t.Fatalf("request context events = %d, want 2", len(contexts))
	}
	firstContext := contexts[0].Event.RequestContext
	secondContext := contexts[1].Event.RequestContext
	if firstContext == nil || secondContext == nil {
		t.Fatalf("missing request context: first=%+v second=%+v", firstContext, secondContext)
	}
	if firstContext.PromptCacheKey != threadID || secondContext.PromptCacheKey != threadID {
		t.Fatalf("prompt cache key should stay pinned to thread id: first=%q second=%q thread=%q", firstContext.PromptCacheKey, secondContext.PromptCacheKey, threadID)
	}
	if firstContext.SystemHash == "" || secondContext.SystemHash != firstContext.SystemHash {
		t.Fatalf("system hash drifted across turns: first=%q second=%q", firstContext.SystemHash, secondContext.SystemHash)
	}
	if secondContext.ToolSurfaceHash != firstContext.ToolSurfaceHash {
		t.Fatalf("tool surface hash drifted across turns: first=%q second=%q", firstContext.ToolSurfaceHash, secondContext.ToolSurfaceHash)
	}

	firstRequest := <-requests
	secondRequest := <-requests
	if firstRequest["prompt_cache_key"] != threadID || secondRequest["prompt_cache_key"] != threadID {
		t.Fatalf("wire prompt_cache_key drifted: first=%#v second=%#v thread=%q", firstRequest["prompt_cache_key"], secondRequest["prompt_cache_key"], threadID)
	}
	if _, exists := firstRequest["previous_response_id"]; exists {
		t.Fatalf("first request should be full context: %#v", firstRequest)
	}
	if secondRequest["previous_response_id"] != "resp_1" {
		t.Fatalf("second request previous_response_id = %#v; body=%#v", secondRequest["previous_response_id"], secondRequest)
	}
	firstInput := firstRequest["input"].([]any)
	secondInput := secondRequest["input"].([]any)
	if len(firstInput) != firstState.InputItems {
		t.Fatalf("first request input items drifted from provider state: wire=%d state=%+v", len(firstInput), firstState)
	}
	if len(secondInput) != secondState.InputItems || len(secondInput) != secondState.DeltaInputItems {
		t.Fatalf("second request should send the provider-reported delta input, wire=%d state=%+v body=%#v", len(secondInput), secondState, secondRequest)
	}
	if len(secondInput) == 0 {
		t.Fatalf("second request missing delta input: %#v", secondRequest)
	}
	if len(secondInput) != 2 {
		t.Fatalf("second request should send the changed runtime-context update and new user input, first=%#v second=%#v states=%+v", firstRequest, secondRequest, providerStates)
	}
	secondInputJSON, err := json.Marshal(secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(secondInputJSON, []byte("State: beta")) || !bytes.Contains(secondInputJSON, []byte("say second answer")) {
		t.Fatalf("second request delta missing fresh context or user input: %s", secondInputJSON)
	}
	if bytes.Contains(secondInputJSON, []byte("State: alpha")) {
		t.Fatalf("second request delta re-sent superseded context: %s", secondInputJSON)
	}
	secondInputText := fmt.Sprintf("%#v", secondInput)
	for _, unwanted := range []string{"[TASK]", "[CONSTRAINT_LEDGER]"} {
		if strings.Contains(secondInputText, unwanted) {
			t.Fatalf("second request should not include default request-only context block %s: %#v", unwanted, secondInput)
		}
	}
	if strings.Contains(secondInputText, "[ENVIRONMENT]") {
		t.Fatalf("second request should not repeat stable environment as request-only context: %#v", secondInput)
	}
}

func TestServerTurnPermissionModeChangesExecutionWithoutCacheDrift(t *testing.T) {
	client := &fakeClient{
		responses: []providers.ChatResponse{
			{ToolCalls: []providers.ToolCall{{
				ID:        "call-read-only-write",
				Name:      "write_file",
				Arguments: `{"path":"blocked.txt","content":"nope\n","create_only":true}`,
			}}},
			{Content: "read only done"},
			{ToolCalls: []providers.ToolCall{{
				ID:        "call-full-write",
				Name:      "write_file",
				Arguments: `{"path":"allowed.txt","content":"ok\n","create_only":true}`,
			}}},
			{Content: "full access done"},
		},
	}
	rt := newTestRuntime(t, client)
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeStandard}
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "model": "fake-model"
    }
  }
}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	rt.Toolkit = kit
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	updatePermission := func(id, mode string) {
		t.Helper()
		raw := fmt.Sprintf(`{"id":%q,"method":"config/model/update","params":{"thread_id":%q,"model":"fake-model","permission_mode":%q}}`, id, threadID, mode)
		if err := srv.handleLine(context.Background(), []byte(raw)); err != nil {
			t.Fatalf("config/model/update %s: %v", id, err)
		}
		response := responseByID(t, parseOutput(t, out.String()), id)
		if response["error"] != nil {
			t.Fatalf("config/model/update %s returned error: %+v", id, response["error"])
		}
		th := srv.thread(threadID)
		th.mu.Lock()
		gotMode := th.PermissionMode
		th.mu.Unlock()
		if gotMode != mode {
			t.Fatalf("thread permission after %s = %q, want %q", id, gotMode, mode)
		}
	}
	startTurn := func(id, prompt string) {
		t.Helper()
		raw := fmt.Sprintf(`{"id":%q,"method":"turn/start","params":{"thread_id":%q,"prompt":%q}}`, id, threadID, prompt)
		if err := srv.handleLine(context.Background(), []byte(raw)); err != nil {
			t.Fatalf("turn/start %s: %v", id, err)
		}
	}

	updatePermission("permission-read-only", config.PermissionModeReadOnly)
	startTurn("2", "try a read-only write")
	waitForTurnCompletedCountForThread(t, out, threadID, 1)
	updatePermission("permission-unconfined", config.PermissionModeUnconfined)
	startTurn("3", "try an unconfined write")
	msgs := waitForTurnCompletedCountForThread(t, out, threadID, 2)

	completed := make([]TurnCompletedNotification, 0, 2)
	for _, msg := range msgs {
		if msg["id"] != nil || msg["method"] != NotificationTurnCompleted {
			continue
		}
		params := remarshal[TurnCompletedNotification](t, msg["params"])
		if params.ThreadID == threadID {
			completed = append(completed, params)
		}
	}
	if len(completed) != 2 {
		t.Fatalf("completed turns = %d, want 2", len(completed))
	}

	contexts := turnEventsByTypeForThread(t, msgs, threadID, providers.EventRequestContext)
	firstContextByTurn := map[string]*providers.RequestContextSummary{}
	for _, event := range contexts {
		if event.Event.RequestContext != nil && firstContextByTurn[event.TurnID] == nil {
			firstContextByTurn[event.TurnID] = event.Event.RequestContext
		}
	}
	firstContext := firstContextByTurn[completed[0].Turn.ID]
	secondContext := firstContextByTurn[completed[1].Turn.ID]
	if firstContext == nil || secondContext == nil {
		t.Fatalf("missing request context: first=%+v second=%+v all=%+v", firstContext, secondContext, contexts)
	}
	if firstContext.PromptCacheKey != threadID || secondContext.PromptCacheKey != threadID {
		t.Fatalf("prompt cache key drifted: first=%q second=%q thread=%q", firstContext.PromptCacheKey, secondContext.PromptCacheKey, threadID)
	}
	if firstContext.SystemHash == "" || secondContext.SystemHash != firstContext.SystemHash {
		t.Fatalf("system hash drifted after permission switch: first=%q second=%q", firstContext.SystemHash, secondContext.SystemHash)
	}
	if firstContext.ToolSurfaceHash == "" || secondContext.ToolSurfaceHash != firstContext.ToolSurfaceHash {
		t.Fatalf("tool surface hash drifted after permission switch: first=%q second=%q", firstContext.ToolSurfaceHash, secondContext.ToolSurfaceHash)
	}

	th := srv.thread(threadID)
	if th == nil || th.execRuntime == nil || th.execRuntime.Toolkit == nil {
		t.Fatalf("missing thread runtime")
	}
	records := th.execRuntime.Toolkit.ToolTelemetry()
	var readOnlyRecord, fullRecord *tools.ToolExecutionRecord
	for i := range records {
		switch records[i].CallID {
		case "call-read-only-write":
			readOnlyRecord = &records[i]
		case "call-full-write":
			fullRecord = &records[i]
		}
	}
	if readOnlyRecord == nil || readOnlyRecord.Success || readOnlyRecord.ErrorKind != "boundary_denied" {
		t.Fatalf("read-only turn should deny write by permission boundary: %+v records=%+v", readOnlyRecord, records)
	}
	if fullRecord == nil || !fullRecord.Success {
		t.Fatalf("full-access turn should execute write: %+v records=%+v", fullRecord, records)
	}
	if _, err := os.Stat(filepath.Join(rt.RootDir, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("read-only turn should not create blocked file, stat err=%v", err)
	}
	if data, err := os.ReadFile(filepath.Join(rt.RootDir, "allowed.txt")); err != nil || string(data) != "ok\n" {
		t.Fatalf("full-access turn should create allowed file, data=%q err=%v", data, err)
	}

	traceSummary, err := sessiontrace.ReplayTrace(completed[1].TracePath)
	if err != nil {
		t.Fatalf("replay trace: %v", err)
	}
	if len(traceSummary.Turns) < 2 {
		t.Fatalf("trace should record both turns: %+v", traceSummary.Turns)
	}
	if traceSummary.Turns[len(traceSummary.Turns)-2].PermissionMode != config.PermissionModeReadOnly ||
		traceSummary.Turns[len(traceSummary.Turns)-1].PermissionMode != config.PermissionModeUnconfined {
		t.Fatalf("trace permission modes = %+v", traceSummary.Turns)
	}
}

func TestServerQueuedTurnReResolvesPermissionsAtStart(t *testing.T) {
	client := &fakeClient{
		responses: []providers.ChatResponse{
			{ToolCalls: []providers.ToolCall{{
				ID:        "call-queued-write",
				Name:      "write_file",
				Arguments: `{"path":"queued-blocked.txt","content":"nope\n","create_only":true}`,
			}}},
			{Content: "queued done"},
		},
	}
	rt := newTestRuntime(t, client)
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	rt.Toolkit = kit
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	// The snapshot froze the broader mode that governed when the turn was
	// queued; the thread has since been tightened to read-only. Start-time
	// re-resolution must ignore the frozen mode and run the turn read-only.
	unconfined := config.ResolvedPermissions{Mode: config.PermissionModeUnconfined}
	started, err := srv.startQueuedTurn(context.Background(), threadID, queuedTurn{
		id:       "queued-stale-unconfined",
		msg:      providers.ChatMessage{Role: "user", Content: "queued write"},
		snapshot: turnRuntimeSnapshot{}.withPermissions(unconfined),
	})
	if err != nil {
		t.Fatalf("startQueuedTurn: %v", err)
	}
	if !started {
		t.Fatal("queued turn did not start")
	}
	msgs := waitForTurnCompletedCountForThread(t, out, threadID, 1)
	completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, msgs, NotificationTurnCompleted)["params"])
	traceSummary, err := sessiontrace.ReplayTrace(completed.TracePath)
	if err != nil {
		t.Fatalf("replay trace: %v", err)
	}
	if len(traceSummary.Turns) == 0 || traceSummary.Turns[len(traceSummary.Turns)-1].PermissionMode != config.PermissionModeReadOnly {
		t.Fatalf("queued turn should re-resolve to the tightened mode: %+v", traceSummary.Turns)
	}
	if _, err := os.Stat(filepath.Join(rt.RootDir, "queued-blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("queued read-only turn should not create file, stat err=%v", err)
	}
	records := srv.thread(threadID).execRuntime.Toolkit.ToolTelemetry()
	if len(records) != 1 || records[0].CallID != "call-queued-write" || records[0].ErrorKind != "boundary_denied" {
		t.Fatalf("queued read-only turn should deny write by boundary: %+v", records)
	}
}

func TestServerTurnStartRendersLightweightSlashCommandForModel(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "done"}}
	rt := newTestRuntime(t, client)
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	payload := map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "/debug login failure"},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	started := remarshal[TurnStartResult](t, responseByID(t, parseOutput(t, out.String()), "2")["result"])
	if len(started.Turn.Items) != 1 || started.Turn.Items[0].Text != "/debug login failure" {
		t.Fatalf("turn should display raw slash command: %+v", started.Turn.Items)
	}
	_ = waitForMethod(t, out, NotificationTurnCompleted)

	client.mu.Lock()
	requestCount := len(client.requests)
	modelPrompt := ""
	if requestCount == 1 && len(client.requests[0].Messages) > 1 {
		modelPrompt = client.requests[0].Messages[1].Content
	}
	client.mu.Unlock()
	if requestCount != 1 {
		t.Fatalf("expected one provider request, got %d", requestCount)
	}
	for _, want := range []string{"Investigate this problem", "login failure", "root cause"} {
		if !strings.Contains(modelPrompt, want) {
			t.Fatalf("model prompt missing %q:\n%s", want, modelPrompt)
		}
	}
	if strings.Contains(modelPrompt, "/debug") {
		t.Fatalf("model prompt should not include raw slash command:\n%s", modelPrompt)
	}

	persisted, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("load persisted history: %v", err)
	}
	if len(persisted) < 1 || persisted[0].DisplayContent != "/debug login failure" || !strings.Contains(persisted[0].Content, "Investigate this problem") {
		t.Fatalf("persisted user message did not keep display/model split: %+v", persisted)
	}
	sessions, err := session.List(rt.SessionDir, 1)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Summary != "/debug login failure" {
		t.Fatalf("session summary should use slash display text: %+v", sessions)
	}
}

func TestServerTurnStartForwardsStreamingUsage(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = usageStreamClient{events: []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "hello"},
		{Type: providers.EventUsage, Usage: &providers.TokenUsage{InputTokens: 8, OutputTokens: 2}},
		{Type: providers.EventContentDelta, Content: " world"},
		{Type: providers.EventUsage, Usage: &providers.TokenUsage{InputTokens: 8, OutputTokens: 4}},
		{Type: providers.EventDone, Usage: &providers.TokenUsage{InputTokens: 8, OutputTokens: 4}},
	}}
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	rt.Toolkit = kit
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	payload := map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "hello"},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	msgs := waitForMethod(t, out, NotificationTurnCompleted)
	usageNotifications := notificationsByMethod(msgs, NotificationTurnUsage)
	if len(usageNotifications) == 0 {
		t.Fatalf("expected streaming usage notification; messages=%+v", msgs)
	}
	firstUsage := remarshal[TurnUsageNotification](t, usageNotifications[0]["params"])
	if firstUsage.ThreadID != threadID || firstUsage.InputTokens != 8 || firstUsage.OutputTokens != 2 {
		t.Fatalf("unexpected first streaming usage: %+v", firstUsage)
	}
	if firstUsage.Model != "fake-model" {
		t.Fatalf("usage notification should carry runner model: %+v", firstUsage)
	}
	if firstUsage.ContextTokens != 10 {
		t.Fatalf("usage notification should carry current request context: %+v", firstUsage)
	}
	if firstUsage.ContextWindowTokens != 0 {
		t.Fatalf("unknown model should not emit fallback context window: %+v", firstUsage)
	}
	completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, msgs, NotificationTurnCompleted)["params"])
	if completed.InputTokens != 8 || completed.OutputTokens != 4 {
		t.Fatalf("unexpected completed usage: %+v", completed)
	}
}

func TestServerTurnStartForwardsRuntimeContextWindow(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.ContextWindowOverride = 512000
	rt.StreamRunner.Client = usageStreamClient{events: []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "hello"},
		{Type: providers.EventUsage, Usage: &providers.TokenUsage{InputTokens: 8, OutputTokens: 2}},
		{Type: providers.EventDone, Usage: &providers.TokenUsage{InputTokens: 8, OutputTokens: 2}},
	}}
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	rt.Toolkit = kit
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	payload := map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "hello"},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	msgs := waitForMethod(t, out, NotificationTurnCompleted)
	usageNotifications := notificationsByMethod(msgs, NotificationTurnUsage)
	if len(usageNotifications) == 0 {
		t.Fatalf("expected streaming usage notification; messages=%+v", msgs)
	}
	firstUsage := remarshal[TurnUsageNotification](t, usageNotifications[0]["params"])
	if firstUsage.ContextWindowTokens != 512000 {
		t.Fatalf("context window should come from runtime override: %+v", firstUsage)
	}
}

func TestServerTurnStartUsesInputLimitAsDisplayedContextWindow(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.ContextWindowOverride = 400000
	rt.StreamRunner.MaxInputTokens = 272000
	rt.StreamRunner.Client = usageStreamClient{events: []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "hello"},
		{Type: providers.EventUsage, Usage: &providers.TokenUsage{InputTokens: 8, OutputTokens: 2}},
		{Type: providers.EventDone, Usage: &providers.TokenUsage{InputTokens: 8, OutputTokens: 2}},
	}}
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	rt.Toolkit = kit
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	payload := map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "hello"},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	msgs := waitForMethod(t, out, NotificationTurnCompleted)
	usageNotifications := notificationsByMethod(msgs, NotificationTurnUsage)
	if len(usageNotifications) == 0 {
		t.Fatalf("expected streaming usage notification; messages=%+v", msgs)
	}
	firstUsage := remarshal[TurnUsageNotification](t, usageNotifications[0]["params"])
	if firstUsage.ContextWindowTokens != 272000 {
		t.Fatalf("context meter should use lower input limit: %+v", firstUsage)
	}
}

func TestServerTurnStartUsesInputLimitWhenContextWindowUnknown(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.ContextWindowOverride = 0
	rt.StreamRunner.MaxInputTokens = 272000
	rt.StreamRunner.Client = usageStreamClient{events: []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "hello"},
		{Type: providers.EventUsage, Usage: &providers.TokenUsage{InputTokens: 8, OutputTokens: 2}},
		{Type: providers.EventDone, Usage: &providers.TokenUsage{InputTokens: 8, OutputTokens: 2}},
	}}
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	rt.Toolkit = kit
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	payload := map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "hello"},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	msgs := waitForMethod(t, out, NotificationTurnCompleted)
	usageNotifications := notificationsByMethod(msgs, NotificationTurnUsage)
	if len(usageNotifications) == 0 {
		t.Fatalf("expected streaming usage notification; messages=%+v", msgs)
	}
	firstUsage := remarshal[TurnUsageNotification](t, usageNotifications[0]["params"])
	if firstUsage.ContextWindowTokens != 272000 {
		t.Fatalf("context meter should use input limit when model window is unknown: %+v", firstUsage)
	}
}

func assertFakeClientRequestCount(t *testing.T, client *fakeClient, want int) {
	t.Helper()
	if got := fakeClientRequestCount(client); got != want {
		t.Fatalf("provider request count = %d, want %d", got, want)
	}
}

func fakeClientRequestCount(client *fakeClient) int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return len(client.requests)
}

func TestServerQueuesUserTurnWhileThreadIsRunning(t *testing.T) {
	client := newBlockingStreamClient("done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	startReq := fmt.Sprintf(`{"id":"2","method":"turn/start","params":{"thread_id":%q,"prompt":"first"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(startReq)); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	<-client.started

	queueReq := fmt.Sprintf(`{"id":"3","method":"turn/queue","params":{"thread_id":%q,"prompt":"/fix login failure","client_id":"queued-1"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(queueReq)); err != nil {
		t.Fatalf("turn/queue: %v", err)
	}
	queueResult := remarshal[TurnQueueResult](t, responseByID(t, parseOutput(t, out.String()), "3")["result"])
	if queueResult.Queued.ID != "queued-1" || queueResult.Queued.ThreadID != threadID {
		t.Fatalf("unexpected queue result: %+v", queueResult)
	}
	if queueResult.Queued.Preview != "/fix login failure" {
		t.Fatalf("queued preview should use slash display text: %+v", queueResult.Queued)
	}

	close(client.release)
	msgs := waitForTurnCompletedCountForThread(t, out, threadID, 2)
	var queuedStarted bool
	for _, msg := range msgs {
		if msg["method"] != NotificationTurnStarted {
			continue
		}
		params := msg["params"].(map[string]any)
		if params["thread_id"] == threadID && params["queue_id"] == "queued-1" {
			queuedStarted = true
			break
		}
	}
	if !queuedStarted {
		t.Fatalf("queued turn did not publish queue_id; output:\n%s", out.String())
	}

	th := srv.thread(threadID)
	th.mu.Lock()
	history := append([]providers.ChatMessage(nil), th.History...)
	th.mu.Unlock()
	var found bool
	for _, msg := range history {
		if msg.Role == "user" &&
			strings.Contains(msg.Content, "Fix this issue") &&
			strings.Contains(msg.Content, "login failure") &&
			msg.DisplayContent == "/fix login failure" &&
			msg.ClientID == "queued-1" &&
			!msg.Steered {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("history missing queued user turn: %+v", history)
	}
}

func TestServerInterruptHoldsPendingUserWorkInOrderAndReleasesOne(t *testing.T) {
	client := newBlockingStreamClient("done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	startReq := fmt.Sprintf(`{"id":"2","method":"turn/start","params":{"thread_id":%q,"prompt":"first"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(startReq)); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	started := remarshal[TurnStartResult](t, responseByID(t, parseOutput(t, out.String()), "2")["result"])
	<-client.started

	queueReq := fmt.Sprintf(`{"id":"3","method":"turn/queue","params":{"thread_id":%q,"prompt":"queued follow-up","client_id":"queued-1"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(queueReq)); err != nil {
		t.Fatalf("turn/queue: %v", err)
	}
	queueReq = fmt.Sprintf(`{"id":"3b","method":"turn/queue","params":{"thread_id":%q,"prompt":"second follow-up","client_id":"queued-2"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(queueReq)); err != nil {
		t.Fatalf("second turn/queue: %v", err)
	}

	steerReq := fmt.Sprintf(`{"id":"4","method":"turn/steer","params":{"thread_id":%q,"expected_turn_id":%q,"prompt":"guide now","client_id":"guide-1","active_document":{"path":"docs/guide.md"}}}`, threadID, started.Turn.ID)
	if err := srv.handleLine(context.Background(), []byte(steerReq)); err != nil {
		t.Fatalf("turn/steer: %v", err)
	}

	interruptReq := fmt.Sprintf(`{"id":"5","method":"turn/interrupt","params":{"thread_id":%q}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(interruptReq)); err != nil {
		t.Fatalf("turn/interrupt: %v", err)
	}
	interruptResponse := responseByID(t, parseOutput(t, out.String()), "5")
	if interruptResponse["error"] != nil {
		t.Fatalf("turn/interrupt returned error: %+v", interruptResponse["error"])
	}
	if srv.hasQueuedUserTurns(threadID) {
		t.Fatal("held turns must not remain in the runnable queue")
	}
	th := srv.thread(threadID)
	th.mu.Lock()
	pendingSteers := len(th.pendingSteers)
	th.mu.Unlock()
	if pendingSteers != 0 {
		t.Fatalf("interrupt should move pending steers out of the active turn, got %d", pendingSteers)
	}
	held, err := srv.loadHeldUserTurns(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 3 || held[0].id != "guide-1" || held[1].id != "queued-1" || held[2].id != "queued-2" {
		t.Fatalf("interrupt did not preserve steer-before-queue order: %+v", held)
	}
	if held[0].origin != session.HeldUserWorkOriginSteer || held[1].origin != session.HeldUserWorkOriginQueue {
		t.Fatalf("held origins were not preserved: %+v", held)
	}
	if held[0].snapshot.ActiveDocument == nil || held[0].snapshot.ActiveDocument.Path != "docs/guide.md" {
		t.Fatalf("held steer lost active document: %+v", held[0])
	}

	waitForMethod(t, out, NotificationTurnError)

	resumeReq := fmt.Sprintf(`{"id":"5b","method":"thread/resume","params":{"session_id":%q}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(resumeReq)); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}
	resumed := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, out.String()), "5b")["result"])
	if len(resumed.HeldUserMessages) != 3 || resumed.HeldUserMessages[0].ID != "guide-1" || resumed.HeldUserMessages[2].ID != "queued-2" {
		t.Fatalf("resume did not return held messages in order: %+v", resumed.HeldUserMessages)
	}
	if resumed.HeldUserMessages[0].ActiveDocument == nil || resumed.HeldUserMessages[0].ActiveDocument.Path != "docs/guide.md" {
		t.Fatalf("resume lost held steer active document: %+v", resumed.HeldUserMessages[0])
	}

	releaseReq := fmt.Sprintf(`{"id":"5c","method":"turn/steer","params":{"thread_id":%q,"prompt":"queued follow-up","client_id":"queued-1"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(releaseReq)); err != nil {
		t.Fatalf("idle held turn/steer: %v", err)
	}
	releaseResponse := responseByID(t, parseOutput(t, out.String()), "5c")
	if releaseResponse["error"] != nil {
		t.Fatalf("idle held turn/steer returned error: %+v", releaseResponse["error"])
	}
	held, err = srv.loadHeldUserTurns(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 2 || held[0].id != "guide-1" || held[1].id != "queued-2" {
		t.Fatalf("releasing queued-1 changed other held messages: %+v", held)
	}
	close(client.release)
	waitForTurnCompletedCountForThread(t, out, threadID, 1)
	held, err = srv.loadHeldUserTurns(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 2 {
		t.Fatalf("held messages auto-drained after released turn completed: %+v", held)
	}

	target := started.Turn.Items[0]
	editReq := fmt.Sprintf(`{"id":"6","method":"thread/edit-message","params":{"thread_id":%q,"turn_id":%q,"item_id":%q}}`, threadID, started.Turn.ID, target.ID)
	if err := srv.handleLine(context.Background(), []byte(editReq)); err != nil {
		t.Fatalf("thread/edit-message: %v", err)
	}
	editResponse := responseByID(t, parseOutput(t, out.String()), "6")
	if editResponse["error"] != nil {
		t.Fatalf("thread/edit-message returned error after interrupt: %+v", editResponse["error"])
	}
}

func TestServerInterruptPersistsPartialTurnMessages(t *testing.T) {
	// D3: an interrupted turn must persist the assistant tool_call and the
	// synthesized aborted tool result the loop already produced, not drop them
	// to a usage-only record. Otherwise the work the user saw on screen
	// vanishes on reload and the disk/memory histories diverge.
	client := &fakeClient{
		responses: []providers.ChatResponse{
			{ToolCalls: []providers.ToolCall{{ID: "call_1", Name: "wait_for_steer", Arguments: `{}`}}},
			{Content: "unreachable"},
		},
	}
	rt := newTestRuntime(t, client)
	blockingTool := newBlockingToolExecutor()
	rt.StreamRunner.Tools = blockingTool
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	startReq := fmt.Sprintf(`{"id":"2","method":"turn/start","params":{"thread_id":%q,"prompt":"please"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(startReq)); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	// The tool is now running; interrupt cancels it mid-flight.
	<-blockingTool.started
	interruptReq := fmt.Sprintf(`{"id":"3","method":"turn/interrupt","params":{"thread_id":%q}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(interruptReq)); err != nil {
		t.Fatalf("turn/interrupt: %v", err)
	}
	waitForMethod(t, out, NotificationTurnError)

	persisted, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("load persisted history: %v", err)
	}
	var hasUser, hasAssistantToolCall, hasToolResult bool
	for _, msg := range persisted {
		switch msg.Role {
		case "user":
			if msg.Content == "please" {
				hasUser = true
			}
		case "assistant":
			for _, tc := range msg.ToolCalls {
				if tc.ID == "call_1" && tc.Name == "wait_for_steer" {
					hasAssistantToolCall = true
				}
			}
		case "tool":
			if msg.ToolCallID == "call_1" {
				hasToolResult = true
			}
		}
	}
	if !hasUser || !hasAssistantToolCall || !hasToolResult {
		t.Fatalf("interrupted turn dropped partial messages: user=%v assistant_toolcall=%v tool_result=%v; persisted=%+v",
			hasUser, hasAssistantToolCall, hasToolResult, persisted)
	}

	// Reload-time repair must be idempotent: the persisted pair is already
	// complete, so a second load returns the same validated history.
	reloaded, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("reload persisted history: %v", err)
	}
	if len(reloaded) != len(persisted) {
		t.Fatalf("reload changed message count: %d vs %d", len(reloaded), len(persisted))
	}
}

func TestServerSteerWaitsForOrdinaryToolSafeBoundary(t *testing.T) {
	client := &fakeClient{
		responses: []providers.ChatResponse{
			{
				ToolCalls: []providers.ToolCall{{
					ID:        "call_1",
					Name:      "wait_for_steer",
					Arguments: `{}`,
				}},
			},
			{Content: "done after steer"},
		},
	}
	rt := newTestRuntime(t, client)
	blockingTool := newBlockingToolExecutor()
	rt.StreamRunner.Tools = blockingTool
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	startReq := fmt.Sprintf(`{"id":"2","method":"turn/start","params":{"thread_id":%q,"prompt":"start"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(startReq)); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	started := remarshal[TurnStartResult](t, responseByID(t, parseOutput(t, out.String()), "2")["result"])

	<-blockingTool.started
	badSteerReq := fmt.Sprintf(`{"id":"bad-steer","method":"turn/steer","params":{"thread_id":%q,"expected_turn_id":"wrong-turn","prompt":"wrong"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(badSteerReq)); err != nil {
		t.Fatalf("bad turn/steer: %v", err)
	}
	badSteerResp := responseByID(t, parseOutput(t, out.String()), "bad-steer")
	if badSteerResp["error"] == nil {
		t.Fatalf("expected steer mismatch error, got %+v", badSteerResp)
	}

	steerReq := fmt.Sprintf(`{"id":"3","method":"turn/steer","params":{"thread_id":%q,"expected_turn_id":%q,"prompt":"steer now","client_id":"steer-1"}}`, threadID, started.Turn.ID)
	if err := srv.handleLine(context.Background(), []byte(steerReq)); err != nil {
		t.Fatalf("turn/steer: %v", err)
	}
	steerResult := remarshal[TurnSteerResult](t, responseByID(t, parseOutput(t, out.String()), "3")["result"])
	if steerResult.TurnID != started.Turn.ID {
		t.Fatalf("unexpected steer result: %+v", steerResult)
	}
	time.Sleep(50 * time.Millisecond)
	for _, msg := range parseOutput(t, out.String()) {
		if msg["method"] == NotificationTurnCompleted {
			t.Fatal("steer canceled an ordinary blocking tool before its safe boundary")
		}
	}
	close(blockingTool.release)

	msgs := waitForMethod(t, out, NotificationTurnCompleted)
	completed := remarshal[TurnCompletedNotification](t, notificationByMethodForThread(t, msgs, NotificationTurnCompleted, threadID)["params"])
	var foundItem bool
	for _, item := range completed.Turn.Items {
		if item.Type == ThreadItemUserMessage && item.Text == "steer now" && item.SourceID == "steer-1" {
			foundItem = true
			break
		}
	}
	if !foundItem {
		t.Fatalf("completed turn missing steer item: %+v", completed.Turn.Items)
	}

	client.mu.Lock()
	requests := append([]providers.ChatRequest(nil), client.requests...)
	client.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected two provider requests, got %d", len(requests))
	}
	var foundSteerInSecondRequest bool
	for _, msg := range requests[1].Messages {
		if msg.Role == "user" && msg.Content == "steer now" && msg.ClientID == "steer-1" && msg.Steered {
			foundSteerInSecondRequest = true
			break
		}
	}
	if !foundSteerInSecondRequest {
		t.Fatalf("second provider request missing steer: %+v", requests[1].Messages)
	}
	var foundCompletedToolResult bool
	for _, msg := range requests[1].Messages {
		if msg.Role == "tool" && msg.ToolCallID == "call_1" && msg.Content == `{"ok":true}` {
			foundCompletedToolResult = true
			break
		}
	}
	if !foundCompletedToolResult {
		t.Fatalf("second provider request missing completed tool result: %+v", requests[1].Messages)
	}

	persisted, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("load persisted history: %v", err)
	}
	var foundPersisted bool
	for _, msg := range persisted {
		if msg.Role == "user" && msg.Content == "steer now" && msg.ClientID == "steer-1" && msg.Steered {
			foundPersisted = true
			break
		}
	}
	if !foundPersisted {
		t.Fatalf("persisted history missing steered input: %+v", persisted)
	}
}

func TestServerSteerReleasesDetachableToolWait(t *testing.T) {
	client := &fakeClient{
		responses: []providers.ChatResponse{
			{
				ToolCalls: []providers.ToolCall{{
					ID:        "call_1",
					Name:      "wait_in_background",
					Arguments: `{}`,
				}},
			},
			{Content: "done after steer"},
		},
	}
	rt := newTestRuntime(t, client)
	detachableTool := newDetachableWaitToolExecutor()
	rt.StreamRunner.Tools = detachableTool
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	startReq := fmt.Sprintf(`{"id":"2","method":"turn/start","params":{"thread_id":%q,"prompt":"start"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(startReq)); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	started := remarshal[TurnStartResult](t, responseByID(t, parseOutput(t, out.String()), "2")["result"])

	<-detachableTool.started
	steerReq := fmt.Sprintf(`{"id":"3","method":"turn/steer","params":{"thread_id":%q,"expected_turn_id":%q,"prompt":"steer now","client_id":"steer-1"}}`, threadID, started.Turn.ID)
	if err := srv.handleLine(context.Background(), []byte(steerReq)); err != nil {
		t.Fatalf("turn/steer: %v", err)
	}
	waitForMethod(t, out, NotificationTurnCompleted)

	client.mu.Lock()
	requests := append([]providers.ChatRequest(nil), client.requests...)
	client.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected two provider requests, got %d", len(requests))
	}
	var foundSteer, foundBackgroundedResult bool
	for _, msg := range requests[1].Messages {
		if msg.Role == "user" && msg.Content == "steer now" && msg.ClientID == "steer-1" && msg.Steered {
			foundSteer = true
		}
		if msg.Role == "tool" && msg.ToolCallID == "call_1" && strings.Contains(msg.Content, `"backgrounded":true`) {
			foundBackgroundedResult = true
		}
	}
	if !foundSteer || !foundBackgroundedResult {
		t.Fatalf("second provider request missing steer or backgrounded tool result: %+v", requests[1].Messages)
	}
}

func TestServerGeneratesThreadTitle(t *testing.T) {
	mainClient := &fakeClient{response: providers.ChatResponse{Content: "done"}}
	titleClient := &fakeClient{response: providers.ChatResponse{Content: "<think>ignore</think>\nFix login crash"}}
	rt := newTestRuntime(t, mainClient)
	rt.TitleClient = titleClient
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	payload := map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "please help me fix the login crash in auth.ts"},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	msgs := waitForMethod(t, out, NotificationThreadUpdated)
	updated := remarshal[ThreadUpdatedNotification](t, notificationByMethod(t, msgs, NotificationThreadUpdated)["params"])
	if updated.Thread.ID != threadID || updated.Thread.Preview != "Fix login crash" {
		t.Fatalf("unexpected title update: %+v", updated)
	}
	sessions, err := session.List(rt.SessionDir, 1)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Title != "Fix login crash" || sessions[0].Summary != "please help me fix the login crash in auth.ts" {
		t.Fatalf("unexpected persisted title: %+v", sessions)
	}

	mainClient.mu.Lock()
	mainRequests := len(mainClient.requests)
	mainClient.mu.Unlock()
	titleClient.mu.Lock()
	titleRequests := len(titleClient.requests)
	titleClient.mu.Unlock()
	if mainRequests != 1 || titleRequests != 1 {
		t.Fatalf("unexpected request counts: main=%d title=%d", mainRequests, titleRequests)
	}
}

func TestServerGeneratesThreadTitleFromFirstTurnSnapshot(t *testing.T) {
	titleClient := &fakeClient{response: providers.ChatResponse{Content: "First task title"}}
	rt := newTestRuntime(t, &fakeClient{})
	rt.TitleClient = titleClient
	out := &lockedBuffer{}
	srv := New(rt, out)

	sess, err := session.CreateWithMetadata(rt.SessionDir, "snapshot-title-thread", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	firstTurnHistory := []providers.ChatMessage{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: "done"},
	}
	currentHistory := append(cloneHistory(firstTurnHistory), providers.ChatMessage{Role: "user", Content: "second task"})
	th := newThreadState(sess.ID, currentHistory, rt.ProviderName, rt.Model, rt.RootDir, true, time.Now().UTC())
	srv.mu.Lock()
	srv.threads[th.ID] = th
	srv.mu.Unlock()

	srv.generateThreadTitle(th.ID, firstTurnHistory, nil)

	sessions, err := session.List(rt.SessionDir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Title != "First task title" {
		t.Fatalf("unexpected generated title: %+v", sessions)
	}
	titleClient.mu.Lock()
	defer titleClient.mu.Unlock()
	if len(titleClient.requests) != 1 {
		t.Fatalf("expected one title request, got %d", len(titleClient.requests))
	}
	prompt := titleClient.requests[0].Messages[len(titleClient.requests[0].Messages)-1].Content
	if !strings.Contains(prompt, "first task") || strings.Contains(prompt, "second task") {
		t.Fatalf("title prompt should use first-turn snapshot, got %q", prompt)
	}
}

func TestServerGeneratesThreadTitleUsesTitleRoleSelection(t *testing.T) {
	titleClient := &fakeClient{response: providers.ChatResponse{Content: "Role title"}}
	rt := newTestRuntime(t, &fakeClient{})
	rt.TitleClient = titleClient
	cfg := config.Config{
		DefaultProvider: "fake-provider",
		Providers: map[string]config.ProviderConfig{
			"fake-provider": {
				Type:    "openai-compatible",
				BaseURL: "https://example.test/v1",
				Model:   "fake-model",
			},
		},
		Agent: config.AgentConfig{
			ModelRoles: config.ModelRolesConfig{
				Title: config.ModelRoleConfig{Model: "gpt-4.1-mini", Effort: "high"},
			},
		},
	}
	roles, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{})
	if err != nil {
		t.Fatalf("modelroles.Resolve: %v", err)
	}
	rt.ModelRoles = roles
	srv := New(rt, &lockedBuffer{})

	result, err := srv.generateThreadTitleCore("title-role-thread", []providers.ChatMessage{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "make the title role explicit"},
	}, false, false, true, nil)
	if err != nil {
		t.Fatalf("generateThreadTitleCore: %v", err)
	}
	if result.Model != "gpt-4.1-mini" || result.CleanedTitle != "Role title" {
		t.Fatalf("unexpected title result: %+v", result)
	}
	titleClient.mu.Lock()
	defer titleClient.mu.Unlock()
	if len(titleClient.requests) != 1 {
		t.Fatalf("expected one title request, got %d", len(titleClient.requests))
	}
	if req := titleClient.requests[0]; req.Model != "gpt-4.1-mini" || req.Effort != "high" {
		t.Fatalf("title request did not use title role: %+v", req)
	}
}

// When the title role inherits the main model, the title must follow the
// conversation's pinned model — not the workspace default, which drifts to
// another session's model after a switch. The thread runtime's client serves
// the request; the workspace title client stays untouched.
func TestServerGeneratesThreadTitleInheritsPinnedThreadModel(t *testing.T) {
	workspaceTitleClient := &fakeClient{response: providers.ChatResponse{Content: "Workspace title"}}
	rt := newTestRuntime(t, &fakeClient{})
	rt.TitleClient = workspaceTitleClient
	rt.ModelRoles = modelroles.Set{
		Title: modelroles.Selection{Inherited: true, Model: "fake-model", APIModel: "fake-model"},
	}
	srv := New(rt, &lockedBuffer{})

	threadClient := &fakeClient{response: providers.ChatResponse{Content: "Thread title"}}
	threadRuntime := &runtime.ThreadRuntime{
		StreamRunner: &agent.StreamRunner{
			Client:   providers.AdaptStreamClient(threadClient),
			Model:    "session-pinned-model",
			APIModel: "session-pinned-model",
		},
	}

	result, err := srv.generateThreadTitleCore("inherit-thread", []providers.ChatMessage{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "pin the session model"},
	}, false, false, true, threadRuntime)
	if err != nil {
		t.Fatalf("generateThreadTitleCore: %v", err)
	}
	if result.Model != "session-pinned-model" || result.CleanedTitle != "Thread title" {
		t.Fatalf("unexpected title result: %+v", result)
	}

	threadClient.mu.Lock()
	threadReqs := len(threadClient.requests)
	gotModel := ""
	if threadReqs == 1 {
		gotModel = threadClient.requests[0].Model
	}
	threadClient.mu.Unlock()
	if threadReqs != 1 || gotModel != "session-pinned-model" {
		t.Fatalf("thread client requests = %d model=%q, want 1 for session-pinned-model", threadReqs, gotModel)
	}

	workspaceTitleClient.mu.Lock()
	wsReqs := len(workspaceTitleClient.requests)
	workspaceTitleClient.mu.Unlock()
	if wsReqs != 0 {
		t.Fatalf("workspace title client received %d requests, want 0", wsReqs)
	}
}

// TestServerGeneratesThreadTitleEndToEndWithStreaming exercises the full
// pipeline with a real streaming title client (not a fakeClient wrapped by
// AdaptStreamClient). It mirrors what production looks like for kimi-k2.6 —
// a provider that REQUIRES streaming and has a pinned temperature — and
// verifies:
//
//   - the title request actually went through StreamChat (not Chat)
//   - thinking deltas were ignored, content deltas were aggregated
//   - temperature matches the per-model mapping
//   - the persisted title and the thread/updated notification Preview carry
//     the cleaned title
//   - the main client received exactly one non-stream chat for the agent
//     loop and the title client received exactly one stream chat
func TestServerGeneratesThreadTitleEndToEndWithStreaming(t *testing.T) {
	t.Parallel()
	mainClient := &scriptedStreamClient{chunks: []string{"d", "one"}}
	titleClient := &scriptedStreamClient{
		prefix: "let me think about a good title\n",
		chunks: []string{"Fix ", "login ", "crash"},
	}
	rt := &runtime.Session{
		ProviderName: "fake-provider",
		Model:        "kimi-k2.6",
		RootDir:      t.TempDir(),
		ConfigPath:   "/tmp/.wuu.json",
		SessionDir:   t.TempDir() + "/.wuu-state/sessions",
		StreamRunner: &agent.StreamRunner{
			Client:       providers.AdaptStreamClient(mainClient),
			Model:        "kimi-k2.6",
			SystemPrompt: "system prompt",
		},
		TitleClient: titleClient,
	}
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	payload := map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "please help me fix the login crash in auth.ts"},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	msgs := waitForMethod(t, out, NotificationThreadUpdated)
	updated := remarshal[ThreadUpdatedNotification](t, notificationByMethod(t, msgs, NotificationThreadUpdated)["params"])
	if updated.Thread.ID != threadID {
		t.Fatalf("notification thread id = %q; want %q", updated.Thread.ID, threadID)
	}
	if updated.Thread.Preview != "Fix login crash" {
		t.Fatalf("notification Preview = %q; want %q", updated.Thread.Preview, "Fix login crash")
	}
	// Summary (the raw first user prompt) must be preserved — the title
	// model only writes to Title, never to Summary.
	if updated.Thread.Preview == "please help me fix the login crash in auth.ts" {
		t.Fatal("Preview should have been replaced by the LLM title, not the raw user prompt")
	}

	sessions, err := session.List(rt.SessionDir, 1)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Title != "Fix login crash" {
		t.Fatalf("persisted Title = %q; want %q", sessions[0].Title, "Fix login crash")
	}
	if sessions[0].Summary != "please help me fix the login crash in auth.ts" {
		t.Fatalf("persisted Summary = %q; want raw user prompt preserved", sessions[0].Summary)
	}

	titleClient.mu.Lock()
	defer titleClient.mu.Unlock()
	if len(titleClient.requests) != 1 {
		t.Fatalf("expected exactly 1 title request, got %d", len(titleClient.requests))
	}
	req := titleClient.requests[0]
	if req.Model != "kimi-k2.6" {
		t.Errorf("title request model = %q; want kimi-k2.6", req.Model)
	}
	if req.Temperature != 1.0 {
		t.Errorf("title request Temperature = %v; want 1.0 for kimi-k2.6", req.Temperature)
	}
	if len(req.Messages) < 2 {
		t.Fatalf("title request must have at least system+user messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || !strings.Contains(req.Messages[0].Content, "title generator") {
		t.Errorf("title system prompt not aligned with opencode: %q", req.Messages[0].Content)
	}
	if req.Messages[1].Role != "user" || !strings.Contains(req.Messages[1].Content, "please help me fix the login crash") {
		t.Errorf("title user message wrong: %q", req.Messages[1].Content)
	}
}

// TestServerRegenerateTitle exercises the thread/regenerate-title JSON-RPC
// method end-to-end: a thread that already has multiple turns (so the
// first-turn auto title gen is skipped) can still be re-titled by hand.
// Verifies:
//
//   - dry-run=true returns the cleaned title but does not persist or notify
//   - dry-run=false persists the title and fires a thread/updated
//     notification with the new Preview
//   - the response surfaces every TitleGenerationResult field
func TestServerRegenerateTitle(t *testing.T) {
	mainClient := &fakeClient{response: providers.ChatResponse{Content: "ok"}}
	titleClient := &scriptedStreamClient{
		prefix: "let me think…\n",
		chunks: []string{"Refactor ", "user ", "service"},
	}
	rt := newTestRuntime(t, mainClient)
	rt.TitleClient = titleClient
	out := &lockedBuffer{}
	srv := New(rt, out)

	// Seed an existing thread that is BEYOND its first turn. The first-turn
	// auto title gen would skip this because history has > 1 user message;
	// the explicit regenerate method must still work.
	sess, err := session.CreateWithMetadata(rt.SessionDir, "regen-thread-1", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	history := []providers.ChatMessage{
		{Role: "user", Content: "first task prompt"},
		{Role: "assistant", Content: "first task answer"},
		{Role: "user", Content: "second task prompt"},
		{Role: "assistant", Content: "second task answer"},
	}
	if err := session.UpdateIndex(rt.SessionDir, sess.ID, len(history), "first task prompt"); err != nil {
		t.Fatal(err)
	}
	if err := rewriteChatHistory(rt.SessionDir, sess.ID, history); err != nil {
		t.Fatal(err)
	}

	// dry-run path: returns the cleaned title without persisting or notifying.
	dryParams, _ := json.Marshal(ThreadRegenerateTitleParams{
		ThreadID: sess.ID,
		DryRun:   true,
	})
	dryReq := []byte(fmt.Sprintf(`{"id":"reg-1","method":%q,"params":%s}`, MethodThreadRegenerateTitle, dryParams))
	if err := srv.handleLine(context.Background(), dryReq); err != nil {
		t.Fatalf("regenerate-title dry-run: %v", err)
	}
	// The handler runs on a background goroutine; wait for its response
	// before asserting anything about persistence.
	waitForResponseByID(t, out, "reg-1")
	// Verify title was NOT persisted.
	if _, ok, _ := session.Find(rt.SessionDir, sess.ID); !ok {
		t.Fatal("session should still exist")
	}
	persisted, _ := session.List(rt.SessionDir, 100)
	for _, p := range persisted {
		if p.ID == sess.ID && p.Title != "" {
			t.Fatalf("dry-run must not persist, got Title=%q", p.Title)
		}
	}

	// Persist path: re-issue without dry-run.
	persistParams, _ := json.Marshal(ThreadRegenerateTitleParams{
		ThreadID: sess.ID,
		DryRun:   false,
	})
	persistReq := []byte(fmt.Sprintf(`{"id":"reg-2","method":%q,"params":%s}`, MethodThreadRegenerateTitle, persistParams))
	if err := srv.handleLine(context.Background(), persistReq); err != nil {
		t.Fatalf("regenerate-title persist: %v", err)
	}
	waitForResponseByID(t, out, "reg-2")
	persisted2, _ := session.List(rt.SessionDir, 100)
	var updated *session.Session
	for i := range persisted2 {
		if persisted2[i].ID == sess.ID {
			updated = &persisted2[i]
		}
	}
	if updated == nil {
		t.Fatal("session missing after persist")
	}
	if updated.Title != "Refactor user service" {
		t.Fatalf("persisted Title = %q; want %q", updated.Title, "Refactor user service")
	}
	if updated.Summary != "first task prompt" {
		t.Fatalf("Summary must be preserved, got %q", updated.Summary)
	}
}

func TestServerThreadForkAtAssistantItem(t *testing.T) {
	client := &fakeClient{
		responses: []providers.ChatResponse{
			{Content: "first answer"},
			{Content: "second answer"},
		},
	}
	rt := newTestRuntime(t, client)
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	startTurn := func(id, prompt string, completedCount int) Turn {
		t.Helper()
		payload := map[string]any{
			"id":     id,
			"method": MethodTurnStart,
			"params": TurnStartParams{ThreadID: threadID, Prompt: prompt},
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal turn request: %v", err)
		}
		if err := srv.handleLine(context.Background(), raw); err != nil {
			t.Fatalf("turn/start: %v", err)
		}
		msgs := waitForNotificationCount(t, out, NotificationTurnCompleted, completedCount)
		completed := notificationsByMethod(msgs, NotificationTurnCompleted)
		return remarshal[TurnCompletedNotification](t, completed[len(completed)-1]["params"]).Turn
	}

	firstTurn := startTurn("2", "first prompt", 1)
	var firstAgentItem ThreadItem
	for _, item := range firstTurn.Items {
		if item.Type == ThreadItemAgentMessage {
			firstAgentItem = item
			break
		}
	}
	if firstAgentItem.ID == "" {
		t.Fatalf("expected first turn to contain assistant item: %+v", firstTurn)
	}
	_ = startTurn("3", "second prompt", 2)

	payload := map[string]any{
		"id":     "4",
		"method": MethodThreadFork,
		"params": ThreadForkParams{
			ThreadID: threadID,
			TurnID:   firstTurn.ID,
			ItemID:   firstAgentItem.ID,
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal fork request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("thread/fork: %v", err)
	}

	msgs := parseOutput(t, out.String())
	forkResponse := responseByID(t, msgs, "4")
	if forkResponse["error"] != nil {
		t.Fatalf("thread/fork returned error: %+v", forkResponse["error"])
	}
	result := remarshal[ThreadForkResult](t, forkResponse["result"])
	fork := result.Thread
	if fork.ID == "" || fork.ID == threadID {
		t.Fatalf("expected new fork thread id, got %+v", fork)
	}
	if fork.ForkedFromID != threadID || fork.ForkedFromTurnID != firstTurn.ID || fork.ForkedFromItemID != firstAgentItem.ID {
		t.Fatalf("fork metadata not returned: %+v", fork)
	}
	if len(fork.Turns) != 1 || len(fork.Turns[0].Items) != 2 {
		t.Fatalf("expected fork to stop at first assistant item, got %+v", fork.Turns)
	}
	if fork.Turns[0].Items[0].Text != "first prompt" || fork.Turns[0].Items[1].Text != "first answer" {
		t.Fatalf("unexpected fork turn items: %+v", fork.Turns[0].Items)
	}

	forkHistory, err := loadChatMessages(rt.SessionDir, fork.ID)
	if err != nil {
		t.Fatalf("load fork history: %v", err)
	}
	visibleForkHistory := visibleMessagesForTest(forkHistory)
	if len(visibleForkHistory) != 2 || visibleForkHistory[0].Content != "first prompt" || visibleForkHistory[1].Content != "first answer" {
		t.Fatalf("unexpected persisted fork history: %+v", forkHistory)
	}
	sourceHistory, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("load source history: %v", err)
	}
	if len(visibleMessagesForTest(sourceHistory)) != 4 {
		t.Fatalf("source history should remain intact, got %+v", sourceHistory)
	}

	metadata, ok, err := session.Find(rt.SessionDir, fork.ID)
	if err != nil {
		t.Fatalf("find fork metadata: %v", err)
	}
	if !ok || metadata.ForkedFromID != threadID || metadata.ForkedFromTurnID != firstTurn.ID || metadata.ForkedFromItemID != firstAgentItem.ID {
		t.Fatalf("fork metadata not persisted: ok=%v metadata=%+v", ok, metadata)
	}
	started := notificationsByMethod(msgs, NotificationThreadStarted)
	if len(started) < 2 {
		t.Fatalf("expected fork to emit thread/started notification, got %+v", msgs)
	}
	forkStarted := remarshal[ThreadStartedNotification](t, started[len(started)-1]["params"])
	if forkStarted.Thread.ID != fork.ID {
		t.Fatalf("unexpected fork started notification: %+v", forkStarted)
	}
}

func TestServerThreadForkDoesNotReuseSourceToolInvocationOwnership(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := session.CreateWithMetadata(rt.SessionDir, "20260718-000000-fork-tool-ledger", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	db, err := session.OpenStore(rt.SessionDir)
	if err != nil {
		t.Fatalf("open session store: %v", err)
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := db.Exec(`
INSERT INTO tool_batches (id, owner_id, operation_id, step_index, status, created_at, updated_at, terminal_at)
VALUES ('fork-source-batch', ?, 'fork-source-operation', 1, 'settled', ?, ?, ?)`, sess.ID, now, now, now); err != nil {
		db.Close()
		t.Fatalf("insert source tool batch: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO tool_invocations (
  id, batch_id, provider_call_id, tool_name, arguments_json, replay_policy, state,
  result_json, prepared_at, running_at, settled_at
) VALUES ('fork-source-invocation', 'fork-source-batch', 'fork-source-call', 'read_file', '{}', 'at_most_once', 'succeeded', '{}', ?, ?, ?)`, now, now, now); err != nil {
		db.Close()
		t.Fatalf("insert source tool invocation: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close session store: %v", err)
	}

	history := []providers.ChatMessage{
		{Role: "user", Content: "read the file"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID: "fork-source-call", Name: "read_file", Arguments: `{"path":"README.md"}`,
			}},
		},
		{
			Role: "tool", ToolCallID: "fork-source-call", ToolInvocationID: "fork-source-invocation",
			Name: "read_file", Content: "contents",
		},
		{Role: "assistant", Content: "file read"},
		{Role: "user", Content: "continue from here"},
	}
	if err := rewriteChatHistory(rt.SessionDir, sess.ID, history); err != nil {
		t.Fatalf("write source history: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	resumeReq := fmt.Sprintf(`{"id":"resume-tool-fork","method":%q,"params":{"session_id":%q}}`, MethodThreadResume, sess.ID)
	if err := srv.handleLine(context.Background(), []byte(resumeReq)); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}
	resumed := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, out.String()), "resume-tool-fork")["result"])
	var targetTurnID, targetItemID string
	for _, turn := range resumed.Thread.Turns {
		for _, item := range turn.Items {
			if item.Type == ThreadItemUserMessage && item.Text == "continue from here" {
				targetTurnID = turn.ID
				targetItemID = item.ID
			}
		}
	}
	if targetItemID == "" {
		t.Fatalf("fork target not found in resumed thread: %+v", resumed.Thread.Turns)
	}

	forkPayload, err := json.Marshal(map[string]any{
		"id":     "fork-with-tool-ledger",
		"method": MethodThreadFork,
		"params": ThreadForkParams{
			ThreadID: sess.ID,
			TurnID:   targetTurnID,
			ItemID:   targetItemID,
			Mode:     "local",
		},
	})
	if err != nil {
		t.Fatalf("marshal fork request: %v", err)
	}
	if err := srv.handleLine(context.Background(), forkPayload); err != nil {
		t.Fatalf("thread/fork: %v", err)
	}
	forkResponse := responseByID(t, parseOutput(t, out.String()), "fork-with-tool-ledger")
	if forkResponse["error"] != nil {
		t.Fatalf("thread/fork returned error: %+v", forkResponse["error"])
	}
	fork := remarshal[ThreadForkResult](t, forkResponse["result"]).Thread
	forkHistory, err := loadChatMessages(rt.SessionDir, fork.ID)
	if err != nil {
		t.Fatalf("load fork history: %v", err)
	}
	for _, message := range forkHistory {
		if message.ToolInvocationID != "" {
			t.Fatalf("fork retained source tool invocation ownership: %+v", forkHistory)
		}
	}
}

func TestServerThreadForkToWorktree(t *testing.T) {
	client := &fakeClient{
		responses: []providers.ChatResponse{
			{Content: "first answer"},
		},
	}
	rt := newTestRuntime(t, client)
	initAppserverGitRepo(t, rt.RootDir)
	stateDir := filepath.Join(rt.RootDir, ".wuu", "state")
	rt.StateDir = stateDir
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	kit.SetStateDir(stateDir)
	rt.Toolkit = kit
	rt.StreamRunner.Tools = kit
	manager, err := process.NewManager(rt.RootDir, filepath.Join(stateDir, "runtime"))
	if err != nil {
		t.Fatalf("process.NewManager: %v", err)
	}
	rt.ProcessManager = manager

	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	startReq, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "first prompt"},
	})
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), startReq); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, waitForMethod(t, out, NotificationTurnCompleted), NotificationTurnCompleted)["params"])

	forkReq, err := json.Marshal(map[string]any{
		"id":     "3",
		"method": MethodThreadFork,
		"params": ThreadForkParams{
			ThreadID: threadID,
			TurnID:   completed.Turn.ID,
			Mode:     "worktree",
		},
	})
	if err != nil {
		t.Fatalf("marshal fork request: %v", err)
	}
	if err := srv.handleLine(context.Background(), forkReq); err != nil {
		t.Fatalf("thread/fork: %v", err)
	}

	msgs := parseOutput(t, out.String())
	response := responseByID(t, msgs, "3")
	if response["error"] != nil {
		t.Fatalf("thread/fork returned error: %+v", response["error"])
	}
	result := remarshal[ThreadForkResult](t, response["result"])
	fork := result.Thread
	if result.Worktree == nil || fork.Worktree == nil {
		t.Fatalf("expected worktree info in fork result: %+v", result)
	}
	if fork.CWD == rt.RootDir || fork.CWD != result.Worktree.Path {
		t.Fatalf("fork should run from worktree cwd, got fork=%+v worktree=%+v", fork, result.Worktree)
	}
	if result.Worktree.BaseHEAD == "" || result.Worktree.BaseRepo != rt.RootDir {
		t.Fatalf("unexpected worktree base info: %+v", result.Worktree)
	}
	if _, err := os.Stat(filepath.Join(result.Worktree.Path, ".git")); err != nil {
		t.Fatalf("worktree .git missing: %v", err)
	}

	metadata, ok, err := session.Find(rt.SessionDir, fork.ID)
	if err != nil {
		t.Fatalf("find fork metadata: %v", err)
	}
	if !ok || metadata.CWD != result.Worktree.Path || metadata.WorktreePath != result.Worktree.Path || metadata.WorktreeBaseRepo != rt.RootDir {
		t.Fatalf("worktree metadata not persisted: ok=%v metadata=%+v", ok, metadata)
	}

	listReq := []byte(`{"id":"4","method":"thread/list"}`)
	if err := srv.handleLine(context.Background(), listReq); err != nil {
		t.Fatalf("thread/list: %v", err)
	}
	list := remarshal[ThreadListResult](t, responseByID(t, parseOutput(t, out.String()), "4")["result"])
	var listedFork *Thread
	for i := range list.Threads {
		if list.Threads[i].ID == fork.ID {
			listedFork = &list.Threads[i]
			break
		}
	}
	if listedFork == nil || listedFork.Worktree == nil {
		t.Fatalf("thread/list should include worktree fork under parent repo, got %+v", list.Threads)
	}

	threadRuntime, err := srv.ensureThreadRuntime(srv.thread(fork.ID))
	if err != nil {
		t.Fatalf("ensureThreadRuntime: %v", err)
	}
	if threadRuntime.Toolkit == nil {
		t.Fatal("expected toolkit on fork runtime")
	}
	if _, err := threadRuntime.Toolkit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"isolated.txt","content":"worktree only\n"}`,
	}); err != nil {
		t.Fatalf("write_file in worktree runtime: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.Worktree.Path, "isolated.txt")); err != nil {
		t.Fatalf("expected file in worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rt.RootDir, "isolated.txt")); !os.IsNotExist(err) {
		t.Fatalf("parent repo should not contain isolated file, stat err=%v", err)
	}

	if err := srv.handleLine(context.Background(), []byte(`{"id":"5","method":"thread/list"}`)); err != nil {
		t.Fatalf("thread/list after worktree write: %v", err)
	}
	afterWrite := remarshal[ThreadListResult](t, responseByID(t, parseOutput(t, out.String()), "5")["result"])
	var dirtyInfo *WorktreeInfo
	for i := range afterWrite.Threads {
		if afterWrite.Threads[i].ID == fork.ID {
			dirtyInfo = afterWrite.Threads[i].Worktree
			break
		}
	}
	if dirtyInfo == nil || !dirtyInfo.Dirty || !containsTestString(dirtyInfo.ChangedFiles, "isolated.txt") {
		t.Fatalf("thread/list should report dirty worktree, got %+v", dirtyInfo)
	}
}

// TestServerWorktreeBoundTurnExecutesInWorktree covers fork-to-worktree step
// 5: the turn entry injects the session's persisted worktree binding into the
// tool execution context, so even when a worktree-bound thread's runtime ends
// up rooted at the parent repo (CWD drift), file tools physically execute
// inside the isolated checkout — never the parent repo the user believes is
// protected.
func TestServerWorktreeBoundTurnExecutesInWorktree(t *testing.T) {
	client := &fakeClient{
		responses: []providers.ChatResponse{
			{Content: "first answer"},
			{ToolCalls: []providers.ToolCall{{
				ID:        "call-worktree-write",
				Name:      "write_file",
				Arguments: `{"path":"ctx-isolated.txt","content":"worktree only\n"}`,
			}}},
			{Content: "wrote in worktree"},
		},
	}
	rt := newTestRuntime(t, client)
	initAppserverGitRepo(t, rt.RootDir)
	stateDir := filepath.Join(rt.RootDir, ".wuu", "state")
	rt.StateDir = stateDir
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	kit.SetStateDir(stateDir)
	rt.Toolkit = kit
	rt.StreamRunner.Tools = kit
	manager, err := process.NewManager(rt.RootDir, filepath.Join(stateDir, "runtime"))
	if err != nil {
		t.Fatalf("process.NewManager: %v", err)
	}
	rt.ProcessManager = manager

	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	startReq, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "first prompt"},
	})
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), startReq); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, waitForMethod(t, out, NotificationTurnCompleted), NotificationTurnCompleted)["params"])

	forkReq, err := json.Marshal(map[string]any{
		"id":     "3",
		"method": MethodThreadFork,
		"params": ThreadForkParams{
			ThreadID: threadID,
			TurnID:   completed.Turn.ID,
			Mode:     "worktree",
		},
	})
	if err != nil {
		t.Fatalf("marshal fork request: %v", err)
	}
	if err := srv.handleLine(context.Background(), forkReq); err != nil {
		t.Fatalf("thread/fork: %v", err)
	}
	result := remarshal[ThreadForkResult](t, responseByID(t, parseOutput(t, out.String()), "3")["result"])
	fork := result.Thread
	if result.Worktree == nil || strings.TrimSpace(result.Worktree.Path) == "" {
		t.Fatalf("expected worktree info in fork result: %+v", result)
	}

	// Simulate runtime-root drift: the in-memory thread loses its worktree
	// CWD, as if the runtime had been (re)built against the parent repo. The
	// session metadata still binds the worktree, and that binding — not the
	// incidental CWD — must decide where tools execute.
	forkTh := srv.thread(fork.ID)
	if forkTh == nil {
		t.Fatalf("fork thread %q was not loaded", fork.ID)
	}
	forkTh.mu.Lock()
	forkTh.CWD = rt.RootDir
	forkTh.mu.Unlock()

	forkTurnReq, err := json.Marshal(map[string]any{
		"id":     "4",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: fork.ID, Prompt: "write the isolated file"},
	})
	if err != nil {
		t.Fatalf("marshal fork turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), forkTurnReq); err != nil {
		t.Fatalf("turn/start on fork: %v", err)
	}
	waitForTurnCompletedCountForThread(t, out, fork.ID, 1)

	if _, err := os.Stat(filepath.Join(result.Worktree.Path, "ctx-isolated.txt")); err != nil {
		t.Fatalf("worktree-bound turn should write inside the checkout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rt.RootDir, "ctx-isolated.txt")); !os.IsNotExist(err) {
		t.Fatalf("parent repo must stay untouched, stat err=%v", err)
	}
}

func TestServerThreadEditMessageRewindsToUserMessage(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.MkdirAll(rt.SessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sess, err := session.CreateWithMetadata(rt.SessionDir, "20260619-000000-edit", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	history := []providers.ChatMessage{
		{
			Role:    "user",
			Content: "first prompt",
			Images:  []providers.InputImage{{MediaType: "image/png", Data: "image-data"}},
			Files:   []providers.InputFile{{MediaType: "application/pdf", Data: "file-data", Filename: "brief.pdf"}},
		},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second prompt"},
		{Role: "assistant", Content: "second answer"},
	}
	if err := rewriteChatHistory(rt.SessionDir, sess.ID, history); err != nil {
		t.Fatalf("write history: %v", err)
	}
	if err := session.UpdateIndex(rt.SessionDir, sess.ID, persistableMessageCount(history), threadPreview(history)); err != nil {
		t.Fatalf("update index: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	resumeReq := fmt.Sprintf(`{"id":"resume","method":"%s","params":{"session_id":%q}}`, MethodThreadResume, sess.ID)
	if err := srv.handleLine(context.Background(), []byte(resumeReq)); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}
	resumed := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, out.String()), "resume")["result"])
	if len(resumed.Thread.Turns) != 2 {
		t.Fatalf("expected two turns, got %+v", resumed.Thread.Turns)
	}
	target := resumed.Thread.Turns[0].Items[0]
	editPayload, err := json.Marshal(map[string]any{
		"id":     "edit",
		"method": MethodThreadEditMessage,
		"params": ThreadEditMessageParams{
			ThreadID: resumed.Thread.ID,
			TurnID:   resumed.Thread.Turns[0].ID,
			ItemID:   target.ID,
		},
	})
	if err != nil {
		t.Fatalf("marshal edit request: %v", err)
	}
	if err := srv.handleLine(context.Background(), editPayload); err != nil {
		t.Fatalf("thread/edit-message: %v", err)
	}

	msgs := parseOutput(t, out.String())
	editResponse := responseByID(t, msgs, "edit")
	if editResponse["error"] != nil {
		t.Fatalf("thread/edit-message returned error: %+v", editResponse["error"])
	}
	result := remarshal[ThreadEditMessageResult](t, editResponse["result"])
	if result.Draft.Prompt != "first prompt" {
		t.Fatalf("unexpected restored draft: %+v", result.Draft)
	}
	if len(result.Draft.Images) != 1 || result.Draft.Images[0].Data != "image-data" {
		t.Fatalf("expected image to be restored in draft: %+v", result.Draft)
	}
	if len(result.Draft.Files) != 1 || result.Draft.Files[0].Filename != "brief.pdf" || result.Draft.Files[0].Data != "file-data" {
		t.Fatalf("expected file to be restored in draft: %+v", result.Draft)
	}
	if len(result.Thread.Turns) != 0 {
		t.Fatalf("expected thread to rewind before first user message, got %+v", result.Thread.Turns)
	}
	persisted, err := loadChatMessages(rt.SessionDir, sess.ID)
	if err != nil {
		t.Fatalf("load persisted history: %v", err)
	}
	if len(persisted) != 0 {
		t.Fatalf("expected persisted history to remove target and later messages, got %+v", persisted)
	}
	updated := notificationsByMethod(msgs, NotificationThreadUpdated)
	if len(updated) == 0 {
		t.Fatalf("expected thread/updated notification")
	}
}

func TestServerThreadEditMessageRespectsCompactionBoundary(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.MkdirAll(rt.SessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sess, err := session.CreateWithMetadata(rt.SessionDir, "20260619-000001-edit-compact", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	history := []providers.ChatMessage{
		{Role: "system", Content: compact.BuildSummaryContent("older prompts are summarized")},
		{Role: "user", Content: "after compact"},
		{Role: "assistant", Content: "after compact answer"},
		{Role: "user", Content: "latest prompt"},
		{Role: "assistant", Content: "latest answer"},
	}
	if err := rewriteChatHistory(rt.SessionDir, sess.ID, history); err != nil {
		t.Fatalf("write history: %v", err)
	}
	if err := session.UpdateIndex(rt.SessionDir, sess.ID, persistableMessageCount(history), threadPreview(history)); err != nil {
		t.Fatalf("update index: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	resumeReq := fmt.Sprintf(`{"id":"resume","method":"%s","params":{"session_id":%q}}`, MethodThreadResume, sess.ID)
	if err := srv.handleLine(context.Background(), []byte(resumeReq)); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}
	resumed := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, out.String()), "resume")["result"])
	if len(resumed.Thread.Turns) != 2 {
		t.Fatalf("expected two visible turns after compact summary, got %+v", resumed.Thread.Turns)
	}
	if len(resumed.Thread.Turns[0].Items) < 2 || resumed.Thread.Turns[0].Items[1].Type != ThreadItemContextCompaction {
		t.Fatalf("expected compaction notice attached to first visible turn, got %+v", resumed.Thread.Turns[0].Items)
	}

	contextItem := resumed.Thread.Turns[0].Items[1]
	rejectedPayload, err := json.Marshal(map[string]any{
		"id":     "reject-summary",
		"method": MethodThreadEditMessage,
		"params": ThreadEditMessageParams{
			ThreadID: resumed.Thread.ID,
			TurnID:   resumed.Thread.Turns[0].ID,
			ItemID:   contextItem.ID,
		},
	})
	if err != nil {
		t.Fatalf("marshal rejected edit request: %v", err)
	}
	if err := srv.handleLine(context.Background(), rejectedPayload); err != nil {
		t.Fatalf("thread/edit-message summary item: %v", err)
	}
	rejected := responseByID(t, parseOutput(t, out.String()), "reject-summary")
	if rejected["error"] == nil {
		t.Fatalf("expected compaction notice edit to be rejected")
	}

	target := resumed.Thread.Turns[0].Items[0]
	editPayload, err := json.Marshal(map[string]any{
		"id":     "edit-visible",
		"method": MethodThreadEditMessage,
		"params": ThreadEditMessageParams{
			ThreadID: resumed.Thread.ID,
			TurnID:   resumed.Thread.Turns[0].ID,
			ItemID:   target.ID,
		},
	})
	if err != nil {
		t.Fatalf("marshal edit request: %v", err)
	}
	if err := srv.handleLine(context.Background(), editPayload); err != nil {
		t.Fatalf("thread/edit-message visible user: %v", err)
	}
	editResponse := responseByID(t, parseOutput(t, out.String()), "edit-visible")
	if editResponse["error"] != nil {
		t.Fatalf("thread/edit-message returned error: %+v", editResponse["error"])
	}
	result := remarshal[ThreadEditMessageResult](t, editResponse["result"])
	if result.Draft.Prompt != "after compact" {
		t.Fatalf("unexpected restored draft: %+v", result.Draft)
	}
	persisted, err := loadChatMessages(rt.SessionDir, sess.ID)
	if err != nil {
		t.Fatalf("load persisted history: %v", err)
	}
	if len(persisted) != 1 || persisted[0].Role != "system" || !compact.IsConversationSummaryContent(persisted[0].Content) {
		t.Fatalf("expected compact summary to remain as edit boundary, got %+v", persisted)
	}
}

func TestServerThreadEditMessageAfterProviderCheckpointWithEarlierVisibleTurns(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := session.CreateWithMetadata(rt.SessionDir, "20260619-000002-edit-checkpoint", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, rec := range []session.HistoryRecord{
		{Role: "user", Content: "older prompt"},
		{Role: "assistant", Content: "older answer"},
	} {
		if err := session.AppendHistoryRecord(rt.SessionDir, sess.ID, rec); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}
	if _, err := session.StoreHistoryCheckpointAtBaseline(
		rt.SessionDir,
		sess.ID,
		session.HistoryCheckpointKindProviderRewrite,
		[]session.HistoryRecord{
			{Role: "system", Content: compact.BuildSummaryContent("older prompts are summarized")},
		},
		2,
	); err != nil {
		t.Fatalf("store provider checkpoint: %v", err)
	}
	for _, rec := range []session.HistoryRecord{
		{Role: "user", Content: "after compact"},
		{Role: "assistant", Content: "after compact answer"},
		{Role: "user", Content: "replace me"},
		{
			Role:       "meta",
			Content:    turnTerminalHistoryRecord,
			ClientID:   fmt.Sprintf("%s-turn-0003", sess.ID),
			StopReason: string(TurnStatusInterrupted),
		},
	} {
		if err := session.AppendHistoryRecord(rt.SessionDir, sess.ID, rec); err != nil {
			t.Fatalf("append retained history: %v", err)
		}
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	resumeReq := fmt.Sprintf(`{"id":"resume","method":"%s","params":{"session_id":%q}}`, MethodThreadResume, sess.ID)
	if err := srv.handleLine(context.Background(), []byte(resumeReq)); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}
	resumed := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, out.String()), "resume")["result"])
	if len(resumed.Thread.Turns) != 3 {
		t.Fatalf("expected earlier visible turn plus two retained turns, got %+v", resumed.Thread.Turns)
	}
	targetTurn := resumed.Thread.Turns[2]
	target := targetTurn.Items[0]
	if targetTurn.Status != TurnStatusInterrupted || target.Seq != 6 || target.Text != "replace me" {
		t.Fatalf("unexpected edit target: %+v", target)
	}
	editPayload, err := json.Marshal(map[string]any{
		"id":     "edit",
		"method": MethodThreadEditMessage,
		"params": ThreadEditMessageParams{
			ThreadID: resumed.Thread.ID,
			TurnID:   targetTurn.ID,
			ItemID:   target.ID,
		},
	})
	if err != nil {
		t.Fatalf("marshal edit request: %v", err)
	}
	if err := srv.handleLine(context.Background(), editPayload); err != nil {
		t.Fatalf("thread/edit-message: %v", err)
	}

	editResponse := responseByID(t, parseOutput(t, out.String()), "edit")
	if editResponse["error"] != nil {
		t.Fatalf("thread/edit-message returned error: %+v", editResponse["error"])
	}
	result := remarshal[ThreadEditMessageResult](t, editResponse["result"])
	if result.Draft.Prompt != "replace me" {
		t.Fatalf("unexpected restored draft: %+v", result.Draft)
	}
	persisted, err := loadChatMessages(rt.SessionDir, sess.ID)
	if err != nil {
		t.Fatalf("load persisted history: %v", err)
	}
	if len(persisted) != 3 || persisted[1].Content != "after compact" || persisted[2].Content != "after compact answer" {
		t.Fatalf("expected edit to preserve retained history before target, got %+v", persisted)
	}
}

func TestServerTurnStartAcceptsImageOnlyPrompt(t *testing.T) {
	// tinyImageOnlyB64 is a real 1×1 JPEG base64-encoded. imageproc now
	// runs on every image that crosses the app-server boundary (see
	// internal/imageproc); the previous "ZmFrZS1pbWFnZQ==" placeholder
	// was 10 bytes of ASCII and trips the detectFormat sanity guard. The
	// exact bytes round-trip unchanged because 1×1 fits within MaxDimension.
	tinyImageOnlyB64 := base64.StdEncoding.EncodeToString(encodeTestJPEG(t, 1, 1, 90))
	client := &fakeClient{
		response: providers.ChatResponse{Content: "saw it"},
	}
	rt := newTestRuntime(t, client)
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	payload := map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{
			ThreadID: threadID,
			Images: []TurnStartImage{{
				MediaType: "image/jpeg",
				Data:      tinyImageOnlyB64,
			}},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	msgs := waitForMethod(t, out, NotificationTurnCompleted)
	started := remarshal[TurnStartResult](t, responseByID(t, msgs, "2")["result"])
	if len(started.Turn.Items) != 1 || len(started.Turn.Items[0].Images) != 1 {
		t.Fatalf("start response missing user image: %+v", started.Turn)
	}
	completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, msgs, NotificationTurnCompleted)["params"])
	if len(completed.Turn.Items) < 1 || len(completed.Turn.Items[0].Images) != 1 {
		t.Fatalf("completed turn missing user image: %+v", completed.Turn)
	}

	client.mu.Lock()
	requestCount := len(client.requests)
	var messages []providers.ChatMessage
	if requestCount > 0 {
		messages = append([]providers.ChatMessage(nil), client.requests[0].Messages...)
	}
	client.mu.Unlock()
	if requestCount != 1 {
		t.Fatalf("expected one provider request, got %d", requestCount)
	}
	if len(messages) < 2 || messages[1].Role != "user" || messages[1].Content != "" || len(messages[1].Images) != 1 {
		t.Fatalf("unexpected provider messages: %+v", messages)
	}
	if messages[1].Images[0].MediaType != "image/jpeg" || messages[1].Images[0].Data != tinyImageOnlyB64 {
		t.Fatalf("unexpected provider image: %+v", messages[1].Images[0])
	}

	persisted, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("load persisted history: %v", err)
	}
	visiblePersisted := visibleMessagesForTest(persisted)
	if len(visiblePersisted) != 2 || len(visiblePersisted[0].Images) != 1 {
		t.Fatalf("unexpected persisted history: %+v", persisted)
	}
	sessions, err := session.List(rt.SessionDir, 1)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Summary != "[Image #1]" {
		t.Fatalf("unexpected session index: %+v", sessions)
	}
}

func TestServerTurnStartAcceptsPDFOnlyPrompt(t *testing.T) {
	client := &fakeClient{
		response: providers.ChatResponse{Content: "read it"},
	}
	rt := newTestRuntime(t, client)
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	payload := map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{
			ThreadID: threadID,
			Files: []TurnStartFile{{
				MediaType: "application/pdf",
				Data:      "JVBERi0xLjQ=",
				Filename:  "brief.pdf",
			}},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	msgs := waitForMethod(t, out, NotificationTurnCompleted)
	started := remarshal[TurnStartResult](t, responseByID(t, msgs, "2")["result"])
	if len(started.Turn.Items) != 1 || len(started.Turn.Items[0].Files) != 1 {
		t.Fatalf("start response missing user file: %+v", started.Turn)
	}
	if started.Turn.Items[0].Files[0].Filename != "brief.pdf" {
		t.Fatalf("unexpected thread item file: %+v", started.Turn.Items[0].Files[0])
	}

	client.mu.Lock()
	requestCount := len(client.requests)
	var messages []providers.ChatMessage
	if requestCount > 0 {
		messages = append([]providers.ChatMessage(nil), client.requests[0].Messages...)
	}
	client.mu.Unlock()
	if requestCount != 1 {
		t.Fatalf("expected one provider request, got %d", requestCount)
	}
	if len(messages) < 2 || messages[1].Role != "user" || messages[1].Content != "" || len(messages[1].Files) != 1 {
		t.Fatalf("unexpected provider messages: %+v", messages)
	}
	if messages[1].Files[0].MediaType != "application/pdf" || messages[1].Files[0].Data != "JVBERi0xLjQ=" || messages[1].Files[0].Filename != "brief.pdf" {
		t.Fatalf("unexpected provider file: %+v", messages[1].Files[0])
	}

	persisted, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("load persisted history: %v", err)
	}
	visiblePersisted := visibleMessagesForTest(persisted)
	if len(visiblePersisted) != 2 || len(visiblePersisted[0].Files) != 1 {
		t.Fatalf("unexpected persisted history: %+v", persisted)
	}
	sessions, err := session.List(rt.SessionDir, 1)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Summary != "[brief.pdf]" {
		t.Fatalf("unexpected session index: %+v", sessions)
	}
}

func TestServerThreadListUsesSessionIndexMetadata(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if _, err := session.CreateWithMetadata(rt.SessionDir, "old-thread", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if err := session.UpdateIndex(rt.SessionDir, "old-thread", 2, "old summary"); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendHistoryRecord(rt.SessionDir, "old-thread", session.HistoryRecord{
		Role: "meta", Content: turnTerminalHistoryRecord, ClientID: "old-thread-turn-0001", StopReason: string(TurnStatusCompleted),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.SetRuntimeSelection(rt.SessionDir, "old-thread", session.RuntimeSelection{
		Provider:       "anthropic",
		Model:          "claude-sonnet-4-6",
		Variant:        "high",
		Effort:         "medium",
		PermissionMode: config.PermissionModeUnconfined,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateWithMetadata(rt.SessionDir, "new-thread", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if err := session.UpdateIndex(rt.SessionDir, "new-thread", 2, "new summary"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateWithMetadata(rt.SessionDir, "archived-thread", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if _, err := session.UpdateArchived(rt.SessionDir, "archived-thread", true); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateWithMetadata(rt.SessionDir, "other-thread", filepath.Join(rt.RootDir, "other")); err != nil {
		t.Fatal(err)
	}
	if _, err := session.UpdatePinned(rt.SessionDir, "old-thread", true); err != nil {
		t.Fatal(err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/list"}`)); err != nil {
		t.Fatalf("thread/list: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[ThreadListResult](t, responseByID(t, msgs, "1")["result"])
	if len(result.Threads) != 2 {
		t.Fatalf("expected two visible workspace threads, got %+v", result.Threads)
	}
	if result.Threads[0].ID != "old-thread" || !result.Threads[0].Pinned || result.Threads[0].Preview != "old summary" {
		t.Fatalf("expected pinned old thread first, got %+v", result.Threads)
	}
	if result.Threads[0].LatestCompletedTurnID != "old-thread-turn-0001" || len(result.Threads[0].Turns) != 0 {
		t.Fatalf("expected lightweight completed-turn summary, got %+v", result.Threads[0])
	}
	if result.Threads[0].ModelProvider != "anthropic" || result.Threads[0].Model != "claude-sonnet-4-6" || result.Threads[0].ModelVariant != "high" {
		t.Fatalf("persisted model selection not restored: %+v", result.Threads[0])
	}
	if result.Threads[0].ModelEffort != "medium" || result.Threads[0].PermissionMode != config.PermissionModeUnconfined {
		t.Fatalf("persisted effort/permission not restored: %+v", result.Threads[0])
	}
	if result.Threads[1].ID != "new-thread" || result.Threads[1].Archived {
		t.Fatalf("unexpected second thread: %+v", result.Threads[1])
	}
}

func TestServerThreadListCanTargetDifferentCWD(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	otherCWD := filepath.Join(rt.RootDir, "other")
	if _, err := session.CreateWithMetadata(rt.SessionDir, "root-thread", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateWithMetadata(rt.SessionDir, "other-thread", otherCWD); err != nil {
		t.Fatal(err)
	}
	if err := session.UpdateIndex(rt.SessionDir, "other-thread", 2, "other summary"); err != nil {
		t.Fatal(err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	raw := fmt.Sprintf(`{"id":"1","method":"thread/list","params":{"cwd":%q}}`, otherCWD)
	if err := srv.handleLine(context.Background(), []byte(raw)); err != nil {
		t.Fatalf("thread/list: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[ThreadListResult](t, responseByID(t, msgs, "1")["result"])
	if len(result.Threads) != 1 {
		t.Fatalf("expected one targeted workspace thread, got %+v", result.Threads)
	}
	if result.Threads[0].ID != "other-thread" || result.Threads[0].Preview != "other summary" {
		t.Fatalf("unexpected targeted thread: %+v", result.Threads[0])
	}
}

func TestServerThreadListOrdersSessionsByUpdatedAt(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if _, err := session.CreateWithMetadata(rt.SessionDir, "first-thread", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if err := session.UpdateIndex(rt.SessionDir, "first-thread", 2, "first summary"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateWithMetadata(rt.SessionDir, "second-thread", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if err := session.UpdateIndex(rt.SessionDir, "second-thread", 2, "second summary"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := session.UpdateIndex(rt.SessionDir, "first-thread", 4, "ignored later summary"); err != nil {
		t.Fatal(err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/list"}`)); err != nil {
		t.Fatalf("thread/list: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[ThreadListResult](t, responseByID(t, msgs, "1")["result"])
	if len(result.Threads) != 2 {
		t.Fatalf("expected two visible workspace threads, got %+v", result.Threads)
	}
	if result.Threads[0].ID != "first-thread" || result.Threads[1].ID != "second-thread" {
		t.Fatalf("expected recently updated thread first, got %+v", result.Threads)
	}
	if !result.Threads[0].UpdatedAt.After(result.Threads[1].UpdatedAt) {
		t.Fatalf("expected first thread updated_at to be newer, got %+v", result.Threads)
	}
}

func TestServerThreadSearchMatchesHistoryAcrossWorkspaces(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	userThread, err := session.CreateWithMetadata(rt.SessionDir, "user-thread", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := rewriteChatHistory(rt.SessionDir, userThread.ID, []providers.ChatMessage{
		{Role: "user", Content: "Investigate the delta-vector login failure"},
		{Role: "assistant", Content: "The login failure comes from stale config."},
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.UpdateIndex(rt.SessionDir, userThread.ID, 2, "login summary"); err != nil {
		t.Fatal(err)
	}
	assistantThread, err := session.CreateWithMetadata(rt.SessionDir, "assistant-thread", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := rewriteChatHistory(rt.SessionDir, assistantThread.ID, []providers.ChatMessage{
		{Role: "user", Content: "summarize the deploy"},
		{Role: "assistant", Content: "The deploy note mentions orion-cache warming."},
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.UpdateIndex(rt.SessionDir, assistantThread.ID, 2, "deploy summary"); err != nil {
		t.Fatal(err)
	}
	archivedThread, err := session.CreateWithMetadata(rt.SessionDir, "archived-thread", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := rewriteChatHistory(rt.SessionDir, archivedThread.ID, []providers.ChatMessage{
		{Role: "user", Content: "delta-vector archived"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.UpdateArchived(rt.SessionDir, archivedThread.ID, true); err != nil {
		t.Fatal(err)
	}
	otherThread, err := session.CreateWithMetadata(rt.SessionDir, "other-thread", filepath.Join(rt.RootDir, "other"))
	if err != nil {
		t.Fatal(err)
	}
	if err := rewriteChatHistory(rt.SessionDir, otherThread.ID, []providers.ChatMessage{
		{Role: "user", Content: "delta-vector other workspace"},
	}); err != nil {
		t.Fatal(err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	userPayload := map[string]any{
		"id":     "1",
		"method": MethodThreadSearch,
		"params": ThreadSearchParams{Query: "delta-vector"},
	}
	rawUserPayload, err := json.Marshal(userPayload)
	if err != nil {
		t.Fatalf("marshal user search request: %v", err)
	}
	if err := srv.handleLine(context.Background(), rawUserPayload); err != nil {
		t.Fatalf("thread/search user query: %v", err)
	}
	assistantPayload := map[string]any{
		"id":     "2",
		"method": MethodThreadSearch,
		"params": ThreadSearchParams{Query: "orion-cache"},
	}
	rawAssistantPayload, err := json.Marshal(assistantPayload)
	if err != nil {
		t.Fatalf("marshal assistant search request: %v", err)
	}
	if err := srv.handleLine(context.Background(), rawAssistantPayload); err != nil {
		t.Fatalf("thread/search assistant content: %v", err)
	}
	msgs := parseOutput(t, out.String())
	userResult := remarshal[ThreadSearchResult](t, responseByID(t, msgs, "1")["result"])
	if len(userResult.Results) != 2 {
		t.Fatalf("expected matches from both workspaces, got %+v", userResult.Results)
	}
	resultIDs := map[string]bool{}
	for _, result := range userResult.Results {
		resultIDs[result.Thread.ID] = true
		if !strings.Contains(result.Snippet, "delta-vector") {
			t.Fatalf("expected user query snippet, got %q", result.Snippet)
		}
	}
	if !resultIDs[userThread.ID] || !resultIDs[otherThread.ID] {
		t.Fatalf("expected user and other workspace threads, got %+v", userResult.Results)
	}
	assistantResult := remarshal[ThreadSearchResult](t, responseByID(t, msgs, "2")["result"])
	if len(assistantResult.Results) != 1 || assistantResult.Results[0].Thread.ID != assistantThread.ID {
		t.Fatalf("expected assistant-thread only, got %+v", assistantResult.Results)
	}
	if !strings.Contains(assistantResult.Results[0].Snippet, "orion-cache") {
		t.Fatalf("expected assistant content snippet, got %q", assistantResult.Results[0].Snippet)
	}
}

func TestServerThreadListIncludesDirectChildAgents(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StateDir = filepath.Join(rt.RootDir, ".wuu", "state")
	if _, err := session.CreateWithMetadata(rt.SessionDir, "root-thread", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if err := session.UpdateIndex(rt.SessionDir, "root-thread", 2, "root summary"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateWithMetadata(rt.SessionDir, "worker-1", rt.RootDir); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	store := agentthread.NewStore(filepath.Join(statepath.SessionArtifactDir(rt.StateDir, "root-thread"), "threads"))
	metas := []agentthread.Metadata{
		{
			ID:        "root-thread",
			Path:      agentthread.RootPath,
			Status:    agentthread.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Source:    agentthread.Source{Kind: agentthread.SourceRoot, Depth: 1},
		},
		{
			ID:        "worker-1",
			SessionID: "root-thread",
			ParentID:  "root-thread",
			Path:      "/root/inspect",
			TaskName:  "inspect",
			Role:      agentcontrol.DefaultSubagentType,
			Status:    agentthread.StatusRunning,
			CreatedAt: now.Add(time.Second),
			UpdatedAt: now.Add(time.Second),
			Source: agentthread.Source{
				Kind:           agentthread.SourceThreadSpawn,
				ParentThreadID: "root-thread",
				ParentPath:     agentthread.RootPath,
				Depth:          2,
			},
		},
		{
			ID:        "worker-2",
			SessionID: "root-thread",
			ParentID:  "worker-1",
			Path:      "/root/inspect/deeper",
			TaskName:  "deeper",
			Role:      agentcontrol.DefaultSubagentType,
			Status:    agentthread.StatusPending,
			CreatedAt: now.Add(2 * time.Second),
			UpdatedAt: now.Add(2 * time.Second),
			Source: agentthread.Source{
				Kind:           agentthread.SourceThreadSpawn,
				ParentThreadID: "worker-1",
				ParentPath:     "/root/inspect",
				Depth:          3,
			},
		},
	}
	for _, meta := range metas {
		if err := store.UpsertThread(meta); err != nil {
			t.Fatalf("upsert thread %s: %v", meta.ID, err)
		}
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/list"}`)); err != nil {
		t.Fatalf("thread/list: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[ThreadListResult](t, responseByID(t, msgs, "1")["result"])
	if len(result.Threads) != 1 {
		t.Fatalf("expected one root thread, got %+v", result.Threads)
	}
	thread := result.Threads[0]
	agents := thread.ChildAgents
	if len(agents) != 1 {
		t.Fatalf("expected only the direct child agent, got %+v", agents)
	}
	if agents[0].ID != "worker-1" || agents[0].TaskName != "inspect" || agents[0].NestedCount != 1 || agents[0].NestedRunningCount != 1 {
		t.Fatalf("unexpected child agent summary: %+v", agents[0])
	}
	if len(thread.Turns) != 0 {
		t.Fatalf("thread/list must not synthesize subagent task-card turns, got %+v", thread.Turns)
	}
}

func TestServerThreadResumeLoadsChildAgentSession(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StateDir = filepath.Join(rt.RootDir, ".wuu", "state")
	if _, err := session.CreateWithMetadata(rt.SessionDir, "root-thread", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if err := session.UpdateIndex(rt.SessionDir, "root-thread", 2, "root summary"); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	meta := agentthread.Metadata{
		ID:              "worker-1",
		SessionID:       "root-thread",
		ParentID:        "root-thread",
		Path:            "/root/inspect",
		TaskName:        "inspect",
		Role:            agentcontrol.DefaultSubagentType,
		LastTaskMessage: "inspect the UI",
		CWD:             rt.RootDir,
		Model:           "worker-model",
		Status:          agentthread.StatusCompleted,
		CreatedAt:       now,
		UpdatedAt:       now.Add(time.Minute),
		Source: agentthread.Source{
			Kind:           agentthread.SourceThreadSpawn,
			ParentThreadID: "root-thread",
			ParentPath:     agentthread.RootPath,
			Depth:          2,
		},
	}
	store := agentthread.NewStore(filepath.Join(statepath.SessionArtifactDir(rt.StateDir, "root-thread"), "threads"))
	if err := store.UpsertThread(meta); err != nil {
		t.Fatalf("upsert worker thread: %v", err)
	}

	workerDir := filepath.Join(statepath.SessionArtifactDir(rt.StateDir, "root-thread"), "workers")
	if err := os.MkdirAll(workerDir, 0o755); err != nil {
		t.Fatalf("mkdir worker history: %v", err)
	}
	rec := persistedAgentHistory{
		ID:          "worker-1",
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "inspect",
		AgentPath:   "/root/inspect",
		ParentID:    "root-thread",
		Description: "inspect",
		Status:      "completed",
		StartedAt:   now,
		CompletedAt: now.Add(time.Minute),
		Model:       "worker-model",
		Prompt:      "inspect the UI",
		Result:      "child session result",
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "worker system"},
			{Role: "user", Content: "inspect the UI"},
			{Role: "assistant", Content: "child session result"},
		},
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("marshal worker history: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workerDir, "worker-1.json"), data, 0o644); err != nil {
		t.Fatalf("write worker history: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	payload, err := json.Marshal(map[string]any{
		"id":     "1",
		"method": MethodThreadResume,
		"params": ThreadResumeParams{SessionID: "worker-1"},
	})
	if err != nil {
		t.Fatalf("marshal resume request: %v", err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[ThreadResumeResult](t, responseByID(t, msgs, "1")["result"])
	thread := result.Thread
	if thread.ID != "worker-1" || !thread.ReadOnly || thread.ParentID != "root-thread" || thread.AgentPath != "/root/inspect" {
		t.Fatalf("unexpected child thread identity: %+v", thread)
	}
	if thread.Model != "worker-model" || thread.Preview != "inspect" {
		t.Fatalf("unexpected child thread metadata: %+v", thread)
	}
	if len(thread.Turns) != 1 || len(thread.Turns[0].Items) != 2 {
		t.Fatalf("unexpected child thread turns: %+v", thread.Turns)
	}
	if got := thread.Turns[0].Items[1].Text; got != "child session result" {
		t.Fatalf("unexpected child agent message: %q", got)
	}
	resumed := remarshal[ThreadResumedNotification](t, notificationByMethod(t, msgs, NotificationThreadResumed)["params"])
	if resumed.Thread.ID != "worker-1" || !resumed.Thread.ReadOnly {
		t.Fatalf("unexpected resumed notification: %+v", resumed)
	}
}

func TestServerChildAgentSessionIsLiveWhileRunning(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StateDir = filepath.Join(rt.RootDir, ".wuu", "state")
	rootID := "root-live"
	artifactDir := statepath.SessionArtifactDir(rt.StateDir, rootID)
	workerClient := newBlockingStreamClient("child live result")
	coord, err := agentcontrol.New(agentcontrol.Config{
		Client:       workerClient,
		DefaultModel: "fake-model",
		ParentRepo:   rt.RootDir,
		WorktreeRoot: filepath.Join(rt.RootDir, ".wuu", "worktrees"),
		SessionID:    rootID,
		HistoryDir:   filepath.Join(artifactDir, "workers"),
		ThreadDir:    filepath.Join(artifactDir, "threads"),
		HarnessDir:   filepath.Join(artifactDir, "harness"),
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	if err != nil {
		t.Fatalf("coordinator: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	rootThread := newThreadState(rootID, nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	rootThread.execRuntime = &runtime.ThreadRuntime{AgentControl: coord}
	srv.mu.Lock()
	srv.threads[rootID] = rootThread
	srv.mu.Unlock()
	srv.subscribeThreadRuntime(rootID, rootThread.execRuntime)

	spawned, err := coord.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "live_child",
		Description: "live child",
		Prompt:      "do it live",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	select {
	case <-workerClient.started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start streaming")
	}

	waitForMethod(t, out, NotificationTurnStarted)
	payload, err := json.Marshal(map[string]any{
		"id":     "resume-child",
		"method": MethodThreadResume,
		"params": ThreadResumeParams{SessionID: spawned.AgentID},
	})
	if err != nil {
		t.Fatalf("marshal resume request: %v", err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatalf("thread/resume child: %v", err)
	}
	msgs := parseOutput(t, out.String())
	result := remarshal[ThreadResumeResult](t, responseByID(t, msgs, "resume-child")["result"])
	if result.Thread.ID != spawned.AgentID || !result.Thread.ReadOnly || result.Thread.Status != ThreadStatusInProgress {
		t.Fatalf("unexpected live child thread: %+v", result.Thread)
	}
	if len(result.Thread.Turns) != 1 || result.Thread.Turns[0].Status != TurnStatusInProgress {
		t.Fatalf("expected running child turn, got %+v", result.Thread.Turns)
	}

	close(workerClient.release)
	msgs = waitForMethod(t, out, NotificationTurnCompleted)
	var childCompleted bool
	var childDelta bool
	for _, msg := range msgs {
		if msg["method"] == NotificationAgentMessageDelta {
			params := msg["params"].(map[string]any)
			if params["thread_id"] == spawned.AgentID && params["delta"] == "child live result" {
				childDelta = true
			}
		}
		if msg["method"] == NotificationTurnCompleted {
			params := msg["params"].(map[string]any)
			if params["thread_id"] == spawned.AgentID {
				childCompleted = true
			}
		}
	}
	if !childDelta || !childCompleted {
		t.Fatalf("expected child delta and completion notifications, delta=%v completed=%v output:\n%s", childDelta, childCompleted, out.String())
	}
	awaitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	awaited, err := coord.AwaitFrom(agentthread.RootPath, awaitCtx, []string{spawned.AgentID})
	if err != nil {
		t.Fatalf("AwaitFrom after child completion: %v", err)
	}
	if len(awaited.Results) != 1 || awaited.Results[0].AgentID != spawned.AgentID {
		t.Fatalf("unexpected awaited child result: %+v", awaited)
	}
}

func TestServerThreadPinAndArchive(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	pinPayload, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodThreadPin,
		"params": ThreadPinParams{ThreadID: threadID, Pinned: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), pinPayload); err != nil {
		t.Fatalf("thread/pin: %v", err)
	}
	pinResult := remarshal[ThreadPinResult](t, responseByID(t, parseOutput(t, out.String()), "2")["result"])
	if pinResult.Thread.ID != threadID || !pinResult.Thread.Pinned {
		t.Fatalf("unexpected pin result: %+v", pinResult)
	}
	pinned, ok, err := session.Find(rt.SessionDir, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || pinned.PinnedAt == nil {
		t.Fatalf("pin not persisted: ok=%v session=%+v", ok, pinned)
	}

	archivePayload, err := json.Marshal(map[string]any{
		"id":     "3",
		"method": MethodThreadArchive,
		"params": ThreadArchiveParams{ThreadID: threadID, Archived: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), archivePayload); err != nil {
		t.Fatalf("thread/archive: %v", err)
	}
	archiveResult := remarshal[ThreadArchiveResult](t, responseByID(t, parseOutput(t, out.String()), "3")["result"])
	if archiveResult.Thread.ID != threadID || !archiveResult.Thread.Archived || archiveResult.Thread.Pinned {
		t.Fatalf("unexpected archive result: %+v", archiveResult)
	}
	archived, ok, err := session.Find(rt.SessionDir, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || archived.PinnedAt != nil {
		t.Fatalf("archive should clear pin: ok=%v session=%+v", ok, archived)
	}

	if err := srv.handleLine(context.Background(), []byte(`{"id":"4","method":"thread/list"}`)); err != nil {
		t.Fatalf("thread/list: %v", err)
	}
	listResult := remarshal[ThreadListResult](t, responseByID(t, parseOutput(t, out.String()), "4")["result"])
	if len(listResult.Threads) != 0 {
		t.Fatalf("archived thread should be hidden, got %+v", listResult.Threads)
	}
}

func TestServerThreadOrganizationUpdatesAreIndependentAndListsUsePersistedMetadata(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WorkspaceID = "workspace-stable"
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	startMessages := parseOutput(t, out.String())
	startedThread := remarshal[ThreadStartResult](t, responseByID(t, startMessages, "1")["result"]).Thread
	threadID := startedThread.ID
	if startedThread.WorkspaceID != rt.WorkspaceID {
		t.Fatalf("thread/start lost stable workspace id: %+v", startedThread)
	}
	startedNotification := remarshal[ThreadStartedNotification](t, notificationByMethod(t, startMessages, NotificationThreadStarted)["params"])
	if startedNotification.Thread.WorkspaceID != rt.WorkspaceID {
		t.Fatalf("thread/started lost stable workspace id: %+v", startedNotification.Thread)
	}
	folder, err := session.CreateFolder(rt.SessionDir, "Topic")
	if err != nil {
		t.Fatal(err)
	}

	update := func(requestID string, params map[string]any) Thread {
		t.Helper()
		params["thread_id"] = threadID
		payload, marshalErr := json.Marshal(map[string]any{
			"id": requestID, "method": MethodThreadOrganizationUpdate, "params": params,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if handleErr := srv.handleLine(context.Background(), payload); handleErr != nil {
			t.Fatalf("thread/organization/update: %v", handleErr)
		}
		return remarshal[ThreadOrganizationUpdateResult](t, responseByID(t, parseOutput(t, out.String()), requestID)["result"]).Thread
	}

	updated := update("2", map[string]any{"folder_id": folder.ID})
	if updated.FolderID != folder.ID {
		t.Fatalf("unexpected organization update: %+v", updated)
	}
	updated = update("3", map[string]any{"folder_id": ""})
	if updated.FolderID != "" {
		t.Fatalf("folder was not cleared: %+v", updated)
	}

	// Change the database without touching this server's loaded thread. Global
	// listing must still treat persisted organization metadata as authoritative.
	if _, err := session.UpdateOrganization(rt.SessionDir, threadID, &folder.ID); err != nil {
		t.Fatal(err)
	}
	if err := srv.notifyThreadUpdated(updated); err != nil {
		t.Fatal(err)
	}
	notifications := notificationsByMethod(parseOutput(t, out.String()), NotificationThreadUpdated)
	notified := remarshal[ThreadUpdatedNotification](t, notifications[len(notifications)-1]["params"])
	if notified.Thread.FolderID != folder.ID || notified.Thread.WorkspaceID != rt.WorkspaceID {
		t.Fatalf("thread update emitted stale organization metadata: %+v", notified.Thread)
	}
	// A registered project's path may change while a thread remains loaded.
	// Scoped listing must use the persisted stable workspace id rather than the
	// in-memory thread's original cwd.
	rt.RootDir = t.TempDir()
	if err := srv.handleLine(context.Background(), []byte(`{"id":"5","method":"thread/list"}`)); err != nil {
		t.Fatalf("thread/list after project move: %v", err)
	}
	scoped := remarshal[ThreadListResult](t, responseByID(t, parseOutput(t, out.String()), "5")["result"])
	if len(scoped.Threads) != 1 || scoped.Threads[0].ID != threadID || scoped.Threads[0].WorkspaceID != rt.WorkspaceID {
		t.Fatalf("thread/list lost loaded thread after project move: %+v", scoped.Threads)
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"4","method":"thread/listAll"}`)); err != nil {
		t.Fatalf("thread/listAll: %v", err)
	}
	list := remarshal[ThreadListResult](t, responseByID(t, parseOutput(t, out.String()), "4")["result"])
	if len(list.Threads) != 1 || list.Threads[0].FolderID != folder.ID {
		t.Fatalf("listAll did not use persisted organization metadata: %+v", list.Threads)
	}
	if list.Threads[0].WorkspaceID != rt.WorkspaceID {
		t.Fatalf("listAll lost stable workspace id: %+v", list.Threads[0])
	}
	secondFolder, err := session.CreateFolder(rt.SessionDir, "Second topic")
	if err != nil {
		t.Fatal(err)
	}
	reorderPayload, err := json.Marshal(map[string]any{
		"id": "6", "method": MethodSessionFolderReorder,
		"params": OrganizationGroupReorderParams{IDs: []string{secondFolder.ID, folder.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), reorderPayload); err != nil {
		t.Fatalf("sessionFolder/reorder: %v", err)
	}
	reordered := remarshal[SessionOrganizationListResult](t, responseByID(t, parseOutput(t, out.String()), "6")["result"])
	if len(reordered.Organization.Folders) != 2 || reordered.Organization.Folders[0].ID != secondFolder.ID || reordered.Organization.Folders[1].ID != folder.ID {
		t.Fatalf("unexpected folder reorder result: %+v", reordered.Organization.Folders)
	}
}

func TestServerThreadRenameNotifiesUpdatedThread(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	renamePayload, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodThreadRename,
		"params": ThreadRenameParams{ThreadID: threadID, Title: "Closed loop title"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), renamePayload); err != nil {
		t.Fatalf("thread/rename: %v", err)
	}

	messages := parseOutput(t, out.String())
	renameResult := remarshal[ThreadRenameResult](t, responseByID(t, messages, "2")["result"])
	if renameResult.Thread.ID != threadID || renameResult.Thread.Title != "Closed loop title" {
		t.Fatalf("unexpected rename result: %+v", renameResult)
	}
	updated := notificationsByMethod(messages, NotificationThreadUpdated)
	if len(updated) == 0 {
		t.Fatalf("expected thread/updated notification after rename, got %+v", messages)
	}
	params := remarshal[ThreadUpdatedNotification](t, updated[len(updated)-1]["params"])
	if params.Thread.ID != threadID || params.Thread.Title != "Closed loop title" {
		t.Fatalf("unexpected thread/updated notification: %+v", params)
	}
}

func TestServerRejectsUnknownTurnParams(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"turn/start","params":{"thread_id":"x","prompt":"p","extra":true}}`)); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	resp := responseByID(t, parseOutput(t, out.String()), "1")
	if resp["error"] == nil {
		t.Fatalf("expected response error, got %+v", resp)
	}
}

func TestServerHTTP400EmitsSingleTerminalErrorWithoutRetry(t *testing.T) {
	client := &fakeClient{err: &providers.HTTPError{
		StatusCode: 400,
		Body:       `{"error":{"type":"invalid_request_error","message":"Invalid request"}}`,
	}}
	rt := newTestRuntime(t, client)
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	raw, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "trigger invalid request"},
	})
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	messages := waitForMethod(t, out, NotificationTurnError)
	errors := notificationsByMethod(messages, NotificationTurnError)
	if len(errors) != 1 {
		t.Fatalf("turn/error notifications = %d, want 1", len(errors))
	}
	if completed := notificationsByMethod(messages, NotificationTurnCompleted); len(completed) != 0 {
		t.Fatalf("turn/completed notifications = %d, want 0", len(completed))
	}
	failed := remarshal[TurnErrorNotification](t, errors[0]["params"])
	if failed.Category != "invalid_request" || failed.StatusCode != 400 || failed.Turn.Status != TurnStatusFailed {
		t.Fatalf("unexpected HTTP 400 terminal notification: %+v", failed)
	}
	client.mu.Lock()
	requestCount := len(client.requests)
	client.mu.Unlock()
	if requestCount != 1 {
		t.Fatalf("provider requests = %d, want 1", requestCount)
	}
}

func TestServerFailedTurnPersistsPairedToolHistoryAndReloadsFailure(t *testing.T) {
	client := &fakeClient{
		responses: []providers.ChatResponse{{
			ToolCalls: []providers.ToolCall{{
				ID:        "call_1",
				Name:      "grep",
				Arguments: `{"pattern":"inspect","path":"."}`,
			}},
		}},
	}
	client.onChat = func(call int, _ providers.ChatRequest) {
		if call == 1 {
			client.mu.Lock()
			client.err = fmt.Errorf("provider unavailable")
			client.mu.Unlock()
		}
	}
	rt := newTestRuntime(t, client)
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("new toolkit: %v", err)
	}
	rt.Toolkit = kit
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	raw, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "inspect"},
	})
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	msgs := waitForMethod(t, out, NotificationTurnError)
	failed := remarshal[TurnErrorNotification](t, notificationByMethod(t, msgs, NotificationTurnError)["params"])
	if failed.Turn.Status != TurnStatusFailed {
		t.Fatalf("turn status = %q, want failed", failed.Turn.Status)
	}

	persisted, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	visible := visibleMessagesForTest(persisted)
	if len(visible) != 3 {
		t.Fatalf("failed turn history length = %d, want user + assistant tool call + tool result: %+v", len(visible), visible)
	}
	if visible[0].Role != "user" || visible[0].Content != "inspect" {
		t.Fatalf("unexpected persisted user message: %+v", visible[0])
	}
	if visible[1].Role != "assistant" || len(visible[1].ToolCalls) != 1 || visible[1].ToolCalls[0].ID != "call_1" {
		t.Fatalf("failed turn did not persist assistant tool call: %+v", visible[1])
	}
	if visible[2].Role != "tool" || visible[2].ToolCallID != "call_1" {
		t.Fatalf("failed turn did not persist paired tool result: %+v", visible[2])
	}

	reloadOut := &lockedBuffer{}
	reloaded := New(rt, reloadOut)
	resume := fmt.Sprintf(`{"id":"reload","method":"thread/resume","params":{"session_id":%q}}`, threadID)
	if err := reloaded.handleLine(context.Background(), []byte(resume)); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}
	result := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, reloadOut.String()), "reload")["result"])
	if len(result.Thread.Turns) != 1 {
		t.Fatalf("reloaded turns = %+v, want one failed turn", result.Thread.Turns)
	}
	reloadedTurn := result.Thread.Turns[0]
	if reloadedTurn.Status != TurnStatusFailed || reloadedTurn.Error == nil || !strings.Contains(reloadedTurn.Error.Message, "provider unavailable") || reloadedTurn.CompletedAt == nil {
		t.Fatalf("reloaded turn did not restore failure: %+v", reloadedTurn)
	}
	if len(reloadedTurn.Items) == 0 || reloadedTurn.Items[len(reloadedTurn.Items)-1].Type != ThreadItemError || !strings.Contains(reloadedTurn.Items[len(reloadedTurn.Items)-1].Error, "provider unavailable") {
		t.Fatalf("reloaded turn did not restore error item: %+v", reloadedTurn.Items)
	}
}

func TestServerInterruptedPartialTurnReloadsInterrupted(t *testing.T) {
	client := newPartialBlockingStreamClient("partial answer")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"start","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"]).Thread.ID
	startTurn := fmt.Sprintf(`{"id":"turn","method":"turn/start","params":{"thread_id":%q,"prompt":"inspect"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(startTurn)); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	<-client.started
	deltaMessages := waitForMethod(t, out, NotificationAgentMessageDelta)
	delta := remarshal[AgentMessageDeltaNotification](t, notificationByMethod(t, deltaMessages, NotificationAgentMessageDelta)["params"])
	if delta.Delta != "partial answer" {
		t.Fatalf("live partial delta = %q", delta.Delta)
	}

	interrupt := fmt.Sprintf(`{"id":"interrupt","method":"turn/interrupt","params":{"thread_id":%q}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(interrupt)); err != nil {
		t.Fatalf("turn/interrupt: %v", err)
	}
	waitForMethod(t, out, NotificationTurnError)

	reloadOut := &lockedBuffer{}
	reloaded := New(rt, reloadOut)
	resume := fmt.Sprintf(`{"id":"reload","method":"thread/resume","params":{"session_id":%q}}`, threadID)
	if err := reloaded.handleLine(context.Background(), []byte(resume)); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}
	result := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, reloadOut.String()), "reload")["result"])
	if len(result.Thread.Turns) != 1 {
		t.Fatalf("reloaded turns = %+v, want one interrupted turn", result.Thread.Turns)
	}
	turn := result.Thread.Turns[0]
	if turn.Status != TurnStatusInterrupted || turn.Error == nil || !strings.Contains(turn.Error.Message, context.Canceled.Error()) || turn.CompletedAt == nil {
		t.Fatalf("reloaded turn did not restore interruption: %+v", turn)
	}
	if len(turn.Items) < 3 || turn.Items[1].Type != ThreadItemAgentMessage || turn.Items[1].Text != "partial answer" || turn.Items[len(turn.Items)-1].Type != ThreadItemError {
		t.Fatalf("reloaded partial turn items = %+v", turn.Items)
	}
}

func TestServerRetriedInterruptedTurnReloadsCompleted(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := session.CreateWithMetadata(rt.SessionDir, "20260716-000002-retried", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := appendChatMessage(rt.SessionDir, sess.ID, providers.ChatMessage{Role: "user", Content: "inspect"}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if _, err := appendChatMessage(rt.SessionDir, sess.ID, providers.ChatMessage{Role: "assistant", Content: "partial answer"}); err != nil {
		t.Fatalf("append partial assistant: %v", err)
	}

	srv := New(rt, &lockedBuffer{})
	th := newThreadState(sess.ID, nil, rt.ProviderName, rt.Model, rt.RootDir, true, time.Now().UTC())
	turnID := sess.ID + "-turn-0001"
	if err := srv.persistTurnTerminal(th, turnID, TurnKindUser, TurnStatusInterrupted, context.Canceled, time.Now().UTC(), nil); err != nil {
		t.Fatalf("persist interrupted terminal: %v", err)
	}
	if _, err := appendChatMessage(rt.SessionDir, sess.ID, providers.ChatMessage{Role: "assistant", Content: "final answer"}); err != nil {
		t.Fatalf("append final assistant: %v", err)
	}
	completedAt := time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC)
	if err := srv.persistTurnTerminal(th, turnID, TurnKindUser, TurnStatusCompleted, nil, completedAt, nil); err != nil {
		t.Fatalf("persist completed terminal: %v", err)
	}

	reloadOut := &lockedBuffer{}
	reloaded := New(rt, reloadOut)
	resume := fmt.Sprintf(`{"id":"reload","method":"thread/resume","params":{"session_id":%q}}`, sess.ID)
	if err := reloaded.handleLine(context.Background(), []byte(resume)); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}
	result := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, reloadOut.String()), "reload")["result"])
	if len(result.Thread.Turns) != 1 {
		t.Fatalf("reloaded turns = %+v, want one completed turn", result.Thread.Turns)
	}
	turn := result.Thread.Turns[0]
	if turn.Status != TurnStatusCompleted || turn.Error != nil || turn.CompletedAt == nil || !turn.CompletedAt.Equal(completedAt) {
		t.Fatalf("reloaded retry did not supersede interrupted terminal: %+v", turn)
	}
	for _, item := range turn.Items {
		if item.Type == ThreadItemError {
			t.Fatalf("completed retry retained stale error item: %+v", turn.Items)
		}
	}
}

func TestServerFailedInternalTurnReloadsOnVisibleAggregate(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := session.CreateWithMetadata(rt.SessionDir, "20260716-000003-internal", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, msg := range []providers.ChatMessage{
		{Role: "user", Content: "start goal work"},
		{Role: "assistant", Content: "initial answer"},
		{Role: "assistant", Content: "partial continuation"},
	} {
		if _, err := appendChatMessage(rt.SessionDir, sess.ID, msg); err != nil {
			t.Fatalf("append %s message: %v", msg.Role, err)
		}
	}

	srv := New(rt, &lockedBuffer{})
	th := newThreadState(sess.ID, nil, rt.ProviderName, rt.Model, rt.RootDir, true, time.Now().UTC())
	failedAt := time.Date(2026, 7, 16, 11, 30, 0, 0, time.UTC)
	if err := srv.persistTurnTerminal(th, session.NewID(), TurnKindInternal, TurnStatusFailed, errors.New("goal continuation failed"), failedAt, nil); err != nil {
		t.Fatalf("persist internal terminal: %v", err)
	}

	reloadOut := &lockedBuffer{}
	reloaded := New(rt, reloadOut)
	resume := fmt.Sprintf(`{"id":"reload","method":"thread/resume","params":{"session_id":%q}}`, sess.ID)
	if err := reloaded.handleLine(context.Background(), []byte(resume)); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}
	result := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, reloadOut.String()), "reload")["result"])
	if len(result.Thread.Turns) != 1 {
		t.Fatalf("reloaded turns = %+v, want one aggregate turn", result.Thread.Turns)
	}
	turn := result.Thread.Turns[0]
	if turn.Status != TurnStatusFailed || turn.Error == nil || turn.Error.Message != "goal continuation failed" || turn.CompletedAt == nil || !turn.CompletedAt.Equal(failedAt) {
		t.Fatalf("reloaded aggregate did not restore internal failure: %+v", turn)
	}
	if len(turn.Items) < 4 || turn.Items[2].Type != ThreadItemAgentMessage || turn.Items[2].Text != "partial continuation" || turn.Items[len(turn.Items)-1].Type != ThreadItemError {
		t.Fatalf("reloaded internal turn items = %+v", turn.Items)
	}
}

func TestServerSettledStreamReconnectSurvivesReload(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := session.CreateWithMetadata(rt.SessionDir, "20260716-000005-reconnect", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := appendChatMessage(rt.SessionDir, sess.ID, providers.ChatMessage{Role: "user", Content: "inspect"}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if _, err := appendChatMessage(rt.SessionDir, sess.ID, providers.ChatMessage{Role: "assistant", Content: "partial answer"}); err != nil {
		t.Fatalf("append partial assistant: %v", err)
	}

	srv := New(rt, &lockedBuffer{})
	th := newThreadState(sess.ID, nil, rt.ProviderName, rt.Model, rt.RootDir, true, time.Now().UTC())
	turnID := sess.ID + "-turn-0001"
	reconnect := &ThreadItem{
		Type:       ThreadItemStreamReconnect,
		Status:     ThreadItemStatusFailed,
		Text:       "connection reset by peer",
		Reason:     "network",
		RetryCount: 5,
		MaxRetries: 5,
	}
	interruptedAt := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	if err := srv.persistTurnTerminal(th, turnID, TurnKindUser, TurnStatusInterrupted, errors.New("stream interrupted"), interruptedAt, reconnect); err != nil {
		t.Fatalf("persist interrupted terminal with reconnect: %v", err)
	}

	reloadOut := &lockedBuffer{}
	reloaded := New(rt, reloadOut)
	resume := fmt.Sprintf(`{"id":"reload","method":"thread/resume","params":{"session_id":%q}}`, sess.ID)
	if err := reloaded.handleLine(context.Background(), []byte(resume)); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}
	result := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, reloadOut.String()), "reload")["result"])
	if len(result.Thread.Turns) != 1 {
		t.Fatalf("reloaded turns = %+v, want one turn", result.Thread.Turns)
	}
	turn := result.Thread.Turns[0]
	var reconnectItem *ThreadItem
	for i := range turn.Items {
		if turn.Items[i].Type == ThreadItemStreamReconnect {
			reconnectItem = &turn.Items[i]
		}
	}
	if reconnectItem == nil {
		t.Fatalf("reloaded turn lost the settled reconnect row: %+v", turn.Items)
	}
	if reconnectItem.Status != ThreadItemStatusFailed || reconnectItem.Text != "connection reset by peer" || reconnectItem.Reason != "network" || reconnectItem.RetryCount != 5 || reconnectItem.MaxRetries != 5 {
		t.Fatalf("reloaded reconnect row = %+v, want failed network 5/5", reconnectItem)
	}
}

func TestServerCompletedTurnSkipsStreamReconnectRecord(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := session.CreateWithMetadata(rt.SessionDir, "20260716-000006-reconnect-skip", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	srv := New(rt, &lockedBuffer{})
	th := newThreadState(sess.ID, nil, rt.ProviderName, rt.Model, rt.RootDir, true, time.Now().UTC())
	reconnect := &ThreadItem{
		Type:       ThreadItemStreamReconnect,
		Status:     ThreadItemStatusFailed,
		Text:       "connection reset by peer",
		Reason:     "network",
		RetryCount: 1,
		MaxRetries: 5,
	}
	if err := srv.persistTurnTerminal(th, sess.ID+"-turn-0001", TurnKindUser, TurnStatusCompleted, nil, time.Now().UTC(), reconnect); err != nil {
		t.Fatalf("persist completed terminal: %v", err)
	}
	metas, err := loadMetaMessages(rt.SessionDir, sess.ID)
	if err != nil {
		t.Fatalf("load meta messages: %v", err)
	}
	for _, meta := range metas {
		if meta.Content == streamReconnectHistoryRecord {
			t.Fatalf("a recovered turn must not persist a reconnect record: %+v", metas)
		}
	}
}

func TestPersistFailedTurnResultRecordsUsageForNonPersistentThread(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := session.CreateWithMetadata(rt.SessionDir, "20260716-000004-ephemeral", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	srv := New(rt, &lockedBuffer{})
	th := newThreadState(sess.ID, nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	res := agent.LoopResult{
		NewMessages:   []providers.ChatMessage{{Role: "assistant", Content: "partial answer"}},
		InputTokens:   13,
		OutputTokens:  5,
		ContextTokens: 21,
	}
	if err := srv.persistFailedTurnResultLocked(th, res, false, rt.ProviderName, rt.Model, 0); err != nil {
		t.Fatalf("persist turn result: %v", err)
	}
	metas, err := loadMetaMessages(rt.SessionDir, sess.ID)
	if err != nil {
		t.Fatalf("load meta messages: %v", err)
	}
	if len(metas) != 1 || metas[0].Content != "token_usage" || metas[0].InputTokens != 13 || metas[0].OutputTokens != 5 || metas[0].ContextTokens != 21 {
		t.Fatalf("non-persistent turn usage was not recorded: %+v", metas)
	}
}

func TestServerFailedTurnPersistsCompactedHistoryRewrite(t *testing.T) {
	client := &fakeClient{
		responses: []providers.ChatResponse{
			{
				ToolCalls: []providers.ToolCall{{
					ID:        "call_1",
					Name:      "grep",
					Arguments: `{"pattern":"inspect","path":"."}`,
				}},
				Usage: &providers.TokenUsage{InputTokens: 4950, OutputTokens: 10},
			},
			{Content: "compacted before provider failure"},
		},
	}
	client.onChat = func(call int, _ providers.ChatRequest) {
		if call == 2 {
			client.mu.Lock()
			client.err = fmt.Errorf("provider unavailable after compact")
			client.mu.Unlock()
		}
	}
	rt := newTestRuntime(t, client)
	rt.StreamRunner.ContextWindowOverride = 5000
	rt.StreamRunner.OutputReserveTokens = 100
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("new toolkit: %v", err)
	}
	rt.Toolkit = kit
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	thread := srv.thread(threadID)
	olderHistory := []providers.ChatMessage{
		{Role: "user", Content: strings.Repeat("older request ", 400)},
		{Role: "assistant", Content: strings.Repeat("older answer ", 400)},
	}
	if err := appendChatMessages(rt.SessionDir, threadID, olderHistory); err != nil {
		t.Fatalf("persist older history: %v", err)
	}
	thread.mu.Lock()
	thread.History = append(thread.History, olderHistory...)
	thread.mu.Unlock()
	raw, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "inspect"},
	})
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	msgs := waitForMethod(t, out, NotificationTurnError)
	failed := remarshal[TurnErrorNotification](t, notificationByMethod(t, msgs, NotificationTurnError)["params"])
	if failed.Turn.Status != TurnStatusFailed {
		t.Fatalf("turn status = %q, want failed", failed.Turn.Status)
	}

	persisted, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	for _, msg := range persisted {
		if msg.Role == "system" &&
			compact.IsConversationSummaryContent(msg.Content) &&
			strings.Contains(msg.Content, "compacted before provider failure") {
			return
		}
	}
	t.Fatalf("failed turn should persist the successful compact rewrite, got %+v", persisted)
}

func TestServerTurnItemsIncludeReasoningAndAgentMessage(t *testing.T) {
	client := &fakeClient{
		response: providers.ChatResponse{
			ReasoningContent: "inspect first",
			Content:          "done",
		},
	}
	rt := newTestRuntime(t, client)
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	raw, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "hello"},
	})
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	msgs := waitForMethod(t, out, NotificationTurnCompleted)
	completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, msgs, NotificationTurnCompleted)["params"])
	var reasoning, agent int
	for _, item := range completed.Turn.Items {
		switch item.Type {
		case ThreadItemReasoning:
			reasoning++
			if item.Text != "inspect first" {
				t.Fatalf("unexpected reasoning item: %+v", item)
			}
		case ThreadItemAgentMessage:
			agent++
			if item.Text != "done" {
				t.Fatalf("unexpected agent item: %+v", item)
			}
		}
	}
	if reasoning != 1 || agent != 1 {
		t.Fatalf("expected one reasoning and one agent item, got reasoning=%d agent=%d turn=%+v", reasoning, agent, completed.Turn)
	}
}

func TestServerThreadResumeLoadsSessionHistory(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sessionID := "20260523-000000-test"
	sess, err := session.CreateWithMetadata(rt.SessionDir, sessionID, rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := rewriteChatHistory(rt.SessionDir, sess.ID, []providers.ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "done"},
	}); err != nil {
		t.Fatalf("write session: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	payload := map[string]any{
		"id":     "1",
		"method": MethodThreadResume,
		"params": ThreadResumeParams{SessionID: sessionID},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal resume request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[ThreadResumeResult](t, responseByID(t, msgs, "1")["result"])
	if result.Thread.ID != sessionID || len(result.Thread.Turns) != 1 {
		t.Fatalf("unexpected resume result: %+v", result)
	}
	if turn := result.Thread.Turns[0]; turn.StartedAt != nil || turn.CompletedAt != nil || turn.DurationMS != nil {
		t.Fatalf("historical turn should leave unknown timing unset: %+v", turn)
	}
	resumed := remarshal[ThreadResumedNotification](t, notificationByMethod(t, msgs, NotificationThreadResumed)["params"])
	if resumed.Thread.ID != sessionID || len(resumed.Thread.Turns) != 1 {
		t.Fatalf("unexpected resume notification: %+v", resumed)
	}

	th := srv.thread(sessionID)
	if th == nil {
		t.Fatal("expected resumed thread")
	}
	if len(th.History) != 3 || th.History[1].Role != "user" || th.History[1].Content != "hello" {
		t.Fatalf("unexpected resumed history: %+v", th.History)
	}
}

func TestServerThreadResumeDisplaysHistoryBeforeProviderCheckpoint(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sessionID := "20260523-000001-checkpoint-history"
	if _, err := session.CreateWithMetadata(rt.SessionDir, sessionID, rt.RootDir); err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, rec := range []session.HistoryRecord{
		{Role: "user", Content: "older question"},
		{Role: "assistant", Content: "older answer"},
		{Role: "user", Content: "recent question"},
		{Role: "assistant", Content: "recent answer"},
	} {
		if err := session.AppendHistoryRecord(rt.SessionDir, sessionID, rec); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}
	if _, err := session.StoreHistoryCheckpointAtBaseline(
		rt.SessionDir,
		sessionID,
		session.HistoryCheckpointKindProviderRewrite,
		[]session.HistoryRecord{
			{Seq: 3, Role: "user", Content: "recent question"},
			{Seq: 4, Role: "assistant", Content: "recent answer"},
		},
		4,
	); err != nil {
		t.Fatalf("store provider checkpoint: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	payload := map[string]any{
		"id":     "1",
		"method": MethodThreadResume,
		"params": ThreadResumeParams{SessionID: sessionID},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal resume request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[ThreadResumeResult](t, responseByID(t, msgs, "1")["result"])
	if len(result.Thread.Turns) != 2 {
		t.Fatalf("resume displayed %d turns, want complete two-turn transcript: %+v", len(result.Thread.Turns), result.Thread.Turns)
	}
	if got := result.Thread.Turns[0].Items[0].Text; got != "older question" {
		t.Fatalf("first displayed question = %q, want older question", got)
	}
	cached := srv.thread(sessionID)
	if cached == nil {
		t.Fatal("expected cached thread")
	}
	cached.mu.Lock()
	providerHistory := append([]providers.ChatMessage(nil), cached.History...)
	cached.mu.Unlock()
	if len(providerHistory) != 3 || providerHistory[1].Content != "recent question" {
		t.Fatalf("provider history should stay checkpointed, got %+v", providerHistory)
	}
}

func TestSQLiteHistoryRoundTripsMessagePayloads(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := session.CreateWithMetadata(rt.SessionDir, "20260523-000010-payloads", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	msg := providers.ChatMessage{
		Role:                 "assistant",
		ClientID:             "client-msg-1",
		Content:              "done",
		Phase:                providers.MessagePhaseFinalAnswer,
		Hidden:               true,
		ProviderItemID:       "msg_1",
		ProviderItemProvider: "openai-gateway",
		ProviderItemModel:    "gpt-test",
		Steered:              true,
		ReasoningContent:     "inspect before answering",
		ReasoningBlocks: []providers.ReasoningBlock{{
			Type:      "thinking",
			Thinking:  "step one",
			Signature: "sig-1",
			Data:      "opaque",
		}},
		Images: []providers.InputImage{{
			MediaType: "image/png",
			Data:      "image-data",
			Width:     640,
			Height:    480,
		}},
		Files: []providers.InputFile{{
			MediaType: "application/pdf",
			Data:      "file-data",
			Filename:  "brief.pdf",
		}},
		ToolCalls: []providers.ToolCall{{
			ID:                   "call_1",
			ProviderItemID:       "fc_1",
			ProviderItemProvider: "openai-gateway",
			ProviderItemModel:    "gpt-test",
			Name:                 "read_file",
			Arguments:            `{"path":"README.md"}`,
			Display:              &providers.ToolCallDisplay{Kind: "read", Text: "README.md"},
		}},
		DiscoveredTools: []providers.LoadableToolDefinition{{
			Type:        "function",
			Name:        "mcp_docs_search",
			Description: "Search docs",
			InputSchema: map[string]any{"type": "object"},
		}},
	}
	if _, err := appendChatMessage(rt.SessionDir, sess.ID, msg); err != nil {
		t.Fatalf("append message: %v", err)
	}

	history, err := loadChatMessages(rt.SessionDir, sess.ID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected one message, got %+v", history)
	}
	got := history[0]
	if got.Role != msg.Role || got.ClientID != msg.ClientID || got.Content != msg.Content || got.Phase != msg.Phase || !got.Hidden || got.ProviderItemID != "msg_1" || got.ProviderItemProvider != "openai-gateway" || got.ProviderItemModel != "gpt-test" || !got.Steered || got.ReasoningContent != msg.ReasoningContent {
		t.Fatalf("message scalar fields did not round-trip: %+v", got)
	}
	if len(got.ReasoningBlocks) != 1 || got.ReasoningBlocks[0].Signature != "sig-1" || got.ReasoningBlocks[0].Data != "opaque" {
		t.Fatalf("reasoning blocks did not round-trip: %+v", got.ReasoningBlocks)
	}
	if len(got.Images) != 1 || got.Images[0].MediaType != "image/png" || got.Images[0].Data != "image-data" || got.Images[0].Width != 640 || got.Images[0].Height != 480 {
		t.Fatalf("images did not round-trip: %+v", got.Images)
	}
	if len(got.Files) != 1 || got.Files[0].MediaType != "application/pdf" || got.Files[0].Data != "file-data" || got.Files[0].Filename != "brief.pdf" {
		t.Fatalf("files did not round-trip: %+v", got.Files)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "call_1" || got.ToolCalls[0].ProviderItemID != "fc_1" || got.ToolCalls[0].ProviderItemProvider != "openai-gateway" || got.ToolCalls[0].ProviderItemModel != "gpt-test" || got.ToolCalls[0].Display == nil || got.ToolCalls[0].Display.Text != "README.md" {
		t.Fatalf("tool calls did not round-trip: %+v", got.ToolCalls)
	}
	if len(got.DiscoveredTools) != 1 || got.DiscoveredTools[0].Name != "mcp_docs_search" || got.DiscoveredTools[0].InputSchema["type"] != "object" {
		t.Fatalf("discovered tools did not round-trip: %+v", got.DiscoveredTools)
	}
}

func TestSQLiteRewriteChatHistoryReplacesMessagesAndPreservesTokenUsage(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := session.CreateWithMetadata(rt.SessionDir, "20260523-000011-rewrite", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if _, err := appendChatMessage(rt.SessionDir, sess.ID, providers.ChatMessage{Role: "user", Content: "old user"}); err != nil {
		t.Fatalf("append old user: %v", err)
	}
	if _, err := appendChatMessage(rt.SessionDir, sess.ID, providers.ChatMessage{Role: "assistant", Content: "old assistant"}); err != nil {
		t.Fatalf("append old assistant: %v", err)
	}
	if err := appendTokenUsage(rt.SessionDir, sess.ID, "anthropic", "claude-sonnet-4-6", providers.TokenUsage{InputTokens: 11, OutputTokens: 7, CacheCreationTokens: 5, CacheReadTokens: 3}, 18); err != nil {
		t.Fatalf("append token usage: %v", err)
	}

	if err := rewriteChatHistory(rt.SessionDir, sess.ID, []providers.ChatMessage{{Role: "user", Content: "new user"}}); err != nil {
		t.Fatalf("rewrite history: %v", err)
	}

	visible, err := loadChatMessages(rt.SessionDir, sess.ID)
	if err != nil {
		t.Fatalf("load visible history: %v", err)
	}
	if len(visible) != 1 || visible[0].Role != "user" || visible[0].Content != "new user" {
		t.Fatalf("rewrite should replace old visible messages, got %+v", visible)
	}
	all, err := session.LoadHistoryRecords(rt.SessionDir, sess.ID, true)
	if err != nil {
		t.Fatalf("load raw history: %v", err)
	}
	if len(all) != 2 || all[1].Role != "meta" || all[1].InputTokens != 11 || all[1].OutputTokens != 7 || all[1].CacheCreationTokens != 5 || all[1].CacheReadTokens != 3 {
		t.Fatalf("rewrite should preserve token usage metadata, got %+v", all)
	}
	if all[1].Provider != "anthropic" || all[1].Model != "claude-sonnet-4-6" {
		t.Fatalf("rewrite should preserve token usage metadata, got provider=%q model=%q", all[1].Provider, all[1].Model)
	}
}

func TestServerThreadResumeRestoresTurnTokenUsage(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := session.CreateWithMetadata(rt.SessionDir, "20260523-000012-usage", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := appendChatMessage(rt.SessionDir, sess.ID, providers.ChatMessage{Role: "user", Content: "inspect"}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if _, err := appendChatMessage(rt.SessionDir, sess.ID, providers.ChatMessage{Role: "assistant", Content: "done"}); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	if err := appendTokenUsage(rt.SessionDir, sess.ID, rt.ProviderName, rt.Model, providers.TokenUsage{InputTokens: 19_600, CacheReadTokens: 113_000}, 88_000); err != nil {
		t.Fatalf("append token usage: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	req := fmt.Sprintf(`{"id":"1","method":"thread/resume","params":{"session_id":%q}}`, sess.ID)
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}
	result := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if len(result.Thread.Turns) != 1 {
		t.Fatalf("expected one turn, got %+v", result.Thread.Turns)
	}
	turn := result.Thread.Turns[0]
	if turn.InputTokens != 19_600 || turn.CacheReadTokens != 113_000 || turn.ContextTokens != 88_000 || turn.UsageModel != rt.Model {
		t.Fatalf("resume should restore token usage on turn: %+v", turn)
	}
}

func TestServerCompactedTurnPersistsAndResumes(t *testing.T) {
	client := &fakeClient{
		responses: []providers.ChatResponse{
			{Content: "summary of older single-turn tool run"},
			{Content: "after compact"},
		},
	}
	rt := newTestRuntime(t, client)
	rt.StreamRunner.Model = "gpt-4-turbo"
	rt.StreamRunner.ContextWindowOverride = 5000
	rt.StreamRunner.CompactKeepRecentTokens = 1000

	if err := os.MkdirAll(rt.SessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sessionID := "20260527-000000-compact"
	sess, err := session.CreateWithMetadata(rt.SessionDir, sessionID, rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	largeToolOutput := strings.Repeat("large output ", 1200)
	initialHistory := []providers.ChatMessage{
		{Role: "user", Content: "debug the failing workbench request"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "call_1", Name: "run_shell", Arguments: `{"command":"rg ContextOverflow"}`},
		}},
		{Role: "tool", Name: "run_shell", ToolCallID: "call_1", Content: largeToolOutput},
		{Role: "assistant", Content: "I found the first clue."},
	}
	if err := rewriteChatHistory(rt.SessionDir, sess.ID, initialHistory); err != nil {
		t.Fatalf("write initial history: %v", err)
	}
	if err := session.UpdateIndex(rt.SessionDir, sess.ID, persistableMessageCount(initialHistory), threadPreview(initialHistory)); err != nil {
		t.Fatalf("update index: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	rawResume, err := json.Marshal(map[string]any{
		"id":     "resume-1",
		"method": MethodThreadResume,
		"params": ThreadResumeParams{SessionID: sess.ID},
	})
	if err != nil {
		t.Fatalf("marshal resume: %v", err)
	}
	if err := srv.handleLine(context.Background(), rawResume); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}

	rawTurn, err := json.Marshal(map[string]any{
		"id":     "turn-1",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: sess.ID, Prompt: "continue"},
	})
	if err != nil {
		t.Fatalf("marshal turn: %v", err)
	}
	if err := srv.handleLine(context.Background(), rawTurn); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	msgs := waitForMethod(t, out, NotificationTurnCompleted)
	completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, msgs, NotificationTurnCompleted)["params"])
	if completed.Content != "after compact" || completed.Turn.Status != TurnStatusCompleted {
		t.Fatalf("unexpected turn completion: %+v", completed)
	}
	if turnEventByType(t, msgs, providers.EventCompact) == nil {
		t.Fatal("expected compact event during resumed turn")
	}

	persisted, err := loadChatMessages(rt.SessionDir, sess.ID)
	if err != nil {
		t.Fatalf("load compacted history: %v", err)
	}
	visiblePersisted := visibleMessagesForTest(persisted)
	if len(visiblePersisted) != 4 {
		t.Fatalf("expected compacted persisted history of 4 messages, got %+v", persisted)
	}
	if visiblePersisted[0].Role != "system" || !strings.Contains(visiblePersisted[0].Content, "summary of older single-turn tool run") {
		t.Fatalf("expected persisted compact summary first, got %+v", visiblePersisted[0])
	}
	if visiblePersisted[1].Role != "assistant" || visiblePersisted[1].Content != "I found the first clue." {
		t.Fatalf("expected recent assistant tail after summary, got %+v", visiblePersisted[1])
	}
	if visiblePersisted[2].Role != "user" || visiblePersisted[2].Content != "continue" {
		t.Fatalf("expected resumed user message after recent tail, got %+v", visiblePersisted[2])
	}
	if visiblePersisted[3].Role != "assistant" || visiblePersisted[3].Content != "after compact" {
		t.Fatalf("expected final assistant message after compact, got %+v", visiblePersisted[3])
	}

	out2 := &lockedBuffer{}
	resumedSrv := New(rt, out2)
	rawResume2, err := json.Marshal(map[string]any{
		"id":     "resume-2",
		"method": MethodThreadResume,
		"params": ThreadResumeParams{SessionID: sess.ID},
	})
	if err != nil {
		t.Fatalf("marshal second resume: %v", err)
	}
	if err := resumedSrv.handleLine(context.Background(), rawResume2); err != nil {
		t.Fatalf("second thread/resume: %v", err)
	}
	resumeMsgs := parseOutput(t, out2.String())
	result := remarshal[ThreadResumeResult](t, responseByID(t, resumeMsgs, "resume-2")["result"])
	if result.Thread.ID != sess.ID || len(result.Thread.Turns) != 2 {
		t.Fatalf("unexpected resumed compacted thread: %+v", result.Thread)
	}
	if result.Thread.Turns[0].Items[0].Text != "debug the failing workbench request" {
		t.Fatalf("expected compacted transcript to retain the older user request: %+v", result.Thread.Turns[0])
	}
	foundCompaction := false
	for _, turn := range result.Thread.Turns {
		for _, item := range turn.Items {
			if item.Type == ThreadItemContextCompaction {
				foundCompaction = true
			}
			if item.Type == ThreadItemToolCall {
				t.Fatalf("compacted transcript restored an obsolete tool payload: %+v", turn)
			}
		}
	}
	if !foundCompaction {
		t.Fatalf("expected resumed transcript to include a context compaction item: %+v", result.Thread.Turns)
	}
	th := resumedSrv.thread(sess.ID)
	if th == nil {
		t.Fatal("expected resumed compacted thread state")
	}
	visibleHistory := visibleMessagesForTest(th.History)
	if len(visibleHistory) != 5 {
		t.Fatalf("expected base system prompt plus compacted persisted history, got %+v", th.History)
	}
	if visibleHistory[1].Role != "system" || !strings.Contains(visibleHistory[1].Content, "summary of older single-turn tool run") {
		t.Fatalf("expected compact summary after base system prompt, got %+v", th.History)
	}
}

func TestServerThreadCompactStartRunsCompactOnlyTurn(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "manual compact summary"}}
	rt := newTestRuntime(t, client)
	rt.StreamRunner.CompactKeepRecentTokens = 1
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	th := srv.thread(threadID)
	if th == nil {
		t.Fatal("expected loaded thread")
	}
	compactHistory := []providers.ChatMessage{
		providers.ChatMessage{Role: "user", Content: strings.Repeat("old request ", 100)},
		providers.ChatMessage{Role: "assistant", Content: strings.Repeat("old answer ", 100)},
		providers.ChatMessage{Role: "user", Content: strings.Repeat("newer request ", 100)},
		providers.ChatMessage{Role: "assistant", Content: strings.Repeat("newer answer ", 100)},
	}
	if err := appendChatMessages(rt.SessionDir, threadID, compactHistory); err != nil {
		t.Fatalf("persist compact history: %v", err)
	}
	th.mu.Lock()
	th.History = append(th.History, compactHistory...)
	th.mu.Unlock()

	raw, err := json.Marshal(map[string]any{
		"id":     "compact-1",
		"method": MethodThreadCompactStart,
		"params": ThreadCompactStartParams{ThreadID: threadID},
	})
	if err != nil {
		t.Fatalf("marshal compact request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("thread/compact/start: %v", err)
	}
	started := remarshal[ThreadCompactStartResult](t, responseByID(t, parseOutput(t, out.String()), "compact-1")["result"])
	if started.Turn.Kind != TurnKindCompact || started.Turn.Status != TurnStatusInProgress || len(started.Turn.Items) != 2 {
		t.Fatalf("compact start should return a compact turn with the user command and progress items, got %+v", started.Turn)
	}
	if started.Turn.Items[0].Type != ThreadItemUserMessage || started.Turn.Items[0].Text != "/compact" {
		t.Fatalf("compact turn should display the triggering slash command, got %+v", started.Turn.Items)
	}
	if started.Turn.Items[1].Type != ThreadItemContextCompaction || started.Turn.Items[1].Status != ThreadItemStatusInProgress || started.Turn.Items[1].Reason != "manual" {
		t.Fatalf("compact turn should immediately display an in-progress compaction item, got %+v", started.Turn.Items)
	}

	msgs := waitForMethod(t, out, NotificationTurnCompleted)
	completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, msgs, NotificationTurnCompleted)["params"])
	if completed.Turn.Kind != TurnKindCompact || completed.Turn.Status != TurnStatusCompleted {
		t.Fatalf("compact completion should keep compact kind, got %+v", completed.Turn)
	}
	if completed.Content != "" {
		t.Fatalf("compact-only turn must not produce a normal assistant response, got %q", completed.Content)
	}
	if len(completed.Turn.Items) == 0 || completed.Turn.Items[0].Type != ThreadItemUserMessage || completed.Turn.Items[0].Text != "/compact" {
		t.Fatalf("completed compact turn should retain user command item, got %+v", completed.Turn.Items)
	}
	if len(completed.Turn.Items) < 2 || completed.Turn.Items[1].Type != ThreadItemContextCompaction || completed.Turn.Items[1].Status != ThreadItemStatusCompleted || completed.Turn.Items[1].Reason != "manual" {
		t.Fatalf("completed compact turn should update the progress item to completed, got %+v", completed.Turn.Items)
	}
	for _, item := range completed.Turn.Items {
		if item.Type == ThreadItemAgentMessage {
			t.Fatalf("compact turn must not contain assistant chat items, got %+v", completed.Turn.Items)
		}
	}
	assertFakeClientRequestCount(t, client, 1)

	persisted, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("load compacted history: %v", err)
	}
	visible := visibleMessagesForTest(persisted)
	if len(visible) == 0 || visible[0].Role != "system" || !strings.Contains(visible[0].Content, "manual compact summary") {
		t.Fatalf("expected compact summary to be persisted first, got %+v", visible)
	}
	for _, msg := range visible {
		if msg.Role == "user" && strings.HasPrefix(strings.TrimSpace(msg.Content), "/compact") {
			t.Fatalf("compact control command should not be persisted as a user message, got %+v", visible)
		}
	}
}

func TestServerThreadCompactStartMarksFailedCompactItem(t *testing.T) {
	client := &fakeClient{err: fmt.Errorf("compact exploded")}
	rt := newTestRuntime(t, client)
	rt.StreamRunner.CompactKeepRecentTokens = 1
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	th := srv.thread(threadID)
	if th == nil {
		t.Fatal("expected loaded thread")
	}
	compactHistory := []providers.ChatMessage{
		providers.ChatMessage{Role: "user", Content: "old request"},
		providers.ChatMessage{Role: "assistant", Content: "old answer"},
		providers.ChatMessage{Role: "user", Content: "newer request"},
		providers.ChatMessage{Role: "assistant", Content: "newer answer"},
	}
	if err := appendChatMessages(rt.SessionDir, threadID, compactHistory); err != nil {
		t.Fatalf("persist compact history: %v", err)
	}
	th.mu.Lock()
	th.History = append(th.History, compactHistory...)
	th.mu.Unlock()

	raw, err := json.Marshal(map[string]any{
		"id":     "compact-failed",
		"method": MethodThreadCompactStart,
		"params": ThreadCompactStartParams{ThreadID: threadID},
	})
	if err != nil {
		t.Fatalf("marshal compact request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("thread/compact/start: %v", err)
	}
	started := remarshal[ThreadCompactStartResult](t, responseByID(t, parseOutput(t, out.String()), "compact-failed")["result"])
	if len(started.Turn.Items) < 2 || started.Turn.Items[1].Type != ThreadItemContextCompaction || started.Turn.Items[1].Status != ThreadItemStatusInProgress {
		t.Fatalf("compact start should display an in-progress item, got %+v", started.Turn.Items)
	}

	msgs := waitForMethod(t, out, NotificationTurnCompleted)
	completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, msgs, NotificationTurnCompleted)["params"])
	if completed.Turn.Kind != TurnKindCompact || completed.Turn.Status != TurnStatusCompleted {
		t.Fatalf("failed compact attempt should still complete the compact-only control turn, got %+v", completed.Turn)
	}
	if len(completed.Turn.Items) < 2 || completed.Turn.Items[1].Type != ThreadItemContextCompaction || completed.Turn.Items[1].Status != ThreadItemStatusFailed {
		t.Fatalf("failed compact should update the progress item to failed, got %+v", completed.Turn.Items)
	}
	if !strings.Contains(completed.Turn.Items[1].Text, "failed") {
		t.Fatalf("failed compact item should keep the failure notice, got %+v", completed.Turn.Items[1])
	}
	assertFakeClientRequestCount(t, client, 1)
}

func TestServerTurnStartSlashCompactRoutesToCompactOnlyTurn(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "slash compact summary"}}
	rt := newTestRuntime(t, client)
	rt.StreamRunner.CompactKeepRecentTokens = 1
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	th := srv.thread(threadID)
	if th == nil {
		t.Fatal("expected loaded thread")
	}
	compactHistory := []providers.ChatMessage{
		providers.ChatMessage{Role: "user", Content: strings.Repeat("old request ", 100)},
		providers.ChatMessage{Role: "assistant", Content: strings.Repeat("old answer ", 100)},
		providers.ChatMessage{Role: "user", Content: strings.Repeat("newer request ", 100)},
	}
	if err := appendChatMessages(rt.SessionDir, threadID, compactHistory); err != nil {
		t.Fatalf("persist compact history: %v", err)
	}
	th.mu.Lock()
	th.History = append(th.History, compactHistory...)
	th.mu.Unlock()

	raw, err := json.Marshal(map[string]any{
		"id":     "compact-compat",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "/compact 后续只保留结论"},
	})
	if err != nil {
		t.Fatalf("marshal turn/start request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start /compact: %v", err)
	}
	resp := responseByID(t, parseOutput(t, out.String()), "compact-compat")
	if resp["error"] != nil {
		t.Fatalf("turn/start /compact returned error: %+v", resp["error"])
	}
	started := remarshal[TurnStartResult](t, resp["result"])
	if started.Turn.Kind != TurnKindCompact || len(started.Turn.Items) != 2 {
		t.Fatalf("turn/start /compact should return compact control turn, got %+v", started.Turn)
	}
	if started.Turn.Items[0].Type != ThreadItemUserMessage || started.Turn.Items[0].Text != "/compact 后续只保留结论" {
		t.Fatalf("turn/start /compact should display the user's raw slash command, got %+v", started.Turn.Items)
	}
	if started.Turn.Items[1].Type != ThreadItemContextCompaction || started.Turn.Items[1].Status != ThreadItemStatusInProgress {
		t.Fatalf("turn/start /compact should immediately display a compaction progress item, got %+v", started.Turn.Items)
	}

	msgs := waitForMethod(t, out, NotificationTurnCompleted)
	completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, msgs, NotificationTurnCompleted)["params"])
	if completed.Turn.Kind != TurnKindCompact || completed.Content != "" {
		t.Fatalf("slash compact should complete without normal assistant response, got %+v", completed)
	}
	if len(completed.Turn.Items) == 0 || completed.Turn.Items[0].Type != ThreadItemUserMessage || completed.Turn.Items[0].Text != "/compact 后续只保留结论" {
		t.Fatalf("completed slash compact should retain raw command item, got %+v", completed.Turn.Items)
	}
	if len(completed.Turn.Items) < 2 || completed.Turn.Items[1].Type != ThreadItemContextCompaction || completed.Turn.Items[1].Status != ThreadItemStatusCompleted {
		t.Fatalf("completed slash compact should update the progress item to completed, got %+v", completed.Turn.Items)
	}
	assertFakeClientRequestCount(t, client, 1)

	persisted, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("load compacted history: %v", err)
	}
	for _, msg := range visibleMessagesForTest(persisted) {
		if msg.Role == "user" && strings.HasPrefix(strings.TrimSpace(msg.Content), "/compact") {
			t.Fatalf("slash compact prompt should not be persisted as a user message, got %+v", persisted)
		}
	}
}

func TestServerRejectsWuuCompactForExternalEngineThread(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	th := srv.thread(threadID)
	th.mu.Lock()
	th.EngineID = "claude"
	th.mu.Unlock()

	raw, err := json.Marshal(map[string]any{
		"id": "compact-external", "method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "/compact"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "compact-external")
	if response["error"] == nil || !strings.Contains(remarshal[ResponseError](t, response["error"]).Message, "unavailable for claude") {
		t.Fatalf("response = %+v", response)
	}
}

func TestServerRejectsSteerForCompactTurn(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	th := srv.thread(threadID)
	if th == nil {
		t.Fatal("expected loaded thread")
	}
	th.mu.Lock()
	th.startCompactTurnLocked("compact-running", providers.ChatMessage{Role: "user", Content: "/compact"}, time.Now().UTC())
	th.mu.Unlock()

	raw, err := json.Marshal(map[string]any{
		"id":     "steer-compact",
		"method": MethodTurnSteer,
		"params": TurnSteerParams{
			ThreadID:       threadID,
			ExpectedTurnID: "compact-running",
			Prompt:         "please also do this",
		},
	})
	if err != nil {
		t.Fatalf("marshal steer request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/steer: %v", err)
	}
	resp := responseByID(t, parseOutput(t, out.String()), "steer-compact")
	errObj, ok := resp["error"].(map[string]any)
	if !ok || !strings.Contains(fmt.Sprint(errObj["message"]), "cannot steer a compact turn") {
		t.Fatalf("expected compact steer rejection, got %+v", resp)
	}
}

func TestServerThreadResumeReturnsLoadedRunningThread(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	th := srv.thread(threadID)
	if th == nil {
		t.Fatal("expected loaded thread")
	}
	now := time.Now().UTC()
	th.mu.Lock()
	th.startTurnLocked("turn-loaded-running", providers.ChatMessage{Role: "user", Content: "keep running"}, now)
	th.mu.Unlock()

	raw, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodThreadResume,
		"params": ThreadResumeParams{SessionID: threadID},
	})
	if err != nil {
		t.Fatalf("marshal resume request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}

	if srv.thread(threadID) != th {
		t.Fatal("resume should not replace an already loaded thread")
	}
	result := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, out.String()), "2")["result"])
	if result.Thread.ID != threadID || result.Thread.Status != ThreadStatusInProgress || len(result.Thread.Turns) != 1 {
		t.Fatalf("unexpected loaded resume result: %+v", result.Thread)
	}
}

func TestServerPrunesIdleCachedThreads(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	srv := New(rt, &lockedBuffer{})
	now := time.Now().UTC()

	keep := newThreadState("thread-keep", nil, rt.ProviderName, rt.Model, rt.RootDir, true, now.Add(-24*time.Hour))
	keep.LastAccessedAt = now.Add(-24 * time.Hour)
	running := newThreadState("thread-running", nil, rt.ProviderName, rt.Model, rt.RootDir, true, now.Add(-23*time.Hour))
	running.LastAccessedAt = now.Add(-23 * time.Hour)
	running.startTurnLocked("running-turn", providers.ChatMessage{Role: "user", Content: "running"}, now)
	queued := newThreadState("thread-queued", nil, rt.ProviderName, rt.Model, rt.RootDir, true, now.Add(-22*time.Hour))
	queued.LastAccessedAt = now.Add(-22 * time.Hour)

	srv.threads[keep.ID] = keep
	srv.threads[running.ID] = running
	srv.threads[queued.ID] = queued
	srv.enqueueQueuedUserTurn(queued.ID, queuedTurn{id: "queued-1", msg: providers.ChatMessage{Role: "user", Content: "later"}})

	for i := 0; i < cachedThreadLimit+4; i++ {
		id := fmt.Sprintf("idle-%02d", i)
		th := newThreadState(id, nil, rt.ProviderName, rt.Model, rt.RootDir, true, now.Add(time.Duration(i)*time.Minute))
		th.LastAccessedAt = now.Add(time.Duration(i) * time.Minute)
		srv.threads[id] = th
	}

	srv.pruneCachedThreads(keep.ID)

	if srv.thread(keep.ID) == nil {
		t.Fatal("kept thread was pruned")
	}
	if srv.thread(running.ID) == nil {
		t.Fatal("running thread was pruned")
	}
	if srv.thread(queued.ID) == nil {
		t.Fatal("queued thread was pruned")
	}
	srv.mu.Lock()
	count := len(srv.threads)
	srv.mu.Unlock()
	if count > cachedThreadLimit {
		t.Fatalf("cached thread count should be bounded, got %d", count)
	}
}

func TestServerTurnStartReloadsPrunedPersistentThread(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providers.ChatResponse{Content: "done"}})
	out := &lockedBuffer{}
	srv := New(rt, out)
	threadID := "thread-pruned"
	if _, err := session.CreateWithMetadata(rt.SessionDir, threadID, rt.RootDir); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := rewriteChatHistory(rt.SessionDir, threadID, []providers.ChatMessage{{Role: "user", Content: "hello"}}); err != nil {
		t.Fatalf("write history: %v", err)
	}

	req := fmt.Sprintf(`{"id":"1","method":"turn/start","params":{"thread_id":%q,"prompt":"continue"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	_ = waitForMethod(t, out, NotificationTurnCompleted)

	th := srv.thread(threadID)
	if th == nil {
		t.Fatal("thread should be reloaded into the cache")
	}
	th.mu.Lock()
	visible := visibleMessagesForTest(th.History)
	th.mu.Unlock()
	if len(visible) != 4 || visible[1].Content != "hello" || visible[2].Content != "continue" || visible[3].Content != "done" {
		t.Fatalf("unexpected reloaded history: %+v", visible)
	}
}

func TestServerThreadResumeRepairsToolResultOrderWithoutWriting(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providers.ChatResponse{Content: "done"}})
	sessionID := "20260523-000001-tools"
	sess, err := session.CreateWithMetadata(rt.SessionDir, sessionID, rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := rewriteChatHistory(rt.SessionDir, sess.ID, []providers.ChatMessage{
		{Role: "user", Content: "inspect"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_1",
				Name:      "read_file",
				Arguments: "{}",
			}},
		},
		{Role: "user", Content: "mid-turn context"},
		{Role: "tool", Name: "read_file", ToolCallID: "call_1", Content: "ok"},
	}); err != nil {
		t.Fatalf("write session: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	payload := map[string]any{
		"id":     "1",
		"method": MethodThreadResume,
		"params": ThreadResumeParams{SessionID: sessionID},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal resume request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}

	th := srv.thread(sessionID)
	if th == nil {
		t.Fatal("expected resumed thread")
	}
	if err := providers.ValidateToolCallHistory(th.History); err != nil {
		t.Fatalf("expected valid resumed history, got %v: %+v", err, th.History)
	}
	roles := make([]string, 0, len(th.History))
	for _, msg := range th.History {
		roles = append(roles, msg.Role)
	}
	if got, want := strings.Join(roles, ","), "system,user,assistant,tool,user"; got != want {
		t.Fatalf("unexpected resumed order: got %s want %s", got, want)
	}

	persisted, err := loadChatMessages(rt.SessionDir, sess.ID)
	if err != nil {
		t.Fatalf("load rewritten session: %v", err)
	}
	roles = roles[:0]
	for _, msg := range persisted {
		roles = append(roles, msg.Role)
	}
	if got, want := strings.Join(roles, ","), "user,assistant,user,tool"; got != want {
		t.Fatalf("read-only resume rewrote durable history: got %s want %s", got, want)
	}

	turnReq := fmt.Sprintf(`{"id":"2","method":"turn/start","params":{"thread_id":%q,"prompt":"continue"}}`, sessionID)
	if err := srv.handleLine(context.Background(), []byte(turnReq)); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	waitForMethod(t, out, NotificationTurnCompleted)
	persisted, err = loadChatMessages(rt.SessionDir, sess.ID)
	if err != nil {
		t.Fatalf("load history after admission repair: %v", err)
	}
	roles = roles[:0]
	for _, msg := range persisted {
		roles = append(roles, msg.Role)
	}
	if got, want := strings.Join(roles, ","), "user,assistant,tool,user,user,assistant"; got != want {
		t.Fatalf("turn admission did not persist repaired order: got %s want %s", got, want)
	}
}

func TestTurnsFromHistoryRestoresToolCallItems(t *testing.T) {
	history := []providers.ChatMessage{
		{Role: "user", Content: "inspect"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_1",
				Name:      "read_file",
				Arguments: `{"path":"internal/appserver/model.go"}`,
				Display:   &providers.ToolCallDisplay{Kind: "read", Text: "读取 model.go"},
			}},
		},
		{
			Role:       "tool",
			ToolCallID: "call_1",
			Content:    `{"path":"internal/appserver/model.go","num_lines":20}`,
		},
		{Role: "assistant", Content: "done"},
	}

	turns := turnsFromHistory("thread", history, time.Unix(0, 0).UTC())
	if len(turns) != 1 {
		t.Fatalf("expected one turn, got %+v", turns)
	}
	items := turns[0].Items
	if len(items) != 3 {
		t.Fatalf("expected user, tool, and assistant items, got %+v", items)
	}
	toolItem := items[1]
	if toolItem.Type != ThreadItemToolCall || toolItem.Name != "read_file" || toolItem.Arguments == "" || toolItem.Result == "" {
		t.Fatalf("unexpected restored tool item: %+v", toolItem)
	}
	if toolItem.Display == nil || toolItem.Display.Text != "读取 model.go" {
		t.Fatalf("expected restored tool display metadata, got %+v", toolItem.Display)
	}
	if items[2].Type != ThreadItemAgentMessage || items[2].Text != "done" {
		t.Fatalf("unexpected assistant item: %+v", items[2])
	}
}

func TestTurnsFromHistoryRestoresPluginToolsAsOrdinaryToolItems(t *testing.T) {
	history := []providers.ChatMessage{
		{Role: "user", Content: "delegate"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_1",
				Name:      "spawn_agent",
				Arguments: `{"name":"inspect","description":"Inspect","prompt":"inspect","subagent_type":"general-purpose","run_in_background":true}`,
			}},
		},
		{
			Role:       "tool",
			ToolCallID: "call_1",
			Content:    `{"agent_id":"worker-1","agent_path":"/root/inspect","status":"running"}`,
		},
	}

	turns := turnsFromHistory("thread", history, time.Unix(0, 0).UTC())
	if len(turns) != 1 || len(turns[0].Items) != 2 {
		t.Fatalf("unexpected turns: %+v", turns)
	}
	item := turns[0].Items[1]
	if item.Type != ThreadItemToolCall || item.Name != "spawn_agent" || item.Result == "" {
		t.Fatalf("unexpected plugin tool item: %+v", item)
	}
}

func TestServerForwardsAgentNotifications(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	workerClient := &fakeClient{response: providers.ChatResponse{Content: "agent done"}}
	coord, err := agentcontrol.New(agentcontrol.Config{
		Client:       providers.AdaptStreamClient(workerClient),
		DefaultModel: "fake-model",
		ParentRepo:   rt.RootDir,
		WorktreeRoot: filepath.Join(rt.RootDir, ".wuu", "worktrees"),
		SessionID:    "sess-agents",
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	if err != nil {
		t.Fatalf("coordinator: %v", err)
	}
	rt.AgentControl = coord

	out := &lockedBuffer{}
	srv := New(rt, out)
	threadID := "sess-agents"
	srv.subscribeThreadRuntime(threadID, &runtime.ThreadRuntime{AgentControl: coord})
	res, err := coord.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "check_bridge",
		Description: "check bridge",
		Prompt:      "do it",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	msgs := waitForMethod(t, out, NotificationAgentMailbox)
	updated := remarshal[AgentUpdatedNotification](t, notificationByMethod(t, msgs, NotificationAgentUpdated)["params"])
	if updated.ThreadID != threadID || updated.Agent.ID != res.AgentID || updated.Agent.TaskName != "check_bridge" {
		t.Fatalf("unexpected agent update: %+v", updated)
	}
	mailbox := remarshal[AgentMailboxNotification](t, notificationByMethod(t, msgs, NotificationAgentMailbox)["params"])
	if mailbox.ThreadID != threadID || mailbox.Message.AgentID != res.AgentID || mailbox.Message.Result != "agent done" || mailbox.Message.Type != "agent_result" {
		t.Fatalf("unexpected mailbox notification: %+v", mailbox)
	}
}

func TestServerAutoResumesRootAgentOnAgentCompletion(t *testing.T) {
	mainClient := &fakeClient{response: providers.ChatResponse{Content: "integrated result"}}
	rt := newTestRuntime(t, mainClient)
	workerClient := &fakeClient{response: providers.ChatResponse{Content: "agent done"}}
	coord, err := agentcontrol.New(agentcontrol.Config{
		Client:       providers.AdaptStreamClient(workerClient),
		DefaultModel: "fake-model",
		ParentRepo:   rt.RootDir,
		WorktreeRoot: filepath.Join(rt.RootDir, ".wuu", "worktrees"),
		SessionID:    "sess-agents",
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	if err != nil {
		t.Fatalf("coordinator: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	threadID := "sess-agents"
	threadRuntime := &runtime.ThreadRuntime{
		StreamRunner: rt.StreamRunner,
		AgentControl: coord,
	}
	rootThread := newThreadState(threadID, []providers.ChatMessage{
		{Role: "user", Content: "please inspect"},
	}, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	rootThread.execRuntime = threadRuntime
	srv.mu.Lock()
	srv.threads[threadID] = rootThread
	srv.mu.Unlock()
	rootThread.runtimeSubscription = srv.subscribeThreadRuntime(threadID, threadRuntime)
	t.Cleanup(func() { releaseThreadRuntime(rootThread) })

	res, err := coord.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "check_bridge",
		Description: "check bridge",
		Prompt:      "do it",
		Synchronous: false,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	msgs := waitForTurnCompletedForThread(t, out, threadID)
	completed := remarshal[TurnCompletedNotification](t, notificationByMethodForThread(t, msgs, NotificationTurnCompleted, threadID)["params"])
	if completed.Content != "integrated result" {
		t.Fatalf("unexpected root turn completion: %+v", completed)
	}

	mainClient.mu.Lock()
	requests := append([]providers.ChatRequest(nil), mainClient.requests...)
	mainClient.mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("expected one main agent request, got %d", len(requests))
	}
	var handoff providers.ChatMessage
	for _, msg := range requests[0].Messages {
		if msg.Role == "user" && msg.Name == wuucontext.AgentNotificationMessageName &&
			strings.Contains(msg.Content, res.AgentID) && strings.Contains(msg.Content, "agent done") {
			handoff = msg
			break
		}
	}
	if handoff.Content == "" {
		t.Fatalf("main agent request missing worker completion handoff: %+v", requests[0].Messages)
	}
	var communication agentthread.InterAgentCommunication
	if err := json.Unmarshal([]byte(handoff.Content), &communication); err != nil {
		t.Fatalf("handoff is not inter-agent JSON: %v\n%s", err, handoff.Content)
	}
	if !communication.TriggerTurn || communication.Recipient != agentthread.RootAgentPath() {
		t.Fatalf("unexpected handoff envelope: %+v", communication)
	}

	awaitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	awaited, err := coord.AwaitFrom(agentthread.RootPath, awaitCtx, []string{res.AgentID})
	if err != nil {
		t.Fatalf("AwaitFrom after auto resume: %v", err)
	}
	// Result delivery is unconditional: an await re-read still returns the
	// content, with the consumed markers telling the parent it was already
	// injected by auto-completion (the ledger dedupes injection, not reads).
	if len(awaited.Results) != 1 || awaited.Results[0].ResultID == "" || !awaited.Results[0].ResultConsumed || awaited.Results[0].ConsumedBy != "auto_completion" || awaited.Results[0].Result == "" {
		t.Fatalf("await after auto resume should return the result with consumed markers set: %+v", awaited)
	}
}

func TestServerQueuesAgentCompletionWhileRootTurnIsRunning(t *testing.T) {
	mainClient := newBlockingStreamClient("root turn done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = mainClient
	workerClient := &fakeClient{response: providers.ChatResponse{Content: "agent done"}}
	coord, err := agentcontrol.New(agentcontrol.Config{
		Client:       providers.AdaptStreamClient(workerClient),
		DefaultModel: "fake-model",
		ParentRepo:   rt.RootDir,
		WorktreeRoot: filepath.Join(rt.RootDir, ".wuu", "worktrees"),
		SessionID:    "sess-agents",
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	if err != nil {
		t.Fatalf("coordinator: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	threadID := "sess-agents"
	threadRuntime := &runtime.ThreadRuntime{
		StreamRunner: rt.StreamRunner,
		AgentControl: coord,
	}
	rootThread := newThreadState(threadID, []providers.ChatMessage{
		{Role: "user", Content: "please inspect"},
	}, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	rootThread.execRuntime = threadRuntime
	srv.mu.Lock()
	srv.threads[threadID] = rootThread
	srv.mu.Unlock()
	rootThread.runtimeSubscription = srv.subscribeThreadRuntime(threadID, threadRuntime)
	t.Cleanup(func() { releaseThreadRuntime(rootThread) })

	req := fmt.Sprintf(`{"id":"1","method":"turn/start","params":{"thread_id":%q,"prompt":"keep working"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	<-mainClient.started

	if _, err := coord.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "check_bridge",
		Description: "check bridge",
		Prompt:      "do it",
		Synchronous: false,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	waitForMethod(t, out, NotificationAgentMailbox)

	close(mainClient.release)
	waitForTurnCompletedCountForThread(t, out, threadID, 2)

	rootThread.mu.Lock()
	history := append([]providers.ChatMessage(nil), rootThread.History...)
	rootThread.mu.Unlock()
	var foundHandoff bool
	for _, msg := range history {
		if msg.Role == "user" && msg.Name == wuucontext.AgentNotificationMessageName &&
			strings.Contains(msg.Content, "agent done") && strings.Contains(msg.Content, `"trigger_turn":true`) {
			foundHandoff = true
			break
		}
	}
	if !foundHandoff {
		t.Fatalf("root history missing queued worker completion handoff: %+v", history)
	}
}

func TestServerSkipsDuplicateAgentCompletionNotificationsAfterAutoResume(t *testing.T) {
	mainClient := &fakeClient{response: providers.ChatResponse{Content: "integrated result"}}
	rt := newTestRuntime(t, mainClient)
	workerClient := &fakeClient{response: providers.ChatResponse{Content: "agent done"}}
	coord, err := agentcontrol.New(agentcontrol.Config{
		Client:       providers.AdaptStreamClient(workerClient),
		DefaultModel: "fake-model",
		ParentRepo:   rt.RootDir,
		WorktreeRoot: filepath.Join(rt.RootDir, ".wuu", "worktrees"),
		SessionID:    "sess-agents",
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	if err != nil {
		t.Fatalf("coordinator: %v", err)
	}
	t.Cleanup(coord.Close)

	out := &lockedBuffer{}
	srv := New(rt, out)
	threadID := "sess-agents"
	threadRuntime := &runtime.ThreadRuntime{
		StreamRunner: rt.StreamRunner,
		AgentControl: coord,
	}
	rootThread := newThreadState(threadID, []providers.ChatMessage{
		{Role: "user", Content: "please inspect"},
	}, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	rootThread.execRuntime = threadRuntime
	srv.mu.Lock()
	srv.threads[threadID] = rootThread
	srv.mu.Unlock()
	rootThread.runtimeSubscription = srv.subscribeThreadRuntime(threadID, threadRuntime)
	t.Cleanup(func() { releaseThreadRuntime(rootThread) })

	res, err := coord.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "check_bridge",
		Description: "check bridge",
		Prompt:      "do it",
		Synchronous: false,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	waitForTurnCompletedForThread(t, out, threadID)
	sa := coord.Manager().Get(res.AgentID)
	if sa == nil {
		t.Fatalf("agent %q not found", res.AgentID)
	}
	coord.Manager().BroadcastSnapshot(sa)
	time.Sleep(150 * time.Millisecond)
	if got := turnCompletedCountForThread(t, out, threadID); got != 1 {
		t.Fatalf("expected duplicate completion notification not to trigger another root turn, got %d; output:\n%s", got, out.String())
	}
}

func TestServerSkipsAutoResumeWhenAwaitAgentsAlreadyReturnedResult(t *testing.T) {
	mainClient := newBlockingStreamClient("root turn done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = mainClient
	workerClient := &fakeClient{response: providers.ChatResponse{Content: "agent done"}}
	coord, err := agentcontrol.New(agentcontrol.Config{
		Client:       providers.AdaptStreamClient(workerClient),
		DefaultModel: "fake-model",
		ParentRepo:   rt.RootDir,
		WorktreeRoot: filepath.Join(rt.RootDir, ".wuu", "worktrees"),
		SessionID:    "sess-agents",
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	if err != nil {
		t.Fatalf("coordinator: %v", err)
	}
	t.Cleanup(coord.Close)

	out := &lockedBuffer{}
	srv := New(rt, out)
	threadID := "sess-agents"
	threadRuntime := &runtime.ThreadRuntime{
		StreamRunner: rt.StreamRunner,
		AgentControl: coord,
	}
	rootThread := newThreadState(threadID, []providers.ChatMessage{
		{Role: "user", Content: "please inspect"},
	}, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	rootThread.execRuntime = threadRuntime
	srv.mu.Lock()
	srv.threads[threadID] = rootThread
	srv.mu.Unlock()
	rootThread.runtimeSubscription = srv.subscribeThreadRuntime(threadID, threadRuntime)
	t.Cleanup(func() { releaseThreadRuntime(rootThread) })

	req := fmt.Sprintf(`{"id":"1","method":"turn/start","params":{"thread_id":%q,"prompt":"keep working"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	<-mainClient.started

	res, err := coord.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "check_bridge",
		Description: "check bridge",
		Prompt:      "do it",
		Synchronous: false,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	waitForMethod(t, out, NotificationAgentMailbox)

	awaitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	awaited, err := coord.AwaitFrom(agentthread.RootPath, awaitCtx, []string{res.AgentID})
	if err != nil {
		t.Fatalf("AwaitFrom: %v", err)
	}
	if len(awaited.Results) != 1 || awaited.Results[0].AgentID != res.AgentID || awaited.Results[0].Result != "agent done" {
		t.Fatalf("unexpected awaited result: %+v", awaited)
	}

	close(mainClient.release)
	waitForTurnCompletedCountForThread(t, out, threadID, 1)
	time.Sleep(150 * time.Millisecond)
	if got := turnCompletedCountForThread(t, out, threadID); got != 1 {
		t.Fatalf("expected awaited agent completion not to trigger a second root turn, got %d; output:\n%s", got, out.String())
	}
}

func newTestRuntime(t *testing.T, client *fakeClient) *runtime.Session {
	t.Helper()
	root := retryingTempDir(t)
	t.Setenv("HOME", retryingTempDir(t))
	environmentSection := "# Environment\n\n- Current working directory: " + root + "\n- Current date: 2026-01-01"
	systemPrompt := "system prompt\n\n" + environmentSection
	return &runtime.Session{
		ProviderName:   "fake-provider",
		Model:          "fake-model",
		RootDir:        root,
		ConfigPath:     root + "/.wuu.json",
		ConfigLoadMode: runtime.ConfigLoadFile,
		SessionDir:     root + "/.wuu-state/sessions",
		HookDispatcher: hooks.NewDispatcher(nil),
		StreamRunner: &agent.StreamRunner{
			Client:       providers.AdaptStreamClient(client),
			Model:        "fake-model",
			SystemPrompt: systemPrompt,
			SystemPromptSections: []agent.SystemPromptSectionInfo{
				{Key: "base", Static: true, Bytes: len("system prompt"), Hash: "base-hash"},
				{Key: "environment", Static: true, Bytes: len(environmentSection), Hash: "environment-hash"},
			},
		},
	}
}

func retryingTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wuu-appserver-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() {
		var removeErr error
		for attempt := 0; attempt < 20; attempt++ {
			removeErr = os.RemoveAll(dir)
			if removeErr == nil {
				return
			}
			time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
		}
		t.Fatalf("remove temp dir %s: %v", dir, removeErr)
	})
	return dir
}

func initAppserverGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
}

func containsTestString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func attachTestProcessManager(t *testing.T, rt *runtime.Session) *process.Manager {
	t.Helper()
	manager, err := process.NewManager(rt.RootDir, filepath.Join(rt.RootDir, "runtime"))
	if err != nil {
		t.Fatalf("process.NewManager: %v", err)
	}
	rt.ProcessManager = manager
	return manager
}

func toolDefinitionNames(defs []providers.ToolDefinition) map[string]bool {
	names := make(map[string]bool, len(defs))
	for _, def := range defs {
		names[def.Name] = true
	}
	return names
}

func waitForAgentStatus(t *testing.T, control *agentcontrol.AgentControl, agentID string, want subagent.Status) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, snap := range control.List() {
			if snap.ID == agentID && snap.Status == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for agent %s status %s", agentID, want)
}

func parseOutput(t *testing.T, output string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var msgs []map[string]any
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("parse output line %q: %v", line, err)
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

func waitForMethod(t *testing.T, out *lockedBuffer, method string) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msgs := parseOutput(t, out.String())
		for _, msg := range msgs {
			if msg["method"] == method {
				return msgs
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; output:\n%s", method, out.String())
	return nil
}

func waitForTurnCompletedForThread(t *testing.T, out *lockedBuffer, threadID string) []map[string]any {
	t.Helper()
	// This is a deadlock guard, not a turn latency requirement. Full-suite CI
	// shares CPU and disk with other packages; return as soon as the event arrives.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		msgs := parseOutput(t, out.String())
		for _, msg := range msgs {
			if msg["method"] != NotificationTurnCompleted || msg["id"] != nil {
				continue
			}
			params := remarshal[TurnCompletedNotification](t, msg["params"])
			if params.ThreadID == threadID {
				return msgs
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for completed turn on %s; output:\n%s", threadID, out.String())
	return nil
}

func waitForTurnCompletedCountForThread(t *testing.T, out *lockedBuffer, threadID string, count int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msgs := parseOutput(t, out.String())
		seen := 0
		for _, msg := range msgs {
			if msg["method"] != NotificationTurnCompleted || msg["id"] != nil {
				continue
			}
			params := remarshal[TurnCompletedNotification](t, msg["params"])
			if params.ThreadID == threadID {
				seen++
			}
		}
		if seen >= count {
			return msgs
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d completed turns on %s; output:\n%s", count, threadID, out.String())
	return nil
}

func turnCompletedCountForThread(t *testing.T, out *lockedBuffer, threadID string) int {
	t.Helper()
	msgs := parseOutput(t, out.String())
	seen := 0
	for _, msg := range msgs {
		if msg["method"] != NotificationTurnCompleted || msg["id"] != nil {
			continue
		}
		params := remarshal[TurnCompletedNotification](t, msg["params"])
		if params.ThreadID == threadID {
			seen++
		}
	}
	return seen
}

func waitForNotificationCount(t *testing.T, out *lockedBuffer, method string, count int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msgs := parseOutput(t, out.String())
		if len(notificationsByMethod(msgs, method)) >= count {
			return msgs
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d %s notifications; output:\n%s", count, method, out.String())
	return nil
}

// waitForResponseByID polls for a response written by a handler dispatched
// through startBackground (text/polish, git/commit-message,
// thread/regenerate-title): handleLine returns before the goroutine writes.
func waitForResponseByID(t *testing.T, out *lockedBuffer, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, msg := range parseOutput(t, out.String()) {
			if msg["id"] == id && msg["method"] == nil {
				return msg
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for response id %s; output:\n%s", id, out.String())
	return nil
}

func responseByID(t *testing.T, msgs []map[string]any, id string) map[string]any {
	t.Helper()
	for _, msg := range msgs {
		if msg["id"] == id && msg["method"] == nil {
			return msg
		}
	}
	t.Fatalf("response id %s not found in %+v", id, msgs)
	return nil
}

func notificationByMethod(t *testing.T, msgs []map[string]any, method string) map[string]any {
	t.Helper()
	for _, msg := range msgs {
		if msg["method"] == method && msg["id"] == nil {
			return msg
		}
	}
	t.Fatalf("notification %s not found in %+v", method, msgs)
	return nil
}

func notificationByMethodForThread(t *testing.T, msgs []map[string]any, method, threadID string) map[string]any {
	t.Helper()
	for _, msg := range msgs {
		if msg["method"] != method || msg["id"] != nil {
			continue
		}
		switch method {
		case NotificationTurnCompleted:
			params := remarshal[TurnCompletedNotification](t, msg["params"])
			if params.ThreadID == threadID {
				return msg
			}
		case NotificationTurnStarted:
			params := remarshal[TurnStartedNotification](t, msg["params"])
			if params.ThreadID == threadID {
				return msg
			}
		}
	}
	t.Fatalf("notification %s for thread %s not found in %+v", method, threadID, msgs)
	return nil
}

func notificationsByMethod(msgs []map[string]any, method string) []map[string]any {
	var out []map[string]any
	for _, msg := range msgs {
		if msg["method"] == method && msg["id"] == nil {
			out = append(out, msg)
		}
	}
	return out
}

func testStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func turnEventByType(t *testing.T, msgs []map[string]any, typ providers.StreamEventType) map[string]any {
	t.Helper()
	for _, msg := range msgs {
		if msg["id"] != nil || msg["method"] != NotificationTurnEvent {
			continue
		}
		params := remarshal[TurnEventNotification](t, msg["params"])
		if params.Event.Type == typ {
			return msg
		}
	}
	t.Fatalf("turn event %s not found in %+v", typ, msgs)
	return nil
}

func turnEventsByTypeForThread(t *testing.T, msgs []map[string]any, threadID string, typ providers.StreamEventType) []TurnEventNotification {
	t.Helper()
	var out []TurnEventNotification
	for _, msg := range msgs {
		if msg["id"] != nil || msg["method"] != NotificationTurnEvent {
			continue
		}
		params := remarshal[TurnEventNotification](t, msg["params"])
		if params.ThreadID == threadID && params.Event.Type == typ {
			out = append(out, params)
		}
	}
	return out
}

func writeAppServerWSEvent(t *testing.T, ctx context.Context, conn *websocket.Conn, raw string) {
	t.Helper()
	if err := conn.Write(ctx, websocket.MessageText, []byte(raw)); err != nil {
		t.Fatalf("write websocket event: %v", err)
	}
}

func remarshal[T any](t *testing.T, value any) T {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal value: %v", err)
	}
	return out
}

func TestSettingsUsageAggregatesAcrossSessions(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	now := time.Now().UTC()

	sess1, err := session.CreateWithMetadata(rt.SessionDir, "usage-anthropic", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendHistoryRecord(rt.SessionDir, sess1.ID, session.HistoryRecord{
		Role: "user", Content: "anthropic session",
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendHistoryRecord(rt.SessionDir, sess1.ID, session.HistoryRecord{
		Role: "user", Content: "follow up",
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendHistoryRecord(rt.SessionDir, sess1.ID, session.HistoryRecord{
		Role: "meta", Content: "token_usage",
		Provider: "anthropic", Model: "claude-sonnet-4-6",
		At:          now.Add(-2 * time.Hour),
		InputTokens: 100, OutputTokens: 50,
		CacheCreationTokens: 80, CacheReadTokens: 20,
	}); err != nil {
		t.Fatal(err)
	}

	sess2, err := session.CreateWithMetadata(rt.SessionDir, "usage-openai", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendHistoryRecord(rt.SessionDir, sess2.ID, session.HistoryRecord{
		Role: "user", Content: "openai session",
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendHistoryRecord(rt.SessionDir, sess2.ID, session.HistoryRecord{
		Role: "user", Content: "follow up",
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendHistoryRecord(rt.SessionDir, sess2.ID, session.HistoryRecord{
		Role: "meta", Content: "token_usage",
		Provider: "openai", Model: "gpt-4o",
		At:          now.Add(-time.Hour),
		InputTokens: 200, OutputTokens: 100,
		CacheCreationTokens: 0, CacheReadTokens: 50,
	}); err != nil {
		t.Fatal(err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)

	raw, err := json.Marshal(map[string]any{
		"id":     "1",
		"method": MethodSettingsUsage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("handleLine: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[SettingsUsageResponse](t, responseByID(t, msgs, "1")["result"])

	if result.TotalSessions != 2 {
		t.Fatalf("total_sessions=%d, want 2", result.TotalSessions)
	}
	if len(result.ModelBreakdowns) != 2 {
		t.Fatalf("expected 2 breakdowns, got %d (%+v)", len(result.ModelBreakdowns), result.ModelBreakdowns)
	}
	// openai total = 200 + 50 + 100 = 350
	// anthropic total = 100 + 20 + 50 = 170
	if result.ModelBreakdowns[0].Provider != "openai" {
		t.Fatalf("expected openai first, got %q", result.ModelBreakdowns[0].Provider)
	}
	if result.ModelBreakdowns[1].Provider != "anthropic" {
		t.Fatalf("expected anthropic second, got %q", result.ModelBreakdowns[1].Provider)
	}
}

func TestSettingsUsageModelBreakdownsSkipZeroUsageBuckets(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	now := time.Now().UTC()

	sess1, err := session.CreateWithMetadata(rt.SessionDir, "usage-real", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range []session.HistoryRecord{
		{Role: "user", Content: "real session"},
		{
			Role: "meta", Content: "token_usage", Provider: "openai", Model: "gpt-4o",
			At: now.Add(-time.Hour), InputTokens: 200, OutputTokens: 100, CacheReadTokens: 50,
		},
	} {
		if err := session.AppendHistoryRecord(rt.SessionDir, sess1.ID, rec); err != nil {
			t.Fatal(err)
		}
	}

	// Providers that never report usage (and compaction markers) persist
	// token_usage rows with all-zero usage but a nonzero context size. A
	// bucket made only of such rows must not surface as a 0/0 card.
	sess2, err := session.CreateWithMetadata(rt.SessionDir, "usage-zero", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range []session.HistoryRecord{
		{Role: "user", Content: "zero session"},
		{
			Role: "meta", Content: "token_usage", Provider: "test", Model: "gpt-test",
			At: now.Add(-time.Hour), ContextTokens: 4096,
		},
	} {
		if err := session.AppendHistoryRecord(rt.SessionDir, sess2.ID, rec); err != nil {
			t.Fatal(err)
		}
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	raw, err := json.Marshal(map[string]any{
		"id":     "1",
		"method": MethodSettingsUsage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("handleLine: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[SettingsUsageResponse](t, responseByID(t, msgs, "1")["result"])

	if len(result.ModelBreakdowns) != 1 {
		t.Fatalf("expected 1 breakdown, got %d (%+v)", len(result.ModelBreakdowns), result.ModelBreakdowns)
	}
	if result.ModelBreakdowns[0].Provider != "openai" || result.ModelBreakdowns[0].InputTokens != 200 {
		t.Fatalf("expected only the openai breakdown, got %+v", result.ModelBreakdowns[0])
	}
}

func TestSettingsUsageModelBreakdownsCoverFullHistory(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	now := time.Now().UTC()

	sess, err := session.CreateWithMetadata(rt.SessionDir, "usage-full-history-models", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range []session.HistoryRecord{
		{Role: "user", Content: "current codex session", At: now.Add(-time.Hour)},
		{
			Role: "meta", Content: "token_usage", Provider: "openai-codex", Model: "gpt-5-codex",
			At: now.Add(-time.Hour), InputTokens: 100, OutputTokens: 20, CacheReadTokens: 300,
		},
		{
			Role: "meta", Content: "token_usage", Provider: "openai", Model: "gpt-4o",
			At: now.AddDate(0, 0, -20), InputTokens: 900, OutputTokens: 100, CacheReadTokens: 0,
		},
	} {
		if err := session.AppendHistoryRecord(rt.SessionDir, sess.ID, rec); err != nil {
			t.Fatal(err)
		}
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	raw, err := json.Marshal(map[string]any{
		"id":     "1",
		"method": MethodSettingsUsage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("handleLine: %v", err)
	}

	msgs := parseOutput(t, out.String())
	result := remarshal[SettingsUsageResponse](t, responseByID(t, msgs, "1")["result"])

	// Every token_usage row counts, however old: openai totals 900+0+100
	// ahead of openai-codex 100+300+20.
	if len(result.ModelBreakdowns) != 2 {
		t.Fatalf("expected two breakdowns, got %d (%+v)", len(result.ModelBreakdowns), result.ModelBreakdowns)
	}
	if result.ModelBreakdowns[0].Provider != "openai" || result.ModelBreakdowns[0].InputTokens != 900 {
		t.Fatalf("expected the older openai bucket first, got %+v", result.ModelBreakdowns[0])
	}
	codex := result.ModelBreakdowns[1]
	if codex.Provider != "openai-codex" || codex.Model != "gpt-5-codex" {
		t.Fatalf("unexpected model breakdown: %+v", codex)
	}
	if codex.InputTokens != 100 || codex.CacheReadTokens != 300 || codex.OutputTokens != 20 {
		t.Fatalf("unexpected model usage totals: %+v", codex)
	}
	if result.Metrics.PromptTokens != 1300 || result.Metrics.CacheReadTokens != 300 {
		t.Fatalf("unexpected headline usage: %+v", result.Metrics)
	}
}
