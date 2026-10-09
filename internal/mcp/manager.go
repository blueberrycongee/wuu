package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/extensions"
)

type serverLifecycle struct {
	ctx     context.Context
	cancel  context.CancelFunc
	revoked bool
}

// Manager holds all active MCP client connections and exposes their tools.
type Manager struct {
	mu         sync.RWMutex
	configs    map[string]ServerConfig
	clients    map[string]*Client
	statuses   map[string]ServerStatus
	servers    map[string]*serverLifecycle
	generation uint64
	oauth      *OAuthManager
	ctx        context.Context
	cancel     context.CancelFunc
	operations sync.WaitGroup
	closed     bool
	closeDone  chan struct{}
	closeErr   error
}

var (
	ErrOAuthRequired = errors.New("mcp OAuth authentication required")
	ErrManagerClosed = errors.New("mcp manager is closed")
	ErrServerRevoked = errors.New("mcp server is revoked")
)

const defaultMCPRedirectURI = "http://127.0.0.1/callback"

type NativeTool struct {
	Definition Tool
	Client     *Client
	Timeout    time.Duration
	Provenance extensions.Provenance
}

// NewManager creates an empty MCP manager.
func NewManager() *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		configs:   make(map[string]ServerConfig),
		clients:   make(map[string]*Client),
		statuses:  make(map[string]ServerStatus),
		servers:   make(map[string]*serverLifecycle),
		ctx:       ctx,
		cancel:    cancel,
		closeDone: make(chan struct{}),
	}
}

func (m *Manager) SetOAuthManager(oauth *OAuthManager) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.oauth = oauth
	m.mu.Unlock()
}

func (m *Manager) Configure(configs map[string]ServerConfig) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	if m.configs == nil {
		m.configs = make(map[string]ServerConfig)
	}
	if m.statuses == nil {
		m.statuses = make(map[string]ServerStatus)
	}
	for name, cfg := range configs {
		name = strings.TrimSpace(name)
		if name == "" || m.serverRevoked(name) {
			continue
		}
		cfg.Name = name
		m.configs[name] = cloneServerConfig(cfg)
		if _, connected := m.clients[name]; connected {
			continue
		}
		state := MCPServerStateConfigured
		if !cfg.IsEnabled() {
			state = MCPServerStateDisabled
		}
		if current, ok := m.statuses[name]; !ok || current.State == "" {
			m.statuses[name] = ServerStatus{Name: name, State: state, AuthStatus: authStatusForConfig(cfg)}
		}
	}
}

// Add connects and registers an MCP server. If the connection fails, the
// error is returned and no client is kept.
func (m *Manager) Add(ctx context.Context, cfg ServerConfig) error {
	cfg.Name = strings.TrimSpace(cfg.Name)
	if cfg.Name == "" {
		return fmt.Errorf("mcp server name is required")
	}
	ctx, finish, err := m.beginConnection(ctx, cfg.Name)
	if err != nil {
		return err
	}
	defer finish()

	m.rememberConfig(cfg)
	if !cfg.IsEnabled() {
		m.recordStatus(ServerStatus{Name: cfg.Name, State: MCPServerStateDisabled, AuthStatus: authStatusForConfig(cfg)})
		return nil
	}
	m.recordStatus(ServerStatus{Name: cfg.Name, State: MCPServerStateConnecting, AuthStatus: authStatusForConfig(cfg)})
	oauth := m.oauthManager()
	if cfg.URL != "" {
		if oauth == nil && cfg.OAuth != nil {
			m.recordStatus(ServerStatus{Name: cfg.Name, State: MCPServerStateAuthRequired, AuthStatus: MCPAuthStatusNotLoggedIn, Error: ErrOAuthRequired.Error()})
			return ErrOAuthRequired
		}
		var token string
		var tokenErr error
		if oauth != nil {
			token, tokenErr = oauth.AccessToken(ctx, cfg.Name)
		}
		if tokenErr != nil && (!errors.Is(tokenErr, credentialstore.ErrNotFound) || cfg.OAuth != nil) {
			if lifecycleErr := m.connectionError(cfg.Name); lifecycleErr != nil {
				return lifecycleErr
			}
			m.recordStatus(ServerStatus{Name: cfg.Name, State: MCPServerStateAuthRequired, AuthStatus: MCPAuthStatusNotLoggedIn, Error: tokenErr.Error()})
			if errors.Is(tokenErr, credentialstore.ErrNotFound) {
				return ErrOAuthRequired
			}
			return fmt.Errorf("load OAuth credentials for MCP server %q: %w", cfg.Name, tokenErr)
		}
		if token != "" {
			cfg = cloneServerConfig(cfg)
			if cfg.Headers == nil {
				cfg.Headers = make(map[string]string)
			}
			cfg.Headers["Authorization"] = "Bearer " + token
			if cfg.OAuth == nil {
				cfg.OAuth = &OAuthConfig{}
			}
		}
	}
	var client *Client
	var tokenProvider TokenProvider
	if cfg.URL != "" {
		if oauth != nil && cfg.OAuth != nil {
			serverName := cfg.Name
			tokenProvider = func(tokenCtx context.Context, forceRefresh bool) (string, error) {
				if forceRefresh {
					return oauth.RefreshAccessToken(tokenCtx, serverName)
				}
				return oauth.AccessToken(tokenCtx, serverName)
			}
		}
		client, err = connectRemoteWithTokenProvider(ctx, cfg, tokenProvider)
	} else {
		client, err = ConnectStdio(ctx, cfg)
	}
	if err != nil {
		if lifecycleErr := m.connectionError(cfg.Name); lifecycleErr != nil {
			return lifecycleErr
		}
		m.recordStatus(ServerStatus{Name: cfg.Name, State: classifyConnectError(err), AuthStatus: authStatusAfterConnectError(cfg, err), Connected: false, Error: err.Error()})
		return err
	}
	client.SetConnectionClosedCallback(func(err error) {
		m.clientConnectionFailed(cfg.Name, client, err)
	})
	// Eagerly discover tools so the toolkit can include them.
	if _, derr := client.DiscoverTools(ctx); derr != nil {
		_ = client.Close()
		if lifecycleErr := m.connectionError(cfg.Name); lifecycleErr != nil {
			return lifecycleErr
		}
		err := fmt.Errorf("discover tools for %q: %w", cfg.Name, derr)
		m.recordStatus(ServerStatus{Name: cfg.Name, State: MCPServerStateFailed, AuthStatus: authStatusForConfig(cfg), Connected: false, Error: err.Error()})
		return err
	}
	client.SetToolsChangedCallback(func() { m.catalogChanged(cfg.Name) })
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = client.Close()
		return ErrManagerClosed
	}
	if m.serverRevoked(cfg.Name) {
		m.mu.Unlock()
		_ = client.Close()
		return ErrServerRevoked
	}
	if connectionErr := client.ConnectionError(); connectionErr != nil {
		err := fmt.Errorf("mcp server %q connection closed: %w", cfg.Name, connectionErr)
		m.statuses[cfg.Name] = ServerStatus{
			Name:       cfg.Name,
			State:      MCPServerStateFailed,
			AuthStatus: connectedAuthStatus(cfg),
			Connected:  false,
			Error:      err.Error(),
		}
		m.mu.Unlock()
		_ = client.Close()
		return err
	}
	old := m.clients[cfg.Name]
	m.clients[cfg.Name] = client
	m.statuses[cfg.Name] = ServerStatus{
		Name:       cfg.Name,
		State:      MCPServerStateConnected,
		AuthStatus: connectedAuthStatus(cfg),
		Connected:  true,
		ToolCount:  len(client.Tools()),
	}
	m.generation++
	m.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (m *Manager) beginConnection(parent context.Context, name string) (context.Context, func(), error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, nil, ErrManagerClosed
	}
	if m.serverRevoked(name) {
		m.mu.Unlock()
		return nil, nil, ErrServerRevoked
	}
	server := m.servers[name]
	if server == nil {
		ctx, cancel := context.WithCancel(m.ctx)
		server = &serverLifecycle{ctx: ctx, cancel: cancel}
		m.servers[name] = server
	}
	lifecycle := server.ctx
	m.operations.Add(1)
	m.mu.Unlock()

	bounded, cancelBounded := withDefaultConnectionTimeout(parent)
	ctx, cancel := context.WithCancel(bounded)
	stopLifecycleCancel := context.AfterFunc(lifecycle, cancel)
	return ctx, func() {
		stopLifecycleCancel()
		cancel()
		cancelBounded()
		m.operations.Done()
	}, nil
}

// serverRevoked requires m.mu to be held.
func (m *Manager) serverRevoked(name string) bool {
	server := m.servers[name]
	return server != nil && server.revoked
}

func (m *Manager) connectionError(name string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return ErrManagerClosed
	}
	if m.serverRevoked(name) {
		return ErrServerRevoked
	}
	return nil
}

func (m *Manager) clientConnectionFailed(name string, client *Client, err error) {
	if m == nil || client == nil || err == nil {
		return
	}
	m.mu.Lock()
	if m.clients[name] != client {
		m.mu.Unlock()
		return
	}
	delete(m.clients, name)
	status := m.statuses[name]
	authStatus := status.AuthStatus
	if authStatus == "" {
		authStatus = connectedAuthStatus(m.configs[name])
	}
	m.statuses[name] = ServerStatus{
		Name:       name,
		State:      MCPServerStateFailed,
		AuthStatus: authStatus,
		Connected:  false,
		Error:      err.Error(),
	}
	m.generation++
	m.operations.Add(1)
	m.mu.Unlock()
	defer m.operations.Done()
	_ = client.Close()
}

func (m *Manager) StartOAuth(ctx context.Context, name string) (OAuthStartResult, error) {
	cfg, oauth, err := m.oauthConfig(name)
	if err != nil {
		return OAuthStartResult{}, err
	}
	result, err := oauth.Start(ctx, OAuthStartOptions{
		ServerID:     cfg.Name,
		ResourceURL:  cfg.URL,
		RedirectURI:  cfg.OAuth.RedirectURI,
		Scopes:       cfg.OAuth.Scopes,
		ClientID:     cfg.OAuth.ClientID,
		ClientSecret: cfg.OAuth.ClientSecret,
		Headers:      cfg.Headers,
	})
	if err != nil {
		return OAuthStartResult{}, err
	}
	m.recordStatus(ServerStatus{Name: cfg.Name, State: MCPServerStateAuthRequired, AuthStatus: MCPAuthStatusNotLoggedIn})
	return result, nil
}

func (m *Manager) OAuthStatus(ctx context.Context, name string) (OAuthStatus, error) {
	_, oauth, err := m.oauthConfig(name)
	if err != nil {
		return OAuthStatus{}, err
	}
	return oauth.Status(ctx, strings.TrimSpace(name))
}

func (m *Manager) FinishOAuth(ctx context.Context, name, state, code string) (ServerStatus, error) {
	cfg, oauth, err := m.oauthConfig(name)
	if err != nil {
		return ServerStatus{}, err
	}
	if _, err := oauth.Finish(ctx, OAuthFinishOptions{ServerID: cfg.Name, State: state, Code: code}); err != nil {
		return ServerStatus{}, err
	}
	status := ServerStatus{Name: cfg.Name, State: MCPServerStateStopped, AuthStatus: MCPAuthStatusOAuth}
	m.recordStatus(status)
	return status, nil
}

func (m *Manager) RemoveOAuth(ctx context.Context, name string) error {
	cfg, oauth, err := m.oauthConfig(name)
	if err != nil {
		return err
	}
	if err := oauth.Remove(ctx, cfg.Name); err != nil {
		return err
	}
	m.closeClient(cfg.Name)
	m.recordStatus(ServerStatus{Name: cfg.Name, State: MCPServerStateAuthRequired, AuthStatus: MCPAuthStatusNotLoggedIn})
	return nil
}

func (m *Manager) oauthConfig(name string) (ServerConfig, *OAuthManager, error) {
	cfg, ok := m.Config(name)
	if !ok {
		return ServerConfig{}, nil, fmt.Errorf("mcp server %q not configured", strings.TrimSpace(name))
	}
	if strings.TrimSpace(cfg.URL) == "" {
		return ServerConfig{}, nil, fmt.Errorf("mcp server %q does not support OAuth", cfg.Name)
	}
	if cfg.OAuth == nil {
		cfg.OAuth = &OAuthConfig{RedirectURI: defaultMCPRedirectURI}
	} else if strings.TrimSpace(cfg.OAuth.RedirectURI) == "" {
		cfg.OAuth.RedirectURI = defaultMCPRedirectURI
	}
	oauth := m.oauthManager()
	if oauth == nil {
		return ServerConfig{}, nil, errors.New("mcp OAuth credential store is unavailable")
	}
	return cfg, oauth, nil
}

func (m *Manager) oauthManager() *OAuthManager {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.oauth
}

func (m *Manager) Connect(ctx context.Context, name string) error {
	cfg, ok := m.Config(name)
	if !ok {
		return fmt.Errorf("mcp server %q not configured", name)
	}
	return m.Add(ctx, cfg)
}

func (m *Manager) Refresh(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		for _, cfg := range m.Configs() {
			if err := m.Add(ctx, cfg); err != nil && !errors.Is(err, ErrServerRevoked) {
				return err
			}
		}
		return nil
	}
	cfg, ok := m.Config(name)
	if !ok {
		return fmt.Errorf("mcp server %q not configured", name)
	}
	m.recordStatus(ServerStatus{Name: name, State: MCPServerStateReconnecting, AuthStatus: authStatusForConfig(cfg)})
	m.closeClient(name)
	return m.Add(ctx, cfg)
}

// Revoke permanently disables one server for this manager's lifetime. Pending
// connections are canceled and cannot publish a client or overwrite its disabled
// status. A replacement generation must use a new manager to admit the name.
func (m *Manager) Revoke(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("mcp server name is required")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrManagerClosed
	}
	server := m.servers[name]
	if server != nil && server.revoked {
		m.mu.Unlock()
		return nil
	}
	if server == nil {
		server = &serverLifecycle{}
		m.servers[name] = server
	}
	server.revoked = true
	cancel := server.cancel
	cfg := m.configs[name]
	cfg.Name = name
	enabled := false
	cfg.Enabled = &enabled
	m.configs[name] = cfg
	authStatus := authStatusForConfig(cfg)
	if current := m.statuses[name]; current.AuthStatus == MCPAuthStatusOAuth {
		authStatus = MCPAuthStatusOAuth
	}
	client := m.clients[name]
	delete(m.clients, name)
	m.statuses[name] = ServerStatus{Name: name, State: MCPServerStateDisabled, AuthStatus: authStatus}
	m.generation++
	if client != nil {
		m.operations.Add(1)
	}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if client != nil {
		defer m.operations.Done()
		return client.Close()
	}
	return nil
}

// Remove disconnects an MCP server by name.
func (m *Manager) Remove(name string) error {
	return m.Disconnect(name)
}

func (m *Manager) Disconnect(name string) error {
	m.mu.Lock()
	if m.serverRevoked(name) {
		m.mu.Unlock()
		return nil
	}
	c, ok := m.clients[name]
	authStatus := authStatusForConfig(m.configs[name])
	if current := m.statuses[name]; current.AuthStatus == MCPAuthStatusOAuth {
		authStatus = MCPAuthStatusOAuth
	}
	if !ok {
		if _, configured := m.configs[name]; configured {
			state := MCPServerStateStopped
			if !m.configs[name].IsEnabled() {
				state = MCPServerStateDisabled
			}
			m.statuses[name] = ServerStatus{Name: name, State: state, AuthStatus: authStatus}
			m.mu.Unlock()
			return nil
		}
		m.mu.Unlock()
		return fmt.Errorf("mcp server %q not found", name)
	}
	delete(m.clients, name)
	m.statuses[name] = ServerStatus{Name: name, State: MCPServerStateStopped, AuthStatus: authStatus}
	m.generation++
	m.operations.Add(1)
	m.mu.Unlock()
	defer m.operations.Done()
	return c.Close()
}

func (m *Manager) Generation() uint64 {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.generation
}

func (m *Manager) NativeTools() []NativeTool {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []NativeTool
	for name, client := range m.clients {
		for _, definition := range client.Tools() {
			out = append(out, NativeTool{
				Definition: definition,
				Client:     client,
				Timeout:    30 * time.Second,
				Provenance: extensions.Provenance{Kind: extensions.KindMCP, Source: name, Scope: "user"},
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		left := NewMCPTool(out[i].Client, out[i].Definition).Name()
		right := NewMCPTool(out[j].Client, out[j].Definition).Name()
		return left < right
	})
	return out
}

// AllTools returns every tool from every connected MCP server, wrapped as
// wuu-compatible *MCPTool instances. The caller owns the slice.
func (m *Manager) AllTools() []*MCPTool {
	native := m.NativeTools()
	out := make([]*MCPTool, 0, len(native))
	for _, tool := range native {
		out = append(out, NewMCPTool(tool.Client, tool.Definition))
	}
	return out
}

func (m *Manager) catalogChanged(name string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.serverRevoked(name) {
		return
	}
	m.generation++
	if status, ok := m.statuses[name]; ok {
		if client := m.clients[name]; client != nil {
			status.ToolCount = len(client.Tools())
			status.State = MCPServerStateReady
			status.Connected = true
			status.Error = ""
			m.statuses[name] = status
		}
	}
}

func (m *Manager) Config(name string) (ServerConfig, bool) {
	if m == nil {
		return ServerConfig{}, false
	}
	name = strings.TrimSpace(name)
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg, ok := m.configs[name]
	return cloneServerConfig(cfg), ok
}

func (m *Manager) Configs() []ServerConfig {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ServerConfig, 0, len(m.configs))
	for _, cfg := range m.configs {
		out = append(out, cloneServerConfig(cfg))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// Status returns a human-readable status summary for each configured server.
func (m *Manager) Status() map[string]ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]ServerStatus, len(m.statuses))
	for name, status := range m.statuses {
		if c, ok := m.clients[name]; ok {
			status.State = MCPServerStateConnected
			if status.AuthStatus == "" {
				status.AuthStatus = authStatusForConfig(m.configs[name])
			}
			status.Connected = true
			status.ToolCount = len(c.Tools())
			status.Error = ""
		}
		out[name] = status
	}
	return out
}

// Close shuts down all connections.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		done := m.closeDone
		m.mu.Unlock()
		<-done
		m.mu.RLock()
		err := m.closeErr
		m.mu.RUnlock()
		return err
	}
	m.closed = true
	cancel := m.cancel
	clients := make([]*Client, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.clients = make(map[string]*Client)
	m.generation++
	for name := range m.statuses {
		authStatus := authStatusForConfig(m.configs[name])
		if m.statuses[name].AuthStatus == MCPAuthStatusOAuth {
			authStatus = MCPAuthStatusOAuth
		}
		m.statuses[name] = ServerStatus{Name: name, State: MCPServerStateStopped, AuthStatus: authStatus}
	}
	m.mu.Unlock()

	cancel()
	var firstErr error
	for _, c := range clients {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	m.operations.Wait()
	m.mu.Lock()
	m.closeErr = firstErr
	close(m.closeDone)
	m.mu.Unlock()
	return firstErr
}

func (m *Manager) recordStatus(status ServerStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.serverRevoked(status.Name) {
		return
	}
	if strings.TrimSpace(status.Name) == "" {
		return
	}
	if m.statuses == nil {
		m.statuses = make(map[string]ServerStatus)
	}
	m.statuses[status.Name] = status
}

func (m *Manager) rememberConfig(cfg ServerConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.serverRevoked(cfg.Name) {
		return
	}
	if m.configs == nil {
		m.configs = make(map[string]ServerConfig)
	}
	m.configs[cfg.Name] = cloneServerConfig(cfg)
}

func (m *Manager) closeClient(name string) {
	m.mu.Lock()
	c := m.clients[name]
	if c != nil {
		delete(m.clients, name)
		m.generation++
		m.operations.Add(1)
	}
	m.mu.Unlock()
	if c != nil {
		defer m.operations.Done()
		_ = c.Close()
	}
}

func cloneServerConfig(cfg ServerConfig) ServerConfig {
	out := cfg
	if cfg.Args != nil {
		out.Args = append([]string(nil), cfg.Args...)
	}
	if cfg.Env != nil {
		out.Env = make(map[string]string, len(cfg.Env))
		for k, v := range cfg.Env {
			out.Env[k] = v
		}
	}
	if cfg.Headers != nil {
		out.Headers = make(map[string]string, len(cfg.Headers))
		for k, v := range cfg.Headers {
			out.Headers[k] = v
		}
	}
	if cfg.OAuth != nil {
		out.OAuth = &OAuthConfig{
			ClientID:     cfg.OAuth.ClientID,
			ClientSecret: cfg.OAuth.ClientSecret,
			Scopes:       append([]string(nil), cfg.OAuth.Scopes...),
			RedirectURI:  cfg.OAuth.RedirectURI,
		}
	}
	if cfg.ToolOverrides != nil {
		out.ToolOverrides = cloneToolOverrides(cfg.ToolOverrides)
	}
	return out
}

type MCPServerState string

const (
	MCPServerStateDisabled     MCPServerState = "disabled"
	MCPServerStateStarting     MCPServerState = "starting"
	MCPServerStateReady        MCPServerState = "ready"
	MCPServerStateAuthRequired MCPServerState = "auth_required"
	MCPServerStateReconnecting MCPServerState = "reconnecting"
	MCPServerStateError        MCPServerState = "error"
	MCPServerStateStopped      MCPServerState = "stopped"

	MCPServerStateConfigured              = MCPServerStateStopped
	MCPServerStateConnecting              = MCPServerStateStarting
	MCPServerStateConnected               = MCPServerStateReady
	MCPServerStateFailed                  = MCPServerStateError
	MCPServerStateNeedsAuth               = MCPServerStateAuthRequired
	MCPServerStateNeedsClientRegistration = MCPServerStateAuthRequired
)

type MCPAuthStatus string

const (
	MCPAuthStatusUnsupported MCPAuthStatus = "unsupported"
	MCPAuthStatusNotLoggedIn MCPAuthStatus = "not_logged_in"
	MCPAuthStatusBearerToken MCPAuthStatus = "bearer_token"
	MCPAuthStatusOAuth       MCPAuthStatus = "oauth"
)

func classifyConnectError(err error) MCPServerState {
	if err == nil {
		return MCPServerStateConnected
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "401") || strings.Contains(msg, "unauthorized"):
		return MCPServerStateNeedsAuth
	case strings.Contains(msg, "client registration"):
		return MCPServerStateNeedsClientRegistration
	default:
		return MCPServerStateFailed
	}
}

func authStatusForConfig(cfg ServerConfig) MCPAuthStatus {
	if strings.TrimSpace(cfg.URL) == "" {
		return MCPAuthStatusUnsupported
	}
	if cfg.OAuth != nil {
		return MCPAuthStatusNotLoggedIn
	}
	if hasAuthHeaders(cfg.Headers) {
		return MCPAuthStatusBearerToken
	}
	return MCPAuthStatusUnsupported
}

func connectedAuthStatus(cfg ServerConfig) MCPAuthStatus {
	if cfg.OAuth != nil {
		return MCPAuthStatusOAuth
	}
	return authStatusForConfig(cfg)
}

func authStatusAfterConnectError(cfg ServerConfig, err error) MCPAuthStatus {
	if classifyConnectError(err) == MCPServerStateNeedsAuth {
		return MCPAuthStatusNotLoggedIn
	}
	return authStatusForConfig(cfg)
}

func hasAuthHeaders(headers map[string]string) bool {
	for key, value := range headers {
		key = strings.ToLower(strings.TrimSpace(key))
		if strings.TrimSpace(value) == "" {
			continue
		}
		if key == "authorization" || strings.Contains(key, "api-key") || strings.Contains(key, "apikey") {
			return true
		}
	}
	return false
}

// ServerStatus describes one MCP server's runtime state.
type ServerStatus struct {
	Name       string         `json:"name"`
	State      MCPServerState `json:"state"`
	AuthStatus MCPAuthStatus  `json:"auth_status,omitempty"`
	Connected  bool           `json:"connected"`
	ToolCount  int            `json:"tool_count"`
	Error      string         `json:"error,omitempty"`
}
