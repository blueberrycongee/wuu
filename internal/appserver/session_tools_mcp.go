package appserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

func localMCPServer(name, endpoint string) (agentengine.MCPServer, error) {
	binary, err := os.Executable()
	if err != nil {
		return agentengine.MCPServer{}, err
	}
	return agentengine.MCPServer{Name: name, URL: endpoint, Stdio: &agentengine.MCPStdioServer{
		Command: binary, Args: []string{"session-tools", "--stdio"},
		Env: map[string]string{"WUU_SESSION_TOOLS_URL": endpoint},
	}}, nil
}

// startSessionToolsMCP binds a capability to one Wuu turn, never to a
// caller-supplied session id or the engine's native conversation reference.
func (s *Server) startSessionToolsMCP(ctx context.Context, th *threadState, turnID string) (agentengine.MCPServer, string, func(), error) {
	noServer := func() {}
	metadata, exists, err := session.Find(s.rt.SessionDir, th.ID)
	if err != nil {
		return agentengine.MCPServer{}, "", noServer, err
	}
	if !exists || metadata.ArchivedAt != nil || metadata.Visibility == pluginhost.SessionVisibilityPlugin || s.rt.Toolkit == nil {
		return agentengine.MCPServer{}, "", noServer, nil
	}
	cwd := firstNonEmpty(th.CWD, s.rt.RootDir)
	tools, err := s.rt.AcquireExternalPluginTools(th.ID, cwd, th.PermissionMode)
	if err != nil {
		return agentengine.MCPServer{}, "", noServer, err
	}
	defer tools.Close()
	tools.ToolExecutor, err = applySessionToolPolicy(tools.ToolExecutor, metadata.ToolPolicyJSON)
	if err != nil {
		return agentengine.MCPServer{}, "", noServer, err
	}
	if len(tools.Definitions()) == 0 {
		return agentengine.MCPServer{}, "", noServer, nil
	}
	instructions, err := tools.Instructions(ctx, th.ModelProvider, th.Model)
	if err != nil {
		return agentengine.MCPServer{}, "", noServer, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return agentengine.MCPServer{}, "", noServer, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return agentengine.MCPServer{}, "", noServer, err
	}
	path := "/" + hex.EncodeToString(secret)
	binding, err := localMCPServer("wuu_session_tools", "http://"+listener.Addr().String()+path)
	if err != nil {
		_ = listener.Close()
		return agentengine.MCPServer{}, "", noServer, err
	}
	turnCtx, cancel := context.WithCancel(ctx)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return turnCtx }}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != listener.Addr().String() || r.Header.Get("Origin") != "" {
			http.Error(w, "origin is not allowed", http.StatusForbidden)
			return
		}
		if r.URL.Path != path || turnCtx.Err() != nil || s.closed.Load() {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.handleSessionToolsMCP(w, r, th, turnID, cwd)
	})
	go func() { _ = server.Serve(listener) }()
	stopOnCancel := context.AfterFunc(turnCtx, func() { _ = server.Close() })
	var once sync.Once
	stop := func() {
		once.Do(func() {
			stopOnCancel()
			cancel()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer shutdownCancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				_ = server.Close()
			}
		})
	}
	return binding, instructions, stop, nil
}

func (s *Server) handleSessionToolsMCP(w http.ResponseWriter, r *http.Request, th *threadState, turnID, cwd string) {
	var request namedAgentMCPRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
		writeNamedAgentMCPResponse(w, namedAgentMCPResponse{JSONRPC: "2.0", Error: map[string]any{"code": -32700, "message": "invalid JSON-RPC request"}})
		return
	}
	response := namedAgentMCPResponse{JSONRPC: "2.0", ID: request.ID}
	if request.JSONRPC != "2.0" || request.Method == "" {
		response.Error = map[string]any{"code": -32600, "message": "invalid JSON-RPC request"}
		writeNamedAgentMCPResponse(w, response)
		return
	}
	if len(request.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	th.mu.Lock()
	active, mode := th.running && th.currentTurn == turnID, th.PermissionMode
	th.mu.Unlock()
	metadata, exists, err := session.Find(s.rt.SessionDir, th.ID)
	if err != nil || !exists || metadata.ArchivedAt != nil || metadata.Visibility == pluginhost.SessionVisibilityPlugin || !active {
		response.Error = map[string]any{"code": -32000, "message": "session tool capability is no longer active"}
		writeNamedAgentMCPResponse(w, response)
		return
	}
	tools, err := s.rt.AcquireExternalPluginTools(th.ID, cwd, mode)
	if err != nil {
		response.Error = map[string]any{"code": -32000, "message": err.Error()}
		writeNamedAgentMCPResponse(w, response)
		return
	}
	defer tools.Close()
	tools.ToolExecutor, err = applySessionToolPolicy(tools.ToolExecutor, metadata.ToolPolicyJSON)
	if err != nil {
		response.Error = map[string]any{"code": -32000, "message": err.Error()}
		writeNamedAgentMCPResponse(w, response)
		return
	}
	switch request.Method {
	case "initialize":
		response.Result = map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "wuu_session_tools", "version": "1"},
		}
	case "ping":
		response.Result = map[string]any{}
	case "tools/list":
		definitions := make([]map[string]any, 0)
		for _, definition := range tools.Definitions() {
			definitions = append(definitions, map[string]any{"name": definition.Name, "description": definition.Description, "inputSchema": definition.InputSchema})
		}
		response.Result = map[string]any{"tools": definitions}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.Name == "" {
			response.Error = map[string]any{"code": -32602, "message": "invalid tool arguments"}
			break
		}
		arguments := strings.TrimSpace(string(params.Arguments))
		if arguments == "" || arguments == "null" {
			arguments = "{}"
		}
		callCtx := loopdriver.WithExecutionContext(r.Context(), loopdriver.ExecutionContext{SessionID: th.ID, ExecutionID: turnID})
		result, callErr := tools.ExecuteResult(callCtx, providers.ToolCall{ID: turnID + ":" + string(request.ID), Name: params.Name, Arguments: arguments})
		text := result.TextProjection()
		if callErr != nil {
			text = callErr.Error()
		}
		response.Result = map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": callErr != nil || result.IsError}
	default:
		response.Error = map[string]any{"code": -32601, "message": "method not found"}
	}
	writeNamedAgentMCPResponse(w, response)
}
