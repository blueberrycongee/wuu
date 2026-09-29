package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/capability"
)

const defaultConnectionTimeout = 30 * time.Second
const modernDiscoveryTimeout = 3 * time.Second

const (
	maxToolCatalogPages       = 100
	maxToolCatalogItems       = 2048
	maxToolCatalogCursorBytes = 64 * 1024
)

// ServerConfig describes one MCP server connection.
type ServerConfig struct {
	Name    string   `json:"name"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"`
	// Transport selects the wire protocol for URL servers: "http" (streamable
	// HTTP, aliases "streamable-http"/"streamable_http") or "sse" (legacy
	// HTTP+SSE). Empty means auto: try streamable HTTP first and fall back to
	// SSE when the endpoint rejects the initialize POST (see ConnectRemote).
	// The names follow Claude Code's .mcp.json convention. Ignored for stdio
	// (command) servers.
	Transport     string                  `json:"transport,omitempty"`
	Env           map[string]string       `json:"env,omitempty"`
	Headers       map[string]string       `json:"headers,omitempty"`
	OAuth         *OAuthConfig            `json:"oauth,omitempty"`
	Enabled       *bool                   `json:"enabled,omitempty"`
	ToolOverrides map[string]ToolOverride `json:"tool_overrides,omitempty"`
}

// Canonical ServerConfig.Transport values. "http" (not "streamable-http") is
// canonical because that is the name Claude Code's .mcp.json uses for its
// StreamableHTTPClientTransport.
const (
	TransportAuto           = ""
	TransportSSE            = "sse"
	TransportStreamableHTTP = "http"
)

// normalizeTransport canonicalizes a user-supplied transport name.
func normalizeTransport(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return TransportAuto, nil
	case "sse":
		return TransportSSE, nil
	case "http", "streamable-http", "streamable_http", "streamablehttp":
		return TransportStreamableHTTP, nil
	default:
		return "", fmt.Errorf("unsupported transport %q (want \"http\"/\"streamable-http\", \"sse\", or empty for auto)", v)
	}
}

type OAuthConfig struct {
	ClientID     string   `json:"client_id,omitempty"`
	ClientSecret string   `json:"client_secret,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	RedirectURI  string   `json:"redirect_uri,omitempty"`
}

// TokenProvider supplies the current bearer token immediately before a
// request. forceRefresh is used once after an HTTP 401 response.
type TokenProvider func(ctx context.Context, forceRefresh bool) (string, error)

func (c ServerConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// ToolOverride lets local config correct or supplement MCP tool metadata.
type ToolOverride struct {
	ReadOnly        *bool                 `json:"read_only,omitempty"`
	ConcurrencySafe *bool                 `json:"concurrency_safe,omitempty"`
	Capability      capability.Capability `json:"capability,omitempty"`
}

// Client is a connected MCP server session.
type Client struct {
	name                     string
	transport                Transport
	inFlight                 *inFlight
	readLoop                 *readLoop
	protocolVersion          string
	mu                       sync.RWMutex
	tools                    []Tool
	overrides                map[string]ToolOverride
	onToolsChanged           func()
	onConnectionClosed       func(error)
	connectionErr            error
	connectionClosedNotified bool
	closed                   bool
	closeOnce                sync.Once
	closeErr                 error
	toolRefreshMu            sync.Mutex
	toolRefreshRunning       bool
	toolRefreshPending       bool
}

// Connect establishes an MCP session with the given transport.
func Connect(ctx context.Context, name string, t Transport) (*Client, error) {
	return connect(ctx, name, t, true)
}

func connect(ctx context.Context, name string, t Transport, tryModern bool) (*Client, error) {
	ctx, cancel := withDefaultConnectionTimeout(ctx)
	defer cancel()

	c := &Client{
		name:      name,
		transport: t,
		inFlight:  newInFlight(),
	}
	c.readLoop = newReadLoop(t, c.inFlight, c.handleNotification, c.handleRequest, c.handleReadLoopExit)
	c.readLoop.Start()

	// Modern servers advertise the stateless 2026-07-28 protocol through
	// server/discover. Legacy servers reject that method and continue through
	// the initialize/initialized handshake below.
	if tryModern {
		discoveryCtx, cancelDiscovery := context.WithTimeout(ctx, modernDiscoveryTimeout)
		discoverBytes, discoverErr := callWithProtocol(discoveryCtx, t, c.inFlight, "server/discover", nil, PreferredProtocolVersion)
		cancelDiscovery()
		if discoverErr == nil {
			var discovered DiscoverResult
			if err := json.Unmarshal(discoverBytes, &discovered); err == nil && containsString(discovered.SupportedVersions, PreferredProtocolVersion) {
				c.protocolVersion = PreferredProtocolVersion
				return c, nil
			}
		}
		if err := ctx.Err(); err != nil {
			c.Close()
			return nil, err
		}
	}

	// Legacy initialize handshake.
	params := InitializeParams{ProtocolVersion: PreferredLegacyProtocolVersion}
	params.ClientInfo.Name = "wuu"
	params.ClientInfo.Version = "0.1.0"
	resultBytes, err := call(ctx, t, c.inFlight, "initialize", params)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}
	var result InitializeResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp initialize decode: %w", err)
	}
	if err := validateProtocolVersion(result.ProtocolVersion); err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp initialize compatibility: %w", err)
	}
	if result.ProtocolVersion == PreferredProtocolVersion {
		c.Close()
		return nil, fmt.Errorf("mcp initialize compatibility: protocol %s does not use initialize", result.ProtocolVersion)
	}
	c.protocolVersion = result.ProtocolVersion

	// A failed initialized notification leaves the server and client with
	// different session state, so treat it as a failed handshake.
	if err := t.Send(ctx, Request{JSONRPC: "2.0", Method: "notifications/initialized"}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp initialized notification: %w", err)
	}

	return c, nil
}

func withDefaultConnectionTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, defaultConnectionTimeout)
}

// ConnectStdio starts a local command as an MCP stdio server and connects.
func ConnectStdio(ctx context.Context, cfg ServerConfig) (*Client, error) {
	cmd := cfg.Command
	args := cfg.Args
	if cmd == "" {
		return nil, fmt.Errorf("mcp server %q: command is required for stdio transport", cfg.Name)
	}
	t, err := NewStdioTransportWithEnv(cmd, args, cfg.Env)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: %w", cfg.Name, err)
	}
	c, err := Connect(ctx, cfg.Name, t)
	if err != nil {
		return nil, err
	}
	c.SetToolOverrides(cfg.ToolOverrides)
	return c, nil
}

// ConnectSSE connects to a remote MCP server over SSE.
func ConnectSSE(ctx context.Context, cfg ServerConfig) (*Client, error) {
	return connectSSEWithTokenProvider(ctx, cfg, nil)
}

func connectSSEWithTokenProvider(ctx context.Context, cfg ServerConfig, tokenProvider TokenProvider) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("mcp server %q: url is required for sse transport", cfg.Name)
	}
	ctx, cancel := withDefaultConnectionTimeout(ctx)
	defer cancel()

	t, err := newSSETransportWithAuth(ctx, cfg.URL, cfg.Headers, tokenProvider)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: %w", cfg.Name, err)
	}
	c, err := connect(ctx, cfg.Name, t, false)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: SSE transport at %s: %w", cfg.Name, cfg.URL, err)
	}
	c.SetToolOverrides(cfg.ToolOverrides)
	return c, nil
}

// ConnectStreamableHTTP connects to a remote MCP server over the streamable
// HTTP transport (MCP spec revision 2025-03-26+).
func ConnectStreamableHTTP(ctx context.Context, cfg ServerConfig) (*Client, error) {
	return connectStreamableHTTPWithTokenProvider(ctx, cfg, nil)
}

func connectStreamableHTTPWithTokenProvider(ctx context.Context, cfg ServerConfig, tokenProvider TokenProvider) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("mcp server %q: url is required for streamable HTTP transport", cfg.Name)
	}
	t := NewStreamableHTTPTransportWithAuth(cfg.URL, cfg.Headers, tokenProvider)
	c, err := Connect(ctx, cfg.Name, t)
	if err != nil {
		// Connect already closed the transport via c.Close().
		return nil, fmt.Errorf("mcp server %q: streamable HTTP transport at %s: %w", cfg.Name, cfg.URL, err)
	}
	c.SetToolOverrides(cfg.ToolOverrides)
	return c, nil
}

// ConnectRemote connects to a URL-based MCP server, honoring
// cfg.Transport:
//
//   - "http" / "streamable-http": streamable HTTP only; a failure is
//     reported as-is with no SSE fallback (the user pinned the transport).
//   - "sse": legacy HTTP+SSE only, exactly the pre-transport-field behavior.
//   - empty (auto): probe streamable HTTP first; if both modern discovery and
//     legacy initialization show the endpoint is incompatible via HTTP 4xx
//     (e.g. 405/404) or a
//     non-JSON-RPC reply, fall back to legacy SSE. This is the client
//     backwards-compatibility strategy from the MCP spec's transports
//     chapter ("Backwards Compatibility"), so existing SSE-only server
//     configs keep working without changes. Network-level failures do not
//     fall back — SSE against the same unreachable endpoint would fail too.
func ConnectRemote(ctx context.Context, cfg ServerConfig) (*Client, error) {
	return connectRemoteWithTokenProvider(ctx, cfg, nil)
}

func connectRemoteWithTokenProvider(ctx context.Context, cfg ServerConfig, tokenProvider TokenProvider) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("mcp server %q: url is required for a remote transport", cfg.Name)
	}
	ctx, cancel := withDefaultConnectionTimeout(ctx)
	defer cancel()

	transport, err := normalizeTransport(cfg.Transport)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: %w", cfg.Name, err)
	}
	switch transport {
	case TransportSSE:
		return connectSSEWithTokenProvider(ctx, cfg, tokenProvider)
	case TransportStreamableHTTP:
		c, err := connectStreamableHTTPWithTokenProvider(ctx, cfg, tokenProvider)
		if err != nil {
			return nil, fmt.Errorf("%w (transport %q explicitly configured; not falling back to SSE)", err, strings.TrimSpace(cfg.Transport))
		}
		return c, nil
	}
	// Auto: streamable HTTP first, SSE fallback for "not a streamable HTTP
	// endpoint" failures.
	c, httpErr := connectStreamableHTTPWithTokenProvider(ctx, cfg, tokenProvider)
	if httpErr == nil {
		return c, nil
	}
	var serr *streamableHTTPError
	if !errors.As(httpErr, &serr) || !serr.fallbackToSSE() {
		return nil, httpErr
	}
	c, sseErr := connectSSEWithTokenProvider(ctx, cfg, tokenProvider)
	if sseErr != nil {
		return nil, fmt.Errorf("%v; SSE fallback also failed: %w", httpErr, sseErr)
	}
	return c, nil
}

// Name returns the server name.
func (c *Client) Name() string { return c.name }

// DiscoverTools fetches the tool list from the server and caches it.
func (c *Client) DiscoverTools(ctx context.Context) ([]Tool, error) {
	var tools []Tool
	var cursor string
	seenCursors := make(map[string]struct{})
	for page := 0; page < maxToolCatalogPages; page++ {
		var params any
		if cursor != "" {
			params = ListToolsParams{Cursor: cursor}
		}
		resultBytes, err := callWithProtocol(ctx, c.transport, c.inFlight, "tools/list", params, c.protocolVersion)
		if err != nil {
			return nil, err
		}
		var result ListToolsResult
		if err := json.Unmarshal(resultBytes, &result); err != nil {
			return nil, fmt.Errorf("decode tools/list page %d: %w", page+1, err)
		}
		if len(result.Tools) > maxToolCatalogItems-len(tools) {
			return nil, fmt.Errorf("tools/list exceeded the catalog limit of %d tools", maxToolCatalogItems)
		}
		tools = append(tools, result.Tools...)
		next := result.NextCursor
		if next == "" {
			break
		}
		if len(next) > maxToolCatalogCursorBytes {
			return nil, fmt.Errorf("tools/list returned a cursor exceeding %d bytes", maxToolCatalogCursorBytes)
		}
		if _, duplicate := seenCursors[next]; duplicate {
			return nil, fmt.Errorf("tools/list returned a repeated pagination cursor")
		}
		seenCursors[next] = struct{}{}
		cursor = next
		if page == maxToolCatalogPages-1 {
			return nil, fmt.Errorf("tools/list exceeded the pagination limit of %d pages", maxToolCatalogPages)
		}
	}
	c.mu.Lock()
	c.tools = tools
	callback := c.onToolsChanged
	c.mu.Unlock()
	if callback != nil {
		callback()
	}
	return tools, nil
}

func (c *Client) SetToolsChangedCallback(callback func()) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.onToolsChanged = callback
	c.mu.Unlock()
}

// SetConnectionClosedCallback registers a callback for an unexpected
// transport termination. If the connection has already failed, the callback
// is invoked immediately.
func (c *Client) SetConnectionClosedCallback(callback func(error)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.onConnectionClosed = callback
	err := c.connectionErr
	if callback != nil && err != nil && !c.connectionClosedNotified {
		c.connectionClosedNotified = true
	} else {
		callback = nil
	}
	c.mu.Unlock()
	if callback != nil {
		callback(err)
	}
}

// ConnectionError returns the terminal transport error, if the connection
// ended unexpectedly.
func (c *Client) ConnectionError() error {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connectionErr
}

func (c *Client) handleReadLoopExit(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	if c.connectionErr == nil {
		c.connectionErr = err
	}
	callback := c.onConnectionClosed
	if callback != nil && !c.connectionClosedNotified {
		c.connectionClosedNotified = true
	} else {
		callback = nil
	}
	connectionErr := c.connectionErr
	c.mu.Unlock()
	if callback != nil {
		callback(connectionErr)
	}
	_ = c.Close()
}

func (c *Client) handleNotification(method string, _ json.RawMessage) {
	switch method {
	case "notifications/tools/list_changed", "tools/list_changed":
		c.scheduleToolRefresh()
	}
}

func (c *Client) scheduleToolRefresh() {
	c.toolRefreshMu.Lock()
	c.toolRefreshPending = true
	if c.toolRefreshRunning {
		c.toolRefreshMu.Unlock()
		return
	}
	c.toolRefreshRunning = true
	c.toolRefreshMu.Unlock()
	go func() {
		for {
			c.toolRefreshMu.Lock()
			c.toolRefreshPending = false
			c.toolRefreshMu.Unlock()
			refreshCtx, cancel := context.WithTimeout(context.Background(), defaultConnectionTimeout)
			_, _ = c.DiscoverTools(refreshCtx)
			cancel()
			c.toolRefreshMu.Lock()
			if !c.toolRefreshPending {
				c.toolRefreshRunning = false
				c.toolRefreshMu.Unlock()
				return
			}
			c.toolRefreshPending = false
			c.toolRefreshMu.Unlock()
		}
	}()
}

func (c *Client) handleRequest(method string, _ json.RawMessage) (json.RawMessage, *RPCError) {
	switch method {
	case "elicitation/create", "elicitation/request":
		return nil, &RPCError{Code: -32000, Message: "MCP elicitation is not supported yet"}
	default:
		return nil, &RPCError{Code: -32601, Message: "MCP client request is not supported"}
	}
}

// Tools returns the cached tool list.
func (c *Client) Tools() []Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Tool, len(c.tools))
	copy(out, c.tools)
	return out
}

// SetToolOverrides replaces local metadata overrides for this server.
func (c *Client) SetToolOverrides(overrides map[string]ToolOverride) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.overrides = cloneToolOverrides(overrides)
}

// ToolOverride returns the local metadata override for a tool, when present.
func (c *Client) ToolOverride(toolName string) (ToolOverride, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	override, ok := c.overrides[toolName]
	return override, ok
}

func cloneToolOverrides(in map[string]ToolOverride) map[string]ToolOverride {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]ToolOverride, len(in))
	for name, override := range in {
		out[name] = override
	}
	return out
}

// CallTool invokes a tool on the server.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (*CallToolResult, error) {
	params := CallToolParams{Name: name, Arguments: arguments}
	resultBytes, err := callWithProtocol(ctx, c.transport, c.inFlight, "tools/call", params, c.protocolVersion)
	if err != nil {
		return nil, err
	}
	var result CallToolResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, fmt.Errorf("decode tools/call: %w", err)
	}
	return &result, nil
}

// Close shuts down the client.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		c.readLoop.signalStop()
		c.closeErr = c.transport.Close()
		c.readLoop.Stop()
		c.inFlight.closeAll()
	})
	return c.closeErr
}
