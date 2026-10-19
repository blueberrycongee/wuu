package appserver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// externalCapabilities owns one turn's authority. The unguessable endpoint is
// handed only to its engine process; request data never supplies scope or grants.
type externalCapabilities struct {
	ctx      context.Context
	cancel   context.CancelFunc
	server   *http.Server
	host     string
	path     string
	executor agent.ToolExecutor
	tools    []mcp.Tool
	allowed  map[string]bool
	serial   chan struct{}

	mu       sync.Mutex
	closed   bool
	requests map[string]context.CancelFunc
	active   sync.WaitGroup
	close    func()
}

func startExternalCapabilities(ctx context.Context, thread *runtime.ThreadRuntime) (*externalCapabilities, error) {
	if thread == nil || thread.CapabilityTools == nil || thread.Toolkit == nil {
		return nil, nil
	}
	// Pin the actual MCP connection objects as well as their definitions. A
	// helper reconnect cannot let delayed work borrow the replacement's control.
	thread.Toolkit.FreezeToolSurface()
	definitions := thread.CapabilityTools.Definitions()
	if len(definitions) == 0 {
		thread.Toolkit.UnfreezeToolSurface()
		return nil, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		thread.Toolkit.UnfreezeToolSurface()
		return nil, fmt.Errorf("start host capability endpoint: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	bridge := &externalCapabilities{
		ctx: ctx, cancel: cancel, host: listener.Addr().String(), path: "/mcp/" + rand.Text(),
		executor: thread.CapabilityTools, allowed: make(map[string]bool, len(definitions)),
		serial: make(chan struct{}, 1), requests: make(map[string]context.CancelFunc),
	}
	for _, definition := range definitions {
		schema, err := json.Marshal(definition.InputSchema)
		if err != nil {
			cancel()
			_ = listener.Close()
			thread.Toolkit.UnfreezeToolSurface()
			return nil, fmt.Errorf("encode host tool %s: %w", definition.Name, err)
		}
		bridge.tools = append(bridge.tools, mcp.Tool{Name: definition.Name, Description: definition.Description, InputSchema: schema})
		bridge.allowed[definition.Name] = true
	}
	bridge.server = &http.Server{Handler: bridge, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}
	bridge.close = sync.OnceFunc(func() {
		bridge.mu.Lock()
		bridge.closed = true
		bridge.cancel()
		bridge.mu.Unlock()
		_ = bridge.server.Close()
		// The cached toolkit cannot be rebound for the next turn while any old
		// request is still unwinding through hooks, permissions or UI cleanup.
		bridge.active.Wait()
		thread.Toolkit.UnfreezeToolSurface()
	})
	go func() { _ = bridge.server.Serve(listener) }()
	context.AfterFunc(ctx, bridge.Close)
	return bridge, nil
}

func (b *externalCapabilities) Binding() agentengine.MCPServer {
	return agentengine.MCPServer{Name: "wuu_host", URL: "http://" + b.host + b.path}
}

func (b *externalCapabilities) Close() { b.close() }

func (b *externalCapabilities) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Exact Host validation blocks DNS rebinding. Browser origins never receive
	// local control authority, even if a page learns the process-local URL.
	if r.Host != b.host || r.Header.Get("Origin") != "" {
		http.Error(w, "host capability origin rejected", http.StatusForbidden)
		return
	}
	if r.URL.Path != b.path || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	b.mu.Lock()
	if b.closed || activity.ControlErr(b.ctx) != nil {
		b.mu.Unlock()
		http.Error(w, "host capability turn has ended or control was revoked", http.StatusGone)
		return
	}
	b.active.Add(1)
	b.mu.Unlock()
	defer b.active.Done()
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "only MCP POST is supported", http.StatusMethodNotAllowed)
		return
	}
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		http.Error(w, "MCP requires application/json", http.StatusUnsupportedMediaType)
		return
	}
	var request struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	if err := decoder.Decode(&request); err != nil {
		writeCapabilityRPC(w, nil, nil, &mcp.RPCError{Code: -32700, Message: "invalid JSON-RPC request"})
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) || request.JSONRPC != "2.0" || request.Method == "" || !validCapabilityRequestID(request.ID) {
		writeCapabilityRPC(w, nil, nil, &mcp.RPCError{Code: -32600, Message: "invalid JSON-RPC request"})
		return
	}
	if len(request.ID) == 0 {
		if request.Method == "notifications/cancelled" {
			var params struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(request.Params, &params) == nil {
				b.mu.Lock()
				cancel := b.requests[string(params.RequestID)]
				b.mu.Unlock()
				if cancel != nil {
					cancel()
				}
			}
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if version := r.Header.Get("MCP-Protocol-Version"); version != "" && request.Method != "server/discover" && !capabilityProtocolVersion(version) {
		http.Error(w, "unsupported MCP protocol version", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithCancel(b.ctx)
	stop := context.AfterFunc(r.Context(), cancel)
	defer func() { stop(); cancel() }()
	id := string(request.ID)
	b.mu.Lock()
	if _, duplicate := b.requests[id]; duplicate {
		b.mu.Unlock()
		writeCapabilityRPC(w, request.ID, nil, &mcp.RPCError{Code: -32600, Message: "request ID is already in flight"})
		return
	}
	b.requests[id] = cancel
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.requests, id); b.mu.Unlock() }()

	var result any
	var rpcError *mcp.RPCError
	switch request.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			rpcError = &mcp.RPCError{Code: -32602, Message: "invalid initialize parameters"}
			break
		}
		version := params.ProtocolVersion
		if !capabilityProtocolVersion(version) {
			version = mcp.PreferredLegacyProtocolVersion
		}
		result = map[string]any{
			"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo":   map[string]string{"name": "wuu-host-capabilities", "version": "1"},
			"instructions": "These Wuu browser and computer tools are bound to this conversation and turn. User takeover or stop revokes control. Use their native image results to inspect screenshots.",
		}
	case "ping":
		result = map[string]any{}
	case "tools/list":
		var params mcp.ListToolsParams
		if len(request.Params) > 0 && json.Unmarshal(request.Params, &params) != nil || params.Cursor != "" {
			rpcError = &mcp.RPCError{Code: -32602, Message: "invalid tool catalog cursor"}
			break
		}
		result = mcp.ListToolsResult{Tools: b.tools}
	case "tools/call":
		var params mcp.CallToolParams
		if json.Unmarshal(request.Params, &params) != nil || !b.allowed[params.Name] {
			rpcError = &mcp.RPCError{Code: -32602, Message: "unknown host capability tool"}
			break
		}
		if len(params.Arguments) == 0 {
			params.Arguments = json.RawMessage(`{}`)
		}
		var arguments map[string]json.RawMessage
		if json.Unmarshal(params.Arguments, &arguments) != nil || arguments == nil {
			rpcError = &mcp.RPCError{Code: -32602, Message: "tool arguments must be an object"}
			break
		}
		result = b.call(ctx, providers.ToolCall{ID: rand.Text(), Name: params.Name, Arguments: string(params.Arguments)})
	default:
		rpcError = &mcp.RPCError{Code: -32601, Message: "method not found"}
	}
	writeCapabilityRPC(w, request.ID, result, rpcError)
}

func (b *externalCapabilities) call(ctx context.Context, call providers.ToolCall) mcp.CallToolResult {
	select {
	case b.serial <- struct{}{}:
		defer func() { <-b.serial }()
	case <-ctx.Done():
		return capabilityToolResult(toolresult.Result{}, ctx.Err())
	}
	if err := activity.ControlErr(ctx); err != nil {
		return capabilityToolResult(toolresult.Result{}, err)
	}
	var result toolresult.Result
	var err error
	if rich, ok := b.executor.(agent.RichToolExecutor); ok {
		result, err = rich.ExecuteResult(ctx, call)
	} else {
		var text string
		text, err = b.executor.Execute(ctx, call)
		result = toolresult.FromText(text)
	}
	if contextProvider, ok := b.executor.(agent.ToolCallContextProvider); ok {
		if additional := contextProvider.TakeAdditionalContext(call); additional != "" {
			result.Content = append(result.Content, toolresult.ContentPart{Type: toolresult.ContentTypeText, Text: additional})
		}
	}
	return capabilityToolResult(result, err)
}

func capabilityToolResult(result toolresult.Result, err error) mcp.CallToolResult {
	out := mcp.CallToolResult{Content: make([]mcp.ToolContent, 0, len(result.Content)+1), StructuredContent: result.StructuredContent, IsError: result.IsError || err != nil, Meta: result.Meta}
	for _, part := range result.Content {
		out.Content = append(out.Content, mcp.ToolContent{Type: part.Type, Text: part.Text, Data: part.Data, MIMEType: part.MIMEType, URI: part.URI, Name: part.Name, Resource: part.Resource})
	}
	if err != nil {
		out.Content = append(out.Content, mcp.ToolContent{Type: "text", Text: err.Error()})
	}
	if result.Activity != nil {
		meta := map[string]any{}
		_ = json.Unmarshal(out.Meta, &meta)
		if meta == nil {
			meta = map[string]any{}
		}
		meta["wuu/activity"] = result.Activity
		out.Meta, _ = json.Marshal(meta)
	}
	return out
}

func capabilityProtocolVersion(version string) bool {
	switch version {
	case "2025-03-26", "2025-06-18", "2025-11-25":
		return true
	default:
		return false
	}
}

func validCapabilityRequestID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	if id[0] == '"' {
		var value string
		return json.Unmarshal(id, &value) == nil
	}
	_, err := strconv.ParseInt(strings.TrimSpace(string(id)), 10, 64)
	return err == nil
}

func writeCapabilityRPC(w http.ResponseWriter, id json.RawMessage, result any, rpcError *mcp.RPCError) {
	if len(id) == 0 {
		id = json.RawMessage(`null`)
	}
	response := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcError != nil {
		response["error"] = rpcError
	} else {
		response["result"] = result
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
