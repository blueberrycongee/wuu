package externalengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/providers"
)

var grokPromptStall = 30 * time.Second

// ACP v1's prompt response owns completion for standard agents. Grok also
// emits a session prompt-complete extension that can arrive first when the
// prompt RPC hangs after the turn actually finished. Later protocol versions
// have different turn boundaries; accepting them without a separate driver is
// unsafe.
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
			ID   string `json:"modelId"`
			Name string `json:"name"`
		} `json:"availableModels"`
	} `json:"models"`
	ConfigOptions []acpConfigOption `json:"configOptions"`
}

// DiscoverModels probes a short-lived ACP session for the agent's advertised
// catalog. It never persists the probe session or sends a prompt.
func (e *Engine) DiscoverModels(ctx context.Context) ([]DiscoveredModel, error) {
	if e == nil || e.entry.Protocol != "acp" {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := startChild(e.binary, e.entry.Args, e.root, nil)
	if err != nil {
		return nil, err
	}
	defer p.close()
	r := &rpc{child: p, handle: func(_ context.Context, method string, _ json.RawMessage, request bool) (any, error) {
		if request {
			return nil, &rpcError{Code: -32601, Message: "unsupported engine request: " + method}
		}
		return nil, nil
	}}
	if _, err := initializeACP(ctx, r); err != nil {
		return nil, err
	}
	cwd := e.root
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	var session acpSession
	if err := r.call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, &session); err != nil {
		return nil, fmt.Errorf("session/new: %w", err)
	}
	return modelsFromACPSession(session), nil
}

func (s *Session) runACP(ctx context.Context, message providers.ChatMessage, t *turn) error {
	p, err := startChild(s.engine.binary, s.engine.entry.Args, s.binding.RootDir, nil)
	if err != nil {
		return err
	}
	defer p.close()
	ref := s.binding.ExternalRef
	prompting := false
	promptID := ""
	profile := acpProfileFor(s.engine.entry.ID)
	var stallTimer *time.Timer
	disarmStall := func() {
		if stallTimer != nil {
			stallTimer.Stop()
			stallTimer = nil
		}
	}
	r := &rpc{child: p}
	r.handle = func(ctx context.Context, method string, params json.RawMessage, request bool) (any, error) {
		if request {
			disarmStall()
			if method == "session/request_permission" {
				return s.acpPermission(ctx, ref, params)
			}
			// Never leave an unknown blocking request unanswered. No filesystem
			// or terminal capabilities are advertised by this client.
			return nil, &rpcError{Code: -32601, Message: "unsupported engine request: " + method}
		}
		if prompting && profile.promptComplete {
			if err := decodeACPPromptComplete(ref, promptID, method, params); err != nil {
				return nil, err
			}
		}
		if method == "session/update" {
			if prompting && !isACPSessionBoilerplate(params) {
				disarmStall()
			}
			if !prompting {
				return nil, decodeACPUpdate(ref, params)
			}
			return nil, t.acpUpdate(ref, params)
		}
		if prompting && isACPPromptCompleteMethod(method) {
			disarmStall()
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
	if err := s.applyACPSelection(setupCtx, r, session, ref); err != nil {
		return err
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
	promptParams := map[string]any{"sessionId": ref, "prompt": blocks}
	if profile.promptComplete {
		promptID = "wuu-p1"
		promptParams["_meta"] = map[string]string{"promptId": promptID, "requestId": promptID}
	}
	var response struct {
		StopReason string `json:"stopReason"`
	}
	promptCtx := ctx
	if profile.promptStall > 0 {
		var cancelStall context.CancelFunc
		promptCtx, cancelStall = context.WithCancel(ctx)
		stallTimer = time.AfterFunc(profile.promptStall, cancelStall)
		defer disarmStall()
	}
	err = r.call(promptCtx, "session/prompt", promptParams, &response)
	if ctx.Err() != nil {
		cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = r.notify(cancelCtx, "session/cancel", map[string]string{"sessionId": ref})
		return ctx.Err()
	}
	if err != nil {
		if profile.promptStall > 0 && errors.Is(err, context.Canceled) && ctx.Err() == nil {
			cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = r.notify(cancelCtx, "session/cancel", map[string]string{"sessionId": ref})
			return fmt.Errorf("%s did not acknowledge the prompt within %s; %s", s.engine.entry.Name, profile.promptStall, profile.stallHint)
		}
		return err
	}
	return finishACPTurn(t, response.StopReason)
}

type acpProfile struct {
	promptComplete bool
	promptStall    time.Duration
	stallHint      string
}

func acpProfileFor(id string) acpProfile {
	switch strings.TrimSpace(id) {
	case "grok":
		return acpProfile{
			promptComplete: true,
			promptStall:    grokPromptStall,
			stallHint:      "the agent process is likely wedged by a stale shared leader or a hung startup check",
		}
	default:
		return acpProfile{}
	}
}

type acpTurnSettled struct {
	StopReason string
}

func (e *acpTurnSettled) Error() string {
	if e == nil || e.StopReason == "" {
		return "engine turn settled"
	}
	return "engine turn settled: " + e.StopReason
}

func applyACPTurnSettled(result any, settled *acpTurnSettled) error {
	if settled == nil {
		return errors.New("engine turn settled without a stop reason")
	}
	if result == nil {
		return nil
	}
	data, err := json.Marshal(map[string]string{"stopReason": settled.StopReason})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("decode session/prompt result: %w", err)
	}
	return nil
}

func decodeACPPromptComplete(ref, promptID, method string, raw json.RawMessage) error {
	if !isACPPromptCompleteMethod(method) {
		return nil
	}
	var notification struct {
		SessionID  string `json:"sessionId"`
		PromptID   string `json:"promptId"`
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(raw, &notification); err != nil {
		return err
	}
	if notification.SessionID != ref {
		return nil
	}
	if promptID != "" && notification.PromptID != "" && notification.PromptID != promptID {
		return nil
	}
	stop := strings.TrimSpace(notification.StopReason)
	if stop == "" {
		stop = "end_turn"
	}
	return &acpTurnSettled{StopReason: stop}
}

func isACPPromptCompleteMethod(method string) bool {
	switch method {
	case "x.ai/session/prompt_complete", "_x.ai/session/prompt_complete":
		return true
	default:
		return false
	}
}

func isACPSessionBoilerplate(raw json.RawMessage) bool {
	var notification struct {
		Update struct {
			Type string `json:"sessionUpdate"`
		} `json:"update"`
	}
	if json.Unmarshal(raw, &notification) != nil {
		return false
	}
	switch notification.Update.Type {
	case "available_commands_update", "config_option_update", "current_mode_update":
		return true
	default:
		return false
	}
}

func finishACPTurn(t *turn, stopReason string) error {
	t.stopReason = stopReason
	switch stopReason {
	case "end_turn":
		return nil
	case "cancelled":
		return context.Canceled
	case "max_tokens", "max_turn_requests", "refusal":
		return fmt.Errorf("engine stopped: %s", stopReason)
	default:
		return fmt.Errorf("engine returned an unknown stop reason %q", stopReason)
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

func (s *Session) applyACPSelection(ctx context.Context, r *rpc, session acpSession, ref string) error {
	if model := strings.TrimSpace(s.binding.Model); model != "" {
		if !session.hasAdvertisedModel(model) {
			if session.Models == nil && session.modelConfigOption() == nil {
				return errors.New("this engine does not advertise model selection; clear the model to use its configured default")
			}
			return fmt.Errorf("model %q is not advertised by this engine", model)
		}
		// Grok Build's picker is first-class `models` + session/set_model.
		// Setting the same id through a category=model config option can be
		// ignored on older CLIs, so prefer set_model when that list exists.
		switch {
		case session.firstClassHasModel(model):
			if session.firstClassCurrent() != model {
				if err := r.call(ctx, "session/set_model", map[string]any{"sessionId": ref, "modelId": model}, nil); err != nil {
					return err
				}
			}
		default:
			option := session.modelConfigOption()
			if option != nil && strings.TrimSpace(option.Current) != model {
				if err := r.call(ctx, "session/set_config_option", map[string]any{"sessionId": ref, "configId": option.ID, "value": model}, nil); err != nil {
					return err
				}
			}
		}
	}
	effort := strings.TrimSpace(s.binding.Effort)
	if effort == "" {
		return nil
	}
	option := session.thoughtLevelOption()
	if option == nil || !option.hasChoice(effort) {
		return fmt.Errorf("%s does not expose reasoning effort through this integration; clear the effort selection", s.engine.entry.Name)
	}
	if strings.TrimSpace(option.Current) == effort {
		return nil
	}
	configID := strings.TrimSpace(option.ID)
	if configID == "" {
		configID = "reasoning_effort"
	}
	return r.call(ctx, "session/set_config_option", map[string]any{"sessionId": ref, "configId": configID, "value": effort}, nil)
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

func decodeACPUpdate(ref string, raw json.RawMessage) error {
	return parseACPUpdate(ref, raw, nil)
}

func (t *turn) acpUpdate(ref string, raw json.RawMessage) error {
	return parseACPUpdate(ref, raw, t)
}

func parseACPUpdate(ref string, raw json.RawMessage, t *turn) error {
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
		if t != nil && content.Type == "text" {
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
		if t != nil {
			t.tool(update.ID, update.Title, string(update.Input), update.Status, output)
		}
	case "plan":
		if t != nil {
			t.emit(providers.StreamEvent{Type: providers.EventTodoUpdate, TodoUpdate: &providers.TodoUpdate{Todos: update.Entries}})
		}
	}
	return nil
}
