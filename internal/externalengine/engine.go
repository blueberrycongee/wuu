// Package externalengine hosts external coding agents through their published
// protocols. Agents retain their own authentication, tools, and native history.
package externalengine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type Engine struct {
	entry        enginecatalog.Entry
	binary, root string
}

func New(entry enginecatalog.Entry, binary, root string) *Engine {
	return &Engine{entry: entry, binary: binary, root: root}
}

// acpEnvironment returns nil to inherit the host environment unchanged.
// Devin treats ACP_BACKEND as "the ACP host supplies credentials" and then
// ignores its own `devin auth login`, refusing session/new until the host calls
// authenticate. Devin Desktop exports the variable to everything it launches,
// so a Wuu started from there would pass it on although Wuu never supplies
// host credentials. Remove once Wuu authenticates Devin as an ACP host.
func (e *Engine) acpEnvironment() []string {
	if e.entry.ID != "devin" {
		return nil
	}
	env := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "ACP_BACKEND=") {
			env = append(env, item)
		}
	}
	return env
}

func (e *Engine) Descriptor(context.Context) (agentengine.Descriptor, error) {
	requiresHostModel := false
	return agentengine.Descriptor{ID: agentengine.EngineID(e.entry.ID), Version: "1", RequiresHostModel: &requiresHostModel, Capabilities: []string{"native-tool-loop", "native-session-resume", "interactive-approval", "model-options", "default-permission-unconfined", e.entry.Protocol}}, nil
}

func (e *Engine) Open(ctx context.Context, req agentengine.OpenRequest) (agentengine.Session, error) {
	return e.SessionForThread(ctx, agentengine.ThreadBinding{ThreadID: req.ThreadID, RootDir: req.RootDir})
}

func (e *Engine) Resume(ctx context.Context, req agentengine.ResumeRequest) (agentengine.Session, error) {
	return e.SessionForThread(ctx, agentengine.ThreadBinding{ThreadID: req.ThreadID, RootDir: req.RootDir, ExternalRef: req.ExternalSessionRef})
}

func (e *Engine) SessionForThread(ctx context.Context, binding agentengine.ThreadBinding) (agentengine.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if binding.RootDir == "" {
		binding.RootDir = e.root
	}
	if binding.Speed != "" && e.entry.Protocol != "acp" {
		return nil, fmt.Errorf("%s does not expose speed selection through this integration", e.entry.Name)
	}
	binding.MCPServers = append([]agentengine.MCPServer(nil), binding.MCPServers...)
	if e.entry.Protocol != "acp" && binding.Effort != "" {
		return nil, fmt.Errorf("%s does not expose reasoning effort through this integration; clear the effort selection", e.entry.Name)
	}
	return &Session{engine: e, binding: binding}, nil
}

type Session struct {
	engine  *Engine
	binding agentengine.ThreadBinding
	mu      sync.Mutex
	cancel  context.CancelFunc
	closed  bool
}

func (s *Session) RunTurn(ctx context.Context, input agentengine.TurnInput, sink agentengine.EventSink) (agentengine.TurnResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.closed || s.cancel != nil {
		s.mu.Unlock()
		cancel()
		return agentengine.TurnResult{}, errors.New("engine session is closed or already running")
	}
	s.cancel = cancel
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); s.cancel = nil; s.mu.Unlock() }()
	t := newTurn(s.engine.entry.ID, sink)
	message := latestUserMessage(input.History)
	var err error
	switch {
	case ctx.Err() != nil:
		err = ctx.Err()
	case strings.TrimSpace(message.Content) == "" && len(message.Images) == 0:
		err = errors.New("engine turn requires a user message")
	case s.binding.PermissionMode == "read_only" && s.engine.entry.Protocol != "acp":
		err = readOnlyUnsupportedError(s.engine.entry.Name)
	case s.engine.entry.Protocol == "acp":
		err = s.runACP(ctx, message, t)
	case s.engine.entry.Protocol == "opencode":
		err = s.runOpenCode(ctx, message, t)
	default:
		err = fmt.Errorf("unsupported engine protocol %q", s.engine.entry.Protocol)
	}
	result := t.result()
	var lastImages []providers.InputImage
	for i := len(result.NewMessages) - 1; i >= 0; i-- {
		if result.NewMessages[i].Role == "assistant" {
			lastImages = result.NewMessages[i].Images
			break
		}
	}
	if len(lastImages) > 0 {
		// Images collected during the turn surface live through the same
		// item.Images path the persisted history projection uses.
		message := providers.ChatMessage{Role: "assistant", Content: t.messageText.String(), Images: lastImages}
		t.emit(providers.StreamEvent{Type: providers.EventMessage, Message: &message})
	}
	if err != nil {
		result.FinishReason = providers.FinishReasonError
		t.failReconnect(err)
		t.emit(providers.StreamEvent{Type: providers.EventError, Error: err})
	} else {
		t.clearReconnect()
		t.emit(providers.StreamEvent{Type: providers.EventDone, StopReason: t.stopReason, FinishReason: result.FinishReason})
	}
	return agentengine.TurnResult{Result: result}, err
}

func (s *Session) Interrupt(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel == nil {
		return agentengine.ErrNoActiveTurn
	}
	s.cancel()
	return nil
}

func (s *Session) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

func (s *Session) persist(ref string) error {
	if strings.TrimSpace(ref) == "" {
		return errors.New("engine returned an empty session ID")
	}
	if s.binding.PersistRef != nil {
		if err := s.binding.PersistRef(ref); err != nil {
			return fmt.Errorf("persist engine session: %w", err)
		}
	}
	s.binding.ExternalRef = ref
	return nil
}

func readOnlyUnsupportedError(name string) error {
	return fmt.Errorf("%s does not enforce Wuu's read-only boundary; choose another engine or change the permission mode explicitly", name)
}

func latestUserMessage(history []providers.ChatMessage) providers.ChatMessage {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			return history[i]
		}
	}
	return providers.ChatMessage{}
}

type turn struct {
	engine         string
	sink           agentengine.EventSink
	messageText    strings.Builder
	diagBuf        strings.Builder
	reconnecting   bool
	retryCount     int
	apiError       string
	terminatedErr  string
	blocks         []turnBlock
	calls          map[string]*turnCall
	tools          map[string]*providers.ToolCall
	acpToolKinds   map[string]string
	acpToolDetails map[string]*toolresult.Result
	usage          providers.TokenUsage
	stopReason     string
}

// turnBlock is one persisted assistant message together with the tool calls
// that followed it, keeping the live transcript order when history reloads.
type turnBlock struct {
	text, thinking strings.Builder
	images         []providers.InputImage
	calls          []*turnCall
}

// turnCall is the durable record of one external tool call: the request plus
// its terminal output once the agent reports completion.
type turnCall struct {
	call   providers.ToolCall
	output string
	detail *toolresult.Result
	done   bool
}

func newTurn(engine string, sink agentengine.EventSink) *turn {
	return &turn{engine: engine, sink: sink, calls: make(map[string]*turnCall), tools: make(map[string]*providers.ToolCall)}
}

func (t *turn) emit(event providers.StreamEvent) {
	if t.sink != nil {
		t.sink(event)
	}
}
func (t *turn) content(text string, thinking bool) {
	if text != "" {
		t.clearReconnect()
	}
	kind := providers.EventContentDelta
	block := t.openTextBlock()
	if thinking {
		block.thinking.WriteString(text)
		kind = providers.EventThinkingDelta
	} else {
		block.text.WriteString(text)
		t.messageText.WriteString(text)
	}
	t.emit(providers.StreamEvent{Type: kind, Content: text})
}

// openTextBlock returns the assistant block still collecting output. A block
// closes once a tool call starts after it, so later text lands in a new block
// and the persisted transcript keeps the live item order.
func (t *turn) openTextBlock() *turnBlock {
	if n := len(t.blocks); n == 0 || len(t.blocks[n-1].calls) > 0 {
		t.blocks = append(t.blocks, turnBlock{})
	}
	return &t.blocks[len(t.blocks)-1]
}

func (t *turn) startReconnect(attempt int, reason string, retryIn time.Duration) {
	if attempt <= 0 {
		t.retryCount++
		attempt = t.retryCount
	} else {
		t.retryCount = attempt
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		t.apiError = reason
	}
	t.reconnecting = true
	t.emit(providers.StreamEvent{
		Type: providers.EventLifecycle,
		Lifecycle: &providers.StreamLifecycle{
			Phase:      providers.StreamPhaseReconnecting,
			RetryCount: t.retryCount,
			RetryIn:    retryIn,
			Reason:     t.apiError,
		},
	})
}

func (t *turn) clearReconnect() {
	if !t.reconnecting {
		return
	}
	t.reconnecting = false
	t.apiError = ""
	t.terminatedErr = ""
	t.retryCount = 0
	t.emit(providers.StreamEvent{
		Type:      providers.EventLifecycle,
		Lifecycle: &providers.StreamLifecycle{Phase: providers.StreamPhaseConnected},
	})
}

func (t *turn) failReconnect(err error) {
	if !t.reconnecting {
		return
	}
	t.reconnecting = false
	reason := t.apiError
	if err != nil && !errors.Is(err, context.Canceled) && strings.TrimSpace(err.Error()) != "" {
		reason = err.Error()
	}
	t.emit(providers.StreamEvent{
		Type: providers.EventLifecycle,
		Lifecycle: &providers.StreamLifecycle{
			Phase:      providers.StreamPhaseFailed,
			RetryCount: t.retryCount,
			Reason:     reason,
		},
	})
}

func (t *turn) image(image providers.InputImage) {
	image.Data = strings.TrimSpace(image.Data)
	if image.Data == "" {
		return
	}
	block := t.openTextBlock()
	block.images = append(block.images, image)
	t.emit(providers.StreamEvent{Type: providers.EventImage, Image: &image})
}

func (t *turn) tool(id, name, args, status, output string, detail *toolresult.Result) {
	if id == "" {
		return
	}
	tc, exists := t.calls[id]
	if exists && tc.done {
		return
	}
	t.clearReconnect()
	if !exists {
		tc = &turnCall{call: providers.ToolCall{ID: id, Name: name, Arguments: args}}
		t.calls[id] = tc
		t.tools[id] = &tc.call
		if len(t.blocks) == 0 {
			t.blocks = append(t.blocks, turnBlock{})
		}
		block := &t.blocks[len(t.blocks)-1]
		block.calls = append(block.calls, tc)
		t.messageText.Reset()
	}
	call := &tc.call
	if name != "" {
		call.Name = name
	}
	if call.Name == "" {
		call.Name = "tool"
	}
	if args != "" {
		call.Arguments = args
	}
	copy := *call
	kind := providers.EventToolUseDelta
	if !exists {
		kind = providers.EventToolUseStart
	}
	t.emit(providers.StreamEvent{Type: kind, ToolCall: &copy})
	if status == "completed" || status == "failed" {
		if output == "" {
			output = status
		}
		var cloned *toolresult.Result
		if detail != nil {
			value := detail.Clone()
			cloned = &value
		}
		tc.output = output
		tc.detail = cloned
		tc.done = true
		t.emit(providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &copy, ToolResult: output, ToolResultDetail: cloned})
		delete(t.tools, id)
	}
}

func (t *turn) result() agent.LoopResult {
	r := agent.LoopResult{StopReason: t.stopReason, FinishReason: providers.FinishReasonStop,
		InputTokens: t.usage.InputTokens, OutputTokens: t.usage.OutputTokens, CacheReadTokens: t.usage.CacheReadTokens, CacheCreationTokens: t.usage.CacheCreationTokens}
	for i := range t.blocks {
		block := &t.blocks[i]
		msg := providers.ChatMessage{
			Role:             "assistant",
			Content:          block.text.String(),
			ReasoningContent: block.thinking.String(),
			Images:           append([]providers.InputImage(nil), block.images...),
		}
		for _, tc := range block.calls {
			msg.ToolCalls = append(msg.ToolCalls, tc.call)
		}
		if msg.Content == "" && msg.ReasoningContent == "" && len(msg.Images) == 0 && len(msg.ToolCalls) == 0 {
			continue
		}
		r.Content += msg.Content
		r.NewMessages = append(r.NewMessages, msg)
		for _, tc := range block.calls {
			if !tc.done {
				continue
			}
			result := providers.ChatMessage{Role: "tool", Name: tc.call.Name, ToolCallID: tc.call.ID, Content: tc.output}
			if tc.detail != nil {
				cloned := tc.detail.Clone()
				result.ToolResult = &cloned
			}
			r.NewMessages = append(r.NewMessages, result)
		}
	}
	for i := len(r.NewMessages) - 1; i >= 0; i-- {
		if r.NewMessages[i].Role == "assistant" {
			r.NewMessages[i].FinishReason = r.FinishReason
			r.NewMessages[i].StopReason = r.StopReason
			break
		}
	}
	return r
}
