// Package mcp implements a lightweight Model Context Protocol client.
//
// It supports stdio, streamable HTTP (MCP spec revision 2025-03-26+), and
// legacy SSE transports, plus tool discovery and invocation. The design
// keeps Wuu's transport layer small while preserving the semantics expected
// by coding-agent harnesses. See streamable_http.go for the remote transport
// selection and SSE fallback rules.
package mcp

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

const (
	// PreferredProtocolVersion is the newest MCP revision Wuu implements.
	PreferredProtocolVersion = "2026-07-28"
	// PreferredLegacyProtocolVersion is the newest revision that uses the
	// initialize/initialized handshake.
	PreferredLegacyProtocolVersion = "2025-11-25"
)

const (
	// These limits are enforced while reading transport data, before JSON is
	// decoded into Go values. Tool/result limits are stricter at the model
	// boundary, but cannot protect a transport from an oversized message.
	maxMCPMessageBytes     = 8 * 1024 * 1024
	maxMCPEventBytes       = 4 * 1024 * 1024
	maxMCPBodyExcerptBytes = 4 * 1024
)

var acceptedProtocolVersions = map[string]struct{}{
	"2026-07-28": {},
	"2026-06-30": {},
	"2025-11-25": {},
	"2025-06-18": {},
	"2025-03-26": {},
	"2024-11-05": {},
}

func validateProtocolVersion(version string) error {
	if _, ok := acceptedProtocolVersions[version]; !ok {
		return fmt.Errorf("unsupported MCP protocol version %q; supported versions are 2026-07-28, 2026-06-30, 2025-11-25, 2025-06-18, 2025-03-26, and 2024-11-05", version)
	}
	return nil
}

type DiscoverResult struct {
	ResultType        string          `json:"resultType,omitempty"`
	SupportedVersions []string        `json:"supportedVersions"`
	Capabilities      json.RawMessage `json:"capabilities"`
	Instructions      string          `json:"instructions,omitempty"`
}

// Request is a JSON-RPC request.
type Request struct {
	JSONRPC  string          `json:"jsonrpc"`
	ID       int64           `json:"-"`
	Method   string          `json:"method,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    *RPCError       `json:"error,omitempty"`
	StringID string          `json:"-"`
	stringID bool
	hasID    bool
}

// Response is a JSON-RPC response.
type Response struct {
	JSONRPC  string          `json:"jsonrpc"`
	ID       int64           `json:"-"`
	Method   string          `json:"method,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    *RPCError       `json:"error,omitempty"`
	StringID string          `json:"-"`
	stringID bool
	hasID    bool
}

// MarshalJSON keeps numeric IDs convenient for the client while accepting
// string IDs from servers. String IDs are required to be echoed byte-for-byte
// when replying to a server-initiated request.
func (r Request) MarshalJSON() ([]byte, error) {
	return marshalRPCMessage(r.JSONRPC, r.ID, r.StringID, r.stringID, r.hasID || r.ID != 0, r.Method, r.Params, r.Result, r.Error)
}

func (r Response) MarshalJSON() ([]byte, error) {
	return marshalRPCMessage(r.JSONRPC, r.ID, r.StringID, r.stringID, r.hasID || r.ID != 0, r.Method, r.Params, r.Result, r.Error)
}

func (r *Request) UnmarshalJSON(data []byte) error {
	message, err := unmarshalRPCMessage(data)
	if err != nil {
		return err
	}
	*r = Request{JSONRPC: message.JSONRPC, ID: message.ID, StringID: message.StringID, stringID: message.stringID, hasID: message.hasID, Method: message.Method, Params: message.Params, Result: message.Result, Error: message.Error}
	return nil
}

func (r *Response) UnmarshalJSON(data []byte) error {
	message, err := unmarshalRPCMessage(data)
	if err != nil {
		return err
	}
	*r = Response{JSONRPC: message.JSONRPC, ID: message.ID, StringID: message.StringID, stringID: message.stringID, hasID: message.hasID, Method: message.Method, Params: message.Params, Result: message.Result, Error: message.Error}
	return nil
}

type rpcMessage struct {
	JSONRPC  string
	ID       int64
	StringID string
	stringID bool
	hasID    bool
	Method   string
	Params   json.RawMessage
	Result   json.RawMessage
	Error    *RPCError
}

func marshalRPCMessage(jsonrpc string, id int64, stringID string, stringIDPresent, hasID bool, method string, params, result json.RawMessage, rpcErr *RPCError) ([]byte, error) {
	message := map[string]any{"jsonrpc": jsonrpc}
	if hasID {
		if stringIDPresent {
			message["id"] = stringID
		} else {
			message["id"] = id
		}
	}
	if method != "" {
		message["method"] = method
	}
	if len(params) > 0 {
		message["params"] = params
	}
	if len(result) > 0 {
		message["result"] = result
	}
	if rpcErr != nil {
		message["error"] = rpcErr
	}
	return json.Marshal(message)
}

func unmarshalRPCMessage(data []byte) (rpcMessage, error) {
	var raw struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method,omitempty"`
		Params  json.RawMessage `json:"params,omitempty"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   *RPCError       `json:"error,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return rpcMessage{}, err
	}
	message := rpcMessage{JSONRPC: raw.JSONRPC, Method: raw.Method, Params: raw.Params, Result: raw.Result, Error: raw.Error}
	if len(raw.ID) == 0 || string(raw.ID) == "null" {
		return message, nil
	}
	message.hasID = true
	if raw.ID[0] == '"' {
		message.stringID = true
		if err := json.Unmarshal(raw.ID, &message.StringID); err != nil {
			return rpcMessage{}, fmt.Errorf("decode JSON-RPC string id: %w", err)
		}
		return message, nil
	}
	if err := json.Unmarshal(raw.ID, &message.ID); err != nil {
		return rpcMessage{}, fmt.Errorf("decode JSON-RPC numeric id: %w", err)
	}
	return message, nil
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
	Name         string           `json:"name"`
	Title        string           `json:"title,omitempty"`
	Description  string           `json:"description"`
	InputSchema  json.RawMessage  `json:"inputSchema"`
	OutputSchema json.RawMessage  `json:"outputSchema,omitempty"`
	Annotations  *ToolAnnotations `json:"annotations,omitempty"`
	Meta         json.RawMessage  `json:"_meta,omitempty"`
}

// ToolAnnotations carries MCP server-provided behavioral hints for a tool.
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// ListToolsResult is returned by tools/list.
type ListToolsResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// ListToolsParams selects the next page of an MCP tool catalog.
type ListToolsParams struct {
	Cursor string `json:"cursor,omitempty"`
}

// CallToolParams are sent in tools/call.
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult is returned by tools/call.
type CallToolResult struct {
	Content           []ToolContent   `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
	Meta              json.RawMessage `json:"_meta,omitempty"`
}

// ToolContent is a single content item in a tool result.
type ToolContent struct {
	Type        string          `json:"type"`
	Text        string          `json:"text,omitempty"`
	Data        string          `json:"data,omitempty"`
	MIMEType    string          `json:"mimeType,omitempty"`
	URI         string          `json:"uri,omitempty"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Resource    json.RawMessage `json:"resource,omitempty"`
	Meta        json.RawMessage `json:"_meta,omitempty"`
}

var requestIDCounter int64

func nextRequestID() int64 {
	return atomic.AddInt64(&requestIDCounter, 1)
}

// inFlight tracks pending requests.
type inFlight struct {
	mu            sync.Mutex
	pending       map[int64]chan Response
	closed        bool
	closedMessage string
}

func newInFlight() *inFlight {
	return &inFlight{pending: make(map[int64]chan Response)}
}

func (f *inFlight) register(id int64) <-chan Response {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		ch := make(chan Response, 1)
		message := f.closedMessage
		if message == "" {
			message = "client closed"
		}
		ch <- Response{Error: &RPCError{Code: -32000, Message: message}}
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

// drop removes a pending request without delivering a response. Used when the
// caller's context is cancelled so the entry does not leak until closeAll.
func (f *inFlight) drop(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.pending, id)
}

func (f *inFlight) closeAll() {
	f.failAllMessage("client closed")
}

func (f *inFlight) failAll(err error) {
	message := "client closed"
	if err != nil {
		message = err.Error()
	}
	f.failAllMessage(message)
}

func (f *inFlight) failAllMessage(message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		f.closedMessage = message
	}
	if f.closedMessage == "" {
		f.closedMessage = "client closed"
	}
	for id, ch := range f.pending {
		ch <- Response{Error: &RPCError{Code: -32000, Message: f.closedMessage}}
		delete(f.pending, id)
	}
}
