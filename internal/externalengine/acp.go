package externalengine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/providers"
)

var grokPromptStall = 30 * time.Second

// Handshake budgets. initialize is local process startup, so 30s of silence is
// a wedged binary. Opening the session is different work: session/load replays
// the agent's own history from disk, and a long conversation legitimately takes
// longer. A too-tight budget here reads as a broken engine.
var (
	acpInitializeTimeout = 30 * time.Second
	acpSessionTimeout    = 120 * time.Second
)

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
		return nil, acpEngineError(e.entry, p, err)
	}
	cwd := e.root
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	var session acpSession
	if err := r.call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, &session); err != nil {
		// Settings surfaces this as the engine's model error, which is where a
		// user discovers that the agent needs configuration of its own.
		return nil, acpEngineError(e.entry, p, fmt.Errorf("session/new: %w", err))
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
	setupCtx, cancelSetup := context.WithTimeout(ctx, acpSessionTimeout)
	defer cancelSetup()
	initCtx, cancelInit := context.WithTimeout(ctx, acpInitializeTimeout)
	init, err := initializeACP(initCtx, r)
	cancelInit()
	if err != nil {
		return acpEngineError(s.engine.entry, p, err)
	}
	mcp := make([]map[string]any, 0, len(s.binding.MCPServers))
	if len(s.binding.MCPServers) > 0 && !init.Capabilities.MCP.HTTP {
		return errors.New("this engine does not support the host's HTTP MCP tools")
	}
	for _, server := range s.binding.MCPServers {
		mcp = append(mcp, map[string]any{"type": "http", "name": server.Name, "url": server.URL, "headers": []any{}})
	}
	params := map[string]any{"cwd": s.binding.RootDir, "mcpServers": mcp}
	var session acpSession
	notice := ""
	switch {
	case ref == "":
		if err := r.call(setupCtx, "session/new", params, &session); err != nil {
			return acpEngineError(s.engine.entry, p, fmt.Errorf("session/new: %w", err))
		}
	case !init.Capabilities.Load:
		// Refusing the turn would strand the thread: the saved reference can
		// never be cleared from the UI, so every later turn would fail the same
		// way. Start fresh and say so instead.
		notice = engineResumeNotice(s.engine.entry.Name, "the agent does not support loading a saved session")
		if err := r.call(setupCtx, "session/new", params, &session); err != nil {
			return acpEngineError(s.engine.entry, p, fmt.Errorf("session/new: %w", err))
		}
	default:
		loadParams := map[string]any{"cwd": s.binding.RootDir, "mcpServers": mcp, "sessionId": ref}
		if err := r.call(setupCtx, "session/load", loadParams, &session); err != nil {
			notice = engineResumeNotice(s.engine.entry.Name, acpRecoverableReason(err))
			if retryErr := r.call(setupCtx, "session/new", params, &session); retryErr != nil {
				return acpEngineError(s.engine.entry, p, fmt.Errorf("session/load: %w; session/new: %w", err, retryErr))
			}
		}
	}
	if ref == "" || notice != "" {
		// A replacement session — the first one, or a load fallback — must
		// become the thread's reference, or every later turn repeats the
		// fallback.
		ref = session.ID
		if err := s.persist(ref); err != nil {
			return err
		}
	}
	if notice != "" {
		t.content(notice, false)
	}
	if err := s.applyACPSelection(setupCtx, r, session, ref); err != nil {
		return err
	}
	cancelSetup()
	text, err := acpPromptMedia(s.binding.ThreadID, message)
	if err != nil {
		return err
	}
	blocks := make([]map[string]string, 0, 2)
	if s.binding.Instructions != "" {
		blocks = append(blocks, map[string]string{"type": "text", "text": "Session instructions supplied by the host:\n" + s.binding.Instructions})
	}
	if text != "" {
		blocks = append(blocks, map[string]string{"type": "text", "text": text})
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
		return acpEngineError(s.engine.entry, p, err)
	}
	return finishACPTurn(t, response.StopReason)
}

const (
	acpImageOnlyPrompt = "See the attached image(s)."
	acpImagePathHeader = "Attached images (local files — open them to view):"
)

// acpPromptMedia keeps user-supplied images in the turn. ACP session/prompt
// here is text-only: Grok advertises promptCapabilities.image=false, and the
// working host path is to write the bytes to local files and put those
// absolute paths in the prompt so the agent's own read_file can see them.
func acpPromptMedia(threadID string, message providers.ChatMessage) (string, error) {
	text := message.Content
	if len(message.Images) == 0 {
		return text, nil
	}
	paths, err := writeACPImageFiles(threadID, message.Images)
	if err != nil {
		return "", err
	}
	return acpImagePathPrompt(text, paths), nil
}

func acpImagePathPrompt(text string, paths []string) string {
	if len(paths) == 0 {
		return text
	}
	body := strings.TrimSpace(text)
	if body == "" {
		body = acpImageOnlyPrompt
	}
	lines := make([]string, 0, len(paths))
	for _, path := range paths {
		lines = append(lines, "- "+path)
	}
	return body + "\n\n" + acpImagePathHeader + "\n" + strings.Join(lines, "\n")
}

func writeACPImageFiles(threadID string, images []providers.InputImage) ([]string, error) {
	dir := acpImageDir(threadID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("write image attachments: %w", err)
	}
	paths := make([]string, 0, len(images))
	for i, image := range images {
		raw, err := base64.StdEncoding.DecodeString(image.Data)
		if err != nil {
			return nil, fmt.Errorf("image %d: invalid base64: %w", i+1, err)
		}
		if len(raw) == 0 {
			return nil, fmt.Errorf("image %d is empty", i+1)
		}
		path := filepath.Join(dir, fmt.Sprintf("%d-%d%s", time.Now().UnixNano(), i+1, acpImageExt(image.MediaType)))
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return nil, fmt.Errorf("write image attachments: %w", err)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		paths = append(paths, abs)
	}
	return paths, nil
}

func acpImageDir(threadID string) string {
	id := strings.TrimSpace(threadID)
	id = strings.ReplaceAll(id, string(filepath.Separator), "-")
	id = strings.ReplaceAll(id, "..", "")
	if id == "" {
		id = "thread"
	}
	return filepath.Join(os.TempDir(), "wuu-acp-images", id)
}

func acpImageExt(mediaType string) string {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	mediaType, _, _ = strings.Cut(mediaType, ";")
	switch mediaType {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
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

// acpEngineError appends the engine's stderr tail to a failure. The protocol
// stream carries only a generic error, while the cause the user can act on — an
// unconfigured provider, a refused login — is written to stderr.
func acpEngineError(entry enginecatalog.Entry, p *child, err error) error {
	if err == nil {
		return nil
	}
	tail := p.stderrSummary()
	if tail == "" {
		return err
	}
	return fmt.Errorf("%w\n%s stderr:\n%s", err, entry.Name, tail)
}

// engineResumeNotice explains a fresh-session fallback in the transcript. The
// agent's own context is gone, so the fallback is never silent.
func engineResumeNotice(name, reason string) string {
	return fmt.Sprintf("%s could not resume the saved agent session (%s). This turn started a new agent session, so the agent no longer has this conversation's earlier context.\n\n", name, reason)
}

// acpRecoverableReason turns a failed session/load into a short reason for the
// notice: the agent's own error message is the useful part, and it is bounded
// because it is quoted into the transcript.
func acpRecoverableReason(err error) string {
	reason := strings.TrimSpace(err.Error())
	if reason == "" {
		return "the agent did not load it"
	}
	return truncateUTF8(reason, 200)
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
	if effort != "" {
		option := session.thoughtLevelOption()
		if option == nil || !option.hasChoice(effort) {
			return fmt.Errorf("%s does not expose reasoning effort through this integration; clear the effort selection", s.engine.entry.Name)
		}
		if strings.TrimSpace(option.Current) != effort {
			configID := strings.TrimSpace(option.ID)
			if configID == "" {
				configID = "reasoning_effort"
			}
			if err := r.call(ctx, "session/set_config_option", map[string]any{"sessionId": ref, "configId": configID, "value": effort}, nil); err != nil {
				return err
			}
		}
	}
	if s.binding.PermissionMode == "unconfined" {
		return session.selectUnattendedMode(ctx, r, ref)
	}
	return nil
}

// selectUnattendedMode applies the agent's own no-prompts mode when it offers
// one. Wuu's unconfined mode has to reach the agent's policy: otherwise the
// agent keeps the mode it started in — devin defaults to accept-edits — and the
// edits it decides to make never reach Wuu's approval path. An agent without
// such a mode keeps its default; the prompts it does send still reach Wuu,
// which answers them under the same policy.
func (s acpSession) selectUnattendedMode(ctx context.Context, r *rpc, ref string) error {
	option := s.modeOption()
	if option == nil {
		return nil
	}
	for _, choice := range unattendedModeChoices {
		if !option.hasChoice(choice) {
			continue
		}
		if strings.TrimSpace(option.Current) == choice {
			return nil
		}
		return r.call(ctx, "session/set_config_option", map[string]any{"sessionId": ref, "configId": option.ID, "value": choice}, nil)
	}
	return nil
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
