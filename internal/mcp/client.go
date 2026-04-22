package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// ServerConfig describes one MCP server connection.
type ServerConfig struct {
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	URL     string            `json:"url,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// Client is a connected MCP server session.
type Client struct {
	name      string
	transport Transport
	inFlight  *inFlight
	readLoop  *readLoop
	mu        sync.RWMutex
	tools     []Tool
	closed    bool
}

// Connect establishes an MCP session with the given transport.
func Connect(name string, t Transport) (*Client, error) {
	c := &Client{
		name:      name,
		transport: t,
		inFlight:  newInFlight(),
	}
	c.readLoop = newReadLoop(t, c.inFlight, nil)
	c.readLoop.Start()

	// Initialize handshake.
	params := InitializeParams{ProtocolVersion: protocolVersion}
	params.ClientInfo.Name = "wuu"
	params.ClientInfo.Version = "0.1.0"
	resultBytes, err := call(context.Background(), t, c.inFlight, "initialize", params)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}
	var result InitializeResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp initialize decode: %w", err)
	}

	// Send initialized notification.
	_ = t.Send(context.Background(), Request{JSONRPC: "2.0", Method: "notifications/initialized"})

	return c, nil
}

// ConnectStdio starts a local command as an MCP stdio server and connects.
func ConnectStdio(cfg ServerConfig) (*Client, error) {
	cmd := cfg.Command
	args := cfg.Args
	if cmd == "" {
		return nil, fmt.Errorf("mcp server %q: command is required for stdio transport", cfg.Name)
	}
	t, err := NewStdioTransport(cmd, args...)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: %w", cfg.Name, err)
	}
	c, err := Connect(cfg.Name, t)
	if err != nil {
		_ = t.Close()
		return nil, err
	}
	return c, nil
}

// ConnectSSE connects to a remote MCP server over SSE.
func ConnectSSE(cfg ServerConfig) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("mcp server %q: url is required for sse transport", cfg.Name)
	}
	t, err := NewSSETransport(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: %w", cfg.Name, err)
	}
	c, err := Connect(cfg.Name, t)
	if err != nil {
		_ = t.Close()
		return nil, err
	}
	return c, nil
}

// Name returns the server name.
func (c *Client) Name() string { return c.name }

// DiscoverTools fetches the tool list from the server and caches it.
func (c *Client) DiscoverTools(ctx context.Context) ([]Tool, error) {
	resultBytes, err := call(ctx, c.transport, c.inFlight, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var result ListToolsResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, fmt.Errorf("decode tools/list: %w", err)
	}
	c.mu.Lock()
	c.tools = result.Tools
	c.mu.Unlock()
	return result.Tools, nil
}

// Tools returns the cached tool list.
func (c *Client) Tools() []Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Tool, len(c.tools))
	copy(out, c.tools)
	return out
}

// CallTool invokes a tool on the server.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (*CallToolResult, error) {
	params := CallToolParams{Name: name, Arguments: arguments}
	resultBytes, err := call(ctx, c.transport, c.inFlight, "tools/call", params)
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
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.readLoop.Stop()
	c.inFlight.closeAll()
	return c.transport.Close()
}
