// Package mcp implements a lightweight Model Context Protocol client.
//
// It supports stdio and SSE transports, tool discovery, and invocation.
// Design aligned with Claude Code's MCP client and Kimi CLI's fastmcp
// integration, but kept minimal to avoid heavy dependencies.
package mcp

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

// Protocol version negotiated during initialize.
const protocolVersion = "2024-11-05"

// Request is a JSON-RPC request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("mcp rpc error %d: %s", e.Code, e.Message)
}

// InitializeParams are sent in the initialize request.
type InitializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	Capabilities    struct {
	} `json:"capabilities"`
	ClientInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

// InitializeResult is returned by initialize.
type InitializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	Capabilities    struct {
		Tools *struct {
			ListChanged bool `json:"listChanged,omitempty"`
		} `json:"tools,omitempty"`
	} `json:"capabilities"`
	ServerInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

// Tool represents an MCP tool definition.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ListToolsResult is returned by tools/list.
type ListToolsResult struct {
	Tools []Tool `json:"tools"`
}

// CallToolParams are sent in tools/call.
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult is returned by tools/call.
type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent is a single content item in a tool result.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

var requestIDCounter int64

func nextRequestID() int64 {
	return atomic.AddInt64(&requestIDCounter, 1)
}

// inFlight tracks pending requests.
type inFlight struct {
	mu       sync.Mutex
	pending  map[int64]chan Response
	closed   bool
}

func newInFlight() *inFlight {
	return &inFlight{pending: make(map[int64]chan Response)}
}

func (f *inFlight) register(id int64) <-chan Response {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		ch := make(chan Response, 1)
		ch <- Response{Error: &RPCError{Code: -32000, Message: "client closed"}}
		return ch
	}
	ch := make(chan Response, 1)
	f.pending[id] = ch
	return ch
}

func (f *inFlight) resolve(id int64, resp Response) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch, ok := f.pending[id]
	if !ok {
		return false
	}
	delete(f.pending, id)
	ch <- resp
	return true
}

func (f *inFlight) closeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	for id, ch := range f.pending {
		ch <- Response{Error: &RPCError{Code: -32000, Message: "client closed"}}
		delete(f.pending, id)
	}
}
