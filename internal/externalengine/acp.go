package externalengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// ACP v1's prompt response owns completion. Later protocol versions have
// different turn boundaries; accepting them without a separate driver is unsafe.
type acpInitialize struct {
	Version      int          `json:"protocolVersion"`
	AuthMethods  []AuthMethod `json:"authMethods"`
	Capabilities struct {
		Load   bool `json:"loadSession"`
		Prompt struct {
			Image bool `json:"image"`
		} `json:"promptCapabilities"`
		MCP struct {
			HTTP bool `json:"http"`
		} `json:"mcpCapabilities"`
	} `json:"agentCapabilities"`
}

type acpSession struct {
	ID     string `json:"sessionId"`
	Models *struct {
		Current   string `json:"currentModelId"`
		Available []struct {
			ID string `json:"modelId"`
		} `json:"availableModels"`
	} `json:"models"`
}

func (s *Session) runACP(ctx context.Context, message providers.ChatMessage, t *turn) error {
	p, err := startChild(s.engine.binary, s.engine.entry.Args, s.binding.RootDir, nil)
	if err != nil {
		return err
	}
	defer p.close()
	ref := s.binding.ExternalRef
	prompting := false
	r := &rpc{child: p}
	r.handle = func(ctx context.Context, method string, params json.RawMessage, request bool) (any, error) {
		if request {
			if method == "session/request_permission" {
				return s.acpPermission(ctx, ref, params)
			}
			// Never leave an unknown blocking request unanswered. No filesystem
			// or terminal capabilities are advertised by this client.
			return nil, &rpcError{Code: -32601, Message: "unsupported engine request: " + method}
		}
		if method == "session/update" && prompting {
			return nil, t.acpUpdate(ref, params)
		}
		return nil, nil
	}
	setupCtx, cancelSetup := context.WithTimeout(ctx, 30*time.Second)
	defer cancelSetup()
	init, err := initializeACP(setupCtx, r)
	if err != nil {
		return err
	}
	if len(message.Images) > 0 && !init.Capabilities.Prompt.Image {
		return errors.New("this engine does not advertise image input support")
	}
	mcp := make([]map[string]any, 0, len(s.binding.MCPServers))
	if len(s.binding.MCPServers) > 0 && !init.Capabilities.MCP.HTTP {
		return errors.New("this engine does not support the host's HTTP MCP tools")
	}
	for _, server := range s.binding.MCPServers {
		mcp = append(mcp, map[string]any{"type": "http", "name": server.Name, "url": server.URL, "headers": []any{}})
	}
	params := map[string]any{"cwd": s.binding.RootDir, "mcpServers": mcp}
	method := "session/new"
	if ref != "" {
		if !init.Capabilities.Load {
			return errors.New("this engine cannot load the saved session; history was not reset")
		}
		method = "session/load"
		params["sessionId"] = ref
	}
	var session acpSession
	if err := r.call(setupCtx, method, params, &session); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if method == "session/new" {
		ref = session.ID
		if err := s.persist(ref); err != nil {
			return err
		}
	}
	if model := strings.TrimSpace(s.binding.Model); model != "" {
		if session.Models == nil {
			return errors.New("this engine does not advertise model selection; clear the model to use its configured default")
		}
		found := false
		for _, available := range session.Models.Available {
			if available.ID == model {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("model %q is not advertised by this engine", model)
		}
		if session.Models.Current != model {
			if err := r.call(setupCtx, "session/set_model", map[string]any{"sessionId": ref, "modelId": model}, nil); err != nil {
				return err
			}
		}
	}
	cancelSetup()
	blocks := make([]map[string]string, 0, 2+len(message.Images))
	if s.binding.Instructions != "" {
		blocks = append(blocks, map[string]string{"type": "text", "text": "Session instructions supplied by the host:\n" + s.binding.Instructions})
	}
	if message.Content != "" {
		blocks = append(blocks, map[string]string{"type": "text", "text": message.Content})
	}
	for _, image := range message.Images {
		blocks = append(blocks, map[string]string{"type": "image", "mimeType": image.MediaType, "data": image.Data})
	}
	prompting = true
	var response struct {
		StopReason string `json:"stopReason"`
	}
	err = r.call(ctx, "session/prompt", map[string]any{"sessionId": ref, "prompt": blocks}, &response)
	if ctx.Err() != nil {
		cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = r.notify(cancelCtx, "session/cancel", map[string]string{"sessionId": ref})
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	t.stopReason = response.StopReason
	switch response.StopReason {
	case "end_turn":
		return nil
	case "cancelled":
		return context.Canceled
	case "max_tokens", "max_turn_requests", "refusal":
		return fmt.Errorf("engine stopped: %s", response.StopReason)
	default:
		return fmt.Errorf("engine returned an unknown stop reason %q", response.StopReason)
	}
}

type acpPermission struct {
	SessionID string `json:"sessionId"`
	Tool      struct {
		ID    string          `json:"toolCallId"`
		Title string          `json:"title"`
		Kind  string          `json:"kind"`
		Input json.RawMessage `json:"rawInput"`
	} `json:"toolCall"`
	Options []struct {
		ID   string `json:"optionId"`
		Kind string `json:"kind"`
	} `json:"options"`
}

func (s *Session) acpPermission(ctx context.Context, ref string, raw json.RawMessage) (any, error) {
	cancelled := map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}
	var req acpPermission
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if ref == "" || req.SessionID != ref {
		return cancelled, nil
	}
	decision := agentengine.ApprovalDecline
	if s.binding.PermissionMode == "unconfined" {
		decision = agentengine.ApprovalAccept
	} else if s.binding.RequestApproval != nil {
		var err error
		kind := agentengine.ApprovalPermissions
		if req.Tool.Kind == "execute" {
			kind = agentengine.ApprovalCommandExecution
		}
		if req.Tool.Kind == "edit" || req.Tool.Kind == "delete" {
			kind = agentengine.ApprovalFileChange
		}
		decision, err = s.binding.RequestApproval(ctx, agentengine.ApprovalRequest{Kind: kind, EngineID: agentengine.EngineID(s.engine.entry.ID), ThreadID: s.binding.ThreadID, ItemID: req.Tool.ID, Reason: req.Tool.Title, CWD: s.binding.RootDir, Permissions: req.Tool.Input})
		if err != nil {
			return cancelled, nil
		}
	}
	if ctx.Err() != nil || decision == agentengine.ApprovalCancel {
		return cancelled, nil
	}
	// A session-scoped host approval must never become the agent's persisted
	// "allow_always" rule. Select only an exact one-shot grant, or cancel.
	wanted := "reject_once"
	if decision == agentengine.ApprovalAccept || decision == agentengine.ApprovalAcceptForSession {
		wanted = "allow_once"
	}
	for _, option := range req.Options {
		if option.Kind == wanted && option.ID != "" {
			return map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": option.ID}}, nil
		}
	}
	return cancelled, nil
}

func (t *turn) acpUpdate(ref string, raw json.RawMessage) error {
	var notification struct {
		SessionID string          `json:"sessionId"`
		Update    json.RawMessage `json:"update"`
	}
	if err := json.Unmarshal(raw, &notification); err != nil {
		return err
	}
	if notification.SessionID != ref {
		return nil
	}
	var update struct {
		Type    string               `json:"sessionUpdate"`
		ID      string               `json:"toolCallId"`
		Title   string               `json:"title"`
		Status  string               `json:"status"`
		Input   json.RawMessage      `json:"rawInput"`
		Output  json.RawMessage      `json:"rawOutput"`
		Content json.RawMessage      `json:"content"`
		Entries []providers.TodoItem `json:"entries"`
	}
	if err := json.Unmarshal(notification.Update, &update); err != nil {
		return err
	}
	switch update.Type {
	case "agent_message_chunk", "agent_thought_chunk":
		var content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(update.Content, &content); err != nil {
			return err
		}
		if content.Type == "text" {
			t.content(content.Text, update.Type == "agent_thought_chunk")
		}
	case "tool_call", "tool_call_update":
		output := string(update.Output)
		if output == "" || output == "null" {
			var blocks []struct {
				Type    string `json:"type"`
				Content struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(update.Content, &blocks) == nil {
				var text []string
				for _, block := range blocks {
					if block.Content.Text != "" {
						text = append(text, block.Content.Text)
					}
				}
				output = strings.Join(text, "\n")
			}
		}
		t.tool(update.ID, update.Title, string(update.Input), update.Status, output)
	case "plan":
		t.emit(providers.StreamEvent{Type: providers.EventTodoUpdate, TodoUpdate: &providers.TodoUpdate{Todos: update.Entries}})
	}
	return nil
}
