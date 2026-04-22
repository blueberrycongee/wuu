package mcp

import (
	"context"
	"fmt"
	"sync"
)

// Manager holds all active MCP client connections and exposes their tools.
type Manager struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

// NewManager creates an empty MCP manager.
func NewManager() *Manager {
	return &Manager{clients: make(map[string]*Client)}
}

// Add connects and registers an MCP server. If the connection fails, the
// error is returned and no client is kept.
func (m *Manager) Add(ctx context.Context, cfg ServerConfig) error {
	var client *Client
	var err error
	if cfg.URL != "" {
		client, err = ConnectSSE(cfg)
	} else {
		client, err = ConnectStdio(cfg)
	}
	if err != nil {
		return err
	}
	// Eagerly discover tools so the toolkit can include them.
	if _, derr := client.DiscoverTools(ctx); derr != nil {
		_ = client.Close()
		return fmt.Errorf("discover tools for %q: %w", cfg.Name, derr)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.clients[cfg.Name]; ok {
		_ = old.Close()
	}
	m.clients[cfg.Name] = client
	return nil
}

// Remove disconnects an MCP server by name.
func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.clients[name]
	if !ok {
		return fmt.Errorf("mcp server %q not found", name)
	}
	delete(m.clients, name)
	return c.Close()
}

// AllTools returns every tool from every connected MCP server, wrapped as
// wuu-compatible *MCPTool instances. The caller owns the slice.
func (m *Manager) AllTools() []*MCPTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*MCPTool
	for _, c := range m.clients {
		for _, t := range c.Tools() {
			out = append(out, NewMCPTool(c, t))
		}
	}
	return out
}

// Status returns a human-readable status summary for each connected server.
func (m *Manager) Status() map[string]ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]ServerStatus, len(m.clients))
	for name, c := range m.clients {
		out[name] = ServerStatus{
			Name:      name,
			Connected: true,
			ToolCount: len(c.Tools()),
		}
	}
	return out
}

// Close shuts down all connections.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for _, c := range m.clients {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	m.clients = make(map[string]*Client)
	return firstErr
}

// ServerStatus describes one MCP server's runtime state.
type ServerStatus struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	ToolCount int    `json:"tool_count"`
	Error     string `json:"error,omitempty"`
}

// PromptCacheStablePrefix returns the number of built-in tools that should
// be kept before MCP tools in the tool list. This preserves prompt cache
// stability: built-in tools form a stable prefix; MCP tools (which may
// change across sessions) are appended so they don't invalidate cache
// anchors inside the built-in block.
func PromptCacheStablePrefix() int {
	// wuu currently registers ~20 built-in tools. We return a generous
	// upper bound so the toolkit can sort MCP tools after all built-ins.
	return 50
}
